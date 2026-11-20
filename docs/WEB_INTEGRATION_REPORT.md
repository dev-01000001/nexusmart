# Giai đoạn 0b – Tích hợp `web/` với API Gateway: Báo cáo nghiệm thu

- **Ngày nghiệm thu:** `2026-11-20`
- **Nhánh git:** `phase-0b` (commit nền `6f143e0`, không commit/push trong quá trình xác minh)
- **Đặc tả:** `.agents/briefs/sites-0b-web-gateway.md`
- **Phạm vi:** Nối ứng dụng Next.js 15 (`web/`, 46 route trang + 50 BFF route handler) với cụm 9 microservice phía sau API Gateway qua lớp BFF cùng origin (cookie `HttpOnly`, bảo vệ CSRF, RBAC phía server, không thêm CORS ở gateway, không thêm dependency runtime vào `web/`).

---

## 1. Tổng quan kiến trúc BFF & Bảo mật

1. **Lớp BFF cùng origin (`web/src/lib/server/**` & `web/src/app/api/**` — 50 route handler):**
   - Trình duyệt chỉ gọi Server Component và route handler cùng origin (`/api/auth/**`, `/api/cart/**`, `/api/vouchers/**`, `/api/checkout/**`, `/api/payments/**`, `/api/orders/**`, `/api/seller/**`, `/api/admin/**`).
   - Next.js server gọi gateway qua biến `GATEWAY_URL` (`http://gateway:8080` trong compose, `http://localhost:28080` khi chạy local) kèm timeout (`GATEWAY_TIMEOUT_MS=8000`) và retry có backoff cho `GET` khi upstream lỗi tạm thời (`502/503/504`). Gateway giữ nguyên **0 header CORS**.
   - **Phiên & Cookie (`config.ts`, `tokens.ts`, `session.ts`, `middleware.ts`):**
     - `access_token` và `refresh_token` lưu trong cookie `HttpOnly`, `SameSite=Lax`, `Path=/`, host-only (không đặt `Domain`), `Secure` (bật ở HTTPS, prefix `__Host-`; dev HTTP dùng `COOKIE_SECURE=false`). Không bao giờ trả token trong JSON response và không lưu trong `localStorage`.
     - Xoay refresh token bằng cơ chế single-flight (cửa sổ deduplication 30 giây khớp family revocation của `services/user`). `middleware.ts` chủ động xoay access token sắp hết hạn và chuyển cookie mới vào request của Server Component; `POST /api/auth/logout` thu hồi token family + JTI ở backend rồi xóa sạch cookie.
   - **Bảo vệ CSRF (`http.ts`):** Mọi route ghi đều đi qua `apiRoute(...)` → `assertSameOrigin(req)` (từ chối `Sec-Fetch-Site: cross-site|same-site`, kiểm tra `Origin` hoặc `Referer` khi thiếu `Origin` so với `APP_ORIGIN`/host+proto; trả `403 BAD_ORIGIN`).
   - **RBAC & Không tin dữ liệu client:**
     - `middleware.ts` chặn khách chưa đăng nhập tại `/tai-khoan`, `/don-hang`, `/seller`, `/admin` (`307` → `/dang-nhap?next=...`).
     - Mọi trang bảo vệ gọi `requireSession()` / `requireRole("seller")` / `requireRole("admin", "super_admin")` dựa trên `/api/v1/users/me` từ backend; mọi API ghi kiểm `assertSession()` / `assertSellerSession()` / `assertAdminSession()`. `shop_id` và `user_id` luôn lấy từ JWT/session ở backend & BFF, không lấy từ body/query của client.
     - Mọi lỗi từ gateway được chuẩn hóa qua `SAFE_MESSAGES` (`errors.ts`), không rò rỉ `trace_id`, lỗi gRPC hay thông điệp nội bộ ra trình duyệt.
2. **Tách biệt nguồn sự thật (`StoreContext` vs `MockStoreContext`):**
   - `web/src/context/StoreContext.tsx` chỉ còn một nguồn sự thật cho giỏ hàng từ backend `/api/cart` (đồng bộ tự động theo `SessionProvider`), lựa chọn voucher/xu trong bộ nhớ cho phiên checkout, và 4 tùy chọn tiện ích theo người xem (`localStorage`, chi tiết ở Mục 5). Đã xóa hoàn toàn giỏ/đơn/auth mẫu khỏi `StoreContext`.
   - Toàn bộ tính năng ngoài phạm vi backend (hội viên, thẻ quà tặng, `.edu`, đánh giá mẫu, hỏi đáp mẫu, phiếu hỗ trợ, chat mẫu, cấu hình/nội dung/khiếu nại mẫu) nằm tách biệt trong bộ nhớ ở `web/src/mock/MockStoreContext.tsx` (không ghi `localStorage`), cùng `sellerStore.ts` và `auditStore.ts` chỉ còn state in-memory cho các tab chưa có backend.

---

## 2. Bảng đối chiếu Route → Nguồn dữ liệu (46/46 trang trong `web/src/app`)

Quy ước phân loại:
- **Thật:** Toàn bộ dữ liệu nghiệp vụ chính và thao tác đọc/ghi đi qua BFF → API Gateway → Microservices.
- **Kết hợp:** Dữ liệu thương mại chính (sản phẩm/tồn SKU/giá/voucher/đơn/duyệt…) đến từ backend thật; phần biên tập/nội dung bổ trợ hoặc tính năng chưa có service (đánh giá, Q&A, hạng hội viên, hồ sơ xưởng mở rộng…) giữ dữ liệu mẫu tách biệt trong `MockStoreContext`/`src/data/*`.
- **Mẫu:** Trang thuộc tính năng chưa có microservice backend trong Phase 0b (tôn trọng "Ngoài phạm vi" trong đặc tả), giữ dữ liệu mẫu in-memory và tách khỏi dữ liệu thật.

