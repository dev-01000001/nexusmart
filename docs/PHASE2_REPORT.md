# Giai đoạn 2 + 2b – Site riêng theo Subdomain & Giữ tồn Flash Sale: Báo cáo nghiệm thu

- **Ngày nghiệm thu:** `2026-11-20`
- **Nhánh git:** `phase-0b` (commit nền `c4961e7`; **0 commit mới**, không push, không đổi nhánh)
- **Đặc tả & Kiến trúc chuẩn:** `.agents/briefs/sites-2-subdomain-sites.md`, `docs/research/sites/review-opus.md`, `AGENTS.md`
- **Xác minh độc lập:** `verifier` (chỉ đọc mã nguồn, chạy độc lập toàn bộ 6 cổng kiểm thử & xác minh trực quan/responsive trên stack thật, chỉ ghi duy nhất báo cáo này)

---

## 1. Tóm tắt các hạng mục đã thực hiện

### 1.1 Giai đoạn 2.A – Module Site trong `services/user`, Gateway REST & Auth Matrix

1. **CSDL & Migration (`services/user/migrations/0002_sites.sql`, `queries/site.sql`, `internal/db/site.sql.go`):**
   - Bảng `sites` (1 site / gian hàng `UNIQUE(shop_id)`, chỉ mục duy nhất không phân biệt hoa thường `idx_sites_subdomain_lower ON sites (LOWER(subdomain))`, trạng thái `DRAFT | PUBLISHED | SUSPENDED`, `draft_config JSONB`, `published_version_id`, `published_at`, `suspended_reason`).
   - Bảng `site_versions` (bản chụp JSON bất biến `config JSONB`, `UNIQUE(site_id, version_number)`, kèm trigger `trg_site_versions_immutable` (`BEFORE UPDATE OR DELETE`) chặn sửa/xóa sau xuất bản ở tầng Postgres, liên kết khóa ngoại `fk_sites_published_version`).
   - Bảng `domains` (ánh xạ `host` → `site_id`, `idx_domains_host_lower`, chỉ mục riêng cho `kind = 'SUBDOMAIN'`, dự phòng cột `kind = 'CUSTOM'`, `verified_at`, `tls_status` cho Giai đoạn 3).
   - Biến môi trường `SITE_ROOT_DOMAIN` (mặc định `nexusmart.localhost`, chuẩn hóa chữ thường): mỗi khi lưu/xuất bản subdomain `<sub>`, tự động upsert bản ghi `domains` với `host = <sub>.<SITE_ROOT_DOMAIN>` (`verified_at = NOW()`).

2. **Protobuf, gRPC RPC & Auth Matrix (`proto/nexusmart/user/v1/user.proto`, `gen/go/nexusmart/user/v1/**`, `services/user/internal/service/sites.go`, `pkg/common/auth/auth.go`):**
   - Công khai (không JWT): `GetSiteByHost` và `GetSiteByShop` — chỉ trả `site_versions.config` đã xuất bản và thông tin `PublicShop` khi `sites.status = 'PUBLISHED'`, `published_version_id IS NOT NULL`, `shops.status = 'ACTIVE'`; mọi trạng thái khác trả `NotFound`, không bao giờ rò rỉ `draft_config`.
   - Chủ gian hàng (`shop_id` lấy từ JWT, chặn chéo gian hàng bằng `PermissionDenied`) / Admin: `GetSiteDraft`, `SaveSiteDraft`, `PublishSite` (từ chối site `SUSPENDED` hoặc shop chưa `ACTIVE`), `RollbackSite`, `ListSiteVersions`.
   - Chỉ Admin (`admin` / `super_admin`): `ListSites`, `SuspendSite` (tạm khóa / mở khóa khôi phục về `PUBLISHED` hoặc `DRAFT`).
   - Toàn bộ 7 RPC không công khai của module site được đăng ký trong `internalRPCs` (`pkg/common/auth/auth.go`) với `allowService=false` (bắt buộc end-user JWT). Mọi thao tác ghi đều ghi nhật ký kiểm toán (`site.save_draft`, `site.publish`, `site.rollback`, `admin.site.suspend`, `admin.site.unsuspend`).

