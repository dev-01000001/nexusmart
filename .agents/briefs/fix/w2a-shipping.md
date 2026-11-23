# Đợt 2a – agent `shipping`
Luật: `w2-rules.md`. Thiết kế: `w2-plan.md` §1.1, §1.3, UC-SHIP05..08, A12 trong `docs/spec.md`.
## Sở hữu
`services/shipping/**`, `tests/e2e/21_*.go`.
## Việc
1. Migration `000002_` (idempotent): `shipments.direction` (default FORWARD), `return_request_id`, `redelivery_count`; bỏ UNIQUE(order_id), thay bằng unique một phần `(order_id, direction) WHERE status <> 'cancelled'`; `shop_shipping_settings`, `carrier_configs`, `fallback_rates` (seed bằng bảng phí dự phòng đang hardcode trong code, giá trị không đổi).
2. `picked` → `delivering`; huỷ được chỉ khi `created|picking`. `CancelShipment` theo chiều; kiểm quyền: chỉ service token hoặc admin (`order` luôn gọi bằng service token; nay có trong `internalRPCs`). Race huỷ với webhook: khoá dòng, kiểm lại trạng thái trước khi ghi; hãng huỷ lỗi → retry như adapter hiện có, trả `Unavailable`.
3. `CreateReturnShipment` (SHP-B4): idempotent theo `(order_id, RETURN)`; phí tính qua aggregator; event `shipment.updated` mang `direction`, `return_request_id`. Webhook chỉ có `order_id` mà không có tracking → chiều FORWARD. `GetShipment` theo chiều; lỗi truy vấn cũ giả định một vận đơn mỗi đơn phải rà hết (`GetShipmentByOrderID`, poll, consumer).
4. `RequestRedelivery` (§1.3): so `sub` trong claims với `shipments.buyer_id` (cột mới ở `000002_`, lấy từ `CreateShipmentRequest.buyer_id` do `order` điền); vận đơn cũ không có `buyer_id` → `FailedPrecondition`. `CreateReturnShipment` chỉ service token hoặc admin.
5. UC-SHIP06: cấu hình shop (claims `shop_id`); `CalculateFee` chỉ trả hãng shop bật VÀ sàn bật; tắt hết → 400; cân nặng mặc định dùng khi sản phẩm không có cân nặng.
6. UC-A12: bật/tắt hãng toàn sàn, token mã hoá AES-256-GCM bằng `SHIPPING_CONFIG_KEY` (không trả lại, không log), bảng phí dự phòng đọc từ DB (cache ngắn), kiểm tra kết nối gọi adapter; token sai → không cho bật. Hãng tắt: không báo giá/không tạo mới, vận đơn cũ vẫn nhận webhook.
7. SHP-B8 phần backend: tìm tỉnh/phường không dấu; mô tả trạng thái tiếng Việt trong `shipment_events`/event thay cho chuỗi tiếng Anh thô; nhãn in mở được (URL theo cấu hình công khai, không `.local`): nếu cần gateway thì ghi "Nhờ agent khác".
## Test bắt buộc
Unit: map trạng thái, race huỷ, unique một phần (huỷ rồi tạo lại), mã hoá token, lọc hãng. E2E `21_shipping_ops_test.go`: huỷ trước/ sau `picked`; vận đơn RETURN đi hết vòng webhook mà không đụng vận đơn FORWARD; giao lại 3 lần rồi 409; shop tắt hãng → quote không còn hãng đó; admin tắt hãng; người mua khác giao lại → 403/404.
