# Báo cáo Swarm T – Hệ thống Template cho Site Gian hàng

- **Ngày hoàn thành:** 2026-11-21
- **Nhánh git:** `phase-0b` (không commit, không đổi nhánh, không rebuild stack hay chạy `make e2e`/`make seed`/`make e2e-web`)
- **Đặc tả & hợp đồng:** `.agents/briefs/m-t-templates.md`, `.agents/briefs/m-all-contract.md`, `docs/adr/0001-site-template-format.md`, `docs/reports/g-web.md`

---

## 1. Hạng mục đã hoàn thành

### 1.1 Biểu quyết định dạng & ADR (`docs/adr/0001-site-template-format.md`)
- Thực hiện tranh luận và bỏ phiếu thực tế giữa **3 worker độc lập** (`prop-json`, `prop-liquid`, `prop-mdx`) trên bảng tin Swarm T (`#4`–`#10`):
  - **Phương án A:** Declarative Theme JSON Schema v2 (`prop-json`)
  - **Phương án B:** Sandboxed Liquid-Subset DSL (`prop-liquid`)
  - **Phương án C:** Component-Restricted MDX Subset (`prop-mdx`)
- **Kết quả:** **3–0 nhất trí chọn Phương án A (`Declarative Theme JSON v2`)** (hạng 2: B, hạng 3: C), vì không cần parser tự viết ở cả Go và TypeScript (tuân thủ Ponytail 0 dependency mới), bề mặt bảo mật XSS/SSRF nhỏ nhất, đồng bộ 1–1 không mất dữ liệu giữa trình sửa trực quan và mã JSON có báo lỗi theo dòng/cột, tốc độ render RSC tối đa và chuyển đổi không mất dữ liệu cho 4 site seed v1.
- Đã ghi đầy đủ phân tích 5 tiêu chí, phiếu bầu, đặc tả Schema v2 và ràng buộc bảo mật vào `docs/adr/0001-site-template-format.md`.

### 1.2 Thư viện 8 Template khác biệt thực sự về bố cục (phủ đủ 5 loại trang)
Đồng bộ 1–1 giữa Backend (`services/user/internal/site/templates_catalog.go`, `services/user/migrations/0004_site_templates.sql`) và Frontend (`web/src/features/site/templateCatalog.ts`):

| # | `id` | Tên mẫu | Khung Header / Footer | Trang chủ | Danh sách (`/san-pham`) | Chi tiết SP (`/san-pham/[slug]`) | Giỏ hàng (`/gio-hang`) | Thông tin (`/gioi-thieu`, `/chinh-sach`) |
|---|---|---|---|---|---|---|---|---|
| 1 | `minimal-fashion` | Thời trang tối giản | `minimal-split` / `minimal-row` | Hero căn giữa + lưới 3 cột thoáng + text căn giữa | `minimal-gallery` (3 cột thoáng) | `classic-split` | `split-summary` | `split-hero` |
| 2 | `editorial` | Tạp chí Editorial | `centered-masthead` / `editorial-cols` | Hero masthead + Lookbook bất đối xứng 1 lớn 2 nhỏ + pull-quote | `editorial-magazine` (2 cột lớn) | `editorial-tall` (ảnh cuộn dọc + buybox dính) | `editorial-ledger` | `editorial-essay` (có TOC cố định) |
| 3 | `dense-grid` | Lưới dày hiệu suất | `utility-compact` / `compact-bar` | Compact bar hero + voucher strip đầu trang + lưới 5 cột | `top-bar` (5 cột mật độ cao) | `dense-spec` (3 vùng gọn + bảng SKU) | `compact-table` | `compact-faq` |
| 4 | `craft-atelier` | Xưởng thủ công | `atelier-framed` / `atelier-footer` | Hero xưởng + dải thông số kiểm định 4 cột (`metrics_strip`) + timeline | `sidebar-left` (sidebar lọc trái cố định) | `atelier-story` (kèm dải câu chuyện chất liệu) | `atelier-bag` | `craft-timeline` (quy trình 4 bước + hồ sơ xưởng) |
| 5 | `sport-active` | Thể thao năng động | `bold-dark` / `bold-dark` | Hero thể thao chéo nền tối + dải telemetry hiệu năng + voucher | `top-bar` (4 cột nền tối) | `sport-telemetry` | `express-bar` | `athletic-manifesto` |
| 6 | `luxury-maison` | Cao cấp Maison | `maison-centered` / `maison-minimal` | Hero khung viền đôi + lưới 2 cột rộng + Lookbook song song | `luxury-curated` (2 cột phòng trưng bày) | `luxury-framed` (khung gallery) | `luxury-folio` | `luxury-maison` |
| 7 | `one-page-launch` | Một trang (Landing) | `anchor-minimal` / `minimal-row` | Fullbleed hero + thông số + sản phẩm + đánh giá (`testimonials`) + voucher + FAQ | `single-strip` (danh sách ngang gọn) | `one-page-drawer` | `express-bar` | `compact-faq` |
| 8 | `multi-sku-catalog` | Catalog nhiều SKU | `catalog-rail` / `compact-bar` | Compact hero + bảng ma trận SKU (`sku_matrix`) + danh sách biến thể | `sku-table` (chuyển đổi bảng SKU từng size/màu và lưới) | `sku-order-matrix` (bảng chọn biến thể SKU trực tiếp) | `compact-table` | `b2b-catalog-sheet` |

