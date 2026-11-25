# Web Frontend (`web/`)

Next.js 15 (`^15.5.29`) App Router, React 19 (`^19.1.0`), TypeScript 5.8 (`^5.8.3`, `strict: true`, alias `@/*` → `src/*`), runtime Node 24. Mô hình **đa cửa hàng độc lập theo tên miền phụ & tên miền riêng** (đã **bỏ sàn chung**): gồm 4 route group với **53 `page.tsx`**, **2 chuyển hướng `308` (`route.ts`)** và **179 BFF API route handler tường minh (`src/app/api/**`)**:

- **`(shop)` — Host gốc nền tảng (`<SITE_ROOT_DOMAIN>`, mặc định `nexusmart.localhost`, hoặc `localhost`):** 7 trang giới thiệu nền tảng, danh bạ gian hàng đã xuất bản, đăng nhập/đăng ký/quên & đặt lại mật khẩu, trợ giúp (`/`, `/ve-nexusmart`, `/tro-giup`, `/dang-nhap`, `/dang-ky`, `/quen-mat-khau`, `/dat-lai-mat-khau`) và 2 chuyển hướng `308` (`/gian-hang/[slug]`, `/san-pham/[slug]`) sang site đã xuất bản. Mọi Buyer API trả `404` trên host gốc.
- **`(site)/s/[host]` — Site gian hàng độc lập (`<subdomain>.<SITE_ROOT_DOMAIN>` hoặc tên miền riêng đã xác minh):** 21 trang khép kín toàn bộ hành trình mua sắm (`/`, `/san-pham`, `/san-pham/[slug]`, `/tim-kiem`, `/flash-sale`, `/voucher`, `/gio-hang`, `/thanh-toan`, `/thanh-toan/cho/[id]`, `/thanh-toan/mock-zalopay`, `/don-hang`, `/don-hang/[id]`, `/tra-cuu-don-hang`, `/tra-cuu-don`, `/tai-khoan`, `/gioi-thieu`, `/chinh-sach`, `/dang-nhap`, `/dang-ky`, `/quen-mat-khau`, `/dat-lai-mat-khau`). `src/middleware.ts` tự rewrite theo `Host`; truy cập trực tiếp `/s/*`, `/seller/*`, `/admin/*` trên host site trả `404`.
- **`(seller)` — Kênh người bán (chỉ trên host gốc):** 16 trang (`/seller`, mở gian hàng ngay khi chưa có shop, sản phẩm/nhập CSV/tồn kho, đơn hàng/lên đơn lô/nhãn PDF, trả hàng `/seller/tra-hang`, khuyến mãi/flash sale/chương trình giảm giá, tài chính/sổ cái/tài khoản ngân hàng, Site Studio `/seller/site` + xem trước + tên miền riêng, chat, báo cáo AI `/seller/bao-cao-ai`, hồ sơ, tương tác).
- **`(admin)` — Cổng quản trị (chỉ trên host gốc):** 9 trang (`/admin`, `/admin/gian-hang`, `/admin/kiem-duyet`, `/admin/sites`, `/admin/flash-sale`, `/admin/doi-soat`, `/admin/khieu-nai`, `/admin/cau-hinh`, `/admin/audit-log`) phân quyền theo 6 vai trò nội bộ (`src/lib/admin-rbac.ts`). Voucher sàn và Xu ở Admin giữ chế độ chỉ-đọc.

## Giao diện và Style

- Dùng CSS Modules cùng `src/styles/tokens.css` (bảng màu Frost & Slate) và `src/styles/globals.css`, font biến thiên duy nhất **Archivo variable** (`--font-archivo` nạp ở `src/app/layout.tsx`).
- Trên site gian hàng, toàn bộ token được ánh xạ sang biến `--site-*` trong `src/features/site/site.module.css` (hỗ trợ cả chế độ sáng và tối `[data-site-theme-mode="dark"]` qua 8 template JSON v2).
- Không dùng Tailwind, không cài thư viện UI/icon/state ngoài (`package.json` chỉ có runtime deps `next`, `react`, `react-dom`). Giữ bề mặt phẳng, không gradient hay shadow tĩnh.
- Chữ hiển thị cho người mua viết bằng tiếng Việt tự nhiên, không để lộ thuật ngữ kỹ thuật nội bộ (JWT, HMAC, Saga, Redis Lua, sandbox, sổ cái ghi kép, optimistic locking).

