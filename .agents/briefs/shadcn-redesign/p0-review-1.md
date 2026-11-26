# Phase 0 — review vòng 1 (Claude, 26/11/2026)

Gate của bạn xanh: lint, format, 503 test, check:data, build. Mình đã chụp lại 5 trang cũ và so pixel với mốc trước khi
đổi (`.runs/shadcn-redesign/baseline/shots`). Seller, admin, trang chủ site và mọi ảnh focus **giống hệt**.
Còn các lỗi dưới đây. Sửa hết trong cùng phạm vi file của `p0-foundation.md`, vẫn cấm đụng file của phiên trang chủ.

## 1. [Lỗi thật] Danh sách trên PDP bị thụt lề sau khi bọc layer
- Kịch bản: site `bach-dang`, PDP `ao-thun-cotton-compact-trang`. Danh sách đặc điểm (`<ul className={styles.pdpDetailList}>`,
  `src/features/site/SitePdpView.tsx:610`) trước sát lề trái, giờ thụt ~18px.
- Nguyên nhân: rule cũ `ul[class], ol[class] { padding: 0; list-style: none }` có độ đặc hiệu (0,1,1), từng thắng
  `.pdpDetailList { padding-left: 18px }` (0,1,0) trong `site.module.css:1344`. Vào `@layer base` thì nó thua.
- Luật đúng: **chỉ rule có độ đặc hiệu ≤ (0,0,1)** (chỉ phần tử, `*`, `::pseudo-element`) mới được vào `@layer base`.
  Rule nào có class, thuộc tính hay pseudo-class thì phải ở ngoài layer, đúng chỗ cũ.
- Sửa `src/styles/globals.css`: đưa **ra khỏi layer**, về vị trí và nội dung như ở `HEAD`, ba khối:
  `ul[class], ol[class]` (dòng ~85), `:focus-visible` (dòng ~130), `tbody tr:last-child td` (dòng ~249, độ đặc hiệu
  (0,1,3)). Các khối phần tử thuần khác giữ trong layer.
- Để vùng `UiScope` không bị ba rule cũ (ngoài layer) đè, thêm vào `src/styles/ui.css`, **ngoài mọi `@layer`**:
  ```css
  [data-ui="true"] :is(ul, ol)[class] { padding: revert-layer; list-style: revert-layer; }
  [data-ui="true"] :focus-visible { outline: revert-layer; outline-offset: revert-layer; }
  [data-ui="true"] tbody tr:last-child td { border-bottom: revert-layer; }
  ```
  `revert-layer` trong rule ngoài layer trả quyền quyết định về các layer, tức base reset của `ui.css` hoặc utility Tailwind.
- Test, thêm vào `src/styles/ui-vars.test.mjs`:
  - `test("globals @layer base only holds element-level selectors")`: mọi selector bên trong các khối `@layer base {…}`
    của `globals.css` không chứa `.`, `[`, hay `:` đơn (chỉ được `::`).
  - `test("ui.css has unlayered revert-layer overrides for legacy unlayered rules")`: ba rule trên có mặt và nằm ngoài
    mọi khối `@layer`.

## 2. [Hồi quy CSS] Mất autoprefixer của Next
- Có `postcss.config.mjs` riêng thì Next bỏ PostCSS mặc định. So CSS biên dịch với mốc: `-webkit-sticky` 26 → 0,
  `-webkit-clip-path` 25 → 0, `-webkit-user-select` 7 → 1. Safari vẫn cần `-webkit-user-select`.
- Sửa: thêm **devDependency** `autoprefixer` (`npm install -D autoprefixer`). `postcss.config.mjs`:
  ```js
  export default {
    plugins: {
      "@tailwindcss/postcss": {},
      // Next drops its built-in autoprefixer once a PostCSS config exists; keep legacy CSS output identical.
      // Targets = Next's default MODERN_BROWSERSLIST_TARGET. Remove in phase 5 with the CSS Modules.
      autoprefixer: { flexbox: "no-2009", overrideBrowserslist: ["chrome 64", "edge 79", "firefox 67", "opera 51", "safari 12"] },
    },
  };
  ```
  **Không** dùng `postcss-preset-env`: nó polyfill `@layer` với danh sách trình duyệt này.
- Kiểm sau build: `cat .next-agy/static/css/*.css | grep -oE -- "-webkit-(sticky|clip-path|user-select)" | sort | uniq -c`
  phải ra khoảng 26 / 25 / ≥7.

## 3. [Chi phí] `ui.css` đang tải trên MỌI trang cũ (+15 KB gzip CSS chặn render, cả site gian hàng)
- Sửa:
  - Bỏ `import "@/styles/ui.css"` khỏi `src/app/layout.tsx` (dòng 8). Chuyển sang
    `src/app/(design)/design/layout.tsx`; khu vực nào chuyển đổi ở phase 1–4 thì layout của khu vực đó tự import.
  - Thêm dòng đầu tiên của `src/styles/globals.css`: `@layer theme, base, components, utilities;`, để thứ tự layer cố
    định kể cả khi `ui.css` nạp sau.
  - `src/lib/fonts.ts`: thêm `preload: false` cho `Inter`. Hiện nó preload trên mọi trang cũ dù không dùng. Giữ class
    biến font trên `<html>`, vì `:root { --ui-font: var(--font-inter) }` cần biến đó ở gốc.
