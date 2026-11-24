# 3b-2 — Agent `notification` (A6 + A7)

- **Sở hữu**: `services/notification/**`. Đọc `w2-rules.md`, `w3b-plan.md` §4 (F1, F12), `w3b-design-a6.md`, `w3b-design-a7.md`, `docs/contracts.md`.
- **Cấm**: sửa `proto/`, `pkg/common/`, service khác, docker, `make up|seed|e2e|coverage`, git ghi.
- **Migration**: không cần migration mới trong 3b-2.

## Phạm vi bắt buộc
1. **Nhãn thanh toán ZaloPay (A6)**: cập nhật helper định dạng phương thức thanh toán trong `services/notification/internal/service/notification.go` (khoảng dòng 1961–1970) nhận `"ZALOPAY"` → `"ví ZaloPay"` trong thông báo/email đơn hàng và thanh toán (giữ `"VietQR"` và `"COD"`).
2. **Consumer `user.shop_status_changed` (A7 / F12)**:
   - Xử lý subject `user.shop_status_changed` với payload `{"shop_id","owner_user_id","shop_name","from_status","to_status","reason","actor_id","approver_id","version","occurred_at"}` (idempotent theo `event_id` qua `processed_events`, không đổi hành vi DLQ/shutdown E1).
   - Khi `to_status == "SUSPENDED"`: gửi thông báo in-app (`SYSTEM`) + email cho `owner_user_id` nêu tên gian hàng, lý do khoá (`reason`), và lưu ý đơn hàng đang chạy vẫn tiếp tục xử lý.
   - Khi `to_status == "ACTIVE"` (từ `SUSPENDED`): gửi thông báo in-app (`SYSTEM`) + email cho `owner_user_id` báo gian hàng đã được mở khoá trở lại.
   - Thiếu `shop_id` hoặc `owner_user_id` hoặc `to_status` không hợp lệ → trả lỗi để vào DLQ (không Ack bỏ theo E1).
3. **Không làm lộ secret/PII**: tiếp tục giữ `sanitizeDLQPayload` cho `user.password_reset_requested` (được `AdminCreateStaffUser` tái dùng — F14) và các event OTP; không log token đặt lại mật khẩu.

## Tiêu chí nghiệm thu & Test bắt buộc
- Unit test `ZALOPAY`: email & in-app `order.created` / `payment.succeeded` với `payment_method = "ZALOPAY"` hiển thị `"ví ZaloPay"`.
- Unit test `user.shop_status_changed`:
  - `ACTIVE -> SUSPENDED` gửi đúng 1 in-app + 1 email tới chủ shop có `reason` và `shop_name`.
  - `SUSPENDED -> ACTIVE` gửi đúng 1 in-app + 1 email mở khoá.
  - Replay cùng `event_id` → idempotent, không tạo thông báo/email trùng.
  - Payload thiếu `owner_user_id` → trả lỗi (vào DLQ, không Ack bỏ).
- Kiểm: `gofmt`, `golangci-lint`, `go build ./...`, `go test -race ./...` (Postgres thật qua `bash scripts/test-db.sh --run`, 0 SKIP) và test mới `-race -count=5`.
