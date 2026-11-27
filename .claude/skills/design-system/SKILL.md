---
name: design-system
description: "Hệ thống thiết kế UI NexusMart trên shadcn/ui (style new-york, radix-ui) + Tailwind CSS v4 (preflight bật toàn cục) và lớp biến --ui-* (Inter cho phần ứng dụng, 5 font thật cho site gian hàng). Dùng khi tạo/sửa trang, component, CSS, token hoặc review UI trong web/."
---

# NexusMart Design System (shadcn/ui + Tailwind v4)

Đặc tả đầy đủ: [design.md](../../../.agents/briefs/shadcn-redesign/design.md) · Quyết định kiến trúc: [ADR-0011](../../../docs/adr/0011-shadcn-ui-tailwind-v4.md). Ảnh tham chiếu Figma ở `.agents/briefs/shadcn-redesign/figma/`.

Toàn bộ `web/` dùng chung một bộ component **shadcn/ui** (`web/src/components/ui/`) và pattern ứng dụng (`web/src/components/app/`), styling bằng **Tailwind CSS v4** với **preflight bật toàn cục** tại `web/src/styles/ui.css` (nạp ở `web/src/app/layout.tsx`, cộng `web/src/features/platform/home/home-tokens.css` và `web/src/features/platform/home/home-keyframes.css` cho khu công khai/trang chủ `/`), `web/postcss.config.mjs` dùng `"@tailwindcss/postcss": { optimize: false }` và lớp biến `--ui-*`.

## 1. Bảng màu & Token (`--ui-*`, §6.1)

Tên Tailwind chuẩn (`bg-primary`, `text-muted-foreground`, `border-border`…) được `@theme inline` trong `web/src/styles/ui.css` ánh xạ sang `--ui-*`, cho phép phần ứng dụng và 8 mẫu site gian hàng đổi theme đồng nhất chỉ bằng biến CSS. Mọi cặp chữ/nền đạt WCAG ≥ 4.5:1, viền ô nhập ≥ 3:1.

| Biến `--ui-*`                        | Giá trị                           | Vai trò                                                                       | Tương phản                   |
| ------------------------------------ | --------------------------------- | ----------------------------------------------------------------------------- | ---------------------------- |
| `background` / `foreground`          | `#EFF3F5` / `#1F3038`             | Nền vùng nội dung / chữ chính                                                 | 12.2:1                       |
| `card`, `popover` / `-foreground`    | `#FFFFFF` / `#1F3038`             | Thẻ, menu, hộp thoại                                                          | 13.6:1                       |
| `primary` / `-foreground`            | `#1F3038` / `#FFFFFF`             | Nút hành động chính (tối)                                                     | 13.6:1                       |
| `secondary` / `-foreground`          | `#E4EAEE` / `#1F3038`             | Nút phụ xám                                                                   | ≥ 10:1                       |
| `muted` / `-foreground`              | `#EFF3F5` / `#52626A`             | Nền dịu / chữ phụ                                                             | 6.3 trên trắng, 5.7 trên nền |
| `accent` / `-foreground`             | `#E4EAEE` / `#152229`             | Nền hover menu & dòng bảng                                                    | ≥ 11:1                       |
| `brand` / `-foreground`              | `#0D7480` / `#FFFFFF`             | Link, focus, nav đang chọn, tiến độ, biểu đồ chính                            | 5.5 trên trắng, 4.9 trên nền |
| `destructive` / `-foreground`        | `#B33A3A` / `#FFFFFF`             | Xoá, cảnh báo hết hàng                                                        | 5.9:1                        |
| `border` / `input` / `ring`          | `#DDE4E8` / `#7D8E97` / `#0D7480` | Viền thẻ / viền ô nhập (3.4:1) / vòng focus 2px                               | —                            |
| `topbar` / `-foreground`             | `#152229` / `#D3DDE2`             | Thanh trên 56px (`AppShell`)                                                  | 11.8:1                       |
| `topbar-input` / `-placeholder`      | `#293E48` / `#9FB0B8`             | Ô tìm kiếm ⌘K trên thanh trên                                                 | 5.0:1                        |
| `sidebar` / `-foreground` / `-muted` | `#E6ECEF` / `#1F3038` / `#52626A` | Menu trái 240px (`-accent: #FFFFFF`, `-border: #DDE4E8`, `-primary: #0D7480`) | 11.5 / 5.3                   |

