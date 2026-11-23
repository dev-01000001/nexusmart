# Đợt 2b – pha hợp đồng – agent `contract`
Đọc `w2b-plan.md` (toàn bộ), `w2-rules.md`, `docs/contracts.md`, code hiện tại. Chỉ làm hợp đồng và khung biên dịch, không cài nghiệp vụ. Sở hữu như `w2a-contract.md`.
## Proto (chỉ thêm, không đổi số trường cũ)
1. `promotion.proto`: `Voucher` thêm `status` (enum ACTIVE|PAUSED|DELETED), `category_ids`; `DiscountLineItem` thêm `category_id`, `shop_discount_program_item_id`; `ShopDiscountBreakdown`/`DiscountBreakdown` thêm `shop_program_discount_vnd`, `shop_freeship_discount_vnd`. RPC: `UpdateVoucher`, `SetVoucherStatus`, `DeleteVoucher`, `ListFlashSaleItems {status, flash_sale_id, shop_id, page}`, `ListAllFlashSales` (admin/seller, kể cả sắp mở/đã xong), `CreateShopDiscountProgram`, `UpdateShopDiscountProgram`, `EndShopDiscountProgram`, `ListShopDiscountPrograms` (seller), `ListActiveShopDiscounts {sku_ids|shop_id}` (công khai), đưa giữ/nhả giới hạn chương trình vào `ReservePromotion`/`ReleasePromotion(Shop)`; `CreateFlashSale` thêm `min_discount_percent`, `category_ids`.
2. `product.proto`: `SKU` thêm `min_price_30d_vnd`; `Category` giữ; RPC `GetCategoryDescendants` (nội bộ cho phạm vi voucher) và cấu hình phí: `SetCategoryFeeSchedule`, `ListCategoryFeeSchedules` (hoặc mở rộng UpdateCategory, chọn cách ít RPC hơn, ghi lý do).
3. `order.proto`: `Order` thêm `shop_program_discount_vnd`, `shop_freeship_discount_vnd`, `payment_fee_vnd`, `payment_fee_bps`, `needs_manual_review` (chỉ điền cho admin); `OrderItem` thêm `commission_bps`, `shop_program_item_id`. RPC `GetSellerReport`, `GetPlatformReport`, `ListOrdersNeedingReview`, `ResolveOrderReview`.
4. `payment.proto`: RPC `GetFeeConfig`/`SetFeeConfig` (payment fee theo phương thức, effective_from), `ListSellerLedger {page}`.
5. `user.proto`: RPC `AddShopBankAccount`, `VerifyShopBankAccountOTP`, `GetShopBankAccount`, `AdminReviewShopBankAccount`, `GetPlatformUserStats` (admin), `GetShopBankAccountForPayout` (nội bộ, service token).
6. `shipping.proto`: `ShopShippingSettings` thêm `free_ship_enabled`, `free_ship_min_order_vnd`, `free_ship_max_vnd`.
## Allowlist, event, env, contracts
- Allowlist đúng nguyên tắc 2a (không RPC nào có dữ liệu riêng được gọi ẩn danh; job nội bộ = true).
- Event: `product.reviewed` `{product_id, product_name, shop_id, decision, reason, occurred_at}`; `promotion.voucher_expiring` `{user_id, voucher_id, code, ends_at}`; `user.shop_bank_account_otp` (OTP không log); stream thêm `product.>`, `promotion.>`.
- Env: `ORDER_FLASH_PAYMENT_EXPIRY_MINUTES=5`, scrape config collector, `NATS_*` cho product (bỏ phụ thuộc thừa thì ghi), biến khác cần thiết.
- `docs/contracts.md`: REST, gRPC, event, mô hình tiền theo §1.
## Kiểm
`make proto`, build+vet 10 module Go, cargo build + test (stub unimplemented), go test pkg/common, compose config. Không rebuild container.
