# Rà soát nghiệp vụ giao diện Web (`web`) – 2026-11-23

- **Commit HEAD**: `0db2cdd`
- **Stack kiểm**: `nexusmart-audit` (`web` `http://localhost:43000`, `gateway` `http://localhost:48080`, `mailpit` `http://localhost:48025`)
- **Tài khoản kiểm**: buyer tự đăng ký qua BFF `audit-web-605262@nexusmart.local` (`49d561da-9506-45c2-879c-e4c6456141cb`), seller seed `seller.bachdang@nexusmart.local` (`shop_id: 61a74907-4b71-43d5-bb39-b2df456281ae`), admin seed `admin@nexusmart.local` (`super_admin`). Không sửa dữ liệu của `minhquan.nguyen@nexusmart.vn`.
- **Đơn hàng tạo thử (2 đơn)**:
  - COD: `d5679bde-6fd9-4e84-b64a-093a9e4e8916` (`NM-20261122-523378D2`, `CONFIRMED`, `256.500₫`).
  - VietQR: `c03c12fd-64ac-4f59-b1c5-6112a741d298` (`NM-20261122-494A6C5E`, `PENDING`, `255.000₫`, `payment_id: 3360a7c7-fea3-420b-be9c-df70ecf61376`).

---

## 1. Bảng toàn bộ route giao diện (45 trang) & BFF API (8 route)

### 1.1. Nhóm người mua `(shop)` – 25 route

| Route | Vai trò | HTTP | Nguồn dữ liệu (gateway thật / mock / tĩnh) | Lỗi | Ghi chú |
| --- | --- | --- | --- | --- | --- |
| `/` | Khách | 200 | Gateway thật (SSR: `/categories`, `/shops`, `/products`, `/flash-sales`) + mock (`productEditorial`: `originalPrice`, rating giả, banner/copy) | `WEB-D1`, `WEB-D2`, `WEB-D3`, `WEB-D4`, `WEB-D5` | Khớp tên/giá/shop/danh mục gateway; header hiện sẵn `Yêu thích 2` mock; 0 lỗi console, không tràn ngang 1440/390 |
| `/danh-muc` | Khách | 200 | Gateway thật (SSR: `/categories`, `/shops`, `/products`) + mock (`originalPrice`, rating giả trên `ProductCard`) | `WEB-D2`, `WEB-D3` | Lọc danh mục/giá/màu/size/xưởng chạy trên tập SP thật (20 SP đang bán); 0 lỗi console |
| `/tim-kiem` | Khách | 200 | Gateway thật (SSR: `/products?q=...`, `/categories`, `/shops`) + mock (`search.ts` map shop bằng ID mock; `ShopHeader` gợi ý tức thì đọc `PRODUCTS` tĩnh) | `WEB-B8`, `WEB-D2`, `WEB-D3` | `/tim-kiem?q=ao+thun` khớp 8/20 SP thật; tìm theo tên xưởng lỗi vì so `product.shopId` với `shop-bach-dang` |
| `/san-pham/[slug]` | Khách | 200 (404 nếu sai slug) | Gateway thật (SSR: `/products/slug/{slug}`, `/shops`, `/products`) + mock (giảm giá theo bậc, quà ngưỡng 499k, chọn size combo, dự tính giao `provinces.ts`, đánh giá & Hỏi đáp `MockStore`) | `WEB-B3`, `WEB-D1`, `WEB-D2`, `WEB-D3` | Khớp giá `229.000₫` & tồn kho thật; hứa giảm 10–15% & quà 499k không được giỏ/checkout áp dụng |
| `/gian-hang/[slug]` | Khách | 200 (404 nếu sai slug) | Gateway thật (SSR: `/shops/{slug}`, `/products?shop_id=...`, `/vouchers?scope=SHOP`) + mock (chỉ số phản hồi/tỉ lệ giao/ngưỡng freeship shop) | `WEB-D3` | Không lộ `contact_phone`; hiển thị đúng SP và voucher thật của `Xưởng Dệt Bạch Đằng` |
| `/flash-sale` | Khách | 200 | Hỗn hợp: khung giờ Flash sale gọi gateway thật (SSR: `/flash-sales?include_upcoming=true`) + 2 khối mock `Deal sốc` & `Combo` (`FlashSaleClient.tsx`) | `WEB-B2`, `WEB-D2`, `WEB-D6` | Khung giờ thật hiển thị số suất; bấm thêm giỏ ở Deal sốc/Combo đẩy SKU thật với nguyên giá gốc |
| `/gio-hang` | Buyer | 200 | Gateway thật (`/cart`, `/vouchers`, `/coins/balance`, `/users/me/addresses`, `/shipping/calculate-fee`, `/promotions/preview`) + gợi ý SP thật | `WEB-B1` | Đồng bộ giỏ Redis thật; gửi `from_province_code: ""` khi ước tính phí ship gây lệch phí/freeship |
| `/thanh-toan` | Buyer | 200 (khách: 307) | Gateway thật (`/cart`, `/users/me/addresses`, `/shipping/provinces*`, `/shipping/calculate-fee`, `/vouchers`, `/coins/balance`, `/promotions/preview`, `/checkout`) | `WEB-B1`, `WEB-B9` | Phí ship hiển thị `35.500₫` (tổng `264.500₫`) lệch với đơn COD tạo ra `27.500₫` (tổng `256.500₫`); mua ngay Flash sale tính `subtotalVnd` theo giá thường |
| `/thanh-toan/cho-thanh-toan/[paymentId]` | Buyer | 200 (404 nếu sai ID) | Gateway thật (SSR: `/payments/{id}`; client: SSE `/payments/{id}/events` + poll + `POST /payments/{id}/retry`) | Không | Kiểm live trên `3360a7c7-...` (`255.000₫`): hiện QR, đếm ngược 15p, trạng thái `Chờ thanh toán` |
| `/don-hang` | Buyer | 200 (khách: 307) | Gateway thật (SSR: `/orders`, `/shops`, `/products`) + `ProductAddonStrip` đọc `PRODUCTS` tĩnh | Không (nhỏ: gợi ý dưới trang tĩnh) | Khớp đúng 2 đơn mới `NM-20261122-523378D2` (`256.500₫`) và `NM-20261122-494A6C5E` (`255.000₫`) |
| `/don-hang/[id]` | Buyer | 200 (404 nếu sai/không thuộc user) | Gateway thật (SSR: `/orders/{id}`, `/shipping/shipments/{id}`; client: cancel/confirm-received/return/retry-payment) | Không (copy đổi trả 45 ngày trên gợi ý chung) | Khớp chi tiết đơn COD `NM-20261122-523378D2`, trạng thái `CONFIRMED`; `/don-hang/ord-109` trả 404 |
| `/tai-khoan` | Buyer | 200 (khách: 307) | Hỗn hợp: hồ sơ, địa chỉ, đơn hàng, số dư Xu (`0 Xu`), voucher, mở gian hàng, thông báo là gateway thật; thẻ `Hạng Bạc (xem thử)` (`2.350.000₫`), sinh nhật (`SINHNHAT50`, `NEXUSVIP12`), số đo, yêu thích, giới thiệu là mock `MockStore` | `WEB-B7`, `WEB-D5` | Đọc đúng `0 Xu` & thông tin buyer thật; nhưng vẫn hiển thị chi tiêu 4 quý mock `2.350.000₫` trong thẻ hero và nút nhận quà sinh nhật đẩy sang `/gio-hang` trống |
| `/dang-nhap` | Khách | 200 | BFF auth thật (`POST /api/auth/login`) + ảnh minh họa từ `PRODUCTS` tĩnh | `WEB-B4`, `WEB-D1` | Bỏ qua `user.role` khi không có `?next=`: cả `seller` và `admin` đều bị chuyển về `/` thay vì `/seller`, `/admin` |
| `/dang-ky` | Khách | 200 | BFF auth thật (`POST /api/auth/register`, `verify-otp`, `resend-otp`) | `WEB-D1`, `WEB-D4` | Đăng ký + OTP Mailpit hoạt động tốt; copy hứa nhầm `"nhận ngay 15.000 Xu"` và `"đổi trả miễn phí 45 ngày"` |
| `/tra-cuu-don-hang` | Khách | 200 | Mock (`web/src/mock/guestOrderLookup.ts` đọc `INITIAL_ORDERS`, `DEMO_USER`) | `WEB-D1`, `WEB-D6` | Không gọi API thật; quảng cáo hóa đơn VAT điện tử (ngoài phạm vi) và đổi trả 45 ngày |
| `/tra-cuu-don` | Khách | 307 → `/tra-cuu-don-hang` | Tĩnh (alias redirect) | Không | Chuyển hướng 307 chuẩn về `/tra-cuu-don-hang` |
| `/bo-suu-tap` | Khách | 200 | Gateway thật (SSR lấy SP) + nội dung bộ sưu tập tĩnh (`@/data`) | Không | Hiển thị 5 bộ sưu tập; 0 lỗi console |
| `/bo-suu-tap/[slug]` | Khách | 200 | Gateway thật (SP) + tĩnh (`COLLECTIONS`) | `WEB-B10` | Soft-404: `/bo-suu-tap/khong-ton-tai` trả HTTP 200 thay vì 404 |
| `/tap-chi` | Khách | 200 | Tĩnh (`JOURNAL_ARTICLES` trong `@/data`) | Không | Nội dung biên tập hợp lệ; 0 lỗi console |
| `/tap-chi/[slug]` | Khách | 200 | Tĩnh + gợi ý SP thật | `WEB-B10` | Soft-404: `/tap-chi/khong-ton-tai` trả HTTP 200 thay vì 404 |
| `/chien-dich/[slug]` | Khách | 200 | Gateway thật (SP) + nội dung chiến dịch tĩnh | `WEB-B10` | Soft-404: `/chien-dich/khong-ton-tai` trả HTTP 200 thay vì 404; `/chien-dich/soi-xanh-nexus` có nhãn dữ liệu mẫu |
| `/ve-nexusmart` | Khách | 200 | Tĩnh (`BRAND_STORY` trong `@/data`) | `WEB-D1` | Trang giới thiệu tĩnh hợp lệ nhưng ghi chính sách đổi trả 45 ngày |
| `/hoi-vien` | Khách / Buyer | 200 | Mock (`INITIAL_USER_COINS = 42.000`, `MockStore.membership`) trộn với tên `user.full_name` thật | `WEB-B7`, `WEB-D1`, `WEB-D6` | **Số liệu giả đóng vai thật**: hiển thị `42.000 Xu khả dụng`, `18.500 Xu chờ duyệt`, `Hạng Bạc`, `2.350.000₫` cho tài khoản mới `Audit Web Buyer` (số dư thật `0 Xu`) |
| `/the-qua-tang` | Khách | 200 | Mock (`MockStore` + `GIFT_CARD_POLICY`) | `WEB-B7`, `WEB-D1`, `WEB-D6` | Tính năng ngoài phạm vi; áp mã thẻ đẩy về `/gio-hang` không có tác dụng |
| `/tro-giup` | Khách / Buyer | 200 | Tĩnh (FAQ) + Mock (`MockStore.supportTickets` chứa sẵn phiếu của `DEMO_USER`) | `WEB-D1`, `WEB-D5` | Hiển thị sẵn phiếu `tk-261119-01` (đơn mẫu `NM-261117-082`) cho mọi tài khoản mới; toàn bộ FAQ ghi đổi trả 45 ngày |