## Lệnh kiểm tra

```bash
cd web
npm run dev           # chạy local Next.js dev server
npm run lint          # eslint .
npm run format:check  # prettier --check "src/**/*.{ts,tsx,css}" (config: web/.prettierrc)
npm run test:unit     # node --test "src/**/*.test.mjs" "scripts/**/*.test.mjs"
npm run check:data    # node scripts/check-data.mjs (kiểm toàn vẹn bộ dữ liệu seed/media & hàm thuần)
npm run build         # next build (kiểm tra TypeScript + SSR/route)
```

Lưu ý:
- Build với `NEXT_DIST_DIR` (ví dụ `NEXT_DIST_DIR=.next-dev npm run build`) có thể tự chèn dòng vào `web/tsconfig.json`; bắt buộc hoàn nguyên `web/tsconfig.json` trước khi commit.
- Kiểm thử E2E qua BFF trong mạng compose chạy từ gốc repo bằng `make e2e-web` (`tests/e2e-web/*.test.mjs`); nạp dữ liệu demo bằng `make seed` (`tools/seed/seed.mjs`).
- `web/next.config.ts` đặt `output: "standalone"`, `distDir: process.env.NEXT_DIST_DIR || ".next"`, `images.unoptimized: true`, `reactStrictMode: true` và cấu hình security headers/CSP.

## Hiện trạng dữ liệu và Hạ tầng

- **Nối backend thật 100% qua BFF tường minh (đã bỏ `/api/gw/*` catch-all và bỏ dữ liệu mẫu thương mại):**
  - Trình duyệt chỉ gọi các route tường minh trong `src/app/api/**` (179 route); Next server gọi Gateway qua mạng Docker nội bộ `bff` (`172.31.250.0/28`, `GATEWAY_URL=http://gateway-bff:8080`, 0 header CORS trên gateway).
  - **Cô lập tenant theo `Host`:** BFF luôn suy ra `shop_id` từ header `Host` (theo `src/lib/site/host.ts` & `src/lib/server/siteLookup.ts`, chỉ chấp nhận site `PUBLISHED` của shop `ACTIVE`) và ép `sales_channel = "STOREFRONT"` + `storefront_shop_id` trên mọi thao tác mua sắm (`src/lib/server/storefront.ts`); không bao giờ gửi `MARKETPLACE` từ web.
  - **Phiên & bảo mật:** Cookie `HttpOnly`, `SameSite=Lax`, **host-only** (`__Host-nm_at`/`nm_at`, `__Host-nm_rt`/`nm_rt` trong `src/lib/server/config.ts`, `tokens.ts`, `session.ts`), xoay refresh token chống thu hồi nhầm token family; kiểm CSRF `Origin`/same-origin (`src/lib/server/http.ts`); `src/middleware.ts` và Server Component kiểm phiên + RBAC qua backend `/api/v1/users/me`.
  - **Dữ liệu thật đầy đủ sau gộp `main` + `3b-3` + `phase-0b`:** danh bạ site & 8 template JSON v2, sản phẩm/SKU/tồn/đánh giá/tóm tắt đánh giá AI, tìm kiếm cô lập theo shop (`services/search` 3b-3; chưa mở UI lịch sử tìm kiếm trên site), flash sale, voucher `SHOP`, chương trình giảm giá shop, giỏ hàng báo SKU không khả dụng, tính phí ship 2 cấp (34 tỉnh / 3.321 phường), checkout COD / VietQR / ZaloPay (`mock-zalopay` chỉ bật ở chế độ mock) + SSE chờ thanh toán `/thanh-toan/cho/[id]`, đơn hàng/đổi địa chỉ/đổi trả 10 bước/giao lại/hoàn tiền/tra cứu đơn khách, OTP SĐT, cài đặt thông báo, quên/đặt lại mật khẩu, chat 1-1 WebSocket, trợ lý AI SSE, cùng đầy đủ màn Seller (nhập CSV, lên đơn lô, nhãn PDF A6, báo cáo, tài khoản ngân hàng, Site Studio, tên miền riêng) và Admin (6 vai trò, phê duyệt 2 người, đối soát VietQR/COD CSV, từ khóa cấm, cấu hình phương thức thanh toán & hãng vận chuyển).
  - **Dữ liệu tĩnh & `localStorage`:** `src/data/*` phục vụ bộ `tools/seed/seed.mjs` và phần biên tập phụ; `localStorage` tuyệt đối không lưu token, giỏ hàng hay đơn hàng (chỉ lưu 4 tùy chọn tiện ích người xem trong `StoreContext.tsx` khóa `nexusmart_viewer_prefs_v1` và trạng thái sidebar trong `SellerShell.tsx`).