### 2.1 Storefront `(shop)` — 26 route

| # | Route | Trạng thái | Nguồn dữ liệu & Lý do |
|---|---|---|---|
| 1 | `/` | **Kết hợp** | **Thật:** danh mục, 4 gian hàng, 18 sản phẩm/giá/SKU, khung Flash sale & đếm ngược đồng hồ server, kho voucher + lưu voucher (`/api/vouchers/[id]/save`) qua `loadStorefront()`. **Mẫu:** khối giới thiệu biên tập, bài viết tạp chí và hạng hội viên (chưa có CMS/Loyalty service). |
| 2 | `/danh-muc` | **Thật** | Đọc danh mục, sản phẩm, gian hàng, giá và tồn SKU thật qua `loadCatalog()` (lọc, sắp xếp, đếm facet, thêm giỏ thật bằng `skuId` UUID). Metadata trình bày (mô tả chất liệu/form/mẹo bảo quản) được ghép từ `catalog-map.ts`. |
| 3 | `/tim-kiem` | **Kết hợp** | **Thật:** danh sách sản phẩm, giá, tồn SKU và gian hàng từ `loadCatalog()`. **Mẫu:** từ khóa xu hướng & chuẩn hóa bỏ dấu/đồng nghĩa chạy phía client (chưa có Search service). |
| 4 | `/san-pham/[slug]` | **Kết hợp** | **Thật:** `loadProductDetail(slug)` luôn gọi trực tiếp `GET /api/v1/products/{id}` (không cache) để lấy tồn kho `available_stock` thật theo từng SKU, giá Flash sale live, voucher shop & sàn thật, thêm giỏ thật (`/api/cart/items`). **Mẫu:** đánh giá (`SAMPLE_REVIEWS`), Q&A (`SAMPLE_QAS`), đăng ký báo có hàng, ước tính ship nhanh trước khi vào checkout (chưa có Review/Q&A/Notification-alert service). |
| 5 | `/gian-hang/[slug]` | **Kết hợp** | **Thật:** thông tin gian hàng công khai, danh sách sản phẩm/SKU và voucher shop/sàn qua `loadShopDetail(slug)`. **Mẫu:** hồ sơ xưởng mở rộng, bảng size tùy chỉnh và chat xưởng (chưa có Shop-profile CMS/Chat service). |
| 6 | `/flash-sale` | **Thật** | Khung giờ Flash sale (`12:00` đang diễn ra, `18:00` & `21:00` sắp mở), đồng hồ server, suất còn lại thực tế và thêm giỏ kèm `flashSaleItemId` từ `loadFlashSales()` (`GET /api/v1/flash-sales`, không cache). |
| 7 | `/voucher` | **Thật** | Danh sách voucher thật theo phạm vi (Sàn / Gian hàng / Freeship), thanh tiến độ lượt còn lại, lưu voucher (`POST /api/vouchers/[id]/save`), áp vào giỏ từ `loadVouchers()`. |
| 8 | `/gio-hang` | **Thật** | Giỏ hàng đa gian hàng thật qua `GET/POST/PATCH/DELETE /api/cart/**`, tồn kho & giá thật, danh sách/lưu/kiểm tra/xem trước voucher & Xu thật qua `/api/vouchers/{route,save,coins,validate,preview}`. Khách chưa đăng nhập thấy trạng thái mời đăng nhập rõ ràng. |
| 9 | `/thanh-toan` | **Thật** | Server sinh `Idempotency-Key` (`randomUUID()`), nạp địa chỉ thật (`/api/v1/users/me/addresses`), tỉnh/phường 2 cấp (`/api/checkout/locations`), báo giá vận chuyển từng gian hàng (`/api/checkout/shipping-quote`), áp voucher/Xu thật, đặt đơn COD & VietQR mock (`POST /api/checkout`). |
| 10 | `/thanh-toan/cho/[id]` | **Thật** | Bảo vệ phía server (`requireSession()`), kiểm tra quyền sở hữu giao dịch (`payment.user_id === session.user.id`), hiển thị QR & thông tin chuyển khoản VietQR mock, polling `GET /api/payments/[id]` tới `SUCCEEDED`/`EXPIRED`/`FAILED`, hỗ trợ thử lại VietQR (`POST /api/payments/[id]/retry`). |
| 11 | `/dang-nhap` | **Thật** | Đăng nhập thật qua `POST /api/auth/login` (đặt cookie `HttpOnly`), hỗ trợ `?next=` an toàn (`safeNextPath` chặn open redirect), đồng bộ `SessionProvider` + `refreshCart()`, điều hướng xác thực OTP nếu tài khoản chưa kích hoạt. |
| 12 | `/dang-ky` | **Thật** | Quy trình 2 bước thật: `POST /api/auth/register` → nhận OTP 6 số qua Mailpit → `POST /api/auth/verify-otp` (có `POST /api/auth/resend-otp` cooldown 30s) → tự đăng nhập `POST /api/auth/login`. |
| 13 | `/tai-khoan` | **Kết hợp** | Bảo vệ server (`requireSession("/tai-khoan")`). **Thật:** hồ sơ cá nhân & sửa hồ sơ (`PATCH /api/auth/me`), số dư Xu (`/api/v1/coins/balance`), sổ địa chỉ 2 cấp CRUD (`/api/auth/addresses/**`, `/api/checkout/locations`), đơn hàng gần đây (`/api/v1/orders`), kho voucher thật. **Mẫu:** tab Hội viên/Thẻ quà tặng/Giới thiệu bạn bè (`MockStoreContext`) và hồ sơ số đo/yêu thích (`localStorage` tiện ích). |
| 14 | `/don-hang` | **Thật** | Bảo vệ server (`requireSession("/don-hang")`), danh sách đơn thật của người mua (`fetchBuyerOrders`), lọc trạng thái/gian hàng, nhóm đơn đa gian hàng, nút tiếp tục thanh toán VietQR (`/thanh-toan/cho/[paymentId]`), mua lại bằng `skuId` thật. |
| 15 | `/don-hang/[id]` | **Thật** | Bảo vệ server (`requireSession`) + kiểm soát sở hữu chặt (`403`/`404` → `notFound()`), timeline đơn thật, trạng thái COD/VietQR & thử lại VietQR, thao tác thật: hủy đơn (`/api/orders/[id]/cancel`), xác nhận đã nhận (`/api/orders/[id]/confirm-received`), yêu cầu đổi trả (`/api/orders/[id]/return`). |
| 16 | `/tra-cuu-don-hang` | **Kết hợp** | **Thật (khi đã đăng nhập):** tra cứu đơn thật của người mua qua `GET /api/orders`, hủy đơn & thử lại VietQR thật. **Hạn chế/Mẫu:** chưa có API backend tra cứu đơn vãng lai (guest lookup không cần phiên) nên khách chưa đăng nhập được hướng dẫn đăng nhập; dải sản phẩm gợi ý đọc từ `PRODUCTS` tĩnh. |
| 17 | `/tra-cuu-don` | **Thật (Redirect)** | Route alias server-side chuyển hướng `redirect("/tra-cuu-don-hang")`. |
| 18 | `/bo-suu-tap` | **Kết hợp** | **Thật:** danh sách sản phẩm/giá/tồn SKU/nút thêm giỏ từ `loadCatalog()`. **Mẫu:** định nghĩa 5 bộ sưu tập biên tập (`COLLECTIONS`, chưa có CMS service). |
| 19 | `/bo-suu-tap/[slug]` | **Kết hợp** | **Thật:** sản phẩm/giá/tồn SKU/gian hàng từ `loadCatalog()`. **Mẫu:** metadata & mô tả lợi ích sợi của từng BST (`COLLECTIONS`). |
| 20 | `/chien-dich/[slug]` | **Kết hợp** | **Thật:** sản phẩm & gian hàng liên kết mua nhanh từ `loadCatalog()`. **Mẫu:** nội dung chiến dịch công nghệ vải & bảng sao kê CSR `Sợi Xanh Nexus` (`isSampleData: true`, chưa có CMS/CSR ledger). |
| 21 | `/ve-nexusmart` | **Kết hợp** | **Thật:** 4 gian hàng đang hoạt động và sản phẩm tiêu biểu từ `loadCatalog()`. **Mẫu:** câu chuyện thương hiệu, mốc phát triển, hồ sơ nhà máy & nhật ký cập nhật (chưa có CMS). |
| 22 | `/tap-chi` | **Mẫu** | Bài viết biên tập `Sổ tay vải & Phối đồ` (`JOURNAL_ARTICLES`) và thư viện bảo quản (`CARE_GUIDE_LIBRARY`); chưa có CMS/Blog service. |
| 23 | `/tap-chi/[slug]` | **Mẫu** | Nội dung bài viết SSG (`JOURNAL_ARTICLES`, 4 bài) và `EmbeddedProductBuyCard` đọc từ `src/data` (khi bấm chọn mua với sản phẩm chưa gắn `skuId` sẽ điều hướng sang trang PDP thật `/san-pham/[slug]`). Chưa có CMS service. |
| 24 | `/hoi-vien` | **Kết hợp** | **Thật (nhỏ):** khi đã đăng nhập, đọc số dư Xu thật từ `GET /api/vouchers/coins` và cho phép lưu voucher qua `saveVoucher()`. **Mẫu:** 4 hạng thành viên, chi tiêu 4 quý, lịch sử Xu, quà sinh nhật, tổng kết năm 2026 và giới thiệu bạn bè (`MockStoreContext`; chưa có Loyalty service). |
| 25 | `/the-qua-tang` | **Mẫu** | Phát hành/áp thẻ quà tặng & xác thực email `.edu` chạy in-memory trong `MockStoreContext`; chưa có Gift Card / `.edu` OTP service. |
| 26 | `/tro-giup` | **Mẫu** | FAQ tách Sàn/Gian hàng, form gọi lại 15 phút, phiếu khiếu nại/bù lẻ tất (`MockStoreContext`), mở panel Chat/Trợ lý AI mẫu; chưa có Helpdesk/VoIP/AI service. |