3. **Kiểm tra dữ liệu chặt phía Go (`services/user/internal/site/validate.go`):**
   - **Subdomain:** regex `^[a-z0-9](?:[a-z0-9-]{1,38}[a-z0-9])$` (3–40 ký tự, không `-` đầu/cuối), chặn `xn--`, danh sách cấm 62 từ khóa hạ tầng/nhãn hiệu (`www`, `api`, `admin`, `seller`, `app`, `mail`, `static`, `cdn`, `nexusmart`, `vietcombank`, `techcombank`, `bidv`, `agribank`, `momo`, `zalopay`, `vnpay`, `shopee`, `lazada`, `tiki`, `gov`, `chinhphu`, …) cùng kiểm tiền tố giả mạo thương hiệu (`vietcombank-*`, `nexusmart-*`, …), duy nhất không phân biệt hoa thường (`AlreadyExists` → HTTP `409`).
   - **Theme & Section JSON (`SiteConfig`):** giải mã bằng `json.NewDecoder` + `DisallowUnknownFields()` và chặn dữ liệu thừa ở mọi tầng (root, `theme`, từng `section`, `props`, `items`). Kiểm tra tương phản WCAG 2.1 ≥ `4.5:1` cho `(bg_color, text_color)`, `(surface_color, text_color)`, `(accent_color, accent_text_color)`; danh sách 5 phông chữ cho phép (`work-sans`, `be-vietnam-pro`, `inter`, `ibm-plex-sans`, `plus-jakarta-sans`); tối đa 20 section thuộc 7 schema có định kiểu (`hero`, `featured_products`, `collection`, `text`, `banner`, `voucher_strip`, `policies`); `ValidateSafeURL` chỉ nhận `https://` hoặc đường dẫn tương đối bắt đầu bằng `/` đơn, chặn `javascript:`, `data:`, `vbscript:`, `http:`, `//`, `\`, ký tự điều khiển.

4. **Gateway REST (`services/gateway/internal/router/{router.go,handlers_site.go,handlers_promotion.go,handlers_product.go}`):**
   - Công khai: `GET /api/v1/sites/by-host?host=`, `GET /api/v1/sites/by-shop/{shop_id}`.
   - Seller (ép `shop_id` từ JWT qua `RequireRoles("seller")` + `RequireSellerShop()` + `requireSellerShopID`): `GET /api/v1/seller/site`, `PUT /api/v1/seller/site/draft` (chuyển nguyên `json.RawMessage` để backend bắt field lạ), `POST /api/v1/seller/site/publish`, `POST /api/v1/seller/site/rollback`, `GET /api/v1/seller/site/versions`.
   - Admin: `GET /api/v1/admin/sites`, `GET /api/v1/admin/sites/{shop_id}`, `POST /api/v1/admin/sites/{site_id}/suspend`, `POST /api/v1/admin/flash-sales/{id}/end`.
   - `skuDTO` bổ sung `flash_sale_reserved_stock` (giữ nguyên `available_stock`), `flashSaleDTO` bổ sung `status`. Giữ **0 header CORS** tại Gateway.

---

### 1.2 Giai đoạn 2.B – Multi-tenant Web (`web/`), Checkout Handoff, SEO, Seller Editor & Admin

1. **Middleware & Cô lập đa gian hàng (`web/src/middleware.ts`, `web/src/lib/site/{host,cache,theme,seo,types}.ts`):**
   - Tuân thủ quy tắc tin proxy Giai đoạn 1 (`TRUST_PROXY_HEADERS === "true"`, mặc định `false` chỉ đọc `Host`).
   - Truy cập trực tiếp `/s` hoặc `/s/...` trả **404** trên mọi host. Host lạ / ngoài miền / nhiều cấp / nhãn cấm trả **404 ngay lập tức** không gọi backend.
   - Trên host site (`<sub>.<SITE_ROOT_DOMAIN>`): chặn `/api`, `/seller`, `/admin`, `/s` bằng **404**; tra cứu `GET /api/v1/sites/by-host` qua cache TTL có giới hạn (`2000` mục, dương `3s`, âm `1.5s`, chống nhân đôi in-flight); không có bản `PUBLISHED` của shop `ACTIVE` trả **404 TRƯỚC khi rewrite**; hợp lệ thì rewrite nội bộ sang `/s/<host>/...` (không ghi cookie phiên).

2. **Trang Site công khai & Cách ly dữ liệu (`web/src/app/(site)/s/[host]/**`, `web/src/features/site/**`, `web/src/lib/server/sites.ts`):**
   - Các tuyến ẩn danh: `/` (dựng từ 7 loại section), `/san-pham` (lọc danh mục, sắp xếp, phân trang), `/san-pham/[slug]` (tồn kho SKU trực tiếp, 404 nếu sản phẩm thuộc gian hàng khác), `/chinh-sach`, `/gioi-thieu`.
   - Không dùng `dangerouslySetInnerHTML`, không chèn HTML/CSS/JS tự do; toàn bộ chuỗi hiển thị dạng văn bản thuần, giao diện đổi màu/phông qua biến CSS có phạm vi `--site-*`.
   - Header/Footer mang thương hiệu gian hàng kèm dòng `"Vận hành trên NexusMart"`. Chỉ nạp sản phẩm và voucher phạm vi `SHOP` của đúng gian hàng đó.

3. **Luồng mua hàng về sàn & Xác minh BFF (`web/src/app/(shop)/{gio-hang,thanh-toan}/**`, `web/src/features/commerce/{checkoutPayload.ts,StorefrontBanner.tsx}`, `web/src/app/api/checkout/route.ts`):**
   - Nút Mua/Thêm giỏ trên site chuyển sang `<marketplaceOrigin>/gio-hang?shop=<id>&channel=storefront&sku=<skuId>&qty=<qty>&site=<host>`.
   - Giỏ hàng & Thanh toán ở chế độ site riêng hiển thị thanh nhận diện màu/tên thương hiệu gian hàng + nút quay lại site, chỉ lọc sản phẩm của gian hàng đó (`/api/cart?shop_id=<id>`), chỉ hiện voucher `SHOP`, ẩn voucher sàn/freeship và Xu. Cookie giữ nguyên `HttpOnly`, host-only trên host sàn.
   - BFF `POST /api/checkout` vẫn **bỏ qua hoàn toàn** `body.sales_channel` thô (bảo toàn bất biến Giai đoạn 1); chỉ gắn `sales_channel: "STOREFRONT"` + `storefront_shop_id` khi có `checkout_channel_hint: "storefront"` VÀ server tự xác minh trực tiếp (`cache: "no-store"`) qua `GET /api/v1/sites/by-shop/<shopId>` rằng gian hàng có site `PUBLISHED` khớp host (nếu không → `400 SITE_NOT_PUBLISHED`).

4. **SEO theo Host (`web/src/lib/site/seo.ts`, `web/src/app/robots.ts`, `web/src/app/sitemap.ts`):**
   - Trang PDP trên site luôn đặt `<link rel="canonical">` về URL PDP gốc trên sàn (`<marketplaceOrigin>/san-pham/<slug>`).
   - Site không có section nội dung hiển thị (`sections: []` hoặc ẩn hết) tự động gắn `<meta name="robots" content="noindex, follow">`; site có nội dung được lập chỉ mục bình thường.
   - `/robots.txt` và `/sitemap.xml` sinh động theo từng host, chỉ liệt kê trang và sản phẩm thuộc đúng gian hàng của host đó.

5. **Trình chỉnh sửa Seller (`/seller/site`, `/seller/site/xem-truoc`) & Quản trị Admin (`/admin/sites`):**
   - 7 BFF route có CSRF `apiRoute` + `assertSellerSession`/`assertAdminSession` (không tin `shop_id` từ client) + gọi `revalidateTag` (`site:<id>`, `site-host:<host>`, `site-shop:<shop_id>`) & `revalidatePath("/s/<host>", "layout")` ngay khi publish/rollback/suspend.
   - `/seller/site`: quản lý subdomain (kiểm tra tức thì), 4 bộ màu gợi ý chuẩn WCAG, 5 ô màu + chỉ số tương phản thời gian thực, 5 phông chữ, chọn ảnh an toàn, quản lý danh sách section (thêm/xóa/lên/xuống/ẩn hiện cho cả 7 loại section, tối đa 20), lịch sử phiên bản và khôi phục.
   - `/seller/site/xem-truoc`: bảo vệ server `requireRole("seller")` + kiểm tra đúng `shop_id`, `force-dynamic`, `robots: { index: false, follow: false }`, tái sử dụng nguyên `SiteShell` + `SiteRenderer`.
   - `/admin/sites`: danh sách toàn bộ site, lọc trạng thái/tìm kiếm, tạm khóa (kèm lý do) và mở khóa.

---

### 1.3 Giai đoạn 2b – Giữ tồn kho cho suất Flash Sale

- **Proto & Auth (`proto/nexusmart/{product,promotion}/v1/*.proto`, `pkg/common/auth/auth.go`):**
  - `product`: thêm `SKU.flash_sale_reserved_stock`, `StockReservationItem.flash_sale_item_id`, và 2 RPC nội bộ `ReserveFlashSaleStock` / `ReleaseFlashSaleStock` (chỉ cho Admin JWT hoặc service token `promotion-service`).
  - `promotion`: thêm `FlashSale.status` (`ACTIVE`/`ENDED`) và `EndFlashSaleWindow` (chỉ Admin end-user JWT).
- **`services/product` (`migrations/000002_flash_sale_reservations.sql`, `internal/server/server.go`, `internal/store/pg_store.go`):**
  - Thêm cột `skus.flash_sale_reserved_stock` kèm ràng buộc `CHECK (reserved_stock + flash_sale_reserved_stock <= stock)`, `stock_reservations.flash_sale_item_id`, bảng `flash_sale_stock_reservations` và chỉ mục hoạt động theo `ends_at`.
  - Khóa nhất quán `skus FOR UPDATE` trước `flash_sale_stock_reservations FOR UPDATE`. `available_stock = max(0, stock - reserved_stock - flash_sale_reserved_stock)`: đơn thường (cả `MARKETPLACE` và `STOREFRONT`) chỉ được mua từ `available_stock`, không thể ăn vào phần giữ flash sale; đơn flash sale tiêu thụ từ `flash_sale_reserved_stock` → `reserved_stock` và tăng `consumed_qty`; hủy đơn hoàn trả đúng phần còn hạn; `UpdateStock` chặn hạ tồn thấp hơn tổng giữ.
  - Bộ quét nền `StartFlashSaleSweeper` (`FLASH_SALE_SWEEP_INTERVAL=2s`) tự nhả phần giữ khi hết khung giờ.
- **`services/promotion` (`migrations/002_flash_sale_window_status.sql`, `internal/service/promotion.go`):**
  - Nối gRPC sang `ProductService` bằng service token `promotion-service`.
  - `ApproveFlashSaleItem`: gọi `ProductService.ReserveFlashSaleStock` **trước** khi duyệt và khởi tạo suất Redis (từ chối `FAILED_PRECONDITION` nếu `available_stock < N`, idempotent khi gọi lặp lại, bù trừ `ReleaseFlashSaleStock` nếu ghi DB lỗi). Từ chối/gỡ duyệt hoặc `EndFlashSaleWindow` hoặc sweeper hết hạn đều xóa khóa Redis và nhả tồn chưa dùng tại `product` một cách idempotent.
- **`services/order` (`internal/service/checkout.go`):**
  - Truyền `flash_sale_item_id` xuống `ProductService.ReserveStock`; kiểm tra tồn sơ bộ tách biệt giữa đơn thường (`available_stock`) và đơn flash (`available_stock + flash_sale_reserved_stock`); gọi `PromotionService.ReservePromotion` (Lua Redis) trước `ProductService.ReserveStock` cho đơn flash sale (kèm bù trừ `ReleasePromotion` nếu lỗi) để loại sớm các yêu cầu vượt suất mà không tranh chấp DB `product`.
- **Hạ tầng & Seed (`docker-compose.yml`, `.env.example`, `tools/seed/seed.mjs`):**
  - Truyền `SITE_ROOT_DOMAIN`, `PRODUCT_GRPC_ADDR`, `FLASH_SALE_SWEEP_INTERVAL` cho các service liên quan.
  - `tools/seed/seed.mjs` tính trước nhu cầu tồn flash sale cho 71 SKU, tự bổ sung tồn đầu vào khi thiếu (giữ nguyên 6 SKU chủ đích hết hàng ở `0`), và nạp idempotent 2 site công khai với giao diện sáng/tối đối lập rõ rệt: `bach-dang` (`Xưởng Dệt Bạch Đằng`, `#F8F6F1`/`#182026`/`#1D4ED8`, `Work Sans`, 7 section) và `sai-gon` (`Kho Basic Sài Gòn`, `#14181C`/`#F4F6F8`/`#D97706`, `Be Vietnam Pro`, 6 section).

---

### 1.4 Hai lỗi thực tế phát hiện và đã khắc phục trong quá trình xác minh độc lập

1. **Gate 5 (`inspect.mjs` lượt 1 — 3 URL FAIL):**
   - `http://bach-dang.nexusmart.localhost:3300/`: FAIL `[headline-lines]` tại `1440px` và `390px` (tiêu đề 7 từ `"Cotton Compact 220gsm từ xưởng Hải Phòng"` xuống 3 dòng ở `66px`/`36px`).
   - `http://bach-dang.nexusmart.localhost:3300/san-pham/combo-3-quan-lot-boxer-modal` và `http://sai-gon.nexusmart.localhost:3300/san-pham/ao-khoac-gio-nhe-gap-gon-xam-tro`: FAIL `[no-title]` tại `768px` và `390px` (ảnh sản phẩm 3:4 xếp trên đẩy `<h1>` xuống `1473px`, ngoài màn hình đầu).
   - **Khắc phục bởi `@web-site` (`web/src/features/site/{site.module.css,SitePdpView.tsx}`):** tinh chỉnh `.heroGrid` (`5.2fr / 6.8fr`) + `.heroTitle` responsive (`44px / 36px / 26px`) và đưa khối `.pdpHeadBlock` (`<h1>` + giá) lên trên bộ ảnh khi màn hình `<= 1024px`. Đã build lại và kiểm chứng đạt **0 FAIL** trên toàn bộ 7/7 URL (21 lượt đo viewport).
2. **Cạn kết nối Postgres dưới tải 1.000 concurrent Phase 2b lặp lại:**
   - Khi chạy `make e2e` lần thứ 2 liên tiếp sau bài `TestE2E_FlashSaleStockReserve_Phase2b` (1.000 `CreateCheckout` đồng thời), tổng kết nối mở từ 7 service đạt `100/100` (`promotion_db=30`, `order_db=24`, `product_db=23`, `user_db=15`, `payment_db=4`, `shipping_db=2`, `notification_db=2`), chạm trần mặc định `max_connections=100` của Postgres khiến `05_webhook_security_test.go` trả `500 DATABASE_ERROR` và `psql` báo `FATAL: sorry, too many clients already`.
   - **Khắc phục bởi `@seed-infra` (`docker-compose.yml`):** nâng cấu hình container `postgres` lên `command: ["postgres", "-c", "max_connections=300"]` (tổng trần pool của 7 service ~148, dư ~2× headroom). Đã áp dụng không mất volume (`docker compose up -d`, không chạy `make down`), xác minh `SHOW max_connections = 300`, và chạy lại toàn bộ chuỗi E2E: bài 1.000 concurrent đạt **0 lỗi `Internal`** (`succeeded=100, failed=900 [FailedPrecondition:900]`), `pg_stat_activity` ổn định ở `108/300`.

---

## 2. Bằng chứng từng Gate (đầu ra thật nguyên văn)

### Gate 1 – Bất biến kho mã nguồn, Chính sách & Môi trường

```text
=== FINAL INVARIANTS CHECK ===
branch: phase-0b
head: c4961e7 feat(1): immutable sales channel on orders and payments, close trust gaps
web/.next: 1795172876 2026-11-20 18:07:56.179789882 +0700
next dirs in web/: web/.next web/.next-platform 
diff next-env/tsconfig lines: 0
diff deps lines: 0
docker compose 5005x published count: 0
gateway CORS OPTIONS count: 0
ports 3111/4777:
LISTEN 0      5                                   127.0.0.1:4777       0.0.0.0:*    users:(("python3",pid=983103,fd=3))          
LISTEN 0      5                                 100.93.43.5:4777       0.0.0.0:*    users:(("socat",pid=2938570,fd=5))           
LISTEN 0      511                               100.93.43.5:3111       0.0.0.0:*    users:(("next-server (v1",pid=1979251,fd=21))
```

Xác nhận bổ sung:
- `stat -c '%Y %y' web/.next` **trước** khi xác minh: `1795172876 2026-11-20 18:07:56.179789882 +0700`; **sau** khi kết thúc toàn bộ gate: `1795172876 2026-11-20 18:07:56.179789882 +0700` (**trùng khớp 100%**).
- `.github/workflows/ci.yml`: chỉ giữ duy nhất trigger `on: workflow_dispatch:` (không có `push`, `pull_request`, `schedule`).
- `git diff -- web/package.json web/package-lock.json '**/go.mod' 'go.mod' services/payment/Cargo.toml services/payment/Cargo.lock`: **0 dòng** (không thêm bất kỳ runtime dependency nào).
- `docker compose config`: **0** cổng `5005x` publish ra host; toàn bộ cổng hạ tầng (`5432`, `26379`, `4222`, `8222`, `1025`, `8025`, `4317`, `24318`, `8889`) và cổng HTTP nội bộ 8 service (`8081`, `28082`, `28083`, `8084`, `28085`, `8086`, `8087`, `8088`) đều ràng buộc `127.0.0.1`.
- Gateway CORS check (`OPTIONS /healthz` và `GET /api/v1/categories` kèm `Origin: https://evil.example`): **0** header `Access-Control-*`.

