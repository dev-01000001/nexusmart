# Báo cáo Triển khai NexusMart Core Backend (`CORE_REPORT.md`)

> **Phạm vi**: Lõi backend NexusMart theo lộ trình Tháng 1–3 (`docs/core-brief.md`, `docs/spec.md`, `AGENTS.md`).  
> **Trạng thái nghiệm thu**: **PASS toàn bộ Gate** (Static/Lint/Format, Unit Test Go + Rust, Docker Compose 14/14 container khỏe mạnh, 8/8 kịch bản E2E tự động, rà soát bảo mật/chất lượng 0 tồn đọng).

---

## 1. Kiến trúc đã dựng

Hệ thống được tổ chức theo mô hình monorepo gọn (tuân thủ nguyên tắc Ponytail: dùng thư viện chuẩn và các thư viện đã chốt trong đặc tả, không thêm framework nặng hay lớp trừu tượng dư thừa):

```text
.
├── proto/nexusmart/<domain>/v1/   # Hợp đồng Protobuf dùng chung cho 8 domain (lint & gen bằng buf)
├── gen/go/                        # Module Go sinh từ Protobuf (github.com/nexusmart/core/gen/go)
├── pkg/common/                    # Module Go dùng chung nhỏ: env, logger, telemetry, metrics, auth
├── services/
│   ├── gateway/                   # Go (chi)      : HTTP 8080, REST /api/v1/*, JWT/RBAC, Redis rate limit, proxy webhook/SSE
│   ├── user/                      # Go (pgx,sqlc) : gRPC 50051 / HTTP 8081, user_db + Redis + NATS
│   ├── product/                   # Go (pgx,sqlc) : gRPC 50052 / HTTP 8082, product_db + NATS
│   ├── cart/                      # Go (go-redis) : gRPC 50053 / HTTP 8083, Redis
│   ├── promotion/                 # Go (pgx,redis): gRPC 50054 / HTTP 8084, promotion_db + Redis Lua + NATS
│   ├── order/                     # Go (pgx,sqlc) : gRPC 50055 / HTTP 8085, order_db + NATS (Saga + Outbox)
│   ├── payment/                   # Rust (Axum/Tonic/SQLx): gRPC 50056 / HTTP 8086, payment_db + NATS
│   ├── shipping/                  # Go (pgx,redis): gRPC 50057 / HTTP 8087, shipping_db + Redis + NATS
│   └── notification/              # Go (pgx,sqlc) : gRPC 50058 / HTTP 8088, notification_db + NATS + Mailpit SMTP
├── infra/
│   ├── postgres/init-dbs.sql      # Khởi tạo 7 database PostgreSQL độc lập
│   └── otel-collector-config.yaml # Cấu hình OpenTelemetry Collector
├── tests/e2e/                     # Bộ kiểm thử E2E tự động bằng Go (8 cổng nghiệm thu)
├── docs/contracts.md              # Hợp đồng API REST/gRPC/NATS/Saga thống nhất
├── docker-compose.yml             # Hạ tầng dev + 9 service backend (multi-stage, non-root)
├── .github/workflows/ci.yml       # CI: buf lint, gofmt, golangci-lint, go test -race, rustfmt, clippy, cargo test, docker build
├── .golangci.yml                  # Cấu hình linter Go dùng chung
├── .env.example                   # Biến môi trường dev/sandbox (không chứa secret thật)
└── Makefile                       # Target chuẩn: proto, lint, test, up, down, e2e
```

