# Payment Service (`services/payment`)

Rust (edition 2021) với Axum (HTTP `:8086`), Tonic (gRPC `:50056`), SQLx (`payment_db` trên PostgreSQL), Tokio. Dockerfile multi-stage (`services/payment/Dockerfile`) build từ root repo, chạy `USER 10001:10001`.

## Lệnh kiểm tra

```bash
cd services/payment
cargo fmt --all -- --check
cargo clippy --all-targets -- -D warnings
cargo test                # toàn bộ unit test (không cần Postgres/NATS)
cargo test <tên_test>     # chạy một test, ví dụ: cargo test test_hmac_verification
```

Unit test không cần DB thật: `http.rs` và `grpc.rs` dùng `PgPoolOptions::connect_lazy`, `ledger.rs` test trên `InMemoryLedger`, `service.rs` chưa có unit test (tích hợp chạy qua `make e2e` ở root). Service dùng `sqlx::query*` runtime, không dùng macro `query!`, nên `cargo build`/`cargo test` không cần DB online.

## Proto, Migration và Auth

- **Proto**: `build.rs` biên dịch `../../proto/nexusmart/payment/v1/payment.proto` bằng `tonic-build`, tự tìm `protoc` ở `~/.local/bin/protoc` và include `~/.local/include` (hoặc `PROTOC`/`PROTOC_INCLUDE`). Đổi hợp đồng: sửa `proto/`, chạy `make proto` ở root cho Go, chạy `cargo build` ở `services/payment` cho Rust.
- **Migration**: `sqlx::migrate!("./migrations").run(&pool)` chạy tự động trong `src/main.rs` khi khởi động (`migrations/0001_init.sql`). SQLx kiểm checksum nên không sửa file đã chạy; đổi schema bằng file mới đánh số `0002_...`.
- **Auth**: `src/auth.rs` (`GrpcAuth::authenticate`) xác minh HS256 JWT người dùng (`JWT_SECRET`) và token nội bộ (`SERVICE_JWT_SECRET`, chỉ cho phép `claims.service == "order-service"` trên 6 RPC trong `SERVICE_METHOD_ALLOWLIST`: `CreatePayment`, `GetPayment`, `CancelPayment`, `CreateRefund`, `GetFeeConfig`, `GetPaymentMethodConfigs`), tương ứng `pkg/common/auth` phía Go. Quyền quản trị theo capability đọc trực tiếp từ `pkg/common/auth/capabilities.json` (token service có 0 capability); các RPC vòng đời thanh toán ngoài bảng §4.11 (`CreatePayment`, `CancelPayment`) chỉ cho phép chủ sở hữu, `admin`/`super_admin` hoặc token `order-service`. Riêng `CreatePayment` khi gọi bằng token service (phục hồi tiến từ watchdog `order`) bị siết thêm trong `src/grpc.rs` (`validate_service_create_payment`): bắt buộc `user_id` và `order_group_id` không rỗng, `idempotency_key == "checkout:" + order_group_id` (`InvalidArgument`), cấm `supersede_previous = true` (`PermissionDenied`). Metadata `x-user-*` không được tin. Handler SSE `GET /payments/{id}/events` dùng chung `GrpcAuth` (`authenticate_header`, từ chối token service) và `resolve_payment_read_scope` để kiểm tra JWT người dùng cùng quyền đọc payment trước khi mở stream; các route HTTP `/internal/*` cũng dùng `GrpcAuth` và kiểm capability theo `capabilities.json` (từ chối token service): `ledger.read` cho `/internal/ledger/summary`, `/internal/reconciliation/queue`; `reconciliation.write` cho `/internal/reconciliation/run`.

## Cơ chế lõi

