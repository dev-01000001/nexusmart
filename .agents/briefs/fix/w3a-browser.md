# 3a – nghiệm thu trên trình duyệt thật (manager tự chạy, yêu cầu của chủ dự án 2026-11-24)

Stack audit sau rebuild: web 43000, gateway 48080, Mailpit 48025. Playwright, mỗi luồng ở 1440×900; màn có thay đổi giao diện xem thêm ở 390×844. Mỗi bước ghi: thao tác thật, kết quả thấy, lỗi console, request 4xx/5xx. Ảnh ở `.runs/w3a/browser/`. Dữ liệu: seed + loadgen (> 100 sản phẩm).

| # | Vai | Luồng | Đạt khi |
|---|---|---|---|
| B1 | Khách | `/` và `/flash-sale` khi có > 100 sản phẩm | Số SP mỗi khung = số suất API trả; bấm vào ra PDP giá flash (trước khi sửa: "0 SP") |
| B2 | Khách | Gõ tìm ở header | Gợi ý hiện giá flash/chương trình shop đang chạy |
| B3 | Khách | Xem request mạng của `/`, danh mục, PDP | Không JSON nào có `commission_rate_bps` |
| B4 | Người mua | Đăng nhập → thêm giỏ → checkout voucher nhập tay sai phạm vi, đúng phạm vi | Lỗi theo phạm vi; giảm đúng số tiền |
| B5 | Người mua | Checkout COD vượt hạn mức → đổi VietQR cùng trang → đặt | Không kẹt khoá, đúng một bộ đơn |
| B6 | Người mua | Đặt VietQR rồi để hết hạn 15 phút thật (PAY06) | Đơn hết hạn, kho/voucher/suất trả lại, trang đơn hiện đúng |
| B7 | Người mua | SKU bị người bán ẩn khi đang trong giỏ | Giỏ và checkout báo rõ, không tính giá 0 |
| B8 | Người bán | Khai tài khoản ngân hàng, OTP từ Mailpit; bấm gửi OTP quá 5 lần/phút | Xác minh được; lần thứ 6 báo chờ, không lỗi trắng |
| B9 | Quản trị | Mở duyệt tài khoản ngân hàng ở tab 1, người bán đổi tài khoản ở tab 2, duyệt tab 1 | 409, trang tải lại, báo dữ liệu đã thay đổi; không dòng nào được duyệt nhầm |
| B10 | Quản trị | Duyệt shop khi chủ shop vừa đổi tên | 409 `OWNER_NAME_CHANGED`, tải lại |
| B11 | Quản trị | Màn phí/danh mục | Vẫn thấy và sửa được hoa hồng (qua `/admin/categories`) |
| B12 | Người bán | Đăng ký flash bằng SKU của shop mình | Đăng ký được, chờ duyệt, duyệt xong hiện ở B1 |