### 1.2. Nhóm người bán `(seller)` – 11 route

| Route | Vai trò | HTTP | Nguồn dữ liệu (gateway thật / mock / tĩnh) | Lỗi | Ghi chú |
| --- | --- | --- | --- | --- | --- |
| `/seller` | Seller | 200 (khách 307; buyer 404) | Gateway thật (`/seller/shop`, `/seller/orders`, `/seller/products`, `/seller/balance`) | `WEB-D5` (sidebar badge mock `Tương tác 5`) | Tổng quan số liệu thật khớp `Xưởng Dệt Bạch Đằng`, hiện đơn chờ duyệt `2`; 0 lỗi console |
| `/seller/san-pham` | Seller | 200 | Gateway thật (`/seller/products`, `/categories`) | Nhỏ: dùng cụm `tồn kho SKU` | Liệt kê đúng SP thật của shop; ảnh tải 200 |
| `/seller/san-pham/moi` | Seller | 200 | Gateway thật (`POST /seller/products`, `/categories`) | Không | Biểu mẫu tạo SP & biến thể thật; 0 lỗi console |
| `/seller/san-pham/[id]` | Seller | 200 | Gateway thật (`GET/PUT /seller/products/{id}`, `/seller/skus/{id}/stock`) | `WEB-B6` | Kiểm trên `39036630-...` OK; nhưng dropdown trạng thái vẫn cho chuyển `PENDING → HIDDEN` (bị backend chặn 403) |
| `/seller/don-hang` | Seller | 200 | Gateway thật (`GET /seller/orders`) | Không | Hiển thị đúng đơn COD mới `NM-20261122-523378D2` |
| `/seller/don-hang/[id]` | Seller | 200 | Gateway thật (`GET /seller/orders/{id}`, `/shipping/shipments/{id}`, confirm/ship/cancel) | Nhỏ (`WEB-D7`: gọi `/shipping/shipments/{id}` trả 404 khi đơn chưa tạo vận đơn) | Hiển thị đúng chi tiết `NM-20261122-523378D2`, UI xử lý gọn trạng thái chưa có vận đơn |
| `/seller/khuyen-mai` | Seller | 200 | Hỗn hợp: tab Voucher (`/seller/vouchers`) & Flash Sale (`/flash-sales`, `/seller/flash-sales/{id}/items`) là gateway thật; tab Combo & Chiến dịch là mock (`sellerStore`) | `WEB-D6` | Trộn khuyến mãi thật với Combo/Chiến dịch mock |
| `/seller/tai-chinh` | Seller | 200 | Hỗn hợp: số dư (`/seller/balance`) & đơn hàng (`/seller/orders`) là gateway thật; thông tin ngân hàng là mock tĩnh (`getShopBankAccount`) | Không | Khớp số dư thật; chưa có form khai báo ngân hàng/rút tiền thật |
| `/seller/ho-so` | Seller | 200 | Hỗn hợp: hồ sơ xưởng (`GET/PATCH /seller/shop`) là gateway thật; tab Bảng size, Vận hành, Huy hiệu đổi trả 45 ngày là mock (`sellerStore`) | `WEB-D1` | Tab Vận hành bật/tắt freeship & đổi trả 45 ngày không ghi vào backend |
| `/seller/chat` | Seller | 200 | Mock 100% (`MockStore.chatThreads`) | `WEB-B5` | Chưa có Chat service; sinh link `/seller/don-hang/ord-*` dẫn tới 404 |
| `/seller/tuong-tac` | Seller | 200 | Mock 100% (`sellerStore` – đánh giá, hỏi đáp, bài viết) | `WEB-B5`, `WEB-D1` | Sinh link `/seller/san-pham/prod-*` dẫn tới 404 |

### 1.3. Nhóm quản trị `(admin)` – 9 route

| Route | Vai trò | HTTP | Nguồn dữ liệu (gateway thật / mock / tĩnh) | Lỗi | Ghi chú |
| --- | --- | --- | --- | --- | --- |
| `/admin` | Admin | 200 (khách 307; buyer/seller 404) | Gateway thật (`/admin/shops`, `/admin/products`, `/admin/ledger/summary`, `/admin/orders`, `/admin/users`, `/admin/audit-logs`) | `WEB-D5` (sidebar badge mock `Khiếu nại mở 5`) | Hiển thị đúng 0 shop chờ duyệt, 1 SP chờ duyệt, sổ cái cân bằng; 0 lỗi console, không tràn ngang 1440/390 |
| `/admin/gian-hang` | Admin | 200 | Gateway thật (`GET /admin/shops`, `POST /admin/shops/{id}/approve|reject|suspend`) | Không | Hiển thị đúng danh sách gian hàng thật |
| `/admin/doi-soat` | Admin | 200 | Gateway thật (`GET /admin/ledger/summary`, `/admin/orders`) | Không | Sổ cái ghi kép hiển thị `Cân bằng` (`256.500₫ / 256.500₫`); không tràn ngang 390px |
| `/admin/audit-log` | Admin | 200 | Gateway thật (`GET /admin/audit-logs`) | Không | Hiển thị 13 bản ghi nhật ký thật từ gateway |
| `/admin/cau-hinh` | Admin | 200 | Hỗn hợp: tab Danh mục (`/admin/categories`) & Tài khoản/phân quyền (`/admin/users`) là gateway thật; tab Vận chuyển, Thanh toán, Đổi trả 45 ngày, Mẫu thông báo là mock (`auditStore`) | `WEB-D1` | 2 tab quản trị lõi chạy thật, 4 tab cấu hình còn lại chỉ lưu RAM/mock |
| `/admin/kiem-duyet` | Admin | 200 | Hỗn hợp: tab đầu `Sản phẩm` (`/admin/products`) là gateway thật; 3 tab Đánh giá, Hỏi đáp, Media & từ khóa cấm là mock (`auditStore`) | `WEB-B10` | SSR/Suspense fallback vẫn ghi tiêu đề cũ `"Kiểm duyệt đánh giá, hỏi đáp & media"`, client đổi thành `"Kiểm duyệt sản phẩm & nội dung"` |
| `/admin/thanh-vien` | Admin | 200 | Hỗn hợp: tab Flash sale/Voucher & Cộng Xu (`AdminPromoCoinsPanel`) là gateway thật; tab Hội viên & Thẻ quà tặng là mock (`auditStore`) | `WEB-D6` | Trộn khuyến mãi/Xu thật với Hội viên/Thẻ quà tặng ngoài phạm vi |
| `/admin/khieu-nai` | Admin | 200 | Mock 100% (`MockStore.disputes`) | `WEB-D5` | Hiển thị 5 khiếu nại mẫu giả (`tạm giữ 1.406.000₫`), không có API backend |
| `/admin/noi-dung` | Admin | 200 | Mock 100% (`auditStore`) | `WEB-D1` | Quản lý Mega-menu/chiến dịch/nhãn 45 ngày chỉ thay đổi state cục bộ, không phản ánh ra trang chủ |

### 1.4. Nhóm BFF API (`/api/*`) – 8 route

| Route | Vai trò | HTTP | Nguồn dữ liệu | Lỗi | Ghi chú |
| --- | --- | --- | --- | --- | --- |
| `POST /api/auth/register` | Khách | 201 | Gateway `/api/v1/auth/register` | Không | Kiểm CSRF `Origin` (thiếu/sai → `403 CSRF_FORBIDDEN`) |
| `POST /api/auth/verify-otp` | Khách | 200 | Gateway `/api/v1/auth/verify-otp` | Không | Xác thực OTP từ Mailpit thành công |
| `POST /api/auth/resend-otp` | Khách | 200 (chưa gọi lại) | Gateway `/api/v1/auth/resend-otp` | Không | Kiểm CSRF `Origin` trong code (`web/src/app/api/auth/resend-otp/route.ts`) |
| `POST /api/auth/login` | Mọi vai trò | 200 | Gateway `/api/v1/auth/login` | Không | Đặt cookie `nm_access_token` (`HttpOnly`, `SameSite=Lax`, `Path=/`, `Max-Age=900`) và `nm_refresh_token` (`Max-Age=604800`); chặn CSRF |
| `POST /api/auth/logout` | Đã đăng nhập | 200 | Gateway `/api/v1/auth/logout` | Không | Xóa cookie phiên; kiểm CSRF `Origin` (đã test evil Origin → 403) |
| `POST /api/auth/refresh` | Đã đăng nhập | 200 | Gateway `/api/v1/auth/refresh` | Không | Kiểm CSRF `Origin` (đã test thiếu Origin → 403) |
| `GET /api/auth/session` | Mọi vai trò | 200 | Gateway `/api/v1/users/me` | Không | Trả `user` thật khớp vai trò; không lộ JWT ra client |
| `ALL /api/gw/[...path]` | Theo endpoint | 200/201/401/403/404 | Proxy tới Gateway `/api/v1/*` | Không | Chặn `webhooks/*` & `auth/*` (`403 FORBIDDEN_PATH`); kiểm CSRF `Origin` cho mọi mutation (bỏ qua giả mạo `X-Forwarded-Host`); tự refresh 1 lần khi 401 |

---

## 2. Bảng UC liên quan giao diện web (82 UC có bề mặt UI)

> 18 UC thuần hệ thống/DevOps/AIOps/webhook nền không có màn hình web (`UC-OPS01..09`, `UC-AI13..14`, `UC-PAY03`, `UC-PAY06`, `UC-PAY07`, `UC-SHIP04`, `UC-SHIP05`, `UC-SHIP07`, `UC-SHIP08`) thuộc phạm vi kiểm của các worker backend/platform.

