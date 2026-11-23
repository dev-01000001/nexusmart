# Báo cáo rà soát nghiệp vụ `cat` – Catalog, Tồn kho, Storefront, Tìm kiếm, Đánh giá

- **Commit HEAD**: `0db2cdd` · **Stack kiểm**: `nexusmart-audit` (Gateway `:48080`, Product HTTP `:48082`, Web Next.js `:43000`, Mailpit `:48025`, Postgres `product_db`/`user_db`).
- **Tài khoản kiểm**: admin/seller seed; buyer riêng `audit-cat-1@nexusmart.local` (OTP qua Mailpit); toàn bộ dữ liệu thử mang tiền tố `AUDIT-cat`.
- **Phạm vi**: 17 UC (`UC-B03, UC-B04, UC-B20, UC-B12, UC-B13, UC-B21, UC-B10, UC-S16, UC-S02, UC-S03, UC-S07, UC-S12, UC-S13, UC-A02, UC-A04, UC-A08, UC-A15`).

---

## 1. Bảng Use Case (17 UC)

| UC | Ưu tiên | Trạng thái | Backend (file:dòng) | REST | Web | Bằng chứng | Ghi chú |
| --- | --- | --- | --- | --- | --- | --- | --- |
| UC-B03 | Cao | MỘT PHẦN | `services/product/internal/server/server.go:661-731`, `services/product/queries/product.sql:116-164` | `GET /api/v1/products` (`handlers_product.go:325-402`) | `/danh-muc`, `/tim-kiem` (`CatalogExplorer.tsx:409-490`, `lib/catalog.ts:308-403`) | `curl "$GW/api/v1/products?keyword=%C3%81o%20thun"` → `200` `total:5`; `?keyword=ao%20thun` → `200` `total:0`; `?category_slug=ao-thun&min_price_vnd=150000&max_price_vnd=200000&sort_by=price_asc` → `200` `total:4`; `?page=2&page_size=5` → `200` `5/18` SP không trùng trang 1; SSR `/tim-kiem?q=ao+thun` → `200` | Không có Search service/index qua event; backend `ILIKE` không tìm được không dấu (`CAT-B1`), không thoát `%` (`?keyword=%25` → 18 SP), không lọc theo đánh giá; web bù bỏ dấu/đồng nghĩa/13 facet client-side trên trần 100 SP (`CAT-B1`, `CAT-B12`). |
| UC-B04 | Cao | MỘT PHẦN | `services/product/internal/server/server.go:627-659,1319-1334` | `GET /api/v1/products/{id}`, `GET /api/v1/products/slug/{slug}` (`handlers_product.go:404-430`) | `/san-pham/[slug]` (`ProductDetailClient.tsx`) | `curl "$GW/api/v1/products/slug/ao-thun-cotton-compact-trang"` → `200` (`min:179000`, `max:179000`, 15 SKU, `available_stock=stock-reserved_stock`); slug sai / SP `PENDING`/`REJECTED`/`HIDDEN` → `404`; SSR `/san-pham/ao-thun-cotton-compact-trang` → `200`, slug sai → `404` | Giá/SKU/tồn/shop/voucher/flash sale thật. Thiếu đánh giá thật (`UC-B10`), tóm tắt đánh giá AI (`UC-AI05`), giá so sánh trong DB (`CAT-D1`); SP ngừng bán trả `404` thay vì báo ngừng bán + gợi ý SP tương tự. |
| UC-B20 | Cao | MỘT PHẦN | `services/product/internal/server/server.go:198-208,661-731` | `GET /api/v1/categories`, `GET /api/v1/products`, `GET /api/v1/shops`, `GET /api/v1/flash-sales` | `/` (`HomeClient.tsx`), `/danh-muc` (`CatalogExplorer.tsx`) | SSR `/` → `200`, `/danh-muc?cat=ao-thun` → `200`; `GET /api/v1/categories` → `200` (6 danh mục). Admin cookie vào `/danh-muc` thấy SP `PENDING` (`pendingProdInDanhMucHTML:true`) | Dữ liệu catalog/shop/flash sale thật. Lỗi lộ SP chưa duyệt ra storefront khi admin/seller đăng nhập (`CAT-B2`); banner & gợi ý bán chạy (`salesRank`) là mock; lọc danh mục cha chỉ gom 1 cấp con ở DB và bị exact-slug lọc rơi ở client (`CAT-B9`); phân trang web chỉ slice ≤100 SP. |
| UC-B12 | Trung bình | THIẾU | Không có (`rg -i "autocomplete\|suggest\|search_history" proto/ services/` = 0 kết quả; không có `services/search`) | Không có trong `services/gateway/internal/router/router.go` | `ShopHeader.tsx:173`, `lib/search.ts:208-230` (mock tĩnh) | Đọc `ShopHeader.tsx:173` & `lib/search.ts:208-230`: `getInstantSearchSuggestions` đọc cứng `PRODUCTS` & `TRENDING_KEYWORDS` tĩnh; `rg -i "searchHistory\|search_history" web/src/` = 0 | Thiếu hoàn toàn API gợi ý debounce 200ms, thiếu gợi ý danh mục/gian hàng, thiếu lịch sử tìm kiếm ở cả client lẫn server (`CAT-B12`). |
| UC-B13 | Thấp | THIẾU | Không có (`rg -i "wishlist\|favorite\|follow" proto/ services/` = 0 kết quả) | Không có trong `router.go` | Yêu thích: mock `localStorage` (`MockStoreContext.tsx:37-41,109-111`); Theo dõi shop: không có trên `ShopDetailClient.tsx` | `rg -i "followShop\|followedShop\|theo dõi gian hàng" web/src/ services/ proto/` = 0; `ShopDetailClient.tsx:251-267` chỉ có nút Chat và Xem SP | Yêu thích chỉ lưu cục bộ trình duyệt, không đồng bộ server/thông báo giảm giá; theo dõi gian hàng chưa có kể cả UI. (Ưu tiên Thấp – spec không bắt buộc cài đặt.) |
| UC-B21 | Trung bình | MỘT PHẦN | `services/user/internal/service/server.go:922-983`, `services/product/internal/server/server.go:661-731` | `GET /api/v1/shops`, `GET /api/v1/shops/{id}`, `GET /api/v1/shops/slug/{slug}` (`handlers_user.go:634-698`) | `/gian-hang/[slug]` (`ShopDetailClient.tsx`) | `curl "$GW/api/v1/shops/slug/xuong-det-bach-dang"` → `200`, `keys=["created_at","description","id","name","slug","status","updated_at"]` (không lộ PII); slug sai → `404`; SSR `/gian-hang/xuong-det-bach-dang` → `200`, slug sai → `404` | Hồ sơ công khai, SP, lưu voucher của shop thật. Thiếu điểm đánh giá, tỷ lệ phản hồi thật, số người theo dõi/nút theo dõi, danh mục riêng của shop (đang phủ số liệu giả từ `web/src/data/shops.ts`); shop khoá/tạm nghỉ trả `404` thay vì ẩn nút mua; seller xem shop mình bị lộ SP chưa duyệt (`CAT-B2`). |
| UC-B10 | Trung bình | THIẾU | Không có (`rg -i "review" proto/ services/` chỉ có `AdminReviewProduct` / `AdminReviewShop`, không có bảng/RPC đánh giá người mua) | Không có trong `router.go` | `web/src/features/product/PdpReviews.tsx` (mock `SAMPLE_REVIEWS` + `MockStoreContext`) | Kiểm tra `proto/nexusmart/product/v1/product.proto` và `services/product/migrations/000001_init.sql`: chỉ có 5 bảng `categories, products, skus, stock_reservations, stock_history` | Không có backend lưu đánh giá, không kiểm điều kiện đơn `COMPLETED` hay giới hạn 1 lần/đơn, không lọc từ cấm, không cập nhật điểm trung bình hay phát event cho AI. |
| UC-S16 | Thấp | THIẾU | Không có (`proto/nexusmart/product/v1/product.proto`, `services/product/`) | Không có trong `router.go` | `/seller/tuong-tac` (`web/src/app/(seller)/seller/tuong-tac/page.tsx`, 100% mock `sellerStore`) | SSR `/seller/tuong-tac` (cookie seller) → `200`; đọc `page.tsx:68-195` chỉ dùng `sellerStore` cục bộ | Không có backend trả lời đánh giá, không ràng buộc 1 lần/sửa 24h hay thông báo cho người mua. (Ưu tiên Thấp.) |
| UC-S02 | Cao | MỘT PHẦN | `services/product/internal/server/server.go:212-586,1425-1455` | `GET/POST /api/v1/seller/products`, `GET/PUT/DELETE /api/v1/seller/products/{id}` (`handlers_product.go:432-569`) | `/seller/san-pham`, `/seller/san-pham/moi`, `/seller/san-pham/[id]` (`ProductFormView.tsx`) | `POST /seller/products` → `201` (`status:PENDING`, auto slug); giá âm/0/số thực/tồn âm → `400`; seller B sửa/xoá SP shop A → `403`; seller tự duyệt `PENDING→APPROVED` → `403`; ẩn/hiện (`APPROVED↔HIDDEN`) & `DELETE` → `200` (sau xoá `GET` → `404`); SSR `/seller/san-pham*` → `200` | Core CRUD + RBAC + ownership chạy tốt. Lỗi: UI cho chọn `PENDING→HIDDEN` gây `403` (`CAT-B5`); `PUT` sửa tồn không sync `OUT_OF_STOCK`/không ghi `stock_history` (`CAT-B4`); gỡ rồi thêm lại cùng `sku_code` bị `409` (`CAT-B6`); thiếu event đồng bộ Search, upload ảnh thật, AI sinh mô tả. |
| UC-S03 | Cao | MỘT PHẦN | `services/product/internal/server/server.go:769-851,1487-1500` | `PATCH /api/v1/seller/skus/{sku_id}/stock` (`handlers_product.go:571-594`) | `/seller/san-pham` (`page.tsx:182-229,672-796`) | `PATCH /seller/skus/{sku}/stock` `10→12` (`expected_version:1`) → `200` (`version:2`), ghi `stock_history`; gửi lại `version:1` → `409 ABORTED`; đặt `new_stock=2 < reserved_stock=3` → `409 FAILED_PRECONDITION`; đặt tất cả SKU về `0` → SP tự chuyển `OUT_OF_STOCK`, nhập lại `7` → `APPROVED`; seller B cập nhật SKU shop A → `403` | Luồng `PATCH` optimistic locking, giữ tồn, lịch sử DB và cảnh báo tồn thấp hoạt động tốt. Lỗi: `PUT /seller/products/{id}` sửa tồn bỏ qua optimistic locking + `stock_history` + `OUT_OF_STOCK` (`CAT-B4`); `PATCH` vẫn cho sửa SKU `INACTIVE` (`CAT-B11`); chưa có API xem `stock_history`; UI không tự đổi badge sang `OUT_OF_STOCK` sau `PATCH` về `0` (`CAT-B13`). |
| UC-S07 | Cao | MỘT PHẦN | `services/product/internal/server/server.go:212-546,1369-1423` | `POST/PUT /api/v1/seller/products` (`handlers_product.go:489-554`) | `/seller/san-pham/moi`, `/seller/san-pham/[id]` (`ProductFormView.tsx:303-333`), `ProductCard.tsx`, `ProductDetailClient.tsx` | Tạo SP 2 SKU (`150000`, `250000`, `attributes:{color,size}`, `weight_grams:220/240`) → `201` (`min_price_vnd:150000`, `max_price_vnd:250000`); bỏ SKU khi `PUT` chỉ `DeactivateSKU` giữ toàn vẹn đơn cũ | Hỗ trợ đa SKU, ma trận màu/size, giá/tồn/cân nặng/ảnh riêng, tính `min_price_vnd`/`max_price_vnd`. Lỗi: không thể thêm lại `sku_code` đã gỡ (`CAT-B6`); thẻ `ProductCard` chỉ hiện `minPriceVnd` chứ chưa hiện khoảng giá min–max (`CAT-B13`); thiếu đồng bộ Search. |
| UC-S12 | Thấp | THIẾU | Không có (`proto/nexusmart/user/v1/user.proto` không có `UpdateShop` hay bảng trang trí/danh mục riêng shop) | Không có trong `router.go` | `/seller/ho-so` (`web/src/app/(seller)/seller/ho-so/page.tsx`: chỉ đọc `GET /seller/shop`, mọi chỉnh sửa lưu mock `sellerStore`) | SSR `/seller/ho-so` → `200`; `proto/nexusmart/user/v1/user.proto` chỉ có `RegisterShop/GetShop/ListPublicShops/Admin*` | Không thể lưu logo/ảnh bìa/banner/danh mục riêng/SP ghim vào DB hay xuất bản ra `/gian-hang/[slug]`. (Ưu tiên Thấp.) |
| UC-S13 | Trung bình | THIẾU | Không có (`rg -i "bulk\|import_product\|csv\|xlsx" proto/ services/product/` = 0 kết quả) | Không có trong `router.go` | Không có trong `web/src/app/(seller)/` (`rg -i "hàng loạt\|import.*csv\|bulk" web/src/` chỉ có ô áp nhanh số tồn SKU trên `ProductFormView.tsx:903`) | Quét toàn bộ `proto/`, `services/product/`, `services/gateway/`, `web/src/app/(seller)/`: không có tải mẫu, upload file, hay job nền nhập sản phẩm hàng loạt | Thiếu hoàn toàn cả backend (job nền, parser, báo cáo lỗi từng dòng) lẫn giao diện. |
| UC-A02 | Cao | MỘT PHẦN | `services/product/internal/server/server.go:588-625` | `GET /api/v1/admin/products`, `GET /api/v1/admin/products/{id}`, `POST /api/v1/admin/products/{id}/review` (`handlers_product.go:596-666`) | `/admin/kiem-duyet` (`AdminProductModerationTab.tsx`) | `POST /admin/products/{id}/review` `REJECT` → `200` (`status:REJECTED`, ẩn danh `404`); seller resubmit `PENDING` → admin `APPROVE` → `200` (ẩn danh `200`); SSR `/admin/kiem-duyet` → `200` | Duyệt/từ chối và ẩn/hiện SP chạy thật. Lỗi/thiếu: **không gửi thông báo cho seller** (`sellerNotifDelta:0`, `CAT-B3`); **không ghi audit log** (`auditLogCount:0`, `CAT-B7`); backend nhận `REJECT` với `reason:""` (`CAT-B10`); không index Search; chưa có xử lý hàng cấm/ghi điểm vi phạm shop; duyệt SP tồn 0 vẫn thành `APPROVED`. |
| UC-A04 | Trung bình | LỖI | `services/product/internal/server/server.go:50-208`, `services/product/queries/product.sql:3-53,120,141` | `GET /api/v1/categories`, `POST/PUT/DELETE /api/v1/admin/categories` (`handlers_product.go:212-301`) | `/admin/cau-hinh` (`AdminCategoriesPanel.tsx`) | Tạo cây 3 cấp `Cha→Con→Cháu` → `201`; xoá danh mục có SP active/có con → `409`. **NHƯNG**: xoá danh mục có SP đã soft-delete → **`500 INTERNAL_ERROR`** (FK `23503`); đặt `Cha.parent_id=Con.id` → **`200` tạo cycle**; lọc `category_id=Cha` → `total:0` (mất SP ở `Cháu`); slug `"AUDIT-cat / Có Dấu!"` → `201`; `PUT` thiếu `commission_rate_bps` → reset `0`; audit log = `0` | Hỏng nhiều invariant lõi của cây danh mục (`CAT-B8`, `CAT-B9`, `CAT-B7`), không cập nhật Search, UI thiếu cột cha và không cho sửa `parent_id`. |
| UC-A08 | Thấp | THIẾU | Không có (`rg -i "banner" proto/ services/` = 0 kết quả) | Không có trong `router.go` | `/admin/noi-dung` (`web/src/app/(admin)/admin/noi-dung/page.tsx`: 100% mock `auditStore`) | SSR `/admin/noi-dung` → `200`; `proto/` & `services/` không có thực thể banner/homepage block; `HomeClient.tsx` đọc hằng số tĩnh `CAMPAIGN_BANNERS`/`MEDIA_BANNERS` | Chưa có backend CMS banner (kèm lịch tự ẩn hết hạn), sắp xếp khối trang chủ hay làm mới cache. (Ưu tiên Thấp.) |
| UC-A15 | Trung bình | THIẾU | Không có (`rg -i "banned\|forbidden" proto/ services/product/` = 0 kết quả trừ lỗi quyền `Forbidden`) | Không có trong `router.go` | `/admin/kiem-duyet` (`page.tsx:48-54,220-252`: mock `auditStore`) | Kiểm tra `services/product/internal/server/server.go`: `CreateProduct`/`UpdateProduct` không có bất kỳ bước lọc từ cấm hay danh mục cấm nào | Thiếu hoàn toàn từ khoá cấm, danh mục hàng cấm, chuẩn hoá chống lách ký tự lạ, xem trước SP ảnh hưởng và job quét lại SP đang bán. |

