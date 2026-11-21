# Swarm Q – Báo cáo Sửa Test Cuối Cùng & Gate Cuối Xanh (`docs/reports/q-final-fix.md`)

- **Thời gian thực hiện:** `2026-11-21 16:44 – 16:49 +07:00` (nhánh `phase-0b`, không commit, không đổi nhánh).
- **Brief & hợp đồng tham chiếu:** `.agents/briefs/m-q-final-fix.md`, `.agents/briefs/m-all-contract.md`, `docs/reports/issues.md` (`QA-03`, `QA-04`), `docs/reports/h.md`.
- **Phạm vi thay đổi:** Chỉ chỉnh sửa 2 file test thuộc quyền sở hữu của Swarm Q (tổng diff `+7/-7` dòng), **không chạm** `services/` và `web/` (giữ nguyên hoàn toàn bộ lọc SSRF `.local` của Swarm T).
- **Quản lý stack lock:** Tạo `.runs/stack.lock` (nội dung `Q`) khi chạy stack compose / Playwright và xóa sạch sau khi hoàn tất.

---

## 1. Thay đổi đã thực hiện

1. **`QA-03` — `tests/e2e/13_sites_backend_test.go` (dùng chung cho `tests/e2e/15_custom_domains_test.go`):**
   - Đổi `banner_url` trong `validSiteConfigPayload` (dòng 32) và assertion `foundCard.CoverURL` (dòng 450) từ `https://sandbox.nexusmart.local/banners/site-hero.jpg` sang `https://cdn.example.com/banners/site-hero.jpg` (host công khai hợp lệ qua `isDisallowedImageHost` trong `services/user/internal/site/validate.go` mà không nới validator).
   - `tests/e2e/15_custom_domains_test.go` gọi lại `validSiteConfigPayload` nên tự động xanh mà không cần chỉnh sửa thêm.

2. **`QA-04` — `tests/playwright/specs/buyer-flow.spec.ts`:**
   - **Bước 2 (`/tim-kiem`):** Thay `page.locator("#site-search-input")` bằng:
     ```ts
     const searchForm = page.getByRole("main").getByRole("search");
     const searchInput = searchForm.getByRole("combobox");
     await expect(searchInput).toBeVisible();
     await searchInput.fill("cotton");
     await searchForm.getByRole("button", { name: "Tìm kiếm", exact: true }).click();
     ```
     Định vị qua `getByRole("main").getByRole("search")` giúp tránh xung đột với `<SiteSearchBar compact />` trong `<header>` và thẻ `<select id="site-search-sort">` (cũng có implicit ARIA role `combobox`) bên trong `<main>`.
   - **Bước 3 (`/gio-hang`):** Sau khi chuyển sang `<SiteSearchResults>` gọi `/api/search` thật, card kết quả mở PDP qua `/san-pham/<uuid>` (do response tìm kiếm không trả `slug`), trong khi trang `/gio-hang` render đường dẫn chuẩn `/san-pham/<slug>` (ví dụ `/san-pham/ao-thun-cotton-compact-xam-da`). Cập nhật locator giỏ hàng từ khớp cứng `href="${selectedProductPath}"` sang `page.locator('article ul li a[href^="/san-pham/"]').filter({ hasText: selectedProductName }).last()` và bỏ biến `selectedProductPath` không còn dùng.

---

## 2. Kết quả Gate Cuối (Lệnh & Đầu ra thật)

| Hạng mục Gate (`m-q-final-fix.md`) | Exit Code | Kết quả | Chi tiết |
|---|---:|---|---|
| `gofmt -l tests/e2e` + `go vet ./...` (+ `golangci-lint`) | **`0`** | **ĐẠT** | 0 file lệch format, 0 lỗi `go vet` / `golangci-lint` |
| `cd tests/playwright && npm run typecheck` | **`0`** | **ĐẠT** | `tsc --noEmit` sạch |
| `make e2e` (20 suite Go E2E `01..20`) | **`0`** | **ĐẠT** | **20/20 PASS** (`18.709s`), bao gồm `13_sites_backend_test.go` và `15_custom_domains_test.go` |
| `make seed` | **`0`** | **ĐẠT** | Idempotent (`sites: created=0, reused=4, templates_applied=4`) |
| `make e2e-web` (8 suite Web E2E `01..08`) | **`0`** | **ĐẠT** | **41/41 PASS** (`14.06s`) |
| Chuỗi `make e2e && make seed && make e2e-web` | **`0`** | **ĐẠT** | `CHAIN_RC=0` |
| Playwright `buyer-flow.spec.ts` trên `http://bach-dang.nexusmart.localhost:3300` | **`0`** | **ĐẠT** | **1/1 PASS** (`2.8s`, đủ 5 bước từ đăng ký OTP đến xem đơn COD) |

### 2.1 Kiểm tra tĩnh `tests/e2e` & `tests/playwright`

Lệnh:
```bash
gofmt -l tests/e2e
(cd tests/e2e && go vet ./... && golangci-lint run -c ../../.golangci.yml ./...)
(cd tests/playwright && npm run typecheck)
```