Toàn bộ 8 bảng màu đạt chuẩn tương phản WCAG 2.1 AA (`>= 4.5:1`) trên cả 3 cặp bắt buộc (`bg/text`, `surface/text`, `accent/accent_text`).

### 1.3 Backend (`services/user/`, `proto/nexusmart/user/`, `pkg/common/auth/`, `services/gateway/`)
- **Validator & Security (`services/user/internal/site/validate.go`, `schema_v2.go`, `templates_catalog.go`):**
  - Pre-decode streaming JSON check: giới hạn kích thước `<= 128 KiB`, độ sâu lồng nhau `<= 8`, **chặn khóa trùng lặp (duplicate keys)**, chặn unknown fields và trailing tokens.
  - Từ chối XSS, style injection và SVG tại save-time cho cả v1 và v2 (`<script>`, `</script>`, `<style>`, `<svg>`, `<iframe>`, `on*=`, `javascript:`, `vbscript:`, `data:`, `expression(`, `url(`, `@import`, `-moz-binding`).
  - `ValidateImageURL`: chặn `.svg`/`.svgz`, `image/svg`, `svg+xml` và chặn SSRF (IPv4/IPv6/IPv4-mapped IPv6/IP dạng số nguyên-hex-octal, `localhost`, `*.localhost`, RFC1918, `169.254.169.254`, `metadata.google.internal`, `*.local`, `*.internal`, hostname 1 nhãn).
  - Hỗ trợ đầy đủ 11 loại section (`hero`, `featured_products`, `collection`, `text`, `banner`, `voucher_strip`, `policies`, `lookbook`, `metrics_strip`, `sku_matrix`, `testimonials`), 9 loại block (`heading`, `paragraph`, `badge`, `cta`, `image`, `stat`, `policy`, `quote`, `feature`), `variant`, `settings`, và `pages.{catalog,pdp,cart,info}` (giới hạn 20 section trang chủ, 8 section/trang phụ, 32 tổng, 12 block/section).
  - `ConvertV1ToV2`: chuyển đổi xác định, không mất dữ liệu toàn bộ 4 site seed cũ (`bach-dang`, `sai-gon`, `da-nang`, `ha-noi`).
  - `ApplyTemplateToConfig`: áp dụng cấu trúc mẫu mới nhưng giữ nguyên nhận diện thương hiệu (`brand_name`, `tagline`, `logo_url`).