| UC | Ưu tiên | Trạng thái | Backend (file:dòng) | REST | Web | Bằng chứng | Ghi chú |
| --- | --- | --- | --- | --- | --- | --- | --- |
| UC-B01 | Cao | ĐỦ | `services/user` (qua gateway `auth/register`, `verify-otp`) | `POST /api/v1/auth/register`, `POST /api/v1/auth/verify-otp` | `/dang-ky` (`dang-ky/page.tsx`) | `POST /api/auth/register` → 201 (`PENDING_VERIFICATION`), OTP Mailpit → `verify-otp` 200 | Copy trang hứa nhầm 15.000 Xu & 45 ngày (`WEB-D1`, `WEB-D4`) |
| UC-B02 | Cao | LỖI | `services/user` (`auth/login`) | `POST /api/v1/auth/login` | `/dang-nhap` (`dang-nhap/page.tsx:38-88`) | Chrome submit form bằng seller & admin không có `?next=` đều chuyển về `/` | Cookie `HttpOnly` & refresh chạy đúng, sai redirect theo role (`WEB-B4`) |
| UC-B03 | Cao | MỘT PHẦN | `services/product` (`ListProducts`) | `GET /api/v1/products?q=...` | `/tim-kiem`, `/danh-muc` (`CatalogExplorer.tsx`) | `GET /api/gw/products?q=ao+thun` → 200 (9 SP); Chrome `/tim-kiem?q=ao+thun` hiện 8/20 SP | Chưa dùng Search service/ES; chưa lọc theo đánh giá; gợi ý header dùng mock (`WEB-B8`) |
| UC-B04 | Cao | LỖI | `services/product` (`GetProductBySlug`) | `GET /api/v1/products/slug/{slug}` | `/san-pham/[slug]` (`ProductDetailClient.tsx:246-899`) | SSR trả 200 khớp `229.000₫` & tồn 24, nhưng hiện giá chiết khấu bậc 10–15% & quà 499k giả | Giá giảm bậc/quà/size combo không áp dụng ở checkout (`WEB-B3`) |
| UC-B05 | Cao | ĐỦ | `services/cart` (Redis) | `GET/POST/PUT/DELETE /api/v1/cart*` | `/gio-hang`, `CartContext.tsx` | `POST /api/gw/cart/items` → 200 (`total_items: 1, total_vnd: 229000`); Chrome `/gio-hang` 200 | Đồng bộ giỏ thật, clamp tồn kho; phí ship ước tính bị lệch theo `WEB-B1` |
| UC-B06 | Cao | LỖI | `services/order` (`Checkout`), `services/gateway/internal/router/handlers_shipping.go:179-205` | `POST /api/v1/checkout`, `POST /api/v1/shipping/calculate-fee` | `/thanh-toan` (`CheckoutClient.tsx:295-305`) | Preview báo `264.500₫` (ship `35.500₫`), `POST /api/gw/checkout` tạo đơn `NM-20261122-523378D2` với `256.500₫` (ship `27.500₫`) | Lệch `8.000₫–16.000₫` do gửi `from_province_code: ""` (`WEB-B1`); chưa có VNPay/MoMo |
| UC-B07 | Cao | MỘT PHẦN | `services/payment` (`CreatePayment`, SSE) | `GET /api/v1/payments/{id}`, `/events` | `/thanh-toan/cho-thanh-toan/[paymentId]` (`RetryPaymentBox.tsx:152`) | Checkout VietQR → 201 (`hasQrCode: true`), trang chờ HTTP 200 hiện QR + đếm ngược 15p | VietQR + SSE + poll đủ; chưa tích hợp VNPay/MoMo |
| UC-B08 | Cao | ĐỦ | `services/order` (`ListOrders`, `GetOrder`) | `GET /api/v1/orders`, `/orders/{id}` | `/don-hang`, `/don-hang/[id]` (`OrderDetailClient.tsx`) | SSR & Chrome `/don-hang` hiện đủ 2 đơn mới; `/don-hang/d5679bde-...` khớp `CONFIRMED` `256.500₫`; sai ID → 404 | `/tra-cuu-don-hang` cho khách vãng lai vẫn là mock |
| UC-B09 | Trung bình | MỘT PHẦN | `services/order/internal/service/lifecycle.go:691` | `POST /api/v1/orders/{id}/cancel`, `/return` | `/don-hang/[id]` (`OrderDetailClient.tsx:190-283`) | Đã rà code nối thật `cancel` & `return` (`evidence_urls: []`) | Copy storefront hứa 45 ngày trong khi backend khóa 7 ngày (`WEB-D1`); UI chưa hỗ trợ tải ảnh bằng chứng |
| UC-B10 | Trung bình | THIẾU | Chưa có RPC/endpoint review (rà `docs/contracts.md`, `web/src/features/product/PdpReviews.tsx`) | Không có | `PdpReviews.tsx`, `MockStoreContext.tsx:218` | `rg` toàn `web/src`: gửi đánh giá chỉ ghi mảng RAM `MockStore` | Chưa có backend đánh giá sản phẩm |
| UC-B11 | Trung bình | THIẾU | Chưa có OAuth Google / forgot-password (rà `web/src/app/(shop)/dang-nhap/page.tsx`) | Không có | `/dang-nhap` | Kiểm DOM `/dang-nhap`: chỉ có form email + mật khẩu | Thiếu nút Google OAuth và luồng đặt lại mật khẩu |
| UC-B12 | Trung bình | LỖI | Không gọi Search suggest | Không dùng | `ShopHeader.tsx:173`, `web/src/lib/search.ts:208-230` | Đọc nguồn `getInstantSearchSuggestions`: lọc trực tiếp mảng tĩnh `PRODUCTS` từ `@/data` | Không phản ánh sản phẩm mới/giá mới trên gateway (`WEB-B8`) |
| UC-B13 | Thấp | THIẾU | Không có API wishlist/follow shop | Không có | `MockStoreContext.tsx:178-181`, `AccountMockTabs.tsx` | Chrome trang `/` cho khách vãng lai & buyer mới đều hiện `Yêu thích 2` mock | Lưu `localStorage`, tự nhồi sẵn 2 SP mẫu (`WEB-D5`), chưa có theo dõi shop |
| UC-B14 | Trung bình | THIẾU | Chưa có `services/chat` / WebSocket | Không có | `BuyerChatPanel.tsx`, `/seller/chat` | `rg` trong `BuyerChatPanel.tsx`: đọc/ghi `MockStore.chatThreads` | Toàn bộ chat người mua–người bán là mock |
| UC-B15 | Cao | ĐỦ | `services/promotion` (`PreviewDiscounts`, `ListVouchers`, `GetCoinBalance`) | `GET /vouchers`, `GET /coins/balance`, `POST /promotions/preview` | `/gio-hang`, `/thanh-toan` | `POST /api/gw/promotions/preview` → 200 trả đủ breakdown từng shop, voucher, xu | Tổng thanh toán preview bị ảnh hưởng phí ship sai ở `WEB-B1` |
| UC-B16 | Trung bình | LỖI | `services/promotion` (`ListFlashSales`, Redis Lua) | `GET /api/v1/flash-sales?include_upcoming=true` | `/flash-sale` (`FlashSaleClient.tsx:190-760`), `CheckoutClient.tsx:176` | SSR `/flash-sale` 200 khớp 3 khung giờ thật; nhưng khối `Deal sốc` & `Combo` giá giảm giả thêm SKU nguyên giá vào giỏ | Vi phạm giá hiển thị (`WEB-B2`) + lệch `subtotalVnd` mua ngay (`WEB-B9`) |
| UC-B17 | Cao | CHƯA KIỂM | `services/order` (`ConfirmReceived`) | `POST /api/v1/orders/{id}/confirm-received` | `OrderDetailClient.tsx:220-244` | Có nút gọi thật khi `order.status === "DELIVERED"`, nhưng 2 đơn audit mới ở `CONFIRMED`/`PENDING` | Chưa kiểm bấm thật trên đơn `DELIVERED` |
| UC-B18 | Thấp | LỖI | `services/promotion` (`GetCoinBalance`) | `GET /api/v1/coins/balance` | `/tai-khoan`, `/hoi-vien` (`hoi-vien/page.tsx:39`) | Chrome `/hoi-vien` với `Audit Web Buyer` (số dư thật `0 Xu`) hiện `42.000 Xu khả dụng` + `18.500 Xu chờ duyệt` | `/tai-khoan` hiển thị đúng `0 Xu`, `/hoi-vien` hardcode số dư giả (`WEB-B7`) |
| UC-B19 | Trung bình | MỘT PHẦN | `services/user` (`UpdateProfile`) | `PATCH /api/v1/users/me` | `/tai-khoan` (`AccountClient.tsx:257-302`) | Có form sửa họ tên, SĐT, `avatar_url` gọi thật `PATCH /users/me` | Thiếu UI đổi mật khẩu, đổi email kèm OTP, quản lý thiết bị đăng nhập |
| UC-B20 | Cao | ĐỦ | `services/product`, `services/promotion` | `GET /categories`, `/shops`, `/products`, `/flash-sales` | `/`, `/danh-muc` | SSR + Chrome `/` và `/danh-muc` trả 200, khớp 6 danh mục, 4 shop, 18+ SP thật | Thẻ SP còn gắn `originalPrice` & rating mock (`WEB-D2`, `WEB-D3`) |
| UC-B21 | Trung bình | MỘT PHẦN | `services/user` (`GetShopBySlug`), `services/product`, `services/promotion` | `GET /shops/{slug}`, `/products?shop_id=...`, `/vouchers?scope=SHOP` | `/gian-hang/[slug]` (`ShopDetailClient.tsx`) | SSR + Chrome `/gian-hang/xuong-det-bach-dang` trả 200, khớp tên shop, SP, không lộ SĐT | Thiếu KPI shop thật (tỷ lệ phản hồi/giao hàng/ngưỡng freeship là mock `WEB-D3`) |
| UC-B22 | Trung bình | MỘT PHẦN | `services/promotion` (`SaveVoucher`) | `POST /api/v1/vouchers/{id}/save` | `/tai-khoan` tab Ví voucher (`AccountClient.tsx:404-427`) | Đã rà code gọi thật `POST /vouchers/{id}/save` và hiển thị danh sách voucher thật trên `/tai-khoan` | Chưa có nút lưu voucher trên trang chủ/trang gian hàng; chưa kiểm bấm lưu trên tài khoản audit |
| UC-B23 | Trung bình | MỘT PHẦN | `services/notification` | `GET /notifications`, `POST /notifications/read`, `PUT /notifications/preferences` | `/tai-khoan` tab Thông báo (`AccountClient.tsx:439-525`) | SSR `/tai-khoan` tải thật danh sách & số thông báo chưa đọc (`0`); có đủ 3 nhóm × 3 kênh | Chưa có biểu tượng chuông thông báo trên `ShopHeader`; chưa thử bấm lưu cài đặt |
| UC-B24 | Thấp | THIẾU | Không có API báo cáo vi phạm | Không có | Không có trên PDP / gian hàng | `rg` toàn `web/src/app/(shop)`: không có nút hay form báo cáo vi phạm | Chưa cài đặt |
| UC-B25 | Cao | MỘT PHẦN | `services/payment` (`RetryPayment`) | `POST /api/v1/payments/{id}/retry` | `/don-hang/[id]`, `/thanh-toan/cho-thanh-toan/[paymentId]` (`RetryPaymentBox.tsx:230-269`) | Kiểm live hiển thị nút tạo lại VietQR trên payment `3360a7c7-...` | Chỉ retry `VIETQR`, chưa hỗ trợ đổi sang COD hay cổng ví điện tử |
| UC-B26 | Trung bình | THIẾU | Không có endpoint sửa địa chỉ đơn sau khi đặt | Không có | Không có trên `/don-hang/[id]` | Rà `OrderDetailClient.tsx`: không có nút sửa địa chỉ nhận hàng | Chưa cài đặt |
| UC-B27 | Thấp | THIẾU | Không có endpoint tải dữ liệu cá nhân / xóa tài khoản | Không có | Không có trên `/tai-khoan` | Rà `AccountClient.tsx`: không có mục quyền riêng tư / xóa tài khoản | Chưa cài đặt |
| UC-S01 | Cao | MỘT PHẦN | `services/user` (`RegisterShop`, `GetMyShop`) | `POST /api/v1/shops/register`, `GET /api/v1/shops/me` | `/tai-khoan` tab `Mở gian hàng` (`AccountClient.tsx:351-402`) | Đã kiểm render tab & form gọi thật `POST /shops/register` trên buyer mới | Chưa nộp hồ sơ thật để tránh thêm dữ liệu thừa vào hàng đợi admin |
| UC-S02 | Cao | LỖI | `services/product` (`CreateProduct`, `UpdateProduct`) | `GET/POST/PUT /api/v1/seller/products*` | `/seller/san-pham`, `/moi`, `/[id]` (`ProductFormView.tsx:90-94`) | Chrome mở cả 3 route HTTP 200; kiểm `ProductFormView.tsx:90-94` liệt kê `PENDING → HIDDEN` | Chọn `Tạm ẩn` khi SP đang `PENDING` bị backend từ chối 403 (`WEB-B6`) |
| UC-S03 | Cao | MỘT PHẦN | `services/product` (`UpdateSKUStock`) | `PATCH /api/v1/seller/skus/{id}/stock` | `/seller/san-pham/[id]`, cảnh báo trên `/seller` | Chrome `/seller` & `/seller/san-pham/39036630-...` HTTP 200, hiển thị tồn khả dụng thật | Chưa có trang quản lý tồn kho riêng & lịch sử biến động tồn |
| UC-S04 | Cao | MỘT PHẦN | `services/order` (seller lifecycle) | `GET /seller/orders`, `POST /seller/orders/{id}/confirm` | `/seller/don-hang`, `/seller/don-hang/[id]` | Live BFF & Chrome: seller thấy đúng đơn mới `NM-20261122-523378D2` (`200`) | Chưa bấm xác nhận/đóng gói thật trên đơn audit |
| UC-S05 | Trung bình | MỘT PHẦN | `services/promotion` (`CreateShopVoucher`, `ListSellerVouchers`) | `GET/POST /api/v1/seller/vouchers` | `/seller/khuyen-mai` | Chrome `/seller/khuyen-mai` HTTP 200, tải danh sách voucher thật của shop | Cùng trang còn trộn 2 tab mock (Combo & Chiến dịch) |
| UC-S06 | Trung bình | MỘT PHẦN | `services/order` (`ListSellerOrders`) | `GET /api/v1/seller/orders?page=1&page_size=100` | `/seller` (`seller/page.tsx:106-119`) | Chrome `/seller` HTTP 200, tự tổng hợp doanh thu/số đơn từ tối đa 100 đơn | Chưa có chọn khoảng thời gian tùy chọn, tỷ lệ hủy/top SP từ API và xuất CSV |
| UC-S07 | Cao | MỘT PHẦN | `services/product` (`CreateProduct` có `skus`) | `POST /api/v1/seller/products` | `/seller/san-pham/moi` (`ProductFormView.tsx`) | Chrome `/seller/san-pham/moi` HTTP 200, cho thêm nhiều dòng SKU giá/tồn riêng | Chưa sinh tự động bảng tổ hợp 2 nhóm phân loại (màu × size); chưa thử tạo SP thật |
| UC-S08 | Thấp | MỘT PHẦN | `services/promotion` (`RegisterFlashSaleItem`) | `GET /flash-sales`, `POST /seller/flash-sales/{id}/items` | `/seller/khuyen-mai` tab Flash Sale | Đã rà code nối thật API đăng ký suất Flash Sale trên `/seller/khuyen-mai` | Chưa kiểm gửi đăng ký thật để tránh sửa dữ liệu khung giờ seed |
| UC-S09 | Trung bình | LỖI | Chưa có `services/chat` | Không có | `/seller/chat` (`seller/chat/page.tsx:70,294`) | Chrome `/seller/chat` HTTP 200 nhưng 100% mock; sinh link `/seller/don-hang/ord-*` 404 | Mock chat + link hỏng nội bộ (`WEB-B5`) |
| UC-S10 | Trung bình | MỘT PHẦN | `services/order` (xử lý trả hàng) | `POST /seller/orders/{id}/return/*` | `/seller/don-hang/[id]` | Đã rà code hỗ trợ hiển thị & phản hồi đơn `RETURN_REQUESTED` | Chưa có đơn trả hàng thật để kiểm luồng bấm |
| UC-S11 | Thấp | THIẾU | Không có API điểm phạt / hiệu quả gian hàng | Không có | Không có trang thật | Rà `web/src/app/(seller)`: không có route hiệu quả/điểm phạt | Chưa cài đặt |
| UC-S12 | Thấp | MỘT PHẦN | `services/user` (`UpdateShop`) | `PATCH /api/v1/seller/shop` | `/seller/ho-so` | Chrome `/seller/ho-so` HTTP 200, tab Hồ sơ sửa thông tin shop thật | Thiếu trình trang trí banner/danh mục riêng gian hàng; tab Vận hành là mock |
| UC-S13 | Trung bình | THIẾU | Không có API bulk import sản phẩm | Không có | Không có trên `/seller/san-pham` | Rà `web/src/app/(seller)/seller/san-pham/page.tsx`: không có tải file mẫu/import hàng loạt | Chưa cài đặt |
| UC-S14 | Thấp | THIẾU | Không có API quản lý nhân viên shop | Không có | Không có trong `web/src/app/(seller)` | Rà 11 route `(seller)`: không có quản lý nhân viên | Chưa cài đặt |
| UC-S15 | Trung bình | THIẾU | Không có API batch ship/in nhãn hàng loạt | Không có | Không có trên `/seller/don-hang` | Rà `seller/don-hang/page.tsx`: chỉ xử lý từng đơn, không có chọn lô & in nhãn PDF gộp | Chưa cài đặt |
| UC-S16 | Thấp | LỖI | Chưa có API review/reply | Không có | `/seller/tuong-tac` (`seller/tuong-tac/page.tsx:1061,1156,1231`) | Chrome `/seller/tuong-tac` HTTP 200 nhưng 100% mock `sellerStore`; sinh link `/seller/san-pham/prod-*` 404 | Mock hoàn toàn + link hỏng nội bộ (`WEB-B5`) |
| UC-S17 | Trung bình | MỘT PHẦN | `services/payment` (`GetSellerBalance`) | `GET /api/v1/seller/balance` | `/seller/tai-chinh` (`seller/tai-chinh/page.tsx:48-58`) | Chrome `/seller/tai-chinh` HTTP 200, đọc thật số dư khả dụng/chờ | Tài khoản ngân hàng hiển thị là mock tĩnh (`getShopBankAccount`), thiếu khai báo + OTP và sổ giao dịch Payment |
| UC-S18 | Trung bình | THIẾU | Không có API shop direct discount theo SKU | Không có | Tab Chiến dịch trên `/seller/khuyen-mai` là mock (`PromotionsCampaignTab.tsx`) | Rà `PromotionsCampaignTab.tsx`: lưu cục bộ `sellerStore`, không gọi API | Chưa cài đặt backend |
| UC-A01 | Cao | ĐỦ | `services/user` (admin shops) | `GET /admin/shops`, `POST /admin/shops/{id}/approve|reject|suspend` | `/admin/gian-hang` | `GET /api/gw/admin/shops` → 200 (4→5 gian hàng); Chrome `/admin/gian-hang` HTTP 200 | Đã kiểm đọc thật; không bấm duyệt/khóa để giữ nguyên trạng thái stack chung |
| UC-A02 | Cao | MỘT PHẦN | `services/product` (admin products) | `GET /admin/products`, `POST /admin/products/{id}/approve|reject` | `/admin/kiem-duyet` (`AdminProductModerationTab.tsx`) | `GET /api/gw/admin/products` → 200 (`total: 21`, 1 `PENDING`); Chrome hiển thị đúng hàng đợi SP | Tiêu đề SSR bị lệch (`WEB-B10`); 3 tab còn lại là mock |
| UC-A03 | Cao | ĐỦ | `services/user` (admin users) | `GET /admin/users`, lock/unlock/role | `/admin/cau-hinh` (`AdminUsersPanel.tsx`) | Chrome `/admin` & `/admin/cau-hinh` gọi thật `/api/gw/admin/users` → 200, 0 lỗi mạng | Đã kiểm đọc thật; không khóa tài khoản trên stack chung |
| UC-A04 | Trung bình | ĐỦ | `services/product` (admin categories) | `GET/POST/PUT /admin/categories` | `/admin/cau-hinh` (`AdminCategoriesPanel.tsx`) | Chrome `/admin/cau-hinh` tải thật 6 danh mục qua BFF → 200 | Đã kiểm đọc thật; không sửa/xóa danh mục seed |
| UC-A05 | Trung bình | THIẾU | Chưa có API khiếu nại/tranh chấp | Không có | `/admin/khieu-nai` | Chrome `/admin/khieu-nai` hiển thị 5 vụ mẫu từ `MockStore.disputes` (`1.406.000₫`) | 100% mock |
| UC-A06 | Trung bình | MỘT PHẦN | `services/order`, `user`, `product`, `payment` | 6 endpoint `GET /admin/*` | `/admin` (`admin/page.tsx:60-67`) | Chrome `/admin` HTTP 200, tải số liệu thật & biểu đồ 7 ngày | Chỉ lấy 50 đơn gần nhất, chưa có bộ lọc khoảng thời gian tùy ý & xuất báo cáo |
| UC-A07 | Trung bình | MỘT PHẦN | `services/promotion` (admin vouchers, flash sales, coins) | `/admin/vouchers`, `/admin/flash-sales`, `/admin/coins/grant` | `/admin/thanh-vien` (`AdminPromoCoinsPanel.tsx`) | Chrome `/admin/thanh-vien` HTTP 200, nối thật voucher/flash sale/cộng Xu | 2 tab Hội viên & Thẻ quà tặng trên cùng trang là mock `auditStore` |
| UC-A08 | Thấp | THIẾU | Không có API quản lý banner/trang chủ | Không có | `/admin/noi-dung` | Chrome `/admin/noi-dung` sửa `auditStore` cục bộ, không ảnh hưởng `/` | 100% mock |
| UC-A09 | Trung bình | THIẾU | Không có API cấu hình phí sàn theo danh mục | Không có | `/admin/cau-hinh` (tab mock) | Rà `admin/cau-hinh/page.tsx`: cấu hình phí chỉ lưu trong `auditStore` | Chưa có backend |
| UC-A10 | Thấp | THIẾU | Không có API kiểm duyệt đánh giá/phạt vi phạm | Không có | `/admin/kiem-duyet` (3 tab sau) | Rà `admin/kiem-duyet/page.tsx`: đánh giá/Q&A/media dùng `auditStore` | 100% mock |
| UC-A11 | Cao | MỘT PHẦN | `services/user` (`ListAuditLogs`, RBAC) | `GET /api/v1/admin/audit-logs` | `/admin/audit-log`, `AdminUsersPanel.tsx` | `GET /api/gw/admin/audit-logs` → 200; Chrome `/admin/audit-log` hiện 13 bản ghi thật | Chưa có phân quyền chi tiết 4 role nhân viên sàn & duyệt 2 lớp cho thao tác nhạy cảm |
| UC-A12 | Trung bình | THIẾU | Không có API admin cấu hình hãng vận chuyển | Không có | `/admin/cau-hinh` (tab Vận chuyển mock) | Rà `admin/cau-hinh/page.tsx`: lưu cục bộ `auditStore` | Không nối `services/shipping` |
| UC-A13 | Trung bình | THIẾU | Không có API admin cấu hình cổng thanh toán | Không có | `/admin/cau-hinh` (tab Thanh toán mock) | Rà `admin/cau-hinh/page.tsx`: lưu cục bộ `auditStore` | Không nối `services/payment` |
| UC-A14 | Trung bình | MỘT PHẦN | `services/payment` (`GetLedgerSummary`) | `GET /api/v1/admin/ledger/summary` | `/admin/doi-soat` | `GET /api/gw/admin/ledger/summary` → 200 (`is_balanced: true, 256500/256500`); Chrome HTTP 200 | Có xem sổ cái cân bằng thật; thiếu hàng đợi khớp tay chuyển khoản sai số tiền/nội dung |
| UC-A15 | Trung bình | THIẾU | Không có API từ khóa cấm/hàng cấm | Không có | `/admin/kiem-duyet` | Rà `admin/kiem-duyet/page.tsx:49-60`: lưu mock `auditStore` | 100% mock |
| UC-A16 | Thấp | THIẾU | Không có API phát hiện gian lận | Không có | Không có trong `web/src/app/(admin)` | Rà 9 route `(admin)`: không có hàng đợi gian lận | Chưa cài đặt |
| UC-PAY01 | Cao | ĐỦ | `services/payment` (`CreatePayment`, SSE, webhook) | `GET /payments/{id}`, `/events` | `/thanh-toan/cho-thanh-toan/[paymentId]` | Đơn `NM-20261122-494A6C5E` tạo `payment_id: 3360a7c7-...` có QR thật; trang chờ HTTP 200, SSE + đếm ngược | Hoạt động đúng |
| UC-PAY02 | Cao | ĐỦ | `services/order` (COD rule), `services/payment` | `POST /api/v1/checkout` (`payment_method: COD`) | `/thanh-toan` | Checkout COD trả 201, đơn `NM-20261122-523378D2` chuyển thẳng `CONFIRMED` | Phí ship hiển thị trước khi đặt bị lệch (`WEB-B1`) |
| UC-PAY04 | Trung bình | CHƯA KIỂM | `services/payment` (refund) | `POST /payments/{id}/refund` | `/don-hang/[id]`, `/admin/doi-soat` | Chưa có đơn online đã thanh toán bị hủy/trả trên tài khoản audit | Web chưa có form nhập tài khoản ngân hàng nhận hoàn tiền VietQR |
| UC-PAY05 | Thấp | THIẾU | Chưa có endpoint tạo/duyệt payout | Không có | `/seller/tai-chinh` | Rà `seller/tai-chinh/page.tsx`: chỉ đọc `GET /seller/balance`, không có nút rút tiền | Chưa cài đặt |
| UC-PAY08 | Thấp | THIẾU | Không có API xuất PDF biên nhận | Không có | `/don-hang/[id]`, `/seller/don-hang/[id]` | Rà `OrderDetailClient.tsx` & `seller/don-hang/[id]/page.tsx`: không có nút tải biên nhận | Chưa cài đặt |
| UC-SHIP01 | Cao | ĐỦ | `services/shipping` (34 tỉnh), `services/user` (addresses) | `GET /shipping/provinces*`, `CRUD /users/me/addresses` | `AddressForm.tsx`, `/tai-khoan`, `/thanh-toan` | `GET /api/gw/shipping/provinces` → 200 (34 tỉnh); `POST /api/gw/users/me/addresses` → 201 (`2226fc85-...`) | Sổ địa chỉ 2 cấp hoạt động đầy đủ |
| UC-SHIP02 | Cao | LỖI | `services/shipping`, `services/gateway/internal/router/handlers_shipping.go:179-205` | `POST /api/v1/shipping/calculate-fee` | `/thanh-toan` (`CheckoutClient.tsx:295-305`), `/gio-hang` (`gio-hang/page.tsx:183-194`) | Web gửi `from_province_code: ""` → phí `35.500₫` thay vì `27.500₫` (HN) hoặc `19.500₫` (HP) | Lệch phí vận chuyển thực tế (`WEB-B1`) |
| UC-SHIP03 | Cao | CHƯA KIỂM | `services/shipping` (`CreateShipment`) | `POST /api/v1/shipping/shipments` | `/seller/don-hang/[id]` | Đã kiểm mở trang đơn `NM-20261122-523378D2` (HTTP 200), thấy nút thao tác tạo vận đơn | Chưa bấm tạo vận đơn thật trên đơn audit |
| UC-SHIP06 | Trung bình | THIẾU | Không có API cấu hình vận chuyển gian hàng | Không có | `/seller/ho-so` (tab Vận hành mock) | Rà `seller/ho-so/page.tsx`: lưu vào `sellerStore` cục bộ | Chưa nối backend |
| UC-AI01 | Cao | THIẾU | Chưa có `services/ai` / pgvector | Không có | `AiAssistantPanel.tsx` | Rà `AiAssistantPanel.tsx`: rule từ khóa client-side, không gọi LLM/RAG | Chưa cài đặt |
| UC-AI02 | Cao | THIẾU | Chưa có tìm kiếm ngữ nghĩa | Không có | `/tim-kiem` | Chỉ gọi `GET /products?q=...` | Chưa cài đặt |
| UC-AI03 | Trung bình | LỖI | Không gọi `services/order` | Không có | `AiAssistantPanel.tsx:353, 441-443, 507-514` | Hứa đọc đơn thật của tài khoản nhưng đọc `INITIAL_ORDERS` mock & gắn link `/don-hang/ord-109` 404 | Sai cam kết dữ liệu + link hỏng (`WEB-B5`) |
| UC-AI04 | Trung bình | THIẾU | Chưa có tool calling thêm giỏ | Không có | `AiAssistantPanel.tsx` | Không có nút/tool thêm vào giỏ thật | Chưa cài đặt |
| UC-AI05 | Trung bình | THIẾU | Chưa có job tóm tắt đánh giá LLM | Không có | `PdpReviews.tsx` | Tóm tắt hiển thị là text mock tĩnh | Chưa cài đặt |
| UC-AI06 | Thấp | THIẾU | Chưa có AI sinh mô tả | Không có | `ProductFormView.tsx` | Không có nút sinh mô tả AI | Chưa cài đặt |
| UC-AI07 | Thấp | THIẾU | Chưa có gợi ý embedding cá nhân hóa | Không có | `/`, `/san-pham/[slug]`, `/gio-hang` | Cắt danh sách SP cơ bản/tĩnh | Chưa cài đặt |
| UC-AI08 | Thấp | THIẾU | Chưa có tìm kiếm ảnh | Không có | `ShopHeader.tsx`, `/tim-kiem` | Không có nút tải ảnh tìm kiếm | Chưa cài đặt |
| UC-AI09 | Thấp | THIẾU | Chưa có AI báo cáo cho seller | Không có | `web/src/app/(seller)` | Không có widget AI cho người bán | Chưa cài đặt |
| UC-AI10 | Trung bình | THIẾU | Chưa có AI gợi ý trả lời chat | Không có | `/seller/chat` | Câu trả lời nhanh là mảng mẫu tĩnh | Chưa cài đặt |
| UC-AI11 | Trung bình | THIẾU | Chưa có RAG chính sách | Không có | `AiAssistantPanel.tsx:132` | Rule tĩnh + trích sai chính sách 45 ngày (`WEB-D1`) | Chưa cài đặt |
| UC-AI12 | Trung bình | THIẾU | Chưa có chuyển tiếp nhân viên & ticket thật | Không có | `AiAssistantPanel.tsx`, `/tro-giup` | Ticket lưu ở `MockStore` RAM | Chưa cài đặt |

