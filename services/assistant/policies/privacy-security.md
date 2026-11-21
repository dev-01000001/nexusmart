# Chính sách Bảo mật, Tài khoản & Quyền riêng tư

## Xác thực tài khoản OTP và mã hoá mật khẩu Argon2id

Khi đăng ký tài khoản mới trên NexusMart, người dùng phải **xác thực mã OTP gửi qua email** để kích hoạt tài khoản (nếu mã OTP sai hoặc hết hạn, hệ thống cho phép gửi lại tối đa 3 lần). Khi thay đổi email hoặc số điện thoại trong hồ sơ, người dùng cũng bắt buộc xác thực OTP gửi tới thông tin mới.

Toàn bộ mật khẩu người dùng được băm bảo mật bằng thuật toán **argon2id** với tham số chuẩn, tuyệt đối không lưu trữ mật khẩu dạng văn bản thuần hay ghi mật khẩu vào nhật ký hệ thống. Khi người dùng đặt lại mật khẩu (liên kết khôi phục có hạn 15 phút) hoặc đổi mật khẩu mới, toàn bộ các phiên đăng nhập cũ sẽ bị thu hồi ngay lập tức.

## Phiên đăng nhập JWT và cơ chế khoá tạm khi sai mật khẩu

NexusMart áp dụng cơ chế xác thực hiện đại kết hợp phân quyền chặt chẽ (RBAC) ở cả API Gateway và từng microservice:

- **Access token (JWT)** có thời hạn ngắn **15 phút** để giảm thiểu rủi ro khi lộ phiên.
- **Refresh token xoay vòng (rotation)** được thu hồi ngay sau mỗi lần làm mới hoặc khi đăng xuất.
- Nếu nhập **sai mật khẩu 5 lần liên tiếp**, tài khoản sẽ bị **khoá tạm trong 15 phút** nhằm chống tấn công dò mật khẩu.

Người dùng có thể xem danh sách thiết bị đang đăng nhập và thực hiện đăng xuất từ xa bất cứ lúc nào trong trang Cài đặt bảo mật. Mọi thay đổi nhạy cảm đều được ghi nhận audit log và gửi email thông báo.

## Quyền chủ thể dữ liệu: Xuất dữ liệu cá nhân và Xoá tài khoản (30 ngày)

Người dùng có toàn quyền kiểm soát dữ liệu cá nhân của mình (sau khi xác thực lại bằng mật khẩu hoặc OTP):

- **Yêu cầu xuất dữ liệu cá nhân**: Hệ thống tổng hợp tệp dữ liệu cá nhân của tài khoản và gửi đường dẫn tải về có thời hạn qua email đã đăng ký.
- **Yêu cầu xoá tài khoản**: Tài khoản phải không còn đơn hàng đang xử lý hoặc khiếu nại đang mở. Khi xác nhận xoá, tài khoản chuyển sang trạng thái **chờ xoá trong 30 ngày**. Trong thời gian 30 ngày này, nếu người dùng đăng nhập lại, yêu cầu xoá sẽ tự động được huỷ. Sau khi hết 30 ngày, hệ thống thực hiện **ẩn danh hoá hoàn toàn dữ liệu cá nhân**, chỉ giữ lại chứng từ đơn hàng tối thiểu phục vụ nghĩa vụ kế toán theo pháp luật.

## Nguyên tắc bảo mật và quyền riêng tư của Trợ lý AI

Trợ lý AI trên NexusMart tuân thủ nghiêm ngặt các nguyên tắc an toàn dữ liệu và giới hạn quyền hạn:

- **Chỉ dùng quyền của chính người dùng**: Mọi công cụ (tra cứu đơn hàng, thêm vào giỏ hàng, xem báo cáo gian hàng, chuyển tiếp nhân viên hỗ trợ) đều thực thi bằng chính **JWT của người dùng đang trò chuyện** và tuyệt đối không có quyền riêng, không dùng token quản trị hay truy vấn trực tiếp cơ sở dữ liệu.
- **Cô lập theo gian hàng**: Khi người dùng mua sắm trên trang của một gian hàng cụ thể, trợ lý AI chỉ truy xuất dữ liệu sản phẩm thuộc đúng gian hàng đó.
- **Bảo vệ dữ liệu cá nhân (PII) và phòng chống Prompt Injection**: Email, số điện thoại, token và bí mật kỹ thuật được tự động che trước khi lưu hội thoại hoặc ghi log. Hệ thống áp dụng bộ lọc chống tấn công prompt injection trực tiếp lẫn gián tiếp (trong mô tả sản phẩm hoặc tin nhắn chat) và không bao giờ tiết lộ chỉ dẫn nội bộ hay thực hiện thanh toán tự động.