- **Hạ tầng:** `web/Dockerfile` build đa giai đoạn (context gốc repo, Node 24 standalone, non-root `USER nextjs:10001`), service `web` trong `docker-compose.yml` (cổng host `WEB_PORT`, mặc định `3300`), job `web` trong `.github/workflows/ci.yml` (`workflow_dispatch`).

## Bố cục `src/`

- `src/app/` — `layout.tsx` (nạp `Archivo`, `tokens.css`, `globals.css`, `SessionProvider`, `StoreProvider`, `RevealObserver`), `not-found.tsx`, `robots.ts`, `sitemap.ts`, `api/` (179 BFF route handler tường minh) và 4 route group `(shop)`, `(site)/s/[host]`, `(seller)`, `(admin)`.
- `src/middleware.ts` — phân loại host, chặn đường dẫn trái host/trực tiếp `/s/*`, redirect `308` về custom `primary_host`, rewrite sang `/s/[host]`, chủ động xoay refresh token và bảo vệ tuyến yêu cầu đăng nhập.
- `src/components/` — component dùng chung (`Button`, `Icons`, `Price`, `ProductCard`, `Reveal`, `PageTransition`, `SessionProvider`, `StateView`, `SvgChart`, `modalFocus.ts`, …).
- `src/features/` — 11 module theo nghiệp vụ: `account/`, `admin/`, `assistant/`, `chat/`, `checkout/`, `commerce/`, `platform/`, `product/`, `search/`, `seller/`, `site/`.
- `src/context/` — `StoreContext.tsx` (giỏ hàng thật từ `/api/cart` + 4 tùy chọn tiện ích người xem).
- `src/lib/` — `server/` (`address.ts`, `config.ts`, `domains.ts`, `error-map.ts`, `errors.ts`, `gateway.ts`, `http.ts`, `proxy-security.ts`, `session.ts`, `siteLookup.ts`, `sites.ts`, `storefront.ts`, `tokens.ts`, `trace.ts`), `site/` (`cache.ts`, `domains.ts`, `host.ts`, `seo.ts`, `theme.ts`, `types.ts`), và các module dùng chung (`a8-types.ts`, `admin-rbac.ts`, `api.ts`, `catalog.ts`, `catalog-map.ts`, `format.ts`, `gateway-types.ts`, `payment-url.ts`, `provinces.ts`, `redirect.ts`, `salesChannel.ts`, `search.ts`).
- `src/data/` — dữ liệu seed dùng chung cho `tools/seed/seed.mjs` và phần biên tập nền tảng (`flash-sale.ts`, `media.ts`, `orders.ts`, `products.ts`, `shops.ts`, `vouchers.ts`; thư mục dữ liệu mẫu cục bộ trước đây đã được gỡ hoàn toàn).
- `src/styles/` — `tokens.css` và `globals.css`.

## Tài liệu thiết kế và QA

- Báo cáo thiết kế & ma trận tính năng: `web/DESIGN_REPORT.md`, `web/FEATURE_MATRIX.md`, `docs/WEB_INTEGRATION_REPORT.md`.
- Art direction và ảnh đối chiếu: `.taste-skill/` (`direction.json`, `storefront-sheet.md`, `seller-sheet.md`, `admin-sheet.md`).
- Khi dựng mới hoặc sửa giao diện, đối chiếu skill `design-system` và kiểm tra trực quan ở mobile `390px`, tablet `768px` và desktop `1440px`.
