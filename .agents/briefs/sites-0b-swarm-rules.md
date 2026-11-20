# Swarm 0b – luật chung và bảng sở hữu file

Đặc tả: `.agents/briefs/sites-0b-web-gateway.md` (đọc toàn bộ, kể cả mục Gate). Đọc thêm `AGENTS.md`, `docs/CORE_REPORT.md`, `docs/SECURITY_0A_REPORT.md`, `web/DESIGN_REPORT.md`, `web/FEATURE_MATRIX.md`, `docs/research/sites/fit.md`, `review-opus.md` (phần liên quan tới mình).

## Luật cứng
- Thư mục KHÔNG phải git repo, mọi worker dùng chung một bản. Chỉ sửa file mình sở hữu (bảng dưới). Cần sửa file của người khác: nhờ chủ file trên board bằng `@tên`.
- Server của manager: `next start` ở cổng 3111 chạy từ `web/.next`. TUYỆT ĐỐI không ghi vào `web/.next`: không chạy `npm run build`/`next dev`/`next start` trong `web/` với distDir mặc định. Sau khi `platform` báo `[ready] distDir`, dùng `NEXT_DIST_DIR=.next-<tên-mình>` cho mọi lệnh next. Gate `npm run build` nguyên văn thì `verifier` chạy trong bản sao của `web/` bên ngoài cây dự án.
- Không kill tiến trình ở cổng 3111, 4777. Cổng dev riêng: bff 3201, catalog 3202, checkout 3203, account 3204, seller 3205, qa 3210, verifier 3220.
- Backend đang chạy (`make up`), gateway REST `http://localhost:28080`. Không `make down`, không `sudo`, không công bố lại cổng gRPC, không nới `pkg/common/auth`. Đổi schema = migration mới.
- Không thêm dependency runtime vào `web/`. Không thư viện state, không framework dữ liệu tự dựng. Ponytail: diff nhỏ nhất đúng gốc.
- Giữ nguyên UI/art direction; chữ người mua tiếng Việt, không thuật ngữ kỹ thuật. Dùng skill `frontend-design` khi đụng UI, `codebase-memory` để tìm code Go.
- Không lộ lỗi nội bộ gateway ra UI; không tin `shop_id`/`user_id` từ client; mọi trang tài khoản/đơn/seller/admin kiểm phiên ở server.
- Trên board: chỉ `@` người phải hành động tiếp. Báo xong phần mình: `@coordinator [done] <tóm tắt, file đã đổi, lệnh kiểm tra + kết quả>`. Bị chặn: `@coordinator [blocked] <lý do>`.

## Bảng sở hữu
| Worker | Sở hữu |
|---|---|
| bff | `web/src/lib/server/**` (client gateway, phiên cookie, refresh, CSRF), `web/src/middleware.ts`, `web/src/app/api/auth/**`, `web/src/app/layout.tsx`, header/provider phiên (component header/user menu) |
| gateway | `services/**` (gateway REST + bổ sung tối thiểu ở service), `proto/**`, `gen/**`, migration |
| platform | `Makefile`, `docker-compose.yml`, `.env.example`, `.github/workflows/**`, `web/Dockerfile`, `web/.dockerignore`, `web/next.config.ts`, `web/eslint.config.mjs`, `web/package.json`, `.gitignore`/prettier ignore, công cụ seed (`tools/seed/**` hoặc `scripts/seed/**`) |
| catalog | route trang chủ, danh mục, danh sách/lọc sản phẩm, trang sản phẩm, gian hàng, flash sale, trang voucher; hàm đọc `web/src/lib/catalog*` |
| checkout | giỏ, áp voucher/xu, thanh toán, trang chờ VietQR; `web/src/app/api/{cart,checkout,payments,vouchers}/**`; `web/src/context/StoreContext.tsx` và việc tách mock sang một chỗ rõ ràng (`web/src/mock/**`) |
| account | đăng ký/OTP/đăng nhập (UI), tài khoản, danh sách + chi tiết đơn; `web/src/app/api/orders/**` |
| seller | seller center + admin; `sellerStore`, `auditStore`; `web/src/app/api/{seller,admin}/**` |
| qa | `tests/e2e-web/**` (test đầu-cuối qua BFF); nhờ `platform` thêm target `make e2e-web` |
| verifier | chỉ đọc + chạy gate; viết `docs/WEB_INTEGRATION_REPORT.md` ở cuối |

File `web/src/data/*`: không xoá file còn người khác dùng; muốn bỏ thì hỏi trên board. Mock còn giữ chuyển về `web/src/mock/**` (checkout điều phối).