### Tách biệt dữ liệu & Giao tiếp liên service
- **Database per Service**: 7 database PostgreSQL độc lập (`user_db`, `product_db`, `promotion_db`, `order_db`, `payment_db`, `shipping_db`, `notification_db`) và Redis (cho `cart`, rate limit `gateway`, OTP/lockout `user`, Lua script `promotion`, cache `shipping`). Không có service nào truy vấn trực tiếp DB của service khác.
- **Đồng bộ**: REST cho client tại `gateway` (`:8080`); gRPC giữa `gateway` và các service nội bộ (`:50051..:50058`) dùng hợp đồng Protobuf chung (`proto/`).
- **Bất đồng bộ**: NATS JetStream với stream chính `NEXUSMART` (subject `user.>`, `order.>`, `payment.>`, `shipment.>`, dedup bằng `Nats-Msg-Id`) và stream `NEXUSMART_DLQ` (`dlq.>`).
- **Độ tin cậy giao dịch**:
  - `order`: máy trạng thái đơn hàng tường minh, Saga orchestration cho checkout đa gian hàng, Idempotency-Key chống tạo đơn trùng, Transactional Outbox + worker watchdog quá hạn thanh toán (UC-PAY06).
  - `product`: giữ/nhả/xác nhận tồn kho theo SKU bằng optimistic locking (`version`) + idempotent theo `order_id`.
  - `promotion`: trừ suất flash sale, lượt voucher và xu bằng Redis Lua script nguyên tử (2 pha), áp khuyến mãi đúng thứ tự `flash sale -> voucher gian hàng -> voucher sàn -> freeship -> xu`, hỗ trợ `ReleasePromotion` hoàn trả 100% khi saga thất bại.
  - `payment` (Rust): toàn bộ tiền lưu số nguyên `i64` / SQL `BIGINT` VND (0 số thực), sổ cái ghi kép (`ledger_accounts`, `ledger_transactions`, `ledger_entries`) cưỡng chế cân bằng `SUM(debit) == SUM(credit)` cả ở Rust lẫn PostgreSQL `DEFERRABLE` trigger, chống sửa/xóa bản ghi sổ cái.
  - `shipping`: interface `Carrier`, adapter GHN (chế độ `mock` mặc định, sẵn sàng `sandbox`) + adapter `mock`, timeout, exponential backoff retry, circuit breaker, bảng phí dự phòng, kiểm tra webhook HMAC + chống replay + chống sự kiện lệch thứ tự (`occurred_at`).
  - `notification`: consumer idempotent theo `processed_events(event_id)` trong cùng transaction, đẩy message độc hại sang `NEXUSMART_DLQ` + bảng `dead_letter_events` sau tối đa 5 lần thử, gửi email SMTP qua Mailpit.

### Bảo mật & Quan sát (Security & Observability)
- **Xác thực & phân quyền**: mật khẩu băm `argon2id`, access token HS256 TTL 15 phút có bắt buộc `exp`, refresh token xoay vòng phát hiện tái sử dụng và thu hồi cả family, thu hồi `jti` trên Redis khi logout và chặn ngay tại Gateway (fail-closed), khóa tạm 15 phút sau 5 lần đăng nhập sai, RBAC ở cả Gateway và từng gRPC service, kiểm tra quyền theo đối tượng (object-level ownership), `audit_logs` chỉ ghi thêm (append-only trigger).
- **Webhook**: kiểm tra chữ ký HMAC-SHA256 thời gian hằng (`hmac::Mac::verify_slice` / `hmac.Equal`), bắt buộc `X-Timestamp` trong cửa sổ 5 phút, chống trùng lặp mức DB (`provider_tx_id` / `event_id`).
- **Log, Trace & Metric**: toàn bộ 9 service xuất log JSON có `trace_id`/`span_id` (OpenTelemetry W3C `traceparent` xuyên suốt HTTP -> gRPC -> NATS), tự động che PII (email/SĐT) và secret (JWT/Bearer/DSN/chữ ký webhook); mỗi service mở `/healthz`, `/readyz`, `/metrics` với đủ bộ chỉ số RED (`http_requests_total`, `http_errors_total`, `http_request_duration_seconds`, `grpc_requests_total`, `grpc_errors_total`, `grpc_request_duration_seconds`).
- **Docker**: toàn bộ 9 `Dockerfile` build nhiều giai đoạn từ root repo và chạy dưới user non-root (`appuser` cho Go, `10001:10001` cho Rust).

---

## 2. Cách chạy

### Công cụ đã cài đặt (user-local, không dùng `sudo`)
- `go`: `/usr/bin/go` (`go1.22.2 linux/amd64`)
- `buf`: `~/.local/bin/buf` (`1.45.0`)
- `protoc`: `~/.local/bin/protoc` (`libprotoc 28.3`, well-known protos tại `~/.local/include`)
- `protoc-gen-go` / `protoc-gen-go-grpc`: `~/go/bin/` (symlink tại `~/.local/bin`)
- `golangci-lint`: `~/.local/bin/golangci-lint` (`1.60.3`)
- `sqlc`: `~/go/bin/sqlc` (`v1.27.0`)
- `rustc` / `cargo`: `~/.cargo/bin/rustc`, `~/.cargo/bin/cargo` (`1.99.1`, kèm `rustfmt` & `clippy`)

