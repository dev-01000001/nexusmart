# F – Hướng dẫn bàn giao nền tảng thiết kế (cho mảng P, S, C, L, D)

Font duy nhất toàn hệ thống: **Archivo** (variable `wdth` + `wght`, nạp một lần ở `web/src/app/layout.tsx`, biến `--font-archivo`, stack `--font-sans: var(--font-archivo), system-ui, sans-serif`). `resolveSiteFontStack()` luôn trả Archivo. Đã bỏ `experimental.viewTransition` vì Next.js 15.5.29 đóng gói React nội bộ không export `ViewTransition` (gây cảnh báo `Attempted import error` khi build); dùng chuyển trang 320ms chỉ `opacity` qua `template.tsx` (`PageTransition`) ở cả 4 route group (không dùng `transform` để không làm nhảy phần tử `position: fixed`).

---

## 1. Token (`web/src/styles/tokens.css`)

- **Bậc nền**: `--bg-0` (`#fff`), `--bg-1` / `--bg` (`#f7f9fa`), `--bg-2` / `--surface-subtle` (`#eff3f5`), `--bg-3` (`#e4eaee`), `--bg-4` (`#d6dfe4`), `--surface` (`#ecf0f2`).
  Ví dụ: `background: var(--bg-2);`
- **Chữ & điểm nhấn**: `--ink-1` (`#152229`), `--ink-2` / `--ink` (`#1f3038`), `--ink-3` (`#495a63`), `--muted` (`#52626a`), `--accent` (`#0d7480`), `--accent-hover` (`#0a5f69`), `--accent-ink` (`#fff`), `--accent-tint` (`#e4f1f3`). Cửa hàng vẫn dùng `--site-*` từ `buildSiteThemeVars()`.
  Ví dụ: `color: var(--ink-1);`
- **Thang chữ trang công khai** (sàn 13px, thân bài ≥ 16px): `--step-small`, `--step-body`, `--step-lead`, `--step-head`, `--step-display`.
  Ví dụ: `font-size: var(--step-head);`
- **Thang chữ app (seller/admin)** (sàn 13px): `--fs-small` (13px), `--fs-body` (14px), `--fs-section` (16px), `--fs-title` (24px), `--fs-kpi` (28px).
  Ví dụ: `font-size: var(--fs-kpi);`
- **Khoảng cách & bo góc**: `--space-1`..`--space-10` (4→112px), `--margin`, `--gutter`, `--section`; `--radius-shop` (0px), `--radius-pill` (9999px), `--radius-card` (8px), `--radius-control` (6px), `--radius-sheet` (16px).
  Ví dụ: `padding: var(--space-6); border-radius: var(--radius-card);`
- **Chuyển động**: `--ease-out`, `--ease-expo`, `--ease-in-out`, `--ease-exit`; `--t-press` (140ms), `--t-hover` (200ms), `--t-ui` (280ms), `--t-page` (320ms), `--t-reveal` (800ms), `--t-open` (1200ms), `--stagger` (80ms).
  Ví dụ: `transition: transform var(--t-hover) var(--ease-expo);`

---

## 2. Class giọng chữ (`web/src/styles/globals.css`)

Dùng `font-stretch` + `font-weight` (có thể ghi đè `font-weight: 400` cho `luxury-maison`), `line-height` ≥ 1.04 bảo vệ dấu tiếng Việt hai tầng.

- `.type-wide` (`wdth 118%`, `weight 650`, `lh 1.08`): `<h1 className="type-wide">Cửa hàng mang tên bạn</h1>`
- `.type-normal` (`wdth 100%`, `weight 560`, `lh 1.16`): `<h2 className="type-normal">Sản phẩm nổi bật</h2>`
- `.type-tight` (`wdth 80%`, `weight 680`, `lh 1.06`): `<h2 className="type-tight">Xưởng Dệt Bạch Đằng</h2>`
- `.type-poster` (`wdth 66%`, `weight 800`, `lh 1.04`): `<p className="type-poster">404</p>`
- `.num` (`font-variant-numeric: tabular-nums`): `<span className="num">199.000₫</span>`

---

## 3. Recipe chuyển động (`data-reveal` & `data-scroll`)

Một `<RevealObserver />` duy nhất đã gắn sẵn ở `web/src/app/layout.tsx` (dùng 1 `IntersectionObserver` `threshold: 0` + 1 `MutationObserver` toàn cây, đánh dấu bằng thuộc tính `data-shown` sau khi React hydrate xong node, tự nhận nút mới khi lọc/đổi tab/chuyển trang). **Các trang chỉ cần gắn thuộc tính `data-reveal`, không cần gọi hook hay component reveal nào thêm.**

- `data-reveal="lines"` — tiêu đề lên từng dòng trong mặt nạ (`padding-block: 0.14em` chống cắt dấu):
  `<h2 data-reveal="lines"><span className="line"><span data-line="" style={{ "--i": 0 } as CSSProperties}>Cửa hàng Việt</span></span></h2>`
