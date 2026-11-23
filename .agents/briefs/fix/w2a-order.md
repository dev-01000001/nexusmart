# Đợt 2a – agent `order`
Luật: `w2-rules.md`. Thiết kế: `w2-plan.md` §1, §2. Skill: `checkout-saga`, `add-event`, `add-migration`.
## Sở hữu
`services/order/**`, `tests/e2e/19_*.go`.
## Việc
1. **Vận đơn** (§1.1): `SellerShipOrder` → `PREPARING` + tracking; `ApplyShipmentUpdate` chuyển `SHIPPING` theo shipment, đi qua `SHIPPING` khi nhận thẳng `delivered|returned`; bỏ qua sự kiện `direction=RETURN` ở luồng đơn. Gọi lại `/ship` khi đã có tracking: idempotent.
2. **Huỷ đơn có vận đơn** (§1.1): gọi `CancelShipment` bằng service token trước; `FailedPrecondition` → 409, không đổi đơn; `NotFound` → tiếp tục. Huỷ tự động (48h) dùng chung đường.
3. **Job**: tự `COMPLETED` sau `ORDER_AUTO_COMPLETE_DAYS`, tự huỷ `CONFIRMED` sau `ORDER_SELLER_CONFIRM_HOURS` (tính từ lúc vào `CONFIRMED`; thiếu cột thì thêm hoặc dùng timeline, đơn cũ dùng `updated_at`). Nhiều bản chạy song song không xử lý đôi. Một đơn lỗi không chặn cả lượt quét.
4. **Trả hàng** (§1.2): migration `0005_` (cột mới của `return_requests`, `return_events`, unique `order_id`, index theo `status, deadline_at`); bản ghi cũ: đơn đang `RETURN_REQUESTED` giữ `PENDING` với hạn mới tính từ lúc migrate, đơn khác đưa về trạng thái kết thúc phù hợp. Cài đủ RPC, watchdog hạn (tự chấp nhận, đóng, tự xác nhận) và watchdog `REFUNDING`. `CreateShipment`/`CreateReturnShipment` điền `buyer_id` từ đơn; `CreateReturnShipment` luôn bằng service token. Vận đơn RETURN: `CreateReturnShipment` gọi SAU khi commit, lặp lại bởi watchdog nếu lỗi (`AWAITING_SHIPMENT` chưa có tracking); hạn 5 ngày tính từ khi có tracking. Kiểm URL bằng chứng: `http(s)`, ≤ 5 URL, ≤ 500 ký tự mỗi URL.
5. **Consumer** `shipment.updated` chiều `RETURN` → `IN_TRANSIT`/`RECEIVED` (idempotent theo `processed_events`, bỏ qua khi yêu cầu đã kết thúc).
6. **Bỏ `ReleaseEscrow`** khỏi `BuyerConfirmReceived` và client (§2). Đường hoàn tất đơn gom về một hàm dùng cho người mua xác nhận, job 7 ngày, `CLOSED`.
7. `Order` proto trả `delivered_at`, `return_request_id`, `return_status`. Phát `order.return_updated` cho mọi lần chuyển.
8. Dọn `payment.refunded`: nhận sự kiện cho đơn `RETURNED` vẫn cộng `refunded_vnd` đúng, không đổi trạng thái đơn.
## Test bắt buộc
Unit (đồng hồ giả): bảng chuyển trạng thái trả hàng đủ dòng ở §1.2 và các chuyển bị cấm; quyền (người mua khác, shop khác, admin); hai lần bấm song song (race) chỉ một lần có tác dụng; `REFUNDING` lỗi `CreateRefund` lần đầu rồi watchdog làm xong, không hoàn hai lần; chỉ restock khi hàng đã về; auto-complete/auto-cancel; huỷ khi hãng đã lấy → 409. E2E `19_order_returns_test.go`: (a) giao → yêu cầu trả → seller chấp nhận trả về → webhook RETURN delivered → seller xác nhận → đơn `RETURNED`, refund `pending` đúng P, tồn kho cộng lại, seller không nhận tiền; (b) từ chối → khiếu nại → admin bác → đơn `COMPLETED`, tiền vào `seller_pending`; (c) `ACCEPT_REFUND` không restock; (d) COD đã giao rồi hoàn; (e) huỷ khi `PREPARING` có vận đơn → vận đơn `cancelled`; sau `picked` → 409.
