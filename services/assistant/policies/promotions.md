# Chính sách Khuyến mãi, Voucher, Xu & Flash Sale

## Thứ tự áp dụng khuyến mãi khi thanh toán

Tại bước checkout, Promotion service tự động kiểm tra điều kiện hợp lệ và tính toán mức giảm giá theo **thứ tự áp dụng cố định**:

1. **Voucher gian hàng** (giảm trực tiếp trên tạm tính của từng gian hàng).
2. **Voucher sàn** (áp dụng sau khi đã trừ voucher gian hàng).
3. **Mã freeship** (giảm phí vận chuyển).
4. **Xu thưởng** (trừ trực tiếp vào tổng tiền còn lại theo số dư xu người mua chọn dùng).

Chi tiết từng khoản giảm giá và bên chịu chi phí (sàn hay gian hàng) đều được lưu rõ ràng trên đơn hàng và biên nhận.

## Quy định kết hợp voucher trên một đơn hàng

Trên mỗi giao dịch checkout, người mua có thể kết hợp đồng thời:

- Tối đa **1 voucher sàn**.
- Tối đa **1 voucher của mỗi gian hàng** có sản phẩm trong đơn.
- Tối đa **1 mã freeship**.
- Kết hợp bật dùng **xu thưởng** nếu tài khoản có số dư khả dụng.

Mỗi voucher đều hiển thị rõ điều kiện đơn tối thiểu, mức giảm tối đa, lượt sử dụng còn lại và thời hạn. Hệ thống sẽ chỉ rõ số tiền còn thiếu nếu đơn hàng chưa đạt giá trị tối thiểu để áp dụng mã.

## Lưu voucher, tích xu và hoàn voucher/xu khi huỷ đơn

Người mua có thể lưu voucher vào ví voucher từ trang chủ, trang gian hàng hoặc khung chat (hệ thống kiểm tra giới hạn lượt lưu mỗi tài khoản và không lưu trùng). Xu thưởng được tích luỹ khi đơn hàng hoàn tất (`completed`) hoặc khi đánh giá sản phẩm được duyệt, được ghi vào sổ giao dịch xu kèm hạn sử dụng rõ ràng.

Khi đặt hàng, voucher và xu được giữ (reserve) đồng bộ trong cùng saga của đơn hàng. Nếu **đơn hàng bị huỷ hợp lệ** hoặc tự huỷ do quá hạn thanh toán 15 phút, **voucher và xu thưởng đã sử dụng sẽ được hoàn lại vào tài khoản người mua nếu vẫn còn thời hạn sử dụng**. Ngược lại, nếu đơn đã hoàn tất nhưng sau đó phát sinh trả hàng hoàn tiền, số xu đã cộng từ đơn hàng đó sẽ bị thu hồi tương ứng.

## Chương trình Flash Sale theo khung giờ

Flash Sale là chương trình giảm giá sốc theo các khung giờ cố định do sàn tổ chức, với các sản phẩm đã được quản trị viên kiểm duyệt giá và khoá tồn kho trước giờ mở bán. Giá Flash Sale phải thấp hơn mức giá thấp nhất trong 30 ngày gần nhất và được ưu tiên nếu trùng thời gian với khuyến mãi thường của gian hàng.

Để bảo đảm công bằng tuyệt đối khi lưu lượng truy cập tăng đột biến:

- Hệ thống thực hiện **trừ suất nguyên tử (atomic) bằng Lua script trên Redis**, cam kết không bán vượt số suất đã công bố (no oversell).
- Mỗi chương trình áp dụng **giới hạn số lượng mua tối đa trên mỗi tài khoản** để tránh đầu cơ gom hàng.
- Đơn hàng Flash Sale cần được thanh toán đúng hạn; nếu quá hạn thanh toán, đơn sẽ bị huỷ và suất được trả lại vào quỹ Flash Sale.