- **Migration & SQLC:**
  - `services/user/migrations/0004_site_templates.sql`: tạo bảng `site_templates`, thêm cột nullable `sites.template_id`, seed idempotent 8 template v2 hợp lệ. Không sửa trigger bất biến `trg_site_versions_immutable`.
  - `services/user/queries/site_templates.sql` + `sqlc generate` → `services/user/internal/db/{models,querier,site_templates.sql}.go` (`queries/site.sql` không đụng vào).
- **Proto, Auth, User gRPC & Gateway REST:**
  - `proto/nexusmart/user/v1/user.proto` + `buf generate --path proto/nexusmart/user/v1/user.proto`: thêm `SiteTemplate`, `ListSiteTemplates`, `GetSiteTemplate`, `ApplySiteTemplate`.
  - `pkg/common/auth/auth.go`: đăng ký `"/nexusmart.user.v1.UserService/ApplySiteTemplate": false` (bắt buộc end-user JWT).
  - `services/user/internal/service/site_templates.go` + `cmd/user/main.go`: `BootstrapSiteTemplates`, `ListSiteTemplates` (công khai), `GetSiteTemplate` (công khai), `ApplySiteTemplate` (seller/admin theo JWT `shop_id`, chỉ ghi bản nháp, ghi audit log `site.apply_template`).
  - `services/gateway/internal/router/handlers_t.go` + 2 dòng mount trong `router.go`:
    - `GET /api/v1/site-templates` (công khai)
    - `GET /api/v1/site-templates/{id}` (công khai)
    - `POST /api/v1/seller/site/apply-template` (seller, lấy `shop_id` từ JWT)
    - `PUT /api/v1/seller/site/draft` (có sẵn, nhận config v1 và v2)

### 1.4 Frontend Site Gian hàng & Tích hợp Swarm G (`web/src/features/site/`, `web/src/app/(site)/`)
- Shared TS contract & zero-dep line-aware validator: `templateTypes.ts`, `templateValidator.ts` (RFC 8259 parser tự viết trả chính xác `line`/`column`, chống duplicate key, depth `<= 8`, XSS/SSRF/SVG, WCAG `>= 4.5:1`), `templateCatalog.ts`.
- Storefront engine: `siteContext.server.ts`, `SiteShell.tsx`, `SiteChrome.tsx`, `SiteRenderer.tsx`, `SiteProductCard.tsx`, `SiteCatalogView.tsx`, `SitePdpView.tsx`, `SiteInfoView.tsx`, `siteTemplates.module.css`, và các trang `app/(site)/s/[host]/{layout,page,san-pham/page,san-pham/[slug]/page,gio-hang/page,gio-hang/CartClient,gioi-thieu/page,chinh-sach/page,tim-kiem/page}.tsx`.
- **Tích hợp đầy đủ cả 5 component của Swarm G** (theo `docs/reports/g-web.md`), điều khiển được qua `layout.slots`:
  - `SiteSearchBar` (`@/features/search`) trong header desktop & mobile drawer (`SiteChrome.tsx`).
  - `SiteSearchResults` (`@/features/search`) tại `app/(site)/s/[host]/tim-kiem/page.tsx`.
  - `SiteAssistantWidget` (`@/features/assistant`) & `SiteChatWidget` (`@/features/chat`) tại cuối `SiteShell.tsx` (chỉ bật ngoài chế độ xem trước khi có `shopId` và slot được bật).
  - `ProductReviewSummary` (`@/features/assistant`) trong `SitePdpView.tsx`.
- 0 `dangerouslySetInnerHTML`, 0 raw CSS/HTML injection.

### 1.5 Trình quản lý Template & Studio Người bán (`web/src/features/seller/site/`, `web/src/app/(seller)/seller/site/`, BFF)
- 3 BFF route mới:
  - `web/src/app/api/site-templates/route.ts`
  - `web/src/app/api/site-templates/[id]/route.ts`
  - `web/src/app/api/seller/site/apply-template/route.ts` (CSRF qua `apiRoute`, `assertSellerSession()`, không tin `shop_id` từ client, gọi `revalidateSiteCache`)
