# Sửa giai đoạn 5: chiếm giữ tên miền (domain squatting)

Manager review `services/user/internal/service/domains.go` (dòng ~120–130 và ~190–205): mọi dòng `domains` có cùng host, kể cả `PENDING` hay `FAILED` của site khác, làm `AddCustomDomain` trả `domain_taken`.
Kịch bản lỗi: gian hàng A thêm `thuonghieu-b.vn` (không phải của A). A không thêm được TXT nên tên miền `PENDING` rồi `FAILED`, nhưng dòng vẫn còn. Chủ thật là gian hàng B thêm `thuonghieu-b.vn` → `409 domain_taken` mãi mãi, đến khi A tự xóa.

Luật mới (cập nhật cả `.agents/briefs/sites-5-contract.md` mục Dữ liệu cho khớp):
- Chỉ dòng `ACTIVE` hoặc `REMOVING` chặn site khác. Dòng `FAILED` của site khác không chặn.
- Dòng `PENDING` của site khác chặn trong tối đa 48 giờ kể từ `created_at`; quá 48 giờ coi như `FAILED` (không chờ job 7 ngày cho việc này).
- Khi site B lấy lại một host đang bị dòng cũ không còn hiệu lực giữ: trong cùng transaction có khóa, gọi Cloudflare `DeleteCustomHostname` cho dòng cũ (mock cũng phải xử lý), xóa dòng cũ, rồi tạo dòng mới cho B. Cloudflare lỗi → trả lỗi, không xóa dòng cũ.
- Cùng site thêm lại host của chính mình đang `FAILED`: cho phép (thay dòng cũ), không tính vượt `domain_limit`.
- Giữ index unique `LOWER(host)`.

Test bắt buộc: unit test trong `services/user/internal/service/domains_test.go` cho cả 4 luật; thêm ca vào `tests/e2e/15_custom_domains_test.go`: shop A thêm `squat.pending.test` → shop B thêm cùng host bị 409; đẩy `created_at` của dòng A lùi 49 giờ (qua DB e2e như các test khác đang làm, hoặc thêm hook test có sẵn) → B thêm được, A không còn dòng; shop A thêm `x.fail.test` → FAILED → B thêm được ngay.
Sở hữu: `services/user/`, `tests/e2e/`, `docs/PHASE5_REPORT.md` (thêm mục "Sửa squatting"). Không sửa `web/`.
Gate: `make lint`, `make test`, rebuild container `user` (`docker compose up -d --build --no-deps --wait user`), rồi `make e2e && make seed && make e2e-web` EXIT 0. Không commit. Không xóa image/build cache Docker.
