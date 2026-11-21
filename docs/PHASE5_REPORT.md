# Giai đoạn 5 – Cloudflare: Subdomain cho từng shop và tên miền riêng — Báo cáo nghiệm thu

- **Ngày hoàn tất gate cuối:** `2026-11-21`
- **Hợp đồng tham chiếu:** `.agents/briefs/sites-5-contract.md`, `.agents/briefs/sites-5c-qa.md`
- **Trạng thái gate cuối:** **PASS (EXIT 0)** — `make lint`, `make test`, `make e2e && make seed && make e2e-web && make e2e-web`, CI chỉ `workflow_dispatch`, chỉ cổng `28080` và `3300` public.

---

## 5A – Backend Cloudflare (proto, user, gateway, notification)

### Đã hoàn thành
- **Proto & codegen (`proto/nexusmart/user/v1/user.proto`, `gen/go/nexusmart/user/v1/`)**:
  - Thêm message `DomainVerificationRecord`, `Domain` (`id`, `host`, `kind`, `status`, `is_primary`, `verification`, `last_error`, `verified_at`, `created_at`) và 4 RPC trong `UserService`: `AddCustomDomain`, `CheckCustomDomain`, `RemoveCustomDomain`, `ListSiteDomains`.
  - Đã chạy `make proto` (`buf lint`, `buf generate`, `cd gen/go && go mod tidy`).
- **Dữ liệu & SQL (`services/user/migrations/0003_domains_cloudflare.sql`, `services/user/queries/site.sql`, `services/user/sqlc.yaml`, `services/user/internal/db/`)**:
  - Migration `0003_domains_cloudflare.sql` mở rộng bảng `domains` không phá dữ liệu cũ: `status` (`DEFAULT 'ACTIVE'`, `CHECK` trong `PENDING|ACTIVE|FAILED|REMOVING`), `cf_record_id`, `cf_custom_hostname_id`, `verification JSONB DEFAULT '[]'`, `last_checked_at`, `last_error`; thêm unique index `idx_domains_site_custom` (tối đa 1 `CUSTOM`/site), `idx_domains_site_primary`, và `idx_domains_poll_status`.
  - Cập nhật `GetPublishedSiteByHost` và toàn bộ join `primary_host` chỉ nhận bản ghi `status = 'ACTIVE'`; cập nhật `UpsertSiteSubdomainDomain` không ghi đè `is_primary` khi site đã có `CUSTOM` primary; sinh lại `sqlc` (`*string` cho các cột `TEXT NULL`).
- **Kiểm tra hostname (`services/user/internal/site/validate.go`, `validate_test.go`)**:
  - Thêm `ValidateCustomHostname(raw, rootDomain string, allowMockTest bool)` dùng `golang.org/x/net/idna` đã có sẵn trong `services/user/go.mod` (`v0.26.0`, không thêm dependency runtime mới): chuyển chữ thường & IDN → punycode, kiểm tra 2–253 ký tự, nhãn 1–63, cấm IP/cổng/wildcard/dấu chấm thừa/TLD thuần số/root domain và subdomain dưới `SITE_ROOT_DOMAIN`, cấm `localhost`/`*.localhost`/`*.local`/`*.internal`, và chỉ cho phép `*.test` khi `CF_MODE=mock`.
- **Adapter Cloudflare (`services/user/internal/cloudflare/`)**:
  - Interface nhỏ `Client` với 2 implement: `MockClient` (stateless, không gọi mạng; `.verified.test` → `ACTIVE` ngay lần kiểm tra đầu, `.pending.test` giữ `PENDING`, `.fail.test` → `FAILED`; trả bản ghi `CNAME` + `TXT`) và `LiveClient` (chỉ dùng `net/http` chuẩn, timeout 10s, retry backoff cho 429/5xx có tôn trọng `Retry-After`, tự động che token trong mọi chuỗi lỗi).
- **User service RPC, DNS subdomain & job nền (`services/user/internal/config/`, `cmd/user/main.go`, `internal/service/`)**:
  - Đọc `CF_MODE` (mặc định `mock`), `CF_API_TOKEN`, `CF_ZONE_ID`, `CF_ORIGIN_HOST`, `CF_CNAME_TARGET`, `CF_POLL_INTERVAL` (mặc định `5m`, dev compose `5s`), tuyệt đối không log token.
  - Tạo bản ghi DNS `CNAME` proxied idempotent khi `PublishSite` lần đầu, giữ nguyên khi `SuspendSite`, xóa bản ghi cũ khi đổi subdomain ở `SaveSiteDraft`.
  - 4 RPC kiểm tra quyền theo `resolveSiteShopID` (chặn seller shop khác), gọi Cloudflare ngoài DB transaction, giới hạn 1 `CUSTOM`/site (`domain_limit`), chống trùng host (`domain_taken`), đổi `is_primary` và `primary_host` khi `ACTIVE`, khi xóa khôi phục subdomain làm primary ngay và gọi Cloudflare DELETE trước khi xóa dòng (lỗi Cloudflare giữ `REMOVING` để job nền thử lại).
  - Job nền `RunDomainPoller` / `PollCustomDomainsOnce`: kiểm tra `PENDING` dưới 7 ngày, đánh `FAILED` quá 7 ngày, và thử lại xóa các dòng `REMOVING`.
- **REST Gateway (`services/gateway/internal/router/router.go`, `handlers_site.go`, `router_domains_test.go`)**:
  - Seller: `GET/POST /api/v1/seller/site/domains`, `POST /api/v1/seller/site/domains/{id}/check`, `DELETE /api/v1/seller/site/domains/{id}` (204), ánh xạ mã lỗi `invalid_hostname` (400), `domain_taken` (409), `domain_limit` (409).
  - Admin: `GET /api/v1/admin/sites/{shop_id}/domains`, `DELETE /api/v1/admin/sites/{shop_id}/domains/{id}` (204).
  - `GET /api/v1/sites/by-host?host=` trả đúng site cho `CUSTOM` `ACTIVE` và `site.primary_host`.
- **Notification & cấu hình môi trường**:
  - `services/notification`: đã dùng `primary_host` từ `GetSiteByShop`; bổ sung test unit trong `sitelink_test.go` cho custom domain và punycode.
  - Cập nhật `.env.example` (giá trị giả) và `docker-compose.yml` (chỉ thêm `CF_*` cho service `user`, mặc định `mock`, `CF_POLL_INTERVAL=5s`).

### Kiểm tra (Gate 5A)
- `make lint` (`buf lint`, `gofmt` + `golangci-lint` toàn bộ 10 Go modules, `rustfmt` + `clippy` cho `services/payment`): **PASS** (0 lỗi).
- `go test -race -count=1 ./...` cho `services/user`, `services/gateway`, `services/notification`: **PASS** (đủ các ca bắt buộc: bảng validate hostname, adapter live với `httptest` gồm tạo/đọc/xóa/429 retry/không lộ token, mock theo đuôi `.verified.test`/`.pending.test`/`.fail.test`, RBAC seller shop khác, giới hạn 1 custom, trùng host 409, xóa khi Cloudflare lỗi giữ `REMOVING` + poller thử lại, đổi primary khi `ACTIVE`, DNS subdomain khi publish/suspend/đổi subdomain).
- `make test`: toàn bộ 10 Go modules (`pkg/common`, `gen/go`, `gateway`, `user`, `product`, `cart`, `promotion`, `order`, `shipping`, `notification`) **PASS**; bước cuối `cargo test services/payment` (không nằm trong phần sửa của 5A) bị lỗi môi trường máy chủ hết dung lượng ổ đĩa (`No space left on device (os error 28)`, phân vùng `/` còn 313M).

### Lệch hợp đồng / Chưa làm
- Không có lệch hợp đồng.
- Không chạy `make e2e`/`seed`/`e2e-web` và không rebuild container theo phân công (thuộc phạm vi swarm 5C). Cần giải phóng dung lượng ổ đĩa trên máy trước khi chạy lại `cargo test services/payment` trong `make test` và trước khi 5C rebuild Docker image.

## 5B – Web: host custom, chuyển hướng 308, SEO, BFF & UI Tên miền

### Đã hoàn thành
- **Phân loại host & cache âm có giới hạn**:
  - `web/src/lib/site/domains.ts`: kiểu dữ liệu `SiteDomainDTO`, bộ chuẩn hóa `normalizeSiteDomain`/`normalizeSiteDomains`, kiểm tra tên miền `validateCustomHostname` (chữ thường ASCII, 2–253 ký tự, nhãn 1–63, cấm IP/cổng/wildcard/`localhost`/`*.local`/`*.internal`/`*.localhost`/root domain và subdomain của root), ánh xạ trạng thái & thông báo lỗi tiếng Việt không rò thuật ngữ nội bộ hay thông tin nhà cung cấp.
  - `web/src/lib/site/host.ts`: mở rộng `classifyHost` thêm loại `custom` và `isTenantSiteHost`; kiểm tra cổng hợp lệ `1..65535` và tối đa một dấu chấm cuối trong `normalizeHostHeader`; hỗ trợ `custom` trong `deriveMarketplaceOrigin`; thêm hàm thuần `resolveCanonicalHostRedirect` cho quyết định 308 (chỉ `GET`, không phải `/api`, không phải `/robots.txt`/`/sitemap.xml`, không phải đường dẫn bị chặn, chỉ từ subdomain sang `primary_host` loại `custom`; giữ scheme+cổng ở dev và ép `https://` ở production).
  - `web/src/lib/site/cache.ts` & `web/src/lib/server/siteLookup.ts`: giới hạn riêng `maxNegativeEntries: 1000` (trong tổng `maxEntries: 2000`) và ưu tiên đẩy bản ghi âm hết hạn/cũ nhất trước để chống tràn bộ nhớ và chống làm rơi cache của các site đang hoạt động.
