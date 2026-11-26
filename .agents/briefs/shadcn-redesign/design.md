# Thiết kế UI/UX: làm lại toàn bộ web NexusMart theo Figma "Shopify UI Screens" trên shadcn/ui

- Ngày: 2026-11-26. Trạng thái: **bản nháp, chờ chủ dự án duyệt**. Người viết: Claude Code (manager). Người làm: agy.
- Nguồn tham chiếu: Figma `Shopify UI Screens (Community)`. Ảnh đã cắt nằm ở `figma/` cạnh file này:
  `01-signup`, `02-home-setup-guide`, `03-order-detail`, `04-products-index`, `05-analytics`,
  `06-select-plan`, `07-components-basic`, `08-components-feature`.
- Vị trí file: đặt ở `.agents/briefs/` chứ không ở `docs/`, vì theo `CLAUDE.md` thì `docs/` do agy viết.
  agy sẽ chép nội dung đã duyệt sang `docs/adr/` (phase 0).

---

## 1. Ý định

Chủ dự án thấy bộ màn hình quản trị trong Figma có UI/UX tốt, muốn toàn bộ web NexusMart theo đó.
Toàn bộ nghĩa là trang nền tảng, đăng nhập, Kênh người bán, Cổng quản trị và **cả site gian hàng của người mua**.
Kèm theo, web phải chuyển sang **một thư viện UI duy nhất: shadcn/ui**.

Lý do phía sau (suy ra từ yêu cầu, chủ dự án sửa nếu sai):
- Giao diện hiện tại là CSS Modules tự viết. Mỗi khu vực có art direction riêng (contact sheet, ledger,
  framed sheet), nên toàn web thiếu nhất quán.
- Muốn một bộ component chung để các agent dựng trang mới nhanh và đồng đều.

## 2. Mục tiêu, không làm, phản mục tiêu

**Mục tiêu**
1. Toàn web dùng chung một bộ component shadcn/ui (Radix + Tailwind v4) và một lớp biến màu `--ui-*`.
2. Kênh người bán và Cổng quản trị theo sát Figma: khung trang, mật độ, thẻ, bảng, nhãn trạng thái, đầu trang.
3. Đăng nhập/đăng ký theo bố cục chia đôi của `01-signup`.
4. Site gian hàng: giữ 8 mẫu giao diện. Mỗi site tự đổi màu, font, bo góc của shadcn theo mẫu shop đã chọn.
5. Sau khi xong không còn CSS Module, không còn token cũ, không còn component tự viết trùng chức năng shadcn.

**Không làm**
- Không đổi backend, gateway, BFF route `/api/*`, hợp đồng dữ liệu.
- Không đổi schema Template JSON v2 (`templateTypes.ts`). Cấu hình site người bán đã lưu phải chạy nguyên vẹn.
- Không thêm tính năng mà nền tảng chưa có, dù Figma có: gói trả phí/bảng giá, Point of Sale, App store,
  bình luận nội bộ trên dòng thời gian đơn, trung tâm thông báo, hCaptcha, "Markets".
- Không làm chế độ tối cho người bán và quản trị (Figma chỉ có nền sáng).
- Không thiết kế lại trang chủ nền tảng: nó đang được làm lại riêng (vòng 2, 26/11, brief `.agents/briefs/home-shopify-v2.md`). Khi tới phase 3 chỉ chuyển bản trang chủ mới nhất trên `main` sang Tailwind/shadcn và giữ nguyên bố cục.

**Phản mục tiêu** (chạy được nhưng vẫn tính là hỏng)
- Dùng tên, logo, icon, ảnh, câu chữ hay số liệu của Shopify; để lại chữ tiếng Anh mẫu.
- Một component vừa dùng class Tailwind vừa dùng CSS Module.
- Trong lúc chuyển dần, một khu vực chưa đụng tới bị đổi giao diện.
- 8 mẫu site trông như nhau, hoặc site đã lưu bị vỡ.
- Trang mua hàng chậm đi rõ rệt: LCP hay JS tải thêm tăng mạnh so với hiện tại.
- Mất a11y: focus, nhãn, tương phản, điều khiển bằng bàn phím.
- Bịa dữ liệu: bước hướng dẫn tự đánh dấu xong, số liệu giả, lời chứng thực giả.

## 3. Ràng buộc

- Next.js 15 App Router, React 19, TypeScript strict, Node 24, `output: "standalone"`.
- CSP hiện không có `style-src`/`script-src`, nên Radix dùng inline style định vị được. Không nới CSP. CSP khai ở hai chỗ:
  `web/next.config.ts` và `buildSiteCspHeader()` (`src/lib/server/config.ts`, áp qua `src/middleware.ts`).
- **Không mất chức năng hay hành vi đang có.** Mọi drawer xem nhanh, hàng mở rộng sửa tồn, tab, thanh điều hướng đáy
  mobile, công cụ đang chạy đều được giữ và đổi sang component mới. Chỗ nào tài liệu này mô tả khác code mà không ghi
  rõ "bỏ" thì code hiện tại thắng, và agy báo lại.
- Mạng: `registry.npmjs.org` và registry shadcn (`ui.shadcn.com/r/...`) đều trả 200. Nếu CLI shadcn lỗi mạng,
  agy chép tay mã component từ tài liệu shadcn vào `src/components/ui/`.
- Luật hiện hành đang cấm việc này: `web/CLAUDE.md` (cấm Tailwind, thư viện UI/icon, cấm bóng tĩnh), skill
  `design-system`, `.taste-skill/direction.json`. Phase 0 phải sửa các luật này trước khi code.
- `AGENTS.md` luật 11: năng lực mới cần ADR. Phase 0 viết `docs/adr/00xx-shadcn-tailwind.md` (agy viết).
- Mọi chữ hiển thị là tiếng Việt, mang thương hiệu NexusMart. Chữ cho người mua không chứa thuật ngữ kỹ thuật.
- Tiền là số nguyên VND, hiển thị bằng `Price`/`formatVnd` hiện có.

## 4. Các cách đã cân nhắc

| Quyết định | Phương án | Được/mất | Chọn |
|---|---|---|---|
| Thư viện | antd | Bảng, form, chọn ngày có sẵn, có tiếng Việt. Nhưng CSS-in-JS chạy lúc runtime làm nặng trang mua hàng, và đổi theme theo từng site khó | |
| | **shadcn/ui** | Theme bằng biến CSS, khớp cơ chế theme từng site sẵn có. Không tốn JS style lúc chạy. Mã nằm trong repo nên chỉnh theo Figma dễ. Đổi lại phải thêm Tailwind và tự ghép bảng | **Chủ dự án chọn (26/11)** |
| | Shuffle / "uilib" | Shuffle là công cụ dựng template, không phải thư viện component. "uilib" chưa xác định được | |
| | Shopify Polaris | Bản React đã ngừng bảo trì, giấy phép riêng | loại |
| Site gian hàng | **A. Một lớp biến `--ui-*`, giữ 8 mẫu** | Site đã lưu không vỡ, 8 mẫu vẫn khác nhau. Đổi lại phải làm lại phần hiển thị của từng mẫu bằng primitive mới | **Chọn** (đề xuất của Claude; chủ dự án bảo "continue", 26/11) |
| | B. Một giao diện chung kiểu Shopify | Nhanh hơn. Nhưng 8 mẫu gần như giống nhau, phần chọn mẫu trong Site Studio mất ý nghĩa | |
| Cách chuyển | **Theo khu vực, có trang mẫu duyệt trước** | Mỗi khu vực chuyển trọn, có điểm duyệt bằng mắt sớm, swarm chạy song song được theo thư mục | **Chọn** |
| | Đổi một lần toàn bộ | Một diff khổng lồ, không review nổi | loại |
| | Từng trang lẻ, không có nền chung | Bốn swarm sẽ ra bốn kiểu | loại |
| Bản duyệt hình | **Trang `/design` (chỉ quản trị) dựng bằng component thật** | Duyệt đúng thứ sẽ chạy, kèm đổi 8 mẫu site ngay trong trang | **Chọn** |
| | Mockup HTML tĩnh cho từng trang | Thấy hình sớm hơn, nhưng mockup sẽ lệch component thật và phải vẽ 53 trang | loại |

## 5. Nền kỹ thuật

### 5.1 Tailwind v4 không preflight trong lúc chuyển dần
- Cài `tailwindcss`, `@tailwindcss/postcss`, tạo `postcss.config.mjs`.
- Trong `src/styles/ui.css` (file mới). **Không import ở root layout**, mà ở layout của từng khu vực đã chuyển (phase 0: `(design)`; phase 1–4: layout của khu vực đó), để trang cũ không tải thêm 15 KB gzip CSS. `globals.css` mở đầu bằng `@layer theme, base, components, utilities;` để thứ tự layer cố định:
  ```css
  @layer theme, base, components, utilities;
  @import "tailwindcss/theme.css" layer(theme);
  @import "tailwindcss/utilities.css" layer(utilities);
  @import "tw-animate-css";
  @custom-variant dark (&:where([data-site-theme-mode="dark"], [data-site-theme-mode="dark"] *));
  ```
  Chưa import preflight, vì preflight sẽ đổi heading, margin và nút ở cả 53 trang cùng lúc.
