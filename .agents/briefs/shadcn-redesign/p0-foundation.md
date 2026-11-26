# Phase 0 — nền shadcn/ui + Tailwind v4 + trang duyệt `/design` (Implementation Plan / brief cho agy)

> Người làm: **agy, một phiên** (`./scripts/agy.sh`). Người giao và review: Claude Code. agy **không commit, không push**,
> không chạy `make up|seed|e2e*`, không docker. Làm lần lượt Task 1 → 8. Xong thì báo cáo theo mục "Báo cáo khi xong".

**Goal:** Dựng nền dùng chung cho đợt làm lại toàn web: Tailwind v4 (không preflight), shadcn/ui, lớp biến `--ui-*`,
font, pattern ứng dụng, và trang duyệt `/design`. **Không trang cũ nào được đổi hình.**

**Architecture:** Tailwind v4 nạp theme + utilities (không preflight) qua `src/styles/ui.css`. shadcn đọc màu qua tên
Tailwind chuẩn, ánh xạ (`@theme inline`) sang biến thô `--ui-*`, nên không đè token cũ trong `tokens.css`. Vùng đã chuyển
nằm trong `UiScope` (`data-ui` + reset thu hẹp + nơi chứa portal Radix), nên trang CSS Module cũ không bị ảnh hưởng.

**Tech Stack:** Next.js 15 App Router, React 19, TypeScript strict, Tailwind CSS v4, shadcn/ui (style `new-york`,
`radix-ui`), lucide-react, cmdk, sonner, tw-animate-css, `node --test` cho unit test (`*.test.mjs`).

**Spec:** `.agents/briefs/shadcn-redesign/design.md` (đã được chủ dự án duyệt 26/11/2026).
**Đọc trước khi code:** spec §5, §6, §7, §8, §9, §11.1, §13b, §14. Ảnh Figma ở `.agents/briefs/shadcn-redesign/figma/`.
Quy tắc chung: `AGENTS.md`, `web/CLAUDE.md` (Task 1 sẽ sửa file này).

## Global Constraints

- Phụ thuộc runtime thêm **chỉ**: `tailwindcss`, `@tailwindcss/postcss`, `tw-animate-css`, `class-variance-authority`,
  `clsx`, `tailwind-merge`, `radix-ui`, `lucide-react`, `cmdk`, `sonner`. Cấm: `next-themes`, `recharts`,
  `@tanstack/react-table`, `react-hook-form`, `zod`, `react-day-picker`, `vaul`, `input-otp`.
- **Không preflight**: `ui.css` không được chứa `@import "tailwindcss";` (bản đầy đủ) hay `tailwindcss/preflight`.
- Biến thô luôn tên `--ui-*`. Không khai `--accent`, `--muted`, `--border`, `--card`, `--chart-*`, `--sidebar*`,
  `--radius`, `--background`… ở `:root`.
- Một component **không** vừa dùng class Tailwind vừa dùng CSS Module.
- `data-ui` / `UiScope` **chỉ** dùng trong `src/components/**` mới và `src/app/(design)/**`. Không gắn vào trang hay
  layout cũ.
- Chữ hiển thị là tiếng Việt, thương hiệu NexusMart. Không chữ/logo/icon/ảnh Shopify, không chữ tiếng Anh mẫu từ Figma.
  Dữ liệu mẫu ở `/design` ghi rõ "mẫu".
- Tiền: số nguyên VND, hiển thị bằng `formatVnd` (`src/lib/format.ts`).
- Module được `node --test` import phải dùng import tương đối **có đuôi `.ts`** (không dùng alias `@/`), giống
  `src/lib/site/theme.ts`.
- Màu, cỡ chữ, bo góc, bóng lấy **đúng giá trị** ở spec §6.1–§6.3 (đã đo tương phản).

**Phạm vi file được sửa/tạo** (ngoài danh sách này là cấm):
`web/package.json`, `web/package-lock.json`, `web/postcss.config.mjs` (mới), `web/components.json` (mới),
`web/src/components/ui/**` (mới), `web/src/components/app/**` (mới), `web/src/hooks/**` (mới),
`web/src/lib/utils.ts` (mới), `web/src/lib/fonts.ts` (mới), `web/src/lib/site/theme.ts` (chỉ **thêm**, không đổi
hàm cũ), `web/src/lib/site/host.ts` (chỉ thêm `/design` vào `ROOT_PROTECTED_PREFIXES`), `web/src/lib/site/siteUi.test.mjs` (mới),
`web/src/styles/ui.css` (mới), `web/src/styles/ui-vars.test.mjs` (mới), `web/src/styles/globals.css` (chỉ bọc layer,
Task 2), `web/src/app/layout.tsx`, `web/src/app/(design)/**` (mới), `web/CLAUDE.md` và `.claude/rules/frontend.md`
(chỉ Task 8 bước 0), `.claude/skills/design-system/SKILL.md`, `.taste-skill/SUPERSEDED.md` (mới), `docs/adr/0011-shadcn-ui-tailwind-v4.md`
(mới), `docs/adr/README.md`.

**Cấm sửa:** `tokens.css`, mọi `*.module.css`, `src/components/*.tsx` cũ (`Button`, `Price`, `SvgChart`, `Icons`,
`StateView`…; được **import** `NexusMark` từ `Icons.tsx`), `src/features/**`, `src/app/(shop|site|seller|admin)/**`,
`src/context/**`, `web/scripts/**`, `tests/**`, `services/**`, `CLAUDE.md` gốc, `AGENTS.md`.

**Có một phiên agy khác đang chạy song song trên cùng working tree** (làm lại trang chủ). File của phiên đó: 
`web/src/features/platform/**`, `web/src/app/(shop)/**`, `web/src/styles/tokens.css`, `web/DESIGN_REPORT.md`,
`.agents/briefs/home-shopify-v2.md`, và vài dòng ngoại lệ "trang chủ" trong `web/CLAUDE.md`, `.claude/rules/frontend.md`.
Không đọc lại rồi "sửa giúp", không hoàn nguyên, không định dạng lại các file đó. **Cấm** `git checkout`, `git restore`,
`git reset`, `git stash`, `git clean`, `rm` trên bất kỳ file nào bạn không tự tạo hay tự sửa (ngoại lệ duy nhất:
`git checkout -- web/tsconfig.json web/next-env.d.ts` sau khi build). Nếu build/lint fail vì file của phiên kia thì
ghi vào báo cáo, không sửa.

## Review Focus

1. **Trang cũ đổi hình** do lớp CSS mới, gồm nền, font, khoảng cách, viền và vòng focus khi bấm Tab. Kỳ vọng: không đổi
   gì. Claude so ảnh trước/sau. agy giữ đúng Task 2 bước 6 (chỉ layer rule phần tử + `ol[class]` + `:focus-visible`) và không gắn `data-ui` ngoài phạm vi.
