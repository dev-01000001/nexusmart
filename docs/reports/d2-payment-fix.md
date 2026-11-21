# Swarm D2 – Sửa lỗ hổng callback ZaloPay & Webhook Production Guard (`d2-payment-fix`)

- **Ngày hoàn thành:** `2026-11-21`
- **Brief tham chiếu:** `.agents/briefs/m-d2-payment-fix.md`, `.agents/briefs/m-all-contract.md`, `docs/reports/d-order-depth.md`
- **Phạm vi sở hữu đã sửa:** chỉ trong `services/payment/`, `deploy/helm/payment/`, và báo cáo này (`docs/reports/d2-payment-fix.md`). Không chạm bất kỳ thư mục nào của các swarm song song (`A`, `E`, `H`, `G2`, `K`).

---

## 1. Phân tích nguyên nhân gốc & phạm vi kiểm tra

1. **ZaloPay callback (`deploy/helm/payment/values-production.yaml`, `services/payment/src/config.rs`, `services/payment/src/zalopay.rs`):**
   - `deploy/helm/payment/values-production.yaml` không khai báo `APP_ENV` lẫn `ZALOPAY_MODE`, nên `Config::from_env()` mặc định `ZALOPAY_MODE=mock`.
   - `ZaloPayAdapter::from_config()` âm thầm fallback sang `ZaloPayMode::Mock` và nạp hằng trong repo (`zalopay-mock-key1-dev` / `zalopay-mock-key2-dev`) khi key rỗng hoặc là placeholder.
   - `is_placeholder_secret()` chưa nhận diện các hằng/secret mẫu đã commit trong repo (`zalopay-mock-key1-dev`, `zalopay-mock-key2-dev`, `dev-only-payment-webhook-hmac-secret`, `sandbox-checksum-key`, `sandbox-sepay-token`, v.v.).
2. **Kiểm tra cùng mẫu lỗi trên VietQR webhook & webhook chung (`webhook_security.rs`, `http.rs`, `config.rs`, `values-production.yaml`):**
   - **Phát hiện 1 (`values-production.yaml:21`):** đặt `VIETQR_MODE: "sandbox"`, nhưng `VietQrMode::parse()` chỉ nhận `"payos"` hoặc `"sepay"` và mặc định `_ => VietQrMode::Mock` — nên cấu hình production âm thầm chạy VietQR ở chế độ `mock`.
   - **Phát hiện 2 (`config.rs:163`):** `PAYMENT_WEBHOOK_SECRET` mặc định `"dev-webhook-secret-change-me"`, còn `.env.example` / `docker-compose.yml` dùng `"dev-only-payment-webhook-hmac-secret"`. Cả `/webhooks/payment` và `/webhooks/vietqr` đều công khai qua API gateway và dùng khóa này để xác thực HMAC — cùng lỗ hổng giả mạo thanh toán nếu triển khai production mà thiếu/nhầm secret.
   - **Phát hiện 3 (`webhook_security.rs:24`, `zalopay.rs:111`):** `hmac::Mac::new_from_slice` chấp nhận khóa rỗng `""`, nên `verify_webhook_request("", ...)` và `verify_zalopay_callback("", ...)` không chặn tường minh trường hợp secret rỗng.

---

## 2. Thay đổi đã thực hiện

### 2.1 `services/payment/`
- **`services/payment/src/config.rs`:**
  - Thêm enum `AppEnv` (`Development`, `Staging`, `Production`) đọc từ `APP_ENV` (mặc định `development` để giữ nguyên 100% tương thích `docker-compose.yml`, mock cục bộ và `tests/e2e`).
  - Xuất hằng dùng chung `DEFAULT_WEBHOOK_SECRET`, `DEFAULT_ZALOPAY_MOCK_KEY1`, `DEFAULT_ZALOPAY_MOCK_KEY2`; mở rộng `is_placeholder_secret()` nhận diện toàn bộ hằng/secret dev/sandbox đã commit trong repo (`KNOWN_REPO_SECRETS`, tiền tố `dev-only-`, cùng các mẫu placeholder sẵn có).
  - Trong `Config::from_env()`, ở môi trường `Production` không bao giờ nạp mặc định `DEFAULT_WEBHOOK_SECRET`.
  - Bổ sung `Config::validate()` (fail-fast khi `APP_ENV=production|prod`): từ chối khởi động nếu `ZALOPAY_MODE=mock`, `VIETQR_MODE=mock`, hoặc bất kỳ bí mật nào (`ZALOPAY_KEY1`, `ZALOPAY_KEY2`, `PAYMENT_WEBHOOK_SECRET`, credential của `payos`/`sepay`, `JWT_SECRET`, `SERVICE_JWT_SECRET`) là rỗng/placeholder/hằng repo, hoặc `JWT_SECRET == SERVICE_JWT_SECRET`.
- **`services/payment/src/main.rs`:**
  - Gọi `config.validate()` ngay sau `init_telemetry` và ghi log JSON lỗi rồi thoát mã khác `0` trước khi kết nối DB hoặc mở cổng.
