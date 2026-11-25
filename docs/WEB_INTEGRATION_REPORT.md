# Báo cáo tích hợp `web/` với API Gateway — Tổng hợp sau gộp `main` (+ `3b-3`) và `phase-0b`

> **Trạng thái hiện hành (sau gộp 2026-11-25):** Đã hợp nhất hai nhánh `main` (kèm sóng `3b-3`) và `phase-0b` theo [ADR-0010](adr/0010-unify-main-and-phase-0b.md). Toàn bộ tài liệu hợp đồng sống nằm tại [docs/contracts.md](contracts.md) và hướng dẫn phát triển frontend nằm tại [web/CLAUDE.md](../web/CLAUDE.md). Tài liệu này giải xung đột và lưu trữ đầy đủ kết quả tích hợp Phase 0b từ cả hai nhánh cùng kiến trúc web đã chốt sau gộp.

---

## 1. Kiến trúc `web/` sau gộp (hiện hành)

1. **Bỏ sàn chung, chuyển 100% mua hàng sang site gian hàng độc lập (`(site)`):**
   - **Host gốc (`<SITE_ROOT_DOMAIN>`, mặc định `nexusmart.localhost`, hoặc `localhost`):** Chỉ phục vụ trang giới thiệu nền tảng + danh bạ site đang xuất bản (`(shop)` gồm 7 trang và 2 chuyển hướng `308` tại `web/src/app/(shop)/gian-hang/[slug]/route.ts` và `web/src/app/(shop)/san-pham/[slug]/route.ts`), Kênh người bán (`/seller/**`, 16 trang) và Cổng quản trị (`/admin/**`, 9 trang). Mọi Buyer API trả `404` trên host gốc.
   - **Host site gian hàng (`<subdomain>.<SITE_ROOT_DOMAIN>` hoặc tên miền riêng đã xác minh):** `web/src/middleware.ts` xác thực host qua `web/src/lib/site/host.ts` và `web/src/lib/server/siteLookup.ts`, rồi rewrite nội bộ sang `web/src/app/(site)/s/[host]/**` (21 trang: trang chủ tùy biến v2, danh mục/sản phẩm, PDP có đánh giá + tóm tắt AI, tìm kiếm cô lập theo shop, flash sale, voucher shop, chương trình giảm giá shop, giỏ hàng cô lập, thanh toán COD/VietQR/ZaloPay, trang chờ `/thanh-toan/cho/[id]`, đơn hàng/đổi trả/giao lại/hoàn tiền, tra cứu đơn, tài khoản/OTP SĐT/cài đặt thông báo, đăng nhập/đăng ký/quên mật khẩu). Truy cập trực tiếp `/s/*`, `/seller/*`, `/admin/*` trên host site trả `404`.
2. **BFF tường minh thay cho proxy catch-all:**
   - Gỡ bỏ catch-all `/api/gw/*` từ nhánh `main`; toàn bộ `web/src/app/api/**` (179 route handler tường minh) xác định `shop_id` từ `Host` và ép `sales_channel = "STOREFRONT"` + `storefront_shop_id` ở máy chủ (`web/src/lib/server/storefront.ts`).
   - Next.js kết nối Gateway qua mạng Docker nội bộ `bff` (`172.31.250.0/28`, `GATEWAY_URL=http://gateway-bff:8080`, 0 header CORS trên Gateway).
   - Phiên lưu hoàn toàn trong cookie `HttpOnly`, `SameSite=Lax`, **host-only** (`__Host-nm_at`/`nm_at`, `__Host-nm_rt`/`nm_rt` qua `web/src/lib/server/config.ts`, `web/src/lib/server/tokens.ts`, `web/src/lib/server/session.ts`), xoay refresh token chống thu hồi nhầm token family và kiểm CSRF theo `Origin` trong `web/src/lib/server/http.ts`.
3. **Hạ tầng và kiểm thử hiện hành:**
   - `web/Dockerfile` build đa giai đoạn từ root repo trên Node 24 (`output: "standalone"`, chạy user không phải root `nextjs:10001`), cổng host `WEB_PORT` (mặc định `3300`).
   - Nạp dữ liệu mẫu idempotent bằng `make seed` (`tools/seed/seed.mjs`, xem `tools/seed/README.md`), kiểm thử unit bằng `cd web && npm run test:unit` và `npm run check:data`, kiểm thử E2E BFF bằng `make e2e-web` (`tests/e2e-web/`).
4. **Kiểm toán `localStorage`:**
   - Không lưu giỏ hàng, đơn hàng, token phiên hay PII trong `localStorage`. `web/src/context/StoreContext.tsx` chỉ lưu 4 tùy chọn tiện ích người xem (`nexusmart_viewer_prefs_v1`: `bodyProfile`, `wishlist`, `recentlyViewed`, `cookiePrefs`) và dọn khóa cũ `nexusmart_store_v2`; `web/src/features/seller/SellerShell.tsx` chỉ lưu trạng thái thu gọn thanh bên.

---

## 2. Lịch sử Phase 0b — Nhánh `phase-0b` (nền BFF tường minh & giao diện hiện hành)

