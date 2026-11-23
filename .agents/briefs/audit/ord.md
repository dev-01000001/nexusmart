# Worker `ord` – giỏ hàng, checkout, đơn hàng, trả hàng, khiếu nại

Luật chung: `.agents/briefs/audit/rules.md`. Báo cáo: `docs/audit/ord.md`, mã `ORD-B<n>`, `ORD-D<n>`, `ORD-F<n>`. Đọc thêm skill `checkout-saga`.

## UC phụ trách
UC-B05, UC-B06, UC-B08, UC-B09, UC-B17, UC-B26, UC-S04, UC-S10, UC-S15, UC-A05, UC-PAY02, UC-PAY06, UC-PAY08, UC-A16.

## Đối chiếu Shopify/sàn
- Giỏ hàng và checkout: giỏ hàng, checkout, thanh toán nhanh, áp mã giảm giá, tính thuế và phí vận chuyển, khôi phục giỏ bỏ dở, draft order, upsell sau mua.
- Đơn hàng: vòng đời đơn, sửa và huỷ đơn, trạng thái thanh toán và giao hàng, định tuyến đơn theo kho, trả hàng và đổi hàng, store credit.
- Đơn nhiều người bán (sàn): tách một lần checkout thành nhiều đơn con, phí vận chuyển theo từng shop.
- Tranh chấp (sàn): khiếu nại giữa người mua và người bán, sàn làm trọng tài, hoàn tiền.

## Phải kiểm trên stack (buyer riêng của mình)
- Giỏ: thêm/sửa số lượng/xoá, vượt tồn, SKU không tồn tại hoặc sản phẩm chưa duyệt, giỏ gộp theo shop, giữ giỏ qua đăng xuất/đăng nhập.
- Checkout 2 shop một lần (COD và VietQR): tách đơn con theo shop, phí ship từng shop, tổng tiền = hàng + ship − giảm giá (đối chiếu từng con số trong `order_db` và payment), giữ tồn kho (`reserved_stock` tăng), outbox event.
- `Idempotency-Key`: gửi lại cùng key cùng payload → cùng kết quả, không đơn trùng; cùng key KHÁC payload → phải 409/422 (báo cáo 0b nói đang trả kết quả cũ: kiểm lại).
- Máy trạng thái: thử chuyển trạng thái không hợp lệ (huỷ đơn đã giao, xác nhận nhận hàng khi chưa giao...), seller xác nhận/chuẩn bị/giao, buyer xác nhận đã nhận (UC-B17: tiền cho seller, xu?), tự huỷ quá hạn thanh toán (watchdog; có thể đọc code + kiểm đơn quá hạn nếu đợi được), huỷ đơn hoàn tồn kho và hoàn voucher/xu.
- COD: hạn mức `ORDER_COD_MAX_VND`, chặn người bom hàng (`ORDER_COD_MAX_REJECTED_COUNT`).
- Trả hàng/hoàn tiền: buyer yêu cầu → seller/admin duyệt/từ chối → hoàn tiền; khiếu nại admin `/admin/khieu-nai`. Sửa địa chỉ sau đặt. Biên nhận đơn. Tra cứu đơn vãng lai `/tra-cuu-don-hang` còn dữ liệu mẫu không.
- `orders.payment_id` sau `POST /api/v1/payments/{id}/retry` (báo cáo 0b nói không cập nhật: kiểm lại).
- Phân quyền: buyer A xem/huỷ đơn buyer B; seller xem đơn shop khác; seller sửa trạng thái đơn shop khác.
- Dữ liệu: đơn hiển thị ở web (`/don-hang`, `/don-hang/[id]`, seller `/seller/don-hang`) khớp DB (tiền, số lượng, trạng thái, địa chỉ, tên SKU, ảnh).