- **Middleware, buyer BFF & SEO theo `primary_host`**:
  - `web/src/middleware.ts`: xử lý host `custom` tương đương host `site` (chặn `/seller`, `/admin`, `/s`, API ngoài danh sách cho phép; rewrite `/s/[host]`; chuyển hướng `308` kèm `Cache-Control: no-store` từ subdomain sang `primary_host` custom trước `withSession`, không chuyển hướng `/api/**`, `/robots.txt`, `/sitemap.xml`).
  - `web/src/lib/server/sites.ts`: `resolveSiteFromRequest` chấp nhận cả `site` và `custom` nên toàn bộ buyer BFF (`/api/cart`, `/api/checkout`, `/api/orders`, `/api/payments`, `/api/vouchers`) hoạt động trên tên miền riêng.
  - `web/src/lib/site/seo.ts`, `web/src/app/robots.ts`, `web/src/app/sitemap.ts`, `web/src/features/platform/directoryModel.ts`: `canonical`, `og:url`, `robots.txt`, `sitemap.xml` và danh bạ cửa hàng dùng `primary_host`.
- **BFF Seller & Admin (CSRF + RBAC)**:
  - `web/src/lib/server/domains.ts`, `web/src/features/seller/site/siteServer.ts`
  - Seller (`apiRoute` + `assertSellerSession`): `GET/POST /api/seller/site/domains`, `POST /api/seller/site/domains/[id]/check`, `DELETE /api/seller/site/domains/[id]`.
  - Admin (`apiRoute` + `assertAdminSession`): `GET /api/admin/sites/[id]/domains`, `DELETE /api/admin/sites/[id]/domains/[domainId]`.
- **UI Seller "Tên miền" & Admin**:
  - `web/src/features/seller/site/SellerSiteDomainsSection.tsx`, `SellerSiteDomains.module.css`, `SellerSiteEditorClient.tsx`, `web/src/app/(seller)/seller/site/page.tsx`, `web/src/app/(seller)/seller/site/ten-mien/page.tsx`: tab **Tên miền** trong `/seller/site` và trang riêng `/seller/site/ten-mien` (địa chỉ mặc định, thêm tên miền riêng, bảng bản ghi DNS CNAME/TXT có nút sao chép trên desktop & mobile, trạng thái tiếng Việt, nút Kiểm tra lại, gỡ tên miền có xác nhận nội tuyến).
  - `web/src/features/admin/sites/AdminSitesClient.tsx`, `AdminSitesClient.module.css`: xem danh sách tên miền của từng site và gỡ tên miền riêng có xác nhận nội tuyến.

### Kiểm tra (Gate 5B)
- `cd web && npm run lint`: **PASS** (0 lỗi, 0 cảnh báo)
- `cd web && npm run format:check`: **PASS**
- `cd web && npm run test:unit`: **PASS** (87/87 test đạt, gồm unit test mới cho `classifyHost`, `validateCustomHostname`, `resolveCanonicalHostRedirect`, cache âm có giới hạn, CSRF custom host và chuẩn hóa/ánh xạ lỗi domain)
- `cd web && npx tsc --noEmit`: **PASS**
- `cd web && NEXT_DIST_DIR=.next-5b npm run build`: **PASS** (đã xóa `.next-5b`, khôi phục `web/tsconfig.json` và `web/next-env.d.ts`)

### Lệch hợp đồng / Chưa làm
- Không có lệch hợp đồng. Chưa chạy `make e2e`/`seed`/`e2e-web` hay rebuild container theo phân công (thuộc phạm vi swarm 5C).

---

## 5C – QA, Seed, Docker & Gate cuối

### Đã hoàn thành
1. **Khôi phục & xác minh stack Docker (`user`, `gateway`, `web`, `postgres`):**
   - Khởi động lại container `postgres` (bị thoát trước đó do hết dung lượng ổ đĩa, đã giải phóng còn ~50 GB), xác nhận PostgreSQL phục hồi WAL sạch và migration `0003_domains_cloudflare.sql` đã áp dụng đầy đủ trên `user_db` (các cột `status`, `cf_record_id`, `cf_custom_hostname_id`, `verification`, `last_checked_at`, `last_error` cùng các index `idx_domains_site_custom`, `idx_domains_site_primary`, `idx_domains_poll_status`).
   - Chạy `GATEWAY_PORT=28080 docker compose up -d --build --no-deps --wait user gateway web`: cả 3 image `nexus_ecom-user`, `nexus_ecom-gateway`, `nexus_ecom-web` khớp nguồn hiện tại và toàn bộ 15 container đều chạy ổn định (`healthy`). Không xóa image hay build cache Docker của dự án khác.
2. **Seed (`tools/seed/seed.mjs`):**
   - Idempotent 100%: shop `ha-noi` gắn tên miền riêng `hanoi-knit.verified.test` (mock → `ACTIVE`, `is_primary=true`, `primary_host=hanoi-knit.verified.test`); shop `da-nang` gắn `danang.pending.test` (`PENDING`, `is_primary=false`, `primary_host=da-nang.nexusmart.localhost`). Xác nhận chạy lần đầu tạo (`created`) và chạy lặp lại tái sử dụng (`reused`) không sinh bản ghi thừa.
3. **Go E2E (`tests/e2e/15_custom_domains_test.go`, `tests/e2e/types.go`):**
   - Kiểm thử đầy đủ qua gateway REST: tạo/kiểm tra/xóa tên miền riêng (`PENDING`, `ACTIVE`, `FAILED`); kiểm tra RBAC (seller shop khác `403`/`404`, buyer `403`, seller gọi admin `403`); giới hạn 1 tên miền riêng/site (`409 domain_limit`); trùng host không phân biệt hoa thường (`409 domain_taken`); 18 ca hostname xấu (`400 invalid_hostname` gồm IPv4/IPv6, root domain, subdomain của root, wildcard, kèm cổng, `localhost`, `.local`, `.internal`, `.localhost`, rỗng, khoảng trắng, gạch dưới); `GET /api/v1/sites/by-host` trả `200` + `primary_host` cho custom `ACTIVE` và `404` cho `PENDING`/`FAILED`/đã xóa; `is_primary` và `primary_host` chuyển đổi và hoàn nguyên chính xác.
   - Đã kiểm tra `gofmt -l tests/e2e` (sạch) và `golangci-lint` trên `tests/e2e` (0 lỗi).
4. **Web E2E (`tests/e2e-web/05_custom_domains.test.mjs`, `01_root_platform.test.mjs`, `03_isolation_and_security.test.mjs`, `lib/fixtures.mjs`):**
   - Gọi bằng header `Host`: `hanoi-knit.verified.test:3300` hiển thị đúng site Hà Nội + phạm vi voucher + chặn `/seller`, `/admin`, `/api/seller/*`, `/api/admin/*`, `/s/*` bằng `404`; `GET ha-noi.nexusmart.localhost:3300/` và `/san-pham?q=jogger` trả `308` sang `http://hanoi-knit.verified.test:3300/...` giữ nguyên scheme/cổng/path/query; `/api/cart` và `/api/auth/me` trên subdomain không bị `308`; `danang.pending.test` trả `404` trên cả trang lẫn API trong khi subdomain `da-nang` vẫn `200`; host lạ/xấu trả `404`; cookie host-only không rò giữa custom host, subdomain và root host + CSRF chéo host trả `403 BAD_ORIGIN`; `<link rel="canonical">`, `og:url`, `/robots.txt`, `/sitemap.xml` và redirect cũ ở root đều dùng `hanoi-knit.verified.test`; BFF seller/admin domain trên root host kiểm soát RBAC và CSRF đúng.
   - Kiểm tra bổ sung `web`: `npm run lint` (0 lỗi), `npm run format:check` (sạch), `npm run test:unit` (87/87 PASS); probe thực tế SSR `/seller/site`, `/seller/site/ten-mien`, `/admin/sites` đều `200` không lộ chuỗi `cloudflare` hay bí mật.

### Gate cuối (đầu ra thật nguyên văn)

#### 1. `make lint`
```text
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
    Finished `dev` profile [unoptimized + debuginfo] target(s) in 0.11s
EXIT=0
```

