# Đợt 1 – pha 1B – agent `shipping`

Luật chung: `rules.md`. Chi tiết lỗi: `docs/audit/shp.md`. Skill: `integrate-provider`.

## Sở hữu
`services/shipping/**`, `tests/e2e/17_*.go`.

## Việc
1. R7 (SHP-B1): `CalculateFee` có `shop_id` mà thiếu `from_province_code` → tự lấy địa chỉ lấy hàng của shop (cùng nguồn mà `order` dùng khi checkout: tìm bằng `rg` trong `services/order`, gọi `user` bằng token service nếu cần). Khoá cache báo giá phải gồm điểm gửi thật (sửa khoá rỗng `shipping:quotes:v1:::79:79001:...`). Không có cả `shop_id` lẫn điểm gửi → 400.
2. SHP-B7: từ chối `weight_grams <= 0`, mã tỉnh/phường không có trong dữ liệu 34 tỉnh, tuyến ngoài vùng phục vụ.
3. SHP-B6: webhook `/webhooks/{carrier}` phải khớp `shipments.carrier_code` của vận đơn, lệch → 4xx, không đổi trạng thái.

## Test bắt buộc
Unit test cho 1–3. E2E `tests/e2e/17_shipping_fee_test.go`: báo giá chỉ có `shop_id` (không có điểm gửi) bằng đúng `shipping_fee_vnd` của đơn checkout cùng shop, cùng địa chỉ, cùng hãng/dịch vụ.