---

## 2. Bảng đối chiếu Shopify / sàn

| Nghiệp vụ | UC hoặc ngoài phạm vi | Trạng thái | Ghi chú |
| --- | --- | --- | --- |
| **Storefront: Theme & trình chỉnh sửa** | Một phần `UC-S12` (Thấp, trang trí shop); theme editor toàn sàn: **ngoài phạm vi đặc tả** (`docs/spec.md:39`: *"Làm mỏng: trang trí gian hàng"*, *"Mọi tính năng không có trong tab Use case"*) | THIẾU | Không có backend chỉnh theme/giao diện shop; `/seller/ho-so` chỉ mock. |
| **Storefront: Trang & blog** | **Ngoài phạm vi đặc tả** (`docs/spec.md:39` — không có trong tab Use case) | Ngoài phạm vi (UI mock) | `web/` có trang SSG tĩnh `/tap-chi`, `/ve-nexusmart`, `/chien-dich/[slug]` không có CMS backend. |
| **Storefront: Menu** | Một phần `UC-A08` (Thấp) & `UC-S12` (Thấp); CMS mega-menu động toàn sàn: **ngoài phạm vi đặc tả** (`docs/spec.md:39`) | THIẾU | `ShopHeader.tsx` dùng hằng số `MEGA_MENU_GROUPS`; `/admin/noi-dung` mock. |
| **Storefront: Tìm kiếm & lọc** | `UC-B03` (Cao), `UC-B12` (Trung bình), `UC-B20` (Cao) *(tìm ngữ nghĩa: `UC-AI02` thuộc AI)* | MỘT PHẦN | Backend lọc giá/danh mục/shop/sort/phân trang thật nhưng không tìm không dấu, không Search service, không gợi ý/lịch sử (`CAT-B1`, `CAT-B12`). |
| **Storefront: Gợi ý sản phẩm** | `UC-B04` (Cao, SP liên quan), `UC-B20` (Cao, gợi ý hôm nay) *(cá nhân hoá: `UC-AI07` Thấp)* | MỘT PHẦN | PDP & trang chủ tính gợi ý phía server/client theo rule thuộc tính trên ≤100 SP, chưa có recommendation/AI embedding. |
| **Storefront: SEO** | Slug URL: `UC-B04`, `UC-B21`, `UC-A04`; bộ SEO (sitemap, robots, canonical, OG/JSON-LD, meta editor): **ngoài phạm vi đặc tả** (`docs/spec.md:39`) | MỘT PHẦN (chỉ slug) | Có slug tiếng Việt cho product/shop/category và `<title>` cơ bản ở `web/src/app/layout.tsx`; chưa có `robots.ts`, `sitemap.ts`, `generateMetadata` động cho PDP/shop. |
| **Storefront: Đa ngôn ngữ (i18n)** | **Ngoài phạm vi đặc tả** (`docs/spec.md:39`, `AGENTS.md` luật 6 chỉ hỗ trợ `VND`, không có UC i18n) | Ngoài phạm vi | Toàn bộ hệ thống chỉ tiếng Việt (`lang="vi"`) và tiền VND. |
| **Storefront: Headless qua Storefront API** | Phục vụ `UC-B03`, `UC-B04`, `UC-B20`, `UC-B21` (REST public); GraphQL Storefront API/API key bên thứ ba: **ngoài phạm vi đặc tả** (`docs/spec.md:39`) | ĐỦ (REST nội bộ) | Có REST công khai `GET /api/v1/{products,categories,shops,flash-sales,vouchers}`; BFF Next.js đang tiêu thụ trực tiếp. |
| **Catalog: Sản phẩm** | `UC-S02` (Cao) | MỘT PHẦN | CRUD + RBAC + soft-delete thật; còn lỗi `CAT-B4`, `CAT-B5`, `CAT-B6` và thiếu đồng bộ Search. |
| **Catalog: Biến thể & tuỳ chọn** | `UC-S07` (Cao) | MỘT PHẦN | Hỗ trợ đa SKU, `attributes` JSONB, giá/tồn/cân nặng/ảnh riêng, `min_price_vnd`/`max_price_vnd`; lỗi re-add `sku_code` (`CAT-B6`), `ProductCard` chưa hiện khoảng giá (`CAT-B13`). |
| **Catalog: Collection thủ công & tự động** | Một phần `UC-S12` (Thấp, danh mục riêng shop); collection tự động theo rule: **ngoài phạm vi đặc tả** (`docs/spec.md:39`) | THIẾU | Web có `/bo-suu-tap` SSG tĩnh từ `web/src/data/`; chưa có trong DB/API. |
| **Catalog: Media** | Một phần `UC-S02` (Cao, ảnh sản phẩm) | MỘT PHẦN | DB lưu mảng URL chuỗi `images JSONB` & `sku.image_url`; chưa có luồng upload object storage/CDN hay kiểm định dạng file ảnh. |
| **Catalog: Sản phẩm số (digital)** | **Ngoài phạm vi đặc tả** (`docs/spec.md:39` — không có trong tab Use case) | Ngoài phạm vi | Toàn bộ luồng đơn hàng gắn với cân nặng và vận chuyển vật lý. |
| **Catalog: Bundle / Combo** | **Ngoài phạm vi đặc tả** (`docs/spec.md:29` tab Phạm vi dòng Khuyến mãi, cột Ngoài phạm vi: *"Combo, mua kèm, chương trình thành viên theo hạng"*) | Ngoài phạm vi (UI mock) | `web/` có UI combo/mua kèm tĩnh trong `productEditorial.ts`; backend không cài. |
| **Catalog: Subscription** | **Ngoài phạm vi đặc tả** (`docs/spec.md:39` — không có trong tab Use case) | Ngoài phạm vi | Không có. |
| **Catalog: Gift card** | **Ngoài phạm vi đặc tả** (`docs/spec.md:39` — không có trong tab Use case) | Ngoài phạm vi (UI mock) | `web/` có `/the-qua-tang` mock hoàn toàn. |
| **Catalog: Giá & giá so sánh** | Giá bán `*_vnd`: `UC-S02`/`UC-S07` (Cao); giá gạch ngang: `UC-S18` (Trung bình), `UC-B16` (Flash sale) | MỘT PHẦN | Có `price_vnd` int64 (chặn âm/0/số thực); **thiếu `original_price_vnd` / giá so sánh trong DB & API** (`CAT-D1`), web đang phủ từ mock. |
| **Catalog: Import / Export hàng loạt** | Import: `UC-S13` (Trung bình); Export catalog: **ngoài phạm vi đặc tả** (`docs/spec.md:39`) | THIẾU | Chưa có import file/job nền (`UC-S13`) lẫn export catalog. |
| **Tồn kho: Nhiều kho** | **Ngoài phạm vi đặc tả** (`docs/spec.md:28` tab Phạm vi dòng Vận chuyển, cột Ngoài phạm vi: *"quản lý kho"*) | Ngoài phạm vi | Mỗi SKU chỉ có 1 tổng tồn kho tại 1 `pickup_address_id` của shop. |
| **Tồn kho: Theo dõi số lượng & điều chỉnh** | `UC-S03` (Cao) | MỘT PHẦN | Optimistic locking + `stock_history` chạy tốt trên `PATCH`; `PUT` đi vòng không ghi lịch sử (`CAT-B4`), chưa có API đọc `stock_history`. |
| **Tồn kho: Chuyển kho / Đơn nhập hàng / Kiểm kê** | **Ngoài phạm vi đặc tả** (`docs/spec.md:28` — *"quản lý kho"*) | Ngoài phạm vi | Không có. |
| **Tồn kho: Trạng thái sẵn có / đã giữ / đang về** | Sẵn có (`available_stock`) & đã giữ (`reserved_stock`): `UC-S03` (Cao), `UC-B06` (Cao); Đang về (`incoming`): **ngoài phạm vi đặc tả** (`docs/spec.md:28` *"quản lý kho"*) | ĐỦ (cho phần trong phạm vi) | `skus` có `stock`, `reserved_stock`, `available_stock = stock - reserved_stock`, chặn `new_stock < reserved_stock` (`409`). |
| **Niềm tin (sàn): Đánh giá & nhận xét** | `UC-B10` (Trung bình), `UC-S16` (Thấp), `UC-A10` (Thấp), `UC-AI05` (Trung bình) | THIẾU | Chưa có backend; PDP và Seller chỉ chạy mock. |
| **Niềm tin (sàn): Kiểm duyệt sản phẩm & hàng cấm** | `UC-A02` (Cao), `UC-A15` (Trung bình) | MỘT PHẦN | Duyệt/từ chối sản phẩm có thật nhưng thiếu thông báo seller (`CAT-B3`), thiếu audit log (`CAT-B7`), không bắt buộc lý do từ chối (`CAT-B10`), chưa có từ khoá cấm/hàng cấm (`UC-A15`). |

