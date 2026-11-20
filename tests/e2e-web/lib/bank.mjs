import { createHmac, randomUUID } from "node:crypto";
import { config } from "./config.mjs";

// Plays the external VietQR provider: posts an HMAC-signed "paid" webhook to
// the gateway, exactly as tests/e2e/03_vietqr_checkout_test.go does. The web
// app never sees the secret; the buyer side is observed only through the BFF.
export async function payVietQR({ paymentId, groupId, amountVND }) {
  const body = JSON.stringify({
    provider: "vietqr_mock",
    provider_tx_id: `TXN-WEB-${randomUUID()}`,
    payment_id: paymentId,
    order_group_id: groupId,
    amount_vnd: amountVND,
    status: "SUCCEEDED",
    reference: `NEXUSMART ${groupId}`,
    occurred_at: new Date().toISOString().replace(/\.\d{3}Z$/, "Z"),
  });
  const sig = createHmac("sha256", config.paymentWebhookSecret).update(body).digest("hex");
  const res = await fetch(`${config.gatewayURL}/webhooks/payment`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-signature": sig,
      "x-timestamp": String(Math.floor(Date.now() / 1000)),
    },
    body,
    signal: AbortSignal.timeout(config.httpTimeoutMs),
  });
  const text = await res.text();
  if (res.status !== 200) throw new Error(`VietQR webhook: HTTP ${res.status}: ${text}`);
}
