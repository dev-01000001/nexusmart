// Endpoints for the web E2E suite. Defaults target the compose network
// (`make e2e-web`); override with env vars to run from the host against a
// dev server, e.g.
//   WEB_URL=http://localhost:3403 HOST_PORT=3403 MAILPIT_URL=http://localhost:8025 \
//   GATEWAY_URL=http://localhost:28080 REDIS_ADDR=localhost:26379 node --test tests/e2e-web/
//
// Every request to the web app carries an explicit `Host` header: the root
// platform host (`SITE_ROOT_DOMAIN`) or a shop site (`<sub>.SITE_ROOT_DOMAIN`).
// The TCP connection always goes to WEB_URL.
const env = (name, fallback) => (process.env[name] ?? "").trim() || fallback;

const webURL = env("WEB_URL", "http://web:3000").replace(/\/$/, "");
const siteRootDomain = env("SITE_ROOT_DOMAIN", "nexusmart.localhost").toLowerCase();
// Optional port appended to every Host header (empty inside compose).
const hostPort = env("HOST_PORT", "");

export const config = {
  webURL,
  webProtocol: new URL(webURL).protocol.replace(/:$/, ""),
  mailpitURL: env("MAILPIT_URL", "http://mailpit:8025").replace(/\/$/, ""),
  // The gateway is used for test discovery (published sites, SKUs, vouchers) and to
  // simulate external actors (payment/shipping/ZaloPay HMAC webhooks). Buyer/seller/admin
  // actions go through the Next.js BFF.
  gatewayURL: env("GATEWAY_URL", "http://gateway:8080").replace(/\/$/, ""),
  siteRootDomain,
  hostPort,
  redisAddr: env("REDIS_ADDR", ""),
  paymentWebhookSecret: env("PAYMENT_WEBHOOK_SECRET", "dev-only-payment-webhook-hmac-secret"),
  shippingWebhookSecret: env("SHIPPING_WEBHOOK_SECRET", "dev-only-shipping-webhook-hmac-secret"),
  zaloPayAppId: Number(env("ZALOPAY_APP_ID", "1")) || 1,
  zaloPayKey2: env("ZALOPAY_KEY2", "dev-only-zalopay-mock-key2"),
  expectProductFallback: env("E2E_EXPECT_PRODUCT_FALLBACK", "") === "1",
  httpTimeoutMs: Number(env("HTTP_TIMEOUT_MS", "20000")),
  pollTimeoutMs: Number(env("POLL_TIMEOUT_MS", "30000")),
  pollIntervalMs: Number(env("POLL_INTERVAL_MS", "400")),
};

if (!config.redisAddr && config.gatewayURL.includes("://gateway:")) {
  config.redisAddr = "redis:6379";
}

/** Host header value (with optional port) for a bare hostname. */
export function withPort(hostname) {
  return config.hostPort ? `${hostname}:${config.hostPort}` : hostname;
}

/** Root platform host header. */
export const ROOT_HOST = withPort(siteRootDomain);

/** Shop site host header for a subdomain label. */
export function siteHost(subdomain) {
  return withPort(`${subdomain}.${siteRootDomain}`);
}

/** Origin a page served on `host` sends with its writes. */
export function originOf(host) {
  return `${config.webProtocol}://${host}`;
}
