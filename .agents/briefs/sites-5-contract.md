# Giai đoạn 5 – Cloudflare: subdomain cho từng shop và tên miền riêng (hợp đồng chung)

Chủ dự án yêu cầu (2026-11-21): tích hợp Cloudflare để (1) tạo subdomain cho từng shop, (2) cho người bán gán shop vào tên miền riêng của họ.
Ba swarm chạy song song theo hợp đồng này. Không swarm nào đổi hợp đồng; cần đổi thì ghi vào `docs/PHASE5_REPORT.md` mục "Đề xuất đổi hợp đồng" và giữ nguyên bản cũ.

## Mô hình Cloudflare
- Zone của nền tảng: `SITE_ROOT_DOMAIN` (dev `nexusmart.localhost`, prod ví dụ `nexusmart.vn`).
- **Subdomain shop**: khi site được xuất bản lần đầu, user service tạo bản ghi DNS `CNAME <sub>.<root> → <CF_ORIGIN_HOST>` proxied (Cloudflare API `POST /zones/{zone_id}/dns_records`, idempotent: tìm theo tên trước khi tạo). Khi admin tạm khóa site: không xóa bản ghi (app đã trả 404). Khi đổi subdomain hoặc xóa site: xóa bản ghi cũ.
- **Tên miền riêng**: Cloudflare for SaaS, Custom Hostnames API `POST /zones/{zone_id}/custom_hostnames` với `{hostname, ssl:{method:"txt", type:"dv"}}`. Người bán thêm tại DNS của họ: `CNAME <hostname> → <CF_CNAME_TARGET>` (ví dụ `shops.nexusmart.vn`) và các bản ghi TXT mà Cloudflare trả về (`ownership_verification`, `ssl.validation_records`). Trạng thái lấy bằng `GET /zones/{zone_id}/custom_hostnames/{id}`. Xóa bằng `DELETE`.
- Token Cloudflare chỉ nằm trong user service (env `CF_API_TOKEN`, `CF_ZONE_ID`, `CF_ORIGIN_HOST`, `CF_CNAME_TARGET`, `CF_MODE=mock|live`, mặc định `mock`). Không bao giờ tới web, log hay response. `.env.example` chỉ có giá trị giả.
- `CF_MODE=mock`: adapter giả lưu trạng thái trong DB của user, không gọi mạng. Hostname kết thúc bằng `.verified.test` chuyển `ACTIVE` ở lần kiểm tra đầu tiên; `.pending.test` luôn `PENDING`; `.fail.test` thành `FAILED`. Adapter live dùng `net/http` thư viện chuẩn, timeout 10 s, retry backoff cho 429/5xx, không thêm SDK.

## Dữ liệu (user service, migration mới `0003_domains_cloudflare.sql`, không phá dữ liệu cũ)
Mở rộng bảng `domains`: `status TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('PENDING','ACTIVE','FAILED','REMOVING'))`, `cf_record_id TEXT NULL`, `cf_custom_hostname_id TEXT NULL`, `verification JSONB NOT NULL DEFAULT '[]'` (danh sách `{type,name,value}` để hiện cho người bán), `last_checked_at TIMESTAMPTZ NULL`, `last_error TEXT NULL`. Dòng SUBDOMAIN cũ giữ `ACTIVE`.
Luật: tối đa 1 tên miền CUSTOM mỗi site (trừ trường hợp cùng site thêm lại chính host của mình đang `FAILED` thì cho phép thay dòng cũ, không tính vượt `domain_limit`); giữ index unique `LOWER(host)` toàn hệ thống; chỉ dòng `ACTIVE` được `GetSiteByHost` trả về. Chống chiếm giữ tên miền (domain squatting): chỉ dòng `ACTIVE`, `REMOVING`, hoặc `PENDING` trong vòng 48 giờ kể từ `created_at` mới chặn site khác (`domain_taken`); dòng `FAILED` hoặc `PENDING` quá 48 giờ của site khác không chặn (coi như `FAILED`, không chờ job 7 ngày). Khi lấy lại/thay thế dòng cũ không còn hiệu lực, trong cùng transaction có khóa phải gọi Cloudflare `DeleteCustomHostname` cho dòng cũ (mock cũng xử lý), xóa dòng cũ rồi tạo dòng mới; Cloudflare lỗi thì trả lỗi và không xóa dòng cũ.

