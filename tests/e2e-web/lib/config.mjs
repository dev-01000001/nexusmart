// Endpoints for the web E2E suite. Defaults target the compose network
// (`make e2e-web`); override with env vars to run from the host against a
// dev server, e.g.
//   WEB_URL=http://localhost:3210 MAILPIT_URL=http://localhost:8025 \
//   GATEWAY_URL=http://localhost:28080 node --test tests/e2e-web/
const env = (name, fallback) => (process.env[name] ?? "").trim() || fallback;

export const config = {
  webURL: env("WEB_URL", "http://web:3000").replace(/\/$/, ""),
  // Origin the BFF accepts for write requests (its own public origin).
  webOrigin: env("WEB_ORIGIN", ""),
  mailpitURL: env("MAILPIT_URL", "http://mailpit:8025").replace(/\/$/, ""),
  // Gateway is only used to play the external bank: the VietQR mock webhook
  // is HMAC-signed and posted the way the payment provider would.
  gatewayURL: env("GATEWAY_URL", "http://gateway:8080").replace(/\/$/, ""),
  siteRootDomain: env("SITE_ROOT_DOMAIN", "nexusmart.localhost").toLowerCase(),
  redisAddr: env("REDIS_ADDR", ""),
  paymentWebhookSecret: env("PAYMENT_WEBHOOK_SECRET", "dev-only-payment-webhook-hmac-secret"),
  httpTimeoutMs: Number(env("HTTP_TIMEOUT_MS", "20000")),
  pollTimeoutMs: Number(env("POLL_TIMEOUT_MS", "30000")),
};

if (!config.webOrigin) config.webOrigin = new URL(config.webURL).origin;
if (!config.redisAddr && config.gatewayURL.includes("://gateway:")) {
  config.redisAddr = "redis:6379";
}
