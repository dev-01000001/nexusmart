# Web Frontend (`web/`)

Next.js 15 (`^15.5.29`) App Router, React 19 (`^19.1.0`), TypeScript 5.8 (`^5.8.3`, `strict: true`, alias `@/*` → `src/*`), runtime Node 24. Mô hình **đa cửa hàng độc lập theo tên miền phụ & tên miền riêng** (đã **bỏ sàn chung**): gồm **4 route group** với **53 `page.tsx`**, **2 chuyển hướng `308` (`route.ts`)** và **183 BFF API route handler tường minh (`src/app/api/**`)**:

- **`(shop)` — Host gốc nền tảng (`<SITE_ROOT_DOMAIN>`, mặc định `nexusmart.localhost`, hoặc `localhost`):** **7 trang** chia 2 nhóm layout và **2 chuyển hướng `308`**:
  - `(shop)/(public)` (bọc `PlatformHeader` / `PlatformFooter`): `/`, `/ve-nexusmart`, `/tro-giup`, cùng 2 chuyển hướng `308` (`/gian-hang/[slug]`, `/san-pham/[slug]`) sang site đã xuất bản.
  - `(shop)` auth đứng riêng (`AuthSplit`, không có đầu/cuối trang nền tảng): `/dang-nhap`, `/dang-ky`, `/quen-mat-khau`, `/dat-lai-mat-khau`.
  - Mọi Buyer API trả `404` trên host gốc.
- **`(site)/s/[host]` — Site gian hàng độc lập (`<subdomain>.<SITE_ROOT_DOMAIN>` hoặc tên miền riêng đã xác minh):** **21 trang** khép kín toàn bộ hành trình mua sắm (`/`, `/san-pham`, `/san-pham/[slug]`, `/tim-kiem`, `/flash-sale`, `/voucher`, `/gio-hang`, `/thanh-toan`, `/thanh-toan/cho/[id]`, `/thanh-toan/mock-zalopay`, `/don-hang`, `/don-hang/[id]`, `/tra-cuu-don-hang`, `/tra-cuu-don`, `/tai-khoan`, `/gioi-thieu`, `/chinh-sach`, `/dang-nhap`, `/dang-ky`, `/quen-mat-khau`, `/dat-lai-mat-khau`). `src/middleware.ts` tự rewrite theo `Host`; truy cập trực tiếp `/s/*`, `/seller/*`, `/admin/*` trên host site trả `404`.
- **`(seller)` — Kênh người bán (chỉ trên host gốc):** **16 trang** (`/seller`, mở gian hàng ngay khi chưa có shop, sản phẩm `/seller/san-pham`, `/seller/san-pham/moi`, `/seller/san-pham/[id]`, đơn hàng `/seller/don-hang`, `/seller/don-hang/[id]`, trả hàng `/seller/tra-hang`, khuyến mãi `/seller/khuyen-mai`, tài chính `/seller/tai-chinh`, Site Studio `/seller/site`, `/seller/site/xem-truoc`, `/seller/site/ten-mien`, chat `/seller/chat`, báo cáo AI `/seller/bao-cao-ai`, hồ sơ `/seller/ho-so`, tương tác `/seller/tuong-tac`).
- **`(admin)` — Cổng quản trị (chỉ trên host gốc):** **9 trang** (`/admin`, `/admin/gian-hang`, `/admin/kiem-duyet`, `/admin/sites`, `/admin/flash-sale`, `/admin/doi-soat`, `/admin/khieu-nai`, `/admin/cau-hinh`, `/admin/audit-log`) phân quyền theo 6 vai trò nội bộ (`src/lib/admin-rbac.ts`). Voucher sàn và Xu ở Admin giữ chế độ chỉ-đọc.

## Giao diện và Style

