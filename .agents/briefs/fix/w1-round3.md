# Đợt 1 – vòng sửa 3 (sau review độc lập lần 2) — vòng cuối của đợt 1

Luật chung `.agents/briefs/fix/rules.md` (sở hữu, test đỏ → xanh, chỉ rebuild service mình, không git write). Phát hiện gốc: `.agents/briefs/fix/review2-P-payment.md`, `.agents/briefs/fix/review2-O-order.md`.

## Quyết định thiết kế của manager (áp dụng ngay)
Huỷ một đơn con của nhóm checkout VietQR CHƯA thanh toán (đơn con còn PENDING, payment nhóm còn pending) = huỷ CẢ nhóm (mọi đơn con PENDING trong nhóm), vì cả nhóm dùng một QR duy nhất. Lý do: bản sửa P0-3 (payment huỷ QR khi có đơn con bị huỷ) khiến đơn con anh em không thể thanh toán (hồi quy), và huỷ lẻ trước `ConfirmStock` gây cộng tồn ảo (review2-O P1-4b). Đơn đã thanh toán / COD giữ nguyên huỷ từng đơn con như hiện tại.

| Agent | Mục phải sửa |
|---|---|
| payment | review2-P: P1 (1, 2, 3), P2 (4, 5, 6, 7), P3 (8, 9). P1-2: thu COD ngay trong `process_order_completed_event` (cùng helper `collect_cod_order_allocations_tx`) thay vì trả Conflict, vì `COMPLETED` luôn sau `DELIVERED`. P1-1: một khoá idempotency duy nhất cho refund thanh toán muộn theo `provider_tx_id` ở mọi đường (webhook, quét hết hạn, đối soát). |
| order | review2-O: P1 (1, 2, 3, 4a), P2 và P3 của order. P1-3: khoá theo nhóm đơn dùng chung DB cho mọi replica (ví dụ `pg_advisory_xact_lock(hashtext(checkout_group_id))` bao trọn kiểm tra → CreatePayment → cập nhật), không đổi proto. Quyết định thiết kế ở trên thay cho P1-4b: `CancelOrder` một đơn con VietQR chưa trả → huỷ cả nhóm (mọi đơn con PENDING), một lần `ReleaseStock` + `ReleasePromotion` cho nhóm, phát `order.cancelled` cho từng đơn con; thêm test. |
| web | Khi người mua bấm huỷ một đơn VietQR chưa thanh toán thuộc nhóm có nhiều gian hàng (cùng `checkout_group_id`, còn chờ thanh toán): hộp xác nhận ghi rõ "Huỷ đơn chưa thanh toán sẽ huỷ toàn bộ lần đặt hàng gồm N gian hàng" (tiếng Việt tự nhiên), sau khi huỷ tải lại danh sách để thấy mọi đơn con đã huỷ. Thêm test e2e-web nếu làm được. |
