# Web Frontend (`web/`)

Next.js 15 (`^15.5.0`) App Router, React 19 (`^19.1.0`), TypeScript 5.8 (`^5.8.3`, `strict: true`, alias `@/*` → `src/*`). Ba route group `(shop)`, `(seller)`, `(admin)` dùng đường dẫn tiếng Việt (44 route chuẩn như `/danh-muc`, `/san-pham/[slug]`, `/gio-hang`, `/thanh-toan`, `/seller/don-hang`, `/admin/doi-soat`; `/tra-cuu-don` chuyển hướng 307 sang `/tra-cuu-don-hang`).

## Giao diện và Style

- Dùng CSS Modules cùng `src/styles/tokens.css` (nguồn sự thật duy nhất: bảng màu Frost and Slate, font Work Sans) và `src/styles/globals.css`.
- Không dùng Tailwind, không cài thư viện UI/icon/state ngoài. Giữ nguyên quy tắc bề mặt phẳng (không gradient, không shadow tĩnh).
- Chữ hiển thị cho người mua ở `(shop)` viết bằng tiếng Việt tự nhiên, không để lộ thuật ngữ kỹ thuật nội bộ (JWT, HMAC, Saga, Redis Lua, sandbox, sổ cái ghi kép, optimistic locking).

## Lệnh kiểm tra

```bash
cd web
npm run dev           # chạy local Next.js dev server
npm run lint          # eslint .
npm run format:check  # prettier --check "src/**/*.{ts,tsx,css}" (config: web/.prettierrc)
npm run check:data    # node scripts/check-data.mjs (kiểm toàn vẹn dữ liệu mẫu & media)
npm run build         # next build (kiểm tra TypeScript + SSG/route)
```

Lưu ý: build với `NEXT_DIST_DIR` (ví dụ `NEXT_DIST_DIR=.next-dev npm run build`) sẽ tự chèn dòng vào `web/tsconfig.json`, cần hoàn nguyên `tsconfig.json` trước khi commit. Test E2E qua BFF chạy từ gốc repo bằng `make e2e-web` (`node --test 'tests/e2e-web/*.test.mjs'`). `web/next.config.ts` đặt `output: "standalone"`, `distDir: process.env.NEXT_DIST_DIR || ".next"`, `images.unoptimized: true` và `reactStrictMode: true`.

## Hiện trạng dữ liệu và Hạ tầng

- **Nối backend qua BFF cùng origin**: trình duyệt chỉ gọi `/api/auth/*` và `/api/gw/*`; Next server gọi gateway qua `GATEWAY_URL` (không bật CORS trên gateway). Access/refresh token giữ hoàn toàn trong cookie `HttpOnly`, `SameSite=Lax`, xoay refresh token theo service `user` và kiểm `Origin` chống CSRF cho mọi thao tác ghi; `src/middleware.ts` và server component kiểm phiên + RBAC theo role backend. Không lưu token, giỏ hàng hay đơn hàng trong `localStorage`.
- **Dữ liệu thật**: danh mục, sản phẩm/SKU/tồn kho, gian hàng, flash sale, voucher, Xu, giỏ hàng, tính phí vận chuyển & xem trước giảm giá, checkout (COD & VietQR với `Idempotency-Key` sinh ở server), danh sách/chi tiết đơn hàng, sổ địa chỉ 2 cấp, thông báo, và các màn có RPC sẵn ở Seller/Admin (đơn, sản phẩm/tồn, khuyến mãi, hồ sơ gian hàng, tài chính, duyệt gian hàng/sản phẩm, người dùng, đối soát sổ cái/hoàn tiền, nhật ký hệ thống).
- **Phần giữ mock tách riêng**: các tính năng chưa có backend (số đo gợi ý size, yêu thích, đã xem, báo có hàng, đánh giá/H&Đ mẫu, chat, trợ lý AI, hội viên/thẻ quà tặng, khiếu nại/cấu hình mẫu) đọc từ `src/mock/` (`MockStoreContext.tsx` chỉ lưu tiện ích theo người xem trong `localStorage`), `src/features/seller/sellerStore.ts`, `src/features/admin/auditStore.ts` và `src/data/*`.
- **Hạ tầng**: `web/Dockerfile` (multi-stage standalone, user không phải root), service `web` trong `docker-compose.yml` (cổng host `WEB_PORT`, mặc định `3000`), job `web` trong `.github/workflows/ci.yml` (`workflow_dispatch`). Nạp dữ liệu demo bằng `make seed` (`tools/seed/seed.mjs`, idempotent, lấy nguồn từ `src/data/*`), chạy kiểm thử đầu-cuối bằng `make e2e-web`.

## Bố cục `src/`

- `src/app/` — `layout.tsx` (nạp `Work_Sans`, `tokens.css`, `globals.css`, `SessionProvider`, `MockStoreProvider`, `CartProvider`), `api/` (route handler BFF `auth/*`, `gw/[...path]`) và 3 route group `(shop)`, `(seller)`, `(admin)`.
- `src/middleware.ts` — kiểm cookie phiên cho route được bảo vệ trước khi vào trang.
- `src/components/` — component dùng chung (`Button`, `Icons`, `Price`, `ProductCard`, `ShopHeader`, `ShopFooter`, `StateView`, `SvgChart`).
- `src/features/` — module theo nghiệp vụ gồm `account/`, `admin/`, `catalog/`, `checkout/`, `commerce/`, `product/`, `seller/`.
- `src/context/` — `SessionContext.tsx` (phiên từ `/api/auth/session`) và `CartContext.tsx` (giỏ hàng thật từ `/api/gw/cart`).
- `src/mock/` — state và dữ liệu mẫu tách khỏi dữ liệu thật (`MockStoreContext.tsx`, `productEditorial.ts`, `guestOrderLookup.ts`).
- `src/data/` — dữ liệu mẫu tĩnh dùng chung cho `tools/seed/seed.mjs` và các màn biên tập/mock.
- `src/lib/` — `gateway-types.ts` (nguồn sự thật DTO gateway), `api.ts` (client BFF + tự refresh 401), `server/` (`gateway.ts`, `auth.ts`, `session.ts`, `csrf.ts`), `catalog.ts` / `catalog-mapper.ts` / `catalog-types.ts`, cùng hàm thuần (`format.ts` tiền VND & nhãn trạng thái chung, `provinces.ts`, `search.ts`, `sizing.ts`).
- `src/styles/` — `tokens.css` (design token nguồn sự thật) và `globals.css` (reset, typography, utility bề mặt & motion).

## Tài liệu thiết kế và QA

- Nghiệm thu UI hiện tại: `web/DESIGN_REPORT.md` và `web/FEATURE_MATRIX.md`.
- Art direction và ảnh đối chiếu: `.taste-skill/` (`direction.json` contact-sheet, `storefront-sheet.md`, `seller-sheet.md`, `admin-sheet.md`).
- Khi dựng mới hoặc sửa giao diện, đối chiếu skill `design-system` và kiểm tra trực quan ở mobile `390px` và desktop `1440px`.