### 2.2 Seller Center `(seller)` — 11 route (tất cả bảo vệ server `requireRole("seller")`)

| # | Route | Trạng thái | Nguồn dữ liệu & Lý do |
|---|---|---|---|
| 27 | `/seller` | **Thật** | Tổng quan gian hàng thật: `/api/v1/seller/shop`, `/api/v1/seller/balance`, `/api/v1/seller/orders`, `/api/v1/seller/products` (cảnh báo tồn thấp, biểu đồ 7 ngày, xác nhận/giao nhanh đơn). |
| 28 | `/seller/san-pham` | **Thật** | Danh sách sản phẩm/SKU thật của gian hàng (`/api/seller/products`), danh mục thật, thao tác nhập kho `+20` theo optimistic locking (`PATCH /api/seller/skus/[skuId]/stock`). |
| 29 | `/seller/san-pham/moi` | **Thật** | Tạo sản phẩm & danh sách biến thể SKU mới thật (`POST /api/seller/products`), `shop_id` lấy từ JWT. |
| 30 | `/seller/san-pham/[id]` | **Thật** | Kiểm tra sở hữu `product.shop_id === session.user.shopId` ở server, cập nhật sản phẩm (`PUT/DELETE /api/seller/products/[id]`) và chỉnh tồn kho từng SKU (`PATCH /api/seller/skus/[skuId]/stock`). |
| 31 | `/seller/don-hang` | **Thật** | Danh sách đơn thật của gian hàng (`GET /api/seller/orders`), xác nhận đơn đơn lẻ/hàng loạt (`POST /api/seller/orders/[id]/confirm`) và giao vận (`POST /api/seller/orders/[id]/ship`). |
| 32 | `/seller/don-hang/[id]` | **Thật** | Kiểm tra sở hữu đơn theo `shop_id` (`GET /api/v1/seller/orders/{id}`), hiển thị chi tiết đơn và xác nhận/giao hàng thật. |
| 33 | `/seller/tai-chinh` | **Thật** | Số dư người bán thật (`GET /api/v1/seller/balance`) cùng doanh thu & hoa hồng tính từ đơn hàng thật của gian hàng (rút tiền ngân hàng ghi rõ chưa nối cổng chi hộ). |
| 34 | `/seller/khuyen-mai` | **Kết hợp** | **Thật:** danh sách & tạo voucher gian hàng (`GET/POST /api/seller/vouchers`), đăng ký SKU vào khung Flash sale (`POST /api/seller/flash-sales/[id]/items`). **Mẫu:** tab Combo & Chiến dịch sàn (gắn nhãn rõ, chưa có RPC backend). |
| 35 | `/seller/ho-so` | **Kết hợp** | **Thật:** thông tin định danh gian hàng từ `/api/v1/seller/shop`. **Mẫu:** cấu hình hồ sơ nhà máy QC, bảng size xưởng và giao nhanh nội thành (`sellerStore` in-memory; chưa có RPC cập nhật hồ sơ mở rộng). |
| 36 | `/seller/chat` | **Mẫu** | Hội thoại 1-1 với người mua (`MockStoreContext` in-memory); chưa có Chat/WebSocket service. |
| 37 | `/seller/tuong-tac` | **Mẫu** | Phản hồi đánh giá, Q&A, thống kê yêu thích & cảnh báo back-in-stock (`MockStoreContext`/`sellerStore` in-memory); chưa có Review/Q&A service. |