- Nguồn tham chiếu: skill `design-system` (`.claude/skills/design-system/SKILL.md`), đặc tả `.agents/briefs/shadcn-redesign/design.md` và `docs/adr/0011-shadcn-ui-tailwind-v4.md`.
- **Một thư viện UI duy nhất**: **shadcn/ui** (`web/components.json`: style `new-york`, gói hợp nhất `radix-ui`, icon `lucide-react`) trong `src/components/ui/` (35 primitive) và bộ pattern ứng dụng trong `src/components/app/` (15 component + 3 helper).
- **Tailwind CSS v4 + preflight toàn cục**: CSS toàn cục duy nhất nằm ở `src/styles/ui.css`, bật `@import "tailwindcss/preflight.css" layer(base);`, nạp ở `src/app/layout.tsx`, đồng thời chứa rule nền `html`/`body`, nền site tối (`html:has([data-site-root][data-site-theme-mode="dark"])`), `:focus-visible` và CSS Reveal/PageTransition. Riêng khu công khai/trang chủ nạp `src/features/platform/home/home-tokens.css` (scope `[data-platform-public="true"]` gắn ở `src/app/(shop)/(public)/layout.tsx`) và `src/features/platform/home/home-keyframes.css`.
- **Biến `--ui-*` và `@theme inline`**: toàn bộ màu, bo góc, bóng và font định nghĩa qua biến `--ui-*` trong `src/styles/ui.css` và ánh xạ sang tên Tailwind chuẩn (`bg-background`, `text-foreground`, `text-muted-foreground`, `bg-primary`, `text-brand`, `border-border`, 6 tông nhãn `bg-badge-*-bg` / `text-badge-*-ink`…) qua `@theme inline`. Không hard-code mã hex rời trong component.
- **Font chữ**:
  - Phần ứng dụng (`(shop)`, `(seller)`, `(admin)`): **Inter** (`--font-inter`, `subsets: ["latin", "vietnamese"]`, `preload: false` trong `src/lib/fonts.ts`; `(shop)/(public)/layout.tsx` thêm `axes: ["opsz"]` cho trang chủ `/`). Mọi số liệu và tiền VND (`Money` / `formatVnd`) dùng `tabular-nums`.
  - Site gian hàng (`(site)`): hỗ trợ **8 mẫu Template JSON v2** (gồm **2 mẫu nền tối**: `sport-active`, `luxury-maison`) và **5 font thật** (`Work Sans`, `Be Vietnam Pro`, `Inter`, `IBM Plex Sans`, `Plus Jakarta Sans` qua `ALLOWED_SITE_FONTS` / `SITE_FONT_LABELS` trong `src/lib/site/types.ts` và `resolveSiteUiFontStack` / `buildSiteUiVars` trong `src/lib/site/theme.ts`). `SiteThemeRoot` (`src/features/site/shell/SiteThemeRoot.tsx`) gắn `data-site-root` và `data-site-theme-mode="light"|"dark"` (làm neo cho `@custom-variant dark` và `html:has([data-site-root][data-site-theme-mode="dark"])`).
- **`UiScope` và portal Radix**: giữ `UiScope` (`src/components/ui/ui-scope.tsx`) làm host cung cấp portal container qua `usePortalContainer()` cho mọi `*Portal` của Radix (`Dialog`, `AlertDialog`, `Sheet`, `Popover`, `DropdownMenu`, `Select`, `Tooltip`) để overlay thừa kế đúng `--ui-*` và `[data-site-theme-mode="dark"]` trên site tối. Không còn CSS reset bám `data-ui`.
- **Bề mặt và bóng**:
  - Phần ứng dụng (`AppShell`, `AuthSplit`, trang nội dung): dùng 3 bậc bóng nhẹ (`shadow-card`, `shadow-button`, `shadow-overlay`), không dùng gradient.
  - Site gian hàng: giữ bề mặt phẳng (`--ui-shadow-card: none`, `--ui-shadow-button: none`), chỉ giữ `shadow-overlay` cho menu, hộp thoại, sheet và toast (`SiteToasterInner` thích ứng `data-site-theme-mode` và `--ui-*`).
  - Ngoại lệ duy nhất: trang chủ nền tảng `/` (`PlatformHome` và nhánh `data-home="true"` của `PlatformHeader`) được dùng gradient, quầng sáng `radial-gradient`, `mask-image`, bóng và bộ biến `--home-*`.
- **Import từng file và ngân sách First Load JS ở site gian hàng**:
  - Trong `src/app/(site)/**` và `src/features/{site,search,commerce,account,chat,assistant}/**`, **import thẳng từng file** (ví dụ `@/components/app/Money`, `@/components/app/EmptyState`, `@/features/site/SitePdpView`), **không** import qua barrel `@/components/app` hoặc `@/features/site` để tránh kéo `AppShell` (`cmdk`, `DropdownMenu`, `Tooltip`, `Sidebar`) hoặc `SiteRenderer` vào mọi trang mua hàng.
  - Các overlay nặng trên site (`SiteMobileDrawer`, `SiteSearchModal`, `SiteToaster`, widget chat/trợ lý trong `SiteSupportDock`) nạp theo nhu cầu bằng `next/dynamic`; `Toaster` đặt ở layout từng khu vực (`(shop)/layout.tsx`, `(admin)/layout.tsx`, `SellerShell`, `SiteHostLayout`), không đặt ở `src/app/layout.tsx` gốc.
  - Ngân sách **First Load JS** của trang chủ site (`/s/[host]`) và PDP (`/s/[host]/san-pham/[slug]`): **≤ 296 kB**.