2. **Nội dung portal** (Dialog, Sheet, DropdownMenu, Select, Popover, Tooltip, Command) mở trong khung site mẫu tối phải
   nhận màu site: nền tối, chữ sáng, đúng bo góc. Kiểm bằng `/design/site` với `sport-active`. Task 5 bước 3 nối
   `usePortalContainer`.
3. **Chữ tiếng Việt dài** (tên shop dài, tên sản phẩm 2 dòng, nhãn nav dài) ở 390px không tràn ngang và cắt bằng `…`
   đúng chỗ. `/design/app` có dữ liệu mẫu tên dài (Task 7).
4. **Trạng thái lạ hoặc rỗng** truyền vào hàm tông nhãn: `null`, `""`, `"FOO"`, chữ thường. Kỳ vọng `neutral`, không
   lỗi. Test ở Task 6.
5. **Theme site hỏng hoặc thiếu**: màu không phải hex, `radius` lạ, `font_family` lạ, theme `null`. Kỳ vọng dùng giá trị
   mặc định, không ném lỗi, tương phản vẫn đạt. Test ở Task 4.

---

### Task 1: Đổi luật và ADR

**Files:**
- Modify: `.claude/skills/design-system/SKILL.md`: viết lại (giữ frontmatter `name: design-system`, sửa `description`).
  Tổng dung lượng < 20 KB (agy cắt skill > 24 KB).
- Create: `.taste-skill/SUPERSEDED.md`.
- Create: `docs/adr/0011-shadcn-ui-tailwind-v4.md`. Modify: `docs/adr/README.md` (thêm một dòng chỉ mục).

- [ ] **Step 1:** (Sửa `web/CLAUDE.md` và `.claude/rules/frontend.md` để **cuối cùng**, ở Task 8 bước 0, vì phiên
  trang chủ đang sửa hai file này.)
- [ ] **Step 2:** Viết lại `SKILL.md` của `design-system` theo spec:
  - bảng màu §6.1 (gồm bộ màu tối cho site), chữ §6.2, khoảng cách/bo/bóng §6.3;
  - danh mục component §7 và ánh xạ tông nhãn §7.1;
  - khung trang §8, 9 kiểu trang §9 (mỗi kiểu 2–4 dòng);
  - theme site §11.1;
  - luật `UiScope`/`data-ui`, đóng băng thư mục, quy trình Playwright.
  Không dán lại cả spec; dẫn link tới spec cho chi tiết.
- [ ] **Step 3:** `.taste-skill/SUPERSEDED.md`: 3–5 dòng, nói `direction.json` và các `*-sheet.md` là art direction cũ,
  đã được thay bằng `.agents/briefs/shadcn-redesign/design.md` từ 26/11/2026, chỉ giữ để tham khảo lịch sử.
- [ ] **Step 4:** ADR `0011-shadcn-ui-tailwind-v4.md` theo mẫu các ADR có sẵn (`docs/adr/0009-*.md`):
  - Trạng thái: "Đã duyệt — chủ dự án chọn shadcn/ui ngày 26/11/2026";
  - Bối cảnh;
  - Quyết định: shadcn/ui + Tailwind v4, lớp `--ui-*`, không preflight tới phase 5;
  - Phương án loại: antd, Shuffle, Polaris, kèm lý do theo spec §4;
  - Hệ quả và rủi ro: preflight, trùng tên biến, CLI shadcn tự ghi CSS, portal Radix, bundle site +15%;
  - Danh sách phụ thuộc.
  Thêm dòng vào `docs/adr/README.md`.
- [ ] **Step 5: Kiểm**
  Run: `rg -n "Không dùng Tailwind|shadow tĩnh" .claude/skills/design-system/SKILL.md; wc -c .claude/skills/design-system/SKILL.md`
  Expected: `rg` không ra dòng nào; `wc -c` < 20000.

### Task 2: Công cụ + lớp CSS nền (Tailwind v4 không preflight, `--ui-*`, reset thu hẹp)

**Files:**
- Modify: `web/package.json`, `web/package-lock.json` (qua `npm install`).
- Create: `web/postcss.config.mjs`, `web/components.json`, `web/src/lib/utils.ts`, `web/src/styles/ui.css`,
  `web/src/styles/ui-vars.test.mjs`.
- Modify: `web/src/styles/globals.css` (chỉ bọc layer), `web/src/app/layout.tsx` (import `ui.css`).

**Interfaces:**
- Produces: `cn(...inputs: ClassValue[]): string` trong `src/lib/utils.ts` (`clsx` + `tailwind-merge`).
- Produces: lớp màu Tailwind
  - shadcn chuẩn: `background`, `foreground`, `card`, `card-foreground`, `popover`, `popover-foreground`, `primary`,
    `primary-foreground`, `secondary`, `secondary-foreground`, `muted`, `muted-foreground`, `accent`, `accent-foreground`,
    `destructive`, `destructive-foreground`, `border`, `input`, `ring`;
  - riêng NexusMart: `brand`, `brand-foreground`, `topbar`, `topbar-foreground`, `topbar-input`, `topbar-placeholder`,
    `sidebar`, `sidebar-foreground`, `sidebar-muted`, `sidebar-accent`, `sidebar-accent-foreground`, `sidebar-border`,
    `sidebar-ring`, `sidebar-primary`, `sidebar-primary-foreground`;
  - 6 tông nhãn: `badge-{success|attention|warning|info|critical|neutral}-bg` và `badge-…-ink`.
- Produces: `shadow-card`, `shadow-button`, `shadow-overlay`; `rounded-sm|md|lg|xl` theo `--ui-radius`; `font-sans` →
  `var(--ui-font)`.

- [ ] **Step 1: Cài phụ thuộc** (trong `web/`):
  `npm install tailwindcss @tailwindcss/postcss tw-animate-css class-variance-authority clsx tailwind-merge radix-ui lucide-react cmdk sonner`.
  Kiểm `package.json`: không có gói nào ngoài danh sách Global Constraints.
- [ ] **Step 2:** `postcss.config.mjs`: `export default { plugins: { "@tailwindcss/postcss": {} } };`.
  `components.json`: `style: "new-york"`, `rsc: true`, `tsx: true`, `tailwind.css: "src/styles/ui.css"`,
  `tailwind.baseColor: "neutral"`, `tailwind.cssVariables: true`, `iconLibrary: "lucide"`, aliases `components: "@/components"`,
  `ui: "@/components/ui"`, `utils: "@/lib/utils"`, `hooks: "@/hooks"`, `lib: "@/lib"`.
  **Không chạy `shadcn init`**: nó ghi đè `ui.css` bằng `@import "tailwindcss"` đầy đủ và base layer toàn cục.
