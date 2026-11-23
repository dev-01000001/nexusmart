# Review độc lập Đợt 2a — `services/payment/**`

Phạm vi đã rà soát toàn bộ (chỉ đọc) so với mốc `44e6585`, đối chiếu [w2-plan.md](file:///home/tai/Document/nexusmart/.agents/briefs/fix/w2-plan.md), [plan.md](file:///home/tai/Document/nexusmart/.agents/briefs/fix/plan.md) §2, [docs/contracts.md](file:///home/tai/Document/nexusmart/docs/contracts.md), [payment.proto](file:///home/tai/Document/nexusmart/proto/nexusmart/payment/v1/payment.proto) và [AGENTS.md](file:///home/tai/Document/nexusmart/AGENTS.md).

---

## P0 — Tiền / vượt quyền / mất dữ liệu

### P0-1. Hạch toán trùng `gateway_clearing` (đơn miễn phí hoặc hoàn tiền đôi 2×) giữa `ResolveReconciliationItem(REFUND)` và `run_reconciliation` / `scan_expired_payments` / `record_late_payment_refund_tx`
- **Vị trí**: [service.rs:2898-2957](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2898-L2957), [service.rs:2551-2582](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2551-L2582), [service.rs:4112-4136](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L4112-L4136)
- **Kịch bản**:
  - `resolve_reconciliation_item(REFUND)` kiểm tra `validate_reconciliation_provider_tx_accountable` và ghi `recon-refund:<tx>` (Nợ `gateway_clearing` / Có `refund_payable`), **không** gán `provider_tx_id` lên bảng `payments`. Ngược lại, 3 đường còn lại không kiểm tra `reconciliation_queue`/`refunds` cho `recon-refund:<tx>` hay `recon-overpay:<tx>`, và `record_late_payment_refund_tx` dùng khoá khác (`late-webhook-refund:<tx>` / `late-webhook:<tx>`).
  - **Nhánh A (xác nhận đơn bằng tiền đã hoàn)**: Payment VietQR `P1` (`200.000₫`, `pending`, mã `NM12345678`); webhook `TX-99` (`200.000₫`) đến thiếu mã đơn → vào `reconciliation_queue` (`order_not_found`); admin xử lý `REFUND` (`recon-refund:TX-99` ghi Nợ `gateway_clearing` 200k / Có `refund_payable` 200k). Sau đó `scan_expired_payments` ([service.rs:2570](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2570)) hoặc `run_reconciliation` ([service.rs:2899](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2899), ví dụ feed SePay có đủ `NM12345678` trong `transaction_content`) thấy `TX-99` khớp `P1` đang `pending` → gọi `confirm_vietqr_payment_tx(P1, "TX-99")` → ghi thêm Nợ `gateway_clearing` 200k / Có `escrow_liability` 200k và xác nhận giao hàng. Một khoản 200k vừa tạo nghĩa vụ hoàn 200k vừa xác nhận đơn 200k (`gateway_clearing` bị Nợ 400k).
  - **Nhánh B (hoàn tiền đôi 400k cho 1 chuyển khoản 200k)**: Tương tự nhưng `P1` đã `expired`/`cancelled`/`superseded` (hoặc nhóm đã có payment `succeeded`). Sau khi admin `REFUND` (`recon-refund:TX-99`, 200k), `run_reconciliation` ([service.rs:2934](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2934)) hoặc `scan_expired_payments` ([service.rs:2559](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2559)) gọi `record_late_payment_refund_tx` → ghi thêm bút toán `late-webhook-refund:TX-99` (200k) và bản ghi `refunds` thứ hai `late-webhook:TX-99` (200k) → `refund_payable` thành 400k.
- **Đề xuất sửa**: Trước khi gọi `confirm_vietqr_payment_tx` hoặc `record_late_payment_refund_tx` trong `run_reconciliation` và `scan_expired_payments`, kiểm tra `provider_tx_id` chưa tồn tại ở `reconciliation_queue` (`assigned`/`refunded`) và `refunds` (`recon-refund:`/`recon-overpay:`/`late-webhook:`), nếu đã có thì bỏ qua coi như đã hạch toán.

---

## P1 — Hỏng luồng chính

### P1-1. Nuốt lỗi `23505` trong transaction PostgreSQL không có `SAVEPOINT` gây `25P02` (transaction aborted) làm sập webhook và treo `reconciliation_runs = 'running'`
- **Vị trí**: [service.rs:745-785](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L745-L785), [service.rs:2872-2897](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2872-L2897), [service.rs:2662-3027](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2662-L3027)
- **Kịch bản**:
  - Sau migration [0004_reconciliation_queue_unique_tx.sql](file:///home/tai/Document/nexusmart/services/payment/migrations/0004_reconciliation_queue_unique_tx.sql#L20-L21), khi webhook lệch tiền chạy song song với `run_reconciliation` (hoặc hai replica chạy `run_reconciliation` đồng thời) trên cùng `provider_tx_id`, cả hai đánh giá `WHERE NOT EXISTS (...)` thấy 0 dòng rồi cùng `INSERT INTO reconciliation_queue`; luồng thứ hai dính lỗi `23505`.
  - Code bắt `is_unique_violation(&err, "provider_tx_id")` rồi bỏ qua nhưng **không bọc `SAVEPOINT`**; PostgreSQL đánh dấu toàn bộ transaction là aborted (`25P02`). Lệnh tiếp theo (`UPDATE webhook_events` ở dòng `773` hoặc `tx.commit()` ở dòng `2976`) thất bại `25P02` → webhook trả HTTP 500 (rollback mất `webhook_events`), còn `run_reconciliation` văng lỗi giữa chừng, bỏ qua mọi giao dịch còn lại và để `reconciliation_runs.status = 'running'` vĩnh viễn (chỉ cập nhật `'failed'` khi lỗi ở `provider_txs_res` dòng `2691`).
- **Đề xuất sửa**: Bọc `INSERT INTO reconciliation_queue` bằng `SAVEPOINT sp_recon_q` + `ROLLBACK TO SAVEPOINT sp_recon_q` khi gặp `23505` (và `RELEASE SAVEPOINT`), đồng thời cập nhật `reconciliation_runs.status = 'failed'` khi bất kỳ bước nào sau dòng `2706` trả `Err`.

### P1-2. Lỗi PayOS `"cổng chưa có dữ liệu"` bị Gateway xóa sạch vì map qua `tonic::Status::unavailable`
- **Vị trí**: [vietqr.rs:237-240](file:///home/tai/Document/nexusmart/services/payment/src/vietqr.rs#L237-L240), [error.rs:96](file:///home/tai/Document/nexusmart/services/payment/src/error.rs#L96) (tương thích chéo với [response.go:100-101](file:///home/tai/Document/nexusmart/services/gateway/internal/httpx/response.go#L100-L101))
- **Kịch bản**: Khi `VIETQR_MODE=payos`, admin gọi `POST /api/v1/admin/payments/reconcile`. `list_provider_transactions` trả `PaymentError::Provider("cổng chưa có dữ liệu: ...")` → `error.rs:96` chuyển thành `tonic::Status::unavailable(...)` → Gateway `MapGRPCError` ghi đè mọi `codes.Unavailable` thành `503 UNAVAILABLE` với chuỗi cứng `"service temporarily unavailable"`, trái `w2-plan.md` §2 yêu cầu trả lỗi rõ `"cổng chưa có dữ liệu"`.
- **Đề xuất sửa**: Tách lỗi không hỗ trợ API đối soát sang variant riêng (map `tonic::Status::unimplemented` hoặc `failed_precondition`, hai mã được Gateway giữ nguyên `st.Message()`) cho nhánh `VietQrMode::PayOs`.

### P1-3. `confirm_vietqr_payment_tx` ghi `paid_at = NOW()` thay vì thời điểm giao dịch thực (`occurred_at`), gây báo `missing_provider` giả qua mốc 00:00 VN hoặc khi `AutoHealAndMatch`/`ASSIGN` qua ngày
- **Vị trí**: [service.rs:4228-4245](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L4228-L4245) (được gọi tại [service.rs:928](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L928), [service.rs:2286](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2286), [service.rs:2570](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2570), [service.rs:2899](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2899))
- **Kịch bản**: Giao dịch thanh toán lúc `2026-11-22 23:59:50 +07:00` (`occurred_at` thuộc ngày VN `2026-11-22`), nhưng webhook đến lúc `2026-11-23 00:00:10 +07:00`, hoặc bị mất webhook rồi ngày `2026-11-23` chạy `run_reconciliation(2026-11-22)` auto-heal, hoặc giao dịch sai nội dung ngày 22 được admin `ASSIGN` vào ngày 23. `confirm_vietqr_payment_tx` đặt `paid_at = NOW()` (thuộc ngày VN `2026-11-23`). Khi đối soát ngày `2026-11-23`, feed cổng ngày 23 không có giao dịch ngày 22 nhưng `local_paid_payments` ([service.rs:2753](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2753)) kéo payment theo `paid_at = 2026-11-23` → gắn `missing_provider` giả.
- **Đề xuất sửa**: Thêm tham số `paid_at: Option<DateTime<Utc>>` (lấy từ `payload.occurred_at` / `ptx.occurred_at` / `webhook_events.received_at`) vào `confirm_vietqr_payment_tx` và ghi `paid_at = COALESCE($3, NOW())`.

### P1-4. `ReleaseEscrow` RPC vẫn tự thu COD và quyết toán đơn `held` chưa qua `order.completed`, có thể giải ngân đơn đang giao hoặc đang tranh chấp
- **Vị trí**: [service.rs:1685-1733](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L1685-L1733)
- **Kịch bản**: Ở đợt 2a, `order` không còn gọi `ReleaseEscrow` và chỉ `order.completed` mới quyết toán `held -> pending_release` sau khi đơn hết tranh chấp. Tuy nhiên `release_escrow` vẫn gọi `collect_cod_order_allocations_tx` (chuyển COD `awaiting_collection -> held` và phát `payment.succeeded`) và `settle_order_to_pending_tx` (`held -> pending_release`) trước khi chuyển sang `seller_available`. Nếu admin gọi `ReleaseEscrow` nhầm trên đơn đang `SHIPPING`/`DELIVERED`/`RETURN_REQUESTED`, hệ thống sẽ ghi nhận thu COD chưa giao và mở khoá tiền ngay cho seller rút khi đơn đang tranh chấp.
- **Đề xuất sửa**: Bỏ gọi `collect_cod_order_allocations_tx` và `settle_order_to_pending_tx` trong `release_escrow`, chỉ cho phép giải ngân sớm các allocation đã ở `pending_release` (trả `409 Conflict` nếu còn `awaiting_collection`/`held`).

### P1-5. `ResolveReconciliationItem(SUSPEND)` không kiểm tra `payment_id` trước khi ghi vào cột FK `resolution_payment_id` → HTTP 500
- **Vị trí**: [service.rs:2192-2212](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2192-L2212), [0004_reconciliation_queue_unique_tx.sql:30](file:///home/tai/Document/nexusmart/services/payment/migrations/0004_reconciliation_queue_unique_tx.sql#L30)
- **Kịch bản**: Cột `resolution_payment_id` có `REFERENCES payments(id)`. Nhánh `SUSPEND` bind trực tiếp `input.payment_id` không kiểm tồn tại (mà UI [doi-soat/page.tsx:289](file:///home/tai/Document/nexusmart/web/src/app/(admin)/admin/doi-soat/page.tsx#L289) luôn gửi kèm `payment_id` kể cả khi chọn `SUSPEND`). Nếu admin gửi một UUID không tồn tại kèm `action = SUSPEND`, PostgreSQL báo lỗi FK `23503` → trả `500 DATABASE_ERROR` thay vì `400`/`404`.
- **Đề xuất sửa**: Với `SUSPEND`, kiểm tra `payments` nếu `input.payment_id.is_some()` (trả `404 NotFound`) hoặc luôn lưu `resolution_payment_id = NULL` khi không dùng `payment_id`.

---

## P2 — Lệch spec / biên

### P2-1. `run_reconciliation` bỏ sót `status = 'refunded'` trong `local_paid_payments`, bỏ lọt `missing_provider` và vô hiệu guard P2-6
- **Vị trí**: [service.rs:2747-2756](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2747-L2756) (đối chiếu [service.rs:2847-2851](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2847-L2851))
- **Kịch bản**: Payment VietQR `P1` (`200k`) thanh toán lúc 10:00 VN rồi bị hủy và hoàn 100% lúc 10:30 VN (`status = 'refunded'`). Truy vấn `local_paid_payments` chỉ lọc `status = 'succeeded'` nên bỏ qua `P1`, khiến một payment đã thu và hoàn trong ngày nhưng thiếu giao dịch thực tế ở phía cổng không bao giờ bị phát hiện `missing_provider` (và làm vô hiệu guard `matches!(current.status.as_str(), "succeeded" | "refunded")` ở dòng `2848`).
- **Đề xuất sửa**: Đổi điều kiện ở [service.rs:2752](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2752) thành `AND status IN ('succeeded', 'refunded')`.

### P2-2. Giao dịch lệch tiền đã được admin giải quyết (`ASSIGN` thừa tiền hoặc `REFUND`) vẫn bị `run_reconciliation` báo `amount_mismatch` lặp lại mãi mãi
- **Vị trí**: [service.rs:2835-2897](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2835-L2897)
- **Kịch bản**: Payment `P1` (`200k`) nhận `TX-OVER` (`230k`) và admin đã `ASSIGN` (xác nhận 200k + tạo hoàn thừa `recon-overpay:TX-OVER` 30k), hoặc nhận `TX-UNDER` (`150k`) và admin đã `REFUND` (`recon-refund:TX-UNDER` 150k). Các lần chạy `run_reconciliation` sau đó vẫn so `local.amount_vnd != ptx.amount_vnd` → luôn tăng `amount_mismatch_count` và sinh dòng `reconciliation_items(amount_mismatch)` mới dù mục hàng đợi đã `assigned`/`refunded`.
- **Đề xuất sửa**: Nếu `provider_tx_id` đã có dòng `reconciliation_queue` ở trạng thái `assigned`/`refunded`, đánh dấu `matched` (kèm ghi chú đã xử lý thủ công) thay vì `amount_mismatch`.

### P2-3. `run_reconciliation` không đẩy `missing_local` vào `reconciliation_queue`, và `fetch_sepay_transactions` chỉ lấy `limit=100` không lọc theo ngày
- **Vị trí**: [service.rs:2801-2817](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2801-L2817), [vietqr.rs:423-438](file:///home/tai/Document/nexusmart/services/payment/src/vietqr.rs#L423-L438)
- **Kịch bản**:
  - (a) Ở SePay, chuyển khoản sai nội dung kèm mất webhook được `run_reconciliation` gắn `missing_local` ở `reconciliation_items` nhưng không chèn vào `reconciliation_queue` → admin không thấy trên hàng đợi để `ASSIGN`/`REFUND`.
  - (b) `fetch_sepay_transactions` chỉ gọi `?limit=100` không truyền khoảng ngày và không phân trang; đối soát ngày cũ (sau khi đã có >100 giao dịch mới) hoặc ngày >100 giao dịch sẽ bỏ sót giao dịch phía SePay → gắn `missing_provider` giả hàng loạt.
- **Đề xuất sửa**: Chèn `missing_local` vào `reconciliation_queue` (dùng chung cơ chế dedup `SAVEPOINT` theo `provider_tx_id`) và truyền khoảng thời gian ngày VN + phân trang trong `fetch_sepay_transactions`.

### P2-4. Giải ngân sớm tiền đang giữ 72 giờ của đơn mới khi shop có hoàn tiền sau quyết toán trên đơn cũ
- **Vị trí**: [service.rs:4361-4408](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L4361-L4408) kết hợp [service.rs:1155-1166](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L1155-L1166)
- **Kịch bản**: Shop A có `ord-0` (`200k`, `released` → `available = 200k`) và `ord-1` (`100k`, `pending_release` lúc `t = 0` → `pending = 100k`). Lúc `t = 1h`, hoàn sau quyết toán `100k` cho `ord-0` rút `seller_pending` trước theo spec → `pending = 0`, `available = 200k`, nhưng `ord-1` vẫn `pending_release` với `split.seller_net_vnd = 100k`. Lúc `t = 71h59m`, `ord-2` (`100k`, mới hoàn tất, phải giữ 72h) quyết toán → `pending = 100k`. 1 phút sau (`t = 72h00m`), `ord-1` đáo hạn và `release_pending_allocation_tx` tính `min(100k, pending_bal=100k) = 100k` → chuyển ngay `100k` của `ord-2` sang `seller_available` sau 1 phút (sớm 71h59m; tổng tiền shop vẫn cân bằng nhưng phá vỡ cửa sổ giữ tiền 72h cho `ord-2`).
- **Đề xuất sửa**: Khi tính `release_amount`, giới hạn trần theo `(pending_bal - tổng seller_net của các allocation pending_release chưa đáo hạn của shop).max(0)`.

### P2-5. `SettleRefund` trả `200 OK` khi gửi `Idempotency-Key` mới kèm `provider_refund_id` khác cho refund đã tất toán
- **Vị trí**: [service.rs:1555-1561](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L1555-L1561)
- **Kịch bản**: Admin A tất toán refund `R1` với `Idempotency-Key: K1`, `provider_refund_id: "FT-111"`. Admin B gọi `SettleRefund(R1)` bằng `Idempotency-Key: K2` mới và `provider_refund_id: "FT-222"` khác (ví dụ vừa chuyển nhầm lần 2). Nhánh `if refund.status == "completed"` trả `200 OK { already_settled: true }` chứa `"FT-111"`, trái với `ResolveReconciliationItem` ([service.rs:2155](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2155)) trả `409 Conflict`, dễ che giấu sai khác mã tham chiếu chuyển khoản.
- **Đề xuất sửa**: Ở dòng `1555`, nếu `refund.status == "completed"` với `idempotency_key` khác (nhất là khi `provider_refund_id` khác), trả `Err(PaymentError::Conflict(...))`.

### P2-6. `process_order_returned_event` bỏ qua COD legacy không có dòng `payment_shop_allocations`
- **Vị trí**: [service.rs:3211-3266](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L3211-L3266) (so với [service.rs:3938-3984](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L3938-L3984))
- **Kịch bản**: `validate_allocations_sum` ([domain.rs:781](file:///home/tai/Document/nexusmart/services/payment/src/domain.rs#L781)) cho phép payment không có `shop_allocations`, và `collect_cod_order_allocations_tx` có nhánh `total_allocs == 0`. Nhưng `process_order_returned_event` chỉ duyệt `allocs`: nếu `allocs` rỗng, cả giao thất bại (`SHIPPING`) lẫn trả hàng sau giao trễ `order.delivered` (`RETURN_REQUESTED`) đều không làm gì → COD payment kẹt `'pending'` vĩnh viễn và `CreateRefund` lỗi `409` mãi mãi.
- **Đề xuất sửa**: Khi `allocs.is_empty()` và `payment.method == "COD" && payment.status == "pending"`, xử lý theo `from_status` (`RETURN_REQUESTED` → `collect_cod_order_allocations_tx`, còn lại → đặt `payments.status = 'cancelled'`).

### P2-7. `scan_expired_payments` lặp vô hạn mỗi 15 giây (và có nguy cơ chiếm hết `LIMIT 50`) khi gặp payment quá hạn nhưng cổng báo lệch tiền
- **Vị trí**: [service.rs:2496-2588](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2496-L2588) (nhánh `Some(_)` dòng `2583-2586`)
- **Kịch bản**: Payment VietQR `pending` quá `expires_at`, phía cổng có giao dịch nhưng `amount_vnd != payment.amount_vnd`. Nhánh `Some(_)` chỉ `continue` mà không chuyển trạng thái payment và không đẩy vào `reconciliation_queue` → mỗi 15 giây worker chọn lại đúng payment đó, liên tục gọi PayOS/SePay (ở SePay gọi lại `fetch_sepay_transactions()` cho từng payment) và nếu tích đủ 50 dòng sẽ chặn toàn bộ các payment quá hạn mới hơn.
- **Đề xuất sửa**: Ở nhánh `Some(provider_tx)` lệch tiền, chèn `reconciliation_queue` (dedup `provider_tx_id`) và cập nhật trạng thái/cờ loại khỏi hàng đợi quét hết hạn.

### P2-8 (Nghi ngờ). `parse_sepay_transactions_response` có thể bỏ rơi mọi giao dịch SePay nếu `amount_in` có dạng thập phân `"255000.00"`
- **Vị trí**: [vietqr.rs:540-549](file:///home/tai/Document/nexusmart/services/payment/src/vietqr.rs#L540-L549)
- **Kịch bản (nghi ngờ)**: Code parse `amount_in` bằng `s.parse::<i64>()`. API `/userapi/transactions/list` thực tế của SePay thường trả `amount_in` dạng chuỗi `DECIMAL` như `"255000.00"`, khi đó `parse::<i64>()` trả `Err` → `unwrap_or(0)` → bị `if amount_in <= 0 { continue; }` bỏ qua toàn bộ giao dịch.
- **Cách kiểm**: Gọi mẫu `GET <SEPAY_BASE_URL>/userapi/transactions/list?limit=1` trên tài khoản sandbox/thật của SePay để xác nhận định dạng JSON của `amount_in`.
- **Đề xuất sửa**: Cho phép cắt hậu tố `.00` (khi phần sau dấu `.` toàn chữ số `0`) trước khi `parse::<i64>()`, vẫn từ chối nếu có phần lẻ khác 0.

---

## P3 — Nhỏ

### P3-1. `load_mock_provider_transactions_for_vn_date` quét toàn bảng `webhook_events` kèm JSONB `raw_payload`
- **Vị trí**: [service.rs:3043-3066](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L3043-L3066)
- **Kịch bản**: Không lọc khoảng `received_at` trong SQL nên mỗi lần chạy đối soát mock sẽ tải toàn bộ lịch sử `webhook_events` vào RAM rồi mới lọc theo ngày VN.
- **Đề xuất sửa**: Thêm điều kiện lọc khoảng `w.received_at` quanh `vn_day_utc_bounds(run_date)` (có đệm ±1 ngày cho lệch `occurred_at`).

### P3-2. So sánh mốc giữ tiền 72 giờ trộn đồng hồ Rust (`Utc::now()`) với PostgreSQL (`NOW()`)
- **Vị trí**: [service.rs:1812, 1820, 1876](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L1812-L1876) so với [service.rs:4475](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L4475)
- **Kịch bản**: `settled_at` ghi bằng `NOW()` của DB, còn `cutoff` tính từ `Utc::now()` của container Rust; lệch đồng hồ khiến release sớm/trễ so với 72h trên DB (và `Duration::hours` có thể panic nếu env quá lớn).
- **Đề xuất sửa**: So sánh trực tiếp bằng đồng hồ PostgreSQL `a.settled_at <= NOW() - make_interval(hours => $1)` với `hold_hours` đã chặn trên.

### P3-3. Index `idx_payments_vietqr_paid_at` trong migration `0004` không khớp biểu thức `COALESCE(paid_at, created_at)`
- **Vị trí**: [0004_reconciliation_queue_unique_tx.sql:32-34](file:///home/tai/Document/nexusmart/services/payment/migrations/0004_reconciliation_queue_unique_tx.sql#L32-L34) và [service.rs:2753-2754](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2753-L2754)
- **Kịch bản**: Index đánh trên `(paid_at)` nhưng query dùng `COALESCE(paid_at, created_at) >= $1 AND COALESCE(paid_at, created_at) < $2`, nên PostgreSQL không dùng được index này.
- **Đề xuất sửa**: Tách điều kiện query thành `((paid_at >= $1 AND paid_at < $2) OR (paid_at IS NULL AND created_at >= $1 AND created_at < $2))` (hoặc tạo index trên biểu thức `COALESCE(paid_at, created_at)`).

---

## Thừa thãi (`ponytail-review`)

- [vietqr.rs:133, 159, 211-219, 233-236, 246-253](file:///home/tai/Document/nexusmart/services/payment/src/vietqr.rs#L133-L253) & [service.rs:951-957](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L951-L957): `delete:` `mock_transactions: Arc<Mutex<Vec<ProviderTransaction>>>` và `record_mock_transaction` (đối soát mock đã đọc từ `webhook_events`; `record_mock_transaction` chỉ gọi sau khi payment đã `succeeded` nên `scan_expired_payments` không bao giờ khớp, chỉ gây tăng RAM vô hạn). Xoá field + hàm; `Mock` trong `check_payment_status` trả `Ok(None)`.
- [domain.rs:607-613](file:///home/tai/Document/nexusmart/services/payment/src/domain.rs#L607-L613): `delete:` `is_payment_in_vn_reconciliation_day` chỉ gọi trong unit test, không có caller production. Xoá.
- [domain.rs:1119-1126](file:///home/tai/Document/nexusmart/services/payment/src/domain.rs#L1119-L1126): `delete:` `is_hold_period_elapsed` chỉ gọi trong unit test, không có caller production. Xoá (hoặc gọi thật trong `service.rs`).
- [0004_reconciliation_queue_unique_tx.sql:32-34](file:///home/tai/Document/nexusmart/services/payment/migrations/0004_reconciliation_queue_unique_tx.sql#L32-L34): `delete:` `idx_payments_vietqr_paid_at` không được query nào khớp nếu giữ `COALESCE(paid_at, created_at)`.

`net: -53 lines possible.`

---

## Kiểm tra Deadlock & Khóa

Đã đối chiếu toàn bộ thứ tự khóa row-lock qua các transaction trong `service.rs` và `ledger.rs`:
- Luồng thanh toán/hoàn tiền/đối soát đều khóa theo hướng nhất quán: `payments` (theo nhóm `ORDER BY id ASC`) → `payment_shop_allocations` (`ORDER BY id ASC`) → `ledger_accounts` (`seller_available` trước `seller_pending` theo `ORDER BY id ASC`).
- Worker giữ tiền 72h (`release_single_matured_allocation`) khóa `payment_shop_allocations` bằng `FOR UPDATE OF a SKIP LOCKED` → `ledger_accounts`, an toàn khi chạy nhiều replica và không tạo vòng khóa với `create_refund`/`settle_order_to_pending_tx`/`release_escrow`/`request_seller_payout`.
- `resolve_reconciliation_item` khóa `reconciliation_queue` theo `provider_tx_id ORDER BY id ASC` → `payments` nhóm; không xung đột ngược chiều với `run_reconciliation` (vì `WHERE NOT EXISTS` chỉ đọc MVCC).
- **Kết luận**: Không thấy chu trình deadlock AB-BA trong `services/payment`.

---

## Các file đã đọc trong phạm vi `services/payment/**` mà không thấy lỗi

- [0003_refund_settlement_and_reconciliation.sql](file:///home/tai/Document/nexusmart/services/payment/migrations/0003_refund_settlement_and_reconciliation.sql) (an toàn trên DB có dữ liệu cũ; backfill `completed` chưa có `refund_settle` về `pending` và `affects_escrow = FALSE` cho `is_late_payment` đúng spec; partial index `idx_payment_allocations_pending_release` khớp worker 72h)
- [ledger.rs](file:///home/tai/Document/nexusmart/services/payment/src/ledger.rs) (sổ cái ghi kép cân bằng, `build_refund_settle_tx`/`build_reconciliation_refund_tx` chuẩn, `get_seller_balance` trừ clawback vào `total_earned_vnd`, `lock_and_get_seller_balances_tx` khóa xác định)
- [grpc.rs](file:///home/tai/Document/nexusmart/services/payment/src/grpc.rs) (RBAC và kiểm quyền theo đối tượng đầy đủ trên cả 13 RPC; che `bank_account_number` cho buyer ở `ListRefunds` và `SubmitRefundBankAccount`)
- [auth.rs](file:///home/tai/Document/nexusmart/services/payment/src/auth.rs) (`SERVICE_METHODS` thu hẹp còn `GetPayment`, `CancelPayment`, `CreateRefund`; từ chối token service ở `ReleaseEscrow` và toàn bộ RPC mới; không tin `x-user-*`)
- [config.rs](file:///home/tai/Document/nexusmart/services/payment/src/config.rs) (đọc `PAYMENT_SELLER_HOLD_HOURS`, `ESCROW_HOLD_SCAN_INTERVAL_SECS`; `Debug` che secret/DSN/token)
- [http.rs](file:///home/tai/Document/nexusmart/services/payment/src/http.rs) (HMAC constant-time + replay ≤ 300s, SSE và `/internal/*` xác minh end-user JWT, `/internal/reconciliation/run` dùng `vn_today`)
- [workers.rs](file:///home/tai/Document/nexusmart/services/payment/src/workers.rs) (5 worker đầy đủ, consumer đăng ký 4 subject `order.{completed,cancelled,delivered,returned}` và tự cập nhật `filter_subjects`, retry 5 lần → `NEXUSMART_DLQ`)
- [main.rs](file:///home/tai/Document/nexusmart/services/payment/src/main.rs), [webhook_security.rs](file:///home/tai/Document/nexusmart/services/payment/src/webhook_security.rs), [telemetry.rs](file:///home/tai/Document/nexusmart/services/payment/src/telemetry.rs), [metrics.rs](file:///home/tai/Document/nexusmart/services/payment/src/metrics.rs), [lib.rs](file:///home/tai/Document/nexusmart/services/payment/src/lib.rs), [Cargo.toml](file:///home/tai/Document/nexusmart/services/payment/Cargo.toml), [CLAUDE.md](file:///home/tai/Document/nexusmart/services/payment/CLAUDE.md), [0001_init.sql](file:///home/tai/Document/nexusmart/services/payment/migrations/0001_init.sql), [0002_payment_money_model.sql](file:///home/tai/Document/nexusmart/services/payment/migrations/0002_payment_money_model.sql)