Đầu ra thật:
```text
=== STEP 1: gofmt + go vet + golangci-lint (tests/e2e) & tsc (tests/playwright) ===
gofmt -l tests/e2e: CLEAN
go vet tests/e2e: PASS
golangci-lint tests/e2e: PASS
npm notice run nexusmart-playwright-e2e@1.0.0 typecheck
npm notice run tsc --noEmit
playwright typecheck: PASS
```

### 2.2 Chuỗi `make e2e && make seed && make e2e-web`

Lệnh (với `.runs/stack.lock` = `Q` và bộ biến cổng host khớp stack sống):
```bash
REDIS_PORT=26379 OTEL_HTTP_PORT=24318 GATEWAY_PORT=28080 \
PRODUCT_HTTP_PORT=28082 CART_HTTP_PORT=28083 ORDER_HTTP_PORT=28085 \
CHAT_HTTP_PORT=28090 ASSISTANT_HTTP_PORT=28091 TELEGRAM_RELAY_PORT=18099 \
make e2e && make seed && make e2e-web
```

Đầu ra thật (`make e2e` — 20/20 PASS):
```text
--- PASS: TestE2E_HealthAndObservability (0.07s)
--- PASS: TestE2E_MultiShopCheckout_COD (2.07s)
--- PASS: TestE2E_MultiShopCheckout_VietQRMock (0.74s)
--- PASS: TestE2E_OrderIdempotency (0.40s)
--- PASS: TestE2E_PaymentWebhookIdempotencyAndSecurity (0.84s)
--- PASS: TestE2E_FlashSale_1000Concurrent_100Slots (0.70s)
--- PASS: TestE2E_PaymentFailed_SagaCompensation (0.74s)
--- PASS: TestE2E_LedgerAlwaysBalanced (0.01s)
--- PASS: TestE2E_SalesChannel (1.87s)
--- PASS: TestE2E_LedgerAndReconciliation_ChannelFilter (0.06s)
--- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata (0.48s)
--- PASS: TestE2E_RateLimit_Real429AndNoXFFBypass (0.07s)
--- PASS: TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit (0.13s)
--- PASS: TestE2E_FlashSaleStockReserve_Phase2b (1.72s)
--- PASS: TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary (0.91s)
--- PASS: TestE2E_Search_VietnameseIndexingFiltersFacetsAndSuggest (2.25s)
--- PASS: TestE2E_Chat_RESTAutoReplyWebSocketSyncAndRBAC (0.77s)
--- PASS: TestE2E_Assistant_SSERAGToolsHandoffSellerAndGuardrails (1.61s)
--- PASS: TestE2E_OrderDepth_ShippingZaloPayReturnsComplaintsAndReconciliation (2.80s)
--- PASS: TestE2E_SiteTemplates_DiscoveryApplyPublishRollbackSecurityAndRBAC (0.43s)
ok  	github.com/nexusmart/core/tests/e2e	18.709s
```

Đầu ra thật (`make seed`):
```text
==> Seed complete:
  sites: created=0, reused=4 (root_domain=nexusmart.localhost, templates_applied=4)
```

Đầu ra thật (`make e2e-web` — 41/41 PASS):
```text
✔ 01. Root host = NexusMart platform only (intro, site directory, redirects, RBAC, SEO) (1015.994983ms)
✔ 02. Site A full buyer commerce & auth lifecycle (OTP, COD + SHOP voucher, VietQR, idempotency, refresh rotation) (1377.461634ms)
✔ 03. Cross-site isolation, IDOR, 401 guards, CSRF across hosts, body tampering & blocked routes (999.325224ms)
✔ 04. Seller/Admin site lifecycle on root, theme security, suspend (pages + APIs) & per-host SEO (6626.361294ms)
✔ 05. Phase 5 custom domains (Host routing, 308 primary redirect, cookie isolation, SEO & BFF domains) (550.661282ms)
✔ 06. Site search, real-time chat (buyer + seller BFF) & AI assistant (SSE, review summary, handoff) (705.485308ms)
✔ 07. Buyer returns, Seller real pages + AI BFF & Admin complaints/config on ROOT_HOST (1693.70152ms)
✔ 08. Site templates discovery, apply-template, draft customization, publish, XSS/SVG/SSRF & RBAC/CSRF (558.220126ms)
ℹ tests 41
ℹ suites 8
ℹ pass 41
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 14058.950837
CHAIN_RC=0
```

### 2.3 Playwright `buyer-flow` trên site host thật (`bach-dang.nexusmart.localhost:3300`)

Lệnh:
```bash
cd tests/playwright && npm run typecheck && npm test
```

Đầu ra thật:
```text
npm notice run nexusmart-playwright-e2e@1.0.0 typecheck
npm notice run tsc --noEmit
npm notice run nexusmart-playwright-e2e@1.0.0 test
npm notice run playwright test

Running 1 test using 1 worker

  ✓  1 [chromium] › specs/buyer-flow.spec.ts:31:3 › Storefront Buyer E2E Journey › register with Mailpit OTP, search product, add to cart, checkout COD, and view order (2.2s)

  1 passed (2.8s)
```

---

## 3. Phần chưa đạt

Không có. Toàn bộ tiêu chí gate của Swarm Q đều đã đạt EXIT 0; `QA-03` và `QA-04` đã được đánh dấu `đã sửa bởi Q` trong `docs/reports/issues.md`.