---

## 3. Lỗi phát hiện (`CAT-B<n>`)

### `CAT-B1` (P1) — Tìm kiếm backend không hỗ trợ tiếng Việt không dấu, không thoát ký tự wildcard `ILIKE`, và thiếu Search service / trần 100 SP trên web
- **UC liên quan**: `UC-B03` (Cao), `docs/spec.md` tab Phạm vi dòng 31.
- **Tái hiện**:
  ```bash
  curl -sS "http://localhost:48080/api/v1/products?keyword=ao%20thun"
  curl -sS "http://localhost:48080/api/v1/products?keyword=%C3%81o%20thun"
  curl -sS "http://localhost:48080/api/v1/products?keyword=%25"
  ```
- **Mong đợi**: `keyword=ao thun` trả về các sản phẩm `"Áo thun..."` (tìm có dấu và không dấu); `keyword=%` không match toàn bộ catalog; dữ liệu được đồng bộ sang Search service qua event để đáp ứng 20.000–50.000 SP.
- **Thực tế**:
  - `keyword=ao%20thun` → `200`, `{"items":[],"page":1,"page_size":20,"total":0}` (trong khi `keyword=Áo thun` trả `total: 5`).
  - `keyword=%25` → `200`, `total: 18` (lộ wildcard SQL `ILIKE`).
  - Không có Search service; web `loadCatalogExplorerData` gọi `GET /api/v1/products?page=1&page_size=100` để tự lọc bỏ dấu/đồng nghĩa/13 facet phía client → mọi sản phẩm từ thứ 101 trở đi biến mất khỏi tìm kiếm không dấu và bộ lọc.
