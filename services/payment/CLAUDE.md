# Payment Service (`services/payment`)

Rust (edition 2021, crate `nexusmart-payment`) với Axum (HTTP `:8086`), Tonic (gRPC `:50056`), SQLx (`payment_db` trên PostgreSQL), Tokio. Dockerfile multi-stage (`services/payment/Dockerfile`) build từ root repo (copy `proto/`, `pkg/common/auth/capabilities.json`, `services/payment/`), chạy `USER 10001:10001`.

## Lệnh kiểm tra

```bash
cd services/payment
cargo fmt --all -- --check
cargo clippy --all-targets -- -D warnings
cargo test                # toàn bộ unit test (không cần Postgres/NATS; integration test tự skip khi chưa có TEST_PAYMENT_DB_URL)
cargo test <tên_test>     # chạy một test, ví dụ: cargo test test_hmac_verification
```

Unit test không cần DB thật (`http.rs` và `grpc.rs` dùng `PgPoolOptions::connect_lazy`, `ledger.rs` test trên `InMemoryLedger`, `zalopay_tests.rs` test mock/loopback). Bộ test tích hợp PostgreSQL thật (`tests/*.rs` và `workers.rs`) đọc `TEST_PAYMENT_DB_URL` (một số file phase-0b cũng nhận fallback từ `PAYMENT_TEST_DATABASE_URL`) và chạy đầy đủ với 0 skip qua `make test-db` (`scripts/test-db.sh`) ở gốc repo. Service dùng `sqlx::query*` runtime (không dùng macro `query!`), nên `cargo build`/`cargo test` không cần DB online.

## Proto, Migration và Auth

- **Proto**: `build.rs` biên dịch `../../proto/nexusmart/payment/v1/payment.proto` bằng `tonic-build`, tự tìm `protoc` ở `~/.local/bin/protoc` và include `~/.local/include` (hoặc `PROTOC`/`PROTOC_INCLUDE`). Đổi hợp đồng: sửa `proto/`, chạy `make proto` ở root cho Go, chạy `cargo build` ở `services/payment` cho Rust.
- **Migration**: `sqlx::migrate!("./migrations").run(&pool)` chạy tự động trong `src/main.rs` khi khởi động (`migrations/0001_init.sql` … `0018_backfill_phase0b_zalopay_refund_settle.sql`, trong đó `0001..0003` giữ nguyên từng byte từ `phase-0b` và `0004..0018` là chuỗi migration của `main`/`3b-3` theo [ADR-0010](../../docs/adr/0010-unify-main-and-phase-0b.md)). SQLx kiểm checksum nên không sửa file đã chạy; đổi schema bằng file mới đánh số kế tiếp (`0019_...`). Lưu ý `0005_refund_settlement_and_reconciliation.sql` đưa các khoản hoàn VietQR cũ chưa có `provider_refund_id` về `pending` (cần rà tay khi nâng cấp từ `phase-0b`, xem `docs/UPGRADE_FROM_PHASE_0B.md`), còn `0018_backfill_phase0b_zalopay_refund_settle.sql` tự động bù idempotent bút toán `refund_settle` cho các khoản hoàn ZaloPay đã chi ở `phase-0b`.
- **Auth**: `src/auth.rs` (`GrpcAuth::authenticate` / `authenticate_optional`) xác minh HS256 JWT người dùng (`JWT_SECRET`) và token nội bộ (`SERVICE_JWT_SECRET`, chỉ cho phép `claims.service == "order-service"` trên 7 RPC trong `SERVICE_METHOD_ALLOWLIST`: `CreatePayment`, `GetPayment`, `CancelPayment`, `CreateRefund`, `GetFeeConfig`, `GetPaymentMethodConfigs`, `AdjustCODShippingFee`), tương ứng `pkg/common/auth` phía Go. Riêng `AdjustCODShippingFee` là service-token-only (`order-service` duy nhất; mọi JWT người dùng kể cả `admin`/`super_admin` đều bị từ chối `PermissionDenied`). Quyền quản trị theo capability đọc trực tiếp từ `pkg/common/auth/capabilities.json` (token service có 0 capability); các RPC vòng đời thanh toán ngoài bảng §4.11 (`CreatePayment`, `CancelPayment`) chỉ cho phép chủ sở hữu, `admin`/`super_admin` hoặc token `order-service`. Riêng `CreatePayment` khi gọi bằng token service (phục hồi tiến từ watchdog `order`) bị siết thêm trong `src/grpc.rs` (`validate_service_create_payment`): bắt buộc `user_id` và `order_group_id` không rỗng, `idempotency_key == "checkout:" + order_group_id` (`InvalidArgument`), cấm `supersede_previous = true` (`PermissionDenied`). Metadata `x-user-*` không được tin. Handler SSE `GET /payments/{id}/events` dùng chung `GrpcAuth` (`authenticate_header`, từ chối token service) và `resolve_payment_read_scope`; các route HTTP `/internal/*` cũng dùng `GrpcAuth` và kiểm capability theo `capabilities.json` (từ chối token service): `ledger.read` cho `/internal/ledger/summary`, `/internal/reconciliation/queue`, `/internal/reconciliation/reports`; `reconciliation.write` cho `/internal/reconciliation/run`.

## Cơ chế lõi

