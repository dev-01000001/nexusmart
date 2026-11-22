---
name: integrate-provider
description: Tích hợp cổng thanh toán (payment/Rust) hoặc hãng vận chuyển (shipping/Go) sau adapter interface có sẵn với chế độ mock mặc định, sandbox qua env, HMAC + X-Timestamp, idempotency DB, circuit breaker/retry, đối soát và gateway proxy. Dùng khi thêm hoặc mở rộng provider thanh toán/vận chuyển.
disable-model-invocation: true
---

# Tích hợp cổng thanh toán hoặc hãng vận chuyển

Đầu vào (`$ARGUMENTS`): `<payment|shipping> <tên-provider>` (ví dụ: `payment payos`, `payment vnpay`, `shipping ghtk`).

Thanh toán đầu tiên giữ COD + VietQR (payOS/SePay mock mặc định); vận chuyển giữ GHN sandbox + mock. Nếu tích hợp cần hạ tầng/OSS mới, chạy skill `evaluate-oss` trước.

## 1. Implement adapter sau interface có sẵn

- [ ] **Shipping (Go)** — theo mẫu `services/shipping/internal/carrier/`:
  - Implement interface `carrier.Carrier` (`carrier.go`: `Code`, `Name`, `IsMock`, `CalculateFee`, `CreateShipment`, `CancelShipment`, `GetStatus`) trong `services/shipping/internal/carrier/<provider>.go` (mẫu `ghn.go`, `mock_express.go`).
  - Map trạng thái raw của hãng về trạng thái chuẩn trong `MapCarrierStatus` (`carrier.go`).
  - Bọc adapter bằng `carrier.NewResilientCarrier(...)` và đăng ký vào `carrier.NewAggregator(...)` trong `services/shipping/cmd/server/main.go`.
- [ ] **Payment (Rust)** — theo mẫu `services/payment/src/vietqr.rs`:
  - Mở rộng `VietQrMode` (`src/config.rs`) và `VietQrAdapter` (`src/vietqr.rs`: `create_qr_intent`, `check_payment_status`, `list_provider_transactions`), hoặc đặt sau trait tương đương.
  - Mọi số tiền là số nguyên `i64` VND; mọi bút toán đi qua sổ cái ghi kép bất biến (`services/payment/src/ledger.rs`).

## 2. Mock mặc định và cấu hình sandbox qua env

- [ ] Mặc định chạy chế độ `mock` deterministic (như `VIETQR_MODE=mock`, `GHN_MODE=mock`) để unit test và `make e2e` chạy không phụ thuộc mạng ngoài.
- [ ] Đọc cấu hình sandbox (`<PROVIDER>_MODE`, `<PROVIDER>_BASE_URL`, `<PROVIDER>_API_KEY`/`_TOKEN`, secret webhook) qua `internal/config/config.go` (Go) hoặc `src/config.rs` (Rust).
- [ ] Khai báo biến mặc định sandbox (giá trị giả) trong `.env.example` và tiêm vào `docker-compose.yml`. **Cấm commit secret thật.**

## 3. Timeout, retry backoff và circuit breaker

- [ ] **Shipping (Go)**: bắt buộc bọc qua `carrier.NewResilientCarrier` (`services/shipping/internal/carrier/resilience.go` — `AttemptTimeout`, exponential backoff có jitter, circuit breaker dùng `github.com/sony/gobreaker/v2`). Đánh dấu lỗi 4xx bằng `&carrier.NonRetryableError{}` để không retry và không mở circuit breaker. Khi tất cả carrier lỗi, `Aggregator.CalculateFeeAll` rơi về `FallbackQuotes` (`fallback.go`).
- [ ] **Payment (Rust)**: dùng `reqwest::Client` có `timeout`, `CircuitBreaker`, và `execute_with_retry` (`services/payment/src/vietqr.rs`: backoff mũ, không retry 4xx trừ HTTP 429).

## 4. Bảo mật webhook và idempotency ở DB

