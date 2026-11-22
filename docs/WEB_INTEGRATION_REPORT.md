# Báo cáo tích hợp `web/` với Gateway — Giai đoạn 0b

> **Nhánh / worktree**: `feat/web-gateway` (`/home/tai/Document/nexusmart-web`) · **Trạng thái**: chưa commit (chờ manager review cuối và commit).
> **Phạm vi**: Nối `web/` (Next.js 15 App Router, React 19, TypeScript) với backend NexusMart qua lớp BFF cùng origin cho trọn luồng mua và các màn Seller/Admin đã có RPC, không thêm dependency runtime vào `web/`, không chạm vùng cấm `services/order`.

---

## 1. Tóm tắt

- `web/` đã chuyển từ 100% dữ liệu mẫu sang gọi backend thật qua **BFF cùng origin** (trình duyệt chỉ gọi `/api/auth/*` và `/api/gw/*`; Next server gọi Gateway qua `GATEWAY_URL`, không bật CORS trên Gateway).
- **Phiên & bảo mật**: Access token và refresh token chỉ lưu trong cookie `HttpOnly`, `SameSite=Lax`, host-only (`nm_access_token`, `nm_refresh_token`); mọi thao tác ghi kiểm tra CSRF theo header `Origin`; làm mới refresh token xoay vòng có **gộp in-flight + cache ân hạn 5s** để tránh request lệch pha làm thu hồi token family.
- **Dữ liệu thật**: Toàn bộ luồng mua (đăng ký + OTP Mailpit, đăng nhập, danh mục, sản phẩm/SKU/tồn thật, gian hàng, flash sale, giỏ hàng, áp voucher/Xu, tính phí ship 2 cấp, đặt hàng COD & VietQR mock kèm trang chờ SSE/polling, danh sách & chi tiết đơn hàng, sổ địa chỉ, thông báo) và phần lõi Seller Center / Admin Portal đã nối API thật.
- **Tách mock sạch**: Đã xóa `web/src/context/StoreContext.tsx`; tách giỏ hàng thật sang `CartContext`, phiên thật sang `SessionContext`, và gom toàn bộ phần chưa có backend vào `web/src/mock/` và `web/src/data/`. Không còn giỏ hàng, đơn hàng hay token trong `localStorage`.

---

## 2. Bảng route → nguồn dữ liệu

> **Ghi chú số lượng route**: Bản `web/` gốc có **44 page route** (24 `(shop)`, 11 `(seller)`, 9 `(admin)`). Giai đoạn 0b bổ sung thêm **1 page route mới** (`/thanh-toan/cho-thanh-toan/[paymentId]` cho màn chờ VietQR) — tổng **45 `page.tsx`** (gồm **12 Thật**, **19 Kết hợp**, **13 Mock**, **1 Chuyển hướng tĩnh**) và **8 BFF API route handler** (`/api/auth/*` × 7, `/api/gw/[...path]`). Bảng dưới được xác minh trực tiếp từ code hiện tại của từng route, không sao chép từ `docs/web-gap-audit.md`.

### 2.1 Storefront `(shop)` — 25 page route (24 gốc + 1 mới)

*(Layout `(shop)/layout.tsx` gắn `ShopHeader`, `ShopFooter`, và 2 panel nổi mock `BuyerChatPanel`, `AiAssistantPanel` từ `useMockStore`.)*