---

### Gate 2 – Backend Static & Unit (`make lint`, `make test`, kiểm tra cú pháp E2E)

```text
=== make lint ===
buf lint
==> gofmt & golangci-lint pkg/common
==> gofmt & golangci-lint gen/go
==> gofmt & golangci-lint services/gateway
==> gofmt & golangci-lint services/user
==> gofmt & golangci-lint services/product
==> gofmt & golangci-lint services/cart
==> gofmt & golangci-lint services/promotion
==> gofmt & golangci-lint services/order
==> gofmt & golangci-lint services/shipping
==> gofmt & golangci-lint services/notification
==> rustfmt & clippy services/payment
    Finished `dev` profile [unoptimized + debuginfo] target(s) in 0.39s
=== tests/e2e gofmt & tests/e2e-web node --check ===
tests/e2e gofmt clean
tests/e2e-web syntax clean
=== make test ===
==> go test pkg/common
ok  	github.com/nexusmart/core/pkg/common/auth	(cached)
ok  	github.com/nexusmart/core/pkg/common/env	(cached)
ok  	github.com/nexusmart/core/pkg/common/logger	(cached)
ok  	github.com/nexusmart/core/pkg/common/metrics	(cached)
ok  	github.com/nexusmart/core/pkg/common/telemetry	(cached)
==> go test gen/go
?   	github.com/nexusmart/core/gen/go/nexusmart/cart/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/notification/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/order/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/payment/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/product/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/promotion/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/shipping/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/user/v1	[no test files]
==> go test services/gateway
?   	github.com/nexusmart/core/services/gateway/cmd/gateway	[no test files]
?   	github.com/nexusmart/core/services/gateway/internal/clients	[no test files]
?   	github.com/nexusmart/core/services/gateway/internal/config	[no test files]
ok  	github.com/nexusmart/core/services/gateway/internal/httpx	(cached)
ok  	github.com/nexusmart/core/services/gateway/internal/middleware	(cached)
ok  	github.com/nexusmart/core/services/gateway/internal/proxy	(cached)
ok  	github.com/nexusmart/core/services/gateway/internal/router	(cached)
==> go test services/user
?   	github.com/nexusmart/core/services/user/cmd/user	[no test files]
?   	github.com/nexusmart/core/services/user/internal/config	[no test files]
?   	github.com/nexusmart/core/services/user/internal/db	[no test files]
?   	github.com/nexusmart/core/services/user/internal/events	[no test files]
ok  	github.com/nexusmart/core/services/user/internal/password	(cached)
ok  	github.com/nexusmart/core/services/user/internal/security	(cached)
?   	github.com/nexusmart/core/services/user/migrations	[no test files]
ok  	github.com/nexusmart/core/services/user/internal/service	(cached)
ok  	github.com/nexusmart/core/services/user/internal/site	(cached)
==> go test services/product
?   	github.com/nexusmart/core/services/product/cmd/product	[no test files]
?   	github.com/nexusmart/core/services/product/internal/db	[no test files]
?   	github.com/nexusmart/core/services/product/internal/store	[no test files]
?   	github.com/nexusmart/core/services/product/migrations	[no test files]
ok  	github.com/nexusmart/core/services/product/internal/server	(cached)
==> go test services/cart
?   	github.com/nexusmart/core/services/cart/cmd/cart	[no test files]
?   	github.com/nexusmart/core/services/cart/internal/store	[no test files]
ok  	github.com/nexusmart/core/services/cart/internal/resilience	(cached)
ok  	github.com/nexusmart/core/services/cart/internal/server	(cached)
==> go test services/promotion
?   	github.com/nexusmart/core/services/promotion/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/promotion/internal/config	[no test files]
?   	github.com/nexusmart/core/services/promotion/internal/domain	[no test files]
?   	github.com/nexusmart/core/services/promotion/internal/store	[no test files]
ok  	github.com/nexusmart/core/services/promotion/internal/calculator	(cached)
ok  	github.com/nexusmart/core/services/promotion/internal/events	(cached)
ok  	github.com/nexusmart/core/services/promotion/internal/lua	(cached)
ok  	github.com/nexusmart/core/services/promotion/internal/service	(cached)
==> go test services/order
?   	github.com/nexusmart/core/services/order/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/order/internal/config	[no test files]
?   	github.com/nexusmart/core/services/order/internal/db	[no test files]
ok  	github.com/nexusmart/core/services/order/internal/clients	(cached)
?   	github.com/nexusmart/core/services/order/internal/grpcserver	[no test files]
ok  	github.com/nexusmart/core/services/order/internal/events	(cached)
?   	github.com/nexusmart/core/services/order/internal/store	[no test files]
?   	github.com/nexusmart/core/services/order/migrations	[no test files]
ok  	github.com/nexusmart/core/services/order/internal/service	(cached)
ok  	github.com/nexusmart/core/services/order/internal/statemachine	(cached)
==> go test services/shipping
?   	github.com/nexusmart/core/services/shipping/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/shipping/internal/cache	[no test files]
?   	github.com/nexusmart/core/services/shipping/internal/config	[no test files]
?   	github.com/nexusmart/core/services/shipping/internal/events	[no test files]
?   	github.com/nexusmart/core/services/shipping/internal/poller	[no test files]
?   	github.com/nexusmart/core/services/shipping/internal/store	[no test files]
?   	github.com/nexusmart/core/services/shipping/migrations	[no test files]
ok  	github.com/nexusmart/core/services/shipping/internal/address	(cached)
ok  	github.com/nexusmart/core/services/shipping/internal/carrier	(cached)
ok  	github.com/nexusmart/core/services/shipping/internal/grpcserver	(cached)
ok  	github.com/nexusmart/core/services/shipping/internal/service	(cached)
ok  	github.com/nexusmart/core/services/shipping/internal/webhook	(cached)
==> go test services/notification
?   	github.com/nexusmart/core/services/notification/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/notification/internal/config	[no test files]
?   	github.com/nexusmart/core/services/notification/internal/db	[no test files]
ok  	github.com/nexusmart/core/services/notification/internal/consumer	(cached)
ok  	github.com/nexusmart/core/services/notification/internal/grpcserver	(cached)
?   	github.com/nexusmart/core/services/notification/internal/store	[no test files]
?   	github.com/nexusmart/core/services/notification/internal/userclient	[no test files]
?   	github.com/nexusmart/core/services/notification/migrations	[no test files]
ok  	github.com/nexusmart/core/services/notification/internal/mailer	(cached)
ok  	github.com/nexusmart/core/services/notification/internal/service	(cached)
==> cargo test services/payment
    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.36s
     Running unittests src/lib.rs (target/debug/deps/nexusmart_payment-e82bf25ec01a60de)

running 39 tests
test result: ok. 39 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.03s

     Running tests/sales_channel_db_integration.rs (target/debug/deps/sales_channel_db_integration-0cb2110ad8e260d1)

running 1 test
test test_pg_migration_immutability_and_channel_filtered_ledger_and_reconciliation ... ok

test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.01s
```

---

### Gate 3 – Web (`lint`, `format:check`, `tsc --noEmit`, 58 Unit Tests, Isolated Build `NEXT_DIST_DIR=.next-verifier`)