- Giao diện Seller Site Studio 6 tab (`SellerSiteEditorClient.tsx`):
  1. **Thư viện mẫu (`TemplateGalleryTab.tsx`):** lọc danh mục, thẻ thông số bố cục, modal xem trước cả 5 loại trang (Desktop/Mobile) bằng `SiteShell` + `SiteRenderer`/`SiteCatalogView`/`SitePdpView` thật, áp dụng mẫu vào bản nháp (không tự xuất bản).
  2. **Giao diện & Bố cục:** subdomain, 5 màu + kiểm tra tương phản WCAG trực tiếp, 5 phông chữ, `radius`, `density`, chrome (`header_style`, `footer_style`, `container_width`, `card_style`, thanh thông báo), bật/tắt slot search/chat/assistant, và cấu hình riêng cho 4 trang phụ (`catalog`, `pdp`, `cart`, `info`).
  3. **Khối & Block (`SectionEditorPanel.tsx`):** chuyển đổi giữa 5 trang (`home`, `catalog`, `pdp`, `cart`, `info`); thêm/xóa/sắp xếp/ẩn hiện đủ 11 loại section, chọn `variant`, sửa `settings`, và trình sửa đầy đủ 9 loại `block` (thêm, xóa, sắp xếp lên/xuống, sửa thuộc tính có kiểm tra URL/XSS).
  4. **Mã template JSON (`TemplateCodeEditorTab.tsx`):** trình sửa mã monospace đồng bộ thanh số dòng, nút định dạng/đồng bộ/đặt lại, bộ đếm byte (`131.072 byte`), danh sách lỗi kiểm tra schema theo đúng **`Dòng X, Cột Y`** (nhấn để nhảy tới dòng lỗi), đồng bộ hai chiều với trình trực quan và khung **Xem trước trực tiếp** 5 trang + Desktop/Mobile.
  5. **Tên miền (`SellerSiteDomainsSection.tsx`):** giữ nguyên hoạt động đầy đủ.
  6. **Lịch sử phiên bản:** dùng nguyên cơ chế xuất bản và khôi phục `site_versions` sẵn có.
- Nâng cấp `/seller/site/xem-truoc` hỗ trợ `?view=home|catalog|pdp|cart|info`.

---

## 2. Lệnh kiểm tra và đầu ra thật