### Chạy lint và unit test toàn bộ monorepo
```bash
export PATH="$HOME/.local/bin:$HOME/go/bin:$HOME/.cargo/bin:$PATH"
make lint
make test
```

### Khởi động toàn bộ stack (máy trống cổng mặc định)
```bash
cp .env.example .env   # tuỳ chọn nếu muốn tuỳ chỉnh biến môi trường
docker compose up --build -d
docker compose ps
make e2e
```

### Khởi động trên máy dev dùng chung bị chiếm sẵn một số cổng host
Trên máy nghiệm thu hiện tại, 6 cổng host đang được tiến trình/container khác sử dụng (`6379`, `4318`, `8080`, `8082`, `8083`, `8085`). `docker-compose.yml` giữ nguyên cổng mặc định nhưng cho phép ghi đè cổng host qua biến môi trường:
```bash
export REDIS_PORT=26379 OTEL_HTTP_PORT=24318 \
       GATEWAY_PORT=28080 PRODUCT_HTTP_PORT=28082 \
       CART_HTTP_PORT=28083 ORDER_HTTP_PORT=28085

docker compose down -v
docker compose up --build -d
docker compose ps

cd tests/e2e && \
  GATEWAY_URL=http://localhost:28080 \
  PRODUCT_HTTP_URL=http://localhost:28082 \
  CART_HTTP_URL=http://localhost:28083 \
  ORDER_HTTP_URL=http://localhost:28085 \
  go test -v -count=1 -timeout 5m ./...
```

Giao diện Mailpit kiểm tra email OTP/đơn hàng: `http://localhost:8025`.  
Dừng và dọn volume: `make down` (`docker compose down -v`).

---

## 3. Kết quả kiểm thử (Final Gate)

Toàn bộ lệnh dưới đây được chạy trực tiếp trên bản mã nguồn cuối cùng sau khi mọi khuyến nghị của `verifier` đã được sửa và kiểm chứng lại.

### 3.1 Kiểm thử tĩnh, format, lint & unit test

| Hạng mục | Lệnh thực thi | Kết quả | Chi tiết |
| --- | --- | --- | --- |
| Protobuf lint | `buf lint` | **PASS** | 0 lỗi trên 8 file `.proto` |
| Go format | `gofmt -l pkg gen services tests` | **PASS** | Danh sách file chưa format rỗng (`[]`) |
| Go lint (10 module) | `make lint` (`golangci-lint -c .golangci.yml`) | **PASS** | 0 cảnh báo trên `pkg/common`, `gen/go`, và 8 Go service |
| Go lint (E2E suite) | `cd tests/e2e && golangci-lint run -c ../../.golangci.yml ./...` | **PASS** | 0 cảnh báo |
| Rust format & clippy | `cargo fmt --all -- --check && cargo clippy --all-targets -- -D warnings` | **PASS** | 0 cảnh báo trên `services/payment` |
| Go unit test (`-race`) | `make test` (`go test -race ./...`) | **PASS** | Toàn bộ 10 Go module xanh (30 package test có race detector) |
| Rust unit test | `cd services/payment && cargo test` | **PASS** | **26 passed, 0 failed** (sổ cái ghi kép, HMAC/replay, 10x webhook, refund cap đa gian hàng, circuit breaker, NATS DLQ, RBAC) |

### 3.2 Trạng thái Docker Compose (`docker compose ps`)

Toàn bộ **14/14 container** khởi động thành công từ ảnh mới build (`docker compose down -v && docker compose up --build -d`) và toàn bộ **13/13 container có healthcheck** đạt `healthy`:

| Container / Service | Trạng thái | Cổng (phiên chạy nghiệm thu) |
| --- | --- | --- |
| `postgres` | `healthy` | `5432->5432` |
| `redis` | `healthy` | `26379->6379` |
| `nats` | `healthy` | `4222->4222`, `8222->8222` |
| `mailpit` | `healthy` | `1025->1025`, `8025->8025` |
| `otel-collector` | `Up` | `4317->4317`, `24318->4318`, `8889->8889` |
| `gateway` | `healthy` | `28080->8080` |
| `user` | `healthy` | `8081->8081`, `50051->50051` |
| `product` | `healthy` | `28082->8082`, `50052->50052` |
| `cart` | `healthy` | `28083->8083`, `50053->50053` |
| `promotion` | `healthy` | `8084->8084`, `50054->50054` |
| `order` | `healthy` | `28085->8085`, `50055->50055` |
| `payment` | `healthy` | `8086->8086`, `50056->50056` |
| `shipping` | `healthy` | `8087->8087`, `50057->50057` |
| `notification` | `healthy` | `8088->8088`, `50058->50058` |

### 3.3 Kết quả 8/8 kịch bản E2E tự động (`PASS` tổng thời gian `6.297s`)

| # | Tên bài test (`tests/e2e/`) | Kết quả | Thời gian | Kiểm chứng chính đã đạt |
| --- | --- | --- | --- | --- |
| 1 | `TestE2E_HealthAndObservability` | **PASS** | `0.56s` | 9/9 service trả `200` trên `/healthz` & `/readyz`; Gateway trả `X-Trace-Id=4bf92f3577b34da6a3ce929d0e0e4736` khớp W3C `traceparent`; 9/9 `/metrics` xuất đủ chỉ số RED. |
| 2 | `TestE2E_MultiShopCheckout_COD` | **PASS** | `2.44s` | Admin tạo danh mục; 2 người bán đăng ký + xác thực OTP từ Mailpit + duyệt gian hàng + duyệt sản phẩm; người mua tạo địa chỉ 2 cấp (34+ tỉnh); giỏ hàng gom 2 nhóm gian hàng; áp đủ voucher shop + sàn + freeship + xu; đặt COD tách 2 đơn con `CONFIRMED`; xoá giỏ; trừ tồn; gửi thông báo in-app & email Mailpit; chặn người mua/bán khác đọc/thao tác đơn (`403`); luồng `confirm -> ship -> webhook GHN (delivering -> delivered) -> confirm-received -> COMPLETED`; timeline đầy đủ; cộng xu thưởng & cập nhật số dư người bán. |
| 3 | `TestE2E_MultiShopCheckout_VietQRMock` | **PASS** | `1.00s` | Checkout đa gian hàng `VIETQR` tạo 2 đơn con `PENDING` kèm `payment_id` + QR; webhook HMAC chuyển cả 2 đơn con -> `CONFIRMED` + thông báo; hoàn tiền một phần `10,000 VND` có idempotency replay (`already_existed=true`). |
| 4 | `TestE2E_OrderIdempotency` | **PASS** | `0.40s` | Gửi lặp tuần tự (`idempotent_replay=true`, trả đúng cùng `checkout_group_id` & `order_id`) và bắn đồng thời 5 request chung `Idempotency-Key`: chỉ tạo đúng 1 đơn và trừ tồn đúng 1 lần. |
| 5 | `TestE2E_PaymentWebhookIdempotencyAndSecurity` | **PASS** | `0.69s` | Chữ ký HMAC sai/thiếu -> `401`; `X-Timestamp` quá hạn >5 phút -> `401`; gửi lặp **10 lần** (5 đồng thời + 5 tuần tự) cùng `provider_tx_id`: xử lý mới đúng **1 lần** (`fresh=1`, `duplicate=9`), ghi sổ cái đúng 1 lần, chỉ có 1 sự kiện chuyển `CONFIRMED`. |
| 6 | `TestE2E_FlashSale_1000Concurrent_100Slots` | **PASS** | `0.50s` | **1.000 yêu cầu đồng thời** tranh **100 suất** hoàn tất trong `112.30ms`: **đúng `succeeded=100`, `failed=900`**, `remaining_slots=0` (0 bán vượt suất); thêm luồng checkout qua Gateway mua ở giá flash `150,000 VND` (giảm từ `400,000 VND`), còn `4/5` suất. |
| 7 | `TestE2E_PaymentFailed_SagaCompensation` | **PASS** | `0.69s` | Đặt `VIETQR` giữ `stock: 30->27`, `voucher_quota: 5->4`, `coins: 10000->3000`; webhook `FAILED` chuyển đơn -> `CANCELLED` và Saga hoàn trả **100%**: `stock=30`, `voucher_quota=5`, `coins=10000`. |
| 8 | `TestE2E_LedgerAlwaysBalanced` | **PASS** | `0.00s` | Chạy sau cùng: **`total_debits_vnd = 3,198,410` == `total_credits_vnd = 3,198,410`**, `is_balanced = true` trên 8 tài khoản sổ cái. |

