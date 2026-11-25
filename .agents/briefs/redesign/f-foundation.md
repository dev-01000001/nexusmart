# F – Nền tảng thiết kế (chạy trước, một mình)
Đọc `.agents/briefs/redesign/design.md` toàn bộ, `AGENTS.md`, `tests/playwright/specs/buyer-flow.spec.ts`.

## Sở hữu
`web/src/styles/**`, `web/src/app/layout.tsx`, `web/src/app/(shop|seller|admin|site)/template.tsx` (mới), `web/src/app/(site)/layout.tsx`, `web/src/components/**`, `web/src/lib/site/theme.ts` + test của nó, `web/src/lib/site/types.ts` (chỉ nhãn font), ô chọn font trong `web/src/features/seller/site/SellerSiteEditorClient.tsx`, hook reveal (`web/src/features/catalog/useRevealObserver.ts`, `web/src/features/commerce/useRevealOnScroll.ts`, `web/src/features/platform/PlatformMotion.tsx`), `web/next.config.ts` (chỉ cờ viewTransition nếu giữ), `web/CLAUDE.md` phần font/style.

## Việc
1. Font Archivo theo mục 2; xoá font cũ và biến `--font-work-sans` ở mọi nơi (rg cả `web/src`; file của mảng khác chỉ được thay đúng tên biến/stack font).
2. `tokens.css`: thang chữ (trang + app), 4 giọng `--type-*` dùng được qua class/mixin `.type-wide/.type-normal/.type-tight/.type-poster`, màu thêm bậc nền (giữ tên token cũ còn được dùng để các mảng khác không vỡ), khoảng cách, bo góc, motion mục 5. `globals.css`: reset, focus, chọn chữ, `scrollbar-gutter`, `html.js` guard, tập recipe chuyển động đặt tên rõ (`[data-reveal="lines|clip|rows|scale|count"]`, `[data-rise]` giữ tương thích), `@media (prefers-reduced-motion)`.
3. Một cơ chế reveal duy nhất: CSS `view()` timeline trong `@supports` + một IntersectionObserver dự phòng; giữ API các hook cũ (re-export) để call site không vỡ.
4. Component dùng chung làm lại: `Button` (pill/vuông, nhãn trượt khi hover, press 140ms, trạng thái loading có skeleton/nét chạy, không đổi bề rộng), `Price`, `StateView` (rỗng/lỗi/tải: 1 câu + 1 nút, skeleton), `SvgChart` (trục, hover có đường dóng, nét vẽ vào một lần), `Icons` (một bộ nét 1.5px nhất quán), `ProductCard`.
5. `template.tsx` chuyển trang 320ms. Thử ViewTransition như mục 5; giữ hoặc bỏ, ghi lý do kèm bằng chứng.
6. Bỏ ô chọn font trong trình chỉnh sửa; giá trị `font_family` đang lưu phải được giữ nguyên khi lưu nháp/xuất bản. Cập nhật unit test cho `resolveSiteFontStack` (mọi giá trị enum + undefined + rác đều ra Archivo).
7. Trang mẫu KHÔNG commit: `.runs/shots/f/` chứa ảnh chụp trang có đủ dấu tiếng Việt (Ữ, Ỗ, ậ, ặ, Ỹ, đ, ₫) ở 4 giọng, chứng minh không cắt dấu và ₫ không rơi font.

## Nghiệm thu
Một font duy nhất được tải (kiểm `document.fonts` trên `/` và một site). `rg -n "work-sans|Work_Sans|Be_Vietnam|IBM_Plex|font-be-vietnam|font-ibm-plex" web/src` chỉ còn giá trị enum dữ liệu. `npm run lint && npm run format:check && npx tsc --noEmit && npm run test:unit && npm run check:data` xanh và `npm run build` xanh (dùng flock).
