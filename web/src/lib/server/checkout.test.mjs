// Run: node --import 'data:text/javascript,import{register}from"node:module";import{pathToFileURL}from"node:url";register(pathToFileURL("tools/seed/ts-resolve.mjs"));' --test web/src/lib/server/checkout.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { buildStorefrontCheckoutPayload } from "../../features/commerce/checkoutPayload.ts";

test("buildStorefrontCheckoutPayload locks STOREFRONT + siteShopId and ignores spoofed channel/shop", () => {
  const spoofedChannel = ["MARKET", "PLACE"].join("");
  const res = buildStorefrontCheckoutPayload(
    {
      address_id: " addr-1 ",
      from_cart: true,
      payment_method: "cod",
      sales_channel: spoofedChannel,
      storefront_shop_id: "shop-evil-b",
      checkout_channel_hint: "marketplace",
      shop_voucher_id: " v-shop-a ",
      shipping_choice: { carrier_code: "GHN", service_code: "STD" },
      platform_voucher_id: "",
      freeship_voucher_id: "",
      coins_to_use: 0,
    },
    "shop-a"
  );

  assert.equal(res.ok, true);
  if (res.ok) {
    assert.equal(res.payload.address_id, "addr-1");
    assert.equal(res.payload.from_cart, true);
    assert.equal(res.payload.payment_method, "COD");
    assert.equal(res.payload.sales_channel, "STOREFRONT");
    assert.equal(res.payload.storefront_shop_id, "shop-a");
    assert.deepEqual(res.payload.shop_voucher_ids, { "shop-a": "v-shop-a" });
    assert.deepEqual(res.payload.shipping_choices, [
      { shop_id: "shop-a", carrier_code: "GHN", service_code: "STD" },
    ]);
    assert.equal("platform_voucher_id" in res.payload, false);
    assert.equal("freeship_voucher_id" in res.payload, false);
    assert.equal("coins_to_use" in res.payload, false);
  }
});

test("buildStorefrontCheckoutPayload supports legacy keyed shop_voucher_ids and shipping_choices", () => {
  const res = buildStorefrontCheckoutPayload(
    {
      address_id: "addr-2",
      payment_method: "VIETQR",
      items: [
        { sku_id: " sku-1 ", quantity: 2, flash_sale_item_id: " fs-1 " },
        { sku_id: "", quantity: 3 },
        { sku_id: "sku-zero", quantity: 0 },
      ],
      shop_voucher_ids: { "shop-a": "v-a", "shop-b": "" },
      shipping_choices: [
        { shop_id: "shop-b", carrier_code: "SPX", service_code: "STD" },
        { shop_id: "shop-a", carrier_code: "GHN", service_code: "EXP" },
      ],
    },
    "shop-a"
  );

  assert.equal(res.ok, true);
  if (res.ok) {
    assert.equal(res.payload.from_cart, false);
    assert.deepEqual(res.payload.items, [
      { sku_id: "sku-1", quantity: 2, flash_sale_item_id: "fs-1" },
    ]);
    assert.deepEqual(res.payload.shop_voucher_ids, { "shop-a": "v-a" });
    assert.deepEqual(res.payload.shipping_choices, [
      { shop_id: "shop-a", carrier_code: "GHN", service_code: "EXP" },
    ]);
    assert.equal(res.payload.sales_channel, "STOREFRONT");
    assert.equal(res.payload.storefront_shop_id, "shop-a");
  }

  // Shipping choice without shop_id in array still gets stamped with siteShopId
  const noShopIdShipping = buildStorefrontCheckoutPayload(
    {
      address_id: "addr-2",
      payment_method: "COD",
      shipping_choices: [{ carrier_code: "GHN", service_code: "STD" }],
    },
    "shop-a"
  );
  assert.equal(noShopIdShipping.ok, true);
  if (noShopIdShipping.ok) {
    assert.deepEqual(noShopIdShipping.payload.shipping_choices, [
      { shop_id: "shop-a", carrier_code: "GHN", service_code: "STD" },
    ]);
  }
});

test("buildStorefrontCheckoutPayload rejects foreign shop vouchers and platform/freeship/coins promos", () => {
  assert.deepEqual(
    buildStorefrontCheckoutPayload(
      {
        address_id: "addr-1",
        payment_method: "COD",
        shop_voucher_ids: { "shop-b": "v-b" },
      },
      "shop-a"
    ),
    { ok: false, code: "FOREIGN_SHOP_VOUCHER" }
  );

  assert.deepEqual(
    buildStorefrontCheckoutPayload(
      {
        address_id: "addr-1",
        payment_method: "COD",
        platform_voucher_id: "v-plat",
      },
      "shop-a"
    ),
    { ok: false, code: "PLATFORM_PROMO_NOT_ALLOWED" }
  );

  assert.deepEqual(
    buildStorefrontCheckoutPayload(
      {
        address_id: "addr-1",
        payment_method: "COD",
        freeship_voucher_id: "v-ship",
      },
      "shop-a"
    ),
    { ok: false, code: "PLATFORM_PROMO_NOT_ALLOWED" }
  );

  assert.deepEqual(
    buildStorefrontCheckoutPayload(
      {
        address_id: "addr-1",
        payment_method: "COD",
        coins_to_use: 5000,
      },
      "shop-a"
    ),
    { ok: false, code: "PLATFORM_PROMO_NOT_ALLOWED" }
  );
});

test("buildStorefrontCheckoutPayload validates siteShopId, address_id, and payment_method", () => {
  assert.deepEqual(
    buildStorefrontCheckoutPayload({ address_id: "addr-1", payment_method: "COD" }, "   "),
    { ok: false, code: "SITE_NOT_PUBLISHED" }
  );
  assert.deepEqual(
    buildStorefrontCheckoutPayload({ address_id: "", payment_method: "COD" }, "shop-a"),
    { ok: false, code: "MISSING_ADDRESS" }
  );
  assert.deepEqual(
    buildStorefrontCheckoutPayload({ address_id: "addr-1", payment_method: "CRYPTO" }, "shop-a"),
    { ok: false, code: "INVALID_PAYMENT_METHOD" }
  );
});
