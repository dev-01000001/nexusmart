# Đợt 2b – agent `web` (sở hữu `web/**`, `tests/e2e-web/07_promotions.test.mjs`)
Theo `w2b-plan.md` §2–§5, §8, contracts 2b, skill `design-system`.
1. Seller: voucher (sửa/tạm dừng/xoá), chương trình giảm giá (thay tab Combo mock), danh sách flash item theo trạng thái, freeship shop, tài khoản ngân hàng + OTP, sổ giao dịch, báo cáo (chọn ngày, biểu đồ, top sản phẩm, CSV).
2. Admin: voucher sàn + phạm vi danh mục, flash item chờ duyệt, phí sàn/danh mục (ngày hiệu lực, lịch sử), duyệt tài khoản ngân hàng, đơn cần xử lý tay, báo cáo toàn sàn + CSV.
3. Người mua: giá gốc gạch từ dữ liệu thật (chương trình/flash đang chạy), giá flash trong giỏ và thanh toán (chỉ suất đang chạy, hết suất thì nói rõ và cho mua giá thường, không COD với flash, đếm ngược hạn 5 phút), voucher không dùng được có lý do, chuông thông báo + số chưa đọc ở header, tìm kiếm tức thời dùng API, khoảng giá trên thẻ.
4. Gỡ/ghi nhãn trang mock theo §5 (không số dư/hạng giả, không link ID mock), 404 thật, bảng sổ cái ở điện thoại, favicon.
5. Soát 390/768/1440, bàn phím; chart theo skill `dataviz` nếu có trong web (không thêm thư viện).
Test: script đơn vị + e2e-web 07 (vòng đời chương trình shop thấy giá gạch ở PDP và đặt hàng đúng tiền; flash trong giỏ; báo cáo + CSV; tài khoản ngân hàng qua Mailpit).