- [ ] **Step 3: Viết test trước** `src/styles/ui-vars.test.mjs` (`node:test` + `node:assert/strict`, đọc file bằng
  `fs.readFileSync(new URL(…, import.meta.url))`). Gom tên custom property **được khai** (regex `/(--[a-z0-9-]+)\s*:/g`) từ:
  - L = `tokens.css` + `globals.css` + `../features/site/site.module.css` + `../features/site/siteTemplates.module.css`;
  - T = `../../node_modules/tailwindcss/theme.css`;
  - U = `ui.css`.
  Các test:
  - `test("ui.css never redefines legacy tokens")`: `L ∩ U` chỉ chứa tên trong `HANDLED = ["--font-sans","--ease-out","--ease-in-out"]`.
  - `test("legacy/tailwind collisions are all handled in @theme inline")`: `L ∩ T` ⊆ `HANDLED`, và mỗi tên trong
    `HANDLED` xuất hiện **bên trong** khối `@theme inline { … }` của `ui.css`.
  - `test("ui.css has no preflight and no global base rules")`: `ui.css` không khớp `/@import\s+["']tailwindcss["']/`,
    không chứa `preflight`, và không có dòng khớp `/^\s*(\*|html|body)\s*[,{]/m`.
  - `test("keyframes names do not collide")`: tên `@keyframes` trong L không trùng tên `@keyframes` trong T,
    `../../node_modules/tw-animate-css/dist/tw-animate.css` (đường dẫn thật theo gói đã cài) và U.
  - `test("app palette matches spec")`: khối `:root` của `ui.css` có `--ui-background: #EFF3F5`,
    `--ui-primary: #1F3038`, `--ui-brand: #0D7480`, `--ui-input: #7D8E97`, `--ui-topbar: #152229`,
    `--ui-sidebar: #E6ECEF`, `--ui-radius: 0.75rem`, `--ui-font: var(--font-inter)` (so không phân biệt hoa thường).
- [ ] **Step 4: Chạy để thấy fail**
  Run: `cd web && node --test src/styles/ui-vars.test.mjs`
  Expected: FAIL (chưa có `ui.css`).
- [ ] **Step 5: Viết `src/styles/ui.css`** theo thứ tự:
  1. `@layer theme, base, components, utilities;` rồi `@import "tailwindcss/theme.css" layer(theme);`,
     `@import "tailwindcss/utilities.css" layer(utilities) source(none);`, `@import "tw-animate-css";`,
     `@source "../";`. `source(none)` tắt tự quét cả `web/` (tài liệu `.md`, script), `@source "../"` chỉ quét `src/`.
  2. `@custom-variant dark (&:where([data-site-theme-mode="dark"], [data-site-theme-mode="dark"] *));`
  3. `@theme inline { … }`:
     - `--color-<tên>: var(--ui-<tên>)` cho mọi màu ở Interfaces;
     - `--font-sans: var(--ui-font), ui-sans-serif, system-ui, sans-serif;`
     - `--ease-out: cubic-bezier(0, 0, 0.2, 1); --ease-in-out: cubic-bezier(0.4, 0, 0.2, 1);`
     - `--radius-sm: max(0px, calc(var(--ui-radius) - 6px)); --radius-md: max(0px, calc(var(--ui-radius) - 4px)); --radius-lg: var(--ui-radius); --radius-xl: var(--ui-radius);`
     - `--shadow-card: var(--ui-shadow-card); --shadow-button: var(--ui-shadow-button); --shadow-overlay: var(--ui-shadow-overlay);`
  4. `:root { … }`: đúng giá trị spec §6.1 và §6.3.
     - `--ui-background:#EFF3F5; --ui-foreground:#1F3038; --ui-card:#FFFFFF; --ui-card-foreground:#1F3038;`
       `--ui-popover:#FFFFFF; --ui-popover-foreground:#1F3038; --ui-primary:#1F3038; --ui-primary-foreground:#FFFFFF;`
       `--ui-secondary:#E4EAEE; --ui-secondary-foreground:#1F3038; --ui-muted:#EFF3F5; --ui-muted-foreground:#52626A;`
       `--ui-accent:#E4EAEE; --ui-accent-foreground:#152229; --ui-brand:#0D7480; --ui-brand-foreground:#FFFFFF;`
       `--ui-destructive:#B33A3A; --ui-destructive-foreground:#FFFFFF; --ui-border:#DDE4E8; --ui-input:#7D8E97;`
       `--ui-ring:#0D7480`.
     - `--ui-topbar:#152229; --ui-topbar-foreground:#D3DDE2; --ui-topbar-input:#293E48; --ui-topbar-placeholder:#9FB0B8;`
     - `--ui-sidebar:#E6ECEF; --ui-sidebar-foreground:#1F3038; --ui-sidebar-muted:#52626A; --ui-sidebar-accent:#FFFFFF;`
       `--ui-sidebar-accent-foreground:#152229; --ui-sidebar-border:#DDE4E8; --ui-sidebar-ring:#0D7480;`
       `--ui-sidebar-primary:#0D7480; --ui-sidebar-primary-foreground:#FFFFFF`.
     - 6 cặp nhãn sáng: success `#DDF3E6/#145338`, attention `#FBEFB5/#5C4A00`, warning `#FCDDB8/#6B3D00`,
       info `#DCEBFA/#0B4A7A`, critical `#F8E2E2/#8C2626`, neutral `#E6ECEF/#2C3E47`.
     - `--ui-radius:0.75rem; --ui-font:var(--font-inter);`
       `--ui-shadow-card: 0 1px 0 rgba(21,34,41,.07), 0 0 0 1px rgba(21,34,41,.06);`
       `--ui-shadow-button: inset 0 -1px 0 rgba(0,0,0,.45), inset 0 1px 0 rgba(255,255,255,.14);`
       `--ui-shadow-overlay: 0 8px 24px rgba(21,34,41,.16);`
  5. `[data-site-theme-mode="dark"] { … }`: chỉ đổi `--ui-destructive:#F07A7A` và 6 cặp nhãn tối (spec §6.1):
     success `#16352A/#8EE0B5`, attention `#3A3210/#F2DE7A`, warning `#3D2A12/#F5B97A`, info `#132C44/#8CC4F5`,
     critical `#3D1A1A/#F5A3A3`, neutral `#262C33/#D3DDE2`.
  6. `@layer base { … }`: reset **thu hẹp trong `[data-ui]`**, dạng `[data-ui] :where(…)`: độ đặc hiệu **(0,1,0)**,
     đủ thắng rule phần tử cũ (`button`, `a`, `th`… (0,0,1)) cùng nằm trong layer `base`, nhưng thua mọi utility Tailwind.
     **Không** viết thành `:where([data-ui] …)` (độ đặc hiệu 0, rule cũ sẽ thắng lại):
     - `[data-ui]{font-family:var(--ui-font),ui-sans-serif,system-ui,sans-serif;color:var(--ui-foreground);-webkit-font-smoothing:antialiased}`
     - `[data-ui] :where(*,::before,::after){box-sizing:border-box;border:0 solid var(--ui-border)}`
     - `[data-ui] :where(h1,h2,h3,h4,h5,h6,p,figure,blockquote,dl,dd,ul,ol,pre){margin:0}`
     - `[data-ui] :where(h1,h2,h3,h4,h5,h6){font-size:inherit;font-weight:inherit;font-family:inherit}`
     - `[data-ui] :where(ul,ol,menu){list-style:none;padding:0}`
     - `[data-ui] :where(a){color:inherit;text-decoration:inherit}`
     - `[data-ui] :where(button,input,select,textarea,optgroup){font:inherit;letter-spacing:inherit;color:inherit;background-color:transparent;padding:0;margin:0;border-radius:0}`
     - `[data-ui] :where(kbd){font-family:inherit}`
     - `[data-ui] :where(th){text-align:inherit;font-weight:inherit;color:inherit}`
     - `[data-ui] :where(td){vertical-align:inherit}`
     - `[data-ui] ::selection{background:var(--ui-brand);color:var(--ui-brand-foreground)}`
     - `[data-ui] :where(button,[role="button"]){cursor:pointer}`
     - `[data-ui] :where(img,svg,video,canvas){display:block;max-width:100%}`
     - `[data-ui] :where(img,video){height:auto}`
     - `[data-ui] :where(table){border-collapse:collapse}`
     - `[data-ui] :focus-visible{outline:2px solid var(--ui-ring);outline-offset:2px}` (độ đặc hiệu (0,2,0), để thắng
       `:focus-visible` cũ (0,1,0) mà Task 2 bước 6 chuyển vào cùng layer `base`)
  Không có rule `*`, `html`, `body` ngoài `[data-ui]`.
