# Ma trận tính năng & tuyến giao diện Web — NexusMart (Giai đoạn 4 · Nghiệm thu cuối)

Ngày cập nhật: `2026-11-21` · Phạm vi: Mô hình **nền tảng đa cửa hàng độc lập theo tên miền phụ** (Giai đoạn 4, nhánh `phase-0b`), nối trực tiếp backend thật (10 microservice Go + Payment Rust qua API Gateway `:28080` và BFF Next.js `:3300`). Host gốc `nexusmart.localhost` chỉ giới thiệu nền tảng, cung cấp danh bạ cửa hàng đang xuất bản và cổng quản trị Seller/Admin; toàn bộ luồng mua sắm diễn ra khép kín trên tên miền phụ của từng gian hàng (`<sub>.nexusmart.localhost`).

> **Quy tắc kiến trúc & bảo mật đang áp dụng đồng nhất (đã kiểm chứng qua 25/25 `make e2e-web` & 17/17 `inspect.mjs` `0 FAIL`):**
> - **Phân tách tuyệt đối theo `Host`:** BFF luôn suy ra `shop_id` từ header `Host` (tuân thủ `TRUST_PROXY_HEADERS`) + site `PUBLISHED` của shop `ACTIVE`; không tin `shop_id`, `sales_channel`, `storefront_shop_id` từ client.
> - **Phiên & CSRF theo từng host:** Cookie `HttpOnly`, `SameSite=Lax`, **host-only (0 `Domain=`)**; refresh rotation độc lập theo từng host; CSRF kiểm tra `Origin` khớp đúng host + proto hiện tại (host khác kể cả site khác/host gốc → `403 BAD_ORIGIN`).
> - **Kênh bán hàng:** Mọi đơn tạo từ web luôn khóa `sales_channel = "STOREFRONT"` + `storefront_shop_id` của host. `web/src` không gửi `"MARKETPLACE"`; Admin/Seller chỉ giữ chế độ xem & lọc chỉ-đọc các đơn lịch sử kênh sàn cũ.
> - **Tiền tệ & thanh toán:** Số nguyên VND, sổ cái ghi kép, COD + VietQR (có webhook HMAC & idempotency key), **không có form thẻ ngân hàng / CVV**.

---

## 1. Tổng quan tuyến ứng dụng sau Giai đoạn 4 (`100` tệp route)

| Khu vực | Host | Số tuyến trang | Trạng thái kết nối backend | Thay đổi chính ở Giai đoạn 4 |
| --- | --- | ---: | --- | --- |
| **Host gốc nền tảng `(shop)`** | `nexusmart.localhost`, `localhost` | **5 trang + 2 redirect `308`** | **Live 100%** (`GET /api/v1/sites`, `/api/auth/*`, `robots.txt`, `sitemap.xml`) | Bỏ toàn bộ 21 trang sàn cũ; dựng trang chủ giới thiệu + danh bạ site thật có phân trang; giữ `308` cho `/gian-hang/[slug]` & `/san-pham/[slug]`; chặn `404` mọi `/api/{cart,checkout,orders,payments,vouchers}/*`. |
| **Site gian hàng độc lập `(site)`** | `<sub>.nexusmart.localhost` | **18 trang** | **Live 100%** (Catalog, Cart, Voucher SHOP, Flash sale, Checkout COD/VietQR, Orders, Auth OTP, Addresses) | Chuyển toàn bộ luồng mua sắm/tài khoản/đăng nhập về `(site)` bọc trong `SiteShell` + token `--site-*` sáng/tối; cô lập giỏ/đơn/payment/SKU/voucher theo `Host`; PDP self-canonical; chặn `404` `/seller`, `/admin`, `/api/seller/*`, `/api/admin/*`. |
| **Kênh người bán `(seller)`** | Chỉ host gốc (`404` trên site) | **13 trang** | **Live 11/13 trang** (2 trang phụ `/seller/ho-so` & `/seller/tuong-tac` dùng state/dữ liệu mẫu cục bộ) | Bổ sung khối trạng thái site + nút `Xem site ↗` + cảnh báo khi chưa publish; bỏ tab khuyến mãi/BST/lookbook cấp sàn; đồng bộ preview site. |
| **Cổng quản trị `(admin)`** | Chỉ host gốc (`404` trên site) | **7 trang** | **Live 100%** (Duyệt shop, duyệt SP, quản lý site, flash sale, đối soát sổ cái, audit log) | Xóa 4 màn mock/sàn chết (`/admin/{cau-hinh,khieu-nai,noi-dung,thanh-vien}`) & `api/admin/vouchers`; bổ sung `/admin/flash-sale`; giữ lọc lịch sử kênh sàn/site riêng. |
| **BFF API (`src/app/api/**`)** | Phân nhánh theo `Host` | **55 route handler** | **Live 100%** | Xóa `/api/vouchers/coins` và `/api/admin/vouchers`; toàn bộ Buyer API phục vụ trên host site theo `Host` và trả `404` trên host gốc hoặc site bị khóa/chưa xuất bản. |

