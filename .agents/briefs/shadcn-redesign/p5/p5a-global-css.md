# P5a — CSS toàn cục: preflight, bỏ `globals.css` + `tokens.css`, bỏ Archivo, dọn PostCSS

Đọc trước: `common.md` (phase 5), `../design.md` §5.1–§5.3, `web/src/styles/{ui.css,globals.css,tokens.css}`,
`web/src/components/ui/ui-scope.tsx`, `web/postcss.config.mjs`.

Hiện trạng: `app/layout.tsx` nạp `tokens.css` + `globals.css` cho mọi trang và font Archivo. `ui.css` (nạp ở layout từng khu) có
Tailwind không preflight, một bản reset riêng chỉ áp trong `[data-ui="true"]` (do `UiScope` gắn), và vài override
`revert-layer` để thắng rule của `globals.css`. Trang chủ nền tảng nằm ngoài `UiScope`, dùng biến `--home-*` trong `tokens.css`.

## Làm theo 3 bước, so pixel sau mỗi bước (`common.md` §3), ghi kết quả vào progress

### Bước 1 — Đổi reset
- Bật preflight toàn cục: `@import "tailwindcss/preflight.css" layer(base);` trong `ui.css`, nạp `ui.css` ở `app/layout.tsx`
  (bỏ import trùng ở layout từng khu nếu không còn cần).
- Bỏ bản reset `[data-ui="true"] :where(...)` và các override `revert-layer` trong `ui.css`. Rule gốc của khu ứng dụng
  (font, màu chữ, nền) giữ lại được dưới `[data-ui="true"]` hoặc chuyển thành class; miễn không còn reset bám `data-ui`.
- **Xoá `globals.css`.** Chuyển sang `ui.css` (hoặc một file CSS mới trong `web/src/styles/`) đúng những rule còn được dùng:
  nền `html/body` của site mẫu tối (`html:has([data-site-root][data-site-theme-mode="dark"])…`), CSS của Reveal
  (`[data-reveal]`, `.js …`, lớp chuyển trang của `PageTransition`), `:focus-visible` mặc định, nền + màu chữ + font của `html/body`
  sao cho trang nào cũng hiện như cũ. Class `.num` (5 file) đổi sang `tabular-nums` (xem nó làm gì ở `globals.css` trước khi đổi);
  `sr-only` dùng utility của Tailwind (kiểm tương đương). Bỏ hẳn rule không còn ai dùng (`.type-*`, `.shop-*`…; `rg` để chứng minh).
- So pixel đủ 6 khu + trang chủ + `states.mjs` (khi có). Ghi `signals/p5a-step1.done`.

### Bước 2 — Bỏ token cũ và Archivo
- **Xoá `tokens.css`.** Biến `--home-*` chỉ trang chủ dùng: giữ ở phạm vi cây trang chủ (đặt bằng `style` trên phần tử gốc ở
  `(shop)/(public)/layout.tsx` như `--home-font`, hoặc một file chỉ khai biến trên selector gốc trang chủ trong
  `features/platform/home/`). Biến khác còn dùng (`--space-*`, `--radius-pill`, `--t-hover`…, xem danh sách file bên dưới):
  thay bằng giá trị Tailwind / `--ui-*` có **cùng giá trị tính ra**, không làm tròn.
- `@theme inline` trong `ui.css`: dọn phần khai riêng để né trùng tên token cũ (`--font-sans`, `--ease-out`, `--ease-in-out`)
  nếu không còn cần; utility phải ra đúng giá trị như cũ.
- **Bỏ Archivo:** `app/layout.tsx` (`next/font` Archivo, `--font-archivo`), `lib/fonts.ts` nếu có, `SITE_FONT_STACK` trong
  `lib/site/theme.ts` (xoá nếu không còn ai dùng ngoài test, sửa test `site.test.mjs:963` cho khớp).
- So pixel lại. Ghi `signals/p5a-step2.done`.

### Bước 3 — PostCSS (chỉ bắt đầu khi có `signals/p5b.done`: lúc đó không còn CSS Module)
- Bỏ hai plugin Next thêm lại cho CSS cũ (`postcss-flexbugs-fixes`, `postcss-preset-env`) khỏi `web/postcss.config.mjs` và
  ghi chú cũ trong file. So pixel lại.
