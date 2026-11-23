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
| `otel-collector` | `otel/opentelemetry-collector-contrib:0.108.0` | `4317` (gRPC), `4318` (HTTP), `8889` (Prom) | OTLP trace & metric collector |

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
- `Authorization: Bearer <access_token>` — Required on authenticated routes. HS256 JWT (TTL `15m`) with claims: `sub` (user ID), `role` (`buyer`|`seller`|`admin`|`super_admin`), `shop_id` (empty unless seller), `jti`, `iat`, `exp`.
- `Idempotency-Key: <unique-key>` — Required on `POST /api/v1/checkout` and `POST /api/v1/admin/payments/{id}/refund`. Replays return the original result without duplicate side effects.
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
| `POST /api/v1/auth/verify-otp` | Public | `{"email","otp"}` | `200` `{"verified":true,"user":{...}}` |
| `POST /api/v1/auth/resend-otp` | Public | `{"email"}` | `200` `{"sent":true}` |
| `POST /api/v1/auth/login` | Public | `{"email","password"}` | `200` `{"access_token","refresh_token","expires_in_seconds":900,"user":{...}}` |
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
| `GET /api/v1/admin/users` | `admin`, `super_admin` | Query: `query`, `role`, `status`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |
| `PATCH /api/v1/admin/users/{id}/status` | `admin`, `super_admin` | `{"status":"ACTIVE"\|"LOCKED","reason"}` | `200` `{"user":{...}}` |
| `PATCH /api/v1/admin/users/{id}/role` | `admin`, `super_admin` | `{"role":"buyer"\|"seller"\|"admin","reason"}` | `200` `{"user":{...}}` |
| `GET /api/v1/admin/shops` | `admin`, `super_admin` | Query: `status`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |
| `POST /api/v1/admin/shops/{id}/review` | `admin`, `super_admin` | `{"decision":"APPROVE"\|"REJECT"\|"REQUEST_INFO","reason"}` | `200` `{"shop":{...}}` |
| `GET /api/v1/admin/audit-logs` | `admin`, `super_admin` | Query: `actor_id`, `target_type`, `target_id`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |

### 4.2 Categories, Products & Inventory (`product` service)
| Method & Path | Auth / RBAC | Request JSON | Response JSON |
| --- | --- | --- | --- |
| `GET /api/v1/categories` | Public | Query: `parent_id` | `200` `{"categories":[...]}` |
| `POST /api/v1/admin/categories` | `admin`, `super_admin` | `{"name","slug","parent_id","commission_rate_bps","sort_order"}` | `201` `{"category":{...}}` |
| `PUT /api/v1/admin/categories/{id}` | `admin`, `super_admin` | Same as create | `200` `{"category":{...}}` |
| `DELETE /api/v1/admin/categories/{id}` | `admin`, `super_admin` | — | `200` `{"ok":true}` |
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
| `POST /api/v1/admin/products/{id}/review` | `admin`, `super_admin` | `{"decision":"APPROVE"\|"REJECT","reason"}` | `200` `{"product":{...}}` |

### 4.3 Shopping Cart (`cart` service)
| Method & Path | Auth / RBAC | Request JSON | Response JSON |
| --- | --- | --- | --- |
| `GET /api/v1/cart` | Any authenticated | — | `200` `{"cart":{"user_id","shop_groups":[...],"total_items","total_vnd"}}` |
| `POST /api/v1/cart/items` | Any authenticated | `{"sku_id","quantity"}` | `200` `{"cart":{...},"clamped_to_stock":false}` |
| `PATCH /api/v1/cart/items/{sku_id}` | Any authenticated | `{"quantity"}` | `200` `{"cart":{...},"clamped_to_stock":false}` |
| `DELETE /api/v1/cart/items/{sku_id}` | Any authenticated | — | `200` `{"cart":{...}}` |
| `DELETE /api/v1/cart` | Any authenticated | Optional `{"sku_ids":[...]}` | `200` `{"ok":true}` |