---

## 2. Danh mục đầy đủ tuyến hoạt động theo 4 khu vực

### 2.1 Host gốc nền tảng (`nexusmart.localhost`)
- `/` — Trang giới thiệu nền tảng NexusMart (6 section: Hero xưởng Việt + video Jacquard, **Danh bạ gian hàng đang mở bán** phân trang `?trang=` từ `GET /api/v1/sites`, khối giải pháp người bán + xem trước tên miền phụ `SubdomainPreview`, lộ trình 4 bước mở gian hàng, phân tách khách mua / nguyên tắc vận hành, FAQ nền tảng). Không giá, không sản phẩm, không giỏ.
- `/dang-nhap`, `/dang-ky` — Dùng chung `AuthForms` (`variant="platform"`), hỗ trợ `?role=seller&ten=...` cho luồng mở gian hàng; người mua đăng nhập tại host gốc được dẫn về `/#danh-ba-gian-hang`.
- `/ve-nexusmart` — Giới thiệu triết lý nền tảng, hạ tầng tích hợp và danh bạ cửa hàng thật.
- `/tro-giup` — Trung tâm trợ giúp nền tảng (lọc FAQ không dấu theo đối tượng Người bán / Khách mua hàng).
- `/gian-hang/[slug]` (`route.ts`), `/san-pham/[slug]` (`route.ts`) — Chuyển hướng `308 Permanent Redirect` sang đúng subdomain nếu site đang `PUBLISHED`, ngược lại `404`.
- `/robots.txt`, `/sitemap.xml` — Sinh động theo `Host` (host gốc liệt kê trang giới thiệu + URL các site `PUBLISHED`).

### 2.2 Site gian hàng độc lập (`<sub>.nexusmart.localhost` — 18 trang)
- **Trưng bày & Khám phá:** `/` (section theo cấu hình đã publish của shop), `/san-pham` (lọc danh mục, tìm tên/chất liệu, sắp xếp, phân trang), `/san-pham/[slug]` (PDP thật, self-canonical, tồn kho SKU, chọn màu/size, voucher `SHOP`, thêm giỏ / mua ngay), `/tim-kiem` (tìm không dấu + đồng nghĩa trong phạm vi gian hàng), `/gioi-thieu`, `/chinh-sach`.
- **Khuyến mãi gian hàng:** `/flash-sale` (khung giờ Flash sale có sản phẩm của chính gian hàng), `/voucher` (kho mã giảm giá `SHOP` của chính gian hàng, lưu & áp mã).
- **Mua hàng & Thanh toán:** `/gio-hang` (giỏ cô lập đúng shop của `Host`, áp mã `SHOP`, xem trước giảm giá từ backend), `/thanh-toan` (địa chỉ 2 cấp 34 tỉnh, báo giá vận chuyển thật, COD & VietQR, idempotency key, luôn `STOREFRONT`), `/thanh-toan/cho/[id]` (chờ thanh toán VietQR, kiểm tra thuộc đúng site & buyer).
- **Tài khoản & Hậu mãi:** `/dang-nhap`, `/dang-ky` (xác thực OTP email), `/tai-khoan` (hồ sơ + sổ địa chỉ 2 cấp thật), `/don-hang` (lọc theo trạng thái, chỉ trả đơn của buyer TẠI gian hàng này), `/don-hang/[id]` (chi tiết đơn, hủy đơn, xác nhận đã nhận, yêu cầu trả hàng — đơn của site khác trả `404`), `/tra-cuu-don-hang` (+ `/tra-cuu-don` chuyển hướng sang `/tra-cuu-don-hang`).

