# Brief – Giai đoạn 2 + 2b: site riêng theo subdomain cho từng gian hàng, giữ tồn flash sale

Bạn là coordinator. Claude Code là manager và sẽ tự chạy lại mọi gate. Đọc trước: `AGENTS.md`,
`docs/research/sites/review-opus.md` (kiến trúc chốt – làm theo đúng nó), `docs/PHASE1_REPORT.md`,
`docs/WEB_INTEGRATION_REPORT.md`, `.agents/briefs/sites-0b-swarm-rules.md`, `web/DESIGN_REPORT.md`.

Nhánh hiện tại: `phase-0b`. KHÔNG commit, không push, không đổi nhánh. CI chỉ `workflow_dispatch`.
Không kill tiến trình cổng 3111 và 4777; không ghi `web/.next` (dùng `NEXT_DIST_DIR` riêng). Không
`sudo`. Không thêm dependency runtime. Không thêm service mới (cấu hình site là module trong `user`).

## 2.A Backend – module site trong service `user`
- Bảng: `sites` (1 site / gian hàng, `subdomain` duy nhất, trạng thái DRAFT|PUBLISHED|SUSPENDED,
  `published_version_id`), `site_versions` (bản chụp JSON bất biến, đánh số), `domains` (host → site;
  giai đoạn này chỉ sinh host `<subdomain>.<SITE_ROOT_DOMAIN>`; cột sẵn cho tên miền riêng sau này).
- RPC: `GetSiteByHost` (công khai, chỉ trả bản PUBLISHED của gian hàng ACTIVE), `GetSiteDraft`,
  `SaveSiteDraft`, `PublishSite`, `RollbackSite`, `ListSiteVersions`. Ghi: chỉ chủ gian hàng
  (`shop_id` từ JWT) hoặc admin; admin có `SuspendSite`. Mọi thao tác ghi vào audit log.
- Subdomain: `[a-z0-9-]{3,40}`, không bắt đầu/kết thúc bằng `-`, danh sách cấm (www, api, admin,
  seller, app, mail, static, cdn, nexusmart, …), duy nhất không phân biệt hoa thường.
- Theme là DỮ LIỆU: token (màu trong bảng cho phép hoặc hex hợp lệ có kiểm tương phản chữ/nền ≥ 4.5,
  font trong danh sách cho phép, logo/ảnh bìa là URL https hoặc đường dẫn nội bộ đã upload) + mảng
  section JSON có schema (`hero`, `featured_products`, `collection`, `text`, `banner`, `voucher_strip`,
  `policies`, tối đa 20 section, giới hạn độ dài từng trường). Backend Go validate schema chặt, từ chối
  field lạ. KHÔNG có HTML/CSS/JS tự do; mọi chuỗi render dạng text. URL chỉ `https:` hoặc đường dẫn
  tương đối; cấm `javascript:`/`data:`.
- Gateway: REST tương ứng dưới `/api/v1/sites/by-host`, `/api/v1/seller/site/**`, `/api/v1/admin/sites/**`.

## 2.B Web – một app, nhiều site
- `web/src/middleware.ts`: đọc `Host` (theo luật tin proxy của giai đoạn 1). Host gốc
  (`SITE_ROOT_DOMAIN`, `localhost`, IP) → sàn như cũ. Host `<sub>.<SITE_ROOT_DOMAIN>` → tra site (cache
  ngắn, có cache âm); không có site PUBLISHED → 404 TRƯỚC khi rewrite; có → rewrite sang
  `app/(site)/s/[host]/…`. Đường dẫn `/s/...` gọi trực tiếp từ ngoài phải 404. Route `/api`, `/seller`,
  `/admin` không phục vụ trên host site.
- Trang site (ẩn danh, cache theo site, revalidate khi publish): trang chủ dựng từ section, danh sách
  sản phẩm của gian hàng (lọc, sắp xếp, phân trang), trang sản phẩm, trang chính sách/giới thiệu. Chỉ
  hiện sản phẩm và voucher SHOP của đúng gian hàng đó. Header/footer mang thương hiệu gian hàng, có
  dòng "Vận hành trên NexusMart".
