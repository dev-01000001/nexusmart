# 3b-3 — Agent `web-b1` (B1 Search + §10-1/2/3 & dữ liệu giá chung)

- **Sở hữu (KHÔNG ĐỤNG FILE CỦA `web-a8`)**:
  - `web/src/lib/gateway-types.ts`, `web/src/lib/catalog.ts`, `web/src/lib/search.ts`, `web/src/lib/search-client.ts` (mới), `web/src/lib/shop-lookup.ts` (mới), `web/src/lib/admin-rbac.ts` (thêm capability `search.reindex`)
  - `web/src/components/ShopHeader.tsx`
  - `web/src/features/catalog/CatalogExplorer.tsx`
  - `web/src/features/account/serverData.ts`
  - `web/src/app/(shop)/page.tsx`, `web/src/app/(shop)/HomeClient.tsx`, `web/src/app/(shop)/tim-kiem/**`, `web/src/app/(shop)/danh-muc/**`, `web/src/app/(shop)/gio-hang/page.tsx`, `web/src/app/(shop)/thanh-toan/page.tsx`, `web/src/app/(shop)/thanh-toan/cho-thanh-toan/[paymentId]/page.tsx`
  - `tools/loadgen/generate-products.mjs` (sửa ảnh §10-2 dòng 79)
  - `tests/e2e-web/14_search_b1.test.mjs` (**KHÔNG** sửa `tests/e2e-web/helpers.mjs`; đặt helper riêng trong file `14_`).
- **Đọc**: `web/CLAUDE.md`, skill `design-system`, `w2-rules.md`, `w3b-plan.md` §5 (**G9–G14**), `w3b-design-b1.md`, `docs/contracts.md`.
- **Cấm**: sửa `proto/`, `services/`, `pkg/common/`, file thuộc `web-a8`, docker, `make up|seed|e2e|e2e-web`, git ghi.

## Phạm vi bắt buộc
1. **Header tìm kiếm & Gợi ý + Lịch sử (`ShopHeader.tsx`, `lib/search-client.ts`, UC-B12, G14)**:
   - Debounce đúng `200 ms`, gọi `GET /api/gw/search/suggest` (hiển thị từ khoá gợi ý, danh mục, gian hàng, từ khoá phổ biến từ server, SP khớp + ghép giá flash/shop discount thật). Gỡ dùng `TRENDING_KEYWORDS`, `SEARCH_SYNONYMS`, `SPELLING_DICTIONARY` tĩnh.
   - Chỉ khi **đã đăng nhập** mới gọi `GET /api/gw/search/history`, hiện tối đa 50 mục kèm nút xoá từng mục (`DELETE /api/gw/search/history/{id}`) và xoá tất cả (`DELETE /api/gw/search/history`); khách ẩn danh không gọi API lịch sử (0 lỗi 401).
2. **`/tim-kiem`, `/danh-muc`, `CatalogExplorer.tsx` (UC-B03, UC-B20, G9, G13)**:
   - Bỏ trần `pageSize: 100` và lọc/phân trang phía client; đọc trực tiếp `GET /api/gw/search` với tổng số thật (`total`), phân trang server (`page`/`page_size`), sắp xếp (`relevance`/`price_asc`/`price_desc`/`newest`/`rating_desc`), 5 facet thật (`category`, `shop`, `price`, `rating`, `in_stock`) + `size`/`color`.
   - Ẩn số đếm giả của 10 facet biên tập (`productEditorial.ts`) khi `total > page_size` (đề xuất 3 b1 §4). Nạp giá khuyến mãi theo `sku_ids` của trang hiện tại (0 lời gọi N+1 tới ProductService).
   - Khi `degraded_mode === "product_fallback"` (Meilisearch tắt), vẫn hiển thị danh sách sản phẩm bình thường (không trắng trang, không 500). Hiện sao thật từ `rating_avg_x100`/`review_count`.
3. **§10-1 Tổng sản phẩm trang chủ**: `HomeCatalogData` (`lib/catalog.ts`) và `HomeClient.tsx` truyền và hiển thị `productPage.total` thật (`"Xem tất cả {total} sản phẩm"`, không phải `products.length`).
4. **§10-2 Ảnh loadgen**: sửa `tools/loadgen/generate-products.mjs:79` `"/products/do-lot-boxer-modal.png"` → `"/products/do-lot-boxer-modal-3.png"`.
5. **§10-3 Tra tên gian vượt 100 gian (`lib/shop-lookup.ts`)**: tạo helper gọi `GET /shops/{id}` song song có khử trùng + giới hạn đồng thời ≤ 8; áp dụng cho cả **5 chỗ** (`lib/catalog.ts`, `gio-hang/page.tsx`, `thanh-toan/page.tsx`, `cho-thanh-toan/[paymentId]/page.tsx`, `features/account/serverData.ts`) để gian hàng thứ 101+ luôn hiện đúng tên thật, không rơi về `"Gian hàng NexusMart"`.
6. **§10 dữ liệu giá cho `SizeFinder` & `CheckoutSuggestionStrip` (giao hẳn cho `web-b1` để tránh đụng file)**:
   - Trong `lib/catalog.ts` (`loadShopDetailData`): nạp thêm `fetchActiveShopDiscounts({ skuIds })` cho mọi SKU của `allProducts` truyền vào `SizeFinder`.
   - Trong `gio-hang/page.tsx`, `thanh-toan/page.tsx`, `cho-thanh-toan/[paymentId]/page.tsx`: nạp active flash sale + shop discount cho các SKU gợi ý truyền vào `CheckoutSuggestionStrip` (tương thích prop mà `web-a8` render).
7. **E2E Web**: viết `tests/e2e-web/14_search_b1.test.mjs` cho tìm không dấu/gõ sai, gợi ý 200ms, lịch sử đăng nhập & xoá, phân trang > 100 SP, tên gian 101+, tổng trang chủ thật, và fallback khi Meilisearch tắt (manager chạy sau).

## Tiêu chí đo được & Kiểm tra
- Không còn trần 100 SP trên `/tim-kiem` & `/danh-muc`, không còn `TRENDING_KEYWORDS` tĩnh ở header, ảnh loadgen khớp file thật trong `web/public/products/`, 5 chỗ tra tên gian không rơi về `"Gian hàng NexusMart"`.
- Kiểm trong `web/`: `npm run lint`, `npm run format:check`, `npm run check:data`, `npm run build`.
