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
- **RBAC Defense-in-Depth**: Gateway validates JWT + route role AND forwards `Authorization` + `x-user-id` / `x-user-role` / `x-shop-id` in gRPC metadata. Every backend service MUST re-verify object-level ownership (e.g., buyer can only read/cancel their own order; seller can only mutate their own `shop_id`'s products/orders; admin cannot lock self or last admin).

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
| `POST /api/v1/shops/register` | `buyer`, `seller` | `{"name","description","pickup_address_id","contact_phone","business_license_doc"}` | `201` `{"shop":{...}}` |
| `GET /api/v1/shops/{id}` | Public | — | `200` `{"shop":{...}}` |
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
| `GET /api/v1/products` | Public | Query: `category_id`, `keyword`, `shop_id`, `status`, `min_price_vnd`, `max_price_vnd`, `sort_by`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |
| `GET /api/v1/products/{id}` | Public | — | `200` `{"product":{...,"skus":[...]}}` |
| `POST /api/v1/seller/products` | `seller` | `{"category_id","name","description","images":[...],"skus":[{"sku_code","name","attributes":{},"price_vnd":100000,"stock":50,"weight_grams":500,"image_url"}]}` | `201` `{"product":{...}}` |
| `PUT /api/v1/seller/products/{id}` | `seller` (shop owner) | Same + `status` | `200` `{"product":{...}}` |
| `DELETE /api/v1/seller/products/{id}` | `seller` (shop owner) | — | `200` `{"ok":true}` |
| `PATCH /api/v1/seller/skus/{sku_id}/stock` | `seller` (shop owner) | `{"new_stock":80,"expected_version":1}` | `200` `{"sku":{...}}` (or `409` on version conflict) |
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
| `POST /api/v1/seller/vouchers` | `seller` | `{"code","name","discount_type":"FIXED"\|"PERCENT","value_vnd","discount_percent","max_discount_vnd","min_order_vnd","quota","per_user_limit","budget_vnd","starts_at","ends_at"}` (forces `scope=SHOP`, `shop_id` from JWT) | `201` `{"voucher":{...}}` |
| `POST /api/v1/admin/vouchers` | `admin`, `super_admin` | Same + `scope:"PLATFORM"\|"FREESHIP"` | `201` `{"voucher":{...}}` |
| `GET /api/v1/flash-sales` | Public | Query: `include_upcoming` | `200` `{"flash_sales":[...]}` |
| `POST /api/v1/admin/flash-sales` | `admin`, `super_admin` | `{"name","starts_at","ends_at"}` | `201` `{"flash_sale":{...}}` |
| `POST /api/v1/seller/flash-sales/{id}/items` | `seller` | `{"sku_id","flash_price_vnd","total_slots","per_user_limit"}` | `201` `{"item":{...}}` |
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
| `POST /api/v1/seller/orders/{id}/confirm` | `seller` (shop owner) | — | `200` `{"order":{...}}` |
| `POST /api/v1/seller/orders/{id}/ship` | `seller` (shop owner) | `{"carrier_code","service_code"}` | `200` `{"order":{...},"tracking_code","label_url"}` |
| `GET /api/v1/admin/orders` | `admin`, `super_admin` | Query: `buyer_id`, `shop_id`, `status`, `page`, `page_size` | `200` `{"items":[...],"page","page_size","total"}` |

### 4.6 Payments, Ledger & Seller Finance (`payment` service)
| Method & Path | Auth / RBAC | Request JSON | Response JSON |
| --- | --- | --- | --- |
| `GET /api/v1/payments/{id}` | Buyer owner / Admin | — | `200` `{"payment":{...}}` |
| `POST /api/v1/payments/{id}/retry` | Buyer owner | `{"method":"COD"\|"VIETQR"}` | `200` `{"payment":{...}}` (cancels superseded pending intent, UC-B25) |
| `GET /api/v1/payments/{id}/events` | Any authenticated (proxied SSE to `payment:8086/payments/{id}/events`) | — | `text/event-stream` (`payment.succeeded` / `payment.expired` / `payment.failed`) |
| `GET /api/v1/seller/balance` | `seller` | — | `200` `{"shop_id","pending_vnd","available_vnd","total_earned_vnd"}` |
| `GET /api/v1/admin/ledger/summary` | `admin`, `super_admin` | — | `200` `{"total_debits_vnd","total_credits_vnd","is_balanced":true,"accounts":[...]}` |
| `POST /api/v1/admin/payments/{id}/refund` | `admin`, `super_admin` + `Idempotency-Key` | `{"order_id","amount_vnd","reason"}` | `200` `{"refund":{...},"already_existed":false}` |
| `POST /api/v1/admin/payments/reconcile` | `admin`, `super_admin` | `{"date":"2026-11-20"}` | `200` `{"run_id","matched_count","missing_local_count","missing_provider_count","amount_mismatch_count","items":[...]}` |

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
| `order.cancelled` | `order` | `notification`, `shipping` | `{"order_id","order_code","buyer_id","shop_id","reason","was_paid":true\|false}` |
| `order.shipped` | `order` | `notification` | `{"order_id","order_code","buyer_id","shop_id","carrier_code","tracking_code"}` |
| `order.delivered` | `order` | `notification` | `{"order_id","order_code","buyer_id","shop_id","tracking_code"}` |
| `order.completed` | `order` | `notification`, `promotion`, `payment` | `{"order_id","order_code","buyer_id","shop_id","subtotal_vnd","commission_vnd","total_payable_vnd","coins_reward"}` |
| `payment.succeeded` | `payment` | `order`, `notification` | `{"payment_id","order_group_id","order_id","user_id","amount_vnd","method","provider_tx_id"}` |
| `payment.failed` | `payment` | `order`, `notification` | `{"payment_id","order_group_id","order_id","user_id","reason"}` |
| `payment.expired` | `payment` | `order`, `notification` | `{"payment_id","order_group_id","order_id","user_id","reason"}` |
| `payment.refunded` | `payment` | `order`, `notification` | `{"refund_id","payment_id","order_id","user_id","amount_vnd","reason"}` |
| `shipment.updated` | `shipping` | `order`, `notification` | `{"shipment_id","order_id","shop_id","carrier_code","tracking_code","status":"picking"\|"delivering"\|"delivered"\|"failed"\|"returning"\|"returned","raw_status","description","occurred_at"}` |

---

## 6. Order State Machine & Checkout Saga

### Valid Order Status Transitions
- `PENDING` → `CONFIRMED` (payment succeeded), `CANCELLED` (payment failed/expired or buyer cancel)
- `CONFIRMED` → `PREPARING` (seller confirms), `SHIPPING` (seller ships directly), `CANCELLED` (buyer/seller cancel before pickup)
- `PREPARING` → `SHIPPING` (seller ships / shipment picked up), `CANCELLED` (before carrier pickup)
- `SHIPPING` → `DELIVERED` (`shipment.updated=delivered`), `RETURNED` (`shipment.updated=returned` after failed delivery)
- `DELIVERED` → `COMPLETED` (buyer confirms received or 7d auto-complete), `RETURN_REQUESTED` (buyer requests return within 7d)
- `RETURN_REQUESTED` → `RETURNED` (return accepted & completed), `COMPLETED` (return rejected/closed)

### Multi-Shop Checkout Saga (`OrderService.CreateCheckout`)
1. **Idempotency check**: Look up `(buyer_id, idempotency_key)` in `checkout_groups`. If found, immediately return the saved response (`idempotent_replay=true`).
2. **Validate & Resolve**: Validate buyer (`UserService.ValidateUser`), fetch address (`UserService.GetAddress`), load items from `CartService.GetCart` (when `from_cart=true`) or `items[]`, fetch SKU details & prices (`ProductService.GetSKUsBatch`), group items by `shop_id`, calculate shipping per shop (`ShippingService.CalculateFee`).
3. **COD Risk Check**: If `payment_method == COD`, enforce `total_payable_vnd <= ORDER_COD_MAX_VND` and `cod_rejected_count < ORDER_COD_MAX_REJECTED_COUNT`.
4. **Reserve Stock**: Call `ProductService.ReserveStock(order_id=checkout_group_id, all_items)` (atomic optimistic locking, idempotent).
5. **Reserve Promotions**: Call `PromotionService.ReservePromotion(order_id=checkout_group_id, ...)` (atomic Redis Lua for flash sale slots, voucher quotas, and coins; order of discount application: `shop voucher -> platform voucher -> freeship -> coins`).
   - *On failure*: compensate step 4 via `ProductService.ReleaseStock(checkout_group_id)` and return error.
6. **Persist Checkout Group + Sub-Orders + Outbox** in one PostgreSQL transaction:
   - One sub-order per `shop_id` sharing `checkout_group_id`.
   - Status is `CONFIRMED` for COD, `PENDING` for VietQR (`payment_expires_at = now + 15m`).
7. **Create Payment**: Call `PaymentService.CreatePayment(...)` with `shop_allocations` (per-sub-order `order_id`, `shop_id`, `subtotal_vnd`, `commission_vnd`, `shipping_fee_vnd`).
   - *On failure*: mark sub-orders `CANCELLED`, compensate via `PromotionService.ReleasePromotion` and `ProductService.ReleaseStock`.
8. **Clear Cart**: Best-effort `CartService.ClearItems(buyer_id, ordered_sku_ids)`.
9. **Async Payment Failure / Expiry Compensation**:
   - On `payment.failed` or `payment.expired`: transition sub-orders `PENDING -> CANCELLED`, call `ProductService.ReleaseStock(checkout_group_id)` and `PromotionService.ReleasePromotion(checkout_group_id)`, write `order.cancelled` outbox event.
   - On `payment.succeeded`: transition sub-orders `PENDING -> CONFIRMED`, call `ProductService.ConfirmStock(checkout_group_id)`, write `order.confirmed` outbox event.
   - On `BuyerConfirmReceived` (`DELIVERED -> COMPLETED`): call `PaymentService.ReleaseEscrow(order_id)` (or emit `order.completed`), credit reward coins via `PromotionService.CreditCoins` (idempotent by `order_id`).

---

## 7. Environment Variable Convention

All defaults for local dev/sandbox live in `.env.example` and are injected by `docker-compose.yml`:
- Common to every service: `SERVICE_NAME`, `GRPC_PORT`, `HTTP_PORT`, `LOG_LEVEL`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `JWT_SECRET`.
- Datastores (only for services that own them): `DATABASE_URL` (pointing exclusively to `<service>_db`), `REDIS_ADDR`, `NATS_URL`, `NATS_STREAM=NEXUSMART`.
- Upstream addresses: `USER_GRPC_ADDR`, `PRODUCT_GRPC_ADDR`, `CART_GRPC_ADDR`, `PROMOTION_GRPC_ADDR`, `ORDER_GRPC_ADDR`, `PAYMENT_GRPC_ADDR`, `PAYMENT_HTTP_URL`, `SHIPPING_GRPC_ADDR`, `SHIPPING_HTTP_URL`, `NOTIFICATION_GRPC_ADDR`.
- Sandbox / Mock toggles: `VIETQR_MODE=mock`, `PAYMENT_WEBHOOK_SECRET`, `GHN_MODE=mock`, `MOCK_CARRIER_ENABLED=true`, `SHIPPING_WEBHOOK_SECRET`, `SMTP_HOST=mailpit`, `SMTP_PORT=1025`.
