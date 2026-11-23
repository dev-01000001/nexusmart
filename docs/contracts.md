# NexusMart Core Backend — Architecture & API Contracts

> **Single Source of Truth** for all Phase 1–2 backend workers (`gateway`, `user`, `product`, `cart`, `promotion`, `order`, `payment`, `shipping`, `notification`, and QA/E2E).
> Aligns with `AGENTS.md`, `docs/core-brief.md`, `docs/spec.md`, `proto/`, and `docker-compose.yml`.

---

## 1. Service Registry, Ports & Data Isolation

Every service owns its own database/datastore exclusively. **No service may query another service's database directly.** Inter-service communication uses **gRPC** (synchronous) and **NATS JetStream** (asynchronous).

| Service | Language | gRPC Port | HTTP Port | Datastore | Notes |
| --- | --- | --- | --- | --- | --- |
| `gateway` | Go (`chi`) | — | `8080` | Redis (rate limits) | Public REST `/api/v1/*`, JWT/RBAC, webhook & SSE reverse proxy |
| `user` | Go (`pgx`, `sqlc`) | `50051` | `8081` | PostgreSQL `user_db` + Redis | Argon2id auth, JWT/rotating refresh, shops, 2-level addresses, audit log |
| `product` | Go (`pgx`, `sqlc`) | `50052` | `8082` | PostgreSQL `product_db` | Categories, products, SKUs, optimistic locking stock reserve/release/confirm |
| `cart` | Go (`go-redis`) | `50053` | `8083` | Redis | Cart grouped by `shop_id`, stock/price sync via `ProductService` |
| `promotion` | Go (`pgx`, `go-redis`) | `50054` | `8084` | PostgreSQL `promotion_db` + Redis | Vouchers (shop/platform/freeship), coins, flash sales via atomic Redis Lua |
| `order` | Go (`pgx`, `sqlc`) | `50055` | `8085` | PostgreSQL `order_db` | Multi-shop checkout split, idempotency, state machine, saga orchestrator, outbox |
| `payment` | Rust (`Axum`, `Tonic`, `SQLx`) | `50056` | `8086` | PostgreSQL `payment_db` | Integer VND double-entry ledger, COD & VietQR, HMAC webhooks, escrow, reconciliation, outbox |
| `shipping` | Go (`pgx`, `go-redis`) | `50057` | `8087` | PostgreSQL `shipping_db` + Redis | 34 provinces/wards + legacy map, `Carrier` interface (GHN sandbox + mock), CB/retry, webhooks |
| `notification` | Go (`pgx`, `sqlc`) | `50058` | `8088` | PostgreSQL `notification_db` | Idempotent NATS consumer, DLQ, Mailpit SMTP email, in-app notifications & preferences |

### Shared Infrastructure Ports
| Component | Image | Ports | Purpose |
| --- | --- | --- | --- |
| `postgres` | `postgres:16-alpine` | `5432` | Hosts 7 isolated DBs created by `infra/postgres/init-dbs.sql` |
| `redis` | `redis:7-alpine` | `6379` | Cart, rate limiting, atomic Lua promotion/flash sale slots, caching |
| `nats` | `nats:2.10-alpine` | `4222` (client), `8222` (monitor) | JetStream stream `NEXUSMART` + DLQ |
| `mailpit` | `axllent/mailpit:v1.20` | `1025` (SMTP), `8025` (Web/API) | Local SMTP sink; QA/E2E reads OTP/order emails from `http://mailpit:8025/api/v1/messages` |
| `otel-collector` | `otel/opentelemetry-collector-contrib:0.108.0` | `4317` (gRPC), `4318` (HTTP), `8889` (Prom), `13133` (health) | OTLP trace/metric collector + Prometheus `/metrics` scraper for all 9 backend services + `health_check` extension |

### Standard Ops Endpoints (Every Service HTTP Port)
Every service (`8080..8088`) MUST expose:
- `GET /healthz` → `200 OK` `{"status":"ok","service":"<name>"}`
- `GET /readyz` → `200 OK` when DB/Redis/NATS dependencies are reachable, else `503 Service Unavailable`
- `GET /metrics` → Prometheus text format exposing RED metrics:
  - `http_requests_total{service,method,route,status}`
  - `http_errors_total{service,method,route,status}`
  - `http_request_duration_seconds{service,method,route}`
  - `grpc_requests_total{service,method,code}`
  - `grpc_errors_total{service,method,code}`
  - `grpc_request_duration_seconds{service,method}`

---

## 2. Monorepo Build & Shared Module Conventions

### Protobuf Contracts (`proto/` → `gen/go/`)
Defined in `proto/nexusmart/<domain>/v1/<domain>.proto`, linted & generated via `buf`:
- `nexusmart.user.v1` → `github.com/nexusmart/core/gen/go/nexusmart/user/v1`
- `nexusmart.product.v1` → `github.com/nexusmart/core/gen/go/nexusmart/product/v1`
- `nexusmart.cart.v1` → `github.com/nexusmart/core/gen/go/nexusmart/cart/v1`
- `nexusmart.promotion.v1` → `github.com/nexusmart/core/gen/go/nexusmart/promotion/v1`
- `nexusmart.order.v1` → `github.com/nexusmart/core/gen/go/nexusmart/order/v1`
- `nexusmart.payment.v1` → `github.com/nexusmart/core/gen/go/nexusmart/payment/v1`
- `nexusmart.shipping.v1` → `github.com/nexusmart/core/gen/go/nexusmart/shipping/v1`
- `nexusmart.notification.v1` → `github.com/nexusmart/core/gen/go/nexusmart/notification/v1`

### Go Service `go.mod` Convention (`go 1.22`)
Each Go service at `services/<name>/go.mod` uses local module replacements:
```go
module github.com/nexusmart/core/services/<name>

go 1.22

replace (
    github.com/nexusmart/core/gen/go => ../../gen/go
    github.com/nexusmart/core/pkg/common => ../../pkg/common
)
```

### Rust Payment Service (`services/payment/`)
- Compiles `../../proto/nexusmart/payment/v1/payment.proto` via `tonic-build` in `build.rs` with include path `../../proto`.
- Host development: `protoc` is installed at `~/.local/bin/protoc` with well-known protos in `~/.local/include`.

### Multi-Stage Dockerfile Convention
`docker-compose.yml` and CI build EVERY service with **repository root (`.`) as build context** and `dockerfile: services/<name>/Dockerfile`:
- Builder stage copies `proto/`, `gen/go/`, `pkg/common/`, and `services/<name>/`.
- Runtime stage MUST run as a **non-root user** (e.g., `USER nonroot` / `USER appuser`) and include `ca-certificates` + `wget` (or `curl` for Rust) so Docker Compose `/healthz` healthchecks succeed.

---

## 3. Global REST API Conventions (Gateway `:8080`)

### Headers
- `Authorization: Bearer <access_token>` — Required on authenticated routes. HS256 JWT (TTL `15m`) with claims: `sub` (user ID), `role` (`buyer`|`seller`|`admin`|`super_admin`), `shop_id` (always empty `""` whenever `role != "seller"`, including on login and refresh for demoted users), `jti`, `iat`, `exp`.
  - **Immediate token revocation (`Redis`)**: `user` sets `user:revoked_before:<user_id>` to Unix epoch seconds (TTL = access token TTL + 1m, i.e. 16m) whenever an account is locked, its role changes, or its password is reset (role changes and password resets also revoke all refresh tokens). Gateway fetches `user:revoked_before:<user_id>` together with per-token revocation state in a single `MGET` call and rejects tokens with `iat <= revoked_before` (`401 Unauthorized`). Redis errors fail closed with `503 Service Unavailable`.
- `Idempotency-Key: <unique-key>` — Required on `POST /api/v1/checkout` and money-mutating admin actions (`POST /api/v1/admin/payments/{id}/refund`, `POST /api/v1/admin/refunds/{id}/settle`, `POST /api/v1/admin/reconciliation/queue/{id}/resolve`). Replays return the original result without duplicate side effects.
- `traceparent` — Optional W3C TraceContext header; gateway propagates across gRPC/HTTP/NATS and echoes `X-Trace-Id` on every response.
- Webhook security headers:
  - `X-Signature`: lowercase hex HMAC-SHA256 of the raw request body using `PAYMENT_WEBHOOK_SECRET` (or `SHIPPING_WEBHOOK_SECRET`).
  - `X-Timestamp`: Unix epoch seconds (string). Rejected if skew exceeds 5 minutes (300s) for replay protection.