- [ ] **Step 6: Bọc `globals.css`**: chỉ bọc vào `@layer base { … }` các khối **chọn thuần theo phần tử**:
  `*, *::before, *::after` (dòng 6–10), `html` (12–20), `body` (43…), `dd`, `h4`, `canvas`, `img`, `kbd`, `button`, `a`,
  `::selection`, `table` (cả hai khối), `th`, `td`, `tbody tr:last-child td`.
  Bọc thêm vào `@layer base` hai khối: `ol[class]` và `:focus-visible` (dòng ~127). Nếu để ngoài layer, chúng thắng
  mọi utility Tailwind (rule ngoài layer luôn thắng rule trong layer) nên vòng focus bị nhân đôi và `list-decimal` vô hiệu.
  Trang cũ không đổi, vì CSS Module của trang cũ vẫn ngoài layer và vẫn thắng như trước.
  Giữ ngoài layer, đúng chỗ cũ:
  - mọi rule có class, thuộc tính khác, `:has(`, `[data-reveal…]`, `.type-*`, `.sr-only`, `.shop-*`;
  - mọi `@media`, `@keyframes`.
  Không đổi nội dung khai báo nào.
- [ ] **Step 7:** `layout.tsx`: thêm `import "@/styles/ui.css";` **trước** `import "@/styles/tokens.css";`.
  `src/lib/utils.ts`: `cn` như Interfaces.
- [ ] **Step 8: Chạy test và build**
  Run: `cd web && node --test src/styles/ui-vars.test.mjs && NEXT_DIST_DIR=.next-agy npm run build`
  Expected: test PASS; build xanh. Nếu `next build` chèn dòng vào `tsconfig.json` thì `git checkout -- tsconfig.json`.

### Task 3: Font

**Files:**
- Create: `web/src/lib/fonts.ts`.
- Modify: `web/src/app/layout.tsx` (gắn biến font vào `<html className>`), `web/src/lib/site/theme.ts` (thêm hàm).
- Test: `web/src/lib/site/siteUi.test.mjs` (mới, dùng chung với Task 4).

**Interfaces:**
- Produces (`fonts.ts`):
  - `inter`: `Inter({ subsets: ["latin","vietnamese"], variable: "--font-inter", display: "swap" })`;
  - `siteWorkSans` (`--font-site-work-sans`), `siteBeVietnamPro` (`--font-site-be-vietnam-pro`),
    `siteIbmPlexSans` (`--font-site-ibm-plex-sans`), `sitePlusJakartaSans` (`--font-site-plus-jakarta-sans`), tất cả
    `preload: false`, `display: "swap"`, subsets `["latin","vietnamese"]`. Font không phải variable thì
    `weight: ["400","500","600","700"]`. Nếu Google không có subset `vietnamese` cho font nào thì dùng
    `["latin","latin-ext"]` và ghi vào báo cáo;
  - `fontVariableClassNames: string`, nối các `.variable` trên.
- Produces (`theme.ts`):
  - `SITE_UI_FONT_STACKS: Record<SiteFontFamily, string>`, giá trị dạng `"var(--font-site-work-sans), system-ui, sans-serif"`;
    riêng `inter` là `"var(--font-inter), system-ui, sans-serif"`;
  - `resolveSiteUiFontStack(fontFamily?: string | null): string`, giá trị lạ hoặc rỗng → stack của
    `DEFAULT_SITE_THEME.font_family`.
- **Giữ nguyên** `SITE_FONT_STACK`, `resolveSiteFontStack`, `SITE_FONT_LABELS` và `--site-font`, vì site cũ vẫn chạy
  Archivo tới phase 4.

- [ ] **Step 1: Viết test trước** trong `siteUi.test.mjs` (import `../site/theme.ts`, `./types.ts` tương đối):
  `test("site ui font stacks map every allowed font")`:
  - với mỗi `f` trong `ALLOWED_SITE_FONTS`, `resolveSiteUiFontStack(f) === SITE_UI_FONT_STACKS[f]`;
  - `resolveSiteUiFontStack("inter")` bắt đầu bằng `"var(--font-inter)"`;
  - `resolveSiteUiFontStack("comic-sans")` và `resolveSiteUiFontStack(undefined)` bằng
    `SITE_UI_FONT_STACKS[DEFAULT_SITE_THEME.font_family]`;
  - `resolveSiteFontStack("inter") === SITE_FONT_STACK` (bất biến cũ còn nguyên).
- [ ] **Step 2:** Run `cd web && node --test src/lib/site/siteUi.test.mjs`. Expected: FAIL (chưa có hàm).
- [ ] **Step 3:** Viết `fonts.ts` và hai export trong `theme.ts`. `layout.tsx`:
  `<html className={`${archivo.variable} ${fontVariableClassNames}`}>`.
- [ ] **Step 4:** Run lại test. Expected: PASS.

### Task 4: Theme site → `--ui-*`

**Files:**
- Modify: `web/src/lib/site/theme.ts` (thêm). Test: `web/src/lib/site/siteUi.test.mjs`.

**Interfaces:**
- Produces:
  - `type SiteUiThemeInput = (SiteTheme & { radius?: string | null; density?: string | null }) | null | undefined`
  - `siteRadiusToCss(radius?: string | null): string`: `"none"`→`"0px"`, `"sm"`→`"4px"`, `"md"`→`"8px"`,
    `"lg"`→`"16px"`; rỗng hoặc lạ → `"0px"`.
  - `interface SiteUiVars` có đúng các khoá: `--ui-background`, `--ui-foreground`, `--ui-card`, `--ui-card-foreground`,
    `--ui-popover`, `--ui-popover-foreground`, `--ui-primary`, `--ui-primary-foreground`, `--ui-secondary`,
    `--ui-secondary-foreground`, `--ui-muted`, `--ui-muted-foreground`, `--ui-accent`, `--ui-accent-foreground`,
    `--ui-brand`, `--ui-brand-foreground`, `--ui-border`, `--ui-input`, `--ui-ring`, `--ui-radius`, `--ui-font`.
  - `buildSiteUiVars(theme: SiteUiThemeInput): SiteUiVars`.
