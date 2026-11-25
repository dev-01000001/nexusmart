# Giai đoạn 4 – Bỏ trang sàn, Mô hình Nền tảng & Cửa hàng độc lập theo Subdomain: Báo cáo nghiệm thu

- **Ngày nghiệm thu:** `2026-11-21`
- **Nhánh git:** `phase-0b` (commit checkpoint nền `b56f8bb wip(4): checkpoint while agy swarm runs (2026-11-21 10:53)`; **0 commit mới** từ verifier, không push, không đổi nhánh)
- **Đặc tả chuẩn:** `.agents/briefs/sites-4-no-marketplace.md`, `.agents/briefs/sites-4-swarm-rules.md`, `AGENTS.md`
- **Xác minh độc lập:** `verifier` (chỉ đọc mã nguồn, chạy độc lập từ đầu đến cuối toàn bộ 7 Gate trên stack live thật, chỉ ghi duy nhất báo cáo này tại `docs/PHASE4_REPORT.md`)

---

## 1. Tóm tắt các hạng mục đã thực hiện theo từng mảng

### 1.1 Backend (`services/user`, `services/order`, `services/notification`, `services/gateway`, `proto`, `tools/seed`, `tests/e2e`)
1. **Danh bạ site công khai (`UserService.ListPublishedSites` & `GET /api/v1/sites`):**
   - Bổ sung gRPC công khai không cần JWT trong `services/user` (giữ nguyên độ chặt của `pkg/common/auth/auth.go`), chỉ liệt kê site trạng thái `PUBLISHED` của gian hàng `ACTIVE`, sắp xếp `published_at DESC`, giới hạn `page_size <= 48`.
   - Gateway REST `GET /api/v1/sites?page=1&page_size=24` trả đúng hợp đồng `{"sites":[{"site_id","shop_id","shop_name","subdomain","primary_host","logo_url","cover_url","tagline"}],"total","page","page_size"}`, giữ **0 header CORS**.
2. **Lọc đơn hàng theo `storefront_shop_id` ở backend (`services/order` & `services/gateway`):**
   - Bổ sung migration không phá dữ liệu `services/order/migrations/0003_orders_storefront_index.sql` tạo chỉ mục `idx_orders_buyer_storefront_created ON public.orders (buyer_id, storefront_shop_id, created_at DESC)`.
   - `GET /api/v1/orders?storefront_shop_id=<uuid>` lọc trực tiếp tại Postgres với kiểm tra UUID ở Gateway và Order service để phân trang chính xác; `GET /api/v1/orders/{id}` luôn trả `sales_channel` và `storefront_shop_id`. Giữ nguyên enum/RPC `MARKETPLACE` cho dữ liệu lịch sử.
3. **Liên kết email/thông báo đơn hàng trỏ đúng host site (`services/notification` + `services/order`):**
   - Gắn `storefront_shop_id` vào toàn bộ 6 payload outbox `order.*` tại `services/order`.
   - Bổ sung migration `services/notification/migrations/000002_order_storefront.sql` (cột `order_participants.storefront_shop_id`), tra cứu host site theo `storefront_shop_id` (xử lý cả trường hợp `payment.*` có `order_id == order_group_id` trỏ về `/don-hang`), nhận `SITE_URL_SCHEME`/`SITE_URL_PORT` qua `docker-compose.yml` và `.env.example`.
4. **Seed dữ liệu & Vệ sinh fixture (`tools/seed/seed.mjs`, `tests/e2e/13_sites_backend_test.go`):**
   - `make seed` tạo/tái sử dụng idempotent đủ **4 site `PUBLISHED`** với 4 bảng màu + phông chữ khác biệt đạt tương phản WCAG `>= 4.5:1`: `bach-dang` (`work-sans`, `#F8F6F1`/`#1D4ED8`), `sai-gon` (`be-vietnam-pro`, `#14181C`/`#D97706`), `da-nang` (`ibm-plex-sans`, `#EEF2EA`/`#3F6B4F`), `ha-noi` (`plus-jakarta-sans`, `#2B1A1E`/`#E9B949`), kèm voucher `SHOP` cho từng shop.
   - Bổ sung `t.Cleanup` tạm khóa site fixture trong `13_sites_backend_test.go` để danh bạ công khai `GET /api/v1/sites` luôn sạch (`total = 4`).

### 1.2 BFF, Middleware, Phân định `Host`, Phiên/CSRF & SEO (`web/src/middleware.ts`, `src/lib/{server,site}/**`, `src/app/api/**`)
1. **Middleware & Cô lập đa gian hàng (`src/middleware.ts`, `src/lib/site/host.ts`, `src/lib/server/siteLookup.ts`):**
   - Truy cập trực tiếp `/s`, `/s/**` hoặc host lạ / nhãn cấm / đa cấp → **404** trên mọi host.
   - **Host site (`<sub>.<SITE_ROOT_DOMAIN>`):** tra cứu site `PUBLISHED` của shop `ACTIVE` theo luật `TRUST_PROXY_HEADERS` (mặc định `false`); nếu chưa xuất bản hoặc bị `SUSPENDED` → **404** cho cả trang lẫn `/api/**`; nếu hợp lệ thì cho `/api/{auth,cart,checkout,orders,payments,vouchers}/**` đi qua route handler, chặn `/seller/**`, `/admin/**`, `/api/seller/**`, `/api/admin/**` bằng **404**, rewrite tuyến trang sang `/s/<host>/...`, và chuyển hướng các tuyến bảo vệ (`/tai-khoan`, `/don-hang`, `/thanh-toan/cho/...`) sang `/dang-nhap?next=...` **trên cùng host site**.
   - **Host gốc (`nexusmart.localhost`, `localhost`, IP):** trả **404** cho toàn bộ `/api/{cart,checkout,orders,payments,vouchers}/**`; giữ `/api/{auth,seller,admin}/**` và bảo vệ `/seller/**`, `/admin/**`.
2. **BFF người mua suy gian hàng duy nhất từ `Host` (`src/lib/server/{sites,storefront,http}.ts`, `src/features/commerce/checkoutPayload.ts`, `src/app/api/{cart,checkout,orders,payments,vouchers}/**`):**
   - `resolveSiteFromRequest(req|headers)` chỉ lấy `{ siteId, shopId, host }` từ `Host` + site `PUBLISHED` (`fresh: true` cho checkout và thao tác ghi đơn/payment); bỏ qua/từ chối mọi `shop_id`, `sales_channel`, `storefront_shop_id` giả mạo từ client.
   - `cart`: cô lập dòng theo `site.shopId`, từ chối SKU gian hàng khác (`404`).
   - `checkout`: luôn khóa `sales_channel = "STOREFRONT"` và `storefront_shop_id = site.shopId`, từ chối `400` voucher sàn / freeship sàn / voucher shop khác / xu (`coins_to_use > 0`). Xóa `/api/vouchers/coins`.
   - `orders` & `payments`: lọc `sales_channel=STOREFRONT&storefront_shop_id=site.shopId` ở backend + xác minh defense-in-depth ở BFF; đơn/payment thuộc site khác hoặc người mua khác đều trả `404`.
3. **Phiên độc lập theo host, CSRF & SEO:**
   - Cookie phiên giữ `HttpOnly`, `SameSite=Lax`, **host-only (không `Domain=`)**, refresh rotation độc lập theo từng host.
   - `assertSameOrigin` yêu cầu `Origin` khớp đúng host + proto đang phục vụ (site B gọi site A, host gốc gọi site, site gọi host gốc, `Origin: null`, thiếu `Origin`, cross-site `Sec-Fetch-Site`, giả mạo `X-Forwarded-*` → `403 BAD_ORIGIN`).
   - `buildSiteProductMetadata` đặt `<link rel="canonical">` về **chính URL trên site** (`<siteOrigin>/san-pham/<slug>`); giữ `noindex` cho site chưa có section nội dung hiển thị.

### 1.3 Host gốc nền tảng (`web/src/app/(shop)/**`, `src/features/platform/**`, `robots.ts`, `sitemap.ts`)
- **Trang chủ `/` (`PlatformHome`, `SiteDirectorySection`, `SubdomainPreview`):** 6 section đúng art direction *Frost & Slate* + `Work Sans` cho hai đối tượng: Người bán (CTA chính `"Mở gian hàng"` → `/dang-ky?role=seller`, xem thử tên miền phụ, năng lực giao diện/COD/VietQR/34 tỉnh/đối soát, lộ trình 4 bước) và Khách ghé (**Danh bạ gian hàng đang mở bán** nạp thật `no-store` từ `GET /api/v1/sites` có phân trang `?trang=`, kèm logo, tên, tagline, ảnh bìa, link thẳng subdomain). Không có lưới sản phẩm, giá VND, giỏ hàng, tìm kiếm sản phẩm hay số liệu/logo/lời khen bịa.
- **Tuyến gốc giữ lại & `308`:** `/dang-nhap`, `/dang-ky` (tái dùng `AuthForms` `variant="platform"`, dẫn người mua đăng nhập tại host gốc về `/#danh-ba-gian-hang`), `/ve-nexusmart`, `/tro-giup`, cùng route handler `308 Permanent Redirect` cho `/gian-hang/[slug]` và `/san-pham/[slug]` khi shop có site `PUBLISHED` (ngược lại `404`).
- **SEO theo `Host` (`robots.ts`, `sitemap.ts`):** host gốc liệt kê các trang giới thiệu nền tảng + link các site `PUBLISHED`; host site chỉ liệt kê trang và sản phẩm thuộc đúng shop đó.
- **Dọn mã chết:** xóa `src/components/{ShopHeader,ShopFooter}.{tsx,module.css}`, `src/mock/**`, `src/data/{admin,chat,content,membership}.ts`.

### 1.4 Host site cửa hàng độc lập (`web/src/app/(site)/s/[host]/**`, `src/features/{site,commerce,account}/**`, `StoreContext.tsx`)
- Bọc toàn bộ **18 trang** dưới `web/src/app/(site)/s/[host]/layout.tsx` + `SiteShell`/`SiteChrome` + token `--site-*` (hỗ trợ cả giao diện sáng lẫn tối): chuyển trọn vẹn `/tim-kiem`, `/flash-sale`, `/voucher`, `/gio-hang`, `/thanh-toan`, `/thanh-toan/cho/[id]`, `/tai-khoan`, `/don-hang`, `/don-hang/[id]`, `/tra-cuu-don-hang`, `/tra-cuu-don`, và bổ sung `/dang-nhap`, `/dang-ky` (OTP) trên chính host site.
- Nút Thêm giỏ / Mua ngay trên PDP gọi thẳng `/api/cart/items` cùng host site (không còn chuyển hướng về host gốc); chưa đăng nhập dẫn sang `/dang-nhap?next=...` cùng host.
- Loại bỏ hoàn toàn giỏ nhiều gian hàng, voucher sàn, freeship sàn, xu, hạng hội viên, thẻ quà tặng, chat/AI mock khỏi `StoreContext` và luồng người mua. Xóa code sàn chết (`features/product/**`, `features/catalog/{CatalogExplorer,SizeFinder}.*`, `features/commerce/{StorefrontBanner,BuyerChatPanel,AiAssistantPanel,OverlayPanel}.*`).

### 1.5 Seller Center & Admin Portal (`web/src/app/(seller)/**`, `web/src/app/(admin)/**`, `src/features/{seller,admin}/**`)
- **Seller Center (`/seller/**`):** hiển thị rõ trạng thái site (`Đang hoạt động` / `Bản nháp` / `Chưa thiết lập` / `Tạm khóa`), nút `"Xem site ↗"` mở đúng URL subdomain và cảnh báo nổi bật khi chưa xuất bản/bị khóa. Bỏ các khối cấp sàn (`PromotionsBundleTab`, `PromotionsCampaignTab`, tab `Lookbook & Bài viết`, freeship sàn `249k`), giữ Voucher `SHOP` và Đăng ký Flash sale của gian hàng.
- **Admin Portal (`/admin/**`):** thu gọn còn **7 màn vận hành thật** (`/admin`, `/admin/gian-hang`, `/admin/kiem-duyet`, `/admin/sites`, `/admin/flash-sale` mới, `/admin/doi-soat`, `/admin/audit-log`), xóa 4 màn mock/sàn chết (`/admin/{cau-hinh,khieu-nai,noi-dung,thanh-vien}`) cùng `api/admin/vouchers`, `auditStore.ts`, `ToggleSwitch.tsx`. Giữ bộ lọc chỉ-đọc xem đơn lịch sử kênh `Sàn NexusMart` bên cạnh `Site riêng`.

### 1.6 QA E2E Web (`tests/e2e-web/**`)
- Viết lại trọn bộ 4 suite (**25 test cases**) gọi qua header `Host` theo mô hình mới:
  1. `01_root_platform.test.mjs` (7 tests): directory REST, `/` host gốc không có giá/SP, 22 URL người mua cũ `404`, `308` `/gian-hang/<slug>` & `/san-pham/<slug>`, 14 buyer API `404` trên host gốc, RBAC Seller/Admin + lọc lịch sử kênh, `robots.txt`/`sitemap.xml`.
  2. `02_site_commerce.test.mjs` (6 tests): trang công khai & redirect bảo vệ cùng host, đăng ký + OTP Mailpit → đăng nhập cookie `HttpOnly` host-only (0 `Domain=`), giỏ + voucher `SHOP` → COD (`STOREFRONT`), VietQR mock + webhook HMAC → PAID/CONFIRMED, idempotency checkout, refresh rotation & logout độc lập theo host.
  3. `03_isolation_and_security.test.mjs` (10 tests): host lạ/cấm/đa cấp `404`, chặn `/s/*`/`/seller`/`/admin`/`/api/{seller,admin}/*` trên host site, 4 theme khác biệt + PDP chéo shop `404`, cô lập cookie chéo site, từ chối SKU/voucher shop B & voucher sàn + `/api/vouchers/coins` `404`, cô lập đơn & payment chéo site cho cùng tài khoản (`404`), IDOR 2 người mua cùng site, 15 endpoint không phiên `401`, CSRF chéo host/spoofed proxy `403`, chống tampering `sales_channel=MARKETPLACE`/`storefront_shop_id`/khuyến mãi sàn.
  4. `04_site_lifecycle_and_seo.test.mjs` (2 tests): vòng đời draft/preview/publish/rollback, cross-seller RBAC, theme validation/XSS, `noindex` site rỗng, Admin tạm khóa/mở khóa chặn cả trang lẫn toàn bộ buyer API bằng `404`, SEO self-canonical PDP & `robots.txt`/`sitemap.xml` theo host.

