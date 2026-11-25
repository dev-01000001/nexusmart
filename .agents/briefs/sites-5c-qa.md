# Swarm 5C – QA, seed, tài liệu
Đọc `.agents/briefs/sites-5-contract.md` trước.
Sở hữu: `tests/`, `tools/seed/`, `docs/PHASE5_REPORT.md` (tổng hợp), `Makefile` (chỉ nếu cần target mới). Bạn là swarm DUY NHẤT được rebuild container và chạy `make e2e`, `make seed`, `make e2e-web`.
Cấm: sửa `services/`, `proto/`, `web/`. Lỗi tìm thấy → ghi vào `docs/PHASE5_REPORT.md` mục "Lỗi gửi 5A/5B" kèm file:dòng và kịch bản; hai swarm kia đọc file này. Chờ 5A/5B có code rồi mới chạy e2e (theo dõi bằng `git status`/build xanh), lặp lại đến khi xanh hoặc hết giờ.
Việc:
1. Seed: shop `ha-noi` có tên miền custom `hanoi-knit.verified.test` (mock → ACTIVE), shop `da-nang` có `danang.pending.test` (PENDING).
2. Go e2e (`tests/e2e`): thêm/kiểm tra/xóa tên miền qua gateway; seller shop khác 403/404; trùng host 409; hostname xấu 400 (IP, root domain, sub của root, wildcard); by-host trả site cho custom ACTIVE, 404 cho PENDING; primary đổi đúng.
3. e2e-web (`tests/e2e-web`, gọi bằng header Host): `hanoi-knit.verified.test:3300` hiện site Hà Nội; `ha-noi.nexusmart.localhost:3300/` → 308 sang host custom, `/api/cart` không bị 308; `danang.pending.test` 404; host lạ 404; cookie host-only không rò giữa custom và subdomain; canonical dùng host custom.
4. Docker: rebuild `user`, `gateway`, `web` khi 5A/5B xong; healthy.
Gate cuối (dán đầu ra thật nguyên văn vào `docs/PHASE5_REPORT.md`): `make lint`, `make test`, chuỗi `make e2e && make seed && make e2e-web && make e2e-web` EXIT 0; CI vẫn chỉ `workflow_dispatch`; chỉ 28080 và 3300 public. Báo cáo ghi rõ phần chưa làm và các lệch khỏi hợp đồng.
