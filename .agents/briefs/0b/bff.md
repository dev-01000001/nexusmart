# 0b pha A – worker `bff`: phiên thật, proxy cùng origin, đăng ký/đăng nhập

Đọc trước: `.agents/briefs/sites-0b-swarm-rules.md` (luật chung, bảng sở hữu), `.agents/briefs/sites-0b-web-gateway.md` mục 1 và 8, `docs/contracts.md` §3 và §4.1, `web/CLAUDE.md`. Chạy song song với `platform` (Docker/compose/seed) và `gateway` (endpoint còn thiếu): không sửa file của họ.

## Mục tiêu
Nền dữ liệu thật cho `web/`: trình duyệt chỉ gọi Next (cùng origin), Next server gọi gateway. Sau pha này các worker pha B chỉ việc dùng helper của bạn để đọc/ghi dữ liệu thật.

## Việc phải làm
1. **Client gateway phía server** (`web/src/lib/server/gateway.ts`, có `import "server-only"` nếu không cần thêm package; nếu không có thì bỏ): gọi `GATEWAY_URL` + `/api/v1/...`, gắn `Authorization: Bearer <access>` từ cookie, timeout (`AbortSignal.timeout`), trả lỗi có `status` + `code` để trang xử lý; không ném chi tiết nội bộ ra UI. Server component và route handler của pha B dùng hàm này.
2. **Phiên cookie** (`web/src/lib/server/session.ts`): access token và refresh token trong cookie `HttpOnly`, `SameSite=Lax`, `Path=/`, không đặt `Domain`, `Secure` theo `COOKIE_SECURE` (mặc định bật khi `NODE_ENV=production`). `maxAge` access theo `expires_in_seconds`, refresh theo `REFRESH_TOKEN_TTL` của service `user` (7 ngày).
3. **Làm mới token**: đọc service `user` (`services/user`) để biết refresh token có xoay vòng và có thu hồi cả họ token khi một refresh token cũ bị dùng lại hay không. Thiết kế sao cho nhiều request song song lúc access token hết hạn KHÔNG làm mất phiên. Lưu ý: server component không ghi được cookie; chỗ ghi cookie là middleware, route handler, server action. Gợi ý đơn giản: middleware làm mới chủ động khi access sắp hết hạn (đọc `exp` chỉ để hẹn giờ, không dùng để phân quyền), còn client helper gặp 401 thì gọi `/api/auth/refresh` một lần (single-flight trong tab) rồi thử lại.
4. **Proxy cùng origin** `web/src/app/api/gw/[...path]/route.ts`: chuyển GET/POST/PUT/PATCH/DELETE tới `GATEWAY_URL/api/v1/<path>` kèm query và body, chỉ chuyển header cần thiết (`Content-Type`, `Accept`, `Idempotency-Key`), gắn Bearer từ cookie. Chặn `webhooks/**` và `auth/**` (đi qua `/api/auth`). Chuyển tiếp được SSE `payments/{id}/events` (stream, không buffer). Lỗi 5xx/mạng → JSON chung `{"error":{"code":"UNAVAILABLE","message":"<tiếng Việt chung chung>"}}`; lỗi 4xx chuyển `status` và `code` của gateway, message không chứa chi tiết nội bộ.
5. **CSRF**: mọi request ghi (không phải GET/HEAD/OPTIONS) vào `/api/**` phải có header `Origin` trùng origin của site (`APP_ORIGIN` nếu đặt, không thì từ `Host`/`X-Forwarded-Host` + proto); thiếu hoặc lệch → 403. Server action đã có kiểm Origin của Next, không cần làm lại.
6. **Route auth** `web/src/app/api/auth/{register,verify-otp,resend-otp,login,refresh,logout,session}/route.ts`: login/refresh ghi cookie, response không chứa token; logout gọi gateway thu hồi refresh token rồi xoá cookie; `session` trả `{user}` hoặc `{user:null}` (dùng cho header phía client).
7. **Middleware** `web/src/middleware.ts`: trang cần phiên (`/tai-khoan`, `/don-hang`, `/thanh-toan`, `/seller/**`, `/admin/**`) không có phiên → chuyển `/dang-nhap?next=<đường dẫn>` (chỉ nhận `next` là đường dẫn nội bộ, chống open redirect). Kiểm role seller/admin làm ở server (helper `requireRole(...)` trong `lib/server` gọi `GET /users/me`, role lấy từ backend), không tin claim đọc thô trong middleware. Gắn `requireRole` vào layout `(seller)` và `(admin)`.
8. **Phiên phía client**: `web/src/context/SessionContext.tsx` (provider + hook) lấy `/api/auth/session` khi mount, để trang tĩnh vẫn tĩnh (không biến cả site thành dynamic chỉ vì header). Header (`ShopHeader`) và menu tài khoản hiện tên người dùng thật / nút đăng nhập.
9. **Trang** `(shop)/dang-nhap` và `(shop)/dang-ky`: đăng ký → nhập OTP gửi qua email (mailpit trong dev) → gửi lại OTP → đăng nhập; lỗi thật (sai mật khẩu, email đã dùng, OTP sai/hết hạn, tài khoản bị khoá, hệ thống bận) hiện bằng chữ tiếng Việt tự nhiên, giữ nguyên thiết kế hiện có. Sau đăng nhập về `next` hoặc trang chủ.
10. **Bỏ mock đăng nhập**: thay `loginMock`/`registerMock`/`logoutMock`/`user`/`isAuthenticated` của `StoreContext` bằng phiên thật ở mọi nơi dùng (grep toàn `web/src`), rồi xoá các trường đó khỏi `StoreContext`. Không đụng phần giỏ/đơn/địa chỉ của `StoreContext` (pha B làm).
11. **Client helper** `web/src/lib/api.ts`: `api(path, init)` gọi `/api/gw/...`, xử lý 401 → refresh một lần → thử lại, trả lỗi có `code` để UI chọn câu tiếng Việt. Kiểu dữ liệu gateway dùng chung đặt ở `web/src/lib/gateway-types.ts` (khai theo `docs/contracts.md`, chỉ những kiểu bạn dùng + user/cart/order/product cơ bản để pha B dùng tiếp).

