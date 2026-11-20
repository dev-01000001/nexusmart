import { before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { payVietQR } from "./lib/bank.mjs";
import { config } from "./lib/config.mjs";
import { WebSession, expectStatus, pollUntil } from "./lib/http.mjs";
import {
  assertNoInternalErrorLeak,
  createShippingAddress,
  demoCredentials,
  discoverTwoShopSkus,
  getShippingChoicesForCart,
  loginAccount,
  newIdempotencyKey,
  pickEligiblePlatformVoucher,
  registerVerifyLoginBuyer,
} from "./lib/fixtures.mjs";

const HTML_HEADERS = { accept: "text/html,application/xhtml+xml" };

describe("02. Buyer Commerce & Security Suite (Cart, Voucher, COD, VietQR, Idempotency, IDOR, 401, CSRF, RBAC)", () => {
  let sku1;
  let sku2;
  let buyerA;
  let addrA;
  let codOrderCount = 0;
  let vietqrOrderCount = 0;
  let buyerAVietqrOrder;
  let buyerAVietqrPaymentId;

  before(async () => {
    [sku1, sku2] = await discoverTwoShopSkus({ minStock: 5 });
    buyerA = await registerVerifyLoginBuyer("buyer-a");
    addrA = await createShippingAddress(buyerA.session, "buyer-a");
  });

  it("(b)(c)(d) adds items from 2 seeded shops, applies a real voucher, checks out COD, and verifies orders", async () => {
    const { session } = buyerA;

    // Add items from 2 DIFFERENT seeded shops into cart via BFF.
    const add1 = expectStatus(
      await session.post("/api/cart/items", { sku_id: sku1.skuId, quantity: 1 }),
      200,
      "POST /api/cart/items (shop 1)"
    );
    assert.equal(add1.cart.total_items, 1);

    const add2 = expectStatus(
      await session.post("/api/cart/items", { sku_id: sku2.skuId, quantity: 1 }),
      200,
      "POST /api/cart/items (shop 2)"
    );
    assert.equal(add2.cart.total_items, 2);

    const cartData = expectStatus(await session.get("/api/cart"), 200, "GET /api/cart");
    let activeCart = cartData.cart;
    assert.equal(activeCart.shop_groups.length, 2, "cart must group items into 2 distinct shops");
    assert.equal(
      new Set(activeCart.shop_groups.map((g) => g.shop_id)).size,
      2,
      "cart shop_groups must have 2 different shop_ids"
    );
    const cartSkuIds = new Set(activeCart.shop_groups.flatMap((g) => g.items.map((i) => i.sku_id)));
    assert.ok(cartSkuIds.has(sku1.skuId), "cart must contain sku1");
    assert.ok(cartSkuIds.has(sku2.skuId), "cart must contain sku2");
    assert.ok(activeCart.total_vnd > 0, "cart total_vnd must be positive");

    if (activeCart.total_vnd < 350_000) {
      const patched = expectStatus(
        await session.patch(`/api/cart/items/${encodeURIComponent(sku1.skuId)}`, { quantity: 2 }),
        200,
        "PATCH /api/cart/items/[skuId]"
      );
      activeCart = patched.cart;
      assert.equal(activeCart.total_items, 3);
    }

    // Apply a real seeded voucher via BFF (save + validate + preview).
    const voucher = await pickEligiblePlatformVoucher(session, activeCart.total_vnd);
    const saveData = expectStatus(
      await session.post(`/api/vouchers/${encodeURIComponent(voucher.id)}/save`, {}),
      200,
      `POST /api/vouchers/${voucher.code}/save`
    );
    assert.equal(saveData.saved, true);

    const { shippingChoices, shippingFees } = await getShippingChoicesForCart(
      session,
      addrA.address,
      activeCart.shop_groups,
      true
    );

    const validateData = expectStatus(
      await session.post("/api/vouchers/validate", {
        code: voucher.code,
        shipping_fees: shippingFees,
      }),
      200,
      `POST /api/vouchers/validate (${voucher.code})`
    );
    assert.equal(validateData.valid, true, `voucher ${voucher.code} must validate cleanly`);
    assert.ok(validateData.discount_vnd > 0, `voucher ${voucher.code} must produce discount > 0`);

    const previewData = expectStatus(
      await session.post("/api/vouchers/preview", {
        shipping_fees: shippingFees,
        platform_voucher_id: voucher.id,
      }),
      200,
      "POST /api/vouchers/preview"
    );
    assert.ok(
      previewData.breakdown.total_platform_voucher_discount_vnd > 0,
      "preview must report positive platform voucher discount"
    );
    assert.ok(
      previewData.breakdown.final_payable_vnd <
        previewData.breakdown.total_subtotal_vnd + previewData.breakdown.total_shipping_fee_vnd,
      "final payable must be strictly reduced by the applied voucher"
    );

    // Checkout with COD (multi-shop).
    const codCheckout = expectStatus(
      await session.post(
        "/api/checkout",
        {
          address_id: addrA.address.id,
          from_cart: true,
          platform_voucher_id: voucher.id,
          payment_method: "COD",
          shipping_choices: shippingChoices,
        },
        { headers: { "Idempotency-Key": newIdempotencyKey("cod-multi") } }
      ),
      201,
      "POST /api/checkout (COD multi-shop)"
    );
    assert.ok(codCheckout.checkout_group_id);
    assert.equal(codCheckout.payment_method, "COD");
    assert.equal(codCheckout.idempotent_replay, false);
    assert.equal(codCheckout.orders.length, 2, "multi-shop COD checkout must create 2 sub-orders");
    assert.equal(
      new Set(codCheckout.orders.map((o) => o.shop_id)).size,
      2,
      "COD sub-orders must belong to 2 different shops"
    );
    codOrderCount = codCheckout.orders.length;

    const cartAfter = expectStatus(await session.get("/api/cart"), 200, "GET /api/cart after COD");
    assert.equal(cartAfter.cart.total_items, 0, "ordered cart items must be cleared after checkout");

    // Verify created orders appear in GET /api/orders and GET /api/orders/[id].
    const listData = expectStatus(
      await session.get("/api/orders?page=1&page_size=20"),
      200,
      "GET /api/orders after COD"
    );
    const listedIds = new Set((listData.items ?? []).map((o) => o.id));
    for (const sub of codCheckout.orders) {
      assert.ok(listedIds.has(sub.id), `GET /api/orders must include created COD order ${sub.id}`);
    }

    let totalPlatformDiscount = 0;
    for (const sub of codCheckout.orders) {
      const detail = expectStatus(
        await session.get(`/api/orders/${encodeURIComponent(sub.id)}`),
        200,
        `GET /api/orders/${sub.id}`
      );
      assert.equal(detail.order.id, sub.id);
      assert.equal(detail.order.checkoutGroupId, codCheckout.checkout_group_id);
      assert.equal(detail.order.paymentMethod, "cod");
      assert.ok(detail.order.items.length >= 1, "order detail must contain line items");
      assert.ok(detail.order.total > 0, "order detail total must be positive");
      totalPlatformDiscount += Number(detail.order.platformVoucherDiscount || 0);
    }
    assert.ok(
      totalPlatformDiscount > 0,
      `voucher ${voucher.code} discount must be persisted on COD orders (got ${totalPlatformDiscount})`
    );
  });

  it("(e) completes VietQR mock payment flow: checkout -> pending payment -> external HMAC bank webhook -> poll PAID & CONFIRMED", async () => {
    const { session } = buyerA;

    expectStatus(
      await session.post("/api/cart/items", { sku_id: sku1.skuId, quantity: 1 }),
      200,
      "POST /api/cart/items (vietqr shop 1)"
    );
    const vqCart = expectStatus(
      await session.post("/api/cart/items", { sku_id: sku2.skuId, quantity: 1 }),
      200,
      "POST /api/cart/items (vietqr shop 2)"
    );
    assert.equal(vqCart.cart.shop_groups.length, 2);

    const { shippingChoices } = await getShippingChoicesForCart(
      session,
      addrA.address,
      vqCart.cart.shop_groups,
      false
    );

    const vqCheckout = expectStatus(
      await session.post(
        "/api/checkout",
        {
          address_id: addrA.address.id,
          from_cart: true,
          payment_method: "VIETQR",
          shipping_choices: shippingChoices,
        },
        { headers: { "Idempotency-Key": newIdempotencyKey("vietqr-multi") } }
      ),
      201,
      "POST /api/checkout (VIETQR)"
    );
    assert.ok(vqCheckout.payment_id, "VietQR checkout must return payment_id");
    assert.ok(vqCheckout.checkout_group_id, "VietQR checkout must return checkout_group_id");
    assert.equal(vqCheckout.payment_method, "VIETQR");
    assert.ok(vqCheckout.qr_url || vqCheckout.qr_code, "VietQR checkout must return qr_url or qr_code");
    assert.ok(vqCheckout.total_payable_vnd > 0);
    assert.equal(vqCheckout.orders.length, 2);
    for (const sub of vqCheckout.orders) {
      assert.equal(sub.status.toUpperCase(), "PENDING", "VietQR sub-orders must start PENDING");
    }
    vietqrOrderCount = vqCheckout.orders.length;
    buyerAVietqrOrder = vqCheckout.orders[0];
    buyerAVietqrPaymentId = vqCheckout.payment_id;

    // Inspect pending payment via BFF before settlement.
    const payBefore = expectStatus(
      await session.get(`/api/payments/${encodeURIComponent(vqCheckout.payment_id)}`),
      200,
      "GET /api/payments/[id] before webhook"
    );
    assert.equal(payBefore.payment.payment_id, vqCheckout.payment_id);
    assert.equal(payBefore.payment.status.toUpperCase(), "PENDING");
    assert.equal(payBefore.payment.amount_vnd, vqCheckout.total_payable_vnd);

    // External bank settles VietQR via HMAC-signed webhook.
    await payVietQR({
      paymentId: vqCheckout.payment_id,
      groupId: vqCheckout.checkout_group_id,
      amountVND: vqCheckout.total_payable_vnd,
    });

    // Poll BFF GET /api/payments/[id] until SUCCEEDED.
    const paidPayment = await pollUntil(`payment ${vqCheckout.payment_id} SUCCEEDED`, async () => {
      const res = await session.get(`/api/payments/${encodeURIComponent(vqCheckout.payment_id)}`);
      const data = expectStatus(res, 200, "poll GET /api/payments/[id]");
      const status = String(data.payment?.status || "").toUpperCase();
      return [status === "SUCCEEDED", `status=${status}`, data.payment];
    });
    assert.equal(paidPayment.status.toUpperCase(), "SUCCEEDED");

    // Poll BFF GET /api/orders/[id] until every sub-order reaches CONFIRMED + paid state.
    for (const sub of vqCheckout.orders) {
      const paidOrder = await pollUntil(`order ${sub.id} CONFIRMED & paid`, async () => {
        const res = await session.get(`/api/orders/${encodeURIComponent(sub.id)}`);
        const data = expectStatus(res, 200, `poll GET /api/orders/${sub.id}`);
        const order = data.order;
        const done =
          order?.backendStatus === "CONFIRMED" &&
          order?.paymentStatus === "da_thanh_toan" &&
          order?.isPaid === true;
        return [
          done,
          `backendStatus=${order?.backendStatus}, paymentStatus=${order?.paymentStatus}, isPaid=${order?.isPaid}`,
          order,
        ];
      });
      assert.equal(paidOrder.paymentMethod, "vietqr");
      assert.equal(paidOrder.isPaid, true);
      assert.equal(paidOrder.backendStatus, "CONFIRMED");
    }
  });

  it("(f) checkout idempotency: resending same Idempotency-Key returns same single order without duplicates", async () => {
    const { session } = buyerA;
    const expectedPrior = codOrderCount + vietqrOrderCount;

    const idemCart = expectStatus(
      await session.post("/api/cart/items", { sku_id: sku1.skuId, quantity: 1 }),
      200,
      "POST /api/cart/items (idem)"
    );
    const { shippingChoices } = await getShippingChoicesForCart(
      session,
      addrA.address,
      idemCart.cart.shop_groups,
      true
    );

    const idemKey = newIdempotencyKey("idem-cart");
    const idemPayload = {
      address_id: addrA.address.id,
      from_cart: true,
      payment_method: "COD",
      shipping_choices: shippingChoices,
    };

    const first = expectStatus(
      await session.post("/api/checkout", idemPayload, {
        headers: { "Idempotency-Key": idemKey },
      }),
      201,
      "POST /api/checkout (idempotency first)"
    );
    assert.equal(first.idempotent_replay, false);
    assert.equal(first.orders.length, 1);

    const replay = expectStatus(
      await session.post("/api/checkout", idemPayload, {
        headers: { "Idempotency-Key": idemKey },
      }),
      200,
      "POST /api/checkout (idempotency replay)"
    );
    assert.equal(replay.idempotent_replay, true, "replay must return idempotent_replay=true");
    assert.equal(replay.checkout_group_id, first.checkout_group_id);
    assert.equal(replay.orders.length, 1);
    assert.equal(replay.orders[0].id, first.orders[0].id, "replay must return exact same order ID");

    const finalOrders = expectStatus(
      await session.get("/api/orders?page=1&page_size=20"),
      200,
      "GET /api/orders after idempotency replay"
    );
    assert.equal(
      finalOrders.total,
      expectedPrior + 1,
      `expected strictly ${expectedPrior + 1} total orders (no duplicate), got ${finalOrders.total}`
    );
    const matchingGroup = (finalOrders.items ?? []).filter(
      (o) => o.checkoutGroupId === first.checkout_group_id
    );
    assert.equal(matchingGroup.length, 1, "only ONE order may exist for the replayed checkout_group_id");
  });

  it("(g) object-level authorization: Buyer A and Buyer B cannot read or mutate each other's orders or payments", async () => {
    const buyerB = await registerVerifyLoginBuyer("buyer-b");
    const addrB = await createShippingAddress(buyerB.session, "buyer-b");
    const cartB = expectStatus(
      await buyerB.session.post("/api/cart/items", { sku_id: sku2.skuId, quantity: 1 }),
      200,
      "Buyer B add cart"
    );
    const shipB = await getShippingChoicesForCart(buyerB.session, addrB.address, cartB.cart.shop_groups, true);
    const checkoutB = expectStatus(
      await buyerB.session.post(
        "/api/checkout",
        {
          address_id: addrB.address.id,
          from_cart: true,
          payment_method: "COD",
          shipping_choices: shipB.shippingChoices,
        },
        { headers: { "Idempotency-Key": newIdempotencyKey("buyer-b") } }
      ),
      201,
      "Buyer B COD checkout"
    );
    const orderB = checkoutB.orders[0];
    assert.ok(orderB?.id);

    // Positive control: Buyer B can read their own order.
    const ownB = expectStatus(
      await buyerB.session.get(`/api/orders/${encodeURIComponent(orderB.id)}`),
      200,
      "Buyer B reading own orderB"
    );
    assert.equal(ownB.order.id, orderB.id);

    // Buyer A reading Buyer B's order MUST return 403 or 404.
    const aReadB = await buyerA.session.get(`/api/orders/${encodeURIComponent(orderB.id)}`);
    expectStatus(aReadB, [403, 404], "Buyer A reading Buyer B order");
    assert.equal(aReadB.text.includes(orderB.order_code), false, "error must not leak Buyer B order code");
    assertNoInternalErrorLeak(aReadB.text, "Buyer A -> Buyer B order");

    // Buyer B reading Buyer A's order MUST return 403 or 404.
    const bReadA = await buyerB.session.get(`/api/orders/${encodeURIComponent(buyerAVietqrOrder.id)}`);
    expectStatus(bReadA, [403, 404], "Buyer B reading Buyer A order");
    assert.equal(bReadA.text.includes(buyerAVietqrOrder.order_code), false, "error must not leak Buyer A order code");
    assertNoInternalErrorLeak(bReadA.text, "Buyer B -> Buyer A order");

    // Buyer B cancelling Buyer A's order MUST return 403 or 404.
    const bCancelA = await buyerB.session.post(
      `/api/orders/${encodeURIComponent(buyerAVietqrOrder.id)}/cancel`,
      { reason: "IDOR attempt" }
    );
    expectStatus(bCancelA, [403, 404], "Buyer B cancelling Buyer A order");
    assertNoInternalErrorLeak(bCancelA.text, "Buyer B cancel -> Buyer A order");

    // Buyer B reading Buyer A's payment MUST return 403 or 404.
    const bReadPayA = await buyerB.session.get(
      `/api/payments/${encodeURIComponent(buyerAVietqrPaymentId)}`
    );
    expectStatus(bReadPayA, [403, 404], "Buyer B reading Buyer A payment");
    assertNoInternalErrorLeak(bReadPayA.text, "Buyer B -> Buyer A payment");
  });

  it("(h) unauthenticated writes and private reads return 401 without leaking internal details", async () => {
    // Anon sends valid same-origin Origin on writes so CSRF passes and auth returns 401.
    const anon = new WebSession("anon");
    const unauthChecks = [
      ["POST /api/cart/items", () => anon.post("/api/cart/items", { sku_id: sku1.skuId, quantity: 1 })],
      ["DELETE /api/cart", () => anon.del("/api/cart")],
      ["PATCH /api/cart/items/[skuId]", () => anon.patch(`/api/cart/items/${encodeURIComponent(sku1.skuId)}`, { quantity: 1 })],
      ["POST /api/checkout", () => anon.post("/api/checkout", { address_id: addrA.address.id, payment_method: "COD" })],
      ["POST /api/checkout/addresses", () => anon.post("/api/checkout/addresses", { recipient_name: "Anon" })],
      ["POST /api/vouchers/preview", () => anon.post("/api/vouchers/preview", {})],
      ["POST /api/vouchers/NEXUS30/save", () => anon.post("/api/vouchers/NEXUS30/save", {})],
      ["POST /api/orders/[id]/cancel", () => anon.post(`/api/orders/${encodeURIComponent(buyerAVietqrOrder.id)}/cancel`, {})],
      ["PATCH /api/auth/me", () => anon.patch("/api/auth/me", { fullName: "Unauth Edit" })],
      ["GET /api/cart", () => anon.get("/api/cart")],
      ["GET /api/orders", () => anon.get("/api/orders")],
      ["GET /api/orders/[id]", () => anon.get(`/api/orders/${encodeURIComponent(buyerAVietqrOrder.id)}`)],
      ["GET /api/payments/[id]", () => anon.get(`/api/payments/${encodeURIComponent(buyerAVietqrPaymentId)}`)],
    ];
    for (const [label, fn] of unauthChecks) {
      const res = await fn();
      expectStatus(res, 401, `Unauthenticated ${label}`);
      assertNoInternalErrorLeak(res.text, `Unauthenticated ${label}`);
    }
  });

  it("(i) CSRF protection rejects foreign/missing Origin and cross-site Fetch-Metadata with 403", async () => {
    const evilOrigin = "https://evil.example";
    const csrfChecks = [
      [
        "foreign Origin on POST /api/cart/items",
        () => buyerA.session.post("/api/cart/items", { sku_id: sku1.skuId, quantity: 1 }, { origin: evilOrigin }),
      ],
      [
        "foreign Origin on POST /api/checkout",
        () =>
          buyerA.session.post(
            "/api/checkout",
            { address_id: addrA.address.id, from_cart: false, items: [{ sku_id: sku1.skuId, quantity: 1 }], payment_method: "COD" },
            { origin: evilOrigin, headers: { "Idempotency-Key": newIdempotencyKey("csrf") } }
          ),
      ],
      [
        "foreign Origin on POST /api/orders/[id]/cancel",
        () =>
          buyerA.session.post(
            `/api/orders/${encodeURIComponent(buyerAVietqrOrder.id)}/cancel`,
            { reason: "CSRF" },
            { origin: evilOrigin }
          ),
      ],
      [
        "foreign Origin on PATCH /api/auth/me",
        () => buyerA.session.patch("/api/auth/me", { fullName: "Hijacked" }, { origin: evilOrigin }),
      ],
      [
        "foreign Origin on POST /api/auth/logout",
        () => buyerA.session.post("/api/auth/logout", {}, { origin: evilOrigin }),
      ],
      [
        "missing Origin & Referer on POST /api/cart/items",
        () => buyerA.session.post("/api/cart/items", { sku_id: sku1.skuId, quantity: 1 }, { origin: null }),
      ],
      [
        "Origin: null string on POST /api/cart/items",
        () => buyerA.session.post("/api/cart/items", { sku_id: sku1.skuId, quantity: 1 }, { origin: "null" }),
      ],
      [
        "Sec-Fetch-Site: cross-site on POST /api/cart/items",
        () =>
          buyerA.session.post(
            "/api/cart/items",
            { sku_id: sku1.skuId, quantity: 1 },
            { origin: config.webOrigin, headers: { "sec-fetch-site": "cross-site" } }
          ),
      ],
    ];
    for (const [label, fn] of csrfChecks) {
      const res = await fn();
      expectStatus(res, 403, `CSRF ${label}`);
      assert.equal(res.data?.error?.code, "BAD_ORIGIN", `CSRF ${label}: expected BAD_ORIGIN`);
      assertNoInternalErrorLeak(res.text, `CSRF ${label}`);
    }

    // Confirm CSRF-blocked logout did NOT destroy Buyer A's session.
    const stillAuthed = expectStatus(
      await buyerA.session.get("/api/auth/me"),
      200,
      "Buyer A /api/auth/me after blocked CSRF logout"
    );
    assert.equal(stillAuthed.user?.id, buyerA.user.id);
  });

  it("(j) enforces server-side RBAC for Buyer, Seller, and Admin pages & BFF APIs", async () => {
    // Buyer can view buyer pages, blocked from /admin, /seller, and privileged APIs.
    expectStatus(await buyerA.session.get("/don-hang", { headers: HTML_HEADERS }), 200, "Buyer GET /don-hang");
    expectStatus(await buyerA.session.get("/tai-khoan", { headers: HTML_HEADERS }), 200, "Buyer GET /tai-khoan");
    expectStatus(await buyerA.session.get("/admin", { headers: HTML_HEADERS }), [403, 404], "Buyer GET /admin");
    expectStatus(await buyerA.session.get("/seller", { headers: HTML_HEADERS }), [403, 404], "Buyer GET /seller");
    expectStatus(await buyerA.session.get("/api/admin/orders"), 403, "Buyer GET /api/admin/orders");
    expectStatus(await buyerA.session.get("/api/seller/orders"), 403, "Buyer GET /api/seller/orders");

    // Seeded demo seller: allowed in /seller & /api/seller/shop, blocked from /admin & /api/admin/orders.
    const seller = await loginAccount(
      demoCredentials.seller.email,
      demoCredentials.seller.password,
      "demo-seller"
    );
    try {
      assert.equal(seller.user.role, "seller");
      expectStatus(await seller.session.get("/seller", { headers: HTML_HEADERS }), 200, "Seller GET /seller");
      const shopApi = expectStatus(await seller.session.get("/api/seller/shop"), 200, "Seller GET /api/seller/shop");
      assert.ok(shopApi.shop?.id, "seller shop API must return owned shop");
      expectStatus(await seller.session.get("/admin", { headers: HTML_HEADERS }), [403, 404], "Seller GET /admin");
      expectStatus(await seller.session.get("/api/admin/orders"), 403, "Seller GET /api/admin/orders");
    } finally {
      await seller.session.post("/api/auth/logout", {});
    }

    // Seeded demo admin: allowed in /admin & /api/admin/ledger (balanced), blocked from /seller & /api/seller/orders.
    const admin = await loginAccount(
      demoCredentials.admin.email,
      demoCredentials.admin.password,
      "demo-admin"
    );
    try {
      assert.ok(
        admin.user.role === "admin" || admin.user.role === "super_admin",
        `expected admin/super_admin role, got ${admin.user.role}`
      );
      expectStatus(await admin.session.get("/admin", { headers: HTML_HEADERS }), 200, "Admin GET /admin");
      const ledger = expectStatus(await admin.session.get("/api/admin/ledger"), 200, "Admin GET /api/admin/ledger");
      assert.equal(ledger.is_balanced, true, "admin double-entry ledger must be balanced");
      expectStatus(await admin.session.get("/seller", { headers: HTML_HEADERS }), [403, 404], "Admin GET /seller");
      expectStatus(await admin.session.get("/api/seller/orders"), 403, "Admin GET /api/seller/orders");
    } finally {
      await admin.session.post("/api/auth/logout", {});
    }
  });
});
