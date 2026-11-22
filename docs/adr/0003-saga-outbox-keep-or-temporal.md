# ADR-0003: Saga và Outbox trong `services/order` — giữ tự viết hay chuyển sang Temporal

- **Trạng thái**: Đề xuất
- **Ngày**: 2026-11-22

## Bối cảnh

`services/order` (6.191 dòng code non-generated trong `internal/`) tự cài Saga orchestration cho checkout đa gian hàng, máy trạng thái đơn và Transactional Outbox trên PostgreSQL (`order_db`) + NATS JetStream (`NEXUSMART`, `NEXUSMART_DLQ`):

- `services/order/internal/service/checkout.go` (1.248 dòng): `CreateCheckout` đảm bảo idempotency theo `(buyer_id, idempotency_key)` trong `checkout_groups` + `keyedLocker`, chạy tuần tự: `ReserveStock` -> `ReservePromotion` -> `PersistCheckoutBatch` (ghi đơn + timeline + `outbox_events` trong 1 TX) -> `CreatePayment` -> `ClearItems` (best-effort), kèm bù trừ đồng bộ khi bước sau lỗi.
- `services/order/internal/service/handlers.go` (340 dòng): `HandlePaymentResult` xác nhận tồn (`ConfirmStock`) hoặc bù trừ (`ReleaseStock` + `ReleasePromotion`) bất đồng bộ khi nhận `payment.*`; `RunExpiryWatchdog` / `ScanExpiredPendingOrders` (UC-PAY06) quét đơn `PENDING` quá hạn và gọi `Payment.GetPayment` trước khi huỷ.
- `services/order/internal/service/lifecycle.go` (767 dòng) & `internal/statemachine/statemachine.go` (143 dòng): 9 trạng thái đơn, chuyển trạng thái có kiểm tra hợp lệ, huỷ/hoàn hàng, `BuyerConfirmReceived` phát `order.completed` + gọi `ReleaseEscrow`.
- `services/order/internal/events/outbox.go` (165 dòng) & `internal/store/postgres.go` (1.115 dòng): `OutboxRelay` poll 250ms + `triggerCh`, batch 50 dùng `FOR UPDATE SKIP LOCKED`, gắn `Nats-Msg-Id` và `traceparent`.
- `services/order/internal/events/consumer.go` (305 dòng): consumer idempotent theo `processed_events`, tối đa 5 lần thử rồi ghi `dead_letter_events` + phát `NEXUSMART_DLQ`.
- Lưu ý liên dịch vụ: `services/payment/src/workers.rs` (444 dòng, Rust) cũng tự chạy transactional outbox trên `payment_db` để phát `payment.*` lên NATS.
- **Kiểm thử phủ**: unit test tại `statemachine_test.go`, `clients_test.go`, `events_test.go`, `service_test.go` (tổng 28 unit test, riêng `service_test.go` 1.670 dòng / 21 test: COD đa gian hàng, idempotency tuần tự/đồng thời, bù trừ khi promotion/payment lỗi, `TestExpiryWatchdog_UC_PAY06`, huỷ đơn đã trả tiền, hoàn hàng); E2E tại `tests/e2e/02_cod_checkout_test.go`, `03_vietqr_checkout_test.go`, `04_order_idempotency_test.go`, `05_webhook_security_test.go` (chỉ chuyển `CONFIRMED` đúng 1 lần sau 10x webhook), `06_flash_sale_test.go`, `07_saga_compensation_test.go` (hoàn 100% stock/voucher/xu), `08_ledger_test.go`. Chưa có E2E riêng cho crash pod giữa bước `ReserveStock` và `PersistCheckoutBatch` hay timeout thật 15 phút.
- **Điểm yếu đã nhận diện**: nếu pod `order` crash sau `ReserveStock`/`ReservePromotion` nhưng trước `PersistCheckoutBatch` (hoặc `compensate*` hỏng sau 3 lần retry), `checkout_groups` có thể kẹt `IN_PROGRESS` mà chưa có worker tự dọn và tự nhả tài nguyên giữ chỗ.

## Ứng viên

- **Giữ saga + outbox tự viết trong `services/order`**: tiếp tục dùng PostgreSQL + NATS JetStream, bổ sung sweep dọn `checkout_groups` kẹt `IN_PROGRESS` khi cần.
- **Temporal** (`temporalio/temporal`): **MIT** (cả server, Go SDK và Rust SDK); bảo trì rất tích cực.
  - Server `temporalio/temporal`: bản mới nhất `v1.32.2` (2026-11-02; nhánh `v1.32.0` phát hành 2026-09-11).
  - Go SDK `temporalio/sdk-go` (`go.temporal.io/sdk`): bản mới nhất `v1.51.0` (2026-11-10), chín muồi cho production.
  - Rust SDK `temporalio/sdk-rust` (crate `temporalio-sdk` + `sdk-core`): đã **GA**, bản mới nhất `v1.2.0` (2026-11-09/10).