### 6 tông nhãn (`badge-{tông}-bg` / `badge-{tông}-ink`)

| Tông        | Sáng (ứng dụng & site sáng)   | Tối (`[data-site-theme-mode="dark"]`) |
| ----------- | ----------------------------- | ------------------------------------- |
| `success`   | `#DDF3E6` / `#145338` (7.8:1) | `#16352A` / `#8EE0B5` (8.6:1)         |
| `attention` | `#FBEFB5` / `#5C4A00` (7.4:1) | `#3A3210` / `#F2DE7A` (9.4:1)         |
| `warning`   | `#FCDDB8` / `#6B3D00` (7.1:1) | `#3D2A12` / `#F5B97A` (7.9:1)         |
| `info`      | `#DCEBFA` / `#0B4A7A` (7.6:1) | `#132C44` / `#8CC4F5` (7.7:1)         |
| `critical`  | `#F8E2E2` / `#8C2626` (7.0:1) | `#3D1A1A` / `#F5A3A3` (7.8:1)         |
| `neutral`   | `#E6ECEF` / `#2C3E47` (9.3:1) | `#262C33` / `#D3DDE2` (10.2:1)        |

Trên site tối (`[data-site-theme-mode="dark"]`), `--ui-destructive` đổi sang `#F07A7A` (≥ 5.8:1 trên `#12161B`, `#151314`, `#1E242B`).

## 2. Chữ, khoảng cách, bo góc & bóng (§6.2–§6.4)

- **Chữ**: **Inter** (`--font-inter`, `subsets: ["latin", "vietnamese"]`, `preload: false` trong `web/src/lib/fonts.ts`; riêng `web/src/app/(shop)/(public)/layout.tsx` thêm `axes: ["opsz"]` cho trang chủ `/`) cho phần ứng dụng; mọi số liệu và tiền VND (`Money` / `formatVnd`) dùng `tabular-nums`.
  - Tiêu đề trang: `20px / 28px / 650` · Tiêu đề thẻ: `14px / 20px / 650` · Chữ thân: `14px / 20px / 450`.
  - Chữ phụ, đầu cột bảng, nhãn: `13px / 18px / 450–550` · Nhãn trạng thái: `12px / 16px / 550` · Số KPI: `24px / 32px / 650`.
- **Khoảng cách**: thang 4px; đệm thẻ `16px` (`≥ 768`: `16px 20px`), khoảng giữa thẻ `16px`, lề trang `16 / 24 / 32px` (`≥ 1280`). Vùng chạm mobile ≥ 40×40px.
- **Bo góc** (`--ui-radius: 0.75rem`): thẻ và hộp thoại `12px` (`rounded-xl`/`rounded-lg`), nút & ô nhập `8px` (`rounded-md`), chip nhỏ `6px` (`rounded-sm`), nhãn trạng thái tròn hẳn (`rounded-full`).
- **Bóng** (áp cho phần ứng dụng; **không gradient**; **site gian hàng giữ phẳng** với `--ui-shadow-card: none`, `--ui-shadow-button: none`, chỉ giữ `shadow-overlay` cho menu/hộp thoại/sheet/toast; ngoại lệ duy nhất là trang chủ `/` với `PlatformHome` và nhánh `data-home="true"` của `PlatformHeader`):
  - `shadow-card`: `0 1px 0 rgba(21,34,41,.07), 0 0 0 1px rgba(21,34,41,.06)`
  - `shadow-button`: `inset 0 -1px 0 rgba(0,0,0,.45), inset 0 1px 0 rgba(255,255,255,.14)`
  - `shadow-overlay`: `0 8px 24px rgba(21,34,41,.16)`
- **Icon & motion**: `lucide-react` (16px trong nút/nav, 20px đầu trang, nét 1.75), chuyển động Radix qua `tw-animate-css` (150–200ms) và Reveal/PageTransition trong `ui.css` (tôn trọng `prefers-reduced-motion`). Không dùng tên/logo/icon/copy Shopify hay chữ tiếng Anh mẫu.

## 3. Component & Ánh xạ tông nhãn (§7, §7.1)

