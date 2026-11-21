# NexusMart Playwright E2E Suite

End-to-end browser verification for the primary buyer shopping flow on a published NexusMart shop storefront host:

1. **Register + Mailpit OTP + Login** (`/dang-ky`, Mailpit API, `/dang-nhap` fallback)
2. **Search** (`/tim-kiem`, click into seeded product detail page `/san-pham/...`)
3. **Add to cart & View cart** (select in-stock SKU, click `Thêm vào giỏ hàng`, assert `/gio-hang`)
4. **Checkout COD** (`/thanh-toan`, add shipping address, wait for shipping quote, select COD, submit order)
5. **View order** (`/don-hang/<id>` detail and `/don-hang` list)

## Prerequisites

- The NexusMart stack is running and seeded (`make seed` publishes `bach-dang.nexusmart.localhost`).
- Next.js web server is reachable at `http://bach-dang.nexusmart.localhost:3300` (or a custom `SITE_BASE_URL`).
- Mailpit API is reachable at `http://127.0.0.1:8025` (or a custom `MAILPIT_URL`).
- An existing Chromium / Google Chrome binary is installed locally (auto-detected from `/home/dev/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome` or `/usr/bin/google-chrome`, or overridden via `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH`).

## Install Dependencies

Skip browser downloads to reuse the local Chromium installation:

```bash
cd tests/playwright
PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm install
```

## Verify Types & Test Discovery (Offline)

```bash
cd tests/playwright
npx tsc --noEmit
npx playwright test --list
```

## Run Against the Local Stack (Port 3300)

Chromium launches with `--host-resolver-rules=MAP *.nexusmart.localhost 127.0.0.1,MAP nexusmart.localhost 127.0.0.1`, so `*.nexusmart.localhost` resolves without modifying `/etc/hosts`.

```bash
cd tests/playwright
npx playwright test
```

## Run Against Custom Endpoints

```bash
cd tests/playwright
SITE_BASE_URL=http://bach-dang.nexusmart.localhost:3403 \
MAILPIT_URL=http://127.0.0.1:8025 \
SITE_ROOT_DOMAIN=nexusmart.localhost \
npx playwright test
```

## Configuration Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `SITE_BASE_URL` | `http://bach-dang.nexusmart.localhost:3300` | Base URL of the published storefront host under test |
| `MAILPIT_URL` | `http://127.0.0.1:8025` | Mailpit HTTP API base URL used to fetch 6-digit registration OTPs |
| `SITE_ROOT_DOMAIN` | `nexusmart.localhost` | Root domain mapped to `127.0.0.1` via Chromium `--host-resolver-rules` |
| `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH` | Auto-detected | Explicit Chromium/Chrome binary path override |

## Reports & Traces

- HTML report is written to `tests/playwright/playwright-report` (`open: "never"`).
- Failure traces and screenshots are saved under `tests/playwright/test-results`.
- View the latest report locally with `npm run report`.