### 2.1 Backend: Proto lint, Go fmt/lint, Go `-race` tests (bao gồm XSS/SSRF/4 site seed)
```text
$ buf lint --path proto/nexusmart/user/v1/user.proto
PASS buf lint

$ gofmt -l & golangci-lint run -c .golangci.yml ./... (pkg/common, gen/go, services/user, services/gateway)
--> pkg/common
--> gen/go
--> services/user
--> services/gateway
PASS gofmt & golangci-lint

$ go test -race -count=1 ./... (pkg/common, services/user, services/gateway)
ok  	github.com/nexusmart/core/pkg/common/auth	1.040s
ok  	github.com/nexusmart/core/pkg/common/env	1.009s
ok  	github.com/nexusmart/core/pkg/common/logger	1.012s
ok  	github.com/nexusmart/core/pkg/common/metrics	1.014s
ok  	github.com/nexusmart/core/pkg/common/telemetry	1.013s
ok  	github.com/nexusmart/core/services/user/internal/cloudflare	1.044s
ok  	github.com/nexusmart/core/services/user/internal/db	1.013s
ok  	github.com/nexusmart/core/services/user/internal/password	1.189s
ok  	github.com/nexusmart/core/services/user/internal/security	1.014s
ok  	github.com/nexusmart/core/services/user/internal/service	2.547s
ok  	github.com/nexusmart/core/services/user/internal/site	1.317s
ok  	github.com/nexusmart/core/services/gateway/internal/httpx	1.011s
ok  	github.com/nexusmart/core/services/gateway/internal/middleware	1.014s
ok  	github.com/nexusmart/core/services/gateway/internal/proxy	1.014s
ok  	github.com/nexusmart/core/services/gateway/internal/router	1.137s

$ (cd services/user && go test -v -run 'Test.*(Template|XSS|SSRF|V1ToV2|Depth|Duplicate|Validate|Apply)' ./internal/site ./internal/service)
--- PASS: TestBuiltInTemplatesValidDistinctAndAccessible (0.01s)
--- PASS: TestMigration0004SiteTemplatesJSONBValidV2 (0.00s)
--- PASS: TestSecurityRejectsXSSStyleInjectionAndSVG (0.00s)
--- PASS: TestValidateImageURLSSRFDefense (0.00s)
--- PASS: TestStructuralLimitsDuplicateKeysUnknownFieldsAndCounts (0.00s)
--- PASS: TestConvertV1ToV2AllFourSeededSites (0.01s)
    --- PASS: TestConvertV1ToV2AllFourSeededSites/bach-dang (0.00s)
    --- PASS: TestConvertV1ToV2AllFourSeededSites/sai-gon (0.00s)
    --- PASS: TestConvertV1ToV2AllFourSeededSites/da-nang (0.00s)
    --- PASS: TestConvertV1ToV2AllFourSeededSites/ha-noi (0.00s)
--- PASS: TestApplyTemplateToConfigPreservesBrandIdentity (0.00s)
--- PASS: TestSiteTemplates_ListGetApplyRBACBrandAndDraftPreservation (0.01s)

$ (cd services/gateway && go test -v -run 'Test.*Template' ./internal/router)
--- PASS: TestGatewaySiteTemplateRoutes (0.00s)
```

### 2.2 Integration test migration Postgres 16 (container tạm `t-test-pg`, đã xóa sạch)
```text
$ TestRunMigrations_SiteTemplatesSeed (container postgres:16-alpine t-test-pg)
PASS (10.17s) — schema_migrations verified 4 rows (0001_init.sql..0004_site_templates.sql, idempotent 2nd run),
site_templates verified 8 active valid v2 rows.
$ docker ps -a --filter "name=t-test"
(0 containers — t-test-pg removed)
```

### 2.3 Web: Prettier, ESLint, 184 Unit Tests (bao gồm XSS/SSRF/Line-Col/4 Seeded Sites), TypeScript, Isolated Build (`NEXT_DIST_DIR=.next-t`)
```text
$ cd web && npm run format:check
All matched files use Prettier code style!

$ cd web && npm run lint
(0 errors, 0 warnings)

$ cd web && npm run test:unit
✔ XSS, style injection & SVG defense (35.164381ms)
✔ SSRF defense on image URLs (21.140703ms)
✔ Structural limits & parser hardening (11.613277ms)
✔ Line & column diagnostic accuracy (1.503499ms)
✔ Legacy v1 -> v2 conversion for all 4 seeded sites (4.109019ms)
...
ℹ tests 184
ℹ suites 10
ℹ pass 184
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ duration_ms 1130.863048

$ cd web && npx tsc --noEmit
(exit 0 — 0 errors)

$ cd web && NEXT_DIST_DIR=.next-t npm run build
 ✓ Compiled successfully in 83s
 ✓ Linting and checking validity of types
 ✓ Generating static pages (58/58)
├ ƒ /api/seller/site/apply-template                 366 B         103 kB
├ ƒ /api/site-templates                             366 B         103 kB
├ ƒ /api/site-templates/[id]                        366 B         103 kB
├ ƒ /s/[host]                                       259 B         211 kB
├ ƒ /s/[host]/chinh-sach                            259 B         211 kB
├ ƒ /s/[host]/gio-hang                            9.47 kB         173 kB
├ ƒ /s/[host]/gioi-thieu                            259 B         211 kB
├ ƒ /s/[host]/san-pham                              259 B         211 kB
├ ƒ /s/[host]/san-pham/[slug]                       259 B         211 kB
├ ƒ /s/[host]/tim-kiem                              196 B         156 kB
├ ƒ /seller/site                                  40.7 kB         273 kB
├ ƒ /seller/site/xem-truoc                        3.29 kB         214 kB

$ Cleanup & Invariants
PRE  web/.next: 1795246357 2026-11-21 14:32:37.581227748 +0700
POST web/.next: 1795246357 2026-11-21 14:32:37.581227748 +0700
INVARIANT OK: web/.next untouched
.next-t exists: CLEAN (absent)
domains.go / 15_custom_domains diff lines: 0
go.mod / package.json diff lines: 0
dangerouslySetInnerHTML matches (non-comment): 0
```