```text
=== web lint ===
npm notice run nexusmart-web@0.1.0 lint
npm notice run eslint .
=== web format:check ===
npm notice run nexusmart-web@0.1.0 format:check
npm notice run prettier --check "src/**/*.{ts,tsx,css}"
Checking formatting...
All matched files use Prettier code style!
=== web tsc --noEmit ===
npm notice run nexusmart-web@0.1.0 npx
npm notice run 'tsc' --noEmit

=== web unit tests (all 7 *.test.mjs via Node 22 + tools/seed/ts-resolve.mjs) ===
# Files: web/scripts/catalog-map.test.mjs,
#        web/src/features/account/accountTypes.test.mjs,
#        web/src/features/account/orderView.test.mjs,
#        web/src/features/commerce/checkoutLogic.test.mjs,
#        web/src/features/seller/site/siteEditorUtils.test.mjs,
#        web/src/lib/server/security.test.mjs,
#        web/src/lib/site/site.test.mjs
✔ mergeCategories keeps storefront slugs only, backend name wins
✔ mergeShops matches profile by name and keeps generic shops
✔ mergeProducts: real price/stock/sku ids, content kept, foreign categories dropped
✔ mergeProducts: unknown product built from attributes, invalid sizes skipped
✔ mergeVouchers keeps copy for known codes and real quota
✔ buildFlashSlots groups SKU items per product with VN clock and status
✔ mergeProducts keeps OUT_OF_STOCK products with 0 available_stock
✔ buildCatalogLookups and find helpers resolve by slug, id, backendId and skuId
✔ checkLiveVoucher validates real quota, min order, expiry, shop scope and discount cap
✔ filterStorefrontShops, mergeVouchers, and buildFlashSlots isolate storefront data from foreign E2E fixtures
✔ createTtlCache deduplicates in-flight calls, respects TTL, and never caches errors
✔ safeNextPath allows safe relative paths and rejects open redirects or auth loops
✔ mapGatewayAddress converts gateway DTO to AddressView
✔ toStorefrontProvinceCode maps numeric backend province codes
✔ maps a COD order with catalog presentation data
✔ falls back to backend names when the product is unknown to the catalog
✔ subtotal shown excludes the flash sale discount already applied to line prices
✔ VietQR pending order exposes payment due time and retry state
✔ cancel reason is shown only when the buyer cancelled
✔ return request reason strips the backend prefix; delivered allows confirm/return
✔ status helpers
✔ preserves sales_channel and storefront_shop_id with legacy MARKETPLACE default
✔ translatePromotionWarning maps English promotion warnings to clean Vietnamese without raw internals
✔ mapCheckoutError maps out-of-stock, COD limit, voucher exhausted, and empty cart safely
✔ mapCartError and mapVoucherError return clean buyer messages and status codes
✔ enrichCartGroups pairs backend cart groups with catalog presentation while preserving real prices and stock
✔ voucher formatting and eligibility reasons handle fixed, percent, exhausted, and min-order vouchers
✔ buildMarketplaceCheckoutPayload strips client sales_channel and storefront_shop_id and always sends MARKETPLACE
✔ salesChannel helpers normalize, validate filters, and provide clean Vietnamese labels
✔ all curated THEME_PRESETS pass WCAG 2.1 >= 4.5 on every required pair (1.705291ms)
✔ contrastRatio detects low-contrast combinations (0.848488ms)
✔ validateSubdomainClient accepts valid shop subdomains and blocks reserved/phishing names (0.34194ms)
✔ validateSafeUrlClient allows empty, single-slash relative, and https URLs only (0.347132ms)
✔ default config and all 7 section templates pass full draft validation (2.681816ms)
✔ validateSiteDraftClient rejects more than 20 sections (0.278872ms)
✔ assertSameOrigin accepts matching same-origin Origin header (16.60576ms)
✔ assertSameOrigin rejects foreign Origin with 403 BAD_ORIGIN (0.485833ms)
✔ assertSameOrigin rejects Origin: null (0.276163ms)
✔ assertSameOrigin rejects cross-site and same-site Sec-Fetch-Site (0.392449ms)
✔ assertSameOrigin rejects missing Origin and Referer (0.184869ms)
✔ assertSameOrigin rejects protocol mismatch between Origin and request (0.187532ms)
✔ assertSameOrigin accepts same-origin Referer only when Origin header is absent (0.414831ms)
✔ errorFromResponse and errorResponse never leak gateway internals or trace_id (2.987281ms)
✔ cookie configuration is HttpOnly, SameSite=Lax, host-only (no Domain) (0.368896ms)
✔ accessExpiresSoon detects missing, malformed, expired, and valid JWTs (0.439051ms)
✔ applyAuthCookiesToHeader replaces or removes auth cookies while preserving others (0.146518ms)
✔ toGatewayAddressBody normalizes camelCase/snake_case and never includes user_id (0.15428ms)
✔ TRUST_PROXY_HEADERS defaults to false and ignores spoofed x-forwarded-host/proto in CSRF checks (0.413638ms)
✔ assertSameOrigin honors x-forwarded-host/proto ONLY when TRUST_PROXY_HEADERS=true (0.313836ms)
✔ buildLoginRedirectUrl and proxy helpers ignore x-forwarded-* by default and honor when TRUST_PROXY_HEADERS=true (0.440744ms)
✔ normalizeHostHeader lowercases, strips port and trailing dot, handles IPv6, rejects malformed (1.177493ms)
✔ classifyHost identifies root hosts, valid single-label site hosts, and rejects reserved/unknown/multi-level (0.583215ms)
✔ blocked site paths and direct /s internal paths are strictly enforced (0.159489ms)
✔ deriveMarketplaceOrigin, deriveSiteOrigin, and buildStorefrontCartUrl preserve proto/port cleanly (0.413917ms)
✔ createSiteHostCache handles positive TTL, negative TTL, error non-caching, inflight dedupe, and eviction (0.501069ms)
✔ WCAG contrast, safe URL sanitization, and CSS theme vars enforce security and readability (1.752858ms)
✔ SEO helpers enforce noindex for empty/hidden sites and marketplace canonical on site PDP (0.596549ms)
✔ BFF checkout payload resolver keeps Phase 1 marketplace lock and verifies published sites for storefront mode (0.850362ms)
ℹ tests 58
ℹ suites 0
ℹ pass 58
ℹ fail 0
ℹ duration_ms 181.431625

=== web isolated build (NEXT_DIST_DIR=.next-verifier) ===
   ▲ Next.js 15.5.29

   Creating an optimized production build ...
 ✓ Compiled successfully in 11.2s
 ✓ Linting and checking validity of types 
 ✓ Collecting page data 
 ✓ Generating static pages (53/53)
 ✓ Collecting build traces 
 ✓ Finalizing page optimization 
├ ƒ /admin/sites                                                    4.09 kB         227 kB
├ ƒ /robots.txt                                                       278 B         103 kB
├ ƒ /s/[host]                                                         850 B         114 kB
├ ƒ /s/[host]/chinh-sach                                              850 B         114 kB
├ ƒ /s/[host]/gioi-thieu                                              850 B         114 kB
├ ƒ /s/[host]/san-pham                                                850 B         114 kB
├ ƒ /s/[host]/san-pham/[slug]                                         850 B         114 kB
├ ƒ /seller/site                                                    12.2 kB         233 kB
├ ƒ /seller/site/xem-truoc                                            435 B         217 kB
├ ƒ /sitemap.xml                                                      278 B         103 kB
+ First Load JS shared by all                                        103 kB
ƒ Middleware                                                        37.7 kB

=== diff next-env.d.ts tsconfig.json ===
0
=== web/.next mtime ===
1795172876 2026-11-20 18:07:56.179789882 +0700
```

---

### Gate 4 – Trạng thái Stack Live, `make seed` Idempotent (2 lần liên tiếp) & Kiểm tra Postgres thật

```text
=== docker compose ps ===
NAME                          IMAGE                                          COMMAND                  SERVICE          CREATED             STATUS                       PORTS
nexus_ecom-cart-1             nexus_ecom-cart                                "/usr/local/bin/cart"    cart             29 minutes ago      Up 29 minutes (healthy)      50053/tcp, 127.0.0.1:28083->8083/tcp
nexus_ecom-gateway-1          nexus_ecom-gateway                             "/usr/local/bin/gate…"   gateway          29 minutes ago      Up 29 minutes (healthy)      0.0.0.0:28080->8080/tcp
nexus_ecom-mailpit-1          axllent/mailpit:v1.20                          "/mailpit"               mailpit          About an hour ago   Up About an hour (healthy)   127.0.0.1:1025->1025/tcp, 127.0.0.1:8025->8025/tcp, 1110/tcp
nexus_ecom-nats-1             nats:2.10-alpine                               "docker-entrypoint.s…"   nats             About an hour ago   Up About an hour (healthy)   127.0.0.1:4222->4222/tcp, 127.0.0.1:8222->8222/tcp, 6222/tcp
nexus_ecom-notification-1     nexus_ecom-notification                        "/usr/local/bin/noti…"   notification     29 minutes ago      Up 29 minutes (healthy)      127.0.0.1:8088->8088/tcp, 50058/tcp
nexus_ecom-order-1            nexus_ecom-order                               "/app/order-server"      order            29 minutes ago      Up 29 minutes (healthy)      50055/tcp, 127.0.0.1:28085->8085/tcp
nexus_ecom-otel-collector-1   otel/opentelemetry-collector-contrib:0.108.0   "/otelcol-contrib --…"   otel-collector   About an hour ago   Up About an hour             127.0.0.1:4317->4317/tcp, 127.0.0.1:8889->8889/tcp, 55678-55679/tcp, 127.0.0.1:24318->4318/tcp
nexus_ecom-payment-1          nexus_ecom-payment                             "/usr/local/bin/nexu…"   payment          About an hour ago   Up About an hour (healthy)   127.0.0.1:8086->8086/tcp, 50056/tcp
nexus_ecom-postgres-1         postgres:16-alpine                             "docker-entrypoint.s…"   postgres         6 minutes ago       Up 6 minutes (healthy)       127.0.0.1:5432->5432/tcp
nexus_ecom-product-1          nexus_ecom-product                             "/usr/local/bin/prod…"   product          29 minutes ago      Up 29 minutes (healthy)      50052/tcp, 127.0.0.1:28082->8082/tcp
nexus_ecom-promotion-1        nexus_ecom-promotion                           "/usr/local/bin/prom…"   promotion        29 minutes ago      Up 29 minutes (healthy)      127.0.0.1:8084->8084/tcp, 50054/tcp
nexus_ecom-redis-1            redis:7-alpine                                 "docker-entrypoint.s…"   redis            About an hour ago   Up About an hour (healthy)   127.0.0.1:26379->6379/tcp
nexus_ecom-shipping-1         nexus_ecom-shipping                            "/usr/local/bin/ship…"   shipping         29 minutes ago      Up 29 minutes (healthy)      127.0.0.1:8087->8087/tcp, 50057/tcp
nexus_ecom-user-1             nexus_ecom-user                                "/usr/local/bin/user"    user             29 minutes ago      Up 29 minutes (healthy)      127.0.0.1:8081->8081/tcp, 50051/tcp
nexus_ecom-web-1              nexus_ecom-web                                 "docker-entrypoint.s…"   web              5 minutes ago       Up 5 minutes (healthy)       0.0.0.0:3300->3000/tcp

=== make seed (run 1) ===
==> Seeding NexusMart backend via http://localhost:28080 (Mailpit: http://localhost:8025)
==> Seed complete:
  sites: created=2, reused=0 (root_domain=nexusmart.localhost)
  products: created=0, reused=18, totalSkus=159, skusToppedUp=71
  sites.map: bach-dang (bach-dang.nexusmart.localhost, PUBLISHED), sai-gon (sai-gon.nexusmart.localhost, PUBLISHED)

=== make seed (run 2 - idempotency check) ===
==> Seeding NexusMart backend via http://localhost:28080 (Mailpit: http://localhost:8025)
==> Seed complete:
  sites: created=0, reused=2 (root_domain=nexusmart.localhost)
  products: created=0, reused=18, totalSkus=159, skusToppedUp=0

=== POSTGRES LIVE STATE & CONNECTIONS POST-E2E ===
 max_connections 
-----------------
 300
(1 row)

     datname     | state  | count 
-----------------+--------+-------
 notification_db | idle   |     1
 order_db        | idle   |    25
 payment_db      | idle   |     5
 postgres        | active |     1
 product_db      | idle   |    23
 promotion_db    | idle   |    30
 shipping_db     | idle   |     2
 user_db         | idle   |    16
                 |        |     5
(9 rows)

-- user_db: sites, domains, trg_site_versions_immutable
                  id                  |               shop_id                | subdomain |  status   | has_pub_ver 
--------------------------------------+--------------------------------------+-----------+-----------+-------------
 efd24c90-7302-4bee-998b-bf2f08a6d2e6 | 62b5b10d-0cdf-46df-ad00-16450172b819 | bach-dang | PUBLISHED | t
 389befb4-e869-4ee7-9788-361671b7e073 | b20516d7-ef2e-4805-9f6c-3f16f4962af0 | sai-gon   | PUBLISHED | t
(2 rows)

             host              |   kind    | is_primary | verified | tls_status 
-------------------------------+-----------+------------+----------+------------
 bach-dang.nexusmart.localhost | SUBDOMAIN | t          | t        | MANAGED
 sai-gon.nexusmart.localhost   | SUBDOMAIN | t          | t        | MANAGED
(2 rows)

           tgname            |    tgrelid    
-----------------------------+---------------
 trg_site_versions_immutable | site_versions
(1 row)

-- product_db: skus.flash_sale_reserved_stock & flash_sale_stock_reservations
        column_name        | data_type 
---------------------------+-----------
 flash_sale_reserved_stock | integer
(1 row)

  status  | count | sum_reserved | sum_consumed 
----------+-------+--------------+--------------
 ACTIVE   |     9 |          615 |          303
 RELEASED |     3 |           90 |           30
(2 rows)

-- promotion_db: flash_sales.status
 status | count 
--------+-------
 ACTIVE |    13
 ENDED  |    10
(2 rows)
```

---

### Gate 5 – Kiểm định trực quan & responsive `inspect.mjs` (`1440 / 768 / 390` — 7/7 URL, **0 FAIL**)

