---
name: design-system
description: 'Hệ thống thiết kế UI NexusMart trên shadcn/ui (style new-york, radix-ui) + Tailwind CSS v4 (chưa preflight tới phase 5) và lớp biến --ui-* (Inter cho phần ứng dụng, 5 font thật cho site gian hàng). Dùng khi tạo/sửa trang, component, CSS, token hoặc review UI trong web/.'
---

# NexusMart Design System (shadcn/ui + Tailwind v4)

Đặc tả đầy đủ: [design.md](../../../.agents/briefs/shadcn-redesign/design.md) · Quyết định kiến trúc: [ADR-0011](../../../docs/adr/0011-shadcn-ui-tailwind-v4.md). Ảnh tham chiếu Figma ở `.agents/briefs/shadcn-redesign/figma/`.

Mọi khu vực đã chuyển dùng chung một bộ component shadcn/ui (`web/src/components/ui/`) và pattern ứng dụng (`web/src/components/app/`), styling qua Tailwind v4 (`web/src/styles/ui.css`, import ở layout từng khu vực đã chuyển, không ở `src/app/layout.tsx` gốc) và lớp biến thô `--ui-*`. **Chưa bật preflight** tới phase 5 để bảo toàn các trang CSS Module chưa chuyển.

## 1. Bảng màu & Token (`--ui-*`, §6.1)

Tên Tailwind chuẩn (`bg-primary`, `text-muted-foreground`, `border-border`…) được `@theme inline` trong `ui.css` ánh xạ sang `--ui-*` để không đè token cũ của `tokens.css`. Mọi cặp chữ/nền đạt WCAG ≥ 4.5:1, viền ô nhập ≥ 3:1.

| Biến `--ui-*` | Giá trị | Vai trò | Tương phản |
| --- | --- | --- | --- |
| `background` / `foreground` | `#EFF3F5` / `#1F3038` | Nền vùng nội dung / chữ chính | 12.2:1 |
| `card`, `popover` / `-foreground` | `#FFFFFF` / `#1F3038` | Thẻ, menu, hộp thoại | 13.6:1 |
| `primary` / `-foreground` | `#1F3038` / `#FFFFFF` | Nút hành động chính (tối) | 13.6:1 |
| `secondary` / `-foreground` | `#E4EAEE` / `#1F3038` | Nút phụ xám | ≥ 10:1 |
| `muted` / `-foreground` | `#EFF3F5` / `#52626A` | Nền dịu / chữ phụ | 6.3 trên trắng, 5.7 trên nền |
| `accent` / `-foreground` | `#E4EAEE` / `#152229` | Nền hover menu & dòng bảng | ≥ 11:1 |
| `brand` / `-foreground` | `#0D7480` / `#FFFFFF` | Link, focus, nav đang chọn, tiến độ, biểu đồ chính | 5.5 trên trắng, 4.9 trên nền |
| `destructive` / `-foreground` | `#B33A3A` / `#FFFFFF` | Xoá, cảnh báo hết hàng | 5.9:1 |
| `border` / `input` / `ring` | `#DDE4E8` / `#7D8E97` / `#0D7480` | Viền thẻ / viền ô nhập (3.4:1) / vòng focus 2px | — |
| `topbar` / `-foreground` | `#152229` / `#D3DDE2` | Thanh trên 56px (`AppShell`) | 11.8:1 |
| `topbar-input` / `-placeholder` | `#293E48` / `#9FB0B8` | Ô tìm kiếm ⌘K trên thanh trên | 5.0:1 |
| `sidebar` / `-foreground` / `-muted` | `#E6ECEF` / `#1F3038` / `#52626A` | Menu trái 240px (`-accent: #FFFFFF`, `-border: #DDE4E8`, `-primary: #0D7480`) | 11.5 / 5.3 |

### 6 tông nhãn (`badge-{tông}-bg` / `badge-{tông}-ink`)

