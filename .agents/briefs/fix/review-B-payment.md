# Review độc lập payment (agy, chỉ đọc) — 2026-11-23

Manager đã tự xác nhận P0-2 trong code (service.rs:145-160 tra idempotency_key toàn cục; gateway/order chuyển nguyên key client).

Dưới đây là kết quả review độc lập (chỉ đọc) cho phạm vi `services/payment/**` và `tests/e2e/13_payment_money_test.go`, đối chiếu với [plan.md](file:///home/tai/Document/nexusmart/.agents/briefs/fix/plan.md), [w1-payment.md](file:///home/tai/Document/nexusmart/.agents/briefs/fix/w1-payment.md), [w1-order.md](file:///home/tai/Document/nexusmart/.agents/briefs/fix/w1-order.md), [w1-gateway.md](file:///home/tai/Document/nexusmart/.agents/briefs/fix/w1-gateway.md), [docs/contracts.md](file:///home/tai/Document/nexusmart/docs/contracts.md) và [AGENTS.md](file:///home/tai/Document/nexusmart/AGENTS.md).

---

## Phát hiện theo mức nặng

### P0 — Sai tiền / vượt quyền / mất dữ liệu

1. **[P0] Race giữa `order.delivered` (NATS async) và `BuyerConfirmReceived` (`ReleaseEscrow` gRPC sync + `order.completed` NATS async) làm mất hoặc kẹt vĩnh viễn doanh thu seller trên đơn COD**
   - **Vị trí**: [service.rs:1324-1371](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L1324-L1371), [service.rs:1915-1941](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L1915-L1941), [service.rs:2982-3004](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2982-L3004)
   - **Kịch bản gây sai**: Đơn COD mới tạo có `payments.status = 'pending'`, `alloc.escrow_status = 'awaiting_collection'`. Khi giao thành công, `order` cập nhật `DELIVERED` + ghi `order.delivered` vào outbox; buyer gọi ngay `confirm-received` (đúng luồng ở [02_cod_checkout_test.go:295-311](file:///home/tai/Document/nexusmart/tests/e2e/02_cod_checkout_test.go#L295-L311), còn `13_payment_money_test.go:218` vô tình che lỗi vì tự poll payment `SUCCEEDED` trước):
     - *Nhánh 1 (out-of-order: `order.completed` đến trước `order.delivered`)*: `process_order_completed_event` ghi `event_id` vào `processed_nats_events`, gọi `settle_order_to_pending_tx` (lọc `a.escrow_status = 'held'` nên trả `Ok(0)`), rồi **commit + ACK** message. Sau đó `order.delivered` chuyển allocation sang `held`, nhưng `order.completed` không bao giờ chạy lại → **tiền kẹt vĩnh viễn ở `escrow_liability`, `seller_pending = 0`, `seller_available = 0`, seller mất toàn bộ tiền hàng**.
     - *Nhánh 2 (in-order nhưng `ReleaseEscrow` sync chạy trước khi `order.delivered` kịp relay)*: `release_escrow` thấy `0` dòng `held`/`pending_release` và trả `Ok(released: false)` (không trả lỗi nên `order` không retry); sau đó `order.delivered` → `held` rồi `order.completed` chỉ chạy `settle_order_to_pending_tx` → `pending_release` (`seller_pending`) mà không còn job nào chuyển tiếp sang `seller_available` trong đợt 1 → **tiền kẹt vĩnh viễn ở `seller_pending`, `available_vnd = 0`**.
   - **Đề xuất sửa**: Nếu tìm thấy allocation COD của `order_id` đang ở `awaiting_collection`, `process_order_completed_event` trả lỗi tạm (rollback không ghi `processed_nats_events`) để JetStream NAK/retry sau `order.delivered`, đồng thời `release_escrow` tự ghi thu COD idempotent (`cod-collect:{payment_id}:{order_id}` + outbox `payment.succeeded`) trước khi quyết toán (vì `ReleaseEscrow` chỉ được gọi cho đơn đã qua `DELIVERED -> COMPLETED`).

2. **[P0] `create_payment` tra `idempotency_key` toàn cục không kiểm khớp `user_id` / `order_group_id` → IDOR lộ payment người khác và ghi hỏng `orders.payment_id` xuyên người dùng**
   - **Vị trí**: [service.rs:146-162](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L146-L162), [service.rs:208-225](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L208-L225), [service.rs:328-346](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L328-L346), [grpc.rs:146](file:///home/tai/Document/nexusmart/services/payment/src/grpc.rs#L146)
   - **Kịch bản gây sai**: Buyer A gọi `POST /api/v1/payments/{payA}/retry` với header `Idempotency-Key: retry-1` (gateway chuyển nguyên giá trị vào `OrderService.RetryPayment` → `PaymentService.CreatePayment` tại [handlers_payment.go:221-233](file:///home/tai/Document/nexusmart/services/gateway/internal/router/handlers_payment.go#L221-L233) và [lifecycle.go:898-913](file:///home/tai/Document/nexusmart/services/order/internal/service/lifecycle.go#L898-L913)). Sau đó Buyer B gọi `POST /api/v1/payments/{payB}/retry` với cùng `Idempotency-Key: retry-1` (hoặc gọi trực tiếp gRPC `CreatePayment` với `user_id = buyerB`). `grpc.rs:146` chỉ kiểm `req.user_id == buyerB` (qua), rồi `create_payment` trả thẳng payment của Buyer A (`payA2`) mà không kiểm `existing.user_id`/`order_group_id`/`amount_vnd` → lộ payment/QR/allocations của A (gRPC) và khiến `OrderService.RetryPayment` ghi đè `orders.payment_id` của đơn B thành `payA2` của A làm hỏng vĩnh viễn đơn B (gọi `GetPayment` bị `403`).
   - **Đề xuất sửa**: Ở cả 3 nhánh kiểm `idempotency_key` trong `create_payment`, đối chiếu `existing.user_id == input.user_id && existing.order_group_id == order_group_id && existing.amount_vnd == input.amount_vnd && existing.method == input.method.as_str()`, khác thì trả `PaymentError::Conflict`.

3. **[P0] Race giữa `order.cancelled` từng đơn con và webhook `SUCCEEDED` trên payment VietQR đa gian hàng đang `pending` làm mất tiền đơn con đầu trong `escrow_liability`**
   - **Vị trí**: [service.rs:2424-2470](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2424-L2470), [service.rs:2873-2913](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2873-L2913)
   - **Kịch bản gây sai**: Đơn VietQR 2 shop (`ord1 = 200k`, `ord2 = 300k`, `payment.amount_vnd = 500k`, `pending`). Buyer huỷ nhóm đơn (`order` huỷ cả 2 đơn, phát `order.cancelled(ord1)` và `order.cancelled(ord2)`; giả sử `CancelPayment` sync lỗi mạng/chậm). `payment` xử lý `order.cancelled(ord1)` → `alloc1 = 'cancelled'`, nhưng vì `remaining_active = 1` nên giữ `payments.status = 'pending'`. Nếu webhook `SUCCEEDED` `500k` đến ngay trước `order.cancelled(ord2)`: `confirm_vietqr_payment_tx` không kiểm allocation nào đã `'cancelled'`, chốt payment thành `'succeeded'`, nạp đủ `500k` vào `escrow_liability` (`alloc1` vẫn `'cancelled'`, `alloc2` thành `'held'`; `order` bỏ qua `payment.succeeded` vì cả 2 đơn đã `CANCELLED`). Tiếp đó `order.cancelled(ord2)` chỉ hoàn `300k` của `ord2` → **`200k` của `ord1` không bao giờ được hoàn** (vì `order.cancelled(ord1)` đã vào `processed_nats_events` và `alloc1` đã `'cancelled'`), kẹt vĩnh viễn trong `escrow_liability`. *(Lưu ý: nhánh giữ `pending` tới đơn cuối xuất phát từ câu chữ `w1-payment.md:12`.)*
   - **Đề xuất sửa**: Trong `process_verified_webhook`, nếu payment VietQR `pending` đã có bất kỳ allocation `'cancelled'` nào thì chuyển sang `LatePaymentRefund` (và/hoặc huỷ ngay payment VietQR `pending` khi nhận `order.cancelled` đầu tiên vì QR thu một cục `P1 + P2` không thể xác nhận một phần).

---

### P1 — Hỏng luồng chính / race nghiêm trọng

4. **[P1] `run_reconciliation` và `scan_expired_payments` xác nhận payment không khoá nhóm và thiếu điều kiện `WHERE status = 'pending'`; đối soát đánh `"matched"` cho payment đã huỷ/hết hạn/superseded**
   - **Vị trí**: [service.rs:1550-1587](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L1550-L1587), [service.rs:1685-1848](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L1685-L1848), [service.rs:2880-2894](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2880-L2894)
   - **Kịch bản gây sai**:
     - *Race đối soát*: `run_reconciliation` đọc `local_payments` không `FOR UPDATE`; nếu `CancelPayment` hoặc `CreatePayment(supersede_previous=true)` chuyển `local` sang `cancelled`/`superseded` đúng lúc đó, `confirm_vietqr_payment_tx` (`UPDATE payments SET status = 'succeeded' WHERE id = $2`, không có `AND status = 'pending'`) vẫn ghi đè `status = 'succeeded'` lên payment đã huỷ/superseded (hoặc đụng `idx_payments_one_succeeded_per_group` làm hỏng cả giao dịch đối soát). Tương tự ở `scan_expired_payments:1557-1585`, không khoá các payment anh em cùng nhóm nên một xung đột unique index sẽ rollback toàn bộ batch 50 payment hết hạn.
     - *Bỏ sót hoàn tiền trễ khi đối soát*: Nếu webhook trễ bị mất và giao dịch thành công của một payment `cancelled`/`expired`/`failed`/`superseded` chỉ xuất hiện trong `provider_txs`, `run_reconciliation` rơi vào nhánh `else` ([service.rs:1832-1847](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L1832-L1847)) và báo `"matched"`, không ghi bút toán `late_payment_refund` và không tạo refund `pending`.
   - **Đề xuất sửa**: Khoá cả nhóm payment theo `ORDER BY id ASC FOR UPDATE` + thêm `AND status = 'pending'` trong `confirm_vietqr_payment_tx`, và trong `run_reconciliation` định tuyến `cancelled|expired|failed|superseded` sang `record_late_duplicate_refund_tx` thay vì `"matched"`.

5. **[P1] Deadlock AB-BA trên bảng `payments` giữa `process_verified_webhook` và `create_payment` / webhook anh em**
   - **Vị trí**: [service.rs:692-714](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L692-L714), [service.rs:2819-2828](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2819-L2828) đối chiếu [service.rs:228-239](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L228-L239)
   - **Kịch bản gây sai**: Nhóm đơn có `P1` (UUID nhỏ) và `P2` (UUID lớn). Khi webhook của `P2` chạy song song với `create_payment(supersede_previous=true)` (hoặc webhook của `P1`): `find_payment_for_webhook_tx` khoá `P2` trước rồi dòng 703 mới khoá toàn bộ nhóm `ORDER BY id ASC` (xin khoá `P1`), còn luồng kia giữ `P1` và xin `P2` → PostgreSQL báo deadlock (`40P01`), trả HTTP `500 DATABASE_ERROR` cho webhook hoặc retry.
   - **Đề xuất sửa**: Tìm `order_group_id`/`order_id` không `FOR UPDATE` trước, rồi khoá toàn bộ các dòng `payments` cùng nhóm một lượt theo `ORDER BY id ASC FOR UPDATE` trước khi đọc lại bản ghi mục tiêu.

---

### P2 — Lệch spec / biên hiếm / phòng thủ chiều sâu

6. **[P2] `process_order_returned_event` bỏ sót đơn `0₫` (COD lẫn VietQR) khi giao thất bại (`SHIPPING -> RETURNED`), để allocation treo `held`**
   - **Vị trí**: [service.rs:2176-2240](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2176-L2240), [domain.rs:650-657](file:///home/tai/Document/nexusmart/services/payment/src/domain.rs#L650-L657)
   - **Kịch bản gây sai**: Đơn `0₫` (`R = 200k, D = 220k, P = 0`) khởi tạo với `payments.status = 'succeeded'`, `alloc.escrow_status = 'held'`. Khi giao thất bại (`SHIPPING -> RETURNED`), `order` phát `order.returned` (`from_status = "SHIPPING"`) và không gọi `CreateRefund` (vì `TotalPayableVND = 0`). `process_order_returned_event` lọc `p.method = 'COD'` (bỏ qua hoàn toàn 0₫ VietQR), còn với 0₫ COD thì `decide_cod_return_action(Some("SHIPPING"), "held")` trả `DeferPostDeliveryReturn` → allocation mãi ở `'held'` (trái với `process_order_cancelled_event:2471` đã chuyển `'cancelled'`); nếu `ReleaseEscrow` bị gọi sẽ hạch toán nhầm `platform_promo_expense` trả seller cho đơn giao thất bại.
   - **Đề xuất sửa**: Xử lý thêm nhánh `payment.amount_vnd == 0` (hoặc allocation `P = 0`) với `from_status != Some("RETURN_REQUESTED")` trong `process_order_returned_event` để chuyển allocation `held -> cancelled` cho cả COD và VietQR.

7. **[P2] Phòng thủ chiều sâu R3 (`PAY-B1`): `create_payment(supersede_previous=true)` vẫn tạo payment `pending` mới khi nhóm chỉ còn payment `cancelled`/`expired`/`failed`**
   - **Vị trí**: [service.rs:241-278](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L241-L278)
   - **Kịch bản gây sai**: `P1` đã chuyển sang `expired`/`cancelled`/`failed` ở `payment_db` nhưng `OrderService.RetryPayment` chạy trước khi `order` nhận event (hoặc caller gRPC gọi trực tiếp `CreatePayment(supersede_previous=true)`). Dòng 242 chỉ chặn `"succeeded" | "refunded"`, vòng `UPDATE ... WHERE status = 'pending'` cập nhật 0 dòng rồi vẫn chèn `P2` ở trạng thái `pending` cho nhóm đơn đã chết.
   - **Đề xuất sửa**: Khi `input.supersede_previous == true`, từ chối bằng `PaymentError::Conflict` nếu không có payment `pending` nào trong nhóm hoặc đã có payment `cancelled`/`expired`/`failed`.

8. **[P2] `create_refund` replay `idempotency_key` không kiểm khớp `payment_id`/`order_id`**
   - **Vị trí**: [service.rs:1009-1020](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L1009-L1020), [service.rs:1197-1212](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L1197-L1212)
   - **Kịch bản gây sai**: Admin gửi `POST /api/v1/admin/payments/{P2}/refund` (`order_id = ord2`) nhưng vô tình dùng lại `Idempotency-Key` đã dùng cho `P1`/`ord1` → trả `200 OK` cùng bản ghi refund của `P1`/`ord1` (`already_existed: true`) thay vì báo lỗi `409 Conflict`. *(Lưu ý: giữ nguyên không kiểm khớp `amount_vnd` cho khoá `order-cancel-refund:*` để không phá cơ chế dedup giữa `OrderService.CancelOrder` và `process_order_cancelled_event` khi đơn đã hoàn một phần.)*
   - **Đề xuất sửa**: Khi replay `idempotency_key` trong `create_refund`, kiểm `existing.payment_id` (nếu request có `payment_id`) và `existing.order_id` khớp mục tiêu, lệch thì trả `PaymentError::Conflict`.

9. **[P2] Hoàn 100% ở trạng thái `pending_release` (và tổ hợp hoàn + huỷ trên đơn đa shop) để `payments.status` lệch (`succeeded` thay vì `refunded`)**
   - **Vị trí**: [service.rs:1236-1270](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L1236-L1270), [service.rs:2383-2422](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2383-L2422), [service.rs:2575-2588](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2575-L2588)
   - **Kịch bản gây sai**: (a) Đơn 1 shop đang ở `escrow_status = 'pending_release'`, admin hoàn đủ 100% `P` (clawback từ seller) → dòng 1242 chỉ cập nhật `WHERE escrow_status = 'held'`, nên `remaining_active_allocs` dòng 1256 vẫn đếm là `1` và `payments.status` giữ nguyên `'succeeded'` thay vì `'refunded'`; (b) COD 2 shop có shop 1 đã `refunded` 100% và shop 2 huỷ trước giao → `collected_active` dòng 2388 đếm cả `'refunded'` nên `payments.status` chuyển thành `'succeeded'` thay vì `'refunded'`; (c) VietQR 2 shop có shop 1 `P1 > 0` đã hoàn 100%, shop 2 `P2 = 0` huỷ sau → nhánh dòng 2575 không chuyển `payments.status` sang `'refunded'`. *(Sổ cái vẫn cân và không cho hoàn vượt.)*
   - **Đề xuất sửa**: Cập nhật `escrow_status = 'refunded'` khi `suborder_remaining_after == Some(0)` cho cả `pending_release`, và đặt `payments.status = 'refunded'` khi tổng đã hoàn bằng tổng đã thu và không còn allocation hoạt động.

---

### P3 — Độ phủ kiểm thử E2E

10. **[P3] `13_payment_money_test.go` chỉ kiểm số dư `escrow_liability` toàn cục `>= 0` ở `PAY-B2`, chưa kiểm bất biến escrow theo riêng payment và chưa kiểm `cod_receivable` ở `PAY-B4`**
    - **Vị trí**: [13_payment_money_test.go:381-387](file:///home/tai/Document/nexusmart/tests/e2e/13_payment_money_test.go#L381-L387), [13_payment_money_test.go:158-235](file:///home/tai/Document/nexusmart/tests/e2e/13_payment_money_test.go#L158-L235)
    - **Kịch bản gây sai**: Trên DB có dữ liệu từ các test trước (`02_`, `03_`), `escrow_liability` toàn cục luôn dương lớn hơn nhiều lần `5,000₫` nên `escrowAcc.BalanceVND < 0` sẽ không bắt được lỗi rút lẹm escrow của riêng payment đó (như yêu cầu "truy vấn tổng escrow theo payment không âm" trong `w1-payment.md:17`). Tương tự `PAY-B4` chưa khẳng định `cod_receivable` không tăng khi đặt/huỷ COD #1 và tăng đúng `P` khi giao COD #2.
    - **Đề xuất sửa**: So sánh delta `escrow_liability` (trước webhook vs sau Case B phải bằng `0`) hoặc truy vấn theo payment, và khẳng định delta `cod_receivable` trước/sau đặt-huỷ-giao ở `PAY-B4`.

---

## Over-engineering (`ponytail-review`)

- `services/payment/src/domain.rs:257-314,880-895`: `delete:` `AllocationSplit` + `compute_allocation_split` và 2 unit test cũ đã thành dead code sau khi thay bằng `SettlementSplit`/`compute_settlement_split`. Xoá hẳn (~75 dòng).
- `services/payment/src/ledger.rs:202-219`: `delete:` `build_cod_confirm_tx` là dead code (chỉ còn 1 chỗ gọi trong unit test `:1022`), thay bằng `build_cod_collect_tx` (~20 dòng).
- `services/payment/src/service.rs:530-577,2243-2285,2372-2422`: `shrink:` lặp 3 lần cùng khối SQL đếm `remaining_awaiting`/`collected_active` + `resolve_cod_payment_status` + `UPDATE payments`. Gộp 1 helper `sync_cod_payment_status_tx` (~70 dòng).
- `services/payment/src/service.rs:874-936,2728-2784`: `reuse:` lặp logic ghi sổ `build_late_payment_refund_tx` + `INSERT INTO refunds` (`late_payment`, `pending`); `record_late_duplicate_refund_tx` còn thừa 2 tham số `_reason`, `_trace_id` và `#[allow(clippy::too_many_arguments)]`. Gộp 1 helper và bỏ 2 tham số thừa (~35 dòng).
- `services/payment/src/service.rs:1921-1936,1956-1971,2159-2174,2311-2326`: `shrink:` lặp 4 lần `INSERT INTO processed_nats_events ... ON CONFLICT DO NOTHING`. Tách 1 helper `claim_nats_event_tx` (~30 dòng).
- `services/payment/src/grpc.rs:48-50,58,73,86,259-264`: `delete:` `CallerAuth::has_role()` luôn `true` (đã chặn role rỗng tại `auth.rs:145`) và nhánh báo lỗi cũ `"missing x-user-id"` (đã chặn `sub` rỗng tại `auth.rs:173`). Xoá nhánh chết (~15 dòng).

`net: -245 lines possible.`

---

## Phần đã đọc và không thấy lỗi

- **Lệnh chỉ đọc đã chạy**:
  - `cd services/payment && cargo test && cargo clippy --all-targets -- -D warnings && cargo fmt --all -- --check` → **48/48 unit test xanh, clippy và rustfmt sạch**.
  - `cd tests/e2e && go vet ./... && go test -c -o /dev/null .` → **biên dịch và `go vet` sạch**.
  - `SELECT` trên `payment_db` (`nexusmart-audit`): xác nhận migration `0001` & `0002` đã áp dụng, `platform_promo_expense (EXPENSE)` tồn tại, `idx_payments_one_succeeded_per_group` đã tạo.
- **Các file trong phạm vi đã đọc toàn bộ và không thấy lỗi logic/bảo mật mới**:
  - [0001_init.sql](file:///home/tai/Document/nexusmart/services/payment/migrations/0001_init.sql), [0002_payment_money_model.sql](file:///home/tai/Document/nexusmart/services/payment/migrations/0002_payment_money_model.sql) (idempotent, DO-block an toàn trên DB cũ đã trùng `order_group_id`, backfill `is_late_payment` khớp `late-webhook:`/`late-autoheal:` trước đợt 1)
  - [auth.rs](file:///home/tai/Document/nexusmart/services/payment/src/auth.rs) (xác minh HS256 tách `user_key`/`service_key`, allowlist `SERVICE_METHODS`, `require_payment_read_access`)
  - [http.rs](file:///home/tai/Document/nexusmart/services/payment/src/http.rs) (SSE và `/internal/*` bắt buộc end-user JWT đúng quyền, từ chối service token; webhook bắt buộc `X-Signature` + `X-Timestamp`)
  - [webhook_security.rs](file:///home/tai/Document/nexusmart/services/payment/src/webhook_security.rs) (HMAC thời gian hằng `verify_slice`, chống replay `<= 300s`, chuyển `failed` sang `LatePaymentRefund`)
  - [ledger.rs](file:///home/tai/Document/nexusmart/services/payment/src/ledger.rs) (bút toán ghi kép cân bằng số nguyên VND; `lock_and_get_seller_balances_tx` khoá theo thứ tự chữ cái `available` → `pending`; trừ 1 mục dead code nêu ở phần ponytail)
  - [workers.rs](file:///home/tai/Document/nexusmart/services/payment/src/workers.rs) (bổ sung `order.delivered`/`order.returned` và `ensure_order_consumer` cập nhật/tạo lại consumer JetStream an toàn)
  - [error.rs](file:///home/tai/Document/nexusmart/services/payment/src/error.rs), [config.rs](file:///home/tai/Document/nexusmart/services/payment/src/config.rs), [main.rs](file:///home/tai/Document/nexusmart/services/payment/src/main.rs), [metrics.rs](file:///home/tai/Document/nexusmart/services/payment/src/metrics.rs), [telemetry.rs](file:///home/tai/Document/nexusmart/services/payment/src/telemetry.rs), [vietqr.rs](file:///home/tai/Document/nexusmart/services/payment/src/vietqr.rs) (không có lỗi mới thuộc đợt 1; `vietqr.rs` chỉ còn mục `PAY-B8` đã xếp đợt 2).
- **Công thức tiền lõi đã soát chéo với `services/order` và `services/notification`**: `validate_allocations_sum`, `compute_settlement_split` (kể cả ví dụ `G=229k, D=30k, C=11.45k, Ship=26k → seller 217.55k, promo 30k`, đơn `0₫`, và hoàn một phần trước quyết toán với `D > 0`), `validate_suborder_refund_cap` (đã chặn đơn con `P=0` không ăn sang escrow shop khác), và `compute_post_settlement_clawback` (`seller_pending` trước rồi `seller_available`, thiếu trả `409`) đều đúng mô hình tiền ở `plan.md`.