- **Gốc nghi ngờ**: `services/product/queries/product.sql:121,142` (`ILIKE '%' || keyword || '%'`), `web/src/lib/catalog.ts:341-360`, `web/src/features/catalog/CatalogExplorer.tsx:409-433`.

### `CAT-B2` (P1) — Lộ sản phẩm `PENDING`/`REJECTED`/`HIDDEN` ra storefront công khai khi Admin hoặc Seller chủ shop đang đăng nhập
- **UC liên quan**: `UC-B20` (Cao), `UC-B04` (Cao), `UC-B21` (Trung bình), `UC-A02` (Cao).
- **Tái hiện**:
  ```bash
  # 1. Seller A tạo sản phẩm AUDIT-cat ở trạng thái PENDING (chưa duyệt)
  curl -sS -X POST "http://localhost:48080/api/v1/seller/products" \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"category_id":"<cat_id>","name":"AUDIT-cat Pending Leak","skus":[{"sku_code":"AC-L1","name":"M","price_vnd":150000,"stock":5,"weight_grams":200}]}'
  # 2. Gọi endpoint public GET /api/v1/products với token admin (hoặc mở http://localhost:43000/danh-muc khi đã đăng nhập admin)
  curl -sS "http://localhost:48080/api/v1/products?page=1&page_size=100" -H "Authorization: Bearer $ADMIN_TOKEN"
  # 3. Seller A gọi endpoint public theo shop_id (đúng lời gọi của /gian-hang/[slug])
  curl -sS "http://localhost:48080/api/v1/products?shop_id=<shopA_id>&page=1&page_size=100" -H "Authorization: Bearer $SELLER_A_TOKEN"
  ```
- **Mong đợi**: Trang storefront công khai (`/`, `/danh-muc`, `/tim-kiem`, `/gian-hang/[slug]`) chỉ hiển thị sản phẩm `APPROVED`/`OUT_OF_STOCK`, bất kể người xem đang đăng nhập vai trò gì (quản trị và người bán đã có endpoint riêng `GET /api/v1/admin/products` và `GET /api/v1/seller/products`).
- **Thực tế**:
  - Ẩn danh không thấy SP (`inAnonList: false`), nhưng Admin gọi `GET /api/v1/products` trả cả SP `PENDING` (`inAdminPublicList: true`); SSR `GET http://localhost:43000/danh-muc` với cookie Admin chứa thẳng SP `PENDING` trong HTML (`pendingProdInDanhMucHTML: true`); Seller A gọi `GET /api/v1/products?shop_id=<shopA>` cũng thấy SP `PENDING` (`inSellerAPublicShopList: true`).
- **Gốc nghi ngờ**: `web/src/lib/catalog.ts:138-141` (`optionalAuth: true` trên `fetchRawProducts`), `services/product/internal/server/server.go:677-684` (tắt `publicOnly` cho admin/seller trên cùng RPC), `services/gateway/internal/router/handlers_product.go:325-391` (không ép public-only cho route storefront `/api/v1/products`).

### `CAT-B3` (P1) — Admin duyệt/từ chối sản phẩm không gửi thông báo cho người bán và không phát event `product.*`
- **UC liên quan**: `UC-A02` (Cao), `UC-S02` (Cao).
- **Tái hiện**:
  ```bash
  # 1. Đếm thông báo trước của seller A
  curl -sS "http://localhost:48080/api/v1/notifications?page=1&page_size=20" -H "Authorization: Bearer $SELLER_TOKEN"
  # 2. Admin từ chối rồi duyệt sản phẩm của seller A
  curl -sS -X POST "http://localhost:48080/api/v1/admin/products/<product_id>/review" \
    -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
    -d '{"decision":"REJECT","reason":"AUDIT-cat thiếu ảnh thực tế"}'
  # 3. Kiểm tra lại thông báo của seller A & Mailpit
  curl -sS "http://localhost:48080/api/v1/notifications?page=1&page_size=20" -H "Authorization: Bearer $SELLER_TOKEN"
  ```