### 4.4 Vouchers, Flash Sales & Coins (`promotion` service)
| Method & Path | Auth / RBAC | Request JSON | Response JSON |
| --- | --- | --- | --- |
| `GET /api/v1/vouchers` | Public / Optional auth | Query: `shop_id`, `scope`, `order_amount_vnd` | `200` `{"vouchers":[...]}` |
| `POST /api/v1/vouchers/{id}/save` | Any authenticated | Optional `{"code"}` | `200` `{"saved":true,"voucher":{...}}` |
| `GET /api/v1/seller/vouchers` | `seller` | — (forces `scope=SHOP`, `shop_id` from JWT) | `200` `{"vouchers":[...]}` |
| `POST /api/v1/seller/vouchers` | `seller` | `{"code","name","discount_type":"FIXED"\|"PERCENT","value_vnd","discount_percent","max_discount_vnd","min_order_vnd","quota","per_user_limit","budget_vnd","starts_at","ends_at"}` (forces `scope=SHOP`, `shop_id` from JWT) | `201` `{"voucher":{...}}` |
| `POST /api/v1/admin/vouchers` | `admin`, `super_admin` | Same + `scope:"PLATFORM"\|"FREESHIP"` | `201` `{"voucher":{...}}` |
| `GET /api/v1/flash-sales` | Public | Query: `include_upcoming` | `200` `{"flash_sales":[...]}` |
| `POST /api/v1/admin/flash-sales` | `admin`, `super_admin` | `{"name","starts_at","ends_at"}` | `201` `{"flash_sale":{...}}` |
| `POST /api/v1/seller/flash-sales/{id}/items` | `seller` | `{"sku_id","flash_price_vnd","total_slots","per_user_limit"}` | `201` `{"item":{...}}` (gateway verifies `sku_id` via `Product.GetSKUsBatch` and checks `sku.shop_id == JWT shop_id`; `404` if SKU not found, `403` `PERMISSION_DENIED` if SKU belongs to another shop) |
| `POST /api/v1/admin/flash-sale-items/{item_id}/approve` | `admin`, `super_admin` | `{"approve":true,"reason"}` | `200` `{"item":{...}}` |
| `GET /api/v1/coins/balance` | Any authenticated | — | `200` `{"user_id","balance":5000,"recent_transactions":[...]}` |
| `POST /api/v1/admin/coins/credit` | `admin`, `super_admin` | `{"user_id","amount","reason","reference_id"}` | `200` `{"user_id","balance","already_credited":false}` |
| `POST /api/v1/promotions/preview` | Any authenticated | `{"shop_items":[...],"shop_voucher_ids":{"shop1":"v1"},"platform_voucher_id","freeship_voucher_id","coins_to_use"}` | `200` `{"breakdown":{...}}` |