- **Phụ thuộc UI runtime đã duyệt (ADR-0011, 10 gói)**: `tailwindcss`, `@tailwindcss/postcss`, `tw-animate-css`, `class-variance-authority`, `clsx`, `tailwind-merge`, `radix-ui`, `lucide-react`, `cmdk`, `sonner`. Không tự ý cài thêm `next-themes`, `recharts`, `@tanstack/react-table`, `react-hook-form`, `zod`, `react-day-picker`, `vaul`, `input-otp`.
- **Ngôn ngữ & a11y**: 100% tiếng Việt tự nhiên, không để lộ thuật ngữ kỹ thuật nội bộ (JWT, HMAC, Saga, Redis Lua, sandbox, sổ cái ghi kép, optimistic locking) trên giao diện người mua. Mỗi trang đúng một `h1`, nút icon có `aria-label` tiếng Việt, vùng chạm mobile ≥ 40×40px, vòng focus rõ trên cả nền sáng và nền tối.

## Lệnh kiểm tra

### Cổng tĩnh, unit test & build (chạy trong `web/`)

```bash
cd web
npm run dev           # chạy local Next.js dev server
npm run lint          # eslint .
npm run format:check  # prettier --check "src/**/*.{ts,tsx,css}" (config: web/.prettierrc)
npm run test:unit     # node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test "src/**/*.test.mjs" "scripts/**/*.test.mjs"
npm run check:data    # node scripts/check-data.mjs (kiểm toàn vẹn bộ dữ liệu seed/media & hàm thuần)
npm run build         # next build (kiểm tra TypeScript + SSR/route)
```

### Kiểm tra luồng UI, trạng thái & đối chiếu ảnh (chạy từ gốc repo)

Khi nhiều tiến trình chạy song song, bọc lệnh trình duyệt bằng `flock /tmp/nm-web-browser.lock`:

```bash
# Luồng chức năng theo khu vực (platform, admin, seller, site, templates — 8 mẫu × COD)
node tests/ui-flows/run.mjs --port <port> --area platform|admin|seller|site|templates

# Chụp trạng thái overlay mở (⌘K, Dialog, Sheet, Popover, Toast), vòng Tab focus và Reveal
node tests/ui-flows/states.mjs --port <port> --out <dir>

# Chụp ảnh 3 khổ (390/768/1440) + kiểm HTTP, console, hydration, tràn ngang, số h1
node .agents/tools/ui-shots.mjs --port <port> --login none|seller|admin|buyer \
  --routes-file tests/ui-flows/routes/<area>.txt --widths 390,768,1440 --out <dir>

# Chụp cuộn theo viewport cho từng vị trí (dùng đối chiếu pixel trang chủ /)
node .agents/tools/scroll-shots.mjs <out-dir> <port> [path=/] [widths=390,768,1440]

# So pixel hai thư mục ảnh cùng tên (YAVG=0, YMAX=0 là giống hệt)
.agents/tools/ui-diff.sh <base-dir> <cand-dir> [out-dir]

# Dựng bản production snapshot tạm để đo First Load JS và kiểm nghiệm
.agents/tools/snap-build.sh <port> [path...]
```

Lưu ý:

- Build với `NEXT_DIST_DIR` (ví dụ `NEXT_DIST_DIR=.next-dev npm run build`) có thể tự chèn dòng vào `web/tsconfig.json`; bắt buộc hoàn nguyên `web/tsconfig.json` trước khi commit.
- Kiểm thử E2E qua BFF trong mạng compose chạy từ gốc repo bằng `make e2e-web` (`tests/e2e-web/*.test.mjs`); nạp dữ liệu demo bằng `make seed` (`tools/seed/seed.mjs`).
- `web/next.config.ts` đặt `output: "standalone"`, `distDir: process.env.NEXT_DIST_DIR || ".next"`, `images.unoptimized: true`, `reactStrictMode: true` và cấu hình security headers/CSP.
- `web/postcss.config.mjs` chỉ khai `"@tailwindcss/postcss": { optimize: false }`: tắt bước Lightning CSS optimize mặc định (vốn ép alpha sang hex 8-bit như `rgb(2 9 10 / 0.62)` → `#02090a9e`, làm trang chủ lệch tới `6/255`; `optimize: false` giữ lệch `≤ 1/255` với CSS production `230.521 B raw / 38.000 B gzip` so mặc định `230.443 B / 38.041 B`). Tailwind v4 vẫn tự phát tiền tố `-webkit-` và Next.js vẫn minify CSS ở production.