#### 2. `make test`
```text
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
ok  	github.com/nexusmart/core/services/user/internal/cloudflare	(cached)
ok  	github.com/nexusmart/core/services/user/internal/password	(cached)
ok  	github.com/nexusmart/core/services/user/internal/security	(cached)
ok  	github.com/nexusmart/core/services/user/internal/service	(cached)
?   	github.com/nexusmart/core/services/user/migrations	[no test files]
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
ok  	github.com/nexusmart/core/services/promotion/internal/calculator	(cached)
ok  	github.com/nexusmart/core/services/promotion/internal/events	(cached)
ok  	github.com/nexusmart/core/services/promotion/internal/lua	(cached)
?   	github.com/nexusmart/core/services/promotion/internal/store	[no test files]
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
ok  	github.com/nexusmart/core/services/shipping/internal/address	(cached)
?   	github.com/nexusmart/core/services/shipping/internal/config	[no test files]
?   	github.com/nexusmart/core/services/shipping/internal/events	[no test files]
?   	github.com/nexusmart/core/services/shipping/internal/poller	[no test files]
?   	github.com/nexusmart/core/services/shipping/internal/store	[no test files]
ok  	github.com/nexusmart/core/services/shipping/internal/carrier	(cached)
ok  	github.com/nexusmart/core/services/shipping/internal/grpcserver	(cached)
?   	github.com/nexusmart/core/services/shipping/migrations	[no test files]
ok  	github.com/nexusmart/core/services/shipping/internal/service	(cached)
ok  	github.com/nexusmart/core/services/shipping/internal/webhook	(cached)
==> go test services/notification
?   	github.com/nexusmart/core/services/notification/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/notification/internal/config	[no test files]
?   	github.com/nexusmart/core/services/notification/internal/db	[no test files]
ok  	github.com/nexusmart/core/services/notification/internal/consumer	(cached)
?   	github.com/nexusmart/core/services/notification/internal/store	[no test files]
ok  	github.com/nexusmart/core/services/notification/internal/grpcserver	(cached)
?   	github.com/nexusmart/core/services/notification/migrations	[no test files]
ok  	github.com/nexusmart/core/services/notification/internal/mailer	(cached)
ok  	github.com/nexusmart/core/services/notification/internal/service	(cached)
ok  	github.com/nexusmart/core/services/notification/internal/userclient	(cached)
==> cargo test services/payment
    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.11s
     Running unittests src/lib.rs (target/debug/deps/nexusmart_payment-e82bf25ec01a60de)

running 39 tests
test auth::tests::rejects_empty_secrets ... ok
test auth::tests::spoofed_metadata_without_jwt_is_unauthenticated ... ok
test domain::tests::test_allocation_rejects_excessive_commission ... ok
test domain::tests::test_allocation_split_exact_vnd ... ok
test domain::tests::test_multi_shop_single_suborder_cancel_refund_scope ... ok
test domain::tests::test_per_suborder_refund_cap_prevents_multi_shop_double_refund ... ok
test auth::tests::user_jwt_wins_over_metadata ... ok
test domain::tests::test_reconciliation_classifications ... ok
test domain::tests::test_reconciliation_channel_matches_partitions_100_percent ... ok
test domain::tests::test_refund_cap_enforced ... ok
test domain::tests::test_storefront_allocation_validation_rejects_cross_shop ... ok
test domain::tests::test_sales_channel_validation_and_defaults ... ok
test auth::tests::http_auth_enforces_user_jwt_and_admin_role ... ok
test grpc::tests::test_proto_timestamp_roundtrip ... ok
test grpc::tests::test_parse_optional_uuid_validation ... ok
test grpc::tests::test_to_proto_payment_legacy_default_and_storefront_mapping ... ok
test ledger::tests::test_unbalanced_transaction_rejected ... ok
test auth::tests::service_tokens_are_verified_and_scoped ... ok
test ledger::tests::test_zero_or_negative_or_single_line_rejected ... ok
test ledger::tests::test_sales_channel_ledger_partitioning_matches_unfiltered_100_percent ... ok
test webhook_security::tests::test_contracts_canonical_webhook_payload_deserializes ... ok
test webhook_security::tests::test_extract_order_code_from_sepay_transfer_content ... ok
test ledger::tests::test_full_lifecycle_ledger_stays_balanced_across_all_scenarios ... ok
test webhook_security::tests::test_webhook_10x_idempotency_simulation ... ok
test workers::tests::test_nats_retry_until_max_deliver_then_dlq ... ok
test webhook_security::tests::test_webhook_amount_mismatch_and_late_payment_decisions ... ok
test webhook_security::tests::test_replay_protection_timestamp_window ... ok
test telemetry::tests::test_extract_trace_id_from_w3c_traceparent ... ok
test webhook_security::tests::test_hmac_verification_valid_and_invalid ... ok
test telemetry::tests::test_masking_pii_and_secrets ... ok
test vietqr::tests::test_vietqr_mock_deterministic_payload_and_valid_hmac ... ok
test grpc::tests::test_grpc_sales_channel_validation_on_create_summary_and_reconcile ... ok
test grpc::tests::test_grpc_validation_and_red_metrics_recorded ... ok
test grpc::tests::test_b1_all_rpcs_reject_anonymous_and_spoofed_metadata_and_disallowed_service_tokens ... ok
test http::tests::test_webhook_rejects_missing_sig_missing_timestamp_invalid_sig_and_stale_ts ... ok
test http::tests::test_healthz_and_red_metrics_no_permissive_cors ... ok
test grpc::tests::test_grpc_defense_in_depth_rbac_enforcement ... ok
test http::tests::test_b1_http_sse_and_internal_routes_reject_anonymous_spoofed_and_non_admin ... ok
test vietqr::tests::test_circuit_breaker_transitions ... ok

test result: ok. 39 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.03s

     Running unittests src/main.rs (target/debug/deps/nexusmart_payment-b11deacfd41411c1)

running 0 tests

test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s

     Running tests/sales_channel_db_integration.rs (target/debug/deps/sales_channel_db_integration-0cb2110ad8e260d1)

running 1 test
test test_pg_migration_immutability_and_channel_filtered_ledger_and_reconciliation ... ok

test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s

   Doc-tests nexusmart_payment

running 0 tests

test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s

EXIT=0
```