| # | Route | Trạng thái | Nguồn dữ liệu thực tế trong code | Lý do / Ghi chú |
| --- | --- | --- | --- | --- |
| 1 | `/` | **Kết hợp** | **Thật** (`gatewayFetch` qua `lib/catalog.ts`): `/categories`, `/shops`, `/products?page_size=100`, `/flash-sales?include_upcoming=true`. **Mock**: `mock/productEditorial.ts` (phủ ảnh lookbook, GSM, tạp chí, hạng hội viên, banner chiến dịch), `SizeFinder`. | Lõi catalog/shop/flash sale thật; phần biên tập/CMS & bảng size giữ mock theo mục 6 brief. |
| 2 | `/danh-muc` | **Kết hợp** | **Thật** (`gatewayFetch` qua `lib/catalog.ts`): `/categories`, `/shops`, `/products` (`category_slug`, `shop_slug`, `keyword`, `min_price_vnd`, `max_price_vnd`, `sort_by`). **Mock/client**: `productEditorial.ts`, `CatalogExplorer.tsx` lọc 13 nhóm thuộc tính & xếp `bestseller` phía client trên <= 100 SP, từ khóa xu hướng, mẹo bảo quản. | Backend chưa có cột thuộc tính may mặc và Search service (`ponytail:`). |
| 3 | `/tim-kiem` | **Kết hợp** | **Thật** (`gatewayFetch` qua `lib/catalog.ts`): `/products?keyword=...`, `/categories`, `/shops`. **Mock/client**: tìm bỏ dấu tiếng Việt, đồng nghĩa, sửa chính tả (`lib/search.ts`), từ khóa xu hướng, lọc 13 nhóm facet trên <= 100 SP. | Chưa có Search service (ngoài phạm vi 0b). |
| 4 | `/san-pham/[slug]` | **Kết hợp** | **Thật** (`gatewayFetch`, `api()`, `CartContext`): `/products/slug/{slug}` (sai slug → 404), `/shops/{id}`, `/products`, `/vouchers`, `POST /vouchers/{id}/save`, `/flash-sales`, `/shipping/provinces`, `POST /cart/items`. **Mock**: `productEditorial.ts`, `useMockStore`, `features/product/*` (đánh giá, H&Đ, báo có hàng, bảng size, yêu thích, chat). | Giá/SKU/tồn/giỏ/voucher thật; đánh giá, H&Đ, wishlist, back-in-stock giữ mock. |
| 5 | `/gian-hang/[slug]` | **Kết hợp** | **Thật** (`gatewayFetch`, `api()`): `/shops/slug/{slug}` (khác `ACTIVE` → 404), `/shops`, `/products?shop_id=...`, `/categories`, `/vouchers`, `POST /vouchers/{id}/save`. **Mock**: `productEditorial.ts`, `useMockStore`, `SizeFinder` (hồ sơ nhà máy/QC, bảng size riêng, BST shop, chat). | Hồ sơ công khai, SP, voucher của shop thật; nội dung xưởng/size/chat giữ mock. |
| 6 | `/flash-sale` | **Kết hợp** | **Thật** (`gatewayFetch`, `CartContext`): `/flash-sales?include_upcoming=true`, `/products`, `/shops`, `POST /cart/items`, link mua ngay `/thanh-toan?sku=...&flash=...`. **Mock**: `productEditorial.ts` (khối Deal sốc & Combo đối chiếu slug lên SP thật), nút nhắc giờ cục bộ. | Khung giờ và `remaining_slots` từ Redis thật; Deal sốc/Combo chưa có ở `promotion`. |
| 7 | `/gio-hang` | **Thật** | **Thật** (`CartContext`, `api()`): `GET/POST/PATCH/DELETE /cart*`, `/products?page_size=100`, `/shops`, `/users/me/addresses`, `POST /shipping/calculate-fee`, `/vouchers`, `/coins/balance`, `POST /promotions/preview`. *(Chỉ dùng `openChat`/`showToast` từ `useMockStore`.)* | Toàn bộ dữ liệu giỏ, giá, tồn, voucher, Xu, phí ship thật; trạng thái tick `selected` giữ tạm trong bộ nhớ React vì `cart` chưa có RPC `SetItemSelected`. |
| 8 | `/thanh-toan` | **Thật** | **Thật** (`requireUser`, `gatewayFetch`, `api()`): `/users/me/addresses`, `/shipping/provinces*`, `/cart` (hoặc chế độ mua ngay `?sku=&qty=&flash=`), `/coins/balance`, `/vouchers`, `/shops`, `POST /shipping/calculate-fee`, `POST /promotions/preview`, `POST /checkout` (kèm `Idempotency-Key` sinh ở server). | 100% backend thật cho COD và VietQR mock; không dùng mock data. |
| 9 | `/thanh-toan/cho-thanh-toan/[paymentId]` *(mới ở 0b)* | **Thật** | **Thật** (`requireUser`, `gatewayFetch`, `api()`, `EventSource`): `/payments/{id}` (403/404 → 404), `/orders/{id}`, SSE `/api/gw/payments/{id}/events` + fallback polling `/payments/{id}`, `POST /payments/{id}/retry`, `/products`, `/shops`. | Route mới thêm ở 0b cho màn chờ và thử lại thanh toán VietQR thật. |
| 10 | `/don-hang` | **Kết hợp** | **Thật** (`requireUser`, `gatewayFetch`, `api()`): `/orders` (phân trang, lọc trạng thái), `/shops`, `/products?page_size=100` (tra ảnh/slug), `POST /cart/items` (mua lại), `RetryPaymentBox` (`/payments/{id}*`). **Mock phụ**: dải gợi ý cuối trang `ProductAddonStrip` đọc `PRODUCTS.slice(0, 4)` từ `web/src/data`. | Toàn bộ danh sách đơn và thao tác là thật; chỉ dải gợi ý cuối trang còn dùng danh sách tĩnh. |
| 11 | `/don-hang/[id]` | **Kết hợp** | **Thật** (`requireUser`, `gatewayFetch`, `api()`): `/orders/{id}` (403/404 → 404), `/orders/{id}/timeline`, `/shipping/shipments/{id}`, `/shops/{id}`, `/products?shop_id=...`, `POST /orders/{id}/{cancel,confirm-received,return}`, `RetryPaymentBox`. **Mock phụ**: dải gợi ý cuối trang `ProductAddonStrip` đọc `PRODUCTS` từ `web/src/data`. | Toàn bộ chi tiết, timeline, vận đơn và thao tác đơn thật; chỉ dải gợi ý cuối trang dùng danh sách tĩnh. |
| 12 | `/tai-khoan` | **Kết hợp** | **Thật** (`requireUser`, `gatewayFetch`, `api()`): `GET/PATCH /users/me`, `GET/POST/PUT/DELETE /users/me/addresses`, `/shipping/provinces*`, `/coins/balance`, `/orders`, `/vouchers`, `POST /vouchers/{id}/save`, `POST /shops/register`, `GET /shops/me`, `/notifications`, `/notifications/preferences`. **Mock**: `AccountMockTabs.tsx`, `useMockStore`, `data` (hạng hội viên, thẻ quà tặng, số đo, yêu thích, giới thiệu). | Hồ sơ, địa chỉ 2 cấp, Xu, đơn, voucher, gian hàng, thông báo thật; tiện ích người xem và loyalty giữ mock. |
| 13 | `/dang-nhap` | **Kết hợp** | **Thật** (`SessionContext`): `POST /api/auth/login`, `POST /api/auth/logout`. **Mock tĩnh**: 4 khung ảnh giới thiệu ở cột phải đọc từ `PRODUCTS` & `MODEL_SHOTS` (`web/src/data`). | Xác thực cookie `HttpOnly` 100% thật; ảnh minh họa cột phải tĩnh. |
| 14 | `/dang-ky` | **Kết hợp** | **Thật** (`authApi`): `POST /api/auth/register`, `POST /api/auth/verify-otp`, `POST /api/auth/resend-otp`. **Mock tĩnh**: 4 khung ảnh giới thiệu cột phải từ `PRODUCTS` & `MODEL_SHOTS` (`web/src/data`). | Đăng ký + OTP Mailpit 100% thật; ảnh minh họa cột phải tĩnh. |
| 15 | `/tra-cuu-don-hang` | **Mock** | `mock/guestOrderLookup.ts`, `web/src/data`, `useMockStore`. | Chưa có RPC tra cứu đơn không đăng nhập ở `services/order` (vùng cấm không sửa trong 0b). |
| 16 | `/tra-cuu-don` | **Chuyển hướng** | `redirect("/tra-cuu-don-hang")` (HTTP 307). | Alias tĩnh, không đọc dữ liệu. |
| 17 | `/bo-suu-tap` | **Mock** | `COLLECTIONS`, `CAMPAIGNS`, `getProductById`, `MODEL_SHOTS` (`web/src/data`). | CMS bộ sưu tập ngoài phạm vi 0b (mục 6 brief). |
| 18 | `/bo-suu-tap/[slug]` | **Mock** | SSG theo `COLLECTIONS`, `getProductById` (`web/src/data`). | CMS bộ sưu tập ngoài phạm vi 0b. |
| 19 | `/tap-chi` | **Mock** | `JOURNAL_ARTICLES`, `CARE_GUIDE_LIBRARY`, `getProductById` (`web/src/data`). | CMS tạp chí ngoài phạm vi 0b. |
| 20 | `/tap-chi/[slug]` | **Kết hợp** | **Mock**: nội dung 4 bài viết SSG & mẹo bảo quản (`web/src/data`). **Thật**: thẻ mua nhúng sản phẩm trong `JournalDetailClient.tsx` gọi `resolveGatewaySkuId()` (`GET /api/gw/products/slug/{slug}`) và `CartContext.addToCart()` (`POST /api/gw/cart/items`). | Bài viết giữ mock CMS; hành động mua nhúng đã nối SKU & giỏ hàng thật. |
| 21 | `/chien-dich/[slug]` | **Mock** | SSG theo `CAMPAIGNS`, `CARE_GUIDE_LIBRARY`, `getProductById` (`web/src/data`). | Chưa có dịch vụ chiến dịch/sao kê CSR (ngoài phạm vi 0b). |
| 22 | `/ve-nexusmart` | **Mock** | `BRAND_STORY_PILLARS`, `BRAND_TIMELINE`, `PLATFORM_CHANGELOG`, `SHOPS`, `getProductsByShop` (`web/src/data`). | Trang giới thiệu tĩnh (chưa gọi `GET /shops`). |
| 23 | `/hoi-vien` | **Mock** | `MEMBERSHIP_TIERS`, `INITIAL_USER_COINS`, `REFERRAL_CONFIG`, `XU_POLICY`, `PRODUCTS` (`web/src/data`), `useMockStore`. *(Chỉ lấy tên từ `useSession()` và nút lưu mã `NEXUSVIP12` qua `CartContext.saveVoucherCode`.)* | Loyalty 4 hạng giữ mock (khác ghi chú dự kiến ở `web-gap-audit.md`: trang này chưa gọi `GET /coins/balance`; số dư Xu thật xem ở `/tai-khoan`, `/gio-hang`, `/thanh-toan`). |
| 24 | `/the-qua-tang` | **Mock** | `GIFT_CARD_*`, `EDU_DISCOUNT_CONFIG`, `PRODUCTS` (`web/src/data`), `useMockStore`. *(Nút dùng mã `EDU15` gán vào `CartContext.setPlatformVoucher`.)* | Chưa có dịch vụ thẻ quà tặng và xác thực `.edu` (ngoài phạm vi 0b). |
| 25 | `/tro-giup` | **Mock** | `HELP_FAQ_ITEMS`, `SUPPORT_CHANNELS`, `COMPLAINT_SLA`, `PRODUCTS` (`web/src/data`), `useMockStore`. *(Chỉ điền sẵn thông tin liên hệ từ `useSession()`.)* | Chưa có Support/Ticket/Chat/AI service (ngoài phạm vi 0b). |

