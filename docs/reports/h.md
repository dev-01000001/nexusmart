# Swarm H – Báo cáo Nghiệm thu QA Tích hợp, Đánh giá Định lượng, Báo cáo & Demo (`docs/reports/h.md`)

- **Thời gian thực hiện:** `2026-11-21 14:47 – 16:42 +07:00` (nhánh `phase-0b`, không commit, không đổi nhánh, không sửa file ngoài quyền sở hữu).
- **Hợp đồng tham chiếu:** `.agents/briefs/m-h-qa-docs.md`, `.agents/briefs/m-all-contract.md`, `docs/spec.md`.
- **Phạm vi sở hữu Swarm H đã bàn giao:**
  - **5 file Go E2E mới (`tests/e2e/`):** `16_search_test.go`, `17_chat_test.go`, `18_assistant_test.go`, `19_order_depth_test.go`, `20_site_templates_test.go` (không sửa `01..15`, không thêm dependency vào `tests/e2e/go.mod`).
  - **3 file Web E2E mới (`tests/e2e-web/`):** `06_search_chat_assistant.test.mjs`, `07_returns_seller_admin.test.mjs`, `08_site_templates.test.mjs` (không sửa `01..05` hay `lib/*`).
  - **Nâng cấp `tools/seed/seed.mjs`:** sửa `PLT-01` (tự động kết thúc 3 khung Flash Sale cũ thiếu `flash_sale_stock_reservations`), nâng cấp 4 site showcase lên template v2 (`craft-atelier`, `sport-active`, `minimal-fashion`, `luxury-maison`), nạp dữ liệu mẫu chat + auto-reply, 1 đơn trả hàng `PENDING` + 1 khiếu nại `OPEN`, và nạp **20.000 sản phẩm** qua `tools/catalog-gen` vào gian hàng đại lý riêng (`seller-catalog@nexusmart.local`, không xuất bản site để giữ nguyên 4 site showcase).
  - **Tài liệu bàn giao:** [docs/EVAL_REPORT.md](../EVAL_REPORT.md), [docs/FINAL_REPORT.md](../FINAL_REPORT.md), [docs/DEMO.md](../DEMO.md), báo cáo này (`docs/reports/h.md`), và ghi nhận 4 lỗi liên swarm (`QA-01`, `QA-02`, `QA-03`, `QA-04`) vào [docs/reports/issues.md](issues.md).
  - **Điều phối stack dùng chung:** tuân thủ nghiêm `.runs/stack.lock` (`H` + `trap EXIT`), giữ nguyên `web/.next` (không đụng cổng `3111`), không xóa ảnh Docker của dự án khác.

---

## 1. Trạng thái tích hợp toàn bộ 11 swarm song song

| Swarm | Phạm vi | Báo cáo (`docs/reports/`) | Trạng thái tích hợp trên stack sống | Kiểm chứng bởi Swarm H |
|---|---|---|---|---|
| **A (`search`)** | `search`, `product` outbox, `catalog-gen` | `search.md` (DONE) | Đã rebuild `search-postgres`, `meilisearch`, `product`, `search` (**20.021 SP**) | `16_search_test.go` **PASS** (`368.5 ms` sync); k6 live **p95 `42.97 ms`** (`0` lỗi); `06_search_*` **PASS** |
| **B (`chat`)** | `services/chat` (WS + Redis Pub/Sub) | `b-chat.md` (DONE) | Đã rebuild `chat` (`:28090`, healthy) | `17_chat_test.go` **PASS 7/7** (`0.82 s`); `06_search_chat_*` **PASS** |
| **C (`assistant`)** | `services/assistant`, `evals/assistant` | `c-assistant.md` (DONE) | Đã rebuild `assistant` (`:28091`, healthy) | `18_assistant_test.go` **PASS 6/6** (TTFT `46.43 ms`); `make eval-assistant` **100/100 & 36/36 PASS**; `06_*` **PASS** |
| **D + D2 (`order-depth` & `payment-fix`)** | `order`, `payment`, `shipping`, `promotion`, `deploy/helm/payment` | `d-order-depth.md`, `d2-payment-fix.md` (DONE) | Đã rebuild `order`, `payment`, `shipping`, `promotion` (healthy) | `19_order_depth_test.go` **PASS 5/5** (`2.81 s`); `07_returns_*` **PASS**; `make helm-lint` **28/28 PASS** |
| **E (`aiops`)** | `services/aiops`, `deploy/observability`, `tools/chaos` | `e-aiops.md` (DONE) | 9 container `obs` + `aiops` (`:8092`, healthy); **5/5 Chaos PASS** | Xác nhận `/healthz`, `/readyz`, `/metrics` sống + Tempo `HTTP 200` (`gateway → search`) |
| **F (`platform`)** | Helm, K8s, CI, k6, Playwright, Backup | `f-platform.md` (DONE) | Đã xác minh CI, Helm, k6 Search 20k, k6 Checkout 200 VU, Restore Drill 9 DB | CI `workflow_dispatch`-only **PASS**; Restore Drill **9 DB / 328.473 dòng 100% PASS**; ghi nhận **`QA-04`** (selector Playwright `/tim-kiem`) |
| **G + G2 (`web` & `web-fix`)** | `web/` (BFF + UI người mua/bán/admin) | `g-web.md`, `g2-web-fix.md` (DONE) | Đã rebuild `web` (`:3300`, `next@15.5.29`, healthy); đã vá `QA-01`, `QA-02`, `OBS-11`, `PLT-02` | `eslint`/`prettier`/`tsc`/`191` unit tests/`npm audit` (`0 vuln`) **PASS**; `make e2e-web` **41/41 PASS x2** |
| **T (`templates`)** | `user/internal/site`, `web` site templates | `t-templates.md` (DONE) | Đã rebuild `user` + `web` (healthy); 4 site seed nâng cấp lên v2 | `20_site_templates_test.go` **PASS 5/5**; `08_site_templates` **PASS**; ghi nhận **`QA-03`** (chặn `.local` làm hỏng `13` & `15`) |
| **K (`core-fix`)** | `pkg/common`, `infra`, `gateway`, `cart`, `notification`, `chat`, `assistant`, `.dockerignore` | `k-core-fix.md` (DONE) | Đã rebuild toàn bộ dịch vụ K + `otel-collector` (healthy) | Tempo `HTTP 200` xác nhận `OBS-01`; `17_chat` & `18_assistant` xác nhận `K-01`/`K-02` |