### 1.7 Kiểm định trực quan & Look-and-Fix (`reviewer` + `web-site` + `qa`, xác minh lại bởi `verifier`)
- Vòng 1 `inspect.mjs` phát hiện `7 FAIL` trên 5/17 URL `(site)` (`[covered-text@390]` do header mobile sticky 4–5 hàng, `[body-size@1440]` trên PDP, `[focal-point@1440]` & `[still-page@1440]` trên `/dang-nhap`) cùng 5 lỗi nhìn thấy trên dark theme (`[contrast]` `"Xem kho mã"` `3.5:1`, trạng thái active vận chuyển/tab lọc đơn chìm nền tối, lộ `"Mock Express"`, lặp phường/tỉnh, cắt placeholder tìm kiếm).
- Đã khắc phục trọn gói (gom header site di động `<= 1024px` về 1 hàng gọn `58px` + drawer, chuẩn hóa cỡ chữ/tiêu điểm/chuyển động, nâng tương phản dark theme, đổi nhãn hiển thị sang `"Giao hàng NexusFast"` & chống lặp địa chỉ), rebuild container `web` (`:3300`) và xác minh độc lập đạt **17/17 URL · 0 FAIL**.

---

## 2. Danh sách đầy đủ các route đã thay đổi (`web/src/app`: `111 → 100` tệp `page.tsx`/`route.ts`)

Đối chiếu trực tiếp bằng `git diff --name-status b103cd9 -- web/src/app` (từ commit nền Giai đoạn 2 `b103cd9` tới bản hiện hành): tổng số tệp `page.tsx` + `route.ts` giảm từ **`111` → `100`** (gỡ **`27`** tệp, thêm **`16`** tệp, giữ lại **`84`** tệp; ngoài ra giữ `app/robots.ts`, `app/sitemap.ts` và bổ sung `app/(site)/s/[host]/layout.tsx`).

### 2.1 Route đã xóa hoàn toàn (14 tệp — chỉ phục vụ mô hình sàn hoặc mock chết)
- **8 trang sàn cũ trên host gốc `(shop)`:**
  1. `web/src/app/(shop)/danh-muc/page.tsx`
  2. `web/src/app/(shop)/bo-suu-tap/page.tsx`
  3. `web/src/app/(shop)/bo-suu-tap/[slug]/page.tsx`
  4. `web/src/app/(shop)/chien-dich/[slug]/page.tsx`
  5. `web/src/app/(shop)/tap-chi/page.tsx`
  6. `web/src/app/(shop)/tap-chi/[slug]/page.tsx`
  7. `web/src/app/(shop)/hoi-vien/page.tsx`
  8. `web/src/app/(shop)/the-qua-tang/page.tsx`
- **4 màn mock/sàn chết trên `(admin)`:**
  9. `web/src/app/(admin)/admin/cau-hinh/page.tsx`
  10. `web/src/app/(admin)/admin/khieu-nai/page.tsx`
  11. `web/src/app/(admin)/admin/noi-dung/page.tsx`
  12. `web/src/app/(admin)/admin/thanh-vien/page.tsx`
- **2 API route cấp sàn:**
  13. `web/src/app/api/admin/vouchers/route.ts`
  14. `web/src/app/api/vouchers/coins/route.ts`

### 2.2 Route đã chuyển từ `(shop)` sang `(site)/s/[host]/` (11 trang chuyển + 2 trang auth mới trên site)
- **11 trang mua sắm/hậu mãi đã xóa khỏi `(shop)` và dựng lại trong `(site)/s/[host]/`:**
  1. `gio-hang/page.tsx` → `web/src/app/(site)/s/[host]/gio-hang/page.tsx`
  2. `thanh-toan/page.tsx` → `web/src/app/(site)/s/[host]/thanh-toan/page.tsx`
  3. `thanh-toan/cho/[id]/page.tsx` → `web/src/app/(site)/s/[host]/thanh-toan/cho/[id]/page.tsx`
  4. `don-hang/page.tsx` → `web/src/app/(site)/s/[host]/don-hang/page.tsx`
  5. `don-hang/[id]/page.tsx` → `web/src/app/(site)/s/[host]/don-hang/[id]/page.tsx`
  6. `tai-khoan/page.tsx` → `web/src/app/(site)/s/[host]/tai-khoan/page.tsx`
  7. `tra-cuu-don-hang/page.tsx` → `web/src/app/(site)/s/[host]/tra-cuu-don-hang/page.tsx`
  8. `tra-cuu-don/page.tsx` → `web/src/app/(site)/s/[host]/tra-cuu-don/page.tsx`
  9. `tim-kiem/page.tsx` → `web/src/app/(site)/s/[host]/tim-kiem/page.tsx`
  10. `flash-sale/page.tsx` → `web/src/app/(site)/s/[host]/flash-sale/page.tsx`
  11. `voucher/page.tsx` → `web/src/app/(site)/s/[host]/voucher/page.tsx`
- **2 trang xác thực riêng trên host site (thêm mới):**
  12. `web/src/app/(site)/s/[host]/dang-nhap/page.tsx`
  13. `web/src/app/(site)/s/[host]/dang-ky/page.tsx`
- **Layout dùng chung mới cho site:** `web/src/app/(site)/s/[host]/layout.tsx`

### 2.3 Route đã chuyển sang `308 Permanent Redirect` (2 tệp)
1. `web/src/app/(shop)/gian-hang/[slug]/page.tsx` (xóa) → `web/src/app/(shop)/gian-hang/[slug]/route.ts` (`308` sang `http(s)://<primary_host>[:port]/` nếu site `PUBLISHED`, ngược lại `404`)
2. `web/src/app/(shop)/san-pham/[slug]/page.tsx` (xóa) → `web/src/app/(shop)/san-pham/[slug]/route.ts` (`308` sang `http(s)://<primary_host>[:port]/san-pham/<slug>` nếu site `PUBLISHED`, ngược lại `404`)

### 2.4 Route giữ lại & thêm mới khác
- **Host gốc `(shop)` giữ lại (5 trang + `robots.ts`, `sitemap.ts`):** `/`, `/dang-nhap`, `/dang-ky`, `/ve-nexusmart`, `/tro-giup`.
- **Host site `(site)/s/[host]` giữ lại (5 trang):** `/`, `/san-pham`, `/san-pham/[slug]`, `/chinh-sach`, `/gioi-thieu`.
- **Seller Center `(seller)` giữ lại (13 trang):** `/seller`, `/seller/san-pham`, `/seller/san-pham/moi`, `/seller/san-pham/[id]`, `/seller/don-hang`, `/seller/don-hang/[id]`, `/seller/khuyen-mai`, `/seller/tai-chinh`, `/seller/site`, `/seller/site/xem-truoc`, `/seller/chat`, `/seller/ho-so`, `/seller/tuong-tac`.
- **Admin Portal `(admin)` (6 trang giữ lại + 1 trang thêm mới):** giữ `/admin`, `/admin/gian-hang`, `/admin/kiem-duyet`, `/admin/sites`, `/admin/doi-soat`, `/admin/audit-log`; **thêm mới** `web/src/app/(admin)/admin/flash-sale/page.tsx`.
- **BFF API (`src/app/api/**`):** giữ **55 route handler** (`12` admin, `9` auth, `3` cart, `4` checkout, `5` orders, `2` payments, `16` seller, `4` vouchers — phân nhánh chặt theo `Host` tại middleware & route).

---

## 3. Bằng chứng từng Gate (đầu ra thật nguyên văn)

### Gate 1 — Bất biến kho mã nguồn, Chính sách & Môi trường

```text
=== GATE 1: PRE-CHECK INVARIANTS ===
branch: phase-0b
head: b56f8bb wip(4): checkpoint while agy swarm runs (2026-11-21 10:53)
ci.yml triggers:
5:on:
6:  workflow_dispatch:
9:  group: ci-${{ github.ref }}
89:  web:
--- diff runtime deps ---
0
--- diff next-env/tsconfig ---
0
--- web/.next mtime (BEFORE) ---
1795179956 2026-11-20 20:05:56.273800486 +0700
--- next dirs in web/ ---
drwxr-xr-x 8 dev dev 4096 Nov 20 20:05 web/.next
drwxr-xr-x 8 dev dev 4096 Nov 20 16:43 web/.next-platform
--- ss -tlnp 3111/4777 ---
LISTEN 0      5                                   127.0.0.1:4777       0.0.0.0:*    users:(("python3",pid=983103,fd=3))          
LISTEN 0      5                                 100.93.43.5:4777       0.0.0.0:*    users:(("socat",pid=2938570,fd=5))           
LISTEN 0      511                               100.93.43.5:3111       0.0.0.0:*    users:(("next-server (v1",pid=2820214,fd=21))
--- docker compose 5005x published count ---
0
--- Gateway CORS check: OPTIONS /healthz ---
(0 Access-Control-* headers)
--- Gateway CORS check: GET /api/v1/sites ---
(0 Access-Control-* headers)
--- docker compose ps ---
NAME                          IMAGE                                          COMMAND                  SERVICE          CREATED             STATUS                       PORTS
nexus_ecom-cart-1             nexus_ecom-cart                                "/usr/local/bin/cart"    cart             15 hours ago        Up 15 hours (healthy)        50053/tcp, 127.0.0.1:28083->8083/tcp
nexus_ecom-gateway-1          nexus_ecom-gateway                             "/usr/local/bin/gate…"   gateway          About an hour ago   Up About an hour (healthy)   0.0.0.0:28080->8080/tcp
nexus_ecom-mailpit-1          axllent/mailpit:v1.20                          "/mailpit"               mailpit          16 hours ago        Up 16 hours (healthy)        127.0.0.1:1025->1025/tcp, 127.0.0.1:8025->8025/tcp, 1110/tcp
nexus_ecom-nats-1             nats:2.10-alpine                               "docker-entrypoint.s…"   nats             16 hours ago        Up 16 hours (healthy)        127.0.0.1:4222->4222/tcp, 127.0.0.1:8222->8222/tcp, 6222/tcp
nexus_ecom-notification-1     nexus_ecom-notification                        "/usr/local/bin/noti…"   notification     About an hour ago   Up About an hour (healthy)   127.0.0.1:8088->8088/tcp, 50058/tcp
nexus_ecom-order-1            nexus_ecom-order                               "/app/order-server"      order            About an hour ago   Up About an hour (healthy)   50055/tcp, 127.0.0.1:28085->8085/tcp
nexus_ecom-otel-collector-1   otel/opentelemetry-collector-contrib:0.108.0   "/otelcol-contrib --…"   otel-collector   16 hours ago        Up 16 hours                  127.0.0.1:4317->4317/tcp, 127.0.0.1:8889->8889/tcp, 55678-55679/tcp, 127.0.0.1:24318->4318/tcp
nexus_ecom-payment-1          nexus_ecom-payment                             "/usr/local/bin/nexu…"   payment          16 hours ago        Up 16 hours (healthy)        127.0.0.1:8086->8086/tcp, 50056/tcp
nexus_ecom-postgres-1         postgres:16-alpine                             "docker-entrypoint.s…"   postgres         15 hours ago        Up 15 hours (healthy)        127.0.0.1:5432->5432/tcp
nexus_ecom-product-1          nexus_ecom-product                             "/usr/local/bin/prod…"   product          15 hours ago        Up 15 hours (healthy)        50052/tcp, 127.0.0.1:28082->8082/tcp
nexus_ecom-promotion-1        nexus_ecom-promotion                           "/usr/local/bin/prom…"   promotion        15 hours ago        Up 15 hours (healthy)        127.0.0.1:8084->8084/tcp, 50054/tcp
nexus_ecom-redis-1            redis:7-alpine                                 "docker-entrypoint.s…"   redis            16 hours ago        Up 16 hours (healthy)        127.0.0.1:26379->6379/tcp
nexus_ecom-shipping-1         nexus_ecom-shipping                            "/usr/local/bin/ship…"   shipping         15 hours ago        Up 15 hours (healthy)        127.0.0.1:8087->8087/tcp, 50057/tcp
nexus_ecom-user-1             nexus_ecom-user                                "/usr/local/bin/user"    user             About an hour ago   Up About an hour (healthy)   127.0.0.1:8081->8081/tcp, 50051/tcp
nexus_ecom-web-1              nexus_ecom-web                                 "docker-entrypoint.s…"   web              17 minutes ago      Up 17 minutes (healthy)      0.0.0.0:3300->3000/tcp
```

Xác nhận hậu kiểm sau khi kết thúc toàn bộ 7 Gate:
- `stat -c '%Y %y' web/.next`: **trước** `1795179956 2026-11-20 20:05:56.273800486 +0700` → **sau mọi gate** `1795179956 2026-11-20 20:05:56.273800486 +0700` (**bất biến 100%**).
- `git diff -- web/package.json web/package-lock.json '**/go.mod' 'go.mod' services/payment/Cargo.toml services/payment/Cargo.lock`: **0 dòng**.
- `git diff -- web/next-env.d.ts web/tsconfig.json`: **0 dòng**.
- `.github/workflows/ci.yml`: chỉ giữ duy nhất trigger `on: workflow_dispatch:` (không `push`, `pull_request`, `schedule`).
- Cổng `3111` (`next-server`, PID `2820214`) và cổng `4777` (`python3`/`socat`) nguyên vẹn; cổng proxy tạm `3420` đã đóng sạch.

---

### Gate 2 — Backend Static & Unit (`make lint`, `gofmt`, `node --check`, `make test`)

