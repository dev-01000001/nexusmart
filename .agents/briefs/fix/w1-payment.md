# Đợt 1 – pha 1B – agent `payment` (Rust)

Luật chung: `rules.md`. Mô hình tiền: `plan.md` mục 2 (bắt buộc theo đúng). Đọc `services/payment/CLAUDE.md`, skill `integrate-provider`. Hợp đồng mới: `ShopAllocation.platform_discount_vnd` (xem `docs/contracts.md`).

## Sở hữu
`services/payment/**`, `tests/e2e/13_*.go`.

## Việc
1. R1: kiểm `amount_vnd = Σ(subtotal_vnd + shipping_fee_vnd − platform_discount_vnd)`; lưu `platform_discount_vnd` vào `payment_shop_allocations` (migration `0002_`). Quyết toán đơn con (`settle_order_to_pending_tx`, khoảng `src/service.rs:2305`): Nợ `escrow_liability` (P đã thu − đã hoàn), Nợ `platform_promo_expense` D (tài khoản mới loại EXPENSE), Có `seller_pending:<shop>`, Có `platform_fee_revenue`, Có `shipping_fee_payable`; bút toán cân.
2. Payment amount 0 (đơn trả hết bằng xu/voucher): `succeeded` ngay, không tạo QR, không ghi `gateway_clearing`, allocation đầy đủ để quyết toán vẫn ghi cho seller R − C.
3. R2 COD (PAY-B4, ORD-B2): tạo payment COD ở trạng thái chờ thu (`pending`), không ghi `cod_receivable` lúc tạo. Tiêu thụ thêm `order.delivered`: ghi Nợ `cod_receivable` / Có `escrow_liability` P của đúng đơn con, phát `payment.succeeded` khi đơn con đó đã thu (đơn nhiều shop: theo từng allocation; payment thành `succeeded` khi mọi allocation còn hiệu lực đã thu). `order.cancelled`/`CancelPayment` với COD chưa giao: allocation/payment → `cancelled`, không còn bút toán treo, escrow không còn `held`. Tiêu thụ `order.returned` (event mới do `order` phát, payload trong `docs/contracts.md` hoặc brief `w1-order.md` mục 4): COD giao thất bại (`from_status` SHIPPING) → huỷ allocation như trên; trả sau khi đã nhận → để đợt 2 (ghi `ponytail:`), không tạo bút toán sai. `create_refund` từ chối (409) payment COD chưa thu và mọi khoản vượt số đã thu − đã hoàn của đơn con.
4. R3 (PAY-B1, PAY-B6): khi nhận `order.cancelled` của đơn cuối cùng trong nhóm, payment VIETQR còn chờ → `cancelled`. Webhook thành công cho payment đã `cancelled`/`superseded`, hoặc nhóm đã có payment thành công khác: ghi nhận tiền đã về và tự tạo refund `pending` lý do `late_payment` (bút toán cân, ví dụ Nợ `gateway_clearing` / Có `refund_payable`), KHÔNG phát `payment.succeeded`, không làm đơn đổi trạng thái. `CreatePayment` với `supersede_previous` từ chối nếu nhóm đã có payment thành công.
5. R4 (PAY-B2): quyết toán trừ phần đã hoàn của đơn con; hoàn sau khi đã quyết toán: trừ `seller_pending:<shop>` rồi `seller_available:<shop>` của đúng shop, không đủ → 409; không bao giờ để tổng Nợ `escrow_liability` của một payment vượt tổng Có đã nạp cho payment đó.
6. NATS: thêm `order.delivered`, `order.returned` vào filter consumer (`src/workers.rs` khoảng dòng 311), xử lý idempotent qua `processed_nats_events`.

## Test bắt buộc
Rust unit test: kiểm tổng allocation có D; bút toán quyết toán cân và đúng số (ví dụ G 229.000, voucher sàn 30.000, hoa hồng 11.450 → seller 217.550, `platform_promo_expense` 30.000); amount 0; COD chờ thu → delivered → ghi `cod_receivable`; huỷ COD không treo bút toán; refund COD chưa thu → lỗi; late payment → refund pending; hoàn một phần rồi quyết toán → escrow không âm. E2E `tests/e2e/13_payment_money_test.go`: tái hiện đúng kịch bản PAY-B1, PAY-B2, PAY-B4 trong `docs/audit/pay.md` và khẳng định kết quả mới; kèm truy vấn tổng escrow theo payment không âm. Phụ thuộc phần `order` chưa xong thì đánh dấu.