### Money & JSON Representation
- **All monetary amounts are 64-bit integer VND** (`*_vnd`, JSON integer, never floats).
- **Timestamps** are ISO-8601 / RFC3339 UTC strings (`2026-11-20T02:30:00Z`).
- **Standard error envelope**:
```json
{
  "error": {
    "code": "INSUFFICIENT_STOCK",
    "message": "SKU sku-123 does not have enough available stock",
    "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736"
  }
}
```
- **Standard paginated list envelope**:
```json
{
  "items": [],
  "page": 1,
  "page_size": 20,
  "total": 42
}
```
- **RBAC Defense-in-Depth**: Gateway validates JWT + route role and forwards **only** `Authorization: Bearer <token>` in gRPC metadata (plain `x-user-id` / `x-user-role` / `x-shop-id` metadata is ignored). Every backend service re-verifies the signed JWT on incoming gRPC calls and enforces object-level ownership (e.g., buyer can only read/cancel their own order; seller can only mutate their own `shop_id`'s products/orders; admin cannot lock self or last admin). Internal service-to-service calls without an end user (saga compensation, background jobs, consumers) use short-lived HS256 service tokens signed by `SERVICE_JWT_SECRET`, accepted only on allowlisted RPCs (`internalRPCs` in `pkg/common/auth/auth.go` for Go services; `SERVICE_METHODS` in `services/payment/src/auth.rs` for `payment`).

---

## 4. Complete REST API Surface

### 4.1 Auth, Profile, Addresses & Shops (`user` service)
| Method & Path | Auth / RBAC | Request JSON | Response JSON |
| --- | --- | --- | --- |
| `POST /api/v1/auth/register` | Public | `{"full_name","email","phone","password"}` | `201` `{"user_id","email","status":"PENDING_VERIFICATION"}` |
| `POST /api/v1/auth/verify-otp` | Public | `{"email","otp"}` | `200` `{"verified":true,"user":{...}}` (generic error for unknown email or wrong OTP to prevent email enumeration) |
| `POST /api/v1/auth/resend-otp` | Public | `{"email"}` | `200` `{"sent":true}` (identical response whether email exists or not) |
| `POST /api/v1/auth/forgot-password` | Public | `{"email"}` | `200` `{"sent":true}` (identical response whether email exists or not; generates 32-byte random single-use token, 15m TTL, stored hashed only) |
| `POST /api/v1/auth/reset-password` | Public | `{"token","new_password"}` | `200` `{"ok":true}` (revokes all refresh tokens and sets Redis `user:revoked_before:<user_id>`) |
| `POST /api/v1/auth/login` | Public | `{"email","password"}` | `200` `{"access_token","refresh_token","expires_in_seconds":900,"user":{...}}` (unknown email runs dummy Argon2id hash; `LOCKED` account with valid password returns `lock_reason` and contact guidance; `User` includes `lock_reason`) |
| `POST /api/v1/auth/refresh` | Public | `{"refresh_token"}` | `200` `{"access_token","refresh_token","expires_in_seconds":900}` |
| `POST /api/v1/auth/logout` | Any authenticated | `{"refresh_token"}` | `200` `{"ok":true}` |
| `GET /api/v1/users/me` | Any authenticated | — | `200` `{"user":{...}}` |
| `PATCH /api/v1/users/me` | Any authenticated | `{"full_name","phone","avatar_url"}` | `200` `{"user":{...}}` |
| `GET /api/v1/users/me/addresses` | Any authenticated | — | `200` `{"addresses":[...]}` |
| `POST /api/v1/users/me/addresses` | Any authenticated | `{"recipient_name","recipient_phone","province_code","ward_code","street_address","is_default","legacy_province","legacy_district","legacy_ward"}` | `201` `{"address":{...}}` |
| `PUT /api/v1/users/me/addresses/{id}` | Any authenticated (owner) | Same as create | `200` `{"address":{...}}` |
| `DELETE /api/v1/users/me/addresses/{id}` | Any authenticated (owner) | — | `200` `{"ok":true}` |
| `POST /api/v1/shops/register` | `buyer`, `seller` | `{"name","slug","description","pickup_address_id","contact_phone","business_license_doc"}` (`slug` optional, auto-slugified from `name` if omitted) | `201` `{"shop":{...}}` |
| `GET /api/v1/shops` | Public | Query: `page`, `page_size` | `200` `{"items":[{"id","name","slug","description","status","created_at","updated_at"}],"page","page_size","total"}` (`ACTIVE` shops only; no private fields) |
| `GET /api/v1/shops/{id}` | Public | — | `200` `{"shop":{"id","name","slug","description","status","created_at","updated_at"}}` (`ACTIVE` shop only; `404` otherwise; no private fields) |
| `GET /api/v1/shops/slug/{slug}` | Public | — | `200` `{"shop":{"id","name","slug","description","status","created_at","updated_at"}}` (`ACTIVE` shop only; `404` otherwise; no private fields) |
| `GET /api/v1/shops/me` | Any authenticated | — | `200` `{"shop":{...}}` (caller's own shop in any status, full fields) |
| `GET /api/v1/seller/shop` | `seller` | — | `200` `{"shop":{...}}` (verified `shop_id` from JWT, full fields) |
| `GET /api/v1/seller/bank-account` | `seller` | — (`shop_id` from JWT) | `200` `{"bank_account":{"id","shop_id","bank_code","bank_name","account_number_masked","account_holder_name","holder_name_matched","status","review_note","payout_locked_until","verified_at","created_at","updated_at"}}` (`account_number_masked` only; `account_number` omitted; `payout_locked_until` omitted unless set; expired `PENDING_OTP` records are not returned on `GET`) |
| `POST /api/v1/seller/bank-account` | `seller` | `{"bank_code","bank_name","account_number","account_holder_name"}` (`shop_id` from JWT) | `201` `{"bank_account":{...},"otp_sent":true}` (starts `PENDING_OTP`; publishes `user.shop_bank_account_otp` email) |
| `POST /api/v1/seller/bank-account/verify-otp` | `seller` | `{"otp"}` (`shop_id` from JWT) | `200` `{"bank_account":{...}}` (`account_holder_name` is compared against `shops.verified_owner_name` captured when admin approved the shop: `VERIFIED` if non-empty and matched, else `PENDING_ADMIN_REVIEW` waiting for admin review — including when `verified_owner_name` is empty; `payout_locked_until = now + USER_BANK_PAYOUT_LOCK_HOURS` default 24h is set **only when replacing an already-`VERIFIED` bank account**, not on first verification; currently informational / not enforced anywhere yet because no seller payout API exists) |
| `GET /api/v1/admin/users` | `admin`, `super_admin` | Query: `query`, `role`, `status`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |
| `PATCH /api/v1/admin/users/{id}/status` | `admin`, `super_admin` | `{"status":"ACTIVE"\|"LOCKED","reason"}` | `200` `{"user":{...}}` |
| `PATCH /api/v1/admin/users/{id}/role` | `admin`, `super_admin` | `{"role":"buyer"\|"seller"\|"admin","reason"}` | `200` `{"user":{...}}` |
| `GET /api/v1/admin/shops` | `admin`, `super_admin` | Query: `status`, `bank_review_pending` (`true` filters to shops with a bank account in `PENDING_ADMIN_REVIEW`), `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` (each shop includes `bank_review_pending`, populated only for admin callers) |
| `POST /api/v1/admin/shops/{id}/review` | `admin`, `super_admin` | `{"decision":"APPROVE"\|"REJECT"\|"REQUEST_INFO","reason"}` | `200` `{"shop":{...}}` (`APPROVE` snapshots the shop owner's current name into `shops.verified_owner_name` for bank-account holder verification) |
| `GET /api/v1/admin/shops/{id}/bank-account` | `admin`, `super_admin` | — | `200` `{"bank_account":{...,"review_note","payout_locked_until"}}` (`account_number_masked` only; `account_number` omitted for all end-user callers including admins; prioritizes any `PENDING_ADMIN_REVIEW` record over older `VERIFIED` records; expired `PENDING_OTP` records are not returned on `GET`) |
| `POST /api/v1/admin/shops/{id}/bank-account/review` | `admin`, `super_admin` | `{"bank_account_id","decision":"APPROVE"\|"REJECT","reason"}` | `200` `{"bank_account":{...,"review_note","payout_locked_until"}}` (`account_number_masked` only; sets `review_note = reason`; `APPROVE` sets `payout_locked_until` **only when replacing an already-`VERIFIED` bank account**, currently not enforced without a payout API) |
| `GET /api/v1/admin/audit-logs` | `admin`, `super_admin` | Query: `actor_id`, `target_type`, `target_id`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |

### 4.2 Categories, Products & Inventory (`product` service)
| Method & Path | Auth / RBAC | Request JSON | Response JSON |
| --- | --- | --- | --- |
| `GET /api/v1/categories` | Public | Query: `parent_id` | `200` `{"categories":[...]}` |
| `POST /api/v1/admin/categories` | `admin`, `super_admin` | `{"name","slug","parent_id","commission_rate_bps","sort_order"}` | `201` `{"category":{...}}` |
| `PUT /api/v1/admin/categories/{id}` | `admin`, `super_admin` | Same as create (omitted `commission_rate_bps`/`sort_order`/`parent_id` preserves existing values via presence flags; rejects ancestor cycles `400`; normalizes slug) | `200` `{"category":{...}}` |
| `DELETE /api/v1/admin/categories/{id}` | `admin`, `super_admin` | — | `200` `{"ok":true}` |
| `GET /api/v1/admin/fees/categories` | `admin`, `super_admin` | Query: `category_id`, `page`, `page_size` | `200` `{"items":[{"id","category_id","commission_rate_bps","effective_from","created_by","note","created_at"}],"page","page_size","total"}` |
| `POST /api/v1/admin/fees/categories/{id}` | `admin`, `super_admin` | `{"commission_rate_bps","effective_from","note"}` (`commission_rate_bps` required `0..10000`, missing → `400`; `effective_from` optional RFC3339, defaults to now; active schedule resolves by `effective_from <= now` ordered by `effective_from DESC, seq DESC`) | `201` `{"schedule":{...}}` |
| `GET /api/v1/products` | Public / Optional auth | Query: `category_id`, `category_slug`, `keyword`, `shop_id`, `shop_slug`, `status`, `min_price_vnd`, `max_price_vnd`, `sort_by`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` (each product includes `slug`) |
| `GET /api/v1/products/{id}` | Public / Optional auth | — | `200` `{"product":{...,"slug","skus":[...]}}` |
| `GET /api/v1/products/slug/{slug}` | Public / Optional auth | — | `200` `{"product":{...,"slug","skus":[...]}}` (public sees `APPROVED`/`OUT_OF_STOCK` only; owner seller/admin sees any status) |
| `GET /api/v1/seller/products` | `seller` | Query: `category_id`, `keyword`, `status`, `min_price_vnd`, `max_price_vnd`, `sort_by`, `page`, `page_size` (forces `shop_id` from JWT) | `200` `{"items":[...],"page","page_size","total"}` (all statuses of seller's own shop) |
| `GET /api/v1/seller/products/{id}` | `seller` (shop owner) | — | `200` `{"product":{...,"skus":[...]}}` (`403` for another shop's product) |
| `POST /api/v1/seller/products` | `seller` | `{"category_id","name","slug","description","images":[...],"skus":[{"sku_code","name","attributes":{},"price_vnd":100000,"stock":50,"weight_grams":500,"image_url"}]}` (`slug` optional, auto-slugified from `name` if omitted) | `201` `{"product":{...}}` |
| `PUT /api/v1/seller/products/{id}` | `seller` (shop owner) | Same + `status` (seller may only hide `APPROVED`/`OUT_OF_STOCK` → `HIDDEN`, unhide `HIDDEN` → `APPROVED`, or resubmit `REJECTED` → `PENDING`; `PENDING`/`REJECTED` → `HIDDEN`/`APPROVED` returns `403`) | `200` `{"product":{...}}` |
| `DELETE /api/v1/seller/products/{id}` | `seller` (shop owner) | — | `200` `{"ok":true}` |
| `PATCH /api/v1/seller/skus/{sku_id}/stock` | `seller` (shop owner) | `{"new_stock":80,"expected_version":1}` | `200` `{"sku":{...}}` (or `409` on version conflict) |
| `GET /api/v1/admin/products` | `admin`, `super_admin` | Query: `category_id`, `shop_id`, `keyword`, `status`, `min_price_vnd`, `max_price_vnd`, `sort_by`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` (all statuses) |
| `GET /api/v1/admin/products/{id}` | `admin`, `super_admin` | — | `200` `{"product":{...,"skus":[...]}}` (any status) |
| `POST /api/v1/admin/products/{id}/review` | `admin`, `super_admin` | `{"decision":"APPROVE"\|"REJECT","reason"}` | `200` `{"product":{...}}` (emits `product.reviewed` via transactional outbox) |

- `SKU` payloads include `min_price_30d_vnd` (computed from `sku_price_history`, defaulting to `price_vnd` when no lower price occurred in the past 30 days) for flash sale minimum-price validation and shop discount price-inflation checks. Storefront `original_price_vnd` (strikethrough price) comes from active shop discount programs / flash sales, never manual input.

### 4.3 Shopping Cart (`cart` service)
| Method & Path | Auth / RBAC | Request JSON | Response JSON |
| --- | --- | --- | --- |
| `GET /api/v1/cart` | Any authenticated | — | `200` `{"cart":{"user_id","shop_groups":[...],"total_items","total_vnd"}}` |
| `POST /api/v1/cart/items` | Any authenticated | `{"sku_id","quantity"}` | `200` `{"cart":{...},"clamped_to_stock":false}` |
| `PATCH /api/v1/cart/items/{sku_id}` | Any authenticated | `{"quantity"}` | `200` `{"cart":{...},"clamped_to_stock":false}` |
| `DELETE /api/v1/cart/items/{sku_id}` | Any authenticated | — | `200` `{"cart":{...}}` |
| `DELETE /api/v1/cart` | Any authenticated | Optional `{"sku_ids":[...]}` | `200` `{"ok":true}` |

### 4.4 Vouchers, Flash Sales, Shop Discount Programs & Coins (`promotion` service)
| Method & Path | Auth / RBAC | Request JSON | Response JSON |
| --- | --- | --- | --- |
| `GET /api/v1/vouchers` | Public / Optional auth | Query: `shop_id`, `scope`, `order_amount_vnd` | `200` `{"vouchers":[...]}` (each voucher includes `status`: `ACTIVE`\|`PAUSED`\|`DELETED`, `category_ids`; storefront returns active, non-deleted vouchers) |
| `POST /api/v1/vouchers/{id}/save` | Any authenticated | Optional `{"code"}` | `200` `{"saved":true,"voucher":{...}}` |
| `GET /api/v1/seller/vouchers` | `seller` | Query: `status`, `include_inactive` (forces `scope=SHOP`, `shop_id` from JWT) | `200` `{"vouchers":[...]}` |
| `POST /api/v1/seller/vouchers` | `seller` | `{"code","name","discount_type":"FIXED"\|"PERCENT","value_vnd","discount_percent","max_discount_vnd","min_order_vnd","quota","per_user_limit","budget_vnd","starts_at","ends_at"}` (forces `scope=SHOP`, `shop_id` from JWT) | `201` `{"voucher":{...}}` |
| `PUT /api/v1/seller/vouchers/{id}` | `seller` (shop owner) | Partial/full mutable fields (`name`, `discount_type`, `value_vnd`, `discount_percent`, `max_discount_vnd`, `min_order_vnd`, `quota`, `per_user_limit`, `budget_vnd`, `starts_at`, `ends_at`; if `used_count > 0`, only `name`, raising `quota`/`budget_vnd`, extending `ends_at`; `code` immutable) | `200` `{"voucher":{...}}` |
| `PATCH /api/v1/seller/vouchers/{id}/status` | `seller` (shop owner) | `{"status":"ACTIVE"\|"PAUSED"}` | `200` `{"voucher":{...}}` (syncs Redis immediately; `PAUSED` blocks redemption including saved wallets) |
| `DELETE /api/v1/seller/vouchers/{id}` | `seller` (shop owner) | — | `200` `{"ok":true}` (soft-delete → `DELETED`, syncs Redis) |
| `GET /api/v1/admin/vouchers` | `admin`, `super_admin` | Query: `scope`, `status`, `include_inactive` (defaults to `true` so inactive vouchers are included unless `include_inactive=false` or filtered by `status`) | `200` `{"vouchers":[...]}` |
| `POST /api/v1/admin/vouchers` | `admin`, `super_admin` | Same as seller create + `scope:"PLATFORM"\|"FREESHIP"`, optional `category_ids:[...]` (evaluated across all category descendants) | `201` `{"voucher":{...}}` |
| `PUT /api/v1/admin/vouchers/{id}` | `admin`, `super_admin` | Same as seller update + `category_ids`, `replace_category_ids` | `200` `{"voucher":{...}}` |
| `PATCH /api/v1/admin/vouchers/{id}/status` | `admin`, `super_admin` | `{"status":"ACTIVE"\|"PAUSED"}` | `200` `{"voucher":{...}}` |
| `DELETE /api/v1/admin/vouchers/{id}` | `admin`, `super_admin` | — | `200` `{"ok":true}` |
| `GET /api/v1/flash-sales` | Public | Query: `include_upcoming` | `200` `{"flash_sales":[...]}` (each flash sale includes `min_discount_percent`, `category_ids`) |
| `GET /api/v1/seller/flash-sales` | `seller` | Query: `status` (`UPCOMING`\|`ACTIVE`\|`ENDED`), `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` (calls `ListAllFlashSales`) |
| `GET /api/v1/seller/flash-sales/items` | `seller` | Query: `status`, `flash_sale_id`, `page`, `page_size` (forces `shop_id` from JWT) | `200` `{"items":[...],"page","page_size","total"}` |
| `POST /api/v1/seller/flash-sales/{id}/items` | `seller` | `{"sku_id","flash_price_vnd","total_slots","per_user_limit"}` | `201` `{"item":{...}}` (checks SKU ownership, `flash_price_vnd < min_price_30d_vnd`, program `min_discount_percent`, `total_slots <= available_stock`, no overlapping flash window for same SKU) |
| `GET /api/v1/admin/flash-sales` | `admin`, `super_admin` | Query: `status` (`UPCOMING`\|`ACTIVE`\|`ENDED`), `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` (calls `ListAllFlashSales`) |
| `POST /api/v1/admin/flash-sales` | `admin`, `super_admin` | `{"name","starts_at","ends_at","min_discount_percent","category_ids":[...]}` | `201` `{"flash_sale":{...}}` |
| `GET /api/v1/admin/flash-sale-items` | `admin`, `super_admin` | Query: `status`, `flash_sale_id`, `shop_id`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |
| `POST /api/v1/admin/flash-sale-items/{item_id}/approve` | `admin`, `super_admin` | `{"approve":true,"reason"}` (`approve` bool required, missing → `400`) | `200` `{"item":{...}}` (allowed **only** when item status is `PENDING`, else `409 FailedPrecondition`; re-validates `flash_price_vnd < min_price_30d_vnd`, `min_discount_percent`, `total_slots <= available_stock`, and non-overlapping windows on approve) |
| `GET /api/v1/shop-discounts/active` | Public | Query: `sku_ids` (comma-separated, max 200) or `shop_id` (**at least one required**; missing both or `sku_ids > 200` → `400 INVALID_ARGUMENT`) | `200` `{"discounts":[{"item_id","program_id","shop_id","sku_id","original_price_vnd","discounted_price_vnd","discount_percent","per_user_limit","starts_at","ends_at"}]}` (caps returned rows at 500) |
| `GET /api/v1/seller/discount-programs` | `seller` | Query: `status`, `page`, `page_size` (forces `shop_id` from JWT) | `200` `{"items":[...],"page","page_size","total"}` |
| `POST /api/v1/seller/discount-programs` | `seller` | `{"name","starts_at","ends_at","items":[{"sku_id","discounted_price_vnd","discount_percent","per_user_limit"}]}` (`shop_id` from JWT) | `201` `{"program":{...},"warnings":[...]}` (verifies SKU ownership & `discounted_price_vnd < price_vnd`; warns on overlapping approved flash sale; warns + sets `price_inflation_flagged` if SKU price increased in last 30d) |
| `PUT /api/v1/seller/discount-programs/{id}` | `seller` (shop owner) | Same + `replace_items` | `200` `{"program":{...},"warnings":[...]}` |
| `POST /api/v1/seller/discount-programs/{id}/end` | `seller` (shop owner) | — | `200` `{"program":{...}}` |
| `GET /api/v1/coins/balance` | Any authenticated | — | `200` `{"user_id","balance":5000,"recent_transactions":[...]}` |
| `POST /api/v1/admin/coins/credit` | `admin`, `super_admin` | `{"user_id","amount","reason","reference_id"}` | `200` `{"user_id","balance","already_credited":false}` |
| `POST /api/v1/promotions/preview` | Any authenticated | `{"shop_items":[{"shop_id","shipping_fee_vnd","items":[{"sku_id","quantity","flash_sale_item_id"}]}],"shop_voucher_ids":{"shop1":"v1"},"platform_voucher_id","freeship_voucher_id","coins_to_use"}` (gateway reads only these fields; it resolves authoritative SKU prices, shop ownership, categories via `ProductService.GetSKUsBatch`, and shop freeship rules via `ShippingService.GetShopShippingSettings` forwarding the buyer JWT; gateway does **not** query shop discount programs itself — `PromotionService.PreviewDiscounts` resolves active shop discount programs internally; gateway does not hold `SERVICE_JWT_SECRET`) | `200` `{"breakdown":{...,"total_shop_program_discount_vnd","total_shop_freeship_discount_vnd","shops":[{...,"shop_program_discount_vnd","shop_freeship_discount_vnd","applied_shop_program_item_ids":[...]}]}}` (`applied_shop_program_item_ids` lists only the `shop_discount_program_item_id`s actually applied for that shop; lines **not** in `applied_shop_program_item_ids` — e.g. exceeding `per_user_limit` — are priced at regular price and not reserved; if a requested `flash_sale_item_id` cannot be applied during preview, the line falls back to an active shop discount program if any with a warning in `warnings`, whereas `POST /api/v1/checkout` rejects that flash sale item) |

### 4.5 Checkout & Orders (`order` service)
| Method & Path | Auth / RBAC | Request JSON | Response JSON |
| --- | --- | --- | --- |
| `POST /api/v1/checkout` | Any authenticated + `Idempotency-Key` header | `{"address_id","from_cart":true,"items":[{"sku_id","quantity","flash_sale_item_id"}],"shop_voucher_ids":{},"platform_voucher_id":"","freeship_voucher_id":"","coins_to_use":0,"payment_method":"COD"\|"VIETQR","shipping_choices":[{"shop_id","carrier_code","service_code"}]}` (any client-supplied `shop_program_item_id` is ignored — omitted from gateway DTO and excluded from `order`'s idempotency `request_hash`; server resolves active shop discount programs authoritatively; unlike preview, an unapplicable `flash_sale_item_id` is rejected) | `201` (or `200` on replay) `{"checkout_group_id","orders":[...],"payment_id","payment_method","payment_status","qr_url","qr_code","payment_expires_at","total_payable_vnd","idempotent_replay":false,"warnings":[...]}` |
| `GET /api/v1/orders` | Any authenticated (buyer scope) | Query: `status`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |
| `GET /api/v1/orders/{id}` | Buyer owner / Seller owner / Admin | — | `200` `{"order":{...}}` (`403` for non-owner) |
| `GET /api/v1/orders/{id}/timeline` | Buyer owner / Seller owner / Admin | — | `200` `{"events":[...]}` |
| `POST /api/v1/orders/{id}/cancel` | Buyer owner / Seller owner / Admin | `{"reason"}` | `200` `{"order":{...}}` |
| `POST /api/v1/orders/{id}/confirm-received` | Buyer owner only (`admin` cannot act on behalf of buyer → `403`) | — | `200` `{"order":{...}}` |
| `POST /api/v1/orders/{id}/return` | Buyer owner only (`admin` cannot act on behalf of buyer → `403`) | `{"reason","evidence_urls":[...]}` | `200` `{"order":{...},"return_request_id"}` (creates unique `ReturnRequest` in `PENDING` for `DELIVERED` order within 7 days) |
| `GET /api/v1/orders/{id}/return` | Buyer owner / Seller owner / Admin | — | `200` `{"return_request":{...}}` (includes `events` timeline) |
| `GET /api/v1/returns` | Any authenticated (buyer scope) | Query: `status`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |
| `GET /api/v1/returns/{id}` | Buyer owner / Seller owner / Admin | — | `200` `{"return_request":{...}}` (`403` for non-owner) |
| `POST /api/v1/returns/{id}/escalate` | Buyer owner | `{"reason","evidence_urls":[...]}` | `200` `{"return_request":{...}}` (`REJECTED -> ESCALATED`) |
| `POST /api/v1/returns/{id}/evidence` | Buyer owner | `{"note","evidence_urls":[...]}` | `200` `{"return_request":{...}}` (`EVIDENCE_REQUESTED -> ESCALATED`) |
| `GET /api/v1/seller/orders` | `seller` | Query: `status`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |
| `GET /api/v1/seller/orders/{id}` | `seller` (shop owner) | — | `200` `{"order":{...}}` (`403` for another shop's order) |
| `POST /api/v1/seller/orders/{id}/confirm` | `seller` (shop owner) | — | `200` `{"order":{...}}` |
| `POST /api/v1/seller/orders/{id}/ship` | `seller` (shop owner) | `{"carrier_code","service_code"}` | `200` `{"order":{...},"tracking_code","label_url"}` (creates shipment and sets `CONFIRMED\|PREPARING -> PREPARING` with `tracking_code`; does NOT transition directly to `SHIPPING`) |
| `GET /api/v1/seller/returns` | `seller` | Query: `status`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` (`shop_id` from JWT) |
| `GET /api/v1/seller/returns/{id}` | `seller` (shop owner) | — | `200` `{"return_request":{...}}` (`403` for another shop's return) |
| `POST /api/v1/seller/returns/{id}/respond` | `seller` (shop owner) | `{"decision":"ACCEPT_REFUND"\|"ACCEPT_RETURN"\|"REJECT","note","evidence_urls":[...]}` | `200` `{"return_request":{...}}` (`REJECT` requires `note`) |
| `POST /api/v1/seller/returns/{id}/confirm-received` | `seller` (shop owner) | — | `200` `{"return_request":{...}}` (`RECEIVED -> REFUNDING`) |
| `POST /api/v1/seller/returns/{id}/dispute` | `seller` (shop owner) | `{"reason","evidence_urls":[...]}` | `200` `{"return_request":{...}}` (`RECEIVED -> ESCALATED`; `reason` required) |
| `GET /api/v1/seller/reports` | `seller` | Query: `from_date`, `to_date` (`YYYY-MM-DD`, `Asia/Ho_Chi_Minh`, ≤ 366d), `top_limit` (`shop_id` from JWT) | `200` `{"report":{"shop_id","from_date","to_date","gmv_vnd","net_revenue_vnd","commission_vnd","payment_fee_vnd","total_orders","completed_orders","cancelled_orders","returned_orders","cancel_rate_bps","return_rate_bps","orders_by_status":[{"status","count"}],"daily":[{"date","order_count","completed_count","cancelled_count","returned_count","gmv_vnd","net_revenue_vnd","commission_vnd","payment_fee_vnd"}],"top_products":[{"product_id","product_name","units_sold","revenue_vnd","order_count"}]}}` (`DailyReportPoint.platform_subsidy_vnd` is always `0` on seller reports) |
| `GET /api/v1/seller/reports/export` | `seller` | Query: `from_date`, `to_date` | `200` `text/csv; charset=utf-8` (UTF-8 BOM, max 5 MiB; CSV injection guarded: non-integer values starting with `=`, `+`, `-`, `@`, `\t`, `\r`, `\n`, or leading whitespace followed by `=`, `+`, `-`, `@` are prefixed with `'`; pure integers including negative integers preserved verbatim) |
| `GET /api/v1/admin/orders` | `admin`, `super_admin` | Query: `buyer_id`, `shop_id`, `status`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |
| `GET /api/v1/admin/orders/needs-review` | `admin`, `super_admin` | Query: `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` (orders with `orphan_shipment_needs_manual_review = true`) |
| `POST /api/v1/admin/orders/{id}/resolve-review` | `admin`, `super_admin` | `{"note"}` | `200` `{"order":{...}}` (clears manual review flag and appends timeline event) |
| `GET /api/v1/admin/reports` | `admin`, `super_admin` | Query: `from_date`, `to_date` (`YYYY-MM-DD`, `Asia/Ho_Chi_Minh`, ≤ 366d), `top_limit` | `200` `{"report":{"from_date","to_date","gmv_vnd","net_revenue_vnd","commission_vnd","payment_fee_vnd","platform_subsidy_vnd","total_orders","completed_orders","cancelled_orders","returned_orders","cancel_rate_bps","return_rate_bps","orders_by_status":[...],"daily":[{"date","order_count","completed_count","cancelled_count","returned_count","gmv_vnd","net_revenue_vnd","commission_vnd","payment_fee_vnd","platform_subsidy_vnd"}],"top_products":[...],"user_stats":{"total_users","active_users","new_users","total_shops","active_shops","pending_shops","new_shops","daily":[{"date","new_users","new_shops"}]}}}` (aggregates `OrderService.GetPlatformReport` + `UserService.GetPlatformUserStats`) |
| `GET /api/v1/admin/reports/export` | `admin`, `super_admin` | Query: `from_date`, `to_date` | `200` `text/csv; charset=utf-8` (UTF-8 BOM, max 5 MiB; same CSV injection guard as seller export: prefixes non-integer cells starting with `=`, `+`, `-`, `@`, `\t`, `\r`, `\n`, or leading whitespace + formula character) |
| `GET /api/v1/admin/returns` | `admin`, `super_admin` | Query: `status`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |
| `GET /api/v1/admin/returns/{id}` | `admin`, `super_admin` | — | `200` `{"return_request":{...}}` |
| `POST /api/v1/admin/returns/{id}/resolve` | `admin`, `super_admin` | `{"decision":"REFUND"\|"RETURN_AND_REFUND"\|"REJECT"\|"REQUEST_EVIDENCE","note","return_fee_bearer":"SELLER"\|"BUYER"\|"PLATFORM"}` | `200` `{"return_request":{...}}` |

- `Order` payloads also include `delivered_at`, `return_request_id`, `return_status`, `shop_program_discount_vnd`, `shop_freeship_discount_vnd`, `payment_fee_vnd`, `payment_fee_bps`, and `needs_manual_review` (populated only for `admin`/`super_admin`; always `false` for buyers/sellers). `OrderItem` payloads also include `commission_bps` and `shop_program_item_id`.
- **Platform fee visibility on `Order`/`OrderItem` (enforced by `OrderService` gRPC and gateway REST)**: `Order.commission_vnd`, `Order.payment_fee_vnd`, `Order.payment_fee_bps`, and `OrderItem.commission_bps` return `0` for any buyer who does not own the order's shop (including sellers purchasing from another shop); only the owning shop seller and `admin`/`super_admin` receive the actual values.
- **Report Metric Definitions (`GetSellerReport` & `GetPlatformReport`)**:
  - `gmv_vnd`: sum of `(P + D)` (`total_payable_vnd + platform_voucher_discount_vnd + freeship_discount_vnd + coins_discount_vnd`) across all orders whose status is **not** `PENDING`, `CANCELLED`, or `RETURNED`.
  - `net_revenue_vnd`, `commission_vnd`, and `payment_fee_vnd` (for `COMPLETED` orders) match the double-entry ledger: refunds **before** settlement (`order.completed`) are applied in waterfall order `seller revenue -> fees -> shipping`, whereas refunds **after** settlement deduct directly from seller revenue (`net_revenue_vnd`) without reducing `commission_vnd` or `payment_fee_vnd`.
  - `DailyReportPoint.platform_subsidy_vnd`: populated only on `GetPlatformReport`; always `0` on `GetSellerReport`.
- Orders containing flash sale items only accept currently active (live) slots (upcoming rejected), disallow `COD` (`409 FAILED_PRECONDITION` / `FLASH_SALE_COD_NOT_ALLOWED`), and expire after `ORDER_FLASH_PAYMENT_EXPIRY_MINUTES` (default `5`, capped by the earliest flash window `ends_at`).

### 4.6 Payments, Ledger & Seller Finance (`payment` service)
| Method & Path | Auth / RBAC | Request JSON | Response JSON |
| --- | --- | --- | --- |
| `GET /api/v1/payments/{id}` | Buyer owner / Seller (shop allocation) / Admin | — | `200` `{"payment":{...}}` (buyer sees `shop_allocations` with `commission_vnd = 0`; seller sees only the `shop_allocations` belonging to their own `shop_id`; `admin`/`super_admin`/service token see all allocations with full fields) |
| `POST /api/v1/payments/{id}/retry` | Buyer owner (optional `Idempotency-Key`) | `{"method":"VIETQR"}` | `200` `{"payment":{...}}` (gateway orchestrates via `OrderService.RetryPayment`, which verifies the order group is still awaiting payment and unexpired, creates a superseding payment, and syncs `orders.payment_id` + `payment_expires_at` across all sub-orders; UC-B25) |
| `GET /api/v1/payments/{id}/events` | Buyer owner / Seller (shop allocation) / Admin — gateway checks payment access before proxying; `payment:8086/payments/{id}/events` verifies end-user JWT and read access directly (`401`/`403`/`404`; service tokens rejected with `403`) | — | `text/event-stream` (`payment.pending` / `payment.succeeded` / `payment.expired` / `payment.failed` / `payment.refunded`; streamed payload filtered by caller role) |
| `GET /api/v1/orders/{id}/refunds` | Buyer owner / Admin | Query: `status`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` (buyer sees `bank_account_masked` only; `bank_account_number` omitted/empty) |
| `POST /api/v1/refunds/{id}/bank-account` | Buyer owner (payment owner) | `{"bank_code","bank_account_number","bank_account_name"}` | `200` `{"refund":{...}}` (buyer submits destination bank account for a `pending` refund) |
| `GET /api/v1/seller/balance` | `seller` | — | `200` `{"shop_id","pending_vnd","available_vnd","total_earned_vnd"}` |
| `GET /api/v1/seller/ledger` | `seller` | Query: `account_filter` (`pending`\|`available`), `reference_type`, `page`, `page_size` (`shop_id` from JWT) | `200` `{"items":[{"id","transaction_id","account_code","reference_type","reference_id","description","debit_vnd","credit_vnd","delta_vnd","created_at"}],"page","page_size","total"}` |
| `GET /api/v1/admin/fees/payment` | `admin`, `super_admin` | Query: `payment_method` (`COD`\|`VIETQR`), `include_history` (`true`\|`false`, default `false`) | `200` `{"active_configs":[{"id","payment_method","fee_bps","effective_from","created_by","note","created_at"}],"history":[...]}` |
| `POST /api/v1/admin/fees/payment` | `admin`, `super_admin` | `{"payment_method":"COD"\|"VIETQR","fee_bps","effective_from","note"}` (`effective_from` optional RFC3339, defaults to now; active config resolves by `effective_from <= now` ordered by `effective_from DESC, seq DESC`) | `201` `{"config":{...}}` |
| `GET /api/v1/admin/ledger/summary` | `admin`, `super_admin` | — | `200` `{"total_debits_vnd","total_credits_vnd","is_balanced":true,"accounts":[...]}` |
| `POST /api/v1/admin/payments/{id}/refund` | `admin`, `super_admin` + `Idempotency-Key` | `{"order_id","amount_vnd","reason"}` | `200` `{"refund":{...},"already_existed":false}` |
| `GET /api/v1/admin/refunds` | `admin`, `super_admin` | Query: `status`, `order_id`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` (includes full `bank_account_number` for manual transfer settlement) |
| `POST /api/v1/admin/refunds/{id}/settle` | `admin`, `super_admin` + `Idempotency-Key` | `{"provider_refund_id","note"}` | `200` `{"refund":{...},"already_settled":false}` (posts Debit `refund_payable` / Credit `gateway_clearing`, marks `completed`, emits `payment.refund_settled`) |
| `POST /api/v1/admin/payments/reconcile` | `admin`, `super_admin` | `{"date":"2026-11-20"}` | `200` `{"run_id","matched_count","missing_local_count","missing_provider_count","amount_mismatch_count","items":[...]}` |
| `GET /api/v1/admin/reconciliation/queue` | `admin`, `super_admin` | Query: `status`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |
| `POST /api/v1/admin/reconciliation/queue/{id}/resolve` | `admin`, `super_admin` + `Idempotency-Key` | `{"action":"ASSIGN"\|"REFUND"\|"SUSPEND","payment_id","note"}` | `200` `{"item":{...},"refund":{...},"already_resolved":false}` |

- Direct HTTP routes on `payment:8086` under `/internal/*` (`GET /internal/ledger/summary`, `GET /internal/reconciliation/queue`, `POST /internal/reconciliation/run`) are not proxied by the gateway and require an end-user `admin`/`super_admin` JWT (`401`/`403`; service tokens rejected with `403`).
- **Allocation, Ledger, Hold Period, Two-Step Refunds & Reconciliation Semantics**:
  - For each sub-order (shop) in a checkout group, let `G = subtotal_vnd`, seller-borne discounts `F = flash_sale_discount_vnd`, `P_shop = shop_program_discount_vnd` (UC-S18), `V_shop = shop_voucher_discount_vnd`, and `S_shop = shop_freeship_discount_vnd` (UC-SHIP06), platform-borne discounts `V_plat = platform_voucher_discount_vnd`, `S_free = freeship_discount_vnd`, `X = coins_discount_vnd`, and full pre-freeship shipping `Ship = shipping_fee_vnd`.
  - **Single item-price rule**: on any single SKU line item, an active flash sale takes precedence over a shop discount program (`F` and `P_shop` never stack on the same line item).
  - **Shop freeship rule**: applies when shop merchandise after `F + P_shop + V_shop` ≥ `free_ship_min_order_vnd`, giving `S_shop = min(Ship, free_ship_max_vnd)`. Platform freeship `S_free` applies afterwards on the remaining shipping fee `Ship - S_shop`. `R` must remain non-negative: if `G - F - P_shop - V_shop - S_shop < 0`, trim `S_shop` so `R >= 0`.
  - Seller net merchandise revenue `R = G - F - P_shop - V_shop - S_shop >= 0`; platform subsidy `D = V_plat + S_free + X`; buyer payable `P = G - F - P_shop - V_shop - V_plat - S_free - S_shop - X + Ship = R + Ship - D >= 0` (`P + D = R + Ship`).
  - **Fees (seller-borne, snapshot at order time)**: category commission `C_cat` (`Order.commission_vnd`) is computed from each line's post-item-discount value using the category's active `commission_bps` (snapshotted on `OrderItem.commission_bps`), capped at `R`. Payment fee `C_pay` (`Order.payment_fee_vnd`) is `P * payment_fee_bps / 10000` using the payment method's active `fee_bps` from `PaymentService.GetFeeConfig` (snapshotted on `Order.payment_fee_bps`), capped so `C_total = C_cat + C_pay <= R`. Both `product` (`category_fee_schedules`) and `payment` (`payment_fee_configs`) select the active schedule where `effective_from <= now` with tie-break order `effective_from DESC, seq DESC`; changing fee schedules with `effective_from` never affects existing orders.
  - `ShopAllocation` carries `subtotal_vnd = R`, `commission_vnd = C_total` (`C_cat + C_pay <= R`), `shipping_fee_vnd = Ship`, `platform_discount_vnd = D`; `Payment.amount_vnd = sum(R + Ship - D) = sum(P)`.
  - Zero-payable orders (`P = 0`) still create a payment (`amount_vnd = 0`, immediately `status = succeeded`, `escrow_status = held`, no QR, no `gateway_clearing` posting) with full `(R, C_total, Ship, D)` allocations so seller settlement credits `R - C_total`.
  - **Escrow Settlement & Seller Hold Period**: On `order.completed`, escrow settlement writes balanced double-entry postings: Debit `escrow_liability` (`P` collected minus refunded), Debit `platform_promo_expense` (`D`, `EXPENSE` account), Credit `seller_pending:<shop>` (`R - C_total` minus refunded merchandise portion), Credit `platform_fee_revenue` (`C_total`), Credit `shipping_fee_payable` (`Ship`), moving the allocation to `pending_release`. `order` does NOT call `ReleaseEscrow`. A background worker in `payment` runs every 60s and transitions `pending_release -> released` (Debit `seller_pending:<shop>` / Credit `seller_available:<shop>`) once `settled_at + PAYMENT_SELLER_HOLD_HOURS` (default `72`) has elapsed. `ReleaseEscrow` RPC is restricted to `admin`/`super_admin` end-user JWTs (removed from `SERVICE_METHODS`) and **only accepts `pending_release` allocations** (`held` or `awaiting_collection` returns `409 Conflict`).
  - **COD**: created in `status = pending` with sub-order allocations in `escrow_status = awaiting_collection` (zero ledger postings, no `payment.succeeded` emitted at checkout). On `order.delivered`, `payment` posts Debit `cod_receivable` / Credit `escrow_liability` for that sub-order's `P`, transitions its allocation `awaiting_collection -> held`, emits `payment.succeeded` for that sub-order, and transitions the parent COD payment to `succeeded` once no `awaiting_collection` allocations remain. Cancel before delivery (`order.cancelled`) or failed delivery (`order.returned` with `from_status = SHIPPING`) marks the uncollected allocation `cancelled` with zero ledger postings and transitions the parent COD payment to `cancelled` (or `succeeded` if sibling allocations were already delivered and none remain awaiting collection). Refunds are only permitted for collected payments (VIETQR `succeeded` or delivered COD).
  - **Two-Step Refunds & Late Webhooks**: `CreateRefund` records the refund obligation (Debit `escrow_liability` / Credit `refund_payable`, or post-settlement Debit `seller_pending` then `seller_available`, `409` if insufficient) and emits `payment.refunded`, leaving the refund row in `status = pending` (awaiting bank transfer). Total refunded per sub-order includes `pending` refunds and cannot exceed buyer-paid `P`. Buyer submits receiving bank details via `SubmitRefundBankAccount` (`POST /api/v1/refunds/{id}/bank-account`); admin settles the transfer via `SettleRefund` (`POST /api/v1/admin/refunds/{id}/settle`), posting Debit `refund_payable` / Credit `gateway_clearing`, setting `status = completed` (`REFUND_STATUS_DONE`) + `settled_at`, and emitting `payment.refund_settled`. A late `SUCCEEDED` webhook for a `superseded` payment or cancelled order group auto-creates a `pending` `late_payment` refund without emitting `payment.succeeded`/`payment.refunded`, never mutating order state or overwriting `orders.payment_status`.
  - **Single-Accounting Invariant & Provider Timestamps**: Every `provider_tx_id` is serialized via a transaction-scoped advisory lock and checked across `payments`, finalized `reconciliation_queue` (`assigned`/`refunded`), and tx-scoped refunds (`late-webhook:*`, `recon-refund:*`, `recon-overpay:*`) so each `provider_tx_id` debits `gateway_clearing` **at most once across all paths** (webhook, expiration scanner, reconciliation run/auto-heal, manual queue resolution). On every VietQR confirmation path, `payments.paid_at` records the provider transaction's `occurred_at` timestamp (falling back to `NOW()` only when absent).
  - **Mismatched Transactions (`reconciliation_queue`)**: Admin resolves each queue item via `ResolveReconciliationItem`: `ASSIGN` (exact amount confirms payment like a valid webhook; overpayment confirms and creates a `pending` refund for the excess; underpayment returns `409`), `REFUND` (requires `payment_id`; posts Debit `gateway_clearing` / Credit `refund_payable` and creates a `pending` refund), `SUSPEND` (holds item with note). Every item can only be finalized once, except `SUSPEND` which may be resolved again.
  - **Scheduled/Manual Reconciliation**: Reconciliation dates are evaluated in `Asia/Ho_Chi_Minh`; mock mode reads recorded provider transactions from `webhook_events` (not RAM); SePay uses the real provider transaction timestamp; PayOS without a transaction-list API returns a clear error and marks the run `failed` instead of flagging `missing_provider` in bulk. Any `missing_local` provider transaction not yet accounted for is enqueued idempotently into `reconciliation_queue` (`reason = "reconciliation_missing_local"`) for manual resolution.

### 4.7 Shipping & Administrative Addresses (`shipping` service)
| Method & Path | Auth / RBAC | Request JSON | Response JSON |
| --- | --- | --- | --- |
| `GET /api/v1/shipping/provinces` | Public | Query: `query` | `200` `{"provinces":[{"code","name"}]}` (34 provinces) |
| `GET /api/v1/shipping/provinces/{code}/wards` | Public | Query: `query` | `200` `{"wards":[{"code","province_code","name"}]}` |
| `POST /api/v1/shipping/addresses/map-legacy` | Public | `{"legacy_province","legacy_district","legacy_ward"}` | `200` `{"found":true,"province_code","province_name","ward_code","ward_name",...}` |
| `POST /api/v1/shipping/calculate-fee` | Any authenticated | `{"shop_id","from_province_code","from_ward_code","to_province_code","to_ward_code","weight_grams","cod_amount_vnd","items":[{"sku_id","quantity"}],"payment_method":"COD"\|"VIETQR"}` (`items` & `payment_method` optional; when `items` is provided — max 100 lines, `quantity` 1..999, duplicate `sku_id` merged — gateway ignores client `weight_grams`/`cod_amount_vnd`, verifies every SKU exists and belongs to `shop_id` via `ProductService.GetSKUsBatch` else `400` without leaking other shops, loads shop `default_weight_grams` via `ShippingService.GetShopShippingSettings` forwarding caller JWT with `NotFound`/<=0 → `500g` and other errors propagated, computes `weight_grams = Σ(sku.weight_grams > 0 ? sku.weight_grams : default_weight_grams) × qty` in `int64` rejecting `> int32` with `400`, and sets `cod_amount_vnd = Σ price_vnd × qty` pre-discount subtotal when `payment_method="COD"` else `0`; when `items` is omitted, legacy `weight_grams`/`cod_amount_vnd` pass-through is preserved) | `200` `{"quotes":[{"carrier_code","carrier_name","service_code","service_name","fee_vnd","estimated_days","is_fallback"}]}` |
| `GET /api/v1/shipping/shipments/{order_id}` | Buyer owner / Seller owner / Admin | Query: `direction` (`FORWARD` default \| `RETURN`) | `200` `{"shipment":{...,"direction","return_request_id","redelivery_count"},"events":[...]}` |
| `GET /api/v1/shipping/labels/{tracking_code}` | Buyer owner / Seller owner / Admin (end-user JWT only; service tokens rejected) | — (optional `.html`/`.pdf` suffix) | `200` `text/html; charset=utf-8` (gateway proxies to `shipping`, strips `X-User-*`, sets `X-Content-Type-Options: nosniff` and `Content-Security-Policy: sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'`; browser links default via web BFF `SHIPPING_LABEL_BASE_URL` = `/api/gw/shipping/labels/{tracking_code}`) |
| `POST /api/v1/orders/{id}/redelivery` | Buyer owner | `{"preferred_date":"2026-11-25","note"}` | `200` `{"shipment":{...}}` (shipping verifies JWT `sub == shipment.buyer_id`; legacy shipments without `buyer_id` return `FailedPrecondition`; only when `FORWARD` shipment is `failed`; **at most 1 request per `failed` attempt** — requires a new carrier `failed` event before requesting again, else `409`; max `SHIPPING_MAX_REDELIVERY` = 3 attempts total, else `409`) |
| `GET /api/v1/seller/shipping-settings` | `seller` | — (`shop_id` from JWT) | `200` `{"settings":{"shop_id","enabled_carrier_codes":[...],"default_weight_grams","default_length_cm","default_width_cm","default_height_cm","free_ship_enabled","free_ship_min_order_vnd","free_ship_max_vnd","updated_at"}}` |
| `PUT /api/v1/seller/shipping-settings` | `seller` | `{"enabled_carrier_codes":[...],"default_weight_grams","default_length_cm","default_width_cm","default_height_cm","free_ship_enabled","free_ship_min_order_vnd","free_ship_max_vnd"}` (`shop_id` from JWT) | `200` `{"settings":{...}}` |
| `GET /api/v1/admin/carriers` | `admin`, `super_admin` | — | `200` `{"carriers":[{"carrier_code","name","enabled","token_configured","fallback_rates":[...],"updated_at"}]}` |
| `PUT /api/v1/admin/carriers/{code}` | `admin`, `super_admin` | `{"enabled":true,"api_token":"...","fallback_rates":[{"region":"SAME_PROVINCE"\|"SAME_REGION"\|"CROSS_REGION","base_fee_vnd","base_weight_grams","extra_fee_vnd_per_500g"}]}` (`enabled` bool required, missing → `400`; `api_token` write-only, encrypted at rest with `SHIPPING_CONFIG_KEY`) | `200` `{"carrier":{...}}` |
| `POST /api/v1/admin/carriers/{code}/test` | `admin`, `super_admin` | — | `200` `{"ok":true,"message":"..."}` |

### 4.8 Notifications (`notification` service)
| Method & Path | Auth / RBAC | Request JSON | Response JSON |
| --- | --- | --- | --- |
| `GET /api/v1/notifications` | Any authenticated | Query: `category`, `unread_only`, `page`, `page_size` | `200` `{"items":[...],"unread_count","page","page_size","total"}` |
| `POST /api/v1/notifications/read` | Any authenticated | `{"notification_ids":[...],"mark_all":false}` | `200` `{"updated_count","unread_count"}` |
| `GET /api/v1/notifications/preferences` | Any authenticated | — | `200` `{"preferences":{...}}` |
| `PUT /api/v1/notifications/preferences` | Any authenticated | `{"order_email","order_in_app","order_push","promotion_email","promotion_in_app","promotion_push","system_email","system_in_app","system_push"}` | `200` `{"preferences":{...}}` |

### 4.9 Webhooks (Proxied by Gateway & Direct on Target Services)
No JWT required; authenticated via HMAC-SHA256 signature.
- `POST /webhooks/payment` & `POST /webhooks/vietqr` → proxied by gateway (`:8080`) directly to `payment` (`:8086`) preserving raw body, `X-Signature`, `X-Timestamp`, and `traceparent`:
  ```json
  {
    "provider": "vietqr_mock",
    "provider_tx_id": "TXN-20261120-0001",
    "payment_id": "pay-uuid",
    "order_group_id": "grp-uuid",
    "amount_vnd": 250000,
    "status": "SUCCEEDED",
    "reference": "NEXUSMART GRP-UUID",
    "occurred_at": "2026-11-20T02:35:00Z"
  }
  ```
  - Invalid/missing `X-Signature` → `401 Unauthorized` (or `403 Forbidden`).
  - Stale `X-Timestamp` (> 5 min) → `401 Unauthorized`.
  - Duplicate `provider_tx_id` → `200 OK` idempotently, zero duplicate ledger entries, zero duplicate events.
- `POST /webhooks/shipping/{carrier}` → proxied by gateway (`:8080`) directly to `shipping` (`:8087`) preserving raw body & `X-Signature`:
  ```json
  {
    "event_id": "ghn-evt-001",
    "tracking_code": "GHN123456789",
    "order_id": "ord-uuid",
    "raw_status": "delivered",
    "description": "Giao hàng thành công",
    "occurred_at": "2026-11-20T09:00:00Z"
  }
  ```
  - Returns `200 OK` immediately after signature verification & idempotent persistence.

### 4.10 Internal & Cross-Service gRPC Contracts (`internalRPCs`)
RPCs listed in `internalRPCs` (`pkg/common/auth/auth.go`) never run anonymously (missing token → `Unauthenticated`). `service_token = true` accepts short-lived `SERVICE_JWT_SECRET` service tokens in addition to end-user JWTs (handlers enforce RBAC/ownership); `service_token = false` requires a forwarded end-user JWT and rejects service tokens (`PermissionDenied`).

| Service / RPC | `service_token` | Request Fields | Response Fields | Purpose |
| --- | --- | --- | --- | --- |
| `OrderService.MarkPaymentResult` | `true` (service token or admin JWT) | `checkout_group_id`, `order_id`, `payment_id`, `status`, `reason` | `ok` | Internal/admin payment result transition alongside NATS events (no anonymous access) |
| `OrderService.{CreateCheckout,GetOrder,ListOrders,CancelOrder,SellerConfirmOrder,SellerShipOrder,BuyerConfirmReceived,RequestReturn,GetOrderTimeline}` | `false` (end-user JWT via gateway; service tokens rejected) | See `order.proto` | See `order.proto` | Core checkout and order lifecycle RPCs with interceptor-level fail-closed auth + object-level RBAC (`OrderService` gRPC zeroes `Order.commission_vnd`, `Order.payment_fee_vnd`, `Order.payment_fee_bps`, and `OrderItem.commission_bps` for buyers who do not own the shop) |
| `OrderService.RetryPayment` | `false` (buyer JWT via gateway) | `payment_id`, `payment_method` (`OrderPaymentMethod`), `idempotency_key` | `checkout_group_id`, `payment_id`, `payment_expires_at` | Validates order group is pending & unexpired, creates superseding payment, updates `orders.payment_id` and `payment_expires_at` |
| `OrderService.{GetReturnRequest,ListReturnRequests,RespondReturn,EscalateReturn,AddReturnEvidence,ConfirmReturnReceived,DisputeReturnGoods,AdminResolveReturn}` | `false` (end-user JWT via gateway; service tokens rejected) | See `order.proto` | `return_request` (or `return_requests`, `total`) | End-to-end return request workflow with object-level buyer/seller/admin authorization |
| `OrderService.{GetSellerReport,GetPlatformReport,ListOrdersNeedingReview,ResolveOrderReview}` | `false` (end-user JWT via gateway; service tokens rejected) | See `order.proto` | Report metrics or flagged orders | Server-side seller/platform reports (`Asia/Ho_Chi_Minh`, ≤ 366d) and admin orphan-shipment manual review queue |
| `ShippingService.GetShipment` | `true` (forwarded end-user JWT via gateway, or service token from `order` watchdog) | `order_id`, `tracking_code`, `direction` (`UNSPECIFIED` = `FORWARD`) | `shipment`, `events` | Reads shipment & tracking timeline; requires JWT or service token (anonymous blocked at interceptor and handler); end-user JWT enforces strict ownership: `seller` with matching `shop_id`, `buyer` matching persisted `shipment.buyer_id`, or `admin`/`super_admin` (`order` watchdog uses service token to sync `RETURN` shipment state) |
| `ShippingService.CalculateFee` | `false` (end-user JWT via gateway / `order` checkout; service tokens rejected) | `shop_id`, `from_*`, `to_*`, `weight_grams`, `cod_amount_vnd` | `quotes` | Carrier fee quoting (no anonymous gRPC access; COD surcharge at quote time is estimated from the pre-discount subtotal) |
| `ShippingService.CreateShipment` | `false` (forwarded seller JWT from `order`; service tokens rejected) | `order_id`, `shop_id`, `carrier_code`, `service_code`, `cod_amount_vnd`, `weight_grams`, `from_address`, `to_address`, `items_summary`, `buyer_id` | `shipment`, `already_existed` | Called by `order` using the forwarded seller JWT (`order` populates `buyer_id` from the order record; `shipping` never trusts `buyer_id` from end-user clients, persists it for `RequestRedelivery` ownership checks, and omits it from `Shipment`). Still permits a platform-disabled carrier when the existing order already selected it (UC-A12 disable applies only to new orders) |
| `ShippingService.CancelShipment` | `true` (service token or `admin`/`super_admin` JWT only) | `order_id`, `reason`, `direction` (`UNSPECIFIED` = `FORWARD`) | `shipment` | `order` **always** calls `CancelShipment` with a service token (even when initiated by buyer, seller, or admin); `shipping` handler allows **only** service token or `admin`/`super_admin` (rejects buyer/seller JWTs); rejects with `FailedPrecondition` (`409`) once picked up |
| `ShippingService.CreateReturnShipment` | `true` (service token or `admin`/`super_admin` JWT only) | `order_id`, `return_request_id`, `shop_id`, `from_address`, `to_address`, `weight_grams`, `carrier_code`, `service_code`, `items_summary`, `buyer_id` | `shipment`, `already_existed` | `order` **always** calls `CreateReturnShipment` with a service token (even when initiated by buyer, seller, or admin), populating `buyer_id` from the order record; `shipping` handler allows **only** service token or `admin`/`super_admin`. Rejects platform-disabled carriers (`FailedPrecondition`); empty `carrier_code` lets `shipping` auto-select among platform-enabled carriers |
| `ShippingService.GetShopShippingSettings` | `true` (service token / any authenticated end-user JWT; anonymous → `Unauthenticated`) | `shop_id` | `settings` | Service token, `admin`/`super_admin`, or owning shop `seller` (`claims.ShopID`) receives full settings (carriers, default parcel weight/dimensions, freeship thresholds). Any authenticated non-owner user (buyer, or another seller requesting a different shop) MUST pass `shop_id` (`InvalidArgument` if empty) and receives ONLY `shop_id`, `default_weight_grams`, and `free_ship_*` (`free_ship_enabled`, `free_ship_min_order_vnd`, `free_ship_max_vnd`), leaving carriers, parcel dimensions, and `updated_at` empty. Gateway promotion preview and shipping fee quoting call this RPC forwarding the buyer JWT (gateway does not hold `SERVICE_JWT_SECRET`) |
| `ShippingService.RequestRedelivery` | `false` (buyer JWT via gateway; service tokens rejected) | `order_id`, `preferred_date`, `note` | `shipment` | `shipping` compares JWT `sub` with persisted `shipment.buyer_id` (`PermissionDenied` on mismatch); legacy shipments without `buyer_id` return `FailedPrecondition` |
| `ShippingService.{UpdateShopShippingSettings,AdminListCarriers,AdminUpdateCarrier,AdminTestCarrier}` | `false` (end-user JWT via gateway; service tokens rejected) | See `shipping.proto` | See `shipping.proto` | Seller shipping settings mutation and admin carrier configuration/testing |
| `NotificationService.{ListNotifications,MarkRead,GetPreferences,UpdatePreferences}` | `false` (end-user JWT via gateway; service tokens rejected) | See `notification.proto` | See `notification.proto` | Personal user notifications and preference management (no anonymous access) |
| `PromotionService.ReleasePromotionShop` | `true` (service token / admin JWT) | `order_id` (checkout group ID), `shop_id`, `reason` | `released`, `already_released`, `coins_returned`, `shop_voucher_released`, `flash_sale_quantity_released`, `platform_voucher_released`, `shop_program_quantity_released` | Idempotent per `(order_id, shop_id)` partial promotion release on single sub-order cancel or `RETURNED` (releases coins, shop voucher, flash sale slots, and shop discount program per-user limits; platform voucher released only when all sub-orders in the group are cancelled) |
| `PromotionService.{ReleasePromotion,CreditCoins}` | `true` (service token / admin JWT) | See `promotion.proto` | See `promotion.proto` | Full-group promotion release (if no reservation exists yet for `order_id`, records a release tombstone so a late-arriving `ReservePromotion` for that `order_id` is rejected) and idempotent coin credit by `reference_id` (no anonymous access) |
| `PromotionService.{GetCoinBalance,PreviewDiscounts,SaveVoucher,ReservePromotion,CreateVoucher,UpdateVoucher,SetVoucherStatus,DeleteVoucher,CreateFlashSale,RegisterFlashSaleItem,ApproveFlashSaleItem,ListFlashSaleItems,ListAllFlashSales,CreateShopDiscountProgram,UpdateShopDiscountProgram,EndShopDiscountProgram,ListShopDiscountPrograms}` | `false` (end-user JWT via gateway / `order` checkout; service tokens rejected) | See `promotion.proto` | See `promotion.proto` | Personal coin balance, discount preview & checkout reservation (`PreviewDiscounts` resolves active shop discount programs internally and falls back from an unapplicable flash sale item to a shop discount program with a warning; `ReservePromotion` rejects unapplicable flash sale items, rejects replay of the same `order_id` by a different user with `PermissionDenied`, and rejects tombstoned/already-released `order_id`s; both populate `ShopDiscountBreakdown.applied_shop_program_item_ids` with the `shop_discount_program_item_id`s actually applied per shop while lines exceeding `per_user_limit` are omitted, priced at regular price, and not reserved), voucher wallet, and seller/admin voucher, flash sale & shop discount program management |
| `ProductService.{ReleaseStock,ConfirmStock,RestockOrderItems,GetCategoryDescendants}` | `true` (service token / `admin`/`super_admin` JWT only for `ReleaseStock`, `ConfirmStock`, `RestockOrderItems`; service token / authenticated JWT for `GetCategoryDescendants`) | See `product.proto` | See `product.proto` | Saga stock compensation/confirmation (`ReleaseStock` and `ConfirmStock` reject non-admin end-user JWTs; `ReleaseStock` when no reservation exists yet records a tombstone in `stock_release_tombstones` so a late-arriving `ReserveStock` or `ConfirmStock` for that `order_id` is rejected with `FailedPrecondition`), return restock, and recursive category descendant lookup for voucher category scope |
| `ProductService.{ReserveStock,CreateCategory,UpdateCategory,DeleteCategory,SetCategoryFeeSchedule,ListCategoryFeeSchedules,CreateProduct,UpdateProduct,DeleteProduct,AdminReviewProduct,UpdateStock}` | `false` (end-user JWT via gateway / `order` checkout; service tokens rejected) | See `product.proto` | See `product.proto` | Checkout stock reserve, admin category/fee-schedule management, and seller/admin catalog mutations |
| `CartService.{GetCart,AddItem,UpdateItemQty,RemoveItem,ClearItems}` | `false` (end-user JWT via gateway / `order` checkout; service tokens rejected) | See `cart.proto` | See `cart.proto` | Personal shopping cart read/write/clear (no anonymous gRPC access; `order` forwards buyer JWT during `CreateCheckout`; handler `resolveUserID` enforces verified JWT ownership and rejects service tokens) |
| `UserService.{GetAddress,GetProfile,RecordCODRejection,WriteAuditLog,GetShopBankAccountForPayout}` | `true` (service token or authorized JWT; `RecordCODRejection` and `GetShopBankAccountForPayout` reject end-user JWTs in handler) | See `user.proto` | See `user.proto` | Personal profile/address lookup, COD rejection tracking, centralized audit log, and internal payout bank-account retrieval |
| `UserService.{ValidateUser,Logout,UpdateProfile,CreateAddress,ListAddresses,UpdateAddress,DeleteAddress,RegisterShop,AdminListUsers,AdminUpdateUserStatus,AdminUpdateUserRole,AdminListShops,AdminReviewShop,ListAuditLogs,AddShopBankAccount,VerifyShopBankAccountOTP,GetShopBankAccount,AdminReviewShopBankAccount,GetPlatformUserStats}` | `false` (end-user JWT via gateway / `order` checkout; service tokens rejected) | See `user.proto` | See `user.proto` | User validation, session logout, profile/address PII, shop registration, shop bank account management/review, admin user/shop/audit management, and platform user/shop stats |

- **Explicitly public RPCs (not in `internalRPCs`; still reject service tokens with `PermissionDenied`)**:
  - `UserService`: `Register`, `VerifyOTP`, `ResendOTP`, `ForgotPassword`, `ResetPassword`, `Login`, `RefreshToken` (unauthenticated auth flows); `GetShop` (public storefront lookup + internal shop metadata read by `shipping`/`notification`/`order`/`cart` without service token); `ListPublicShops` (active shop directory).
  - `ShippingService`: `ListProvinces`, `ListWards` (public administrative catalog, also called by `user` address validation without token); `MapLegacyAddress` (public legacy address mapper).
  - `PromotionService`: `ListAvailableVouchers`, `ListActiveFlashSales`, `ListActiveShopDiscounts` (public / optional-auth storefront catalog; `ListActiveShopDiscounts` requires `sku_ids` (max 200) or `shop_id`, returning `InvalidArgument` if both are missing or `sku_ids > 200`, and caps output at 500 rows).
  - `ProductService`: `ListCategories`, `GetProduct`, `ListProducts` (public / optional-auth storefront catalog); `GetSKUsBatch` (non-PII SKU catalog lookup called by `cart` without JWT forwarding).

- **Rust `payment` `SERVICE_METHODS` & RPC visibility** (`services/payment/src/auth.rs`): accepts service tokens ONLY on `CreatePayment`, `GetPayment`, `CancelPayment`, `CreateRefund`, and `GetFeeConfig`. `ReleaseEscrow`, `SetFeeConfig`, `ListSellerLedger`, `ListRefunds`, `SubmitRefundBankAccount`, `SettleRefund`, `ListReconciliationQueue`, and `ResolveReconciliationItem` are **not** in `SERVICE_METHODS` and require an end-user JWT.
  - `PaymentService.CreatePayment` (service token — `order` watchdog forward recovery only): requires non-empty `user_id`, non-empty `order_group_id`, and exact `idempotency_key == "checkout:" + order_group_id` (`InvalidArgument` if missing or mismatched); forbids `supersede_previous = true` (`PermissionDenied`, since payment retry is buyer-only). End-user JWT behavior is unchanged.
  - `PaymentService.GetFeeConfig`: restricted strictly to `admin`/`super_admin` JWT and internal service tokens (buyers and sellers rejected with `PermissionDenied`).
  - `PaymentService.GetPayment`: buyer owner receives `shop_allocations` with `commission_vnd = 0`; seller receives only allocations matching their own `shop_id`; `admin`/`super_admin` and service tokens receive full details.
- Key protobuf additions:
  - `nexusmart.payment.v1.ShopAllocation.platform_discount_vnd` (`int64`, field 6): platform-borne discount `D` for the sub-order (`amount_vnd = sum(subtotal_vnd + shipping_fee_vnd - platform_discount_vnd)`). Plus `PaymentFeeConfigItem`, `SellerLedgerEntry`, and RPCs `GetFeeConfig`, `SetFeeConfig`, `ListSellerLedger`.
  - `nexusmart.promotion.v1`: `VoucherStatus`, `Voucher.{status(23), category_ids(24)}`, `CreateVoucherRequest.category_ids(16)`, `ListAvailableVouchersRequest.{include_inactive(5), status(6)}`, `DiscountLineItem.{category_id(5), shop_discount_program_item_id(6)}`, `ShopDiscountInput.{free_ship_enabled(5), free_ship_min_order_vnd(6), free_ship_max_vnd(7)}`, `ShopDiscountBreakdown.{shop_program_discount_vnd(12), shop_freeship_discount_vnd(13), applied_shop_program_item_ids(14)}`, `DiscountBreakdown.{total_shop_program_discount_vnd(14), total_shop_freeship_discount_vnd(15)}`, `FlashSale.{min_discount_percent(6), category_ids(7)}`, `CreateFlashSaleRequest.{min_discount_percent(5), category_ids(6)}`, `FlashSaleItem.{original_price_vnd(11), created_at(12)}`, `FlashSaleReservationItem.shop_id(4)`, `ShopDiscountProgramReservationItem`, `ReservePromotionRequest.shop_discount_program_items(9)`, `ReleasePromotionShopResponse.shop_program_quantity_released(7)`; plus `ShopDiscountProgramStatus`, `ShopDiscountProgram*`, `ActiveShopDiscount`, and RPCs `UpdateVoucher`, `SetVoucherStatus`, `DeleteVoucher`, `ListFlashSaleItems`, `ListAllFlashSales`, `CreateShopDiscountProgram`, `UpdateShopDiscountProgram`, `EndShopDiscountProgram`, `ListShopDiscountPrograms`, `ListActiveShopDiscounts`.
  - `nexusmart.product.v1`: `SKU.min_price_30d_vnd(18)`, `UpdateCategoryRequest.{has_commission_rate_bps(7), has_sort_order(8), has_parent_id(9)}`, `CategoryFeeSchedule`, and RPCs `GetCategoryDescendants`, `SetCategoryFeeSchedule`, `ListCategoryFeeSchedules`.
  - `nexusmart.order.v1`: `Order.{delivered_at(30), return_request_id(31), return_status(32), shop_program_discount_vnd(33), shop_freeship_discount_vnd(34), payment_fee_vnd(35), payment_fee_bps(36), needs_manual_review(37)}`, `OrderItem.{commission_bps(13), shop_program_item_id(14)}`, `CheckoutItemInput.shop_program_item_id(4)` (ignored when sent by clients), `CreateCheckoutResponse.warnings(11)`, `DailyReportPoint.platform_subsidy_vnd(10)` (platform report only; always `0` on seller report); plus `Return*`, `OrderStatusCount`, `DailyReportPoint`, `TopProductStat`, and RPCs `GetSellerReport`, `GetPlatformReport`, `ListOrdersNeedingReview`, `ResolveOrderReview`.
  - `nexusmart.shipping.v1`: `Shipment.{direction(13), return_request_id(14), redelivery_count(15)}` (omits `buyer_id`), `ShopShippingSettings.{free_ship_enabled(8), free_ship_min_order_vnd(9), free_ship_max_vnd(10)}`, `UpdateShopShippingSettingsRequest.{free_ship_enabled(7), free_ship_min_order_vnd(8), free_ship_max_vnd(9)}`, plus `CreateShipmentRequest.buyer_id(10)`, `CreateReturnShipmentRequest.buyer_id(10)`, `CancelShipmentRequest.direction(3)`, `GetShipmentRequest.direction(3)`.
  - `nexusmart.payment.v1.Refund`: `bank_code(9)`, `bank_account_masked(10)`, `bank_account_name(11)`, `bank_account_number(12, admin-only)`, `provider_refund_id(13)`, `is_late_payment(14)`, `settled_at(15)`, `settlement_note(16)`; plus `ReconciliationAction`, `ReconciliationQueueItem`.
  - `nexusmart.user.v1`: `User.lock_reason(13)`, `Shop.bank_review_pending(13, admin-only)`, `AdminListShopsRequest.bank_review_pending_only(4)`, `ForgotPassword`, `ResetPassword`, `ShopBankAccountStatus`, `ShopBankAccount` (`account_number(6)` populated ONLY by internal service-token `GetShopBankAccountForPayout`), `DailyUserShopStat`, and RPCs `AddShopBankAccount`, `VerifyShopBankAccountOTP`, `GetShopBankAccount`, `AdminReviewShopBankAccount`, `GetPlatformUserStats`, `GetShopBankAccountForPayout`.

---

## 5. NATS JetStream Event Contracts

### Stream & DLQ Configuration
- **Primary Stream**: `NEXUSMART`
  - Subjects: `user.>`, `order.>`, `payment.>`, `shipment.>`, `product.>`, `promotion.>` (services updating an existing NATS stream MUST call `UpdateStream` when any required subject is missing rather than returning early on `StreamInfo`)
  - Retention: `LimitsPolicy`, Storage: `FileStorage`, Deduplication window: `2m` (`Nats-Msg-Id` set to `event_id`).
- **Dead Letter Queue (DLQ)**:
  - Stream `NEXUSMART_DLQ` capturing `dlq.>` (and/or local `dead_letter_events` table in consumer DB).
  - Consumers MUST track processed `event_id` in a `processed_events(event_id PRIMARY KEY, processed_at)` table inside the same DB transaction as their state update (**idempotent consumer**). After max delivery attempts (default `5`), poison messages are published to `dlq.<original_subject>` and acked.
- **Trace Propagation**: Publishers inject W3C `traceparent` into NATS message headers AND include `trace_id` inside the JSON envelope.

### Canonical JSON Event Envelope
Every message published to `NEXUSMART` MUST use this exact JSON envelope:
```json
{
  "event_id": "01934b12-8a4c-7b90-8123-456789abcdef",
  "event_type": "payment.succeeded",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "occurred_at": "2026-11-20T02:35:00Z",
  "payload": {}
}
```

### Event Subjects & Payload Schemas

| Subject / `event_type` | Publisher | Subscribers | `payload` Fields |
| --- | --- | --- | --- |
| `user.registered` | `user` | `notification` | `{"user_id","email","full_name","otp","expires_at","is_resend":false}` (`is_resend` optional boolean; `true` on resend OTP: sends OTP email & caches contact, does **not** create a duplicate welcome in-app notification) |
| `user.password_reset_requested` | `user` | `notification` | `{"user_id","email","full_name","reset_token","expires_at"}` (**SECURITY**: `reset_token` MUST NEVER be logged by publisher or consumer) |
| `user.shop_reviewed` | `user` | `notification` | `{"shop_id","owner_user_id","shop_name","decision":"APPROVE"\|"REJECT"\|"REQUEST_INFO","reason"}` |
| `user.shop_bank_account_otp` | `user` | `notification` | `{"user_id","shop_id","email","full_name","otp","expires_at","bank_code","account_number_masked"}` (**SECURITY**: `otp` MUST NEVER be logged by publisher or consumer, MUST NOT appear in email Subject, and MUST be redacted before DLQ persistence) |
| `product.reviewed` | `product` (outbox) | `notification` | `{"product_id","product_name","shop_id","decision":"APPROVE"\|"REJECT","reason","occurred_at"}` |
| `promotion.voucher_expiring` | `promotion` | `notification` | `{"user_id","voucher_id","code","ends_at"}` (emitted at most once per `(user_id, voucher_id)` 24h before `ends_at`) |
| `order.created` | `order` | `notification` | `{"checkout_group_id","order_id","order_code","buyer_id","shop_id","payment_method","total_payable_vnd"}` |
| `order.confirmed` | `order` | `notification` | `{"order_id","order_code","buyer_id","shop_id","payment_method","total_payable_vnd"}` |
| `order.cancelled` | `order` | `notification`, `shipping`, `payment` | `{"order_id","order_code","buyer_id","shop_id","reason","was_paid":true\|false}` |
| `order.shipped` | `order` | `notification` | `{"order_id","order_code","buyer_id","shop_id","carrier_code","tracking_code"}` |
| `order.delivered` | `order` | `notification`, `payment` | `{"order_id","order_code","buyer_id","shop_id","tracking_code"}` |
| `order.returned` | `order` | `payment`, `notification` | `{"order_id","order_code","checkout_group_id","buyer_id","shop_id","payment_method","from_status":"SHIPPING"\|"RETURN_REQUESTED","total_payable_vnd","reason","was_paid"}` |
| `order.return_updated` | `order` | `notification` | `{"return_request_id","order_id","order_code","checkout_group_id","buyer_id","shop_id","status","previous_status","reason","return_tracking_code","deadline_at","actor_role"}` |
| `order.completed` | `order` | `notification`, `promotion`, `payment` | `{"order_id","order_code","buyer_id","shop_id","subtotal_vnd","commission_vnd","payment_fee_vnd","total_payable_vnd","coins_reward"}` (`commission_vnd` = `C_cat` category commission only; `payment_fee_vnd` = `C_pay`) |
| `payment.succeeded` | `payment` | `order`, `notification` | `{"payment_id","order_group_id","order_id","user_id","amount_vnd","method","provider_tx_id"}` |
| `payment.failed` | `payment` | `order`, `notification` | `{"payment_id","order_group_id","order_id","user_id","reason"}` |
| `payment.expired` | `payment` | `order`, `notification` | `{"payment_id","order_group_id","order_id","user_id","reason"}` |
| `payment.refunded` | `payment` | `order`, `notification` | `{"refund_id","payment_id","order_id","user_id","amount_vnd","reason"}` |
| `payment.refund_settled` | `payment` | `notification` | `{"refund_id","payment_id","order_id","user_id","amount_vnd"}` |
| `shipment.updated` | `shipping` | `order`, `notification` | `{"shipment_id","order_id","shop_id","carrier_code","tracking_code","status":"picking"\|"delivering"\|"delivered"\|"failed"\|"returning"\|"returned","raw_status","description","occurred_at","direction":"FORWARD"\|"RETURN","return_request_id","redelivery_count"}` (missing `direction` defaults to `"FORWARD"`) |

### Event Consumer Rules
- `payment` consumes `order.delivered` (per-sub-order COD collection: posts Debit `cod_receivable` / Credit `escrow_liability`, transitions allocation `awaiting_collection -> held`, emits sub-order `payment.succeeded`, and marks parent COD payment `succeeded` once no allocations remain awaiting collection), `order.returned` (cancels uncollected COD allocation with zero ledger postings when `from_status = SHIPPING` and `escrow_status = awaiting_collection`; for `from_status = RETURN_REQUESTED`, `order` initiates the idempotent `CreateRefund` call via `order-return-refund:<order_id>`), `order.completed` (settles escrow into `seller_pending` with `pending_release`; 60s hold-release worker moves `pending_release -> released` after `PAYMENT_SELLER_HOLD_HOURS`), and `order.cancelled`.
- `order` consumes `payment.*` (`payment.succeeded`, `payment.failed`, `payment.expired`, `payment.refunded`) and ignores events whose `payment_id` does not match the checkout group's (or sub-order's) current active `payment_id` (events from superseded payments never change order status or overwrite `orders.payment_status`). On `payment.refunded`, `order` accumulates `refunded_vnd` and sets `orders.payment_status = "REFUNDED"` only once `refunded_vnd >= total_payable_vnd`. `order` also consumes `shipment.updated` for both `FORWARD` (order shipping/delivery/return lifecycle) and `RETURN` (return request `AWAITING_SHIPMENT -> IN_TRANSIT -> RECEIVED`).
- `notification` resolves seller owner via `UserService` (`USER_GRPC_ADDR`) and delivers in-app + email notifications to sellers for: new confirmed orders (`order.confirmed`), cancelled orders, new/escalated return requests (`order.return_updated`), returned goods received, failed delivery (`shipment.updated=failed`), returning/returned parcels (`returning|returned`), product moderation decisions (`product.reviewed`), and bank-account verification OTP emails (`user.shop_bank_account_otp`, never logging `otp` or placing it in Subject). For buyers, `shipment.updated` (`delivering|delivered|failed`) sends email in addition to in-app, `payment.refund_settled` notifies transfer completion, `promotion.voucher_expiring` delivers `PROMOTION` category reminder notifications, and `user.password_reset_requested` emails a link under `WEB_BASE_URL` without logging `reset_token`. If `shipment.updated` or `order.return_updated` arrives before `order.created` has populated order participants, the consumer returns an error so NATS redelivers without writing `processed_events` early.

---

## 6. Order State Machine, Return Workflow & Checkout Saga

### Valid Order Status Transitions
- `PENDING` → `CONFIRMED` (payment succeeded), `CANCELLED` (payment failed/expired or buyer cancel)
- `CONFIRMED` → `PREPARING` (seller confirms OR `POST /api/v1/seller/orders/{id}/ship` creates shipment with `tracking_code`; ship does NOT jump directly to `SHIPPING`), `CANCELLED` (buyer/seller cancel before carrier pickup, or auto-cancel job **only while still `CONFIRMED`** past `ORDER_SELLER_CONFIRM_HOURS` default 48h with `cancel_reason = "seller_confirm_timeout"` — checked inside row lock so already-`PREPARING` orders are never auto-cancelled)
- `PREPARING` → `SHIPPING` (on `shipment.updated` `FORWARD` with `delivering|delivered|failed|returning|returned`, emitting `order.shipped`; if `delivered` or `returned` arrives while the order is still `CONFIRMED|PREPARING`, it traverses through `SHIPPING` to the target status within the same transaction with both timeline events and outbox events), `CANCELLED` (before carrier pickup; if `tracking_code` exists, `order` calls `ShippingService.CancelShipment` synchronously first and returns `409` if the carrier has already picked up the parcel)
- `SHIPPING` → `DELIVERED` (`shipment.updated` `FORWARD` = `delivered`), `RETURNED` (`shipment.updated` `FORWARD` = `returned` after failed delivery)
- `DELIVERED` → `COMPLETED` (buyer confirms received OR auto-complete job after `ORDER_AUTO_COMPLETE_DAYS` default 7d when no return request exists, sharing the exact `BuyerConfirmReceived` completion path), `RETURN_REQUESTED` (buyer creates return request within 7d)
- `RETURN_REQUESTED` → `RETURNED` (when `return_requests.status` enters `REFUNDING`), `COMPLETED` (when `return_requests.status` enters `CLOSED`, via the same completion path as `BuyerConfirmReceived`)
- Carrier status mapping & monotonic transitions: `picked` maps to `delivering` (carrier has parcel, no longer cancellable); `ready_to_pick|picking` stay `picking` (still cancellable). Shipment status transitions are strictly monotonic (`CanTransitionStatus`): terminal statuses (`delivered`, `returned`, `cancelled`) never transition further, and backward/stale webhooks (e.g., `delivering`/`failed`/`returning` → `created`/`picking`, or `returning` → `delivering`) are ignored without emitting outbox events, while valid redelivery (`failed` → `delivering`) and repeated `failed` are allowed. Cancelled shipments (and `returned` `RETURN` shipments) do not block creating a new shipment for the same order.

### Return Request State Machine (`return_requests.status`)
At most one return request per order (`UNIQUE(order_id)`). Every transition locks the `return_requests` row `FOR UPDATE`, validates current status inside the transaction, appends a `return_events` row, and writes `order.return_updated` to `outbox_events`.

| From | Event / Trigger | To | Next Deadline & Timeout Action |
| --- | --- | --- | --- |
| *(new)* | Buyer `POST /orders/{id}/return` (`DELIVERED`, ≤ 7d) | `PENDING` | `ORDER_RETURN_SELLER_RESPONSE_DAYS` (3d) → auto-accept return (`AWAITING_SHIPMENT`) |
| `PENDING` | Seller `ACCEPT_REFUND` (refund without return) | `REFUNDING` | — |
| `PENDING` | Seller `ACCEPT_RETURN` (or `PENDING` timeout) | `AWAITING_SHIPMENT` (+ create `RETURN` shipment) | `ORDER_RETURN_SHIP_DAYS` (5d) set immediately on entry (refreshed when tracking attaches): expired with tracking → cancel `RETURN` shipment then `CLOSED` (if carrier already picked up, first sync live status via `GetShipment(RETURN)`, else extend `+24h` grace up to max 3 times then `ESCALATED`); expired without tracking → `ESCALATED` |
| `PENDING` | Seller `REJECT` (`note` required) | `REJECTED` | 3d → `CLOSED` |
| `REJECTED` | Buyer `EscalateReturn` | `ESCALATED` | — |
| `AWAITING_SHIPMENT` | `shipment.updated` `RETURN` = `delivering` | `IN_TRANSIT` | 14d → `ESCALATED` |
| `AWAITING_SHIPMENT` | Deadline expires without `return_tracking_code`, OR `shipment.updated` `RETURN` = `returned`/`cancelled` or `failed` (`lost`\|`damage`\|`destroy`) | `ESCALATED` | — |
| `IN_TRANSIT` | Transit deadline (14d) expires, OR `shipment.updated` `RETURN` = `returned`/`cancelled` or `failed` (`lost`\|`damage`\|`destroy`) | `ESCALATED` | — |
| `AWAITING_SHIPMENT` / `IN_TRANSIT` | `shipment.updated` `RETURN` = `delivered` | `RECEIVED` | `ORDER_RETURN_INSPECT_DAYS` (3d) → auto-confirm (`REFUNDING`) |
| `RECEIVED` | Seller `ConfirmReturnReceived` | `REFUNDING` | — |
| `RECEIVED` | Seller `DisputeReturnGoods` (`reason` required) | `ESCALATED` | — |
| `ESCALATED` | Admin `REQUEST_EVIDENCE` | `EVIDENCE_REQUESTED` | 3d → `CLOSED` |
| `EVIDENCE_REQUESTED` | Buyer `AddReturnEvidence` | `ESCALATED` | — |
| `ESCALATED` | Admin `REFUND` | `REFUNDING` | — |
| `ESCALATED` | Admin `RETURN_AND_REFUND` (rejected with `409` if `goods_received = true`, prior `RETURN` shipment is `delivering`\|`returning`\|`failed`\|`delivered`, or escalated from 14d `IN_TRANSIT` timeout) | `AWAITING_SHIPMENT` (+ create `RETURN` shipment) | `ORDER_RETURN_SHIP_DAYS` (5d) set immediately on entry (same expiry rules as above) |
| `ESCALATED` | Admin `REJECT` (cancels live `RETURN` shipment first unless terminal or escalated from 14d transit timeout) | `CLOSED` | — |
| `REFUNDING` | All idempotent post-commit side effects succeed | `REFUNDED` | — |

- **Entering `REFUNDING`**: Within a single DB transaction, transition order `RETURN_REQUESTED -> RETURNED`, write order timeline + `return_events`, and enqueue outbox events `order.returned` (`from_status = RETURN_REQUESTED`) and `order.return_updated`. Post-commit (retried by watchdog until all succeed): (1) `PaymentService.CreateRefund` for remaining `P - refunded_vnd` when `> 0` with idempotency key `order-return-refund:<order_id>` (applies to both `VIETQR` and collected `COD`), (2) `PromotionService.ReleasePromotionShop`, (3) `ProductService.RestockOrderItems` **only** when goods actually reached the seller (`goods_received = true` via `RECEIVED` and not on the disputed-wrong-goods branch). Only after all required side effects succeed does `return_requests.status` transition `REFUNDING -> REFUNDED`.
- **Entering `CLOSED` & `AWAITING_SHIPMENT` Expiry**: Transitions order `RETURN_REQUESTED -> COMPLETED` through the same completion path as `BuyerConfirmReceived` (`order.completed` outbox, reward coins, settlement). When `AWAITING_SHIPMENT` expires with a `return_tracking_code`, the watchdog calls `ShippingService.CancelShipment(RETURN)` **before** moving to `CLOSED`; if the carrier has already picked up the parcel (`FailedPrecondition`), the watchdog first syncs live status via `ShippingService.GetShipment(RETURN)` (service token) and otherwise extends the deadline (`+24h`) **at most 3 times** (`maxReturnDeadlineExtensions = 3`), after which it auto-escalates to `ESCALATED`. When `AWAITING_SHIPMENT` expires with no tracking code (return shipment creation never succeeded), it transitions to `ESCALATED` instead of `CLOSED`. Admin `REJECT` cancels any still-live `RETURN` shipment before closing (except when escalated from the 14d `IN_TRANSIT` timeout, where it closes with an audit note). Admin `RETURN_AND_REFUND` checks `ShippingService.GetShipment(RETURN)` (service token) and rejects (`FailedPrecondition` / `409`) whenever `goods_received = true`, the prior `RETURN` shipment is `delivering`/`returning`/`failed`/`delivered`, or `escalated_from_transit_timeout = true` (admin must choose `REFUND` or `REJECT`).
- **Return shipping fee**: Records `return_fee_vnd` and `return_fee_bearer` (`SELLER` by default when seller accepts; chosen by admin when resolving). Not posted to ledger in Wave 2a.

### Multi-Shop Checkout Saga (`OrderService.CreateCheckout`)
1. **Idempotency check**: Look up `(buyer_id, idempotency_key)` in `checkout_groups` along with a deterministic SHA-256 `request_hash` of the canonicalized checkout payload (excluding client-supplied `shop_program_item_id`, which is ignored). If found with a matching hash, return the saved response (`idempotent_replay=true`); if reused with a different payload, reject with `FailedPrecondition` / `409 Conflict` (`IDEMPOTENCY_KEY_CONFLICT`).
2. **Validate & Resolve**: Validate buyer (`UserService.ValidateUser`), fetch address (`UserService.GetAddress`), load items from `CartService.GetCart` (when `from_cart=true`) or `items[]`, fetch SKU details, effective category `commission_rate_bps`, and prices (`ProductService.GetSKUsBatch`), group items by `shop_id`, load shop shipping settings (`ShippingService.GetShopShippingSettings` for `default_weight_grams` fallback and `free_ship_*` rules), calculate shipping per shop (`ShippingService.CalculateFee`; COD surcharge at quote time is estimated from the pre-discount subtotal), and fetch active payment fee bps (`PaymentService.GetFeeConfig`, service token).
3. **Flash Sale & COD Risk Check**: If any line item uses a flash sale, require the flash sale window to be currently active (reject upcoming), disallow `COD` (`FailedPrecondition` / HTTP `409 FAILED_PRECONDITION`, `FLASH_SALE_COD_NOT_ALLOWED`), and use `ORDER_FLASH_PAYMENT_EXPIRY_MINUTES` (default `5`, capped by the earliest flash window `ends_at`). If `payment_method == COD`, enforce `total_payable_vnd <= ORDER_COD_MAX_VND` and `cod_rejected_count < ORDER_COD_MAX_REJECTED_COUNT`.
4. **Reserve Stock**: Call `ProductService.ReserveStock(order_id=checkout_group_id, all_items)` (atomic optimistic locking, idempotent).
5. **Reserve Promotions**: Call `PromotionService.ReservePromotion(order_id=checkout_group_id, ...)` (atomic Redis Lua for flash sale slots with `FlashSaleReservationItem.shop_id`, shop discount program per-user limits with `ShopDiscountProgramReservationItem`, voucher quotas, and coins; single item-price rule: flash wins over shop discount program; order of discount application: `item price (flash OR shop program) -> shop voucher -> shop freeship -> platform voucher -> platform freeship -> coins`). Each `ShopDiscountBreakdown.applied_shop_program_item_ids` reports the `shop_discount_program_item_id`s actually applied and reserved for that shop (lines exceeding `per_user_limit` are excluded, priced at regular price, and not reserved).
   - *On failure*: compensate step 4 via `ProductService.ReleaseStock(checkout_group_id)` and return error.
6. **Persist Checkout Group + Sub-Orders + Outbox** in one PostgreSQL transaction (running under a detached timeout context, locking `checkout_groups FOR UPDATE` and requiring `status = 'IN_PROGRESS'`):
   - One sub-order per `shop_id` sharing `checkout_group_id`, persisting `shop_program_discount_vnd`, `shop_freeship_discount_vnd`, `payment_fee_vnd`, `payment_fee_bps`, `checkout_groups.warnings_json`, and per-item `commission_bps` + `shop_program_item_id` (populated only for items present in `ShopDiscountBreakdown.applied_shop_program_item_ids`).
   - VietQR (`P > 0`): `status = PENDING`, `payment_status = PENDING`, `payment_expires_at = now + ORDER_FLASH_PAYMENT_EXPIRY_MINUTES` (default `5m`) if any flash sale item is present else `now + ORDER_PAYMENT_EXPIRY_MINUTES` (default `15m`); COD (`P > 0`): `status = CONFIRMED`, `payment_status = PENDING` (awaiting collection until `order.delivered`); zero-payable (`P = 0`): `status = CONFIRMED`, `payment_status = SUCCEEDED`.
   - **Shipping initialization guard**: while `checkout_groups.status == 'IN_PROGRESS'`, `SellerConfirmOrder` (`CONFIRMED -> PREPARING`) remains allowed, but `SellerShipOrder` and any transition moving an order to `SHIPPING` or beyond (`HandleShipmentUpdated` `FORWARD`, `BuyerConfirmReceived`, `RequestReturn`) are rejected with `FailedPrecondition` (`PAYMENT_NOT_INITIALIZED: đơn đang khởi tạo thanh toán, vui lòng thử lại sau ít phút`) before creating any shipment.
7. **Create Payment**: Call `PaymentService.CreatePayment(...)` under a detached caller context (preserving the buyer JWT while surviving client disconnect; including 0 VND orders where `amount_vnd = 0`) with `shop_allocations` (per-sub-order `order_id`, `shop_id`, `subtotal_vnd = R`, `commission_vnd = C_cat + C_pay <= R`, `shipping_fee_vnd = Ship`, `platform_discount_vnd = D`; `amount_vnd = sum(R + Ship - D)`).
   - *On ambiguous error*: probe `PaymentService.GetPayment(order_group_id)` under a detached service context. If found, reconcile any non-`PENDING` payment status (returning `Unavailable` without completing the group if reconciliation fails) and finalize forward. If `NotFound`: `CONFIRMED` (COD / 0 VND) orders return `Unavailable` without cancelling so the watchdog forward-recovers; `PENDING` (VietQR) orders cancel and compensate. On definite error, cancel sub-orders (`CancelCheckoutGroupOrders`, refusing non-`IN_PROGRESS` groups) and compensate (`CancelPayment`, `ReleasePromotion`, `ReleaseStock` / `RestockOrderItems`).
8. **Clear Cart**: Best-effort `CartService.ClearItems(buyer_id, ordered_sku_ids)`.
9. **Async Payment Failure / Expiry Compensation, Restock, Partial Release, COD Rejection & Watchdogs**:
   - On `payment.failed` or `payment.expired` (matching the checkout group's current `payment_id`): transition sub-orders `PENDING -> CANCELLED`, call `ProductService.ReleaseStock(checkout_group_id)` and `PromotionService.ReleasePromotion(checkout_group_id)`, write `order.cancelled` outbox event. Events for non-current/superseded `payment_id`s are ignored.
   - On `payment.succeeded` (matching the checkout group's current `payment_id`): transition sub-orders `PENDING -> CONFIRMED`, call `ProductService.ConfirmStock(checkout_group_id)`, write `order.confirmed` outbox event.
   - On `payment.refunded` (matching the sub-order's current `payment_id`): accumulate `refunded_vnd` and set `orders.payment_status = REFUNDED` only once `refunded_vnd >= total_payable_vnd`.
   - On `OrderService.RetryPayment`: rejects COD / non-VIETQR, verifies `payment_id` matches the group's current active payment and every sub-order is `PENDING` and unexpired, creates a superseding payment, and updates `orders.payment_id` and `orders.payment_expires_at` across all sub-orders in the group.
   - On cancellation of an order that already has a `tracking_code`: `order` calls `ShippingService.CancelShipment` synchronously first; if the carrier already picked up the parcel (`FailedPrecondition` / `409`), the order remains unchanged. Shipping's `order.cancelled` consumer stays as a safety net.
   - On cancellation of a single sub-order (while active siblings remain): call `ProductService.RestockOrderItems(order_id=sub_order_id, items)` (service-token or admin only, not exposed over REST; atomic stock increment, idempotent per `(order_id, sku_id)` via `stock_history`) and `PromotionService.ReleasePromotionShop(order_id=checkout_group_id, shop_id, reason)` (returns sub-order coins, shop voucher quota, flash sale slots; platform/freeship vouchers are released once all sub-orders in the group are released). When no active sibling remains, full group promotion release (`ReleasePromotion`) and stock release/restock run.
   - On `shipment.updated` (`FORWARD`, `returned`): transition `SHIPPING` (or `CONFIRMED|PREPARING -> SHIPPING -> RETURNED`) `-> RETURNED`, emit `order.returned` (`from_status = SHIPPING`) via transactional outbox, call `ProductService.RestockOrderItems(sub_order_id, items)` and `PromotionService.ReleasePromotionShop(checkout_group_id, shop_id, "order_returned")`; if COD delivery failed, call `UserService.RecordCODRejection(user_id, order_id, reason)`; if paid online (`VIETQR`, `SUCCEEDED`, `total_payable_vnd > 0`), call idempotent `PaymentService.CreateRefund`. On `shipment.updated` (`RETURN`), advance `return_requests` (`AWAITING_SHIPMENT -> IN_TRANSIT` on `delivering`, `AWAITING_SHIPMENT|IN_TRANSIT -> RECEIVED` on `delivered`).
   - On `BuyerConfirmReceived` (`DELIVERED -> COMPLETED`), auto-complete after `ORDER_AUTO_COMPLETE_DAYS`, or return `CLOSED`: write `order.completed` (carrying `coins_reward` computed on buyer actual merchandise payment `min(P, max(0, (G - F - P_shop - V_shop) - V_plat - X))`, excluding shipping and without deducting shop freeship `S_shop`; 0 VND order earns 0 coins) to the transactional outbox; `order` does **not** call `PaymentService.ReleaseEscrow`. `promotion` credits reward coins idempotently on `order.completed`, and `payment` settles into `seller_pending` (`pending_release`) and releases to `seller_available` via its 60s background hold worker after `PAYMENT_SELLER_HOLD_HOURS`.
   - **Watchdogs, Durable Recovery Flags & Scheduled Sweeps**:
     - **Stale `IN_PROGRESS` & Incomplete Checkout Groups, VietQR Expiry**:
       - `ScanStaleCheckoutGroups`: sweeps `IN_PROGRESS` groups with no persisted sub-orders older than `ORDER_STALE_CHECKOUT_AFTER` (default `5m`) as well as groups with `needs_cleanup = TRUE` (`IN_PROGRESS` or `FAILED`, no sub-orders). Uses claim-first two-statement `READ COMMITTED` serialization (`SELECT checkout_groups FOR UPDATE`, then fresh-snapshot `NOT EXISTS (orders)` check, flipping `status = 'FAILED', needs_cleanup = TRUE` before calling `ReleasePromotion` and `ReleaseStock`, and clearing `needs_cleanup = FALSE` only after both succeed).
       - `ScanIncompleteCheckoutGroups`: recovers `IN_PROGRESS` groups that already have persisted sub-orders past `ORDER_STALE_CHECKOUT_AFTER`: probes `PaymentService.GetPayment(order_group_id)` first; if `NotFound`, forward-recovers live (non-`CANCELLED`) `CONFIRMED`/`PREPARING`/`SHIPPING` orders via service-token `PaymentService.CreatePayment` (`idempotency_key = "checkout:" + order_group_id`, `supersede_previous = false`, excluding `CANCELLED` orders from allocations and group total), refuses legacy `DELIVERED`/`COMPLETED`/`RETURN_REQUESTED`/`RETURNED` orders (logs Error for manual reconciliation without closing the group), closes all-`CANCELLED` groups `FAILED`, and cancels/compensates purely unpaid `PENDING` VietQR groups.
       - `ScanExpiredPendingOrders`: sweeps expired `PENDING` VietQR orders after probing `PaymentService.GetPayment` so late `SUCCEEDED`/`REFUNDED` payments are never downgraded to `EXPIRED`.
     - **Pending Stock Confirmation (`checkout_groups.pending_stock_confirm`)**: set atomically when orders confirm (`COD`, 0-VND, or VietQR `payment.succeeded`) and cleared only after `ProductService.ConfirmStock` succeeds (or all sub-orders are cancelled); retried by `ScanPendingStockConfirms`.
     - **Pending Cancel Compensation (`orders.pending_cancel_compensation`)**: set on order cancellation and cleared only after all post-commit compensations (`ReleaseStock`/`RestockOrderItems`, `ReleasePromotion`/`ReleasePromotionShop`, `CreateRefund`, `CancelPayment`) succeed; retried by `ScanPendingCancelCompensations`.
     - **Orphan Shipment Cancel & Manual Review (`orders.pending_shipment_cancel`, `orders.orphan_shipment_needs_manual_review`)**: retries `CancelShipment(FORWARD)` on cancelled orders with an orphaned shipment (`ScanPendingOrphanShipmentCancels`); if the carrier already picked up (`FailedPrecondition`), records an audit timeline note and flags `orphan_shipment_needs_manual_review = true` for manual admin handling.
     - **Seller Confirm Timeout**: auto-cancels orders **only while still `CONFIRMED`** past `ORDER_SELLER_CONFIRM_HOURS` (default `48`) via the standard cancel path with `cancel_reason = "seller_confirm_timeout"`.
     - **Auto-Complete Delivered Orders**: transitions `DELIVERED` orders past `ORDER_AUTO_COMPLETE_DAYS` (default `7`) with no return request to `COMPLETED` via the shared completion path.
     - **Return Deadline & `REFUNDING` Watchdog**: scans expired `return_requests` deadlines (`PENDING`, `AWAITING_SHIPMENT`, `IN_TRANSIT`, `REJECTED`, `RECEIVED`, `EVIDENCE_REQUESTED`) via randomized batch `SELECT` (`ORDER BY random()` to prevent starvation behind failing rows; `FOR UPDATE SKIP LOCKED` is used only by the outbox relay) and executes each transition under a per-row `FOR UPDATE` lock (`orders` + `return_requests`) that re-validates status and deadline inside the transaction; retries idempotent `CreateReturnShipment` for `AWAITING_SHIPMENT` without tracking, and retries incomplete `REFUNDING` post-commit side effects until `REFUNDED`.

---

## 7. Environment Variable Convention

All defaults for local dev/sandbox live in `.env.example` and are injected by `docker-compose.yml`:
- Common to every service: `SERVICE_NAME`, `GRPC_PORT`, `HTTP_PORT`, `LOG_LEVEL`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `JWT_SECRET`, `SERVICE_JWT_SECRET`.
- Datastores (only for services that own them): `DATABASE_URL` (pointing exclusively to `<service>_db`), `REDIS_ADDR`, `NATS_URL`, `NATS_STREAM=NEXUSMART` (including `product` for `product.reviewed` transactional outbox).
- Upstream addresses: `USER_GRPC_ADDR`, `PRODUCT_GRPC_ADDR` (used by `gateway`, `cart`, `order`, `promotion`), `CART_GRPC_ADDR`, `PROMOTION_GRPC_ADDR`, `ORDER_GRPC_ADDR`, `PAYMENT_GRPC_ADDR`, `PAYMENT_HTTP_URL`, `SHIPPING_GRPC_ADDR` (used by `gateway`, `user`, `order`, `promotion`), `SHIPPING_HTTP_URL`, `NOTIFICATION_GRPC_ADDR`.
- Business rule & security timers/keys: `RATE_LIMIT_AUTH_PER_MIN=10`, `RATE_LIMIT_WEBHOOK_PER_MIN=600`, `ORDER_PAYMENT_EXPIRY_MINUTES=15`, `ORDER_FLASH_PAYMENT_EXPIRY_MINUTES=5`, `ORDER_AUTO_COMPLETE_DAYS=7`, `ORDER_SELLER_CONFIRM_HOURS=48`, `ORDER_RETURN_SELLER_RESPONSE_DAYS=3`, `ORDER_RETURN_SHIP_DAYS=5`, `ORDER_RETURN_INSPECT_DAYS=3`, `PAYMENT_SELLER_HOLD_HOURS=72`, `USER_BANK_PAYOUT_LOCK_HOURS=24`, `PROMOTION_VOUCHER_EXPIRY_NOTICE_HOURS=24`, `SHIPPING_MAX_REDELIVERY=3`, `SHIPPING_CONFIG_KEY` (32-byte base64 AES key for encrypting carrier API tokens; dev-only default in `.env.example`/`docker-compose.yml`), `SHIPPING_LABEL_BASE_URL` (base URL for browser-accessible shipping label links; compose defaults to `${WEB_BASE_URL:-http://localhost:${WEB_PORT:-3000}}/api/gw/shipping/labels`), `WEB_BASE_URL` (notification email link base URL), `OTEL_HEALTH_PORT=13133` (collector `health_check` extension; `infra/otel-collector-config.yaml` scrapes `/metrics` from all 9 services).
- Sandbox / Mock toggles: `VIETQR_MODE=mock`, `PAYMENT_WEBHOOK_SECRET`, `GHN_MODE=mock`, `SHIPPING_WEBHOOK_SECRET`, `SMTP_HOST=mailpit`, `SMTP_PORT=1025`.
