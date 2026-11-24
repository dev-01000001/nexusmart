---
name: add-event
description: Thêm sự kiện bất đồng bộ trên NATS JetStream (stream NEXUSMART, envelope JSON chuẩn, transactional outbox, consumer idempotent với processed_events, retry + NEXUSMART_DLQ và docs/contracts.md §5). Dùng khi thêm domain event mới giữa các service.
disable-model-invocation: true
---

# Thêm sự kiện NATS JetStream

Đầu vào (`$ARGUMENTS`): `<prefix>.<action>` (ví dụ: `order.returned`, `payment.settled`, `shipment.cancelled`).

## 1. Thiết kế subject và payload

- [ ] Đặt tên subject theo quy ước `<domain>.<past_tense>` trên stream `NEXUSMART` (`docs/contracts.md` §5). Các prefix đang bật: `user.>`, `order.>`, `payment.>`, `shipment.>`, `product.>`, `promotion.>`.
- [ ] Dùng đúng envelope JSON chuẩn:
  ```json
  {"event_id":"<uuid>","event_type":"<subject>","trace_id":"<trace_id>","occurred_at":"<RFC3339-UTC>","payload":{}}
  ```
- [ ] Mọi trường tiền trong `payload` là số nguyên VND (`*_vnd`, `int64`/`i64`); định danh shop là `shop_id`.
- [ ] Khai báo hằng subject và struct payload ở service phát và các service nhận (mẫu `services/order/internal/events/types.go`, `services/shipping/internal/events/envelope.go`).

## 2. Nếu thêm prefix mới chưa có trên stream `NEXUSMART`

Các service Go dùng chung `pkg/common/jetstream.EnsureStreams` (union-merge subject mới và gọi `UpdateStream` nếu thiếu, không trả sớm khi stream đã tồn tại); `payment` (Rust) dùng `CANONICAL_PRIMARY_SUBJECTS` + `ensure_stream_subjects` trong `services/payment/src/workers.rs`. Khi thêm prefix mới (ví dụ `review.>`), cập nhật đồng bộ ở cả 2 nơi:

- [ ] `pkg/common/jetstream/stream.go` (`CanonicalPrimarySubjects`, dùng chung bởi `user`, `product`, `promotion`, `order`, `shipping`, `notification`)
- [ ] `services/payment/src/workers.rs` (`CANONICAL_PRIMARY_SUBJECTS`)

## 3. Phía phát — Transactional Outbox (D9 / D9-bis)

- [ ] Ghi bản ghi vào bảng `outbox_events` trong **cùng PostgreSQL transaction** với cập nhật nghiệp vụ (mẫu `services/order/migrations/0014_outbox_lease.sql`, `services/order/internal/store/postgres.go`, `services/product/internal/store/pg_store.go`, `services/shipping/internal/store/store.go`, `services/payment/src/workers.rs`).
- [ ] **`aggregate_id` và khóa dòng**: PostgreSQL cấp `seq` (`BIGSERIAL`) lúc `INSERT` chứ không phải lúc `COMMIT`. Do đó transaction ghi **phải khóa dòng thực thể đích TRƯỚC khi chèn `outbox_events`** và đặt `aggregate_id` bằng đúng ID thực thể đã khóa (`order`: `orders.id`, `shipping`: `shipment_id`, `payment`: `order_group_id` sau `lock_payment_group_tx`, `product`: `products.id` sau `GetProductByIDForUpdate`). Migration thêm `seq` phải backfill `seq` cho các dòng chưa phát theo `(created_at ASC, id ASC)`.
- [ ] **Claim 2 tầng & publish ngoài transaction**: tuyệt đối không giữ DB transaction mở khi gọi NATS. Relay lấy `leaseDeadline = now + lease` (`30s`) trước khi claim, chạy CTE 2 tầng (`locked` lọc `NOT EXISTS` event trước cùng aggregate đang có lease/backoff + `ORDER BY seq ASC LIMIT 50 FOR UPDATE SKIP LOCKED`, rồi `eligible` lọc event trước cùng aggregate không nằm trong `locked`), commit ngay rồi mới publish ra JetStream với:
  - Header `Nats-Msg-Id` = `event_id` (khớp cửa sổ dedup `2m` của stream `NEXUSMART`, gửi ít nhất một lần).
  - Header `traceparent` (tiêm qua `telemetry.InjectTraceHeaders` bên Go hoặc `inject_current_span_to_nats` bên Rust) và `trace_id` trong body envelope.