### 2.3 Kênh người bán (`/seller/**` — 13 trang trên host gốc)
- `/seller` (Tổng quan + `SellerSiteStatus` có trạng thái site & nút `Xem site ↗`), `/seller/san-pham`, `/seller/san-pham/moi`, `/seller/san-pham/[id]`, `/seller/don-hang`, `/seller/don-hang/[id]`, `/seller/khuyen-mai` (Voucher SHOP & Đăng ký Flash sale), `/seller/tai-chinh` (Sổ cái ghi kép VND & số dư thật), `/seller/site` (Trình dựng giao diện, bản nháp, xuất bản, hoàn tác phiên bản), `/seller/site/xem-truoc` (Xem trước bản nháp), `/seller/chat`, `/seller/ho-so` *(mẫu cục bộ)*, `/seller/tuong-tac` *(mẫu cục bộ)*.

### 2.4 Cổng quản trị (`/admin/**` — 7 trang trên host gốc)
- `/admin` (Tổng quan quản trị, GMV, sổ cái cân đối, đơn hàng gần đây có lọc kênh `Tất cả` / `Sàn NexusMart` / `Site riêng`), `/admin/gian-hang` (Duyệt hồ sơ mở gian hàng), `/admin/kiem-duyet` (Duyệt sản phẩm), `/admin/sites` (Quản lý & tạm khóa/mở khóa site gian hàng), `/admin/flash-sale` (Duyệt suất Flash sale), `/admin/doi-soat` (Đối soát sổ cái ghi kép VND, có lọc kênh lịch sử), `/admin/audit-log` (Nhật ký kiểm toán gồm sự kiện `site`).

---

## 3. Đối chiếu 55 tính năng gốc (`B01–B17`, `P01–P20`, `C01–C18`) sang Giai đoạn 4

Bảng dưới ánh xạ toàn bộ 55 tính năng từ báo cáo Đợt 2/3 sang trạng thái thực tế của Giai đoạn 4 (phân tách rõ tính năng đã nối backend thật, tính năng đã gỡ bỏ có chủ đích khi bỏ trang sàn, và phần chưa nối dịch vụ ngoài):

### 3.1 Nhóm Duyệt & Khám phá (`B01–B17`)