- `data-reveal="clip"` — mở ảnh/video/khối màu một lần bằng `clip-path`:
  `<figure data-reveal="clip"><img src="/banners/vietnam-workshop.jpg" alt="..." /></figure>`
- `data-reveal="rows"` — danh sách/hàng bảng vào so le 80ms theo thứ tự con (tự đặt `--i` đến 12 hàng):
  `<ul data-reveal="rows"><li>Hàng 1</li><li>Hàng 2</li></ul>`
- `data-reveal="scale"` — khung tiêu điểm mở nhẹ `0.96 → 1`:
  `<section data-reveal="scale">...</section>`
- `data-reveal="count"` — chạy số một lần khi vào khung nhìn (HTML gốc giữ nguyên giá trị cuối khi tắt JS):
  `<strong className="num" data-reveal="count" data-count-to="1250000" data-count-format="vnd">1.250.000₫</strong>`
- `data-rise` / `.lines [data-line]` — vào ngay màn đầu khi tải trang (tương thích bản cũ):
  `<p data-rise="" style={{ "--i": 1 } as CSSProperties}>Một câu ngắn.</p>`

**Hiệu ứng gắn thẳng vào tiến độ cuộn (`@supports (animation-timeline: view())`, trạng thái cuối là mặc định, chỉ dùng khi gọi tên)**:
- `data-scroll="open"` — mở ảnh/video từ khung nhỏ ra đầy khung theo cuộn (`clip-path`):
  `<div data-scroll="open"><video src="/atmosphere/loom.mp4" /></div>`
- `data-scroll="drift"` — ảnh trôi nhẹ theo cuộn (≤ 6%, đặt `overflow: hidden` ở khung cha):
  `<img data-scroll="drift" src="/banners/vietnam-workshop.jpg" alt="..." />`

---

## 4. Component dùng chung (`import { ... } from "@/components"`)

- **`Button`**: `variant` (`primary|ink|secondary|outline|ghost|danger`), `size` (`sm|md|lg`), `shape` (`square|pill|app`), `loading` (thanh trượt phẳng, giữ nguyên bề rộng), `href`. Nhãn chuỗi tự trượt khi hover.
  `<Button variant="primary" shape="pill" loading={saving}>Mở gian hàng</Button>`
- **`Price`**: hiển thị số nguyên VND dạng `tabular-nums`, `size` (`sm|md|lg`), `tone` (`default|accent|inverted`), `showDiscount`.
  `<Price amount={199000} originalAmount={259000} size="md" showDiscount />`
- **`StateView`**: `kind` (`loading|empty|error`), `surface` (`shop|app`), `skeletonVariant` (`table|cards|detail`), tối đa 1 câu + 1 nút, skeleton phẳng không gradient/spinner.
  `<StateView kind="empty" surface="app" title="Chưa có đơn hàng." actionLabel="Tạo đơn" actionHref="/seller/don-hang" />`
- **`SvgChart`** (`LineChart`, `BarChart`, `BarList`, `ProgressBar`, `RingChart`): trục & nhãn ≥ 13px, hover có đường dóng + tooltip, đường và vùng tô mở đồng bộ một lần qua cùng `clipPath`.
  `<LineChart labels={days} series={[{ name: "Doanh thu", values: gmv }]} formatValue={formatVnd} showArea />`
- **`Icons`**: bộ icon nét `1.5px` đồng nhất (`IconSearch`, `IconBag`, `IconFilter`, `IconCommand`, `IconArrowRight`, `IconPause`…, `NexusMark`).
  `<IconSearch size={16} />`
- **`ProductCard`**: ảnh 3:4 (hover đổi ảnh thứ hai + `scale(1.035)`), nút yêu thích trong suốt (không ô nền trắng), tên 1 dòng, giá, chỉ báo `Hết hàng`/`Còn N` khi `N ≤ 5`; khay thêm nhanh chỉ hiện khi hover/focus ở thiết bị có hover, không có nút dưới thẻ.
  `<ProductCard product={product} />`
- **`PageTransition`**: khung chuyển trang 320ms chỉ `opacity` dùng trong `template.tsx` (tự động có ở `(shop)`, `(seller)`, `(admin)`, `(site)`).
  `<PageTransition>{children}</PageTransition>`
- **`modalFocus`** (`hasOpenModal`, `getFocusableElements`, `useModalFocusTrap`, `UseModalFocusTrapOptions` từ `@/components/modalFocus` hoặc `@/components`): ngăn xếp focus trap + phím `Esc`/`Tab` dùng chung cho drawer/dialog/palette (dùng cho Admin và `SellerDrawer`).
  `useModalFocusTrap({ open, containerRef: panelRef, onEscape: onClose });`