### 4.5 Checkout & Orders (`order` service)
| Method & Path | Auth / RBAC | Request JSON | Response JSON |
| --- | --- | --- | --- |
| `POST /api/v1/checkout` | Any authenticated + `Idempotency-Key` header | `{"address_id","from_cart":true,"items":[{"sku_id","quantity","flash_sale_item_id"}],"shop_voucher_ids":{},"platform_voucher_id":"","freeship_voucher_id":"","coins_to_use":0,"payment_method":"COD"\|"VIETQR","shipping_choices":[{"shop_id","carrier_code","service_code"}]}` | `201` (or `200` on replay) `{"checkout_group_id","orders":[...],"payment_id","payment_method","payment_status","qr_url","qr_code","payment_expires_at","total_payable_vnd","idempotent_replay":false}` |
| `GET /api/v1/orders` | Any authenticated (buyer scope) | Query: `status`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |
| `GET /api/v1/orders/{id}` | Buyer owner / Seller owner / Admin | — | `200` `{"order":{...}}` (`403` for non-owner) |
| `GET /api/v1/orders/{id}/timeline` | Buyer owner / Seller owner / Admin | — | `200` `{"events":[...]}` |
| `POST /api/v1/orders/{id}/cancel` | Buyer owner / Seller owner / Admin | `{"reason"}` | `200` `{"order":{...}}` |
| `POST /api/v1/orders/{id}/confirm-received` | Buyer owner | — | `200` `{"order":{...}}` |
| `POST /api/v1/orders/{id}/return` | Buyer owner | `{"reason","evidence_urls":[...]}` | `200` `{"order":{...},"return_request_id"}` |
| `GET /api/v1/seller/orders` | `seller` | Query: `status`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |
| `GET /api/v1/seller/orders/{id}` | `seller` (shop owner) | — | `200` `{"order":{...}}` (`403` for another shop's order) |
| `POST /api/v1/seller/orders/{id}/confirm` | `seller` (shop owner) | — | `200` `{"order":{...}}` |
| `POST /api/v1/seller/orders/{id}/ship` | `seller` (shop owner) | `{"carrier_code","service_code"}` | `200` `{"order":{...},"tracking_code","label_url"}` |
| `GET /api/v1/admin/orders` | `admin`, `super_admin` | Query: `buyer_id`, `shop_id`, `status`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |

### 4.6 Payments, Ledger & Seller Finance (`payment` service)
| Method & Path | Auth / RBAC | Request JSON | Response JSON |
| --- | --- | --- | --- |
| `GET /api/v1/payments/{id}` | Buyer owner / Admin | — | `200` `{"payment":{...}}` |
| `POST /api/v1/payments/{id}/retry` | Buyer owner (optional `Idempotency-Key`) | `{"method":"VIETQR"}` | `200` `{"payment":{...}}` (gateway orchestrates via `OrderService.RetryPayment`, which verifies the order group is still awaiting payment and unexpired, creates a superseding payment, and syncs `orders.payment_id` + `payment_expires_at` across all sub-orders; UC-B25) |
| `GET /api/v1/payments/{id}/events` | Buyer owner / Admin — gateway checks payment owner before proxying; `payment:8086/payments/{id}/events` verifies end-user JWT and read access directly (`401`/`403`/`404`; service tokens rejected with `403`; direct calls also allow sellers with a shop allocation) | — | `text/event-stream` (`payment.pending` / `payment.succeeded` / `payment.expired` / `payment.failed` / `payment.refunded`) |
| `GET /api/v1/seller/balance` | `seller` | — | `200` `{"shop_id","pending_vnd","available_vnd","total_earned_vnd"}` |
| `GET /api/v1/admin/ledger/summary` | `admin`, `super_admin` | — | `200` `{"total_debits_vnd","total_credits_vnd","is_balanced":true,"accounts":[...]}` |
| `POST /api/v1/admin/payments/{id}/refund` | `admin`, `super_admin` + `Idempotency-Key` | `{"order_id","amount_vnd","reason"}` | `200` `{"refund":{...},"already_existed":false}` |
| `POST /api/v1/admin/payments/reconcile` | `admin`, `super_admin` | `{"date":"2026-11-20"}` | `200` `{"run_id","matched_count","missing_local_count","missing_provider_count","amount_mismatch_count","items":[...]}` |

- Direct HTTP routes on `payment:8086` under `/internal/*` (`GET /internal/ledger/summary`, `GET /internal/reconciliation/queue`, `POST /internal/reconciliation/run`) are not proxied by the gateway and require an end-user `admin`/`super_admin` JWT (`401`/`403`; service tokens rejected with `403`).
- **Allocation, Ledger & COD Semantics**:
  - For each sub-order (shop) in a checkout group, let `G = subtotal_vnd`, seller-borne discounts `F = flash_sale_discount_vnd` and `V_shop = shop_voucher_discount_vnd`, platform-borne discounts `V_plat = platform_voucher_discount_vnd`, `S_free = freeship_discount_vnd`, `X = coins_discount_vnd`, and full pre-freeship shipping `Ship = shipping_fee_vnd`.
  - Seller merchandise revenue `R = G - F - V_shop`; platform subsidy `D = V_plat + S_free + X`; buyer payable `P = R + Ship - D >= 0` (`P + D = R + Ship`); commission `C` is capped at `R`.
  - `ShopAllocation` carries `subtotal_vnd = R`, `commission_vnd = C`, `shipping_fee_vnd = Ship`, `platform_discount_vnd = D`; `Payment.amount_vnd = sum(R + Ship - D) = sum(P)`.
  - Zero-payable orders (`P = 0`) still create a payment (`amount_vnd = 0`, immediately `status = succeeded`, `escrow_status = held`, no QR, no `gateway_clearing` posting) with full `(R, C, Ship, D)` allocations so seller settlement credits `R - C`.
  - On `order.completed`, escrow settlement writes balanced double-entry postings: Debit `escrow_liability` (`P` collected minus refunded), Debit `platform_promo_expense` (`D`, `EXPENSE` account), Credit `seller_pending:<shop>` (`R - C` minus refunded merchandise portion), Credit `platform_fee_revenue` (`C`), Credit `shipping_fee_payable` (`Ship`).
  - **COD**: created in `status = pending` with sub-order allocations in `escrow_status = awaiting_collection` (zero ledger postings, no `payment.succeeded` emitted at checkout). On `order.delivered`, `payment` posts Debit `cod_receivable` / Credit `escrow_liability` for that sub-order's `P`, transitions its allocation `awaiting_collection -> held`, emits `payment.succeeded` for that sub-order, and transitions the parent COD payment to `succeeded` once no `awaiting_collection` allocations remain. Cancel before delivery (`order.cancelled`) or failed delivery (`order.returned` with `from_status = SHIPPING`) marks the uncollected allocation `cancelled` with zero ledger postings and transitions the parent COD payment to `cancelled` (or `succeeded` if sibling allocations were already delivered and none remain awaiting collection). Refunds are only permitted for collected payments (VIETQR `succeeded` or delivered COD).
  - **Refunds & Late Webhooks**: total refunds per sub-order cannot exceed buyer-paid `P` for that sub-order; post-settlement refunds debit `seller_pending` then `seller_available` (`409` if insufficient) without overdrawing `escrow_liability`. A late `SUCCEEDED` webhook for a `superseded` payment or cancelled order group auto-creates a `pending` `late_payment` refund without emitting `payment.succeeded`/`payment.refunded`, never mutating order state or overwriting `orders.payment_status`.

### 4.7 Shipping & Administrative Addresses (`shipping` service)
| Method & Path | Auth / RBAC | Request JSON | Response JSON |
| --- | --- | --- | --- |
| `GET /api/v1/shipping/provinces` | Public | Query: `query` | `200` `{"provinces":[{"code","name"}]}` (34 provinces) |
| `GET /api/v1/shipping/provinces/{code}/wards` | Public | Query: `query` | `200` `{"wards":[{"code","province_code","name"}]}` |
| `POST /api/v1/shipping/addresses/map-legacy` | Public | `{"legacy_province","legacy_district","legacy_ward"}` | `200` `{"found":true,"province_code","province_name","ward_code","ward_name",...}` |
| `POST /api/v1/shipping/calculate-fee` | Any authenticated | `{"shop_id","from_province_code","from_ward_code","to_province_code","to_ward_code","weight_grams","cod_amount_vnd"}` | `200` `{"quotes":[{"carrier_code","carrier_name","service_code","service_name","fee_vnd","estimated_days","is_fallback"}]}` |
| `GET /api/v1/shipping/shipments/{order_id}` | Buyer owner / Seller owner / Admin | — | `200` `{"shipment":{...},"events":[...]}` |

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
| `OrderService.RetryPayment` | `false` (buyer JWT via gateway) | `payment_id`, `payment_method` (`OrderPaymentMethod`), `idempotency_key` | `checkout_group_id`, `payment_id`, `payment_expires_at` | Validates order group is pending & unexpired, creates superseding payment, updates `orders.payment_id` and `payment_expires_at` |
| `PromotionService.ReleasePromotionShop` | `true` (service token / admin JWT) | `order_id` (checkout group ID), `shop_id`, `reason` | `released`, `already_released`, `coins_returned`, `shop_voucher_released`, `flash_sale_quantity_released`, `platform_voucher_released` | Idempotent per `(order_id, shop_id)` partial promotion release on single sub-order cancel or `RETURNED` (uses `FlashSaleReservationItem.shop_id` captured during `ReservePromotion`); platform voucher is released only when all sub-orders in the group are cancelled |
| `UserService.RecordCODRejection` | `true` (service token only; handler rejects end-user JWTs) | `user_id`, `order_id`, `reason` | `cod_rejected_count`, `already_recorded` | Idempotent per `order_id` increment of buyer `cod_rejected_count` on COD delivery rejection/return |
| `UserService.WriteAuditLog` | `true` (admin JWT / service token) | `actor_id`, `actor_role`, `action`, `target_type`, `target_id`, `before_json`, `after_json`, `reason`, `ip_address` | `audit_log` | Centralized admin audit trail written by gateway or services (no anonymous access) |
| `UserService.ValidateUser` | `false` (forwarded buyer JWT) | `user_id` | `valid`, `role`, `status`, `shop_id`, `phone_verified`, `cod_rejected_count` | Buyer validation and COD risk check during `OrderService.CreateCheckout` (no anonymous access) |
| `PromotionService.CreditCoins` | `true` (admin JWT / service token) | `user_id`, `amount`, `reason`, `reference_id` | `user_id`, `balance`, `already_credited` | Idempotent coin credit by `reference_id` (admin or service token only; no anonymous access) |

- New protobuf fields supporting these contracts:
  - `nexusmart.payment.v1.ShopAllocation.platform_discount_vnd` (`int64`, field 6): platform-borne discount `D` for the sub-order (`amount_vnd = sum(subtotal_vnd + shipping_fee_vnd - platform_discount_vnd)`).
  - `nexusmart.promotion.v1.FlashSaleReservationItem.shop_id` (`string`, field 4): shop owning the reserved flash sale SKU so `ReleasePromotionShop` can release slots per shop.

---

## 5. NATS JetStream Event Contracts

### Stream & DLQ Configuration
- **Primary Stream**: `NEXUSMART`
  - Subjects: `user.>`, `order.>`, `payment.>`, `shipment.>`
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
| `user.registered` | `user` | `notification` | `{"user_id","email","full_name","otp","expires_at"}` |
| `user.shop_reviewed` | `user` | `notification` | `{"shop_id","owner_user_id","shop_name","decision":"APPROVE"\|"REJECT"\|"REQUEST_INFO","reason"}` |
| `order.created` | `order` | `notification` | `{"checkout_group_id","order_id","order_code","buyer_id","shop_id","payment_method","total_payable_vnd"}` |
| `order.confirmed` | `order` | `notification` | `{"order_id","order_code","buyer_id","shop_id","payment_method","total_payable_vnd"}` |
| `order.cancelled` | `order` | `notification`, `shipping`, `payment` | `{"order_id","order_code","buyer_id","shop_id","reason","was_paid":true\|false}` |
| `order.shipped` | `order` | `notification` | `{"order_id","order_code","buyer_id","shop_id","carrier_code","tracking_code"}` |
| `order.delivered` | `order` | `notification`, `payment` | `{"order_id","order_code","buyer_id","shop_id","tracking_code"}` |
| `order.returned` | `order` | `payment`, `notification` | `{"order_id","order_code","checkout_group_id","buyer_id","shop_id","payment_method","from_status":"SHIPPING"\|"RETURN_REQUESTED","total_payable_vnd","reason","was_paid"}` |
| `order.completed` | `order` | `notification`, `promotion`, `payment` | `{"order_id","order_code","buyer_id","shop_id","subtotal_vnd","commission_vnd","total_payable_vnd","coins_reward"}` |
| `payment.succeeded` | `payment` | `order`, `notification` | `{"payment_id","order_group_id","order_id","user_id","amount_vnd","method","provider_tx_id"}` |
| `payment.failed` | `payment` | `order`, `notification` | `{"payment_id","order_group_id","order_id","user_id","reason"}` |
| `payment.expired` | `payment` | `order`, `notification` | `{"payment_id","order_group_id","order_id","user_id","reason"}` |
| `payment.refunded` | `payment` | `order`, `notification` | `{"refund_id","payment_id","order_id","user_id","amount_vnd","reason"}` |
| `shipment.updated` | `shipping` | `order`, `notification` | `{"shipment_id","order_id","shop_id","carrier_code","tracking_code","status":"picking"\|"delivering"\|"delivered"\|"failed"\|"returning"\|"returned","raw_status","description","occurred_at"}` |

### Event Consumer Rules
- `payment` consumes `order.delivered` (per-sub-order COD collection: posts Debit `cod_receivable` / Credit `escrow_liability`, transitions allocation `awaiting_collection -> held`, emits sub-order `payment.succeeded`, and marks parent COD payment `succeeded` once no allocations remain awaiting collection) and `order.returned` (cancels uncollected COD allocation with zero ledger postings when `from_status = SHIPPING` and `escrow_status = awaiting_collection`; post-delivery return reversal for `from_status = RETURN_REQUESTED` is deferred to Wave 2), in addition to `order.completed` and `order.cancelled`.
- `order` consumes `payment.*` (`payment.succeeded`, `payment.failed`, `payment.expired`, `payment.refunded`) and ignores events whose `payment_id` does not match the checkout group's (or sub-order's) current active `payment_id` (events from superseded payments never change order status or overwrite `orders.payment_status`). On `payment.refunded`, `order` accumulates `refunded_vnd` and sets `orders.payment_status = "REFUNDED"` only once `refunded_vnd >= total_payable_vnd`.