### 2.2 Seller Center `(seller)` — 11 page route

*(Toàn bộ route group được `(seller)/layout.tsx` bảo vệ bằng `requireRole(["seller"], "/seller")`; `SellerContext` nạp `GET /seller/shop` — fallback `GET /shops/me` — và badge `GET /seller/orders?status=CONFIRMED`. Sidebar `SellerShell` còn hiển thị badge mock cho tin nhắn và tương tác.)*

| # | Route | Trạng thái | Nguồn dữ liệu thực tế trong code | Lý do / Ghi chú |
| --- | --- | --- | --- | --- |
| 26 | `/seller` | **Thật** | **Thật** (`api()`): `/seller/orders?page=1&page_size=100`, `/seller/products?page=1&page_size=100`, `/seller/balance`, `POST /seller/orders/{id}/confirm`. *(Biểu đồ 7 ngày và tồn thấp tính trực tiếp từ dữ liệu thật; chỉ dùng `showToast` từ `useMockStore`.)* | 100% số liệu thật từ backend (vượt mức dự kiến giữ mock biểu đồ trong `web-gap-audit.md`). |
| 27 | `/seller/san-pham` | **Thật** | **Thật** (`api()`): `/seller/products?page=1&page_size=100`, `/categories`, `PATCH /seller/skus/{sku_id}/stock` (`expected_version`), `DELETE /seller/products/{id}`. | Quản lý sản phẩm mọi trạng thái và cập nhật tồn optimistic locking thật. |
| 28 | `/seller/san-pham/moi` | **Thật** | **Thật** (`api()` trong `ProductFormView.tsx`): `/categories`, `POST /seller/products` (hỗ trợ `slug` & ma trận SKU). | Tạo sản phẩm mới vào backend thật. |
| 29 | `/seller/san-pham/[id]` | **Thật** | **Thật** (`api()` trong `ProductFormView.tsx`): `/categories`, `GET /seller/products/{id}` (khác shop → 403), `PUT /seller/products/{id}`, `DELETE /seller/products/{id}`. | Chỉnh sửa/ẩn/xóa sản phẩm của chính shop. |
| 30 | `/seller/don-hang` | **Thật** | **Thật** (`api()`): `/seller/orders?page=1&page_size=100`, `POST /seller/orders/{id}/confirm`, `POST /seller/orders/{id}/ship`. | Toàn bộ danh sách và xử lý đơn thật. |
| 31 | `/seller/don-hang/[id]` | **Thật** | **Thật** (`api()`): `/seller/orders/{id}` (khác shop → 403), `/orders/{id}/timeline`, `/shipping/shipments/{id}`, `POST /seller/orders/{id}/{confirm,ship}`, `POST /orders/{id}/cancel`. | Chi tiết, timeline, vận đơn và thao tác đơn thật. |
| 32 | `/seller/khuyen-mai` | **Kết hợp** | **Thật** (`api()`): `/seller/vouchers`, `POST /seller/vouchers`, `/flash-sales?include_upcoming=true`, `/seller/products`, `POST /seller/flash-sales/{id}/items`. **Mock**: `PromotionsBundleTab`, `PromotionsCampaignTab`, `sellerStore`, `PRODUCTS`. | Voucher shop & đăng ký flash sale thật (item `PENDING` mới gửi chỉ giữ tạm trong state phiên); Combo & đăng ký BST giữ mock. |
| 33 | `/seller/tai-chinh` | **Kết hợp** | **Thật** (`api()`): `/seller/balance` (`available_vnd`, `pending_vnd`, `total_earned_vnd`), `/seller/orders?page=1&page_size=50`. **Mock**: `sellerStore.getShopBankAccount` (thông tin tài khoản ngân hàng tham chiếu). | Số dư & bảng đối chiếu theo đơn thật; chưa có Payout service. |
| 34 | `/seller/ho-so` | **Kết hợp** | **Thật** (`SellerContext` → `/seller/shop`): tên, slug, trạng thái, mô tả, SĐT, giấy phép. **Mock**: `sellerStore` (hồ sơ nhà máy/QC, bảng size riêng, cấu hình freeship/menu). | Chưa có RPC `UpdateShop` và cấu hình xưởng ở backend. |
| 35 | `/seller/chat` | **Mock** | `useMockStore` (`chatThreads`), `sellerStore`, `web/src/data` (`SHOPS`, `getSellerWorkspace`). | Chưa có Chat service (ngoài phạm vi 0b). |
| 36 | `/seller/tuong-tac` | **Mock** | `sellerStore`, `web/src/data` (đánh giá, Q&A, FAQ, lookbook, khách chờ có hàng). | Chưa có Review/Q&A/Lookbook/Back-in-stock service. |

### 2.3 Admin Portal `(admin)` — 9 page route

