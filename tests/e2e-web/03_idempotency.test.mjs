import assert from "node:assert/strict";
import crypto from "node:crypto";
import { test } from "node:test";
import {
  WebClient,
  calculateShippingQuote,
  createBuyerAddress,
  ensureRateBudget,
  pickSingleShopSku,
  registerAndLoginBuyer,
} from "./helpers.mjs";

test("03. Checkout idempotency via BFF: replaying Idempotency-Key returns idempotent_replay:true without duplicating orders", async () => {
  await ensureRateBudget(20, { label: "03-idempotency" });
  const client = new WebClient();
  const buyer = await registerAndLoginBuyer(client, "idem");
  const { address, province, ward } = await createBuyerAddress(client, buyer);
  const picked = await pickSingleShopSku(client, { minStock: 3 });

  const quote = await calculateShippingQuote(client, {
    shopId: picked.product.shop_id,
    province,
    ward,
    weightGrams: picked.sku.weight_grams || 300,
    codAmountVnd: picked.sku.price_vnd,
  });

  const idemKey = `web-idem-${crypto.randomUUID()}`;
  const checkoutBody = {
    address_id: address.id,
    from_cart: false,
    items: [{ sku_id: picked.sku.id, quantity: 1 }],
    payment_method: "COD",
    shipping_choices: [
      {
        shop_id: picked.product.shop_id,
        carrier_code: quote.carrier_code,
        service_code: quote.service_code,
      },
    ],
  };

  // First submission creates 1 order.
  const firstRes = await client.json("POST", "/api/gw/checkout", {
    headers: { "Idempotency-Key": idemKey },
    body: checkoutBody,
  });
  assert.ok(
    firstRes.status === 201 || firstRes.status === 200,
    `first checkout expected 201/200, got ${firstRes.status}: ${firstRes.text}`
  );
  assert.equal(firstRes.data?.idempotent_replay, false, `first call must not be replay: ${firstRes.text}`);
  assert.ok(firstRes.data?.checkout_group_id, `missing checkout_group_id: ${firstRes.text}`);
  assert.equal(firstRes.data?.orders?.length, 1);

  // Replay with the exact same Idempotency-Key must return the original checkout group & order.
  const replayRes = await client.json("POST", "/api/gw/checkout", {
    headers: { "Idempotency-Key": idemKey },
    body: checkoutBody,
  });
  assert.ok(
    replayRes.status === 200 || replayRes.status === 201,
    `replayed checkout expected 200/201, got ${replayRes.status}: ${replayRes.text}`
  );
  assert.equal(
    replayRes.data?.idempotent_replay,
    true,
    `replayed checkout expected idempotent_replay:true, got ${replayRes.text}`
  );
  assert.equal(
    replayRes.data?.checkout_group_id,
    firstRes.data.checkout_group_id,
    "replayed checkout must return the same checkout_group_id"
  );
  assert.equal(replayRes.data?.orders?.length, 1);
  assert.equal(
    replayRes.data.orders[0].id,
    firstRes.data.orders[0].id,
    "replayed checkout must return the same order id"
  );

  // Order count for this buyer must still be exactly 1.
  const ordersRes = await client.json("GET", "/api/gw/orders?page=1&page_size=20");
  assert.equal(ordersRes.status, 200);
  assert.equal(
    ordersRes.data?.total,
    1,
    `expected total=1 order after idempotent replay, got ${ordersRes.data?.total}`
  );
  assert.equal(
    ordersRes.data?.items?.length,
    1,
    `expected 1 order item after idempotent replay, got ${ordersRes.data?.items?.length}`
  );
});