Toàn bộ 6 trang site công khai chạy trực tiếp vào `http://bach-dang.nexusmart.localhost:3300` và `http://sai-gon.nexusmart.localhost:3300` (phân giải loopback trực tiếp); riêng `/seller/site --app` chạy qua reverse proxy tạm `127.0.0.1:3220 → 127.0.0.1:3300` gắn cookie phiên thật của `seller@nexusmart.local` (đã đối chiếu ảnh chụp màn hình xác nhận hiển thị giao diện chỉnh sửa `Site riêng` của `Xưởng Dệt Bạch Đằng`, không bị redirect đăng nhập, và đã tắt proxy tạm sạch sau khi đo).

| # | URL | Chế độ | `1440px` / `768px` / `390px` | Kết quả |
|---|---|---|---|---|
| 1 | `http://bach-dang.nexusmart.localhost:3300/` | Công khai (Light theme, `Work Sans`) | H1 `2/2/2` dòng (`43.9/36/26px`), `0px` tràn ngang, 11 ảnh | **0 FAIL**, 4 WARN |
| 2 | `http://bach-dang.nexusmart.localhost:3300/san-pham` | Công khai (Danh mục riêng) | H1 `1/1/2` dòng (`60/35.3/34px`), `0px` tràn ngang, 5 ảnh | **0 FAIL**, 3 WARN |
| 3 | `http://bach-dang.nexusmart.localhost:3300/san-pham/combo-3-quan-lot-boxer-modal` | Công khai (PDP) | H1 `2/1/1` dòng (`40/36/28px`) ở màn đầu, `0px` tràn ngang | **0 FAIL**, 4 WARN |
| 4 | `http://sai-gon.nexusmart.localhost:3300/` | Công khai (Dark theme, `Be Vietnam Pro`) | H1 `2/1/2` dòng (`43.9/36/26px`), `0px` tràn ngang, 9 ảnh | **0 FAIL**, 4 WARN |
| 5 | `http://sai-gon.nexusmart.localhost:3300/san-pham` | Công khai (Danh mục riêng) | H1 `1/1/2` dòng (`60/35.3/34px`), `0px` tràn ngang, 6 ảnh | **0 FAIL**, 3 WARN |
| 6 | `http://sai-gon.nexusmart.localhost:3300/san-pham/ao-khoac-gio-nhe-gap-gon-xam-tro` | Công khai (PDP) | H1 `2/1/1` dòng (`40/36/28px`) ở màn đầu, `0px` tràn ngang | **0 FAIL**, 3 WARN |
| 7 | `http://127.0.0.1:3220/seller/site` (proxy → `:3300`) | `--app` (Phiên `seller@nexusmart.local`) | H1 `"Site riêng"` `1/1/1` dòng (`24/24/21px`), `Work Sans 100%` | **0 FAIL**, 1 WARN |

Đầu ra thật nguyên văn của cả 7 lượt chạy `inspect.mjs`:

```text
================================================================
=== INSPECT: bach-dang-home (http://bach-dang.nexusmart.localhost:3300/)  ===
================================================================
TASTESKILL · INSPECT   http://bach-dang.nexusmart.localhost:3300/

MEASURED (first viewport listed)
  hero headline   "Cotton Compact 220gsm từ xưởng Hải Phòng"  7 words · 2 line(s) · 43.9px (3vw) · Work Sans 500 · tracking -0.03em · leading 1.08
  @768px          2 line(s) · 36px
  @390px          2 line(s) · 26px
  typefaces       Work Sans 100%
  sizes in use    14 / 16 / 18 / 22 / 34 / 44 px   body 16px / 1.55
  colour          page #f7f9fa · sections #f8f6f1 #ffffff #182026 · button #1d4ed8
  page            5.5 screens · 616 words · 11 images · hero media 33% img
  headings        7 · longest 2 line(s) · median 8 words

WARN — fix, or be able to defend
  [card-rows] @1440px (+768, 390)  3 rows of identical boxed cards.
  [palette-cream] @1440px (+768, 390)  Yellow-cream background (section #f8f6f1, text #f8f6f1).
  [no-footage] @1440px  No video anywhere on the page.
  [lopsided] @1440px  A section keeps its content on one side and leaves 799px of the width empty: "Ưu đãi riêng của Xưởng Dệt Bạch Đằng".

RESULT: 0 FAIL, 4 WARN — measurements pass. Passing the meter is the floor, not the grade: score the screenshots with references/critique.md.
>>> EXIT 0 (bach-dang-home)

================================================================
=== INSPECT: bach-dang-list (http://bach-dang.nexusmart.localhost:3300/san-pham)  ===
================================================================
TASTESKILL · INSPECT   http://bach-dang.nexusmart.localhost:3300/san-pham

MEASURED (first viewport listed)
  hero headline   "Sản phẩm của Xưởng Dệt Bạch Đằng"  7 words · 1 line(s) · 60px (4.2vw) · Work Sans 400 · tracking -0.03em · leading 1.08
  @768px          1 line(s) · 35.3px
  @390px          2 line(s) · 34px
  typefaces       Work Sans 100%
  sizes in use    14 / 16 / 18 / 22 / 60 px   body 16px / 1.55
  colour          page #f7f9fa · sections #f8f6f1 #182026 · button #1d4ed8
  page            1.5 screens · 201 words · 5 images · hero media 11% img
  headings        1 · longest 1 line(s) · median - words

WARN — fix, or be able to defend
  [palette-cream] @1440px (+768, 390)  Yellow-cream background (section #f8f6f1, text #f8f6f1).
  [no-footage] @1440px  No video anywhere on the page.
  [still-page] @1440px  Little motion: 10 element(s) moved while the page loaded and scrolled.

RESULT: 0 FAIL, 3 WARN — measurements pass. Passing the meter is the floor, not the grade: score the screenshots with references/critique.md.
>>> EXIT 0 (bach-dang-list)

================================================================
=== INSPECT: bach-dang-pdp (http://bach-dang.nexusmart.localhost:3300/san-pham/combo-3-quan-lot-boxer-modal)  ===
================================================================
TASTESKILL · INSPECT   http://bach-dang.nexusmart.localhost:3300/san-pham/combo-3-quan-lot-boxer-modal

MEASURED (first viewport listed)
  hero headline   "Combo 3 Quần lót Boxer Sợi Modal"  7 words · 2 line(s) · 40px (2.8vw) · Work Sans 500 · tracking -0.025em · leading 1.12
  @768px          1 line(s) · 36px
  @390px          1 line(s) · 28px
  typefaces       Work Sans 100%
  sizes in use    14 / 16 / 18 / 22 / 28 / 34 / 40 px   body 16px / 1.65
  colour          page #f7f9fa · sections #f8f6f1 #ffffff #182026 · button #1d4ed8
  page            2.9 screens · 332 words · 9 images · hero media 39% img
  headings        3 · longest 2 line(s) · median 8 words

WARN — fix, or be able to defend
  [palette-cream] @1440px (+768, 390)  Yellow-cream background (section #f8f6f1, text #f8f6f1).
  [no-footage] @1440px  No video anywhere on the page.
  [still-page] @1440px  Little motion: 12 element(s) moved while the page loaded and scrolled.
  [measure] @768px  Longest paragraph runs about 99 characters per line.

RESULT: 0 FAIL, 4 WARN — measurements pass. Passing the meter is the floor, not the grade: score the screenshots with references/critique.md.
>>> EXIT 0 (bach-dang-pdp)

================================================================
=== INSPECT: sai-gon-home (http://sai-gon.nexusmart.localhost:3300/)  ===
================================================================
TASTESKILL · INSPECT   http://sai-gon.nexusmart.localhost:3300/

MEASURED (first viewport listed)
  hero headline   "Trang phục vận động nhẹ mát Sài Gòn"  8 words · 2 line(s) · 43.9px (3vw) · Be Vietnam Pro 500 · tracking -0.03em · leading 1.08
  @768px          1 line(s) · 36px
  @390px          2 line(s) · 26px
  typefaces       Be Vietnam Pro 100%
  sizes in use    14 / 16 / 18 / 22 / 34 / 44 px   body 16px / 1.55
  colour          page #f7f9fa · sections #14181c #1e242b · button #d97706
  page            4.8 screens · 573 words · 9 images · hero media 33% img
  headings        6 · longest 2 line(s) · median 7 words

WARN — fix, or be able to defend
  [card-rows] @1440px (+768)  2 rows of identical boxed cards.
  [no-footage] @1440px  No video anywhere on the page.
  [font-not-dealt] @1440px  The sheet dealt Work Sans and Work Sans, but the page renders Be Vietnam Pro.
  [lopsided] @1440px  A section keeps its content on one side and leaves 803px of the width empty: "Mã ưu đãi Kho Basic Sài Gòn".

RESULT: 0 FAIL, 4 WARN — measurements pass. Passing the meter is the floor, not the grade: score the screenshots with references/critique.md.
>>> EXIT 0 (sai-gon-home)

================================================================
=== INSPECT: sai-gon-list (http://sai-gon.nexusmart.localhost:3300/san-pham)  ===
================================================================
TASTESKILL · INSPECT   http://sai-gon.nexusmart.localhost:3300/san-pham

MEASURED (first viewport listed)
  hero headline   "Sản phẩm của Kho Basic Sài Gòn"  7 words · 1 line(s) · 60px (4.2vw) · Be Vietnam Pro 400 · tracking -0.03em · leading 1.08
  @768px          1 line(s) · 35.3px
  @390px          2 line(s) · 34px
  typefaces       Be Vietnam Pro 100%
  sizes in use    14 / 16 / 18 / 22 / 60 px   body 16px / 1.55
  colour          page #f7f9fa · sections #14181c #1e242b · button #333940
  page            2.2 screens · 233 words · 6 images · hero media 11% img
  headings        1 · longest 1 line(s) · median - words

WARN — fix, or be able to defend
  [no-footage] @1440px  No video anywhere on the page.
  [font-not-dealt] @1440px  The sheet dealt Work Sans and Work Sans, but the page renders Be Vietnam Pro.
  [still-page] @1440px  Little motion: 11 element(s) moved while the page loaded and scrolled.

RESULT: 0 FAIL, 3 WARN — measurements pass. Passing the meter is the floor, not the grade: score the screenshots with references/critique.md.
>>> EXIT 0 (sai-gon-list)

================================================================
=== INSPECT: sai-gon-pdp (http://sai-gon.nexusmart.localhost:3300/san-pham/ao-khoac-gio-nhe-gap-gon-xam-tro)  ===
================================================================
TASTESKILL · INSPECT   http://sai-gon.nexusmart.localhost:3300/san-pham/ao-khoac-gio-nhe-gap-gon-xam-tro

MEASURED (first viewport listed)
  hero headline   "Áo khoác Gió nhẹ gấp gọn Xám tro"  8 words · 2 line(s) · 40px (2.8vw) · Be Vietnam Pro 500 · tracking -0.025em · leading 1.12
  @768px          1 line(s) · 36px
  @390px          1 line(s) · 28px
  typefaces       Be Vietnam Pro 100%
  sizes in use    14 / 16 / 18 / 22 / 28 / 34 / 40 px   body 16px / 1.65
  colour          page #f7f9fa · sections #14181c #1e242b · button #d97706
  page            2.7 screens · 362 words · 10 images · hero media 39% img
  headings        3 · longest 2 line(s) · median 8 words

WARN — fix, or be able to defend
  [no-footage] @1440px  No video anywhere on the page.
  [font-not-dealt] @1440px  The sheet dealt Work Sans and Work Sans, but the page renders Be Vietnam Pro.
  [still-page] @1440px  Little motion: 13 element(s) moved while the page loaded and scrolled.

RESULT: 0 FAIL, 3 WARN — measurements pass. Passing the meter is the floor, not the grade: score the screenshots with references/critique.md.
>>> EXIT 0 (sai-gon-pdp)

================================================================
=== INSPECT: seller-site (http://127.0.0.1:3220/seller/site) --app ===
================================================================
TASTESKILL · INSPECT   http://127.0.0.1:3220/seller/site

MEASURED (first viewport listed)
  hero headline   "Site riêng"  2 words · 1 line(s) · 24px (1.7vw) · Work Sans 600 · tracking -0.01em · leading 1.2
  @768px          1 line(s) · 24px
  @390px          1 line(s) · 21px
  typefaces       Work Sans 100%
  sizes in use    12 / 13 / 14 / 15 / 16 / 24 px   body 14px / 1.43
  colour          page #f7f9fa · sections #f7f9fa #ffffff · button #f7f9fa
  page            1.2 screens · 390 words · 0 images · hero media none
  headings        0 · longest 0 line(s) · median - words

WARN — fix, or be able to defend
  [panel-void] @1440px  1 card(s) hold a blank band inside: 157px in "Tên miền phụ & Thương hiệu".

RESULT: 0 FAIL, 1 WARN — measurements pass. Passing the meter is the floor, not the grade: score the screenshots with references/critique.md.
>>> EXIT 0 (seller-site)

=== TOTAL INSPECT FAIL COUNT: 0 ===
```

