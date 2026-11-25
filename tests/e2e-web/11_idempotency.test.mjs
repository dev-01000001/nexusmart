import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { siteHost } from "./lib/config.mjs";
import { Browser, expectStatus } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  cancelOrderBestEffort,
  createShippingAddress,
  listOrderIds,
  newIdempotencyKey,
  pickSku,
  quoteShipping,
  registerBuyer,
  resolveSite,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);

describe("11. Checkout idempotency replay on site host (from_cart: false)", () => {
  let siteA;
  let skuA;

  before(async () => {
    await resetRateLimitKeys();
    siteA = await resolveSite(SEEDED.bachDang.sub);
    skuA = await pickSku(siteA.shopId, { minStock: 10, maxPriceVnd: 900_000 });
  });

  after(async () => {
    await resetRateLimitKeys();
  });

  it("replaying the same Idempotency-Key returns 200 idempotent_replay=true and creates strictly 1 order", async () => {
    const buyerA = new Browser("idem-site-a").at(SITE_A_HOST);
    let createdOrderId = null;

    try {
      await registerBuyer(buyerA, "idem-11", { verifyPhone: true });
      const address = await createShippingAddress(buyerA, "idem-11");
      const { shippingChoices } = await quoteShipping(
        buyerA,
        address,
        [{ shop_id: siteA.shopId, subtotal_vnd: skuA.price, total_weight_grams: 400 }],
        true
      );

      const idemKey = newIdempotencyKey("idem-direct");
      const payload = {
        address_id: address.id,
        from_cart: false,
        payment_method: "COD",
        items: [{ sku_id: skuA.skuId, quantity: 1 }],
        shipping_choices: shippingChoices,
      };

      const first = expectStatus(
        await buyerA.post("/api/checkout", payload, { headers: { "Idempotency-Key": idemKey } }),
        201,
        "first checkout with Idempotency-Key"
      );
      assert.equal(first.idempotent_replay, false);
      assert.equal(first.orders.length, 1);
      createdOrderId = first.orders[0].id;

      const replay = expectStatus(
        await buyerA.post("/api/checkout", payload, { headers: { "Idempotency-Key": idemKey } }),
        200,
        "replay checkout with identical Idempotency-Key"
      );
      assert.equal(replay.idempotent_replay, true);
      assert.equal(replay.checkout_group_id, first.checkout_group_id);
      assert.equal(replay.orders.length, 1);
      assert.equal(replay.orders[0].id, createdOrderId);

      const listed = await listOrderIds(buyerA);
      assert.equal(listed.total, 1, `expected strictly 1 order after idempotent replay, got ${listed.total}`);
      assert.deepEqual(listed.ids, [createdOrderId]);
    } finally {
      await cancelOrderBestEffort(buyerA, createdOrderId, "11_idempotency cleanup");
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });
});
