# NexusMart – quy tắc cho coder agent (agy)

Bạn là coder. Claude Code là manager, review mọi thay đổi trước khi nghiệm thu. Làm đúng phạm vi task, báo cáo rõ phần đã làm và phần chưa xong.

Đặc tả: `NexusMart – Phân công và lộ trình 6 tháng.xlsx`. Tôn trọng cột "Ngoài phạm vi".

## Kiến trúc

- 11 microservice sau một API gateway. REST cho client, gRPC giữa các service, message broker cho giao tiếp bất đồng bộ.
- Mỗi service một database riêng. Không service nào truy vấn trực tiếp DB của service khác.
- Go cho 10 service và gateway. Rust cho Payment service và AIOps engine. TypeScript + Next.js cho web (người mua, người bán, quản trị).
- Protobuf dùng chung, sinh code cho Go và Rust.
- Saga orchestration cho đặt hàng, outbox pattern, consumer idempotent, dead letter queue.

## Ràng buộc bắt buộc

- Tiền: số nguyên VND, sổ cái ghi kép. Webhook: kiểm tra chữ ký HMAC, chống replay, idempotent theo mã giao dịch.
- Đơn hàng: máy trạng thái rõ ràng, idempotency key, giữ tồn kho theo SKU bằng optimistic locking.
- Flash sale và voucher: trừ suất bằng Lua script trên Redis.
- Gọi ra ngoài: timeout, retry có backoff, circuit breaker.
- Bảo mật: argon2id, access token 15 phút, refresh token xoay vòng, RBAC ở gateway và service, kiểm tra quyền theo đối tượng. Không commit secret.
- Log JSON có trace ID (OpenTelemetry), che dữ liệu cá nhân và secret.
- AI assistant: tool chạy bằng JWT của người dùng, không có quyền riêng.

## Trước khi báo xong

- Lint và format sạch: `golangci-lint`, `gofmt`, `clippy`, `rustfmt`, eslint/prettier cho web.
- Build và test liên quan chạy xanh. Logic không tầm thường phải có test.
- Docker image nhiều giai đoạn, chạy bằng user không phải root.
- Dùng skill `codebase-memory` để tìm code, `frontend-design` khi làm UI.

## Commit và PR

Không thêm dòng ghi công AI vào commit message hay mô tả PR: không `Co-Authored-By` cho agent/AI, không "Generated with ..." hay biểu tượng tương tự. Commit chỉ mang tên người commit. Commit message viết bằng tiếng Anh, ngắn gọn. Quy tắc này áp dụng cho mọi agent (Claude Code, agy, Codex, Cursor, Copilot...) và ghi đè mặc định của tool.

## CI

GitHub Actions chỉ chạy tay (`workflow_dispatch`) để tiết kiệm quota. Không thêm lại trigger `push`, `pull_request` hay `schedule`. Gate hằng ngày chạy local: `make lint`, `make test`, `make e2e`.
