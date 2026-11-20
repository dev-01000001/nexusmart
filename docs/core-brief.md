# Swarm brief – NexusMart core (luồng mua hàng đầu-cuối)

Bạn là coordinator của swarm. Claude Code là manager và sẽ review. Đọc `AGENTS.md` và `docs/spec.md` (đặc tả đầy đủ: phạm vi, 100 use case, tích hợp, công nghệ) trước khi chia việc.

## Mục tiêu
Dựng lõi backend theo lộ trình tháng 1–3: một người mua đăng ký, tìm sản phẩm, thêm giỏ, áp voucher, đặt hàng nhiều gian hàng, thanh toán, nhận thông báo, theo dõi đơn. Chạy được toàn bộ bằng `docker compose up` và có test đầu-cuối chứng minh.

## Trong phạm vi
- `proto/`: Protobuf dùng chung, sinh code Go và Rust bằng `buf`.
- `services/gateway` (Go, chi): REST cho client, JWT, rate limit Redis theo IP và tài khoản, RBAC.
- `services/user` (Go, pgx, sqlc): đăng ký, đăng nhập, argon2id, access token 15 phút, refresh token xoay vòng, role người mua, người bán, admin, hồ sơ gian hàng và duyệt.
- `services/product` (Go): danh mục, sản phẩm có biến thể, tồn kho theo SKU với optimistic locking, giữ và nhả tồn.
- `services/cart` (Go, Redis): giỏ gom theo gian hàng.
- `services/promotion` (Go, Redis Lua): voucher sàn, voucher gian hàng, freeship, xu, flash sale; reserve và hoàn trong saga.
- `services/order` (Go): máy trạng thái đơn, saga orchestration, outbox, idempotency key, tách đơn con theo gian hàng.
- `services/payment` (Rust, Axum, sqlx): tiền số nguyên VND, sổ cái ghi kép, COD, VietQR qua payOS hoặc SePay (sandbox, có mock), webhook HMAC chống replay và idempotent, job hết hạn và đối soát.
- `services/shipping` (Go): interface Carrier, adapter GHN sandbox và một adapter mock, timeout, retry backoff, circuit breaker, webhook idempotent.
- `services/notification` (Go): consumer idempotent, dead letter queue, email qua SMTP giả (mailpit).
- Hạ tầng dev: `docker-compose.yml` với PostgreSQL (mỗi service một database), Redis, NATS JetStream, mailpit, OpenTelemetry collector. Log JSON có trace ID, metric RED cho từng service.
- CI: GitHub Actions chạy lint, test, build image cho từng service.

## Ngoài phạm vi của swarm này
Search, chat, AI assistant, AIOps, Kubernetes, kiểm thử tải. Thư mục `web/` thuộc swarm khác: KHÔNG sửa `web/`, `.taste-skill/`, `.agents/`.

## Ràng buộc
- Mọi ràng buộc trong `AGENTS.md` và cột "Yêu cầu kỹ thuật và tiêu chí đo" của `docs/spec.md`.
- Không service nào đọc database của service khác. Giao tiếp nội bộ qua gRPC và event.
- Không secret thật trong repo. Dùng `.env.example` và giá trị sandbox.
- Ponytail: thư viện chuẩn và thư viện đã nêu trong sheet Công nghệ trước. Không thêm framework, không trừu tượng hoá cho tương lai. Một module Go dùng chung nhỏ cho log, trace, middleware; không hơn.
- Máy này thiếu `rustc`, `cargo`, `protoc`, `buf`. Cài bản user-local (rustup, binary buf) hoặc build trong Docker. Không dùng `sudo`.

## Chia việc gợi ý
1. Nền: layout monorepo, `proto/` và hợp đồng API, module dùng chung, compose, migration, CI. Các worker khác chờ hợp đồng proto.
2. Song song theo ownership thư mục: gateway + user; product + cart; promotion; order; payment (Rust); shipping + notification.
3. Một worker QA: viết và chạy test đầu-cuối liên tục, gửi lỗi về đúng worker.

## Gate trước khi báo xong
- `golangci-lint`, `gofmt`, `go test ./...`, `cargo clippy -- -D warnings`, `cargo fmt --check`, `cargo test` sạch.
- `docker compose up` khởi động mọi service khoẻ mạnh.
- Test đầu-cuối tự động (script hoặc Go test) chạy xanh, gồm:
  - luồng mua hàng nhiều gian hàng với COD và với VietQR mock;
  - gửi lại yêu cầu tạo đơn cùng idempotency key: chỉ một đơn;
  - webhook thanh toán lặp 10 lần: ghi nhận một lần; chữ ký sai bị từ chối;
  - 1.000 yêu cầu đồng thời cho 100 suất flash sale: bán đúng 100;
  - thanh toán thất bại: saga hoàn tồn kho, voucher, xu;
  - sổ cái cân bằng sau mọi kịch bản.
- Viết `docs/CORE_REPORT.md`: kiến trúc đã dựng, cách chạy, kết quả từng test, phần chưa xong, mọi chỗ lệch khỏi đặc tả kèm lý do.