*(Toàn bộ route group được `(admin)/layout.tsx` bảo vệ bằng `requireRole(["admin", "super_admin"], "/admin")`; `AdminShell` gọi `admin/shops?status=PENDING`, `admin/products?status=PENDING`, `admin/ledger/summary` cho badge thật và đọc `disputes` mock cho badge khiếu nại.)*

| # | Route | Trạng thái | Nguồn dữ liệu thực tế trong code | Lý do / Ghi chú |
| --- | --- | --- | --- | --- |
| 37 | `/admin` | **Thật** | **Thật** (`api()`): `/admin/shops?status=PENDING`, `/admin/products?status=PENDING`, `/admin/ledger/summary`, `/admin/orders?page=1&page_size=50`, `/admin/users?page=1&page_size=1`, `/admin/audit-logs?page=1&page_size=5`. | Biểu đồ 7 ngày và toàn bộ KPI tổng hợp trực tiếp từ API thật, không dùng mock. |
| 38 | `/admin/gian-hang` | **Thật** | **Thật** (`api()`): `/admin/shops?page=1&page_size=100`, `POST /admin/shops/{id}/review` (`APPROVE`/`REQUEST_INFO`/`REJECT`). | Duyệt gian hàng thật (tự nâng `buyer → seller` và ghi audit log). |
| 39 | `/admin/doi-soat` | **Kết hợp** | **Thật** (`api()`): `/admin/ledger/summary`, `POST /admin/payments/reconcile`, `/admin/orders`, `POST /admin/payments/{id}/refund` (kèm `Idempotency-Key`). **Mock**: tab lịch sử phiên đối soát mẫu đọc `ADMIN_RECONCILIATION` (`web/src/data`). | Sổ cái cân bằng, chạy đối soát và hoàn tiền thật; chưa có RPC liệt kê lịch sử `reconciliation_runs`. |
| 40 | `/admin/audit-log` | **Thật** | **Thật** (`api()`): `/admin/audit-logs` (phân trang, lọc `target_type`, `actor_id`, `target_id`). | 100% nhật ký kiểm toán thật từ `user` service. |
| 41 | `/admin/cau-hinh` | **Kết hợp** | **Thật** (`api()`): `AdminCategoriesPanel` (`/categories`, `POST/PUT/DELETE /admin/categories`) và `AdminUsersPanel` (`/admin/users`, `PATCH /admin/users/{id}/{status,role}`). **Mock**: 4 tab cấu hình động (đặt hàng/ship, đổi trả, cổng thanh toán, mẫu email/ZNS) dùng `useMockStore` & `auditStore`. | Quản trị danh mục & người dùng thật; chưa có Platform Config service. |
| 42 | `/admin/kiem-duyet` | **Kết hợp** | **Thật** (`api()` trong `AdminProductModerationTab`): `/admin/products`, `/admin/products/{id}`, `POST /admin/products/{id}/review`, `/admin/shops`, `/categories`. **Mock**: 3 tab duyệt đánh giá, Q&A, media & từ khóa cấm (`auditStore`, `web/src/data`). | Duyệt sản phẩm thật; chưa có Moderation service cho đánh giá/Q&A/media. |
| 43 | `/admin/thanh-vien` | **Kết hợp** | **Thật** (`api()` trong `AdminPromoCoinsPanel`): `/vouchers`, `POST /admin/vouchers`, `/flash-sales?include_upcoming=true`, `POST /admin/flash-sales`, `POST /admin/flash-sale-items/{item_id}/approve`, `/admin/users`, `POST /admin/coins/credit`. **Mock**: tab hạng hội viên/giới thiệu/`.edu` và thẻ quà tặng (`auditStore`, `web/src/data`). | Voucher sàn, flash sale và cộng Xu thật; admin phải nhập tay `item_id` để duyệt item flash sale. |
| 44 | `/admin/khieu-nai` | **Mock** | `useMockStore` (`disputes`), `auditStore`, `web/src/data`. | Chưa có Dispute/SLA workflow và RPC duyệt trả hàng ở `services/order` (vùng cấm). |
| 45 | `/admin/noi-dung` | **Mock** | `auditStore` (`useAdminContentState`), `web/src/data`. | Chưa có CMS / Search Admin / CSR service (ngoài phạm vi 0b). |

---

## 3. Endpoint gateway, RPC, field và migration đã thêm

Đối chiếu trực tiếp từ `git diff HEAD -- services proto docs/contracts.md` và các file migration mới:

### 3.1 Migration và tầng DB (`sqlc`)

- **`services/product/migrations/000003_add_product_slug.sql`**:
  - Thêm cột `slug TEXT NOT NULL DEFAULT ''` vào `products`, backfill `slug = LOWER(id)` cho bản ghi cũ, tạo partial unique index `idx_products_slug_active ON products (slug) WHERE deleted_at IS NULL AND slug <> ''`.
  - Cập nhật `services/product/queries/product.sql` (thêm `GetProductBySlug`, bổ sung `slug` vào `CreateProduct`, `GetProductByID`, `UpdateProduct`, `ListProductsFiltered`), sinh lại `services/product/internal/db/*`, cập nhật `store/` (`pg_store.go`, `memory_store.go`) và `server.go` (tự sinh slug chuẩn hóa bỏ dấu tiếng Việt `foldVietnameseRune`, gắn hậu tố `id[:8]` nếu trùng).
- **`services/user/migrations/0002_add_shop_slug.sql`**:
  - Thêm cột `slug TEXT NOT NULL DEFAULT ''` vào `shops`, backfill `slug = LOWER(id::text)`, tạo partial unique index `idx_shops_slug_nonempty ON shops (slug) WHERE slug <> ''`.
  - Cập nhật `services/user/queries/user.sql` (thêm `GetShopBySlug`, bổ sung `slug` vào `CreateShop`), sinh lại `services/user/internal/db/*`, bổ sung `helpers.go` (chuẩn hóa slug shop tiếng Việt) và `server.go`.

### 3.2 Protobuf và gRPC (`gen/go` đã sinh lại bằng `make proto`)

- **`proto/nexusmart/product/v1/product.proto`**:
  - Thêm `string slug` vào `Product` (`14`), `CreateProductRequest` (`7`, tùy chọn), `UpdateProductRequest` (`9`, tùy chọn), `GetProductRequest` (`2`, tra theo `id` hoặc `slug`).
- **`proto/nexusmart/user/v1/user.proto`**:
  - Thêm `string slug` vào `Shop` (`12`), `RegisterShopRequest` (`7`, tùy chọn), `GetShopRequest` (`3`, tra theo `shop_id`, `owner_user_id` hoặc `slug`).
  - Thêm RPC **`UserService.ListPublicShops(ListPublicShopsRequest) returns (ListPublicShopsResponse)`**: chỉ trả gian hàng `status = 'ACTIVE'` và xóa trắng toàn bộ trường riêng tư ở tầng service trước khi trả.