### 2.3 Admin Portal `(admin)` — 9 route (tất cả bảo vệ server `requireRole("admin", "super_admin")`)

| # | Route | Trạng thái | Nguồn dữ liệu & Lý do |
|---|---|---|---|
| 38 | `/admin` | **Thật** | Tổng quan sàn thật: danh sách gian hàng, đơn toàn sàn, GMV/hoa hồng, biểu đồ 7 ngày, trạng thái cân đối sổ cái ghi kép (`/api/v1/admin/ledger/summary`), hàng đợi duyệt và nhật ký kiểm toán. |
| 39 | `/admin/gian-hang` | **Thật** | Danh sách/chi tiết gian hàng (`GET /api/admin/shops`) và duyệt/yêu cầu bổ sung/từ chối (`POST /api/admin/shops/[id]/review`). |
| 40 | `/admin/doi-soat` | **Thật** | Sổ cái ghi kép VND (`GET /api/admin/ledger`: tổng nợ, tổng có, trạng thái cân đối, chi tiết từng tài khoản) và chạy đối soát theo yêu cầu (`POST /api/admin/reconcile`). |
| 41 | `/admin/audit-log` | **Thật** | Nhật ký kiểm toán thật từ backend (`GET /api/admin/audit-logs`). |
| 42 | `/admin/kiem-duyet` | **Kết hợp** | **Thật:** tab mặc định `Sản phẩm chờ duyệt` (`GET /api/admin/products` + `POST /api/admin/products/[id]/review`). **Mẫu:** tab Đánh giá, Q&A và Media biến thể gắn nhãn `(mẫu)` (chưa có service kiểm duyệt nội dung). |
| 43 | `/admin/thanh-vien` | **Kết hợp** | **Thật:** tab mặc định `Mã sàn & Flash sale` — tạo mã `PLATFORM`/`FREESHIP` (`POST /api/admin/vouchers`), tạo khung Flash sale (`POST /api/admin/flash-sales`), duyệt/từ chối suất Flash sale (`POST /api/admin/flash-sale-items/[id]/approve`). **Mẫu:** tab Hạng hội viên & Thẻ quà tặng gắn nhãn `(mẫu)`. |
| 44 | `/admin/khieu-nai` | **Mẫu** | Hàng đợi tranh chấp cấp 2 & SLA (`MockStoreContext` in-memory); chưa có Dispute service. |
| 45 | `/admin/noi-dung` | **Mẫu** | Quản trị CMS trang chủ, Mega-menu, từ đồng nghĩa tìm kiếm, chiến dịch (`MockStoreContext` in-memory); chưa có CMS service. |
| 46 | `/admin/cau-hinh` | **Mẫu** | Cấu hình chính sách sàn, mẫu Email/Zalo ZNS, vận chuyển (`MockStoreContext` in-memory); chưa có Config service. |

