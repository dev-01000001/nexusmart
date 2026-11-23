# Rà soát nghiệp vụ 2026-11-23 – luật chung cho mọi worker

Mục tiêu: rà toàn bộ nghiệp vụ NexusMart trên stack chạy thật để biết cái nào chưa có, cái nào có nhưng lỗi, dữ liệu đã chuẩn chưa. Đợt này CHỈ RÀ SOÁT: không sửa code, không sửa tài liệu ngoài file báo cáo của mình. Manager (Claude Code) kiểm lại phát hiện rồi mới lập kế hoạch sửa.

Brief riêng: `.agents/briefs/audit/<tên>.md`. Tám worker chạy song song: `usr`, `cat`, `ord`, `pay`, `prm`, `shp`, `plt`, `web`.

## Phạm vi chuẩn
- Nguồn sự thật: `docs/spec.md`, tab "Use case" (100 UC, cột cuối là Ưu tiên: Cao / Trung bình / Thấp) và tab "Phạm vi" (cột "Ngoài phạm vi"). Spec ghi: use case mức Thấp chỉ đặc tả, không bắt buộc cài đặt; mọi tính năng không có trong tab Use case là ngoài phạm vi.
- Bảng Shopify/sàn trong brief riêng là danh mục đối chiếu chủ dự án đưa. Xếp mỗi dòng vào UC tương ứng, hoặc ghi "ngoài phạm vi đặc tả" kèm dẫn chứng (dòng nào của spec).
- Đọc trước: `AGENTS.md`, `docs/contracts.md`, `docs/CORE_REPORT.md` §5–6, `docs/WEB_INTEGRATION_REPORT.md` §8, `docs/web-gap-audit.md`, `web/FEATURE_MATRIX.md` (phần liên quan tới mình). Mục "chưa xong" trong các file đó là lời khai cũ, PR #2 có thể đã sửa: kiểm lại trên code HEAD (`0db2cdd`) và trên stack, không chép lại.

## Stack để kiểm: project compose `nexusmart-audit` (cổng khai trong `.env` ở gốc repo)
- Gateway REST: `http://localhost:48080` (`/healthz`). Web Next.js (BFF, chạy trong compose): `http://localhost:43000`. Mailpit UI/API: `http://localhost:48025` (OTP đăng ký, email thông báo).
- Postgres: `docker compose exec -T postgres psql -U nexusmart -d <db>` với db `user_db`, `product_db`, `promotion_db`, `payment_db`, `shipping_db`, `notification_db`, `order_db`. CHỈ SELECT.
- Redis: `docker compose exec -T redis redis-cli` (chỉ lệnh đọc). NATS monitor: `http://localhost:48222`. Log: `docker compose logs --since 30m <service>`.
- HTTP ops của service (healthz, metrics, route nội bộ): user 48081, product 48082, cart 48083, promotion 48084, order 48085, payment 48086, shipping 48087, notification 48088 (chỉ 127.0.0.1). gRPC không publish ra host.
- Mọi lệnh `docker compose` chạy ở `/home/tai/Document/nexusmart` để nhận `.env`.
- Tài khoản seed (dev): admin `admin@nexusmart.local` / `AdminDevPassword123!` (super_admin); seller `seller.bachdang|seller.saigon|seller.danang|seller.hanoi@nexusmart.local` / `SellerDevPassword123!`; buyer demo `minhquan.nguyen@nexusmart.vn` / `BuyerDevPassword123!` (CHỈ ĐỌC bằng tài khoản này: không đặt hàng, không sửa giỏ/địa chỉ/hồ sơ).
- Dữ liệu seed sinh từ `web/src/data/*` qua `tools/seed/seed.mjs`: 4 shop, 6 danh mục, 18 sản phẩm, 11 voucher, 3 khung flash sale (16 item), 1 buyer có 42.000 xu.

