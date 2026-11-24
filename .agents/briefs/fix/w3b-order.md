# 3b-1 – agent `order` (sở hữu `services/order/**`)
Migration `0015_`. Theo E4, E18 (phía order), E19, E20 (phía order).
1. E4: `from_cart=true` gặp dòng `selected` không còn bán → `SKU_INACTIVE`, trước mọi giữ hàng.
2. E19: kiểm SĐT xác minh, khu vực, tỷ lệ bom hàng TRƯỚC khi giữ kho/khuyến mãi; đổi phương thức sang COD ở mọi đường (kể cả retry) đi cùng hàm kiểm; `user`/`shipping` lỗi tạm → từ chối có thể thử lại, không bỏ qua kiểm. Đếm tỷ lệ bằng một truy vấn có index, chỉ đơn trạng thái cuối.
3. E18: `SellerShipOrder` nhận `queued` → lưu `shipment_job_status`, đơn không nhảy trạng thái; `shipment.updated` `created` gắn mã vận đơn/nhãn idempotent; đơn đã huỷ thì bù trừ huỷ vận đơn; `FAILED` hiện cho người bán.
4. E20: `order.delivered` mang `carrier_code` + `tracking_code`.
Không đụng máy trạng thái ngoài phần nêu; không phá D11, idempotency, saga hiện có. Kiểm: lint, build, Postgres thật `-race` cả gói ×2 (0 SKIP), bài mới ×5.
