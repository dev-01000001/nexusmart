---
name: platform-stack
description: 'Tham chiếu stack hạ tầng, datastore, message broker, observability, codegen và client lib đã chốt của NexusMart (Postgres 16, Redis 7, NATS JetStream 2.10, OTel Collector, Mailpit, buf, sqlc) và danh mục nhóm B chưa chốt. Dùng khi sửa docker-compose, biến môi trường, kết nối DB/Redis/NATS/OTel, chọn thư viện Go/Rust, hoặc cân nhắc thêm thành phần hạ tầng mới.'
---

# NexusMart Platform Stack

Nguồn sự thật: `docker-compose.yml`, `.env.example`, `infra/`, `docs/contracts.md`, `buf.yaml`, `buf.gen.yaml`, các `go.mod`, `services/*/sqlc.yaml`, `services/payment/Cargo.toml`.

## 1. Thành phần hạ tầng đã chốt

| Thành phần | Image & phiên bản | Cổng container → biến host (mặc định) | Cấu hình / Init | Service đang dùng |
| --- | --- | --- | --- | --- |
| PostgreSQL 16 | `postgres:16-alpine` | `5432` → `POSTGRES_PORT` (`5432`) | `infra/postgres/init-dbs.sql` | `user`, `product`, `promotion`, `order`, `payment`, `shipping`, `notification` |
| Redis 7 | `redis:7-alpine` (`--appendonly yes`) | `6379` → `REDIS_PORT` (`6379`) | `REDIS_ADDR` (`redis:6379`) | `gateway`, `user`, `cart`, `promotion`, `shipping` |
| NATS JetStream 2.10 | `nats:2.10-alpine` (`-js -m 8222 -sd /data`) | `4222` → `NATS_PORT` (`4222`), `8222` → `NATS_MONITOR_PORT` (`8222`) | `NATS_URL`, `NATS_STREAM=NEXUSMART` | `user`, `promotion`, `order`, `payment`, `shipping`, `notification` |
| OTel Collector | `otel/opentelemetry-collector-contrib:0.108.0` | `4317` → `OTEL_GRPC_PORT`, `4318` → `OTEL_HTTP_PORT`, `8889` → `OTEL_PROM_PORT` | `infra/otel-collector-config.yaml` | Cả 9 service backend (`gateway` + 8 domain service) |
| Mailpit | `axllent/mailpit:v1.20` | `1025` → `MAILPIT_SMTP_PORT`, `8025` → `MAILPIT_UI_PORT` | `SMTP_HOST=mailpit`, `SMTP_PORT=1025`, `SMTP_FROM` | `notification` (gửi SMTP), `tests/e2e` (đọc API `:8025`) |

Cổng service backend (`docker-compose.yml`, `.env.example`):
- gRPC `50051..50058` chỉ nội bộ mạng compose, không publish ra host.
- HTTP API/ops `8080..8088` override qua `GATEWAY_PORT`, `USER_HTTP_PORT`, `PRODUCT_HTTP_PORT`, `CART_HTTP_PORT`, `PROMOTION_HTTP_PORT`, `ORDER_HTTP_PORT`, `PAYMENT_HTTP_PORT`, `SHIPPING_HTTP_PORT`, `NOTIFICATION_HTTP_PORT`. Mọi service mở `/healthz`, `/readyz`, `/metrics`.

## 2. PostgreSQL 16 (7 DB tách biệt)

- `infra/postgres/init-dbs.sql` khởi tạo 7 DB độc lập: `user_db`, `product_db`, `promotion_db`, `order_db`, `payment_db`, `shipping_db`, `notification_db`. Không service nào truy vấn DB của service khác. `gateway` và `cart` không dùng Postgres.
- Migration chỉ đi tới (forward-only, không có file down), tự chạy lúc service khởi động:
  - Đánh số: `0001_` (`user`, `order`, `payment`), `000001_` (`product`, `shipping`, `notification`), `001_` (`services/promotion/internal/store/migrations/`).
  - Go nhúng `.sql` bằng `go:embed`: `user` (`schema_migrations.version`), `product` và `order` (`schema_migrations.filename`) chạy mỗi file trong 1 transaction và ghi bảng; `promotion` (`internal/store/store.go`), `shipping` (`internal/store/store.go`), `notification` (`internal/db/migrate.go`) chạy lại toàn bộ `.sql` mỗi lần khởi động bằng `pool.Exec` nên SQL phải idempotent.
  - Rust `payment` dùng `sqlx::migrate!` (kiểm checksum, không sửa file cũ đã chạy).
- Client lib:
  - Go: `github.com/jackc/pgx/v5 v5.6.0` (ở 6 service Go có DB).
  - Rust (`services/payment/Cargo.toml`): `sqlx = "0.8"` (`runtime-tokio`, `tls-rustls`, `postgres`, `uuid`, `chrono`, `json`, `migrate`, `macros`; dùng `sqlx::query`/`sqlx::query_as`, không dùng macro `query!`).