#### Bảng đối chiếu sổ cái ghi kép sau toàn bộ kịch bản E2E (`GET /api/v1/admin/ledger/summary`)

| Mã tài khoản (`account_code`) | Loại | Tổng Nợ (VND) | Tổng Có (VND) | Số dư (VND) |
| --- | --- | ---: | ---: | ---: |
| `cod_receivable` | ASSET | 1,650,000 | 0 | 1,650,000 |
| `escrow_liability` | LIABILITY | 386,955 | 2,471,000 | 2,084,045 |
| `gateway_clearing` | ASSET | 821,000 | 0 | 821,000 |
| `platform_fee_revenue` | REVENUE | 0 | 20,000 | 20,000 |
| `refund_payable` | LIABILITY | 0 | 10,000 | 10,000 |
| `seller_available:<shop1>` | LIABILITY | 0 | 340,455 | 340,455 |
| `seller_pending:<shop1>` | LIABILITY | 340,455 | 340,455 | 0 |
| `shipping_fee_payable` | LIABILITY | 0 | 16,500 | 16,500 |
| **Tổng cộng** | — | **3,198,410** | **3,198,410** | **Cân bằng (`true`)** |

### 3.4 Kiểm tra Log, Eventing, Email & Ranh giới thư mục
- **Log JSON & bảo mật dữ liệu**: toàn bộ 9 service xuất log JSON có `trace_id`/`span_id`; `user` che email (`bu***@nexusmart.test`), `payment` che chữ ký (`3037***[REDACTED]`). Quét toàn bộ log sau E2E: **`0` dòng `ERROR`**, **`0` secret/mật khẩu/JWT bị lộ**.
- **NATS JetStream**: stream `NEXUSMART` nhận `42` event với `6` consumer hoạt động ổn định; stream `NEXUSMART_DLQ` có **`0` message**.
- **Mailpit SMTP**: nhận đủ **`40` email** (OTP xác thực, tạo đơn, xác nhận đơn, huỷ đơn, thanh toán thành công/thất bại, hoàn tiền).
- **Rate limit Gateway**: đã kiểm chứng thực tế khi chạy 2 lượt E2E liên tiếp trong <60 giây (>300 req/phút/IP) — Gateway trả `HTTP 429` với envelope chuẩn `{"error":{"code":"RATE_LIMITED",...,"trace_id":"..."}}`.
- **Ranh giới không chỉnh sửa**: xác nhận các tệp `CLAUDE.md`, `AGENTS.md`, `docs/core-brief.md`, `docs/spec.md`, `scripts/agy.sh`, `.agents/` giữ nguyên timestamp trước khi swarm chạy; `web/` và `.taste-skill/` hoàn toàn không bị swarm backend chỉnh sửa.

---

## 4. Rà soát chất lượng & bảo mật (Verifier)

