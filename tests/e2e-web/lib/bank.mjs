import { createHmac, randomInt, randomUUID } from "node:crypto";
import { config } from "./config.mjs";
import { parseRetryAfterMs, RATE_LIMIT_MAX_WAIT_MS, sleep } from "./http.mjs";

async function fetchJsonWith429(url, initFactory) {
  let totalWaitedMs = 0;
  while (true) {
    const res = await fetch(url, initFactory());
    if (res.status === 429) {
      const waitMs = parseRetryAfterMs(res.headers.get("retry-after")) + 250;
      if (totalWaitedMs + waitMs <= RATE_LIMIT_MAX_WAIT_MS) {
        await res.body?.cancel().catch(() => {});
        totalWaitedMs += waitMs;
        await sleep(waitMs);
        continue;
      }
    }
    const text = await res.text();
    let data = null;
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = null;
      }
    }
    return { status: res.status, text, data };
  }
}

/**
 * Simulates the external VietQR provider calling Gateway `/webhooks/payment` directly
 * with HMAC-SHA256 `X-Signature` and `X-Timestamp`.
 */
export async function postSignedPaymentWebhook(payload, options = {}) {
  const secret = options.secret ?? config.paymentWebhookSecret;
  const rawBody = JSON.stringify(payload);
  const signature =
    options.overrideSignature ??
    createHmac("sha256", secret).update(rawBody, "utf8").digest("hex");
  const timestamp = String(options.timestampSec ?? Math.floor(Date.now() / 1000));

  return fetchJsonWith429(`${config.gatewayURL}/webhooks/payment`, () => ({
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-signature": signature,
      "x-timestamp": timestamp,
    },
    body: rawBody,
    signal: AbortSignal.timeout(config.httpTimeoutMs),
  }));
}

// Plays the external VietQR provider: posts an HMAC-signed "paid" webhook to
// the gateway, exactly as tests/e2e/03_vietqr_checkout_test.go does. The web
// app never sees the secret; the buyer side is observed only through the BFF.
export async function payVietQR({ paymentId, groupId, amountVND, providerTxId }) {
  const res = await postSignedPaymentWebhook({
    provider: "vietqr_mock",
    provider_tx_id: providerTxId ?? `TXN-WEB-${randomUUID()}`,
    payment_id: paymentId,
    order_group_id: groupId,
    amount_vnd: amountVND,
    status: "SUCCEEDED",
    reference: `NEXUSMART ${groupId}`,
    occurred_at: new Date().toISOString().replace(/\.\d{3}Z$/, "Z"),
  });
  if (res.status !== 200) throw new Error(`VietQR webhook: HTTP ${res.status}: ${res.text}`);
  return res;
}

/**
 * Simulates a shipping carrier calling Gateway `/webhooks/shipping/{carrier}` directly
 * with HMAC-SHA256 `X-Signature` and `X-Timestamp`.
 */
export async function postSignedShippingWebhook(carrier, payload, options = {}) {
  const secret = options.secret ?? config.shippingWebhookSecret;
  const rawBody = JSON.stringify(payload);
  const signature =
    options.overrideSignature ??
    createHmac("sha256", secret).update(rawBody, "utf8").digest("hex");
  const timestamp = String(options.timestampSec ?? Math.floor(Date.now() / 1000));

  return fetchJsonWith429(
    `${config.gatewayURL}/webhooks/shipping/${encodeURIComponent(carrier || "mock_express")}`,
    () => ({
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-signature": signature,
        "x-timestamp": timestamp,
      },
      body: rawBody,
      signal: AbortSignal.timeout(config.httpTimeoutMs),
    })
  );
}

/**
 * Drives a shipment through `delivering` -> `delivered` via signed shipping webhooks.
 */
export async function deliverShipmentViaWebhook({
  carrierCode = "mock_express",
  trackingCode,
  orderId,
  secret,
}) {
  const whDelivering = await postSignedShippingWebhook(
    carrierCode,
    {
      event_id: `e2e-ship-deliv-${randomUUID()}`,
      tracking_code: trackingCode,
      order_id: orderId,
      raw_status: "delivering",
      description: "Đang giao kiện hàng",
      occurred_at: new Date().toISOString(),
    },
    { secret }
  );
  if (whDelivering.status !== 200) {
    throw new Error(`shipping delivering webhook HTTP ${whDelivering.status}: ${whDelivering.text}`);
  }
  const whDelivered = await postSignedShippingWebhook(
    carrierCode,
    {
      event_id: `e2e-ship-done-${randomUUID()}`,
      tracking_code: trackingCode,
      order_id: orderId,
      raw_status: "delivered",
      description: "Giao kiện hàng thành công",
      occurred_at: new Date().toISOString(),
    },
    { secret }
  );
  if (whDelivered.status !== 200) {
    throw new Error(`shipping delivered webhook HTTP ${whDelivered.status}: ${whDelivered.text}`);
  }
  return { whDelivering, whDelivered };
}

/**
 * Simulates a signed ZaloPay v2 callback to Gateway `/webhooks/zalopay`.
 */
export async function postSignedZaloPayCallback({
  appTransId,
  amountVnd,
  zpTransId,
  appId = config.zaloPayAppId,
  key2 = config.zaloPayKey2,
  appUser = "nexusmart-e2e-web",
}) {
  const nowMs = Date.now();
  const dataStr = JSON.stringify({
    app_id: appId,
    app_trans_id: appTransId,
    app_time: nowMs,
    app_user: appUser,
    amount: amountVnd,
    embed_data: "{}",
    item: "[]",
    zp_trans_id: zpTransId ?? nowMs * 1000 + randomInt(1, 999),
    server_time: nowMs,
    channel: 38,
    merchant_user_id: "",
    user_fee_amount: 0,
    discount_amount: 0,
  });
  const mac = createHmac("sha256", key2).update(dataStr, "utf8").digest("hex");
  return fetchJsonWith429(`${config.gatewayURL}/webhooks/zalopay`, () => ({
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ data: dataStr, mac, type: 1 }),
    signal: AbortSignal.timeout(config.httpTimeoutMs),
  }));
}