*(Ghi chú các mức WARN đều hợp lệ theo ngữ cảnh: `[font-not-dealt]` trên `sai-gon` xuất hiện do gian hàng chủ đích chọn token phông `Be Vietnam Pro` khác phông `Work Sans` của sàn; `[palette-cream]` trên `bach-dang` do gian hàng chủ đích chọn nền sáng ấm `#F8F6F1`; `[lopsided]` trên khối voucher xuất hiện vì mỗi gian hàng seed 1 mã `SHOP` riêng nên lưới 3 cột hiển thị đúng 1 thẻ; `[no-footage]`/`[still-page]` trên danh mục & PDP là thiết kế thương mại tinh gọn không nhúng video tự động.)*

---

### Gate 6 – Chuỗi E2E liền mạch (`make e2e && make seed && make e2e-web && make e2e-web` → EXIT 0)

#### 6.1 `make e2e` (14/14 PASS trong `11.176s`)

```text
GOMODCACHE="$(go env GOMODCACHE)" docker compose --profile e2e run --rm --user "$(id -u):$(id -g)" e2e
=== RUN   TestE2E_HealthAndObservability
--- PASS: TestE2E_HealthAndObservability (0.26s)
=== RUN   TestE2E_MultiShopCheckout_COD
--- PASS: TestE2E_MultiShopCheckout_COD (2.06s)
=== RUN   TestE2E_MultiShopCheckout_VietQRMock
--- PASS: TestE2E_MultiShopCheckout_VietQRMock (0.71s)
=== RUN   TestE2E_OrderIdempotency
--- PASS: TestE2E_OrderIdempotency (0.41s)
=== RUN   TestE2E_PaymentWebhookIdempotencyAndSecurity
--- PASS: TestE2E_PaymentWebhookIdempotencyAndSecurity (0.69s)
=== RUN   TestE2E_FlashSale_1000Concurrent_100Slots
    06_flash_sale_test.go:150: FlashSale 1000 concurrent requests completed in 167.714624ms: succeeded=100 failed=900
--- PASS: TestE2E_FlashSale_1000Concurrent_100Slots (0.64s)
=== RUN   TestE2E_PaymentFailed_SagaCompensation
    07_saga_compensation_test.go:141: Saga compensation verified: stock=30 voucher_quota=5 coins=10000 all 100% restored
--- PASS: TestE2E_PaymentFailed_SagaCompensation (0.72s)
=== RUN   TestE2E_LedgerAlwaysBalanced
    08_ledger_test.go:9: Double-Entry Ledger Summary: total_debits_vnd=123900240 total_credits_vnd=123900240 is_balanced=true accounts=34
--- PASS: TestE2E_LedgerAlwaysBalanced (0.00s)
=== RUN   TestE2E_SalesChannel
--- PASS: TestE2E_SalesChannel (2.23s)
=== RUN   TestE2E_LedgerAndReconciliation_ChannelFilter
    10_ledger_channel_test.go:21: Ledger totals: ALL=(debits=125899240 credits=125899240 balanced=true) MARKETPLACE=(debits=113988240 credits=113988240 balanced=true) STOREFRONT=(debits=11911000 credits=11911000 balanced=true)
    10_ledger_channel_test.go:66: Reconciliation (2026-11-20): ALL=(items=55 matched=31) MARKETPLACE=(items=49 matched=25) STOREFRONT=(items=6 matched=6)
--- PASS: TestE2E_LedgerAndReconciliation_ChannelFilter (0.08s)
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata
--- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata (0.59s)
=== RUN   TestE2E_RateLimit_Real429AndNoXFFBypass
    12_ratelimit_test.go:69: Genuine 429 observed after 60 allowed requests: X-RateLimit-Limit="60" X-RateLimit-Remaining="0" Retry-After="60" body={"error":{"code":"RATE_LIMITED","message":"rate limit exceeded, please retry later","trace_id":"2ba556e5d5f7411040ddc77825ac5ffb"}}
--- PASS: TestE2E_RateLimit_Real429AndNoXFFBypass (0.07s)
=== RUN   TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit
    13_sites_backend_test.go:323: Validation case [unknown root field] rejected cleanly: HTTP 400
    13_sites_backend_test.go:323: Validation case [unknown theme field] rejected cleanly: HTTP 400
    13_sites_backend_test.go:323: Validation case [unknown section field] rejected cleanly: HTTP 400
    13_sites_backend_test.go:323: Validation case [unknown section props field] rejected cleanly: HTTP 400
    13_sites_backend_test.go:323: Validation case [javascript URL in theme.logo_url] rejected cleanly: HTTP 400
    13_sites_backend_test.go:323: Validation case [data URL in theme.banner_url] rejected cleanly: HTTP 400
    13_sites_backend_test.go:323: Validation case [javascript URL in hero cta_href] rejected cleanly: HTTP 400
    13_sites_backend_test.go:323: Validation case [insecure http URL in hero image_url] rejected cleanly: HTTP 400
    13_sites_backend_test.go:323: Validation case [low contrast bg/text (< 4.5)] rejected cleanly: HTTP 400
    13_sites_backend_test.go:323: Validation case [low contrast accent/accent_text (< 4.5)] rejected cleanly: HTTP 400
    13_sites_backend_test.go:323: Validation case [disallowed font family] rejected cleanly: HTTP 400
    13_sites_backend_test.go:323: Validation case [more than 20 sections] rejected cleanly: HTTP 400
    13_sites_backend_test.go:667: Site 5402c07a-bebd-4988-bf37-3e5fc76a4b05 audit trail verified: map[admin.site.suspend:1 admin.site.unsuspend:1 site.publish:2 site.rollback:1 site.save_draft:2]
--- PASS: TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit (0.22s)
    --- PASS: TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit/SubdomainValidation_RejectsInvalidReservedAndPhishing (0.01s)
    --- PASS: TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit/StrictConfigValidation_RejectsUnknownFieldsUnsafeURLsLowContrastAndTooManySections (0.01s)
    --- PASS: TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit/DraftPublishV2Rollback_And_DuplicateCaseInsensitive (0.08s)
    --- PASS: TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit/CrossTenantIsolation_SellerBCannotAccessSellerA (0.11s)
    --- PASS: TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit/AdminSuspendUnsuspend_And_AuditLogs (0.02s)
=== RUN   TestE2E_FlashSaleStockReserve_Phase2b
    14_flash_sale_stock_reserve_2b_test.go:108: Insufficient-stock flash approval rejected cleanly: HTTP 409 body={"error":{"code":"FAILED_PRECONDITION","message":"INSUFFICIENT_STOCK: sku ea94387b-d92d-4c04-aec7-06af0601f537 has 20 available, requested 50 for flash sale","trace_id":"2b1eab0dc6707f611999450271171761"}}
    14_flash_sale_stock_reserve_2b_test.go:211: Regular MARKETPLACE #6 rejected cleanly: HTTP 409
    14_flash_sale_stock_reserve_2b_test.go:226: Regular STOREFRONT #6 rejected cleanly: HTTP 409
    14_flash_sale_stock_reserve_2b_test.go:314: Phase 2b 1000 concurrent flash checkouts completed in 1.604415791s: succeeded=100 failed=900 error_codes=map[FailedPrecondition:900]
    14_flash_sale_stock_reserve_2b_test.go:502: Phase 2b Ledger Summary: debits=142732240 credits=142732240 is_balanced=true accounts=34
--- PASS: TestE2E_FlashSaleStockReserve_Phase2b (2.47s)
    --- PASS: TestE2E_FlashSaleStockReserve_Phase2b/A_ApproveRejected_WhenAvailableStockInsufficient (0.01s)
    --- PASS: TestE2E_FlashSaleStockReserve_Phase2b/BC_RegularIsolationBothChannels_And_1000ConcurrentFlashCheckouts (1.69s)
    --- PASS: TestE2E_FlashSaleStockReserve_Phase2b/D_WindowEnd_ReleasesUnconsumedStock_Idempotent_And_RegularBuysReleased (0.41s)
    --- PASS: TestE2E_FlashSaleStockReserve_Phase2b/E_DoubleEntryLedger_Balanced (0.00s)
PASS
ok  	github.com/nexusmart/core/tests/e2e	11.176s
```

#### 6.2 `make seed` (trong chuỗi liền mạch — Idempotent PASS)

```text
node --no-warnings=MODULE_TYPELESS_PACKAGE_JSON tools/seed/seed.mjs
==> Seeding NexusMart backend via http://localhost:28080 (Mailpit: http://localhost:8025)
==> Seed complete:
  sites: created=0, reused=2 (root_domain=nexusmart.localhost)
```

#### 6.3 `make e2e-web` lần 1 (19/19 PASS trong `5516.57ms`) & lần 2 (19/19 PASS trong `5534.52ms`)