### 3.3 Endpoint REST Gateway mới / nâng cấp (`services/gateway/internal/router/` & `docs/contracts.md`)

| Endpoint | Auth / RBAC | RPC backend | Hành vi & kiểm soát dữ liệu |
| --- | --- | --- | --- |
| `GET /api/v1/shops` *(mới)* | Public | `User.ListPublicShops` | Chỉ trả shop `ACTIVE`, dùng `publicShopDTO` (`id`, `name`, `slug`, `description`, `status`, `created_at`, `updated_at`), không lộ PII/giấy phép. |
| `GET /api/v1/shops/slug/{slug}` *(mới)* | Public | `User.GetShop(slug)` | Khác `ACTIVE` → `404`; chỉ trả `publicShopDTO`. |
| `GET /api/v1/shops/{id}` *(siết lại)* | Public | `User.GetShop(id)` | Vá rò rỉ dữ liệu riêng tư: khác `ACTIVE` → `404`, chỉ trả `publicShopDTO`. |
| `GET /api/v1/shops/me` *(mới)* | Any authenticated | `User.GetShop(owner_user_id)` | Lấy `owner_user_id` từ JWT claims; trả đầy đủ `shopDTO` mọi trạng thái của chính caller. |
| `GET /api/v1/products/slug/{slug}` *(mới)* | Optional auth | `Product.GetProduct(slug)` | Ẩn danh/buyer chỉ xem `APPROVED`/`OUT_OF_STOCK`; chủ shop/admin xem mọi trạng thái. |
| `GET /api/v1/products` *(nâng cấp)* | Optional auth | `Product.ListProducts` (+ `ListCategories`/`GetShop`) | Thêm query `category_slug`, `shop_slug`; chuẩn hóa `sort_by` (`price-asc`/`price-desc`); DTO có `slug`. |
| `POST /api/v1/shops/register`, `POST/PUT /api/v1/seller/products*` *(nâng cấp)* | Authenticated / `seller` | `RegisterShop`, `CreateProduct`, `UpdateProduct` | Nhận thêm trường `slug` tùy chọn (bỏ trống tự sinh từ `name`). |
| `GET /api/v1/seller/shop` *(mới)* | `seller` + `RequireSellerShop()` | `User.GetShop(shop_id)` | Ép `shop_id` từ JWT claims; trả đầy đủ `shopDTO`. |
| `GET /api/v1/seller/products` *(mới)* | `seller` + `RequireSellerShop()` | `Product.ListProducts` | Ép `shop_id` từ JWT claims (bỏ qua `shop_id` client gửi); trả mọi trạng thái sản phẩm của shop. |
| `GET /api/v1/seller/products/{id}` *(mới)* | `seller` + `RequireSellerShop()` | `Product.GetProduct` | Kiểm `product.shop_id == claims.ShopID`, khác shop → `403`. |
| `GET /api/v1/seller/orders/{id}` *(mới)* | `seller` + `RequireSellerShop()` | `Order.GetOrder` | Kiểm `order.shop_id == claims.ShopID`, khác shop → `403`. |
| `GET /api/v1/seller/vouchers` *(mới)* | `seller` + `RequireSellerShop()` | `Promotion.ListAvailableVouchers` | Ép `scope=SHOP`, `shop_id` từ JWT claims (chỉ trả voucher đang hiệu lực). |
| `GET /api/v1/admin/products` *(mới)* | `admin`, `super_admin` | `Product.ListProducts` | Trả sản phẩm mọi trạng thái (lọc `status`, `shop_id`, `category_id`, `keyword`, `sort_by`). |
| `GET /api/v1/admin/products/{id}` *(mới)* | `admin`, `super_admin` | `Product.GetProduct` | Xem chi tiết sản phẩm mọi trạng thái. |
| `POST /api/v1/seller/flash-sales/{id}/items` *(siết lại)* | `seller` + `RequireSellerShop()` | `Product.GetSKUsBatch` + `Promotion.RegisterFlashSaleItem` | Kiểm chủ sở hữu SKU trước khi đăng ký flash sale: khác shop → `403`, không tồn tại → `404`. |

*(Ngoài ra `services/product` siết `resolveUpdatedProductStatus` chặn seller tự chuyển `PENDING`/`REJECTED` → `HIDDEN` → `APPROVED`. Đã bổ sung unit/integration test tại `services/gateway/internal/router/web_gaps_test.go`, `services/product/internal/server/slug_test.go`, `services/product/internal/server/server_test.go` và `services/user/internal/service/server_test.go`.)*

---

## 4. Kiến trúc BFF và bảo mật

- **Luồng phiên cookie (`web/src/lib/server/session.ts`, `web/src/app/api/auth/*`, `web/src/middleware.ts`, `web/src/lib/api.ts`)**:
  - Cookie `nm_access_token` (TTL theo `expires_in_seconds`, mặc định 15 phút) và `nm_refresh_token` (7 ngày) đặt `HttpOnly`, `SameSite=Lax`, `Path=/`, host-only (không đặt `Domain`), `Secure` bật khi `NODE_ENV=production` và tắt được bằng `COOKIE_SECURE=false` trên HTTP cục bộ.
  - 7 route auth BFF: `POST /api/auth/{register,verify-otp,resend-otp,login,refresh,logout}` và `GET /api/auth/session` (chỉ đọc; khi access thiếu/hết hạn mà còn refresh cookie sẽ trả `can_refresh: true` để client gọi `POST /api/auth/refresh` có kiểm CSRF).
  - **Làm mới token chống thu hồi nhầm token family**:
    - Trình duyệt (`api.ts`): `refreshSessionOnce()` gộp single-flight cho các lời gọi `api()` cùng gặp `401` rồi retry 1 lần; bắn `nexusmart:session-expired` nếu refresh thất bại.
    - Server (`session.ts` + `middleware.ts`): middleware chạy trên **Node runtime** (`runtime: "nodejs"`, Next 15.5) và dùng chung trạng thái refresh qua `globalThis` với các route `/api/*`; `refreshTokensWithGateway()` gộp `inFlightRefreshes` và giữ **cache ân hạn 5s** (`REFRESH_GRACE_TTL_MS = 5_000`, tối đa 200 entry, khóa `sha256(oldRefreshToken)`) để các request đến liền kề trước khi trình duyệt kịp ghi `Set-Cookie` nhận lại cặp token vừa xoay.
    - `middleware.ts`: bảo vệ 5 tiền tố `/tai-khoan`, `/don-hang`, `/thanh-toan`, `/seller`, `/admin` (không phiên → `307 /dang-nhap?next=...`, đường dẫn `next` qua hàm thuần `sanitizeNextPath` tại `web/src/lib/redirect.ts` chống open redirect kể cả sau chuẩn hóa dot-segment); chủ động refresh khi access token còn <= 60s (bỏ qua prefetch request); ghi cập nhật header `cookie` cho Server Component trong cùng lượt request; lỗi `5xx` từ gateway không đá nhầm phiên hợp lệ ra login (trả `503` nếu access đã hết hạn).
    - `POST /api/auth/logout`: xóa ngay cache ân hạn dùng chung, hủy cả refresh đang bay, gọi `POST /api/v1/auth/logout` thu hồi token hiện tại lẫn cặp vừa xoay trong cửa sổ ân hạn (nếu access hết hạn sẽ đổi 1 lần để thu hồi đúng family) và xóa cả hai cookie.