**Tổng hợp 46 route:** **23 Thật** (gồm 1 alias redirect) · **14 Kết hợp** · **9 Mẫu** (100% tính năng chưa có microservice backend, tách riêng khỏi dữ liệu thật).

---

## 3. Endpoint Gateway REST & Bổ sung RPC/Proto đã thực hiện

### 3.1 Đã bổ sung trong commit nền `6f143e0` (trên nhánh `phase-0b`)

- **Proto & `services/user` (1 RPC mới):**
  - `proto/nexusmart/user/v1/user.proto` (+ `gen/go/nexusmart/user/v1/{user.pb.go,user_grpc.pb.go}`): thêm `rpc ListActiveShops(ListActiveShopsRequest) returns (ListActiveShopsResponse)`.
  - `services/user/internal/service/server.go`: cài đặt `ListActiveShops` chỉ trả các gian hàng trạng thái `ACTIVE` cho storefront công khai (không cần JWT).
- **Gateway REST (`services/gateway/internal/router/{router.go,handlers_user.go,handlers_product.go,router_storefront_test.go}`):**
  - `GET /api/v1/shops`: công khai danh sách gian hàng `ACTIVE` dưới dạng `publicShopDTO` (chỉ gồm `id`, `name`, `description`, `status`, `created_at` — che thông tin chủ shop, SĐT, giấy phép, địa chỉ lấy hàng).
  - `GET /api/v1/shops/{id}`: tách projection — trả `publicShopDTO` cho khách/người mua (chỉ shop `ACTIVE`), chỉ trả bản ghi đầy đủ cho chủ shop hoặc admin.
  - `GET /api/v1/seller/shop`: trả thông tin đầy đủ gian hàng của seller dựa trên `shop_id` trong JWT (`RequireRoles("seller")` + `RequireSellerShop()`).
  - `GET /api/v1/seller/products`: liệt kê toàn bộ sản phẩm (mọi trạng thái) thuộc `shop_id` của seller từ JWT, không tin query `shop_id`.

### 3.2 Bổ sung thêm trong Phase 0b (0 thay đổi proto / service / migration)

Các file sửa đổi: `services/gateway/internal/router/{router.go,handlers_order.go,handlers_promotion.go,router_storefront_test.go}`:
- `GET /api/v1/admin/products`: gắn RBAC `admin`/`super_admin` ở gateway, ủy quyền `ProductService.ListProducts` để Admin duyệt sản phẩm đa trạng thái across shops.
- `GET /api/v1/seller/orders/{id}`: gắn `RequireRoles("seller")` + `RequireSellerShop()`, kiểm tra `order.shop_id == jwt.shop_id` (khác shop trả `403 PERMISSION_DENIED`).
- `GET /api/v1/seller/vouchers`: gắn `RequireRoles("seller")` + `RequireSellerShop()`, ép `shop_id` từ JWT và `scope=VOUCHER_SCOPE_SHOP` khi gọi `PromotionService.ListAvailableVouchers`.

---

## 4. Hướng dẫn vận hành & Chạy kiểm thử

### 4.1 Khởi chạy cụm backend + web (Docker Compose)

> **Lưu ý trên máy dev hiện tại:** Các cổng mặc định `6379`, `4318`, `8080`, `8082`, `8083`, `8085`, `3111`, `4777` có thể trùng với tiến trình khác. Sử dụng biến override cổng host trong `.env.example`:

```bash
# 1. Khởi chạy backend + web (không dùng `make down` trừ khi chủ đích xóa volume)
REDIS_PORT=26379 OTEL_HTTP_PORT=24318 GATEWAY_PORT=28080 \
PRODUCT_HTTP_PORT=28082 CART_HTTP_PORT=28083 ORDER_HTTP_PORT=28085 \
WEB_PORT=3300 make up

# 2. Nạp dữ liệu mẫu storefront (idempotent — chạy lại nhiều lần không tạo trùng)
make seed
```

- **Web Production (Compose):** `http://localhost:3300` (cổng mặc định `WEB_PORT=3300`, không đụng cổng `3111` của manager hay `4777` của monitor).
- **Gateway REST:** `http://localhost:28080` (không công bố cổng gRPC `50051–50058` ra host; không bật CORS).
- **Mailpit UI (xem OTP đăng ký):** `http://localhost:8025`.

### 4.2 Tài khoản demo sau `make seed`

| Vai trò | Email | Mật khẩu | Ghi chú |
|---|---|---|---|
| Admin (`super_admin`) | `admin@nexusmart.local` | `AdminDevPassword123!` | Bootstrap mặc định |
| Người mua (`buyer`) | `buyer@nexusmart.local` | `BuyerDemo123!` | Có sẵn `42.000 Xu` & địa chỉ mặc định Hà Nội |
| Người bán chính (`seller`) | `seller@nexusmart.local` | `SellerDemo123!` | `Xưởng Dệt Bạch Đằng` |
| 3 xưởng khác (`seller`) | `seller-saigon@nexusmart.local`, `seller-danang@nexusmart.local`, `seller-hanoi@nexusmart.local` | `SellerDemo123!` | `Kho Basic Sài Gòn`, `Sợi Tự Nhiên Đà Nẵng`, `Dệt Kim Hà Nội` |