---

## 6. Order State Machine & Checkout Saga

### Valid Order Status Transitions
- `PENDING` → `CONFIRMED` (payment succeeded), `CANCELLED` (payment failed/expired or buyer cancel)
- `CONFIRMED` → `PREPARING` (seller confirms), `SHIPPING` (seller ships directly), `CANCELLED` (buyer/seller cancel before pickup)
- `PREPARING` → `SHIPPING` (seller ships / shipment picked up), `CANCELLED` (before carrier pickup)
- `SHIPPING` → `DELIVERED` (`shipment.updated=delivered`), `RETURNED` (`shipment.updated=returned` after failed delivery)
- `DELIVERED` → `COMPLETED` (buyer confirms received; **7d auto-complete job is NOT YET IMPLEMENTED**), `RETURN_REQUESTED` (buyer requests return within 7d)
- `RETURN_REQUESTED` → `RETURNED` (`shipment.updated=returned`), `COMPLETED` (allowed in state machine, though `BuyerConfirmReceived` strictly requires `DELIVERED`; **return rejection/close RPC is NOT YET IMPLEMENTED**)

### Multi-Shop Checkout Saga (`OrderService.CreateCheckout`)
1. **Idempotency check**: Look up `(buyer_id, idempotency_key)` in `checkout_groups` along with a deterministic SHA-256 `request_hash` of the canonicalized checkout payload. If found with a matching hash, return the saved response (`idempotent_replay=true`); if reused with a different payload, reject with `FailedPrecondition` / `409 Conflict` (`IDEMPOTENCY_KEY_CONFLICT`).
2. **Validate & Resolve**: Validate buyer (`UserService.ValidateUser`), fetch address (`UserService.GetAddress`), load items from `CartService.GetCart` (when `from_cart=true`) or `items[]`, fetch SKU details & prices (`ProductService.GetSKUsBatch`), group items by `shop_id`, calculate shipping per shop (`ShippingService.CalculateFee`).
3. **COD Risk Check**: If `payment_method == COD`, enforce `total_payable_vnd <= ORDER_COD_MAX_VND` and `cod_rejected_count < ORDER_COD_MAX_REJECTED_COUNT`.
4. **Reserve Stock**: Call `ProductService.ReserveStock(order_id=checkout_group_id, all_items)` (atomic optimistic locking, idempotent).
5. **Reserve Promotions**: Call `PromotionService.ReservePromotion(order_id=checkout_group_id, ...)` (atomic Redis Lua for flash sale slots with `FlashSaleReservationItem.shop_id`, voucher quotas, and coins; order of discount application: `shop voucher -> platform voucher -> freeship -> coins`).
   - *On failure*: compensate step 4 via `ProductService.ReleaseStock(checkout_group_id)` and return error.
