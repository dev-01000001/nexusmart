# 3b-1 – agent `payment` (sở hữu `services/payment/**`). Đọc `services/payment/CLAUDE.md`.
Migration `0009_` (sqlx kiểm checksum). Theo E1, E2, E20.
1. E1/E2 trước: `js.publish` phải chờ PubAck, cả hai nơi lỗi → NAK có trễ; không Ack event thiếu trường; lỗi lúc tắt không vào DLQ; đổi `max_deliver` không xoá consumer đang có.
2. E20: `ImportCodReconciliation` chỉ admin/super_admin; khoá theo mã vận đơn; hai lần nhập song song cùng file → một bút toán; mỗi dòng một transaction ngắn, một dòng hỏng không chặn dòng khác; nhập lại cho kết quả `already_reconciled`; `order.delivered` đến sau khi nhập file → dòng trong hàng đợi, không mất. Sổ cái luôn cân, có test đếm bút toán.
3. `ListReconciliationRuns` phân trang.
Kiểm: fmt, clippy -D warnings, `cargo test` 20 lượt, Postgres thật 0 SKIP.
