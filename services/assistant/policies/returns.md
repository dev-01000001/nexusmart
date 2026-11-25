# Chính sách Đổi trả & Hoàn tiền

## Thời hạn và điều kiện đổi trả

Người mua trên NexusMart có quyền gửi yêu cầu đổi trả và hoàn tiền trong vòng **7 ngày** kể từ thời điểm đơn hàng được giao thành công (trạng thái `delivered`). Nếu sau 7 ngày người mua không xác nhận và không phát sinh khiếu nại, đơn hàng tự động chuyển sang trạng thái hoàn tất (`completed`).

Để yêu cầu đổi trả được xét duyệt nhanh chóng, người mua cần cung cấp **bằng chứng ảnh hoặc video mở hộp** thể hiện rõ tình trạng sản phẩm bị lỗi kỹ thuật, hư hỏng trong quá trình vận chuyển, giao sai phân loại (màu sắc, kích cỡ) hoặc giao thiếu số lượng. Sản phẩm gửi trả phải giữ nguyên tem mác, phụ kiện và bao bì ban đầu nếu không thuộc trường hợp hàng hư hỏng.

## Quy trình phản hồi của người bán

Sau khi người mua gửi yêu cầu đổi trả, hệ thống sẽ chuyển yêu cầu ngay tới gian hàng liên quan. Người bán có thời hạn **3 ngày** để xem xét bằng chứng và lựa chọn một trong ba phương án:

1. Chấp nhận hoàn tiền ngay cho người mua.
2. Chấp nhận yêu cầu và đề nghị người mua gửi trả hàng về kho.
3. Từ chối yêu cầu kèm lý do và bằng chứng đối chứng.

Nếu người bán **không phản hồi trong vòng 3 ngày**, hệ thống sẽ **tự động chấp nhận** yêu cầu đổi trả nhằm bảo vệ quyền lợi người mua.

## Vận đơn chiều ngược và thời hạn gửi trả hàng

Khi yêu cầu trả hàng được chấp nhận, NexusMart tự động tạo **vận đơn chiều ngược** lấy hàng tại địa chỉ của người mua và giao về kho của người bán. Người mua sẽ nhận mã vận đơn kèm hướng dẫn đóng gói trên ứng dụng.

Người mua cần bàn giao kiện hàng cho hãng vận chuyển hoặc gửi tại bưu cục trong thời hạn **5 ngày**. Quá 5 ngày mà người mua không gửi hàng, yêu cầu trả hàng sẽ tự động bị đóng. Khi hàng hoàn về tới kho, người bán có **3 ngày** để kiểm tra và xác nhận tình trạng hàng trước khi hệ thống tiến hành hoàn tiền. Phí vận chuyển chiều trả hàng do bên có lỗi chi trả theo kết luận cuối cùng.

## Khiếu nại lên sàn phân xử

Trong trường hợp người bán từ chối yêu cầu đổi trả, hoặc người bán phản ánh hàng hoàn về không đúng tình trạng thực tế, hai bên có quyền mở **khiếu nại lên sàn NexusMart phân xử**. Quản trị viên sẽ kiểm tra toàn bộ thông tin đơn hàng, bằng chứng ảnh/video và lịch sử trao đổi giữa hai bên để đưa ra quyết định cuối cùng: hoàn tiền toàn phần/một phần, đổi hàng hoặc bác khiếu nại. Nếu hồ sơ thiếu bằng chứng, quản trị viên sẽ yêu cầu bổ sung trong vòng 3 ngày; quá hạn khiếu nại sẽ được đóng.

## Quy trình hoàn tiền qua QR và ví điện tử

Khi đơn hàng đã thanh toán online có quyết định huỷ hợp lệ hoặc đổi trả thành công, hệ thống tự động tạo bản ghi hoàn tiền (refund):

- **Thanh toán qua ví điện tử**: Payment service gọi trực tiếp API hoàn tiền của cổng thanh toán để hoàn lại về ví của người mua.
- **Thanh toán qua chuyển khoản VietQR**: Người mua nhập thông tin tài khoản ngân hàng nhận tiền hoàn; quản trị viên kiểm tra, phê duyệt và thực hiện chuyển khoản hoàn lại.

Số tiền hoàn tuyệt đối không vượt quá số tiền người mua đã thanh toán. Mọi bước hoàn tiền đều được ghi nhận đầy đủ trong sổ cái và nhật ký kiểm toán (audit log).

## Chính sách huỷ đơn hàng

Người mua được phép **huỷ đơn hàng trước khi đơn chuyển sang trạng thái đang giao (`shipping`)**, bao gồm các trạng thái chờ thanh toán (`pending`), đã xác nhận (`confirmed`) và đang chuẩn bị hàng (`preparing`). Khi huỷ đơn hợp lệ, hệ thống tự động huỷ vận đơn chưa lấy hàng, trả lại tồn kho theo SKU, hoàn lại voucher và xu thưởng (nếu còn hạn), đồng thời kích hoạt hoàn tiền nếu đơn đã thanh toán online.

Khi đơn hàng đã chuyển sang trạng thái `shipping`, hệ thống **không cho phép huỷ trực tiếp**. Trong trường hợp này, người mua cần chờ giao hàng để tạo yêu cầu đổi trả trong 7 ngày hoặc trao đổi với nhân viên hỗ trợ.
