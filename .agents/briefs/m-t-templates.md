# Swarm T – Template cho site gian hàng
Hợp đồng: `m-all-contract.md`. Hiện trạng: `docs/PHASE2_REPORT.md` (config site JSON strict, 6 section, nháp, xuất bản, khôi phục).
Chủ dự án yêu cầu: nhiều template; người bán đổi template và chỉnh sửa template. Template không nhất thiết là HTML.

1. Biểu quyết định dạng: 3 worker mỗi người đề xuất một định dạng (ví dụ: JSON khai báo section/block/settings kiểu theme; ngôn ngữ template sandbox như Liquid tự viết tập con; MDX giới hạn component). Tiêu chí: an toàn (người bán không chạy được HTML/JS/CSS tùy ý, không XSS, không SSRF qua URL ảnh), dễ cho người bán, render server-side nhanh, kiểm tra được bằng schema, bản sửa có phiên bản. Bỏ phiếu, ghi kết quả và lý do vào `docs/adr/0001-site-template-format.md`.
2. Thư viện ≥ 8 template khác nhau thật sự về bố cục (không chỉ đổi màu): thời trang tối giản, editorial, lưới dày, xưởng thủ công, thể thao, cao cấp, một trang, catalog nhiều SKU. Mỗi template phủ trang chủ, danh sách, chi tiết sản phẩm, giỏ, trang thông tin.
3. Người bán: xem trước và áp dụng template vào bản nháp; trình sửa trực quan (thêm, xóa, sắp xếp section và block, sửa settings, token màu/chữ có kiểm tra tương phản) và chế độ sửa mã template theo định dạng đã chọn, có kiểm tra schema, báo lỗi theo dòng, xem trước trực tiếp. Xuất bản và khôi phục dùng cơ chế phiên bản sẵn có.
4. Backend (`services/user/internal/site/`): validator cho định dạng mới (strict, giới hạn kích thước và độ sâu), migration bảng template, REST theo hợp đồng. Template cũ của 4 site seed chuyển đổi được.
5. Chèn component search/chat/assistant của G vào layout site khi có (đọc `docs/reports/g-web.md`).
Gate: `make lint`, `make test` cho user; web lint, format, `npm run test:unit`, tsc, build (`NEXT_DIST_DIR=.next-t`); test XSS (script, `javascript:` URL, style injection, SVG) bị từ chối.
