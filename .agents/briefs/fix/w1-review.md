# Đợt 1 – review độc lập (chỉ đọc)

Bạn là reviewer độc lập cho thay đổi của đợt sửa 1 (nhánh `fix/business-audit`, chưa commit: xem bằng `git diff` và file mới trong `git status --short`). Không sửa file nào, không git write, không rebuild/restart container, không chạy `make up|down|seed|e2e|e2e-web`.

Bối cảnh bắt buộc đọc: `.agents/briefs/fix/plan.md` (mục 2 mô hình tiền, mục 3 nguyên nhân gốc), brief của từng agent `.agents/briefs/fix/w1-*.md`, `docs/contracts.md`, `AGENTS.md`.

## Tìm gì
1. Lỗi đúng/sai: logic sai so với mô hình tiền và brief, điều kiện biên (0₫, nhiều shop, hoàn một phần nhiều lần, huỷ một phần rồi huỷ hết, webhook lặp/muộn, sự kiện đến sai thứ tự), race (transaction, khoá, idempotency), bút toán không cân, tiền âm, lỗi bị nuốt, nil/None, migration không idempotent hoặc lỗi trên DB đã có dữ liệu cũ, đổi hành vi làm hỏng luồng cũ (checkout, seed `tools/seed/seed.mjs`, web).
2. Bảo mật: kiểm quyền theo đối tượng, RPC nội bộ gọi ẩn danh được, tin dữ liệu client (`shop_id`, `user_id`, IP), lộ thông điệp nội bộ.
3. Over-engineering (theo skill `ponytail-review`): abstraction thừa, code trùng có thể dùng lại thứ sẵn có, cấu hình không cần.

Mỗi phát hiện phải có: `file:dòng`, kịch bản cụ thể gây sai (đầu vào/trạng thái → kết quả sai), mức (P0 sai tiền/vượt quyền/mất dữ liệu, P1 hỏng luồng chính, P2 lệch spec/biên hiếm, P3 nhỏ), đề xuất sửa một dòng. Không báo điều chưa chắc như đã chắc: chưa chứng minh được thì ghi "nghi ngờ" và cách kiểm. Được chạy lệnh chỉ đọc: `go test`/`go vet` trong thư mục module, `cargo test` trong `services/payment`, `rg`, truy vấn SELECT vào Postgres của stack `nexusmart-audit` (`docker compose exec -T postgres psql -U nexusmart -d <db>`).

## Câu trả lời cuối
Danh sách phát hiện xếp theo mức nặng, mỗi mục một khối ngắn theo đúng các trường trên. Nếu không thấy lỗi ở phần nào, ghi rõ đã đọc những file nào.