- `globals.css` hiện không nằm trong layer nào, nên rule kiểu `h1 {…}`, `button {…}`, `a {…}` của nó sẽ thắng utility
  của shadcn. Phase 0 chỉ chuyển rule có **độ đặc hiệu ≤ (0,0,1)** (chỉ phần tử, `*`, `::pseudo-element`: `html`,
  `body`, `h1`–`h6`, `p`, `a`, `button`, `img`…) vào `@layer base`.
  - Rule theo class, thuộc tính hay trạng thái giữ **ngoài layer**: `ul[class], ol[class]`, `:focus-visible`,
    `tbody tr:last-child td`, `[data-reveal]`, `.sr-only`, `.type-*`…
    Lý do: các rule này có độ đặc hiệu cao hơn rule CSS Module và đang thắng nó (ví dụ `ul[class]` (0,1,1) thắng
    `.pdpDetailList` (0,1,0)). Đưa vào layer thì chúng thua, và trang cũ đổi hình (bản đầu của phase 0 đã làm danh sách
    PDP thụt lề). Trong vùng `UiScope`, `ui.css` có override ngoài layer dùng `revert-layer` để trả quyền cho
    base/utility của Tailwind.
  - `UiScope` xuất `data-ui="true"`, và selector luôn là `[data-ui="true"]`, vì mã cũ đã dùng `data-ui` với giá trị khác.
  - Có `postcss.config.mjs` riêng thì Next bỏ autoprefixer mặc định, nên thêm devDependency `autoprefixer` với danh sách
    trình duyệt mặc định của Next để CSS cũ giữ nguyên. Gỡ ở phase 5.
  - Soát bằng Playwright rằng các trang CSS Module cũ không đổi hình, và chụp thêm lúc bấm Tab ở 2–3 trang cũ để
    chắc vòng focus vẫn hiện.
- Phase cuối (dọn dẹp) bật preflight (`@import "tailwindcss/preflight.css" layer(base)`), rồi xoá reset cũ.

### 5.2 Biến màu `--ui-*`, tránh đè token cũ
Biến chuẩn của shadcn (`--accent`, `--muted`, `--border`, `--card`, `--chart-1`…) **trùng tên** token cũ trong
`tokens.css`, nhưng nghĩa khác: `--accent` cũ là màu teal thương hiệu, còn ở shadcn là nền hover. Nếu dùng tên gốc,
mọi CSS Module chưa chuyển sẽ vỡ màu.
- Biến thô đặt tên `--ui-*`, ví dụ `--ui-background`, `--ui-primary`, `--ui-accent`, `--ui-radius`.
- `@theme inline` ánh xạ tên màu Tailwind chuẩn sang `--ui-*`:
  `--color-background: var(--ui-background)`, `--color-primary: var(--ui-primary)`, `--radius-lg: var(--ui-radius)`, …
- Nhờ vậy mã component shadcn (`bg-primary`, `text-muted-foreground`, `rounded-md`) chép vào **giữ nguyên**,
  token cũ vẫn sống tới phase dọn dẹp.
- Thêm màu riêng: `brand` (teal NexusMart), 6 tông nhãn `badge-{success|attention|warning|info|critical|neutral}`
  (mỗi tông có `-bg`/`-ink`), và `topbar`, `sidebar`.
- **Trùng tên với theme của chính Tailwind.** Đã soát `tokens.css`, `globals.css`, `site.module.css`,
  `siteTemplates.module.css`: trùng `--font-sans`, `--ease-out`, `--ease-in-out`. Token cũ không nằm trong layer
  nên sẽ thắng: utility `font-sans` sẽ ra Archivo và Inter không bao giờ hiện.
  - Cách xử lý: khai ba biến này trong `@theme inline` (`--font-sans: var(--font-inter), ui-sans-serif, system-ui, sans-serif`,
    v.v.) để utility biên dịch thẳng ra giá trị, không đọc biến `:root`.
  - Phần tử gốc của khu vực đã chuyển (`AppShell`, `AuthSplit`, gốc site) gắn `font-sans` hoặc font site tương ứng.
- **CLI shadcn tự ghi CSS và phá lớp cách ly.** Phase 0 phải xử lý cả ba:
  - `shadcn init` chèn khối `@layer base` gồm `* { border-color: … }`, `body { background; color }`, outline-ring.
    Khối này không được áp toàn cục, vì sẽ tô lại mọi trang cũ (nền body, màu viền, trang chủ tối). Chỉ áp trong
    `[data-ui]`, thuộc tính gắn trên phần tử gốc khu vực đã chuyển, tới phase 5.
  - `shadcn add` chèn `cssVars` của component vào `:root` bằng tên chuẩn (`sidebar` thêm `--sidebar*`, `chart` thêm
    `--chart-1…5` trùng token cũ). Sau mỗi lần `add`, diff `ui.css` và đổi tên biến vừa chèn sang `--ui-*`.
  - Tiêu chí phase 0: giao giữa tập tên custom property của `tokens.css` + `globals.css` + `site*.module.css` với tập
    biến do Tailwind theme + shadcn khai phải **rỗng**, hoặc mỗi phần tử trùng đã được xử lý như trên.
    Kèm script kiểm trong báo cáo.

### 5.3 Font
- Phần ứng dụng (nền tảng, đăng nhập, người bán, quản trị): **Inter** (`next/font/google`, subset `latin` + `vietnamese`,
  biến `--font-inter`). Figma dùng Inter. Archivo bị gỡ ở phase dọn dẹp.
- Site gian hàng dùng đúng font mẫu đã chọn. Danh sách được phép có sẵn ở `ALLOWED_SITE_FONTS`:
  `work-sans`, `be-vietnam-pro`, `inter`, `ibm-plex-sans`, `plus-jakarta-sans`.
  Hiện `resolveSiteFontStack` bỏ qua font này và luôn trả Archivo, còn `SITE_FONT_LABELS` (`src/lib/site/types.ts`)
  gán cả 5 font thành nhãn "Archivo". Thiết kế mới sửa cả hai để dùng đúng font và nhãn thật.
  Nạp cả 5 bằng `next/font/google` với `preload: false`. Trình duyệt chỉ tải file của font site thật sự dùng.
- Site Studio thêm ô chọn font (`Select`, 5 lựa chọn) trong chế độ "Tuỳ chỉnh". Trường `font_family` đã có trong schema
  và đã được kiểm tra, hiện chỉ sửa được qua tab mã JSON.

### 5.4 Phụ thuộc thêm (chỉ chừng này)
`tailwindcss`, `@tailwindcss/postcss`, `tw-animate-css`, `class-variance-authority`, `clsx`, `tailwind-merge`,
`radix-ui`, `lucide-react`, `cmdk` (ô tìm kiếm ⌘K), `sonner` (toast).

Không thêm:
- `@tanstack/react-table`: các trang đã có sẵn logic lọc, phân trang, chọn dòng; dùng primitive `Table`.
- `react-hook-form`/`zod`: form giữ logic kiểm tra hiện có, dùng component `Field` của shadcn.
- `recharts`: giữ `SvgChart` (quản trị) và `SellerTrendChart` (tổng quan người bán), chỉ đổi kiểu. Bỏ `linearGradient`
  của `SellerTrendChart`, thay bằng vùng tô phẳng độ mờ thấp.
- `react-day-picker`: ngày giờ dùng `<input type="date|datetime-local">` bọc trong `Input`.

### 5.5 Cấu trúc thư mục
- `components.json`: style `new-york`, `rsc: true`, `tsx: true`, `css: src/styles/ui.css`, `iconLibrary: lucide`,
  alias `@/components/ui`, `@/lib/utils`.
- `src/components/ui/*`: mã shadcn, chỉ chỉnh theo §6 và §7.
- `src/components/app/*`: pattern ghép từ shadcn, dùng chung nhiều khu vực: `AppShell`, `PageHeader`, `StatusBadge`,
  `IndexTable`, `MetricStrip`, `DetailLayout`, `EmptyState`, `ErrorState`, `AuthSplit`, `KpiChartCard`, `SetupGuide`, `Timeline`.
- `src/lib/utils.ts`: hàm `cn`.
- Component cũ trong `src/components/` (`Button`, `StateView`, `modalFocus`…) và `AdminUi.tsx` (`AdminDrawer`,
  `AdminConfirmDialog`, `SlidingTabBar`) bị thay dần, rồi xoá khi không còn ai import.

## 6. Ngôn ngữ thị giác (phần ứng dụng: nền tảng, đăng nhập, người bán, quản trị)

Theo sát Figma, đổi đen Shopify sang slate NexusMart, nhấn bằng teal. Mọi cặp chữ/nền dưới đây đã đo:
chữ ≥ 4.5:1, viền ô nhập ≥ 3:1.

