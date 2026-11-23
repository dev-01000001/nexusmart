# Đợt 1 – pha 1B – agent `web`

Luật chung: `rules.md`. Đọc `web/CLAUDE.md`, skill `design-system`, `frontend-design`. Chi tiết lỗi: `docs/audit/web.md`, `docs/audit/cat.md`, `docs/audit/shp.md`, `docs/audit/pay.md`.

## Sở hữu
`web/**`, `tests/e2e-web/**`.

## Việc
1. WEB-B2, WEB-B3 (spec dòng 29: combo, mua kèm ngoài phạm vi): GỠ khối "Deal sốc", "Combo & Mua nhiều giảm thêm" ở `/flash-sale`; gỡ ở trang sản phẩm mọi lời hứa giá backend không áp dụng (giảm theo bậc "Mua 2 giảm 10%", quà theo ngưỡng 499.000₫, chọn size từng món combo). Giữ flash sale thật.
2. R7 (SHP-B1, WEB-B1): `/gio-hang` và `/thanh-toan` không gửi `from_province_code`/`from_ward_code` rỗng tới `shipping/calculate-fee`; để backend tự lấy điểm gửi theo `shop_id` (agent `shipping` đang sửa). Phí hiển thị phải bằng phí của đơn tạo ra.
3. CAT-B5, WEB-B6: form sửa sản phẩm seller không cho chọn "Tạm ẩn" khi đang PENDING.
4. PAY-B10, ORD-B11: không hiện nút "Thanh toán lại" cho payment EXPIRED; hiện hướng dẫn đặt lại.
5. WEB-B9: "Mua ngay" flash sale (`?sku=&qty=&flash=`): tạm tính hiển thị phải khớp giá backend sẽ tính khi checkout (kiểm cách `order` áp giá flash; không khớp được thì bỏ hiển thị giá flash ở luồng này).
6. WEB-B4: đăng nhập xong chuyển theo vai trò backend trả về: seller → `/seller`, admin → `/admin`, buyer → trang trước đó hoặc `/`.
7. SHP-B2 phía UI: form giao hàng của seller (`web/src/app/(seller)/seller/don-hang/page.tsx` khoảng dòng 75-76, `[id]/page.tsx` khoảng dòng 57-58) mặc định hãng/dịch vụ của đơn, không cố định `ghn/STANDARD`.

## Kiểm
`npm run lint`, `npm run format:check`, `npm run check:data`, `npm run build` trong `web/`. Thêm test `tests/e2e-web/05_audit_fixes.test.mjs`: trang sản phẩm và `/flash-sale` không còn chữ "Mua 2 giảm", "Combo", "Deal sốc"; đăng nhập seller chuyển `/seller`. Cuối cùng `docker compose up -d --build web` và chạy `make e2e-web` (dán kết quả).