- Quy tắc (`bg`, `surface`, `ink`, `accent`, `accentInk` chuẩn hoá giống `buildSiteThemeVars`, `mixHex(a, b, w)` = w phần `a`):
  - background=bg; foreground=ink; card/popover=surface; card-/popover-foreground=ink.
  - primary=accent; primary-foreground=accentInk; brand=accent; brand-foreground=accentInk.
  - ring=accent nếu `contrastRatio(accent,bg) ≥ 3`, ngược lại ring=ink (vòng focus phải thấy được trên nền site tối).
  - secondary=`mixHex(ink,bg,0.08)`; muted=`mixHex(ink,bg,0.06)`; accent=`mixHex(ink,bg,0.10)`; secondary-/accent-foreground=ink.
  - muted-foreground: bắt đầu `w=0.70`, tăng 0.05 tới khi `contrastRatio(c,bg) ≥ 4.5` **và** `contrastRatio(c,surface) ≥ 4.5`.
    Tới `w=1` thì dùng `ink`.
  - border=`mixHex(ink,bg,0.14)`; input: bắt đầu `w=0.45`, tăng 0.05 tới khi `contrastRatio(c,bg) ≥ 3`.
  - radius=`siteRadiusToCss(theme?.radius)`; font=`resolveSiteUiFontStack(theme?.font_family)`.
  - Không đổi `buildSiteThemeVars`.

- [ ] **Step 1: Viết test trước** (`siteUi.test.mjs`, import thêm `../../features/site/templateCatalog.ts`):
  - `test("every platform template yields accessible ui vars")`: với mỗi `t` trong `PLATFORM_TEMPLATES`,
    `v = buildSiteUiVars(t.config.theme)` phải thoả:
    - `contrastRatio(v["--ui-muted-foreground"], v["--ui-background"]) >= 4.5`;
    - `contrastRatio(v["--ui-muted-foreground"], v["--ui-card"]) >= 4.5`;
    - `contrastRatio(v["--ui-input"], v["--ui-background"]) >= 3`;
    - `contrastRatio(v["--ui-ring"], v["--ui-background"]) >= 3`;
    - `v["--ui-primary"] === t.config.theme.accent_color.toUpperCase()`;
    - `v["--ui-primary-foreground"] === t.config.theme.accent_text_color.toUpperCase()`.
  - `test("radius mapping")`: `siteRadiusToCss("none")==="0px"`, `("sm")==="4px"`, `("md")==="8px"`, `("lg")==="16px"`,
    `("")==="0px"`, `(undefined)==="0px"`, `("xl")==="0px"`.
  - `test("broken theme falls back to defaults")`: `buildSiteUiVars(null)` không ném lỗi và
    `["--ui-background"] === DEFAULT_SITE_THEME.bg_color.toUpperCase()`. `buildSiteUiVars({ ...DEFAULT_SITE_THEME, bg_color: "red", radius: "weird", font_family: "x" })`
    cho background mặc định, radius `"0px"`, font = stack mặc định.
  - `test("ui vars has exactly the documented keys")`: `Object.keys(buildSiteUiVars(null)).sort()` bằng danh sách 21
    khoá ở Interfaces (sắp xếp).
- [ ] **Step 2:** Run `cd web && node --test src/lib/site/siteUi.test.mjs`. Expected: FAIL.
- [ ] **Step 3:** Viết `siteRadiusToCss`, `SiteUiVars`, `buildSiteUiVars` trong `theme.ts`.
- [ ] **Step 4:** Run lại + `node --test src/lib/site/site.test.mjs`. Expected: cả hai PASS (test cũ không đổi).

### Task 5: Component shadcn + `UiScope` (portal)

**Files:**
- Create (qua CLI rồi chỉnh tay): `web/src/components/ui/{button,badge,card,input,textarea,label,field,select,checkbox,radio-group,switch,tabs,table,dialog,alert-dialog,sheet,dropdown-menu,popover,command,tooltip,collapsible,accordion,progress,separator,skeleton,avatar,breadcrumb,pagination,scroll-area,toggle,toggle-group,sidebar,sonner,alert}.tsx`
  và `web/src/hooks/use-mobile.ts`.
- Create: `web/src/components/ui/ui-scope.tsx`.

**Interfaces:**
- Produces `ui-scope.tsx` (`"use client"`):
  - `UiScope(props: React.ComponentProps<"div">): React.JSX.Element` render
    `<div data-ui {...props}>{children}<div data-ui-portal ref={setContainer} /></div>` và cung cấp context;
  - `usePortalContainer(): HTMLElement | null`, ngoài `UiScope` thì trả `null` (Radix tự dùng `document.body`).
- Produces biến thể (cva):
  - `Button` `variant`: `default` (nền `primary`, chữ `primary-foreground`, `shadow-button`), `outline` (nền `card`,
    viền `border`, `shadow-card`), `secondary`, `ghost`, `link` (chữ `brand`), `destructive`;
    `size`: `default` (h-9), `sm` (h-8), `lg` (h-10), `icon` (size-9);
  - `Badge` `variant`: `default`, `secondary`, `outline`, `success`, `attention`, `warning`, `info`, `critical`,
    `neutral` (6 tông cuối dùng `bg-badge-<tông>-bg text-badge-<tông>-ink`, `rounded-full`, `text-xs font-medium`);
  - `Card`: `rounded-xl bg-card shadow-card` (không viền đậm), `CardHeader` tiêu đề `text-sm font-semibold`, có `CardAction`;
  - `Alert` `variant`: `default`, `destructive`, `inverse` (nền `topbar`, chữ trắng);
  - `Tabs` kiểu Figma `04`: tab chọn có nền `secondary`, `rounded-md`.

- [ ] **Step 1:** `cd web && cp src/styles/ui.css /tmp/ui.css.before-shadcn` (file chưa được git theo dõi nên
  `git diff` không thấy CLI chèn gì). Rồi `npx shadcn@latest add button badge card input textarea label field select checkbox radio-group switch tabs table dialog alert-dialog sheet dropdown-menu popover command tooltip collapsible accordion progress separator skeleton avatar breadcrumb pagination scroll-area toggle toggle-group sidebar sonner alert --yes`.
  Nếu CLI lỗi mạng hoặc đòi chạy `init`, chép mã từ tài liệu shadcn (bản Tailwind v4, `radix-ui`) vào đúng file và ghi
  vào báo cáo.