```text
=== GATE 2: BACKEND STATIC & UNIT ===
--- make lint ---
buf lint
==> gofmt & golangci-lint pkg/common
==> gofmt & golangci-lint gen/go
==> gofmt & golangci-lint services/gateway
==> gofmt & golangci-lint services/user
==> gofmt & golangci-lint services/product
==> gofmt & golangci-lint services/cart
==> gofmt & golangci-lint services/promotion
==> gofmt & golangci-lint services/order
==> gofmt & golangci-lint services/shipping
==> gofmt & golangci-lint services/notification
==> rustfmt & clippy services/payment
    Finished `dev` profile [unoptimized + debuginfo] target(s) in 0.63s
--- gofmt -l tests/e2e ---
tests/e2e gofmt clean
--- node --check tests/e2e-web ---
tests/e2e-web syntax clean (10 files)
--- make test ---
==> go test pkg/common
ok  	github.com/nexusmart/core/pkg/common/auth	(cached)
ok  	github.com/nexusmart/core/pkg/common/env	(cached)
ok  	github.com/nexusmart/core/pkg/common/logger	(cached)
ok  	github.com/nexusmart/core/pkg/common/metrics	(cached)
ok  	github.com/nexusmart/core/pkg/common/telemetry	(cached)
==> go test gen/go
?   	github.com/nexusmart/core/gen/go/nexusmart/cart/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/notification/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/order/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/payment/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/product/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/promotion/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/shipping/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/user/v1	[no test files]
==> go test services/gateway
?   	github.com/nexusmart/core/services/gateway/cmd/gateway	[no test files]
?   	github.com/nexusmart/core/services/gateway/internal/clients	[no test files]
?   	github.com/nexusmart/core/services/gateway/internal/config	[no test files]
ok  	github.com/nexusmart/core/services/gateway/internal/httpx	(cached)
ok  	github.com/nexusmart/core/services/gateway/internal/middleware	(cached)
ok  	github.com/nexusmart/core/services/gateway/internal/proxy	(cached)
ok  	github.com/nexusmart/core/services/gateway/internal/router	(cached)
==> go test services/user
?   	github.com/nexusmart/core/services/user/cmd/user	[no test files]
?   	github.com/nexusmart/core/services/user/internal/config	[no test files]
?   	github.com/nexusmart/core/services/user/internal/db	[no test files]
?   	github.com/nexusmart/core/services/user/internal/events	[no test files]
ok  	github.com/nexusmart/core/services/user/internal/password	(cached)
ok  	github.com/nexusmart/core/services/user/internal/security	(cached)
?   	github.com/nexusmart/core/services/user/migrations	[no test files]
ok  	github.com/nexusmart/core/services/user/internal/service	(cached)
ok  	github.com/nexusmart/core/services/user/internal/site	(cached)
==> go test services/product
?   	github.com/nexusmart/core/services/product/cmd/product	[no test files]
?   	github.com/nexusmart/core/services/product/internal/db	[no test files]
?   	github.com/nexusmart/core/services/product/internal/store	[no test files]
?   	github.com/nexusmart/core/services/product/migrations	[no test files]
ok  	github.com/nexusmart/core/services/product/internal/server	(cached)
==> go test services/cart
?   	github.com/nexusmart/core/services/cart/cmd/cart	[no test files]
ok  	github.com/nexusmart/core/services/cart/internal/resilience	(cached)
?   	github.com/nexusmart/core/services/cart/internal/store	[no test files]
ok  	github.com/nexusmart/core/services/cart/internal/server	(cached)
==> go test services/promotion
?   	github.com/nexusmart/core/services/promotion/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/promotion/internal/config	[no test files]
?   	github.com/nexusmart/core/services/promotion/internal/domain	[no test files]
ok  	github.com/nexusmart/core/services/promotion/internal/calculator	(cached)
ok  	github.com/nexusmart/core/services/promotion/internal/events	(cached)
ok  	github.com/nexusmart/core/services/promotion/internal/lua	(cached)
?   	github.com/nexusmart/core/services/promotion/internal/store	[no test files]
ok  	github.com/nexusmart/core/services/promotion/internal/service	(cached)
==> go test services/order
?   	github.com/nexusmart/core/services/order/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/order/internal/config	[no test files]
?   	github.com/nexusmart/core/services/order/internal/db	[no test files]
?   	github.com/nexusmart/core/services/order/internal/grpcserver	[no test files]
ok  	github.com/nexusmart/core/services/order/internal/clients	(cached)
ok  	github.com/nexusmart/core/services/order/internal/events	(cached)
?   	github.com/nexusmart/core/services/order/internal/store	[no test files]
?   	github.com/nexusmart/core/services/order/migrations	[no test files]
ok  	github.com/nexusmart/core/services/order/internal/service	(cached)
ok  	github.com/nexusmart/core/services/order/internal/statemachine	(cached)
==> go test services/shipping
?   	github.com/nexusmart/core/services/shipping/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/shipping/internal/cache	[no test files]
ok  	github.com/nexusmart/core/services/shipping/internal/address	(cached)
?   	github.com/nexusmart/core/services/shipping/internal/config	[no test files]
?   	github.com/nexusmart/core/services/shipping/internal/events	[no test files]
?   	github.com/nexusmart/core/services/shipping/internal/poller	[no test files]
?   	github.com/nexusmart/core/services/shipping/internal/store	[no test files]
?   	github.com/nexusmart/core/services/shipping/migrations	[no test files]
ok  	github.com/nexusmart/core/services/shipping/internal/carrier	(cached)
ok  	github.com/nexusmart/core/services/shipping/internal/grpcserver	(cached)
ok  	github.com/nexusmart/core/services/shipping/internal/service	(cached)
ok  	github.com/nexusmart/core/services/shipping/internal/webhook	(cached)
==> go test services/notification
?   	github.com/nexusmart/core/services/notification/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/notification/internal/config	[no test files]
?   	github.com/nexusmart/core/services/notification/internal/db	[no test files]
ok  	github.com/nexusmart/core/services/notification/internal/consumer	(cached)
ok  	github.com/nexusmart/core/services/notification/internal/grpcserver	(cached)
ok  	github.com/nexusmart/core/services/notification/internal/mailer	(cached)
?   	github.com/nexusmart/core/services/notification/internal/store	[no test files]
ok  	github.com/nexusmart/core/services/notification/internal/service	(cached)
?   	github.com/nexusmart/core/services/notification/migrations	[no test files]
ok  	github.com/nexusmart/core/services/notification/internal/userclient	(cached)
==> cargo test services/payment
    Finished `test` profile [unoptimized + debuginfo] target(s) in 2.45s
     Running unittests src/lib.rs (target/debug/deps/nexusmart_payment-e82bf25ec01a60de)

running 39 tests
test auth::tests::rejects_empty_secrets ... ok
test domain::tests::test_multi_shop_single_suborder_cancel_refund_scope ... ok
test domain::tests::test_reconciliation_classifications ... ok
test domain::tests::test_allocation_split_exact_vnd ... ok
test domain::tests::test_refund_cap_enforced ... ok
test domain::tests::test_reconciliation_channel_matches_partitions_100_percent ... ok
test domain::tests::test_per_suborder_refund_cap_prevents_multi_shop_double_refund ... ok
test domain::tests::test_allocation_rejects_excessive_commission ... ok
test grpc::tests::test_proto_timestamp_roundtrip ... ok
test grpc::tests::test_parse_optional_uuid_validation ... ok
test domain::tests::test_storefront_allocation_validation_rejects_cross_shop ... ok
test ledger::tests::test_unbalanced_transaction_rejected ... ok
test ledger::tests::test_zero_or_negative_or_single_line_rejected ... ok
test domain::tests::test_sales_channel_validation_and_defaults ... ok
test grpc::tests::test_to_proto_payment_legacy_default_and_storefront_mapping ... ok
test telemetry::tests::test_extract_trace_id_from_w3c_traceparent ... ok
test auth::tests::spoofed_metadata_without_jwt_is_unauthenticated ... ok
test ledger::tests::test_sales_channel_ledger_partitioning_matches_unfiltered_100_percent ... ok
test webhook_security::tests::test_extract_order_code_from_sepay_transfer_content ... ok
test ledger::tests::test_full_lifecycle_ledger_stays_balanced_across_all_scenarios ... ok
test webhook_security::tests::test_contracts_canonical_webhook_payload_deserializes ... ok
test webhook_security::tests::test_webhook_10x_idempotency_simulation ... ok
test webhook_security::tests::test_webhook_amount_mismatch_and_late_payment_decisions ... ok
test workers::tests::test_nats_retry_until_max_deliver_then_dlq ... ok
test auth::tests::user_jwt_wins_over_metadata ... ok
test webhook_security::tests::test_replay_protection_timestamp_window ... ok
test auth::tests::http_auth_enforces_user_jwt_and_admin_role ... ok
test webhook_security::tests::test_hmac_verification_valid_and_invalid ... ok
test auth::tests::service_tokens_are_verified_and_scoped ... ok
test vietqr::tests::test_circuit_breaker_transitions ... ok
test telemetry::tests::test_masking_pii_and_secrets ... ok
test vietqr::tests::test_vietqr_mock_deterministic_payload_and_valid_hmac ... ok
test grpc::tests::test_grpc_validation_and_red_metrics_recorded ... ok
test grpc::tests::test_grpc_sales_channel_validation_on_create_summary_and_reconcile ... ok
test grpc::tests::test_grpc_defense_in_depth_rbac_enforcement ... ok
test grpc::tests::test_b1_all_rpcs_reject_anonymous_and_spoofed_metadata_and_disallowed_service_tokens ... ok
test http::tests::test_healthz_and_red_metrics_no_permissive_cors ... ok
test http::tests::test_webhook_rejects_missing_sig_missing_timestamp_invalid_sig_and_stale_ts ... ok
test http::tests::test_b1_http_sse_and_internal_routes_reject_anonymous_spoofed_and_non_admin ... ok

test result: ok. 39 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.10s

     Running unittests src/main.rs (target/debug/deps/nexusmart_payment-b11deacfd41411c1)

running 0 tests

test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.01s

     Running tests/sales_channel_db_integration.rs (target/debug/deps/sales_channel_db_integration-0cb2110ad8e260d1)

running 1 test
test test_pg_migration_immutability_and_channel_filtered_ledger_and_reconciliation ... ok

test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.05s

   Doc-tests nexusmart_payment

running 0 tests

test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s

--- web/.next mtime after Gate 2 ---
1795179956 2026-11-20 20:05:56.273800486 +0700
```

---

### Gate 3 — Web Static, 77 Unit Tests, Isolated Build (`NEXT_DIST_DIR=.next-verifier`) & Design Taste Lint

Chạy `npx tsc --noEmit` và `NEXT_DIST_DIR=.next-verifier npm run build` trong bản sao sạch `/tmp/nexus-web-verify-build` (loại trừ `.next*`, symlink `node_modules`, xóa sạch ngay sau khi chạy) nhằm bảo toàn tuyệt đối `web/.next` (`1795179956`) của server cổng `3111` và giữ `git diff -- web/tsconfig.json web/next-env.d.ts` = `0` dòng (chi tiết tại Mục 6.1).

