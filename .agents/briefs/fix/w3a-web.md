# 3a – agent `web` (sở hữu `web/**`, `tests/e2e-web/08_*.test.mjs`). Đọc `web/CLAUDE.md`, `.claude/rules/frontend.md`, skill `design-system`.
Theo `w3a-plan.md` D1, D6, D8. Chi tiết: `w2b-review2-web-result.md` P2-3 ý 3, P3-1, P3-2.
1. D8/A3: `loadHomeCatalogData`, `loadFlashSalePageData` (và nơi khác ghép suất flash với danh sách sản phẩm) lấy sản phẩm theo `sku_ids` của suất; số suất hiển thị = số suất API trả (suất có sản phẩm không còn bán thì bỏ và không đếm, ghi rõ).
2. P2-3 ý 3: mã nhập tay thử đúng phạm vi (sàn, freeship, shop), lỗi theo từng phạm vi; không lộ mã ẩn.
3. P3-1: gợi ý ở header hiện giá flash/chương trình shop đang chạy. P3-2: `similarProducts` lấy ưu đãi của mọi shop trong danh sách.
4. D1: màn admin phí/danh mục dùng `/admin/categories`; không nơi nào ở `(shop)` đọc hoa hồng.
5. D6: gửi `bank_account_id` và `expected_owner_name`; 409 thì tải lại và báo "dữ liệu đã thay đổi".
Test: unit hiện có + mới; `08_` viết để chạy với stack có > 100 sản phẩm (manager nạp loadgen). Gate: lint, format:check, check:data, tsc, build.
6. D11: `POST /checkout` trả 409 `CHECKOUT_FAILED` → xoay `checkoutIdempotencyKey` sang khoá mới, báo tiếng Việt rõ và link tới danh sách đơn; không xoay khi mơ hồ hoặc `IDEMPOTENCY_KEY_CONFLICT`. Mã mới `SHOP_PICKUP_ADDRESS_REQUIRED` có câu báo cho người mua và cho người bán.
