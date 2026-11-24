# 3b-1 – agent `notification` (sở hữu `services/notification/**`)
E1 (giữ `sanitizeDLQPayload`), rồi E19: mail SMS mock cho `user.phone_otp_requested` (tiêu đề không có mã, thêm subject vào nhóm nhạy cảm), thông báo người bán khi job vận đơn `FAILED` nếu hợp đồng có event. Test: lúc tắt máy không vào DLQ; DLQ hai nơi lỗi → không Ack; khởi động lại không phát lại.