```text
=== GATE 3: WEB STATIC, UNIT & ISOLATED BUILD ===
--- web lint ---
npm notice run nexusmart-web@0.1.0 lint
npm notice run eslint .
--- web format:check ---
npm notice run nexusmart-web@0.1.0 format:check
npm notice run prettier --check "src/**/*.{ts,tsx,css}"
Checking formatting...
All matched files use Prettier code style!
--- web check-data.mjs ---
PASS  VND integer formatting
PASS  34 Vietnamese provinces and 2-level ward structure
PASS  18 products, 6 categories, 4 shops, exact image contract, integer VND
PASS  Shop voucher presentation data and validation
PASS  Flash sale slots and prices reconcile
PASS  Orders cover all 6 statuses and totals reconcile
PASS  Seller daily KPIs, order receivables, and double-entry ledger reconcile
PASS  Product metadata, gallery paths, and shop profiles
PASS  Vietnamese search synonyms and per-shop sizing helpers
PASS  Editorial models, banners, category tiles, and workshop media exist

ALL 10 DATA INTEGRITY CHECKS PASSED.
--- web npm run test:unit (all 11 *.test.mjs, Node v22.22.2) ---
npm notice run nexusmart-web@0.1.0 test:unit
npm notice run node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test "src/**/*.test.mjs" "scripts/**/*.test.mjs"
✔ mergeCategories keeps storefront slugs only, backend name wins (2.880308ms)
✔ mergeShops matches profile by name and keeps generic shops (0.460685ms)
✔ mergeProducts: real price/stock/sku ids, content kept, foreign categories dropped (0.549865ms)
✔ mergeProducts: unknown product built from attributes, invalid sizes skipped (0.438354ms)
✔ mergeVouchers keeps copy for known codes and real quota (0.595058ms)
✔ buildFlashSlots groups SKU items per product with VN clock and status (0.777685ms)
✔ mergeProducts keeps OUT_OF_STOCK products with 0 available_stock (0.210816ms)
✔ buildCatalogLookups and find helpers resolve by slug, id, backendId and skuId (0.33424ms)
✔ checkLiveVoucher validates real quota, min order, expiry, shop scope and discount cap (14.873139ms)
✔ filterStorefrontShops, mergeVouchers, and buildFlashSlots isolate storefront data from foreign E2E fixtures (0.881353ms)
✔ createTtlCache deduplicates in-flight calls, respects TTL, and never caches errors (0.858197ms)
✔ safeNextPath allows safe relative paths and rejects open redirects or auth loops (0.990523ms)
✔ mapGatewayAddress converts gateway DTO to AddressView (0.726857ms)
✔ toStorefrontProvinceCode maps numeric backend province codes (0.163275ms)
✔ site variant keeps a safe next path and falls back to the account page (3.107552ms)
✔ platform variant routes admins and sellers to their own area only (0.222543ms)
✔ platform variant sends buyers to the shop directory whatever next says (0.125605ms)
✔ maps a COD order with catalog presentation data (2.37637ms)
✔ falls back to backend names when the product is unknown to the catalog (1.331226ms)
✔ subtotal shown excludes the flash sale discount already applied to line prices (0.337152ms)
✔ VietQR pending order exposes payment due time and retry state (0.234086ms)
✔ cancel reason is shown only when the buyer cancelled (0.349467ms)
✔ return request reason strips the backend prefix; delivered allows confirm/return (0.376983ms)
✔ status helpers (0.2793ms)
✔ preserves sales_channel and storefront_shop_id with legacy MARKETPLACE default (0.339739ms)
✔ translatePromotionWarning maps English promotion warnings to clean Vietnamese without raw internals (15.781714ms)
✔ mapCheckoutError maps out-of-stock, COD limit, voucher exhausted, and empty cart safely (0.481313ms)
✔ mapCartError and mapVoucherError return clean buyer messages and status codes (0.270052ms)
✔ enrichCartGroups pairs backend cart groups with catalog presentation while preserving real prices and stock (0.567106ms)
✔ voucher formatting and eligibility reasons handle fixed, percent, exhausted, and min-order shop vouchers (0.497064ms)
✔ formatCarrierLabel and formatServiceLabel replace internal Mock Express copy with natural Vietnamese (0.625305ms)
✔ formatAddressLine avoids duplicating ward or province already embedded in street_address (1.245541ms)
✔ directory site keeps the request port and only safe image URLs (1.739278ms)
✔ directory site falls back to subdomain and rejects foreign or reserved hosts (0.493592ms)
✔ directory response dedupes rows, caps page size and derives page count (1.038673ms)
✔ page param parsing and compact pager (0.413693ms)
✔ subdomain suggestion strips Vietnamese marks and checks reserved labels (0.423803ms)
✔ legacy shop slug matches id, subdomain or the old profile name (0.455483ms)
✔ legacy redirect target stays on the site origin (0.202459ms)
✔ legacy HTTP responses use 308/404 with no-store and safe decoding (17.382126ms)
✔ all curated THEME_PRESETS pass WCAG 2.1 >= 4.5 on every required pair (1.977573ms)
✔ contrastRatio detects low-contrast combinations (0.59312ms)
✔ validateSubdomainClient accepts valid shop subdomains and blocks reserved/phishing names (0.22714ms)
✔ validateSafeUrlClient allows empty, single-slash relative, and https URLs only (0.203975ms)
✔ default config and all 7 section templates pass full draft validation (0.826148ms)
✔ validateSiteDraftClient rejects more than 20 sections (0.198599ms)
✔ buildPublicSiteUrl keeps the site host and borrows only the request port (1.147282ms)
✔ buildPublicSiteUrl rejects empty or unsafe hosts (0.206621ms)
✔ summarizeSellerSite maps backend site status to the seller-facing state (0.909256ms)
✔ buildStorefrontCheckoutPayload locks STOREFRONT + siteShopId and ignores spoofed channel/shop (1.371781ms)
✔ buildStorefrontCheckoutPayload supports legacy keyed shop_voucher_ids and shipping_choices (0.471936ms)
✔ buildStorefrontCheckoutPayload rejects foreign shop vouchers and platform/freeship/coins promos (0.197829ms)
✔ buildStorefrontCheckoutPayload validates siteShopId, address_id, and payment_method (0.144405ms)
{"level":"warn","msg":"bff_gateway_unavailable","service":"web-bff","trace_id":"abc-trace-secret"}
✔ assertSameOrigin accepts matching same-origin Origin header (18.169017ms)
✔ assertSameOrigin rejects cross-site, root-to-site, site-to-root, and foreign Origin with 403 BAD_ORIGIN (0.95027ms)
✔ assertSameOrigin rejects Origin: null (0.34825ms)
✔ assertSameOrigin rejects cross-site and same-site Sec-Fetch-Site (0.264841ms)
✔ assertSameOrigin rejects missing Origin and Referer (0.24184ms)
✔ assertSameOrigin rejects protocol mismatch between Origin and request (0.17565ms)
✔ assertSameOrigin accepts same-origin Referer only when Origin header is absent (0.240628ms)
✔ errorFromResponse and errorResponse never leak gateway internals or trace_id (2.595039ms)
✔ cookie configuration is HttpOnly, SameSite=Lax, host-only (no Domain) (0.328373ms)
✔ accessExpiresSoon detects missing, malformed, expired, and valid JWTs (0.411738ms)
✔ applyAuthCookiesToHeader replaces or removes auth cookies while preserving others (0.17932ms)
✔ toGatewayAddressBody normalizes camelCase/snake_case and never includes user_id (0.162945ms)
✔ TRUST_PROXY_HEADERS defaults to false and ignores spoofed x-forwarded-host/proto in CSRF checks (0.278887ms)
✔ assertSameOrigin honors x-forwarded-host/proto ONLY when TRUST_PROXY_HEADERS=true (0.227923ms)
✔ buildLoginRedirectUrl keeps site host and honors TRUST_PROXY_HEADERS only when enabled (0.352234ms)
✔ parsePublishedSiteHost accepts PUBLISHED + ACTIVE sites and rejects DRAFT/SUSPENDED/inactive (0.154891ms)
✔ storefront tenant scoping isolates cart, orders, payments, and vouchers to the site's shop (0.582038ms)
✔ normalizeHostHeader lowercases, strips port and trailing dot, handles IPv6, rejects malformed (2.134137ms)
✔ classifyHost identifies root hosts, valid single-label site hosts, and rejects reserved/unknown/multi-level (1.079495ms)
✔ site vs root API routing, blocked paths, protected pages, and direct /s internal paths are strictly enforced (0.509977ms)
✔ deriveMarketplaceOrigin and deriveSiteOrigin preserve proto/port cleanly (0.388628ms)
✔ createSiteHostCache handles positive TTL, negative TTL, error non-caching, inflight dedupe, and eviction (0.804056ms)
✔ WCAG contrast, safe URL sanitization, and CSS theme vars enforce security and readability (2.033415ms)
✔ SEO helpers enforce noindex for empty/hidden sites and site-own canonical on site PDP (0.622172ms)
ℹ tests 77
ℹ suites 0
ℹ pass 77
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 178.662315
--- isolated clean copy /tmp/nexus-web-verify-build (excluding .next*) ---
--- npx tsc --noEmit (pre-build, clean copy) ---
npm notice run nexusmart-web@0.1.0 npx
npm notice run 'tsc' --noEmit
tsc pre-build: EXIT 0 (0 errors)
--- NEXT_DIST_DIR=.next-verifier npm run build (clean copy) ---
npm notice run nexusmart-web@0.1.0 build
npm notice run next build
   ▲ Next.js 15.5.29

   Creating an optimized production build ...
 ✓ Compiled successfully in 9.5s
 ✓ Linting and checking validity of types 
 ✓ Collecting page data 
 ✓ Generating static pages (42/42)
 ✓ Collecting build traces 
 ✓ Finalizing page optimization 

Route (app)                                      Size  First Load JS
┌ ƒ /                                         5.89 kB         117 kB
├ ƒ /_not-found                                 998 B         104 kB
├ ƒ /admin                                    7.83 kB         146 kB
├ ƒ /admin/audit-log                           5.5 kB         144 kB
├ ƒ /admin/doi-soat                           7.18 kB         148 kB
├ ƒ /admin/flash-sale                         6.24 kB         147 kB
├ ƒ /admin/gian-hang                          6.95 kB         148 kB
├ ƒ /admin/kiem-duyet                         6.91 kB         148 kB
├ ƒ /admin/sites                              6.05 kB         153 kB
├ ƒ /api/* (55 BFF route handlers)              278 B         103 kB
├ ƒ /dang-ky                                  1.22 kB         144 kB
├ ƒ /dang-nhap                                 1.2 kB         144 kB
├ ƒ /gian-hang/[slug]                           278 B         103 kB
├ ƒ /robots.txt                                 278 B         103 kB
├ ƒ /s/[host]                                   765 B         121 kB
├ ƒ /s/[host]/chinh-sach                        765 B         121 kB
├ ƒ /s/[host]/dang-ky                           199 B         143 kB
├ ƒ /s/[host]/dang-nhap                         196 B         143 kB
├ ƒ /s/[host]/don-hang                        3.97 kB         148 kB
├ ƒ /s/[host]/don-hang/[id]                   6.24 kB         150 kB
├ ƒ /s/[host]/flash-sale                      5.72 kB         144 kB
├ ƒ /s/[host]/gio-hang                        9.66 kB         148 kB
├ ƒ /s/[host]/gioi-thieu                        765 B         121 kB
├ ƒ /s/[host]/san-pham                          765 B         121 kB
├ ƒ /s/[host]/san-pham/[slug]                   765 B         121 kB
├ ƒ /s/[host]/tai-khoan                       15.1 kB         165 kB
├ ƒ /s/[host]/thanh-toan                      12.5 kB         157 kB
├ ƒ /s/[host]/thanh-toan/cho/[id]             6.16 kB         144 kB
├ ƒ /s/[host]/tim-kiem                        2.77 kB         131 kB
├ ƒ /s/[host]/tra-cuu-don                       278 B         103 kB
├ ƒ /s/[host]/tra-cuu-don-hang                8.32 kB         152 kB
├ ƒ /s/[host]/voucher                         3.59 kB         142 kB
├ ƒ /san-pham/[slug]                            278 B         103 kB
├ ƒ /seller                                   9.26 kB         165 kB
├ ƒ /seller/chat                              4.81 kB         160 kB
├ ƒ /seller/don-hang                          5.24 kB         161 kB
├ ƒ /seller/don-hang/[id]                     5.47 kB         161 kB
├ ƒ /seller/ho-so                              7.3 kB         163 kB
├ ƒ /seller/khuyen-mai                        6.83 kB         162 kB
├ ƒ /seller/san-pham                          5.44 kB         161 kB
├ ƒ /seller/san-pham/[id]                       152 B         163 kB
├ ƒ /seller/san-pham/moi                        151 B         163 kB
├ ƒ /seller/site                              11.2 kB         173 kB
├ ƒ /seller/site/xem-truoc                    1.19 kB         147 kB
├ ƒ /seller/tai-chinh                         4.34 kB         160 kB
├ ƒ /seller/tuong-tac                         7.63 kB         163 kB
├ ƒ /sitemap.xml                                278 B         103 kB
├ ƒ /tro-giup                                 8.16 kB         123 kB
└ ƒ /ve-nexusmart                             7.78 kB         122 kB
+ First Load JS shared by all                  103 kB
  ├ chunks/581-77e44b6083dc4cdd.js            46.4 kB
  ├ chunks/c5be14a5-249233ad85a90506.js       54.2 kB
  └ other shared chunks (total)               2.21 kB

ƒ Middleware                                  38.2 kB

--- npx tsc --noEmit (post-build, clean copy) ---
npm notice run nexusmart-web@0.1.0 npx
npm notice run 'tsc' --noEmit
tsc post-build: EXIT 0 (0 errors)
Cleaned /tmp/nexus-web-verify-build
--- design-taste-frontend lint.mjs web/src ---
TASTESKILL · LINT   289 file(s)

WARN
  [numbering] Zero-padding in script. Fine for a clock, a date or a price; if it builds 01 / 02 / 03 labels, remove it.  (10×)
      web/src/app/(site)/s/[host]/thanh-toan/cho/[id]/PaymentWaitingClient.tsx:31  padStart(2, "0")
      web/src/features/admin/AdminOverviewClient.tsx:54  padStart(2, "0")
      web/src/features/admin/AdminOverviewClient.tsx:55  padStart(2, "0")
      web/src/features/admin/AdminOverviewClient.tsx:66  padStart(2, "0")
      web/src/features/seller/SellerOverviewClient.tsx:82  padStart(2, "0")
      web/src/features/seller/SellerOverviewClient.tsx:83  padStart(2, "0")
      … 4 more
  [em-dash] Em-dash habit. Use a full stop or a comma; keep at most one or two on a page.  (1×)
      (all files):0  5 em dashes

RESULT: 0 FAIL, 11 WARN
--- design-taste-frontend lint.mjs --app seller+admin ---
TASTESKILL · LINT   83 file(s)

WARN
  [numbering] Zero-padding in script. Fine for a clock, a date or a price; if it builds 01 / 02 / 03 labels, remove it.  (6×)
      web/src/features/seller/SellerOverviewClient.tsx:82  padStart(2, "0")
      web/src/features/seller/SellerOverviewClient.tsx:83  padStart(2, "0")
      web/src/features/seller/SellerOverviewClient.tsx:103  padStart(2, "0")
      web/src/features/admin/AdminOverviewClient.tsx:54  padStart(2, "0")
      web/src/features/admin/AdminOverviewClient.tsx:55  padStart(2, "0")
      web/src/features/admin/AdminOverviewClient.tsx:66  padStart(2, "0")

RESULT: 0 FAIL, 6 WARN
--- web/.next mtime & diff check after Gate 3 ---
1795179956 2026-11-20 20:05:56.273800486 +0700
diff next-env/tsconfig lines: 0
```

---

### Gate 4 — Chuỗi E2E liền mạch (`make e2e && make seed && make e2e-web && make e2e-web`), Seed Idempotent & Postgres thật

#### 4.1 Chuỗi nguyên văn 4 lệnh liền mạch (`EXIT 0`)