6. **Persist Checkout Group + Sub-Orders + Outbox** in one PostgreSQL transaction:
   - One sub-order per `shop_id` sharing `checkout_group_id`.
   - VietQR (`P > 0`): `status = PENDING`, `payment_status = PENDING`, `payment_expires_at = now + 15m`; COD (`P > 0`): `status = CONFIRMED`, `payment_status = PENDING` (awaiting collection until `order.delivered`); zero-payable (`P = 0`): `status = CONFIRMED`, `payment_status = SUCCEEDED`.
7. **Create Payment**: Call `PaymentService.CreatePayment(...)` (including 0 VND orders where `amount_vnd = 0`) with `shop_allocations` (per-sub-order `order_id`, `shop_id`, `subtotal_vnd = R`, `commission_vnd = C`, `shipping_fee_vnd = Ship`, `platform_discount_vnd = D`; `amount_vnd = sum(R + Ship - D)`).
   - *On failure*: mark sub-orders `CANCELLED`, compensate via `PromotionService.ReleasePromotion` and `ProductService.ReleaseStock`.
8. **Clear Cart**: Best-effort `CartService.ClearItems(buyer_id, ordered_sku_ids)`.
9. **Async Payment Failure / Expiry Compensation, Restock, Partial Release, COD Rejection & Watchdogs**:
   - On `payment.failed` or `payment.expired` (matching the checkout group's current `payment_id`): transition sub-orders `PENDING -> CANCELLED`, call `ProductService.ReleaseStock(checkout_group_id)` and `PromotionService.ReleasePromotion(checkout_group_id)`, write `order.cancelled` outbox event. Events for non-current/superseded `payment_id`s are ignored.
   - On `payment.succeeded` (matching the checkout group's current `payment_id`): transition sub-orders `PENDING -> CONFIRMED`, call `ProductService.ConfirmStock(checkout_group_id)`, write `order.confirmed` outbox event.
   - On `payment.refunded` (matching the sub-order's current `payment_id`): accumulate `refunded_vnd` and set `orders.payment_status = REFUNDED` only once `refunded_vnd >= total_payable_vnd`.
   - On `OrderService.RetryPayment`: rejects COD / non-VIETQR, verifies `payment_id` matches the group's current active payment and every sub-order is `PENDING` and unexpired, creates a superseding payment, and updates `orders.payment_id` and `orders.payment_expires_at` across all sub-orders in the group.
   - On cancellation of a single sub-order (while active siblings remain): call `ProductService.RestockOrderItems(order_id=sub_order_id, items)` (service-token or admin only, not exposed over REST; atomic stock increment, idempotent per `(order_id, sku_id)` via `stock_history`) and `PromotionService.ReleasePromotionShop(order_id=checkout_group_id, shop_id, reason)` (returns sub-order coins, shop voucher quota, flash sale slots; platform/freeship vouchers are released once all sub-orders in the group are released). When no active sibling remains, full group promotion release (`ReleasePromotion`) and stock release/restock run.
   - On `shipment.updated=returned`: transition `SHIPPING|RETURN_REQUESTED -> RETURNED`, emit `order.returned` via transactional outbox, call `ProductService.RestockOrderItems(sub_order_id, items)` and `PromotionService.ReleasePromotionShop(checkout_group_id, shop_id, "order_returned")`; if COD delivery failed (`from_status == SHIPPING`), call `UserService.RecordCODRejection(user_id, order_id, reason)`; if paid online (`VIETQR`, `SUCCEEDED`, `total_payable_vnd > 0`), call idempotent `PaymentService.CreateRefund`.
   - On `BuyerConfirmReceived` (`DELIVERED -> COMPLETED`): write `order.completed` (carrying `coins_reward` computed on buyer actual merchandise payment `max(0, R - V_plat - X)`, excluding shipping; 0 VND order earns 0 coins) to the transactional outbox and call best-effort `PaymentService.ReleaseEscrow(order_id)`; `promotion` credits reward coins idempotently when consuming `order.completed` (rather than a synchronous `PromotionService.CreditCoins` RPC).
   - **Stale `IN_PROGRESS` Checkout Group Sweep**: runs on the same `RunExpiryWatchdog` ticker as expired VietQR `PENDING` order checks (`ORDER_STALE_CHECKOUT_AFTER`, default `5m`). Sweeps `IN_PROGRESS` `checkout_groups` older than the threshold that have no persisted sub-orders, calls idempotent `PromotionService.ReleasePromotion(checkout_group_id)` then `ProductService.ReleaseStock(checkout_group_id)` under a service token (`NotFound` treated as already released), and transitions the group to `FAILED` only after both succeed (groups that already have persisted sub-orders are left untouched).

---

## 7. Environment Variable Convention

All defaults for local dev/sandbox live in `.env.example` and are injected by `docker-compose.yml`:
- Common to every service: `SERVICE_NAME`, `GRPC_PORT`, `HTTP_PORT`, `LOG_LEVEL`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `JWT_SECRET`.
- Datastores (only for services that own them): `DATABASE_URL` (pointing exclusively to `<service>_db`), `REDIS_ADDR`, `NATS_URL`, `NATS_STREAM=NEXUSMART`.
- Upstream addresses: `USER_GRPC_ADDR`, `PRODUCT_GRPC_ADDR`, `CART_GRPC_ADDR`, `PROMOTION_GRPC_ADDR`, `ORDER_GRPC_ADDR`, `PAYMENT_GRPC_ADDR`, `PAYMENT_HTTP_URL`, `SHIPPING_GRPC_ADDR`, `SHIPPING_HTTP_URL`, `NOTIFICATION_GRPC_ADDR`.
- Sandbox / Mock toggles: `VIETQR_MODE=mock`, `PAYMENT_WEBHOOK_SECRET`, `GHN_MODE=mock`, `MOCK_CARRIER_ENABLED=true`, `SHIPPING_WEBHOOK_SECRET`, `SMTP_HOST=mailpit`, `SMTP_PORT=1025`.
