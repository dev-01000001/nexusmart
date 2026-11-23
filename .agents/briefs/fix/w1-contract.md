# Đợt 1 – pha 1A – agent `contract`: hợp đồng dùng chung

Luật chung: `.agents/briefs/fix/rules.md`. Đọc `plan.md` mục 2 (mô hình tiền) và skill `add-rpc`. Pha này chạy MỘT MÌNH trước các agent service; chỉ đổi hợp đồng, không cài nghiệp vụ.

## Sở hữu
`proto/**`, `gen/go/**` (chỉ qua `make proto`), `pkg/common/auth/auth.go`, `docs/contracts.md`. Được sửa thêm tối thiểu ở service khác CHỈ để code biên dịch lại sau khi đổi proto (ví dụ struct literal Rust thiếu trường mới: thêm `..Default::default()` hoặc giá trị 0), không đổi hành vi.

## Việc
1. `proto/nexusmart/payment/v1/payment.proto`, `message ShopAllocation`: thêm `int64 platform_discount_vnd = 6;` kèm comment: phần giảm giá do sàn chịu (voucher sàn + freeship + xu) của đơn con; `subtotal_vnd` là doanh thu hàng của seller sau giảm giá seller chịu (R trong plan.md); `amount_vnd` của payment = Σ(subtotal_vnd + shipping_fee_vnd − platform_discount_vnd).
2. `proto/nexusmart/promotion/v1/promotion.proto`:
   - `message FlashSaleReservationItem`: thêm `string shop_id = 4;`.
   - RPC mới `rpc ReleasePromotionShop(ReleasePromotionShopRequest) returns (ReleasePromotionShopResponse);`
   - `message ReleasePromotionShopRequest { string order_id = 1; string shop_id = 2; string reason = 3; }` (`order_id` là id đã dùng khi `ReservePromotion`, tức checkout group id; idempotent theo `(order_id, shop_id)`).
   - `message ReleasePromotionShopResponse { bool released = 1; bool already_released = 2; int64 coins_returned = 3; bool shop_voucher_released = 4; int32 flash_sale_quantity_released = 5; bool platform_voucher_released = 6; }`
3. `proto/nexusmart/user/v1/user.proto`:
   - RPC mới `rpc RecordCODRejection(RecordCODRejectionRequest) returns (RecordCODRejectionResponse);`
   - `message RecordCODRejectionRequest { string user_id = 1; string order_id = 2; string reason = 3; }` (idempotent theo `order_id`).
   - `message RecordCODRejectionResponse { int32 cod_rejected_count = 1; bool already_recorded = 2; }`
4. `proto/nexusmart/order/v1/order.proto`:
   - RPC mới `rpc RetryPayment(RetryPaymentRequest) returns (RetryPaymentResponse);`
   - `RetryPaymentRequest`: `string payment_id = 1;` (payment hiện tại của nhóm đơn, lấy từ path REST), phương thức thanh toán (dùng enum phương thức thanh toán đã có trong `order.proto`; nếu chưa có thì dùng kiểu mà `CreateCheckoutRequest` đang dùng), `string idempotency_key = 3;`.
   - `RetryPaymentResponse`: `string checkout_group_id = 1; string payment_id = 2; google.protobuf.Timestamp payment_expires_at = 3;`
5. `make proto` (buf lint + generate + `go mod tidy` trong `gen/go`). Rust `payment` tự biên dịch proto qua `build.rs`: chạy `cargo build` và `cargo test` trong `services/payment`, sửa tối thiểu nếu struct literal hỏng.
6. `pkg/common/auth/auth.go` `internalRPCs` (AGENTS.md luật 10): thêm `/nexusmart.promotion.v1.PromotionService/ReleasePromotionShop`, `/nexusmart.user.v1.UserService/RecordCODRejection`, `/nexusmart.order.v1.OrderService/RetryPayment` theo đúng ngữ nghĩa map hiện có (giá trị `true` = cho phép token service). `RetryPayment` do gateway gọi bằng JWT người mua (KHÔNG cho token service nếu map phân biệt). Rà thêm theo USR-B5 và PRM-B4 trong `docs/audit/usr.md`, `docs/audit/prm.md`: `UserService/WriteAuditLog` (gateway sẽ gọi bằng JWT admin đang thao tác, hoặc token service), `UserService/ValidateUser` (tìm mọi nơi gọi bằng `rg`, giữ đúng cách gọi hiện có), `PromotionService/CreditCoins` (chỉ token service hoặc admin): không RPC nào trong số đó được chạy ẩn danh. Cập nhật test trong `pkg/common/auth` cho các mục mới.
7. `docs/contracts.md`: thêm 4 RPC mới, trường mới, ngữ nghĩa allocation theo plan.md mục 2; mục event: `payment` sẽ tiêu thụ thêm `order.delivered` và `order.returned` (để ghi khoản thu COD và xử lý hoàn), `order` vẫn tiêu thụ `payment.*` nhưng bỏ qua sự kiện của payment không phải payment hiện tại của nhóm đơn.

## Nghiệm thu
- `make proto` thành công, `git diff --stat gen/go` chỉ chứa file sinh từ 4 proto trên.
- `make lint` và `make test` xanh toàn repo (dán 3 dòng cuối mỗi lệnh).
- Không đổi hành vi nghiệp vụ nào (ngoài allowlist). Service Go mới có RPC chưa cài: dùng `Unimplemented...Server` đã nhúng, không viết stub tay.
