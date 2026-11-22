# 0b pha A – worker `platform`: Docker, compose, CI, seed

Đọc trước: `.agents/briefs/sites-0b-swarm-rules.md` (luật chung, bảng sở hữu), `.agents/briefs/sites-0b-web-gateway.md` mục 4 và 7, `docs/contracts.md` §4, skill `platform-stack`, `web/CLAUDE.md`. Chạy song song với `bff` (đang sửa `web/src`) và `gateway` (đang sửa `services/**`): không sửa file của họ.

## Việc phải làm
1. **`web/next.config.ts`**: `output: "standalone"`, `distDir: process.env.NEXT_DIST_DIR || ".next"`; giữ cấu hình hiện có. Thêm `.next-*` vào `.gitignore`, ignore của eslint/prettier nếu cần.
2. **`web/Dockerfile`** nhiều giai đoạn, build context là gốc repo như các service khác (`docker compose` `context: .`, `dockerfile: web/Dockerfile`), `npm ci` từ `web/package-lock.json`, chạy bằng user không phải root, chỉ copy output standalone + `public` + `.next/static`, `HEALTHCHECK` hoặc healthcheck trong compose. Repo chưa có `.dockerignore`: thêm `.dockerignore` ở gốc (loại `.git`, `**/node_modules`, `**/.next*`, `**/target`, …) và xác nhận build của MỌI service hiện có vẫn chạy (`docker compose build`), vì file này áp cho cả build Go/Rust.
3. **Service `web` trong `docker-compose.yml`**: cổng `${WEB_PORT:-3000}:3000` (không bao giờ 3111), `GATEWAY_URL=http://gateway:8080`, `COOKIE_SECURE=${COOKIE_SECURE:-false}` (local chạy http), `depends_on` gateway healthy. Cập nhật `.env.example` (`WEB_PORT`, `COOKIE_SECURE`, `GATEWAY_URL` cho dev trên host, `APP_ORIGIN` tuỳ chọn) kèm chú thích ngắn.
4. **CI**: thêm job `web` vào `.github/workflows/ci.yml` (hoặc file mới) chạy `npm ci`, `npm run lint`, `npm run format:check`, `npm run check:data`, `npm run build` trong `web/`. Trigger CHỈ `workflow_dispatch`.
5. **`make seed`** (`tools/seed/**`, ngôn ngữ tuỳ bạn, ưu tiên không thêm dependency: Node 24 có `fetch` và chạy được TypeScript đơn giản bằng type stripping, hoặc Go): nạp vào backend qua gateway REST (`SEED_GATEWAY_URL`, mặc định `http://localhost:${GATEWAY_PORT}` đọc từ `.env`) đúng dữ liệu mẫu hiện có của web:
   - nguồn là chính `web/src/data/*` (đọc trực tiếp hoặc sinh ra từ đó), không chép tay một bản lệch;
   - 4 gian hàng (mỗi gian hàng một tài khoản người bán: đăng ký → OTP lấy từ mailpit API `MAILPIT_URL`, mặc định `http://localhost:${MAILPIT_UI_PORT}` → đăng nhập → đăng ký shop → admin bootstrap duyệt; xác nhận role đổi sang `seller` và đăng nhập lại để lấy token mới);
   - danh mục (admin), 18 sản phẩm với đủ biến thể/SKU, giá `*_vnd`, tồn kho, khối lượng, ảnh (đường dẫn tương đối như `/products/...`, `/models/...` để web phục vụ từ `web/public`), admin duyệt sản phẩm;
   - voucher sàn/freeship (admin) và voucher gian hàng (seller) tương ứng `web/src/data/vouchers.ts`; khung flash sale tương ứng `web/src/data/flash-sale.ts`, đặt thời gian theo "bây giờ" để luôn có một khung đang chạy và một khung sắp tới, seller thêm item, admin duyệt;
   - một tài khoản người mua demo (có một địa chỉ mặc định hợp lệ theo `GET /shipping/provinces`) và một ít xu nếu dữ liệu mẫu có;
   - in ra bảng tài khoản demo (email/mật khẩu dev, không secret thật) và ghi vào `tools/seed/README.md`;
   - **chạy lại nhiều lần không tạo trùng** (kiểm tồn tại trước khi tạo; 409/đã tồn tại coi là xong), và báo rõ phần nào tạo mới / đã có;
   - slug: web đang dùng `/san-pham/[slug]` và `/gian-hang/[slug]`. Ghi trong báo cáo slug sản phẩm backend sinh ra có trùng slug trong `web/src/data/products.ts` không, và gian hàng có slug không (worker `gateway` đang rà khoảng hở này; nếu seed cần endpoint chưa có, ghi vào "Nhờ worker khác" thay vì sửa `services/**`).
6. **`make e2e-web`**: target chạy `node --test tests/e2e-web/` với `WEB_URL` (mặc định `http://localhost:${WEB_PORT}`), `MAILPIT_URL`, `GATEWAY_URL`. Thư mục test do worker `qa` viết ở pha B; bạn chỉ thêm target.

## Tiêu chí nghiệm thu
- `docker compose config` hợp lệ; `docker compose build` toàn bộ service thành công với `.dockerignore` mới (dán dòng cuối).
- `docker compose up -d --build web` (ở gốc worktree) → `curl -sI http://localhost:33000/` trả 200, container chạy user không phải root (`docker compose exec web id -u` ≠ 0). Nếu build web lỗi do `bff` đang sửa dở `web/src`, đợi và thử lại; vẫn lỗi thì ghi rõ lỗi trong báo cáo.
- `make seed` chạy trên stack trống (stack của worktree hiện chưa có dữ liệu) rồi chạy lại lần hai: lần hai không tạo bản ghi mới. Dán tóm tắt hai lần chạy và số liệu kiểm: `curl -s localhost:38080/api/v1/products?page_size=50` đủ 18 sản phẩm, `.../categories`, `.../flash-sales?include_upcoming=true`, `.../vouchers`.
- Không sửa `web/src/**`, `services/**`. `make lint` vẫn sạch (seed viết bằng Go thì nằm trong lint/test hoặc ghi lý do).