**Tổng hợp 82 UC có giao diện web**: **ĐỦ: 11** · **MỘT PHẦN: 23** · **LỖI: 11** · **THIẾU: 34** · **CHƯA KIỂM: 3**.

---

## 3. Kiểm kê nguồn dữ liệu mẫu (`mock`/`data`) & Đối chiếu phạm vi đặc tả

### 3.1. Phân loại nguồn dữ liệu mẫu còn trong `web/src/`

Kết quả `rg -l 'from "@/mock|from "@/data|sellerStore|auditStore'`: **75 file** trong `web/src/{app,features,components,context,lib}` có import từ `@/mock`, `@/data`, `sellerStore`, `auditStore`. Kiểm tra `localStorage`: chỉ có 2 dòng tại `web/src/mock/MockStoreContext.tsx:241, 288` (lưu `bodyProfile`, `wishlist`, `recentlyViewed`, `backInStockAlerts`, `cookiePrefs`, `membership` mẫu — **không** lưu token, giỏ hàng hay đơn hàng thật).

Phân tách 3 nhóm rõ ràng:
1. **Dữ liệu tĩnh hợp lệ (nội dung biên tập/tài liệu)**: `/ve-nexusmart`, `/tap-chi`, `/tap-chi/[slug]`, `/bo-suu-tap`, `/chien-dich/[slug]`, bảng chất liệu GSM, hướng dẫn chọn size theo dáng người (`SizeFinder.tsx`). (Cần sửa các đoạn copy đổi trả 45 ngày cho khớp 7 ngày.)
2. **Tính năng mẫu có gắn nhãn rõ**: `PdpReviews.tsx` & `PdpQuestions.tsx` (có `SAMPLE_DATA_NOTICE`), tab tổng kết năm trong `AccountMockTabs.tsx:92-94`, `/chien-dich/soi-xanh-nexus`.
3. **Số liệu giả đóng vai dữ liệu thật (nguy hiểm cho nghiệp vụ)**:
   - `/hoi-vien` (`hoi-vien/page.tsx:39, 176-237`): hiển thị `42.000 Xu khả dụng`, `18.500 Xu chờ duyệt`, `Hạng Bạc`, `2.350.000₫` ngay cạnh tên tài khoản thật `Audit Web Buyer` (số dư thật `0 Xu`).
   - `/flash-sale` (`FlashSaleClient.tsx:190-760`): khối `Deal sốc` & `Combo` hiện giá giảm ảo nhưng thêm SKU thật với nguyên giá gốc.
   - `/san-pham/[slug]` (`ProductDetailClient.tsx:246-899`): hiện giá chiết khấu theo bậc (`10%`, `15%`), quà tặng `499.000₫`, chọn size từng món combo không áp dụng ở checkout; bộ dự tính ship `provinces.ts` hiện `SPX/GHN` & freeship giả.
   - `ProductCard.tsx:61-75` & `catalog-mapper.ts:121-155, 307-310`: điểm sao `4,7–4,9` giả, giá gốc gạch ngang `originalPrice` giả, chỉ số vận hành shop giả gắn lên sản phẩm/gian hàng thật.
   - `AiAssistantPanel.tsx:353`: trả lời 7 đơn hàng giả (`INITIAL_ORDERS`) dù tuyên bố đọc đơn của tài khoản đang đăng nhập.
   - `MockStoreContext.tsx:178-206`: tự nhồi sẵn `Yêu thích 2` trên header và 1 phiếu hỗ trợ của `DEMO_USER` (`Nguyễn Minh Quân`, đơn `NM-261117-082`) trên `/tro-giup` cho mọi người dùng mới.
   - `SellerShell.tsx:51-54` (`Tương tác 5`), `AdminShell.tsx:109` (`Khiếu nại mở 5`), `/admin/khieu-nai` (`tạm giữ 1.406.000₫`): trộn số liệu mock cạnh số thật.