#### 3. `make e2e && make seed && make e2e-web && make e2e-web`
```text
GOMODCACHE="$(go env GOMODCACHE)" docker compose --profile e2e run --rm --user "$(id -u):$(id -g)" e2e
 Container nexus_ecom-e2e-run-b1bf380c99ac Creating 
 Container nexus_ecom-e2e-run-b1bf380c99ac Created 
=== RUN   TestE2E_HealthAndObservability
=== RUN   TestE2E_HealthAndObservability/All9Services_HealthzAndReadyz
=== RUN   TestE2E_HealthAndObservability/Gateway_TraceIDHeader
    01_health_test.go:43: Gateway returned X-Trace-Id=4bf92f3577b34da6a3ce929d0e0e4736 (sent traceparent=4bf92f3577b34da6a3ce929d0e0e4736)
=== RUN   TestE2E_HealthAndObservability/All9Services_REDMetrics
--- PASS: TestE2E_HealthAndObservability (0.07s)
    --- PASS: TestE2E_HealthAndObservability/All9Services_HealthzAndReadyz (0.02s)
    --- PASS: TestE2E_HealthAndObservability/Gateway_TraceIDHeader (0.00s)
    --- PASS: TestE2E_HealthAndObservability/All9Services_REDMetrics (0.06s)
=== RUN   TestE2E_MultiShopCheckout_COD
    02_cod_checkout_test.go:19: [seller1] received OTP from Mailpit for seller1-f6e3df8a-2@nexusmart.test
    02_cod_checkout_test.go:19: [seller2] received OTP from Mailpit for seller2-f6e3df8a-6@nexusmart.test
    02_cod_checkout_test.go:20: Marketplace ready: category=3cd86345-d638-4e43-9d01-26c7ee190a5e shop1=9fe1eab7-c9cf-42a4-bd64-8c0cd1612eb5 shop2=93db584c-0220-4c06-8d3e-3fb08e11d7b2
    02_cod_checkout_test.go:40: [buyer-cod] received OTP from Mailpit for buyer-cod-f6e3df8a-15@nexusmart.test
    02_cod_checkout_test.go:74: Shipping quotes: shop1=ghn/STANDARD (24000 VND), shop2=ghn/STANDARD (24000 VND)
    02_cod_checkout_test.go:231: [buyer-intruder] received OTP from Mailpit for buyer-intruder-f6e3df8a-18@nexusmart.test
--- PASS: TestE2E_MultiShopCheckout_COD (2.52s)
=== RUN   TestE2E_MultiShopCheckout_VietQRMock
    03_vietqr_checkout_test.go:22: [buyer-vietqr] received OTP from Mailpit for buyer-vietqr-f6e3df8a-22@nexusmart.test
--- PASS: TestE2E_MultiShopCheckout_VietQRMock (0.70s)
=== RUN   TestE2E_OrderIdempotency
    04_order_idempotency_test.go:17: [buyer-idem] received OTP from Mailpit for buyer-idem-f6e3df8a-26@nexusmart.test
=== RUN   TestE2E_OrderIdempotency/SequentialReplay
=== RUN   TestE2E_OrderIdempotency/ConcurrentBurst
--- PASS: TestE2E_OrderIdempotency (0.40s)
    --- PASS: TestE2E_OrderIdempotency/SequentialReplay (0.02s)
    --- PASS: TestE2E_OrderIdempotency/ConcurrentBurst (0.02s)
=== RUN   TestE2E_PaymentWebhookIdempotencyAndSecurity
    05_webhook_security_test.go:20: [buyer-webhook] received OTP from Mailpit for buyer-webhook-f6e3df8a-30@nexusmart.test
=== RUN   TestE2E_PaymentWebhookIdempotencyAndSecurity/RejectInvalidAndMissingSignature
=== RUN   TestE2E_PaymentWebhookIdempotencyAndSecurity/RejectStaleTimestamp
=== RUN   TestE2E_PaymentWebhookIdempotencyAndSecurity/Replay10Times_RecordedOnce
--- PASS: TestE2E_PaymentWebhookIdempotencyAndSecurity (0.99s)
    --- PASS: TestE2E_PaymentWebhookIdempotencyAndSecurity/RejectInvalidAndMissingSignature (0.00s)
    --- PASS: TestE2E_PaymentWebhookIdempotencyAndSecurity/RejectStaleTimestamp (0.00s)
    --- PASS: TestE2E_PaymentWebhookIdempotencyAndSecurity/Replay10Times_RecordedOnce (0.62s)
=== RUN   TestE2E_FlashSale_1000Concurrent_100Slots
=== RUN   TestE2E_FlashSale_1000Concurrent_100Slots/1000Concurrent_100Slots_ZeroOversell
    06_flash_sale_test.go:150: FlashSale 1000 concurrent requests completed in 295.886577ms: succeeded=100 failed=900
=== RUN   TestE2E_FlashSale_1000Concurrent_100Slots/RealCheckout_AtFlashPrice
    06_flash_sale_test.go:209: [buyer-flash] received OTP from Mailpit for buyer-flash-f6e3df8a-35@nexusmart.test
--- PASS: TestE2E_FlashSale_1000Concurrent_100Slots (1.00s)
    --- PASS: TestE2E_FlashSale_1000Concurrent_100Slots/1000Concurrent_100Slots_ZeroOversell (0.57s)
    --- PASS: TestE2E_FlashSale_1000Concurrent_100Slots/RealCheckout_AtFlashPrice (0.41s)
=== RUN   TestE2E_PaymentFailed_SagaCompensation
    07_saga_compensation_test.go:29: [buyer-saga] received OTP from Mailpit for buyer-saga-f6e3df8a-40@nexusmart.test
    07_saga_compensation_test.go:81: Reserved state: stock=27 (before=30) voucher_quota=4 (before=5) coins=3000 (before=10000)
    07_saga_compensation_test.go:141: Saga compensation verified: stock=30 voucher_quota=5 coins=10000 all 100% restored
--- PASS: TestE2E_PaymentFailed_SagaCompensation (1.01s)
=== RUN   TestE2E_LedgerAlwaysBalanced
    08_ledger_test.go:9: Double-Entry Ledger Summary: total_debits_vnd=299571110 total_credits_vnd=299571110 is_balanced=true accounts=48
    08_ledger_test.go:13:   account=cod_receivable                             type=ASSET      debit= 236168500 credit=         0 balance= 236168500
    08_ledger_test.go:13:   account=escrow_liability                           type=LIABILITY  debit=  14294055 credit= 272667500 balance= 258373445
    08_ledger_test.go:13:   account=gateway_clearing                           type=ASSET      debit=  36499000 credit=         0 balance=  36499000
    08_ledger_test.go:13:   account=platform_fee_revenue                       type=REVENUE    debit=         0 credit=    720000 balance=    720000
    08_ledger_test.go:13:   account=refund_payable                             type=LIABILITY  debit=         0 credit=    330000 balance=    330000
    08_ledger_test.go:13:   account=seller_available:0e17298b-c59a-4198-b374-63299a81fb11 type=LIABILITY  debit=         0 credit=    795455 balance=    795455
    08_ledger_test.go:13:   account=seller_available:1528456d-6466-4ca0-87b9-066dff4e0a39 type=LIABILITY  debit=         0 credit=    795455 balance=    795455
    08_ledger_test.go:13:   account=seller_available:1f6fe8a8-23cf-4110-8ef8-3df199c305cf type=LIABILITY  debit=         0 credit=    340455 balance=    340455
    08_ledger_test.go:13:   account=seller_available:3058d178-6439-4e37-a8eb-0921d958ed37 type=LIABILITY  debit=         0 credit=    340455 balance=    340455
    08_ledger_test.go:13:   account=seller_available:3cbc559f-3486-405d-9c5f-ba3e5502fadc type=LIABILITY  debit=         0 credit=    795455 balance=    795455
    08_ledger_test.go:13:   account=seller_available:3d828c4d-eac7-4676-92e0-3df86aa7762d type=LIABILITY  debit=         0 credit=    795455 balance=    795455
    08_ledger_test.go:13:   account=seller_available:45e8294d-645f-4baf-a466-bffdcd99ee55 type=LIABILITY  debit=         0 credit=    795455 balance=    795455
    08_ledger_test.go:13:   account=seller_available:543a5f4a-2b94-4f75-90de-f764178682a3 type=LIABILITY  debit=         0 credit=    795455 balance=    795455
    08_ledger_test.go:13:   account=seller_available:5e21e466-1fb0-48b0-b6e6-85196bce0b4a type=LIABILITY  debit=         0 credit=    795455 balance=    795455
    08_ledger_test.go:13:   account=seller_available:670ebdd0-b619-4534-921a-18d08a1a7bb6 type=LIABILITY  debit=         0 credit=    795455 balance=    795455
    08_ledger_test.go:13:   account=seller_available:9637a5a4-2cc5-4375-bb0a-08a0633ef9b9 type=LIABILITY  debit=         0 credit=    795455 balance=    795455
    08_ledger_test.go:13:   account=seller_available:985c5ba5-253e-4072-9281-cc05f28501be type=LIABILITY  debit=         0 credit=    795455 balance=    795455
    08_ledger_test.go:13:   account=seller_available:9fe1eab7-c9cf-42a4-bd64-8c0cd1612eb5 type=LIABILITY  debit=         0 credit=    340455 balance=    340455
    08_ledger_test.go:13:   account=seller_available:abbb5365-b07e-47dc-9797-2215884e9280 type=LIABILITY  debit=         0 credit=    340455 balance=    340455
    08_ledger_test.go:13:   account=seller_available:b9b18cd3-eb8f-4479-b5db-6d48dc069fb7 type=LIABILITY  debit=         0 credit=    340455 balance=    340455
    08_ledger_test.go:13:   account=seller_available:bba28f95-dbeb-4bdd-9b0b-64ecf308602c type=LIABILITY  debit=         0 credit=    340455 balance=    340455
    08_ledger_test.go:13:   account=seller_available:c010bc1d-372f-48af-bb98-ca68c24de500 type=LIABILITY  debit=         0 credit=    340455 balance=    340455
    08_ledger_test.go:13:   account=seller_available:d56e7149-6e61-4b90-8e16-979dafd3bd4c type=LIABILITY  debit=         0 credit=    340455 balance=    340455
    08_ledger_test.go:13:   account=seller_available:def2dd01-9625-4e16-baef-8ea63307275c type=LIABILITY  debit=         0 credit=    340455 balance=    340455
    08_ledger_test.go:13:   account=seller_available:e9bf694a-356e-4682-81e5-4219989b1c2f type=LIABILITY  debit=         0 credit=    795455 balance=    795455
    08_ledger_test.go:13:   account=seller_available:f07c8f2b-e3ae-4a32-9766-03d307aa1256 type=LIABILITY  debit=         0 credit=    795455 balance=    795455
    08_ledger_test.go:13:   account=seller_pending:0e17298b-c59a-4198-b374-63299a81fb11 type=LIABILITY  debit=    795455 credit=    795455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:1528456d-6466-4ca0-87b9-066dff4e0a39 type=LIABILITY  debit=    795455 credit=    795455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:1f6fe8a8-23cf-4110-8ef8-3df199c305cf type=LIABILITY  debit=    340455 credit=    340455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:3058d178-6439-4e37-a8eb-0921d958ed37 type=LIABILITY  debit=    340455 credit=    340455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:3cbc559f-3486-405d-9c5f-ba3e5502fadc type=LIABILITY  debit=    795455 credit=    795455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:3d828c4d-eac7-4676-92e0-3df86aa7762d type=LIABILITY  debit=    795455 credit=    795455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:45e8294d-645f-4baf-a466-bffdcd99ee55 type=LIABILITY  debit=    795455 credit=    795455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:543a5f4a-2b94-4f75-90de-f764178682a3 type=LIABILITY  debit=    795455 credit=    795455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:5e21e466-1fb0-48b0-b6e6-85196bce0b4a type=LIABILITY  debit=    795455 credit=    795455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:670ebdd0-b619-4534-921a-18d08a1a7bb6 type=LIABILITY  debit=    795455 credit=    795455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:9637a5a4-2cc5-4375-bb0a-08a0633ef9b9 type=LIABILITY  debit=    795455 credit=    795455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:985c5ba5-253e-4072-9281-cc05f28501be type=LIABILITY  debit=    795455 credit=    795455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:9fe1eab7-c9cf-42a4-bd64-8c0cd1612eb5 type=LIABILITY  debit=    340455 credit=    340455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:abbb5365-b07e-47dc-9797-2215884e9280 type=LIABILITY  debit=    340455 credit=    340455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:b9b18cd3-eb8f-4479-b5db-6d48dc069fb7 type=LIABILITY  debit=    340455 credit=    340455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:bba28f95-dbeb-4bdd-9b0b-64ecf308602c type=LIABILITY  debit=    340455 credit=    340455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:c010bc1d-372f-48af-bb98-ca68c24de500 type=LIABILITY  debit=    340455 credit=    340455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:d56e7149-6e61-4b90-8e16-979dafd3bd4c type=LIABILITY  debit=    340455 credit=    340455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:def2dd01-9625-4e16-baef-8ea63307275c type=LIABILITY  debit=    340455 credit=    340455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:e9bf694a-356e-4682-81e5-4219989b1c2f type=LIABILITY  debit=    795455 credit=    795455 balance=         0
    08_ledger_test.go:13:   account=seller_pending:f07c8f2b-e3ae-4a32-9766-03d307aa1256 type=LIABILITY  debit=    795455 credit=    795455 balance=         0
    08_ledger_test.go:13:   account=shipping_fee_payable                       type=LIABILITY  debit=         0 credit=    634500 balance=    634500
--- PASS: TestE2E_LedgerAlwaysBalanced (0.00s)
=== RUN   TestE2E_SalesChannel
    09_sales_channel_test.go:40: [buyer-channel] received OTP from Mailpit for buyer-channel-f6e3df8a-49@nexusmart.test
=== RUN   TestE2E_SalesChannel/DefaultCheckout_ReadsAsMarketplace
=== RUN   TestE2E_SalesChannel/Storefront_COD_CartFilter_SelectiveClear_And_Immutability
=== RUN   TestE2E_SalesChannel/Storefront_VietQRMock_Webhook_And_Refund
=== RUN   TestE2E_SalesChannel/Storefront_CrossShopItemRejected_ZeroStockChange
    09_sales_channel_test.go:402: Cross-shop sole item rejected cleanly: HTTP 400 body={"error":{"code":"INVALID_ARGUMENT","message":"STOREFRONT_SHOP_MISMATCH: all checkout items must belong to storefront shop 9fe1eab7-c9cf-42a4-bd64-8c0cd1612eb5","trace_id":"01aeeb8877fe906703c4bd863eb499c5"}}
    09_sales_channel_test.go:418: Cross-shop mixed items rejected cleanly: HTTP 400 body={"error":{"code":"INVALID_ARGUMENT","message":"STOREFRONT_SHOP_MISMATCH: all checkout items must belong to storefront shop 9fe1eab7-c9cf-42a4-bd64-8c0cd1612eb5","trace_id":"41a0eab4518e7b0a73f2dbce561c0ccc"}}
=== RUN   TestE2E_SalesChannel/Storefront_InvalidAndInactiveShopRejected_ZeroStockChange
    09_sales_channel_test.go:483: [pending-shop-owner] received OTP from Mailpit for pending-shop-owner-f6e3df8a-52@nexusmart.test
=== RUN   TestE2E_SalesChannel/Storefront_PlatformVoucher_Freeship_OtherShopVoucher_Coins_Rejected
    09_sales_channel_test.go:604: PLATFORM voucher on STOREFRONT rejected cleanly: HTTP 400 body={"error":{"code":"INVALID_ARGUMENT","message":"STOREFRONT_PLATFORM_VOUCHER_NOT_ALLOWED: platform vouchers cannot be used on STOREFRONT orders","trace_id":"880b7e0d1241be901c94fd239b31fab1"}}
    09_sales_channel_test.go:604: FREESHIP voucher on STOREFRONT rejected cleanly: HTTP 400 body={"error":{"code":"INVALID_ARGUMENT","message":"STOREFRONT_FREESHIP_VOUCHER_NOT_ALLOWED: platform freeship vouchers cannot be used on STOREFRONT orders","trace_id":"be1e7b9b13b2f56a8bb8a8ae0b11682d"}}
    09_sales_channel_test.go:604: Other-shop SHOP voucher on STOREFRONT rejected cleanly: HTTP 400 body={"error":{"code":"INVALID_ARGUMENT","message":"STOREFRONT_VOUCHER_SHOP_MISMATCH: shop voucher SHPVF6E3DF8A46 belongs to shop \"93db584c-0220-4c06-8d3e-3fb08e11d7b2\", not storefront shop \"9fe1eab7-c9cf-42a4-bd64-8c0cd1612eb5\"","trace_id":"93e50fc51d99d21346c78d71864e0ae2"}}
    09_sales_channel_test.go:604: Platform coins on STOREFRONT rejected cleanly: HTTP 400 body={"error":{"code":"INVALID_ARGUMENT","message":"STOREFRONT_COINS_NOT_ALLOWED: platform coins cannot be used on STOREFRONT orders","trace_id":"0d55a02ba0b05d09d0aaaabb7a862fb7"}}
--- PASS: TestE2E_SalesChannel (2.21s)
    --- PASS: TestE2E_SalesChannel/DefaultCheckout_ReadsAsMarketplace (0.03s)
    --- PASS: TestE2E_SalesChannel/Storefront_COD_CartFilter_SelectiveClear_And_Immutability (0.48s)
    --- PASS: TestE2E_SalesChannel/Storefront_VietQRMock_Webhook_And_Refund (0.73s)
    --- PASS: TestE2E_SalesChannel/Storefront_CrossShopItemRejected_ZeroStockChange (0.11s)
    --- PASS: TestE2E_SalesChannel/Storefront_InvalidAndInactiveShopRejected_ZeroStockChange (0.37s)
    --- PASS: TestE2E_SalesChannel/Storefront_PlatformVoucher_Freeship_OtherShopVoucher_Coins_Rejected (0.09s)
=== RUN   TestE2E_LedgerAndReconciliation_ChannelFilter
=== RUN   TestE2E_LedgerAndReconciliation_ChannelFilter/LedgerSummary_ByChannel_BalancedAndExactSum
    10_ledger_channel_test.go:21: Ledger totals: ALL=(debits=301570110 credits=301570110 balanced=true) MARKETPLACE=(debits=257780110 credits=257780110 balanced=true) STOREFRONT=(debits=43790000 credits=43790000 balanced=true)
=== RUN   TestE2E_LedgerAndReconciliation_ChannelFilter/Reconciliation_ByChannel_ExactSum
    10_ledger_channel_test.go:66: Reconciliation (2026-11-21): ALL=(items=33 matched=33) MARKETPLACE=(items=17 matched=17) STOREFRONT=(items=16 matched=16)
--- PASS: TestE2E_LedgerAndReconciliation_ChannelFilter (0.03s)
    --- PASS: TestE2E_LedgerAndReconciliation_ChannelFilter/LedgerSummary_ByChannel_BalancedAndExactSum (0.01s)
    --- PASS: TestE2E_LedgerAndReconciliation_ChannelFilter/Reconciliation_ByChannel_ExactSum (0.02s)
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/ProductService.UpdateStock
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/CartService.ClearItems
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/CartService.GetCart
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/ShippingService.CreateShipment
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/ShippingService.CancelShipment
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/ShippingService.GetShipment
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/UserService.ValidateUser
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/UserService.WriteAuditLog
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/OrderService.MarkPaymentResult
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/NotificationService.ListNotifications
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/NotificationService.MarkRead
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PromotionService.CreditCoins
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PromotionService.PreviewDiscounts
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PromotionService.GetCoinBalance
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PromotionService.SaveVoucher
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PaymentService.GetLedgerSummary
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PaymentService.ReleaseEscrow
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PaymentService.RunReconciliation
--- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata (0.85s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/ProductService.UpdateStock (0.07s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/CartService.ClearItems (0.14s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/CartService.GetCart (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/ShippingService.CreateShipment (0.20s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/ShippingService.CancelShipment (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/ShippingService.GetShipment (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/UserService.ValidateUser (0.16s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/UserService.WriteAuditLog (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/OrderService.MarkPaymentResult (0.07s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/NotificationService.ListNotifications (0.08s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/NotificationService.MarkRead (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PromotionService.CreditCoins (0.05s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PromotionService.PreviewDiscounts (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PromotionService.GetCoinBalance (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PromotionService.SaveVoucher (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PaymentService.GetLedgerSummary (0.08s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PaymentService.ReleaseEscrow (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PaymentService.RunReconciliation (0.00s)
=== RUN   TestE2E_RateLimit_Real429AndNoXFFBypass
    12_ratelimit_test.go:69: Genuine 429 observed after 60 allowed requests: X-RateLimit-Limit="60" X-RateLimit-Remaining="0" Retry-After="60" body={"error":{"code":"RATE_LIMITED","message":"rate limit exceeded, please retry later","trace_id":"e5cfaec5d463119ee0770ed1c5132c9a"}}
--- PASS: TestE2E_RateLimit_Real429AndNoXFFBypass (0.07s)
=== RUN   TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit
=== RUN   TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit/SubdomainValidation_RejectsInvalidReservedAndPhishing
=== RUN   TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit/StrictConfigValidation_RejectsUnknownFieldsUnsafeURLsLowContrastAndTooManySections
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
=== RUN   TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit/DraftPublishV2Rollback_And_DuplicateCaseInsensitive
=== RUN   TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit/CrossTenantIsolation_SellerBCannotAccessSellerA
=== RUN   TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit/AdminSuspendUnsuspend_And_AuditLogs
    13_sites_backend_test.go:705: Site 2ac36c94-e7d0-46bc-bc50-234a9a90b151 audit trail verified: map[admin.site.suspend:1 admin.site.unsuspend:1 site.publish:2 site.rollback:1 site.save_draft:2]
--- PASS: TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit (0.14s)
    --- PASS: TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit/SubdomainValidation_RejectsInvalidReservedAndPhishing (0.01s)
    --- PASS: TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit/StrictConfigValidation_RejectsUnknownFieldsUnsafeURLsLowContrastAndTooManySections (0.01s)
    --- PASS: TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit/DraftPublishV2Rollback_And_DuplicateCaseInsensitive (0.03s)
    --- PASS: TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit/CrossTenantIsolation_SellerBCannotAccessSellerA (0.08s)
    --- PASS: TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit/AdminSuspendUnsuspend_And_AuditLogs (0.02s)
=== RUN   TestE2E_FlashSaleStockReserve_Phase2b
    14_flash_sale_stock_reserve_2b_test.go:46: [buyer-fs2b] received OTP from Mailpit for buyer-fs2b-f6e3df8a-55@nexusmart.test
=== RUN   TestE2E_FlashSaleStockReserve_Phase2b/A_ApproveRejected_WhenAvailableStockInsufficient
    14_flash_sale_stock_reserve_2b_test.go:108: Insufficient-stock flash approval rejected cleanly: HTTP 409 body={"error":{"code":"FAILED_PRECONDITION","message":"INSUFFICIENT_STOCK: sku 1a26bb8d-8cf1-459b-8a4c-343ee9c5be0a has 20 available, requested 50 for flash sale","trace_id":"e8f319cad815855ddabf45f1fb0b81e8"}}
=== RUN   TestE2E_FlashSaleStockReserve_Phase2b/BC_RegularIsolationBothChannels_And_1000ConcurrentFlashCheckouts
    14_flash_sale_stock_reserve_2b_test.go:211: Regular MARKETPLACE #6 rejected cleanly: HTTP 409
    14_flash_sale_stock_reserve_2b_test.go:226: Regular STOREFRONT #6 rejected cleanly: HTTP 409
    14_flash_sale_stock_reserve_2b_test.go:314: Phase 2b 1000 concurrent flash checkouts completed in 1.090710893s: succeeded=100 failed=900 error_codes=map[ResourceExhausted:3 FailedPrecondition:897]
=== RUN   TestE2E_FlashSaleStockReserve_Phase2b/D_WindowEnd_ReleasesUnconsumedStock_Idempotent_And_RegularBuysReleased
=== RUN   TestE2E_FlashSaleStockReserve_Phase2b/E_DoubleEntryLedger_Balanced
    14_flash_sale_stock_reserve_2b_test.go:502: Phase 2b Ledger Summary: debits=318403110 credits=318403110 is_balanced=true accounts=48
--- PASS: TestE2E_FlashSaleStockReserve_Phase2b (2.27s)
    --- PASS: TestE2E_FlashSaleStockReserve_Phase2b/A_ApproveRejected_WhenAvailableStockInsufficient (0.01s)
    --- PASS: TestE2E_FlashSaleStockReserve_Phase2b/BC_RegularIsolationBothChannels_And_1000ConcurrentFlashCheckouts (1.17s)
    --- PASS: TestE2E_FlashSaleStockReserve_Phase2b/D_WindowEnd_ReleasesUnconsumedStock_Idempotent_And_RegularBuysReleased (0.72s)
    --- PASS: TestE2E_FlashSaleStockReserve_Phase2b/E_DoubleEntryLedger_Balanced (0.00s)
=== RUN   TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary
    15_custom_domains_test.go:140: [cdom-a] received OTP from Mailpit for cdom-a-f6e3df8a-61@nexusmart.test
    15_custom_domains_test.go:141: [cdom-b] received OTP from Mailpit for cdom-b-f6e3df8a-65@nexusmart.test
=== RUN   TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/SetupSites_InitialSubdomainDomainsActivePrimary
=== RUN   TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/BadHostnames_RejectedWith400
    15_custom_domains_test.go:251: Bad hostname [ipv4 loopback] ("127.0.0.1") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [ipv4 private] ("192.168.1.10") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [ipv6 bracketed loopback] ("[::1]") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [ipv6 address] ("2001:db8::1") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [root domain] ("nexusmart.localhost") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [subdomain of root] ("shop.nexusmart.localhost") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [wildcard example] ("*.example.test") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [wildcard verified] ("*.shop.verified.test") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [host with port] ("shop.verified.test:3300") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [no dot localhost] ("localhost") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [no dot onlylabel] ("onlylabel") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [forbidden .local] ("shop.local") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [forbidden .internal] ("shop.internal") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [forbidden .localhost] ("foo.localhost") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [empty hostname] ("") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [whitespace hostname] ("   ") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [space in hostname] ("bad host.test") rejected cleanly: HTTP 400 (invalid_hostname)
    15_custom_domains_test.go:251: Bad hostname [underscore in hostname] ("bad_host.test") rejected cleanly: HTTP 400 (invalid_hostname)
=== RUN   TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/PendingCustomDomain_CreateCheckAndNotPublic
=== RUN   TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/CustomDomainLimitAndDuplicateHost_Conflict409
=== RUN   TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/RBAC_CrossTenantAndRoleIsolation
=== RUN   TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/DeletePendingCustomDomain_204AndRemoved
=== RUN   TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/ActiveCustomDomain_PromotesPrimaryAndResolvesByHost
=== RUN   TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/AdminListDomains_And_FailedCustomDomainDeletedByAdmin
    15_custom_domains_test.go:492: Fail domain e2e-fail-f6e3df8a.fail.test transitioned cleanly to FAILED (last_error="mock verification failed")
=== RUN   TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/DeleteActiveCustomDomain_RevertsPrimaryToSubdomain
--- PASS: TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary (0.92s)
    --- PASS: TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/SetupSites_InitialSubdomainDomainsActivePrimary (0.02s)
    --- PASS: TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/BadHostnames_RejectedWith400 (0.01s)
    --- PASS: TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/PendingCustomDomain_CreateCheckAndNotPublic (0.01s)
    --- PASS: TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/CustomDomainLimitAndDuplicateHost_Conflict409 (0.00s)
    --- PASS: TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/RBAC_CrossTenantAndRoleIsolation (0.01s)
    --- PASS: TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/DeletePendingCustomDomain_204AndRemoved (0.01s)
    --- PASS: TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/ActiveCustomDomain_PromotesPrimaryAndResolvesByHost (0.02s)
    --- PASS: TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/AdminListDomains_And_FailedCustomDomainDeletedByAdmin (0.01s)
    --- PASS: TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary/DeleteActiveCustomDomain_RevertsPrimaryToSubdomain (0.02s)
PASS
ok  	github.com/nexusmart/core/tests/e2e	13.209s
node --no-warnings=MODULE_TYPELESS_PACKAGE_JSON tools/seed/seed.mjs
==> Seeding NexusMart backend via http://localhost:28080 (Mailpit: http://localhost:8025)
==> Seed complete:
  sites: created=0, reused=4 (root_domain=nexusmart.localhost)
{
  "gateway": "http://localhost:28080",
  "site_root_domain": "nexusmart.localhost",
  "accounts": {
    "admin": {
      "email": "admin@nexusmart.local",
      "role": "super_admin",
      "id": "206d75d9-fcdb-450f-b7da-9f81d2942398"
    },
    "buyer": {
      "email": "buyer@nexusmart.local",
      "password": "BuyerDemo123!",
      "role": "buyer",
      "id": "ce38fb6b-b57a-4f27-b054-50de5d493724",
      "coins": 42000
    },
    "seller": {
      "email": "seller@nexusmart.local",
      "password": "SellerDemo123!",
      "role": "seller",
      "id": "e98f6a39-3d59-4ca2-9538-0739b1d575a9",
      "shop_id": "62b5b10d-0cdf-46df-ad00-16450172b819",
      "shop_name": "Xưởng Dệt Bạch Đằng"
    }
  },
  "summary": {
    "users": {
      "created": 0,
      "reused": 5
    },
    "shops": {
      "created": 0,
      "reused": 4,
      "map": {
        "shop-bach-dang": {
          "id": "62b5b10d-0cdf-46df-ad00-16450172b819",
          "name": "Xưởng Dệt Bạch Đằng"
        },
        "shop-sai-gon": {
          "id": "b20516d7-ef2e-4805-9f6c-3f16f4962af0",
          "name": "Kho Basic Sài Gòn"
        },
        "shop-da-nang": {
          "id": "317e706a-3541-473a-a123-93848eec624b",
          "name": "Sợi Tự Nhiên Đà Nẵng"
        },
        "shop-ha-noi": {
          "id": "5a97169b-8561-487e-88fc-80b52c910878",
          "name": "Dệt Kim Hà Nội"
        }
      }
    },
    "categories": {
      "created": 0,
      "reused": 6,
      "map": {
        "ao-thun": "e33a811b-6f44-4fc2-8b3e-a37615e77ba1",
        "ao-polo": "2802a23d-58be-4605-8f56-e4102ebf297e",
        "quan-short": "da5c65fb-a3fc-4119-be97-48c75ff36bf6",
        "quan-jogger": "14dab1e8-ca1f-4e7c-b56b-65411e1d08ad",
        "do-lot": "fdd7dff5-f1a3-4ed4-add7-2fca530084a0",
        "tat-phu-kien": "0a5bae06-03ec-4e1b-b805-2db64c77ebd7"
      }
    },
    "products": {
      "created": 0,
      "reused": 18,
      "totalSkus": 159,
      "skusToppedUp": 0
    },
    "vouchers": {
      "created": 0,
      "reused": 11
    },
    "flashSales": {
      "windowsCreated": 0,
      "windowsReused": 3,
      "itemsCreated": 0,
      "itemsReused": 76
    },
    "sites": {
      "created": 0,
      "reused": 4,
      "map": {
        "bach-dang": {
          "shop_id": "62b5b10d-0cdf-46df-ad00-16450172b819",
          "shop_name": "Xưởng Dệt Bạch Đằng",
          "subdomain": "bach-dang",
          "primary_host": "bach-dang.nexusmart.localhost",
          "status": "PUBLISHED",
          "action": "reused",
          "font_family": "work-sans",
          "bg_color": "#F8F6F1",
          "accent_color": "#1D4ED8"
        },
        "sai-gon": {
          "shop_id": "b20516d7-ef2e-4805-9f6c-3f16f4962af0",
          "shop_name": "Kho Basic Sài Gòn",
          "subdomain": "sai-gon",
          "primary_host": "sai-gon.nexusmart.localhost",
          "status": "PUBLISHED",
          "action": "reused",
          "font_family": "be-vietnam-pro",
          "bg_color": "#14181C",
          "accent_color": "#D97706"
        },
        "da-nang": {
          "shop_id": "317e706a-3541-473a-a123-93848eec624b",
          "shop_name": "Sợi Tự Nhiên Đà Nẵng",
          "subdomain": "da-nang",
          "primary_host": "da-nang.nexusmart.localhost",
          "status": "PUBLISHED",
          "action": "reused",
          "font_family": "ibm-plex-sans",
          "bg_color": "#EEF2EA",
          "accent_color": "#3F6B4F",
          "custom_domain": "danang.pending.test",
          "custom_domain_status": "PENDING",
          "custom_domain_action": "reused"
        },
        "ha-noi": {
          "shop_id": "5a97169b-8561-487e-88fc-80b52c910878",
          "shop_name": "Dệt Kim Hà Nội",
          "subdomain": "ha-noi",
          "primary_host": "hanoi-knit.verified.test",
          "status": "PUBLISHED",
          "action": "reused",
          "font_family": "plus-jakarta-sans",
          "bg_color": "#2B1A1E",
          "accent_color": "#E9B949",
          "custom_domain": "hanoi-knit.verified.test",
          "custom_domain_status": "ACTIVE",
          "custom_domain_action": "reused"
        }
      }
    }
  }
}
docker compose up -d --build --no-deps --wait web
 Image nexus_ecom-web Building 
#1 [internal] load local bake definitions
#1 reading from stdin 514B done
#1 DONE 0.0s

#2 [internal] load build definition from Dockerfile
#2 transferring dockerfile: 1.08kB done
#2 DONE 0.0s

#3 [internal] load metadata for docker.io/library/node:22-alpine
#3 DONE 0.0s

#4 [internal] load .dockerignore
#4 transferring context: 191B done
#4 DONE 0.0s

#5 [deps 1/4] FROM docker.io/library/node:22-alpine
#5 DONE 0.0s

#6 [internal] load build context
#6 transferring context: 28.29kB 0.0s done
#6 DONE 0.0s

#7 [deps 2/4] WORKDIR /app
#7 CACHED

#8 [builder 3/5] COPY --from=deps /app/node_modules ./node_modules
#8 CACHED

#9 [builder 5/5] RUN NEXT_DIST_DIR=.next npm run build
#9 CACHED

#10 [runner 3/5] COPY --from=builder --chown=node:node /app/public ./public
#10 CACHED

#11 [deps 3/4] COPY package.json package-lock.json ./
#11 CACHED

#12 [builder 4/5] COPY . .
#12 CACHED

#13 [runner 4/5] COPY --from=builder --chown=node:node /app/.next/standalone ./
#13 CACHED

#14 [deps 4/4] RUN npm ci --no-audit --no-fund
#14 CACHED

#15 [runner 5/5] COPY --from=builder --chown=node:node /app/.next/static ./.next/static
#15 CACHED

#16 exporting to image
#16 exporting layers done
#16 writing image sha256:d59d796944feb5c293ce9e69cdc9197f1bd12f0f408dd5c70667ee20a7c34ead done
#16 naming to docker.io/library/nexus_ecom-web done
#16 DONE 0.0s

#17 resolving provenance for metadata file
#17 DONE 0.0s
 Image nexus_ecom-web Built 
 Container nexus_ecom-web-1 Running 
 Container nexus_ecom-web-1 Waiting 
 Container nexus_ecom-web-1 Healthy 
docker compose --profile e2e-web run --rm e2e-web
 Container nexus_ecom-e2e-web-run-72bb10e51e4f Creating 
 Container nexus_ecom-e2e-web-run-72bb10e51e4f Created 
▶ 01. Root host = NexusMart platform only (intro, site directory, redirects, RBAC, SEO)
  ✔ public directory REST lists only PUBLISHED sites with the contract fields and capped page size (109.731822ms)
  ✔ root / (both SITE_ROOT_DOMAIN and localhost) is the NexusMart intro: 200, real site directory, no products or prices (44.622808ms)
  ✔ platform pages stay on root; every old marketplace buyer URL is 404 (138.153899ms)
  ✔ /gian-hang/<slug> and /san-pham/<slug> 308 to the shop's published site; unknown slugs 404 (35.612338ms)
  ✔ buyer APIs (cart, checkout, orders, payments, vouchers) are 404 on the root host, anonymous or logged in (183.5726ms)
  ✔ RBAC on root: anonymous redirected to /dang-nhap; buyer, seller, admin only reach their own area (888.79394ms)
  ✔ root robots.txt / sitemap.xml list the intro and published sites, never product or removed buyer URLs (9.072214ms)
✔ 01. Root host = NexusMart platform only (intro, site directory, redirects, RBAC, SEO) (1443.767478ms)
▶ 02. Site A full buyer commerce & auth lifecycle (OTP, COD + SHOP voucher, VietQR, idempotency, refresh rotation)
  ✔ public storefront pages serve 200 on site A; unauthenticated protected pages redirect on the SAME site host (125.57411ms)
  ✔ registers buyer + Mailpit OTP -> logs in on site A with HttpOnly host-only cookies and no token leak (551.612869ms)
  ✔ adds site A SKU to cart, applies site A SHOP voucher, places COD order -> STOREFRONT for shop A (125.363646ms)
  ✔ completes VietQR mock + HMAC webhook flow on site A -> payment SUCCEEDED & order PAID / CONFIRMED (899.2226ms)
  ✔ checkout idempotency: resending same Idempotency-Key returns same single order without duplicates (59.17461ms)
  ✔ refresh rotation is independent per host and logout revokes only that host's session (69.80929ms)
✔ 02. Site A full buyer commerce & auth lifecycle (OTP, COD + SHOP voucher, VietQR, idempotency, refresh rotation) (1873.710363ms)
▶ 03. Cross-site isolation, IDOR, 401 guards, CSRF across hosts, body tampering & blocked routes
  ✔ unknown, foreign, reserved, and multi-level hosts return 404 on pages and APIs (48.186842ms)
  ✔ direct /s/... is 404 on every host; /seller, /admin, /api/seller/*, /api/admin/* are 404 on site hosts (55.013947ms)
  ✔ all 4 seeded sites render distinct themes, isolate products/vouchers, and return 404 on cross-shop PDP (74.082206ms)
  ✔ cookie session of site A does NOT authenticate on site B (host-only isolation) (70.415336ms)
  ✔ site A rejects site B SKUs and site B / platform vouchers; carts stay isolated per site (134.796759ms)
  ✔ same account logged into both site A and site B: orders & payments of A are invisible (404) on B and vice versa (176.722319ms)
  ✔ IDOR between two different buyers on the same site A returns 403/404 (92.593225ms)
  ✔ unauthenticated writes and private reads on site A return 401 without leaking internals (33.739505ms)
  ✔ CSRF rejects Origin of another site, root host, evil origin, missing/null Origin, Fetch-Metadata & spoofed proxy headers with 403 (26.104132ms)
  ✔ checkout body tampering (MARKETPLACE / foreign storefront_shop_id / platform discounts / foreign SKU items) is ignored or rejected (92.302385ms)
✔ 03. Cross-site isolation, IDOR, 401 guards, CSRF across hosts, body tampering & blocked routes (866.620134ms)
▶ 04. Seller/Admin site lifecycle on root, theme security, suspend (pages + APIs) & per-host SEO
  ✔ seller draft/publish/rollback lifecycle, cross-seller RBAC, theme validation, empty-section noindex, and admin suspend/unsuspend (pages + APIs) (6572.307086ms)
  ✔ SEO per site host: product canonical points to OWN site URL, substantive sites are indexable, host-scoped robots.txt & sitemap.xml (59.516194ms)
✔ 04. Seller/Admin site lifecycle on root, theme security, suspend (pages + APIs) & per-host SEO (6647.705169ms)
▶ 05. Phase 5 custom domains (Host routing, 308 primary redirect, cookie isolation, SEO & BFF domains)
  ✔ a) ACTIVE custom domain renders Ha Noi site, scopes vouchers, and blocks seller/admin/internal routes with 404 (102.555522ms)
  ✔ b) Subdomain page requests 308 to ACTIVE custom primary host preserving scheme, port, path & query (4.853673ms)
  ✔ c) Subdomain /api/cart (anon -> 401, authed -> 200) and /api/auth/me are NEVER 308-redirected (78.281027ms)
  ✔ d) PENDING custom domain (danang.pending.test) returns 404 on pages & APIs while da-nang subdomain stays 200 (30.093627ms)
  ✔ e) Unknown and malformed custom hosts return 404 for / and /api/cart (14.304771ms)
  ✔ f) Host-only cookies do not leak between custom host and subdomain; cross-host CSRF rejected with 403 BAD_ORIGIN (504.928148ms)
  ✔ g) Canonical, robots.txt, sitemap.xml and root legacy 308 redirects all use custom host hanoi-knit.verified.test (46.009339ms)
  ✔ h) Seller and Admin domain management BFF routes on ROOT_HOST enforce RBAC, CSRF, and list ACTIVE custom domain (80.68818ms)
✔ 05. Phase 5 custom domains (Host routing, 308 primary redirect, cookie isolation, SEO & BFF domains) (898.912571ms)
ℹ tests 33
ℹ suites 5
ℹ pass 33
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 12053.263528
docker compose up -d --build --no-deps --wait web
 Image nexus_ecom-web Building 
#1 [internal] load local bake definitions
#1 reading from stdin 514B done
#1 DONE 0.0s

#2 [internal] load build definition from Dockerfile
#2 transferring dockerfile: 1.08kB done
#2 DONE 0.0s

#3 [internal] load metadata for docker.io/library/node:22-alpine
#3 DONE 0.0s

#4 [internal] load .dockerignore
#4 transferring context: 191B done
#4 DONE 0.0s

#5 [deps 1/4] FROM docker.io/library/node:22-alpine
#5 DONE 0.0s

#6 [internal] load build context
#6 transferring context: 28.29kB done
#6 DONE 0.0s

#7 [runner 4/5] COPY --from=builder --chown=node:node /app/.next/standalone ./
#7 CACHED

#8 [builder 5/5] RUN NEXT_DIST_DIR=.next npm run build
#8 CACHED

#9 [deps 2/4] WORKDIR /app
#9 CACHED

#10 [builder 4/5] COPY . .
#10 CACHED

#11 [runner 3/5] COPY --from=builder --chown=node:node /app/public ./public
#11 CACHED

#12 [builder 3/5] COPY --from=deps /app/node_modules ./node_modules
#12 CACHED

#13 [deps 4/4] RUN npm ci --no-audit --no-fund
#13 CACHED

#14 [deps 3/4] COPY package.json package-lock.json ./
#14 CACHED

#15 [runner 5/5] COPY --from=builder --chown=node:node /app/.next/static ./.next/static
#15 CACHED

#16 exporting to image
#16 exporting layers done
#16 writing image sha256:d59d796944feb5c293ce9e69cdc9197f1bd12f0f408dd5c70667ee20a7c34ead done
#16 naming to docker.io/library/nexus_ecom-web done
#16 DONE 0.0s

#17 resolving provenance for metadata file
#17 DONE 0.0s
 Image nexus_ecom-web Built 
 Container nexus_ecom-web-1 Running 
 Container nexus_ecom-web-1 Waiting 
 Container nexus_ecom-web-1 Healthy 
docker compose --profile e2e-web run --rm e2e-web
 Container nexus_ecom-e2e-web-run-b2654f1281d1 Creating 
 Container nexus_ecom-e2e-web-run-b2654f1281d1 Created 
▶ 01. Root host = NexusMart platform only (intro, site directory, redirects, RBAC, SEO)
  ✔ public directory REST lists only PUBLISHED sites with the contract fields and capped page size (46.698288ms)
  ✔ root / (both SITE_ROOT_DOMAIN and localhost) is the NexusMart intro: 200, real site directory, no products or prices (50.31237ms)
  ✔ platform pages stay on root; every old marketplace buyer URL is 404 (112.506505ms)
  ✔ /gian-hang/<slug> and /san-pham/<slug> 308 to the shop's published site; unknown slugs 404 (28.573367ms)
  ✔ buyer APIs (cart, checkout, orders, payments, vouchers) are 404 on the root host, anonymous or logged in (128.271395ms)
  ✔ RBAC on root: anonymous redirected to /dang-nhap; buyer, seller, admin only reach their own area (777.890654ms)
  ✔ root robots.txt / sitemap.xml list the intro and published sites, never product or removed buyer URLs (5.475625ms)
✔ 01. Root host = NexusMart platform only (intro, site directory, redirects, RBAC, SEO) (1177.873847ms)
▶ 02. Site A full buyer commerce & auth lifecycle (OTP, COD + SHOP voucher, VietQR, idempotency, refresh rotation)
  ✔ public storefront pages serve 200 on site A; unauthenticated protected pages redirect on the SAME site host (111.447865ms)
  ✔ registers buyer + Mailpit OTP -> logs in on site A with HttpOnly host-only cookies and no token leak (129.293321ms)
  ✔ adds site A SKU to cart, applies site A SHOP voucher, places COD order -> STOREFRONT for shop A (121.845669ms)
  ✔ completes VietQR mock + HMAC webhook flow on site A -> payment SUCCEEDED & order PAID / CONFIRMED (480.899656ms)
  ✔ checkout idempotency: resending same Idempotency-Key returns same single order without duplicates (63.170504ms)
  ✔ refresh rotation is independent per host and logout revokes only that host's session (77.591862ms)
✔ 02. Site A full buyer commerce & auth lifecycle (OTP, COD + SHOP voucher, VietQR, idempotency, refresh rotation) (1022.302495ms)
▶ 03. Cross-site isolation, IDOR, 401 guards, CSRF across hosts, body tampering & blocked routes
  ✔ unknown, foreign, reserved, and multi-level hosts return 404 on pages and APIs (33.271686ms)
  ✔ direct /s/... is 404 on every host; /seller, /admin, /api/seller/*, /api/admin/* are 404 on site hosts (51.066999ms)
  ✔ all 4 seeded sites render distinct themes, isolate products/vouchers, and return 404 on cross-shop PDP (64.344053ms)
  ✔ cookie session of site A does NOT authenticate on site B (host-only isolation) (68.326672ms)
  ✔ site A rejects site B SKUs and site B / platform vouchers; carts stay isolated per site (127.995851ms)
  ✔ same account logged into both site A and site B: orders & payments of A are invisible (404) on B and vice versa (159.841938ms)
  ✔ IDOR between two different buyers on the same site A returns 403/404 (492.12187ms)
  ✔ unauthenticated writes and private reads on site A return 401 without leaking internals (30.982685ms)
  ✔ CSRF rejects Origin of another site, root host, evil origin, missing/null Origin, Fetch-Metadata & spoofed proxy headers with 403 (25.286613ms)
  ✔ checkout body tampering (MARKETPLACE / foreign storefront_shop_id / platform discounts / foreign SKU items) is ignored or rejected (87.818099ms)
✔ 03. Cross-site isolation, IDOR, 401 guards, CSRF across hosts, body tampering & blocked routes (1199.01769ms)
▶ 04. Seller/Admin site lifecycle on root, theme security, suspend (pages + APIs) & per-host SEO
  ✔ seller draft/publish/rollback lifecycle, cross-seller RBAC, theme validation, empty-section noindex, and admin suspend/unsuspend (pages + APIs) (6553.186887ms)
  ✔ SEO per site host: product canonical points to OWN site URL, substantive sites are indexable, host-scoped robots.txt & sitemap.xml (65.090782ms)
✔ 04. Seller/Admin site lifecycle on root, theme security, suspend (pages + APIs) & per-host SEO (6638.402508ms)
▶ 05. Phase 5 custom domains (Host routing, 308 primary redirect, cookie isolation, SEO & BFF domains)
  ✔ a) ACTIVE custom domain renders Ha Noi site, scopes vouchers, and blocks seller/admin/internal routes with 404 (98.408126ms)
  ✔ b) Subdomain page requests 308 to ACTIVE custom primary host preserving scheme, port, path & query (6.012452ms)
  ✔ c) Subdomain /api/cart (anon -> 401, authed -> 200) and /api/auth/me are NEVER 308-redirected (89.502407ms)
  ✔ d) PENDING custom domain (danang.pending.test) returns 404 on pages & APIs while da-nang subdomain stays 200 (35.368126ms)
  ✔ e) Unknown and malformed custom hosts return 404 for / and /api/cart (14.385598ms)
  ✔ f) Host-only cookies do not leak between custom host and subdomain; cross-host CSRF rejected with 403 BAD_ORIGIN (510.753609ms)
  ✔ g) Canonical, robots.txt, sitemap.xml and root legacy 308 redirects all use custom host hanoi-knit.verified.test (38.834146ms)
  ✔ h) Seller and Admin domain management BFF routes on ROOT_HOST enforce RBAC, CSRF, and list ACTIVE custom domain (79.677816ms)
✔ 05. Phase 5 custom domains (Host routing, 308 primary redirect, cookie isolation, SEO & BFF domains) (912.383162ms)
ℹ tests 33
ℹ suites 5
ℹ pass 33
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 11271.22404
EXIT=0
```

