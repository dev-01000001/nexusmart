# 3b-3 — Agent `payment` (A8: G2)

- **Sở hữu**: `services/payment/**`. Đọc `services/payment/CLAUDE.md`, `w2-rules.md`, `w3b-plan.md` §5 (**G2**), `docs/contracts.md`.
- **Cấm**: sửa `proto/`, `pkg/common/`, service khác, docker, `make up|seed|e2e|coverage`, git ghi.
- **Migration**: `services/payment/migrations/0015_w3b3_cod_fee_adjustments.sql` (`sqlx::migrate!` kiểm checksum — không sửa migration cũ): bảng `cod_fee_adjustments(id UUID PK, idempotency_key TEXT UNIQUE NOT NULL, payment_id UUID NOT NULL REFERENCES payments(id), order_id TEXT NOT NULL, old_shipping_fee_vnd BIGINT NOT NULL, new_shipping_fee_vnd BIGINT NOT NULL, new_order_payable_vnd BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`. **KHÔNG** thêm `parent_order_id` hay phụ thu trả trước (G2).

## Phạm vi bắt buộc
1. **Allowlist `services/payment/src/auth.rs`**: thêm `("AdjustCODShippingFee", &["order-service"])` vào `SERVICE_METHOD_ALLOWLIST`. Trong `src/grpc.rs`, **chỉ chấp nhận** `caller.is_service && caller.service == "order-service"`; mọi JWT người dùng cuối (kể cả `buyer`, `seller`, `admin`, `super_admin`) và mọi service khác phải bị từ chối `PermissionDenied`.
2. **RPC `AdjustCODShippingFee` (`src/grpc.rs`, `src/service.rs`, `src/domain.rs`)**:
   - Validate `order_id` không rỗng, `idempotency_key` không rỗng, `new_shipping_fee_vnd >= 0`.
   - Trong một transaction Postgres: kiểm `cod_fee_adjustments` theo `idempotency_key` (nếu đã có cho cùng `order_id` và `new_shipping_fee_vnd` → trả kết quả đã lưu với `already_applied = true`; khác tham số → `FailedPrecondition` xung đột idempotency).
   - Khoá `payments` và tất cả `payment_shop_allocations` của payment đó theo thứ tự `ORDER BY id ASC FOR UPDATE`.
   - Bắt buộc `payment.method == "COD"`, `payment.status == "pending"`, và dòng allocation của `order_id` có `escrow_status == "awaiting_collection"`. Nếu đã thu (`held`/`pending_release`/`released`), đã huỷ, hoặc là `VIETQR`/`ZALOPAY` → từ chối `FailedPrecondition` với tiền tố `COD_ALREADY_COLLECTED`.
   - Tính `new_order_payable_vnd = allocation.subtotal_vnd + new_shipping_fee_vnd - allocation.platform_discount_vnd` (phải `>= 0`, không sửa `subtotal_vnd`, `commission_vnd`, `platform_discount_vnd`). Hỗ trợ cả **tăng và giảm**.
   - Cập nhật `payment_shop_allocations.shipping_fee_vnd = new_shipping_fee_vnd` và tính lại `payments.amount_vnd = SUM(subtotal_vnd + shipping_fee_vnd - platform_discount_vnd)` cho các allocation chưa `cancelled` của payment đó (phải `>= 0`).
   - Ghi `cod_fee_adjustments`, **0 bút toán sổ cái mới** (chưa thu tiền). Khi sau đó `order.delivered` và `order.completed` về, sổ cái ghi đúng số mới và `total_debits_vnd == total_credits_vnd`.

## Tiêu chí đo được & Ca tấn công/đua bắt buộc
- Tấn công quyền: `buyer`, `seller`, `admin`, `super_admin`, và service token `product-service` gọi `AdjustCODShippingFee` → đều `PermissionDenied`.
- Tấn công trạng thái/tiền: gọi trên đơn `VIETQR`/`ZALOPAY`, COD đã `delivered` (`held`), COD đã `cancelled` → `FailedPrecondition` `COD_ALREADY_COLLECTED`; gọi với `new_shipping_fee_vnd < 0` hoặc làm `payable < 0` → `InvalidArgument`/`FailedPrecondition`.
- Đua & toàn vẹn sổ cái: (a) `AdjustCODShippingFee` chạy song song với `order.delivered` → khoá `FOR UPDATE` chỉ cho một thứ tự nhất quán, sau `order.completed` tổng Nợ = tổng Có; (b) gọi lặp cùng `idempotency_key` 5 lần → `already_applied = true`, `amount_vnd` đúng 1 lần đổi.
- Kiểm: `cargo fmt --all -- --check`, `cargo clippy --all-targets -- -D warnings`, và Postgres thật qua `bash scripts/test-db.sh --run bash -c 'cd services/payment && cargo test -- --nocapture'` (0 ignored/skipped; lặp test mới 50 lượt).