| Mã | Tính năng | Trạng thái Giai đoạn 4 | Vị trí hiện hành | Ghi chú / Phần ngoài phạm vi |
| --- | --- | --- | --- | --- |
| B01 | Điều hướng toàn trang | **Live** | `PlatformHeader`/`PlatformFooter` (host gốc) & `SiteShell`/`SiteChrome` (header 1 hàng + drawer di động trên site) | Đã gỡ hộp thoại cookie & mega-menu sàn cũ. |
| B02 | Mega-menu đa ngành toàn sàn | **Đã gỡ (mô hình sàn)** | — | Không còn trang sàn đa ngành; mỗi site dùng menu gọn của riêng cửa hàng. |
| B03 | Danh mục & Bộ sưu tập | **Live theo site** (bỏ BST sàn) | `<sub>.../san-pham`, section `collection` trên `<sub>.../` | Đã xóa `/danh-muc`, `/bo-suu-tap/**` khỏi host gốc. |
| B04 | Trang chủ | **Live (2 vai trò tách biệt)** | `/` host gốc (giới thiệu + danh bạ site thật) & `/` site (section tùy biến theo shop) | Không còn khối bán hàng đa shop trên host gốc. |
| B05 | Tìm kiếm không dấu & đồng nghĩa | **Live theo site** | `<sub>.../tim-kiem`, ô tìm kiếm trên `SiteChrome` & `SiteCatalogView` | Tìm kiếm chạy trong danh mục thật của gian hàng hiện tại; chưa có Search service độc lập. |
| B06 | Từ khóa xu hướng toàn sàn | **Đã gỡ (mô hình sàn)** | — | Gỡ khỏi host gốc và không dùng trên site đơn gian hàng. |
| B07 | Xử lý kết quả tìm kiếm rỗng | **Live theo site** | `<sub>.../tim-kiem`, `<sub>.../san-pham` | Gợi ý sửa chính tả & xóa lọc trong phạm vi shop (bỏ nút mở rộng ra toàn sàn). |
| B08 | Bộ lọc danh mục & tìm nhanh | **Live theo site** | `<sub>.../san-pham` (lọc danh mục thật + tên/chất liệu) | Faceted 13 tiêu chí của `CatalogExplorer` sàn cũ đã gỡ cùng trang sàn. |
| B09 | Sắp xếp danh sách | **Live theo site** | `<sub>.../san-pham` (`Mới nhất`, `Giá tăng dần`, `Giá giảm dần`) | Sắp xếp trên dữ liệu Catalog thật của gian hàng. |
| B10 | Ảnh sản phẩm & chuyển biến thể | **Live theo site** | `SiteProductCard`, `SitePdpView`, `ProductCard` (dải mua kèm) | Ảnh studio + ảnh người mẫu trong `public/products` & `public/models`; chưa upload CDN ngoài. |
| B11 | Thêm nhanh vào giỏ (`Quick-add`) | **Live theo site** | `ProductCard` (`ProductAddonStrip` ở giỏ/thanh toán/đơn hàng) & `SitePdpView` | Gọi thẳng `POST /api/cart/items` thật theo `Host`; chưa đăng nhập chuyển `/dang-nhap?next=...`. |
| B12 | Nhãn giảm giá / Flash sale | **Live theo site** | `SiteProductCard`, `SitePdpView`, `<sub>.../flash-sale` | Tính trực tiếp từ giá SKU & suất Flash sale thật của shop. |
| B13 | Phân trang danh sách | **Live** | Danh bạ site `/?trang=` (server pagination `GET /api/v1/sites`) & `<sub>.../san-pham` | Phân trang danh bạ site thật qua backend gRPC/REST. |
| B14 | Tạp chí / Blog sàn (`Sổ tay vải`) | **Đã gỡ (mô hình sàn)** | — | Đã xóa `/tap-chi/**` và tab Lookbook sàn trong Seller/Admin. |
| B15 | Bảng size xưởng & hồ sơ vận hành | **Cục bộ (Seller)** | `/seller/ho-so` (giữ chỉnh bảng size mẫu cục bộ) | Đã gỡ `SizeFinder` sàn cũ trên host gốc. |
| B16 | Giới thiệu thương hiệu & minh bạch | **Live** | `/ve-nexusmart` (host gốc) & `<sub>.../gioi-thieu` (từng cửa hàng) | Nạp trực tiếp từ danh bạ/cấu hình site thật. |
| B17 | Chiến dịch sàn (`Công nghệ vải`, `Sợi Xanh Nexus`) | **Đã gỡ (mô hình sàn)** | — | Đã xóa `/chien-dich/[slug]` và `/admin/noi-dung`. |

### 3.2 Nhóm Trang sản phẩm (`P01–P20`)

