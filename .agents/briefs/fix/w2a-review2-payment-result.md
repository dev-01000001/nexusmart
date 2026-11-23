# Review độc lập Đợt 2a (lượt 2) — `services/payment/**`

Đã rà soát toàn bộ `services/payment/**` (chỉ đọc) sau bản sửa lượt 1, đối chiếu [w2a-review-payment-result.md](file:///home/tai/Document/nexusmart/.agents/briefs/fix/w2a-review-payment-result.md), [w2-plan.md](file:///home/tai/Document/nexusmart/.agents/briefs/fix/w2-plan.md), [plan.md](file:///home/tai/Document/nexusmart/.agents/briefs/fix/plan.md) §2, [docs/contracts.md](file:///home/tai/Document/nexusmart/docs/contracts.md), [payment.proto](file:///home/tai/Document/nexusmart/proto/nexusmart/payment/v1/payment.proto), `services/order` và `services/gateway`.

---

## (a) Kết quả nghiệm thu các mục lượt 1

- **P0-1** (hạch toán trùng `gateway_clearing` + advisory lock theo `provider_tx_id`): **chưa đóng hẳn** — còn 2 lỗ hổng mới/sót trên `provider_tx_id` (xem **P0-1**, **P1-1** và **P2-1** bên dưới).
- **P1-1** (`25P02` + treo `reconciliation_runs = 'running'`): đã đóng.
- **P1-2** (PayOS `"cổng chưa có dữ liệu"`): **Gateway đã đóng**, nhưng **chưa thông suốt qua Web BFF** do mã `501` bị cắt thành `503` (xem **P2-2** bên dưới).
- **P1-3** (`paid_at` theo `occurred_at`) & **migration `0005_reconciliation_fixes.sql`**: đã đóng.
- **P1-4** (`release_escrow` chỉ chấp nhận `pending_release`): đã đóng.
- **P1-5** (`SUSPEND` kiểm tra FK `payment_id`): đã đóng.
- **P2-1** (`local_paid_payments` gồm `refunded`): đã đóng.
- **P2-2** (`amount_mismatch` không lặp lại sau `ASSIGN`/`REFUND`): đã đóng (trừ ca `provider_tx_id` có khoảng trắng nêu ở **P0-1**).
- **P2-3(a)** (`missing_local` vào `reconciliation_queue`): đã đóng (lưu ý ca kẹt `pending` ở **P2-1**).
- **P2-3(b)** (SePay lọc ngày & phân trang): **chưa đóng hẳn và phát sinh lỗi mới** (xem **P1-2** bên dưới).
- **P2-4** (trần giải ngân 72h bảo vệ đơn chưa đáo hạn): đã đóng (đã kiểm bất biến cân bằng sổ, không kẹt `seller_pending`; xem thêm **P3-1**).
- **P2-5** (`SettleRefund` từ chối `Idempotency-Key` mới trên refund đã `completed`): đã đóng.
- **P2-6** (`process_order_returned_event` xử lý COD legacy không có allocation): đã đóng.
- **P2-7** (`scan_expired_payments` không lặp vô hạn khi lệch tiền): đã đóng.
- **P2-8** (`parse_integer_vnd_amount` chấp nhận `.00`, chặn phần lẻ ≠ 0): đã đóng.
- **P3-1**, **P3-2**, **P3-3**, **thừa thãi lượt 1**: đã đóng.

---

## (b) & (c) Phát hiện mới / còn mở (xếp theo mức)

### P0 — Tiền / vượt quyền / mất dữ liệu

#### P0-1. Không `.trim()` `provider_tx_id` trước khi lưu DB làm vô hiệu `check_provider_tx_accounting_tx`, gây hạch toán trùng `gateway_clearing` 2×
- **Vị trí**: [webhook_security.rs:132](file:///home/tai/Document/nexusmart/services/payment/src/webhook_security.rs#L132), [service.rs:683](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L683), [service.rs:792](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L792), [service.rs:917](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L917), [service.rs:960](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L960), [service.rs:4287-4303](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L4287-L4303), [service.rs:4547](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L4547), [ledger.rs:316](file:///home/tai/Document/nexusmart/services/payment/src/ledger.rs#L316)
- **Kịch bản**: `lock_provider_tx_xact` và `check_provider_tx_accounting_tx` đều gọi `provider_tx_id.trim()` rồi so `WHERE provider_tx_id = $1`, nhưng `process_verified_webhook` không trim `payload.provider_tx_id` trước khi ghi `webhook_events`, `reconciliation_queue`, `payments.provider_tx_id` và `build_late_payment_refund_tx`.
  1. Webhook 1 (chữ ký hợp lệ) gửi `provider_tx_id: " TX-99 "` (có khoảng trắng) cho payment VietQR `P1` (`200.000₫`, `pending`, `NM1234567890`) → `payments.provider_tx_id = ' TX-99 '`, `status = 'succeeded'`, ghi Nợ `gateway_clearing` 200k / Có `escrow_liability` 200k.
  2. Sau đó `run_reconciliation` (feed SePay trả `"TX-99"`) hoặc Webhook 2 gửi `"TX-99"` khớp `P1` (qua `provider_order_code` hoặc `payment_id`): `webhook_events` `ON CONFLICT (provider_tx_id)` trượt (`'TX-99' != ' TX-99 '`), `check_provider_tx_accounting_tx("TX-99")` truy vấn `WHERE provider_tx_id = 'TX-99'` → cả 3 cờ `on_payment`, `recon_queue_finalized`, `has_tx_refund` đều `FALSE`.
  3. `decide_reconciliation_match_action` (`Some(" TX-99 ") != Some("TX-99")`) hoặc `decide_webhook_action` (`status = 'succeeded'`) chọn `LatePaymentRefund` → `record_late_payment_refund_tx` ghi Nợ `gateway_clearing` thêm 200k / Có `refund_payable` 200k và tạo refund `late-webhook:TX-99` (200k). Một khoản 200k vừa xác nhận đơn 200k vừa sinh hoàn tiền 200k (`gateway_clearing` Nợ 400k). Tương tự nếu Webhook 1 vào `reconciliation_queue` với `" TX-99 "` rồi admin `ASSIGN` đúng tiền (không có dòng `refunds`), bản ghi sau với `"TX-99"` vượt qua `check_provider_tx_accounting_tx`.
- **Đề xuất sửa**: Chuẩn hóa `provider_tx_id = provider_tx_id.trim()` ngay đầu `process_verified_webhook`, `confirm_vietqr_payment_tx`, `enqueue_reconciliation_item_tx`, `record_late_payment_refund_tx` và `build_late_payment_refund_tx`.

---

### P1 — Hỏng luồng chính

#### P1-1. `check_provider_tx_accounting_tx` không lọc `status IN ('succeeded', 'refunded')` trên `payments`, khiến payment bị `MarkFailed` làm bỏ sót hoàn tiền muộn
- **Vị trí**: [service.rs:847-858](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L847-L858), [service.rs:4294](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L4294), [service.rs:2941-2973](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2941-L2973)
- **Kịch bản**: Nhánh `MarkFailed` (`service.rs:850`) ghi `status = 'failed', provider_tx_id = $1` lên `payments` nhưng **không** ghi bút toán `gateway_clearing`. Nếu cổng dùng chung ID yêu cầu thanh toán (ví dụ payOS `/data/id` tại [vietqr.rs:414](file:///home/tai/Document/nexusmart/services/payment/src/vietqr.rs#L414)) báo `FAILED` trước (`payments.status = 'failed', provider_tx_id = 'TX-F1'`), rồi tiền thực tế vẫn vào ngân hàng và xuất hiện trong `run_reconciliation`: `check_provider_tx_accounting_tx("TX-F1")` thấy `EXISTS(SELECT 1 FROM payments WHERE provider_tx_id = 'TX-F1')` là `TRUE` → `service.rs:2943` đánh dấu `"matched"` (`"already accounted for"`) và **bỏ qua `record_late_payment_refund_tx`**. Đơn ở trạng thái `failed`, sổ `gateway_clearing = 0`, người mua mất tiền không được tạo hoàn.
- **Đề xuất sửa**: Đổi [service.rs:4294](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L4294) thành `EXISTS(SELECT 1 FROM payments WHERE provider_tx_id = $1 AND status IN ('succeeded', 'refunded'))` (và không ghi `provider_tx_id` trên nhánh `MarkFailed` để không chiếm ràng buộc `UNIQUE(provider_tx_id)`).

#### P1-2. SePay `check_payment_status` treo toàn bộ luồng hết hạn VietQR khi tổng giao dịch 2 ngày ≥ 500, gây bão N+1 HTTP và bỏ lọt giao dịch đúng sau giao dịch sai
- **Vị trí**: [vietqr.rs:18-20](file:///home/tai/Document/nexusmart/services/payment/src/vietqr.rs#L18-L20), [vietqr.rs:216-233](file:///home/tai/Document/nexusmart/services/payment/src/vietqr.rs#L216-L233), [vietqr.rs:459-498](file:///home/tai/Document/nexusmart/services/payment/src/vietqr.rs#L459-L498), [service.rs:2538-2553](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2538-L2553)
- **Kịch bản**:
  1. Trong `scan_expired_payments`, vòng lặp `for candidate in expired_candidates` (tối đa 50 đơn/lượt) gọi `check_payment_status` từng đơn → ở chế độ `Sepay`, mỗi đơn gọi `fetch_sepay_transactions_paginated(min = hôm qua 00:00 VN, max = None, limit = 100, cap = 5)`. Khi tài khoản ngân hàng có ≥ 500 giao dịch trong 2 ngày (gồm cả giao dịch tiền ra `amount_in = 0` vì `raw_count` đếm toàn mảng `transactions`), trang 5 có `raw_count == 100` → trả `Err(Provider("sepay transaction feed exceeded pagination cap..."))` → `service.rs:2545` bỏ qua (`continue`, defer expiry). Mọi payment VietQR quá hạn không bao giờ hết hạn được bên `payment`, đồng thời worker gửi tới `50 × 5 = 250` request HTTP mỗi 15 giây tới SePay (dễ chạm rate limit / mở circuit breaker).
  2. `vietqr.rs:230-232` dùng `.find(|t| t.provider_order_code == provider_order_code)` trả đúng 1 giao dịch đầu tiên: nếu người mua chuyển sai số tiền (`TX-1`, đã đưa hàng đợi hoặc đã hoàn) rồi chuyển lại đúng số tiền (`TX-2`) trước khi hết hạn, `find` bắt phải `TX-1` → `scan_expired_payments` vẫn chuyển `P1` sang `expired` dù `TX-2` đã thanh toán đủ.
  3. Tại [vietqr.rs:490-497](file:///home/tai/Document/nexusmart/services/payment/src/vietqr.rs#L490-L497), khi tổng giao dịch đúng bằng bội số `cap * limit` (500 hoặc 5000), `raw_count == limit` ở `page == cap` cũng báo lỗi giả `"exceeded pagination cap"`.
  4. **Nghi ngờ (phân trang `page` của SePay)**: `fetch_sepay_transactions_paginated` phân trang bằng `page=1..cap` nhưng tài liệu SePay `/userapi/transactions/list` dùng con trỏ `since_id` (không rõ có hỗ trợ `page`), và vòng lặp không dừng khi số `tx_id` mới thêm vào `seen_tx_ids` bằng `0`. Nếu SePay bỏ qua `page`, mọi ngày ≥ 100 giao dịch sẽ gọi lặp cùng 100 bản ghi suốt 50 trang rồi luôn fail `"exceeded pagination cap"`. *Cách kiểm*: gọi `GET <SEPAY_BASE_URL>/userapi/transactions/list?limit=1&page=1` và `?limit=1&page=2` trên tài khoản SePay thật/sandbox xem `transactions[0].id` có đổi không.
- **Đề xuất sửa**: Tải feed SePay một lần cho cả batch `scan_expired_payments` (dùng con trỏ `since_id` hoặc dừng khi trang không có `provider_tx_id` mới), và truyền toàn bộ giao dịch khớp `provider_order_code` để ưu tiên giao dịch chưa hạch toán & khớp số tiền.

#### P1-3. Lệch mã gRPC giữa `CancelPayment` (`AlreadyExists`) và `services/order` (`FailedPrecondition`) làm kẹt vĩnh viễn `orders.pending_cancel_compensation`
- **Vị trí**: [error.rs:95](file:///home/tai/Document/nexusmart/services/payment/src/error.rs#L95), [service.rs:555-560](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L555-L560) đối chiếu [lifecycle.go:361-376](file:///home/tai/Document/nexusmart/services/order/internal/service/lifecycle.go#L361-L376) và [handlers.go:571-619](file:///home/tai/Document/nexusmart/services/order/internal/service/handlers.go#L571-L619)
- **Kịch bản**: `PaymentService.CancelPayment` trả `PaymentError::Conflict` → gRPC **`codes.AlreadyExists`** khi `payments.status` là `expired`, `failed`, `superseded`, `succeeded` hoặc `refunded`. Trong khi đó `cancelPaymentIgnoreTerminal` bên `services/order` ([lifecycle.go:371](file:///home/tai/Document/nexusmart/services/order/internal/service/lifecycle.go#L371)) chỉ bỏ qua `codes.NotFound || codes.FailedPrecondition`.
  - Khi `scan_expired_payments` (hoặc webhook `FAILED`) vừa chuyển payment VietQR `P1` sang `'expired'`/`'failed'` và người mua bấm huỷ đơn `PENDING` trước khi `order` nhận event `payment.expired`: `order` đặt đơn `CANCELLED` + `pending_cancel_compensation = TRUE` rồi gọi `CancelPayment(P1)` → nhận `codes.AlreadyExists` → coi là lỗi và giữ `pending_cancel_compensation = TRUE`. Consumer `order.cancelled` bên `payment` ([service.rs:3547-3799](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L3547-L3799)) không đổi trạng thái payment đã `expired`/`failed`/`superseded`.
  - Kết quả: watchdog `ScanPendingCancelCompensations` lặp vô hạn mỗi 10 giây trên đơn này mãi mãi; tích đủ 20 đơn sẽ chiếm hết `LIMIT 20` và chặn bù trừ các đơn huỷ mới hơn.
- **Đề xuất sửa**: Thêm `|| code == codes.AlreadyExists` tại [lifecycle.go:371](file:///home/tai/Document/nexusmart/services/order/internal/service/lifecycle.go#L371) (hoặc ở [service.rs:547](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L547) coi payment đã `cancelled`/`expired`/`failed` là đã huỷ idempotent trong `cancel_payment`).

---

### P2 — Lệch spec / biên

#### P2-1. Mục `reconciliation_queue` bị kẹt vĩnh viễn ở trạng thái `pending` nếu `provider_tx_id` được hạch toán sau qua webhook / `run_reconciliation` / `scan_expired_payments`
- **Vị trí**: [service.rs:2163-2164](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2163-L2164), [service.rs:2909-2921](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2909-L2921), [service.rs:4363-4430](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L4363-L4430), [service.rs:4519-4609](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L4519-L4609)
- **Kịch bản**:
  - Nhánh A (từ sửa P2-3(a)): `run_reconciliation` thấy giao dịch SePay `TX-99` thiếu mã đơn (`missing_local`) → chèn `Q1` (`resolution_status = 'pending'`). Vài phút sau webhook trễ cho `TX-99` kèm `payment_id = P1` về → `check_provider_tx_accounting_tx("TX-99")` trả `false` (vì `Q1` mới `pending`) → xác nhận `P1` (`payments.provider_tx_id = 'TX-99'`).
  - Nhánh B: Webhook `TX-99` thiếu mã đơn → chèn `Q1` (`pending`); sau đó `run_reconciliation` (feed SePay đủ `NM...`) hoặc `scan_expired_payments` gọi `confirm_vietqr_payment_tx` hoặc `record_late_payment_refund_tx`.
  - Cả hai nhánh đều không đóng `Q1`. Khi admin mở hàng đợi và gọi `ResolveReconciliationItem` trên `Q1`, kiểm tra ở `service.rs:2163-2164` chạy trước `match input.action` nên **cả 3 thao tác `ASSIGN`, `REFUND`, `SUSPEND` đều trả `409 Conflict` vĩnh viễn** — mục `Q1` kẹt `pending` không thể xử lý.
- **Đề xuất sửa**: Trong `confirm_vietqr_payment_tx` và `record_late_payment_refund_tx` (đều đã giữ `lock_provider_tx_xact`), tự cập nhật các dòng `reconciliation_queue` đang `pending`/`suspended` của cùng `provider_tx_id` sang `'assigned'` / `'refunded'` với `resolved_at = NOW()`.

#### P2-2. Lỗi PayOS `501 UNIMPLEMENTED` từ `ProviderUnsupported` vẫn bị Web BFF xóa thành `503 UNAVAILABLE`
- **Vị trí**: [error.rs:102](file:///home/tai/Document/nexusmart/services/payment/src/error.rs#L102), đối chiếu [response.go:102-103](file:///home/tai/Document/nexusmart/services/gateway/internal/httpx/response.go#L102-L103), [route.ts:179-181](file:///home/tai/Document/nexusmart/web/src/app/api/gw/[...path]/route.ts#L179-L181) và [error-map.ts:201-207, 257-259](file:///home/tai/Document/nexusmart/web/src/lib/server/error-map.ts#L201-L207)
- **Kịch bản**: Gateway `MapGRPCError` đã giữ nguyên thông điệp `"cổng chưa có dữ liệu: ..."` với HTTP `501 UNIMPLEMENTED`. Tuy nhiên khi admin bấm chạy đối soát trên `/admin/doi-soat` qua BFF, `route.ts:179` (`if (upstream.status >= 500)`) chặn mọi mã `>= 500` kể cả `501` và trả về `503 UNAVAILABLE` `"Hệ thống đang tạm bận, vui lòng thử lại sau ít phút."`, làm nhánh map `"cổng chưa có dữ liệu"` tại `error-map.ts:201-207` không bao giờ chạy.
- **Đề xuất sửa**: Map `PaymentError::ProviderUnsupported` tại [error.rs:102](file:///home/tai/Document/nexusmart/services/payment/src/error.rs#L102) sang `tonic::Status::failed_precondition(m.clone())` (Gateway trả `409 FAILED_PRECONDITION` giữ nguyên `msg`, đi xuyên BFF tới `toSafeVietnameseMessage`).

---

### P3 — Nhỏ

#### P3-1. Truy vấn N+1 trong `release_pending_allocation_tx` khi đang giữ khóa độc quyền `ledger_accounts`
- **Vị trí**: [service.rs:4690-4739](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L4690-L4739)
- **Kịch bản**: Sau khi đã khóa `FOR UPDATE` hai dòng `seller_available:<shop>` và `seller_pending:<shop>`, vòng lặp `for sib in unmatured_siblings` chạy thêm 2 truy vấn SQL tuần tự cho mỗi đơn chưa đáo hạn 72h của shop đó, giữ khóa lâu và chặn đồng thời `order.completed`, `create_refund`, `request_seller_payout` của cùng shop khi lưu lượng cao.
- **Đề xuất sửa**: Gộp truy vấn các allocation chưa đáo hạn kèm tổng đã hoàn của từng allocation vào một câu SQL duy nhất (`LEFT JOIN` + `GROUP BY`) trước khi tính `compute_settlement_split`.

#### P3-2. `IncomingWebhookPayload.occurred_at` thiếu alias `transactionDate` / `transactionDateTime` của SePay/PayOS
- **Vị trí**: [webhook_security.rs:122-123](file:///home/tai/Document/nexusmart/services/payment/src/webhook_security.rs#L122-L123), [service.rs:2299](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L2299), [service.rs:3219](file:///home/tai/Document/nexusmart/services/payment/src/service.rs#L3219)
- **Kịch bản**: `IncomingWebhookPayload` đã có alias SePay/PayOS cho `amount_vnd` (`transferAmount`) và `reference` (`content`), nhưng `occurred_at` chưa có alias `transactionDate` / `transaction_date` / `transactionDateTime`; webhook thật từ SePay/PayOS bị rơi về `paid_at = NOW()` nếu đi qua mốc 00:00 VN.
- **Đề xuất sửa**: Thêm `#[serde(default, alias = "transactionDate", alias = "transaction_date", alias = "transactionDateTime")]` cho `occurred_at` và đọc cùng các khóa này khi parse `raw_payload`.

---

## Kiểm tra Deadlock, Lộ PII & Đối chiếu `order` / `gateway`

- **Deadlock**: Đã dựng lại toàn bộ đồ thị khóa sau khi thêm `pg_advisory_xact_lock(hashtextextended(provider_tx_id, 7071))`. Cả 4 đường (`process_verified_webhook`, `resolve_reconciliation_item`, `scan_expired_payments`, `execute_reconciliation_run`) đều lấy tối đa 1 advisory lock ngay đầu transaction (trước mọi `FOR UPDATE`), tiếp theo là `reconciliation_queue` (`ORDER BY id ASC`) → `payments` nhóm (`ORDER BY id ASC`) → `payment_shop_allocations` (`ORDER BY id ASC`) → `ledger_accounts` (`seller_available` rồi `seller_pending`, `release_escrow` pre-lock theo `shop_id` tăng dần). **Không thấy chu trình deadlock AB-BA.**
- **Lộ PII / Secret**: Đã soát mọi `info!`/`warn!`/`error!`, `Debug` của `Config`/`RefundRecord`/`SubmitRefundBankAccountInput`, phản hồi gRPC buyer vs admin, outbox `payment.refund_settled`, và middleware [audit.go:384-415](file:///home/tai/Document/nexusmart/services/gateway/internal/middleware/audit.go#L384-L415) (`bank_account_number` bị redaction thành `[REDACTED]`, chỉ giữ `bank_account_masked`). **Không thấy lỗi lộ PII/secret.**
- **Đối chiếu `services/order` & `services/gateway`**: Khớp chữ ký RPC/event và RBAC trên tất cả các đường (`order` không còn gọi `ReleaseEscrow`, dùng `order-return-refund:<order_id>`, gateway kiểm chủ đơn trước khi proxy SSE và `ListRefunds`), ngoại trừ 2 điểm đã nêu tại **P1-3** (`lifecycle.go:371`) và **P2-2** (BFF `501` -> `503`).

---

## Các file trong `services/payment/**` đã đọc và không thấy lỗi

- [0005_reconciliation_fixes.sql](file:///home/tai/Document/nexusmart/services/payment/migrations/0005_reconciliation_fixes.sql), [0004_reconciliation_queue_unique_tx.sql](file:///home/tai/Document/nexusmart/services/payment/migrations/0004_reconciliation_queue_unique_tx.sql), [0003_refund_settlement_and_reconciliation.sql](file:///home/tai/Document/nexusmart/services/payment/migrations/0003_refund_settlement_and_reconciliation.sql), [0002_payment_money_model.sql](file:///home/tai/Document/nexusmart/services/payment/migrations/0002_payment_money_model.sql), [0001_init.sql](file:///home/tai/Document/nexusmart/services/payment/migrations/0001_init.sql)
- [domain.rs](file:///home/tai/Document/nexusmart/services/payment/src/domain.rs)
- [grpc.rs](file:///home/tai/Document/nexusmart/services/payment/src/grpc.rs)
- [auth.rs](file:///home/tai/Document/nexusmart/services/payment/src/auth.rs)
- [http.rs](file:///home/tai/Document/nexusmart/services/payment/src/http.rs)
- [workers.rs](file:///home/tai/Document/nexusmart/services/payment/src/workers.rs)
- [config.rs](file:///home/tai/Document/nexusmart/services/payment/src/config.rs)
- [main.rs](file:///home/tai/Document/nexusmart/services/payment/src/main.rs), [telemetry.rs](file:///home/tai/Document/nexusmart/services/payment/src/telemetry.rs), [metrics.rs](file:///home/tai/Document/nexusmart/services/payment/src/metrics.rs), [lib.rs](file:///home/tai/Document/nexusmart/services/payment/src/lib.rs), [Cargo.toml](file:///home/tai/Document/nexusmart/services/payment/Cargo.toml), [build.rs](file:///home/tai/Document/nexusmart/services/payment/build.rs), [Dockerfile](file:///home/tai/Document/nexusmart/services/payment/Dockerfile)
