---
paths:
  - "services/payment/**"
---

# Quy tắc dịch vụ Payment

- **Tiền tệ**: mọi số tiền là số nguyên `i64` (`BIGINT` trong Postgres), đơn vị VND, đặt tên trường `*_vnd`; cấm dùng số thực (`f32`/`f64`).
- **Sổ cái ghi kép bất biến**: mọi bút toán phải có ≥ 2 dòng, mỗi dòng chỉ dương ở `debit_vnd` hoặc `credit_vnd`, tổng `debit_vnd = credit_vnd > 0` (`LedgerTransactionDraft::validate` trong `services/payment/src/ledger.rs`, và trigger `trg_ledger_entries_balanced` trong `services/payment/migrations/0001_init.sql`). Không `UPDATE`/`DELETE` trên `ledger_transactions` hay `ledger_entries` (bị chặn bởi `prevent_ledger_mutation`).
- **Xác minh webhook**: bắt buộc kiểm `X-Signature` bằng HMAC-SHA256 so sánh thời gian hằng (`hmac::Mac::verify_slice`) và bắt buộc `X-Timestamp` lệch tối đa 300 giây (`verify_webhook_request` trong `services/payment/src/webhook_security.rs`).
- **Idempotency**:
  - Webhook chống trùng bằng `webhook_events.provider_tx_id` (`UNIQUE` + `ON CONFLICT DO NOTHING` trong `PaymentService::process_verified_webhook`, `services/payment/src/service.rs`).
  - Hoàn tiền bắt buộc có `Idempotency-Key` (gateway kiểm ở `services/gateway/internal/router/handlers_payment.go`, DB ép `UNIQUE` trên `refunds.idempotency_key`, kiểm tra trong `PaymentService::create_refund`); tổng hoàn không vượt quá số đã trả và phần phân bổ đơn con (`validate_suborder_refund_cap` trong `services/payment/src/domain.rs`).
- **Đối soát**: kích hoạt qua gRPC `RunReconciliation` (`services/payment/src/grpc.rs`), `PaymentService::run_reconciliation` (`services/payment/src/service.rs`) và worker định kỳ `run_reconciliation_scheduler` (`services/payment/src/workers.rs`). Kết quả ghi vào `reconciliation_runs`/`reconciliation_items`; sai lệch tiền hoặc thiếu khớp phải đẩy vào `reconciliation_queue` để xử lý thủ công, không tự xác nhận thanh toán.
- **Không log bí mật và PII thô**: `Config::Debug` (`services/payment/src/config.rs`) đã che secret và DSN (`[REDACTED]`), nhưng `tracing` JSON logger (`services/payment/src/telemetry.rs`) không tự lọc trường log — bắt buộc dùng `mask_secret`, `mask_bank_account`, `sanitize_headers` và cấm log thô số thẻ, token, secret, chữ ký hay `DATABASE_URL`.
- **Gọi provider ngoài**: mọi lời gọi HTTP ra ngoài phải đi qua `VietQrAdapter::execute_with_retry` (`services/payment/src/vietqr.rs`) có `reqwest` timeout (`EXTERNAL_HTTP_TIMEOUT_SECS`), retry exponential backoff (`EXTERNAL_MAX_RETRIES`) và `CircuitBreaker`. Chuyển đổi `mock` (mặc định) / `payos` / `sepay` qua biến môi trường `VIETQR_MODE`.