- [ ] **Step 2: Dọn sau CLI**:
  - `diff /tmp/ui.css.before-shadcn src/styles/ui.css`. Mọi biến CLI chèn vào `:root`, `.dark`, `@theme inline`, `@layer base` phải được đổi tên
    sang `--ui-*` (vd. `--sidebar` → `--ui-sidebar`, mapping `--color-sidebar: var(--ui-sidebar)`), hoặc xoá nếu trùng
    thứ đã có. Xoá khối `.dark {}`. Xoá `@import "tailwindcss"` nếu CLI thêm.
  - `package.json`: gỡ mọi gói ngoài danh sách cho phép mà CLI tự cài (vd. `next-themes`).
  - `sonner.tsx`: bỏ `next-themes`, cố định `theme="light"`. Toast nền `#1F3038` chữ trắng (qua `toastOptions.classNames`),
    vị trí `bottom-right`.
  - `sidebar.tsx`: bỏ ghi cookie `sidebar_state`. `SidebarProvider` nhận thêm `storageKey?: string` và lưu/đọc trạng
    thái mở bằng `localStorage` (đọc sau khi mount để không lệch hydrate).
  - Chỉnh biến thể theo Interfaces.
- [ ] **Step 3: Nối portal**: trong `dialog`, `alert-dialog`, `sheet`, `popover`, `dropdown-menu`, `select`, `tooltip`,
  phần `*Portal` nhận `container={usePortalContainer() ?? undefined}`. `command` dùng `Dialog` nên tự nhận.
- [ ] **Step 4:** Run: `cd web && node --test src/styles/ui-vars.test.mjs && npm run lint && npx tsc --noEmit -p .`
  Expected: PASS, không lỗi lint/type. Nếu `tsc` báo lỗi ở file cũ không thuộc phạm vi thì ghi vào báo cáo, không sửa.

### Task 6: Pattern ứng dụng (`src/components/app/`)

**Files (mỗi pattern một file):**
- Create: `AppShell.tsx`, `PageHeader.tsx`, `StatusBadge.tsx`, `statusTone.ts`, `IndexTable.tsx`, `MetricStrip.tsx`,
  `DetailLayout.tsx`, `EmptyState.tsx`, `ErrorState.tsx`, `AuthSplit.tsx`, `KpiChartCard.tsx`, `SetupGuide.tsx`,
  `setupProgress.ts`, `Timeline.tsx`, `SaveBar.tsx`, `Money.tsx`, `charts.tsx`, `index.ts`.
- Test: `web/src/components/app/statusTone.test.mjs`, `web/src/components/app/setupProgress.test.mjs`.

**Interfaces (phase 1–4 dùng đúng tên này):**
- `statusTone.ts` (thuần, không import React):
  - `type StatusTone = "success" | "attention" | "warning" | "info" | "critical" | "neutral"`
  - `orderStatusTone(status?: string | null): StatusTone`
  - `shipmentJobTone(status?: string | null): StatusTone`
  - `productStatusTone(status?: string | null): StatusTone`
  - `bankAccountStatusTone(status?: string | null): StatusTone`
  - `siteStatusTone(status?: string | null): StatusTone` (`SiteStatus` ở `src/lib/site/types.ts`: `PUBLISHED` → success,
    `DRAFT` → info, `SUSPENDED` → critical)
  - So khớp không phân biệt hoa thường; lạ/rỗng → `"neutral"`. Ánh xạ đúng spec §7.1.
- `StatusBadge({ tone, children, icon }: { tone: StatusTone; children: React.ReactNode; icon?: LucideIcon })`
- `setupProgress.ts`:
  - `type SetupStepStatus = "done" | "todo" | "unknown"`
  - `interface SetupStepState { id: string; status: SetupStepStatus; optional?: boolean }`
  - `setupProgress(steps: SetupStepState[]): { done: number; total: number; complete: boolean }`: chỉ đếm bước không
    `optional`; `unknown` không tính là xong; `complete = total > 0 && done === total`.
- `SetupGuide({ title, steps, storageKey })` với `steps: Array<SetupStepState & { title: string; description?: string; badge?: { tone: StatusTone; label: string }; primary?: React.ReactNode; secondary?: React.ReactNode; image?: { src: string; alt: string } }>`
  - Hiển thị theo spec A9. Bước đầu tiên chưa xong mở sẵn. Thu gọn lưu `localStorage[storageKey]`.
  - `complete` thì hiện dòng "✓ Đã thiết lập xong". Bước optional có nhãn "Tuỳ chọn", bước unknown có nhãn "Chưa kiểm tra được".
- `AppShell` (`"use client"`):
  - `AppShell(props: { area: "seller" | "admin"; areaLabel: string; homeHref: string; navGroups: AppNavGroup[]; commandItems: AppCommandItem[]; account: AppAccount; storageKey: string; mobileNav?: React.ReactNode; children: React.ReactNode })`
  - `interface AppNavGroup { title: string; items: AppNavItem[] }`
  - `interface AppNavItem { href: string; label: string; icon: LucideIcon; badge?: number; external?: boolean; children?: Array<{ href: string; label: string }>; match?: "exact" | "prefix" }`
  - `interface AppCommandItem { label: string; group: string; href: string; keywords?: string[] }`
  - `interface AppAccount { name: string; subtitle?: string; menu: Array<{ label: string; href?: string; onSelect?: () => void; destructive?: boolean }> }`
  - Bố cục, màu, responsive đúng spec §8.1:
    - thanh trên 56px `bg-topbar` gồm `NexusMark` (import từ `@/components/Icons`), "NexusMart" và `Badge` `areaLabel`;
    - ô ⌘K mở `CommandDialog`, phím tắt `Ctrl/⌘+K`;
    - menu trái 240px dùng `Sidebar` `collapsible="icon"` + `storageKey`; < 768 thì `Sheet` trái + slot `mobileNav`;
    - gốc là `UiScope` + `text-sm`;
    - mục active theo `usePathname()` + `match` (mặc định `prefix`, riêng `homeHref` là `exact`);
    - `children` chỉ hiện khi mục cha active.
- `PageHeader(props: { title: string; backHref?: string; badges?: React.ReactNode; subtitle?: React.ReactNode; secondaryActions?: React.ReactNode; moreActions?: Array<{ label: string; href?: string; onSelect?: () => void; destructive?: boolean }>; pager?: { prevHref?: string; nextHref?: string }; primaryAction?: React.ReactNode })`
  - Theo spec §8.2. < 768 thì chỉ còn `primaryAction`, phần còn lại vào "Thêm thao tác ▾".
- `IndexTable(props: { tabs?: Array<{ key: string; label: string; count?: number; href?: string }>; activeTab?: string; onTabChange?: (key: string) => void; toolbar?: React.ReactNode; selection?: { count: number; actions: React.ReactNode; onClear: () => void }; footer?: React.ReactNode; children: React.ReactNode })`
  - Thẻ chứa tabs/toolbar và vùng `children` (thường là `<Table>`); `selection.count > 0` thì thanh chọn thay hàng tabs.
  - Kèm `IndexRowCard(props: { href?: string; onClick?: () => void; media?: React.ReactNode; title: React.ReactNode; badge?: React.ReactNode; meta?: React.ReactNode; leading?: React.ReactNode })`
    cho danh sách thẻ trên mobile.
