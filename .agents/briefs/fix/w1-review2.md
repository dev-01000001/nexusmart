# Đợt 1 – review độc lập lần 2 (chỉ đọc) cho phần sửa vòng 2

Như `.agents/briefs/fix/w1-review.md` (cùng luật chỉ đọc, cùng định dạng phát hiện), nhưng chỉ soát phần đã viết lại ở vòng 2. Bối cảnh: phát hiện vòng 1 `.agents/briefs/fix/review-A-order-promotion.md`, `.agents/briefs/fix/review-B-payment.md`; mô hình tiền `.agents/briefs/fix/plan.md` mục 2. Không sửa file, không git write, không rebuild, không chạy make up/down/seed/e2e.

## payment (reviewer P)
Soát các hàm đổi ở vòng 2: `collect_cod_order_allocations_tx`, `release_escrow`, `process_order_completed_event`, `process_order_cancelled_event`, `process_order_returned_event`, `process_verified_webhook`, `lock_payment_group_tx`, `find_payment_for_webhook_tx`, `confirm_vietqr_payment_tx`, `scan_expired_payments`, `run_reconciliation`, `record_late_payment_refund_tx`, `create_payment` (idempotency match), `create_refund` (idempotency match, refunded status), `sync_cod_payment_status_tx`, `claim_nats_event_tx`. Bắt buộc trả lời:
1. `process_order_completed_event` trả Conflict khi còn allocation COD `awaiting_collection`: nếu `order.delivered` vào DLQ hoặc không bao giờ tới, `order.completed` có lặp tới max-deliver rồi vào DLQ để seller không bao giờ được quyết toán không? Đường tự thu COD ở `release_escrow` có che được không, và nếu không thì đề xuất cách tối thiểu.
2. Thứ tự khoá mới có còn đường nào khoá `payments` theo thứ tự khác (`create_payment`, `cancel_payment`, refund) gây deadlock?
3. Có đường nào ghi bút toán hai lần khi NATS gửi lại sau lỗi tạm (Conflict → NAK)?

## order (reviewer O)
Soát các hàm đổi ở vòng 2: `HandlePaymentResult`, `HandleShipmentUpdated`, `ApplyShipmentUpdate`, `TransitionOrderStatus`, `TransitionOrdersPaymentResult`, `compensateCancelledOrder`, `RetryPayment`, `UpdateGroupPaymentOnRetry`, `RecordPaymentRefunded`, `MarkEventProcessed`, migration `0004_retry_idempotency.sql`. Bắt buộc trả lời:
1. Xử lý sự kiện giờ chỉ đánh dấu đã xử lý sau khi gọi downstream thành công: khi gửi lại sau thành công một phần, `RestockOrderItems`, `ReleasePromotionShop`/`ReleasePromotion`, `CreateRefund`, `RecordCODRejection`, `ConfirmStock`/`ReleaseStock` có idempotent thật không (khoá idempotency nào, ở service nào)? Có nguy cơ hoàn tồn hai lần hay hoàn tiền hai lần không?
2. `RetryPayment` replay theo `(payment_id, idempotency_key)` sau khi nhóm đã bị huỷ hoặc quá hạn: trả gì? Có làm sống lại payment không?
3. Có đường nào cập nhật `orders.payment_status`/`refunded_vnd` sai khi `payment.refunded` và `order.cancelled` đến chéo thứ tự?

Câu trả lời cuối: danh sách phát hiện theo mức nặng; nếu không thấy lỗi ở câu hỏi bắt buộc nào thì ghi rõ lý do kèm `file:dòng` đã đọc.
