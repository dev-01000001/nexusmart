# 3b-2 — Agent `qa` (A6 + A7)

- **Sở hữu**: `tests/e2e/**` (chạy sau khi tất cả service và `gateway` hoàn thành). Đọc `w2-rules.md`, `w3b-plan.md` §4 (F1–F15), `w3b-design-a6.md` §6, `w3b-design-a7.md` §6, `docs/contracts.md`, và mục "ca e2e cần có" trong báo cáo các agent 3b-2.
- **Cấm**: sửa `proto/`, `services/`, `pkg/common/`, `web/`, docker, `make up|seed|e2e|e2e-web|coverage`, git ghi.
- **File mới**: `tests/e2e/44_zalopay_payment_w3b2_test.go` (A6) và `tests/e2e/45_admin_rbac_approvals_w3b2_test.go` (A7). Không làm chậm vượt trần e2e (dùng lại fixture buyer đã xác minh SĐT qua Mailpit một lần, không sleep cứng khi có thể poll điều kiện).

## Kịch bản bắt buộc — `44_` (A6: ZaloPay, UC-B25, UC-A13)
1. **ZaloPay mock end-to-end (F1–F2)**: checkout `ZALOPAY` → `201` có `pay_url`, đơn `PENDING`; gửi `POST /webhooks/zalopay` sai `mac` → từ chối, đơn vẫn `PENDING`; gửi lệch `amount` → không confirm, xuất hiện trong `reconciliation/queue`; gửi đúng `mac` → `200`, đơn `CONFIRMED`, sổ cái `is_balanced = true`; phát lại 5–10 lần cùng `zp_trans_id` → đúng 1 bút toán, sổ cái cân.
2. **Đổi phương thức `RetryPayment` (F5)**:
   - Đơn thường `ZALOPAY` → `RetryPayment(VIETQR)` → tạo QR mới, hạn thanh toán không vượt hạn gốc; gửi IPN muộn của giao dịch ZaloPay cũ (`superseded`) → sinh `refund` `is_late_payment=true`, không đổi trạng thái đơn.
   - Đơn thường `VIETQR` → `RetryPayment(COD)` → `switched_to_cod=true`, đơn chuyển `CONFIRMED`; thử lại trên đơn Flash Sale → `409 FAILED_PRECONDITION` (`FLASH_SALE_COD_NOT_ALLOWED`); thử trên buyer chưa xác minh SĐT / vượt hạn mức COD → `409`.
3. **Hoàn tiền ZaloPay (F4)**: huỷ đơn `ZALOPAY` đã thanh toán → tự hoàn về ví mock → `refund` đạt `DONE`, sổ cái cân.
4. **Cấu hình phương thức (F6–F7)**: `PUT /api/v1/admin/payment-methods/ZALOPAY` với `expected_version` sai → `409 VERSION_CONFLICT`; đúng version tắt `ZALOPAY`, hạ COD max xuống 500.000đ, đặt VietQR min 50.000đ → checkout/retry vi phạm bị chặn `409` (`PAYMENT_METHOD_DISABLED` / `COD_LIMIT_EXCEEDED` / `ORDER_AMOUNT_OUT_OF_RANGE`); IPN cho đơn ZaloPay tạo trước khi tắt vẫn `200` xác nhận; thử tắt phương thức bật cuối → `400`. Nhớ khôi phục cấu hình mặc định trong `t.Cleanup`!

## Kịch bản bắt buộc — `45_` (A7: RBAC 6 vai trò, Duyệt 2 người, Khoá shop, A03)
1. **Tạo nhân viên không mật khẩu & RBAC (F9, F9-bis, F10, F14)**: `super_admin` gọi `POST /api/v1/admin/staff` tạo `moderator`, `cskh`, `accountant`, `ops1`, `ops2` → đọc email đặt lại mật khẩu từ Mailpit → gọi `reset-password` → đăng nhập thành công. Kiểm ma trận dương & âm: `admin` thường gọi `POST /admin/staff` hoặc `PATCH /admin/users/{id}/role` → `403`; `cskh` khoá `moderator`/`admin` → `403`, khoá `buyer` → `200`; `moderator` duyệt SP/shop `200`, gọi `settle refund`/`credit coins`/`update carrier`/`admin/orders` → `403`; `accountant` xem báo cáo/đối soát/settle/credit coins `200`, duyệt SP → `403`; `cskh` giải quyết trả hàng `200`, đổi phí/`credit coins` → `403`; `ops` gọi `credit coins` → `403` (F9-bis).
2. **Duyệt 2 người khoá/mở gian hàng (F11–F12)**: `ops1` gọi `POST /admin/shops/{id}/suspend` → shop vẫn `ACTIVE`; `ops1` tự duyệt → `403 MAKER_CHECKER_CONFLICT`; `ops2` duyệt → shop `SUSPENDED`, token cũ của seller bị thu hồi, sản phẩm ẩn khỏi `GET /api/v1/products`, checkout mới vào shop → `409 SHOP_SUSPENDED`, đơn tạo trước đó vẫn ship được; `ops2` mở khoá `unsuspend` → SP `APPROVED` hiện lại, SP seller tự ẩn vẫn `HIDDEN`.
3. **Duyệt 2 người hoàn tiền lớn & chống chia nhỏ 24h (F11)**: `cskh` hoàn 6.000.000đ → `PENDING_APPROVAL`, sổ cái chưa đổi; `cskh` tự duyệt → `403`; `accountant` duyệt → `PENDING`, sổ cái cập nhật và cân; kiểm thêm ca chia nhỏ 2 lần 3.000.000đ cùng payment → lần 2 vào `PENDING_APPROVAL`.
4. **Hoạt động người dùng & Audit (F13)**: `GET /api/v1/admin/users/{id}/activities` trả đủ timeline đăng nhập/trạng thái + `recent_orders` (với `cskh`) và `200` + `recent_orders: []` (với `moderator`); `GET /api/v1/admin/audit-logs?from_time=...&to_time=...` có đủ cặp bản ghi maker/checker.

## Kiểm tra
- `cd tests/e2e && gofmt -l . && go vet ./... && go test -c ./...` (không chạy `make e2e`; manager chạy sau).