### 6.1 Màu
| Biến | Giá trị | Dùng cho | Tương phản |
|---|---|---|---|
| `--ui-background` | `#EFF3F5` | nền vùng nội dung | chữ `#1F3038`: 12.2 |
| `--ui-foreground` | `#1F3038` | chữ chính | |
| `--ui-card`, `--ui-popover` | `#FFFFFF` | thẻ, menu, hộp thoại | |
| `--ui-primary` / `-foreground` | `#1F3038` / `#FFFFFF` | nút chính (tối như Figma) | 13.6 |
| `--ui-secondary` / `-foreground` | `#E4EAEE` / `#1F3038` | nút xám ("Xuất", "Nhập") | |
| `--ui-muted` / `-foreground` | `#EFF3F5` / `#52626A` | chữ phụ | trên trắng 6.3, trên nền 5.7 |
| `--ui-accent` / `-foreground` | `#E4EAEE` / `#152229` | nền hover của menu và dòng bảng | |
| `--ui-brand` / `-foreground` | `#0D7480` / `#FFFFFF` | link, viền focus, mục nav đang chọn, thanh tiến độ, đường biểu đồ chính | trên trắng 5.5, trên nền 4.9 |
| `--ui-destructive` | `#B33A3A` | nút xoá, chữ "Hết hàng" | 5.9 |
| `--ui-border` | `#DDE4E8` | viền thẻ, vạch ngăn bảng | |
| `--ui-input` | `#7D8E97` | viền ô nhập, checkbox | 3.4 |
| `--ui-ring` | `#0D7480` | vòng focus 2px | |
| `--ui-topbar` / chữ | `#152229` / `#D3DDE2` | thanh trên | 11.8 |
| ô tìm trên thanh trên | nền `#293E48`, chữ gợi ý `#9FB0B8` | | 5.0 |
| `--ui-sidebar` / chữ / chữ phụ | `#E6ECEF` / `#1F3038` / `#52626A` | menu trái | 11.5 / 5.3 |

Nhãn trạng thái (bo tròn hẳn, chữ 12px đậm 500, có chấm hoặc icon nhỏ như Figma):
| Tông | Nền / chữ | Tương phản | Figma tương ứng |
|---|---|---|---|
| success | `#DDF3E6` / `#145338` | 7.8 | Active |
| attention | `#FBEFB5` / `#5C4A00` | 7.4 | Unfulfilled |
| warning | `#FCDDB8` / `#6B3D00` | 7.1 | Payment pending, Pending |
| info | `#DCEBFA` / `#0B4A7A` | 7.6 | Draft |
| critical | `#F8E2E2` / `#8C2626` | 7.0 | (huỷ, lỗi) |
| neutral | `#E6ECEF` / `#2C3E47` | 9.3 | Archived |

Trên site gian hàng dùng mẫu tối (`[data-site-theme-mode="dark"]`), các tông trên đổi sang bộ tối dưới đây. Tương phản
đã đo trên nền `#12161B`, `#151314`, `#1E242B`:
| Tông | Nền / chữ (tối) | Tương phản |
|---|---|---|
| success | `#16352A` / `#8EE0B5` | 8.6 |
| attention | `#3A3210` / `#F2DE7A` | 9.4 |
| warning | `#3D2A12` / `#F5B97A` | 7.9 |
| info | `#132C44` / `#8CC4F5` | 7.7 |
| critical | `#3D1A1A` / `#F5A3A3` | 7.8 |
| neutral | `#262C33` / `#D3DDE2` | 10.2 |
| `--ui-destructive` | `#F07A7A` | ≥ 5.8 trên cả ba nền |

### 6.2 Chữ (Inter, số dùng `tabular-nums`)
| Vai trò | Cỡ / dòng / độ đậm |
|---|---|
| Tiêu đề trang | 20px / 28px / 650 |
| Tiêu đề thẻ | 14px / 20px / 650 |
| Chữ thân (mặc định) | 14px / 20px / 450 |
| Chữ phụ, đầu cột bảng, nhãn | 13px / 18px / 450–550 |
| Nhãn trạng thái | 12px / 16px / 550 |
| Số KPI | 24px / 32px / 650 |

Figma dùng 13px. Ở đây tăng lên 14px vì dấu tiếng Việt cần thêm chiều cao.

### 6.3 Khoảng cách, bo góc, bóng
- Thang 4px. Đệm thẻ 16px (màn ≥ 768: 16px 20px). Khoảng giữa các thẻ 16px. Lề trang 16px (mobile) / 24px / 32px (≥1280).
- Bo góc: thẻ 12px, nút/ô nhập 8px, nhãn tròn hẳn, menu/hộp thoại 12px. `--ui-radius: 0.75rem`.
- Bóng: bỏ luật cũ "không đổ bóng tĩnh" cho phần ứng dụng, vì Figma có:
  - thẻ: `0 1px 0 rgba(21,34,41,.07), 0 0 0 1px rgba(21,34,41,.06)`;
  - nút chính có gờ: `inset 0 -1px 0 rgba(0,0,0,.45), inset 0 1px 0 rgba(255,255,255,.14)`;
  - menu/hộp thoại: `0 8px 24px rgba(21,34,41,.16)`.
- Không gradient trong phần ứng dụng.

### 6.4 Icon và chuyển động
- `lucide-react`, cỡ 16px trong nút/nav, 20px ở đầu trang, nét 1.75. Không dùng icon của Shopify.
- Chuyển động: `tw-animate-css` cho mở/đóng Radix (150–200ms). Tôn trọng `prefers-reduced-motion`.

## 7. Danh mục component (Figma → shadcn → NexusMart)

| Figma (`07`, `08`) | shadcn | Biến thể / chỉnh | Dùng ở |
|---|---|---|---|
| Nút tối "Add product", "Create shipping label" | `button` | `default` = tối + gờ | hành động chính, mỗi vùng tối đa một nút |
| Nút trắng viền "Fulfill item", "Send invoice" | `button` | `outline` = nền trắng, viền `--ui-border`, bóng thẻ | hành động phụ trong thẻ |
| Nút xám "Export", "Import", "More actions ▾" | `button` + `dropdown-menu` | `secondary`; "Thêm thao tác ▾" là `DropdownMenu` | đầu trang |
| Nút chỉ chữ "Import products", link xanh | `button` | `ghost`, `link` (màu `brand`) | |
| Nút icon (lọc, sắp xếp, ‹ ›) | `button` | `size="icon"`, `outline` | thanh công cụ bảng, đầu trang chi tiết |
| Ô tìm "Search ⌘K" | `command` trong `dialog` | ô tối trên thanh trên mở `CommandDialog` | AppShell |
| Email / Password, ô hiện mật khẩu | `input`, `label`, `field` | nút con mắt `ghost` trong ô | đăng nhập, form |
| Thanh độ mạnh mật khẩu | `progress` | màu theo mức | đăng ký |
| Checkbox | `checkbox` | viền `--ui-input` | bảng, form |
| Radio vòng nét đứt "Manage your digital products" | `radio-group` / icon trạng thái | vòng nét đứt = chưa xong, dấu ✓ = xong | SetupGuide |
| Chip trạng thái | `badge` → `StatusBadge` | 6 tông (§6.1), có `icon` tuỳ chọn | bảng, đầu trang, thẻ |
| Chip biến thể "Brown / B5 / Grid" | `badge` | `secondary`, vuông nhẹ | dòng sản phẩm trong đơn |
| Giá "$18.00" viền tròn | `badge` | `outline` | giá trong thẻ |
| Tabs "All / Active / Draft / Archived" | `tabs` | kiểu nền xám cho tab đang chọn (Figma `04`) | `IndexTable` |
| Bảng sản phẩm | `table` | đầu cột 13px chữ phụ, dòng 52px, hover `--ui-accent`, cột checkbox | `IndexTable` |
| Thẻ trắng | `card` | `CardHeader` 14px đậm, `CardAction` cho nút ✎ / ⋯ / ✕ | mọi nơi |
| OrderItemList, OrderTotalSummary, CustomerDetailsCard | `card` + `separator` | ghép trong `DetailLayout` | chi tiết đơn |
| Setup guide (thu gọn được, "0 / 11 completed") | `collapsible` + `progress` | `SetupGuide` | Tổng quan người bán |
| Timeline + avatar chữ cái "JS" | `avatar` + danh sách | `Timeline` chỉ đọc (không có ô bình luận) | chi tiết đơn |
| Toast "Your trial just started" (góc dưới phải) | `sonner` | nền tối `#1F3038`, chữ trắng | sau khi lưu, thêm vào giỏ… |
| Banner tối "Extend your trial…" | `alert` | biến thể `inverse` (nền tối, nút trắng, nút ✕) | thông báo quan trọng của shop (vd. site chưa xuất bản) |
| (không có trong Figma) | `dialog`, `alert-dialog`, `sheet`, `popover`, `tooltip`, `select`, `textarea`, `switch`, `accordion`, `skeleton`, `breadcrumb`, `pagination`, `scroll-area`, `toggle-group`, `sidebar` | kiểu theo §6 | thay `AdminDrawer` → `Sheet`, `AdminConfirmDialog` → `AlertDialog`, `SlidingTabBar` → `Tabs`, `modalFocus` → Radix |

### 7.1 Ánh xạ trạng thái sang tông nhãn
| Đối tượng | Trạng thái → tông |
|---|---|
| Đơn hàng (người mua và người bán) | `PENDING` → warning · `CONFIRMED`, `PREPARING` → attention · `READY_TO_SHIP`, `SHIPPING` → info · `DELIVERED`, `COMPLETED` → success · `DELIVERY_FAILED`, `CANCELLED` → critical · `RETURN_REQUESTED` → warning · `RETURNED`, `REFUNDED` → neutral |
| Job vận đơn | `QUEUED` → info · `FAILED` → critical |
| Sản phẩm (`sellerApiTypes.ts`) | `APPROVED` → success · `PENDING` → attention · `REJECTED` → critical · `OUT_OF_STOCK` → warning · `HIDDEN` → neutral |
| Tài khoản ngân hàng | `VERIFIED` → success · `PENDING_OTP`, `PENDING_ADMIN_REVIEW` → attention · `REJECTED` → critical |
| Trả hàng, khiếu nại, duyệt gian hàng | đang chờ → attention · cần người bán/khách bổ sung → warning · chấp nhận/hoàn tất → success · từ chối → critical · đóng → neutral |
| Site | đã xuất bản → success · bản nháp → info · bị khoá → critical |

