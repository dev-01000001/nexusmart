# Swarm 5A – backend Cloudflare
Đọc `.agents/briefs/sites-5-contract.md` (hợp đồng, bắt buộc theo đúng) trước.
Sở hữu: `proto/`, `services/user/`, `services/gateway/`, `services/notification/` (nếu link email cần primary_host), `.env.example`, `docker-compose.yml` (chỉ thêm env CF_* cho user, mặc định mock, `CF_POLL_INTERVAL=5s` ở dev).
Cấm: `web/`, `tests/`, `tools/`, `docs/` (trừ ghi phần của bạn vào `docs/PHASE5_REPORT.md` mục "5A").
Việc: migration 0003, adapter Cloudflare (interface nhỏ, 2 implement: mock và live), validate hostname, 4 RPC + job nền, REST gateway, by-host nhận CUSTOM, tạo bản ghi DNS subdomain khi publish lần đầu.
Test bắt buộc (go test): validate hostname (bảng ca hợp lệ/không hợp lệ), adapter live với `httptest` giả API Cloudflare (tạo, đọc trạng thái, xóa, 429 retry, token không lộ trong lỗi), mock chuyển trạng thái theo đuôi, quyền seller shop khác bị từ chối, giới hạn 1 custom, trùng host 409, xóa khi Cloudflare lỗi giữ REMOVING, primary đổi khi ACTIVE.
Gate: `make lint`, `make test` xanh. Không chạy `make e2e`/`seed`/`e2e-web` (swarm 5C làm). Không rebuild container (5C làm).