```text
--- make e2e-web (run 1) ---
▶ 01. Buyer Auth Lifecycle, Unauthenticated Page Guards & Seeded Storefront SSR
  ✔ (a) registers a fresh buyer, verifies Mailpit OTP, logs in with HttpOnly host-only cookies and no token leak, refreshes, and logs out (156.763577ms)
  ✔ (j) redirects unauthenticated visitors away from protected pages to /dang-nhap (7.272525ms)
  ✔ (j) renders seeded storefront pages with HTTP 200 and real Vietnamese catalog data (207.25702ms)
✔ 01. Buyer Auth Lifecycle, Unauthenticated Page Guards & Seeded Storefront SSR (387.655578ms)
▶ 02. Buyer Commerce & Security Suite (Cart, Voucher, COD, VietQR, Idempotency, IDOR, 401, CSRF, RBAC)
  ✔ (b)(c)(d) adds items from 2 seeded shops, applies a real voucher, checks out COD, and verifies orders (142.472316ms)
  ✔ (e) completes VietQR mock payment flow: checkout -> pending payment -> external HMAC bank webhook -> poll PAID & CONFIRMED (513.422174ms)
  ✔ (f) checkout idempotency: resending same Idempotency-Key returns same single order without duplicates (54.203139ms)
  ✔ (g) object-level authorization: Buyer A and Buyer B cannot read or mutate each other's orders or payments (145.76429ms)
  ✔ (h) unauthenticated writes and private reads return 401 without leaking internal details (37.313861ms)
  ✔ (i) CSRF protection rejects foreign/missing Origin and cross-site Fetch-Metadata with 403 (28.150155ms)
  ✔ (j) enforces server-side RBAC for Buyer, Seller, and Admin pages & BFF APIs (302.84908ms)
✔ 02. Buyer Commerce & Security Suite (Cart, Voucher, COD, VietQR, Idempotency, IDOR, 401, CSRF, RBAC) (1375.54525ms)
▶ 03. Phase 1 Sales Channel BFF Contract, Channel Filters & Proxy Header Hardening
  ✔ (a) BFF POST /api/checkout ignores client sales_channel=STOREFRONT and forces MARKETPLACE; GET /api/cart?shop_id filters without mutating cart (106.182834ms)
  ✔ (b) Seller orders and Admin ledger/reconciliation channel filter endpoints & SSR pages work via BFF (263.563018ms)
  ✔ (c) B5 proxy header hardening: spoofed X-Forwarded-Host / X-Forwarded-Proto cannot bypass CSRF or hijack redirects (5.067297ms)
✔ 03. Phase 1 Sales Channel BFF Contract, Channel Filters & Proxy Header Hardening (520.342597ms)
▶ 04. Phase 2 Multi-Tenant Subdomain Sites E2E (Isolation, Lifecycle, Security, Checkout Handoff & SEO)
  ✔ 1. Unknown, foreign, reserved, and multi-level hosts return HTTP 404 (23.566101ms)
  ✔ 2. Site A & Site B isolate products/vouchers, enforce 404 on cross-shop PDP, and render distinct themes (151.946563ms)
  ✔ 3 & 4. Direct /s/<host> returns 404 on every host; /admin, /seller, and /api/* return 404 on site host (14.765527ms)
  ✔ 5, 6, 7 & 9b. Ephemeral shop draft lifecycle, preview RBAC, security validation, XSS escaping, admin suspend/unsuspend, and empty-sections noindex (2528.033759ms)
  ✔ 8. Buy from site -> storefront cart/checkout handoff creates STOREFRONT order; unpublished shop hint rejected 400 (216.922414ms)
  ✔ 9a & 9c. SEO rules: marketplace canonical on site PDP, indexable seeded site (no noindex), and host-specific robots.txt & sitemap.xml (42.029696ms)
✔ 04. Phase 2 Multi-Tenant Subdomain Sites E2E (Isolation, Lifecycle, Security, Checkout Handoff & SEO) (3013.751949ms)
ℹ tests 19
ℹ suites 4
ℹ pass 19
ℹ fail 0
ℹ duration_ms 5516.573432

--- make e2e-web (run 2) ---
▶ 01. Buyer Auth Lifecycle, Unauthenticated Page Guards & Seeded Storefront SSR
  ✔ (a) registers a fresh buyer, verifies Mailpit OTP, logs in with HttpOnly host-only cookies and no token leak, refreshes, and logs out (126.190479ms)
  ✔ (j) redirects unauthenticated visitors away from protected pages to /dang-nhap (7.127763ms)
  ✔ (j) renders seeded storefront pages with HTTP 200 and real Vietnamese catalog data (144.576378ms)
✔ 01. Buyer Auth Lifecycle, Unauthenticated Page Guards & Seeded Storefront SSR (296.767791ms)
▶ 02. Buyer Commerce & Security Suite (Cart, Voucher, COD, VietQR, Idempotency, IDOR, 401, CSRF, RBAC)
  ✔ (b)(c)(d) adds items from 2 seeded shops, applies a real voucher, checks out COD, and verifies orders (120.874654ms)
  ✔ (e) completes VietQR mock payment flow: checkout -> pending payment -> external HMAC bank webhook -> poll PAID & CONFIRMED (897.293995ms)
  ✔ (f) checkout idempotency: resending same Idempotency-Key returns same single order without duplicates (53.179122ms)
  ✔ (g) object-level authorization: Buyer A and Buyer B cannot read or mutate each other's orders or payments (534.174477ms)
  ✔ (h) unauthenticated writes and private reads return 401 without leaking internal details (37.124151ms)
  ✔ (i) CSRF protection rejects foreign/missing Origin and cross-site Fetch-Metadata with 403 (25.511157ms)
  ✔ (j) enforces server-side RBAC for Buyer, Seller, and Admin pages & BFF APIs (247.345146ms)
✔ 02. Buyer Commerce & Security Suite (Cart, Voucher, COD, VietQR, Idempotency, IDOR, 401, CSRF, RBAC) (2056.877948ms)
▶ 03. Phase 1 Sales Channel BFF Contract, Channel Filters & Proxy Header Hardening
  ✔ (a) BFF POST /api/checkout ignores client sales_channel=STOREFRONT and forces MARKETPLACE; GET /api/cart?shop_id filters without mutating cart (104.034349ms)
  ✔ (b) Seller orders and Admin ledger/reconciliation channel filter endpoints & SSR pages work via BFF (231.475913ms)
  ✔ (c) B5 proxy header hardening: spoofed X-Forwarded-Host / X-Forwarded-Proto cannot bypass CSRF or hijack redirects (5.326969ms)
✔ 03. Phase 1 Sales Channel BFF Contract, Channel Filters & Proxy Header Hardening (481.066493ms)
▶ 04. Phase 2 Multi-Tenant Subdomain Sites E2E (Isolation, Lifecycle, Security, Checkout Handoff & SEO)
  ✔ 1. Unknown, foreign, reserved, and multi-level hosts return HTTP 404 (16.645292ms)
  ✔ 2. Site A & Site B isolate products/vouchers, enforce 404 on cross-shop PDP, and render distinct themes (105.722655ms)
  ✔ 3 & 4. Direct /s/<host> returns 404 on every host; /admin, /seller, and /api/* return 404 on site host (11.338806ms)
  ✔ 5, 6, 7 & 9b. Ephemeral shop draft lifecycle, preview RBAC, security validation, XSS escaping, admin suspend/unsuspend, and empty-sections noindex (2083.664264ms)
  ✔ 8. Buy from site -> storefront cart/checkout handoff creates STOREFRONT order; unpublished shop hint rejected 400 (195.393924ms)
  ✔ 9a & 9c. SEO rules: marketplace canonical on site PDP, indexable seeded site (no noindex), and host-specific robots.txt & sitemap.xml (34.926859ms)
✔ 04. Phase 2 Multi-Tenant Subdomain Sites E2E (Isolation, Lifecycle, Security, Checkout Handoff & SEO) (2486.603461ms)
ℹ tests 19
ℹ suites 4
ℹ pass 19
ℹ fail 0
ℹ duration_ms 5534.519629
```

---

## 3. Hướng dẫn mở và kiểm thử 2 site mẫu trên máy dev

### 3.1 Biến môi trường & Khởi động cụm dịch vụ

- Biến môi trường cốt lõi (đã cấu hình mặc định trong `docker-compose.yml` và `.env.example`):
  - `SITE_ROOT_DOMAIN=nexusmart.localhost` (dùng chung cho `user`, `gateway`, `web`, `e2e`, `e2e-web`).
  - `FLASH_SALE_SWEEP_INTERVAL=2s` (cho `product`, `promotion`).
  - `TRUST_PROXY_HEADERS=false` (mặc định; web chỉ tin header `Host`).
- Lệnh dựng/chạy trên máy dev (tránh trùng cổng nội bộ, bảo toàn volume, **không** dùng `make down`):

```bash
REDIS_PORT=26379 OTEL_HTTP_PORT=24318 GATEWAY_PORT=28080 \
PRODUCT_HTTP_PORT=28082 CART_HTTP_PORT=28083 ORDER_HTTP_PORT=28085 \
WEB_PORT=3300 docker compose up -d --build

make seed
```

### 3.2 Địa chỉ truy cập trực tiếp 2 site mẫu & các màn quản trị

Trên Linux và trình duyệt hiện đại (Chrome/Chromium/Firefox), `*.localhost` tự phân giải về loopback (`127.0.0.1` / `::1`) theo RFC 6761 mà **không cần sửa `/etc/hosts`**. (Nếu dùng công cụ cũ không hỗ trợ `*.localhost`, có thể thêm `127.0.0.1 nexusmart.localhost bach-dang.nexusmart.localhost sai-gon.nexusmart.localhost` vào `/etc/hosts` hoặc truyền `curl -H "Host: bach-dang.nexusmart.localhost:3300" http://127.0.0.1:3300/`.)

| Đích | URL trên máy dev | Mô tả |
|---|---|---|
| **Site mẫu 1 (Light / Work Sans)** | `http://bach-dang.nexusmart.localhost:3300` | `Xưởng Dệt Bạch Đằng` — nền `#F8F6F1`, nhấn `#1D4ED8`, 7 section (`/`, `/san-pham`, `/san-pham/combo-3-quan-lot-boxer-modal`, `/chinh-sach`, `/gioi-thieu`, `/robots.txt`, `/sitemap.xml`) |
| **Site mẫu 2 (Dark / Be Vietnam Pro)** | `http://sai-gon.nexusmart.localhost:3300` | `Kho Basic Sài Gòn` — nền `#14181C`, nhấn `#D97706`, 6 section (`/`, `/san-pham`, `/san-pham/ao-khoac-gio-nhe-gap-gon-xam-tro`, `/chinh-sach`, `/gioi-thieu`, `/robots.txt`, `/sitemap.xml`) |
| **Sàn chính (Marketplace)** | `http://localhost:3300` hoặc `http://nexusmart.localhost:3300` | Cửa hàng tổng hợp NexusMart, giỏ hàng & checkout (kể cả chế độ `?shop=<id>&channel=storefront`) |
| **Trình chỉnh sửa Seller** | `http://localhost:3300/seller/site` | Chỉnh subdomain, màu/phông/logo/banner, 7 loại section, xuất bản, lịch sử & khôi phục |
| **Xem trước bản nháp (Seller)** | `http://localhost:3300/seller/site/xem-truoc` | Chỉ chủ shop đang đăng nhập xem được (`noindex`, `force-dynamic`) |
| **Quản trị Site (Admin)** | `http://localhost:3300/admin/sites` | Danh sách toàn bộ site, lọc trạng thái/tìm kiếm, tạm khóa & mở khóa |

