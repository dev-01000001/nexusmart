
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
