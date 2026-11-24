# 3a – agent `order` (sở hữu `services/order/**`, `tests/e2e/36_*.go`)
Theo `w3a-plan.md` D3, D9, D10.
1. D3: gọi `ReservePromotion` bằng token service của order, không kèm JWT người mua; mọi retry/forward recovery cũng vậy. Các lời gọi khác giữ JWT.
2. D9 outbox (migration `0014_`).
3. D10: nhận `is_post_settlement`; test các thứ tự: hoàn trước tất toán tới trước/sau `order.completed`, event trùng, event cũ thiếu trường. Báo cáo seller và sàn khớp công thức sổ cái.
4. Nhóm toàn huỷ: đơn `EXPIRED` không bị ghi thành `CANCELLED`.
5. Gọi lại checkout (cùng Idempotency-Key) khi nhóm đã `FAILED`: trả lỗi rõ kèm mã lý do, không trả 200.
6. Vòng chờ 50 ms gọi `GetPayment` (checkout.go ~792, ~819): giảm tải (backoff tăng dần, tổng thời gian chờ không đổi), dừng theo ctx.
7. `resolveShopPickupAddress` không được dùng địa chỉ người mua làm nơi lấy hàng. Thiếu địa chỉ shop → lỗi rõ khi tạo vận đơn/trả hàng (đơn đã đặt không hỏng); checkout tính phí khi shop thiếu địa chỉ → quyết định theo code shipping và ghi báo cáo.
8. Độ phủ order ≥ 60% theo `make coverage` (nay 57,0%; `internal/store/postgres.go` gần 0% vì test Postgres bị bỏ qua). Ưu tiên test Postgres thật cho store (khoá, đua, trạng thái).
E2E 36: nhóm checkout `IN_PROGRESS` (retry trong lúc đang chạy, phục hồi), FAILED trả lỗi.