## Hiện trạng dữ liệu và Hạ tầng

- **Nối backend thật 100% qua BFF tường minh (đã bỏ `/api/gw/*` catch-all và bỏ dữ liệu mẫu thương mại):**
  - Trình duyệt chỉ gọi các route tường minh trong `src/app/api/**` (183 route); Next server gọi Gateway qua mạng Docker nội bộ `bff` (`172.31.250.0/28`, `GATEWAY_URL=http://gateway-bff:8080`, 0 header CORS trên gateway).
  - **Cô lập tenant theo `Host`:** BFF luôn suy ra `shop_id` từ header `Host` (theo `src/lib/site/host.ts` & `src/lib/server/siteLookup.ts`, chỉ chấp nhận site `PUBLISHED` của shop `ACTIVE`) và ép `sales_channel = "STOREFRONT"` + `storefront_shop_id` trên mọi thao tác mua sắm (`src/lib/server/storefront.ts`); không bao giờ gửi `MARKETPLACE` từ web.
  - **Phiên & bảo mật:** Cookie `HttpOnly`, `SameSite=Lax`, **host-only** (`__Host-nm_at`/`nm_at`, `__Host-nm_rt`/`nm_rt` trong `src/lib/server/config.ts`, `tokens.ts`, `session.ts`), xoay refresh token chống thu hồi nhầm token family; kiểm CSRF `Origin`/same-origin (`src/lib/server/http.ts`); `src/middleware.ts` và Server Component kiểm phiên + RBAC qua backend `/api/v1/users/me`.
  - **Dữ liệu thật đầy đủ:** danh bạ site & 8 mẫu Template JSON v2, sản phẩm/SKU/tồn/đánh giá/tóm tắt đánh giá AI, tìm kiếm cô lập theo shop (`services/search`; chưa mở UI lịch sử tìm kiếm trên site), flash sale, voucher `SHOP`, chương trình giảm giá shop, giỏ hàng báo SKU không khả dụng, tính phí ship 2 cấp (34 tỉnh / 3.321 phường), checkout COD / VietQR / ZaloPay (`mock-zalopay` chỉ bật ở chế độ mock) + SSE chờ thanh toán `/thanh-toan/cho/[id]`, đơn hàng/đổi địa chỉ/đổi trả 10 bước/giao lại/hoàn tiền/tra cứu đơn khách, OTP SĐT, cài đặt thông báo, quên/đặt lại mật khẩu, chat 1-1 WebSocket, trợ lý AI SSE, cùng đầy đủ màn Seller (nhập CSV, lên đơn lô, nhãn PDF A6, báo cáo, tài khoản ngân hàng, Site Studio, tên miền riêng) và Admin (6 vai trò, phê duyệt 2 người, đối soát VietQR/COD CSV, từ khóa cấm, cấu hình phương thức thanh toán & hãng vận chuyển).
  - **Dữ liệu tĩnh & `localStorage`:** `src/data/*` phục vụ bộ `tools/seed/seed.mjs` và phần biên tập phụ; `localStorage` tuyệt đối không lưu token, giỏ hàng hay đơn hàng — chỉ lưu 4 tùy chọn tiện ích người xem trong `src/context/StoreContext.tsx` (`nexusmart_viewer_prefs_v1`), trạng thái thu gọn thanh bên của `AppShell` / `Sidebar` (`nm.seller.sidebarCollapsed` trong `src/features/seller/shell/sellerNav.ts`, `nm.admin.sidebar.v1` trong `src/features/admin/shell/nav.ts`) và trạng thái thu gọn thẻ hướng dẫn thiết lập (`src/components/app/SetupGuide.tsx`).
- **Hạ tầng:** `web/Dockerfile` build đa giai đoạn (context gốc repo, Node 24 standalone, non-root `USER nextjs:10001`), service `web` trong `docker-compose.yml` (cổng host `WEB_PORT`, mặc định `3300`), job `web` trong `.github/workflows/ci.yml` (`workflow_dispatch`).

## Bố cục `src/`

