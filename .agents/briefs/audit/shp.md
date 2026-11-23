# Worker `shp` – vận chuyển

Luật chung: `.agents/briefs/audit/rules.md`. Báo cáo: `docs/audit/shp.md`, mã `SHP-B<n>`, `SHP-D<n>`, `SHP-F<n>`. Đọc thêm skill `integrate-provider`.

## UC phụ trách
UC-SHIP01 (đơn vị hành chính 2 cấp 34 tỉnh, bảng ánh xạ cũ → mới; phần CRUD sổ địa chỉ do `usr` kiểm), UC-SHIP02, UC-SHIP03, UC-SHIP04, UC-SHIP05, UC-SHIP06, UC-SHIP07, UC-SHIP08, UC-A12.

## Đối chiếu Shopify/sàn
- Giao hàng: vùng và biểu phí, phí tính theo hãng, mua nhãn vận chuyển, giao nội thành, nhận tại cửa hàng, giao một phần, tracking, tích hợp 3PL.

## Phải kiểm trên stack (buyer riêng của mình)
- Danh sách tỉnh/phường (`GET /api/v1/shipping/provinces` ...): đúng 34 tỉnh mới, tên tiếng Việt chuẩn; ánh xạ 63 tỉnh cũ → 34 mới (thử vài tỉnh đã sáp nhập); phường/xã đủ đến mức nào (spec cần 2 cấp).
- Tính phí khi checkout theo từng shop (cân nặng SKU, địa chỉ lấy hàng của shop, địa chỉ nhận), hai hãng (GHN mock và hãng thứ hai/mock) qua interface Carrier chung; timeout, retry backoff, circuit breaker (đọc code, dẫn file:dòng).
- Tạo vận đơn khi seller xử lý đơn, in nhãn (`label_url`), huỷ vận đơn, vận đơn trả hàng, giao thất bại và hoàn hàng.
- Webhook trạng thái: chữ ký sai → từ chối, `X-Timestamp` lệch > 300 giây → từ chối, cùng `event_id` gửi nhiều lần → xử lý 1 lần, trạng thái đơn bên `order` đổi theo (DELIVERED → đơn hoàn tất?), job polling dự phòng.
- Cấu hình vận chuyển của gian hàng (bật/tắt hãng), admin cấu hình hãng.
- Đối soát COD với hãng (phối hợp `pay`; chỉ ghi phần shipping cung cấp dữ liệu gì).
- Dữ liệu: địa chỉ lấy hàng của 4 shop seed hợp lệ; phí ship hiển thị ở web khớp backend; trạng thái vận đơn ở web (`/don-hang/[id]`, seller) khớp `shipping_db`.