| Mã | Tính năng | Trạng thái Giai đoạn 4 | Vị trí hiện hành | Ghi chú / Phần ngoài phạm vi |
| --- | --- | --- | --- | --- |
| P01 | Chọn màu & size theo SKU thật | **Live** | `<sub>.../san-pham/[slug]` (`SitePdpView`) | Khóa SKU theo `shop_id` của host; sản phẩm shop khác trả `404`. |
| P02 | Tồn kho SKU thật & giữ tồn khi đặt | **Live** | `SitePdpView` + Inventory/Order backend (optimistic locking) | Vô hiệu hóa cỡ hết tồn (`stock = 0`) và giới hạn số lượng theo tồn kho thật. |
| P03 | Đăng ký báo có hàng (Back-in-stock) | **Cục bộ (Seller)** | `/seller/tuong-tac` (mẫu cục bộ) | Chưa có API/thông báo đẩy Back-in-stock ở backend. |
| P04–P06 | Bảng size modal, gợi ý size 3 dáng, thông số người mẫu | **Chuyển pha sau (site PDP)** | Gỡ khỏi PDP sàn cũ (`features/product/**`) | `SitePdpView` tập trung ảnh người mẫu, thông số sợi/GSM/form, chọn màu/size, tồn kho, voucher SHOP và nút mua thật. |
| P07 | Vải, GSM, form & xuất xứ | **Live** | `SitePdpView`, `SiteProductCard` | Hiển thị trực tiếp từ dữ liệu Catalog + metadata sản phẩm. |
| P08 | Điểm nổi bật & hướng dẫn bảo quản | **Live (một phần)** | `SitePdpView` (`Điểm nổi bật`), `<sub>.../chinh-sach` | Hiển thị danh sách điểm nổi bật của từng sản phẩm. |
| P09 | Thư viện ảnh PDP (`gallery` theo màu) | **Live** | `SitePdpView` (ảnh chính 3:4 + hàng 4 ảnh thu nhỏ đổi ảnh theo màu) | Dùng ảnh gốc trong `public/models` & `public/products`; chưa upload CDN ngoài. |
| P10–P11 | Đánh giá có ảnh & Hỏi đáp công khai | **Cục bộ (Seller) / Pha sau** | `/seller/tuong-tac` (mẫu cục bộ) | Chưa có Review/QA service backend; đã gỡ `SAMPLE_REVIEWS`/`SAMPLE_QAS` khỏi PDP người mua. |
| P12 | Giá nguyên VND, `% giảm` & giá Flash sale | **Live** | `SitePdpView`, `SiteProductCard`, `<sub>.../flash-sale` | Đồng bộ giá gốc, giá bán và giá Flash sale đang mở từ backend. |
| P13 | Giảm giá tầng / Combo sàn | **Đã gỡ (UI mock sàn)** | — | Đã gỡ `PromotionsBundleTab` ở Seller; giá tính hoàn toàn ở Promotion/Order backend. |
| P14 | Voucher gian hàng (`SHOP`) trên PDP | **Live** | `SitePdpView` (khối `Ưu đãi riêng của <Shop>`) + `<sub>.../voucher` | Chỉ hiển thị voucher `SHOP` thuộc đúng shop của host; đã gỡ voucher sàn/freeship sàn/Xu. |
| P15 | Gợi ý mua thêm cùng gian hàng | **Live** | `ProductAddonStrip` trên `<sub>.../gio-hang`, `/thanh-toan`, `/don-hang` | Lọc 100% sản phẩm cùng gian hàng đang truy cập. |
| P16 | Sản phẩm khác cùng gian hàng | **Live** | `SitePdpView` (`Sản phẩm khác của <Shop>`) | Không gợi ý sản phẩm của gian hàng khác (nguyên tắc cô lập site). |
| P17–P18 | Đã xem gần đây & Yêu thích (`wishlist`) | **Cục bộ phiên** | `StoreContext` (`recordProductView`, `wishlist`), `ProductCard` | Lưu cục bộ phía trình duyệt; chưa đồng bộ DB tài khoản. |
| P19 | Tính cước vận chuyển 34 tỉnh 2 cấp | **Live** | `<sub>.../thanh-toan` (`POST /api/checkout/shipping-quote`) | Gọi Shipping service thật theo trọng lượng & địa chỉ nhận; ẩn nhãn nội bộ `Mock Express` bằng `Giao hàng NexusFast`. |
| P20 | Chính sách bán hàng & đổi trả của shop | **Live** | `<sub>.../chinh-sach`, khối chính sách trên `<sub>.../` | Đọc trực tiếp từ cấu hình xuất bản của gian hàng. |

### 3.3 Nhóm Giỏ hàng, Thanh toán, Sau mua, Tài khoản & Hỗ trợ (`C01–C18`)