- **Sổ cái ghi kép (`src/ledger.rs`)**: tiền `i64` VND; `LedgerTransactionDraft::validate` và `post_transaction` đảm bảo mỗi giao dịch cân bằng debit = credit, kết hợp trigger DB `trg_ledger_entries_balanced` và `trg_ledger_*_immutable` (`migrations/0001_init.sql`). Hỗ trợ gắn và lọc `sales_channel` (`STOREFRONT`/`MARKETPLACE`).
- **Webhook (`src/webhook_security.rs`, `src/zalopay.rs`)**: `verify_webhook_request` kiểm `X-Signature` (HMAC-SHA256 constant-time qua `verify_slice`) và bắt buộc `X-Timestamp` lệch ≤ 300 giây; `/webhooks/zalopay` xác minh MAC bằng `ZALOPAY_KEY2` và kiểm `server_time` (lệch > 300s gọi `/v2/query` xác thực lại); `decide_webhook_action` quyết định xác nhận, hoàn tiền trễ hay đẩy vào `reconciliation_queue`.
- **Workers (`src/workers.rs`)**: 6 tác vụ nền Tokio — `run_outbox_relay` (`outbox_events` lease-based `FOR UPDATE SKIP LOCKED` → NATS `NEXUSMART`), `run_expiration_scanner` (`scan_expired_payments` + ZaloPay query & late-payment sweep), `run_reconciliation_scheduler` (`run_reconciliation` + dọn run COD treo), `run_escrow_hold_releaser` (chuyển `pending_release → released` sau `PAYMENT_SELLER_HOLD_HOURS`), `run_zalopay_refund_poller` (CAS lease dispatch/poll `/v2/refund` & `/v2/query_refund`), `run_nats_order_consumer` (idempotent qua `processed_nats_events` cho `order.{completed,cancelled,delivered,returned}`, retry 5 lần → `NEXUSMART_DLQ` + `dead_letter_events`).
- **VietQR (`src/vietqr.rs`) & ZaloPay (`src/zalopay.rs`)**: `VietQrAdapter` hỗ trợ `VIETQR_MODE=mock|payos|sepay`; `ZaloPayAdapter` hỗ trợ `ZALOPAY_MODE=mock|sandbox` (live mode ngoài phạm vi đợt này); gọi ngoài đi qua `execute_with_retry` / `execute_http_json_with_retry` có timeout `reqwest`, exponential backoff và `CircuitBreaker`.

## Bố cục `src/`

- `src/lib.rs` — xuất 14 module công khai của crate `nexusmart-payment`.
- `src/main.rs` — khởi tạo telemetry, mở `PgPool`, chạy migration, spawn 6 worker, mở HTTP `:8086` và gRPC `:50056`.
- `src/config.rs` — đọc `Config::from_env()`, enum `VietQrMode` / `ZaloPayMode` / `AppEnv`, `Debug` tự che secret/DSN (`[REDACTED]`).
- `src/auth.rs` — xác minh Bearer HS256 JWT người dùng, bảng `capabilities.json` và token service theo `SERVICE_METHOD_ALLOWLIST`.
- `src/domain.rs` — kiểu dữ liệu miền, `sqlx::FromRow` structs, phân bổ/giới hạn hoàn tiền, phân loại đối soát và chuẩn hóa `sales_channel`.
- `src/error.rs` — `PaymentError` ánh xạ sang HTTP JSON (không lộ lỗi DB) và `tonic::Status`.
- `src/grpc.rs` — `PaymentGrpcServer` triển khai 23 RPC của `PaymentService`, kiểm quyền sở hữu/capability và ghi RED metrics.
- `src/http.rs` — `build_router`: `/healthz`, `/readyz`, `/metrics`, `/webhooks/{payment,vietqr,zalopay}`, SSE `/payments/{id}/events`, `/internal/{ledger/summary,reconciliation/queue,reconciliation/run,reconciliation/reports}`.
- `src/ledger.rs` — sổ cái ghi kép, các hàm builder/post/truy vấn số dư (`get_ledger_summary`, `get_seller_balance`, `list_seller_ledger`) và `InMemoryLedger`.
- `src/metrics.rs` — đăng ký Prometheus RED metrics, `payment_webhooks_total`, `payment_ledger_transactions_total` và `red_metrics_middleware`.
- `src/service.rs` — `PaymentService`: luồng DB chính (`create_payment`, `process_verified_webhook`, `create_refund`, `decide_refund_approval`, `settle_refund`, `adjust_cod_shipping_fee`, `import_cod_reconciliation`, `release_escrow`, `run_reconciliation`, `scan_expired_payments`, xử lý event đơn).
- `src/telemetry.rs` — `init_telemetry` (JSON tracing + OTLP), `extract_or_generate_trace_id`, hàm che dữ liệu (`mask_secret`, `mask_phone`, `mask_email`, `mask_bank_account`, `sanitize_headers`).
- `src/vietqr.rs` — `VietQrAdapter` (`mock|payos|sepay`), `CircuitBreaker` và `execute_with_retry`.
- `src/zalopay.rs` (+ `src/zalopay_tests.rs`) — `ZaloPayAdapter` (`mock|sandbox`), `MockZaloPay`, xác minh MAC callback, `/v2/{create,query,refund,query_refund}`.
- `src/webhook_security.rs` — xác minh chữ ký/replay webhook, parse `IncomingWebhookPayload`, `decide_webhook_action`.
- `src/workers.rs` — 6 worker nền: hết hạn/quét trễ ZaloPay, outbox relay, đối soát định kỳ, giải phóng escrow, poll hoàn tiền ZaloPay và NATS consumer `order.{completed,cancelled,delivered,returned}`.
