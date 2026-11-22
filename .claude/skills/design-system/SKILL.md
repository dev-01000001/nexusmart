---
name: design-system
description: 'Hệ thống thiết kế UI NexusMart cho web Next.js: token Frost and Slate + Work Sans (web/src/styles/tokens.css, globals.css), art direction 3 khu vực (shop contact-sheet, seller Achromatic ledger, admin Framed sheet), sản phẩm tham chiếu, điều cấm house rules và quy trình soát Playwright 390/768/1440. Dùng khi tạo/sửa trang, component, CSS, token hoặc review UI trong web/.'
---

# NexusMart Design System

Nguồn sự thật: `web/src/styles/tokens.css`, `web/src/styles/globals.css`, `web/src/app/layout.tsx`, `.taste-skill/`, `web/DESIGN_REPORT.md`. Toàn bộ `web/` dùng Next.js App Router + CSS Modules thuần (không Tailwind, không UI kit, không thư viện icon/state ngoài).

## 1. Nguồn sự thật Token & Base CSS

### `web/src/styles/tokens.css` (1 tệp chung cho cả `(shop)`, `(seller)`, `(admin)`)

| Nhóm token | Biến chính & giá trị | Ghi chú dùng |
| --- | --- | --- |
| Palette lõi (*Frost and slate*) | `--bg: #f7f9fa`, `--surface: #ecf0f2`, `--ink: #1f3038`, `--muted: #5d6b72`, `--accent: #0d7480`, `--accent-ink: #ffffff` | Nền trắng lạnh, chữ slate đậm thay đen thuần; teal chỉ làm điểm nhấn |
| Bề mặt & dải `(shop)` | `--surface-white: #ffffff`, `--surface-subtle: #eff3f5`, `--surface-muted: #e3e9ec`, `--accent-hover: #0a5f69`, `--accent-tint: #e4f1f3`, `--accent-tint-strong: #d2e7ea`, `--accent-tint-ink: #0a4e57`, `--band-dark: #1f3038`, `--band-dark-{surface,elevated,ink,muted,accent}`, `--band-warm: #eef1ef`, `--ink-strong: #152229`, `--ink-secondary: #394b54`, `--badge-discount-{bg,ink}`, `--badge-flash-{bg,ink}` | Phân bậc bằng bậc màu phẳng, không gradient và không shadow lúc nghỉ |
| Bề mặt app `(seller)` / `(admin)` | `--card: #ffffff`, `--raised: #e6ecef`, `--active: #e4e9ec`, `--border: #dde4e8`, `--rule: #e3e8eb`, `--control: #c5cfd5`, `--primary-ink-bg: #1f3038`, `--primary-ink-fg: #ffffff` | Đường viền mảnh `1px`, nút chính vô sắc cho seller |
| Trạng thái & Pill (WCAG ≥ 4.5:1) | `--positive: #1b6e4b`, `--negative: #b33a3a`, `--amber: #9e6b13`, `--amber-ink: #754c09`, `--pill-{positive,negative,warning,neutral}-{bg,ink}` | Màu ngữ nghĩa chỉ đặt lên trạng thái và delta |
| Biểu đồ | `--chart-1: #0d7480`, `--chart-2: #5b8c94`, `--chart-3: #9e6b13`, `--chart-muted: #c2cdd3`, `--chart-mark: #1f3038`, `--chart-span: 0.06`, `--grid: #e3e8eb`, `--axis-ink: #5d6b72` | Chuỗi chính dùng `--chart-1`, 1 biểu đồ trả lời 1 câu hỏi |
| Typography | `--font-sans: var(--font-work-sans), "Work Sans", sans-serif` | Nạp ở `web/src/app/layout.tsx`: `Work_Sans` (`latin`, `vietnamese`, weights `300, 400, 500, 600`) |
| Thang chữ `(shop)` | `--step-small`, `--step-body`, `--step-lead`, `--step-head`, `--step-display` (tất cả `clamp(...)`) | Không tự tạo cỡ chữ thứ 6 |
| Thang chữ app `(seller)` / `(admin)` | `--fs-title: 24px`, `--fs-section: 16px`, `--fs-kpi: 24px`, `--fs-kpi-lead: 30px`, `--fs-body: 14px`, `--fs-small: 12px` | Căn số `tabular-nums` |
| Bố cục & hình học | `--margin`, `--gutter`, `--section`, `--gap: 16px`, `--card-pad: 20px`, `--pad: 24px`, `--control-height: 34px`, `--row-height: 48px`, `--nav-row: 32px`, `--seller-sidebar-width: 236px`, `--admin-sidebar-width: 228px`, `--admin-topbar-height: 48px` | Đồng nhất gutter giữa các hàng trong cùng màn |
| Bo góc (bimodal) | `--radius-shop: 0px`, `--radius-card: 8px`, `--radius-control: 6px`, `--radius-chip: 4px`, `--radius-sheet: 16px` | Storefront góc vuông; app dùng cặp chặt (`8/6px`) và tờ sheet `16px` |
| Motion | `--ease-{out,expo,in-out,soft}`, `--t-press: 140ms`, `--t-hover: 180ms`, `--t-ui: 220ms`, `--t-reveal: 800ms` | Khớp `prefers-reduced-motion: reduce` |