| Tông | Sáng (ứng dụng & site sáng) | Tối (`[data-site-theme-mode="dark"]`) |
| --- | --- | --- |
| `success` | `#DDF3E6` / `#145338` (7.8:1) | `#16352A` / `#8EE0B5` (8.6:1) |
| `attention` | `#FBEFB5` / `#5C4A00` (7.4:1) | `#3A3210` / `#F2DE7A` (9.4:1) |
| `warning` | `#FCDDB8` / `#6B3D00` (7.1:1) | `#3D2A12` / `#F5B97A` (7.9:1) |
| `info` | `#DCEBFA` / `#0B4A7A` (7.6:1) | `#132C44` / `#8CC4F5` (7.7:1) |
| `critical` | `#F8E2E2` / `#8C2626` (7.0:1) | `#3D1A1A` / `#F5A3A3` (7.8:1) |
| `neutral` | `#E6ECEF` / `#2C3E47` (9.3:1) | `#262C33` / `#D3DDE2` (10.2:1) |

Trên site tối (`[data-site-theme-mode="dark"]`), `--ui-destructive` đổi sang `#F07A7A` (≥ 5.8:1 trên `#12161B`, `#151314`, `#1E242B`).

## 2. Chữ, khoảng cách, bo góc & bóng (§6.2–§6.4)

- **Chữ**: **Inter** (`--font-inter`, `subsets: ["latin", "vietnamese"]`, `preload: false`) cho phần ứng dụng; mọi số liệu và tiền VND (`formatVnd`) dùng `tabular-nums`.
  - Tiêu đề trang: `20px / 28px / 650` · Tiêu đề thẻ: `14px / 20px / 650` · Chữ thân: `14px / 20px / 450`.
  - Chữ phụ, đầu cột bảng, nhãn: `13px / 18px / 450–550` · Nhãn trạng thái: `12px / 16px / 550` · Số KPI: `24px / 32px / 650`.
- **Khoảng cách**: thang 4px; đệm thẻ `16px` (`≥ 768`: `16px 20px`), khoảng giữa thẻ `16px`, lề trang `16 / 24 / 32px` (`≥ 1280`). Vùng chạm mobile ≥ 40×40px.
- **Bo góc** (`--ui-radius: 0.75rem`): thẻ và hộp thoại `12px` (`rounded-xl`/`rounded-lg`), nút & ô nhập `8px` (`rounded-md`), chip nhỏ `6px` (`rounded-sm`), nhãn trạng thái tròn hẳn (`rounded-full`).
- **Bóng** (chỉ áp cho phần ứng dụng; **không gradient**; **site gian hàng giữ phẳng**, chỉ nổi ở menu/hộp thoại/sheet):
  - `shadow-card`: `0 1px 0 rgba(21,34,41,.07), 0 0 0 1px rgba(21,34,41,.06)`
  - `shadow-button`: `inset 0 -1px 0 rgba(0,0,0,.45), inset 0 1px 0 rgba(255,255,255,.14)`
  - `shadow-overlay`: `0 8px 24px rgba(21,34,41,.16)`
- **Icon & motion**: `lucide-react` (16px trong nút/nav, 20px đầu trang, nét 1.75), chuyển động qua `tw-animate-css` (150–200ms, tôn trọng `prefers-reduced-motion`). Không dùng tên/logo/icon/copy Shopify hay chữ tiếng Anh mẫu.

## 3. Component & Ánh xạ tông nhãn (§7, §7.1)

- **Primitive (`src/components/ui/`)**: `button` (`default`, `outline`, `secondary`, `ghost`, `link`, `destructive`; `size: default/sm/lg/icon`), `badge` (+ 6 tông), `card` (`CardAction`), `input`, `textarea`, `label`, `field`, `select`, `checkbox`, `radio-group`, `switch`, `tabs` (nền `secondary` khi chọn), `table`, `dialog`, `alert-dialog`, `sheet`, `dropdown-menu`, `popover`, `command`, `tooltip`, `collapsible`, `accordion`, `progress`, `separator`, `skeleton`, `avatar`, `breadcrumb`, `pagination`, `scroll-area`, `toggle`, `toggle-group`, `sidebar`, `sonner` (nền `#1F3038`, chữ trắng, `bottom-right`), `alert` (`default`, `destructive`, `inverse`), `ui-scope`.
- **Pattern (`src/components/app/`)**: `AppShell`, `PageHeader`, `StatusBadge`, `statusTone`, `IndexTable` (+ `IndexRowCard`), `MetricStrip`, `DetailLayout`, `EmptyState`, `ErrorState`, `AuthSplit`, `KpiChartCard`, `SetupGuide`, `setupProgress`, `Timeline`, `SaveBar`, `Money`, `charts` (`LineChart`, `BarChart` không gradient).
- **Ánh xạ trạng thái → tông (`statusTone.ts`, không phân biệt hoa thường, lạ/rỗng → `neutral`)**:
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