## 3. Redis 7 — Phân vai theo service

Client lib: Go `github.com/redis/go-redis/v9 v9.6.1` (`miniredis/v2` cho unit test ở `cart` `v2.33.0`, `promotion` `v2.40.0`). Rust `payment` không dùng Redis.

| Service | Mục đích & Key prefix | File cài đặt |
| --- | --- | --- |
| `cart` | Giỏ hàng theo user dạng hash `cart:<user_id>`, TTL `30d` | `services/cart/internal/store/redis_store.go` |
| `gateway` | Rate limit sliding-window bằng Lua (`rl:ip:*`, `rl:user:*`, `rl:flash:user:*`, `rl:flash:ip:*`; biến `RATE_LIMIT_*_PER_MIN`) và kiểm JTI thu hồi fail-closed (`user:revoked_jti:<jti>`) | `services/gateway/internal/middleware/ratelimit.go`, `services/gateway/internal/middleware/auth.go` |
| `user` | OTP + Lua `verifyOTPScript` (`user:otp:*`, `user:otp_attempts:*`, `user:otp_resend:*`), khóa đăng nhập brute-force (`user:login_fail:*`, `user:login_lock:*`), ghi JTI thu hồi (`user:revoked_jti:*`) | `services/user/internal/security/redis_store.go` |
| `promotion` | Lua nguyên tử `reserve_atomic.lua` & `release_atomic.lua` trừ/hoàn suất flash sale, quota & ngân sách voucher, giới hạn per-user (tombstone TTL `30d`) | `services/promotion/internal/lua/scripts.go` |
| `shipping` | `RedisQuoteCache` cache báo giá vận chuyển (TTL `5m`, gom khối lượng theo bậc `100g`) | `services/shipping/internal/cache/quote_cache.go` |

## 4. NATS JetStream 2.10 — Stream, Dedup, Outbox, DLQ

Client lib: Go `github.com/nats-io/nats.go v1.36.0`; Rust `async-nats = "0.38"`.

- **Stream chính `NEXUSMART`**: subjects `user.>`, `order.>`, `payment.>`, `shipment.>`, `LimitsPolicy`, `FileStorage`, cửa sổ dedup `2m` (`120s`) theo header `Nats-Msg-Id = event_id`.
- **Stream DLQ `NEXUSMART_DLQ`**: subject `dlq.>`, `LimitsPolicy`, `FileStorage`. Consumer thử tối đa `5` lần rồi đẩy poison message sang `dlq.<original_subject>` và `Ack`.
- **Outbox & Consumer idempotent**:
  - `order`, `payment`, `shipping` phát event qua bảng `outbox_events` + outbox relay poll (`250ms` ở `order`/`shipping`); `user` publish trực tiếp JetStream.
  - Bảng chống trùng theo `event_id`: `processed_events` (`order`, `promotion`, `shipping`, `notification`) và `processed_nats_events` (`payment`).
  - Bảng lưu cục bộ poison message `dead_letter_events` có ở `order`, `shipping`, `notification` (`payment` và `promotion` đẩy thẳng lên stream `NEXUSMART_DLQ`).
- **Nơi tạo stream idempotent trong code**:
  - `services/user/internal/events/publisher.go:ensureStream` (chỉ tạo `NEXUSMART`)
  - `services/order/internal/events/outbox.go:EnsureStreams` (`NEXUSMART` + `NEXUSMART_DLQ`)
  - `services/promotion/internal/events/consumer.go:EnsureStreams` (`NEXUSMART` + `NEXUSMART_DLQ`)
  - `services/shipping/internal/events/envelope.go:EnsureStreams` (`NEXUSMART` + `NEXUSMART_DLQ`)
  - `services/notification/internal/consumer/consumer.go:EnsureStreams` (`NEXUSMART` + `NEXUSMART_DLQ`)
  - `services/payment/src/workers.rs:connect_jetstream` (`NEXUSMART` + `NEXUSMART_DLQ`)
- **Lưu ý lệch compose/code**: `docker-compose.yml` truyền `NATS_URL`/`NATS_STREAM` và `depends_on: nats` cho `product`, nhưng `services/product` hiện không import hay dùng NATS.

## 5. OpenTelemetry Collector & Mailpit