```text
=== GATE 4: CONTINUOUS E2E CHAIN ===
>>> Running: make e2e && make seed && make e2e-web && make e2e-web

--- [1/4] make e2e ---
GOMODCACHE="$(go env GOMODCACHE)" docker compose --profile e2e run --rm --user "$(id -u):$(id -g)" e2e
=== RUN   TestE2E_HealthAndObservability
--- PASS: TestE2E_HealthAndObservability (0.25s)
    --- PASS: TestE2E_HealthAndObservability/All9Services_HealthzAndReadyz (0.01s)
    --- PASS: TestE2E_HealthAndObservability/Gateway_TraceIDHeader (0.00s)
    --- PASS: TestE2E_HealthAndObservability/All9Services_REDMetrics (0.24s)
=== RUN   TestE2E_MultiShopCheckout_COD
--- PASS: TestE2E_MultiShopCheckout_COD (6.55s)
=== RUN   TestE2E_MultiShopCheckout_VietQRMock
--- PASS: TestE2E_MultiShopCheckout_VietQRMock (0.91s)
=== RUN   TestE2E_OrderIdempotency
--- PASS: TestE2E_OrderIdempotency (0.51s)
    --- PASS: TestE2E_OrderIdempotency/SequentialReplay (0.05s)
    --- PASS: TestE2E_OrderIdempotency/ConcurrentBurst (0.06s)
=== RUN   TestE2E_PaymentWebhookIdempotencyAndSecurity
--- PASS: TestE2E_PaymentWebhookIdempotencyAndSecurity (0.72s)
    --- PASS: TestE2E_PaymentWebhookIdempotencyAndSecurity/RejectInvalidAndMissingSignature (0.00s)
    --- PASS: TestE2E_PaymentWebhookIdempotencyAndSecurity/RejectStaleTimestamp (0.00s)
    --- PASS: TestE2E_PaymentWebhookIdempotencyAndSecurity/Replay10Times_RecordedOnce (0.33s)
=== RUN   TestE2E_FlashSale_1000Concurrent_100Slots
    06_flash_sale_test.go:150: FlashSale 1000 concurrent requests completed in 201.965175ms: succeeded=100 failed=900
--- PASS: TestE2E_FlashSale_1000Concurrent_100Slots (0.70s)
    --- PASS: TestE2E_FlashSale_1000Concurrent_100Slots/1000Concurrent_100Slots_ZeroOversell (0.30s)
    --- PASS: TestE2E_FlashSale_1000Concurrent_100Slots/RealCheckout_AtFlashPrice (0.39s)
=== RUN   TestE2E_PaymentFailed_SagaCompensation
    07_saga_compensation_test.go:141: Saga compensation verified: stock=30 voucher_quota=5 coins=10000 all 100% restored
--- PASS: TestE2E_PaymentFailed_SagaCompensation (1.02s)
=== RUN   TestE2E_LedgerAlwaysBalanced
    08_ledger_test.go:9: Double-Entry Ledger Summary: total_debits_vnd=224911880 total_credits_vnd=224911880 is_balanced=true accounts=42
--- PASS: TestE2E_LedgerAlwaysBalanced (0.00s)
=== RUN   TestE2E_SalesChannel
--- PASS: TestE2E_SalesChannel (1.86s)
    --- PASS: TestE2E_SalesChannel/DefaultCheckout_ReadsAsMarketplace (0.03s)
    --- PASS: TestE2E_SalesChannel/Storefront_COD_CartFilter_SelectiveClear_And_Immutability (0.48s)
    --- PASS: TestE2E_SalesChannel/Storefront_VietQRMock_Webhook_And_Refund (0.38s)
    --- PASS: TestE2E_SalesChannel/Storefront_CrossShopItemRejected_ZeroStockChange (0.08s)
    --- PASS: TestE2E_SalesChannel/Storefront_InvalidAndInactiveShopRejected_ZeroStockChange (0.38s)
    --- PASS: TestE2E_SalesChannel/Storefront_PlatformVoucher_Freeship_OtherShopVoucher_Coins_Rejected (0.13s)
=== RUN   TestE2E_LedgerAndReconciliation_ChannelFilter
    10_ledger_channel_test.go:21: Ledger totals: ALL=(debits=226910880 credits=226910880 balanced=true) MARKETPLACE=(debits=198435880 credits=198435880 balanced=true) STOREFRONT=(debits=28475000 credits=28475000 balanced=true)
    10_ledger_channel_test.go:66: Reconciliation (2026-11-21): ALL=(items=15 matched=15) MARKETPLACE=(items=8 matched=8) STOREFRONT=(items=7 matched=7)
--- PASS: TestE2E_LedgerAndReconciliation_ChannelFilter (0.09s)
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata
--- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata (0.60s)
=== RUN   TestE2E_RateLimit_Real429AndNoXFFBypass
--- PASS: TestE2E_RateLimit_Real429AndNoXFFBypass (0.08s)
=== RUN   TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit
    13_sites_backend_test.go:705: Site 098bd6fa-0737-4145-86e5-a3354d0d4b2f audit trail verified: map[admin.site.suspend:1 admin.site.unsuspend:1 site.publish:2 site.rollback:1 site.save_draft:2]
--- PASS: TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit (0.13s)
=== RUN   TestE2E_FlashSaleStockReserve_Phase2b
    14_flash_sale_stock_reserve_2b_test.go:314: Phase 2b 1000 concurrent flash checkouts completed in 790.061413ms: succeeded=100 failed=900 error_codes=map[ResourceExhausted:3 FailedPrecondition:897]
    14_flash_sale_stock_reserve_2b_test.go:502: Phase 2b Ledger Summary: debits=243743880 credits=243743880 is_balanced=true accounts=42
--- PASS: TestE2E_FlashSaleStockReserve_Phase2b (2.23s)
PASS
ok  	github.com/nexusmart/core/tests/e2e	15.650s

--- [2/4] make seed ---
node --no-warnings=MODULE_TYPELESS_PACKAGE_JSON tools/seed/seed.mjs
==> Seeding NexusMart backend via http://localhost:28080 (Mailpit: http://localhost:8025)
==> Seed complete:
  sites: created=0, reused=4 (root_domain=nexusmart.localhost)
  users: created=0, reused=5 | shops: created=0, reused=4 | categories: created=0, reused=6
  products: created=0, reused=18, totalSkus=159, skusToppedUp=6 | vouchers: created=0, reused=11
  flashSales: windowsCreated=0, windowsReused=3, itemsCreated=0, itemsReused=76
  sites.map: bach-dang (PUBLISHED, work-sans, #F8F6F1/#1D4ED8), sai-gon (PUBLISHED, be-vietnam-pro, #14181C/#D97706), da-nang (PUBLISHED, ibm-plex-sans, #EEF2EA/#3F6B4F), ha-noi (PUBLISHED, plus-jakarta-sans, #2B1A1E/#E9B949)

--- [3/4] make e2e-web (run 1) ---
 ✔ Image nexus_ecom-web       Built                                         0.3s
 ✔ Container nexus_ecom-web-1 Healthy                                       0.5s
▶ 01. Root host = NexusMart platform only (intro, site directory, redirects, RBAC, SEO)
  ✔ public directory REST lists only PUBLISHED sites with the contract fields and capped page size (151.314287ms)
  ✔ root / (both SITE_ROOT_DOMAIN and localhost) is the NexusMart intro: 200, real site directory, no products or prices (86.858199ms)
  ✔ platform pages stay on root; every old marketplace buyer URL is 404 (145.650203ms)
  ✔ /gian-hang/<slug> and /san-pham/<slug> 308 to the shop's published site; unknown slugs 404 (41.907869ms)
  ✔ buyer APIs (cart, checkout, orders, payments, vouchers) are 404 on the root host, anonymous or logged in (145.546134ms)
  ✔ RBAC on root: anonymous redirected to /dang-nhap; buyer, seller, admin only reach their own area (444.071936ms)
  ✔ root robots.txt / sitemap.xml list the intro and published sites, never product or removed buyer URLs (8.588647ms)
✔ 01. Root host = NexusMart platform only (intro, site directory, redirects, RBAC, SEO) (1063.276078ms)
▶ 02. Site A full buyer commerce & auth lifecycle (OTP, COD + SHOP voucher, VietQR, idempotency, refresh rotation)
  ✔ public storefront pages serve 200 on site A; unauthenticated protected pages redirect on the SAME site host (141.025958ms)
  ✔ registers buyer + Mailpit OTP -> logs in on site A with HttpOnly host-only cookies and no token leak (151.887573ms)
  ✔ adds site A SKU to cart, applies site A SHOP voucher, places COD order -> STOREFRONT for shop A (120.562104ms)
  ✔ completes VietQR mock + HMAC webhook flow on site A -> payment SUCCEEDED & order PAID / CONFIRMED (494.259397ms)
  ✔ checkout idempotency: resending same Idempotency-Key returns same single order without duplicates (61.070034ms)
  ✔ refresh rotation is independent per host and logout revokes only that host's session (70.586306ms)
✔ 02. Site A full buyer commerce & auth lifecycle (OTP, COD + SHOP voucher, VietQR, idempotency, refresh rotation) (1076.487565ms)
▶ 03. Cross-site isolation, IDOR, 401 guards, CSRF across hosts, body tampering & blocked routes
  ✔ unknown, foreign, reserved, and multi-level hosts return 404 on pages and APIs (34.774148ms)
  ✔ direct /s/... is 404 on every host; /seller, /admin, /api/seller/*, /api/admin/* are 404 on site hosts (50.399637ms)
  ✔ all 4 seeded sites render distinct themes, isolate products/vouchers, and return 404 on cross-shop PDP (65.031082ms)
  ✔ cookie session of site A does NOT authenticate on site B (host-only isolation) (76.710368ms)
  ✔ site A rejects site B SKUs and site B / platform vouchers; carts stay isolated per site (140.048282ms)
  ✔ same account logged into both site A and site B: orders & payments of A are invisible (404) on B and vice versa (181.752123ms)
  ✔ IDOR between two different buyers on the same site A returns 403/404 (98.354436ms)
  ✔ unauthenticated writes and private reads on site A return 401 without leaking internals (36.119578ms)
  ✔ CSRF rejects Origin of another site, root host, evil origin, missing/null Origin, Fetch-Metadata & spoofed proxy headers with 403 (39.250393ms)
  ✔ checkout body tampering (MARKETPLACE / foreign storefront_shop_id / platform discounts / foreign SKU items) is ignored or rejected (85.332207ms)
✔ 03. Cross-site isolation, IDOR, 401 guards, CSRF across hosts, body tampering & blocked routes (863.84239ms)
▶ 04. Seller/Admin site lifecycle on root, theme security, suspend (pages + APIs) & per-host SEO
  ✔ seller draft/publish/rollback lifecycle, cross-seller RBAC, theme validation, empty-section noindex, and admin suspend/unsuspend (pages + APIs) (6571.916175ms)
  ✔ SEO per site host: product canonical points to OWN site URL, substantive sites are indexable, host-scoped robots.txt & sitemap.xml (58.797309ms)
✔ 04. Seller/Admin site lifecycle on root, theme security, suspend (pages + APIs) & per-host SEO (6650.487394ms)
ℹ tests 25
ℹ suites 4
ℹ pass 25
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 9949.90487

--- [4/4] make e2e-web (run 2) ---
 ✔ Image nexus_ecom-web       Built                                         0.1s
 ✔ Container nexus_ecom-web-1 Healthy                                       0.5s
▶ 01. Root host = NexusMart platform only (intro, site directory, redirects, RBAC, SEO)
  ✔ public directory REST lists only PUBLISHED sites with the contract fields and capped page size (55.347073ms)
  ✔ root / (both SITE_ROOT_DOMAIN and localhost) is the NexusMart intro: 200, real site directory, no products or prices (39.61745ms)
  ✔ platform pages stay on root; every old marketplace buyer URL is 404 (117.953471ms)
  ✔ /gian-hang/<slug> and /san-pham/<slug> 308 to the shop's published site; unknown slugs 404 (36.355141ms)
  ✔ buyer APIs (cart, checkout, orders, payments, vouchers) are 404 on the root host, anonymous or logged in (132.780173ms)
  ✔ RBAC on root: anonymous redirected to /dang-nhap; buyer, seller, admin only reach their own area (858.36858ms)
  ✔ root robots.txt / sitemap.xml list the intro and published sites, never product or removed buyer URLs (6.711458ms)
✔ 01. Root host = NexusMart platform only (intro, site directory, redirects, RBAC, SEO) (1279.109634ms)
▶ 02. Site A full buyer commerce & auth lifecycle (OTP, COD + SHOP voucher, VietQR, idempotency, refresh rotation)
  ✔ public storefront pages serve 200 on site A; unauthenticated protected pages redirect on the SAME site host (183.305513ms)
  ✔ registers buyer + Mailpit OTP -> logs in on site A with HttpOnly host-only cookies and no token leak (603.952534ms)
  ✔ adds site A SKU to cart, applies site A SHOP voucher, places COD order -> STOREFRONT for shop A (210.489186ms)
  ✔ completes VietQR mock + HMAC webhook flow on site A -> payment SUCCEEDED & order PAID / CONFIRMED (521.018741ms)
  ✔ checkout idempotency: resending same Idempotency-Key returns same single order without duplicates (89.657599ms)
  ✔ refresh rotation is independent per host and logout revokes only that host's session (104.891613ms)
✔ 02. Site A full buyer commerce & auth lifecycle (OTP, COD + SHOP voucher, VietQR, idempotency, refresh rotation) (1766.794603ms)
▶ 03. Cross-site isolation, IDOR, 401 guards, CSRF across hosts, body tampering & blocked routes
  ✔ unknown, foreign, reserved, and multi-level hosts return 404 on pages and APIs (47.631366ms)
  ✔ direct /s/... is 404 on every host; /seller, /admin, /api/seller/*, /api/admin/* are 404 on site hosts (73.464786ms)
  ✔ all 4 seeded sites render distinct themes, isolate products/vouchers, and return 404 on cross-shop PDP (97.85239ms)
  ✔ cookie session of site A does NOT authenticate on site B (host-only isolation) (100.27257ms)
  ✔ site A rejects site B SKUs and site B / platform vouchers; carts stay isolated per site (200.011348ms)
  ✔ same account logged into both site A and site B: orders & payments of A are invisible (404) on B and vice versa (249.288064ms)
  ✔ IDOR between two different buyers on the same site A returns 403/404 (594.840967ms)
  ✔ unauthenticated writes and private reads on site A return 401 without leaking internals (66.670896ms)
  ✔ CSRF rejects Origin of another site, root host, evil origin, missing/null Origin, Fetch-Metadata & spoofed proxy headers with 403 (62.908042ms)
  ✔ checkout body tampering (MARKETPLACE / foreign storefront_shop_id / platform discounts / foreign SKU items) is ignored or rejected (161.88811ms)
✔ 03. Cross-site isolation, IDOR, 401 guards, CSRF across hosts, body tampering & blocked routes (1733.189049ms)
▶ 04. Seller/Admin site lifecycle on root, theme security, suspend (pages + APIs) & per-host SEO
  ✔ seller draft/publish/rollback lifecycle, cross-seller RBAC, theme validation, empty-section noindex, and admin suspend/unsuspend (pages + APIs) (6837.875657ms)
  ✔ SEO per site host: product canonical points to OWN site URL, substantive sites are indexable, host-scoped robots.txt & sitemap.xml (89.969515ms)
✔ 04. Seller/Admin site lifecycle on root, theme security, suspend (pages + APIs) & per-host SEO (6956.625706ms)
ℹ tests 25
ℹ suites 4
ℹ pass 25
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 12055.001861
```

#### 4.2 Seed Idempotency bổ sung, Danh bạ REST & Trạng thái Postgres thật

