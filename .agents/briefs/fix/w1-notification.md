# Đợt 1 – pha 1B – agent `notification`

Luật chung: `rules.md`. Chi tiết lỗi: `docs/audit/prm.md` (PRM-B5, B6, B7), `docs/audit/usr.md` (USR-B9), `docs/audit/ord.md` (ORD-B10).

## Sở hữu
`services/notification/**`.

## Việc
1. R2 phía thông báo (PRM-B5): đơn COD lúc đặt báo "Đặt hàng thành công, thanh toán khi nhận hàng"; không còn "thanh toán ... qua COD thành công" lúc đặt. `payment.succeeded` của COD (payment sẽ phát khi đơn đã giao và thu tiền) báo đã thu tiền.
2. PRM-B7: tiêu đề email OTP không chứa mã (ví dụ "Mã xác minh tài khoản NexusMart"); nội dung OTP và mọi email giao dịch bằng tiếng Việt tự nhiên; số tiền định dạng `256.500₫`; trạng thái/phương thức hiển thị nhãn tiếng Việt thay vì enum thô.
3. PRM-B6, USR-B9, ORD-B10: link trong thông báo in-app trỏ tới route web có thật (liệt kê `web/src/app` để lấy route: chi tiết đơn dùng `order_id`, không dùng `checkout_group_id`; trang seller; trang xác minh OTP thật). Không sửa `web/`.

## Test bắt buộc
Unit test: nội dung/tiêu đề theo phương thức COD vs VIETQR; tiêu đề OTP không chứa chuỗi 6 chữ số; định dạng tiền; link đơn dùng `order_id`.
