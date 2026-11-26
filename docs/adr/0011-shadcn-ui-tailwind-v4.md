# ADR-0011: Hệ thống UI `shadcn/ui` + Tailwind CSS v4 cho `web/`

- **Trạng thái**: Đã duyệt — chủ dự án chọn shadcn/ui ngày 26/11/2026
- **Ngày**: 2026-11-26

## Bối cảnh

- Giao diện `web/` (Next.js 15 App Router, React 19, TypeScript strict, 53 trang thuộc 4 khu vực `(shop)`, `(site)`, `(seller)`, `(admin)`) hiện viết tay hoàn toàn bằng CSS Modules, `tokens.css` và các component tự dựng (`Button`, `AdminDrawer`, `AdminConfirmDialog`, `SlidingTabBar`, `modalFocus.ts`…).
- Ba khu vực từng theo ba art direction tách biệt (contact sheet, ledger, framed sheet), khiến giao diện toàn nền tảng thiếu nhất quán và tốn công xây lại các tương tác a11y phức tạp (dialog, combobox, menu, tooltip, sheet, focus trap).
- Chủ dự án muốn thống nhất toàn bộ web NexusMart theo bộ màn hình quản trị Figma (`Shopify UI Screens`, đã điều chỉnh sang thương hiệu và bảng màu Frost & Slate + Teal của NexusMart, đặc tả tại `.agents/briefs/shadcn-redesign/design.md`), đồng thời vẫn giữ đủ 8 mẫu site gian hàng độc lập (Template JSON v2, gồm cả 2 mẫu nền tối) và khả năng nâng cấp dần từng khu vực mà không làm vỡ các trang cũ.
- Luật cũ (`web/CLAUDE.md`, `.claude/rules/frontend.md`) cấm cài Tailwind và thư viện UI ngoài khi chưa có ADR được duyệt (`AGENTS.md` luật 11).

## Quyết định

1. **Chọn `shadcn/ui` (style `new-york`, dùng gói hợp nhất `radix-ui`) kết hợp Tailwind CSS v4 (`@tailwindcss/postcss`)** làm hệ thống component và styling duy nhất cho toàn bộ `web/`.
2. **Tách biệt lớp biến `--ui-*` (không đè token cũ)**:
   - Toàn bộ biến thô của hệ thống mới đặt tên `--ui-*` (`--ui-background`, `--ui-primary`, `--ui-brand`, `--ui-sidebar`, `--ui-topbar`, 6 cặp `--ui-badge-*`, `--ui-radius`, `--ui-font`, `--ui-shadow-*`).
   - Dùng `@theme inline` trong `web/src/styles/ui.css` để ánh xạ tên Tailwind chuẩn (`--color-primary`, `--color-muted-foreground`, `--font-sans`, `--ease-out`, `--ease-in-out`, `--radius-*`, `--shadow-*`) sang `--ui-*`, tránh đè biến trùng tên trong `tokens.css` (`--accent`, `--muted`, `--border`, `--card`, `--chart-*`, `--font-sans`, `--ease-out`, `--ease-in-out`).
3. **Chưa bật Tailwind preflight cho tới phase 5**:
   - `web/src/styles/ui.css` chỉ nạp `tailwindcss/theme.css` (`layer(theme)`), `tailwindcss/utilities.css` (`layer(utilities) source(none)`) với `@source "../"` và `tw-animate-css`; không import `tailwindcss` bản đầy đủ hay `tailwindcss/preflight`.
   - `globals.css` chỉ bọc các rule chọn thuần theo phần tử + `ol[class]` + `:focus-visible` vào `@layer base` để utility Tailwind thắng được trong khu vực mới trong khi CSS Module cũ (ngoài layer) không đổi hình.
   - Khu vực đã chuyển bọc trong `UiScope` (`data-ui`): áp reset thu hẹp `[data-ui] :where(...)` và cung cấp container cho các `*Portal` của Radix (`usePortalContainer`) để Dialog/Sheet/Popover/Select/Tooltip/DropdownMenu thừa kế `--ui-*` khi nằm trong khung site tối.