---

## 2. Kết quả Gate Cuối Chính thức (Đầu ra thật)

Chạy sau mọi thay đổi cuối cùng trên toàn bộ mã nguồn và stack 29 container đang sống (xuất bộ biến override cổng host `REDIS_PORT=26379 OTEL_HTTP_PORT=24318 GATEWAY_PORT=28080 PRODUCT_HTTP_PORT=28082 CART_HTTP_PORT=28083 ORDER_HTTP_PORT=28085 CHAT_HTTP_PORT=28090 ASSISTANT_HTTP_PORT=28091 TELEGRAM_RELAY_PORT=18099`):

### 2.1 Tổng hợp trạng thái Gate cuối

| Hạng mục Gate cuối (`m-h-qa-docs.md`) | Exit Code | Kết quả | Ghi chú |
|---|---:|---|---|
| `make lint` (`buf lint`, 15 Go modules `gofmt` + `golangci-lint`, Rust `payment` & `aiops` `rustfmt` + `clippy -D warnings`) | **`0`** | **ĐẠT** | 0 lỗi, 0 cảnh báo |
| `tests/e2e` (`gofmt` + `golangci-lint`) & `web` (`eslint`, `prettier --check`, `test:unit`, `tsc --noEmit`, `npm audit --omit=dev --audit-level=high`) | **`0`** | **ĐẠT** | `191/191` unit tests PASS; `found 0 vulnerabilities` |
| `make test` (15 Go modules `go test -race ./...` + Rust `payment` & `aiops` `cargo test`) | **`0`** | **ĐẠT** | 100% PASS |
| `make e2e` (20 suite Go E2E `01..20` trên catalog 20.021 SP) | **`2`** | **CHƯA ĐẠT (18/20 PASS)** | **Toàn bộ 5 suite mới `16..20` PASS (`5/5`) và `01..12, 14` PASS (`13/13`)**. Chỉ **`13_sites_backend_test.go`** và **`15_custom_domains_test.go`** FAIL do **`QA-03`** ở mã ngoài phạm vi Swarm H (`services/user/internal/site/validate.go:513` vs `tests/e2e/13_sites_backend_test.go:32`). |
| `make seed` (trên stack đã nạp 20.000 SP) | **`0`** | **ĐẠT** | 100% idempotent (`created=0`, `reused=4` sites, `templates_applied=4`, `catalog=20000 reused`) |
| `make e2e-web` (Lượt 1: toàn bộ 8 suite `01..08`) | **`0`** | **ĐẠT** | **41/41 PASS** (`16.99 s`) |
| `make e2e-web` (Lượt 2 liên tiếp: chứng minh idempotency) | **`0`** | **ĐẠT** | **41/41 PASS** (`14.98 s`) |
| CI workflows chỉ `workflow_dispatch` | **`0`** | **ĐẠT** | Chỉ `workflow_dispatch:` ở cả `ci.yml` và `security.yml` |
| Chỉ cổng `28080` và `3300` public; `0` cổng gRPC public | **`0`** | **ĐẠT** | Duy nhất `gateway (0.0.0.0:28080)` & `web (0.0.0.0:3300)`; `0` cổng `5005x`/`5006x` mở ra host |

