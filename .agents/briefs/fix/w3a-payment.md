# 3a – agent `payment` (sở hữu `services/payment/**`, `tests/e2e/37_*.go`). Đọc `services/payment/CLAUDE.md`.
Theo `w3a-plan.md` D9, D10. Migration `0008_` (sqlx kiểm checksum, không sửa file cũ).
1. Test Postgres thật qua `TEST_PAYMENT_DB_URL` (không có thì bỏ qua có thông báo): đua 23505 ở `CreatePayment` (hai request cùng nhóm, hai unique index succeeded, đơn 0₫), webhook lặp 20 lần song song chỉ một bút toán, hoàn tiền đua; các câu SQL đang chỉ được test bằng hàm Rust phải chạy thật.
2. D10: `payment.refunded` có `is_post_settlement` đúng trong mọi đường hoàn (tự động, admin, trả hàng, webhook hoàn); xác định trong cùng transaction với trạng thái tất toán.
3. SSE: id không phải UUID → 400 gọn, không 500, không truy vấn DB.
4. Index cho `payment_fee_configs` theo truy vấn thật (kèm `EXPLAIN` trên vài nghìn dòng).
5. D9 outbox.
6. Đo `cargo llvm-cov --summary-only` (đã cài); ≥ 60%, thiếu thì thêm test hành vi cho luồng tiền/webhook/hoàn. Ghi số trước/sau.
E2E 37: SSE id sai, và một ca webhook lặp song song nếu chưa có bài nào phủ (kiểm 05, 13, 20, 29 trước).
Kiểm: fmt, clippy -D warnings, `cargo test` 20 lượt liên tiếp không đỏ.
