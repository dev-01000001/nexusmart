# 3b-1 – agent `web` (sở hữu `web/**`, `tests/e2e-web/09_*`, `10_*`). Đọc `web/CLAUDE.md`, skill `design-system`, `frontend-design`.
1. A4: form đánh giá trên đơn `COMPLETED`, PDP + thẻ dùng sao thật, bỏ `SAMPLE_REVIEWS` và sao tĩnh; tab "Từ khoá cấm" (xem trước, quét lại theo lô có tiến độ), ô "danh mục cấm" trong màn danh mục; PDP ngừng bán có gợi ý tương tự (`noindex`).
2. Giỏ: dòng "Không còn bán" (E3/E4).
3. A5: `AddressForm` quy đổi địa chỉ cũ; danh sách phường lớn phải tìm/lọc được bằng bàn phím; PDP tính phí bằng API (bỏ `estimateDelivery`, tên hãng giả); xác minh SĐT; COD hiện lý do chặn + lối đi; người bán thấy "Đang tạo vận đơn (tự thử lại)"/lỗi; admin nhập file COD, bỏ tab mẫu `phien_mau`; bỏ hằng phí mẫu (E21).
Không `step` > 1 trên ô số gửi máy chủ; form có bảng desktop + thẻ mobile dùng `noValidate` + kiểm JS + `role="alert"`. Không chữ kiểu quảng cáo. Kiểm: lint, format:check, tsc, check:data, unit; mã thoát thật.