Nhãn chữ lấy đúng `label` đang có (`orderMeta.ts`, `GATEWAY_ORDER_STATUS_LABELS`, nhãn trạng thái phía người bán…).
Chỉ đổi tông màu. Hiện có 4 tông `pos | neg | warn | neutral`, thiết kế mới thành 6 tông. Trạng thái nào không có
trong bảng thì dùng neutral, và agy liệt kê chúng trong báo cáo.

## 8. Khung trang

### 8.1 `AppShell`: người bán và quản trị (Figma `02`–`06`)
```
┌──────────────────────────────────────────────────────────────────────────────┐
│ ◆ NexusMart [Người bán]      [ 🔍 Tìm kiếm hoặc thao tác…         ⌘K ]  (Tên shop ▾)│  thanh trên 56px, #152229
├───────────────┬──────────────────────────────────────────────────────────────┤
│ VẬN HÀNH      │                                                              │
│ ▣ Tổng quan   │   (nội dung trang, nền #EFF3F5)                              │
│ ▣ Đơn hàng  3 │                                                              │
│ ▣ Trả hàng  1 │  ← mục cấp 1 như hiện tại, giữ số đếm trả hàng chờ xử lý     │
│ ▣ Sản phẩm    │                                                              │
│   · Thêm mới  │  ← mục con chỉ hiện khi mục cha đang chọn                    │
│ ▣ Khuyến mãi  │                                                              │
│ KHÁCH HÀNG    │                                                              │
│ ▣ Tin nhắn    │                                                              │
│ ▣ Tương tác   │                                                              │
│ CỬA HÀNG      │                                                              │
│ ▣ Site bán hàng ↗│                                                           │
│ ▣ Tài chính   │                                                              │
│ ▣ Báo cáo AI  │                                                              │
│               │                                                              │
│ ⚙ Hồ sơ shop  │  ← ghim đáy như "Settings" của Figma                         │
└───────────────┴──────────────────────────────────────────────────────────────┘
   menu trái 240px, #E6ECEF
```
- Thanh trên:
  - Logo `NexusMark` + chữ "NexusMart" trắng, kèm nhãn nhỏ "Người bán" hoặc "Quản trị" để phân biệt khu vực.
  - Ô tìm ở giữa (tối đa 480px), mở `CommandDialog`. Nội dung lấy từ các mục nav và "Thao tác nhanh" đang có ở
    `SellerShell` (thêm sản phẩm, đơn chờ xác nhận, SKU sắp hết, trả hàng, tên miền…), hoặc mục nav theo quyền ở `AdminShell`.
  - Bên phải là nút shop/người dùng: avatar chữ cái + tên. Menu gồm Xem site (người bán), Hồ sơ, vai trò (quản trị),
    Về trang chủ NexusMart, Đăng xuất.
  - Không thêm chuông thông báo. Thông báo của người bán vẫn ở tab "Thông báo" của `/seller/tuong-tac` như hiện tại.
- Menu trái:
  - Nhóm và mục **giữ đúng** như `SellerShell`/`AdminShell` hiện tại, gồm cả lọc theo quyền 6 vai trò quản trị và trạng thái chưa có shop.
  - Mục đang chọn có nền trắng bo 8px, chữ đậm 600, icon màu `brand`. Số đếm ở bên phải (vd. đơn chờ) là `Badge` neutral.
  - Mục con thụt lề, chỉ hiện khi mục cha đang chọn (như Products → Collections… của Figma).
  - Giữ trạng thái thu gọn sidebar đang lưu trong `localStorage`.
- Responsive:
  - < 1024: menu trái thu thành icon 56px.
  - < 768: menu trái ẩn, mở bằng nút ☰ trên thanh trên dưới dạng `Sheet` từ trái. Ô tìm thu thành nút icon.
    **Giữ thanh điều hướng đáy 5 nút** đang có ở `SellerShell` (mobile), dựng lại bằng Tailwind.
    `AppShell` có slot `mobileNav` để người bán dùng, quản trị thì không.
- Dùng component `sidebar` của shadcn (`SidebarProvider`, `collapsible="icon"`). Lưu trạng thái vào `localStorage`
  như hiện tại, không dùng cookie.

### 8.2 `PageHeader`
```
←  #NM-10293  [Chờ thanh toán] [Chờ xử lý]            [In nhãn] [Thêm thao tác ▾] [‹][›]  [Xác nhận đơn]
   26/11/2026 lúc 09:48 · Site: aodep.nexusmart.vn
```
- Nút ← (`ghost` icon) chỉ có ở trang con. Tiêu đề 20px. Nhãn trạng thái đặt ngay sau tiêu đề, dòng phụ 13px bên dưới.
- Bên phải theo thứ tự: nút phụ (`secondary`), "Thêm thao tác ▾", ‹ › chuyển bản ghi (chỉ khi danh sách nguồn có sẵn),
  rồi nút chính (`default`) ở ngoài cùng.
- < 768: chỉ giữ nút chính. Các nút khác gom vào "Thêm thao tác ▾".

### 8.3 `AuthSplit`: đăng nhập, đăng ký, quên/đặt lại mật khẩu (Figma `01`)
```
┌──────────────────────────────────────────────────────────────────┐ nền #152229 phẳng
│ ┌──────────────────────────────┬───────────────────────────────┐ │
│ │ ◆ NexusMart                  │                               │ │
│ │                              │      ảnh /banners/...         │ │
│ │ Tạo tài khoản NexusMart      │      (bo 12px, phủ tối nhẹ)   │ │
│ │ Mở gian hàng miễn phí.       │                               │ │
│ │ Email     [               ]  │  "Cửa hàng mang tên bạn,      │ │
│ │ Mật khẩu  [            👁]  │   thanh toán COD và VietQR."  │ │
│ │ ▬▬▬▬▬▬▬▬ Độ mạnh: tốt        │   (câu giới thiệu thật, không │ │
│ │ [     Tạo tài khoản      ]   │    phải lời chứng thực)       │ │
│ │ ───────── hoặc ─────────     │                               │ │
│ │ Đã có tài khoản? Đăng nhập → │                               │ │
│ │ Trợ giúp · Chính sách        │                               │ │
│ └──────────────────────────────┴───────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────┘
```
- Thẻ trắng tối đa 1180px, bo 16px. Cột form 1/2, ô nhập tối đa 420px. Cột ảnh chỉ hiện từ 1024 trở lên.
- Mobile: thẻ chiếm toàn màn, nền trắng, không có ảnh.
- **Host gốc:** 4 trang auth đứng riêng, toàn màn, không có `PlatformHeader`/`PlatformFooter`, giống Figma.
  Vì `(shop)/layout.tsx` đang bọc header/footer cho mọi trang, phase 3 tách route group (URL không đổi):
  header/footer cho `/`, `/ve-nexusmart`, `/tro-giup` và 2 redirect; khung `AuthSplit` cho 4 trang auth.
  Logo ở góc thẻ là đường về trang chủ.
- **Site gian hàng:** trang auth vẫn nằm trong `SiteShell` (giữ đầu/cuối trang của shop, `[data-site-root]`, theme và
  `StoreContext`). `AuthSplit` hiện thành thẻ trong vùng nội dung, nhận màu site (§11). Cột ảnh dùng `banner_url` của
  mẫu, logo và tên là của shop.
- Giữ nguyên toàn bộ logic, field (họ tên, email, SĐT, mật khẩu, nhập lại, bước OTP 6 ô), thông báo lỗi và chuyển hướng
  của `AuthForms.tsx`/`PlatformAuthShell.tsx`. Trên host gốc giữ cả **nút chuyển vai trò "Người bán / Khách mua"**
  (dạng `ToggleGroup` trên form) và **nhãn gợi ý tên miền phụ** đi từ trang chủ (`?role=seller&ten=…`, dạng `Badge`
  dưới tiêu đề).
- Thanh độ mạnh mật khẩu là chỉ báo mới, chỉ chạy phía client (độ dài + loại ký tự). Nó không chặn gửi form ngoài
  phần kiểm tra đã có.
- Không có bộ chọn quốc gia và captcha như Figma.

### 8.4 Trang nền tảng (host gốc)
- `PlatformHeader`/`PlatformFooter` và trang chủ: chuyển sang Tailwind/shadcn, **giữ nguyên bố cục, màu và gradient** của bản
  trang chủ mới nhất trên `main` (vòng 2 theo `.agents/briefs/home-shopify-v2.md`, commit `04fec00` đang chạy production, thay bản `a9a4b1f`). Font Inter (vòng 2 đã dùng).
- `/ve-nexusmart`: giữ bố cục 4 màn ảnh toàn chiều rộng hiện có, chỉ chuyển công nghệ và font.
- `/tro-giup`: trang nội dung khổ 720px, tiêu đề theo §6.2. Câu hỏi thường gặp dùng `accordion`.

## 9. Kiểu trang

