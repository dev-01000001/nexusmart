# P5d — Tài liệu sau redesign (phase 5)

Đọc trước: `common.md` (phase 5), `../design.md` (spec đã duyệt), `p5a-global-css.md` (hợp đồng CSS cuối), mã hiện tại trong `web/`.
Viết cho: lập trình viên và agent mới vào dự án, cần biết **cách làm UI bây giờ**, không cần lịch sử chuyển đổi.

## Trạng thái cuối cần mô tả (đã chốt)
- Một thư viện UI: shadcn/ui (new-york, radix-ui) trong `web/src/components/ui/`, pattern ứng dụng trong `web/src/components/app/`.
  Tailwind v4, **preflight bật toàn cục**, CSS duy nhất ở `web/src/styles/ui.css` (+ `features/platform/home/` cho trang chủ).
  Không còn CSS Module, `globals.css`, `tokens.css`, font Archivo, component cũ (`Button`, `Price`, `ProductCard`, `StateView`,
  `SvgChart`), trang `/design`.
- Biến `--ui-*` và `@theme inline`; màu tông nhãn; Inter cho phần ứng dụng; site gian hàng: 8 mẫu, 2 mẫu tối, 5 font thật qua
  `resolveSiteUiFontStack`, `SiteThemeRoot` gắn `data-site-root` / `data-site-theme-mode`.
- `UiScope` còn giữ (portal container cho Radix), không còn reset bám `data-ui`.
- Import từng file, không qua barrel `@/components/app` / `@/features/site` ở site (ngân sách First Load JS trang chủ site + PDP ≤ 296 kB).
- Kiểm UI: `tests/ui-flows/run.mjs --area platform|admin|seller|site|templates`, `tests/ui-flows/states.mjs`,
  `.agents/tools/{ui-shots.mjs,ui-diff.sh,scroll-shots.mjs,snap-build.sh}`.
- Chi tiết P5a chưa chốt (giá trị `optimize` của `@tailwindcss/postcss`, file CSS phụ nếu có): để chỗ `<!-- P5A -->`, Claude sẽ báo.

## Việc
1. `web/CLAUDE.md`: viết lại phần UI/CSS/component/kiểm tra theo trạng thái trên; bỏ mọi chỉ dẫn về CSS Module, token cũ, Archivo,
   "chưa preflight", `/design`. Giữ các phần không liên quan (BFF, host routing…) nếu còn đúng; sai thì sửa theo mã.
2. `web/DESIGN_REPORT.md`: báo cáo nghiệm thu redesign. Số liệu phase 1–4 lấy từ `_ledgers/2026-11-26-shadcn-redesign/HANDOFF.md` §0
   (59/59 luồng, 8/8 mẫu × COD, test:unit 510, First Load JS từng route chính so mốc, lệch trang chủ ≤ 6/255 do Lightning CSS và cách xử lý).
   Phase 5: để mục chờ số liệu `<!-- P5 -->`. Ghi cả phần chưa đạt và lý do.
3. `web/FEATURE_MATRIX.md`: ma trận tuyến khớp mã hiện tại (đếm route từ `web/src/app`, bỏ `/design`, ghi component shadcn/app chính mỗi trang).
4. `.claude/skills/design-system/SKILL.md`: cập nhật theo trạng thái cuối (mô tả, luật cách ly, CSS, kiểm định); bỏ luật chỉ còn đúng trong lúc
   chuyển dần (độ đặc hiệu `globals.css`, `revert-layer`, plugin PostCSS của Next, cấm trộn với CSS Module).
5. Soát `docs/` và `web/` tìm chỗ còn nhắc `globals.css`, `tokens.css`, `module.css`, `Archivo`, `/design`, `StateView`… trong tài liệu
   (không phải mã): liệt kê trong báo cáo, sửa nếu là tài liệu hướng dẫn hiện hành; báo cáo nghiệm thu cũ (`docs/*_REPORT.md`) thì để nguyên.

## Phạm vi ghi
Chỉ các file `.md` nêu trên và tài liệu hướng dẫn trong `docs/`, `web/` tìm thấy ở bước 5. Cấm sửa mã, test, `AGENTS.md`, `CLAUDE.md` gốc,
`.agents/briefs/**`.

## Xong khi
- Mọi câu lệnh, đường dẫn, tên component trong tài liệu tồn tại thật (`rg`/`ls` kiểm từng cái, ghi bảng kiểm vào báo cáo).
- `npx prettier --check` trên các file `.md` đã sửa (nếu repo format `.md`).
- Báo cáo: danh sách file đã sửa, chỗ `<!-- P5A -->` / `<!-- P5 -->` còn chờ.