```text
=== GATE 4: EXTRA SEED IDEMPOTENCY RUN ===
==> Seeding NexusMart backend via http://localhost:28080 (Mailpit: http://localhost:8025)
==> Seed complete:
  sites: created=0, reused=4 (root_domain=nexusmart.localhost)
  users: created=0, reused=5 | shops: created=0, reused=4 | categories: created=0, reused=6
  products: created=0, reused=18, totalSkus=159, skusToppedUp=3 | vouchers: created=0, reused=11

=== GATE 4: LIVE PUBLIC SITE DIRECTORY ===
curl -sS 'http://localhost:28080/api/v1/sites?page=1&page_size=24'
{"page":1,"page_size":24,"sites":[{"site_id":"2a9f4704-1d02-4df9-a65d-f8657415fbb4","shop_id":"5a97169b-8561-487e-88fc-80b52c910878","shop_name":"Dệt Kim Hà Nội","subdomain":"ha-noi","primary_host":"ha-noi.nexusmart.localhost","logo_url":"/atmosphere/cotton-twill-slate.jpg","cover_url":"/atmosphere/jacquard-loom-poster.jpg","tagline":"Áo polo dệt kim, jogger nỉ chân cua và tất cotton gân theo tay nghề dệt kim miền Bắc"},{"site_id":"79955ca3-ae69-4c77-b2e2-4584277bfd59","shop_id":"317e706a-3541-473a-a123-93848eec624b","shop_name":"Sợi Tự Nhiên Đà Nẵng","subdomain":"da-nang","primary_host":"da-nang.nexusmart.localhost","logo_url":"/banners/campaign-green-fiber.jpg","cover_url":"/banners/campaign-office.jpg","tagline":"Áo polo pique, quần short linen và đồ lót sợi tre thoáng mát cho miền Trung nắng gió"},{"site_id":"389befb4-e869-4ee7-9788-361671b7e073","shop_id":"b20516d7-ef2e-4805-9f6c-3f16f4962af0","shop_name":"Kho Basic Sài Gòn","subdomain":"sai-gon","primary_host":"sai-gon.nexusmart.localhost","logo_url":"/banners/campaign-running.jpg","cover_url":"/banners/campaign-running.jpg","tagline":"Đồ chạy bộ Dry-Fit siêu nhẹ và trang phục vận động cho khí hậu miền Nam"},{"site_id":"efd24c90-7302-4bee-998b-bf2f08a6d2e6","shop_id":"62b5b10d-0cdf-46df-ad00-16450172b819","shop_name":"Xưởng Dệt Bạch Đằng","subdomain":"bach-dang","primary_host":"bach-dang.nexusmart.localhost","logo_url":"/banners/vietnam-workshop.jpg","cover_url":"/banners/campaign-everyday.jpg","tagline":"Áo thun Cotton Compact 220gsm may trực tiếp tại xưởng Hải Phòng"}],"total":4}

=== GATE 4: POSTGRES LIVE VERIFICATION ===
 max_connections 
-----------------
 300
(1 row)

     datname     | state  | count 
-----------------+--------+-------
 notification_db | idle   |     1
 order_db        | idle   |    25
 payment_db      | idle   |     7
 postgres        | active |     1
 product_db      | idle   |    23
 promotion_db    | idle   |    30
 shipping_db     | idle   |     2
 user_db         | idle   |    14
                 |        |     5
(9 rows)

--- user_db: sites (status = PUBLISHED) ---
                  id                  |               shop_id                | subdomain |  status   | has_pub_ver |         published_at          
--------------------------------------+--------------------------------------+-----------+-----------+-------------+-------------------------------
 2a9f4704-1d02-4df9-a65d-f8657415fbb4 | 5a97169b-8561-487e-88fc-80b52c910878 | ha-noi    | PUBLISHED | t           | 2026-11-21 02:35:53.724172+00
 79955ca3-ae69-4c77-b2e2-4584277bfd59 | 317e706a-3541-473a-a123-93848eec624b | da-nang   | PUBLISHED | t           | 2026-11-21 02:35:53.708951+00
 389befb4-e869-4ee7-9788-361671b7e073 | b20516d7-ef2e-4805-9f6c-3f16f4962af0 | sai-gon   | PUBLISHED | t           | 2026-11-20 12:28:54.625117+00
 efd24c90-7302-4bee-998b-bf2f08a6d2e6 | 62b5b10d-0cdf-46df-ad00-16450172b819 | bach-dang | PUBLISHED | t           | 2026-11-20 12:28:54.61277+00
(4 rows)

--- user_db: domains (4 seeded primary hosts + historical E2E suspended rows) ---
 bach-dang.nexusmart.localhost | SUBDOMAIN | t | t | MANAGED
 da-nang.nexusmart.localhost   | SUBDOMAIN | t | t | MANAGED
 ha-noi.nexusmart.localhost    | SUBDOMAIN | t | t | MANAGED
 sai-gon.nexusmart.localhost   | SUBDOMAIN | t | t | MANAGED
 (cộng 19 bản ghi fixture e2e-site-*/e2e-web-* thuộc các site đã SUSPENDED, không lộ trong danh bạ công khai)

--- order_db: storefront index & channel distribution ---
 idx_orders_buyer_storefront_created | CREATE INDEX idx_orders_buyer_storefront_created ON public.orders USING btree (buyer_id, storefront_shop_id, created_at DESC)

 sales_channel | count 
---------------+-------
 MARKETPLACE   |  1088
 STOREFRONT    |    71
(2 rows)

--- notification_db: storefront column ---
     table_name     |    column_name     | data_type 
--------------------+--------------------+-----------
 order_participants | storefront_shop_id | text
(1 row)

=== web/.next mtime after Gate 4 ===
1795179956 2026-11-20 20:05:56.273800486 +0700
```

---

### Gate 5 — Grep bắt buộc (Route cây `web/src/app`, `"MARKETPLACE"`, `localStorage`)

```text
=== GATE 5: ROUTES & GREP ===
--- find web/src/app -name 'page.tsx' -o -name 'route.ts' | sort (100 files) ---
web/src/app/(admin)/admin/audit-log/page.tsx
web/src/app/(admin)/admin/doi-soat/page.tsx
web/src/app/(admin)/admin/flash-sale/page.tsx
web/src/app/(admin)/admin/gian-hang/page.tsx
web/src/app/(admin)/admin/kiem-duyet/page.tsx
web/src/app/(admin)/admin/page.tsx
web/src/app/(admin)/admin/sites/page.tsx
web/src/app/api/admin/audit-logs/route.ts
web/src/app/api/admin/flash-sale-items/[id]/approve/route.ts
web/src/app/api/admin/flash-sales/route.ts
web/src/app/api/admin/ledger/route.ts
web/src/app/api/admin/orders/route.ts
web/src/app/api/admin/products/[id]/review/route.ts
web/src/app/api/admin/products/route.ts
web/src/app/api/admin/reconcile/route.ts
web/src/app/api/admin/shops/[id]/review/route.ts
web/src/app/api/admin/shops/route.ts
web/src/app/api/admin/sites/[id]/suspend/route.ts
web/src/app/api/admin/sites/route.ts
web/src/app/api/auth/addresses/[id]/route.ts
web/src/app/api/auth/addresses/route.ts
web/src/app/api/auth/login/route.ts
web/src/app/api/auth/logout/route.ts
web/src/app/api/auth/me/route.ts
web/src/app/api/auth/refresh/route.ts
web/src/app/api/auth/register/route.ts
web/src/app/api/auth/resend-otp/route.ts
web/src/app/api/auth/verify-otp/route.ts
web/src/app/api/cart/items/route.ts
web/src/app/api/cart/items/[skuId]/route.ts
web/src/app/api/cart/route.ts
web/src/app/api/checkout/addresses/route.ts
web/src/app/api/checkout/locations/route.ts
web/src/app/api/checkout/route.ts
web/src/app/api/checkout/shipping-quote/route.ts
web/src/app/api/orders/[id]/cancel/route.ts
web/src/app/api/orders/[id]/confirm-received/route.ts
web/src/app/api/orders/[id]/return/route.ts
web/src/app/api/orders/[id]/route.ts
web/src/app/api/orders/route.ts
web/src/app/api/payments/[id]/retry/route.ts
web/src/app/api/payments/[id]/route.ts
web/src/app/api/seller/balance/route.ts
web/src/app/api/seller/flash-sales/[id]/items/route.ts
web/src/app/api/seller/orders/[id]/confirm/route.ts
web/src/app/api/seller/orders/[id]/route.ts
web/src/app/api/seller/orders/[id]/ship/route.ts
web/src/app/api/seller/orders/route.ts
web/src/app/api/seller/products/[id]/route.ts
web/src/app/api/seller/products/route.ts
web/src/app/api/seller/shop/route.ts
web/src/app/api/seller/site/draft/route.ts
web/src/app/api/seller/site/publish/route.ts
web/src/app/api/seller/site/rollback/route.ts
web/src/app/api/seller/site/route.ts
web/src/app/api/seller/site/versions/route.ts
web/src/app/api/seller/skus/[skuId]/stock/route.ts
web/src/app/api/seller/vouchers/route.ts
web/src/app/api/vouchers/[id]/save/route.ts
web/src/app/api/vouchers/preview/route.ts
web/src/app/api/vouchers/route.ts
web/src/app/api/vouchers/validate/route.ts
web/src/app/(seller)/seller/chat/page.tsx
web/src/app/(seller)/seller/don-hang/[id]/page.tsx
web/src/app/(seller)/seller/don-hang/page.tsx
web/src/app/(seller)/seller/ho-so/page.tsx
web/src/app/(seller)/seller/khuyen-mai/page.tsx
web/src/app/(seller)/seller/page.tsx
web/src/app/(seller)/seller/san-pham/[id]/page.tsx
web/src/app/(seller)/seller/san-pham/moi/page.tsx
web/src/app/(seller)/seller/san-pham/page.tsx
web/src/app/(seller)/seller/site/page.tsx
web/src/app/(seller)/seller/site/xem-truoc/page.tsx
web/src/app/(seller)/seller/tai-chinh/page.tsx
web/src/app/(seller)/seller/tuong-tac/page.tsx
web/src/app/(shop)/dang-ky/page.tsx
web/src/app/(shop)/dang-nhap/page.tsx
web/src/app/(shop)/gian-hang/[slug]/route.ts
web/src/app/(shop)/page.tsx
web/src/app/(shop)/san-pham/[slug]/route.ts
web/src/app/(shop)/tro-giup/page.tsx
web/src/app/(shop)/ve-nexusmart/page.tsx
web/src/app/(site)/s/[host]/chinh-sach/page.tsx
web/src/app/(site)/s/[host]/dang-ky/page.tsx
web/src/app/(site)/s/[host]/dang-nhap/page.tsx
web/src/app/(site)/s/[host]/don-hang/[id]/page.tsx
web/src/app/(site)/s/[host]/don-hang/page.tsx
web/src/app/(site)/s/[host]/flash-sale/page.tsx
web/src/app/(site)/s/[host]/gio-hang/page.tsx
web/src/app/(site)/s/[host]/gioi-thieu/page.tsx
web/src/app/(site)/s/[host]/page.tsx
web/src/app/(site)/s/[host]/san-pham/page.tsx
web/src/app/(site)/s/[host]/san-pham/[slug]/page.tsx
web/src/app/(site)/s/[host]/tai-khoan/page.tsx
web/src/app/(site)/s/[host]/thanh-toan/cho/[id]/page.tsx
web/src/app/(site)/s/[host]/thanh-toan/page.tsx
web/src/app/(site)/s/[host]/tim-kiem/page.tsx
web/src/app/(site)/s/[host]/tra-cuu-don-hang/page.tsx
web/src/app/(site)/s/[host]/tra-cuu-don/page.tsx
web/src/app/(site)/s/[host]/voucher/page.tsx

--- grep -rn "MARKETPLACE" web/src ---
web/src/data/orders.ts:153:  salesChannel?: "MARKETPLACE" | "STOREFRONT";
web/src/features/account/orderView.test.mjs:227:test("preserves sales_channel and storefront_shop_id with legacy MARKETPLACE default", () => {
web/src/features/account/orderView.test.mjs:229:  assert.equal(legacy.salesChannel, "MARKETPLACE");
web/src/features/account/orderView.test.mjs:230:  assert.equal(legacy.sales_channel, "MARKETPLACE");
web/src/features/admin/AdminReconciliationClient.tsx:24:  { id: "MARKETPLACE", label: "Sàn NexusMart" },
web/src/features/admin/AdminReconciliationClient.tsx:29:  if (channel === "MARKETPLACE") return "Sàn NexusMart";
web/src/features/admin/AdminOverviewClient.tsx:331:                  <option value="MARKETPLACE">
web/src/lib/salesChannel.ts:5:export type SalesChannel = "MARKETPLACE" | "STOREFRONT";
web/src/lib/salesChannel.ts:8:export const DEFAULT_SALES_CHANNEL: SalesChannel = "MARKETPLACE";
web/src/lib/salesChannel.ts:19: * Legacy or empty values always default to `"MARKETPLACE"`.
web/src/lib/salesChannel.ts:41: * - `"MARKETPLACE"` -> `"MARKETPLACE"`
web/src/lib/salesChannel.ts:50:  if (upper === "MARKETPLACE" || upper === "STOREFRONT") return upper;
web/src/lib/salesChannel.ts:66:    value: "MARKETPLACE",

--- grep -rn "localStorage" web/src ---
web/src/context/StoreContext.tsx:49:  // Real backend cart for the current shop site (single source of truth = /api/cart, never localStorage)
web/src/context/StoreContext.tsx:70:  // Viewer-only convenience preferences (persisted in localStorage, zero PII/cart/orders/tokens)
web/src/context/StoreContext.tsx:122:  // Load viewer-only preferences from localStorage after mount (zero cart/orders/tokens/PII)
web/src/context/StoreContext.tsx:125:      const raw = window.localStorage.getItem(VIEWER_PREFS_STORAGE_KEY);
web/src/context/StoreContext.tsx:146:      window.localStorage.removeItem("nexusmart_store_v2");
web/src/context/StoreContext.tsx:157:      window.localStorage.setItem(
web/src/features/seller/sellerStore.ts:10: * shop profile, site config, seller balances) are NEVER stored here and NEVER stored in localStorage.
```

**Phân loại chi tiết kết quả Gate 5:**
- **Cây route `web/src/app`:** **0** route hoặc component người mua của sàn còn sót ngoài `(site)/s/[host]/` (trừ 2 route handler `308` bắt buộc theo đặc tả).
- **`"MARKETPLACE"` trong `web/src`:** **0 vị trí gửi đi** trong bất kỳ lệnh ghi/tạo đơn/checkout/BFF nào (`checkoutPayload.ts`, `api/checkout/**`, `api/cart/**`, `api/orders/**`, `api/payments/**`, `api/vouchers/**` đều **0 hits**). Toàn bộ 13 dòng khớp chỉ thuộc **4 vị trí chỉ-đọc/lọc đơn lịch sử** và unit test đọc đơn cũ:
  1. `web/src/lib/salesChannel.ts` (type, nhãn tiếng Việt và tùy chọn bộ lọc chỉ-đọc cho Admin/Seller xem đơn lịch sử).
  2. `web/src/features/admin/AdminOverviewClient.tsx` & `AdminReconciliationClient.tsx` (`<option>` lọc & nhãn hiển thị chỉ-đọc cho đơn/sổ cái lịch sử kênh sàn cũ).
  3. `web/src/data/orders.ts` (định nghĩa kiểu trường đọc `salesChannel`).
  4. `web/src/features/account/orderView.test.mjs` (unit test xác nhận ánh xạ chỉ-đọc trường `sales_channel` của đơn cũ).
