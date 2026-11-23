# Đợt 2a – agent `qa` (chạy sau khi 7 agent xong)
Luật: `w2-rules.md`. Thiết kế: `w2-plan.md`.
## Sở hữu
Test e2e cũ `tests/e2e/01..18`, `client.go`, `config.go`, `fixtures_test.go`, `types.go`, `mailpit.go`; `tests/e2e-web/01..05`, `helpers.mjs`; `tools/seed/**`.
## Việc
1. Sửa kỳ vọng cũ ĐÚNG theo luật mới, chỉ khi luật mới ở `w2-plan.md` buộc đổi: sau `/ship` đơn là `PREPARING`, webhook `delivering` mới sang `SHIPPING`; sau confirm-received tiền ở `pending_vnd`; refund tạo ra ở `pending`. Mỗi chỗ đổi ghi lý do (mục nào của `w2-plan.md`). Không nới lỏng hay xoá assertion khác; test đỏ vì lỗi thật thì báo `file:dòng`, không sửa test cho xanh.
2. Seed vẫn idempotent, chạy 2 lần; đơn mẫu đi qua đủ bước trạng thái mới.
3. Chạy trên stack audit hiện tại theo từng lô (nghỉ 65 giây giữa các lô vì rate limit), gồm cả bài mới 19–24 và e2e-web 06; báo từng test đỏ kèm nguyên nhân `file:dòng`. Lượt chạy đầy đủ trên stack sạch do manager làm. Không rebuild service. Agent web có thể còn rebuild web: e2e-web đỏ vì web đang khởi động thì chạy lại.
4. Test mới của các agent (19–24, 06) không để lại trạng thái làm hỏng test khác (ví dụ tắt hãng vận chuyển phải bật lại, token hãng sai phải khôi phục kể cả khi test fail giữa chừng); báo chỗ chưa dọn.