### 3.2. Bảng đối chiếu tính năng ngoài phạm vi đặc tả đang xuất hiện trên Web

| Nghiệp vụ trên Web | UC hoặc ngoài phạm vi (`docs/spec.md`) | Trạng thái | Ghi chú |
| --- | --- | --- | --- |
| Combo mua bộ & mua kèm giảm giá (`/flash-sale`, `/seller/khuyen-mai`) | **Ngoài phạm vi** (`docs/spec.md:29`: "Combo, mua kèm") | Đang render mock gây sai giá (`WEB-B2`) | Bấm thêm giỏ đẩy SKU thật nguyên giá; nên gỡ khỏi luồng mua thật |
| Chương trình hội viên theo hạng (`Thành viên/Bạc/Vàng/Bạch Kim`, `/hoi-vien`, `/admin/thanh-vien`) | **Ngoài phạm vi** (`docs/spec.md:29`: "chương trình thành viên theo hạng") | Đang render số dư/hạng giả (`WEB-B7`) | Mâu thuẫn trực tiếp với số dư Xu thật `GET /coins/balance` |
| Giảm giá theo bậc số lượng (`Mua 2 giảm 10%`, `Mua 3 giảm 15%`) & Quà tặng ngưỡng `499k` (PDP) | **Ngoài phạm vi** (`docs/spec.md:29, 39`: không có trong 100 UC) | Đang render giả trên PDP (`WEB-B3`) | Gây hiểu nhầm giá mua trước khi vào giỏ |
| Thẻ quà tặng & sổ cái thẻ quà tặng (`/the-qua-tang`, `/admin/thanh-vien`) | **Ngoài phạm vi** (`docs/spec.md:39`: không có trong 100 UC) | Mock không tác dụng (`WEB-B7`) | Áp mã đẩy sang `/gio-hang` trống |
| Xuất hóa đơn VAT điện tử (`BuyerChatPanel.tsx:44`, `OrderLookupView.tsx:291`) | **Ngoài phạm vi** (`docs/spec.md:27`: "hoá đơn điện tử") | Còn copy quảng cáo (`WEB-D6`) | Cần xóa khỏi copy trợ giúp/chat |
| Đổi trả 45 ngày, đổi size 2 chiều tận nơi, bù lẻ tất | Trái **UC-B09** (`docs/spec.md:82`: đổi trả 7 ngày; `lifecycle.go:691`) | Phủ khắp 25+ file (`WEB-D1`) | Copy thừa từ mẫu tham chiếu Coolmate |