- **`services/payment/src/error.rs`:**
  - Thêm biến thể `PaymentError::ServiceUnavailable(String)` ánh xạ sang HTTP `503 Service Unavailable` (`SERVICE_UNAVAILABLE`) và gRPC `Unavailable`.
- **`services/payment/src/webhook_security.rs`:**
  - Thêm guard dùng chung `ensure_webhook_secret_usable(secret, is_production)`: trả `503` nếu production dùng secret rỗng/placeholder/hằng repo; trả `401` nếu secret rỗng ở mọi môi trường.
  - Chặn `secret.trim().is_empty()` và timestamp `<= 0` trực tiếp trong `verify_webhook_request()`.
- **`services/payment/src/zalopay.rs`:**
  - Dùng hằng chung từ `config.rs`; trong `ZaloPayAdapter::from_config()` không bao giờ fallback sang `DEFAULT_ZALOPAY_MOCK_KEY1`/`DEFAULT_ZALOPAY_MOCK_KEY2` khi `is_production == true`.
  - Chặn `key2.trim().is_empty()` trong `verify_zalopay_callback()`.
  - Defense-in-depth trong `ZaloPayAdapter::verify_callback()`: nếu production mà `mode != Sandbox` hoặc `key2` là placeholder/hằng repo → trả `503 Service Unavailable` (ngay cả khi khởi tạo trực tiếp không qua `main.rs`). Với production hợp lệ (`Sandbox` + key thật), callback ký bằng `zalopay-mock-key2-dev` bị từ chối `401 Unauthorized`.
- **`services/payment/src/http.rs`:**
  - Áp dụng `ensure_webhook_secret_usable` trên cả `/webhooks/payment` và `/webhooks/vietqr`, và chặn `/webhooks/vietqr` trả `503` nếu production ở `VietQrMode::Mock`.
  - Ghi nhận metric `rejected_misconfigured` cho lỗi `503` ở cả 3 route webhook.
- **Kiểm thử bổ sung:**
  - `src/config.rs`: `test_is_placeholder_secret`, `test_app_env_and_production_config_validation`
  - `src/webhook_security.rs`: `test_empty_secret_and_production_placeholder_guard`
  - `src/zalopay.rs`: `test_production_rejects_mock_key_callback_while_dev_accepts`
  - `src/http.rs`: `test_production_http_webhooks_reject_repo_mock_keys_while_dev_accepts`
  - `src/error.rs`: bổ sung kiểm tra `ServiceUnavailable` -> `503`
  - `tests/zalopay_and_lifecycle_db_integration.rs`: `test_pg_production_rejects_mock_key_callback_while_dev_confirms` (xác minh end-to-end với PostgreSQL 17 thật: production từ chối callback ký bằng `zalopay-mock-key2-dev` / `dev-only-payment-webhook-hmac-secret` với 401/503 và giữ nguyên trạng thái `pending`, trong khi cấu hình dev vẫn nhận `200 OK` và xác nhận thanh toán `succeeded`).

### 2.2 `deploy/helm/payment/`
- `deploy/helm/payment/values-production.yaml`: đặt `APP_ENV: "production"`, `VIETQR_MODE: "payos"` (sửa giá trị `"sandbox"` vốn bị parse thành `mock`), `ZALOPAY_MODE: "sandbox"`, kèm chú thích danh sách secret bắt buộc trong `nexusmart-payment-secret`.
- `deploy/helm/payment/values.yaml`: khai báo tường minh `APP_ENV: "development"`, `ZALOPAY_MODE: "mock"`.
- `deploy/helm/payment/values-staging.yaml`: khai báo tường minh `APP_ENV: "staging"`, `ZALOPAY_MODE: "mock"`.

---

## 3. Lệnh kiểm tra và đầu ra thật

### 3.1 Rust format & lint (`cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`)
```text
$ (cd services/payment && cargo fmt --check)
(exit 0)

$ (cd services/payment && cargo clippy --all-targets -- -D warnings)
    Finished `dev` profile [unoptimized + debuginfo] target(s) in 0.64s
```

### 3.2 Xác minh binary thực từ chối khởi động khi cấu hình production không an toàn
```text
$ APP_ENV=production ./services/payment/target/debug/nexusmart-payment
{"timestamp":"2026-11-21T09:13:49.784979Z","level":"ERROR","message":"refusing to start NexusMart Payment service with unsafe production configuration","service":"payment","error":"ZALOPAY_MODE must be 'sandbox' (mock mode is forbidden in production)","target":"nexusmart_payment"}
Error: ZALOPAY_MODE must be 'sandbox' (mock mode is forbidden in production)
exit_code=1

$ APP_ENV=production ZALOPAY_MODE=sandbox ZALOPAY_KEY1=zalopay-mock-key1-dev ZALOPAY_KEY2=zalopay-mock-key2-dev ./services/payment/target/debug/nexusmart-payment
{"timestamp":"2026-11-21T09:13:49.788038Z","level":"ERROR","message":"refusing to start NexusMart Payment service with unsafe production configuration","service":"payment","error":"ZALOPAY_KEY1 must be set to a non-placeholder secret in production","target":"nexusmart_payment"}
Error: ZALOPAY_KEY1 must be set to a non-placeholder secret in production
exit_code=1
```