- **Mong đợi**: Theo `docs/spec.md` (`UC-A02` dòng 45 liệt kê `Product, Search, Notification`) và `cat.md` mục 4, người bán nhận được thông báo khi sản phẩm bị từ chối kèm lý do hoặc được phê duyệt; đồng thời phát event để Search index.
- **Thực tế**: `sellerNotifDelta = 0`, không có email Mailpit, stream `NEXUSMART` không có subject `product.>`. `services/product` không kết nối NATS và `services/notification` không có consumer xử lý kết quả duyệt sản phẩm.
- **Gốc nghi ngờ**: `services/product/cmd/product/main.go`, `services/product/internal/server/server.go:588-625`, `docs/contracts.md:278` (stream `NEXUSMART` chỉ gồm `user.>`, `order.>`, `payment.>`, `shipment.>`).

### `CAT-B4` (P1) — `PUT /api/v1/seller/products/{id}` sửa tồn kho bỏ qua đồng bộ `OUT_OF_STOCK`, optimistic locking và `stock_history`
- **UC liên quan**: `UC-S02` (Cao), `UC-S03` (Cao).
- **Tái hiện**:
  ```bash
  # Trên sản phẩm đang APPROVED có 2 SKU (stock > 0), gửi PUT đặt stock=0 cho tất cả SKU:
  curl -sS -X PUT "http://localhost:48080/api/v1/seller/products/<product_id>" \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"skus":[{"id":"<sku1_id>","sku_code":"AUDIT-CAT-BLK-M","name":"Đen / M","price_vnd":150000,"stock":0,"weight_grams":220},{"id":"<sku2_id>","sku_code":"AUDIT-CAT-WHT-L","name":"Trắng / L","price_vnd":250000,"stock":0,"weight_grams":240}]}'
  ```
- **Mong đợi**: Sản phẩm hết hàng ở mọi SKU phải tự chuyển trạng thái `OUT_OF_STOCK`; mọi thay đổi tồn kho phải qua kiểm soát tranh chấp (optimistic locking) và ghi lịch sử vào `stock_history` (spec `UC-S03`). Ngoài ra seller không được chuyển `OUT_OF_STOCK -> APPROVED` khi tổng tồn vẫn bằng `0`.
- **Thực tế**:
  - `PUT` trả `200` với `status: "APPROVED"` dù toàn bộ SKU có `stock: [0, 0]`; `SELECT * FROM stock_history` xác nhận **0 bản ghi** được ghi từ `PUT` (chỉ có 4 bản ghi từ `PATCH`).
  - `resolveUpdatedProductStatus` cho phép seller gửi `PUT {"status":"APPROVED"}` từ `OUT_OF_STOCK` mà không kiểm tồn > 0.
- **Gốc nghi ngờ**: `services/product/internal/server/server.go:420-532` (không gọi `syncProductStockStatus`, không `InsertStockHistory`, không kiểm `expected_version`), `server.go:1442-1445`.

### `CAT-B5` (P1) — Form sửa sản phẩm người bán cho chọn `"Tạm ẩn"` (`HIDDEN`) ở trạng thái `PENDING` nhưng backend trả `403`
- **UC liên quan**: `UC-S02` (Cao).
- **Tái hiện**:
  ```bash
  # Trên sản phẩm đang ở trạng thái PENDING, mở /seller/san-pham/<id>, chọn "Tạm ẩn" (HIDDEN) và Lưu:
  curl -sS -X PUT "http://localhost:48080/api/v1/seller/products/<pending_product_id>" \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"status":"HIDDEN"}'
  ```
- **Mong đợi**: Danh sách trạng thái trên UI phải khớp đúng ma trận trạng thái backend cho phép (không hiển thị tuỳ chọn chắc chắn thất bại `403`).
- **Thực tế**: UI `ProductFormView.tsx:90-94` hiển thị lựa chọn `HIDDEN ("Tạm ẩn")` cho `PENDING`, nhưng gửi lên bị backend trả `403 PERMISSION_DENIED: "only approved products can be hidden by seller"`.
- **Gốc nghi ngờ**: `web/src/features/seller/ProductFormView.tsx:90-94` lệch với `services/product/internal/server/server.go:1437-1441`.

### `CAT-B6` (P1) — Không thể thêm lại biến thể cùng `sku_code` sau khi đã gỡ khỏi sản phẩm
- **UC liên quan**: `UC-S07` (Cao), `UC-S02` (Cao).
- **Tái hiện**:
  ```bash
  # 1. Gỡ SKU AUDIT-CAT-WHT-L bằng PUT chỉ giữ lại SKU 1:
  curl -sS -X PUT "http://localhost:48080/api/v1/seller/products/<product_id>" \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"skus":[{"id":"<sku1_id>","sku_code":"AUDIT-CAT-BLK-M","name":"Đen / M","price_vnd":150000,"stock":5,"weight_grams":220}]}'
  # 2. Thêm lại biến thể với mã SKU AUDIT-CAT-WHT-L:
  curl -sS -X PUT "http://localhost:48080/api/v1/seller/products/<product_id>" \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"skus":[{"id":"<sku1_id>","sku_code":"AUDIT-CAT-BLK-M","name":"Đen / M","price_vnd":150000,"stock":5,"weight_grams":220},{"sku_code":"AUDIT-CAT-WHT-L","name":"Trắng / L mới","price_vnd":260000,"stock":3,"weight_grams":240}]}'
  ```
- **Mong đợi**: Seller gỡ nhầm hoặc mở bán lại một biến thể (màu/size cũ với mã SKU chuẩn) phải lưu được bình thường (hoặc kích hoạt lại SKU cũ), không bị khoá vĩnh viễn mã SKU.
- **Thực tế**: Bước 2 trả `409 ALREADY_EXISTS: "duplicate sku_code within product"` vì `DeactivateSKU` giữ nguyên dòng `status = 'INACTIVE'` đụng ràng buộc `UNIQUE (product_id, sku_code)`.
- **Gốc nghi ngờ**: `services/product/migrations/000001_init.sql:52`, `services/product/internal/server/server.go:460-492`.

### `CAT-B7` (P2) — Thao tác quản trị danh mục và duyệt sản phẩm không ghi `audit_logs`
- **UC liên quan**: `UC-A02` (Cao), `UC-A04` (Trung bình), `docs/spec.md:26` (*"Audit log chỉ ghi thêm cho mọi thao tác quản trị"*).
- **Tái hiện**:
  ```bash
  # Sau khi POST /api/v1/admin/products/<product_id>/review hoặc POST/PUT/DELETE /api/v1/admin/categories:
  curl -sS "http://localhost:48080/api/v1/admin/audit-logs?target_id=<product_id>" -H "Authorization: Bearer $TOKEN"
  curl -sS "http://localhost:48080/api/v1/admin/audit-logs?target_id=<category_id>" -H "Authorization: Bearer $TOKEN"
  ```
- **Mong đợi**: Ghi nhận đầy đủ ai, hành động, đối tượng, dữ liệu trước/sau, lý do vào `audit_logs` như `AdminReviewShop` ở `services/user`.
- **Thực tế**: Cả hai truy vấn đều trả `total: 0`.
- **Gốc nghi ngờ**: `services/product/internal/server/server.go:50-196,588-625`, `services/gateway/internal/router/handlers_product.go:230-301,643-666`.

### `CAT-B8` (P2) — Xoá danh mục từng có sản phẩm đã xoá mềm (soft-delete) gây `500 INTERNAL_ERROR` (lỗi FK `23503`) và kẹt vĩnh viễn
- **UC liên quan**: `UC-A04` (Trung bình).
- **Tái hiện**:
  ```bash
  # 1. Tạo danh mục mới -> tạo 1 SP trong danh mục đó -> xoá mềm SP bằng DELETE /api/v1/seller/products/<prod_id>
  # 2. Admin xoá danh mục rỗng:
  curl -sS -X DELETE "http://localhost:48080/api/v1/admin/categories/<cat_id>" -H "Authorization: Bearer $TOKEN"
  ```
