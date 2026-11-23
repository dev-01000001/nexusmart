import assert from "node:assert/strict";
import crypto from "node:crypto";
import { test } from "node:test";
import {
  WebClient,
  calculateShippingQuote,
  createBuyerAddress,
  ensureRateBudget,
  normalizeStatus,
  pickTwoShopSkus,
  pollUntil,
  postSignedPaymentWebhook,
  registerAndLoginBuyer,
} from "./helpers.mjs";

test("02. VietQR mock via BFF: checkout VIETQR -> HMAC webhook to gateway -> payment SUCCEEDED & orders CONFIRMED", async () => {
  await ensureRateBudget(30, { label: "02-vietqr" });
  const client = new WebClient();
  const buyer = await registerAndLoginBuyer(client, "vietqr");
  const { address, province, ward } = await createBuyerAddress(client, buyer);

  const [item1, item2] = await pickTwoShopSkus(client, { minStock: 2 });
  for (const item of [item1, item2]) {
    const addRes = await client.json("POST", "/api/gw/cart/items", {
      body: { sku_id: item.sku.id, quantity: 1 },
    });
    assert.equal(
      addRes.status,
      200,
      `add cart item ${item.sku.id} expected 200, got ${addRes.status}: ${addRes.text}`
    );
  }

  const quote1 = await calculateShippingQuote(client, {
    shopId: item1.product.shop_id,
    province,
    ward,
    weightGrams: item1.sku.weight_grams || 300,
    codAmountVnd: 0,
  });
  const quote2 = await calculateShippingQuote(client, {
    shopId: item2.product.shop_id,
    province,
    ward,
    weightGrams: item2.sku.weight_grams || 300,
    codAmountVnd: 0,
  });

  // Checkout VIETQR through BFF.
  const checkoutRes = await client.json("POST", "/api/gw/checkout", {
    headers: { "Idempotency-Key": `web-vietqr-${crypto.randomUUID()}` },
    body: {
      address_id: address.id,
      from_cart: true,
      payment_method: "VIETQR",
      shipping_choices: [
        {
          shop_id: item1.product.shop_id,
          carrier_code: quote1.carrier_code,
          service_code: quote1.service_code,
        },
        {
          shop_id: item2.product.shop_id,
          carrier_code: quote2.carrier_code,
          service_code: quote2.service_code,
        },
      ],
    },
  });
  assert.ok(
    checkoutRes.status === 201 || checkoutRes.status === 200,
    `VIETQR checkout expected 201/200, got ${checkoutRes.status}: ${checkoutRes.text}`
  );

  const checkout = checkoutRes.data;
  assert.ok(checkout?.checkout_group_id, `missing checkout_group_id: ${checkoutRes.text}`);
  assert.ok(checkout?.payment_id, `missing payment_id: ${checkoutRes.text}`);
  assert.ok(checkout.qr_url || checkout.qr_code, `missing qr_url/qr_code: ${checkoutRes.text}`);
  assert.ok(
    Number.isInteger(checkout.total_payable_vnd) && checkout.total_payable_vnd > 0,
    `expected positive integer total_payable_vnd, got ${checkout.total_payable_vnd}`
  );
  assert.equal(checkout.orders?.length, 2, `expected 2 VietQR sub-orders: ${checkoutRes.text}`);
  for (const order of checkout.orders) {
    assert.equal(
      normalizeStatus(order.status),
      "PENDING",
      `VietQR sub-order ${order.id} should start PENDING, got ${order.status}`
    );
  }

  // Payment must start PENDING before webhook arrives.
  const payBefore = await client.json(
    "GET",
    `/api/gw/payments/${encodeURIComponent(checkout.payment_id)}`
  );
  assert.equal(
    payBefore.status,
    200,
    `GET payment before webhook expected 200, got ${payBefore.status}: ${payBefore.text}`
  );
  assert.equal(
    normalizeStatus(payBefore.data?.payment?.status),
    "PENDING",
    `expected payment PENDING before webhook, got ${payBefore.data?.payment?.status}`
  );

  // External payment provider sends HMAC-SHA256 signed webhook directly to Gateway.
  const webhookRes = await postSignedPaymentWebhook({
    provider: "vietqr_mock",
    provider_tx_id: `TXN-WEB-E2E-${crypto.randomUUID()}`,
    payment_id: checkout.payment_id,
    order_group_id: checkout.checkout_group_id,
    amount_vnd: checkout.total_payable_vnd,
    status: "SUCCEEDED",
    reference: `NEXUSMART ${checkout.checkout_group_id}`,
    occurred_at: new Date().toISOString(),
  });
  assert.equal(
    webhookRes.status,
    200,
    `POST /webhooks/payment expected 200, got ${webhookRes.status}: ${webhookRes.text}`
  );

  // Poll BFF until payment transitions to SUCCEEDED.
  await pollUntil(
    async () => {
      const res = await client.json(
        "GET",
        `/api/gw/payments/${encodeURIComponent(checkout.payment_id)}`
      );
      if (res.status !== 200) {
        return { done: false, detail: `HTTP ${res.status}: ${res.text}` };
      }
      const status = normalizeStatus(res.data?.payment?.status);
      return { done: status === "SUCCEEDED", detail: `payment.status=${status}` };
    },
    { label: `payment ${checkout.payment_id} -> SUCCEEDED` }
  );

  // Poll BFF until every sub-order transitions PENDING -> CONFIRMED.
  for (const subOrder of checkout.orders) {
    await pollUntil(
      async () => {
        const res = await client.json(
          "GET",
          `/api/gw/orders/${encodeURIComponent(subOrder.id)}`
        );
        if (res.status !== 200) {
          return { done: false, detail: `HTTP ${res.status}: ${res.text}` };
        }
        const status = normalizeStatus(res.data?.order?.status);
        return { done: status === "CONFIRMED", detail: `order.status=${status}` };
      },
      { label: `VietQR sub-order ${subOrder.id} -> CONFIRMED` }
    );
  }
});
