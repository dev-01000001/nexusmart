---
paths:
  - "web/**"
---

# Quy tắc Frontend (`web/`)

- **Chỉ dùng design token từ `tokens.css`**: mọi màu sắc, font chữ (`--font-sans`, `--step-*`, `--fs-*`), spacing/layout (`--margin`, `--gutter`, `--gap`, `--pad`, `--card-pad`...), radius (`--radius-*`) và motion (`--ease-*`, `--t-*`) phải đọc từ `web/src/styles/tokens.css`. Thiếu giá trị cần dùng thì thêm token mới vào `web/src/styles/tokens.css`, không hard-code rải rác trong CSS Modules. Lưu ý: repo hiện không có token shadow vì giữ quy tắc bề mặt phẳng (không shadow tĩnh, không gradient — xem `web/src/styles/tokens.css` và `web/DESIGN_REPORT.md`).
- **Không thêm Tailwind hay thư viện UI**: giữ CSS Modules + `tokens.css`/`globals.css`; không cài Tailwind CSS, thư viện UI component, bộ icon hay thư viện state ngoài khi chưa có ADR được duyệt.
- **Component tương tác phức tạp**: dialog phức tạp, combobox, date picker cần chờ ADR chọn thư viện headless; chưa có ADR thì không tự cài gói mới hay dựng lại phức tạp ngoài HTML ngữ nghĩa tối giản.
- **Kiểm tra trực quan và gate trước khi báo xong**:
  - Màn hình mới hoặc thay đổi bố cục phải chụp ảnh bằng Playwright ở cả mobile (`390px`) và desktop (`1440px`), sau đó đối chiếu với art direction ở `.taste-skill/` và skill `design-system` trước khi báo xong.
  - Chạy xanh toàn bộ gate trong `web/`: `npm run lint`, `npm run format:check`, `npm run check:data`, `npm run build`.
- **Ngôn ngữ cho người mua**: mọi văn bản hiển thị trong `web/src/app/(shop)/`, `web/src/components/`, `web/src/features/{catalog,commerce,product}/` phải viết bằng tiếng Việt tự nhiên cho người mua, không dùng thuật ngữ kỹ thuật nội bộ (JWT, HMAC, Saga, Redis Lua, sandbox, sổ cái ghi kép, optimistic locking).