## Kiểm tra hostname (`services/user/internal/site/validate.go`)
Chữ thường, IDN chuyển punycode (`golang.org/x/net/idna` nếu đã có trong go.mod, nếu không thì từ chối ký tự ngoài ASCII), 2–253 ký tự, mỗi nhãn 1–63, không IP, không cổng, không wildcard, có ít nhất một dấu chấm, không phải root domain hay bất kỳ host nào dưới `SITE_ROOT_DOMAIN`, không thuộc danh sách cấm (`localhost`, `*.local`, `*.internal`, `*.localhost` trừ khi `CF_MODE=mock` và đuôi `.test`).

## gRPC (proto `user`, thêm vào UserService)
- `AddCustomDomain(shop_id, hostname) → Domain`
- `CheckCustomDomain(shop_id, domain_id) → Domain` (gọi Cloudflare, cập nhật trạng thái)
- `RemoveCustomDomain(shop_id, domain_id) → Empty`
- `ListSiteDomains(shop_id) → repeated Domain`
- `Domain { id, host, kind, status, is_primary, verification[] {type,name,value}, last_error, verified_at, created_at }`
Quyền: giống `resolveSiteShopID` (seller chỉ shop trong JWT; admin cần shop_id).
Job nền trong user service: mỗi `CF_POLL_INTERVAL` (mặc định 5 phút, dev 5 s) kiểm tra các domain `PENDING` có `created_at` dưới 7 ngày; quá 7 ngày → `FAILED` với `last_error`.
Khi CUSTOM thành `ACTIVE`: nó thành `is_primary=true`, subdomain `is_primary=false`. `primary_host` của site = host primary. Xóa CUSTOM: subdomain lại primary, gọi Cloudflare DELETE trước rồi mới xóa dòng (lỗi Cloudflare → giữ `REMOVING`, job thử lại).

## REST (gateway)
- `GET /api/v1/seller/site/domains` → `{domains:[Domain]}`
- `POST /api/v1/seller/site/domains` body `{hostname}` → 201 `{domain}`; 400 `invalid_hostname`; 409 `domain_taken` hoặc `domain_limit`
- `POST /api/v1/seller/site/domains/{id}/check` → `{domain}`
- `DELETE /api/v1/seller/site/domains/{id}` → 204
- Admin: `GET /api/v1/admin/sites/{shop_id}/domains`, `DELETE /api/v1/admin/sites/{shop_id}/domains/{id}`
- `GET /api/v1/sites/by-host?host=` giữ nguyên, nhận cả host CUSTOM `ACTIVE`; response thêm `site.primary_host` đúng theo luật trên.
JSON field dùng snake_case như các handler site hiện có.

## Web (Host routing)
- `classifyHost`: host dưới root như cũ; host ngoài root, hợp lệ theo luật hostname → loại `custom`, tra `GET /sites/by-host` (dùng cache dương/âm sẵn có, cache âm cho host lạ, giới hạn kích thước cache để chống làm đầy bộ nhớ). Không thấy → 404. IP, `localhost`, host có ký tự lạ → 404 không gọi backend.
- Host custom được đối xử như host site (rewrite `/s/[host]`, buyer API, chặn `/seller`, `/admin`).
- Khi site có primary là CUSTOM: request trang (GET, không phải `/api`) tới subdomain → 308 sang `https://<custom><path>` (dev: giữ scheme và cổng hiện tại). Canonical, sitemap, robots, link email dùng `primary_host`.
- Cookie vẫn host-only; CSRF so `Origin` với host đang phục vụ (đã có).
- Seller center: màn "Tên miền" trong khu site: thêm tên miền, bảng bản ghi DNS cần thêm (CNAME + TXT, nút sao chép), trạng thái, nút "Kiểm tra lại", xóa có xác nhận. Admin: xem và gỡ tên miền của site.

## Ngoài phạm vi
Tài khoản Cloudflare thật (chỉ mock chạy trong e2e; live có test đơn vị với `httptest` giả API Cloudflare), Caddy, SSO giữa các tên miền, mua tên miền hộ.

## Luật chung cho mọi swarm
Đọc `AGENTS.md`, `CLAUDE.md` mục "Chạy nhiều swarm song song". Không commit, không đổi nhánh. Không sửa thư mục của swarm khác. Server cổng 3111 của manager đang chạy: không build vào `web/.next`, dùng `NEXT_DIST_DIR=.next-<swarm>` rồi xóa thư mục và khôi phục `web/tsconfig.json`, `web/next-env.d.ts`. CI giữ chỉ `workflow_dispatch`. Không thêm dependency runtime mới.