- **Ngày nghiệm thu gốc:** `2026-11-20` (commit nền `phase-0b`, đặc tả `.agents/briefs/sites-0b-web-gateway.md`).
- **Kiến trúc BFF đã chọn giữ làm nền:**
  - Route handler tường minh theo miền (`/api/auth/**`, `/api/cart/**`, `/api/vouchers/**`, `/api/checkout/**`, `/api/payments/**`, `/api/orders/**`, `/api/seller/**`, `/api/admin/**`), timeout `GATEWAY_TIMEOUT_MS=8000`, retry backoff cho `GET` khi `502/503/504`, chuẩn hóa lỗi qua `web/src/lib/server/errors.ts`.
  - Bảo vệ CSRF qua `assertSameOrigin` (`web/src/lib/server/http.ts`, từ chối `Sec-Fetch-Site: cross-site|same-site` và `Origin` không khớp → `403 BAD_ORIGIN`).
  - Tách biệt nguồn sự thật giỏ hàng trong `web/src/context/StoreContext.tsx` (100% từ `/api/cart`) khỏi state mẫu in-memory cũ (toàn bộ dữ liệu mẫu thương mại sau đó đã được gỡ hoàn toàn khỏi `web/src/`).
  - Bổ sung cổng mặc định `WEB_PORT=3300` (tránh trùng cổng `3111`/`4777`), các bước nâng cấp lên đa cửa hàng theo subdomain (Giai đoạn 2–4, redesign UI) và bộ kiểm thử `tests/e2e-web/01_root_platform.test.mjs` … `08_site_templates.test.mjs`.
- **Kết quả xác minh giai đoạn 0b trên `phase-0b`:**
  - `cd web && npm run lint`, `npm run format:check`, `npx tsc --noEmit`, `npm run build` đều PASS (0 lỗi); unit test web 38/38 PASS; `make lint` / `make test` / `make e2e` (8/8) / `make seed` (idempotent) / `make e2e-web` (10/10) đều PASS; kiểm tra trực quan Playwright (1440/768/390) đạt 0 FAIL trên 6 tuyến trọng yếu (đã sửa tràn ngang 14px ở `ShopHeader`).

---

## 3. Lịch sử Phase 0b — Nhánh `main` / `feat/web-gateway` (phần backend & bảo mật đã hợp nhất)

- **Phạm vi đóng góp được giữ lại sau gộp:**
  - **Migration & slug chuẩn hóa tiếng Việt:** Thêm cột `slug` cho `products` và `shops` (đã đánh số lại thành `services/product/migrations/000005_add_product_slug.sql` và `services/user/migrations/0005_add_shop_slug.sql` sau các migration của `phase-0b`).
  - **RPC & REST Gateway đã hợp nhất:** `UserService.ListPublicShops` (hợp nhất với `ListActiveShops` của `phase-0b`), `GET /api/v1/shops`, `GET /api/v1/shops/{id}` & `GET /api/v1/shops/slug/{slug}` (chỉ trả `publicShopDTO` cho shop `ACTIVE`), `GET /api/v1/shops/me`, `GET /api/v1/products/slug/{slug}`, `GET /api/v1/seller/{shop,products,products/{id},orders/{id},vouchers}`, `GET /api/v1/admin/products*`.
  - **Các bản vá bảo mật đã giữ lại trong code gộp:**
    1. Chặn seller tự duyệt sản phẩm qua chuỗi `PENDING`/`REJECTED` → `HIDDEN` → `APPROVED` trong `services/product/internal/server/server.go`.
    2. Chống open redirect sau chuẩn hóa dot-segment (`next=/..//evil.com`) qua `web/src/lib/redirect.ts` (`sanitizeNextPath`).
    3. Kiểm tra chủ sở hữu SKU trước khi seller đăng ký flash sale (`services/gateway/internal/router/handlers_promotion.go`, khác shop → `403`, không tồn tại → `404`).
    4. Xoay `Idempotency-Key` và chống gửi trùng ở thao tác hoàn tiền admin; `GET /api/auth/me` không xoay token ngoài ý muốn; các phát hiện về `services/order` (`Idempotency-Key` hash, luồng đổi trả 10 trạng thái, tra cứu đơn khách `POST /api/v1/orders/lookup`, đổi địa chỉ trước lấy hàng, ZaloPay, kiểm duyệt & 6 vai trò admin) đã được hoàn thiện ở các sóng tiếp theo trên `main` và hợp nhất đầy đủ.
    5. Bảo vệ `X-Forwarded-For` bằng mạng nội bộ `bff` (`172.31.250.0/28`) + `TRUSTED_PROXIES` trong `docker-compose.yml` và `services/gateway/internal/middleware/clientip.go`.
- **Phần của `feat/web-gateway` đã thay thế khi gộp:**
  - Nhánh giao diện sàn chung `(shop)` (45 route) và proxy catch-all `/api/gw/*` đã được thay bằng mô hình đa cửa hàng độc lập `(site)/s/[host]/**` và BFF tường minh `/api/*` của `phase-0b`.

---

## 4. Hạn chế còn lại sau đợt gộp

- Lịch sử tìm kiếm người dùng (`/api/v1/search/history`) đã có ở backend/gateway nhưng chưa mở giao diện trên `(site)` trong đợt gộp này.
- Các hạng mục hoãn sang đợt sau (ZaloPay live mode, `assistant` chuyển sang `GetSellerReport`, siết `ChatService.ExchangeWSTicket` bằng service token, kênh khiếu nại độc lập không kèm đổi trả) được theo dõi trong [ADR-0010](adr/0010-unify-main-and-phase-0b.md) và [docs/UPGRADE_FROM_PHASE_0B.md](UPGRADE_FROM_PHASE_0B.md).
