# Swarm 4 – luật chung, bảng sở hữu file, hợp đồng giao diện

Đặc tả: `.agents/briefs/sites-4-no-marketplace.md` (đọc TOÀN BỘ, kể cả mục Gate). Đọc thêm phần liên quan
tới mình trong: `AGENTS.md`, `docs/PHASE2_REPORT.md` (mục 1.2, 3), `docs/PHASE1_REPORT.md` (mục A, B),
`docs/WEB_INTEGRATION_REPORT.md`, `.agents/briefs/sites-2-subdomain-sites.md`, `web/DESIGN_REPORT.md`.

## Luật cứng
- Thư mục là git repo nhánh `phase-0b`, mọi worker dùng CHUNG một bản làm việc. KHÔNG `git commit`,
  `git push`, `git checkout/switch/stash/reset/restore`, không đổi nhánh. Chỉ sửa file mình sở hữu
  (bảng dưới). Cần sửa file người khác: nhờ chủ file trên board bằng `@tên`.
- Server của manager: `next start` ở cổng 3111 chạy từ `web/.next`. TUYỆT ĐỐI không ghi `web/.next`:
  mọi lệnh next dùng `NEXT_DIST_DIR=.next-<tên-mình>` (vd `cd web && NEXT_DIST_DIR=.next-bff npx next dev -p 3403`).
- Không kill tiến trình ở cổng 3111, 4777. Không `sudo`. Không `make down`, không xoá volume.
  Cổng dev riêng: web-root 3401, web-site 3402, bff 3403, seller-admin 3404, qa 3410, verifier 3420, reviewer 3421.
- Stack backend đang chạy (`docker compose ps`), gateway REST `http://localhost:28080`, container web ở
  `:3300` (Host `nexusmart.localhost:3300`, site `bach-dang.nexusmart.localhost:3300`, `sai-gon...`).
  `SITE_ROOT_DOMAIN=nexusmart.localhost`. Dev server Next: chạy với `GATEWAY_URL=http://localhost:28080
  SITE_ROOT_DOMAIN=nexusmart.localhost`, thử site bằng `curl -H 'Host: bach-dang.nexusmart.localhost:<cổng>'`.
- Chỉ `backend` được rebuild container service Go/Rust (`docker compose up -d --build --no-deps <svc>`).
  Container `web` (`:3300`) chỉ được rebuild qua `make e2e-web` bởi `qa`/`verifier`.
- Không thêm dependency runtime. CI chỉ `workflow_dispatch`. Đổi schema = migration mới, không phá dữ liệu,
  giữ enum/RPC `MARKETPLACE` ở backend. Không nới `pkg/common/auth` (thêm RPC công khai mới là được).
- Xoá code chết thay vì để lại; không viết lớp tương thích, không redirect giả, không mock thay dữ liệu thật.
- Giữ art direction/typeface/house rules hiện có; chữ người dùng tiếng Việt, không thuật ngữ kỹ thuật,
  không bịa số liệu, logo đối tác hay lời khen. Dùng skill `frontend-design` khi đụng UI, `codebase-memory`
  để tìm code Go. Không lộ lỗi nội bộ gateway ra UI.
- Không tin `shop_id`/`user_id`/`sales_channel`/`storefront_shop_id` từ client. Gian hàng LUÔN suy từ `Host`.
- Trước khi báo xong: lint/format/tsc (web: `cd web && npm run lint && npm run format:check && npx tsc --noEmit`;
  Go: `gofmt` + `golangci-lint` module mình sửa) và test liên quan xanh. Logic không tầm thường có test.
- Board: chỉ `@` người phải hành động tiếp. Xong: `@coordinator [done] <tóm tắt, file đã đổi, lệnh kiểm
  tra + đầu ra>`. Bị chặn: `@coordinator [blocked] <lý do>`. Tham chiếu bài cũ bằng `#seq`.

