# Chính sách Thanh toán & COD

## Thanh toán khi nhận hàng (COD) và hạn mức 5.000.000 VNĐ

NexusMart hỗ trợ hình thức **thanh toán khi nhận hàng (COD)** cho người mua đã đăng nhập và xác thực số điện thoại bằng mã OTP. Giá trị đơn hàng **COD tối đa là 5.000.000 VNĐ** (5 triệu đồng). Với các đơn hàng vượt quá hạn mức 5.000.000 VNĐ hoặc giao tới khu vực hãng vận chuyển không hỗ trợ thu hộ, người mua cần lựa chọn phương thức thanh toán trực tuyến.

Đối với đơn COD hợp lệ, đơn hàng chuyển thẳng sang trạng thái đã xác nhận (`confirmed`) sau khi giữ tồn kho thành công. Shipper sẽ thu tiền mặt trực tiếp khi giao hàng và đối soát theo phiên với sàn.

## Kiểm soát rủi ro bom hàng COD

Để hạn chế đơn ảo và bảo vệ người bán khỏi thiệt hại chi phí vận chuyển hai chiều, NexusMart áp dụng cơ chế kiểm soát rủi ro COD theo tài khoản. Nếu một tài khoản có **quá 3 lần bom hàng** (từ chối nhận đơn COD hoặc không liên lạc được dẫn tới hoàn hàng), hệ thống sẽ **tự động tắt phương thức COD** đối với tài khoản đó và hiển thị rõ lý do tại bước thanh toán.

Tài khoản bị tắt COD vẫn có thể mua sắm bình thường trên sàn bằng phương thức thanh toán trước (VietQR hoặc ví điện tử).

## Thanh toán chuyển khoản VietQR qua payOS và SePay

Người mua có thể thanh toán trực tuyến nhanh chóng bằng **mã QR chuyển khoản VietQR** tích hợp qua cổng **payOS hoặc SePay**. Khi đặt đơn, hệ thống tạo mã VietQR kèm chính xác số tiền thanh toán và nội dung chứa mã đơn hàng.

- **Thời hạn thanh toán là 15 phút** kể từ thời điểm tạo đơn hàng (hiển thị đồng hồ đếm ngược trên màn hình checkout).
- Khi người mua quét mã chuyển khoản thành công, cổng thanh toán gửi webhook về Payment service (kiểm tra chữ ký HMAC, chống phát lại replay và xử lý idempotent theo mã giao dịch). Đơn hàng tự động chuyển sang `confirmed` và cập nhật tức thì qua SSE.

## Tự động huỷ đơn quá hạn và thanh toán lại đơn chờ

Nếu **quá 15 phút** mà giao dịch thanh toán online vẫn chưa hoàn tất, job nền của hệ thống sẽ kiểm tra lại trạng thái lần cuối với cổng thanh toán. Nếu chưa thanh toán, saga sẽ **tự động huỷ đơn hàng, trả lại tồn kho theo SKU**, đồng thời hoàn lại voucher và xu thưởng còn hạn.

Trong thời gian đơn ở trạng thái chờ thanh toán (`pending`) và chưa quá hạn, người mua có thể bấm **thanh toán lại** ở trang đơn hàng. Hệ thống sẽ huỷ giao dịch cũ chưa hoàn tất và tạo mã QR mới có hạn 15 phút. Lưu ý: màn hình thanh toán lại không cho phép chuyển từ thanh toán online sang COD; nếu muốn dùng COD, người mua cần huỷ đơn cũ và tạo đơn checkout mới.

## Hoàn tiền, đối soát và giới hạn an toàn của Trợ lý AI

Mọi số tiền trên NexusMart được tính toán và lưu trữ bằng **số nguyên VNĐ** trong hệ thống **sổ cái ghi kép (double-entry ledger)**, bảo đảm tổng Nợ luôn bằng tổng Có. Hệ thống chạy đối soát tự động hằng ngày với cổng thanh toán online và theo phiên với hãng vận chuyển COD. Trường hợp người mua chuyển khoản thiếu tiền, thừa tiền hoặc sai nội dung chuyển khoản sẽ không tự động xác nhận đơn mà được chuyển sang hàng đợi đối soát tay của kế toán sàn để xử lý hoặc hoàn tiền.

**Quy định an toàn bắt buộc**: Trợ lý AI trên NexusMart **không bao giờ tự động đặt hàng (checkout) hay tự thực hiện thanh toán hộ người dùng**. Trợ lý AI chỉ hỗ trợ tư vấn, tra cứu đơn và thêm sản phẩm vào giỏ sau khi người mua xác nhận rõ ràng; mọi giao dịch tiền tệ đều phải do chính người mua thực hiện trên trang thanh toán chính thức.