- `MetricStrip({ items }: { items: Array<{ label: string; value: React.ReactNode; hint?: React.ReactNode }> })`
- `DetailLayout({ main, aside }: { main: React.ReactNode; aside: React.ReactNode })`: lưới 2/3 + 1/3, tối đa 1000px,
  < 1024 thì xếp dọc.
- `EmptyState({ icon, title, description, action }: { icon: LucideIcon; title: string; description?: string; action?: React.ReactNode })`
- `ErrorState({ title, description, onRetry, retryHref }: { title?: string; description: string; onRetry?: () => void; retryHref?: string })`,
  `title` mặc định "Chưa tải được dữ liệu", nút "Thử lại".
- `AuthSplit(props: { variant: "standalone" | "embedded"; brand: { name: string; href: string; logo?: React.ReactNode }; title: string; subtitle?: React.ReactNode; aside?: { imageSrc: string; caption: string }; footer?: React.ReactNode; children: React.ReactNode })`
  - Theo spec §8.3. `standalone` có nền `topbar` toàn màn. `embedded` là thẻ trong vùng nội dung, nhận màu của scope cha.
- `KpiChartCard({ title, tooltip, value, chart, footer }: { title: string; tooltip?: string; value: React.ReactNode; chart?: React.ReactNode; footer?: React.ReactNode })`
- `Timeline({ items }: { items: Array<{ id: string; at: string; title: string; description?: string }> })`: chỉ đọc.
- `SaveBar({ visible, onDiscard, onSave, saving, message }: { visible: boolean; onDiscard: () => void; onSave: () => void; saving?: boolean; message?: string })`
  - Thanh dính đỉnh vùng nội dung, nền `topbar`, mặc định "Thay đổi chưa lưu", nút [Huỷ] [Lưu].
- `Money({ vnd, compareAtVnd, size }: { vnd: number; compareAtVnd?: number; size?: "sm" | "md" | "lg" })`: dùng
  `formatVnd`, `tabular-nums`; giá gốc gạch ngang khi `compareAtVnd > vnd`.
- `charts.tsx`: `LineChart` và `BarChart` **cùng props** với `src/components/SvgChart.tsx` (`LineChartProps`,
  `BarChartProps`, `LineSeries`, `ChartAnnotation`), vẽ lại bằng SVG + Tailwind:
  - màu chuỗi chính `brand`, chuỗi phụ `muted-foreground`, lưới `border`;
  - **không** gradient, `<title>`/`aria-label` giữ như cũ.
  - Không sửa `SvgChart.tsx`.
- `index.ts` export lại tất cả.

- [ ] **Step 1: Viết test trước**:
  - `statusTone.test.mjs`:
    - `orderStatusTone`: `PENDING`→warning; `CONFIRMED`, `PREPARING`→attention; `READY_TO_SHIP`, `SHIPPING`→info;
      `DELIVERED`, `COMPLETED`→success; `DELIVERY_FAILED`, `CANCELLED`→critical; `RETURN_REQUESTED`→warning;
      `RETURNED`, `REFUNDED`→neutral; `"shipping"` (thường)→info; `null`, `""`, `"FOO"`→neutral.
    - `shipmentJobTone`: `QUEUED`→info, `FAILED`→critical.
    - `productStatusTone`: `APPROVED`→success, `PENDING`→attention, `REJECTED`→critical, `OUT_OF_STOCK`→warning,
      `HIDDEN`→neutral.
    - `bankAccountStatusTone`: `VERIFIED`→success, `PENDING_OTP`, `PENDING_ADMIN_REVIEW`→attention, `REJECTED`→critical.
    - `siteStatusTone`: `PUBLISHED`→success, `DRAFT`→info, `SUSPENDED`→critical, `null`→neutral.
  - `setupProgress.test.mjs`:
    - `[done, done, todo] + optional done` → `{ done: 2, total: 3, complete: false }`;
    - `[done, unknown, done]` → `{ done: 2, total: 3, complete: false }`;
    - `[done, done, done] + optional todo` → `complete: true`;
    - `[]` → `{ done: 0, total: 0, complete: false }`.
- [ ] **Step 2:** Run `cd web && node --test src/components/app/statusTone.test.mjs src/components/app/setupProgress.test.mjs`.
  Expected: FAIL.
- [ ] **Step 3:** Viết `statusTone.ts`, `setupProgress.ts`. Run lại. Expected: PASS.
- [ ] **Step 4:** Viết các component còn lại theo Interfaces và spec §6–§9. Chỉ dùng `components/ui` + Tailwind,
  không CSS Module. Mọi nút icon có `aria-label` tiếng Việt.
- [ ] **Step 5:** Run `cd web && npm run lint && npx tsc --noEmit -p .`. Expected: không lỗi trong file mới.

### Task 7: Trang duyệt `/design`

**Files:**
- Create: `web/src/app/(design)/design/layout.tsx`, `page.tsx` (nền tảng + component), `app/page.tsx` (AppShell +
  9 kiểu trang), `auth/page.tsx` (AuthSplit), `site/page.tsx` (khung site 8 mẫu), `_samples.ts` (dữ liệu mẫu dùng
  chung), cùng các file client cần thiết trong thư mục này.
- Modify: `web/src/lib/site/host.ts`: thêm `"/design"` vào `ROOT_PROTECTED_PREFIXES`, để middleware xoay phiên như
  `/admin`.

**Interfaces:**
- Consumes: mọi thứ của Task 2–6; `requireRole` (`@/lib/server/session`); `ADMIN_STAFF_ROLES` (`@/lib/admin-rbac`);
  `PLATFORM_TEMPLATES` (`@/features/site/templateCatalog`) chỉ để **đọc**; `buildSiteUiVars`, `isDarkHex`.

- [ ] **Step 1:** `layout.tsx` (server):
  - `await requireRole(...ADMIN_STAFF_ROLES)`;
  - `export const metadata = { title: "Thiết kế (nội bộ)", robots: { index: false, follow: false } }`;
  - `export const dynamic = "force-dynamic"`;
  - bọc `UiScope` + `<Toaster />`.
- [ ] **Step 2:** `/design`:
  - mục lục 3 link (`/design/app`, `/design/auth`, `/design/site`);
  - **Nền tảng**: bảng màu §6.1 (ô màu + mã hex + tỉ lệ tương phản ghi sẵn), thang chữ §6.2, bo góc, bóng;
  - **Component**: mỗi component §7 ở trạng thái thường, hover (ghi chú), focus, disabled, lỗi (ô nhập có `aria-invalid`),
    đang tải (`Skeleton`);
  - 6 tông `StatusBadge`;
  - nút mở `Dialog`, `AlertDialog`, `Sheet`, `DropdownMenu`, `Popover`, `Tooltip`, `CommandDialog`;
  - nút bắn toast.