| Mã | Tính năng | Trạng thái Giai đoạn 4 | Vị trí hiện hành | Ghi chú / Phần ngoài phạm vi |
| --- | --- | --- | --- | --- |
| C01 | Giỏ hàng & Thanh toán 1 trang | **Live theo site** | `<sub>.../gio-hang`, `<sub>.../thanh-toan` | Luồng bắt buộc đăng nhập (giỏ khách chưa đăng nhập để pha sau theo brief). Không còn giỏ đa gian hàng. |
| C02 | Quà ngưỡng đơn / freeship toàn sàn | **Đã gỡ (mô hình sàn)** | — | Không áp dụng freeship toàn sàn hay quà hội viên trên site riêng. |
| C03 | Áp mã & ví voucher `SHOP` | **Live (Redis Lua backend)** | `<sub>.../gio-hang`, `/thanh-toan`, `/voucher` (`POST /api/vouchers/{validate,preview}`) | Chỉ chấp nhận mã `SHOP` của đúng site; từ chối mã sàn, freeship sàn, mã shop khác & Xu. |
| C04 | Sổ địa chỉ 2 cấp 34 tỉnh & ĐVVC | **Live** | `<sub>.../thanh-toan`, `<sub>.../tai-khoan` (`/api/auth/addresses`, `/api/checkout/locations`, `shipping-quote`) | Đã sửa chống lặp phường/tỉnh trong hiển thị địa chỉ; hãng vận chuyển thật SPX/GHN tích hợp API đối tác để pha sau. |
| C05 | Thanh toán COD, VietQR & Thanh toán lại | **Live (Rust Payment + Webhook HMAC)** | `<sub>.../thanh-toan`, `/thanh-toan/cho/[id]`, `/don-hang/[id]` (`/api/payments/[id]{,/retry}`) | Không form thẻ/CVV; chống replay & đối soát sổ cái ghi kép nguyên VND; cô lập payment theo site. |
| C06 | Xuất hóa đơn VAT / Túi quà | **Pha sau (backend)** | — | Chưa kết nối nhà cung cấp HĐĐT (VNPT/MISA/Viettel); đã gỡ form mock trên checkout. |
| C07 | Thông báo đơn hàng & Tra cứu đơn | **Live** | `services/notification` (link trỏ đúng host site từ `storefront_shop_id`) + `<sub>.../tra-cuu-don-hang` | Email thật qua Mailpit/SMTP; Zalo OA ZNS thật để pha sau. |
| C08 | Hủy đơn & hoàn tiền | **Live** | `<sub>.../don-hang/[id]` (`POST /api/orders/[id]/cancel`) | Nhả tồn kho & cập nhật sổ cái qua Saga/Outbox backend; kiểm tra thuộc đúng site & buyer. |
| C09 | Xác nhận nhận hàng & Yêu cầu trả hàng | **Live** | `<sub>.../don-hang/[id]` (`confirm-received`, `return`) | Xác minh quyền sở hữu + thuộc đúng site; vận đơn đổi size 2 chiều tự động để pha sau. |
| C10 | Lịch sử đơn hàng, sổ địa chỉ & tài khoản | **Live theo site** | `<sub>.../don-hang`, `<sub>.../tai-khoan` | `GET /api/orders` lọc `storefront_shop_id` tại backend Postgres; đơn ở site khác trả `404` dù cùng tài khoản. |
| C11–C12 | Hội viên Nexus, Xu & Giới thiệu bạn bè | **Đã gỡ (mô hình sàn)** | — | Đã xóa `/hoi-vien`, `/api/vouchers/coins`, `/admin/thanh-vien`, `src/data/membership.ts`. |
| C13 | Hồ sơ số đo cơ thể | **Đã gỡ khỏi sàn** | — | Đã gỡ khỏi `/tai-khoan` site; danh sách yêu thích cục bộ vẫn hoạt động. |
| C14 | Ưu đãi sinh nhật & Tổng kết năm | **Đã gỡ (mô hình sàn)** | — | Gỡ cùng chương trình Hội viên Nexus. |
| C15 | Flash sale theo gian hàng | **Live (Redis Lua backend)** | `<sub>.../flash-sale`, `/seller/khuyen-mai`, `/admin/flash-sale` | Chỉ hiển thị suất Flash sale thuộc sản phẩm của gian hàng hiện tại; trừ suất nguyên tử bằng Redis Lua. |
| C16 | Thẻ quà tặng toàn sàn & `.edu` | **Đã gỡ (mô hình sàn)** | — | Đã xóa `/the-qua-tang` và tab thẻ quà tặng trong Admin. |
| C17 | Hỗ trợ đa kênh, Chat 1-1 & Trợ lý AI | **Một phần / Pha sau** | `/tro-giup` (host gốc), `/seller/chat` (Seller) | Đã gỡ `BuyerChatPanel` & `AiAssistantPanel` mock khỏi `(shop)`/`(site)`; WebSocket chat thời gian thực & AI Assistant dùng JWT người dùng triển khai khi nối dịch vụ AI/Chat. |
| C18 | Trung tâm trợ giúp & FAQ nền tảng | **Live** | `/tro-giup` (host gốc), `<sub>.../chinh-sach` (từng cửa hàng) | Đã viết lại sạch cho mô hình nền tảng đa cửa hàng (phân nhóm Người bán / Khách mua hàng); gỡ màn khiếu nại mock `/admin/khieu-nai`. |