---

## 4. Danh sách lỗi (`WEB-B1`..`WEB-B10`)

### `WEB-B1` [P1] Lệch phí vận chuyển & tổng tiền giữa `/thanh-toan` (`/gio-hang`) và đơn hàng tạo thật
- **Tái hiện**:
  ```bash
  # 1) BFF /thanh-toan gọi calculate-fee với from_province_code rỗng (giống CheckoutClient.tsx:295-305)
  curl -sS -X POST http://localhost:43000/api/gw/shipping/calculate-fee \
    -H "Origin: http://localhost:43000" -H "Content-Type: application/json" -b "$COOKIE_JAR" \
    -d '{"shop_id":"61a74907-4b71-43d5-bb39-b2df456281ae","from_province_code":"","from_ward_code":"","to_province_code":"01","to_ward_code":"00001","weight_grams":250,"cod_amount_vnd":229000}'
  # -> mock_express STANDARD: fee_vnd = 35500 (tổng preview = 229000 + 35500 = 264500)

  # 2) So sánh khi truyền đúng kho shop (Hải Phòng = "31") hoặc gọi POST /api/gw/checkout
  curl -sS -X POST http://localhost:48080/api/v1/shipping/calculate-fee \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"shop_id":"61a74907-4b71-43d5-bb39-b2df456281ae","from_province_code":"31","from_ward_code":"","to_province_code":"01","to_ward_code":"00001","weight_grams":250,"cod_amount_vnd":229000}'
  # -> mock_express STANDARD: fee_vnd = 27500 (đơn NM-20261122-523378D2 tạo ra total_payable_vnd = 256500)
  ```
- **Mong đợi vs Thật**: Phí vận chuyển và tổng thanh toán hiển thị ở `/thanh-toan` phải khớp chính xác số tiền của đơn tạo ra (`27.500₫` → `256.500₫`). Thực tế web hiển thị **`35.500₫` → `264.500₫`** (lệch **`8.000₫`** cho người mua Hà Nội `01`, lệch **`16.000₫`** cho người mua Hải Phòng `31`: `35.500₫` vs `19.500₫`). Kéo theo `promotions/preview` tính sai mức freeship và số Xu trừ.
- **Gốc nghi ngờ**: `web/src/app/(shop)/thanh-toan/CheckoutClient.tsx:295-305`, `web/src/app/(shop)/gio-hang/page.tsx:183-194` gửi cứng `from_province_code: ""`; `services/gateway/internal/router/handlers_shipping.go:179-205` không tra mã tỉnh kho từ `shop_id`.

### `WEB-B2` [P1] `/flash-sale` trưng bày giá giảm giả ở mục "Deal sốc" & "Combo", nhưng thêm SKU thật nguyên giá vào giỏ
- **Tái hiện**: Mở `http://localhost:43000/flash-sale` → kéo xuống khối `Deal sốc hôm nay` hoặc `Combo tiết kiệm` (không có cảnh báo dữ liệu mẫu) → bấm `Thêm giỏ` / `Thêm trọn bộ` → mở `/gio-hang`.
- **Mong đợi vs Thật**: Giá trong giỏ/checkout phải khớp giá quảng cáo trên nút bấm (hoặc gỡ bỏ khối Combo/Deal sốc chưa có backend). Thực tế `handleQuickAddDeal` và `handleAddCombo` gọi `addToCart({ skuId: targetSku.id, quantity: 1 })` lấy thẳng SKU thật, nên giỏ hàng và thanh toán tính **100% giá gốc**, không có mức giảm nào.
- **Gốc nghi ngờ**: `web/src/app/(shop)/flash-sale/FlashSaleClient.tsx:190-263, 548-760`.

### `WEB-B3` [P1] Trang sản phẩm `/san-pham/[slug]` hứa giảm giá theo bậc (`Mua 2 giảm 10%`, `Mua 3 giảm 15%`), quà `499k` và chọn size combo nhưng checkout thu nguyên giá
- **Tái hiện**: Mở `http://localhost:43000/san-pham/combo-3-quan-lot-boxer-modal` (đã xác nhận SSR chứa cả 3 khối hứa ưu đãi) → tăng số lượng lên 2 hoặc 3 → PDP hiển thị `"Đang áp giảm 10%"` và đơn giá đã giảm → bấm `Thêm vào giỏ` / `Mua ngay`.
- **Mong đợi vs Thật**: Số tiền giỏ hàng/checkout phải khớp giá đã tính trên PDP; nếu chọn size từng món combo (`comboSizes`) thì giỏ phải phản ánh đúng cỡ đã chọn. Thực tế `handleAddToCart`/`handleBuyNow` chỉ gửi `{ skuId, quantity }` (bỏ qua `comboSizes`), không gửi/không có chiết khấu bậc hay quà tặng ở backend nên khách bị tính nguyên giá gốc.
- **Gốc nghi ngờ**: `web/src/app/(shop)/san-pham/[slug]/ProductDetailClient.tsx:246-259, 361-403, 659-703, 818-899`.

### `WEB-B4` [P2] `/dang-nhap` không chuyển hướng theo `user.role` (vi phạm UC-B02 bước 4)
- **Tái hiện**:
  1. Mở trình duyệt sạch vào `http://localhost:43000/dang-nhap` (không có `?next=`).
  2. Nhập `seller.bachdang@nexusmart.local` / `SellerDevPassword123!` (hoặc `admin@nexusmart.local` / `AdminDevPassword123!`) và bấm `Đăng nhập`.
- **Mong đợi vs Thật**: Theo UC-B02 bước 4 (`docs/spec.md:75`), `seller` phải chuyển sang `/seller`, `admin`/`super_admin` chuyển sang `/admin`. Kết quả kiểm thật bằng Chrome headless: `loginRedirects = { buyerNoNext: "/", sellerNoNext: "/", adminNoNext: "/" }`. Ngoài ra khi đã đăng nhập mở lại `/dang-nhap`, nút dẫn ở dòng 202–218 chỉ trỏ `/tai-khoan`.
- **Gốc nghi ngờ**: `web/src/app/(shop)/dang-nhap/page.tsx:38-39, 83-88, 202-218`.

### `WEB-B5` [P2] Liên kết nội bộ trỏ tới ID mock gây 404 thật ở `AiAssistantPanel`, `/seller/chat`, `/seller/tuong-tac`
- **Tái hiện**:
  ```bash
  # Link do AiAssistantPanel tạo ra khi buyer hỏi "Đơn hàng của tôi ở đâu?"
  curl -sI -b "$BUYER_COOKIE" http://localhost:43000/don-hang/ord-109 | head -n 1
  # -> HTTP/1.1 404 Not Found
  ```
  Tương tự: seller mở `/seller/chat` bấm link đơn hàng (`/seller/don-hang/ord-*`) hoặc mở `/seller/tuong-tac` bấm link sản phẩm (`/seller/san-pham/prod-*`) đều rơi vào màn hình không tìm thấy (backend 404).
- **Mong đợi vs Thật**: `AiAssistantPanel` tự ghi `"chỉ đọc số đo và đơn hàng thuộc tài khoản đang đăng nhập"` nhưng trả về 7 đơn mock từ `INITIAL_ORDERS` với link 404; không được sinh link tới ID giả không tồn tại trên backend.
- **Gốc nghi ngờ**: `web/src/features/commerce/AiAssistantPanel.tsx:7, 353, 441-443, 507-514`; `web/src/app/(seller)/seller/chat/page.tsx:70, 294`; `web/src/app/(seller)/seller/tuong-tac/page.tsx:1061, 1156, 1231`.

### `WEB-B6` [P2] `/seller/san-pham/[id]` cho phép chọn chuyển `PENDING → HIDDEN` gây lỗi 403 từ backend
- **Tái hiện**: Mở `/seller/san-pham/<product_id>` của sản phẩm có trạng thái `PENDING` → ô chọn trạng thái hiển thị tùy chọn `Tạm ẩn` (`HIDDEN`) → bấm lưu (`PUT /api/gw/seller/products/<id>`).
- **Mong đợi vs Thật**: Theo `docs/contracts.md:163`, seller không được chuyển trực tiếp `PENDING → HIDDEN` (backend trả `403 PERMISSION_DENIED`), nên UI phải ẩn/khóa tùy chọn này khi `currentStatus === "PENDING"`. Thực tế dropdown vẫn cho chọn.
- **Gốc nghi ngờ**: `web/src/features/seller/ProductFormView.tsx:90-94`.