- **Mong đợi**: Hoặc cho xoá sạch (nếu chỉ còn SP đã xoá mềm), hoặc trả lỗi nghiệp vụ `409 FAILED_PRECONDITION` có hướng dẫn rõ ràng, không trả `500` và không lộ thông báo lỗi SQL.
- **Thực tế**: Trả `500 INTERNAL_ERROR`: `{"error":{"code":"INTERNAL_ERROR","message":"delete category: ERROR: update or delete on table \"categories\" violates foreign key constraint \"products_category_id_fkey\" on table \"products\" (SQLSTATE 23503)","trace_id":"..."}}`. Danh mục bị kẹt vĩnh viễn không thể xoá qua API (dù không còn sản phẩm hoạt động nào).
- **Gốc nghi ngờ**: `services/product/queries/product.sql:24-27` (`CountProductsInCategory` lọc `AND deleted_at IS NULL`) mâu thuẫn với `services/product/migrations/000001_init.sql:19` (`REFERENCES categories(id) ON DELETE RESTRICT`).

### `CAT-B9` (P2) — Các lỗi cây danh mục: cho tạo vòng lặp (`cycle`), lọc cha chỉ quét 1 cấp con, không chuẩn hoá slug, reset `commission_rate_bps` về 0 khi sửa
- **UC liên quan**: `UC-A04` (Trung bình), `UC-B20` (Cao).
- **Tái hiện**:
  ```bash
  # 1. Tạo Cha -> Con -> Cháu; sau đó đặt Cha.parent_id = Con.id:
  curl -sS -X PUT "http://localhost:48080/api/v1/admin/categories/<parent_id>" \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"name":"AUDIT-cat Cha","slug":"audit-cat-cha","parent_id":"<child_id>","commission_rate_bps":600,"sort_order":90}'
  # 2. Tạo & duyệt SP trong Cháu, lọc theo Cha:
  curl -sS "http://localhost:48080/api/v1/products?category_id=<parent_id>"
  # 3. Tạo danh mục với slug không hợp lệ:
  curl -sS -X POST "http://localhost:48080/api/v1/admin/categories" \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"name":"AUDIT-cat BadSlug","slug":"AUDIT-cat / Có Dấu!","commission_rate_bps":500}'
  # 4. Sửa tên danh mục mà không truyền commission_rate_bps:
  curl -sS -X PUT "http://localhost:48080/api/v1/admin/categories/<grandchild_id>" \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"name":"AUDIT-cat Cháu Sửa","slug":"audit-cat-chau","parent_id":"<child_id>"}'
  ```
- **Mong đợi**: (1) Chặn vòng lặp tổ tiên `400`; (2) Lọc danh mục cha bao gồm mọi cấp con cháu (cả backend lẫn web); (3) Slug chuẩn hoá `[a-z0-9-]` như `CreateProduct`; (4) Không vô tình xoá `commission_rate_bps` về `0`; UI hiển thị cây cha-con và cho sửa `parent_id`.
- **Thực tế**: (1) Trả `200`, `parent_id = <child_id>` (tạo cycle 2 nút); (2) `byParentTotal: 0` trong khi `byChildTotal: 1`, `byGrandTotal: 1` (và `CatalogExplorer.tsx:189` so exact `product.rawCategorySlug !== filters.cat` làm rơi luôn cả cấp 2 khi sort mặc định `bestseller`); (3) Trả `201`, `slug: "AUDIT-cat / Có Dấu!"`; (4) Trả `200`, `commission_rate_bps: 0, sort_order: 0`.
- **Gốc nghi ngờ**: `services/product/internal/server/server.go:73-76,119-143`, `services/product/queries/product.sql:120,141`, `web/src/features/catalog/CatalogExplorer.tsx:189`, `web/src/features/admin/AdminCategoriesPanel.tsx:288-354`.

### `CAT-B10` (P2) — `AdminReviewProduct` chấp nhận từ chối (`REJECT`) với `reason` rỗng ở backend
- **UC liên quan**: `UC-A02` (Cao).
- **Tái hiện**:
  ```bash
  curl -sS -X POST "http://localhost:48080/api/v1/admin/products/<product_id>/review" \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"decision":"REJECT","reason":""}'
  ```
- **Mong đợi**: Backend từ chối với `400 INVALID_ARGUMENT` khi `decision=REJECT` mà thiếu lý do (spec `UC-A02`: *"từ chối kèm lý do"*).
- **Thực tế**: Trả `200`, `status: "REJECTED", rejection_reason: ""`. (Hiện chỉ kiểm tra trên UI `AdminProductModerationTab.tsx:149`.)
- **Gốc nghi ngờ**: `services/product/internal/server/server.go:602-605`.

### `CAT-B11` (P2) — `PATCH /api/v1/seller/skus/{sku_id}/stock` vẫn cho sửa tồn SKU đã vô hiệu hoá (`INACTIVE`)
- **UC liên quan**: `UC-S03` (Cao).
- **Tái hiện**:
  ```bash
  # Sau khi gỡ sku2 bằng PUT /seller/products/{id} (sku2.status = 'INACTIVE'):
  curl -sS -X PATCH "http://localhost:48080/api/v1/seller/skus/<inactive_sku_id>/stock" \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"new_stock":9,"expected_version":1}'
  ```
- **Mong đợi**: Từ chối bằng `409 FAILED_PRECONDITION` hoặc `404 NOT_FOUND` vì SKU đã bị ngừng hoạt động.
- **Thực tế**: Trả `200`, `{"sku":{...,"stock":9,"is_active":false}}` và ghi lịch sử tồn cho SKU đã xoá.
- **Gốc nghi ngờ**: `services/product/internal/server/server.go:792-794` (chỉ kiểm `cur.ProductDeleted`, không kiểm `cur.Status == store.SKUStatusInactive`).

### `CAT-B12` (P2) — Tìm kiếm tức thời ở `ShopHeader` đọc dữ liệu mock tĩnh và `searchProducts` tra sai `shopId` thật
- **UC liên quan**: `UC-B12` (Trung bình), `UC-B03` (Cao).
- **Tái hiện**: Gõ từ khoá của sản phẩm mới tạo `AUDIT-cat` vào ô tìm kiếm trên `ShopHeader` hoặc gõ tên xưởng `"Bạch Đằng"` trên `/tim-kiem`.
- **Mong đợi**: Gợi ý tìm kiếm phải phản ánh sản phẩm/giá thật từ backend; tìm theo tên xưởng/thành phố phải khớp với `CatalogProduct.shopName`.
- **Thực tế**: `ShopHeader.tsx:173` gọi `getInstantSearchSuggestions()` (`web/src/lib/search.ts:208-230`) vốn đọc cứng mảng tĩnh `PRODUCTS` (18 SP mẫu, giá tĩnh); `web/src/lib/search.ts:82` tra `SHOPS.find(s => s.id === product.shopId)` so chuỗi `"shop-bach-dang"` với UUID Postgres nên luôn trả `undefined`.
- **Gốc nghi ngờ**: `web/src/lib/search.ts:81-103,208-230`, `web/src/components/ShopHeader.tsx:173,471`.

### `CAT-B13` (P3) — Sai lệch hiển thị UI: trạng thái hết hàng ở trang seller không tự cập nhật và `ProductCard` chưa hiện khoảng giá min–max
- **UC liên quan**: `UC-S03` (Cao), `UC-S07` (Cao).
- **Tái hiện**: (1) Tại `/seller/san-pham`, đặt tồn SKU cuối cùng của SP `APPROVED` về `0` qua nút "Lưu tồn"; (2) Xem thẻ sản phẩm có 2 SKU giá khác nhau (`150.000₫` và `250.000₫`) trên `/danh-muc`.
- **Mong đợi**: (1) Badge trạng thái chuyển ngay sang `"Hết hàng"` (`OUT_OF_STOCK`); (2) Thẻ sản phẩm hiển thị khoảng giá `150.000₫ – 250.000₫` theo spec `UC-S07`.
- **Thực tế**: (1) `web/src/app/(seller)/seller/san-pham/page.tsx:204-212` chỉ thay `sku` trong state, không gọi lại `loadData()` nên badge vẫn hiện `"Đang bán"` cho tới khi F5; (2) `web/src/components/ProductCard.tsx:142` chỉ hiện `product.price` (`minPriceVnd`).
- **Gốc nghi ngờ**: `web/src/app/(seller)/seller/san-pham/page.tsx:204-212`, `web/src/components/ProductCard.tsx:142`.

