# Giai đoạn 0b – luật chung cho mọi worker

Đặc tả giai đoạn: `.agents/briefs/sites-0b-web-gateway.md` (đọc toàn bộ, kể cả mục Gate). Brief riêng của từng worker nằm ở `.agents/briefs/0b/<tên>.md`. Đọc thêm `AGENTS.md`, `web/CLAUDE.md`, `docs/contracts.md`, `docs/CORE_REPORT.md`, `docs/SECURITY_0A_REPORT.md`, `web/FEATURE_MATRIX.md`, `docs/research/sites/fit.md`, `docs/research/sites/review-opus.md` (phần liên quan tới mình).

Các worker chạy song song theo pha, mỗi worker là một tiến trình agy riêng, không có board chung. Manager (Claude Code) điều phối giữa các pha và review sau mỗi pha.

## Chỗ làm việc
- Làm trong worktree `/home/tai/Document/nexusmart-web` (nhánh `feat/web-gateway`). KHÔNG đọc/sửa/chạy lệnh trong `/home/tai/Document/nexusmart`: thư mục đó là của một phiên khác đang làm việc.
- Đây là git repo. Không `git commit`, `git stash`, `git checkout`, `git reset`, `git clean`: manager commit sau khi review. Mọi worker dùng chung một cây file, nên chỉ sửa file mình sở hữu (bảng dưới và brief riêng). Cần sửa file của worker khác: ghi vào mục "Nhờ worker khác" trong báo cáo cuối, kèm file và lý do; không tự sửa.
- Tìm code Go bằng skill `codebase-memory` với project `home-tai-Document-nexusmart-web` (đã index worktree này); tìm trong `pkg/common/env` và `web/` thì dùng `rg`.

## Backend của worktree
- Stack compose riêng, project `nexusmart-web`, cổng host khai trong `.env` ở gốc worktree (không commit, không sửa file này). Gateway REST: `http://localhost:38080`. Mailpit UI/API: `http://localhost:38025`. Postgres: `localhost:35432`. Web trong compose (sau khi `platform` thêm): `http://localhost:33000`.
- Stack project `nexusmart` (gateway `28080`, postgres `5432`) là của phiên khác: không đụng container, không gọi API của nó.
- Không `make down`, không `docker compose down -v`, không `sudo`, không công bố lại cổng gRPC, không nới `pkg/common/auth`. Đổi schema = migration mới.
- Chỉ chủ code của service mới rebuild service đó: `docker compose up -d --build <service>` (chạy ở gốc worktree để nhận `.env`). `platform` chỉ rebuild `web`.

## Next.js
- Server của manager (cổng 3111) và monitor (4777): không kill, không dùng.
- Cổng dev riêng: bff 3201, catalog 3202, checkout 3203, account 3204, seller 3205, qa 3210, verifier 3220.
- Từ pha B, mọi lệnh `next build`/`next dev`/`next start` dùng `NEXT_DIST_DIR=.next-<tên-mình>` để không giẫm thư mục build của nhau (`platform` thêm `distDir` đọc biến này ở pha A). Ở pha A chỉ `bff` chạy `next build`/`next dev` trên host (thư mục mặc định `.next`); `platform` kiểm build web bằng Docker.
- Biến môi trường dùng chung (tên đã chốt, không đổi): `GATEWAY_URL` (Next server gọi gateway; dev trên host: `http://localhost:38080`, trong compose: `http://gateway:8080`), `COOKIE_SECURE` (`false` để tắt cờ `Secure` khi chạy http cục bộ; mặc định bật khi `NODE_ENV=production`), `APP_ORIGIN` (tuỳ chọn, origin công khai dùng khi kiểm `Origin` sau reverse proxy).

## Luật cứng (giữ từ brief 0b)
- Không thêm dependency runtime vào `web/`. Không thư viện state, không framework dữ liệu tự dựng. Ponytail: diff nhỏ nhất đúng gốc.
- Giữ nguyên UI và art direction (skill `design-system`, `frontend-design`); chữ cho người mua là tiếng Việt tự nhiên, không thuật ngữ kỹ thuật.
- Không lộ lỗi nội bộ gateway ra UI; không tin `shop_id`/`user_id` từ client; mọi trang tài khoản/đơn/seller/admin kiểm phiên ở server; RBAC theo role backend trả về.
- Token chỉ nằm trong cookie `HttpOnly`; không lưu token, giỏ, đơn trong `localStorage`.
- CI chỉ `workflow_dispatch`. Commit message (manager viết) bằng tiếng Anh, không dòng ghi công AI.

## Vùng đang có thay đổi chưa merge ở nhánh khác (KHÔNG sửa)
`services/order/internal/{config,service,store,db}/**`, `services/order/queries/**`, `services/order/migrations/**`. Nếu luồng web thật sự cần đổi ở đó: dừng phần đó, ghi vào báo cáo để manager xử lý. Migration mới của `product` bắt đầu từ `000003_`.

## Báo cáo cuối (câu trả lời cuối của tiến trình agy)
1. Đã xong / chưa xong, đối chiếu từng tiêu chí trong brief riêng.
2. File đã tạo/sửa.
3. Lệnh kiểm tra đã chạy và kết quả thật (dán dòng kết quả, không tóm tắt "đều xanh").
4. Nhờ worker khác (file, lý do).
5. Quyết định đáng chú ý và giới hạn còn lại (`ponytail:` nếu cắt góc có trần).

## Bảng sở hữu theo pha
| Pha | Worker | Sở hữu |
|---|---|---|
| A | bff | `web/src/lib/server/**`, `web/src/lib/api.ts`, `web/src/lib/gateway-types.ts`, `web/src/middleware.ts`, `web/src/app/api/auth/**`, `web/src/app/api/gw/**`, `web/src/app/layout.tsx`, `web/src/context/SessionContext.tsx`, trang `(shop)/dang-nhap`, `(shop)/dang-ky`; ở pha A được sửa mọi file khác trong `web/src` CHỈ để thay phần đăng nhập/đăng ký/đăng xuất mock bằng phiên thật |
| A | platform | `Makefile`, `docker-compose.yml`, `.env.example`, `.dockerignore`, `.github/workflows/**`, `web/Dockerfile`, `web/next.config.ts`, `web/package.json`, `web/eslint.config.mjs`, `.gitignore`, `web/.prettierignore`, `tools/seed/**` |
| A | gateway | `services/**` (trừ vùng cấm ở trên), `proto/**`, `gen/**`, `pkg/common/**` (không nới auth), `docs/contracts.md`, `docs/web-gap-audit.md`, `tests/e2e/**` |
| B0 | store-split | tách `StoreContext` thành `CartContext`, `OrdersContext`, `web/src/mock/MockStoreContext.tsx` (brief `0b/store-split.md`) |
| B1 | catalog, checkout, account, seller-admin, qa | mục "Sở hữu" trong brief riêng `0b/<tên>.md`; điểm giao nhau theo `0b/b1-contracts.md` |