- `src/app/` — `layout.tsx` (nạp `fontVariableClassNames` từ `src/lib/fonts.ts`, `src/styles/ui.css`, `SessionProvider`, `StoreProvider`, `RevealObserver`), `not-found.tsx`, `robots.ts`, `sitemap.ts`, `api/` (183 BFF route handler tường minh) và 4 route group `(shop)` (gồm `(public)` và 4 trang auth đứng riêng), `(site)/s/[host]`, `(seller)`, `(admin)`.
- `src/middleware.ts` — phân loại host, chặn đường dẫn trái host/trực tiếp `/s/*`, redirect `308` về custom `primary_host`, rewrite sang `/s/[host]`, chủ động xoay refresh token và bảo vệ tuyến yêu cầu đăng nhập.
- `src/components/` —
  - `ui/` — 35 primitive shadcn/ui (`accordion`, `alert`, `alert-dialog`, `avatar`, `badge`, `breadcrumb`, `button`, `card`, `checkbox`, `collapsible`, `command`, `dialog`, `dropdown-menu`, `field`, `input`, `label`, `pagination`, `popover`, `progress`, `radio-group`, `scroll-area`, `select`, `separator`, `sheet`, `sidebar`, `skeleton`, `sonner`, `switch`, `table`, `tabs`, `textarea`, `toggle`, `toggle-group`, `tooltip`, `ui-scope`);
  - `app/` — 15 pattern dùng chung (`AppShell`, `AuthSplit`, `DetailLayout`, `EmptyState`, `ErrorState`, `IndexTable`, `KpiChartCard`, `MetricStrip`, `Money`, `PageHeader`, `SaveBar`, `SetupGuide`, `StatusBadge`, `Timeline`, `charts`) và 3 helper (`navMatch.ts`, `setupProgress.ts`, `statusTone.ts`), kèm barrel `index.ts`;
  - tệp dùng chung gốc: `Icons.tsx` (`NexusMark` và icon dùng ở trang chủ), `PageTransition.tsx`, `Reveal.tsx` (+ `revealUtils.ts`), `SessionProvider.tsx`.
- `src/features/` — 11 module theo nghiệp vụ: `account/`, `admin/`, `assistant/`, `chat/`, `checkout/`, `commerce/`, `platform/` (gồm `home/home-tokens.css`, `home/home-keyframes.css`), `product/`, `search/`, `seller/`, `site/`.
- `src/context/` — `StoreContext.tsx` (giỏ hàng thật từ `/api/cart` + 4 tùy chọn tiện ích người xem).
- `src/lib/` — `utils.ts` (`cn`), `fonts.ts` (Inter + 4 font site bổ sung qua `next/font/google`), `server/` (`address.ts`, `config.ts`, `domains.ts`, `error-map.ts`, `errors.ts`, `gateway.ts`, `http.ts`, `proxy-security.ts`, `session.ts`, `siteLookup.ts`, `sites.ts`, `storefront.ts`, `tokens.ts`, `trace.ts`), `site/` (`cache.ts`, `domains.ts`, `host.ts`, `seo.ts`, `theme.ts`, `types.ts`), và các module dùng chung (`a8-types.ts`, `admin-rbac.ts`, `api.ts`, `catalog.ts`, `catalog-map.ts`, `format.ts`, `gateway-types.ts`, `payment-url.ts`, `provinces.ts`, `redirect.ts`, `salesChannel.ts`, `search.ts`).
- `src/data/` — dữ liệu seed dùng chung cho `tools/seed/seed.mjs` và phần biên tập nền tảng (`flash-sale.ts`, `media.ts`, `orders.ts`, `products.ts`, `shops.ts`, `vouchers.ts`).
- `src/styles/` — chỉ còn `ui.css` và `ui-vars.test.mjs`.

## Tài liệu thiết kế và QA

- Skill thiết kế hiện hành: `.claude/skills/design-system/SKILL.md` (symlink `.agents/skills/design-system/SKILL.md`).
- Báo cáo thiết kế, ma trận tuyến & tích hợp: `web/DESIGN_REPORT.md`, `web/FEATURE_MATRIX.md`, `docs/WEB_INTEGRATION_REPORT.md`, `docs/adr/0011-shadcn-ui-tailwind-v4.md`.
- Khi dựng mới hoặc sửa giao diện, đối chiếu skill `design-system`, chạy `tests/ui-flows/run.mjs` / `tests/ui-flows/states.mjs` và kiểm tra trực quan ở 3 khổ `390px`, `768px`, `1440px`.