---

## 4. Dữ liệu (`CAT-D<n>`)

**Kiểm chứng khớp seed lõi (đạt)**:
- `product_db` & `GET /api/v1/products?page=1&page_size=100` & `GET /api/v1/categories` & `GET /api/v1/shops`: đủ **6 danh mục**, **4 gian hàng**, **18 sản phẩm**, **159 SKU**, **0 sai lệch** về tên, slug, `price_vnd`, `stock`, `available_stock = stock - reserved_stock`, thuộc tính `color`/`color_code`/`size`, danh sách ảnh, ánh xạ danh mục và gian hàng; **0 sản phẩm/danh mục rác** ban đầu.
- Kiểm media từ `http://localhost:43000`: **40/40 đường dẫn ảnh** (`/products/*`, `/models/*`, `/atmosphere/*`) và **2 tài sản video** (`/atmosphere/jacquard-loom-loop.webm`, `/atmosphere/jacquard-loom-poster.jpg`) đều trả HTTP **`200`**. `TZ=UTC npm run check:data` đạt **15/15 PASS**.

**Vấn đề dữ liệu phát hiện**:
- **`CAT-D1` (Thiếu giá so sánh & `% giảm` trong DB/API)**: `10/18` sản phẩm trong `web/src/data/products.ts` có `originalPrice` (giá gốc gạch ngang và `% giảm`), nhưng `products`/`skus` trong `product_db` và `product.proto` hoàn toàn không có cột `original_price_vnd` / `compare_at_price_vnd`. Storefront đang đắp mock theo `slug` tại `web/src/lib/catalog-mapper.ts:307-310`; sản phẩm mới seller tạo không thể có giá so sánh.
- **`CAT-D2` (Thiếu bộ dữ liệu mẫu 20.000–50.000 sản phẩm & thiếu SP đa mức giá)**: `docs/spec.md:31,36` yêu cầu bộ dữ liệu mẫu **20.000–50.000 sản phẩm** phục vụ kiểm thử tìm kiếm và tải k6; hiện chỉ có `tools/seed/seed.mjs` nạp 18 sản phẩm tĩnh, chưa có script sinh dữ liệu quy mô lớn. Ngoài ra toàn bộ 18 SP seed đều có `min_price_vnd == max_price_vnd`, chưa có SP nào có biến thể lệch giá để kiểm hiển thị khoảng giá (`UC-S07`).
- **`CAT-D3` (Lệch hoa hồng & cấu trúc danh mục con giữa DB và `web/src/data/`)**: `web/src/data/shops.ts:412` ghi `Sợi Tự Nhiên Đà Nẵng` có `commissionRate: 0.055` (5,5%), còn `tools/seed/seed.mjs:574` đặt cứng toàn bộ 6 danh mục `commission_rate_bps = 500` (5,0%). Đồng thời `web/src/data/products.ts:248-380` định nghĩa 15 danh mục con (`subCategories`), nhưng `product_db.categories` chỉ có 6 danh mục gốc phẳng (`parent_id = NULL`), còn danh mục con trên web là map lọc thuộc tính tĩnh (`CatalogExplorer.tsx:122-138`).
- **`CAT-D4` (Số liệu thống kê giả lập trộn vào trang thật)**: Thẻ sản phẩm bịa điểm sao mặc định `4.7–4.9` (12–47 đánh giá) qua hàm hash tĩnh `getFallbackSampleRating` (`web/src/components/ProductCard.tsx:61-75`) dù chưa có bảng đánh giá; trang gian hàng `/gian-hang/[slug]` hiển thị chỉ số cứng từ `shops.ts` (`98,6%`, `Trong 15 phút`, địa chỉ kho, năm thành lập); 13 thuộc tính may mặc (`gender`, `material`, `fit`, `gsm`, `salesRank`, `modelInfo`) không lưu DB mà phủ theo slug 18 SP mẫu.
- **`CAT-D5` (Ghi chú tồn dư kiểm thử trên stack `nexusmart-audit`)**: Danh mục thử nghiệm `3cbad063-0541-40bb-9f23-bcb8b87aa0f6` (`AUDIT-cat Cháu (stuck by FK bug)`, `sort_order=999`) còn nằm lại trong `product_db.categories` do chính lỗi FK `CAT-B8` chặn xoá sau khi SP bên trong đã soft-delete; toàn bộ sản phẩm `AUDIT-cat` và 2 danh mục cha/con khác đã được xoá sạch qua API.

---

## 5. Đề xuất sửa (`CAT-F<n>`)