- **Sổ cái ghi kép (`src/ledger.rs`)**: tiền `i64` VND; `LedgerTransactionDraft::validate` và `post_transaction` đảm bảo mỗi giao dịch cân bằng debit = credit, kết hợp trigger DB `trg_ledger_entries_balanced` và `trg_ledger_*_immutable` (`migrations/0001_init.sql`). Builder: `build_vietqr_confirm_tx`, `build_cod_confirm_tx`, `build_refund_tx`, `build_escrow_settle_tx`, `build_escrow_release_tx`, `build_seller_payout_tx`.
- **Webhook (`src/webhook_security.rs`)**: `verify_webhook_request` kiểm `X-Signature` (HMAC-SHA256 constant-time qua `verify_slice`) và bắt buộc `X-Timestamp` lệch ≤ 300 giây; `decide_webhook_action` quyết định xác nhận, hoàn tiền trễ hay đẩy vào `reconciliation_queue`.
- **Workers (`src/workers.rs`)**: 4 tác vụ nền Tokio — `run_outbox_relay` (`outbox_events` với `FOR UPDATE SKIP LOCKED` → NATS `NEXUSMART`), `run_expiration_scanner` (`scan_expired_payments`), `run_reconciliation_scheduler` (`run_reconciliation`), `run_nats_order_consumer` (idempotent qua `processed_nats_events`, retry 5 lần → `NEXUSMART_DLQ`).
- **VietQR (`src/vietqr.rs`)**: `VietQrAdapter` hỗ trợ `VIETQR_MODE=mock|payos|sepay` (`create_qr_intent`, `check_payment_status`, `list_provider_transactions`); gọi ngoài đi qua `execute_with_retry` có timeout `reqwest`, exponential backoff và `CircuitBreaker`.

## Bố cục `src/`

- `src/lib.rs` — xuất 13 module công khai của crate `nexusmart_payment`.
- `src/main.rs` — khởi tạo telemetry, mở `PgPool`, chạy migration, spawn 4 worker, mở HTTP `:8086` và gRPC `:50056`.
- `src/config.rs` — đọc `Config::from_env()`, enum `VietQrMode`, `Debug` tự che secret/DSN (`[REDACTED]`).
- `src/auth.rs` — xác minh Bearer HS256 JWT người dùng và token service theo allowlist `SERVICE_METHODS`.
- `src/domain.rs` — kiểu dữ liệu miền, `sqlx::FromRow` structs, hàm chia phân bổ/giới hạn hoàn tiền (`compute_allocation_split`, `validate_suborder_refund_cap`) và phân loại đối soát (`classify_reconciliation_pair`).
- `src/error.rs` — `PaymentError` ánh xạ sang HTTP JSON (không lộ lỗi DB) và `tonic::Status`.
- `src/grpc.rs` — `PaymentGrpcServer` triển khai 8 RPC của `PaymentService`, kiểm quyền sở hữu/RBAC và ghi RED metrics.
- `src/http.rs` — `build_router`: `/healthz`, `/readyz`, `/metrics`, `/webhooks/{payment,vietqr}`, SSE `/payments/{id}/events`, `/internal/*`.
- `src/ledger.rs` — sổ cái ghi kép, các hàm builder/post/truy vấn số dư (`get_ledger_summary`, `get_seller_balance`) và `InMemoryLedger`.
- `src/metrics.rs` — đăng ký Prometheus RED metrics, `payment_webhooks_total`, `payment_ledger_transactions_total` và `red_metrics_middleware`.
- `src/service.rs` — `PaymentService`: luồng DB chính (`create_payment`, `process_verified_webhook`, `create_refund`, `release_escrow`, `run_reconciliation`, `scan_expired_payments`, xử lý event đơn).
- `src/telemetry.rs` — `init_telemetry` (JSON tracing + OTLP), `extract_or_generate_trace_id`, hàm che dữ liệu (`mask_secret`, `mask_phone`, `mask_email`, `mask_bank_account`, `sanitize_headers`).
- `src/vietqr.rs` — `VietQrAdapter` (`mock|payos|sepay`), `CircuitBreaker` và `execute_with_retry`.
- `src/webhook_security.rs` — xác minh chữ ký/replay webhook, parse `IncomingWebhookPayload`, `decide_webhook_action`.
- `src/workers.rs` — 4 worker nền: hết hạn, outbox relay, đối soát định kỳ và NATS consumer `order.{completed,cancelled}`.
