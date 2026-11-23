# Đợt 1 – pha 1B – agent `promotion`

Luật chung: `rules.md`. Mô hình tiền: `plan.md` mục 2. Hợp đồng mới: `PromotionService.ReleasePromotionShop`, `FlashSaleReservationItem.shop_id`.

## Sở hữu
`services/promotion/**`, `tests/e2e/14_*.go`.

## Việc
1. R5 (PRM-B3, ORD-B3): cài `ReleasePromotionShop(order_id, shop_id)`: lấy phần của shop từ `promotion_reservations.breakdown_json` (đã có `coinsDiscountVnd`, `shopVoucherId` theo shop) và `flash_sale_items_json` (dùng `shop_id` mới; reservation cũ thiếu thì tra `flash_sale_items` theo `flash_sale_item_id`): hoàn xu (ghi `coin_transactions`), trả lượt voucher shop, trả suất flash (Redis Lua + DB, cùng đường với `ReleasePromotion`). Voucher sàn chỉ hoàn khi mọi shop trong reservation đã hoàn, lúc đó reservation → RELEASED. Idempotent theo `(order_id, shop_id)`: ghi danh sách shop đã hoàn (migration `002_`, SQL idempotent như `ADD COLUMN IF NOT EXISTS`). `ReleasePromotion(order_id)` chỉ hoàn phần còn lại, không hoàn hai lần.
2. Khi `ReservePromotion`, lưu `shop_id` của từng item flash.
3. PRM-B4: `CreditCoins` chỉ cho token service hoặc admin; `SaveVoucher`, `GetCoinBalance`, `PreviewDiscounts` bắt buộc có claims người dùng (không còn nhánh "không có claims thì coi như nội bộ"), trừ khi gọi bằng token service ở RPC nằm trong allowlist. Kiểm mọi nơi gọi các RPC này (`rg` trong `services/`) để không làm hỏng luồng checkout.
4. PRM-B8: `ListAvailableVouchers` loại voucher hết hạn, chưa bắt đầu, hết ngân sách/lượt, dùng cùng điều kiện với `PreviewDiscounts`/`ReservePromotion`.
5. Chỉ đọc: consumer `order.completed` của promotion có cộng xu thưởng không. Nếu có và `order` cũng cộng (`services/order/internal/service/lifecycle.go:611`), ghi vào báo cáo để tránh cộng hai lần; không sửa phía order.

## Test bắt buộc
Unit test: tách phần theo shop từ breakdown; idempotent gọi 2 lần; hoàn phần cuối thì hoàn voucher sàn; `ReleasePromotion` sau khi đã hoàn một shop không hoàn lại lần nữa; quyền của 4 RPC ở mục 3. E2E `tests/e2e/14_promotion_release_test.go`: checkout 2 shop có voucher shop + voucher sàn + xu, huỷ một đơn con → số dư xu tăng đúng phần đơn con, `used_count` voucher shop giảm 1, voucher sàn chưa hoàn; huỷ nốt → voucher sàn hoàn. (Phụ thuộc `order` gọi RPC mới; chưa xong thì đánh dấu.)