4. **Đóng băng thư mục nền sau phase 0**: `web/src/components/ui/` và `web/src/components/app/` được hoàn thiện ở phase 0 cùng trang duyệt nội bộ `/design`, sau đó đóng băng để phase 1–4 triển khai song song theo khu vực. Phase 5 sẽ bật preflight và xoá CSS Module/token cũ.

## Phương án loại

| Phương án | Lý do loại (theo spec §4) |
| --- | --- |
| **Ant Design (`antd`)** | Bảng, form và tiếng Việt có sẵn nhưng CSS-in-JS chạy lúc runtime làm nặng trang mua hàng (ảnh hưởng LCP/JS site) và khó đổi theme động theo 8 mẫu site gian hàng. |
| **Shuffle / "uilib"** | Shuffle là công cụ dựng template, không phải thư viện UI component; "uilib" không xác định được thư viện tiêu chuẩn phù hợp. |
| **Shopify Polaris** | Bản React chính thức đã ngừng bảo trì (chuyển sang web components gắn hệ sinh thái Shopify) và có giấy phép/ràng buộc thiết kế riêng. |

## Hệ quả và rủi ro

- **Tích cực**:
  - Styling hoàn toàn dựa trên CSS variables và utility tĩnh (không runtime CSS-in-JS), khớp cơ chế đổi theme động theo từng site (`buildSiteUiVars`).
  - Mã nguồn component nằm trong repo (`src/components/ui/`), dễ chuẩn hoá theo thiết kế Figma và chuẩn a11y (Radix lo focus trap, phím `Esc`, ARIA).
  - Cho phép chuyển dần theo 5 phase song song mà không làm đổi hình 53 trang hiện tại.
- **Rủi ro & biện pháp kiểm soát**:
  - **Preflight làm vỡ trang cũ**: trì hoãn preflight tới phase 5; dùng unit test `ui-vars.test.mjs` chặn `@import "tailwindcss"`, `preflight` và rule toàn cục trong `ui.css`, kết hợp đối chiếu ảnh Playwright trước/sau.
  - **Trùng tên biến CSS giữa `tokens.css`/`site*.module.css` và Tailwind/shadcn**: dùng tiền tố `--ui-*` và ánh xạ inline cho 3 biến trùng (`--font-sans`, `--ease-out`, `--ease-in-out`); kiểm tự động bằng `ui-vars.test.mjs`.
  - **CLI `shadcn` tự ghi đè CSS và cài thêm gói**: không chạy `shadcn init`; luôn sao lưu & `diff` `ui.css` sau `shadcn add`, đổi tên biến chèn thêm sang `--ui-*`, xoá `.dark {}` và gỡ gói ngoài danh sách (như `next-themes`).
  - **Portal Radix rơi ra ngoài scope theme của site tối**: nối `usePortalContainer()` từ `UiScope` vào tất cả `*Portal` (`Dialog`, `AlertDialog`, `Sheet`, `Popover`, `DropdownMenu`, `Select`, `Tooltip`) để overlay nhận đúng `--ui-*` và `[data-site-theme-mode="dark"]`.
  - **Phình bundle JS trên trang mua hàng (ngân sách +15% First Load JS)**: giữ mốc đo phase 0; trên site gian hàng ở phase 4, nạp `CommandDialog`, `Sheet` và `Toaster` theo nhu cầu bằng `next/dynamic` và đặt `Toaster` theo từng layout thay vì `app/layout.tsx` gốc.

## Danh sách phụ thuộc

- **Phụ thuộc runtime được phép thêm (10 gói)**:
  - `tailwindcss`
  - `@tailwindcss/postcss`
  - `tw-animate-css`
  - `class-variance-authority`
  - `clsx`
  - `tailwind-merge`
  - `radix-ui`
  - `lucide-react`
  - `cmdk`
  - `sonner`
- **Không cài thêm**: `next-themes`, `recharts` (giữ biểu đồ SVG nhẹ), `@tanstack/react-table` (dùng primitive `Table` với logic lọc/phân trang sẵn có), `react-hook-form`, `zod`, `react-day-picker` (dùng input ngày chuẩn trình duyệt), `vaul`, `input-otp`.