Dữ liệu nạp sau `make seed`: **5 tài khoản** (1 buyer + 4 seller, cộng 1 super_admin bootstrap), **4 gian hàng `ACTIVE`**, **6 danh mục**, **18 sản phẩm / 159 SKU** (gồm 6 SKU tồn `0` để kiểm trạng thái hết hàng), **11 voucher**, **3 khung Flash sale** (`12:00` đang diễn ra [8 SP], `18:00` & `21:00` sắp mở [4 SP/khung], tổng 76 Flash sale SKU).

### 4.3 Chạy local dev Next.js mà không ghi đè `web/.next`

```bash
cd web
NEXT_DIST_DIR=.next-dev GATEWAY_URL=http://localhost:28080 COOKIE_SECURE=false \
  npx next dev -p 3220
```

### 4.4 Chạy kiểm thử tự động

```bash
make lint      # buf lint + gofmt/golangci-lint (10 Go modules) + rustfmt/clippy (payment)
make test      # go test -race (10 Go modules) + cargo test (31 tests)
make e2e       # 8 bài E2E backend trong network compose
make seed      # Seed idempotent qua gateway + Mailpit
make e2e-web   # Rebuild container web + 10 bài E2E qua BFF trong network compose
```

---

## 5. Kiểm toán `localStorage` (`grep -rn "localStorage" web/src`)

Kết quả chạy `grep -rn "localStorage" web/src` chỉ có **1 file duy nhất** gọi `window.localStorage` (`web/src/context/StoreContext.tsx`); **0 chỗ lưu giỏ hàng, 0 chỗ lưu đơn hàng, 0 chỗ lưu token/phiên đăng nhập, 0 chỗ lưu địa chỉ/PII**:

| File & Dòng | Loại | Nội dung & Lý do giữ lại |
|---|---|---|
| `web/src/context/StoreContext.tsx:168` | Đọc (`getItem`) | Đọc khóa `nexusmart_viewer_prefs_v1` gồm đúng 4 tiện ích theo người xem: `bodyProfile` (cao/nặng/dáng cho gợi ý size), `wishlist` (đánh dấu SP yêu thích — chưa có Wishlist service), `recentlyViewed` (tối đa 12 ID SP vừa xem), `cookiePrefs` (tùy chọn banner cookie). Không chứa PII, giỏ, đơn hay token. |
| `web/src/context/StoreContext.tsx:202` | Xóa (`removeItem`) | Dọn dẹp khóa cũ `nexusmart_store_v2` từ bản mock trước Phase 0b nếu còn tồn tại trong trình duyệt người dùng. |
| `web/src/context/StoreContext.tsx:213` | Ghi (`setItem`) | Lưu 4 trường tiện ích `bodyProfile`, `wishlist`, `recentlyViewed`, `cookiePrefs` vào `nexusmart_viewer_prefs_v1` sau khi hydrate. |
| `web/src/context/StoreContext.tsx:60,87,165` | Comment | Ghi chú quy tắc không lưu giỏ/đơn/token/PII vào `localStorage`. |
| `web/src/mock/MockStoreContext.tsx:9` | Comment | Ghi chú `MockStoreContext` chỉ sống trong bộ nhớ, không ghi `localStorage`. |
| `web/src/features/seller/sellerStore.ts:11` | Comment | Ghi chú không lưu dữ liệu backend vào `localStorage`. |
| `web/src/features/admin/auditStore.ts:13` | Comment | Ghi chú không dùng `localStorage`. |

---

## 6. Kết quả xác minh từng Gate (chạy độc lập bởi `verifier`)

### Gate 1 — Bất biến kho mã nguồn & Chính sách

| Kiểm tra | Lệnh / Bằng chứng | Kết quả |
|---|---|---|
| Nhánh & Commit | `git branch --show-current` → `phase-0b`; `git rev-parse --short HEAD` → `6f143e0` (0 commit mới) | **PASS** |
| Trigger CI | `.github/workflows/ci.yml` chỉ có `on: workflow_dispatch:` (0 `push`/`pull_request`/`schedule`), có thêm job `web` và `docker-build-web` | **PASS** |
| Runtime deps `web/` | `git diff HEAD~1 -- web/package.json` trống; chỉ giữ `next`, `react`, `react-dom` (0 dependency runtime mới) | **PASS** |
| `pkg/common/auth` | `git diff HEAD -- pkg/common/auth` trống (không sửa đổi/nới lỏng) | **PASS** |
| Cổng gRPC nội bộ | `docker compose config \| grep -c 'published: "5005'` → `0` | **PASS** |
| Gateway không CORS | `curl -sSI -X OPTIONS -H 'Origin: https://evil.example' http://localhost:28080/healthz` & `GET /api/v1/categories` → 0 header `Access-Control-*` | **PASS** |
| `web/Dockerfile` | 3 stage (`deps` → `builder` → `runner`, standalone output), chạy bằng non-root `USER node` | **PASS** |
| Bảo toàn môi trường | `web/.next` giữ nguyên mtime `1795145079` (`2026-11-20 10:24:39.936420820 +0700`); cổng `3111` (`next-server`) và `4777` (`python3`/`socat`) giữ nguyên | **PASS** |

### Gate 2 — Chất lượng mã Web, Build sạch & Unit Test

