# Đợt 2a – agent `web`
Luật: `w2-rules.md`, `web/CLAUDE.md`, skill `design-system`, `frontend-design`. Hợp đồng REST: `docs/contracts.md`.
## Sở hữu
`web/**`, `tests/e2e-web/06_returns.test.mjs`.
## Việc (dùng component, token, mẫu trang có sẵn; không thêm thư viện; tiếng Việt tự nhiên, không enum thô)
1. Người mua `/don-hang/[id]`: yêu cầu trả hàng (lý do, tối đa 5 URL bằng chứng, ghi rõ chưa hỗ trợ tải ảnh trực tiếp), tiến trình trả hàng (timeline, hạn kế tiếp, mã vận đơn trả), mở khiếu nại khi bị từ chối, bổ sung bằng chứng, yêu cầu giao lại khi giao thất bại, trạng thái hoàn tiền và form nhập tài khoản nhận. Đơn `PREPARING` có vận đơn vẫn huỷ được (báo lỗi 409 dễ hiểu nếu hãng đã lấy).
2. Người bán: `/seller/don-hang` và chi tiết theo luồng mới (tạo vận đơn → "Chờ hãng lấy hàng" → "Đang giao"); trang `/seller/tra-hang` (danh sách theo trạng thái, hạn phản hồi, chấp nhận hoàn tiền / yêu cầu trả hàng / từ chối, xác nhận đã nhận hàng, báo hàng sai); `/seller/ho-so` thêm cấu hình vận chuyển; `/seller/tai-chinh` sửa PAY-B12: số dư chờ có thời gian giữ, công thức đúng.
3. Quản trị: `/admin/khieu-nai` dùng dữ liệu thật (danh sách, chi tiết hai bên, quyết định, bên chịu phí); `/admin/doi-soat` thêm hàng đợi giao dịch không khớp và hàng đợi hoàn tiền chờ chuyển khoản; `/admin/cau-hinh` thêm hãng vận chuyển. Ngày đối soát mặc định theo giờ VN.
4. Tài khoản: `/quen-mat-khau`, `/dat-lai-mat-khau`, liên kết từ đăng nhập; tài khoản bị khoá hiện lý do; token bị thu hồi (401) → đăng xuất sạch và về trang đăng nhập.
5. ORD-B12, USR-B10 phần web: nhãn tiếng Việt timeline, tab RETURNED, điều hướng theo role.
6. Bàn phím và trợ năng cơ bản cho form/hộp thoại mới; soát Playwright 390/768/1440 theo `design-system`.
## Test bắt buộc
`06_returns.test.mjs`: người mua yêu cầu trả → người bán chấp nhận → webhook RETURN delivered → xác nhận → người mua thấy hoàn tiền và nhập tài khoản → admin tất toán; quên mật khẩu qua Mailpit. Thêm kiểm 390px không tràn ngang cho các trang mới. Backend phần nào chưa xong thì ghi rõ.