### A1. Danh sách + bảng (Figma `04`)
```
Ví dụ `/seller/san-pham` (các nút và tab đều lấy theo chức năng thật đang có):
```
Sản phẩm                                              [Nhập CSV] [Thêm thao tác ▾] [Thêm sản phẩm]
┌────────────────────────────────────────────────────────────────────────────────┐
│ Đang bán  128      │  Sắp hết hàng  6           │  Chờ duyệt  2                  │  MetricStrip (tuỳ trang, chỉ số liệu thật)
└────────────────────────────────────────────────────────────────────────────────┘
┌────────────────────────────────────────────────────────────────────────────────┐
│ [Tất cả] Đang bán  Chờ duyệt  Bị từ chối  Hết hàng  Đã ẩn          [🔍≡] [⇅]   │
├──────┬────────────────────────┬────────────┬────────────────┬──────────┬───────┤
│      │ Sản phẩm ⇅             │ Trạng thái │ Tồn kho        │ Danh mục │ Giá   │
├──────┼────────────────────────┼────────────┼────────────────┼──────────┼───────┤
│ [ảnh]│ Áo polo cổ bẻ          │ [Đang bán] │ 0 còn (đỏ) [Sửa tồn ▾] │ Áo │ 249.000₫│
│      │   └ SKU Xanh/L  [ 12 ] [Lưu]   ← hàng mở rộng sửa tồn inline, giữ như hiện tại │
│ [ảnh]│ Quần kaki ống đứng     │ [Chờ duyệt]│ 12 còn · 4 SKU │ Quần     │ 399.000₫│
├──────┴────────────────────────┴────────────┴────────────────┴──────────┴───────┤
│ ‹ Trước   Trang 1 / 7   Sau ›                                                  │
└────────────────────────────────────────────────────────────────────────────────┘
```
- Tabs lấy đúng bộ lọc mà trang đang có, không lấy của trang khác, và giữ query string hiện tại. Ví dụ:
  `/seller/don-hang` dùng 6 nhóm `SellerOrderFilterKey`, không dùng `ORDER_FILTER_TABS` của người mua.
  `/seller/san-pham` dùng bộ tab của `SellerProductsClient`.
- Nút 🔍≡ mở hàng tìm kiếm + bộ lọc ngay trên bảng.
- Cột checkbox **chỉ có ở trang đang có thao tác lô** (vd. lên đơn lô, in nhãn PDF ở đơn hàng). Chọn dòng → thanh
  công cụ thay cho hàng tabs, hiện "Đã chọn 3" kèm hành động lô.
- Bấm cả dòng: nếu trang đang có drawer xem nhanh (`SellerOrderQuickDrawer`, `SellerDrawer` sản phẩm, drawer quản trị)
  thì mở `Sheet` xem nhanh từ phải, trong đó có nút "Mở trang chi tiết". Nếu không có thì mở trang chi tiết.
  Ô checkbox và các nút trong dòng không kích hoạt việc này.
- `IndexTable` hỗ trợ hàng mở rộng (vd. sửa tồn từng SKU ngay trong bảng sản phẩm).
- Tồn kho 0 hiện chữ `destructive`.
- < 768: bảng thành danh sách thẻ, mỗi dòng gồm ảnh, tên, nhãn trạng thái và 1–2 thông tin phụ. Bỏ đầu cột. Checkbox giữ ở đầu dòng nếu trang có thao tác lô.
- Trạng thái: đang tải dùng `Skeleton` 5 dòng. Trống dùng `EmptyState` trong thẻ (icon lucide 40px, tiêu đề, một câu, nút chính).
  Lỗi dùng `ErrorState` với nút "Thử lại".

### A2. Chi tiết 2/3 + 1/3 (Figma `03`)
```
PageHeader (§8.2)
┌───────────────────────────────────────┐  ┌─────────────────────┐
│ [Chờ xử lý (2)]                   ⋯  │  │ Ghi chú của khách  ✎│
│ [ảnh] Áo polo · Xanh / L   249.000 × 1│  ├─────────────────────┤
│                    [Huỷ] [Xác nhận]   │  │ Khách hàng          │
└───────────────────────────────────────┘  │ Nguyễn A · 3 đơn    │
┌───────────────────────────────────────┐  │ Liên hệ        ⧉   │
│ [Chờ thanh toán]                      │  │ Địa chỉ giao   ⧉   │
│ Tạm tính   1 sản phẩm       249.000₫  │  └─────────────────────┘
│ Giảm giá   VOUCHER10        −24.900₫  │  ┌─────────────────────┐
│ Phí giao   GHN · 2 kg        30.000₫  │  │ Vận chuyển          │
│ Tổng                        254.100₫  │  │ mã vận đơn, hãng…   │
│ Thu khi giao (COD)          254.100₫  │  └─────────────────────┘
└───────────────────────────────────────┘
Lịch sử xử lý
│ ● 09:48 Đặt hàng   ● 10:02 Xác nhận   … (Timeline chỉ đọc, từ order.timeline)
```
- Cột chính tối đa 1000px tổng. Thẻ sidebar xếp xuống dưới cột chính khi < 1024.
- Nút ⧉ (sao chép) chỉ có ở email, SĐT, địa chỉ. ✎ chỉ có khi trang đã có chức năng sửa tương ứng (vd. đổi địa chỉ).

### A3. Form tạo/sửa (sản phẩm, khuyến mãi, flash sale)
- Cùng lưới 2/3 + 1/3 với A2:
  - Cột chính: thẻ "Thông tin" (tên, mô tả), "Hình ảnh", "Biến thể & SKU" (bảng sửa trực tiếp), "Giá".
  - Cột phụ: "Trạng thái" (`Select`), "Phân loại" (danh mục, thẻ).
- Khi form có thay đổi chưa lưu, hiện thanh lưu dính đỉnh, nền tối như thanh trên: "Thay đổi chưa lưu"
  + [Huỷ] [Lưu], theo thói quen Shopify. Thanh nằm trong khung trang, không đè lên menu trái.
- Lỗi nằm dưới từng ô (`FieldError`). Ô đầu tiên bị lỗi được focus khi bấm Lưu.

### A4. Tổng quan / phân tích (Figma `05`)
Dùng cho `/seller` (phần báo cáo theo kỳ, xuất CSV, `SellerTrendChart` đang nằm ở `SellerOverviewClient`) và `/admin`.
```
Tổng quan                                                        [Xuất CSV]
[📅 7 ngày qua ▾]
┌──────────────────┐┌──────────────────┐┌──────────────────┐
│ Doanh thu     ⓘ  ││ Đơn hàng      ⓘ  ││ Giá trị TB/đơn ⓘ │  KpiChartCard: tiêu đề gạch chân chấm (có Tooltip giải thích),
│ 12.450.000₫      ││ 54               ││ 230.555₫         │  số 24px, biểu đồ đường của kỳ đang chọn,
│  ╱‾‾ biểu đồ      ││                  ││                  │  chú giải khoảng ngày ở đáy
└──────────────────┘└──────────────────┘└──────────────────┘
```
- Lưới 3 cột ≥ 1280, 2 cột ≥ 768, 1 cột trên mobile.
- Chỉ hiện chỉ số mà API đang trả về. **Không có "So với kỳ trước"**, vì `GwSellerReport` lẫn `GatewayPlatformReport`
  đều không trả dữ liệu kỳ trước.

### A5. Cài đặt / hồ sơ
- Khổ 800px. Mỗi nhóm cài đặt là một hàng gồm cột trái 1/3 (tiêu đề + một câu giải thích) và cột phải 2/3 là thẻ chứa field.
- Lưu bằng thanh lưu dính như A3.
- Cài đặt nhiều phần (quản trị `cau-hinh`): xem §10. Tab nào là danh sách thì theo A1 + `Sheet`, không ép vào khổ 800px.

### A6. Hộp thư (tin nhắn)
- Hai cột: danh sách hội thoại 320px (tìm kiếm, mục chưa đọc in đậm, có chấm `brand`), bên phải là luồng tin.
  Ô soạn ở đáy, gửi bằng Enter.
- Mobile: chỉ thấy một cột, mở hội thoại thì trượt sang, có nút ← quay lại.
- Giữ nguyên logic WebSocket hiện có.

### A7. Trình sửa site (Site Studio, `SellerSiteEditorClient`)
- Toàn màn trong AppShell, menu trái thu thành icon.
- Thanh trên của vùng sửa: tên site + `StatusBadge` (bản nháp/đã xuất bản), bên phải [Lưu nháp] [Xuất bản].
  Bên dưới là thanh chuyển chế độ (`Tabs`), **giữ đủ chức năng của 6 tab hiện có** nhưng gom thành 5 chế độ:
  | Chế độ | Gom từ tab cũ | Bố cục |
  |---|---|---|
  | Mẫu | Thư viện mẫu | A8 toàn chiều rộng. Xem trước mẫu bằng `Dialog` lớn |
  | Tuỳ chỉnh | Giao diện & Bố cục + Khối & Block | Cột trái 360px chứa `Accordion` (màu + cảnh báo tương phản `checkThemeContrast`, font, logo/banner, thông báo, bố cục 4 trang phụ catalog/pdp/cart/info, slots, section và block của 5 trang); bên phải xem trước site với nút 390/768/1440 |
  | Mã JSON | Mã template | 2 cột: trình sửa JSON v2 có đánh số dòng + bảng lỗi bấm để nhảy dòng; bên phải xem trước trực tiếp |
  | Tên miền | Tên miền | giữ nội dung tab hiện có, theo A5 |
  | Phiên bản | Phiên bản | A1: danh sách phiên bản + [Khôi phục] (`AlertDialog` xác nhận) |
- Ô nhập subdomain đang nằm trong tab "Giao diện & Bố cục" chuyển sang chế độ "Tên miền".

### A8. Chọn mẫu bằng thẻ cột (Figma `06`)
- Dùng trong chế độ "Mẫu" của Site Studio. Onboarding mở shop (`SellerShopOnboarding`) **không** có bước chọn mẫu,
  vì API site chỉ mở khi shop đã `ACTIVE`.
- 3 cột thẻ. Mỗi thẻ gồm: nhãn nhỏ "Đang dùng" nếu có, tên mẫu 24px, mô tả, ảnh xem trước bo 8px, nút [Dùng mẫu này] full
  width, rồi danh sách đặc điểm (`tags`, bố cục) theo kiểu "Features" của Figma.
- Không có giá tiền, vì nền tảng không có gói trả phí.

### A9. Hướng dẫn thiết lập (Figma `02`)
- Thẻ đầu trang Tổng quan người bán (shop `ACTIVE`) khi chưa xong: "Hướng dẫn thiết lập · 1 / 3 hoàn thành" + `Progress`.
- Mỗi bước là một mục `Collapsible`. Bước đầu tiên chưa xong mở sẵn, có mô tả, [nút chính] [nút phụ] và ảnh minh hoạ
  bên phải (ảnh sẵn có trong `web/public`).
- Các bước, mỗi bước đánh dấu theo dữ liệu thật:
  | # | Bước | Xong khi | Nút |
  |---|---|---|---|
  | 1 | Thêm sản phẩm đầu tiên | shop có ≥ 1 sản phẩm, bất kể trạng thái duyệt | [Thêm sản phẩm] → `/seller/san-pham/moi`, [Nhập CSV] |
  | 2 | Thêm tài khoản ngân hàng nhận tiền | `bank_account.status` là `PENDING_ADMIN_REVIEW` hoặc `VERIFIED`. Khi `PENDING_ADMIN_REVIEW` thì kèm nhãn "Chờ duyệt" | [Thêm tài khoản] → `/seller/tai-chinh` |
  | 3 | Chọn mẫu và xuất bản site | site `PUBLISHED` | [Mở Site Studio] → `/seller/site` |
  | – | Gắn tên miền riêng | tên miền `CUSTOM` đã `ACTIVE`. Đang xác minh thì kèm nhãn "Đang xác minh" | [Thêm tên miền] → `/seller/site/ten-mien` |
- Bước tên miền có nhãn "Tuỳ chọn" và **không tính** vào tiến độ. Shop chỉ dùng tên miền phụ vẫn hoàn tất được.
- Việc chọn mẫu gộp vào bước xuất bản, vì API không phân biệt "đã tự chọn mẫu" với "bản nháp mặc định". Không tự đánh
  dấu một bước mà dữ liệu không chứng minh được.
- `/seller/page.tsx` nạp thêm `loadSellerBankAccount()` và `loadSellerSiteDomainsServer()` (BFF đã có) để tính trạng thái.
  Nạp lỗi thì bước đó hiện "Chưa kiểm tra được", không đánh dấu xong.
- Khi đủ 3 bước bắt buộc, thẻ đổi thành dòng "✓ Đã thiết lập xong" như "All caught up" của Figma.
  Người bán thu gọn được, trạng thái thu gọn lưu `localStorage`.
- `SellerSiteStatus` (thông tin site, link) vẫn hiện như hiện tại, nằm dưới thẻ hướng dẫn.
- Người dùng chưa có shop hoặc shop chờ duyệt: giữ luồng `SellerShopOnboarding` hiện có (hồ sơ gian hàng + địa chỉ kho
  → chờ duyệt), giao diện theo A5.

## 10. Bảng gán trang

| Tuyến | Kiểu | Ghi chú |
|---|---|---|
| `/` | nền tảng (§8.4) | chuyển công nghệ, giữ bố cục bản mới nhất trên `main` (vòng 2) |
| `/ve-nexusmart` | nội dung (§8.4) | giữ bố cục 4 màn ảnh lớn |
| `/tro-giup` | nội dung (§8.4) | `accordion` cho hỏi đáp |
| `/dang-nhap`, `/dang-ky`, `/quen-mat-khau`, `/dat-lai-mat-khau` | AuthSplit đứng riêng | route group mới, URL không đổi |
| `/seller` | A9 + A4 | chưa có shop/chờ duyệt: onboarding (A5). Shop `ACTIVE`: hướng dẫn thiết lập + báo cáo theo kỳ (lọc ngày, xuất CSV, `SellerTrendChart`) + việc cần làm + `SellerOrderQuickDrawer` |
| `/seller/don-hang` | A1 | 6 nhóm `SellerOrderFilterKey`, xem nhanh bằng `Sheet`, lên đơn lô, nhãn PDF, trạng thái job vận đơn |
| `/seller/don-hang/[id]` | A2 | đúng Figma `03` |
| `/seller/san-pham` | A1 | đúng Figma `04` nhưng không có checkbox lô, không có "Xuất CSV". Có nhập CSV (`Dialog`), lọc "sắp hết", xem nhanh `Sheet`, hàng mở rộng sửa tồn SKU |
| `/seller/san-pham/moi`, `/seller/san-pham/[id]` | A3 | |
| `/seller/tra-hang` | A1 + `Sheet` chi tiết | |
| `/seller/khuyen-mai` | A1 trong `Tabs` (voucher, flash sale, chương trình giảm giá) | tạo/sửa bằng `Sheet` hoặc trang A3 tuỳ độ dài form |
| `/seller/chat` | A6 | |
| `/seller/tuong-tac` | `Tabs`: "Đánh giá" (danh sách tóm tắt đánh giá AI theo sản phẩm, chỉ đọc) + "Thông báo" (danh sách, đánh dấu đã đọc) | không có hỏi đáp hay trả lời |
| `/seller/site` | A7 | |
| `/seller/site/xem-truoc` | giữ trong khung người bán như hiện tại | khung xem trước + nút 390/768/1440 |
| `/seller/site/ten-mien` | A5 + bảng bản ghi DNS | |
| `/seller/tai-chinh` | `MetricStrip` + `Tabs`: sổ cái (A1), tài khoản ngân hàng (A5, giữ luồng OTP) | không có biểu đồ, lọc ngày hay xuất CSV |
| `/seller/bao-cao-ai` | A4 | văn bản báo cáo trong thẻ |
| `/seller/ho-so` | A5 | |
| `/admin` | A4 | `SvgChart` |
| `/admin/gian-hang`, `/admin/khieu-nai`, `/admin/sites` | A1 + `Sheet` chi tiết + `AlertDialog` khi duyệt/từ chối | phê duyệt 2 người giữ nguyên |
| `/admin/kiem-duyet` | `Tabs`: hàng đợi duyệt (A1 + `Sheet`) + từ khoá cấm (`AdminBannedKeywordsTab`, A1) | |
| `/admin/flash-sale` | A1 + A3 | |
| `/admin/doi-soat` | A1 trong `Tabs` + `Dialog` tải CSV | |
| `/admin/cau-hinh` | `Tabs`, mỗi tab theo bản chất | 7 tab: Cổng thanh toán, Phê duyệt 2 người, Biểu phí, Hãng vận chuyển, Danh mục, Người dùng (A1 + `Sheet`), Voucher sàn & Xu (chỉ đọc), cùng khối `AdminSearchReindexBlock`. Tab nào là danh sách thì A1, tab nào là form thì A5 |
| `/admin/audit-log` | A1 chỉ đọc, bộ lọc | |
| site gian hàng (21 trang) | §11 | |

## 11. Site gian hàng (phương án A)

### 11.1 Ánh xạ theme mẫu → `--ui-*`
`buildSiteThemeVars` trả thêm các biến `--ui-*`, đặt trên phần tử gốc của site (nơi đang đặt `--site-*`).
Trong lúc chuyển, các biến `--site-*` cũ vẫn giữ.
- Hàm hiện nhận `SiteTheme` (`src/lib/site/types.ts`), kiểu này chưa có `radius`/`density` (hai trường nằm ở
  `SiteThemeV2`). Mở rộng tham số thành `SiteTheme & Partial<Pick<SiteThemeV2, "radius" | "density">>`, giữ tương
  thích với mọi chỗ đang gọi.
| `--ui-*` | Lấy từ theme mẫu |
|---|---|
| `background` | `bg_color` |
| `foreground` | `text_color` |
| `card`, `popover` | `surface_color` |
| `primary` / `-foreground` | `accent_color` / `accent_text_color` |
| `brand` | `accent_color` |
| `secondary`, `muted`, `accent` | `mixHex(text_color, bg_color, 0.06–0.10)` |
| `muted-foreground` | `mixHex(text_color, bg_color, 0.70)` (phải ≥ 4.5:1 so với `background`; không đạt thì tăng tỉ lệ) |
| `border` | `mixHex(text_color, bg_color, 0.14)` |
| `input` | `mixHex(text_color, bg_color, 0.45)` (≥ 3:1) |
| `ring` | `accent_color` |
| `radius` | `none` → 0 · `sm` → 4px · `md` → 8px · `lg` → 16px · rỗng → 0, giống fallback `"none"` hiện tại ở `SiteShell.tsx`, để site v1 cũ không đổi bo góc |
| font | `font_family` → biến font tương ứng (§5.3) |
| chế độ tối | dùng lại thuộc tính `data-site-theme-mode="dark"\|"light"` đang có trên `SiteShell` (E2E `03_isolation_and_security`, `05_custom_domains` kiểm thuộc tính này, phải giữ). Biến thể `dark:` và bộ màu tối §6.1 bám theo nó. Không thêm thuộc tính mới |

- `density` (`compact | balanced | airy`) chỉ đổi khoảng cách section và container (`--site-section-gap`, `--site-gutter`).
  Không đổi kích cỡ nút và ô nhập, để chạm bằng tay luôn ≥ 40px.
- Bóng trên site: mặc định không có, giữ chất phẳng của các mẫu. Chỉ menu, hộp thoại, sheet có bóng nổi.

### 11.2 Giữ nguyên
- 8 archetype, mọi giá trị `header_style`, `footer_style`, `container_width`, `card_style`, `catalog_layout`, `pdp_layout`,
  `cart_layout`, `info_layout`, `sections` và biến thể khối.
- Mỗi biến thể được dựng lại bằng utility Tailwind + primitive shadcn, nhưng phải giữ đúng sự khác biệt về cấu trúc mà
  `templateCatalog.ts` mô tả.
- Xoá `site.module.css` và `siteTemplates.module.css` khi không còn ai dùng.
- Logic dữ liệu, SEO, cô lập theo host, `StoreContext`, widget trợ lý AI, chat, `SiteSupportDock`: giữ nguyên, chỉ đổi giao diện.

### 11.3 UX từng màn (áp cho mọi mẫu, kiểu riêng theo mẫu)
| Màn | Thiết kế |
|---|---|
| Đầu trang | Theo `header_style`. Ô tìm mở `CommandDialog` (gợi ý từ `SiteSearchBar` hiện có). Icon giỏ có số lượng. Mobile: ☰ mở `Sheet` trái chứa nav + tài khoản |
| Trang chủ | Các section theo mẫu. Nút dùng `Button` (primary = `accent`). Thẻ sản phẩm thống nhất (dưới) |
| Thẻ sản phẩm | Ảnh tỉ lệ cố định bo theo `radius`, tên 2 dòng, `Price` (giá gốc gạch nếu có), nhãn giảm giá/flash sale là `Badge`. `card_style` đổi khung/viền/chữ |
| Danh mục / tìm kiếm | Theo `catalog_layout`. Bộ lọc là cột trái ≥ 1024, `Sheet` trên mobile (thay cho drawer tự viết đang có). Sắp xếp bằng `Select`. Phân trang bằng `Pagination`. Trống/lỗi/đang tải theo A1 |
| Chi tiết sản phẩm | Theo `pdp_layout`. Chọn biến thể bằng `ToggleGroup` dạng chip, SKU hết hàng thì mờ + gạch. Ô số lượng có nút −/+. [Thêm vào giỏ] primary + [Mua ngay] outline. Mobile: thanh mua dính đáy. Đánh giá + tóm tắt AI trong thẻ. Các khối thông tin dài (mô tả, thông số, chính sách) dùng `Accordion` hoặc giữ `CollapsibleProse` theo `pdp_layout` |
| Thêm vào giỏ | Giữ drawer xác nhận đang có ở `SitePdpView` (`cartConfirmDrawer`), dựng lại bằng `Sheet` phải: sản phẩm vừa thêm, tạm tính, [Xem giỏ hàng] [Thanh toán]. Thêm vào giỏ từ chỗ khác (thẻ sản phẩm ở danh mục, nếu có) thì hiện toast `sonner` |
| Giỏ hàng | Theo `cart_layout`. Dòng sản phẩm có ảnh, biến thể (`Badge` phụ), −/+ và xoá. SKU không còn bán: dòng mờ + `Alert` giải thích. Tóm tắt đơn ở cột phải dính khi cuộn |
| Thanh toán | 2 cột: trái là form (liên hệ, địa chỉ 2 cấp tỉnh/phường dùng `Select` có tìm kiếm, giao hàng, phương thức COD/VietQR/ZaloPay dạng thẻ `RadioGroup`); phải là tóm tắt đơn + mã giảm giá. Mobile: tóm tắt thành `Accordion` "Xem đơn hàng · 254.100₫" ở đầu trang. Nút [Đặt hàng] full width |
| Chờ thanh toán | Thẻ giữa trang: mã QR VietQR, số tiền, nội dung chuyển khoản (nút sao chép), trạng thái SSE (`Badge` warning → success), đếm ngược |
| Đơn hàng / chi tiết đơn / tra cứu | Danh sách thẻ đơn (mã, ngày, `StatusBadge`, tổng). Chi tiết theo A2 rút gọn. Đổi trả 10 bước dùng `Sheet` + bước tiến độ |
| Tài khoản | `Tabs` giữ đúng 4 tab hiện có và query `?tab=`: "Hồ sơ" (gồm xác minh SĐT OTP neo `#xac-minh-sdt` mà trang thanh toán trỏ tới, và cài đặt thông báo), "Sổ địa chỉ", "Mã giảm giá", "Yêu thích". Mỗi tab theo A5. Không thêm tab "Bảo mật" |
| Đăng nhập/đăng ký/quên | `AuthSplit` theo màu site (§8.3) |
| Giới thiệu, chính sách | Theo `info_layout`, chữ khổ 720px |
| Flash sale, voucher | Lưới thẻ. Voucher có nút [Lưu mã]/[Sao chép]. Flash sale có đồng hồ đếm ngược + `Progress` đã bán |