- `buildSiteUiVars(theme)` trong `web/src/lib/site/theme.ts` ánh xạ 8 mẫu Template JSON v2 sang 21 biến `--ui-*`: `background`/`card`/`foreground`/`primary`/`brand`, `secondary`/`muted`/`accent`/`border` trộn bằng `mixHex`, `muted-foreground` tự tăng tỉ lệ đạt ≥ 4.5:1 trên cả `background` và `card`, `input` và `ring` ≥ 3:1 trên `background`, `radius` (`none→0px`, `sm→4px`, `md→8px`, `lg→16px`, fallback `0px`), và `font` từ 5 font nạp bằng `next/font/google` (`preload: false`: Inter, Work Sans, Be Vietnam Pro, IBM Plex Sans, Plus Jakarta Sans).
- Chế độ tối bám thuộc tính `[data-site-theme-mode="dark"]` sẵn có (`@custom-variant dark`). Bề mặt site giữ phẳng, chỉ overlay có bóng.

## 6. Luật cách ly (`UiScope`), đóng băng & Kiểm định Playwright

- **Cách ly `UiScope` / `data-ui="true"`**: mọi vùng đã chuyển bọc trong `UiScope` xuất **`data-ui="true"`** (mã cũ đã dùng thuộc tính `data-ui` với giá trị khác, nên selector CSS mới luôn là `[data-ui="true"]` cho reset `[data-ui="true"] :where(...)` và override `revert-layer`), đồng thời cung cấp portal container qua `usePortalContainer()` giúp Dialog/Sheet/Popover/Select/Tooltip/DropdownMenu thừa kế `--ui-*` của site tối. **Không** gắn `data-ui="true"` vào trang/layout CSS Module cũ.
- **Import `ui.css`, luật layer & `autoprefixer`**:
  - `ui.css` chỉ import ở layout của từng khu vực đã chuyển (hiện ở `src/app/(design)/design/layout.tsx`), **không** import ở `src/app/layout.tsx` gốc để các trang cũ (kể cả site gian hàng) không tải thêm CSS chặn render.
  - Dòng đầu `src/styles/globals.css` chốt `@layer theme, base, components, utilities;`. Chỉ rule độ đặc hiệu ≤ `(0,0,1)` (phần tử, `*`, `::pseudo-element`) vào `@layer base`; 3 rule cũ có độ đặc hiệu cao hơn (`ul[class], ol[class]`, `:focus-visible`, `tbody tr:last-child td`) giữ nguyên ngoài layer và có override `revert-layer` ngoài layer cho `[data-ui="true"]` trong `ui.css`.
  - `autoprefixer` (`devDependency`) được cấu hình trong `web/postcss.config.mjs` theo `MODERN_BROWSERSLIST_TARGET` của Next để giữ vendor prefix cho CSS cũ giống hệt mặc định, và sẽ gỡ ở phase 5 cùng CSS Modules.
- **Cấm trộn**: một component không vừa dùng Tailwind vừa dùng CSS Module. Không khai biến thô ngoài tiền tố `--ui-*` ở `:root`. Không cài phụ thuộc ngoài danh sách ADR-0011.
- **Đóng băng sau phase 0**: `src/components/ui/` và `src/components/app/` đóng băng khi phase 1–4 chạy song song; swarm khu vực không chạy `shadcn add` và không tự sửa hai thư mục này.
- **Ngôn ngữ**: 100% tiếng Việt tự nhiên, không rò thuật ngữ kỹ thuật (JWT, HMAC, Saga, Redis Lua, sandbox, sổ cái ghi kép, optimistic locking) trên giao diện người mua.
- **Quy trình kiểm định**:
  1. Cổng tĩnh trong `web/`: `npm run lint && npm run format:check && npm run test:unit && npm run check:data && NEXT_DIST_DIR=.next-agy npm run build` (hoàn nguyên `tsconfig.json` sau build).
  2. Chụp Playwright ở 3 khổ **390 / 768 / 1440** trên server sản phẩm cách ly (không dùng cổng `3111`/`4777`): `horizontalOverflow: 0`, không cắt chữ, không che bởi thanh đáy mobile, đúng trạng thái loading/empty/error, vòng focus rõ trên cả nền sáng và nền tối, và trang CSS Module cũ không đổi hình.