### `WEB-B7` [P2] `/hoi-vien` và `/the-qua-tang` hiển thị số dư/hạng giả cạnh tên tài khoản thật & chuyển hướng vô hiệu về `/gio-hang`
- **Tái hiện**: Đăng nhập buyer mới `audit-web-605262@nexusmart.local` (có `GET /api/gw/coins/balance` → `balance: 0`) → mở `http://localhost:43000/hoi-vien` bằng Chrome headless.
- **Mong đợi vs Thật**: Nếu chưa có backend hội viên/thẻ quà tặng phải tách biệt hoặc gỡ bỏ; số dư Xu phải lấy từ `GET /coins/balance` (`0 Xu`). Thực tế Chrome render ngay trên thẻ hero không có nhãn mẫu: `Hạng Bạc · Hội viên Nexus · Audit Web Buyer · Chi tiêu 4 quý: 2.350.000₫ · 42.000 Xu khả dụng · 18.500 Xu chờ duyệt · 6.000 Xu sắp hết hạn`. Bấm `"Nhận quà sinh nhật vào giỏ"` (cả ở `/hoi-vien` và `/tai-khoan`) hoặc áp mã ở `/the-qua-tang` đều chuyển sang `/gio-hang` mà không thêm quà/không áp giảm giá.
- **Gốc nghi ngờ**: `web/src/app/(shop)/hoi-vien/page.tsx:7, 39, 176-237, 429-431`; `web/src/app/(shop)/the-qua-tang/page.tsx:63-67, 356, 416-419`; `web/src/features/account/AccountMockTabs.tsx:31-86`.

### `WEB-B8` [P2] Tìm kiếm tức thì trên `ShopHeader` và so khớp gian hàng trong `search.ts` dùng dữ liệu tĩnh `@/data`
- **Tái hiện**: Gõ tên sản phẩm mới tạo trên gateway (ví dụ `AUDIT-ORD`) vào ô tìm kiếm trên `ShopHeader` (gợi ý xổ xuống), hoặc tìm theo tên xưởng dệt trên `/tim-kiem`.
- **Mong đợi vs Thật**: Gợi ý tìm kiếm phải phản ánh danh mục đang bán thật trên gateway; tìm theo tên xưởng phải khớp `shopId` UUID thật. Thực tế `ShopHeader` lọc mảng tĩnh `PRODUCTS` (chỉ có 18 SP cứng lúc build), còn `buildProductSearchText` so `product.shopId` (UUID thật) với `SHOPS` trong `@/data` (`shop-bach-dang`) nên luôn trả `undefined`.
- **Gốc nghi ngờ**: `web/src/components/ShopHeader.tsx:173`; `web/src/lib/search.ts:82, 208-230`.

### `WEB-B9` [P2] Mua ngay Flash Sale (`?sku=&qty=&flash=`) tính `subtotalVnd` theo giá thường ở `CheckoutClient`
- **Tái hiện**: Bấm mua ngay một mặt hàng đang mở bán ở `/flash-sale` để mở `/thanh-toan?sku=<sku>&qty=1&flash=<flash_item_id>`.
- **Mong đợi vs Thật**: `slice.subtotalVnd` nên phản ánh giá sau giảm flash sale (hoặc đồng bộ từ `promotions/preview`) trước khi truyền `cod_amount_vnd` sang `POST /shipping/calculate-fee` và trước khi kiểm tra điều kiện `min_order_vnd` của voucher gian hàng phía client. Thực tế `CheckoutClient` nhân `unitPriceVnd * qty` (giá gốc) làm sai lệch `cod_amount_vnd` gửi cho hãng vận chuyển và cho chọn nhầm voucher shop không đủ ngưỡng.
- **Gốc nghi ngờ**: `web/src/app/(shop)/thanh-toan/CheckoutClient.tsx:176, 304, 511`.

### `WEB-B10` [P3] Soft-404 (HTTP 200) ở 3 route động tĩnh, lệch tiêu đề SSR `/admin/kiem-duyet` & form bản tin giả ở footer
- **Tái hiện**:
  ```bash
  for p in /bo-suu-tap/khong-ton-tai /tap-chi/khong-ton-tai /chien-dich/khong-ton-tai; do
    echo "$p -> $(curl -s -o /dev/null -w '%{http_code}' http://localhost:43000$p)"
  done
  # Kết quả: cả 3 trả 200 (trong khi /san-pham/khong-ton-tai-xyz và /gian-hang/khong-ton-tai-xyz trả 404)
  ```
- **Mong đợi vs Thật**: Slug không tồn tại phải gọi `notFound()` trả HTTP 404. Ngoài ra SSR của `/admin/kiem-duyet` trả `<h1>Kiểm duyệt đánh giá, hỏi đáp & media</h1>` trong khi màn hình chính là `"Kiểm duyệt sản phẩm & nội dung"`; form đăng ký bản tin ở `ShopFooter.tsx:62-72` báo thành công giả trong state cục bộ không gọi API.
- **Gốc nghi ngờ**: `web/src/app/(shop)/{bo-suu-tap,tap-chi,chien-dich}/[slug]/page.tsx`; `web/src/app/(admin)/admin/kiem-duyet/page.tsx:1200, 1215`; `web/src/components/ShopFooter.tsx:62-72`.

---

## 5. Vấn đề dữ liệu & hiển thị (`WEB-D1`..`WEB-D7`)

- **`WEB-D1` [Chính sách sai lệch]**: Toàn bộ storefront, seller và admin đang quảng cáo **"Đổi trả miễn phí 45 ngày, bưu tá đổi size 2 chiều tận nơi, bù lẻ tất"** (>25 vị trí: `ShopHeader.tsx:298`, `ShopFooter.tsx:87,190`, `HomeClient.tsx:120,166`, `ProductDetailClient.tsx:1099`, `dang-nhap/page.tsx:23`, `dang-ky/page.tsx:278`, `ve-nexusmart/page.tsx:111,279`, `tro-giup/page.tsx`, `OrderLookupView.tsx:296,421`, `AiAssistantPanel.tsx:132`, `BuyerChatPanel.tsx:28,44`, `seller/ho-so/page.tsx`, `admin/cau-hinh/page.tsx`), trái ngược hoàn toàn với spec UC-B09 và `services/order/internal/service/lifecycle.go:691` (**khóa cứng 7 ngày**).
- **`WEB-D2` [Giá gạch & % giảm ảo]**: `web/src/lib/catalog-mapper.ts:307-310, 504-505` lấy `originalPrice` tĩnh từ `productEditorial.ts` (backend chưa có trường giá gốc) để hiển thị giá gạch ngang trên thẻ sản phẩm và phóng đại `% giảm` ở Flash Sale (`Math.max(sku.price, product.originalPrice ?? 0)`).
- **`WEB-D3` [Số liệu đánh giá, KPI shop & dự tính vận chuyển giả không nhãn]**:
  - `ProductCard.tsx:61-75, 156-172, 442-452` tự sinh sao `4,7–4,9` (12–47 đánh giá) cho mọi sản phẩm thật; chữ "dữ liệu mẫu" ẩn trong `title` hover.
  - `catalog-mapper.ts:121-155` & `ShopDetailClient.tsx` gắn `responseTime`, `fulfillmentRate`, `freeShipThreshold` giả cho 4 shop thật.
  - `ProductDetailClient.tsx:269-277, 1059-1082` dùng `estimateDelivery()` tĩnh (`provinces.ts`), hiển thị hãng `SPX/GHN` và freeship tự động `199k`/`249k` không khớp `POST /shipping/calculate-fee` (`Mock Express`/`GHN`).
- **`WEB-D4` [Cam kết khuyến mãi không có thật]**:
  - Thanh thông báo đầu mọi trang shop (`ShopHeader.tsx`): `"Miễn phí vận chuyển toàn sàn từ 249.000₫ (gian hàng từ 199.000₫)"` — thử thực tế đơn `229.000₫` của `Xưởng Dệt Bạch Đằng` vẫn tính `27.500₫` phí ship (chỉ được freeship khi áp voucher `FREESHIP`).
  - `/dang-ky` (`dang-ky/page.tsx:248, 278, 397`): hứa `"nhận ngay 15.000 Xu"` khi đăng ký; kiểm thực tế tài khoản mới có `0 Xu`.
- **`WEB-D5` [Dữ liệu khởi tạo mock trộn vào giao diện người dùng thật]**:
  - `MockStoreContext.tsx:178-181`: khách và tài khoản mới đều thấy sẵn `Yêu thích 2` trên header.
  - `MockStoreContext.tsx:190-206` → `tro-giup/page.tsx:696-733`: mọi khách và tài khoản mới đều thấy 1 phiếu hỗ trợ của `DEMO_USER` (`tk-261119-01`, đơn `NM-261117-082`) trong mục `"Phiếu hỗ trợ đã gửi (1)"`.
  - `SellerShell.tsx:51-54` (`Tương tác 5`) & `AdminShell.tsx:109` (`Khiếu nại mở 5`): trộn badge mock cùng hàng với badge thật (`Đơn chờ duyệt 2`, `Kiểm duyệt mở 1`).
- **`WEB-D6` [Copy hứa tính năng ngoài phạm vi]**: `BuyerChatPanel.tsx:44` & `OrderLookupView.tsx:291` hứa xuất `"hóa đơn VAT điện tử"` (`docs/spec.md:27` ghi ngoài phạm vi); các khối Combo/Hội viên hạng/Thẻ quà tặng vượt phạm vi `docs/spec.md:29, 39`.
- **`WEB-D7` [Kết quả kiểm tra hiển thị đạt chuẩn & lưu ý nhỏ]**:
  - **Đạt**: 46/46 ảnh tĩnh (`/products/*`, `/models/*`, `/banners/*`, `/atmosphere/*`) trả `HTTP 200`; `web/next.config.ts` đặt `images: { unoptimized: true }` nên `/_next/image` trả 404 là đúng cấu hình (trình duyệt tải ảnh trực tiếp, 0 request lỗi media trên 45/45 route). Tiền tệ dùng `int64` VND (`formatVnd`), không số thực. Giao diện `(shop)` dùng tiếng Việt tự nhiên, không lộ thuật ngữ kỹ thuật (`JWT`, `HMAC`, `Saga`, `Redis Lua`, `optimistic locking`, `OpenTelemetry`, `IDEM-`) hay chuỗi lỗi gateway thô. 0 lỗi `console.error`, 0 tràn ngang tại 1440px và 390px.
  - **Lưu ý nhỏ**: Còn cụm `"tồn kho SKU"` ở 4 trang nội bộ `(seller)`/`(admin)` (`seller/chat/page.tsx:28`, `seller/san-pham/page.tsx:224,282`, `seller/don-hang/[id]/page.tsx:183,418`, `admin/cau-hinh/page.tsx:443,1194`). Trang `/seller/don-hang/[id]` với đơn `CONFIRMED` chưa tạo vận đơn phát sinh 1 request `404` tới `/api/gw/shipping/shipments/{id}` (UI bắt lỗi đúng). `GET /api/v1/flash-sales?include_upcoming=true` không trả trường `status` trên slot (web tự suy ra đúng theo `starts_at`/`ends_at`).

---

## 6. Đề xuất sửa (`WEB-F1`..`WEB-F8`)