### 2.2 Đầu ra thật — `make lint` & `web` / `tests/e2e` kiểm định

```text
==========================================
FINAL GATE 1: make lint
==========================================
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
==> gofmt & golangci-lint services/search
==> gofmt & golangci-lint services/chat
==> gofmt & golangci-lint services/assistant
==> gofmt & golangci-lint tools/catalog-gen
==> gofmt & golangci-lint evals/assistant
==> rustfmt & clippy services/payment
    Finished `dev` profile [unoptimized + debuginfo] target(s) in 0.48s
==> rustfmt & clippy services/aiops
    Finished `dev` profile [unoptimized + debuginfo] target(s) in 0.14s
FINAL_GATE_MAKE_LINT_RC=0

==========================================
FINAL GATE 1b: tests/e2e gofmt & golangci-lint + web lint/format/unit/tsc/audit
==========================================
All matched files use Prettier code style!
ℹ tests 191
ℹ suites 11
ℹ pass 191
ℹ fail 0
ℹ duration_ms 348.242517
found 0 vulnerabilities
FINAL_GATE_WEB_AND_E2E_LINT_RC=0
```

### 2.3 Đầu ra thật — `make test`

```text
==========================================
FINAL GATE 2: make test
==========================================
==> go test pkg/common          (PASS -race)
==> go test gen/go              (PASS -race)
==> go test services/gateway    (PASS -race)
==> go test services/user       (PASS -race)
==> go test services/product    (PASS -race)
==> go test services/cart       (PASS -race)
==> go test services/promotion  (PASS -race)
==> go test services/order      (PASS -race)
==> go test services/shipping   (PASS -race)
==> go test services/notification (PASS -race)
==> go test services/search     (PASS -race)
==> go test services/chat       (PASS -race)
==> go test services/assistant  (PASS -race)
==> go test tools/catalog-gen   (PASS -race)
==> go test evals/assistant     (PASS -race)
==> cargo test services/payment
test result: ok. 61 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.09s
test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.01s
test result: ok. 2 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s
==> cargo test services/aiops
test result: ok. 9 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.04s
test result: ok. 6 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.19s
FINAL_GATE_MAKE_TEST_RC=0
```

### 2.4 Đầu ra thật — `make e2e`, `make seed`, `make e2e-web && make e2e-web`

```text
--- 3a. make e2e (full 01..20) ---
--- PASS: TestE2E_HealthAndObservability (0.07s)
--- PASS: TestE2E_MultiShopCheckout_COD (2.00s)
--- PASS: TestE2E_MultiShopCheckout_VietQRMock (0.72s)
--- PASS: TestE2E_OrderIdempotency (1.06s)
--- PASS: TestE2E_PaymentWebhookIdempotencyAndSecurity (1.05s)
--- PASS: TestE2E_FlashSale_1000Concurrent_100Slots (0.69s)
--- PASS: TestE2E_PaymentFailed_SagaCompensation (0.72s)
--- PASS: TestE2E_LedgerAlwaysBalanced (0.01s)
--- PASS: TestE2E_SalesChannel (1.94s)
--- PASS: TestE2E_LedgerAndReconciliation_ChannelFilter (0.06s)
--- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata (0.57s)
--- PASS: TestE2E_RateLimit_Real429AndNoXFFBypass (0.07s)
--- FAIL: TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit (0.03s)   # QA-03: sandbox.nexusmart.local rejected by .local SSRF rule
--- PASS: TestE2E_FlashSaleStockReserve_Phase2b (1.83s)
--- FAIL: TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary (0.79s) # QA-03: inherits validSiteConfigPayload from 13_sites_backend_test.go:32
    16_search_test.go:212: Search indexing latency for product f5fdc433-04bf-4203-823d-68c78c441504: 368.523216ms
--- PASS: TestE2E_Search_VietnameseIndexingFiltersFacetsAndSuggest (2.29s)
--- PASS: TestE2E_Chat_RESTAutoReplyWebSocketSyncAndRBAC (0.82s)
    18_assistant_test.go:329: Assistant SSE RAG TTFT=46.434523ms
--- PASS: TestE2E_Assistant_SSERAGToolsHandoffSellerAndGuardrails (1.62s)
--- PASS: TestE2E_OrderDepth_ShippingZaloPayReturnsComplaintsAndReconciliation (2.81s)
--- PASS: TestE2E_SiteTemplates_DiscoveryApplyPublishRollbackSecurityAndRBAC (0.45s)
FINAL_GATE_MAKE_E2E_RC=2

--- 3b. make seed ---
==> Seed complete:
  sites: created=0, reused=4 (root_domain=nexusmart.localhost, templates_applied=4)
FINAL_GATE_MAKE_SEED_RC=0

--- 3c. make e2e-web (Run 1) ---
ℹ tests 41
ℹ suites 8
ℹ pass 41
ℹ fail 0
ℹ duration_ms 16990.519386
FINAL_GATE_MAKE_E2E_WEB_1_RC=0

--- 3d. make e2e-web (Run 2 - back-to-back idempotency) ---
ℹ tests 41
ℹ suites 8
ℹ pass 41
ℹ fail 0
ℹ duration_ms 14981.625763
FINAL_GATE_MAKE_E2E_WEB_2_RC=0
```

