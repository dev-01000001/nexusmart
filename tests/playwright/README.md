# NexusMart Playwright E2E Suite

End-to-end browser verification for buyer storefront flows on a published NexusMart shop site host, executed at both desktop (`desktop-1440`, 1440×900) and mobile (`mobile-390`, 390×844) viewports:

1. **`specs/buyer-flow.spec.ts`** — Register + Mailpit email OTP + phone OTP verification (`/tai-khoan#xac-minh-sdt`) → Search (`/tim-kiem`) → PDP → Cart (`/gio-hang`) → COD Checkout (`/thanh-toan`) → Order detail & list (`/don-hang/<id>`, `/don-hang`).
2. **`specs/zalopay-mock.spec.ts`** — Register → Add to cart → ZaloPay checkout → Waiting page (`/thanh-toan/cho/<id>`) → Neutral mock simulation page (`/thanh-toan/mock-zalopay`) → Switch payment method (`Đổi sang VietQR`).
3. **`specs/reviews-flow.spec.ts`** — Self-contained delivered order → Confirm receipt (`Đã nhận được hàng`) → Submit 5★ verified review with image → Verify review displays publicly on site PDP (`/san-pham/<slug>`).
4. **`specs/returns-flow.spec.ts`** — Self-contained delivered order → Open return modal → Submit reason + external evidence URL → Verify `Bằng chứng 1 (example.com)` + `nofollow` → Seller accepts return → Reload & verify return tracking code and return label link.

## Prerequisites

- The NexusMart stack is running and seeded (`make seed` publishes `bach-dang.nexusmart.localhost`).
- Next.js web server is reachable at `SITE_BASE_URL` (default `http://bach-dang.nexusmart.localhost:3300`).
- Gateway is reachable at `GATEWAY_URL` (default `http://127.0.0.1:28080`).
- Mailpit API is reachable at `MAILPIT_URL` (default `http://127.0.0.1:8025`).
- An existing Chromium / Google Chrome binary is installed locally (auto-detected or overridden via `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH`).

## Install Dependencies

Skip browser downloads to reuse the local Chromium installation:

```bash
cd tests/playwright
PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm ci
```

## Verify Types & Test Discovery (Offline)

```bash
cd tests/playwright
npx tsc --noEmit
npx playwright test --list
```

## Run Against Custom Endpoints

```bash
cd tests/playwright
SITE_BASE_URL=http://bach-dang.nexusmart.localhost:3403 \
GATEWAY_URL=http://127.0.0.1:28080 \
MAILPIT_URL=http://127.0.0.1:8025 \
SITE_ROOT_DOMAIN=nexusmart.localhost \
npx playwright test
```

## Configuration Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `SITE_BASE_URL` | `http://bach-dang.nexusmart.localhost:3300` | Base URL of the published storefront host under test |
| `GATEWAY_URL` | `http://127.0.0.1:28080` | Gateway base URL for test setup & payment/shipping webhooks |
| `MAILPIT_URL` | `http://127.0.0.1:8025` | Mailpit HTTP API base URL used to fetch 6-digit OTPs |
| `SITE_ROOT_DOMAIN` | `nexusmart.localhost` | Root domain mapped to `127.0.0.1` via Chromium `--host-resolver-rules` |
| `PAYMENT_WEBHOOK_SECRET` | `dev-only-payment-webhook-hmac-secret` | HMAC secret for simulated VietQR payment webhooks |
| `SHIPPING_WEBHOOK_SECRET` | `dev-only-shipping-webhook-hmac-secret` | HMAC secret for simulated carrier shipping webhooks |
| `SEED_SELLER_EMAIL` / `SEED_SELLER_PASSWORD` | `seller@nexusmart.local` / `SellerDemo123!` | Seeded shop seller credentials for fixture fulfillment |
| `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH` | Auto-detected | Explicit Chromium/Chrome binary path override |
