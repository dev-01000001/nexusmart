import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { ROOT_HOST, siteHost } from "./lib/config.mjs";
import { Browser, expectStatus } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  assertNoInternalErrorLeak,
  cancelOrderBestEffort,
  checkout,
  createShippingAddress,
  fillCart,
  listOrderIds,
  newIdempotencyKey,
  orderBackendStatus,
  pickPlatformVoucher,
  pickShopVoucher,
  pickSku,
  quoteShipping,
  registerBuyer,
  resolveSite,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);
const SITE_B_HOST = siteHost(SEEDED.saiGon.sub);

describe("09. COD checkout on site host + SHOP voucher & retirement of multi-shop marketplace checkout", () => {
  let siteA;
  let siteB;
  let skuA;
  let skuB;
  let voucherA;
  let platformVoucher;

  before(async () => {
    await resetRateLimitKeys();
    siteA = await resolveSite(SEEDED.bachDang.sub);
    siteB = await resolveSite(SEEDED.saiGon.sub);
    skuA = await pickSku(siteA.shopId, { minStock: 10, maxPriceVnd: 900_000 });
    skuB = await pickSku(siteB.shopId, { minStock: 5, maxPriceVnd: 900_000 });
    voucherA = await pickShopVoucher(siteA.shopId, SEEDED.bachDang.voucherCode);
    platformVoucher = await pickPlatformVoucher("NEXUS30");
  });

  after(async () => {
    await resetRateLimitKeys();
  });

  it("completes single-shop COD checkout on site host with SHOP voucher and persists order detail & SSR", async () => {
    const browser = new Browser("cod-site-a");
    const buyerA = browser.at(SITE_A_HOST);
    let createdOrderId = null;

    try {
      await registerBuyer(buyerA, "cod-v1", { verifyPhone: true });
      const address = await createShippingAddress(buyerA, "cod-v1");

      const neededQty = Math.min(4, Math.max(1, Math.ceil((voucherA.min_order_vnd || 0) / skuA.price)));
      const cart = await fillCart(buyerA, skuA.skuId, neededQty);
      assert.equal(cart.shop_groups.length, 1);
      assert.equal(cart.shop_groups[0].shop_id, siteA.shopId);

      const { shippingChoices, shippingFees } = await quoteShipping(buyerA, address, cart.shop_groups, true);

      const val = expectStatus(
        await buyerA.post("/api/vouchers/validate", {
          code: voucherA.code,
          shipping_fees: shippingFees,
        }),
        200,
        `POST ${SITE_A_HOST}/api/vouchers/validate`
      );
      assert.equal(val.valid, true);
      assert.ok(val.discount_vnd > 0, "SHOP voucher must yield positive discount");

      const preview = expectStatus(
        await buyerA.post("/api/vouchers/preview", {
          shipping_fees: shippingFees,
          shop_voucher_ids: { [siteA.shopId]: voucherA.id },
        }),
        200,
        `POST ${SITE_A_HOST}/api/vouchers/preview`
      );
      assert.ok(preview.breakdown.total_shop_voucher_discount_vnd > 0);
      assert.equal(preview.breakdown.total_platform_voucher_discount_vnd ?? 0, 0);

      const coRes = await checkout(buyerA, {
        addressId: address.id,
        shippingChoices,
        paymentMethod: "COD",
        extra: { shop_voucher_ids: { [siteA.shopId]: voucherA.id } },
        key: newIdempotencyKey("cod-09"),
      });
      const co = expectStatus(coRes, 201, `POST ${SITE_A_HOST}/api/checkout (COD)`);
      assert.equal(co.payment_method, "COD");
      assert.equal(co.idempotent_replay, false);
      assert.equal(co.sales_channel, "STOREFRONT");
      assert.equal(co.storefront_shop_id, siteA.shopId);
      assert.equal(co.orders.length, 1);

      const created = co.orders[0];
      createdOrderId = created.id;
      assert.equal(created.shop_id, siteA.shopId);
      assert.equal(orderBackendStatus(created), "CONFIRMED");

      // Cart cleared after COD checkout.
      const cartAfter = expectStatus(await buyerA.get("/api/cart"), 200, "GET /api/cart after COD");
      assert.equal(cartAfter.cart.total_items, 0);

      // Order listed + detailed + SSR 200 with order_code.
      const listed = await listOrderIds(buyerA);
      assert.ok(listed.ids.includes(created.id), `order ${created.id} must appear in GET /api/orders`);

      const detail = expectStatus(
        await buyerA.get(`/api/orders/${encodeURIComponent(created.id)}`),
        200,
        `GET /api/orders/${created.id}`
      );
      assert.equal(detail.order.id, created.id);
      assert.equal(detail.order.paymentMethod, "cod");
      assert.ok(Number(detail.order.shopVoucherDiscount || 0) > 0);

      const ssr = await buyerA.page(`/don-hang/${encodeURIComponent(created.id)}`);
      expectStatus(ssr, 200, `GET ${SITE_A_HOST}/don-hang/${created.id}`);
      assert.ok(ssr.text.includes(created.order_code), `SSR /don-hang/${created.id} must show order_code`);
      assertNoInternalErrorLeak(ssr.text, `SSR /don-hang/${created.id}`);
    } finally {
      await cancelOrderBestEffort(buyerA, createdOrderId, "09_cod_checkout cleanup");
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("confirms multi-shop marketplace cart/checkout & platform vouchers are retired on root and site hosts", async () => {
    const anonRoot = new Browser("cod-root-retired").at(ROOT_HOST);
    for (const path of ["/gio-hang", "/thanh-toan", "/don-hang", "/danh-muc"]) {
      const pageRes = await anonRoot.page(path);
      assert.equal(pageRes.status, 404, `root GET ${path} must return 404 after marketplace removal, got ${pageRes.status}`);
    }
    for (const [method, path, json] of [
      ["GET", "/api/cart"],
      ["POST", "/api/checkout", { from_cart: true, payment_method: "COD" }],
      ["GET", "/api/orders"],
      ["GET", "/api/gw/cart"],
      ["POST", "/api/gw/checkout", {}],
    ]) {
      const apiRes = await anonRoot.request(method, path, { json });
      assert.equal(apiRes.status, 404, `root ${method} ${path} must return 404, got ${apiRes.status}`);
    }

    // Site A refuses foreign shop B SKU (multi-shop cart impossible on storefront) and platform vouchers.
    const buyerA = new Browser("cod-site-reject").at(SITE_A_HOST);
    try {
      await registerBuyer(buyerA, "cod-reject", { verifyPhone: true });
      const addr = await createShippingAddress(buyerA, "cod-reject");
      const cart = await fillCart(buyerA, skuA.skuId, 1);

      const foreignAdd = await buyerA.post("/api/cart/items", { sku_id: skuB.skuId, quantity: 1 });
      expectStatus(foreignAdd, [400, 404], "site A must reject adding shop B SKU to cart");

      if (platformVoucher) {
        const { shippingChoices } = await quoteShipping(buyerA, addr, cart.shop_groups, true);
        const coPlat = await checkout(buyerA, {
          addressId: addr.id,
          shippingChoices,
          paymentMethod: "COD",
          extra: { platform_voucher_id: platformVoucher.id },
          key: newIdempotencyKey("cod-plat-reject"),
        });
        expectStatus(coPlat, [400, 404], "site A checkout must reject platform_voucher_id");
      }
    } finally {
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });
});