#### 4. Kiểm tra CI (`workflow_dispatch` duy nhất) và cổng public (`28080`, `3300`)
```text
$ grep -nE '^(on:|  (push|pull_request|schedule|workflow_dispatch):)' .github/workflows/*.yml
5:on:
6:  workflow_dispatch:

$ docker compose ps --format "table {{.Name}}\t{{.Status}}\t{{.Ports}}"
NAME                          STATUS                   PORTS
nexus_ecom-cart-1             Up 19 hours (healthy)    50053/tcp, 127.0.0.1:28083->8083/tcp
nexus_ecom-gateway-1          Up 2 hours (healthy)     0.0.0.0:28080->8080/tcp
nexus_ecom-mailpit-1          Up 20 hours (healthy)    127.0.0.1:1025->1025/tcp, 127.0.0.1:8025->8025/tcp, 1110/tcp
nexus_ecom-nats-1             Up 20 hours (healthy)    127.0.0.1:4222->4222/tcp, 127.0.0.1:8222->8222/tcp, 6222/tcp
nexus_ecom-notification-1     Up 5 hours (healthy)     127.0.0.1:8088->8088/tcp, 50058/tcp
nexus_ecom-order-1            Up 5 hours (healthy)     50055/tcp, 127.0.0.1:28085->8085/tcp
nexus_ecom-otel-collector-1   Up 20 hours              127.0.0.1:4317->4317/tcp, 127.0.0.1:8889->8889/tcp, 55678-55679/tcp, 127.0.0.1:24318->4318/tcp
nexus_ecom-payment-1          Up 20 hours (healthy)    127.0.0.1:8086->8086/tcp, 50056/tcp
nexus_ecom-postgres-1         Up 6 minutes (healthy)   127.0.0.1:5432->5432/tcp
nexus_ecom-product-1          Up 19 hours (healthy)    50052/tcp, 127.0.0.1:28082->8082/tcp
nexus_ecom-promotion-1        Up 19 hours (healthy)    127.0.0.1:8084->8084/tcp, 50054/tcp
nexus_ecom-redis-1            Up 20 hours (healthy)    127.0.0.1:26379->6379/tcp
nexus_ecom-shipping-1         Up 19 hours (healthy)    127.0.0.1:8087->8087/tcp, 50057/tcp
nexus_ecom-user-1             Up 2 hours (healthy)     127.0.0.1:8081->8081/tcp, 50051/tcp
nexus_ecom-web-1              Up 2 hours (healthy)     0.0.0.0:3300->3000/tcp
```