- **Thử riêng** `"@tailwindcss/postcss": { optimize: false }`: hiện bản production lệch trang chủ ≤ 6/255 so với 43101 vì
  Lightning CSS làm tròn alpha (`rgb(2 9 10 / 0.62)` → `#02090a9e`). Cần bản production để đo: build bằng
  `.agents/tools/snap-build.sh 43125 <các file bạn đã đổi + file của P5b>` (chạy nền; xong thì tắt), chụp
  `scroll-shots.mjs` trên 43125 và 43101, `ui-diff.sh`. Giữ `optimize: false` chỉ khi trang chủ về 0 (hoặc nhỏ hơn rõ) và các khu
  khác trên 43125 so 43102 chỉ lệch loại (b). Ghi kích thước CSS trước/sau. Không đạt thì bỏ thử nghiệm, ghi lại.
- Ghi `signals/p5a.done`.

## `UiScope`
Không gỡ component và không gỡ wrapper ở phase này (nó còn giữ portal container cho Radix). Chỉ bỏ CSS reset bám `[data-ui]`.
Bỏ thuộc tính `data-ui` thì được, không bắt buộc.

## Phạm vi ghi
- `web/src/app/layout.tsx`, `web/src/styles/**` (gồm xoá `globals.css`, `tokens.css`; sửa `ui-vars.test.mjs` theo luật test),
  `web/postcss.config.mjs` (bước 3), `web/src/lib/fonts.ts`, `web/src/lib/site/theme.ts` (chỉ font) + `web/src/lib/site/site.test.mjs`
  (chỉ assertion font), `web/src/components/ui/ui-scope.tsx`, `web/src/components/{Reveal,PageTransition,Icons}.tsx`,
  `web/src/components/ui/toggle-group.tsx`, `web/src/components/app/{AppShell,AuthSplit}.tsx` (chỉ phần CSS/`data-ui`).
- Layout khu: `web/src/app/{(admin),(seller),(shop),(site)}/layout.tsx`, `web/src/app/(shop)/template.tsx`, `web/src/app/(site)/error.tsx`.
- Trang chủ + nền tảng: `web/src/app/(shop)/(public)/layout.tsx`, `(shop)/(public)/{tro-giup,ve-nexusmart}/page.tsx`,
  `web/src/features/platform/{PlatformHeader,PlatformFooter,PlatformHome,SiteDirectoryDoors,SiteDirectorySection,StorefrontStageMini,SubdomainPreview,VietQrSvg,NotFoundView}.tsx`,
  `web/src/features/platform/home/**`.
- Site shell: `web/src/features/site/SiteShell.tsx`, `web/src/features/site/shell/{SiteThemeRoot.tsx,siteStyleVars.ts}`.
- Khác: `web/src/features/commerce/cartEnrich.ts` (token), `web/src/features/seller/site/SectionEditorPanel.tsx` (chỉ `.num`),
  `web/src/features/seller/sellerDedupe.test.mjs` (chỉ mock `UiScope` nếu bạn đổi nó).
- **Cấm:** mọi file trong phạm vi P5b (`p5b-dead-code.md`), `web/src/app/(design)/**`, `tests/**`, `web/scripts/**`.

## Xong khi
- `rg -n "globals\.css|tokens\.css|archivo|Archivo|revert-layer" web/src` chỉ còn chỗ có lý do ghi trong báo cáo (vd. tên font trong test
  của file khác).
- Lệnh kiểm `common.md` §1 xanh toàn app; `ui-vars.test.mjs` viết lại theo cấu trúc CSS mới (bất biến: không trùng tên biến với
  Tailwind/shadcn, `--ui-*` đủ bộ, `@layer` đúng thứ tự, preflight nằm trong `layer(base)`).
- Bảng diff 3 bước theo `common.md` §4, không còn loại (c). Trang chủ so 43101 không tệ hơn trước phase 5.
- Luồng `run.mjs` 4 khu xanh trên 43110.
