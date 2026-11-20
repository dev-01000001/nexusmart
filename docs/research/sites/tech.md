# Kiến trúc kỹ thuật đa tenant cho Site Gian hàng (NexusMart)

> **Bối cảnh**: Mở rộng NexusMart (`web/` Next.js `15.5.29`, backend Go + Rust `payment`, PostgreSQL 16, Redis 7, NATS — `đã kiểm chứng`: `docs/CORE_REPORT.md`, `web/DESIGN_REPORT.md`) để mỗi gian hàng có site riêng song song sàn chung. **Quy ước**: `đã kiểm chứng` = nguồn chính thức/mã nguồn; `suy luận` = phân tích cho NexusMart.

## 0. Phản biện đề xuất sơ bộ của manager

| Đề xuất | Kết luận | Phản biện kỹ thuật |
| --- | --- | --- |
| 1. App Next.js, `middleware` rewrite `(site)/[shop]/...` | Sửa path (`suy luận`) | Route group `(site)` bị bỏ khỏi URL (`đã kiểm chứng`: [Route Groups](https://nextjs.org/docs/15/app/api-reference/file-conventions/route-groups)), dễ đụng `/danh-muc`, `/seller`, `/admin`. Rewrite sang `/s/[host]/...`, chặn `/s/*` ở domain chính. |
| 2. Service Go `storefront` giữ cấu hình site | Đồng ý (`suy luận`) | Đúng chuẩn DB riêng (`storefront_db`), gRPC `:50059` / HTTP `:8089`. |
| 3. Theme dữ liệu (token + section JSON), cấm code | Đồng ý (`suy luận`) | Tránh gánh nặng sandbox/XSS kiểu Liquid, dùng thẳng `web/src/styles/tokens.css`. |
| 4. Checkout dùng lõi `order`/`payment` | Đồng ý (`suy luận`) | Giữ nguyên Saga, sổ cái ghi kép VND, webhook HMAC, đối soát. |
| 5. Một tài khoản mua, sàn song song site riêng | Thêm SSO & SEO (`suy luận`) | Cookie không chia sẻ khác domain -> cần Redirect SSO PKCE và `rel="canonical"`. |
| 6. Tên miền riêng: DNS + Caddy On-Demand TLS | Cần gia cố (`suy luận`) | Cần `ask` nội bộ chống DoS, wildcard cert `*.nexusmart.vn`, shared storage khi >1 node. |

## 1. Next.js 15 Multi-Tenant: Middleware, ISR & `next/image`

- **Middleware & Rewrite**: Theo [Multi-Tenant](https://nextjs.org/docs/15/app/guides/multi-tenant), [Middleware](https://nextjs.org/docs/15/app/api-reference/file-conventions/middleware) và [Vercel Platforms Starter Kit](https://github.com/vercel/platforms/) (`đã kiểm chứng` — bản mới dùng `proxy.ts` trên Next.js 16 nhưng giữ nguyên rewrite theo `Host`), `middleware.ts` đọc `Host`, bỏ qua `nexusmart.vn`, rồi `NextResponse.rewrite` sang `/s/${host}${pathname}`.
- **Không fetch trong Middleware**: `fetch` ở middleware không được cache (`đã kiểm chứng`: [Caching](https://nextjs.org/docs/15/app/guides/caching)). Middleware chỉ chuẩn hóa host và rewrite; Server Component mới gọi `storefront` để hưởng Request Memoization và Data Cache (`suy luận`).
- **ISR & Đa pod**: Cache tách theo path rewrite và xóa tức thì bằng `revalidateTag("site:<id>")` (`đã kiểm chứng`: [ISR](https://nextjs.org/docs/15/app/guides/incremental-static-regeneration), [revalidateTag](https://nextjs.org/docs/15/app/api-reference/functions/revalidateTag)). Tự host nhiều pod bắt buộc bật Redis `cacheHandler` kèm `cacheMaxMemorySize: 0` (`đã kiểm chứng`: [Cache Handler](https://nextjs.org/docs/15/app/api-reference/config/next-config-js/incrementalCacheHandlerPath)). Server Actions so khớp `Origin` với `Host`/`X-Forwarded-Host` (`đã kiểm chứng`: [serverActions](https://nextjs.org/docs/15/app/api-reference/config/next-config-js/serverActions)), nên proxy giữ đúng `X-Forwarded-Host` và giao dịch đi qua REST Gateway (`suy luận`).
- **`next/image`**: `web/next.config.ts` đang đặt `unoptimized: true` (`đã kiểm chứng`). Production cấm `remotePatterns` wildcard cho URL seller nhập (SSRF/tốn CPU). Upload media vào CDN sàn (`cdn.nexusmart.vn/shops/<shop_id>/...`), khóa `remotePatterns` 1 host hoặc custom `loader` (`đã kiểm chứng`: [Image](https://nextjs.org/docs/15/app/api-reference/components/image), [images](https://nextjs.org/docs/15/app/api-reference/config/next-config-js/images)).

## 2. Tên miền riêng, DNS & TLS tự động

- **Xác minh & Apex**: Shop gắn `TXT _nexusmart-verify.<domain>` xác minh sở hữu rồi trỏ `CNAME -> shops.nexusmart.vn`. DNS cấm `CNAME` ở apex (`shopabc.vn`) (`đã kiểm chứng`: [CNAME Flattening](https://developers.cloudflare.com/dns/cname-flattening/)), nên hỗ trợ CNAME flattening/ALIAS, `A`/`AAAA` về IP tĩnh Caddy/LB, hoặc redirect apex -> `www` (`suy luận`).
- **Caddy On-Demand TLS & `ask`**: Cấp/gia hạn chứng chỉ ở handshake đầu khi `on_demand_tls { ask <internal-url> }` trả `200` (`đã kiểm chứng`: [On-Demand TLS](https://caddyserver.com/on-demand-tls), [Global Options](https://caddyserver.com/docs/caddyfile/options)). Caddy đã bỏ `rate_limit`/`burst` cũ (`đã kiểm chứng`: [commit d7564d6](https://github.com/caddyserver/caddy/commit/d7564d632fbed209e81978c5c2c529a7bf1836f7)) và cần `storage` chia sẻ (Redis/Postgres/S3) khi chạy cụm.
- **Let's Encrypt**: Token bucket: **300 đơn/3 giờ/tài khoản**, **50 chứng chỉ/registered domain/7 ngày**, **5 chứng chỉ trùng định danh/7 ngày** (`đã kiểm chứng`: [Rate Limits](https://letsencrypt.org/docs/rate-limits/)). **Hệ quả** (`suy luận`): `*.nexusmart.vn` **bắt buộc dùng 1 wildcard cert** (DNS-01) tránh trần 50/tuần; chỉ bật On-Demand TLS cho custom domain.

| Tiêu chí | Caddy On-Demand TLS | Cloudflare for SaaS |
| --- | --- | --- |
| Chi phí | $0 cert, tốn LB ([Auto HTTPS](https://caddyserver.com/docs/automatic-https), `đã kiểm chứng`) | **100 hostname free**, thêm **$0.10/tháng**, trần **50.000** ([Plans](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/plans/), `đã kiểm chứng`) |
| Apex (`A`) | Trỏ IP tĩnh Caddy/LB (`suy luận`) | **Apex Proxying/BYOIP chỉ ở Enterprise** ([Apex](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/start/advanced-settings/apex-proxying/), `đã kiểm chứng`) |
| Xác minh/Edge | Tự viết `ask` + DNS poller, tự lo WAF (`suy luận`) | Sẵn TXT/Delegated DCV ([Validation](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/domain-support/hostname-validation/)), CDN, WAF, DDoS (`đã kiểm chứng`) |

## 3. Cô lập Tenant & Phiên đa tên miền

- **Cô lập 3 lớp**: (1) `pkg/common/auth/auth.go` xác thực JWT HS256 gắn `claims.ShopID` vào context (`đã kiểm chứng`) — RPC ghi lấy `shop_id` duy nhất từ token, bỏ qua payload (`suy luận`); (2) mọi bảng tenant có `shop_id UUID NOT NULL`; (3) PostgreSQL 16 bật `ENABLE` + `FORCE ROW LEVEL SECURITY`, role app không `BYPASSRLS` (`đã kiểm chứng`: [PG16 RLS](https://www.postgresql.org/docs/16/ddl-rowsecurity.html)). Với `pgxpool`, bắt buộc `SET LOCAL app.current_shop_id = '...'` trong transaction (`BEGIN...COMMIT`), cấm `SET` session tránh rò qua pool (`đã kiểm chứng`: [PG16 SET](https://www.postgresql.org/docs/16/sql-set.html)). Đọc công khai dùng role `storefront_reader`.
- **Cookie & Redirect SSO**: Cookie cô lập theo domain, third-party cookie iframe bị chặn (`đã kiểm chứng`: [MDN Cookies](https://developer.mozilla.org/en-US/docs/Web/HTTP/Guides/Cookies)). Dùng **OAuth 2.0 Auth Code + PKCE** (`đã kiểm chứng`: [RFC 7636](https://www.rfc-editor.org/info/rfc7636/), [RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.pdf)) (`suy luận`): `shopabc.vn` chuyển sang `auth.nexusmart.vn/sso/authorize` (`state`, `code_challenge`); `user` kiểm tra `redirect_uri` khớp domain `ACTIVE`, trả `one-time code` (Redis TTL 60s) về `shopabc.vn/api/auth/callback` để BFF đổi token và đặt cookie **Host-Only** (`HttpOnly; Secure; SameSite=Lax`). Logout thu hồi `user:revoked_jti:<jti>` fail-closed ở Gateway (`đã kiểm chứng`: `docs/CORE_REPORT.md`) vô hiệu toàn cục; giỏ hàng đồng bộ qua Redis `cart`.

## 4. Theme JSON Schema & Bản nháp / Xem trước / Phiên bản

| Hướng triển khai | Cơ chế | Đánh giá cho NexusMart |
| --- | --- | --- |
| **JSON Section + Token** *(Chọn)* | Registry RSC + JSONB + `tokens.css` (`suy luận`) | Chống XSS, nhẹ, khóa WCAG, hợp Ponytail. |
| **Shopify Liquid (OS 2.0)** | JSON template (≤25 section, ≤50 block) + Liquid sandbox ([Shopify](https://shopify.dev/docs/storefronts/themes/architecture/templates/json-templates), `đã kiểm chứng`) | Chi phí dựng runtime/sandbox quá lớn (`suy luận`). |
| **Puck (`@puckeditor/core`)** | Editor React MIT tự host, `<Puck>` xuất JSON, `<Render>` vẽ ([Puck](https://puckeditor.com/docs/getting-started), `đã kiểm chứng`) | Hợp nhúng kéo-thả Pha 2, tự chủ JSON (`suy luận`). |
| **Builder.io** | Visual CMS SaaS, iframe editor + Content API ([Builder.io](https://www.builder.io/c/docs/how-builder-works-technical), `đã kiểm chứng`) | Vendor lock-in, tốn phí traffic (`suy luận`). |
| **Medusa / Saleor / Vendure** | Headless commerce + Next.js starter ([Medusa](https://docs.medusajs.com/resources/nextjs-starter), [Saleor](https://docs.saleor.io/quickstart/storefront), [Vendure](https://docs.vendure.io/current/framework/storefront/storefront-starters), `đã kiểm chứng`) | Thiếu visual theme builder đa tenant tích hợp (`suy luận`). |

- **Bản nháp, Xem trước, Phiên bản**: Seller sửa trên `pages`/`sections`; khi `PublishSite`, Go validate JSON Schema, đóng băng snapshot vào `versions`, cập nhật `sites.published_version_id`, phát NATS `storefront.site.published` -> `revalidateTag` (`suy luận`). Xem trước dùng Next.js 15 **Draft Mode** (`đã kiểm chứng`: [Draft Mode](https://nextjs.org/docs/15/app/guides/draft-mode)): `/api/preview` xác thực token 15 phút, bật `__prerender_bypass`, gắn `X-Robots-Tag: noindex, nofollow` và `Cache-Control: private, no-store`. Rollback đổi `published_version_id`.

## 5. CSP/XSS, SEO & Chống lạm dụng

- **CSP & XSS**: Cấm seller nhập HTML/JS/CSS và cấm `dangerouslySetInnerHTML`. Dù React 19 chặn `javascript:` ở `href`/`src`, OWASP yêu cầu validate URL phía server (`https:` hoặc `/`) và sanitize Markdown bằng `bluemonday` (`đã kiểm chứng`: [OWASP XSS](https://cheatsheetseries.owasp.org/cheatsheets/Cross_Site_Scripting_Prevention_Cheat_Sheet.html)). Kiểm tra CSS token theo regex hex/whitelist chống CSS Injection. **Lưu ý CSP `nonce`**: Next.js 15 xác nhận `nonce` ép **Dynamic Rendering, mất ISR** (`đã kiểm chứng`: [Next.js CSP](https://nextjs.org/docs/15/app/guides/content-security-policy)), nên storefront công khai dùng CSP tĩnh chặt (`default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'`, mở `frame-ancestors` cho Seller Center lúc preview) (`suy luận`).
- **SEO**: Google xếp **301 > `rel="canonical"` > Sitemap**, trần **50.000 URL / 50 MB** dùng URL tuyệt đối đúng host (`đã kiểm chứng`: [Canonical](https://developers.google.com/search/docs/crawling-indexing/consolidate-duplicate-urls), [Sitemap](https://developers.google.com/search/docs/crawling-indexing/sitemaps/build-sitemap)). Áp dụng (`suy luận`): `301` subdomain -> custom domain chính; `/sitemap.xml` và `/robots.txt` động theo `Host`; PDP dùng `canonical_mode` (`MARKETPLACE` mặc định khi chung mô tả sàn, `SELF` khi nội dung riêng).
- **Chống lạm dụng** (`suy luận`): `ask` có Redis **negative cache 60s** chống TLS DoS; bật `strict_sni_host on` trên Caddy (`đã kiểm chứng`: [Global Options](https://caddyserver.com/docs/caddyfile/options)) chống Host Spoofing; khóa reserved slug (`www`, `api`, `auth`, `admin`, `seller`, `pay`, `cdn`); rate limit Redis 2 tầng (IP + `site_id`); trần ≤25 section/trang, ≤20 trang/site, ≤256 KB/payload, ≤3 custom domain/shop; cờ `SUSPENDED` hạ site <5s.

## 6. Schema dữ liệu (`storefront_db`)

| Bảng | Cột chính & ràng buộc (`suy luận`) |
| --- | --- |
| `themes` | `id TEXT PK`, `name`, `schema_version INT`, `default_tokens JSONB`, `allowed_sections JSONB`, `is_active BOOL` |
| `sites` | `id UUID PK`, `shop_id UUID UNIQUE`, `slug TEXT UNIQUE` (`^[a-z0-9-]{3,50}$`), `theme_id FK`, `tokens JSONB`, `nav JSONB`, `canonical_mode` (`MARKETPLACE\|SELF`), `status` (`DRAFT\|ACTIVE\|SUSPENDED`), `published_version_id FK` |
| `domains` | `id UUID PK`, `site_id FK`, `shop_id UUID`, `hostname` (`UNIQUE(LOWER(hostname))`), `kind` (`SUBDOMAIN\|CUSTOM`), `verification_token`, `status` (`PENDING_DNS\|VERIFIED\|ACTIVE\|FAILED`), `is_primary` (partial unique 1/site) |
| `pages` | `id UUID PK`, `site_id FK`, `shop_id UUID`, `page_type` (`HOME\|COLLECTION\|PRODUCT\|CUSTOM\|POLICY`), `slug` (`UNIQUE(site_id,slug)`), `title`, `seo JSONB`, `is_enabled BOOL` |
| `sections` | `id UUID PK`, `page_id FK`, `site_id`, `shop_id UUID`, `section_type`, `sort_order INT` (`0..24`), `settings JSONB`, `is_visible BOOL`, `INDEX(page_id,sort_order)` |
| `versions` | `id UUID PK`, `site_id FK`, `shop_id UUID`, `version_no INT` (`UNIQUE(site_id,version_no)`), `snapshot JSONB`, `checksum`, `published_by UUID`, `note`, `created_at` |

```sql
-- Áp dụng đồng bộ cho cả 5 bảng tenant (sites, domains, pages, sections, versions)
ALTER TABLE sites ENABLE ROW LEVEL SECURITY;
ALTER TABLE sites FORCE ROW LEVEL SECURITY;
CREATE POLICY rls_sites ON sites TO storefront_app
  USING (shop_id::text = current_setting('app.current_shop_id', true))
  WITH CHECK (shop_id::text = current_setting('app.current_shop_id', true));
```

## 7. Hợp đồng gRPC (`proto/nexusmart/storefront/v1/storefront.proto`)

```protobuf
syntax = "proto3";
package nexusmart.storefront.v1;
option go_package = "github.com/nexusmart/core/gen/go/nexusmart/storefront/v1;storefrontv1";

service StorefrontService {
  // Public / Edge (cached Redis)
  rpc ResolveSiteByHost(ResolveSiteByHostRequest) returns (ResolveSiteByHostResponse); // hostname, preview_token -> site, primary_host, redirect, snapshot_json
  rpc CheckDomainAllowed(CheckDomainAllowedRequest) returns (CheckDomainAllowedResponse); // Caddy ask: hostname -> allowed
  // Seller (shop_id khóa từ JWT context, tuân buf lint STANDARD)
  rpc GetMySite(GetMySiteRequest) returns (GetMySiteResponse);
  rpc UpsertSiteSettings(UpsertSiteSettingsRequest) returns (UpsertSiteSettingsResponse); // theme_id, tokens_json, nav_json, canonical_mode
  rpc CreateDomain(CreateDomainRequest) returns (CreateDomainResponse);
  rpc VerifyDomain(VerifyDomainRequest) returns (VerifyDomainResponse);
  rpc DeleteDomain(DeleteDomainRequest) returns (DeleteDomainResponse);
  rpc UpsertPage(UpsertPageRequest) returns (UpsertPageResponse); // page + repeated Section
  rpc DeletePage(DeletePageRequest) returns (DeletePageResponse);
  rpc CreatePreviewToken(CreatePreviewTokenRequest) returns (CreatePreviewTokenResponse);
  rpc PublishSite(PublishSiteRequest) returns (PublishSiteResponse);
  rpc ListVersions(ListVersionsRequest) returns (ListVersionsResponse);
  rpc RollbackVersion(RollbackVersionRequest) returns (RollbackVersionResponse);
  // Admin
  rpc AdminSuspendSite(AdminSuspendSiteRequest) returns (AdminSuspendSiteResponse);
}
```

## 8. Khuyến nghị, Rủi ro & Câu hỏi còn mở

### Khuyến nghị cụ thể cho NexusMart (`suy luận`)
1. **Lộ trình 2 pha**: Pha 1 mở `<slug>.nexusmart.vn` (1 wildcard cert DNS-01) + rewrite `/s/[host]/...` + Redis `cacheHandler` + Go `storefront` (RLS `SET LOCAL`) + 8–10 JSON section từ `web/`. Pha 2 mở custom domain bằng **Caddy On-Demand TLS** (`ask` + negative cache + `strict_sni_host on`) và Redirect SSO PKCE; chuyển Cloudflare for SaaS khi vượt ~500 custom domain cần CDN/WAF toàn cầu.
2. **Đóng cứng media & CSP sớm**: gom ảnh shop về CDN sàn trước khi bật tối ưu `next/image`; dùng CSP tĩnh cho storefront công khai để giữ ISR.

### Rủi ro
- **Rò dữ liệu qua pool**: thiếu `SET LOCAL` trong transaction hoặc thiếu `FORCE ROW LEVEL SECURITY` làm vô hiệu hóa RLS.
- **Lệch cache cụm**: thiếu Redis `cacheHandler` hay Caddy shared `storage` gây stale page và chạm trần Let's Encrypt.
- **Ma sát SSO**: cần 1 thao tác chuyển hướng trên custom domain -> giữ Guest Checkout mượt song song SSO.
- **Phishing tên miền**: shop trỏ domain mạo danh gây rủi ro pháp lý cho IP chung của sàn.

### Câu hỏi còn mở
1. Giỏ hàng trên `shopabc.vn` chỉ hiện hàng `shopabc` hay cả giỏ đa gian hàng trên sàn?
2. Voucher sàn (`PLATFORM`) và Xu hội viên có dùng được trên custom domain không?
3. Checkout giữ ở `shopabc.vn/thanh-toan` hay chuyển sang `checkout.nexusmart.vn` để gọn SSO và cổng thanh toán?
4. `canonical_mode` mặc định cho PDP là `MARKETPLACE` (ưu tiên sàn) hay `SELF` (ưu tiên thương hiệu shop)?
