# NexusMart k6 Load Tests

Stdlib-only [k6](https://k6.io/) load-testing suite (`k6/http`, `k6/net/grpc`, `k6/crypto`, `k6/encoding`, `k6/metrics`) with zero npm dependencies.

## Scenarios & Spec Targets

| Script | Spec Target | Key Metrics / Thresholds |
|---|---|---|
| `checkout.js` | **200 VUs**, p95 read API `< 300 ms`, p95 create order `< 1 s` | `read_duration` p95 `< 300ms`, `checkout_duration` p95 `< 1000ms`, `server_errors_5xx == 0` |
| `flash-sale.js` | **1,000 concurrent requests** competing for **100 slots** -> **100 sold, 0 oversell, `remaining_slots == 0`** | `flash_sale_sold == 100` (gRPC) / `<= 100` (HTTP), `flash_sale_unexpected_errors == 0`, teardown `remaining_slots == 0` |
| `search.js` | **p95 query `< 200 ms`**, Vietnamese diacritics & non-diacritics, filters, sort, suggest | `search_duration` p95 `< 200ms`, `suggest_duration` p95 `< 200ms`, `server_errors_5xx == 0` |

## Rate Limiting Notes (Important!)

The default `docker-compose.yml` gateway configures Redis sliding-window rate limits:
- `RATE_LIMIT_IP_PER_MIN=300` (per client IP across `/api/v1/*`)
- `RATE_LIMIT_USER_PER_MIN=180` (per authenticated user JWT)
- `RATE_LIMIT_FLASH_SALE_PER_MIN=60` (flash-sale routes)
- `TRUSTED_PROXY_CIDRS=172.31.250.0/28` (only the internal `bff` network; `X-Forwarded-For` from localhost or `nexus_ecom_default` is intentionally ignored per B2 security rules).

What this means during load tests:
1. All HTTP scripts **separate `rate_limited_429` from `server_errors_5xx`** and exclude fast-path 429 rejections from latency Trends so p95 reflects real backend processing time.
2. Against the default dev compose stack from a single host IP, high-VU runs will legitimately see HTTP 429s after 300 req/min.
3. For full 200-VU saturation benchmarks (staging or dedicated load run), either disable/raise rate limits on the gateway (`RATE_LIMIT_IP_PER_MIN=0 RATE_LIMIT_USER_PER_MIN=0`) and increase `BUYER_POOL_SIZE`, or run through a trusted ingress/proxy CIDR with `-e SPOOF_XFF=true`.
4. For the 1,000-request flash-sale atomicity proof, `flash-sale.js` defaults to `MODE=grpc` (calling `PromotionService.ReservePromotion` directly inside the compose network with 1,000 distinct HS256 JWTs, matching `tests/e2e/06_flash_sale_test.go`) so Gateway IP rate limiting does not mask Redis Lua concurrency.

## Environment Variables

All scripts share these optional `__ENV` overrides:

| Variable | Default (host) | Compose-network value |
|---|---|---|
| `GATEWAY_URL` | `http://127.0.0.1:28080` | `http://gateway:8080` |
| `MAILPIT_URL` | `http://127.0.0.1:8025` | `http://mailpit:8025` |
| `PROMOTION_GRPC_ADDR` | `promotion:50054` | `promotion:50054` |
| `JWT_SECRET` | `dev-only-nexusmart-hs256-secret-change-me` | match gateway/promotion |
| `SITE_ROOT_DOMAIN` | `nexusmart.localhost` | `nexusmart.localhost` |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | `admin@nexusmart.local` / `AdminDevPassword123!` | same |
| `SELLER_EMAIL` / `SELLER_PASSWORD` | `seller@nexusmart.local` / `SellerDemo123!` | same |
| `BUYER_EMAIL` / `BUYER_PASSWORD` | `buyer@nexusmart.local` / `BuyerDemo123!` | same |
| `SUMMARY_JSON` | *(empty)* | optional path to write full k6 summary JSON |

## Syntax Check (`k6 inspect`)

```bash
export PATH="$HOME/.local/bin:$PATH"
k6 inspect tests/load/checkout.js
k6 inspect tests/load/flash-sale.js
k6 inspect tests/load/search.js
```

## Running Locally (Host)

```bash
export PATH="$HOME/.local/bin:$PATH"

# 1. Checkout & Read API (quick smoke)
k6 run -e VUS=10 -e DURATION=15s -e BUYER_POOL_SIZE=5 tests/load/checkout.js

# 1b. Checkout & Read API (full 200-VU spec run — raise/disable gateway rate limits first)
k6 run -e VUS=200 -e DURATION=30s -e BUYER_POOL_SIZE=20 -e SKU_POOL_SIZE=20 tests/load/checkout.js

# 2. Flash Sale via Gateway HTTP (1,000 requests / 100 slots)
k6 run -e MODE=http -e VUS=100 -e TOTAL_REQUESTS=1000 -e TOTAL_SLOTS=100 tests/load/flash-sale.js

# 3. Search (strict /api/v1/search — requires Swarm A mounted)
k6 run -e VUS=50 -e DURATION=30s tests/load/search.js

# 3b. Search fallback to /api/v1/products when Swarm A is not yet mounted
k6 run -e VUS=20 -e DURATION=15s -e FALLBACK_PRODUCTS=true tests/load/search.js
```

## Running Inside Compose Network (Required for `flash-sale.js` `MODE=grpc`)

Internal gRPC ports (`promotion:50054`) are deliberately not published to the host. Run k6 on `nexus_ecom_default` using either the `grafana/k6` image or a container mounting the user-local binary:

```bash
# Using grafana/k6 on the compose network
docker run --rm --network nexus_ecom_default \
  -v "$PWD:/work:ro" -w /work \
  -e GATEWAY_URL=http://gateway:8080 \
  -e MAILPIT_URL=http://mailpit:8025 \
  -e PROMOTION_GRPC_ADDR=promotion:50054 \
  -e MODE=grpc \
  grafana/k6:latest run tests/load/flash-sale.js

# Or using the host's user-local ~/.local/bin/k6 binary inside alpine (zero extra image pull)
docker run --rm --network nexus_ecom_default \
  -v "$HOME/.local/bin/k6:/usr/local/bin/k6:ro" \
  -v "$PWD:/work:ro" -w /work \
  -e GATEWAY_URL=http://gateway:8080 \
  -e MAILPIT_URL=http://mailpit:8025 \
  -e PROMOTION_GRPC_ADDR=promotion:50054 \
  -e MODE=grpc \
  alpine:3.20 k6 run tests/load/flash-sale.js
```