---

## Lỗi gửi 5A/5B

Không có lỗi chặn gate (blocker) ở 5A hoặc 5B — toàn bộ gate cuối (`make lint`, `make test`, `make e2e && make seed && make e2e-web && make e2e-web`) đều đạt **EXIT 0**. Ghi nhận 2 điểm lệch nhỏ ở **5B** để đối chiếu với hợp đồng (5C không sửa `services/` hay `web/`):

1. **[5B · Nhỏ] Từ chối IDN Unicode ở web BFF/UI trước khi chuyển xuống backend (`web/src/lib/site/domains.ts:183-190`, `web/src/app/api/seller/site/domains/route.ts:41`):**
   - **Kịch bản:** Người bán nhập tên miền có dấu / IDN (ví dụ `cửa-hàng.vn`) qua `/seller/site/ten-mien` hoặc `POST /api/seller/site/domains`. BFF gọi `validateCustomHostname` và chặn ngay bằng `400 invalid_hostname` (`reason: "non_ascii"`), trong khi backend 5A (`services/user/internal/site/validate.go:258`) hỗ trợ chuyển IDN sang punycode bằng `golang.org/x/net/idna`. Hiện trên web người bán phải tự nhập dạng punycode (`xn--...`).
2. **[5B · Nhỏ] IP literal và `localhost` trả `200` (host `root`) thay vì `404` (`web/src/lib/site/host.ts:224-231`):**
   - **Kịch bản:** Gửi `GET /` với `Host: 127.0.0.1:3300`, `Host: localhost:3300`, hoặc IP bất kỳ như `Host: 203.0.113.10:3300` tới container `web` đều vào nhánh `kind: "root"` và trả `200 OK` (trang giới thiệu nền tảng; các buyer API `/api/cart`, ... vẫn `404`; **không** gọi backend `/sites/by-host`). Mục Web của hợp đồng ghi: `IP, localhost, host có ký tự lạ → 404 không gọi backend`.
   - **Ngữ cảnh:** Nhánh `localhost` và `127.0.0.1` đang phục vụ kiểm thử Giai đoạn 4 (`tests/e2e-web/01_root_platform.test.mjs` yêu cầu `localhost` trả `200`) và healthcheck Docker (`wget http://127.0.0.1:3000/`). Nếu muốn bám sát hợp đồng hơn cho môi trường thật, 5B có thể giữ riêng loopback (`127.0.0.1`, `[::1]`, `localhost`, `web`) khi dev/healthcheck và trả `404` cho các địa chỉ IP công cộng khác.