## 12. Nội dung

- Chữ tiếng Việt tự nhiên. Không có chữ Shopify, Point of Sale, Markets, trial, plan; không câu tiếng Anh mẫu từ Figma.
- Không lời chứng thực, không số liệu bịa. Minh hoạ chỉ dùng ảnh đã có trong `web/public/`.
- Ô trống thì có câu hướng dẫn cụ thể + nút hành động, ví dụ "Chưa có đơn nào. Chia sẻ link site để nhận đơn đầu tiên." [Sao chép link site].
- Người mua không thấy thuật ngữ kỹ thuật (JWT, saga, sandbox…).

## 13. A11y và responsive

- Một `h1` mỗi trang. Mỗi vùng chính có `aria-labelledby`. Nút icon có `aria-label` tiếng Việt.
- Vòng focus `ring-2 ring-ring ring-offset-2`, thấy được trên cả nền tối (thanh trên, toast).
- Radix lo bẫy focus, `Esc` và trả focus. Bảng giữ `<table>` thật, cột sắp xếp có `aria-sort`.
- Vùng chạm tối thiểu 40×40 trên mobile.
- Soát ở 390 / 768 / 1440: không tràn ngang, không chữ bị cắt, menu và bảng đổi dạng đúng như mô tả.

## 13b. Luật cũ phải đổi (phase 0, agy viết)
- `web/CLAUDE.md`: thay mục "Giao diện và Style" bằng shadcn/ui + Tailwind v4, lớp `--ui-*`, Inter, quy tắc
  "không trộn CSS Module với Tailwind trong một component", và bỏ "không bóng tĩnh" cho phần ứng dụng.
