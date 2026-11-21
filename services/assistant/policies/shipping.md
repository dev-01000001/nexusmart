# Chính sách Vận chuyển & Địa chỉ

## Hãng vận chuyển tích hợp (GHN và GHTK)

NexusMart hợp tác với hai hãng vận chuyển tiêu chuẩn thông qua kiến trúc adapter thống nhất:

- **GHN (Giao Hàng Nhanh)**: hãng vận chuyển chính hỗ trợ tính phí, tạo vận đơn tự động, thu hộ COD và đồng bộ trạng thái hành trình theo thời gian thực qua webhook.
- **GHTK (Giao Hàng Tiết Kiệm)**: hãng vận chuyển thứ hai mang lại thêm lựa chọn chi phí và thời gian giao hàng phù hợp cho từng khu vực.

Các gian hàng cần bật ít nhất một hãng vận chuyển và khai báo địa chỉ kho lấy hàng hợp lệ. Trạng thái vận đơn được chuẩn hoá thành các mốc: đang lấy hàng (`picking`), đang giao (`delivering`), đã giao (`delivered`), giao thất bại (`failed`), đang hoàn (`returning`) và đã hoàn (`returned`).

## Sổ địa chỉ 2 cấp 34 tỉnh thành và chuyển đổi địa chỉ cũ

Sổ địa chỉ trên NexusMart tuân thủ danh mục hành chính **2 cấp gồm 34 tỉnh thành và phường/xã** mới nhất. Khi thêm địa chỉ nhận hàng hoặc địa chỉ kho, người dùng chỉ cần chọn tỉnh/thành phố, chọn phường/xã, nhập số nhà/tên đường và thông tin người nhận.

Đối với người dùng quen nhập **địa chỉ kiểu cũ (có quận/huyện)**, hệ thống tích hợp **bảng mapping địa chỉ cũ sang mới** để tự động gợi ý đơn vị hành chính tương ứng. Hệ thống đồng thời lưu cả mã địa chỉ mới và mã chuyển đổi của từng hãng vận chuyển để đảm bảo tạo vận đơn chính xác ngay cả khi đối tác chưa đồng bộ hoàn toàn danh mục mới.

## Tính phí vận chuyển và chính sách Freeship

Tại bước checkout, Shipping service gọi song song API của các hãng vận chuyển đang bật để tính phí riêng cho từng gian hàng dựa trên:

- Tuyến giao hàng (từ địa chỉ kho gian hàng đến địa chỉ nhận của người mua).
- Tổng cân nặng và kích thước ước tính của các sản phẩm trong đơn.
- Gói dịch vụ giao hàng tiêu chuẩn mà người mua lựa chọn.

Nếu một hãng tạm thời quá tải hoặc timeout, hệ thống sẽ tự động bỏ qua hãng đó bằng circuit breaker; trường hợp toàn bộ hãng gặp sự cố, sàn áp dụng bảng phí dự phòng theo vùng và cân nặng đã cấu hình sẵn. Người mua có thể giảm hoặc miễn phí vận chuyển bằng **mã freeship của sàn** hoặc hưởng **chính sách freeship theo giá trị đơn tối thiểu** của từng gian hàng.

## Sửa địa chỉ nhận hàng sau khi đặt đơn

Người mua được phép **sửa địa chỉ nhận hàng hoặc số điện thoại người nhận khi đơn hàng chưa chuyển sang trạng thái `shipping`** (chưa bàn giao cho shipper lấy hàng). Khi thay đổi địa chỉ:

- Hệ thống tự động tính lại phí vận chuyển theo tuyến mới. Nếu phí mới cao hơn, người mua cần xác nhận phần chênh lệch hoặc giữ nguyên địa chỉ cũ.
- Nếu đơn đã có vận đơn, hệ thống sẽ cập nhật địa chỉ sang hãng vận chuyển (hoặc huỷ vận đơn cũ và tạo lại vận đơn mới nếu hãng không hỗ trợ sửa trực tiếp).

Khi đơn hàng đã ở trạng thái `shipping`, hệ thống từ chối sửa địa chỉ trực tiếp; người mua cần liên hệ hãng vận chuyển theo mã vận đơn để được hỗ trợ.

## Xử lý giao hàng thất bại và hoàn hàng

Khi shipper giao hàng không thành công (vận đơn chuyển sang `failed`), hệ thống gửi thông báo ngay cho cả người mua và người bán để hẹn lịch giao lại. Nếu vượt quá số lần giao lại tối đa theo quy định của hãng, kiện hàng sẽ được **hoàn về kho người bán** (`returning` -> `returned`).

Khi hàng hoàn về kho thành công, tồn kho SKU được cộng lại tự động. Với đơn đã thanh toán online, hệ thống khởi tạo quy trình hoàn tiền cho người mua. Với đơn COD bị người nhận từ chối nhận hàng không có lý do hợp lệ, hệ thống ghi nhận lịch sử bom hàng vào tài khoản người mua.