## Tiêu chí

| Tiêu chí | Giữ saga + outbox tự viết | Chuyển sang Temporal |
|---|---|---|
| Độ phức tạp vận hành | Thấp (tái dụng Postgres + NATS sẵn có) | Cao (cụm server 4 role, DB persistence + visibility riêng, UI, versioning workflow) |
| Rủi ro chuyển đổi | Thấp | Cao (viết lại `CreateCheckout`/handlers thành deterministic workflow + activities, vẫn cần NATS/outbox cho pub-sub sang `notification`/`payment`/`promotion`) |
| Khớp gRPC + Go/Rust | Rất tốt (gRPC trực tiếp, không ràng buộc determinism) | Rất tốt (SDK Go `v1.51.0` và Rust `v1.2.0` GA đều chính thức, MIT) |
| Chi phí vận hành | Rất thấp | Trung bình–cao (tự host tốn cụm riêng; dùng Temporal Cloud tốn phí theo action) |
| Bảo mật | Theo chuẩn gRPC nội bộ hiện tại | Cần mTLS worker-server và payload codec nếu history chứa PII/thông tin thanh toán |

## Quyết định

- **Đề xuất của coder**: **Giữ saga và transactional outbox tự viết trong `services/order` ở giai đoạn hiện tại**. Luồng checkout hiện tại ngắn (4–5 bước RPC), cần phản hồi đồng bộ nhanh trả `qr_url`/`orders`, đã chạy xanh 8/8 E2E và 28 unit test trong `order`. Thay thế bằng Temporal không xoá được NATS JetStream hay outbox của `services/payment`, trong khi kéo thêm một hệ thống phân tán nặng. Để bịt lỗ hổng crash giữa `ReserveStock` và `PersistCheckoutBatch`, chỉ cần thêm một sweep định kỳ dọn `checkout_groups` ở trạng thái `IN_PROGRESS` quá hạn (cùng mẫu với `RunExpiryWatchdog`).
- **Điều kiện kích hoạt để chuyển sang Temporal**: khi phát sinh nhiều workflow dài ngày/nhiều nhánh chờ tác nhân ngoài (ví dụ: tự động hoàn tất 7 ngày, quy trình khiếu nại tranh chấp `UC-A05` nhiều vòng có duyệt tay, đối soát COD đa bước, hoặc saga chạm >5 service có bù trừ lồng nhau), và nhu cầu audit/replay từng bước trên UI vượt chi phí vận hành cụm Temporal.
- **Phụ thuộc**: ADR nơi triển khai (chưa chốt) — tự host Temporal HA trên Kubernetes đòi hỏi công sức vận hành lớn; nếu lên cloud có thể cân nhắc Temporal Cloud khi đạt ngưỡng kích hoạt.
- **Chủ dự án quyết**: (trống)

## Hệ quả

- **Giữ saga + outbox tự viết (đề xuất)**: giữ kiến trúc gọn trên Postgres + NATS, độ trễ checkout thấp, không thêm hạ tầng mới; đánh đổi phải tự bảo trì logic retry/watchdog/sweep và thiếu giao diện trực quan để xem lịch sử từng bước saga hay replay một đơn lỗi.
- **Chuyển sang Temporal**: có durable execution qua mọi điểm crash, timer dài ngày chuẩn xác (15 phút, 7 ngày), retry/backoff và lịch sử replay rõ ràng trên UI, hỗ trợ tốt cả Go lẫn Rust; đánh đổi thêm cụm vận hành nặng, đòi hỏi kỷ luật code workflow deterministic + versioning, và vẫn phải duy trì cơ chế phát sự kiện (outbox/NATS) cho các consumer pub-sub.

## Ngày kiểm tra trạng thái ứng viên

- **Ngày kiểm tra**: 2026-11-22
- **Nguồn**:
  - Temporal Server: https://github.com/temporalio/temporal, https://github.com/temporalio/temporal/releases
  - Temporal Go SDK: https://github.com/temporalio/sdk-go, https://github.com/temporalio/sdk-go/releases
  - Temporal Rust SDK: https://github.com/temporalio/sdk-rust, https://github.com/temporalio/sdk-rust/releases, https://crates.io/crates/temporalio-sdk