- **`localStorage` trong `web/src`:** Chỉ dùng duy nhất khóa `VIEWER_PREFS_STORAGE_KEY` trong `StoreContext.tsx` để lưu `wishlist` (ID sản phẩm yêu thích) và `recentlyViewed` (ID sản phẩm đã xem gần đây), đồng thời xóa khóa cũ `nexusmart_store_v2`; **0** lưu giỏ hàng, đơn hàng, token hay dữ liệu cá nhân (PII).

---

### Gate 6 — Kiểm định trực quan độc lập `inspect.mjs` (`1440 / 768 / 390` — **17/17 URL · 0 FAIL**)

Chạy độc lập hoàn toàn trên container `web` bản cuối (`:3300`) và proxy phiên tạm cổng `3420` (tái dùng `.taste-skill/inspect-p4/authproxy.mjs`, output cách ly tại `/tmp/nexus-p4-verifier-inspect`, đã tắt sạch proxy `3420`, gọi `DELETE /api/cart` dọn sạch giỏ tạm buyer trên cả 2 site và xóa sạch thư mục tạm sau khi mở kiểm ảnh chụp).

#### 6.1 Bảng tổng hợp 17/17 URL (**51/51 lượt đo viewport · `0 FAIL` · `0px` tràn ngang · `0` ảnh vỡ**)

| # | URL kiểm định | Chế độ | Dữ liệu thật & Phiên | F / W | H1 (`1440 / 768 / 390` px) | Phông | Tràn ngang | Ảnh vỡ | Console |
|---|---|---:|---|---:|---|---|---:|---:|---|
| 1 | `http://nexusmart.localhost:3300/` | page | Công khai (4 site PUBLISHED thật) | **`0 / 1`** | `67.7 / 36.1 / 32` | Work Sans | `0/0/0` | `0` | `0` |
| 2 | `http://bach-dang.nexusmart.localhost:3300/` | page | Công khai (theme sáng `#f8f6f1`) | **`0 / 3`** | `43.9 / 36 / 26` | Work Sans | `0/0/0` | `0` | `0` |
| 3 | `http://bach-dang.nexusmart.localhost:3300/san-pham` | page | Công khai (danh sách SP thật) | **`0 / 3`** | `69.1 / 36.9 / 34` | Work Sans | `0/0/0` | `0` | `0` |
| 4 | `http://bach-dang.nexusmart.localhost:3300/san-pham/ao-thun-cotton-compact-trang` | page | Công khai (PDP thật, `BACHDANG20`) | **`0 / 3`** | `40 / 36 / 28` | Work Sans | `0/0/0` | `0` | `0` |
| 5 | `http://bach-dang.nexusmart.localhost:3420/gio-hang` | page | Buyer đăng nhập, **2 SP** (`418.000₫`) | **`0 / 2`** | `66.2 / 36 / 36` | Work Sans | `0/0/0` | `0` | `0` |
| 6 | `http://bach-dang.nexusmart.localhost:3420/thanh-toan` | page | Buyer đăng nhập, **2 SP + cước thật** | **`0 / 2`** | `66.2 / 36 / 36` | Work Sans | `0/0/0` | `0` | `0` |
| 7 | `http://bach-dang.nexusmart.localhost:3300/dang-nhap` | page | Công khai | **`0 / 3`** | `69.1 / 36.9 / 34` | Work Sans | `0/0/0` | `0` | `0` |
| 8 | `http://bach-dang.nexusmart.localhost:3420/don-hang` | page | Buyer đăng nhập, **1 đơn thật** | **`0 / 3`** | `66.2 / 36 / 36` | Work Sans | `0/0/0` | `0` | React `#418` *(múi giờ)* |
| 9 | `http://sai-gon.nexusmart.localhost:3300/` | page | Công khai (theme tối `#14181c`) | **`0 / 3`** | `43.9 / 36 / 26` | Be Vietnam Pro | `0/0/0` | `0` | `0` |
| 10 | `http://sai-gon.nexusmart.localhost:3300/san-pham` | page | Công khai (danh sách SP thật) | **`0 / 3`** | `69.1 / 36.9 / 34` | Be Vietnam Pro | `0/0/0` | `0` | `0` |
| 11 | `http://sai-gon.nexusmart.localhost:3300/san-pham/ao-thun-the-thao-dryfit-xanh-co-vit` | page | Công khai (PDP thật, `SAIGON25`) | **`0 / 3`** | `40 / 36 / 28` | Be Vietnam Pro | `0/0/0` | `0` | `0` |
| 12 | `http://sai-gon.nexusmart.localhost:3420/gio-hang` | page | Buyer đăng nhập, **2 SP** (`648.000₫`) | **`0 / 2`** | `66.2 / 36 / 36` | Be Vietnam Pro | `0/0/0` | `0` | `0` |
| 13 | `http://sai-gon.nexusmart.localhost:3420/thanh-toan` | page | Buyer đăng nhập, **2 SP + cước thật** | **`0 / 2`** | `66.2 / 36 / 36` | Be Vietnam Pro | `0/0/0` | `0` | `0` |
| 14 | `http://sai-gon.nexusmart.localhost:3300/dang-nhap` | page | Công khai | **`0 / 3`** | `69.1 / 36.9 / 34` | Be Vietnam Pro | `0/0/0` | `0` | `0` |
| 15 | `http://sai-gon.nexusmart.localhost:3420/don-hang` | page | Buyer đăng nhập, **1 đơn thật** (`NM-20261121-1318540F`) | **`0 / 3`** | `66.2 / 36 / 36` | Be Vietnam Pro | `0/0/0` | `0` | React `#418` *(múi giờ)* |
| 16 | `http://nexusmart.localhost:3420/seller` | `--app` | `seller@nexusmart.local` (Xưởng Dệt Bạch Đằng) | **`0 / 0`** | `24 / 24 / 21` | Work Sans | `0/0/0` | `0` | `0` |
| 17 | `http://nexusmart.localhost:3420/admin` | `--app` | `admin@nexusmart.local` (Super Admin) | **`0 / 1`** | `24 / 24 / 22` | Work Sans | `0/0/0` | `0` | React `#418` *(múi giờ)* |

**Kiểm tra ảnh chụp độc lập:** Đã mở trực tiếp 11 ảnh chụp đại diện (`01-root` desktop fold + page-01, `02-bd-home` mobile fold, `04-bd-pdp` desktop fold, `05-bd-cart` desktop fold, `09-sg-home` desktop fold, `11-sg-pdp` desktop fold, `13-sg-checkout` desktop fold, `15-sg-orders` desktop fold, `16-seller` desktop fold, `17-admin` desktop fold). Xác nhận: host gốc `/` hiển thị đúng 4 cửa hàng PUBLISHED thật và không có giá/lưới sản phẩm; header mobile site gọn 1 hàng không che chữ; dark theme `sai-gon` có tương phản sắc nét ở nút/liên kết/ô vận chuyển/tab đang chọn; nhãn vận chuyển là `"Giao hàng NexusFast"` (không còn `"Mock Express"`), địa chỉ không lặp phường/tỉnh; `/seller` hiện rõ trạng thái `"Đang hoạt động"` + nút `"Xem site ↗"`; `/admin` giữ đúng 7 mục điều hướng thật, sổ cái `0₫ lệch` và bộ lọc kênh lịch sử.

#### 6.2 Đầu ra thật nguyên văn của 17 lượt đo `inspect.mjs` & hậu kiểm dọn dẹp

```text
=== INSPECT [01-root]: http://nexusmart.localhost:3300/ ===
  hero headline   "Cửa hàng riêng cho mỗi thương hiệu"  7 words · 2 line(s) · 67.7px (4.7vw) · Work Sans 300
  @768px 2 line(s) · 36.1px | @390px 2 line(s) · 32px | typefaces Work Sans 100% | body 16px / 1.55
  page 7.2 screens · 1584 words · 12 images | WARN: [card-rows] @1440px
  RESULT: 0 FAIL, 1 WARN — measurements pass. >>> EXIT 0 (01-root)

=== INSPECT [02-bd-home]: http://bach-dang.nexusmart.localhost:3300/ ===
  hero headline   "Cotton Compact 220gsm từ xưởng Hải Phòng"  7 words · 2 line(s) · 43.9px (3vw) · Work Sans 500
  @768px 2 line(s) · 36px | @390px 2 line(s) · 26px | typefaces Work Sans 100% | body 16px / 1.55
  page 5.6 screens · 620 words · 11 images | WARN: [card-rows], [palette-cream], [no-footage]
  RESULT: 0 FAIL, 3 WARN — measurements pass. >>> EXIT 0 (02-bd-home)

=== INSPECT [03-bd-list]: http://bach-dang.nexusmart.localhost:3300/san-pham ===
  hero headline   "Sản phẩm của Xưởng Dệt Bạch Đằng"  7 words · 1 line(s) · 69.1px (4.8vw) · Work Sans 400
  @768px 1 line(s) · 36.9px | @390px 2 line(s) · 34px | typefaces Work Sans 100% | body 18px / 1.55
  page 1.5 screens · 199 words · 5 images | WARN: [palette-cream], [no-footage], [still-page]
  RESULT: 0 FAIL, 3 WARN — measurements pass. >>> EXIT 0 (03-bd-list)

=== INSPECT [04-bd-pdp]: http://bach-dang.nexusmart.localhost:3300/san-pham/ao-thun-cotton-compact-trang ===
  hero headline   "Áo thun Cotton Compact 220gsm Trắng"  6 words · 2 line(s) · 40px (2.8vw) · Work Sans 500
  @768px 1 line(s) · 36px | @390px 1 line(s) · 28px | typefaces Work Sans 100% | body 16px / 1.65
  page 2.9 screens · 332 words · 9 images | WARN: [palette-cream], [no-footage], [still-page]
  RESULT: 0 FAIL, 3 WARN — measurements pass. >>> EXIT 0 (04-bd-pdp)

=== INSPECT [07-bd-login]: http://bach-dang.nexusmart.localhost:3300/dang-nhap ===
  hero headline   "Đăng nhập tại Xưởng Dệt Bạch Đằng"  7 words · 2 line(s) · 69.1px (4.8vw) · Work Sans 400
  @768px 1 line(s) · 36.9px | @390px 2 line(s) · 34px | typefaces Work Sans 100% | body 16px / 1.55
  page 1.3 screens · 226 words · 5 images | WARN: [palette-cream], [no-footage], [still-page]
  RESULT: 0 FAIL, 3 WARN — measurements pass. >>> EXIT 0 (07-bd-login)

=== INSPECT [09-sg-home]: http://sai-gon.nexusmart.localhost:3300/ ===
  hero headline   "Trang phục vận động nhẹ mát Sài Gòn"  8 words · 2 line(s) · 43.9px (3vw) · Be Vietnam Pro 500
  @768px 1 line(s) · 36px | @390px 2 line(s) · 26px | typefaces Be Vietnam Pro 100% | body 16px / 1.55
  page 4.8 screens · 577 words · 9 images | WARN: [card-rows], [no-footage], [font-not-dealt]
  RESULT: 0 FAIL, 3 WARN — measurements pass. >>> EXIT 0 (09-sg-home)

=== INSPECT [10-sg-list]: http://sai-gon.nexusmart.localhost:3300/san-pham ===
  hero headline   "Sản phẩm của Kho Basic Sài Gòn"  7 words · 1 line(s) · 69.1px (4.8vw) · Be Vietnam Pro 400
  @768px 1 line(s) · 36.9px | @390px 2 line(s) · 34px | typefaces Be Vietnam Pro 100% | body 16px / 1.55
  page 2.2 screens · 231 words · 6 images | WARN: [no-footage], [font-not-dealt], [still-page]
  RESULT: 0 FAIL, 3 WARN — measurements pass. >>> EXIT 0 (10-sg-list)

=== INSPECT [11-sg-pdp]: http://sai-gon.nexusmart.localhost:3300/san-pham/ao-thun-the-thao-dryfit-xanh-co-vit ===
  hero headline   "Áo thun Thể thao Dry-Fit Xanh cổ vịt"  8 words · 2 line(s) · 40px (2.8vw) · Be Vietnam Pro 500
  @768px 1 line(s) · 36px | @390px 1 line(s) · 28px | typefaces Be Vietnam Pro 100% | body 16px / 1.65
  page 2.8 screens · 355 words · 10 images | WARN: [no-footage], [font-not-dealt], [still-page]
  RESULT: 0 FAIL, 3 WARN — measurements pass. >>> EXIT 0 (11-sg-pdp)

=== INSPECT [14-sg-login]: http://sai-gon.nexusmart.localhost:3300/dang-nhap ===
  hero headline   "Đăng nhập tại Kho Basic Sài Gòn"  7 words · 2 line(s) · 69.1px (4.8vw) · Be Vietnam Pro 400
  @768px 1 line(s) · 36.9px | @390px 2 line(s) · 34px | typefaces Be Vietnam Pro 100% | body 16px / 1.55
  page 1.3 screens · 232 words · 5 images | WARN: [no-footage], [font-not-dealt], [still-page]
  RESULT: 0 FAIL, 3 WARN — measurements pass. >>> EXIT 0 (14-sg-login)

[bach-dang.nexusmart.localhost] existing orders: 1 | ready: cart_items=2, total_vnd=418000, orders=1
[sai-gon.nexusmart.localhost] existing orders: 1 | ready: cart_items=2, total_vnd=648000, orders=1

=== INSPECT [05-bd-cart]: http://bach-dang.nexusmart.localhost:3420/gio-hang ===
  hero headline   "Giỏ hàng · Xưởng Dệt Bạch Đằng"  7 words · 1 line(s) · 66.2px (4.6vw) · Work Sans 300
  @768px 1 line(s) · 36px | @390px 2 line(s) · 36px | typefaces Work Sans 100% | body 16px / 1.5
  page 2.4 screens · 400 words · 11 images | WARN: [palette-cream], [no-footage]
  RESULT: 0 FAIL, 2 WARN — measurements pass. >>> EXIT 0 (05-bd-cart)

=== INSPECT [06-bd-checkout]: http://bach-dang.nexusmart.localhost:3420/thanh-toan ===
  hero headline   "Thanh toán · Xưởng Dệt Bạch Đằng"  7 words · 1 line(s) · 66.2px (4.6vw) · Work Sans 300
  @768px 1 line(s) · 36px | @390px 2 line(s) · 36px | typefaces Work Sans 100% | body 16px / 1.5
  page 2.6 screens · 485 words · 11 images | WARN: [palette-cream], [no-footage]
  RESULT: 0 FAIL, 2 WARN — measurements pass. >>> EXIT 0 (06-bd-checkout)

=== INSPECT [08-bd-orders]: http://bach-dang.nexusmart.localhost:3420/don-hang ===
  hero headline   "Đơn hàng tại Xưởng Dệt Bạch Đằng"  7 words · 1 line(s) · 66.2px (4.6vw) · Work Sans 300
  @768px 1 line(s) · 36px | @390px 2 line(s) · 36px | typefaces Work Sans 100% | body 18px / 1.55
  page 2.1 screens · 311 words · 10 images | WARN: [palette-cream], [no-footage], [still-page]
  RESULT: 0 FAIL, 3 WARN — measurements pass. >>> EXIT 0 (08-bd-orders)

=== INSPECT [12-sg-cart]: http://sai-gon.nexusmart.localhost:3420/gio-hang ===
  hero headline   "Giỏ hàng · Kho Basic Sài Gòn"  7 words · 1 line(s) · 66.2px (4.6vw) · Be Vietnam Pro 300
  @768px 1 line(s) · 36px | @390px 2 line(s) · 36px | typefaces Be Vietnam Pro 100% | body 16px / 1.5
  page 2.4 screens · 404 words · 11 images | WARN: [no-footage], [font-not-dealt]
  RESULT: 0 FAIL, 2 WARN — measurements pass. >>> EXIT 0 (12-sg-cart)

=== INSPECT [13-sg-checkout]: http://sai-gon.nexusmart.localhost:3420/thanh-toan ===
  hero headline   "Thanh toán · Kho Basic Sài Gòn"  7 words · 1 line(s) · 66.2px (4.6vw) · Be Vietnam Pro 300
  @768px 1 line(s) · 36px | @390px 2 line(s) · 36px | typefaces Be Vietnam Pro 100% | body 16px / 1.5
  page 2.6 screens · 488 words · 11 images | WARN: [no-footage], [font-not-dealt]
  RESULT: 0 FAIL, 2 WARN — measurements pass. >>> EXIT 0 (13-sg-checkout)

=== INSPECT [15-sg-orders]: http://sai-gon.nexusmart.localhost:3420/don-hang ===
  hero headline   "Đơn hàng tại Kho Basic Sài Gòn"  7 words · 1 line(s) · 66.2px (4.6vw) · Be Vietnam Pro 300
  @768px 1 line(s) · 36px | @390px 2 line(s) · 36px | typefaces Be Vietnam Pro 100% | body 18px / 1.55
  page 2.2 screens · 338 words · 11 images | WARN: [no-footage], [font-not-dealt], [still-page]
  RESULT: 0 FAIL, 3 WARN — measurements pass. >>> EXIT 0 (15-sg-orders)

=== INSPECT [16-seller]: http://nexusmart.localhost:3420/seller --app ===
  hero headline   "Tổng quan gian hàng"  4 words · 1 line(s) · 24px (1.7vw) · Work Sans 600
  @768px 1 line(s) · 24px | @390px 1 line(s) · 21px | typefaces Work Sans 100% | body 13px / 1.38
  page 1.4 screens · 505 words · 4 images | FINDINGS none.
  RESULT: 0 FAIL, 0 WARN — measurements pass. >>> EXIT 0 (16-seller)

=== INSPECT [17-admin]: http://nexusmart.localhost:3420/admin --app ===
  hero headline   "Tổng quan quản trị"  4 words · 1 line(s) · 24px (1.7vw) · Work Sans 600
  @768px 1 line(s) · 24px | @390px 1 line(s) · 22px | typefaces Work Sans 100% | body 14px / 1.43
  page 2.4 screens · 567 words · 0 images | WARN: [edge-text] @390px ("· E2E web unsuspend check")
  RESULT: 0 FAIL, 1 WARN — measurements pass. >>> EXIT 0 (17-admin)

TOTAL: dirs=17 F=0 W=40 overflow=0 brokenImg=0 hydration418=[08-bd-orders,15-sg-orders,17-admin]
[cleanup] DELETE /api/cart on bach-dang.nexusmart.localhost: HTTP 200
[cleanup] DELETE /api/cart on sai-gon.nexusmart.localhost: HTTP 200
Port 3420 closed cleanly.
=== ALL 17 INSPECT URLS PASSED ===
web/.next mtime: 1795179956 2026-11-20 20:05:56.273800486 +0700
```