- Skill `design-system` (`.claude/skills/design-system/` → symlink `.agents/skills/`): viết lại theo tài liệu này.
- `.taste-skill/direction.json` + các sheet: ghi chú đã thay bằng tài liệu này.
- `docs/adr/00xx-shadcn-tailwind.md`: lý do, phương án loại, rủi ro (preflight, trùng tên biến, bundle).
- `CLAUDE.md` gốc mục Skill QA: `design-taste-frontend` vẫn dùng khi review. Claude tự sửa phần này.

## 13c. Unit test đang bám CSS Module và component cũ
Nhiều test đọc thẳng `.module.css` hoặc grep tên class/component cũ. Ví dụ:
- `web/scripts/w3b3-web-a8.test.mjs` đọc `don-hang/page.module.css`.
- `w3b-web`, `w3a-web`, `w2b-web`, `audit-fixes`, `w3b2-web` đọc `adminShared.module.css` và grep `AdminDrawer`, `shared.mobileList`.
- `src/lib/site/site.test.mjs` khẳng định font luôn là "Archivo".

Chính sách:
- Test **logic** (hàm thuần, model dữ liệu, RBAC, kiểm tra input) giữ nguyên, không được sửa cho xanh.
- Test khẳng định **chi tiết CSS Module/tên class/tên component cũ** được viết lại để kiểm cùng bất biến trên mã mới.
  Ví dụ: cột hành động dính phải, xuống dòng ở 390px, ô chọn 100% chiều rộng trên mobile, ma trận 9 route quản trị.
  Bất biến nào không kiểm được bằng đọc mã nguồn thì chuyển thành bước Playwright trong tiêu chí §15. Không xoá test
  mà không thay thế. Mỗi test bị viết lại hoặc chuyển phải ghi vào báo cáo của phase.