### 3.3 `cargo test` (unit + PostgreSQL 17 integration thật trên container tạm `d2-test-pg`, đã xóa sạch sau khi chạy)
```text
$ (cd services/payment && cargo test)
running 61 tests
...
test config::tests::test_app_env_and_production_config_validation ... ok
test http::tests::test_production_http_webhooks_reject_repo_mock_keys_while_dev_accepts ... ok
test webhook_security::tests::test_empty_secret_and_production_placeholder_guard ... ok
test zalopay::tests::test_production_rejects_mock_key_callback_while_dev_accepts ... ok
...
test result: ok. 61 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.06s

$ (cd services/payment && PAYMENT_TEST_DATABASE_URL="postgres://nexusmart:***@127.0.0.1:<port>/postgres" cargo test)
     Running unittests src/lib.rs
test result: ok. 61 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.06s

     Running tests/sales_channel_db_integration.rs
test test_pg_migration_immutability_and_channel_filtered_ledger_and_reconciliation ... ok
test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.24s

     Running tests/zalopay_and_lifecycle_db_integration.rs
test test_pg_production_rejects_mock_key_callback_while_dev_confirms ... ok
test test_pg_zalopay_full_lifecycle_refunds_seller_balance_and_reconciliation_reports ... ok
test result: ok. 2 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.59s
```

### 3.4 Helm lint & `kubeconform`
```text
$ helm lint deploy/helm/payment
==> Linting deploy/helm/payment
[INFO] Chart.yaml: icon is recommended

1 chart(s) linted, 0 chart(s) failed

$ helm lint --strict deploy/helm/payment -f deploy/helm/payment/values-production.yaml
==> Linting deploy/helm/payment
[INFO] Chart.yaml: icon is recommended

1 chart(s) linted, 0 chart(s) failed

$ helm lint --strict deploy/helm/payment -f deploy/helm/payment/values-staging.yaml
==> Linting deploy/helm/payment
[INFO] Chart.yaml: icon is recommended

1 chart(s) linted, 0 chart(s) failed

$ helm template payment deploy/helm/payment -f deploy/helm/payment/values-production.yaml | kubeconform -strict -ignore-missing-schemas -summary
Summary: 5 resources found parsing stdin - Valid: 5, Invalid: 0, Errors: 0, Skipped: 0

$ helm template payment deploy/helm/payment --namespace nexusmart-staging -f deploy/helm/payment/values-staging.yaml | kubeconform -strict -summary -kubernetes-version 1.31.0
Summary: 4 resources found parsing stdin - Valid: 4, Invalid: 0, Errors: 0, Skipped: 0
```

Rendered `ConfigMap` với `values-production.yaml`:
```yaml
data:
  APP_ENV: "production"
  GRPC_PORT: "50056"
  HTTP_PORT: "8086"
  NATS_STREAM: "NEXUSMART"
  NATS_URL: "nats://nats:4222"
  OTEL_EXPORTER_OTLP_ENDPOINT: "otel-collector:4317"
  OTEL_EXPORTER_OTLP_INSECURE: "true"
  PAYMENT_EXPIRY_MINUTES: "15"
  RUST_LOG: "info"
  SERVICE_NAME: "payment"
  VIETQR_MODE: "payos"
  ZALOPAY_MODE: "sandbox"
```

---

## 4. Tương thích & Ghi chú ngoài phạm vi sở hữu

1. **Tương thích `docker-compose.yml` và `tests/e2e`:** Không đổi `docker-compose.yml` hay `tests/e2e`. Khi `APP_ENV` không đặt (mặc định `development`), `ZALOPAY_MODE=mock`, `VIETQR_MODE=mock`, `zalopay-mock-key2-dev` và `dev-only-payment-webhook-hmac-secret` tiếp tục hoạt động bình thường. (Không chạy `make e2e` hoặc rebuild stack theo luật `m-all-contract.md` — phần đó thuộc Swarm H.)
2. **Ghi chú ngoài phạm vi (`deploy/k8s/create-secrets.sh`, thuộc Swarm F):** `deploy/k8s/create-secrets.sh:141-149` hiện tạo `nexusmart-payment-secret` với `DATABASE_URL`, `JWT_SECRET`, `SERVICE_JWT_SECRET`, `PAYMENT_WEBHOOK_SECRET`, `PAYOS_*`, `SEPAY_API_TOKEN`, nhưng **chưa nạp `ZALOPAY_KEY1` và `ZALOPAY_KEY2`**, và đang dùng `optional_env` cho `PAYOS_*`/`SEPAY_API_TOKEN`. Khi triển khai `nexusmart-production`, Swarm F / operator cần bổ sung `ZALOPAY_KEY1`, `ZALOPAY_KEY2` và yêu cầu bắt buộc bộ key tương ứng `VIETQR_MODE` trong `deploy/k8s/create-secrets.sh` (D2 không sửa file này vì nằm ngoài phạm vi sở hữu).
