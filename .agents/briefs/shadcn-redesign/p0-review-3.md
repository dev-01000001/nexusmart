# Phase 0 — review vòng 3 (Claude, 26/11/2026) = brief vòng 4

Vòng 3 đạt: hết #418 ở 390px, menu 13/13 link chỉ sáng một mục, lint 0, 510 test (đã commit `7632db5`).
Còn một lỗi, do chính brief vòng 1 của mình gây ra.

## Lỗi: CSS cũ biên dịch khác trước ở 59/70 file
- **Nguyên nhân:**
  - Có `postcss.config.mjs` thì Next bỏ **cả** pipeline mặc định, chứ không chỉ autoprefixer. Pipeline đó là
    `postcss-flexbugs-fixes` + `postcss-preset-env`
    (`node_modules/next/dist/build/webpack/config/blocks/css/plugins.js`, hàm `getDefaultPlugins`).
  - Vòng 1 mình chỉ bảo thêm lại autoprefixer, và dặn sai rằng preset-env "polyfill `@layer`". Đã thử: với cấu hình
    stage 3 của Next, `@layer` trong `globals.css` vẫn giữ nguyên (6 → 6).
- **Bằng chứng:** `node .runs/shadcn-redesign/css-pipeline-diff.mjs` chạy mọi CSS trong `web/src` (trừ `ui.css`) qua
  (A) pipeline mặc định của Next và (B) `web/postcss.config.mjs` hiện tại. Kết quả `70 files, 59 differ`. B thiếu:
  - `grid-gap` dự phòng cạnh `gap`;
  - `font-feature-settings` dự phòng cho `font-variant-numeric`/`font-kerning`;
  - `flex: 1` → `flex: 1 1` của flexbugs.
- **Đã thử với `ui.css`:** chạy Tailwind trước rồi qua hai plugin mặc định của Next. Số lượng `@layer`, `revert-layer`,
  `:where(`, `:is(`, `[data-ui="true"]`, `color-mix(`, `@property` không đổi. Thêm dự phòng cũ và đổi
  `@media (width >= 48rem)` thành `(min-width: 48rem)`, tốt hơn cho Safari cũ.

## Sửa (bớt một phụ thuộc)
1. `web/postcss.config.mjs` thay bằng:
   ```js
   // Next drops its default PostCSS plugins once this file exists. Re-add them after Tailwind so legacy CSS compiles
   // exactly as before (options mirror getDefaultPlugins in next/dist/build/webpack/config/blocks/css/plugins.js).
   // Remove the two Next plugins in phase 5 together with the CSS Modules.
   const config = {
     plugins: {
       "@tailwindcss/postcss": {},
       "next/dist/compiled/postcss-flexbugs-fixes": {},
       "next/dist/compiled/postcss-preset-env": {
         browsers: ["chrome 64", "edge 79", "firefox 67", "opera 51", "safari 12"],
         autoprefixer: { flexbox: "no-2009" },
         stage: 3,
         features: { "custom-properties": false },
       },
     },
   };

   export default config;
   ```
2. Gỡ devDependency: `cd web && npm uninstall -D autoprefixer`. Soát `package.json` và `package-lock.json`: chỉ mất
   autoprefixer và các gói chỉ nó dùng.
3. Sửa tài liệu cho đúng cách mới. Bỏ mọi chỗ nói `autoprefixer` là devDependency, thay bằng: "`postcss.config.mjs` thêm
   lại hai plugin mặc định của Next (`next/dist/compiled/postcss-flexbugs-fixes` + `postcss-preset-env`, cùng tuỳ chọn)
   sau Tailwind; không thêm phụ thuộc; gỡ ở phase 5". Các chỗ cần sửa:
   - `docs/adr/0011-shadcn-ui-tailwind-v4.md` (dòng ~24 và mục phụ thuộc tạm thời ~60-62);
   - `.claude/skills/design-system/SKILL.md` (dòng ~93-96);
   - `web/CLAUDE.md` (dòng ~19);
   - `.claude/rules/frontend.md` (dòng ~9).

**Phạm vi.** Chỉ 7 file trên (`postcss.config.mjs`, `package.json`, `package-lock.json` và 4 tài liệu).

**Cấm:**
- `next build`;
- lệnh git ghi, `checkout`/`restore`/`stash`/`clean`/`rm`;
- đụng `web/.next-qa` hay file nào khác.

## Kiểm cuối của bạn
```bash
node .runs/shadcn-redesign/css-pipeline-diff.mjs   # phải ra "70 files, 0 differ" và "@layer count: source 6, pipeline A 6"
cd web && npm run lint && npm run format:check && npm run test:unit && npm run check:data
```

**Báo cáo:**
- file đã sửa;
- dòng tổng kết từng lệnh;
- danh sách gói bị gỡ khỏi lockfile;
- chỗ làm khác brief và lý do.