- Sở hữu:
  - `web/scripts/*.test.mjs` và `tests/**` thuộc **swarm QA**. Swarm khu vực chỉ báo danh sách assertion vỡ
    (file:dòng + bất biến cần giữ), swarm QA viết lại. Như vậy tránh nhiều swarm cùng sửa một file test.
  - Test nằm cạnh mã (`src/**/*.test.mjs`) thuộc swarm sở hữu thư mục đó.

## 14. Lộ trình

| Phase | Nội dung | Chủ sở hữu thư mục | Điểm duyệt |
|---|---|---|---|
| 0. Nền | Đổi luật + ADR (agy). Mốc đo (Claude tự đo trước khi agy chạy). Cài phụ thuộc, `ui.css`, `--ui-*` (cả bộ màu tối của site), Inter, nạp 5 font site + `resolveSiteUiFontStack` → `--ui-font` (font site cũ `--site-font`/`SITE_FONT_LABELS` giữ Archivo tới phase 4, để site cũ không đổi hình). Component `ui/*` và pattern `app/*` (§5.5), `AppShell`, `AuthSplit`, ánh xạ theme site (§11.1). Trang duyệt `/design` (xem dưới) | `web/src/components/**`, `web/src/styles/**`, `web/src/lib/utils.ts`, `web/src/lib/site/theme.ts` (chỉ thêm) + `siteUi.test.mjs`, `web/src/lib/site/host.ts` (thêm `/design` vào tiền tố bảo vệ), `web/src/app/(design)/**`, `web/src/app/layout.tsx`, cấu hình, `package.json` | **Chủ dự án duyệt `/design` trên trình duyệt** trước khi làm tiếp |
| 1. Người bán | 16 trang + onboarding + `SellerShell` → `AppShell` | `src/app/(seller)/**`, `src/features/seller/**` | Playwright + so Figma `02`–`06` |
| 2. Quản trị | 9 trang + `AdminShell` → `AppShell` | `src/app/(admin)/**`, `src/features/admin/**` | |
| 3. Nền tảng + đăng nhập | `(shop)` + `features/platform` + `AuthForms` | `src/app/(shop)/**`, `src/features/platform/**`, `src/features/account/AuthForms.tsx` + `AuthForms.module.css` | |
| 4. Site gian hàng | 21 trang, 8 mẫu, widget; đổi `SITE_FONT_LABELS` + `resolveSiteFontStack` sang 5 font thật và sửa test font trong `site.test.mjs` | `src/app/(site)/**`, `src/features/{site,commerce,account,search,assistant,chat,product,checkout}/**` (trừ `AuthForms.*`), `src/context/**` | Playwright 8 mẫu × 3 khổ |
| 5. Dọn dẹp | Bật preflight, xoá token cũ, `globals.css` cũ, CSS Module, component cũ, Archivo. Cập nhật `web/CLAUDE.md`, `DESIGN_REPORT.md`, `FEATURE_MATRIX.md` | toàn `web/` | lint/test/build + `make e2e-web` |

- **Đóng băng `src/components/ui/` và `src/components/app/` sau phase 0.** Phase 0 thêm đủ mọi component ở §7 và mọi
  pattern ở §5.5. Phase 1–4 chạy chung một working tree, nên swarm khu vực không chạy `shadcn add` và không sửa hai thư
  mục này. Thiếu gì thì báo, một swarm duy nhất (do Claude chỉ định) bổ sung, rồi các swarm khác dùng.
- **Trang duyệt `/design`**: route group riêng `src/app/(design)/design/`, đặt ngoài `(admin)` để `AppShell` và
  `AuthSplit` hiện đúng kích thước toàn màn, và không đụng ma trận RBAC 9 route quản trị (`admin-rbac.ts` default-deny).
  - Server kiểm phiên: chỉ người có một trong 6 vai trò quản trị, còn lại `notFound()`. Gắn `robots: noindex`.
    Không có trong menu hay ⌘K.
  - Nội dung: mọi component ở trạng thái thường, hover, focus, tắt, lỗi; 9 kiểu trang dựng bằng dữ liệu mẫu ghi rõ
    "mẫu"; một khung site có `Select` đổi 8 mẫu (cả 2 mẫu tối).
  - Phase 5 xoá trang này, vì lúc đó các trang thật đã là bản tham chiếu.
- Phase 1–4 chạy song song sau khi duyệt phase 0, vì thư mục không giao nhau.
- `src/features/account/AuthForms.tsx` (form đăng nhập/đăng ký/quên/đặt lại) dùng chung cho host gốc và site
  gian hàng. File này **thuộc phase 3**, chuyển một lần. Phase 4 chỉ bọc trang đăng nhập của site bằng `AuthSplit`
  theo màu site, và không sửa `AuthForms.tsx`. Các file còn lại của `features/account` thuộc phase 4.
- Component cũ ở `src/components/*` (`Button`, `Price`, `ProductCard`, `StateView`, `SvgChart`…) **giữ nguyên tới
  phase 5**, để khu vực chưa chuyển không đổi hình. Khu vực đã chuyển dùng bản mới trong `components/ui` hoặc `components/app`.
  `Price` và `SvgChart` có bản Tailwind mới trong `components/app` ngay từ phase 0, bản cũ bị xoá ở phase 5.
- Phase 0 ghi **mốc đo trước khi đổi**: bảng kích thước JS từng route từ `next build`, và ảnh Playwright 390/768/1440
  của mỗi kiểu trang hiện tại. Lưu vào `.runs/shadcn-redesign/baseline/` để dùng cho tiêu chí 4 và 7 của §15.
- Chỉ swarm QA được chạy `make e2e-web` và rebuild container.

## 15. Tiêu chí nghiệm thu (mỗi phase)

1. `npm run lint`, `format:check`, `test:unit`, `check:data`, `build` (với `NEXT_DIST_DIR` riêng) xanh.
2. Trong phạm vi phase: không còn file `.module.css` và import từ component cũ đã bị thay. Kiểm bằng `rg`.
3. Playwright 390/768/1440 trên `next start` nối gateway audit: chụp mỗi kiểu trang. Không tràn ngang, đúng hành vi responsive §8–§11.
4. Khu vực ngoài phạm vi phase không đổi hình (chụp trước/sau so sánh).
5. Không mất chức năng: mọi nút, form, bộ lọc, phân trang, thao tác lô trước đây vẫn chạy (đi qua luồng chính trên trình duyệt thật).
6. Phase 4: lưu thử cả 8 mẫu từ Site Studio, mỗi mẫu đi trọn luồng xem sản phẩm → giỏ → thanh toán COD trên trình duyệt thật.
7. Phase 4 và 5: cột **First Load JS** của route trang chủ site và trang sản phẩm (bảng `next build`) không tăng quá
   15% so với mốc đo ở phase 0. Ngưỡng này ưu tiên hơn việc nạp sẵn mọi thứ: trên site, `CommandDialog` (tìm kiếm),
   `Sheet` (menu mobile, bộ lọc, giỏ) và `Toaster` nạp bằng `next/dynamic` khi cần. `Toaster` không đặt ở
   `src/app/layout.tsx` gốc mà ở layout từng khu vực.
8. Phase 5: `make e2e-web` xanh.

## 16. Để agy tự quyết (không cần hỏi)
- Chi tiết px nằm trong khuôn khổ §6 và đo được tương phản.
- Icon lucide cụ thể cho từng mục nav.
- Chia component `app/*` nhỏ hơn hay gộp lại, miễn tên và vai trò như §5.5.
- Nội dung dữ liệu mẫu của `/design`, miễn ghi rõ là mẫu.
- Cách tách route group cho 4 trang auth ở host gốc, miễn URL không đổi và redirect 308 vẫn chạy.

## 17. Quyết định của Claude (chủ dự án kiểm khi duyệt)
1. Phương án A cho site gian hàng (chủ dự án trả lời "continue" khi được hỏi A/B).
2. Inter cho phần ứng dụng, thay Archivo. Site dùng đúng font mẫu; Site Studio thêm ô chọn font.
3. Bỏ luật "không bóng tĩnh" và "không thư viện UI" cho phần ứng dụng. Site vẫn phẳng.
4. Màu chính của nút là slate tối như Figma. Teal NexusMart dành cho link, focus, mục đang chọn, tiến độ, biểu đồ.
5. Giữ `SvgChart` và `SellerTrendChart`, không thêm Recharts. Không TanStack Table, không react-hook-form.
6. Giữ mọi hành vi đang có: drawer xác nhận giỏ ở trang sản phẩm, drawer xem nhanh, sửa tồn trong bảng, thanh đáy
   mobile người bán, 4 tab tài khoản, bố cục `/ve-nexusmart`.
7. Hướng dẫn thiết lập: 3 bước bắt buộc + 1 tuỳ chọn, chỉ đánh dấu theo dữ liệu thật.
8. Trang duyệt `/design` đặt sau đăng nhập quản trị, ngoài khung quản trị, xoá ở phase 5.
9. Không chế độ tối cho người bán và quản trị.
10. 4 trang auth ở host gốc đứng riêng không có đầu/cuối trang như Figma. Trên site thì vẫn trong khung shop.
11. Site Studio gom 6 tab thành 5 chế độ, giữ đủ chức năng.
12. Test bám CSS Module được viết lại theo bất biến, do swarm QA sở hữu `web/scripts/` và `tests/`.
13. Ngân sách First Load JS +15% cho trang chủ site và trang sản phẩm, được ưu tiên hơn việc nạp sẵn component.