- Test (`ui-vars.test.mjs`): `src/app/layout.tsx` không chứa `ui.css`; `src/app/(design)/design/layout.tsx` có import
  `@/styles/ui.css`.
- Kiểm sau build: CSS của route `/s/[host]` không còn chứa `--ui-background`.

## 4. [UX] AppShell coi mọi mục có query là đang chọn
- Kịch bản: `/design/app?kieu=a1`. Cả 11 mục menu trái đều nền trắng có bóng, vì `isPathActive`
  (`src/components/app/AppShell.tsx:97`) bỏ query: mọi href `/design/app?kieu=…` đều khớp `/design/app`. Menu thật của
  người bán cũng sẽ gặp lỗi này nếu mục con dùng query (ví dụ tab tồn kho).
- Sửa: tách `isPathActive` thành hàm thuần trong `src/components/app/navMatch.ts`:
  `isNavItemActive(pathname: string, search: string, href: string, homeHref: string, match?: "exact" | "prefix"): boolean`.
  - href có query: chỉ active khi path khớp (theo `match`) **và** mọi cặp key=value trong query của href có trong `search`.
  - href không query: giữ nguyên hành vi cũ.
  - `AppShell` lấy `search` bằng `useSearchParams()`.
  - Mục cha active khi một mục con active (đã có).
- Test `src/components/app/navMatch.test.mjs`:
  - `("/seller","", "/seller","/seller")` → true;
  - `("/seller/don-hang","", "/seller","/seller")` → false (home là exact);
  - `("/seller/don-hang/123","", "/seller/don-hang","/seller")` → true (prefix);
  - `("/design/app","?kieu=a1", "/design/app?kieu=a1","/x")` → true;
  - `("/design/app","?kieu=a2", "/design/app?kieu=a1","/x")` → false;
  - `("/seller/san-pham","?view=ton-kho&page=2", "/seller/san-pham?view=ton-kho","/seller")` → true;
  - `("/seller/san-pham","", "/seller/san-pham?view=ton-kho","/seller")` → false.
- Mẫu ở `/design/app`: mục "Sản phẩm & Kho hàng" (href `/design/app`, không query) đổi thành `match: "exact"` hoặc một
  href riêng, để không sáng cùng lúc với mọi kiểu.

## 5. [Chữ tiếng Việt dài, 390px] Tiêu đề bước SetupGuide bị cắt mất nghĩa
- Kịch bản: `/design/app?kieu=a9` ở 390px. Bước tuỳ chọn hiện "Gắn …", vì 2 nhãn "Tuỳ chọn" + "Chưa kiểm tra được" chiếm
  hết hàng. "Thêm tài khoản ngân hà…" cũng bị cắt.
- Sửa `src/components/app/SetupGuide.tsx:145`: tiêu đề `line-clamp-2` thay cho `truncate`; nhóm nhãn `flex-wrap` và
  xuống dòng dưới tiêu đề khi hẹp (< 640px).

## 6. Tài liệu
- `.claude/rules/frontend.md` dòng 12 còn bảo đối chiếu "art direction ở `.taste-skill/`". Đổi thành đối chiếu skill
  `design-system` + `.agents/briefs/shadcn-redesign/design.md`, chụp 3 khổ 390/768/1440. Giữ dòng ngoại lệ trang chủ.
- `web/CLAUDE.md` (mục Giao diện và Style), `.claude/skills/design-system/SKILL.md`, `docs/adr/0011-shadcn-ui-tailwind-v4.md`,
  ghi đúng cách làm mới:
  - (a) `UiScope` xuất `data-ui="true"`; mã cũ đã dùng `data-ui` với giá trị khác, nên selector mới luôn là `[data-ui="true"]`;
  - (b) `ui.css` import ở layout của từng khu vực đã chuyển, không ở root;
  - (c) luật layer: chỉ rule ≤ (0,0,1) của `globals.css` vào `@layer base`, ba rule cũ ngoài layer có override `revert-layer`;
  - (d) `autoprefixer` là devDependency giữ CSS cũ như trước, gỡ ở phase 5.

## Kiểm cuối (giữ nguyên Task 8)
`npm run lint && npm run format:check && npm run test:unit && npm run check:data && NEXT_DIST_DIR=.next-agy npm run build`,
rồi `git checkout -- tsconfig.json next-env.d.ts`, chạy các lệnh kiểm ở mục 2 và 3, xoá `.next-agy`.
Báo cáo: file đã sửa theo mục, kết quả lệnh (dòng tổng kết), số tiền tố ở mục 2, chỗ làm khác và lý do.