### 2.5 Đầu ra thật — CI `workflow_dispatch`-only & Cổng Public

```text
.github/workflows/ci.yml:6:on:
.github/workflows/ci.yml:7:  workflow_dispatch:
.github/workflows/security.yml:5:on:
.github/workflows/security.yml:6:  workflow_dispatch:

PUBLIC_PORTS_0_0_0_0:
nexus_ecom-gateway-1 0.0.0.0:28080->8080/tcp
nexus_ecom-web-1 0.0.0.0:3300->3000/tcp

PUBLISHED_GRPC_PORTS (must be empty):
(NONE - 0 gRPC ports published)

SUMMARY: LINT=0 WEB_LINT=0 TEST=0 E2E=2 SEED=0 E2E_WEB_1=0 E2E_WEB_2=0
```

### 2.6 Đầu ra thật — Đo kiểm bổ sung của Swarm H (k6 Search 20k, Checkout 200 VU, Restore Drill 9 DB, Trace Tempo)

```text
=== k6 Live Search on 20,021-product catalog via gateway-bff (0x 429, FALLBACK_PRODUCTS=false) ===
[search setup] /api/v1/search live (suggest=true) | parity check: "áo thun" total=17262 vs "ao thun" total=17262
Search API  : count=7771 | p50=7.43 ms | p95=42.97 ms (target < 200 ms)
Suggest API : count=2550 | p50=2.44 ms | p95=42.62 ms (target < 200 ms)
Reliability : 429_rate_limited=0 | 5xx_server_errors=0 | 4xx_client_errors=0

=== k6 Live Checkout 200 VU via gateway-bff (quiet machine) ===
Read API     : count=25374 | p50=4.67 ms | p95=38.75 ms (target < 300 ms)
Checkout API : orders=3381 | p50=66.18 ms | p95=204.95 ms (target < 1000 ms)
Reliability  : 429_rate_limited=5708 | 5xx_server_errors=0 | 4xx_client_errors=0

=== Backup & Restore Drill (all 9 live Postgres DBs) ===
TOTAL (9 DBs) | 77 tables | 328473 / 328473 rows | restore 2.45s | PASS

=== Live OTel Trace Propagation (Gateway -> OTel Collector -> Tempo) ===
Captured X-Trace-Id: 8182b0c51294ad0ffd98776142cae2ed
Tempo lookup HTTP=200 bytes=1484
Tempo trace services: [ 'gateway', 'search' ]
```

---

## 3. Phần chưa đạt tuyệt đối (ngoài quyền sở hữu Swarm H)

1. **`QA-03` (High — chặn `make e2e` đạt 20/20):** Đã ghi chi tiết tại [docs/reports/issues.md](issues.md). Hai file `tests/e2e/13_sites_backend_test.go` và `tests/e2e/15_custom_domains_test.go` thất bại duy nhất vì `validSiteConfigPayload` ở `13_sites_backend_test.go:32` gửi `"banner_url": "https://sandbox.nexusmart.local/banners/site-hero.jpg"`, trong khi `services/user/internal/site/validate.go:513` (Swarm T) chặn hậu tố `.local`. Swarm H bị cấm sửa cả `services/user/` lẫn `tests/e2e/01..15`. Sau khi Manager/Swarm T xử lý 1 dòng này, `make e2e` sẽ đạt **20/20 EXIT 0**.
2. **`QA-04` (Medium — Playwright bước 2):** Đã ghi chi tiết tại [docs/reports/issues.md](issues.md). `tests/playwright/specs/buyer-flow.spec.ts:94` (Swarm F) tìm `#site-search-input` cũ sau khi `/tim-kiem` đổi sang `<SiteSearchResults>` / `<SiteSearchBar>` của Swarm G/T. Sau khi cập nhật selector này, Playwright sẽ chạy xanh trên giao diện v2 mới.