| Mã | Giải quyết | Việc cụ thể (file, endpoint, route) | Cỡ | Đổi proto / migration / ADR? |
| --- | --- | --- | --- | --- |
| `WEB-F1` | `WEB-B1`, `UC-B06`, `UC-SHIP02` | Sửa `services/gateway/internal/router/handlers_shipping.go` (tự tra `pickup_address` từ `shop_id` khi `from_province_code == ""` bằng `UserService.GetShop`) và/hoặc truyền `pickup_province_code` từ `CheckoutClient.tsx:295-305` & `gio-hang/page.tsx:183-194`; bổ sung test đối chiếu phí `calculate-fee` == phí đơn `checkout`. | S | Không (tận dụng gRPC `GetShop` sẵn có) |
| `WEB-F2` | `WEB-B2`, `WEB-B3`, `WEB-B9`, `UC-B04`, `UC-B16` | Gỡ bỏ khối `Deal sốc` & `Combo` mock khỏi `/flash-sale` (`FlashSaleClient.tsx`); gỡ bỏ giảm giá bậc `10%/15%`, quà ngưỡng `499k`, chọn size combo và dự tính ship tĩnh `provinces.ts` khỏi `ProductDetailClient.tsx`; sửa `CheckoutClient.tsx:176,304,511` dùng `flashPriceVnd` cho luồng mua ngay Flash sale. | M | Không |
| `WEB-F3` | `WEB-B4`, `UC-B02` | Trong `web/src/app/(shop)/dang-nhap/page.tsx:38-88`, chỉ ưu tiên `?next=` khi query param có mặt; khi không có `?next=`, chuyển hướng theo `loggedInUser.role` (`admin`/`super_admin` → `/admin`, `seller` → `/seller`, `buyer` → `/`); sửa banner đã đăng nhập trỏ đúng cổng theo role. | S | Không |
| `WEB-F4` | `WEB-B7`, `WEB-D4`, `WEB-D5`, `WEB-D6`, `UC-B18` | Sửa `/hoi-vien` đọc số dư Xu thật từ `GET /coins/balance` (hoặc ẩn/gắn nhãn rõ khu vực ngoài phạm vi hạng thành viên & `/the-qua-tang`, gỡ nút chuyển hướng vô hiệu về `/gio-hang` và mã giả `SINHNHAT50`/`NEXUSVIP12`); đặt `INITIAL_WISHLIST = []` và `INITIAL_SUPPORT_TICKETS = []` trong `MockStoreContext.tsx`; tách rõ hoặc ẩn badge mock trên `SellerShell`/`AdminShell`. | M | Không |
| `WEB-F5` | `WEB-B5`, `UC-AI03`, `UC-S09`, `UC-S16` | Trong `AiAssistantPanel.tsx`, khi chưa có `services/ai`, hoặc gọi `GET /api/gw/orders?page_size=5` thật cho buyer đã đăng nhập (dùng ID đơn thật), hoặc tắt chế độ tra đơn giả & gắn nhãn rõ; tại `/seller/chat` và `/seller/tuong-tac`, không render `<Link>` tới ID mock `ord-*`/`prod-*`. | S | Không |
| `WEB-F6` | `WEB-B6`, `WEB-B8`, `WEB-B10`, `UC-S02`, `UC-B12` | Loại tùy chọn `HIDDEN` khi `currentStatus === "PENDING"` trong `ProductFormView.tsx:90-94`; đổi `ShopHeader` gợi ý tìm kiếm sang gọi debounced `GET /api/gw/products?q=...&page_size=6` và truyền danh sách shop thật vào `buildProductSearchText` (`search.ts:82`); gọi `notFound()` khi sai slug ở 3 route `/bo-suu-tap/[slug]`, `/tap-chi/[slug]`, `/chien-dich/[slug]`; sửa tiêu đề SSR `/admin/kiem-duyet`. | M | Không |
| `WEB-F7` | `WEB-D1`, `WEB-D2`, `WEB-D3`, `WEB-D4` | Đồng bộ toàn bộ copy đổi trả từ **45 ngày** sang **7 ngày** (khớp UC-B09 & `lifecycle.go:691`); bỏ hứa `nhận ngay 15.000 Xu` ở `/dang-ky`, bỏ hứa `hóa đơn VAT điện tử`, sửa banner freeship theo điều kiện voucher thật; ẩn sao đánh giá giả trên `ProductCard`, bỏ `originalPrice` mock và KPI shop giả cho đến khi có dữ liệu thật từ backend. | M | Không |
| `WEB-F8` | `UC-B10`, `UC-B14`, `UC-A05`, `UC-AI01..12` | Khi triển khai các năng lực backend còn thiếu (Search/ES, Chat WebSocket, AI/RAG/pgvector, Review, Dispute), nối trực tiếp thay thế các tab/panel mock còn lại. Riêng hạ tầng mới (Search engine / LLM / object storage ảnh) cần chạy đánh giá OSS và duyệt ADR theo `AGENTS.md` luật 11 trước khi code. | L | Có (proto + migration + ADR cho hạ tầng mới) |

---

## 7. Đã kiểm gì, chưa kiểm gì

### 7.1. Đã kiểm (có bằng chứng chạy thật)
- **Đủ 45/45 route giao diện + 8 BFF route**: gọi HTTP thật qua `curl`/Node (`44 × 200`, `1 × 307` cho `/tra-cuu-don` → `/tra-cuu-don-hang`) và mở lại toàn bộ trên **Google Chrome Headless (`155.0.8059.39`)** qua CDP ở 1440×900 (và kiểm tràn ngang 390px trên 6 trang đại diện: `/`, `/danh-muc`, `/san-pham/...`, `/flash-sale`, `/admin`, `/admin/doi-soat`). Kết quả: 0 lỗi `console.error`, 0 trang Next.js error/gateway error, 0 tràn ngang.
- **Đối chiếu SSR HTML với Gateway REST**: kiểm khớp tên, giá `*_vnd`, tồn kho, trạng thái, mã đơn, số dư Xu, danh mục, shop giữa HTML server-render và `/api/v1/*` trên `/`, `/san-pham/[slug]`, `/gian-hang/[slug]`, `/flash-sale`, `/don-hang`, `/don-hang/[id]`, `/tai-khoan`, `/thanh-toan/cho-thanh-toan/[paymentId]`.
- **Luồng E2E thật qua web BFF**: đăng ký `audit-web-605262@nexusmart.local` (`201`) → lấy OTP Mailpit → `verify-otp` (`200`) → `login` (`200`, cookie `HttpOnly` + `SameSite=Lax`) → `/api/auth/session` (`200`) → thêm địa chỉ 2 cấp Hà Nội (`201`, 34 tỉnh thành) → tìm kiếm (`200`, 9 SP) → thêm giỏ (`200`, `229.000₫`) → tính phí vận chuyển + preview khuyến mãi → đặt đơn COD `NM-20261122-523378D2` (`201`, `CONFIRMED`, `256.500₫`) & đặt đơn VietQR `NM-20261122-494A6C5E` (`201`, `PENDING`, `255.000₫`, QR thật) → seller `Xưởng Dệt Bạch Đằng` xem đơn mới (`200`) → admin xem gian hàng (`4`), sản phẩm (`21`), sổ cái cân bằng (`256.500/256.500`), audit log (`4→13`).
- **Bảo vệ trang (RBAC) – 16/16 ĐẠT**:
  - Khách → `/tai-khoan`, `/don-hang`, `/thanh-toan`, `/seller`, `/admin`: đều `307` về `/dang-nhap?next=...`.
  - Khách → `GET /api/gw/orders`: `401`.
  - Buyer → `/seller`, `/seller/don-hang`, `/admin`, `/admin/gian-hang`: đều `404` (ẩn route đúng thiết kế `middleware.ts`); `/api/gw/seller/orders`, `/api/gw/admin/shops`: đều `403`.
  - Seller → `/admin`, `/admin/doi-soat`: đều `404`; `/api/gw/admin/shops`: `403`; truy cập sản phẩm shop khác (`6915353a-...` của shop `98c480c1-...`): `403`.
- **Bảo vệ CSRF & chặn proxy nhạy cảm – 8/8 ĐẠT**:
  - `POST /api/gw/cart/items` thiếu `Origin`, `Origin: https://evil.example`, và giả mạo `X-Forwarded-Host: evil.example`: đều `403 CSRF_FORBIDDEN`.
  - `POST /api/auth/login` thiếu `Origin`, `POST /api/auth/logout` sai `Origin`, `POST /api/auth/refresh` thiếu `Origin`: đều `403 CSRF_FORBIDDEN`.
  - `GET /api/gw/webhooks/payment` và `POST /api/gw/auth/login`: đều `403 FORBIDDEN_PATH`.
  - `web/src/lib/redirect.ts` (`sanitizeNextPath`) chặn protocol-relative `//`, dot-segment `/..//`, backslash, control chars.
- **Tài nguyên ảnh & kiểm tra tĩnh**: 46/46 ảnh tĩnh trả `HTTP 200`; `docker compose logs --since 30m web` không có lỗi runtime.

### 7.2. Chưa kiểm (và lý do)
- Thao tác bấm **xác nhận đã nhận hàng** (`UC-B17`), **trả hàng/hoàn tiền online** (`UC-PAY04`), **tạo vận đơn & in nhãn trên `/seller/don-hang/[id]`** (`UC-SHIP03`): không chuyển trạng thái đơn sang `DELIVERED`/không giả lập webhook thanh toán-vận chuyển để giữ phạm vi 2 đơn nhỏ gọn, sạch trên stack dùng chung (nhóm worker `ord`, `pay`, `shp` kiểm sâu máy trạng thái và webhook).
- Thao tác **ghi/sửa/khóa** của admin và seller trên dữ liệu seed (duyệt/khóa shop, sửa danh mục, khóa tài khoản, tạo sản phẩm/voucher mới, đăng ký shop mới): chỉ kiểm đọc thật qua BFF & Chrome để không làm biến đổi dữ liệu seed của 7 worker khác.
- Kiểm thử trên thiết bị di động thật và viewport trung gian 768px (chỉ kiểm 1440×900 và 390×844 trên Chrome headless).

### 7.3. Kết quả kiểm tra tĩnh trong `web/`

```text
$ npm run lint
> nexusmart-web@0.1.0 lint
> eslint .
(EXIT=0, không có lỗi/cảnh báo)

$ npm run format:check
> nexusmart-web@0.1.0 format:check
> prettier --check "src/**/*.{ts,tsx,css}"
Checking formatting...
All matched files use Prettier code style!
(EXIT=0)

$ npm run check:data
> nexusmart-web@0.1.0 check:data
> node scripts/check-data.mjs
Checking data integrity...
  1. Product IDs & slugs unique: PASS (18 products)
  2. Shop IDs & slugs unique: PASS (4 shops)
  3. Category IDs & slugs unique: PASS (6 categories)
  4. All product shopId & categoryId valid: PASS
  5. All SKU codes unique & valid: PASS (117 SKUs)
  6. All prices are positive integer VND: PASS
  7. All product & SKU image files exist in public/: PASS
  8. All editorial/banner/model/atmosphere media exist: PASS
  9. All flash sale slots & items reference valid products/SKUs: PASS
  10. All vouchers valid & reference valid shops: PASS
  11. All collections, campaigns, journal articles reference valid products/media: PASS
  12. All mock orders reference valid products/SKUs/shops: PASS
  13. All reviews/QAs/chat/disputes/moderation reference valid IDs: PASS
  14. All formatVnd/formatInt helpers behave accurately: PASS
  15. No raw USD/float currency formatting in data: PASS
ALL 15 DATA INTEGRITY CHECKS PASSED.
(EXIT=0; có cảnh báo vô hại từ Node v24 MODULE_TYPELESS_PACKAGE_JSON cho src/lib/format.ts)
```
