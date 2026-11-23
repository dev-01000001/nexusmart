# Đợt 2a – agent `user`
Luật: `w2-rules.md`. Thiết kế: `w2-plan.md` §3.
## Sở hữu
`services/user/**`, `tests/e2e/22_*.go`.
## Việc
1. USR-B1: ghi `user:revoked_before:<user_id>` khi khoá, đổi role (cả lên và xuống), đặt lại mật khẩu; đổi role thu hồi refresh token. Lỗi Redis → thao tác thất bại rõ ràng (không khoá nửa vời).
2. USR-B8: phản hồi đồng nhất (HTTP 200 `{"sent":true}` cho resend/forgot; `verify-otp` sai/không tồn tại/không chờ xác minh cùng một lỗi 400), băm giả khi email không tồn tại, `LOCKED` + mật khẩu đúng → 403 kèm lý do khoá và liên hệ hỗ trợ; LOCKED + mật khẩu sai → 401 như thường. `register` trùng SĐT/email: giữ 409 (bắt buộc để người dùng sửa) nhưng ghi vào báo cáo.
3. UC-B11 mật khẩu: migration `0004_` bảng `password_resets` (hash token, hạn 15 phút, dùng một lần, yêu cầu mới vô hiệu yêu cầu cũ), giới hạn 3 lần/giờ/email, event `user.password_reset_requested` qua đường phát event sẵn có của user. Đổi mật khẩu bằng argon2id như đăng ký, cùng chính sách độ mạnh.
4. `ResendOTP` không tái phát `user.registered` gây trùng chào mừng (PRM-B14): dùng event riêng nếu đã có, nếu không thì giữ event và thêm cờ trong payload cho notification bỏ in-app; báo agent notification.
5. UC-A03: admin không tự khoá mình, không khoá/hạ quyền admin cuối cùng (kiểm, sửa nếu thiếu).
## Test bắt buộc
Unit + E2E `22_account_security_test.go`: khoá user đang đăng nhập → token cũ 401 ngay (cần gateway xong: ghi chú nếu chưa), đăng nhập lại 403 kèm lý do; hạ quyền admin → token cũ 401; quên mật khẩu qua Mailpit → đặt lại → token cũ và refresh cũ vô hiệu, link dùng lần hai bị từ chối, link hết hạn (unit); dò tài khoản: phản hồi giống hệt cho email có/không.
