# Đợt 1 – pha 1B – agent `order`

Luật chung: `rules.md`. Mô hình tiền: `plan.md` mục 2 (bắt buộc theo đúng). Skill: `checkout-saga`, `add-event`, `add-migration`. Hợp đồng mới (pha 1A đã sinh): `ShopAllocation.platform_discount_vnd`, `FlashSaleReservationItem.shop_id`, `PromotionService.ReleasePromotionShop`, `UserService.RecordCODRejection`, `OrderService.RetryPayment` (xem `docs/contracts.md`).

## Sở hữu
`services/order/**`, `tests/e2e/12_*.go`.

## Việc
1. R1 (PAY-B3, PRM-B1, ORD-B1): allocation gửi payment theo plan.md: `subtotal_vnd = R = G − F − V_shop`, `commission_vnd = C` (giữ cách tính, trần R), `shipping_fee_vnd = Ship`, `platform_discount_vnd = D = V_plat + S_free + X`; Σ(R + Ship − D) = `amount_vnd`. Gốc ở `internal/service/checkout.go` khoảng dòng 1141-1155.
2. Đơn 0₫ (PRM-B1): luôn gọi `CreatePayment` kể cả `total_payable_vnd = 0` (payment sẽ trả `succeeded` ngay); không còn nhánh bỏ qua payment. Đơn sau đó có `payment_id` thật.
3. R2 COD (ORD-B8): đơn COD mới đặt có `payment_status` "chờ thu" (giá trị PENDING sẵn có), chỉ thành SUCCEEDED khi nhận `payment.succeeded` của payment hiện tại (payment phát khi đơn delivered). Huỷ đơn COD vẫn gọi `CancelPayment` như hiện tại.
4. Event mới `order.returned` qua outbox (skill `add-event`): phát khi đơn chuyển sang RETURNED, payload tối thiểu `{"order_id","order_code","checkout_group_id","buyer_id","shop_id","payment_method","from_status","total_payable_vnd"}` (`from_status` cho biết SHIPPING = giao thất bại/từ chối nhận, hay RETURN_REQUESTED = trả sau khi nhận). Ghi vào báo cáo mục "Cập nhật contracts".
5. R3 (PAY-B1, PAY-B5, ORD-B5): cài `RetryPayment`: chỉ người mua chủ nhóm đơn; mọi đơn con còn chờ thanh toán, chưa quá `payment_expires_at`; `payment_id` trong request đúng payment hiện tại; chỉ cùng phương thức VIETQR (giữ luật cũ chặn đổi sang COD). Gọi `CreatePayment` với `supersede_previous = true`, allocation tính lại từ dữ liệu đơn đã lưu, hạn mới = now + `ORDER_PAYMENT_EXPIRY_MINUTES`; cập nhật `payment_id` + `payment_expires_at` cho mọi đơn con trong nhóm trong một transaction. Đơn đã huỷ/quá hạn/đã trả → lỗi 409 (gRPC FailedPrecondition).
6. PAY-B6 phía order: consumer `payment.*` và `MarkPaymentResult` bỏ qua sự kiện có `payment_id` khác payment hiện tại của đơn; nhóm đơn đã CANCELLED thì giữ CANCELLED khi nhận `payment.succeeded` (payment tự hoàn); `payment.refunded` chỉ đặt `payment_status = REFUNDED` khi tổng hoàn bằng số đã trả của đơn con đó, hoàn một phần thì giữ nguyên trạng thái đơn.
7. R5 (PRM-B3, ORD-B3): huỷ một đơn con khi nhóm còn đơn con khác đang hoạt động → gọi `ReleasePromotionShop(checkout_group_id, shop_id)`; đơn con cuối cùng bị huỷ → `ReleasePromotion` như cũ. Đơn RETURNED → `ReleasePromotionShop`. Khi `ReservePromotion`, điền `shop_id` cho từng `FlashSaleReservationItem`.
8. R6 (ORD-B6): đơn COD chuyển SHIPPING → RETURNED → gọi `UserService.RecordCODRejection(buyer_id, order_id, reason)` bằng token service (idempotent theo đơn).
9. ORD-B4: lưu hash payload chuẩn hoá của checkout (buyer, items sắp xếp, địa chỉ, voucher, xu, phương thức, lựa chọn vận chuyển) cạnh idempotency key (migration `0003_`). Cùng key, khác hash → 409 với thông điệp rõ ràng; cùng key cùng hash → trả kết quả cũ như hiện tại.
10. ORD-B7: `BuyerConfirmReceived` chỉ từ DELIVERED; đang RETURN_REQUESTED → 409.
11. SHP-B2: `SellerShipOrder` dùng hãng/dịch vụ người mua đã chọn lúc checkout; request gửi hãng/dịch vụ khác → 409 (để trống thì dùng lựa chọn của đơn).
12. PRM-B2: xu thưởng khi completed theo plan.md (tiền thực trả cho hàng `R − V_plat − X` của đơn con, ×tỷ lệ hiện có, không âm; đơn 0₫ = 0). Gốc nghi ở `internal/service/lifecycle.go:611`, `internal/service/service.go:205-214`. Kiểm `services/promotion` consumer `order.completed` để chắc không cộng hai lần (chỉ đọc, ghi vào báo cáo nếu thấy).

## Test bắt buộc
Unit test cho: phép tính allocation (đủ G, F, V_shop, V_plat, S_free, X, Ship; tổng khớp amount), hash idempotency, luật trạng thái (7, 10, 11), chọn RPC hoàn khuyến mãi (7), gọi RecordCODRejection (8). E2E `tests/e2e/12_order_money_test.go`: (a) đơn voucher sàn + xu: allocation gửi payment đúng R, D; (b) đơn 0₫ có payment; (c) huỷ một đơn con hoàn đúng xu và lượt voucher shop của đơn con đó; (d) retry trên nhóm đã huỷ → 409, retry hợp lệ → `payment_id` của mọi đơn con đổi; (e) cùng key khác payload → 409; (f) confirm-received khi RETURN_REQUESTED → 409. Phần phụ thuộc payment/promotion/user chưa xong thì đánh dấu trong báo cáo.