Trước khi chạy nghiệm thu cuối, worker `verifier` độc lập đã kiểm toán toàn bộ mã nguồn qua 3 vòng và xác nhận **0 tồn đọng** sau khi các lỗi sau được xử lý triệt để kèm unit test hồi quy:
- **`foundation` (`pkg/common/`, `Makefile`, `docker-compose.yml`)**: bổ sung `Unwrap()`/`Flush()` cho HTTP middleware wrapper để không làm hỏng SSE streaming; thêm fail-fast cho vòng lặp `Makefile`; bật `jwt.WithExpirationRequired()`; mở rộng che DSN/Bearer/PII trong logger; tách cứng container `HTTP_PORT: "8080"` khi đổi `GATEWAY_PORT` và tham số hoá các host port.
- **`payment` (`services/payment/`)**: bắt buộc `X-Timestamp` trên webhook (chặn bỏ qua chống replay); giới hạn hoàn tiền theo từng sub-order trong `payment_shop_allocations` (chặn double-refund làm hỏng phân bổ các gian hàng còn lại trong đơn gộp); gộp idempotency + cập nhật trạng thái trong cùng transaction NATS consumer và tôn trọng `max_deliver=5` trước khi đẩy DLQ; gỡ CORS `permissive`; bổ sung defense-in-depth RBAC trên gRPC.
- **`gateway-user` (`services/gateway/`, `services/user/`)**: chuyển lời gọi thu hồi refresh-token family ra ngoài `WithTx` đã rollback khi gặp concurrent replay; kiểm tra `user:revoked_jti:<jti>` trên Redis ngay tại Gateway (fail-closed `503`) để vô hiệu JWT toàn cục sau logout; chặn retry payment ở trạng thái kết thúc hoặc đổi `VIETQR -> COD` vượt rào kiểm soát rủi ro COD; thay `chimw.Recoverer` bằng panic recovery trả JSON chuẩn có `trace_id`; chuyển `VerifyOTP` sang Redis Lua script atomic SHA-256.
- **`order` (`services/order/`)**: loại bỏ rò rỉ raw caller JWT khỏi `WithInternalAdminContext` (sửa lỗi buyer huỷ đơn gọi restock và tra cứu địa chỉ lấy hàng của shop); chuẩn hóa ràng buộc hủy/hoàn trả.
- **`product-cart` (`services/product/`, `services/cart/`)**: sửa lỗi bộ lọc `public_only` bị bỏ qua khi `ListProducts` có tham số `status` và kiểm tra quyền hiển thị trong `GetProduct`; loại SKU inactive khỏi tổng giỏ hàng; khắc phục thundering herd ở trạng thái half-open circuit breaker.
- **`promotion` (`services/promotion/`)**: siết RBAC gRPC trên các RPC quản trị và kiểm tra ownership cho `ReservePromotion`; sửa nhánh concurrent duplicate reservation không trả `Reserved: true` khi DB lỗi; tái khởi tạo suất flash sale theo `RemainingSlots`.
- **`shipping-notif` (`services/shipping/`, `services/notification/`)**: khóa RBAC trên `CancelShipment` và bắt buộc `claims.ShopID != ""` với seller; thêm status guard SQL chống race huỷ vận đơn và chặn webhook ghi đè trạng thái kết thúc (`delivered`/`returned`/`cancelled`); bắt buộc `X-Timestamp` trên webhook vận chuyển; trả exit code khác 0 khi startup lỗi; đưa lời gọi mạng gRPC/SMTP ra khỏi PostgreSQL TX trong `notification`.

---

## 5. Phần chưa xong & Ngoài phạm vi

### Ngoài phạm vi của swarm này (theo `docs/core-brief.md`)
- Frontend `web/` và `.taste-skill/` (thuộc swarm giao diện riêng).
- `Search` service (Elasticsearch / hybrid vector search), `Chat` service (WebSocket thời gian thực), `AI assistant` (RAG / pgvector / function calling), `AIOps` engine (z-score / EWMA / runbook), Kubernetes / Helm / HPA, kiểm thử tải `k6` và E2E trình duyệt `Playwright`.
- Các hạng mục ngoài phạm vi toàn đồ án (`docs/spec.md`): service mesh, multi-region, app mobile native, livestream/game/affiliate/trả góp, thanh toán tiền thật/lưu thẻ/hoá đơn điện tử.

