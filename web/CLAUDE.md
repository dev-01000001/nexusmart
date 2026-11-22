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

Hiện không có test runner (unit hay E2E). `web/next.config.ts` đặt `images.unoptimized: true` và `reactStrictMode: true`.

## Hiện trạng dữ liệu và Hạ tầng

- 100% dữ liệu mẫu, chưa gọi API gateway: dữ liệu tĩnh ở `src/data/*`, state người mua ở `src/context/StoreContext.tsx` (lưu `localStorage` key `nexusmart_store_v2`), state người bán ở `src/features/seller/sellerStore.ts` và quản trị ở `src/features/admin/auditStore.ts` (bộ nhớ phiên qua `useSyncExternalStore`).
- Chưa có `web/Dockerfile`, chưa khai báo trong `docker-compose.yml` và chưa có job CI.
- Kế hoạch nối dữ liệu thật qua BFF cùng origin (cookie `HttpOnly`, không CORS trên gateway) nằm ở `.agents/briefs/sites-0b-web-gateway.md` (chưa thực hiện).

## Bố cục `src/`

- `src/app/` — `layout.tsx` (nạp `Work_Sans`, `tokens.css`, `globals.css`, `StoreProvider`) và 3 route group `(shop)`, `(seller)`, `(admin)`.
- `src/components/` — component dùng chung (`Button`, `Icons`, `Price`, `ProductCard`, `ShopHeader`, `ShopFooter`, `StateView`, `SvgChart`).
- `src/features/` — module theo nghiệp vụ gồm `admin/`, `catalog/`, `commerce/`, `product/`, `seller/`.
- `src/data/` — dữ liệu mẫu tĩnh (`products.ts`, `shops.ts`, `vouchers.ts`, `flash-sale.ts`, `orders.ts`, `seller.ts`, `admin.ts`, `membership.ts`, `content.ts`, `chat.ts`, `media.ts`).
- `src/lib/` — hàm thuần (`format.ts` tiền VND, `provinces.ts` 34 tỉnh 2 cấp, `search.ts` tìm kiếm bỏ dấu/đồng nghĩa, `sizing.ts` gợi ý size & phí giao).
- `src/context/` — `StoreContext.tsx` giữ trạng thái giỏ hàng, đơn hàng, yêu thích, địa chỉ, hội viên (lưu `localStorage`).
- `src/styles/` — `tokens.css` (design token nguồn sự thật) và `globals.css` (reset, typography, utility bề mặt & motion).

## Tài liệu thiết kế và QA

- Nghiệm thu UI hiện tại: `web/DESIGN_REPORT.md` và `web/FEATURE_MATRIX.md`.
- Art direction và ảnh đối chiếu: `.taste-skill/` (`direction.json` contact-sheet, `storefront-sheet.md`, `seller-sheet.md`, `admin-sheet.md`).
- Khi dựng mới hoặc sửa giao diện, đối chiếu skill `design-system` và kiểm tra trực quan ở mobile `390px` và desktop `1440px`.
