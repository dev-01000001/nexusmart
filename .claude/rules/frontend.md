---
paths:
  - "web/**"
---

# Quy tắc Frontend (`web/`)

- **Token và hệ thống UI theo ADR-0011**: khu vực đã chuyển dùng lớp biến `--ui-*`, Tailwind CSS v4 (chưa bật preflight tới phase 5; `ui.css` import ở layout từng khu vực đã chuyển, không ở `src/app/layout.tsx` gốc) và component từ `src/components/ui/` + `src/components/app/` theo `.agents/briefs/shadcn-redesign/design.md`, bọc trong `UiScope` (`data-ui="true"` — selector mới luôn là `[data-ui="true"]` vì mã cũ đã dùng `data-ui` với giá trị khác); phần ứng dụng dùng bóng nhẹ theo spec §6.3, site gian hàng vẫn phẳng. Khu vực chưa chuyển giữ CSS Modules + `web/src/styles/tokens.css`, không hard-code màu rải rác và **cấm trộn** Tailwind với CSS Module trong cùng một component. Ngoại lệ (2026-11-26): chỉ trang chủ `/` (`PlatformHome*`, component con trang chủ và nhánh `data-home="true"` của `PlatformHeader`) được dùng gradient, quầng sáng `radial-gradient`, `mask-image` và `box-shadow` qua nhóm token `--home-*`; `(site)`, `(seller)`, `(admin)` và các trang `(shop)` khác ở phần chưa chuyển giữ nguyên luật cũ.
- **Đóng băng thư mục nền và danh sách phụ thuộc UI**: sau phase 0, `web/src/components/ui/` và `web/src/components/app/` đóng băng — không tự chạy `shadcn add` ở swarm khu vực và chỉ dùng đúng các phụ thuộc UI đã duyệt trong ADR-0011 (`tailwindcss`, `@tailwindcss/postcss`, `tw-animate-css`, `class-variance-authority`, `clsx`, `tailwind-merge`, `radix-ui`, `lucide-react`, `cmdk`, `sonner`; kèm `devDependency` `autoprefixer` giữ CSS cũ tới phase 5).
- **Component tương tác phức tạp**: dialog, alert-dialog, sheet, dropdown-menu, popover, select, tooltip, combobox (`command`) dùng primitive Radix qua `web/src/components/ui/` (nối `usePortalContainer` từ `UiScope`), không tự dựng lại modal/combobox thủ công.
- **Kiểm tra trực quan và gate trước khi báo xong**:
  - Màn hình mới hoặc thay đổi bố cục phải chụp ảnh bằng Playwright ở 3 khổ mobile (`390px`), tablet (`768px`) và desktop (`1440px`), sau đó đối chiếu với skill `design-system` và `.agents/briefs/shadcn-redesign/design.md` trước khi báo xong.
  - Chạy xanh toàn bộ gate trong `web/`: `npm run lint`, `npm run format:check`, `npm run check:data`, `npm run build`.
- **Ngôn ngữ cho người mua**: mọi văn bản hiển thị trong `web/src/app/(shop)/`, `web/src/components/`, `web/src/features/{catalog,commerce,product}/` phải viết bằng tiếng Việt tự nhiên cho người mua, không dùng thuật ngữ kỹ thuật nội bộ (JWT, HMAC, Saga, Redis Lua, sandbox, sổ cái ghi kép, optimistic locking).