### `web/src/styles/globals.css`

- Reset gọn (`box-sizing: border-box`, bỏ margin mặc định), nền `--bg`, chữ `--ink`, `body` cỡ `--step-body`, `line-height: 1.55`.
- `:focus-visible` viền `2px solid var(--accent)`, `outline-offset: 2px`; `::selection` nền `--accent` chữ `--accent-ink`.
- `.num` và `table` bắt buộc `font-variant-numeric: tabular-nums`; bảng mặc định `th` màu `--muted` viền `--border`, `td` viền `--rule`.
- Tiện ích storefront opt-in: `.shop-container`, `.shop-body` (`16px`, `max-width: 62ch`), `.shop-lead`, `.shop-surface-{primary,subtle,accent,dark}`.
- Motion chuẩn hóa: `.lines`/`.line`/`[data-line]` (`nm-line-rise` `900ms`, trễ `110ms` giữa 2 dòng), `[data-rise]`, `[data-reveal].is-in`; khi `prefers-reduced-motion: reduce` đặt thời lượng `0.01ms`.

## 2. Art Direction & Ba ngữ cảnh giao diện

Cả 3 khu vực dùng chung palette *Frost and slate* và 1 phông *Work Sans* (`.taste-skill/{seller,admin}/direction.json` đều ghi `"keep": ["palette", "type"]`, ghi đè font/màu gốc của sheet app).

### Storefront `(shop)` — *The contact sheet*
- Nguồn: `.taste-skill/direction.json`, `.taste-skill/storefront-sheet.md`, `web/DESIGN_REPORT.md`.
- Ý tưởng: bày sản phẩm theo nhịp tờ contact sheet phẳng, góc vuông (`--radius-shop: 0px`), khoảng thở rộng, ưu tiên ảnh sản phẩm (`web/public/products/`, 3:4) và ảnh người mẫu Việt (`web/public/models/`, 3:4, `object-position: top center`).
- Composition: 1 `h1` trên màn đầu, hero vòng 18 ảnh, lưới 4 cột desktop không hàng mồ côi; phân bậc bằng 1 bề mặt chính `--surface-white` + dải `--band-dark` / `--accent-tint` thay vì hộp xám lồng nhau; màu `--accent` teal tối đa 1 vị trí nổi bật/màn.
- Ngôn ngữ: 100% tiếng Việt tự nhiên cho người mua, số tiền nguyên VND (`199.000₫`); **không** để lộ thuật ngữ kỹ thuật (`JWT`, `sổ cái ghi kép`, `IDEM-`, `sandbox`, `2 cấp`, `tồn kho SKU`).

### Seller Center `(seller)` — *Achromatic ledger*
- Nguồn: `.taste-skill/seller/direction.json`, `.taste-skill/seller-sheet.md`.
- Dày thông tin: KPI làm tab cho biểu đồ, bộ lọc dạng count-card, bảng đơn/sản phẩm/tồn kho/tài chính.
- Shell `Open plane`: sidebar `236px` đặt trực tiếp trên nền `--bg` (không topbar desktop), mobile gập thành topbar `48px` + drawer.
- Ngân sách màu `<1%`: chrome vô sắc, nút chính dùng `--primary-ink-bg` (`#1f3038`), teal `--chart-1` chỉ dùng cho chuỗi dữ liệu chính; chỉ in đậm (`700`) cho số tiền khi cần nổi bật, mọi số liệu căn phải `tabular-nums`, số `0` hiển thị `0`, thiếu hiển thị `—`.

### Admin Portal `(admin)` — *Framed sheet*
- Nguồn: `.taste-skill/admin/direction.json`, `.taste-skill/admin-sheet.md`.
- Dày thông tin: KPI strip + biểu đồ chính + side rail, bảng kèm tab gạch chân `2px` & filter bar, màn cấu hình 2 cột (`450px` nhãn + `700px` panel).
- Shell `Tinted frame, white sheet`: sidebar `228px` và topbar `48px` dùng chung nền `--surface` tạo khung chữ L (không kẻ viền giữa sidebar và topbar); nội dung nằm trong tờ trắng `--card` inset `8px`, bo `--radius-sheet` (`16px`), viền `1px solid var(--border)`, không đổ bóng.
- Ngân sách màu `<3%`: tối đa 3 lớp lồng nhau (frame → sheet → card).

## 3. Sản phẩm tham chiếu & Lý do