### Các hạng mục phụ Tháng 1–3 chưa triển khai sâu trong đợt Core này
- **Ví điện tử thứ hai (VNPay / MoMo / ZaloPay sandbox)**: mới tập trung làm sâu COD và VietQR (adapter hỗ trợ `mock`, `payos`, `sepay`) cùng sổ cái ghi kép và đối soát.
- **Đăng nhập Google OAuth 2.0 & luồng quên mật khẩu qua link email (UC-B11)**: hiện đã hoàn thiện đầy đủ đăng ký email + OTP Mailpit, đăng nhập Argon2id, JWT 15 phút, xoay vòng refresh token, logout thu hồi JTI.
- **Đánh giá sản phẩm (UC-B10), Yêu thích/Theo dõi (UC-B13), Từ khoá cấm tự động (UC-A15), Khiếu nại tranh chấp Admin (UC-A05)**: phần lõi tập trung vào chuỗi mua hàng đầu-cuối (đã có `RequestReturn` trên `OrderService`).
- **Xuất biên nhận PDF / in nhãn vận đơn hàng loạt / cấu hình token cổng động qua Admin UI (UC-A12, UC-A13)**: hiện cấu hình nhà cung cấp qua biến môi trường chuẩn 12-factor và trả về `label_url` mock/sandbox.
- **Nhập file đối soát COD lô từ hãng vận chuyển & bộ dữ liệu mẫu 20k–50k sản phẩm**: `payment` đã có `RunReconciliation` và hàng đợi `reconciliation_queue` (UC-A14, UC-PAY07); sinh dữ liệu tải lớn thuộc giai đoạn Tháng 5 cùng k6.

---

## 6. Mọi chỗ lệch khỏi đặc tả kèm lý do

| # | Điểm lệch so với đặc tả gốc | Lý do kỹ thuật |
| --- | --- | --- |
| 1 | `POST /api/v1/payments/{id}/retry` chặn đổi trực tiếp `VIETQR -> COD` (trả `409 Conflict`, chỉ cho retry cùng phương thức `VIETQR` khi đang `PENDING`/`FAILED`). | Tránh vượt qua kiểm soát rủi ro COD (hạn mức tối đa, số lần bom hàng UC-PAY02) nằm ở `OrderService` và tránh làm lệch `orders.payment_method` giữa `payment_db` và `order_db`. |
| 2 | Cấu hình mặc định chạy ở chế độ `VIETQR_MODE=mock` và `GHN_MODE=mock` trong `.env.example` và `docker-compose.yml`. | Đảm bảo không commit secret thật vào repo và `docker compose up` + `make e2e` chạy hoàn toàn offline/ổn định; adapter thật cho payOS/SePay và GHN sandbox đã được cài sẵn (có timeout, backoff retry, circuit breaker) và bật qua biến môi trường. |
| 3 | Dữ liệu địa giới hành chính 2 cấp (`shipping_db`) seed đủ **34 tỉnh/thành** mới, phường/xã đại diện cho từng tỉnh và bảng ánh xạ **63 tỉnh cũ -> 34 tỉnh mới** thay vì nạp toàn bộ ~10.000 phường/xã toàn quốc. | Đáp ứng đầy đủ hợp đồng API 2 cấp và kiểm chứng ánh xạ cũ-mới trong khi giữ migration gọn nhẹ, khởi động nhanh trong môi trường dev/CI. |
| 4 | Kịch bản kiểm thử 1.000 yêu cầu đồng thời cho 100 suất flash sale (`TestE2E_FlashSale_1000Concurrent_100Slots`) gọi trực tiếp gRPC `PromotionService.ReservePromotion`, kèm thêm subtest checkout thật qua Gateway ở giá flash sale. | Nếu đẩy 1.000 request từ cùng 1 IP qua Gateway, bộ giới hạn `RATE_LIMIT_IP_PER_MIN=300` sẽ chặn bằng `429` trước khi tới Redis Lua; tách kiểm chứng concurrency trực tiếp lên `PromotionService` giúp chứng minh chính xác 100/1000 không bán vượt suất, đồng thời vẫn kiểm chứng luồng REST checkout flash sale end-to-end. |
| 5 | Toàn bộ cổng host trong `docker-compose.yml` được tham số hóa qua biến môi trường (giữ nguyên giá trị mặc định khớp đặc tả). | Cho phép khởi động sạch trên các máy dev dùng chung có tiến trình khác chiếm sẵn cổng (ví dụ `6379`, `4318`, `8080`, `8082`, `8083`, `8085`) mà không phá vỡ cấu hình mặc định trên máy sạch. |
| 6 | Bộ E2E ở `tests/e2e/` là Go module riêng chạy qua `make e2e` thay vì nằm chung trong `make test`. | `make test` chạy unit test độc lập không cần hạ tầng live (chạy nhanh trong CI), còn `make e2e` chạy kiểm thử đầu-cuối trên toàn bộ stack Docker Compose đang hoạt động; cả hai đều được kiểm tra sạch `gofmt` và `golangci-lint`. |
