# Brief – Giai đoạn 4: bỏ trang sàn. Host gốc chỉ giới thiệu NexusMart; mỗi subdomain là một cửa hàng đầy đủ

Chủ dự án quyết định (2026-11-21): "bỏ trang sàn và trang chủ chỉ là giới thiệu về nexusmart, các
sub domain mới là các site riêng". Mô hình giờ giống Shopify: NexusMart là nền tảng, người mua chỉ
mua trên site của từng gian hàng.

Bạn là coordinator. Claude Code là manager và sẽ tự chạy lại mọi gate. Đọc trước: `AGENTS.md`,
`docs/PHASE2_REPORT.md`, `docs/PHASE1_REPORT.md`, `docs/WEB_INTEGRATION_REPORT.md`,
`.agents/briefs/sites-2-subdomain-sites.md`, `web/DESIGN_REPORT.md`.

Nhánh hiện tại: `phase-0b`. KHÔNG commit, không push, không đổi nhánh. CI chỉ `workflow_dispatch`.
Không kill tiến trình cổng 3111 và 4777; không ghi `web/.next` (dùng `NEXT_DIST_DIR` riêng). Không
`sudo`. Không thêm dependency runtime. Xóa code chết thay vì để lại; không viết lớp tương thích.

## A. Host gốc (`SITE_ROOT_DOMAIN`, `localhost`, IP)
Chỉ còn:
- `/`: trang giới thiệu NexusMart cho hai đối tượng: người bán (mở site riêng trên subdomain, theme,
  thanh toán COD/VietQR, vận chuyển, đối soát) và khách ghé (danh bạ các site đang PUBLISHED, mỗi ô
  link sang subdomain: logo, tên, mô tả ngắn, ảnh bìa – dữ liệu thật từ backend, cần RPC/REST công khai
  liệt kê site PUBLISHED có phân trang). CTA chính: "Mở gian hàng". Không có lưới sản phẩm, không giỏ,
  không tìm kiếm sản phẩm. Giữ art direction, typeface, house rules hiện có; không bịa số liệu, logo
  đối tác hay lời khen. Trang phải dày, không phải một hero rồi hết.
- `/dang-nhap`, `/dang-ky` (cho người bán và admin; có luồng đăng ký mở gian hàng), `/seller/**`,
  `/admin/**`, các trang tĩnh nền tảng (`/ve-nexusmart`, `/tro-giup`, chính sách/điều khoản nếu có).
- Mọi route người mua cũ của sàn (danh mục, tìm kiếm, sản phẩm, flash sale, bộ sưu tập, chiến dịch,
  gian hàng, giỏ, thanh toán, đơn hàng, tài khoản người mua, hội viên, thẻ quà tặng, tạp chí, voucher
  sàn, tra cứu đơn…) bị GỠ khỏi host gốc: xóa route, component, mock và test chỉ phục vụ chúng. URL cũ
  trả 404 (riêng `/gian-hang/<slug>` và `/san-pham/<slug>` 308 sang site của gian hàng nếu site
  PUBLISHED, ngược lại 404). Người mua đăng nhập ở host gốc được dẫn về danh bạ site.

## B. Host site (`<sub>.<SITE_ROOT_DOMAIN>`) = cửa hàng đầy đủ
- Giữ: trang chủ theo section, danh sách + trang sản phẩm, chính sách, giới thiệu.
- Chuyển về đây (tái dùng component giỏ/checkout/tài khoản hiện có, bọc trong layout + token theme của
  gian hàng, không chuyển hướng sang host gốc nữa): tìm kiếm trong gian hàng, flash sale của gian hàng,
  voucher SHOP, giỏ hàng, thanh toán (COD, VietQR, trang chờ thanh toán), đăng ký (OTP), đăng nhập,
  tài khoản + sổ địa chỉ, danh sách/chi tiết đơn, hủy đơn, tra cứu đơn.
- BFF: `/api/**` của người mua giờ phục vụ trên host site (trước đây 404). Server LUÔN suy ra gian hàng
  từ `Host` qua site PUBLISHED (không tin `shop_id`, `channel`, hint từ client): giỏ chỉ đọc/ghi dòng
  của gian hàng đó; checkout luôn `sales_channel=STOREFRONT` + `storefront_shop_id` của host; danh
  sách/chi tiết đơn chỉ trả đơn của người mua TẠI gian hàng đó (đơn ở site khác → 404); sản phẩm, SKU,
  voucher của gian hàng khác → 404/400. `/api/seller/**`, `/api/admin/**`, `/seller`, `/admin` vẫn 404
  trên host site. Trên host gốc, API giỏ/checkout/đơn của người mua trả 404.