- **Bảo vệ CSRF (`web/src/lib/server/csrf.ts`)**:
  - `verifyCsrfOrigin()` chạy trên mọi phương thức không an toàn (`POST`, `PUT`, `PATCH`, `DELETE`) của `/api/gw/*` và `/api/auth/*`: bắt buộc có header `Origin` khớp tuyệt đối với origin mong đợi (`APP_ORIGIN` nếu khai báo — compose mặc định `http://localhost:${WEB_PORT:-3000}` — hoặc `Host` + proto của `req.url`, không tin `X-Forwarded-Host`/`X-Forwarded-Proto` do client gửi); thiếu hoặc khác origin trả `403 CSRF_FORBIDDEN`.
- **Proxy `/api/gw/[...path]` (`web/src/app/api/gw/[...path]/route.ts`)**:
  - Chặn traversal/ký tự điều khiển và chặn cứng root segment `webhooks` và `auth` (`403 FORBIDDEN_PATH`).
  - Gắn `Authorization: Bearer <nm_access_token>` từ cookie, chuyển `Idempotency-Key` và `Last-Event-ID`, hỗ trợ stream SSE không đệm cho `GET /payments/{id}/events` (`X-Accel-Buffering: no`), timeout 15s (non-SSE).
  - Mọi lỗi upstream đi qua `sanitizeGatewayError()` (`web/src/lib/server/gateway.ts`) chuyển thành thông báo tiếng Việt an toàn; lỗi `5xx` trả `503 UNAVAILABLE`, không lộ lỗi nội bộ gateway.
- **Chuyển tiếp IP client (`X-Forwarded-For`)**:
  - `extractClientIp()` trích hop đầu của `x-forwarded-for` (fallback `x-real-ip`, lọc ký tự điều khiển) và gắn `X-Forwarded-For` trên `gatewayFetch`, `refreshTokensWithGateway` và `/api/gw/*` để rate limit ở gateway hoạt động theo từng client.
- **RBAC ở server & không tin `shop_id`/`user_id` client**:
  - `getServerSession()` (`web/src/lib/server/auth.ts`) luôn gọi `GET /api/v1/users/me` để lấy `role` và `shop_id` đã xác minh từ backend (`readJwtExp()` chỉ đọc `exp` cho proactive refresh, tuyệt đối không dùng cho phân quyền).
  - `requireUser()` bảo vệ `/thanh-toan`, `/thanh-toan/cho-thanh-toan/[paymentId]`, `/don-hang`, `/don-hang/[id]`, `/tai-khoan`.
  - `requireRole(["seller"])` trong `(seller)/layout.tsx` và `requireRole(["admin", "super_admin"])` trong `(admin)/layout.tsx` trả `404` (`notFound()`) khi tài khoản sai vai trò.
  - Ở gateway, mọi endpoint `/seller/*` ép `shop_id` từ JWT claims (`RequireSellerShop()`) và kiểm quyền sở hữu tài nguyên (`403` nếu khác shop).
- **`Idempotency-Key` cho checkout**:
  - `web/src/app/(shop)/thanh-toan/page.tsx` sinh `nm-checkout-${randomUUID()}` phía server bằng `node:crypto` cho mỗi lượt render trang thanh toán và truyền vào `CheckoutClient` để gửi qua header `Idempotency-Key` lên `POST /api/gw/checkout` (giữ nguyên key khi retry lỗi mạng).

---

## 5. Cách chạy

1. **Khởi động toàn bộ stack (backend + `web` trong Docker Compose)**:
   ```bash
   make up
   ```
   - `web` build đa giai đoạn từ `web/Dockerfile` (`output: "standalone"`, chạy bằng user không phải root `nextjs:10001`), nối nội bộ tới `GATEWAY_URL=http://gateway:8080`, mặc định `APP_ORIGIN=http://localhost:${WEB_PORT:-3000}`, mở cổng host theo biến `WEB_PORT` (mặc định `3000` trong `.env.example`; worktree này đặt `33000` trong `.env` cục bộ, không dùng cổng `3111`).
2. **Nạp dữ liệu mẫu idempotent (`make seed`)**:
   ```bash
   make seed
   ```
   - Script `tools/seed/seed.mjs` lấy nguồn trực tiếp từ `web/src/data/*` qua Gateway REST & Mailpit, nạp 4 gian hàng, 6 danh mục, 18 sản phẩm (đủ SKU/giá/tồn/ảnh), 11 voucher, 3 khung Flash Sale (16 item) và tài khoản demo (chi tiết ở `tools/seed/README.md`):

   | Vai trò | Email | Mật khẩu dev | Ghi chú |
   | --- | --- | --- | --- |
   | `admin` | `admin@nexusmart.local` | `AdminDevPassword123!` | Admin bootstrap (theo `.env`) |
   | `buyer` | `minhquan.nguyen@nexusmart.vn` | `BuyerDevPassword123!` | Nguyễn Minh Quân · địa chỉ mặc định TP.HCM · 42.000 xu |
   | `seller` | `seller.bachdang@nexusmart.local` | `SellerDevPassword123!` | Xưởng Dệt Bạch Đằng |
   | `seller` | `seller.saigon@nexusmart.local` | `SellerDevPassword123!` | Kho Basic Sài Gòn |
   | `seller` | `seller.danang@nexusmart.local` | `SellerDevPassword123!` | Sợi Tự Nhiên Đà Nẵng |
   | `seller` | `seller.hanoi@nexusmart.local` | `SellerDevPassword123!` | Dệt Kim Hà Nội |

3. **Chạy `web/` ở chế độ dev trên host**:
   ```bash
   cd web
   GATEWAY_URL=http://localhost:38080 COOKIE_SECURE=false npm run dev -- -p 3201
   ```
   *(Thay `38080` bằng `GATEWAY_PORT` trong `.env`; có thể đặt thêm `APP_ORIGIN` khi chạy sau reverse proxy.)*