- [ ] **Step 3:** `/design/app?kieu=a1|a2|…|a9` render `AppShell` (nav mẫu của người bán theo spec §8.1, tên shop mẫu
  dài "Xưởng may Áo Đẹp Sài Gòn chi nhánh Thủ Đức (mẫu)"). Nội dung là kiểu trang tương ứng spec §9, dữ liệu mẫu:
  - A1 bảng sản phẩm (có hàng mở rộng sửa tồn) và bảng đơn (có checkbox + thanh chọn);
  - A2 chi tiết đơn; A3 form sản phẩm + `SaveBar`; A4 3 `KpiChartCard` với `LineChart`; A5 cài đặt;
  - A6 hộp thư; A7 khung Site Studio 5 chế độ (chỉ hình); A8 3 thẻ chọn mẫu; A9 `SetupGuide` (1/3, có bước optional);
  - mobile hiện `mobileNav` mẫu 5 nút;
  - thanh trên có liên kết chuyển nhanh giữa các kiểu.
- [ ] **Step 4:** `/design/auth?bien-the=standalone|embedded`: `AuthSplit` với form đăng ký mẫu gồm họ tên, email, SĐT,
  mật khẩu + thanh độ mạnh, nhập lại, `ToggleGroup` "Người bán / Khách mua", `Badge` gợi ý tên miền phụ. Form không gửi đi đâu.
- [ ] **Step 5:** `/design/site?mau=<template_id>`:
  - `Select` chọn 1 trong 8 `PLATFORM_TEMPLATES`, mặc định `minimal-fashion`;
  - khung là `UiScope` có `style={buildSiteUiVars(theme)}` và `data-site-theme-mode={isDarkHex(bg) ? "dark" : "light"}`,
    **không** gắn `data-site-root`;
  - trong khung là các primitive site:
    - đầu trang mẫu (logo chữ, nav, ô tìm mở `CommandDialog`, icon giỏ có số);
    - 4 thẻ sản phẩm (ảnh trong `web/public/products|models`, `Money` có giá gạch, `Badge` giảm giá);
    - khối mua của PDP (`ToggleGroup` biến thể có SKU hết hàng, −/+ số lượng, 2 nút);
    - dòng giỏ hàng;
    - tóm tắt thanh toán + `RadioGroup` COD/VietQR;
    - nút mở `Sheet` "Đã thêm vào giỏ" và `Dialog`;
    - 6 tông nhãn;
    - `AuthSplit embedded`.
- [ ] **Step 6:** Run `cd web && NEXT_DIST_DIR=.next-agy npm run build`. Expected: build xanh, bảng route có
  `/design`, `/design/app`, `/design/auth`, `/design/site`. Hoàn nguyên `tsconfig.json` nếu bị chèn.

### Task 8: Sửa luật trong `web/CLAUDE.md` + `.claude/rules/frontend.md`, rồi cổng kiểm cuối

- [ ] **Step 0:** Đọc lại **ngay trước khi sửa** hai file (phiên trang chủ có thể vừa thêm dòng). Chỉ sửa đúng phần
  dưới đây bằng thay thế có chủ đích; **giữ nguyên mọi dòng nói về ngoại lệ của trang chủ** (gradient, bóng, Inter cho
  trang chủ).
  - `web/CLAUDE.md`: mục `## Giao diện và Style` mới của `web/CLAUDE.md` nêu đủ các ý sau, ngắn gọn:
  - nguồn thiết kế là `.agents/briefs/shadcn-redesign/design.md`;
  - UI dùng shadcn/ui (`src/components/ui`) + pattern `src/components/app`, Tailwind v4 **chưa bật preflight tới phase 5**;
  - màu qua biến `--ui-*` (tên Tailwind chuẩn `bg-primary`… ánh xạ sang `--ui-*`); font Inter (`--font-inter`) cho
    phần ứng dụng; vùng mới bọc trong `UiScope`;
  - **cấm trộn** Tailwind và CSS Module trong một component; khu vực chưa chuyển vẫn là CSS Module + `tokens.css`, đừng sửa;
  - `src/components/ui` và `src/components/app` **đóng băng sau phase 0**: swarm khu vực không chạy `shadcn add`, thiếu
    thì báo;
  - phần ứng dụng được dùng bóng nhẹ theo spec §6.3; site gian hàng vẫn phẳng;
  - phụ thuộc UI được phép là danh sách ở Global Constraints;
  - soát Playwright 390/768/1440.
  Xoá các câu cũ "Không dùng Tailwind, không cài thư viện UI/icon/state ngoài" và "Giữ bề mặt phẳng, không gradient hay
  shadow tĩnh". Giữ câu về chữ tiếng Việt cho người mua.
  - `.claude/rules/frontend.md` dòng 8–10 ("Chỉ dùng design token từ `tokens.css`", "Không thêm Tailwind hay thư viện
    UI", "Component tương tác phức tạp"): viết lại cho đúng luật mới (khu vực đã chuyển dùng `--ui-*`/Tailwind/shadcn theo
    ADR 0011, khu vực chưa chuyển giữ CSS Module + `tokens.css`; dialog/combobox dùng Radix qua `components/ui`).
  - Kiểm: `rg -n "Không dùng Tailwind|Không thêm Tailwind|shadow tĩnh" web/CLAUDE.md .claude/rules/frontend.md`
    không ra dòng nào (trừ dòng ngoại lệ của trang chủ, nếu có).
- [ ] **Step 1:** Run (trong `web/`):
  `npm run lint && npm run format:check && npm run test:unit && npm run check:data && NEXT_DIST_DIR=.next-agy npm run build && git checkout -- tsconfig.json`
  Expected: tất cả xanh. `format:check` fail thì chạy `npx prettier --write` **chỉ trên file trong phạm vi**.
- [ ] **Step 2:** Run `git status --short` (ở gốc repo). Đối chiếu với "Phạm vi file". Chỉ hoàn nguyên file ngoài phạm vi
  **mà chính bạn đã tạo hoặc sửa**. File khác (của phiên trang chủ, hay đã bẩn từ trước) thì để nguyên và liệt kê trong
  báo cáo.
- [ ] **Step 3:** Run `rg -n "data-ui|UiScope" web/src --glob '!web/src/components/**' --glob '!web/src/app/(design)/**'`.
  Expected: không ra dòng nào.
- [ ] **Step 4:** Xoá `web/.next-agy` và `/tmp/ui.css.before-shadcn`.

## Báo cáo khi xong

1. Danh sách file tạo/sửa, nhóm theo Task.
2. Kết quả từng lệnh ở Task 8 (dán dòng tổng kết: số test pass/fail, bảng First Load JS của `/design*`).
3. Mọi chỗ làm khác plan và lý do, ví dụ:
   - CLI shadcn lỗi nên chép tay;
   - font không có subset `vietnamese`;
   - biến CLI chèn vào đã đổi tên ra sao;
   - gói CLI tự cài đã gỡ;
   - lỗi `tsc` ở file cũ ngoài phạm vi.
4. Những gì chưa xong hoặc còn nghi ngờ.