---

## 4. Xác nhận cập nhật `web/DESIGN_REPORT.md` và `web/FEATURE_MATRIX.md`

Đã đọc và đối chiếu độc lập cả hai tài liệu báo cáo trong `web/` với trạng thái mã nguồn và kết quả chạy gate thật:
1. **`web/DESIGN_REPORT.md`:** Đã bổ sung đầy đủ phần **Giai đoạn 4 (`2026-11-21`)** ở đầu báo cáo (mục `G4.1`–`G4.5`), phản ánh chính xác kiến trúc phân tách 4 khu vực theo `Host`, kiểm kê `111 → 100` tệp route, grep `"MARKETPLACE"` (`0` chỗ gửi đi), Design Taste Lint (`289 files: 0 FAIL, 11 WARN`; `--app` `83 files: 0 FAIL, 6 WARN`), bảng đo 17/17 URL `0 FAIL` và nhật ký Look-and-Fix chi tiết 7 nhóm lỗi; đồng thời bảo toàn nguyên vẹn phần hồ sơ lịch sử Đợt 1–3 phía dưới.
2. **`web/FEATURE_MATRIX.md`:** Đã viết lại sạch cho mô hình nền tảng đa cửa hàng độc lập Giai đoạn 4 (tổng quan `100` route theo 4 khu vực + bảng đối chiếu toàn bộ 55 tính năng gốc `B01–B17`, `P01–P20`, `C01–C18` phân loại rõ ràng giữa *Live*, *Live theo site*, *Cục bộ / Pha sau*, và *Đã gỡ theo mô hình sàn*).

---

## 5. Phần chưa làm / Ngoài phạm vi kèm lý do

1. **Giỏ hàng khách vãng lai (chưa đăng nhập) trên host site:** Theo đặc tả Giai đoạn 4, giỏ hàng tiếp tục yêu cầu đăng nhập (chuyển hướng `/dang-nhap?next=...` trên cùng site); hỗ trợ giỏ khách ẩn danh và gộp giỏ khi đăng nhập để ở pha sau.
2. **Dữ liệu mẫu cục bộ còn lại ở 3 trang phụ trong Seller Center (`/seller/ho-so`, `/seller/tuong-tac`, `/seller/chat`):**
   - `/seller/ho-so`: hồ sơ xưởng sản xuất, cấu hình vận hành và bảng size xưởng dùng state in-memory (`sellerStore.ts`) vì chưa có bảng/RPC mở rộng hồ sơ vận hành xưởng ở backend.
   - `/seller/tuong-tac`: danh sách đánh giá khách hàng, hỏi đáp công khai và đăng ký báo có hàng dùng dữ liệu mẫu cục bộ vì chưa xây Review/QA & Back-in-stock service.
   - `/seller/chat`: luồng tin nhắn người bán dùng dữ liệu mẫu cục bộ in-memory vì chưa có microservice Chat WebSocket thời gian thực (đã gỡ `BuyerChatPanel` và `AiAssistantPanel` mock khỏi UI người mua).
3. **Tính năng Giai đoạn 3 / Pha sau (ngoài phạm vi Giai đoạn 4):**
   - Tên miền riêng (`CUSTOM` domain) + Caddy On-Demand TLS (`ask` + DNS poller nền) và SSO đa tên miền (PKCE/JWKS).
   - Search service chuyên dụng (hiện `/tim-kiem` tìm không dấu + đồng nghĩa trực tiếp trên catalog thật của gian hàng), AI Assistant gọi tool bằng JWT người dùng.
   - Kết nối API thật của đối tác vận chuyển (GHN/GHTK/SPX), xuất hóa đơn điện tử VAT (VNPT/MISA/Viettel), Zalo OA ZNS, và upload ảnh CDN ngoài (ảnh hiện lưu trực tiếp dưới `web/public/`).
   - Đồng bộ server cho danh sách yêu thích (`wishlist`) và đã xem gần đây (`recentlyViewed`) — hiện giữ làm tùy chọn hiển thị nhẹ phía trình duyệt (không chứa giỏ/đơn/token/PII).

---

## 6. Mọi chỗ lệch & Lưu ý minh bạch

1. **Cơ chế chạy `tsc --noEmit` & `NEXT_DIST_DIR=.next-verifier npm run build` trong bản sao sạch `/tmp/nexus-web-verify-build`:**
   - Theo luật cứng của môi trường làm việc, server cổng `3111` của manager đang phục vụ từ `web/.next` (mtime `1795179956`, `2026-11-20 20:05:56`), và `web/tsconfig.json` đang include cả `.next/types/**/*.ts` lẫn `.next-platform/types/**/*.ts` vốn chứa stub của 26 route sàn cũ đã xóa. Nếu chạy `tsc` hoặc `next build` trực tiếp trong `web/`, TypeScript sẽ báo `TS2307` trên các stub cũ đó và `next build` sẽ tự ghi đè `web/tsconfig.json` + `web/next-env.d.ts`.
   - Do đó, tương tự tiền lệ Gate 2 Phase 0b (`docs/WEB_INTEGRATION_REPORT.md`) và `.dockerignore` trong `web/Dockerfile`, verifier sao chép sạch `web/` (loại trừ `.next*`, symlink `node_modules`) sang `/tmp/nexus-web-verify-build`, xác minh `npx tsc --noEmit` (trước và sau build) + `NEXT_DIST_DIR=.next-verifier npm run build` đạt **EXIT 0**, rồi xóa sạch thư mục tạm. Toàn bộ `web/.next`, `web/.next-platform`, `web/tsconfig.json` và `web/next-env.d.ts` được giữ nguyên vẹn **0 dòng diff**.
2. **Cảnh báo React Hydration `#418` trên console tại `/don-hang` và `/admin`:**
   - Trong `report.json` của Gate 6, ghi nhận cảnh báo React `#418` ở `08-bd-orders`, `15-sg-orders` (2 cảnh báo) và `17-admin` (3 cảnh báo) do `formatDateTimeVi`/`formatShortTimeDateVi` chưa ép `timeZone: "Asia/Ho_Chi_Minh"`, dẫn tới chuỗi giờ SSR trong container Docker (múi giờ UTC) khác chuỗi giờ hydrate trên trình duyệt kiểm định (`+07:00`). Không gây lỗi hiển thị, vỡ khung hay lỗi chức năng; ghi nhận minh bạch để cố định múi giờ ở pha sau.
3. **Chuỗi `"MARKETPLACE"` chỉ-đọc trong `web/src`:**
   - Đặc tả yêu cầu backend giữ nguyên enum/RPC `MARKETPLACE`, Admin vẫn xem được đơn cũ kênh sàn, và web không bao giờ gửi `"MARKETPLACE"` đi. Verifier xác nhận **0 vị trí gửi đi** trong toàn bộ luồng người mua và write BFF; 13 dòng khớp `grep "MARKETPLACE" web/src` đều là định nghĩa kiểu, nhãn hiển thị và bộ lọc chỉ-đọc lịch sử cho Admin/Seller cùng 1 unit test đọc đơn cũ (chi tiết ở Gate 5).
4. **Một WARN `[edge-text] @390px` trên `/admin` trong Gate 6:**
   - Khác với bản chụp trước chạy E2E (`0 FAIL, 0 WARN`), lượt chạy độc lập ngay sau Gate 4 `make e2e-web` phát sinh 1 WARN `[edge-text] @390px` do dòng nhật ký kiểm toán gần nhất chứa chuỗi tiếng Anh dài `"· E2E web unsuspend check"` trong bảng nhỏ ở mobile (`0 FAIL`, không tràn ngang `overflow=0/0/0`).
5. **Commit nền trên nhánh `phase-0b`:**
   - Trước khi verifier khởi động, nhánh `phase-0b` đã có các commit checkpoint tự động (`b56f8bb wip(4): checkpoint while agy swarm runs (2026-11-21 10:53)`). Verifier không chạy bất kỳ lệnh ghi git nào (`0` commit mới) và chỉ ghi duy nhất tệp `docs/PHASE4_REPORT.md`.

---

## 7. Truy cập nhanh trên môi trường dev (`:3300`)

| Đối tượng | URL dev (`container web :3300`) | Ghi chú |
|---|---|---|
| **Host gốc nền tảng** | `http://nexusmart.localhost:3300/` | Giới thiệu NexusMart + danh bạ 4 site PUBLISHED thật |
| **Site 1 (Light · Work Sans)** | `http://bach-dang.nexusmart.localhost:3300/` | `Xưởng Dệt Bạch Đằng` (`#F8F6F1` / `#1D4ED8`, voucher `BACHDANG20`) |
| **Site 2 (Dark · Be Vietnam Pro)** | `http://sai-gon.nexusmart.localhost:3300/` | `Kho Basic Sài Gòn` (`#14181C` / `#D97706`, voucher `SAIGON25`) |
| **Site 3 (Sage · IBM Plex Sans)** | `http://da-nang.nexusmart.localhost:3300/` | `Sợi Tự Nhiên Đà Nẵng` (`#EEF2EA` / `#3F6B4F`) |
| **Site 4 (Burgundy · Plus Jakarta Sans)** | `http://ha-noi.nexusmart.localhost:3300/` | `Dệt Kim Hà Nội` (`#2B1A1E` / `#E9B949`) |
| **Seller Center** | `http://nexusmart.localhost:3300/seller` | `seller@nexusmart.local` / `SellerDemo123!` |
| **Admin Portal** | `http://nexusmart.localhost:3300/admin` | `admin@nexusmart.local` / `AdminDevPassword123!` |
| **Tài khoản Buyer mẫu** | Đăng nhập riêng trên từng site | `buyer@nexusmart.local` / `BuyerDemo123!` |