4. **Chạy kiểm thử đầu-cuối qua web BFF**:
   ```bash
   make e2e-web
   ```
   *(Tự nạp `.env`, chạy `node --test 'tests/e2e-web/*.test.mjs'` với `WEB_URL`, `MAILPIT_URL`, `GATEWAY_URL`.)*
5. **Build song song bằng `NEXT_DIST_DIR`**:
   - `web/next.config.ts` cấu hình `distDir: process.env.NEXT_DIST_DIR || ".next"`. Khi cần build nhiều tiến trình song song không ghi đè `.next`, chạy ví dụ `NEXT_DIST_DIR=.next-dev npm run build`.
   - **Lưu ý**: Next.js sẽ tự thêm đường dẫn `.next-dev/types/**/*.ts` vào `web/tsconfig.json`; bắt buộc hoàn nguyên `web/tsconfig.json` trước khi commit.

---

## 6. Kết quả gate

Số liệu lần chạy cuối của manager sau toàn bộ bản sửa security review:

- **Kiểm tra `web/` (`cd web`)**:
  - `npm run lint`: **exit 0**.
  - `npm run format:check`: sạch (`All matched files use Prettier code style!`).
  - `TZ=UTC npm run check:data`: **`ALL 15 DATA INTEGRITY CHECKS PASSED`** (đã thêm kiểm định dạng giờ Việt Nam và kiểm `sanitizeNextPath` chống open redirect).
  - `npx tsc --noEmit`: **OK**.
  - `npm run build`: **`Compiled successfully`**.
- **UI lint (`taste-skill lint.mjs web/src`)**: **0 FAIL, 3 WARN**.
- **Backend lint & unit test**:
  - `make lint`: **exit 0**.
  - `make test`: **exit 0**.
- **Backend E2E (`make e2e`)**: **9/9 PASS**.
- **Web BFF E2E (`make e2e-web`)**: **13/13 pass, 0 skip** — bao phủ toàn bộ 12 ca trước đó và bổ sung ca `4j` (đăng xuất xóa cache làm mới ở middleware, refresh song song middleware + API không thu hồi họ token, `GET /api/auth/session` chỉ đọc) cùng mở rộng ca `4c` (giả `X-Forwarded-Host` / `X-Forwarded-Proto` → `403`).
- **Kiểm tra trực quan `inspect.mjs` (1440 / 768 / 390) trên dữ liệu thật**:
  - Trang công khai: `/` (**0 FAIL, 0 WARN**), `/danh-muc` (**0 FAIL, 1 WARN**), `/san-pham/ao-khoac-gio-nhe-gap-gon-xam-tro` (**0 FAIL, 0 WARN**), `/flash-sale` (**0 FAIL, 1 WARN**), `/gian-hang/xuong-det-bach-dang` (**0 FAIL, 1 WARN**).
  - Trang cần đăng nhập — chạy `inspect.mjs --app` với cookie phiên của buyer demo (bản sao `inspect.mjs` chèn cookie qua CDP `Network.setCookie`) trên dữ liệu thật (2 đơn COD của 2 gian hàng, giỏ còn 2 nhóm):
    - `/gio-hang`: **0 FAIL, 1 WARN** (`h1` 66px).
    - `/thanh-toan`: **0 FAIL, 2 WARN** (`ragged-row`, `h1` 66px).
    - `/don-hang`: **0 FAIL, 2 WARN** (`below-fold`, `h1`).
    - `/don-hang/[id]`: **0 FAIL, 3 WARN** (`ragged-row`, `below-fold`, `h1`).
    - `/tai-khoan`: **0 FAIL, 2 WARN** (`below-fold`, `h1`).
- **Playwright end-to-end UI & hydration**:
  - Đăng nhập UI `buyer` / `seller` / `admin` điều hướng đúng tới `/tai-khoan`, `/seller`, `/admin`; `next=/..//evil.com` sau đăng nhập về `/`; không `pageerror`; không còn React hydration `#418`, thời gian hiển thị theo giờ Việt Nam (`TZ=UTC` ở container `web`, trình duyệt `Asia/Saigon`).
- **Kiểm tra `localStorage` (`rg -n "localStorage" web/src`)**:
  - Chỉ còn đúng **2 dòng** trong `web/src/mock/MockStoreContext.tsx` (dòng 241 `getItem` và dòng 288 `setItem`, đã có chú thích lý do: tiện ích theo người xem); không còn giỏ hàng, đơn hàng hay token trong `localStorage`.

---

## 7. Security review

Đã thực hiện security review độc lập chỉ đọc (agy) trên toàn bộ diff giai đoạn 0b và tái hiện từng phát hiện bằng request thật:

| Mức độ | Phát hiện | Xử lý |
| --- | --- | --- |
| **CAO** | Trạng thái làm mới token tách đôi giữa `middleware` (Edge) và route `/api` (Node): đăng xuất không xóa cache ân hạn bên Edge (phiên dùng tiếp sau đăng xuất), refresh đồng thời hai bên gây thu hồi họ token. | **ĐÃ SỬA**: chuyển middleware sang `runtime: "nodejs"` (Next 15.5), dùng chung trạng thái refresh qua `globalThis`, `logout` hủy cả refresh đang bay và thu hồi cặp vừa xoay (`web/src/middleware.ts`, `web/src/lib/server/session.ts`, `web/src/app/api/auth/logout/route.ts`). |
| **CAO** | Seller tự duyệt sản phẩm qua chuỗi `PENDING`/`REJECTED` → `HIDDEN` → `APPROVED` (lỗi có sẵn ở `services/product`, lộ qua nút ẩn/hiện ở kênh người bán). | **ĐÃ SỬA**: seller chỉ được ẩn sản phẩm đang `APPROVED`/`OUT_OF_STOCK` (`services/product/internal/server/server.go` `resolveUpdatedProductStatus`, test `TestSellerProductStatusTransitionMatrix`). |
| **TRUNG BÌNH** | Open redirect `next=/..//evil.com` (chuẩn hóa dot-segment thành `//evil.com`). | **ĐÃ SỬA**: gộp về một hàm thuần `web/src/lib/redirect.ts` (`sanitizeNextPath`) kiểm lại sau chuẩn hóa, có phép kiểm trong `npm run check:data`. |
| **TRUNG BÌNH** | Kiểm tra CSRF tin `X-Forwarded-Host` / `X-Forwarded-Proto` do client gửi khi `APP_ORIGIN` rỗng. | **ĐÃ SỬA**: chỉ dùng `APP_ORIGIN` hoặc `Host` + proto của `req.url` (`web/src/lib/server/csrf.ts`); `docker-compose.yml` mặc định `APP_ORIGIN=http://localhost:${WEB_PORT:-3000}`. |
| **TRUNG BÌNH** | Seller đăng ký flash sale cho SKU thuộc gian hàng khác. | **ĐÃ SỬA** ở gateway: kiểm chủ sở hữu SKU qua `Product.GetSKUsBatch`, khác shop → `403`, không có → `404` (`services/gateway/internal/router/handlers_promotion.go`, test `TestSellerRegisterFlashSaleItemSKUOwnership`). |
| **THẤP** | Form hoàn tiền admin giữ `Idempotency-Key` cũ sau khi thành công và thiếu chặn gửi trùng; `GET /api/auth/session` xoay token trên `GET`. | **ĐÃ SỬA**: xoay key mới và chặn gửi trùng ở form hoàn tiền admin; `GET /api/auth/session` chỉ đọc, client gọi `POST /api/auth/refresh` khi `can_refresh: true`. |