- Phiên: cookie vẫn `HttpOnly`, host-only, theo từng host (đăng nhập riêng từng site; tài khoản dùng
  chung ở backend). KHÔNG đặt `Domain=` cho cookie. CSRF kiểm `Origin` trùng đúng host đang phục vụ.
  Refresh rotation hoạt động độc lập theo host.
- Tính năng cấp sàn không còn trên UI người mua: voucher sàn, freeship sàn, xu, hạng hội viên, thẻ quà
  tặng, giỏ nhiều gian hàng. Backend: giữ nguyên RPC và enum `MARKETPLACE` (không xóa schema, không
  migration phá dữ liệu); web không bao giờ gửi `MARKETPLACE`. Admin vẫn xem được đơn cũ kênh sàn.
- SEO: canonical của trang sản phẩm là chính URL trên site (không còn trỏ về sàn); luật `noindex` cho
  site chưa có nội dung giữ nguyên; `sitemap.xml`/`robots.txt` theo host; sitemap host gốc gồm trang
  giới thiệu và link các site PUBLISHED.
- Email/thông báo đơn hàng dùng link về đúng host site của đơn (nếu service notification đang chèn
  link sàn thì sửa; lấy host từ `storefront_shop_id`).
- Khách chưa đăng nhập: giỏ yêu cầu đăng nhập như hiện tại (giỏ khách là việc sau).

## C. Seller, admin, seed
- Seller center: bỏ các màn/khối chỉ có nghĩa với sàn (đăng ký flash sale sàn giữ nếu là flash sale
  của chính gian hàng; voucher sàn bỏ). Tổng quan hiện link "Xem site" và trạng thái site; gian hàng
  chưa PUBLISH site thấy lời nhắc rõ vì chưa có site thì chưa bán được.
- Admin: bỏ màn quản lý nội dung trang chủ sàn/chiến dịch sàn/hội viên nếu không còn nơi hiển thị;
  giữ duyệt gian hàng, duyệt sản phẩm, site, đơn, sổ cái/đối soát, audit.
- `make seed`: cả 4 gian hàng mẫu đều có site PUBLISHED với theme khác nhau; idempotent.

## Gate (dán đầu ra thật vào báo cáo)
- `make lint`, `make test`; web lint/format/tsc/unit/build (`NEXT_DIST_DIR` riêng).
- Chuỗi `make e2e && make seed && make e2e-web && make e2e-web` xanh liền mạch. `tests/e2e-web` viết
  lại theo mô hình mới (gọi bằng header `Host`), tối thiểu:
  host gốc `/` 200 và không chứa giá/sản phẩm; danh bạ liệt kê đúng site PUBLISHED; URL người mua cũ
  trên host gốc 404; `/api/cart`, `/api/checkout`, `/api/orders` trên host gốc 404;
  trên site A: đăng ký+OTP → đăng nhập (cookie HttpOnly, host-only, không `Domain`) → thêm giỏ → voucher
  SHOP → COD → thấy đơn, đơn là `STOREFRONT` đúng shop; VietQR mock + webhook → PAID; idempotency;
  cookie của site A không tạo phiên trên site B; SKU/voucher của B trên site A bị từ chối; đơn đặt ở A
  không hiện và không đọc được trên B (404) dù cùng tài khoản đăng nhập ở B; IDOR giữa hai người mua;
  ghi không phiên 401; CSRF `Origin` của host khác (kể cả site khác và host gốc) → 403; body cố nhét
  `sales_channel=MARKETPLACE` hoặc `storefront_shop_id` của shop khác bị bỏ qua/từ chối;
  `/seller`, `/admin`, `/api/seller/*`, `/api/admin/*` trên host site 404; RBAC seller/admin trên host
  gốc; site bị khóa → cả trang lẫn API 404.
- `grep` chứng minh không còn route/component người mua của sàn trong `web/src/app` ngoài `(site)` và
  không còn chuỗi "MARKETPLACE" được gửi đi từ `web/src`.
- `inspect.mjs` (`/home/dev/.claude/skills/design-taste-frontend/scripts/inspect.mjs`) 0 FAIL ở
  1440/768/390: host gốc `/`; với 2 site: trang chủ, danh sách, sản phẩm, giỏ (đã đăng nhập, có hàng),
  thanh toán, đăng nhập, đơn hàng; `--app` cho `/seller` và `/admin`. Mở ảnh chụp và sửa lỗi nhìn thấy.
- `docs/PHASE4_REPORT.md`: đã làm, danh sách route đã xóa/chuyển, bằng chứng từng gate, phần chưa làm
  kèm lý do, mọi chỗ lệch. Cập nhật `web/DESIGN_REPORT.md` và `web/FEATURE_MATRIX.md` cho mô hình mới.