## Bảng sở hữu (web = thư mục `web/`)
| Worker | Sở hữu |
|---|---|
| web-root | `src/app/(shop)/**` TRỪ các thư mục giao cho web-site (dưới); `src/app/robots.ts`, `src/app/sitemap.ts`; `src/features/platform/**` (mới: trang giới thiệu, danh bạ site, fetch danh bạ phía server); `src/components/**` trừ `ProductCard*` và `SessionProvider.tsx`; `src/data/**`, `src/mock/**` (không xoá file người khác còn import – hỏi trên board) |
| web-site | `src/app/(site)/**`; các thư mục `src/app/(shop)/{gio-hang,thanh-toan,don-hang,tai-khoan,tra-cuu-don,tra-cuu-don-hang,tim-kiem,flash-sale,voucher}` (chuyển sang `(site)` rồi xoá khỏi `(shop)`); `src/features/{site,commerce,account,catalog,product}/**` TRỪ `features/commerce/checkoutPayload.ts`; `src/context/**`; `src/components/ProductCard*`; `src/lib/{catalog,catalog-map,search,sizing,provinces,format,api}.ts`; `web/scripts/**` |
| bff | `src/middleware.ts`; `src/lib/server/**`; `src/lib/site/**` (host, cache, seo – gồm canonical, theme, test); `src/app/layout.tsx`; `src/components/SessionProvider.tsx`; `src/app/api/{auth,cart,checkout,payments,vouchers,orders}/**`; `src/features/commerce/checkoutPayload.ts`; `web/next.config.ts`, `web/package.json` (chỉ script, không dependency) |
| seller-admin | `src/app/(seller)/**`, `src/app/(admin)/**`, `src/features/{seller,admin}/**`, `src/app/api/{seller,admin}/**`, `src/lib/salesChannel.ts` |
| backend | `services/**`, `proto/**`, `gen/**`, `pkg/**`, migration, `tools/seed/**`, `tests/e2e/**`, `Makefile`, `docker-compose.yml`, `.env.example` |
| qa | `tests/e2e-web/**` |
| verifier | chỉ đọc + chạy gate; viết `docs/PHASE4_REPORT.md` |
| reviewer | chỉ đọc + review diff web + `inspect.mjs`; viết `web/DESIGN_REPORT.md`, `web/FEATURE_MATRIX.md` |

Test unit chung: `features/commerce/checkoutLogic.test.mjs` thuộc web-site; test của `checkoutPayload.ts`
chuyển sang file mới `src/lib/server/checkout.test.mjs` (bff), web-site xoá phần tương ứng khỏi file cũ.
Form đăng nhập/đăng ký: web-site tách thành component dùng chung trong `features/account/` (có prop
biến thể `site` | `platform`), post `#seq` khi sẵn sàng; web-root đổi trang gốc `/dang-nhap`, `/dang-ky`
sang dùng component đó.

## Hợp đồng giao diện (chốt, không tự đổi – muốn đổi thì hỏi coordinator)
1. REST công khai danh bạ (backend làm): `GET /api/v1/sites?page=1&page_size=24` (page_size tối đa 48),
   chỉ site `PUBLISHED` của shop `ACTIVE`, sắp xếp `published_at DESC`. Trả:
   `{"sites":[{"site_id","shop_id","shop_name","subdomain","primary_host","logo_url","cover_url","tagline"}],"total","page","page_size"}`.
   `tagline` = mô tả ngắn lấy từ config đã publish/hồ sơ shop (có thể rỗng); URL ảnh có thể rỗng.
   gRPC tương ứng trong `services/user` (`ListPublishedSites`), công khai không JWT.
2. Đơn của người mua theo gian hàng (backend làm nếu chưa có): `GET /api/v1/orders?storefront_shop_id=<uuid>`
   lọc ở backend (phân trang đúng); `GET /api/v1/orders/{id}` trả `storefront_shop_id` + `sales_channel`.
3. BFF (bff làm): `resolveSiteFromRequest(req|headers)` trong `src/lib/server/sites.ts` →
   `{siteId, shopId, host} | null`, chỉ từ `Host` (theo luật `TRUST_PROXY_HEADERS`) + site PUBLISHED.
   API người mua trên site host không nhận `shop_id` từ client; client (web-site) gọi `/api/cart`,
   `/api/checkout`, `/api/orders`... tương đối, không gửi `shop_id`/`channel`.
4. Seed (backend làm): 4 gian hàng mẫu `bach-dang`, `sai-gon`, `da-nang`, `ha-noi` đều PUBLISHED, theme khác
   nhau rõ rệt (màu nền/nhấn/phông), idempotent, có voucher SHOP cho từng shop.