---

## 3. Đối chiếu tiêu chí nghiệm thu của Brief & Spec

| Yêu cầu trong `.agents/briefs/m-t-templates.md` | Trạng thái | Bằng chứng |
|---|---|---|
| 1. Biểu quyết 3 worker & ghi ADR `docs/adr/0001-site-template-format.md` | **Đạt** | Tranh luận & bỏ phiếu 3–0 (`prop-json`, `prop-liquid`, `prop-mdx`), chọn Declarative Theme JSON v2 |
| 2. ≥ 8 template khác nhau thực sự về bố cục, mỗi mẫu phủ đủ 5 trang (trang chủ, danh sách, PDP, giỏ, thông tin) | **Đạt** | 8 archetype độc lập đồng bộ Go/SQL/TS, đều đạt WCAG AA ≥ 4.5:1 |
| 3. Seller: xem trước & áp dụng vào nháp, sửa trực quan (section + block + settings + token WCAG), sửa mã có kiểm tra schema, báo lỗi theo dòng, xem trước trực tiếp, xuất bản & khôi phục qua phiên bản sẵn có | **Đạt** | 6 tab Seller Studio + 3 BFF route + xem trước 5 trang `/seller/site/xem-truoc?view=...` |
| 4. Backend: validator strict (kích thước, độ sâu, duplicate key), migration bảng `site_templates`, REST theo hợp đồng, chuyển đổi 4 site seed cũ | **Đạt** | `0004_site_templates.sql` test thật trên Postgres 16; `ConvertV1ToV2` test xanh trên cả 4 site seed; 3 REST route + `PUT /draft` |
| 5. Chèn component search/chat/assistant của Swarm G (`docs/reports/g-web.md`) | **Đạt** | Đã nối đủ 5 component: `SiteSearchBar`, `SiteSearchResults`, `SiteAssistantWidget`, `SiteChatWidget`, `ProductReviewSummary` |
| 6. Gate: Go lint/test cho `user` (& `gateway`/`pkg/common`); web lint, format, `test:unit`, `tsc`, build `.next-t`; test XSS (`<script>`, `javascript:`, style injection, SVG) & SSRF bị từ chối | **Đạt** | 100% cổng chạy xanh; 184/184 web unit test; Go `-race` xanh; XSS/SSRF/SVG bị từ chối ở cả Go và TS |

---

## 4. Phần chưa làm / Phạm vi để lại cho Swarm H

- **Chạy E2E sống (`make up`, `make seed`, `make e2e`, `make e2e-web`) và chụp ảnh kiểm định trực quan trên stack Docker:** theo hợp đồng chung `.agents/briefs/m-all-contract.md`, chỉ Swarm H được quyền rebuild stack và chạy E2E/seed chung. Toàn bộ migration, unit test, security test (XSS/SSRF/SVG/depth/duplicate-key), typecheck và production build cô lập (`NEXT_DIST_DIR=.next-t`) của Swarm T đã kiểm định đầy đủ và đạt 100%.
- **Không phát hiện lỗi cản trở nào ở phần của Swarm G** trong quá trình tích hợp 5 component (`SiteSearchBar`, `SiteSearchResults`, `SiteAssistantWidget`, `SiteChatWidget`, `ProductReviewSummary`) — cả lint, format, unit test và `next build` đều chạy xanh cùng nhau.