- [ ] **Kiểm ngân sách lease & chốt `claim_token`**: trước mỗi lần publish kiểm còn đủ `publish_timeout` (`5s`); đánh dấu đã gửi theo `claim_token`. Nếu hết ngân sách lease, mất lease (`0` dòng), hoặc lỗi DB mark → nhả phần còn lại bằng 1 câu `UPDATE ... WHERE id = ANY(...) AND claim_token = ...` và dừng lô.
- [ ] **Xử lý lỗi (dùng chung `pkg/common/jetstream/outbox.go` bên Go)**:
  - Lỗi không phải hạ tầng: đặt backoff theo dòng `claimed_until = NOW() + min(2^attempts, 60)s`, nhả các event sau cùng aggregate trong lô không tăng `attempts`. **Không** đánh dấu `failed` hay dead-letter dòng outbox (sẽ phá thứ tự aggregate); dòng hỏng giữ lại thử tiếp và log `ERROR` (kèm `event_id`, `event_type`, không payload) từ lần thứ 10 (`attempts >= 10`).
  - Lỗi hạ tầng (`jetstream.IsInfraError`, gồm `*nats.APIError` mã `>= 500`): dừng lô ngay, nhả claim các dòng còn lại không tăng `attempts` (`payment` cũng không tăng `retry_count` trên dòng lỗi và nối lại NATS), vòng relay lùi `250ms -> 5s` (bỏ qua wake trigger trong lúc lùi).
- [ ] **Tắt tiến trình sạch**: đợi outbox relay (và consumer) chạy xong in-flight rồi mới đóng NATS và DB pool.
- [ ] Tránh publish thẳng không qua outbox cho event nghiệp vụ quan trọng (`services/user/internal/events/publisher.go` là ngoại lệ cũ).

## 4. Phía nhận — Consumer idempotent, DLQ và tắt sạch

- [ ] Khử trùng trong **cùng DB transaction** với thay đổi trạng thái nghiệp vụ:
  - Go: bảng `processed_events(event_id PRIMARY KEY, ...)` — chèn `ON CONFLICT DO NOTHING`, nếu `rows_affected == 0` thì bỏ qua và `Ack` (mẫu `ProcessEventTx` trong `services/notification/internal/store/store.go`).
  - Rust (`payment`): bảng `processed_nats_events(event_id PRIMARY KEY, ...)` trong cùng transaction với cập nhật nghiệp vụ (mẫu `handle_order_event` trong `services/payment/src/workers.rs`).
- [ ] Trích trace context từ header `traceparent` (`telemetry.ExtractTraceHeaders`) trước khi xử lý.
- [ ] Chính sách retry, DLQ và tắt máy (mẫu `services/order/internal/events/consumer.go`, `services/notification/internal/consumer/consumer.go`, `services/payment/src/workers.rs`):
  - Lỗi tạm thời: `NakWithDelay` với số lần thử tối đa `5` (`maxAttempts = 5`, `MaxDeliver = 6` bên Go; `MAX_NATS_DELIVERIES = 5` bên Rust).
  - JSON/envelope hỏng hoặc đã chạm `maxAttempts`: ghi bảng `dead_letter_events` (ở các service có bảng này: `notification`, `order`, `shipping`) và/hoặc publish sang `dlq.<original_subject>` trên `NEXUSMART_DLQ`. **Chỉ được `Ack` sau khi lưu thành công vào `dead_letter_events` hoặc publish thành công lên `NEXUSMART_DLQ`** (nếu cả hai cùng lỗi thì `Nak` để không làm rơi message).
  - **Lỗi do đang tắt máy không vào DLQ**: khi context bị huỷ vì graceful shutdown (`context.Canceled` / `stopping`), không đẩy message vào DLQ mà để JetStream phát lại sau (đã áp dụng ở `order`; các service khác sẽ đồng bộ ở đợt 3b).
  - **Không `Drain()` / `Unsubscribe()` durable consumer do tiến trình tạo**: trong `nats.go`, `QueueSubscribe(..., nats.Durable(...))` gắn cờ tự tạo consumer khiến `sub.Drain()` / `sub.Unsubscribe()` **xóa luôn durable consumer trên server**, làm hỏng replica khác và replay toàn bộ stream khi khởi động lại. Khi dừng consumer, chỉ đặt cờ `stopping = true` chặn callback mới, đợi handler đang chạy xong (`wg.Wait()`), rồi để `nc.Close()` ngắt kết nối sạch trong khi vẫn giữ nguyên durable consumer.

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