- **Primitive (`web/src/components/ui/` — 35 tệp)**: `accordion`, `alert` (`default`, `destructive`, `inverse`), `alert-dialog`, `avatar`, `badge` (+ 6 tông), `breadcrumb`, `button` (`default`, `outline`, `secondary`, `ghost`, `link`, `destructive`; `size: default/xs/sm/lg/icon/icon-xs/icon-sm/icon-lg`), `card` (`CardAction`), `checkbox`, `collapsible`, `command`, `dialog`, `dropdown-menu`, `field`, `input`, `label`, `pagination`, `popover`, `progress`, `radio-group`, `scroll-area`, `select`, `separator`, `sheet`, `sidebar`, `skeleton`, `sonner` (ứng dụng: nền `#1F3038`, chữ trắng, `bottom-right`; site: `SiteToasterInner` đọc `data-site-theme-mode` và dùng `--ui-*`), `switch`, `table`, `tabs` (nền `secondary` khi chọn), `textarea`, `toggle`, `toggle-group`, `tooltip`, `ui-scope`.
- **Pattern (`web/src/components/app/`)**: `AppShell`, `PageHeader`, `StatusBadge`, `statusTone`, `IndexTable` (+ `IndexRowCard`), `MetricStrip`, `DetailLayout`, `EmptyState`, `ErrorState`, `AuthSplit`, `KpiChartCard`, `SetupGuide`, `setupProgress`, `navMatch`, `Timeline`, `SaveBar`, `Money`, `charts` (`LineChart`, `BarChart` không gradient).
- **Ánh xạ trạng thái → tông (`web/src/components/app/statusTone.ts`, không phân biệt hoa thường, lạ/rỗng → `neutral`)**:
  - Đơn hàng: `PENDING`, `RETURN_REQUESTED` → `warning`; `CONFIRMED`, `PREPARING` → `attention`; `READY_TO_SHIP`, `SHIPPING` → `info`; `DELIVERED`, `COMPLETED` → `success`; `DELIVERY_FAILED`, `CANCELLED` → `critical`; `RETURNED`, `REFUNDED` → `neutral`.
  - Job vận đơn: `QUEUED` → `info`; `FAILED` → `critical`.
  - Sản phẩm: `APPROVED` → `success`; `PENDING` → `attention`; `REJECTED` → `critical`; `OUT_OF_STOCK` → `warning`; `HIDDEN` → `neutral`.
  - Tài khoản ngân hàng: `VERIFIED` → `success`; `PENDING_OTP`, `PENDING_ADMIN_REVIEW` → `attention`; `REJECTED` → `critical`.
  - Site: `PUBLISHED` → `success`; `DRAFT` → `info`; `SUSPENDED` → `critical`.
  - Trả hàng / khiếu nại / duyệt gian hàng: chờ duyệt → `attention`; cần bổ sung → `warning`; chấp nhận/hoàn tất → `success`; từ chối → `critical`; đóng → `neutral`.

## 4. Khung trang (§8) & 9 kiểu trang (§9)