- Mua hàng (MVP, không SSO): nút Mua/Thêm giỏ chuyển về host sàn
  `…/gio-hang?shop=<id>&channel=storefront` (hoặc trang trung gian thêm SKU rồi chuyển). Giỏ và
  checkout ở chế độ này: chỉ dòng của gian hàng đó, mang logo/màu của gian hàng, có link quay lại site.
  BFF checkout gửi `sales_channel=STOREFRONT` + `storefront_shop_id` CHỈ khi server tự xác minh được
  gian hàng có site PUBLISHED; không tin tham số client một cách mù quáng (client chỉ gợi ý shop, mọi
  luật do backend giai đoạn 1 ép). Cookie vẫn host-only trên host sàn.
- SEO: trang sản phẩm trên site đặt `canonical` về URL sản phẩm trên sàn; site chưa có section nội dung
  riêng → `noindex`; `robots.txt` và `sitemap.xml` theo host; `title`/OG theo gian hàng.
- Seller center: màn "Site riêng" (`/seller/site`): chọn subdomain, sửa token theme, thêm/xóa/sắp xếp
  section bằng form (không cần kéo thả), xem trước bản nháp (route preview chỉ chủ shop xem được,
  `noindex`), xuất bản, lịch sử phiên bản và khôi phục. Admin: danh sách site, tạm khóa.
- Giữ art direction và house rules của `web/` cho phần khung; theme của gian hàng chỉ đổi token.

## 2b Giữ tồn cho suất flash sale
Tồn kho dùng chung giữa sàn và site. Khi một SKU được duyệt vào khung flash sale với N suất, service
`product` phải giữ N đơn vị (reserved cho flash sale) từ lúc duyệt tới khi khung kết thúc hoặc bị hủy;
đơn thường (cả hai kênh) không được bán vào phần này; đơn flash sale trừ từ phần giữ; hết khung thì
nhả phần còn lại. Duyệt bị từ chối nếu tồn khả dụng < N. Idempotent, an toàn khi chạy đồng thời.

## Gate (dán đầu ra thật vào báo cáo)
- Mọi gate cũ: `make lint`, `make test`, web lint/format/tsc/build, chuỗi
  `make e2e && make seed && make e2e-web && make e2e-web` xanh liền mạch.
- `make seed` tạo thêm ít nhất 2 site PUBLISHED với theme khác nhau rõ rệt (idempotent).
- E2E mới (web, gọi bằng header `Host`): host lạ → 404; site A chỉ có sản phẩm của A, không lộ sản
  phẩm/voucher của B; `/s/<host>` trực tiếp → 404; `/admin`, `/seller`, `/api/*` trên host site → 404;
  nháp không hiện công khai trước khi publish; publish → hiện; rollback → về bản cũ; seller B không
  sửa/xem nháp được site của A (403/404); payload theme có field lạ, `javascript:` URL, thẻ `<script>`
  trong text → bị từ chối hoặc render dạng text thuần; subdomain cấm/trùng bị từ chối; site bị khóa →
  404; mua từ site → đơn `STOREFRONT` đúng `storefront_shop_id`; canonical và noindex đúng luật.
- E2E 2b: duyệt N suất khi tồn < N bị từ chối; đơn thường không ăn vào phần giữ (bán hết phần thường
  thì báo hết hàng trong khi flash sale vẫn còn N); 1.000 yêu cầu đồng thời vẫn bán đúng số suất; hết
  khung nhả tồn; sổ cái cân.
- `inspect.mjs` (`/home/dev/.claude/skills/design-taste-frontend/scripts/inspect.mjs`) 0 FAIL ở
  1440/768/390 cho trang chủ + danh sách + sản phẩm của 2 site mẫu, và `--app` cho `/seller/site`.
- `docs/PHASE2_REPORT.md`: đã làm, bằng chứng từng gate, cách mở site mẫu trên máy dev (host nào, env
  nào), phần chưa làm kèm lý do, mọi chỗ lệch khỏi `review-opus.md`.