## Tiêu chí nghiệm thu
- `cd web && npm run lint && npm run format:check && npm run build` sạch (`.next` mặc định; chỉ bạn build ở pha A).
- Chạy `GATEWAY_URL=http://localhost:38080 COOKIE_SECURE=false npx next dev -p 3201` (hoặc `next start -p 3201` sau build) rồi kiểm bằng `curl` có cookie jar, dán kết quả vào báo cáo:
  1. Đăng ký email mới → lấy OTP từ mailpit API `http://localhost:38025/api/v1/messages` → verify → login: `Set-Cookie` có `HttpOnly`, `SameSite=Lax`, không `Domain`; body không có token.
  2. `GET /api/gw/users/me` có cookie → 200; không cookie → 401.
  3. `POST /api/gw/cart/items` có cookie nhưng `Origin: http://evil.example` → 403; không có `Origin` → 403; đúng `Origin` → đi tới gateway.
  4. `GET /api/gw/webhooks/...` và `/api/gw/auth/login` → bị chặn.
  5. Refresh: gọi `/api/auth/refresh` → cookie mới; dùng lại refresh token cũ bị từ chối; 10 request song song tới `/api/gw/users/me` ngay sau khi xoá cookie access (giữ refresh) qua client flow không làm mất phiên (mô tả cách bạn kiểm).
  6. Logout → cookie bị xoá, refresh token cũ không dùng được nữa.
  7. Vào `/tai-khoan` không phiên → 307 tới `/dang-nhap?next=%2Ftai-khoan`; `/dang-nhap?next=https://evil.example` sau đăng nhập không ra ngoài site.
  8. Tài khoản buyer vào `/seller` và `/admin` → không xem được (404 hoặc chuyển trang), admin bootstrap (`ADMIN_BOOTSTRAP_EMAIL`/`PASSWORD` trong `.env`) vào `/admin` được.
- `rg -n "localStorage" web/src` không có token; `rg -n "loginMock|registerMock|logoutMock" web/src` rỗng.
- Kiểm trực quan trang đăng nhập/đăng ký ở 390px và 1440px (Playwright nếu có trong môi trường, không thì mô tả), không vỡ thiết kế.