| Kiểm tra | Kết quả |
|---|---|
| `cd web && npm run lint` | **PASS** (0 errors, 0 warnings) |
| `cd web && npm run format:check` | **PASS** (`All matched files use Prettier code style!`) |
| `cd web && npx tsc --noEmit` | **PASS** (0 errors) |
| Build nguyên văn `npm run build` trong bản sao tách biệt `/tmp/nexus-web-verify-build` (xóa sau khi chạy) | **PASS** (`Compiled successfully`, `Generating static pages (47/47)`, `web/.next` mtime bất biến `1795145079`) |
| `node /home/dev/.claude/skills/design-taste-frontend/scripts/lint.mjs web/src` | **PASS** (`285 file(s)`, `RESULT: 0 FAIL, 10 WARN` — 0 FAIL mới) |
| Web unit tests (`security.test.mjs` 12 + `catalog-map.test.mjs` 11 + `checkoutLogic.test.mjs` 5 + `orderView.test.mjs` 7 + `accountTypes.test.mjs` 3) | **PASS 38/38** (`fail 0`, `161.5ms`) |

### Gate 3 — Backend (`make lint`, `make test`, `make e2e`)

| Lệnh | Kết quả |
|---|---|
| `make lint` | **PASS** (`buf lint`, `gofmt` + `golangci-lint` trên 10 Go module, `cargo fmt --check` + `cargo clippy --all-targets -- -D warnings` trên `services/payment`) |
| `make test` | **PASS** (`go test -race ./...` trên 10 Go module + `cargo test` `31/31` trên `services/payment`) |
| `make e2e` | **PASS 8/8**: Health & OTel TraceID & RED metrics, MultiShop COD, MultiShop VietQR Mock, Order Idempotency (sequential + concurrent), Webhook HMAC & 10× replay, Flash Sale 1000 concurrent/100 slots (0 oversell), PaymentFailed Saga Compensation (100% hoàn stock/voucher/xu), Double-Entry Ledger cân đối (`32.243.870 == 32.243.870`, `is_balanced=true`) |

### Gate 4 — Seed Idempotent & Web E2E (`make seed`, `make e2e-web`)

| Lệnh | Kết quả |
|---|---|
| `make seed` (lần 1 & lần 2 liên tiếp) | **PASS** (exit 0 cả 2 lần; `created: 0`, `reused: 5 users / 4 shops / 6 categories / 18 products [159 SKUs] / 11 vouchers / 3 flash windows [76 items]`, buyer coins giữ nguyên `42.000`) |
| `make e2e-web` | **PASS 10/10** (`suites 2, pass 10, fail 0, duration_ms 2180.03`): (a) đăng ký + OTP Mailpit → đăng nhập cookie `HttpOnly` host-only (0 token leak) → `/api/auth/me` → refresh rotation → logout; (b)(c)(d) giỏ 2 gian hàng → áp voucher thật → đặt COD → thấy đơn & giảm giá trong `/api/orders`; (e) VietQR mock → webhook HMAC ngoài → poll `SUCCEEDED` & `CONFIRMED`; (f) gửi lại checkout cùng `Idempotency-Key` trả đúng 1 đơn (`201` → `200 idempotent_replay`); (g) Buyer A không đọc/hủy được đơn/payment của Buyer B (`403/404`); (h) 13 endpoint ghi/riêng tư không có phiên → `401`; (i) CSRF (`Origin` lạ / thiếu `Origin` / `Origin: null` / `Sec-Fetch-Site: cross-site`) → `403 BAD_ORIGIN`; (j) RBAC server-side Buyer/Seller/Admin + sổ cái cân đối |

### Gate 5 — `localStorage` Audit

- **PASS:** Chi tiết ở Mục 5 (0 giỏ, 0 đơn, 0 token/auth, 0 PII; chỉ còn `nexusmart_viewer_prefs_v1` cho 4 tiện ích người xem và `removeItem("nexusmart_store_v2")` dọn dữ liệu cũ).

### Gate 6 — Kiểm định trực quan/responsive `inspect.mjs` (`1440 / 768 / 390` với dữ liệu thật)

- **Sự cố phát hiện & đã xử lý trong quá trình xác minh:** Ở lượt chạy `inspect.mjs` đầu tiên với phiên người mua đã đăng nhập, cả 3 route `/gio-hang`, `/thanh-toan`, `/don-hang` đều báo `1 FAIL [overflow] @1440px` (`Page scrolls sideways by 14px`, nguyên nhân do nút tài khoản `Minh Quân ▾` rộng hơn `Đăng nhập` kết hợp với `.searchWrap { min-width: 190px }` đẩy nút `Giỏ hàng` lệch phải 14px). Đã báo trực tiếp cho `@bff`; `@bff` chỉnh khoảng cách header & hạ `min-width` `.searchWrap` xuống `150px` trong `web/src/components/ShopHeader.module.css`, sau đó `verifier` đã chạy lại toàn bộ Gate 2, rebuild container `web` qua `make e2e-web` (10/10) và chạy lại `inspect.mjs` trên tất cả các tuyến.
- **Phương thức kiểm thử phiên thật:** 3 route công khai chạy trực tiếp vào compose `web` (`http://127.0.0.1:3300`); 3 route cần phiên người mua (`/gio-hang`, `/thanh-toan`, `/don-hang`) chạy qua reverse proxy tạm `127.0.0.1:3220 → 127.0.0.1:3300` gắn cookie phiên thật (`nm_at`, `nm_rt`) của `buyer@nexusmart.local` (có sẵn 2 sản phẩm từ 2 gian hàng trong giỏ và đơn hàng thật trong `/api/orders`), đã đối chiếu ảnh chụp màn hình xác nhận render đúng dữ liệu thật (không bị redirect về `/dang-nhap`).

