# C – Mua hàng và tài khoản (trên site) + form xác thực
Đọc `design.md`, `f-handoff.md`, toàn bộ `tests/playwright/specs/buyer-flow.spec.ts`.

## Sở hữu
`web/src/features/{commerce,account}/**` (chỉ giao diện; không đổi logic `checkoutPayload.ts`, `checkoutErrors.ts`, `*.server.ts`, `*Types.ts`, test), `web/src/app/(site)/s/[host]/{gio-hang,thanh-toan,don-hang,tai-khoan,tra-cuu-don,tra-cuu-don-hang,dang-nhap,dang-ky}/**` + CSS cạnh.

## Việc
1. Giỏ: danh sách lớn ảnh + tên + biến thể + bước số lượng; tóm tắt dính; xoá có hoàn tác 5s; hàng rời trượt và thu gọn; rỗng: 1 câu + 1 nút. Giữ `aside[aria-label="Tóm tắt đơn hàng và mã ưu đãi"]`.
2. Thanh toán: một trang, 3 chặng tự mở/đóng (Địa chỉ → Giao hàng → Thanh toán), chặng xong thu thành 1 dòng có nút Sửa; cột đơn hàng dính. Phương thức là ô lớn. Nút đặt hàng hiển thị số tiền; trạng thái gửi không nhảy bề rộng. Giữ mọi id/role/nhãn test. Không che bớt thông tin phí/tổng.
3. Chờ thanh toán VietQR: QR lớn giữa màn, đếm ngược dạng vòng, số tiền + nội dung chuyển khoản có nút chép, thành công chuyển cảnh.
4. Đơn hàng: danh sách thẻ ngang ảnh sản phẩm, tab trạng thái có gạch chạy; chi tiết: dòng thời gian trạng thái trực quan, hành động chính nổi, phần phụ trong `<details>`; trả hàng là sheet từng bước.
5. Tài khoản: nav trái gọn, mỗi mục một việc; sổ địa chỉ thẻ + sheet sửa. Tra cứu đơn: 2 ô lớn giữa màn.
6. Đăng nhập/đăng ký/OTP: ô OTP 6 số rời tự nhảy (vẫn giữ `#otp-code` nhận `fill`), nhãn nổi, hiện/ẩn mật khẩu, lỗi nằm dưới ô + rung nhẹ ≤ 4px (tắt khi reduced-motion).

## Nghiệm thu
Playwright buyer-flow của QA phải xanh không sửa test. Form dùng được bằng bàn phím, `autocomplete` đúng, lỗi gắn `aria-describedby`.