- **OTel Collector** (`infra/otel-collector-config.yaml`): nhận OTLP gRPC `:4317` và HTTP `:4318`, batch `timeout: 2s` / `send_batch_size: 256`; xuất traces ra `debug`, metrics ra `prometheus` (`:8889`) + `debug`.
  - Go (`pkg/common/telemetry/telemetry.go`): `go.opentelemetry.io/otel v1.28.0`, `go.opentelemetry.io/otel/sdk v1.28.0`, `go.opentelemetry.io/otel/trace v1.28.0`, `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.28.0`; RED metrics qua `github.com/prometheus/client_golang v1.19.1`.
  - Rust (`services/payment/src/telemetry.rs`): `opentelemetry = "0.27"`, `opentelemetry_sdk = "0.27"`, `opentelemetry-otlp = "0.27"` (`grpc-tonic`), `tracing-opentelemetry = "0.28"`, `tracing-subscriber = "0.3"` (JSON), `prometheus = "0.13"`.
  - Lan truyền W3C `traceparent` xuyên suốt HTTP, gRPC, NATS và trả `X-Trace-Id` trên HTTP response.
- **Mailpit**: `services/notification/internal/mailer/mailer.go` gửi email bằng Go stdlib `net/smtp` tới `SMTP_HOST:SMTP_PORT` (`mailpit:1025`). `tests/e2e/mailpit.go` đọc hộp thư tại `http://mailpit:8025/api/v1/messages`.

## 6. Codegen, RPC & Runtime Libraries (`buf`, `sqlc`, Go/Rust/Web)

- **`buf` & Protobuf**: `buf` `v1.45.0` (ghim ở `.github/workflows/ci.yml`), `buf.yaml` (`v2`, lint `STANDARD`, breaking `FILE`), `buf.gen.yaml` (plugin cục bộ `protoc-gen-go v1.34.2`, `protoc-gen-go-grpc v1.5.1`, `paths=source_relative` → `gen/go`). Lệnh sinh: `make proto`.
- **Go (`go 1.22`, CI `1.22.2`)**: monorepo nhiều module nối bằng `replace => ../../gen/go` và `../../pkg/common`. Thư viện chung: `google.golang.org/grpc v1.65.0`, `google.golang.org/protobuf v1.34.2`, `github.com/go-chi/chi/v5 v5.1.0` (`pkg/common`, `gateway`, `shipping`), `github.com/golang-jwt/jwt/v5 v5.2.1`, `github.com/google/uuid v1.6.0`, `golang.org/x/crypto v0.25.0` (argon2id ở `user`), `github.com/sony/gobreaker/v2 v2.0.0` (circuit breaker ở `shipping`).
- **Rust (`services/payment/Cargo.toml`)**: tự biên dịch `proto/nexusmart/payment/v1/payment.proto` trong `services/payment/build.rs` bằng `tonic-build = "0.12"` (cần `protoc`, máy dev `libprotoc 28.3`); runtime `tonic = "0.12"`, `prost = "0.13"`, `axum = "0.8"`, `tokio = "1.43"`, `reqwest = "0.12"`, `hmac = "0.12"`, `sha2 = "0.10"`.
- **`sqlc` (`v1.27.0`, `version: "2"`, `postgresql`, `pgx/v5`)**: dùng ở 4 service `user`, `product`, `order`, `notification` (`services/<svc>/sqlc.yaml`, `queries/` → `internal/db/`). Không sửa tay file có dòng `Code generated by sqlc. DO NOT EDIT.`; `internal/db/migrate.go` là code viết tay. `promotion`, `shipping`, `cart`, `gateway`, `payment` không dùng sqlc.
- **Tích hợp thanh toán & vận chuyển giai đoạn đầu** (`.env.example`): COD, VietQR qua payOS/SePay (`VIETQR_MODE=mock` mặc định), GHN sandbox + adapter mock (`GHN_MODE=mock`, `MOCK_CARRIER_ENABLED=true`).
- **Web (`web/package.json`)**: `next ^15.5.0`, `react ^19.1.0`, `typescript ^5.8.3`, CSS thuần (chưa nối gateway, chưa vào `docker-compose.yml`/CI).

## 7. Chưa chốt (Nhóm B) & Luật thêm thành phần

Các hạng mục nhóm B dưới đây **chưa chốt** — chỉ quyết định và theo dõi qua `docs/adr/README.md`:
- Ingress / Load Balancer (và cơ chế cấp TLS cho domain riêng)
- Search engine (service `search` chưa dựng)
- Object storage / CDN cho media người bán
- Observability backend (lưu trữ trace/log/metric sau OTel Collector)
- Phân quyền chi tiết & nâng cấp tin cậy liên service (mTLS hoặc JWT bất đối xứng + JWKS thay HS256 dùng chung)
- Nơi triển khai: **chưa rõ** (chưa chốt Kubernetes tự host hay cloud)
- Headless UI (hiện `web/` dùng CSS Modules thuần, không UI kit)

**Luật**: Chỉ thêm thành phần hạ tầng hoặc thư viện nền mới sau khi ADR tương ứng trong `docs/adr/README.md` được duyệt, rồi cập nhật lại skill này.
