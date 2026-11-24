import assert from "node:assert/strict";
import crypto from "node:crypto";
import { test } from "node:test";
import {
  ACCESS_COOKIE,
  COD_SAFE_SUBTOTAL_CAP_VND,
  COD_SAFE_TOTAL_CAP_VND,
  REFRESH_COOKIE,
  WebClient,
  calculateShippingQuote,
  createBuyerAddress,
  ensureRateBudget,
  normalizeStatus,
  pickTwoShopSkus,
  registerAndLoginBuyer,
} from "./helpers.mjs";

test("01. Buyer flow via BFF: register -> OTP -> login -> 2-shop cart -> NEXUS10 preview -> COD checkout -> orders", async () => {
  await ensureRateBudget(30, { label: "01-cod" });
  const client = new WebClient();

  // 1. Register -> Mailpit OTP -> Verify OTP -> Login via BFF.
  const buyer = await registerAndLoginBuyer(client, "cod");

  // Login response must NOT expose tokens in JSON body; tokens live only in HttpOnly host-only cookies.
  assert.equal(buyer.loginRes.data?.user?.email, buyer.email);
  assert.equal(buyer.loginRes.data?.user?.role, "buyer");
  assert.equal(buyer.loginRes.data?.access_token, undefined, "access_token must not be in response body");
  assert.equal(buyer.loginRes.data?.refresh_token, undefined, "refresh_token must not be in response body");

  const accessCookie = client.getCookieMeta(ACCESS_COOKIE);
  const refreshCookie = client.getCookieMeta(REFRESH_COOKIE);
  assert.ok(accessCookie?.value, "nm_access_token cookie must be set on login");
  assert.ok(refreshCookie?.value, "nm_refresh_token cookie must be set on login");
  assert.equal(accessCookie.httpOnly, true, "nm_access_token must be HttpOnly");
  assert.equal(refreshCookie.httpOnly, true, "nm_refresh_token must be HttpOnly");
  assert.equal(accessCookie.sameSite, "lax", "nm_access_token must be SameSite=Lax");
  assert.equal(refreshCookie.sameSite, "lax", "nm_refresh_token must be SameSite=Lax");
  assert.equal(accessCookie.domain, null, "nm_access_token must be host-only (no Domain)");
  assert.equal(refreshCookie.domain, null, "nm_refresh_token must be host-only (no Domain)");
  assert.ok(
    !buyer.loginRes.text.includes(accessCookie.value),
    "raw login body must not leak access token"
  );
  assert.ok(
    !buyer.loginRes.text.includes(refreshCookie.value),
    "raw login body must not leak refresh token"
  );

  // Confirm session endpoint reflects authenticated user.
  const sessionRes = await client.json("GET", "/api/auth/session");
  assert.equal(sessionRes.status, 200);
  assert.equal(sessionRes.data?.user?.email, buyer.email);

  // 2. Discover platform voucher (prefer NEXUS10, else any active PLATFORM voucher with remaining quota).
  const vouchersRes = await client.json("GET", "/api/gw/vouchers");
  assert.equal(
    vouchersRes.status,
    200,
    `GET /api/gw/vouchers expected 200, got ${vouchersRes.status}: ${vouchersRes.text}`
  );
  const platformVouchers = (vouchersRes.data?.vouchers ?? []).filter(
    (v) =>
      String(v.scope).toUpperCase() === "PLATFORM" &&
      v.is_active !== false &&
      (v.remaining_quota ?? 1) > 0 &&
      (v.min_order_vnd ?? 0) <= COD_SAFE_SUBTOTAL_CAP_VND
  );
  assert.ok(platformVouchers.length > 0, `no active PLATFORM voucher found: ${vouchersRes.text}`);
  const voucher =
    platformVouchers.find((v) => String(v.code).toUpperCase() === "NEXUS10") ?? platformVouchers[0];

  // 3. Pick 2 SKUs from 2 different shops meeting voucher min_order_vnd while staying well below COD cap (< 2M VND incl. shipping).
  const [item1, item2] = await pickTwoShopSkus(client, {
    minTotalVnd: voucher.min_order_vnd ?? 0,
    maxTotalVnd: COD_SAFE_SUBTOTAL_CAP_VND,
    minStock: 2,
  });
  assert.notEqual(
    item1.product.shop_id,
    item2.product.shop_id,
    "picked SKUs must belong to 2 different shops"
  );

  // Add both SKUs to cart through BFF.
  for (const item of [item1, item2]) {
    const addRes = await client.json("POST", "/api/gw/cart/items", {
      body: { sku_id: item.sku.id, quantity: item.quantity },
    });
    assert.equal(
      addRes.status,
      200,
      `add cart item ${item.sku.id} expected 200, got ${addRes.status}: ${addRes.text}`
    );
  }

  const cartRes = await client.json("GET", "/api/gw/cart");
  assert.equal(cartRes.status, 200);
  const shopGroups = cartRes.data?.cart?.shop_groups ?? [];
  assert.equal(
    shopGroups.length,
    2,
    `expected cart to group into 2 shops, got ${shopGroups.length}: ${cartRes.text}`
  );

  // 4. Create 2-level address and calculate shipping quotes for both shops.
  const { address, province, ward } = await createBuyerAddress(client, buyer);

  const sub1 = item1.sku.price_vnd * item1.quantity;
  const sub2 = item2.sku.price_vnd * item2.quantity;
  assert.ok(
    sub1 + sub2 >= (voucher.min_order_vnd ?? 0),
    `picked cart subtotal (${sub1 + sub2}) must meet voucher ${voucher.code} min_order_vnd (${voucher.min_order_vnd})`
  );
  const quote1 = await calculateShippingQuote(client, {
    shopId: item1.product.shop_id,
    province,
    ward,
    weightGrams: (item1.sku.weight_grams || 300) * item1.quantity,
    codAmountVnd: sub1,
  });
  const quote2 = await calculateShippingQuote(client, {
    shopId: item2.product.shop_id,
    province,
    ward,
    weightGrams: (item2.sku.weight_grams || 300) * item2.quantity,
    codAmountVnd: sub2,
  });
  assert.ok(
    sub1 + sub2 + quote1.fee_vnd + quote2.fee_vnd < COD_SAFE_TOTAL_CAP_VND,
    `COD order total including shipping (${sub1 + sub2 + quote1.fee_vnd + quote2.fee_vnd}) must stay below ${COD_SAFE_TOTAL_CAP_VND} VND`
  );

  // 5. Save and preview platform voucher through BFF.
  const saveVoucherRes = await client.json(
    "POST",
    `/api/gw/vouchers/${encodeURIComponent(voucher.id)}/save`,
    { body: { code: voucher.code } }
  );
  assert.ok(
    saveVoucherRes.status === 200 || saveVoucherRes.status === 201,
    `save voucher ${voucher.code} expected 200/201, got ${saveVoucherRes.status}: ${saveVoucherRes.text}`
  );

  const previewRes = await client.json("POST", "/api/gw/promotions/preview", {
    body: {
      shop_items: [
        {
          shop_id: item1.product.shop_id,
          subtotal_vnd: sub1,
          shipping_fee_vnd: quote1.fee_vnd,
          items: [
            {
              sku_id: item1.sku.id,
              quantity: item1.quantity,
              unit_price_vnd: item1.sku.price_vnd,
            },
          ],
        },
        {
          shop_id: item2.product.shop_id,
          subtotal_vnd: sub2,
          shipping_fee_vnd: quote2.fee_vnd,
          items: [
            {
              sku_id: item2.sku.id,
              quantity: item2.quantity,
              unit_price_vnd: item2.sku.price_vnd,
            },
          ],
        },
      ],
      platform_voucher_id: voucher.id,
    },
  });
  assert.equal(
    previewRes.status,
    200,
    `preview voucher ${voucher.code} expected 200, got ${previewRes.status}: ${previewRes.text}`
  );
  const breakdown = previewRes.data?.breakdown;
  assert.ok(breakdown, `missing breakdown in preview response: ${previewRes.text}`);
  assert.ok(
    breakdown.total_platform_voucher_discount_vnd > 0,
    `expected positive discount for platform voucher ${voucher.code}, got ${JSON.stringify(breakdown)}`
  );
  assert.ok(
    Number.isInteger(breakdown.final_payable_vnd) && breakdown.final_payable_vnd > 0,
    `expected positive integer final_payable_vnd, got ${breakdown.final_payable_vnd}`
  );

  // 6. Checkout COD with Idempotency-Key.
  const idemKey = `web-cod-${crypto.randomUUID()}`;
  const checkoutRes = await client.json("POST", "/api/gw/checkout", {
    headers: { "Idempotency-Key": idemKey },
    body: {
      address_id: address.id,
      from_cart: true,
      platform_voucher_id: voucher.id,
      payment_method: "COD",
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
    `COD checkout expected 201/200, got ${checkoutRes.status}: ${checkoutRes.text}`
  );

  const checkout = checkoutRes.data;
  assert.ok(checkout?.checkout_group_id, `missing checkout_group_id: ${checkoutRes.text}`);
  assert.equal(checkout.idempotent_replay, false);
  assert.equal(
    checkout.orders?.length,
    2,
    `expected 2 shop sub-orders, got ${checkout.orders?.length}: ${checkoutRes.text}`
  );
  assert.ok(
    Number.isInteger(checkout.total_payable_vnd) && checkout.total_payable_vnd > 0,
    `expected positive integer total_payable_vnd, got ${checkout.total_payable_vnd}`
  );

  const subOrderShops = new Set(checkout.orders.map((o) => o.shop_id));
  assert.ok(subOrderShops.has(item1.product.shop_id), "missing sub-order for shop 1");
  assert.ok(subOrderShops.has(item2.product.shop_id), "missing sub-order for shop 2");
  for (const order of checkout.orders) {
    assert.equal(
      normalizeStatus(order.status),
      "CONFIRMED",
      `COD sub-order ${order.id} expected CONFIRMED, got ${order.status}`
    );
  }
  const totalPlatformDiscount = checkout.orders.reduce(
    (sum, o) => sum + (o.platform_voucher_discount_vnd ?? 0),
    0
  );
  assert.ok(
    totalPlatformDiscount > 0,
    `expected applied platform voucher discount across sub-orders, got ${totalPlatformDiscount}`
  );

  // 7. Confirm cart cleared after from_cart checkout and both orders appear in /api/gw/orders.
  const cartAfterRes = await client.json("GET", "/api/gw/cart");
  assert.equal(cartAfterRes.status, 200);
  assert.equal(
    cartAfterRes.data?.cart?.total_items ?? 0,
    0,
    `expected cart cleared after COD checkout, got ${cartAfterRes.text}`
  );

  const ordersRes = await client.json("GET", "/api/gw/orders?page=1&page_size=20");
  assert.equal(
    ordersRes.status,
    200,
    `GET /api/gw/orders expected 200, got ${ordersRes.status}: ${ordersRes.text}`
  );
  const listedIds = new Set((ordersRes.data?.items ?? []).map((o) => o.id));
  for (const order of checkout.orders) {
    assert.ok(listedIds.has(order.id), `order ${order.id} missing from /api/gw/orders`);
    const detailRes = await client.json("GET", `/api/gw/orders/${encodeURIComponent(order.id)}`);
    assert.equal(detailRes.status, 200, `owner read order ${order.id} failed: ${detailRes.text}`);
    assert.equal(detailRes.data?.order?.id, order.id);
  }
});
