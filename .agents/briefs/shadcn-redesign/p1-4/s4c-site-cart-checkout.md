# S4c — Site gian hàng: giỏ + thanh toán + flash sale + voucher (phase 4, đợt 2)

Đọc trước: `common.md`, spec §11.1, §11.3 (Giỏ hàng, Thanh toán, Chờ thanh toán, Flash sale / voucher), §12, §13, §15
mục 6–7.
Shell site do S4a làm: chỉ chụp ảnh nghiệm thu khi đã có `signals/site-shell.done`.

## Phạm vi (chỉ những file này; file khác là cấm)
- `web/src/app/(site)/s/[host]/gio-hang/**`, `thanh-toan/**` (gồm `cho/[id]/**`, `mock-zalopay/**`), `flash-sale/**`,
  `voucher/**`
- `web/src/features/commerce/`: `AddressFieldsForm.tsx`, `AddressForm.tsx`, `ProductAddonStrip.tsx`,
  `RetryPaymentBox.tsx`, `VietQrPreview.tsx`, `orderDisplay.tsx`.
- File mới chỉ trong thư mục con mới `web/src/features/commerce/ui/`.
- **Chỉ đọc:**
  - `commerce.module.css` (dùng chung, không xoá), `OrderLookupView.*` (S4d);
  - `addressHelpers.ts`, `cartEnrich.ts`, `checkoutErrors.ts`, `checkoutPayload.ts`, `formFeedback.ts`, `types.ts`,
    `usePageStateParam.ts`;
  - `context/StoreContext.tsx`.

## Export dùng chéo (giữ nguyên tên / props)
- `RetryPaymentBox` (trang chi tiết đơn của S4d dùng).
- `AddressForm` / `AddressFieldsForm` (sổ địa chỉ của S4d có thể dùng).

## Việc
- **Giỏ hàng**, theo `cart_layout`:
  - dòng có ảnh, biến thể (`Badge` phụ), −/+ và xoá;
  - SKU không còn bán: dòng mờ + `Alert` giải thích;
  - tóm tắt đơn ở cột phải, dính khi cuộn.
- **Thanh toán (2 cột):**
  - **trái:** liên hệ; địa chỉ 2 cấp tỉnh / phường bằng ô chọn có tìm kiếm (`Popover` + `Command`); giao hàng; phương
    thức COD / VietQR / ZaloPay là thẻ `RadioGroup`;
  - **phải:** tóm tắt đơn + mã giảm giá;
  - **mobile:** tóm tắt thành `Accordion` "Xem đơn hàng · <tổng>" ở đầu trang; nút [Đặt hàng] full width;
  - giữ `Idempotency-Key`, xử lý lỗi `checkoutErrors`, chuyển hướng sang chờ thanh toán / đơn đã tạo;
  - giữ neo `#xac-minh-sdt` trỏ sang tài khoản.
- **Chờ thanh toán (`cho/[id]`):**
  - thẻ giữa trang: mã QR VietQR, số tiền, nội dung chuyển khoản (nút sao chép);
  - trạng thái SSE bằng `Badge` (warning → success); đếm ngược;
  - giữ logic SSE / thử lại (`RetryPaymentBox`).
- `mock-zalopay`: chuyển giao diện, giữ hành vi (chỉ bật ở chế độ mock).
- **Flash sale:** lưới thẻ + đồng hồ đếm ngược + `Progress` đã bán.
- **Voucher:** lưới thẻ + [Lưu mã] / [Sao chép].
- **Ngân sách JS:** thành phần nặng ít dùng (`Command` chọn địa chỉ…) nạp bằng `next/dynamic` nếu làm tăng First Load
  đáng kể.

## Xong khi
- `rg "module.css|styles\." <phạm vi>` rỗng, trừ phần chỉ đọc dùng chung.
- Lệnh kiểm `common.md` §4 xanh trong phạm vi.
- Ảnh 390 / 768 / 1440 trên 4 site seed, đăng nhập người mua:
  - giỏ có hàng, giỏ trống, giỏ có SKU không còn bán (nếu tạo được);
  - thanh toán (desktop và `Accordion` mobile);
  - chờ thanh toán VietQR;
  - `/flash-sale`, `/voucher`; một mẫu tối.
- Luồng đã đi bằng tài khoản người mua, **trên cả 4 site**:
  - thêm hàng → giỏ −/+ / xoá;
  - thanh toán **COD** với địa chỉ thật (tìm tỉnh / phường trong ô chọn) → đơn được tạo;
  - một lần **VietQR** tới trang chờ thanh toán, thấy QR + nút sao chép;
  - áp mã giảm giá hợp lệ / không hợp lệ;
  - lưu / sao chép voucher.
  - Ghi mã đơn đã tạo.
