# Review độc lập đợt 2b — `services/payment` & `pkg/common`

> Phạm vi rà soát (chỉ đọc trên `git diff d88221d` và file mới): `services/payment` (cấu hình phí theo thời gian, sổ giao dịch người bán, quyết toán/hoàn/giải ngân với mô hình tiền `R` gồm `P_shop`/`S_shop` và `C_total` gồm `payment_fee`, stream NATS, span/log, migration `0007`) và `pkg/common` (`jetstream`, `logger` gRPC, `telemetry`, `auth` allowlist).

---

## Phát hiện theo mức độ nghiêm trọng

### P0 — Tiền / Vượt quyền / Mất dữ liệu

1. **[pkg/common/auth/auth.go:36-140](pkg/common/auth/auth.go#L36-L140) & [pkg/common/auth/auth.go:378-388](pkg/common/auth/auth.go#L378-L388)** — **`CartService` bị bỏ sót khỏi `internalRPCs`, cho phép gọi ẩn danh đọc và xoá sạch giỏ hàng của bất kỳ user nào (IDOR)**
   - **Kịch bản cụ thể**: Đợt 2b mở rộng `internalRPCs` để khoá gọi ẩn danh cho 6 Go service (`UserService`, `ProductService`, `PromotionService`, `OrderService`, `ShippingService`, `NotificationService`) nhưng bỏ sót hoàn toàn 5 RPC của `CartService` (`GetCart`, `AddItem`, `UpdateItemQty`, `RemoveItem`, `ClearItems` — đồng thời [docs/contracts.md:379-383](docs/contracts.md#L379-L383) không khai báo `CartService` là public). Khi một caller trong mạng nội bộ gọi gRPC không có `authorization` tới `CartService.GetCart{user_id: "<victim>"}` hoặc `CartService.ClearItems{user_id: "<victim>", sku_ids: []}` → `GRPCUnaryServerInterceptor` thấy `internal == false` nên cho qua với `claims == nil` → [services/cart/internal/server/server.go:60](services/cart/internal/server/server.go#L60), [230](services/cart/internal/server/server.go#L230), [423-434](services/cart/internal/server/server.go#L423-L434) gọi `resolveUserID(ctx, req.GetUserId(), false)` tin luôn `req.UserId` và trả toàn bộ giỏ hoặc xoá sạch giỏ nạn nhân. (Cả `gateway` lẫn `order` qua [services/order/internal/clients/clients.go:96](services/order/internal/clients/clients.go#L96) và [checkout.go:541](services/order/internal/service/checkout.go#L541), [761](services/order/internal/service/checkout.go#L761) đều forward sẵn end-user JWT khi gọi `CartService`.)
   - **Mức**: **P0**
   - **Đề xuất sửa**: Thêm 5 RPC `/nexusmart.cart.v1.CartService/{GetCart,AddItem,UpdateItemQty,RemoveItem,ClearItems}: false` vào `internalRPCs` ở [pkg/common/auth/auth.go:140](pkg/common/auth/auth.go#L140) và bổ sung kiểm thử vào [pkg/common/auth/grpc_trust_test.go](pkg/common/auth/grpc_trust_test.go).

---

### P1 — Hỏng luồng chính

- Không phát hiện lỗi P1 trong phạm vi `services/payment` và `pkg/common`.

---

### P2 — Lệch spec / Biên

2. **[services/payment/src/workers.rs:196-215](services/payment/src/workers.rs#L196-L215) (cùng [257-272](services/payment/src/workers.rs#L257-L272), [361-370](services/payment/src/workers.rs#L361-L370))** — **`connect_jetstream` nuốt lỗi `ensure_stream_subjects`, làm bỏ lỡ hợp subject hoặc đốt sạch retry của outbox**
   - **Kịch bản cụ thể**: Khi `ensure_stream_subjects` cho `NEXUSMART` (dòng 201-203) hoặc `NEXUSMART_DLQ` (dòng 209-211) lỗi (ví dụ NATS đang election / lỗi tạm lúc khởi động), `connect_jetstream` chỉ log `warn!` rồi vẫn trả `Some(js)` (dòng 214).
     - Nếu `NEXUSMART` cũ mới chỉ có 4 subject: `run_outbox_relay` cache `js_opt = Some(js)` và `run_nats_order_consumer` vẫn `get_stream` thành công → không bao giờ thử lại việc hợp thêm `product.>`/`promotion.>`.
     - Nếu `NEXUSMART` chưa được tạo: `relay_outbox_batch` publish lỗi, bắt lỗi trong `mark_outbox_retry` rồi trả `Ok(())` (dòng 361-370), nên `run_outbox_relay` không reset `js_opt = None` (dòng 269-272) → các bản ghi `outbox_events` đốt hết `MAX_OUTBOX_RETRIES = 10` và chuyển vĩnh viễn sang `status = 'failed'`.
   - **Mức**: **P2**
   - **Đề xuất sửa**: Cho `connect_jetstream` trả `None` ngay khi bất kỳ lời gọi `ensure_stream_subjects` nào trả `Err` để worker tự thử lại ở chu kỳ kế tiếp.

3. **[services/payment/src/http.rs:63-90](services/payment/src/http.rs#L63-L90) & [201-210](services/payment/src/http.rs#L201-L210)** — **`process_webhook_http` bỏ qua `trace_id` của `http_trace_middleware`, làm đứt dây trace webhook → outbox → NATS**
   - **Kịch bản cụ thể**: Khi PayOS/SePay/mock gọi `POST /webhooks/payment` hoặc `/webhooks/vietqr` không kèm `traceparent` (hoặc kèm `x-trace-id` không phải 32-hex), `http_trace_middleware` tạo OTel server span với `trace_id = T1`, ghi log `http request` mang `T1` và gắn response header `X-Trace-Id: T1`. Bên trong `process_webhook_http` (dòng 207-210) lại gọi `extract_or_generate_trace_id(...)` độc lập → sinh UUID ngẫu nhiên mới `T2 != T1`, ghi `T2` vào log cảnh báo và `outbox_events.trace_id` → sự kiện `payment.succeeded` trên NATS mang `T2`, không tương quan được với HTTP span / `X-Trace-Id` (`T1`).
   - **Mức**: **P2**
   - **Đề xuất sửa**: Lưu `trace_id` (hoặc lấy từ active OTel span) vào Axum `Request::extensions` trong `http_trace_middleware` và dùng lại trong `process_webhook_http` thay vì gọi `extract_or_generate_trace_id`.

4. **[services/payment/src/grpc.rs:771-811](services/payment/src/grpc.rs#L771-L811)** — **`GetFeeConfig` cho phép `buyer`/`seller` truyền `at_time` tương lai để đọc lịch phí chưa hiệu lực và lộ `created_by`/`note` nội bộ**
   - **Kịch bản cụ thể**: `GetFeeConfig` chặn `include_history = true` với non-admin (`grpc.rs:772-774`) để không lộ lịch sử/lịch hẹn phí, nhưng không giới hạn `at_time` và không lọc trường nội bộ cho non-admin. Bất kỳ JWT `buyer`/`seller` nào gọi gRPC `GetFeeConfig(include_history=false, at_time="2099-01-01T00:00:00Z")` vẫn đọc được toàn bộ mức phí đã lên lịch tương lai cùng `created_by` (admin user ID) và `note` vận hành nội bộ.
   - **Mức**: **P2**
   - **Đề xuất sửa**: Với caller không phải admin và không phải service token, từ chối `at_time` khác `None` (hoặc ép `at_time = None`) và xoá `created_by`/`note` trước khi trả `active_configs`.

5. **[pkg/common/jetstream/stream.go:79](pkg/common/jetstream/stream.go#L79), [101](pkg/common/jetstream/stream.go#L101), [122-123](pkg/common/jetstream/stream.go#L122-L123), [167-173](pkg/common/jetstream/stream.go#L167-L173)** — **`EnsureStream` có race last-writer-wins khi hai caller dùng `extraPrimarySubjects` khác nhau**
   - **Kịch bản cụ thể**: Docstring cam kết *"Concurrent startup conflicts are resolved safely via read-merge-update retries"* và API nhận `extraPrimarySubjects ...string`. Tuy nhiên `UpdateStream` của NATS JetStream không kiểm tra version/CAS: nếu service A thêm `foo.>` và service B thêm `bar.>` đồng thời lên stream đang có 4 subject cũ, cả hai đọc cùng snapshot, A update `[4 cũ, foo.>]` thành công rồi B update `[4 cũ, bar.>]` cũng thành công, làm mất âm thầm `foo.>` mà vòng retry không chạy. (Hiện tại chưa kích hoạt vì 7/7 Go service đều truyền cùng 6 subject chuẩn và không dùng extra.)
   - **Mức**: **P2**
   - **Đề xuất sửa**: Bỏ tham số variadic `extraPrimarySubjects`/`extraSubjects` (chỉ dùng danh sách canonical thống nhất) hoặc đọc lại `StreamInfo` sau `UpdateStream` để kiểm tra đủ `desired.Subjects` trước khi return `nil`.

---

### P3 — Nhỏ

6. **[services/payment/src/metrics.rs:37-44](services/payment/src/metrics.rs#L37-L44) & [207-212](services/payment/src/metrics.rs#L207-L212)** — **`http_errors_total` ghi HELP là 5xx nhưng vẫn đếm cả 4xx**
   - **Kịch bản cụ thể**: Đợt 2b đổi mô tả thành `"Total number of HTTP 5xx error responses."` cho khớp [pkg/common/metrics/metrics.go:45](pkg/common/metrics/metrics.go#L45), nhưng `red_metrics_middleware` dòng 207 vẫn kiểm `status.is_client_error() || status.is_server_error()`. Mọi request webhook sai chữ ký (`401`) hay `400`/`404` đều làm tăng counter 5xx.
   - **Mức**: **P3**
   - **Đề xuất sửa**: Đổi [services/payment/src/metrics.rs:207](services/payment/src/metrics.rs#L207) thành `if status.is_server_error()`.

7. **[services/payment/src/workers.rs:303-328](services/payment/src/workers.rs#L303-L328)** — **`EventEnvelope.trace_id` để rỗng nếu `outbox_events.trace_id == ""` dù NATS header đã sinh `active_trace_id`**
   - **Kịch bản cụ thể**: `envelope` được dựng ở dòng 303-309 với `row.trace_id.clone()` trước khi tính `active_trace_id` ở dòng 325. Nếu một dòng outbox có `trace_id` rỗng, header `traceparent`/`x-trace-id` mang `active_trace_id` mới nhưng JSON body `envelope.trace_id` vẫn là `""`, lệch [docs/contracts.md:406](docs/contracts.md#L406).
   - **Mức**: **P3**
   - **Đề xuất sửa**: Dựng `pub_span` và tính `active_trace_id` trước rồi gán `trace_id: active_trace_id.clone()` vào `EventEnvelope`.

8. **[services/payment/migrations/0007_payment_fee_configs.sql:17-18](services/payment/migrations/0007_payment_fee_configs.sql#L17-L18) & [services/payment/src/service.rs:2000](services/payment/src/service.rs#L2000), [2014](services/payment/src/service.rs#L2014)** — **Tie-break đặt `created_at DESC` trước `seq DESC` phụ thuộc đồng hồ wall-clock**
   - **Kịch bản cụ thể**: Nếu 2 bản ghi phí được append với cùng `effective_from` và đồng hồ Postgres bị chỉnh lùi (NTP step-back) giữa hai lần INSERT, bản ghi mới (`seq` lớn hơn) có `created_at` nhỏ hơn nên `ORDER BY payment_method ASC, effective_from DESC, created_at DESC, seq DESC` chọn bản ghi cũ thay vì bản ghi mới nhất. (Tương tự trong [services/payment/src/domain.rs:508-510](services/payment/src/domain.rs#L508-L510).)
   - **Mức**: **P3**
   - **Đề xuất sửa**: Đổi tie-break sang `(effective_from DESC, seq DESC)` (dùng `BIGSERIAL seq` đơn điệu làm mốc ưu tiên ngay sau `effective_from`).

---

## Nghi ngờ (cần kiểm chứng runtime)

- **[services/payment/src/telemetry.rs:197-205](services/payment/src/telemetry.rs#L197-L205) & [services/payment/src/workers.rs:311](services/payment/src/workers.rs#L311)**: Vì `outbox_events` chỉ lưu `trace_id` (không lưu `traceparent`), `extract_context_from_trace_id` tạo một `parent_span_id` ngẫu nhiên 16-hex qua `format_traceparent`. Khi export lên Jaeger/Tempo, span `nats.publish` cùng `trace_id` có thể bị báo *invalid/missing parent span* thay vì gắn trực tiếp làm con của span gốc.
  - **Cách kiểm**: Gửi một giao dịch có `traceparent` sinh outbox event (`payment.succeeded`), mở trace đó trên collector/UI (hoặc log OTLP debug exporter) để xem span `nats.publish` có báo thiếu parent span ID không; nếu có, lưu `traceparent` vào `outbox_events` hoặc tạo OTel Span Link thay vì parent giả.

---

## Ponytail review (thừa thãi / đơn giản hoá)

- [services/payment/src/domain.rs:491-518](services/payment/src/domain.rs#L491-L518): Bỏ `pub fn resolve_active_fee_configs` khỏi production API (chỉ được gọi trong unit test của chính file, nhân bản logic SQL ở `service.rs:1995-2001`) → chuyển vào `#[cfg(test)]` hoặc xoá.
- [services/payment/src/domain.rs:592](services/payment/src/domain.rs#L592): Bỏ nhánh chết `"platform_fee" | "fee" | "payment_fee"` trong `format_seller_ledger_description` (không có ledger transaction nào ghi các `reference_type` này lên `seller_pending:*`/`seller_available:*`) → để rơi vào nhánh `other`.
- [services/payment/src/service.rs:2085-2237](services/payment/src/service.rs#L2085-L2237): Gộp 2 nhánh `Some(shop_id)` / `None` trong `list_seller_ledger` (đang lặp lại ~150 dòng SQL `COUNT` + CTE `page_entries`) → dùng 1 cặp query với `$1::TEXT` nullable (`($1::TEXT IS NULL AND a.shop_id IS NOT NULL) OR a.shop_id = $1`) kết hợp `STARTS_WITH(e.account_id, ...)`.
- [pkg/common/jetstream/stream.go:79](pkg/common/jetstream/stream.go#L79), [101](pkg/common/jetstream/stream.go#L101): Bỏ variadic `extraPrimarySubjects ...string` / `extraSubjects ...string` (0 caller trong toàn repo dùng và gây vấn đề concurrency ở mục P2 #5) → dùng thẳng `CanonicalPrimarySubjects()`.
- [pkg/common/telemetry/telemetry.go:108-120](pkg/common/telemetry/telemetry.go#L108-L120): `StartConsumerSpan` thêm vào `pkg/common` nhưng chỉ có 1 caller ([services/notification/internal/consumer/consumer.go:165](services/notification/internal/consumer/consumer.go#L165)) trong khi `promotion` vẫn tự làm tay → tái sử dụng thống nhất ở mọi Go consumer hoặc giữ inline đến khi có >=2 caller.

---

## Kiểm tra các hạng mục đã đạt & File đã đọc không thấy lỗi

### Điểm đã xác minh đúng trong phạm vi
- **Mô hình tiền 2b (`R` gồm `P_shop` + `S_shop`, `C_total = commission + payment_fee`)**: `validate_allocations_sum`, `compute_settlement_split`, `build_escrow_settle_tx`, `build_post_settlement_refund_tx`, `release_pending_allocation_tx` bảo toàn `P + D = R + Ship` và cân sổ kép ở mọi biên (`C_total == R`, chặn `C_total > R`, `R == 0`, đơn `0₫`, nhiều shop VietQR/COD, hoàn một phần trước & sau settlement, trả hàng, huỷ 1 đơn con, COD thất bại). Đổi cấu hình phí mới không làm đổi đơn cũ vì `payment_shop_allocations.commission_vnd` là snapshot cố định lúc tạo payment.
- **Sổ giao dịch người bán (`ListSellerLedger`) & số dư (`GetSellerBalance`)**: Cùng nguồn `ledger_entries`, `ListSellerLedger` khoá `shop_id` theo JWT claims (chặn buyer, seller xem chéo shop, service token), `escrow_settle` tách 1 transaction per `(order_id, shop_id)` nên `platform_fee_vnd` trong mô tả khớp từng đơn con.
- **Migration `0007_payment_fee_configs.sql` & `build.rs`**: Forward-only, trigger chặn `UPDATE`/`DELETE`, ràng buộc `CHECK` đầy đủ, seed baseline `0 bps` mốc `2020-01-01T00:00:00Z` an toàn trên DB đã có dữ liệu cũ, `CREATE INDEX IF NOT EXISTS`, `build.rs` có `cargo:rerun-if-changed=migrations`.
- **RBAC `SERVICE_METHODS` (Rust) & `logger/grpc.go` (Go) & PII/secrets**: `SERVICE_METHODS` chỉ thêm `GetFeeConfig` (chặn service token trên `SetFeeConfig`, `ListSellerLedger`); `logger.GRPCUnaryServerInterceptor` ghi 1 dòng JSON/RPC có `trace_id`/`span_id`, bỏ qua payload/metadata/raw error và gắn đúng thứ tự interceptor ở 7 service Go; không thấy rò rỉ số tài khoản ngân hàng hay secret ở log/event.

### Danh sách file đã đọc mà không thấy lỗi
- [pkg/common/auth/grpc_trust_test.go](pkg/common/auth/grpc_trust_test.go)
- [pkg/common/go.mod](pkg/common/go.mod), [pkg/common/go.sum](pkg/common/go.sum)
- [pkg/common/jetstream/stream_test.go](pkg/common/jetstream/stream_test.go)
- [pkg/common/logger/grpc.go](pkg/common/logger/grpc.go), [pkg/common/logger/grpc_test.go](pkg/common/logger/grpc_test.go), [pkg/common/logger/logger.go](pkg/common/logger/logger.go)
- [pkg/common/metrics/metrics.go](pkg/common/metrics/metrics.go)
- [pkg/common/telemetry/telemetry.go](pkg/common/telemetry/telemetry.go), [pkg/common/telemetry/telemetry_test.go](pkg/common/telemetry/telemetry_test.go) *(chỉ có gợi ý ponytail ở trên)*
- [services/payment/Cargo.toml](services/payment/Cargo.toml), [services/payment/build.rs](services/payment/build.rs)
- [services/payment/migrations/0001_init.sql](services/payment/migrations/0001_init.sql) .. [0006_w2a_refund_reconcile.sql](services/payment/migrations/0006_w2a_refund_reconcile.sql)
- [services/payment/src/auth.rs](services/payment/src/auth.rs)
- [services/payment/src/config.rs](services/payment/src/config.rs)
- [services/payment/src/error.rs](services/payment/src/error.rs)
- [services/payment/src/ledger.rs](services/payment/src/ledger.rs)
- [services/payment/src/main.rs](services/payment/src/main.rs)
- [services/payment/src/vietqr.rs](services/payment/src/vietqr.rs)
- [services/payment/src/webhook_security.rs](services/payment/src/webhook_security.rs)

