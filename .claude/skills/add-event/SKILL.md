---
name: add-event
description: Thêm sự kiện bất đồng bộ trên NATS JetStream (stream NEXUSMART, envelope JSON chuẩn, transactional outbox, consumer idempotent với processed_events, retry + NEXUSMART_DLQ và docs/contracts.md §5). Dùng khi thêm domain event mới giữa các service.
disable-model-invocation: true
---

# Thêm sự kiện NATS JetStream

Đầu vào (`$ARGUMENTS`): `<prefix>.<action>` (ví dụ: `order.returned`, `payment.settled`, `shipment.cancelled`).

## 1. Thiết kế subject và payload

- [ ] Đặt tên subject theo quy ước `<domain>.<past_tense>` trên stream `NEXUSMART` (`docs/contracts.md` §5). Các prefix đang bật: `user.>`, `order.>`, `payment.>`, `shipment.>`.
- [ ] Dùng đúng envelope JSON chuẩn:
  ```json
  {"event_id":"<uuid>","event_type":"<subject>","trace_id":"<trace_id>","occurred_at":"<RFC3339-UTC>","payload":{}}
  ```
- [ ] Mọi trường tiền trong `payload` là số nguyên VND (`*_vnd`, `int64`/`i64`); định danh shop là `shop_id`.
- [ ] Khai báo hằng subject và struct payload ở service phát và các service nhận (mẫu `services/order/internal/events/types.go`, `services/shipping/internal/events/envelope.go`).

## 2. Nếu thêm prefix mới chưa có trên stream `NEXUSMART`

Hiện cấu hình stream `NEXUSMART` được khởi tạo độc lập ở 6 service. Khi thêm prefix mới (ví dụ `review.>`), phải cập nhật đồng bộ danh sách `Subjects` ở cả 6 nơi:

- [ ] `services/user/internal/events/publisher.go` (`ensureStream`)
- [ ] `services/order/internal/events/outbox.go` (`EnsureStreams`)
- [ ] `services/promotion/internal/events/consumer.go` (`EnsureStreams`)
- [ ] `services/shipping/internal/events/envelope.go` (`EnsureStreams`)
- [ ] `services/notification/internal/consumer/consumer.go` (`EnsureStreams`)
- [ ] `services/payment/src/workers.rs` (`connect_jetstream`)
- [ ] Lưu ý: các hàm `ensureSingleStream` hiện trả sớm nếu `js.StreamInfo` tìm thấy stream đã tồn tại; nếu cập nhật trên NATS đang có dữ liệu cần thêm bước `js.UpdateStream` (hoặc tạo lại môi trường dev).

## 3. Phía phát — Transactional Outbox

- [ ] Ghi bản ghi vào bảng `outbox_events` trong **cùng PostgreSQL transaction** với cập nhật nghiệp vụ (mẫu `services/order/migrations/0001_init.sql`, `services/order/internal/events/outbox.go`, `services/shipping/internal/events/outbox.go`, `services/payment/src/workers.rs`).
- [ ] Relay worker đọc batch (`FOR UPDATE SKIP LOCKED` hoặc `WHERE published_at IS NULL`), dựng `Envelope`, publish lên JetStream với:
  - Header `Nats-Msg-Id` = `event_id` (khớp cửa sổ dedup `2m` của stream `NEXUSMART`).
  - Header `traceparent` (tiêm qua `telemetry.InjectTraceHeaders` bên Go hoặc `format_traceparent` bên Rust) và `trace_id` trong body envelope.
- [ ] Đánh dấu `published_at` / `status = 'sent'` sau khi JetStream trả ack; lỗi thì tăng `attempts`/`retry_count` để thử lại.
- [ ] Tránh publish thẳng không qua outbox cho event nghiệp vụ quan trọng (`services/user/internal/events/publisher.go` là ngoại lệ cũ).

## 4. Phía nhận — Consumer idempotent và DLQ

- [ ] Khử trùng trong **cùng DB transaction** với thay đổi trạng thái nghiệp vụ:
  - Go: bảng `processed_events(event_id PRIMARY KEY, ...)` — chèn `ON CONFLICT DO NOTHING`, nếu `rows_affected == 0` thì bỏ qua và `Ack` (mẫu `ProcessEventTx` trong `services/notification/internal/store/store.go`).
  - Rust (`payment`): bảng `processed_nats_events(event_id PRIMARY KEY, ...)` trong cùng transaction với cập nhật nghiệp vụ (mẫu `handle_order_event` trong `services/payment/src/workers.rs`).
- [ ] Trích trace context từ header `traceparent` (`telemetry.ExtractTraceHeaders`) trước khi xử lý.
- [ ] Chính sách retry & DLQ (mẫu `services/notification/internal/consumer/consumer.go`, `services/order/internal/events/consumer.go`, `services/payment/src/workers.rs`):
  - Lỗi tạm thời: `NakWithDelay` với số lần thử tối đa `5` (`maxAttempts = 5`, `MaxDeliver = 6` bên Go; `MAX_NATS_DELIVERIES = 5` bên Rust).
  - JSON/envelope hỏng hoặc đã chạm `maxAttempts`: ghi bảng `dead_letter_events` (ở các service có bảng này: `notification`, `order`, `shipping`), publish sang subject `dlq.<original_subject>` trên stream `NEXUSMART_DLQ`, rồi `Ack`.

## 5. Unit test và cập nhật tài liệu

- [ ] Test outbox relay: kiểm tra `Nats-Msg-Id`, `traceparent`, JSON envelope, cập nhật trạng thái đã gửi (mẫu `services/order/internal/events/events_test.go`).
- [ ] Test consumer: xử lý thành công, gửi lặp cùng `event_id` không tạo tác dụng phụ lặp, lỗi tạm thời gọi `NakWithDelay`, lỗi quá `maxAttempts` hoặc payload lỗi đẩy vào `dlq.<subject>` + `dead_letter_events` và `Ack` (mẫu `services/notification/internal/consumer/consumer_test.go`, `services/promotion/internal/events/consumer_test.go`, `services/payment/src/workers.rs`).
- [ ] Cập nhật `docs/contracts.md` §5: thêm event mới vào bảng "Event Subjects & Payload Schemas" (và danh sách `Subjects` nếu thêm prefix mới).

## Gate

```bash
make lint
make test
```

Kiểm tra riêng gói events/consumer của các service thay đổi (ví dụ):

```bash
cd services/order && go test -race -v ./internal/events/...
cd services/notification && go test -race -v ./internal/consumer/...
cd services/payment && cargo test nats
```