- **`AppShell` (§8.1)**: thanh trên 56px `bg-topbar` (`NexusMark` + "NexusMart" + nhãn khu vực, ô ⌘K mở `CommandDialog`, menu tài khoản/shop); menu trái 240px `bg-sidebar` dùng `Sidebar` (`collapsible="icon"`, lưu `localStorage`, không cookie); `< 1024` thu icon 56px; `< 768` mở bằng `Sheet` trái + slot `mobileNav` đáy 5 nút (người bán).
- **`PageHeader` (§8.2)**: nút `←` cho trang con, tiêu đề 20px + nhãn trạng thái, dòng phụ 13px; phải gồm nút phụ, "Thêm thao tác ▾", `‹ ›`, nút chính. `< 768` chỉ giữ nút chính, gom còn lại vào "Thêm thao tác ▾".
- **`AuthSplit` (§8.3)**: `standalone` (host gốc, nền `topbar` toàn màn, thẻ trắng tối đa 1180px chia đôi, mobile toàn màn nền trắng) và `embedded` (trong `SiteShell` nhận màu site). Giữ đủ field, OTP, nút chuyển vai trò và thanh độ mạnh mật khẩu phía client.
- **9 kiểu trang (§9)**:
  - **A1 (Danh sách + bảng)**: `MetricStrip` (tuỳ trang) + `IndexTable` (tabs giữ đúng bộ lọc thật, nút tìm/lọc, checkbox lô chỉ khi có thao tác lô, hỗ trợ hàng mở rộng, xem nhanh bằng `Sheet`). Mobile đổi sang danh sách `IndexRowCard`.
  - **A2 (Chi tiết 2/3 + 1/3)**: `DetailLayout` tối đa 1000px (`< 1024` xếp dọc), cột chính là thẻ mặt hàng + thanh toán + `Timeline` chỉ đọc; cột phụ là ghi chú, khách hàng, vận chuyển.
  - **A3 (Form tạo/sửa)**: lưới 2/3 + 1/3 như A2, `FieldError` dưới từng ô, hiện `SaveBar` dính đỉnh nền `topbar` khi có thay đổi chưa lưu.
  - **A4 (Tổng quan / phân tích)**: chọn kỳ + lưới `KpiChartCard` (3 cột `≥ 1280`, 2 cột `≥ 768`, 1 cột mobile) dùng `LineChart`/`BarChart` phẳng; chỉ hiện số liệu thật từ API.
  - **A5 (Cài đặt / hồ sơ)**: khổ 800px, mỗi nhóm chia 1/3 tiêu đề + mô tả và 2/3 thẻ form, lưu qua `SaveBar`.
  - **A6 (Hộp thư)**: 2 cột (danh sách hội thoại 320px có chấm `brand` chưa đọc + luồng tin); mobile hiển thị một cột kèm nút quay lại.
  - **A7 (Site Studio)**: toàn màn trong `AppShell` (menu thu icon), thanh trên gồm trạng thái + `[Lưu nháp]` `[Xuất bản]`, 5 chế độ (`Tabs`: Mẫu, Tuỳ chỉnh, Mã JSON, Tên miền, Phiên bản).
  - **A8 (Chọn mẫu)**: lưới 3 cột thẻ mẫu (nhãn "Đang dùng", tên 24px, mô tả, ảnh xem trước, nút `[Dùng mẫu này]` toàn rộng, danh sách đặc điểm). Không giá tiền.
  - **A9 (Hướng dẫn thiết lập)**: thẻ `SetupGuide` với `Progress` + các bước `Collapsible` bám dữ liệu thật (3 bước bắt buộc + 1 tuỳ chọn tên miền riêng), thu gọn lưu `localStorage`, đủ 3 bước hiện "✓ Đã thiết lập xong".

## 5. Theme site gian hàng (§11.1)

- `buildSiteUiVars(theme)` và `resolveSiteUiFontStack(fontFamily)` trong `web/src/lib/site/theme.ts` ánh xạ **8 mẫu Template JSON v2** (gồm **2 mẫu nền tối**: `sport-active`, `luxury-maison`) sang 21 biến `--ui-*`: `background`/`card`/`foreground`/`primary`/`brand`, `secondary`/`muted`/`accent`/`border` trộn bằng `mixHex`, `muted-foreground` tự tăng tỉ lệ đạt ≥ 4.5:1 trên cả `background` và `card`, `input` và `ring` ≥ 3:1 trên `background`, `radius` (`none→0px`, `sm→4px`, `md→8px`, `lg→16px`, fallback `0px`), và `font` từ **5 font thật** nạp bằng `next/font/google` (`preload: false` trong `web/src/lib/fonts.ts`: `Inter`, `Work Sans`, `Be Vietnam Pro`, `IBM Plex Sans`, `Plus Jakarta Sans`).
- `SiteThemeRoot` (`web/src/features/site/shell/SiteThemeRoot.tsx`) bọc `UiScope` và gắn `data-site-root`, `data-site-theme-mode="light"|"dark"`, `data-site-archetype`, `data-site-header`… Chế độ tối bám `[data-site-theme-mode="dark"]` (`@custom-variant dark` và `html:has([data-site-root][data-site-theme-mode="dark"])`). Bề mặt site giữ phẳng, chỉ overlay có bóng.

## 6. Kiến trúc CSS, `UiScope`, ngân sách JS & Kiểm định