*(Các phát hiện còn lại sau review — thuộc phạm vi hạ tầng triển khai, vùng cấm `services/order`, hoặc thiết kế có sẵn trước 0b — được liệt kê trong Mục 8.)*

---

## 8. Phần chưa xong và rủi ro còn lại

- **Vùng cấm `services/order` chưa sửa trong 0b** (do nhánh khác đang thay đổi `services/order/internal/{config,service,store,db}/**`, `queries/**`, `migrations/**`):
  - Tra cứu đơn vãng lai `/tra-cuu-don-hang` (`web/src/features/commerce/OrderLookupView.tsx`, `web/src/mock/guestOrderLookup.ts`) vẫn dùng dữ liệu mẫu.
  - Chưa có luồng duyệt/từ chối trả hàng (`RETURN_REQUESTED -> RETURNED/COMPLETED`) ở backend và `/admin/khieu-nai`.
  - Cột `orders.payment_id` không tự cập nhật sau `POST /api/v1/payments/{id}/retry` (UI theo dõi `payment_id` mới trả về từ `/retry`, nhưng tải lại trang đơn cũ vẫn đọc `payment_id` ban đầu).
  - Gửi lại `Idempotency-Key` với payload khác vẫn trả kết quả cũ ở `services/order` (phát hiện từ security review, chưa sửa vì thuộc vùng cấm).
- **Rủi ro bảo mật / hạ tầng còn lại (từ security review)**:
  - **Giả mạo `X-Forwarded-For`**: client có thể tự đặt `X-Forwarded-For` để né giới hạn tốc độ theo IP vì gateway lấy hop đầu (`services/gateway/internal/middleware/ratelimit.go:174-176`) → khi triển khai bắt buộc có reverse proxy tin cậy ghi đè `X-Forwarded-For` phía trước `web`/`gateway`.
  - **HTTP trực tiếp của `services/payment`**: chưa xác thực ở `/internal/*` và SSE, cổng đang publish ra host trong `docker-compose.yml` (có sẵn từ trước 0b; không nên công bố ra ngoài khi triển khai).
  - **Gateway trả thông điệp `codes.Internal`**: gọi trực tiếp gateway `:38080` còn trả nguyên thông điệp lỗi nội bộ (luồng qua BFF `web/` đã che bằng `sanitizeGatewayError`).
  - **Access token cũ sau đăng xuất**: theo thiết kế JWT không trạng thái, chỉ JTI của access token hiện tại trong request logout bị thu hồi; access token khác đã phát hành trước đó còn hiệu lực tới hết 15 phút.
- **Thiếu RPC cho một số thao tác Seller/Admin**:
  - Admin chưa có endpoint liệt kê flash sale item `PENDING` (`web/src/features/admin/AdminPromoCoinsPanel.tsx` nhập tay `item_id`).
  - Chưa có RPC cập nhật hồ sơ shop (`/seller/ho-so`), bật/tắt/xóa voucher shop (`/seller/khuyen-mai`), liệt kê lịch sử `reconciliation_runs` (`/admin/doi-soat`), hay rút tiền ngân hàng seller (`/seller/tai-chinh`).
  - `GET /api/v1/seller/vouchers` gọi `Promotion.ListAvailableVouchers` nên chỉ trả voucher đang hiệu lực.
- **Giới hạn `ponytail:` trên trần 100 sản phẩm**:
  - Tìm kiếm bỏ dấu/đồng nghĩa, lọc 13 nhóm thuộc tính may mặc và sắp xếp bán chạy (`salesRank`) chạy phía client trên tối đa 100 sản phẩm thật (`web/src/lib/catalog.ts:341`, `web/src/features/catalog/CatalogExplorer.tsx:406,476,498`).
  - Tra cứu ảnh và slug sản phẩm cho trang đơn hàng (`web/src/features/account/serverData.ts:35-65`) và giỏ hàng (`web/src/app/(shop)/gio-hang/page.tsx:111`) đọc tối đa 100 sản phẩm công khai; sản phẩm ngoài giới hạn hoặc đã ẩn sẽ hiển thị fallback.
- **Bộ nhớ một tiến trình cho cache refresh (`web/src/lib/server/session.ts`)**:
  - Sau khi gộp middleware về Node runtime qua `globalThis`, không còn tách Edge/Node; trần còn lại là phạm vi một tiến trình Next.js — khi chạy nhiều replica cần chuyển cơ chế gộp in-flight và cache ân hạn 5s ra Redis.
- **Tính năng giữ mock theo mục 6 brief**:
  - Hội viên 4 hạng, thẻ quà tặng, xác thực `.edu`, tạp chí, bộ sưu tập, chiến dịch/CSR, chat 1-1, trợ lý AI, đánh giá/H&Đ/báo có hàng, khiếu nại SLA, CMS cấu hình sàn nằm tại `web/src/mock/`, `web/src/data/`, `web/src/features/seller/sellerStore.ts` và `web/src/features/admin/auditStore.ts` (cùng dải gợi ý `PRODUCTS` ở `/don-hang`, `/don-hang/[id]` và cột ảnh minh họa tĩnh ở `/dang-nhap`, `/dang-ky`).
- **Cảnh báo `taste-skill` lint**: còn **3 WARN** (0 FAIL).
- **Phạm vi review code**: chưa chạy trọn bộ skill `code-review` / `simplify` / `ponytail-review` trên toàn bộ diff (~30 nghìn dòng thay đổi ở `web/`).
- **Lưu ý khi gộp với nhánh `fix/stock-checkout-logs`**:
  - `docs/contracts.md` cả hai nhánh cùng sửa → cần gộp tay.
  - Migration không trùng số: nhánh kia thêm `services/order/migrations/0002_*`, nhánh này thêm `services/user/migrations/0002_add_shop_slug.sql` và `services/product/migrations/000003_add_product_slug.sql`.
- **Chưa commit**: Toàn bộ thay đổi hiện nằm trên nhánh `feat/web-gateway` trong worktree `/home/tai/Document/nexusmart-web`.