| Mã | Giải quyết | Việc cụ thể (file, RPC, migration, route) | Cỡ | Đổi proto? | Migration? | Cần ADR? |
| --- | --- | --- | --- | --- | --- | --- |
| `CAT-F1` | `CAT-B2` | Tách rõ chế độ public-only trên `GET /api/v1/products`: ở `web/src/lib/catalog.ts:138-141` gọi `fetchRawProducts` ẩn danh (`optionalAuth: false`) hoặc thêm cờ `public_only` ở `ListProductsRequest` (gateway ép `true` cho `/api/v1/products`, `false` cho `/seller/products` & `/admin/products`). | S | Tuỳ chọn (không bắt buộc nếu bỏ `optionalAuth` ở `GET /products`) | Không | Không |
| `CAT-F2` | `CAT-B4`, `CAT-B5`, `CAT-B6`, `CAT-B10`, `CAT-B11`, `CAT-B13` | `services/product`: (a) `UpdateProduct` ghi `stock_history`, gọi `syncProductStockStatus`, không cho `OUT_OF_STOCK→APPROVED` khi tổng tồn = 0; (b) `000004_sku_code_partial_unique.sql` đổi `UNIQUE(product_id, sku_code)` thành partial unique `WHERE status <> 'INACTIVE'` (hoặc tái kích hoạt SKU cũ); (c) `AdminReviewProduct` bắt buộc `reason != ""` khi `REJECT` và kiểm tồn > 0 khi `APPROVE`; (d) `UpdateStock` chặn SKU `INACTIVE`. `web/`: gỡ `HIDDEN` khỏi trạng thái `PENDING` ở `ProductFormView.tsx:90-94`; reload status ở `seller/san-pham/page.tsx:204-212`; hiện khoảng giá min–max ở `ProductCard.tsx:142`. | M | Không | Có (`product/000004_*`) | Không |
| `CAT-F3` | `CAT-B8`, `CAT-B9` | `services/product`: (a) `DeleteCategory` kiểm tra cả SP soft-delete (hoặc chuyển sang soft-delete danh mục `deleted_at` qua migration `000005_category_soft_delete.sql` thay vì `DELETE` cứng đụng FK); (b) `UpdateCategory` duyệt tổ tiên chặn cycle, chuẩn hoá `slug` qua `normalizeSlug`, chỉ cập nhật `commission_rate_bps`/`sort_order` khi có gửi (dùng wrapper/field mask hoặc pointer); (c) `ListProductsFiltered` & `CountProductsFiltered` dùng `WITH RECURSIVE` gom đủ mọi cấp danh mục con. `web/`: sửa `CatalogExplorer.tsx:189` đối chiếu theo cây `parentId` và bổ sung cột cha + chọn `parent_id` ở `AdminCategoriesPanel.tsx`. | M | Không (hoặc thêm `optional` cho `UpdateCategoryRequest`) | Có (`product/000005_*`) | Không |
| `CAT-F4` | `CAT-B3`, `CAT-B7` | Thêm transactional `outbox_events` + NATS publisher vào `services/product` phát `product.reviewed` / `product.upserted` / `product.deleted` (mở rộng subject `product.>` trên stream `NEXUSMART`); thêm handler ở `services/notification` gửi in-app + email cho seller khi duyệt/từ chối SP; gọi `UserService.WriteAuditLog` (qua service token hoặc event) trong `AdminReviewProduct` và `Create/Update/DeleteCategory`. | M | Không | Có (`product/000006_outbox.sql`) | Không (dùng NATS JetStream đã chốt) |
| `CAT-F5` | `CAT-B1` (ngắn hạn), `CAT-B12` | Ngắn hạn trước khi có Search service: thêm extension `unaccent` (hoặc cột `search_text` chuẩn hoá không dấu `foldVietnameseRune` + index `pg_trgm`) trong `product_db`, thoát `%`/`_` trước `ILIKE`; sửa `ShopHeader.tsx` gọi `GET /api/gw/products?keyword=...` (debounce 200ms) và sửa `web/src/lib/search.ts:82` dùng `product.shopName` thay vì tra `SHOPS` tĩnh. | M | Không | Có (`product/000007_search_text.sql`) | Không |
| `CAT-F6` | `CAT-B1` (dài hạn), `UC-B03`, `UC-B12`, `CAT-D2` | Dựng `Search` service (Elasticsearch / OpenSearch / Meilisearch) tiêu thụ event `product.>` (độ trễ <5s), hỗ trợ tiếng Việt không dấu, đồng nghĩa, fuzzy, facet động, gợi ý từ khoá & lịch sử tìm kiếm (Client + Server), bỏ trần 100 SP trên `CatalogExplorer`; viết script sinh bộ dữ liệu tải 20.000–50.000 sản phẩm. | L | Có (`search.proto`) | Có (index + bảng lịch sử tìm kiếm) | **CÓ** (AGENTS.md luật 11: chạy `evaluate-oss` chọn search engine và ghi ADR vào `docs/adr/` trước khi code) |
| `CAT-F7` | `UC-B10`, `UC-S16`, `UC-A15`, `CAT-D1`, `CAT-D4` | Mở rộng `product` (hoặc tách domain): (a) thêm `original_price_vnd` vào `skus`/`products`; (b) thêm bảng & RPC `reviews` (ràng buộc xác minh đơn `COMPLETED` qua `OrderService`, 1 lần/mục đơn, cập nhật điểm trung bình, seller reply 1 lần/sửa 24h, phát event cho AI summary); (c) thêm bảng & RPC `banned_keywords` / `prohibited_categories` chuẩn hoá văn bản chống lách, kiểm tra khi `CreateProduct`/`UpdateProduct`/tạo review và quét lại SP đang bán. | L | Có (`product.proto`) | Có | Không (dùng PostgreSQL/NATS đã chốt; riêng upload ảnh đánh giá/SP cần ADR Object Storage ở `CAT-F8`) |
| `CAT-F8` | `UC-S13`, Media upload | Thêm năng lực lưu file (MinIO/S3) cho ảnh sản phẩm/đánh giá và file CSV/XLSX nhập hàng loạt; dựng job nền kiểm định từng dòng (`import_jobs`, `import_job_errors`) + tải file mẫu & file lỗi cho `UC-S13`. *(Các UC mức Thấp `UC-B13`, `UC-S12`, `UC-A08` chỉ triển khai sau khi chốt ưu tiên ngoài phạm vi bắt buộc.)* | L | Có (`product.proto`) | Có | **CÓ** (AGENTS.md luật 11: cần `evaluate-oss` + ADR cho Object Storage) |

---

## 6. Đã kiểm gì, chưa kiểm gì

- **Đã kiểm trên code HEAD (`0db2cdd`)**:
  - Đọc toàn bộ `docs/spec.md`, `docs/contracts.md`, `docs/CORE_REPORT.md` (§5–6), `docs/WEB_INTEGRATION_REPORT.md` (§8), `docs/web-gap-audit.md`, `web/FEATURE_MATRIX.md`, `web/CLAUDE.md`, `tools/seed/seed.mjs`.
  - Đọc mã nguồn `proto/nexusmart/product/v1/product.proto`, `proto/nexusmart/user/v1/user.proto`, toàn bộ `services/product/` (migrations, `queries/product.sql`, `pg_store.go`, `server.go`, `cmd/product/main.go`), `services/gateway/internal/router/{router,handlers_product,handlers_user}.go`, `httpx/response.go`, và các file web liên quan (`lib/catalog.ts`, `lib/catalog-mapper.ts`, `lib/search.ts`, `CatalogExplorer.tsx`, `ShopHeader.tsx`, `ProductCard.tsx`, `ProductDetailClient.tsx`, `ShopDetailClient.tsx`, `HomeClient.tsx`, `ProductFormView.tsx`, `seller/san-pham/page.tsx`, `seller/ho-so/page.tsx`, `seller/tuong-tac/page.tsx`, `AdminProductModerationTab.tsx`, `AdminCategoriesPanel.tsx`, `admin/kiem-duyet/page.tsx`, `admin/noi-dung/page.tsx`, `PdpReviews.tsx`, `MockStoreContext.tsx`).
  - Chạy test chỉ đọc trên host: `cd services/product && go test -count=1 ./...` (PASS) và `cd web && TZ=UTC npm run check:data` (15/15 PASS).
- **Đã kiểm sống trên stack `nexusmart-audit`**:
  - Đối chiếu 100% 6 danh mục, 4 shop, 18 sản phẩm, 159 SKU giữa `product_db`/API và `web/src/data/*`; kiểm HTTP `200` cho 40 ảnh + 2 video từ `http://localhost:43000`.
  - Kiểm tìm kiếm có dấu/không dấu/đồng nghĩa/wildcard, lọc danh mục/shop/giá, sắp xếp, phân trang; chi tiết SP theo `id`/`slug`, shop công khai không lộ PII; SSR storefront + seller + admin routes.
  - Tạo buyer `audit-cat-1@nexusmart.local` (OTP Mailpit); kiểm validation giá âm/0/số thực/tồn âm, tạo SP đa biến thể, quyền hiển thị SP `PENDING`/`REJECTED`/`HIDDEN` (và tái hiện lộ SP `PENDING` ra `/danh-muc` khi admin đăng nhập), cô lập `shop_id` chéo giữa 2 seller, chống tự duyệt, duyệt/từ chối rỗng lý do, thông báo seller & audit log.
  - Kiểm optimistic locking `PATCH /seller/skus/{id}/stock` (`409` version cũ), giữ tồn qua đơn VietQR `AUDIT-cat` (`available_stock = stock - reserved_stock`, chặn `new_stock < reserved_stock` → `409`), tự chuyển `OUT_OF_STOCK ↔ APPROVED`, đối chiếu `stock_history` trong DB, tái hiện lỗi `PUT` stock=0, lỗi re-add `sku_code`, lỗi `PATCH` SKU `INACTIVE`.
  - Kiểm cây danh mục 3 cấp, tái hiện lỗi cycle, lỗi lọc cấp cháu, lỗi slug không chuẩn hoá, lỗi mất `commission_rate_bps`, và lỗi `500` FK khi xoá danh mục có SP đã soft-delete.
- **Chưa kiểm**:
  - Tương tác DOM trình duyệt thật (Playwright click/scroll ở 390/768/1440px) — mới kiểm SSR HTML, API qua Gateway/BFF và mã nguồn React/TSX.
  - Kiểm thử tải / p95 độ trễ tìm kiếm ở quy mô 20.000–50.000 sản phẩm (vì chưa có Search service và chưa có bộ dữ liệu 20k–50k SP).
  - Các UC thuộc domain AI assistant (`UC-AI02`, `UC-AI05`, `UC-AI06`, `UC-AI07`) và khuyến mãi (`UC-S18`, `UC-B16`) chỉ ghi nhận điểm giao diện tiếp giáp với `product`.
