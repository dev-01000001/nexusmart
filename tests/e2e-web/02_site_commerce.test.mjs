import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { payVietQR } from "./lib/bank.mjs";
import { siteHost } from "./lib/config.mjs";
import { Browser, expectStatus, pollUntil } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  assertAuthCookieHeaders,
  assertNoTokenLeak,
  checkout,
  createShippingAddress,
  fillCart,
  listOrderIds,
  loginOn,
  newIdempotencyKey,
  pickShopVoucher,
  pickSku,
  quoteShipping,
  registerBuyer,
  resolveSite,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);
const SITE_B_HOST = siteHost(SEEDED.saiGon.sub);
const bare = (host) => host.replace(/:\d+$/, "");

function findRefreshCookie(browser, host) {
  return browser.cookiesFor(host).find((c) => c.name === "nm_rt" || c.name === "__Host-nm_rt")?.value ?? "";
}

describe("02. Site A full buyer commerce & auth lifecycle (OTP, COD + SHOP voucher, VietQR, idempotency, refresh rotation)", () => {
  let siteA;
  let skuA;
  let voucherA;
  let browser;
  let clientA;
  let buyer;
  let addressA;
  let ordersCreatedOnA = 0;

  before(async () => {
    await resetRateLimitKeys();
    siteA = await resolveSite(SEEDED.bachDang.sub);
    skuA = await pickSku(siteA.shopId, { minStock: 10 });
    voucherA = await pickShopVoucher(siteA.shopId, SEEDED.bachDang.voucherCode);
    browser = new Browser("site-a-buyer");
    clientA = browser.at(SITE_A_HOST);
  });

  after(async () => {
    await resetRateLimitKeys();
  });

  it("public storefront pages serve 200 on site A; unauthenticated protected pages redirect on the SAME site host", async () => {
    const anonA = new Browser("site-a-anon").at(SITE_A_HOST);
    for (const path of [
      "/",
      "/san-pham",
      `/san-pham/${SEEDED.bachDang.productSlug}`,
      "/chinh-sach",
      "/gioi-thieu",
      "/tim-kiem",
      "/flash-sale",
      "/voucher",
      "/dang-nhap",
      "/dang-ky",
    ]) {
      const res = await anonA.page(path);
      expectStatus(res, 200, `anon GET ${SITE_A_HOST}${path}`);
    }

    for (const path of ["/tai-khoan", "/don-hang"]) {
      const res = await anonA.page(path);
      expectStatus(res, [302, 303, 307, 308], `anon GET ${SITE_A_HOST}${path}`);
      const loc = new URL(res.location, `http://${SITE_A_HOST}`);
      assert.equal(loc.pathname, "/dang-nhap", `unauth ${path} must redirect to /dang-nhap, got ${res.location}`);
      assert.ok(loc.searchParams.get("next"), `unauth ${path} redirect must carry next=`);
      assert.equal(
        bare(loc.host),
        bare(SITE_A_HOST),
        `unauth ${path} on site A must stay on ${SITE_A_HOST}, got ${res.location}`
      );
    }
  });

  it("registers buyer + Mailpit OTP -> logs in on site A with HttpOnly host-only cookies and no token leak", async () => {
    buyer = await registerBuyer(clientA, "site-a");

    const meRes = await clientA.get("/api/auth/me");
    const me = expectStatus(meRes, 200, `GET ${SITE_A_HOST}/api/auth/me`);
    assert.equal(me.user?.id, buyer.user.id);
    assert.equal(me.user?.email, buyer.creds.email);
    assertNoTokenLeak(meRes.text, "GET /api/auth/me on site A", [
      buyer.authCookies.access.value,
      buyer.authCookies.refresh.value,
    ]);

    // Authenticated buyer pages render 200 inside site A's own shell.
    for (const path of ["/tai-khoan", "/don-hang", "/gio-hang", "/thanh-toan"]) {
      const res = await clientA.page(path);
      expectStatus(res, 200, `buyer GET ${SITE_A_HOST}${path}`);
      assert.ok(res.text.includes(SEEDED.bachDang.shopName), `${SITE_A_HOST}${path} must render Shop A brand`);
    }

    addressA = await createShippingAddress(clientA, "site-a");
  });

  it("adds site A SKU to cart, applies site A SHOP voucher, places COD order -> STOREFRONT for shop A", async () => {
    // Start with qty=1 then PATCH quantity if needed to meet voucherA.min_order_vnd.
    let cart = await fillCart(clientA, skuA.skuId, 1);
    assert.equal(cart.shop_groups.length, 1, "site A cart must only contain shop A group");
    assert.equal(cart.shop_groups[0].shop_id, siteA.shopId);

    const neededQty = Math.min(5, Math.max(1, Math.ceil((voucherA.min_order_vnd || 0) / skuA.price)));
    if (neededQty > 1) {
      const patched = expectStatus(
        await clientA.patch(`/api/cart/items/${encodeURIComponent(skuA.skuId)}`, { quantity: neededQty }),
        200,
        `PATCH ${SITE_A_HOST}/api/cart/items/${skuA.skuId}`
      );
      cart = patched.cart;
    }
    assert.equal(cart.total_items, neededQty);
    assert.ok(
      cart.total_vnd >= (voucherA.min_order_vnd || 0),
      `cart subtotal ${cart.total_vnd} must meet ${voucherA.code} min_order_vnd ${voucherA.min_order_vnd}`
    );

    // GET /api/vouchers on site A lists ONLY SHOP vouchers of site A (zero platform/freeship/other shop).
    const vList = expectStatus(await clientA.get("/api/vouchers"), 200, `GET ${SITE_A_HOST}/api/vouchers`);
    const vouchers = vList.vouchers ?? [];
    assert.ok(vouchers.length > 0, "site A /api/vouchers must return Shop A SHOP vouchers");
    for (const v of vouchers) {
      assert.equal(String(v.scope).toUpperCase(), "SHOP", `site A /api/vouchers leaked non-SHOP voucher ${v.code} (${v.scope})`);
      assert.equal(v.shop_id, siteA.shopId, `site A /api/vouchers leaked foreign shop voucher ${v.code}`);
    }
    assert.ok(
      vouchers.some((v) => v.id === voucherA.id),
      `site A /api/vouchers must include ${voucherA.code}`
    );

    // Save + validate + preview Shop A's SHOP voucher.
    const saveData = expectStatus(
      await clientA.post(`/api/vouchers/${encodeURIComponent(voucherA.id)}/save`, {}),
      200,
      `POST ${SITE_A_HOST}/api/vouchers/${voucherA.code}/save`
    );
    assert.equal(saveData.saved, true);

    const { shippingChoices, shippingFees } = await quoteShipping(clientA, addressA, cart.shop_groups, true);

    const validateData = expectStatus(
      await clientA.post("/api/vouchers/validate", {
        code: voucherA.code,
        shipping_fees: shippingFees,
      }),
      200,
      `POST ${SITE_A_HOST}/api/vouchers/validate (${voucherA.code})`
    );
    assert.equal(validateData.valid, true, `Shop A voucher ${voucherA.code} must validate cleanly`);
    assert.ok(validateData.discount_vnd > 0, `Shop A voucher ${voucherA.code} must produce discount > 0`);

    const previewData = expectStatus(
      await clientA.post("/api/vouchers/preview", {
        shipping_fees: shippingFees,
        shop_voucher_ids: { [siteA.shopId]: voucherA.id },
      }),
      200,
      `POST ${SITE_A_HOST}/api/vouchers/preview`
    );
    assert.ok(
      previewData.breakdown.total_shop_voucher_discount_vnd > 0,
      "preview must report positive SHOP voucher discount"
    );
    assert.equal(previewData.breakdown.total_platform_voucher_discount_vnd ?? 0, 0);
    assert.equal(previewData.breakdown.total_freeship_discount_vnd ?? 0, 0);
    assert.equal(previewData.breakdown.total_coins_discount_vnd ?? 0, 0);
    assert.ok(
      previewData.breakdown.final_payable_vnd <
        previewData.breakdown.total_subtotal_vnd + previewData.breakdown.total_shipping_fee_vnd,
      "final payable must be strictly reduced by the SHOP voucher"
    );

    // Place COD order on site A with Shop A's SHOP voucher.
    const cod = expectStatus(
      await checkout(clientA, {
        addressId: addressA.id,
        shippingChoices,
        paymentMethod: "COD",
        extra: { shop_voucher_ids: { [siteA.shopId]: voucherA.id } },
        key: newIdempotencyKey("site-a-cod"),
      }),
      201,
      `POST ${SITE_A_HOST}/api/checkout (COD)`
    );
    assert.ok(cod.checkout_group_id);
    assert.equal(cod.payment_method, "COD");
    assert.equal(cod.idempotent_replay, false);
    assert.equal(cod.sales_channel, "STOREFRONT", "site A COD checkout must set sales_channel=STOREFRONT");
    assert.equal(cod.storefront_shop_id, siteA.shopId, "site A COD checkout must set storefront_shop_id=shopA");
    assert.equal(cod.orders.length, 1, "site A checkout must create exactly 1 order");

    const created = cod.orders[0];
    assert.equal(created.shop_id, siteA.shopId);
    assert.equal(created.sales_channel, "STOREFRONT");
    assert.equal(created.storefront_shop_id, siteA.shopId);
    ordersCreatedOnA += 1;

    // Cart on site A must be empty after checkout.
    const cartAfter = expectStatus(await clientA.get("/api/cart"), 200, `GET ${SITE_A_HOST}/api/cart after COD`);
    assert.equal(cartAfter.cart.total_items, 0, "site A cart must be cleared after COD checkout");

    // Verify order in GET /api/orders and GET /api/orders/{id}.
    const listed = await listOrderIds(clientA);
    assert.ok(listed.ids.includes(created.id), `GET ${SITE_A_HOST}/api/orders must include COD order ${created.id}`);

    const detail = expectStatus(
      await clientA.get(`/api/orders/${encodeURIComponent(created.id)}`),
      200,
      `GET ${SITE_A_HOST}/api/orders/${created.id}`
    );
    assert.equal(detail.order.id, created.id);
    assert.equal(detail.order.checkoutGroupId, cod.checkout_group_id);
    assert.equal(detail.order.paymentMethod, "cod");
    assert.equal(detail.order.sales_channel ?? detail.order.salesChannel, "STOREFRONT");
    assert.equal(detail.order.storefront_shop_id ?? detail.order.storefrontShopId, siteA.shopId);
    assert.ok(
      Number(detail.order.shopVoucherDiscount || 0) > 0,
      `SHOP voucher ${voucherA.code} discount must be persisted on order (got ${detail.order.shopVoucherDiscount})`
    );
  });

  it("completes VietQR mock + HMAC webhook flow on site A -> payment SUCCEEDED & order PAID / CONFIRMED", async () => {
    const cart = await fillCart(clientA, skuA.skuId, 1);
    const { shippingChoices } = await quoteShipping(clientA, addressA, cart.shop_groups, false);

    const vq = expectStatus(
      await checkout(clientA, {
        addressId: addressA.id,
        shippingChoices,
        paymentMethod: "VIETQR",
        key: newIdempotencyKey("site-a-vietqr"),
      }),
      201,
      `POST ${SITE_A_HOST}/api/checkout (VIETQR)`
    );
    assert.ok(vq.payment_id, "VietQR checkout must return payment_id");
    assert.ok(vq.checkout_group_id, "VietQR checkout must return checkout_group_id");
    assert.equal(vq.payment_method, "VIETQR");
    assert.equal(vq.sales_channel, "STOREFRONT");
    assert.equal(vq.storefront_shop_id, siteA.shopId);
    assert.ok(vq.qr_url || vq.qr_code, "VietQR checkout must return qr_url or qr_code");
    assert.ok(vq.total_payable_vnd > 0);
    assert.equal(vq.orders.length, 1);
    assert.equal(vq.orders[0].status.toUpperCase(), "PENDING");
    ordersCreatedOnA += 1;

    const orderId = vq.orders[0].id;

    // Pending payment readable through site A BFF.
    const payBefore = expectStatus(
      await clientA.get(`/api/payments/${encodeURIComponent(vq.payment_id)}`),
      200,
      `GET ${SITE_A_HOST}/api/payments/${vq.payment_id} before webhook`
    );
    assert.equal(payBefore.payment.payment_id, vq.payment_id);
    assert.equal(payBefore.payment.status.toUpperCase(), "PENDING");
    assert.equal(payBefore.payment.amount_vnd, vq.total_payable_vnd);

    // Waiting page renders 200 on site A.
    expectStatus(
      await clientA.page(`/thanh-toan/cho/${encodeURIComponent(vq.payment_id)}`),
      200,
      `GET ${SITE_A_HOST}/thanh-toan/cho/${vq.payment_id}`
    );

    // External bank settles VietQR via HMAC-signed webhook.
    await payVietQR({
      paymentId: vq.payment_id,
      groupId: vq.checkout_group_id,
      amountVND: vq.total_payable_vnd,
    });

    const paidPayment = await pollUntil(`payment ${vq.payment_id} SUCCEEDED`, async () => {
      const res = await clientA.get(`/api/payments/${encodeURIComponent(vq.payment_id)}`);
      const data = expectStatus(res, 200, `poll GET ${SITE_A_HOST}/api/payments/${vq.payment_id}`);
      const status = String(data.payment?.status || "").toUpperCase();
      return [status === "SUCCEEDED", `status=${status}`, data.payment];
    });
    assert.equal(paidPayment.status.toUpperCase(), "SUCCEEDED");

    const paidOrder = await pollUntil(`order ${orderId} CONFIRMED & PAID`, async () => {
      const res = await clientA.get(`/api/orders/${encodeURIComponent(orderId)}`);
      const data = expectStatus(res, 200, `poll GET ${SITE_A_HOST}/api/orders/${orderId}`);
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
    assert.equal(paidOrder.sales_channel ?? paidOrder.salesChannel, "STOREFRONT");
    assert.equal(paidOrder.storefront_shop_id ?? paidOrder.storefrontShopId, siteA.shopId);
  });

  it("checkout idempotency: resending same Idempotency-Key returns same single order without duplicates", async () => {
    const cart = await fillCart(clientA, skuA.skuId, 1);
    const { shippingChoices } = await quoteShipping(clientA, addressA, cart.shop_groups, true);
    const key = newIdempotencyKey("site-a-idem");

    const first = expectStatus(
      await checkout(clientA, { addressId: addressA.id, shippingChoices, paymentMethod: "COD", key }),
      201,
      `POST ${SITE_A_HOST}/api/checkout (idempotency first)`
    );
    assert.equal(first.idempotent_replay, false);
    assert.equal(first.orders.length, 1);
    ordersCreatedOnA += 1;

    const replay = expectStatus(
      await checkout(clientA, { addressId: addressA.id, shippingChoices, paymentMethod: "COD", key }),
      200,
      `POST ${SITE_A_HOST}/api/checkout (idempotency replay)`
    );
    assert.equal(replay.idempotent_replay, true, "replay must return idempotent_replay=true");
    assert.equal(replay.checkout_group_id, first.checkout_group_id);
    assert.equal(replay.orders.length, 1);
    assert.equal(replay.orders[0].id, first.orders[0].id, "replay must return exact same order id");

    const listed = await listOrderIds(clientA);
    assert.equal(
      listed.total,
      ordersCreatedOnA,
      `expected strictly ${ordersCreatedOnA} orders on site A (no duplicate), got ${listed.total}`
    );
    const matchingGroup = listed.items.filter((o) => o.checkoutGroupId === first.checkout_group_id);
    assert.equal(matchingGroup.length, 1, "only ONE order may exist for the replayed checkout_group_id");
  });

  it("refresh rotation is independent per host and logout revokes only that host's session", async () => {
    // Log the SAME buyer into site B inside the SAME browser jar.
    const clientB = browser.at(SITE_B_HOST);
    await loginOn(clientB, buyer.creds.email, buyer.creds.password, "site-b-same-browser");

    const rtABefore = findRefreshCookie(browser, SITE_A_HOST);
    const rtBBefore = findRefreshCookie(browser, SITE_B_HOST);
    assert.ok(rtABefore && rtBBefore, "both site A and site B must hold independent refresh cookies");
    assert.notEqual(rtABefore, rtBBefore, "each host login must issue its own refresh token");

    // Rotate refresh token on site A.
    const refreshRes = await clientA.post("/api/auth/refresh", {});
    const refreshData = expectStatus(refreshRes, 200, `POST ${SITE_A_HOST}/api/auth/refresh`);
    assert.equal(refreshData.ok, true);
    const rotated = assertAuthCookieHeaders(refreshRes, `POST ${SITE_A_HOST}/api/auth/refresh`);
    assertNoTokenLeak(refreshRes.text, `POST ${SITE_A_HOST}/api/auth/refresh`, [
      rotated.access.value,
      rotated.refresh.value,
    ]);
    assert.notEqual(rotated.refresh.value, rtABefore, "site A refresh token must rotate");
    assert.equal(findRefreshCookie(browser, SITE_B_HOST), rtBBefore, "rotating site A must NOT overwrite site B cookie");

    // Both hosts stay authenticated after site A's rotation.
    const meA = expectStatus(await clientA.get("/api/auth/me"), 200, `GET ${SITE_A_HOST}/api/auth/me after refresh`);
    assert.equal(meA.user?.id, buyer.user.id);
    const meB = expectStatus(await clientB.get("/api/auth/me"), 200, `GET ${SITE_B_HOST}/api/auth/me after site A refresh`);
    assert.equal(meB.user?.id, buyer.user.id);

    // Snapshot site A's pre-logout cookies to confirm backend revocation after logout.
    const staleSiteACookieHeader = browser.cookieHeaderFor(SITE_A_HOST);

    // Logout on site A clears site A cookies and revokes its token family, leaving site B intact.
    const logoutA = expectStatus(await clientA.post("/api/auth/logout", {}), 200, `POST ${SITE_A_HOST}/api/auth/logout`);
    assert.equal(logoutA.ok, true);
    assert.equal(browser.cookiesFor(SITE_A_HOST).length, 0, "logout on site A must clear site A cookies");

    const meAAfterLogout = await clientA.get("/api/auth/me");
    expectStatus(meAAfterLogout, [200, 401], `GET ${SITE_A_HOST}/api/auth/me after logout`);
    if (meAAfterLogout.status === 200) {
      assert.equal(meAAfterLogout.data?.user ?? null, null, "site A /api/auth/me must return user=null after logout");
    }
    expectStatus(await clientA.get("/api/orders"), 401, `GET ${SITE_A_HOST}/api/orders after logout`);

    // Replaying site A's revoked refresh cookie must fail with 401.
    const replayRefresh = await new Browser("revoked-replay")
      .at(SITE_A_HOST)
      .post("/api/auth/refresh", {}, { cookies: false, headers: { cookie: staleSiteACookieHeader } });
    expectStatus(replayRefresh, 401, `replay revoked refresh token on ${SITE_A_HOST}`);

    // Site B session in the same browser is completely unaffected!
    const meBAfterLogoutA = expectStatus(
      await clientB.get("/api/auth/me"),
      200,
      `GET ${SITE_B_HOST}/api/auth/me after site A logout`
    );
    assert.equal(meBAfterLogoutA.user?.id, buyer.user.id, "site B session must remain active after site A logout");
    expectStatus(await clientB.get("/api/orders"), 200, `GET ${SITE_B_HOST}/api/orders while still logged in on B`);

    // Cleanly log out site B too.
    expectStatus(await clientB.post("/api/auth/logout", {}), 200, `POST ${SITE_B_HOST}/api/auth/logout`);
    assert.equal(browser.cookiesFor(SITE_B_HOST).length, 0, "logout on site B must clear site B cookies");
  });
});