| # | Route | Chế độ | `1440px` / `768px` / `390px` | Kết quả |
|---|---|---|---|---|
| 1 | `/` (Trang chủ) | Công khai (dữ liệu seed thật) | `Work Sans 100%`, `0px` tràn ngang, 62 ảnh | **0 FAIL, 0 WARN** |
| 2a | `/danh-muc` (Danh mục) | Công khai (18 SP / 6 danh mục thật) | `Work Sans 100%`, `0px` tràn ngang, 24 ảnh | **0 FAIL, 1 WARN** (`[no-footage]`) |
| 2b | `/danh-muc?cat=ao-thun` | Công khai (lọc danh mục thật) | `Work Sans 100%`, `0px` tràn ngang, 10 ảnh | **0 FAIL, 2 WARN** (`[no-footage]`, `[still-page]`) |
| 3 | `/san-pham/ao-thun-cotton-compact-trang` | Công khai (tồn SKU & Flash sale live) | `Work Sans 100%`, `0px` tràn ngang, 27 ảnh | **0 FAIL, 0 WARN** |
| 4 | `/gio-hang` | Phiên `buyer@nexusmart.local` (2 SP / 2 xưởng) | `Work Sans 100%`, `0px` tràn ngang, 10 ảnh | **0 FAIL, 3 WARN** (`[no-footage]`, `[off-centre]`, `[still-page]`) |
| 5 | `/thanh-toan` | Phiên `buyer@nexusmart.local` (địa chỉ + 42k Xu + 2 xưởng) | `Work Sans 100%`, `0px` tràn ngang, 10 ảnh | **0 FAIL, 2 WARN** (`[no-footage]`, `[still-page]`) |
| 6 | `/don-hang` | Phiên `buyer@nexusmart.local` (đơn hàng thật) | `Work Sans 100%`, `0px` tràn ngang, 9 ảnh | **0 FAIL, 2 WARN** (`[no-footage]`, `[still-page]`) |

**Tổng Gate 6:** **0 FAIL** trên cả 6 tuyến bắt buộc (7 URL · 21 lượt đo viewport `1440/768/390`).

---

## 7. Phần chưa làm / Ngoài phạm vi & Hạn chế đã biết

1. **Tính năng giữ dữ liệu mẫu (ngoài phạm vi backend Phase 0b):**
   - Chương trình `Hội viên Nexus` (xét hạng 4 quý, lịch sử Xu, sinh nhật, giới thiệu bạn bè), `Thẻ quà tặng` & xác thực `.edu`, `Tạp chí / Lookbook` & `CMS nội dung`, `Đánh giá & Hỏi đáp (Q&A)`, `Chat 1-1` & `Trợ lý AI`, `Trung tâm trợ giúp & Khiếu nại cấp 2`, `Cấu hình sàn / mẫu Email & Zalo ZNS`, `Rút tiền ngân hàng cho Seller`, `Xuất hóa đơn điện tử (VAT)` và `Tra cứu đơn vãng lai không cần đăng nhập`. Toàn bộ đã được cô lập trong `web/src/mock/MockStoreContext.tsx` (in-memory) hoặc gắn nhãn dữ liệu mẫu rõ ràng.
2. **Hạn chế đã biết trong môi trường dev/test dùng chung DB:**
   - **Rate limit theo IP giữa `make e2e` và `make e2e-web` khi chạy sát nhau:** Trong Docker Compose, container chạy 1 lần của `make e2e` và `make e2e-web` có thể được cấp lại cùng IP bridge nội bộ (ví dụ `172.30.13.17`). Nếu chạy `make e2e` (~180 request) rồi chạy ngay `make e2e-web` (~145 request) trong cùng cửa sổ 60 giây dưới cấu hình mặc định `RATE_LIMIT_IP_PER_MIN=300`, 1–2 request cuối có thể gặp `429 RATE_LIMITED` (khi `getSession()` ném `GatewayError("rate_limited")` ở layout bảo vệ sẽ trả 500 vì chưa có `error.tsx` riêng cho 429). Chạy độc lập từng lệnh hoặc 2 lượt `make e2e-web` liên tiếp đều pass `10/10` sạch.
   - **Voucher phát sinh từ Go E2E (`PLA...`) hiển thị trên `/gio-hang`:** Sau khi chạy `make e2e` trên cùng database dev, các voucher sàn test tự sinh (`PLA<hex>`) vẫn nằm trong `promotion_db` và xuất hiện ở danh sách "Ưu đãi toàn sàn" trên `/gio-hang` (vì `CartPage` gọi trực tiếp `GET /api/v1/vouchers` thay vì qua bộ lọc `mergeVouchers` của `catalog-map.ts`). Không ảnh hưởng chức năng và không xảy ra trên DB sạch chỉ chạy `make seed`.
   - **Cảnh báo `inspect.mjs` mức WARN:** Các trang chức năng thương mại (`/danh-muc`, `/gio-hang`, `/thanh-toan`, `/don-hang`) có WARN `[no-footage]` (cố ý không nhúng video vào màn giỏ/thanh toán/đơn), `[still-page]` (số phần tử chuyển động 10–15 dưới ngưỡng 16 của trang editorial), và `[off-centre]` trên `/gio-hang` do thanh cuộn trình duyệt ở 1440px. Tất cả đều đạt `0 FAIL`.