- [ ] **Xác thực chữ ký thời gian hằng** trên raw request body (không parse JSON trước khi verify):
  - Payment: `verify_webhook_request` (`services/payment/src/webhook_security.rs`, dùng `hmac::Mac::verify_slice`, hỗ trợ `<hex>` hoặc `sha256=<hex>`, hỗ trợ ký raw body hoặc `"{ts}.{raw_body}"`).
  - Shipping: `VerifySignature` (`services/shipping/internal/webhook/handler.go`, dùng `hmac.Equal`).
  - Thiếu hoặc sai `X-Signature` → trả `401`. Không log raw secret/signature (che bằng `mask_secret` như `services/payment/src/http.rs`).
- [ ] **Chống replay bằng `X-Timestamp`**: bắt buộc có header `X-Timestamp` (Unix seconds), lệch tối đa **300 giây (5 phút)**; thiếu hoặc quá hạn → trả `401`.
- [ ] **Idempotency ở DB (trong cùng transaction xử lý)**:
  - Payment: `INSERT INTO webhook_events (...) ON CONFLICT (provider_tx_id) DO NOTHING RETURNING id` (`provider_tx_id UNIQUE` trong `services/payment/migrations/0001_init.sql`, xử lý ở `process_verified_webhook` trong `services/payment/src/service.rs`). Gửi lặp → trả `200 OK` (`duplicate: true`), không tạo thêm bút toán sổ cái hay outbox event.
  - Shipping: bảng `webhook_events(event_id PRIMARY KEY)` (`services/shipping/migrations/000001_init.sql`, xử lý ở `ApplyWebhookEvent` trong `services/shipping/internal/store/store.go`). Gửi lặp → trả `200 OK` (`outcome: "duplicate"`).

## 5. Đối soát và route webhook qua Gateway

- [ ] **Đối soát**:
  - Payment: nối `check_payment_status` và `list_provider_transactions` (`src/vietqr.rs`) vào `run_reconciliation` (`src/service.rs`, bảng `reconciliation_runs`/`reconciliation_items`/`reconciliation_queue`); giữ nguyên quy tắc `decide_webhook_action` (`src/webhook_security.rs`: lệch số tiền → queue đối soát, thanh toán trễ → hoàn tự động).
  - Shipping: implement `GetStatus` để `services/shipping/internal/poller/poller.go` đồng bộ khi thiếu webhook.
- [ ] **Gateway proxy**: dùng `proxy.NewWebhookProxy` (`services/gateway/internal/proxy/proxy.go`, giữ nguyên raw body, `X-Signature`, `X-Timestamp`, `traceparent`). Payment đang proxy `POST /webhooks/payment`, `POST /webhooks/vietqr`; shipping đang proxy `POST /webhooks/shipping/{carrier}` trong `services/gateway/internal/router/router.go`. Nếu thêm đường dẫn mới, gắn qua `NewWebhookProxy`, không đọc lại body ở gateway.

## 6. Test và tài liệu

- [ ] Unit test webhook & resilience: thiếu/sai `X-Signature` → `401`, thiếu hoặc quá hạn `X-Timestamp` (> 300s) → `401`, gửi lặp cùng mã giao dịch/event chỉ ghi 1 lần, retry + circuit breaker chuyển trạng thái đúng (mẫu `services/payment/src/webhook_security.rs`, `services/payment/src/http.rs`, `services/payment/src/vietqr.rs`, `services/shipping/internal/webhook/handler_test.go`, `services/shipping/internal/carrier/carrier_test.go`).
- [ ] E2E: kiểm tra/mở rộng kịch bản mẫu `tests/e2e/05_webhook_security_test.go` (`TestE2E_PaymentWebhookIdempotencyAndSecurity`: chữ ký sai, timestamp cũ, bắn lặp 10 lần đồng thời + tuần tự, sổ cái vẫn cân bằng).
- [ ] Cập nhật `docs/contracts.md` §4.9 (định dạng webhook) và §7 (biến môi trường mới).

## Gate

```bash
make lint
make test
```

Kiểm tra riêng module tích hợp:

```bash
# Payment:
cd services/payment && cargo test webhook && cargo test vietqr

# Shipping:
cd services/shipping && go test -race ./internal/carrier/... ./internal/webhook/...

# E2E webhook (chỉ chạy khi stack docker compose đã sẵn sàng):
GOMODCACHE="$(go env GOMODCACHE)" docker compose --profile e2e run --rm --user "$(id -u):$(id -g)" e2e \
  go test -v -count=1 -run TestE2E_PaymentWebhookIdempotencyAndSecurity ./...
```