- **Tính năng storefront (55 tính năng)**: **Coolmate** (`.taste-skill/swarm-brief.md`, `.taste-skill/features-brief.md`, `.taste-skill/research/{browse,product,checkout}.md`) — tham chiếu độ phủ tính năng bán đồ basic VN (lọc, bảng size/chất liệu, combo, flash sale, hội viên, hậu mãi). **Cấm** dùng tên, logo, câu chữ, ảnh, tên chương trình, chính sách hay bố cục của Coolmate.
- **Nguyên tắc bố cục `contact-sheet`**: 3 site nghiên cứu trong `.taste-skill/direction.json` & `.taste-skill/storefront-sheet.md` (chỉ lấy nguyên tắc, cấm lấy tên/copy/ảnh): **Smallbits** (`opl-smallbits`), **PROOOOF** (`opl-proooof`), **Decathlon Yestalgia** (`aww-decathlon-yestalgia`).
- **Seller Center & Admin Portal**: **chưa chọn** sản phẩm thương mại tham chiếu cụ thể (sheet trong `.taste-skill/{seller,admin}/direction.json` tổng hợp từ các màn ẩn danh).
- **Hướng site riêng đa gian hàng (tài liệu nghiên cứu)**: `docs/research/sites/{COMMON.md,fit.md,review-opus.md,shopify.md}` đối chiếu **Shopify** (theme/section JSON có schema, khóa checkout), **Etsy Pattern** (1 nguồn tồn/đánh giá nuôi song song sàn + site riêng) và **Shop Pay** (1 tài khoản dùng chung); loại trừ mở HTML/CSS/Liquid hay kéo-thả tự do.

## 4. Điều cấm (House Rules)

Nguồn: `.taste-skill/{storefront-sheet,seller-sheet,admin-sheet,swarm-brief}.md`, `web/DESIGN_REPORT.md`, chú thích `web/src/styles/tokens.css`.

- **Toàn site**: cấm gradient, cấm shadow lúc nghỉ (resting shadow), cấm glassmorphism/glow, cấm icon đặt trong ô màu (icon cards), cấm sparkline trên KPI, cấm biểu đồ cầu vồng, cấm emoji, cấm bảng màu cream-and-terracotta hoặc violet/indigo, cấm lorem/Acme/logo báo chí bịa, cấm form nhập số thẻ ngân hàng/CVV.
- **Trên `(shop)`**: cấm eyebrow label trên heading, cấm font monospace, cấm đánh số `01/02/03`, cấm thin rules trang trí, heading tối đa `2` dòng ở mọi viewport, chữ thân chạy tối thiểu `16px` màu `--ink` (không chữ xám mờ, tương phản ≥ `4.5:1`), không cỡ chữ dưới `13px`, không dùng opacity mờ để phân biệt mục đang chọn với mục thường.
- **Trên `(seller)` / `(admin)`**: cấm nút chính màu thương hiệu trên `(seller)`, cấm tô màu nền thẻ KPI, cấm biểu đồ thiếu trục Y, cấm kẻ viền giữa sidebar và topbar trên `(admin)`, cấm bóng dưới sheet, cấm lồng quá 3 cấp, cấm tên kỹ thuật nội bộ (`Redis Lua`, `optimistic locking`, `HMAC`, `Saga`, `JWT`, `OpenTelemetry`).
- **Màu cứng đã biết cần tránh lặp lại** (`docs/research/sites/fit.md` §3): không thêm màu hex cứng mới ngoài `tokens.css` (hiện còn hex lẻ ở `web/src/components/Button.module.css` và `web/src/components/ShopFooter.module.css`).

## 5. Cách soát & Kiểm định Playwright (390 / 768 / 1440)

1. Chạy cổng tĩnh trong `web/`:
   ```bash
   cd web && npm run lint && npm run format:check && npm run check:data && npm run build
   ```
2. Chạy server sản phẩm cách ly để chụp màn hình: **không kill** cổng `3111` (server manager) hay `4777` (agent monitor); dùng cổng khác (ví dụ `PORT=3120 npm run start`) rồi dừng sạch khi xong.
3. Chụp Playwright ở đủ 3 kích thước **1440** (desktop), **768** (tablet), **390** (mobile) theo cấu trúc mẫu trong `.taste-skill/inspect/`, `.taste-skill/inspect-p2/`, `.taste-skill/inspect-pdp/` (`{desktop,tablet,mobile}-{fold,page-01..}.jpg` + `report.json`). Lưu ý: script `lint.mjs`/`inspect.mjs` của skill `design-taste-frontend` (`--app` cho seller/admin) do manager Claude Code chạy khi review (`CLAUDE.md`).
4. Mở trực tiếp ảnh chụp desktop + mobile để đối chiếu với token và điều cấm:
   - `horizontalOverflow: 0` ở cả 3 viewport; nạp đúng font `Work Sans`.
   - Không hàng lưới mồ côi, không chữ bị cắt mép (`edge-text`) hay bị che bởi thanh đáy mobile (`covered-text`), không khoảng trống quá nửa màn hình.
   - Đủ trạng thái `?state=loading`, `?state=empty`, `?state=error`; không rò tên thương hiệu ngoài hay thuật ngữ kỹ thuật trên `(shop)`.
