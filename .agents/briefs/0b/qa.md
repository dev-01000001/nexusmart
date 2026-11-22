# 0b pha B1 – worker `qa`: test đầu-cuối qua web (BFF) trên backend thật

Đọc trước: `.agents/briefs/sites-0b-swarm-rules.md`, `.agents/briefs/0b/b1-contracts.md`, `.agents/briefs/sites-0b-web-gateway.md` mục "Gate trước khi báo xong", `docs/contracts.md`, `tests/e2e/*.go` (cách ký webhook VietQR HMAC, lấy OTP từ mailpit, dựng dữ liệu), `web/src/app/api/**` (route BFF).

## Sở hữu
`tests/e2e-web/**` (chỉ thư mục này). Target `make e2e-web` đã có (`node --test tests/e2e-web/`, biến `WEB_URL`, `MAILPIT_URL`, `GATEWAY_URL` đọc từ `.env`).

## Việc phải làm
Viết bộ test bằng `node:test` + `fetch` có sẵn của Node 24 (không thêm dependency, không trình duyệt), đi qua web server (BFF) `WEB_URL` với cookie jar tự quản. Chỉ được gọi thẳng gateway (`GATEWAY_URL`) cho việc của hệ thống bên ngoài: webhook VietQR ký HMAC giả lập nhà cung cấp; còn lại mọi thao tác người dùng đi qua `WEB_URL`. Mỗi lần chạy tự tạo dữ liệu riêng (email/sđt duy nhất), chạy lại nhiều lần được.

Các kịch bản bắt buộc (gate của brief 0b):
1. Đăng ký `/api/auth/register` → OTP từ mailpit → `/api/auth/verify-otp` → `/api/auth/login` (cookie `HttpOnly`, body không có token) → thêm giỏ 2 SKU của 2 gian hàng khác nhau (`/api/gw/cart/items`, SKU lấy từ `/api/gw/products`) → xem trước voucher sàn `NEXUS10` (hoặc voucher sàn có trong `/api/gw/vouchers`) → tạo địa chỉ → checkout COD (`/api/gw/checkout`, `Idempotency-Key`) → đơn có trong `/api/gw/orders`.
2. Một lượt VietQR mock: checkout VIETQR → gửi webhook ký HMAC tới gateway như `tests/e2e/03_vietqr_checkout_test.go` (`PAYMENT_WEBHOOK_SECRET` từ `.env`) → `/api/gw/payments/{id}` thành đã thanh toán và đơn chuyển trạng thái tương ứng.
3. Gửi lại checkout cùng `Idempotency-Key` → `idempotent_replay:true`, cùng `checkout_group_id`, số đơn không tăng.
4. Người mua A không đọc được đơn của người mua B (`/api/gw/orders/{id}` → 403 hoặc 404), và trang `/don-hang/<id>` của B với cookie A không lộ dữ liệu.
5. Route ghi không có phiên (`POST /api/gw/cart/items`) → 401.
6. Request ghi từ `Origin` lạ → 403; không có `Origin` → 403.
Thêm: `/tai-khoan` không phiên → 307 về `/dang-nhap?next=…`; buyer vào `/seller`, `/admin` → 404; `/san-pham/<slug thật>` trả 200 có tên sản phẩm; refresh token cũ dùng lại bị từ chối.

## Chạy
- Web để test: dùng service `web` trong compose (`http://localhost:33000`). Ảnh hiện tại build từ code pha A (route `/api/auth/*`, `/api/gw/*` đã có). Khi cần code mới hơn của worker khác, chạy `docker compose up -d --build web` ở gốc worktree (bạn được rebuild `web` trong B1); build lỗi do worker khác đang sửa dở thì chờ rồi thử lại, không sửa hộ.
- Kịch bản phụ thuộc trang của worker khác (ví dụ HTML `/san-pham/<slug>` hiện dữ liệu thật) mà chưa xong: đánh dấu `skip` kèm lý do, không xoá.

## Tiêu chí nghiệm thu
- `make e2e-web` chạy xanh mọi kịch bản không phụ thuộc worker khác; dán toàn bộ dòng kết quả (`# tests`, `# pass`, `# fail`, `# skipped`). Chạy hai lần liên tiếp đều xanh.
- Test hỏng khi phá đúng thứ nó kiểm (thử tạm bỏ `Idempotency-Key` hoặc đổi `Origin` và xác nhận test fail, rồi hoàn nguyên; mô tả trong báo cáo).
- Lỗi phát hiện trong code của worker khác: ghi vào "Nhờ worker khác" với `file:dòng` và kịch bản tái hiện.
