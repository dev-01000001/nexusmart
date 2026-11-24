# 3b-1 – agent `shipping` (sở hữu `services/shipping/**`, `tools/` phần nạp phường)
Migration `000006_` (idempotent, chạy lại mỗi lần khởi động). Theo E1, E15–E18, E21.
1. E1 consumer TRƯỚC, một commit logic riêng trong báo cáo.
2. E15 dữ liệu phường: tải nguồn công khai, đối chiếu hai nguồn, báo số thật; `SOURCES.md`; giữ mã cũ. Khởi động lại không upsert lại khi checksum không đổi.
3. E17 phí công khai, phường đại diện, cache ngắn, không lộ kho; mạch hở và timeout giữ như cũ.
4. E18 job tạo vận đơn: hai worker + tiêm lỗi 503 ba lần → đúng một vận đơn; huỷ giữa chừng → bù trừ; mock hãng có chỗ tiêm lỗi chỉ bật bằng env test.
Kiểm: gofmt, lint, build, Postgres thật `-race` ×2 (0 SKIP), bài mới ×5.