### 3.3 Tài khoản dev sau `make seed`

| Vai trò | Email | Mật khẩu | Gian hàng / Site |
|---|---|---|---|
| Seller Site 1 | `seller@nexusmart.local` | `SellerDemo123!` | `Xưởng Dệt Bạch Đằng` (`bach-dang.nexusmart.localhost`, `PUBLISHED`) |
| Seller Site 2 | `seller-saigon@nexusmart.local` | `SellerDemo123!` | `Kho Basic Sài Gòn` (`sai-gon.nexusmart.localhost`, `PUBLISHED`) |
| Seller chưa tạo site | `seller-danang@nexusmart.local`, `seller-hanoi@nexusmart.local` | `SellerDemo123!` | `Sợi Tự Nhiên Đà Nẵng`, `Dệt Kim Hà Nội` (để thử tạo mới/xuất bản) |
| Người mua | `buyer@nexusmart.local` | `BuyerDemo123!` | Có sẵn `42.000 Xu` & địa chỉ mặc định |
| Quản trị viên | `admin@nexusmart.local` | `AdminDevPassword123!` | Toàn quyền `/admin/sites` |

---

## 4. Phần chưa làm / Ngoài phạm vi & Lý do

1. **Custom domain, Caddy On-Demand TLS (`ask` + DNS poller nền), ARI và Wildcard DNS-01:** thuộc **Giai đoạn 3 (sau đồ án)** theo `docs/research/sites/review-opus.md` §5.2–§5.3 (Quyết định 5); bảng `domains` đã chuẩn bị sẵn cột `kind = 'CUSTOM'`, `verified_at`, `tls_status`.
2. **SSO đa tên miền (PKCE), JWT bất đối xứng (EdDSA/RS256 + JWKS) & giỏ hàng khách vãng lai trên subdomain:** thuộc **Giai đoạn 3** theo `review-opus.md` §5.3 (Quyết định 2). MVP chuyển luồng Mua/Giỏ về host sàn (`?shop=&channel=storefront`) và giữ cookie `HttpOnly` host-only.
3. **Trình kéo-thả (drag-and-drop) & chèn HTML/CSS/JS tự do:** đặc tả Giai đoạn 2 chủ đích loại bỏ (chỉ dùng form thêm/xóa/lên/xuống và theme thuần dữ liệu JSON để loại bỏ rủi ro XSS).
4. **Redis `cacheHandler` chia sẻ cache Next.js giữa nhiều pod:** không cần cho triển khai 1 container `web` trong đồ án; sẽ bổ sung khi mở rộng đa pod (`review-opus.md` §1 #10).
5. **Kịch bản `k6` 200 VU tách rời:** kiểm thử chịu tải đồng thời 2b được tích hợp trực tiếp trong Go E2E (`14_flash_sale_stock_reserve_2b_test.go`: 1.000 goroutine `OrderService.CreateCheckout` đồng thời + kiểm tra đơn thường 2 kênh song song + test `-race` trên `product`/`promotion`/`order`), tránh thêm công cụ runtime ngoài.
6. **Biểu phí hoa hồng riêng theo kênh, đối soát COD theo kênh từ `shipping`, hóa đơn điện tử VAT & trường thuế NĐ 117:** giữ nguyên tỷ lệ hoa hồng theo danh mục như Giai đoạn 1 và ngoài phạm vi Giai đoạn 2 (`review-opus.md` §5.3 Quyết định 3 & §5.4).
7. **Dữ liệu Flash Sale seed lịch sử trước Giai đoạn 2b trên volume Postgres hiện có:** 3 khung flash sale seed cũ (`slot-12`, `slot-18`, `slot-21`) đã được duyệt từ trước khi triển khai 2b nên `make seed` tái sử dụng nguyên trạng (không tự ghi đè lại `flash_sale_stock_reservations` cho bản ghi cũ). Mọi suất flash sale duyệt mới (bao gồm khi seed trên DB sạch và toàn bộ kịch bản trong `14_flash_sale_stock_reserve_2b_test.go`) đều giữ và nhả tồn tự động qua `flash_sale_stock_reservations`.

---

## 5. Đối chiếu kiến trúc & Các điểm lệch so với `docs/research/sites/review-opus.md`

### 5.1 Các nguyên tắc lõi tuân thủ 100%

- **Một app `web/` duy nhất:** `middleware.ts` chuẩn hóa `Host` (theo luật tin proxy), trả `404` cho host lạ/chưa xuất bản *trước khi* rewrite sang `app/(site)/s/[host]/...`, chặn `/s/*`, `/api/*`, `/seller/*`, `/admin/*` trên host site.
- **Module site trong `services/user` (`user_db`):** không thêm microservice thứ 12; 3 bảng `sites`, `site_versions` (bất biến có trigger DB), `domains`; gRPC `GetSiteByHost`, `SaveSiteDraft`, `PublishSite`, `RollbackSite` (+ bổ trợ `GetSiteByShop`, `GetSiteDraft`, `ListSiteVersions`, `ListSites`, `SuspendSite`).
- **Theme là dữ liệu:** token JSON + section schema, backend Go validate chặt bằng `DisallowUnknownFields()`, kiểm tra tương phản WCAG ≥ 4.5, chặn `javascript:`/`data:` URL, không có HTML tự do.
- **Checkout về host sàn & Cô lập cookie:** chuyển về `/gio-hang?shop=<id>&channel=storefront`, giỏ một khóa Redis lọc theo `shop_id`, chỉ nhận voucher `SHOP` của đúng gian hàng (tái sử dụng `scope`), cookie `HttpOnly` host-only (không đặt `Domain=.nexusmart.*`), BFF tự xác minh site `PUBLISHED` trước khi gửi `sales_channel=STOREFRONT`.
- **SEO & Chống giả mạo:** PDP site trỏ `canonical` về PDP sàn; site rỗng section đặt `noindex`; `robots.txt` và `sitemap.xml` riêng theo host; danh sách chặn subdomain giả mạo ngân hàng/thương hiệu (`vietcombank*`, `nexusmart*`, …).
- **Tồn kho dùng chung & Giữ tồn Flash Sale (2b):** giữ tồn trong `product` (`skus.flash_sale_reserved_stock` + `flash_sale_stock_reservations`), đơn thường hai kênh không ăn vào suất flash, 0 bán quá tồn dưới 1.000 yêu cầu đồng thời, tự nhả tồn khi hết khung.

### 5.2 Các điểm lệch / điều chỉnh có chủ đích so với `docs/research/sites/review-opus.md`

| # | Mục trong `review-opus.md` | Triển khai thực tế (GĐ 2 + 2b) | Lý do |
|---|---|---|---|
| 1 | §5.1: Trang site render ISR thuần gắn tag `site:<id>` | Các tuyến `app/(site)/s/[host]/**` render động theo request (`headers()` để tự dựng `marketplaceOrigin`/`siteOrigin` giữ đúng cổng dev `:3300`) kết hợp **Next Data Cache theo tag** (`revalidate: 30`, tag `site:<id>`, `site-host:<host>`, `site-shop:<shop_id>`) + `revalidateTag`/`revalidatePath` tức thì khi publish/rollback/suspend; middleware có cache ngắn (`3s` dương / `1.5s` âm) | Đảm bảo hoạt động mượt cả trên cổng dev (`:3300`) mà không cần hardcode `APP_ORIGIN`, đồng thời vẫn đáp ứng vượt mức SLA cập nhật của §5.2 (publish & suspend phản ánh trong `≤ 3–4s`, tốt hơn ngưỡng `≤ 10s` / `≤ 60s`). |
| 2 | §5.2: Tóm tắt "6 section" | Triển khai đủ **7 loại section** (`hero`, `featured_products`, `collection`, `text`, `banner`, `voucher_strip`, `policies`) | Tuân theo đúng yêu cầu chi tiết của đặc tả `.agents/briefs/sites-2-subdomain-sites.md` (§2.A). |
| 3 | §5.4: "Draft Mode `no-store`" (ngụ ý Next.js `draftMode()` cookie trên host site) | Đặt tuyến xem trước bản nháp tại `/seller/site/xem-truoc` trên host sàn (`requireRole("seller")`, `force-dynamic`, `noindex`, tái sử dụng `SiteShell` + `SiteRenderer`) | Giữ host subdomain hoàn toàn ẩn danh và không chia sẻ cookie phiên (`host-only`), loại trừ hoàn toàn rủi ro rò rỉ bản nháp hoặc cookie sang subdomain. |
| 4 | §4: "Danh sách thương hiệu cấm và duyệt slug" | Áp dụng kiểm duyệt tự động bằng regex + 62 từ cấm + chặn tiền tố thương hiệu/ngân hàng & punycode (`xn--`), kết hợp hậu kiểm và `SuspendSite` ngay tức thì của Admin tại `/admin/sites` | Phù hợp phạm vi MVP (không tạo hàng đợi duyệt thủ công chặn seller thiết lập site), vẫn chặn ngay tên giả mạo và cho phép Admin khóa tức thì kèm audit log. |
| 5 | §4: Giữ tồn flash sale "ngay khi chiến dịch bắt đầu" | Giữ tồn trong `product` **ngay từ lúc duyệt (`ApproveFlashSaleItem`)** cho tới khi khung kết thúc/bị hủy (hoặc sweeper `2s` dọn khi quá `ends_at`) | Tuân theo đúng đặc tả 2b (`.agents/briefs/sites-2-subdomain-sites.md` §2b) và bảo vệ tồn mạnh hơn (không để đơn thường mua cạn tồn giữa lúc duyệt và lúc mở khung). |
| 6 | §5.2: Kiểm thử 2b bằng "k6: 200 VU" | Kiểm thử bằng `TestE2E_FlashSaleStockReserve_Phase2b` trong `make e2e` (1.000 `CreateCheckout` đồng thời + kiểm đơn thường 2 kênh + kiểm sổ cái cân) và test `-race` | Không thêm công cụ runtime ngoài; kiểm chứng trực tiếp qua gRPC/DB/Redis thật ngay trong cổng `make e2e` hằng ngày. |
| 7 | §4: Viết lại chính sách `UC-B14` ("link ngoài sàn" trong chat) | Chưa sửa mã Chat | Hệ thống chưa có microservice Chat (màn `/seller/chat` vẫn là dữ liệu mẫu in-memory); sẽ cập nhật quy tắc lọc link khi xây dịch vụ Chat. |