*(5A: không ghi nhận lỗi nào trong quá trình kiểm thử tích hợp và E2E.)*

---

## Phần chưa làm / Ngoài phạm vi

- **Tài khoản Cloudflare thật (`CF_MODE=live` với mạng thật):** ngoài phạm vi hợp đồng; E2E chạy với `CF_MODE=mock`, còn `LiveClient` đã được kiểm thử đơn vị bằng `httptest` ở 5A.
- **Caddy / On-Demand TLS, SSO đa tên miền, mua tên miền hộ:** ngoài phạm vi Giai đoạn 5 theo hợp đồng.
- **Kiểm thử E2E hộp đen email đơn hàng qua Mailpit cho tên miền custom:** chưa thêm ca đặt đơn thật trên `hanoi-knit.verified.test` để đọc nội dung email trong Mailpit; tuy nhiên đã xác nhận thực tế `GET /api/v1/sites/by-shop/5a97169b-8561-487e-88fc-80b52c910878` trả `primary_host = "hanoi-knit.verified.test"` và unit test `TestStorefrontOrderLinksToCustomPrimaryHost` trong `services/notification/internal/service/sitelink_test.go` đạt xanh.

---

## Các lệch khỏi hợp đồng

1. **`classifyHost` đối với `IP` và `localhost` (`web/src/lib/site/host.ts:224-231`):** Trả `kind: "root"` (`200` trang giới thiệu nền tảng, không gọi `/sites/by-host`) thay vì `404` như câu chữ hợp đồng, để bảo toàn tương thích với healthcheck Docker (`127.0.0.1:3000`) và bộ test nền tảng Giai đoạn 4 (`localhost`). Các host có ký tự lạ, wildcard, `.local`, `.internal` vẫn trả `404` không gọi backend đúng hợp đồng.
2. **Xử lý IDN giữa 5A và 5B (`web/src/lib/site/domains.ts:183-190` vs `services/user/internal/site/validate.go:258`):** Backend chuyển IDN Unicode sang punycode (`golang.org/x/net/idna`), còn lớp kiểm tra web từ chối ký tự ngoài ASCII trước khi gửi lên gateway (vẫn nhận punycode `xn--...`).
3. **Phương thức `HEAD` trên subdomain đã có custom `ACTIVE` (`web/src/lib/site/host.ts:399`):** Chỉ chuyển hướng `308` cho `GET` (khớp đúng câu chữ hợp đồng `request trang (GET, không phải /api)`), nên `HEAD /` trên `ha-noi.nexusmart.localhost:3300` trả `200`.

## Đề xuất đổi hợp đồng

- Nếu tiếp tục giữ `localhost` và `127.0.0.1` là host `root` để phục vụ dev và healthcheck Docker (`docker-compose.yml:474`), đề xuất cập nhật mục Web trong `.agents/briefs/sites-5-contract.md` phân tách rõ: `localhost`/loopback phục vụ host `root` (không tra `/sites/by-host`), còn IP ngoài loopback và host chứa ký tự lạ trả `404` không gọi backend.