- **CSS toàn cục, Preflight & PostCSS**:
  - `web/src/styles/ui.css` chốt `@layer theme, base, components, utilities;`, bật preflight toàn cục (`@import "tailwindcss/preflight.css" layer(base);`), nạp ở `web/src/app/layout.tsx`, và chứa toàn bộ rule nền `html`/`body`, nền site tối (`html:has([data-site-root][data-site-theme-mode="dark"])`), `:focus-visible`, cùng CSS Reveal/PageTransition.
  - Biến riêng trang chủ tách vào `web/src/features/platform/home/home-tokens.css` (scope `[data-platform-public="true"]` gắn ở `web/src/app/(shop)/(public)/layout.tsx`) cùng `web/src/features/platform/home/home-keyframes.css`.
  - `web/postcss.config.mjs` chỉ còn `"@tailwindcss/postcss": { optimize: false }`: tránh Lightning CSS ép alpha sang hex 8-bit (`rgb(2 9 10 / 0.62)` → `#02090a9e`) làm trang chủ lệch tới `6/255`; với `optimize: false` lệch còn `≤ 1/255`, CSS production `230.521 B raw / 38.000 B gzip` (mặc định `230.443 B / 38.041 B`); Tailwind v4 vẫn tự phát `-webkit-` và Next.js vẫn minify CSS.
- **`UiScope` (portal container cho Radix)**:
  - `UiScope` (`web/src/components/ui/ui-scope.tsx`) cung cấp portal container qua `usePortalContainer()` cho mọi `*Portal` của Radix (`Dialog`, `AlertDialog`, `Sheet`, `Popover`, `DropdownMenu`, `Select`, `Tooltip`), đảm bảo overlay thừa kế đúng `--ui-*` và `[data-site-theme-mode="dark"]` trên site tối.
  - Không còn reset CSS bám thuộc tính `data-ui`.
- **Import từng file & ngân sách First Load JS trên site**:
  - Trong `web/src/app/(site)/**` và `web/src/features/{site,search,commerce,account,chat,assistant}/**`, luôn **import thẳng từng file** (`@/components/app/Money`, `@/components/app/EmptyState`, `@/features/site/SitePdpView`…), **không** import qua barrel `@/components/app` hay `@/features/site`.
  - Các overlay nặng trên site (`SiteMobileDrawer`, `SiteSearchModal`, `SiteToaster`, widget chat/trợ lý trong `SiteSupportDock`) nạp lazy bằng `next/dynamic`; `Toaster` đặt theo từng layout khu vực, không đặt ở `web/src/app/layout.tsx`.
  - Giữ **First Load JS** trang chủ site (`/s/[host]`) và PDP (`/s/[host]/san-pham/[slug]`) **≤ 296 kB**.
- **Quy ước chung**: không cài phụ thuộc ngoài 10 gói UI đã duyệt ở ADR-0011; không khai biến CSS ở `:root` ngoài tiền tố `--ui-*` (riêng `--site-*` nằm trên `SiteThemeRoot` và `--home-*` scope trong `[data-platform-public="true"]` của `home-tokens.css`). Ngôn ngữ 100% tiếng Việt tự nhiên, không rò thuật ngữ kỹ thuật (JWT, HMAC, Saga, Redis Lua, sandbox, sổ cái ghi kép, optimistic locking) trên giao diện người mua.
- **Quy trình kiểm định**:
  1. Cổng tĩnh trong `web/`: `npm run lint && npm run format:check && npm run test:unit && npm run check:data && NEXT_DIST_DIR=.next-check npm run build` (hoàn nguyên `web/tsconfig.json` sau build).
  2. Luồng chức năng & trạng thái (từ gốc repo): `node tests/ui-flows/run.mjs --port <port> --area platform|admin|seller|site|templates` và `node tests/ui-flows/states.mjs --port <port> --out <dir>`.
  3. Soát trực quan & so pixel ở 3 khổ **390 / 768 / 1440**: `node .agents/tools/ui-shots.mjs`, `node .agents/tools/scroll-shots.mjs`, `.agents/tools/ui-diff.sh`, `.agents/tools/snap-build.sh`: `horizontalOverflow: 0`, 1 `h1`/trang, 0 lỗi console/hydration, đúng trạng thái loading/empty/error, vòng focus rõ trên cả nền sáng và nền tối.