## Luật cứng
- KHÔNG sửa file nào trong repo ngoài `docs/audit/<tên>.md` (được tạo thư mục `docs/audit/`). Không git write (commit, stash, checkout, reset, clean, add).
- KHÔNG `make up|down|seed|e2e|e2e-web|proto`, KHÔNG `docker compose up|down|restart|stop|rm|build|kill`, không kill tiến trình. Stack dùng chung cho tám worker. Không đụng stack project `nexusmart` (cổng 28080, 3000) và `nexusmart-web` (38080, 33000); không dùng cổng 3111, 4777.
- DB và Redis chỉ đọc. Mọi thay đổi dữ liệu đi qua API như người dùng thật.
- Buyer: tự đăng ký tài khoản riêng `audit-<tên>-<n>@nexusmart.local` (lấy OTP qua Mailpit API, xem `tools/seed/seed.mjs` hàm `waitForOtp`). Seller/admin dùng tài khoản seed. Mọi thứ mình tạo (sản phẩm, voucher, flash sale, shop, danh mục...) đặt tên bắt đầu bằng `AUDIT-<tên>`.
- Không làm cạn tồn kho, suất flash sale, lượt voucher của dữ liệu seed: mỗi luồng 1–2 đơn là đủ; cần thử hết suất thì tự tạo voucher/flash sale/sản phẩm AUDIT riêng.
- Rate limit gateway theo IP (300 request/phút, dùng chung cho tám worker): không bắn tải, không vòng lặp quá 20 request. Gặp 429 thì chờ 60 giây rồi thử lại; không tính là lỗi trừ khi đang kiểm chính rate limit.
- Provider đều ở chế độ mock (VietQR, GHN). Không gọi dịch vụ ngoài thật. Webhook thử: ký HMAC-SHA256 bằng secret dev trong `.env` (`PAYMENT_WEBHOOK_SECRET`, `SHIPPING_WEBHOOK_SECRET`), cách ký xem `tests/e2e/05_webhook_security_test.go` và `docs/contracts.md`.
- Tìm code: skill `codebase-memory` (project `home-tai-Document-nexusmart`; index có thể cũ hơn HEAD, kết luận "không có" phải soát lại bằng `rg`). Tìm trong `pkg/common/env` và `web/` bằng `rg`.
- Được chạy lệnh kiểm chỉ đọc trên host: `go test`/`go vet` trong thư mục module, `cargo test` trong `services/payment`, `npm run lint|format:check|check:data` trong `web/`. Không `npm run build`, không `npm install`.

## Mỗi kết luận phải có bằng chứng
- ĐỦ: file:dòng backend + endpoint REST + route web (nếu UC có giao diện) VÀ một lần gọi thật trên stack (curl rút gọn, mã HTTP, trích body ngắn).
- LỖI: kịch bản tái hiện chạy lại được (curl đầy đủ, token thay bằng `$TOKEN`), mong đợi theo spec/contracts vs kết quả thật, file:dòng nghi là gốc.
- THIẾU: đã tìm ở đâu (pattern `rg`, `proto/`, router gateway, `web/src/app`) mà không thấy.
- Không suy đoán. Chưa kiểm được thì ghi CHƯA KIỂM kèm lý do.

## Báo cáo: `docs/audit/<tên>.md` (tiếng Việt, gọn, không văn mẫu)
1. Bảng UC: `| UC | Ưu tiên | Trạng thái | Backend (file:dòng) | REST | Web | Bằng chứng | Ghi chú |`. Trạng thái là một trong: ĐỦ, MỘT PHẦN, THIẾU, LỖI, CHƯA KIỂM.
2. Bảng đối chiếu Shopify/sàn (các dòng trong brief riêng): `| Nghiệp vụ | UC hoặc ngoài phạm vi | Trạng thái | Ghi chú |`.
3. Lỗi: mã `<TÊN-IN-HOA>-B<n>` (ví dụ `ORD-B3`), mức P0 (sai/mất tiền, lộ dữ liệu, vượt quyền, mất dữ liệu) / P1 (hỏng luồng UC Cao) / P2 (hỏng luồng UC Trung bình, lệch spec/contracts) / P3 (nhỏ, hiển thị). Mỗi lỗi: tái hiện, mong đợi vs thật, file:dòng.
4. Dữ liệu: mã `<TÊN>-D<n>`: dữ liệu seed hoặc hiển thị sai, thiếu, không nhất quán giữa DB / API / web (giá, tồn kho, ảnh 404, slug, danh mục, tiếng Việt, số dư, sổ cái lệch...).
5. Đề xuất sửa: mã `<TÊN>-F<n>`, mỗi mục ghi lỗi/UC nó giải quyết, việc cụ thể (file, RPC, migration, route), cỡ S/M/L, có cần đổi proto / migration / ADR không (AGENTS.md luật 11: năng lực hạ tầng mới cần ADR trước khi code).
6. Đã kiểm gì, chưa kiểm gì.

Câu trả lời cuối (stdout, ngắn): đường dẫn báo cáo, số UC theo từng trạng thái, danh sách lỗi P0/P1 mỗi lỗi một dòng.
