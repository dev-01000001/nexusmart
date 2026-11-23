# Worker `web` – giao diện web chạy thật (người mua, người bán, quản trị)

Luật chung: `.agents/briefs/audit/rules.md`. Báo cáo: `docs/audit/web.md`, mã `WEB-B<n>`, `WEB-D<n>`, `WEB-F<n>`. Đọc thêm `web/CLAUDE.md`, skill `design-system`.

## Phạm vi
Toàn bộ route trong `web/src/app` (nhóm `(shop)`, `(seller)`, `(admin)`) chạy trên `http://localhost:43000` (bản build production trong compose). Các worker khác kiểm nghiệp vụ backend; bạn kiểm cái người dùng thấy: trang nào lỗi, trang nào còn dữ liệu mẫu, dữ liệu hiển thị có khớp backend không.

## Phải kiểm
- Lập bảng mọi route: `| Route | Vai trò | HTTP | Nguồn dữ liệu (gateway thật / mock / tĩnh) | Lỗi | Ghi chú |`. Mỗi route gọi thật với đúng vai trò: khách vãng lai, buyer (tự đăng ký buyer riêng qua BFF của web, cookie phiên `HttpOnly`), seller seed, admin seed. Đăng nhập qua BFF: xem `web/src/app/api/auth/**` và `tests/e2e-web/helpers.mjs`.
- Trang render phía server: dùng `curl` lấy HTML và đối chiếu nội dung (tên, giá, tồn kho, trạng thái) với API gateway. Phần render phía client: được dùng Chrome headless có sẵn (`google-chrome --headless=new --disable-gpu --user-data-dir=/tmp/audit-web-chrome --dump-dom <url>` hoặc `--screenshot=/tmp/audit-web-*.png --window-size=1440,900`), không cài thêm gì. Nếu không chạy được trình duyệt thì ghi CHƯA KIỂM cho phần client.
- Dữ liệu mẫu còn sót: `rg` import từ `web/src/mock`, `web/src/data`, `sellerStore`, `auditStore`, `localStorage` trong code chạy thật; trang nào hiển thị số liệu không có ở backend (doanh thu, đánh giá, hội viên, chat, trợ lý AI...). Phân biệt rõ: dữ liệu tĩnh hợp lệ (nội dung chính sách, văn bản) và số liệu giả đóng vai dữ liệu thật.
- Hiển thị: giá VND đúng định dạng và khớp `*_vnd`, % giảm đúng, ảnh có tải được không (HTTP 200 cho `/products/...`, `/models/...`, `_next/image`), link hỏng (404 nội bộ), chữ cho người mua là tiếng Việt tự nhiên, không lộ thông điệp lỗi kỹ thuật/gateway.
- Bảo vệ trang: buyer vào `/seller/*`, `/admin/*`; khách vào trang tài khoản/đơn hàng; seller vào `/admin/*`. Mutation BFF có kiểm `Origin` (CSRF) không.
- Luồng chính qua web (dùng route BFF như trình duyệt sẽ gọi): đăng ký → đăng nhập → tìm → giỏ → checkout COD → xem đơn; seller xem đơn mới; admin xem trang duyệt. Chỉ 1–2 đơn.
- Chạy `npm run lint`, `npm run format:check`, `npm run check:data` trong `web/` và dán kết quả. Không `npm run build`, không `npm install`.
