# Đợt 2a – agent `notification`
Luật: `w2-rules.md`. Thiết kế: `w2-plan.md` §4.
## Sở hữu
`services/notification/**`, `tests/e2e/24_*.go`.
## Việc
1. Consumer mới: `order.return_updated` (người mua và người bán, nội dung theo trạng thái, link đúng: người mua `/don-hang/<order_id>`, người bán `/seller/don-hang/<order_id>`, khiếu nại thêm thông báo cho người bán), `payment.refund_settled`, `user.password_reset_requested` (email có link `WEB_BASE_URL/dat-lai-mat-khau?token=...`, không lưu token vào bảng thông báo, không log).
2. Người bán: `order.confirmed` (đơn mới), `order.cancelled`, `shipment.updated` failed/returning/returned, in-app + email theo cài đặt. Chủ shop lấy qua `UserService.GetShop` (xem cách `order`/`shipping` gọi), lưu bảng `shop_owners` (migration `000003_`), cache; lỗi tạm thời → trả lỗi để gửi lại.
3. `shipment.updated` đến trước `order.created`: trả lỗi để NATS gửi lại, không ghi `processed_events`; nhưng quá số lần → DLQ như luật chung. Chiều `RETURN` không gửi nội dung "đang giao cho bạn".
4. Email cho người mua khi `delivering|delivered|failed`. Tất cả tiếng Việt, không enum thô, số tiền định dạng VND. `order.delivered` và `shipment.updated=delivered` không gửi hai email trùng.
5. Thông báo đơn hàng và bảo mật không tắt được hoàn toàn (UC-B23): kiểm và ép ở server.
## Test bắt buộc
Unit theo từng event (đúng người nhận, đúng link, không trùng, out-of-order trả lỗi). E2E `24_notification_w2_test.go`: người bán nhận thông báo đơn mới và yêu cầu trả hàng; email quên mật khẩu tới Mailpit. Phần phụ thuộc order/user chưa xong thì đánh dấu.
