import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { ROOT_HOST, config, originOf, siteHost } from "./lib/config.mjs";
import { Browser, expectStatus } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  assertNoInternalErrorLeak,
  cartSkuIds,
  checkout,
  createShippingAddress,
  demoCredentials,
  fillCart,
  listOrderIds,
  loginOn,
  newIdempotencyKey,
  pickPlatformVoucher,
  pickShopVoucher,
  pickSku,
  primarySiteHost,
  quoteShipping,
  registerBuyer,
  resolveSite,
} from "./lib/fixtures.mjs";

const ROOT = config.siteRootDomain;
const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);
const SITE_B_HOST = siteHost(SEEDED.saiGon.sub);
const FAKE_ID = "00000000-0000-4000-8000-000000000000";

describe("03. Cross-site isolation, IDOR, 401 guards, CSRF across hosts, body tampering & blocked routes", () => {
  let siteA;
  let siteB;
  let skuA;
  let skuB;
  let voucherA;
  let voucherB;
  let platformVoucher;

  let browser1;
  let buyer1;
  let buyer1A;
  let buyer1B;
  let addr1A;
  let addr1B;
  let orderA;
  let paymentA;
  let orderB;

  before(async () => {
    await resetRateLimitKeys();
    siteA = await resolveSite(SEEDED.bachDang.sub);
    siteB = await resolveSite(SEEDED.saiGon.sub);
    skuA = await pickSku(siteA.shopId, { minStock: 10 });
    skuB = await pickSku(siteB.shopId, { minStock: 10 });
    voucherA = await pickShopVoucher(siteA.shopId, SEEDED.bachDang.voucherCode);
    voucherB = await pickShopVoucher(siteB.shopId, SEEDED.saiGon.voucherCode);
    platformVoucher = await pickPlatformVoucher("NEXUS30");

    browser1 = new Browser("buyer-1");
    buyer1A = browser1.at(SITE_A_HOST);
    buyer1B = browser1.at(SITE_B_HOST);
  });

  after(async () => {
    await Promise.allSettled([
      buyer1A.post("/api/auth/logout", {}),
      buyer1B.post("/api/auth/logout", {}),
    ]);
    await resetRateLimitKeys();
  });

  it("unknown, foreign, reserved, and multi-level hosts return 404 on pages and APIs", async () => {
    const badHosts = [
      `unknown-shop.${ROOT}`,
      "evil.example.com",
      `a.b.${ROOT}`,
      `admin.${ROOT}`,
      `api.${ROOT}`,
      `seller.${ROOT}`,
      `app.${ROOT}`,
      `nexusmart.${ROOT}`,
      `vietcombank-uudai.${ROOT}`,
    ];
    for (const host of badHosts) {
      const client = new Browser(`bad-host-${host}`).at(host);
      for (const path of ["/", "/api/auth/me", "/api/cart"]) {
        const res = await client.get(path, { headers: { accept: "*/*" } });
        assert.equal(res.status, 404, `Host ${host}${path} must return 404, got ${res.status}`);
      }
    }
  });

  it("www.<SITE_ROOT_DOMAIN> permanently redirects (308) pages and /api/* to apex origin preserving path and query", async () => {
    const wwwHost = `www.${ROOT}`;
    const configuredAppOrigin = (process.env.APP_ORIGIN ?? "")
      .split(",")
      .map((s) => s.trim().replace(/\/+$/, ""))
      .filter(Boolean)[0];
    let expectedOrigin = "";
    if (configuredAppOrigin) {
      try {
        const parsed = new URL(configuredAppOrigin);
        if (
          (parsed.protocol === "http:" || parsed.protocol === "https:") &&
          parsed.hostname.toLowerCase() !== wwwHost
        ) {
          expectedOrigin = parsed.origin;
        }
      } catch {
        // Fall back to request protocol + SITE_ROOT_DOMAIN below.
      }
    }
    if (!expectedOrigin) {
      expectedOrigin = `${config.webProtocol}://${ROOT}`;
    }

    // Browser/rawRequest uses node:http(s) directly and never auto-follows redirects (manual redirect).
    const client = new Browser("www-redirect").at(wwwHost);
    for (const path of ["/", "/ve-nexusmart?ref=e2e&tab=1", "/api/auth/me", "/api/cart?v=1"]) {
      const res = await client.get(path, { headers: { accept: "*/*" } });
      assert.equal(res.status, 308, `Host ${wwwHost}${path} must return 308, got ${res.status}`);
      assert.equal(
        res.location,
        `${expectedOrigin}${path}`,
        `Host ${wwwHost}${path} must redirect to ${expectedOrigin}${path}, got ${res.location}`
      );
    }

    const postRes = await client.post("/api/auth/login?from=www", { email: "x@example.com" });
    assert.equal(
      postRes.status,
      308,
      `POST ${wwwHost}/api/auth/login?from=www must return 308, got ${postRes.status}`
    );
    assert.equal(
      postRes.location,
      `${expectedOrigin}/api/auth/login?from=www`,
      `POST ${wwwHost}/api/auth/login?from=www must redirect to ${expectedOrigin}/api/auth/login?from=www, got ${postRes.location}`
    );
  });

  it("direct /s/... is 404 on every host; /seller, /admin, /api/seller/*, /api/admin/* are 404 on site hosts", async () => {
    const anonRoot = new Browser("internal-root").at(ROOT_HOST);
    for (const path of ["/s", `/s/${SEEDED.bachDang.sub}.${ROOT}`, `/s/${SEEDED.bachDang.sub}.${ROOT}/san-pham`]) {
      const res = await anonRoot.page(path);
      assert.equal(res.status, 404, `Direct root access to ${path} must return 404, got ${res.status}`);
    }

    const anonSiteA = new Browser("blocked-site-a").at(SITE_A_HOST);
    const blockedOnSite = [
      "/s",
      `/s/${SEEDED.bachDang.sub}.${ROOT}`,
      "/seller",
      "/seller/site",
      "/seller/don-hang",
      "/admin",
      "/admin/sites",
      "/api/seller/shop",
      "/api/seller/orders",
      "/api/seller/site",
      "/api/admin/orders",
      "/api/admin/sites",
      "/api/admin/ledger",
    ];
    for (const path of blockedOnSite) {
      const res = await anonSiteA.get(path, { headers: { accept: "*/*" } });
      assert.equal(res.status, 404, `Blocked path ${SITE_A_HOST}${path} must return 404, got ${res.status}`);
    }

    // Even a logged-in seller/admin session on site A still gets 404 on /seller, /admin, /api/seller/*, /api/admin/*.
    const adminOnSiteA = new Browser("admin-on-site-a").at(SITE_A_HOST);
    await loginOn(adminOnSiteA, demoCredentials.admin.email, demoCredentials.admin.password, "admin-site-a");
    try {
      for (const path of ["/seller", "/admin", "/api/seller/shop", "/api/admin/sites", "/api/admin/orders"]) {
        const res = await adminOnSiteA.get(path, { headers: { accept: "*/*" } });
        assert.equal(res.status, 404, `Admin on ${SITE_A_HOST}${path} must still get 404, got ${res.status}`);
      }
    } finally {
      await adminOnSiteA.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("all 4 seeded sites render distinct themes, isolate products/vouchers, and return 404 on cross-shop PDP", async () => {
    for (const shop of Object.values(SEEDED)) {
      const host = primarySiteHost(shop);
      const home = await new Browser(`theme-${shop.sub}`).at(host).page("/");
      expectStatus(home, 200, `GET ${host}/`);
      assert.ok(home.text.includes(shop.shopName), `${host}/ must render ${shop.shopName}`);
      assert.match(
        home.text,
        /Vận hành trên(?:<!--\s*-->|\s)*(?:<a\b[^>]*>)?NexusMart/,
        `${host}/ must display NexusMart attribution`
      );
      assert.ok(
        home.text.includes(`data-site-theme-mode="${shop.theme.mode}"`) &&
          home.text.includes(shop.theme.bg) &&
          home.text.includes(shop.theme.accent),
        `${host}/ must render distinct theme tokens (${shop.theme.mode}, ${shop.theme.bg}, ${shop.theme.accent})`
      );
    }

    const anonA = new Browser("iso-a").at(SITE_A_HOST);
    const anonB = new Browser("iso-b").at(SITE_B_HOST);

    const homeA = await anonA.page("/");
    assert.ok(homeA.text.includes(SEEDED.bachDang.voucherCode), "Site A must show Shop A voucher BACHDANG20");
    for (const forbidden of [
      SEEDED.saiGon.productSlug,
      SEEDED.saiGon.shopName,
      SEEDED.saiGon.voucherCode,
      SEEDED.daNang.voucherCode,
      SEEDED.haNoi.voucherCode,
      "NEXUS30",
      "FREESHIP25",
    ]) {
      assert.equal(homeA.text.includes(forbidden), false, `Site A leaked foreign/platform item: ${forbidden}`);
    }

    const crossAtoB = await anonA.page(`/san-pham/${SEEDED.saiGon.productSlug}`);
    assert.equal(crossAtoB.status, 404, `Shop B PDP on Site A must return 404, got ${crossAtoB.status}`);

    const crossBtoA = await anonB.page(`/san-pham/${SEEDED.bachDang.productSlug}`);
    assert.equal(crossBtoA.status, 404, `Shop A PDP on Site B must return 404, got ${crossBtoA.status}`);
  });

  it("cookie session of site A does NOT authenticate on site B (host-only isolation)", async () => {
    buyer1 = await registerBuyer(buyer1A, "iso-buyer1", { verifyPhone: true });

    // Host-only cookies: jar has cookies for site A only, zero for site B or root.
    assert.ok(browser1.cookiesFor(SITE_A_HOST).length >= 2, "browser must hold auth cookies for site A");
    assert.equal(browser1.cookiesFor(SITE_B_HOST).length, 0, "site A cookies must NEVER match site B");
    assert.equal(browser1.cookiesFor(ROOT_HOST).length, 0, "site A cookies must NEVER match root host");

    // Same browser visiting site B without logging in on B is unauthenticated.
    const meB = await buyer1B.get("/api/auth/me");
    expectStatus(meB, [200, 401], `GET ${SITE_B_HOST}/api/auth/me with only site A session`);
    if (meB.status === 200) {
      assert.equal(meB.data?.user ?? null, null, "site B /api/auth/me must return user=null with only site A cookies");
    }
    expectStatus(await buyer1B.get("/api/cart"), 401, `GET ${SITE_B_HOST}/api/cart without site B session`);
    expectStatus(await buyer1B.get("/api/orders"), 401, `GET ${SITE_B_HOST}/api/orders without site B session`);
    expectStatus(
      await buyer1B.post("/api/cart/items", { sku_id: skuB.skuId, quantity: 1 }),
      401,
      `POST ${SITE_B_HOST}/api/cart/items without site B session`
    );
  });

  it("site A rejects site B SKUs and site B / platform vouchers; carts stay isolated per site", async () => {
    // Foreign SKU on site A -> 400 or 404.
    const badSku = await buyer1A.post("/api/cart/items", { sku_id: skuB.skuId, quantity: 1 });
    expectStatus(badSku, [400, 404], `POST ${SITE_A_HOST}/api/cart/items with shop B SKU`);
    assertNoInternalErrorLeak(badSku.text, "foreign SKU on site A");

    // Log the same buyer into site B and put skuB in site B's cart, skuA in site A's cart.
    await loginOn(buyer1B, buyer1.creds.email, buyer1.creds.password, "buyer1-on-site-b");
    const cartB = await fillCart(buyer1B, skuB.skuId, 1);
    assert.deepEqual(cartSkuIds(cartB), [skuB.skuId], "site B cart must hold skuB");

    const cartA = await fillCart(buyer1A, skuA.skuId, 1);
    assert.deepEqual(cartSkuIds(cartA), [skuA.skuId], "site A cart must hold ONLY skuA, never skuB");

    // Clearing site A's cart must NOT wipe site B's cart.
    expectStatus(await buyer1A.del("/api/cart"), [200, 204], `DELETE ${SITE_A_HOST}/api/cart`);
    const cartBAfterClearA = expectStatus(await buyer1B.get("/api/cart"), 200, `GET ${SITE_B_HOST}/api/cart`);
    assert.deepEqual(cartSkuIds(cartBAfterClearA.cart), [skuB.skuId], "DELETE /api/cart on A must not affect B");

    // Re-populate site A cart so voucher rejection tests run against a non-empty cart.
    const qtyForVoucher = Math.min(5, Math.max(1, Math.ceil((voucherB.min_order_vnd || 0) / skuA.price)));
    await fillCart(buyer1A, skuA.skuId, qtyForVoucher);

    // Site B voucher on Site A -> rejected (400/404).
    const valForeign = await buyer1A.post("/api/vouchers/validate", { code: voucherB.code });
    expectStatus(valForeign, [400, 404], `POST ${SITE_A_HOST}/api/vouchers/validate with shop B voucher ${voucherB.code}`);
    assertNoInternalErrorLeak(valForeign.text, "foreign voucher validate on site A");

    const saveForeign = await buyer1A.post(`/api/vouchers/${encodeURIComponent(voucherB.id)}/save`, {});
    expectStatus(saveForeign, [400, 404], `POST ${SITE_A_HOST}/api/vouchers/${voucherB.code}/save on site A`);
    assertNoInternalErrorLeak(saveForeign.text, "foreign voucher save on site A");

    // Platform voucher on Site A -> rejected (400/404).
    if (platformVoucher) {
      const valPlatform = await buyer1A.post("/api/vouchers/validate", { code: platformVoucher.code });
      expectStatus(
        valPlatform,
        [400, 404],
        `POST ${SITE_A_HOST}/api/vouchers/validate with platform voucher ${platformVoucher.code}`
      );
      const savePlatform = await buyer1A.post(`/api/vouchers/${encodeURIComponent(platformVoucher.id)}/save`, {});
      expectStatus(
        savePlatform,
        [400, 404],
        `POST ${SITE_A_HOST}/api/vouchers/${platformVoucher.code}/save on site A`
      );
    }

    // /api/vouchers/coins does not exist in Phase 4 (coins removed from storefront UI/BFF).
    const coinsRes = await buyer1A.get("/api/vouchers/coins");
    assert.equal(coinsRes.status, 404, `GET ${SITE_A_HOST}/api/vouchers/coins must be 404, got ${coinsRes.status}`);

    // Preview with foreign/platform voucher on Site A -> rejected 400/404 (or zero discount if 200).
    const prevForeign = await buyer1A.post("/api/vouchers/preview", {
      shop_voucher_ids: { [siteB.shopId]: voucherB.id, [siteA.shopId]: voucherB.id },
      ...(platformVoucher ? { platform_voucher_id: platformVoucher.id } : {}),
      coins_to_use: 10000,
    });
    expectStatus(prevForeign, [200, 400, 404], `POST ${SITE_A_HOST}/api/vouchers/preview with foreign voucher`);
    if (prevForeign.status === 200) {
      assert.equal(prevForeign.data?.breakdown?.total_shop_voucher_discount_vnd ?? 0, 0, "foreign shop voucher must give 0 discount");
      assert.equal(prevForeign.data?.breakdown?.total_platform_voucher_discount_vnd ?? 0, 0, "platform voucher must give 0 discount");
      assert.equal(prevForeign.data?.breakdown?.total_coins_discount_vnd ?? 0, 0, "coins must give 0 discount on site");
    }
  });

  it("same account logged into both site A and site B: orders & payments of A are invisible (404) on B and vice versa", async () => {
    addr1A = await createShippingAddress(buyer1A, "buyer1-a");
    addr1B = addr1A; // shared backend user account owns the address

    // Place VietQR order on site A (gives us both orderA and paymentA).
    const cartA = await fillCart(buyer1A, skuA.skuId, 1);
    const shipA = await quoteShipping(buyer1A, addr1A, cartA.shop_groups, false);
    const chkA = expectStatus(
      await checkout(buyer1A, {
        addressId: addr1A.id,
        shippingChoices: shipA.shippingChoices,
        paymentMethod: "VIETQR",
        key: newIdempotencyKey("iso-order-a"),
      }),
      201,
      `POST ${SITE_A_HOST}/api/checkout (buyer1 VietQR)`
    );
    orderA = chkA.orders[0];
    paymentA = chkA.payment_id;
    assert.ok(orderA?.id && paymentA);

    // Place COD order on site B with the SAME buyer account.
    const cartB = await fillCart(buyer1B, skuB.skuId, 1);
    const shipB = await quoteShipping(buyer1B, addr1B, cartB.shop_groups, true);
    const chkB = expectStatus(
      await checkout(buyer1B, {
        addressId: addr1B.id,
        shippingChoices: shipB.shippingChoices,
        paymentMethod: "COD",
        key: newIdempotencyKey("iso-order-b"),
      }),
      201,
      `POST ${SITE_B_HOST}/api/checkout (buyer1 COD)`
    );
    orderB = chkB.orders[0];
    assert.ok(orderB?.id);
    assert.equal(orderB.sales_channel, "STOREFRONT");
    assert.equal(orderB.storefront_shop_id, siteB.shopId);

    // Positive controls on own site.
    expectStatus(await buyer1A.get(`/api/orders/${encodeURIComponent(orderA.id)}`), 200, "buyer1 reads orderA on site A");
    expectStatus(await buyer1A.get(`/api/payments/${encodeURIComponent(paymentA)}`), 200, "buyer1 reads paymentA on site A");
    expectStatus(await buyer1B.get(`/api/orders/${encodeURIComponent(orderB.id)}`), 200, "buyer1 reads orderB on site B");

    // Cross-site isolation on Site B for the SAME user!
    const listB = await listOrderIds(buyer1B);
    assert.ok(listB.ids.includes(orderB.id), "site B /api/orders must list orderB");
    assert.equal(listB.ids.includes(orderA.id), false, "site B /api/orders must NOT list orderA from site A");

    const bReadOrderA = await buyer1B.get(`/api/orders/${encodeURIComponent(orderA.id)}`);
    expectStatus(bReadOrderA, 404, `GET ${SITE_B_HOST}/api/orders/${orderA.id} (same user, cross-site)`);
    assert.equal(bReadOrderA.text.includes(orderA.order_code), false, "404 must not leak orderA order_code");
    assertNoInternalErrorLeak(bReadOrderA.text, "cross-site GET orderA on site B");

    const bCancelOrderA = await buyer1B.post(`/api/orders/${encodeURIComponent(orderA.id)}/cancel`, { reason: "cross-site" });
    expectStatus(bCancelOrderA, 404, `POST ${SITE_B_HOST}/api/orders/${orderA.id}/cancel (same user, cross-site)`);

    const bReadPaymentA = await buyer1B.get(`/api/payments/${encodeURIComponent(paymentA)}`);
    expectStatus(bReadPaymentA, 404, `GET ${SITE_B_HOST}/api/payments/${paymentA} (same user, cross-site)`);

    const bOrderPageA = await buyer1B.page(`/don-hang/${encodeURIComponent(orderA.id)}`);
    expectStatus(bOrderPageA, 404, `GET ${SITE_B_HOST}/don-hang/${orderA.id} (same user, cross-site)`);

    // Symmetric check on Site A: orderB from Site B must NOT appear or be readable on Site A!
    const listA = await listOrderIds(buyer1A);
    assert.ok(listA.ids.includes(orderA.id), "site A /api/orders must list orderA");
    assert.equal(listA.ids.includes(orderB.id), false, "site A /api/orders must NOT list orderB from site B");
    expectStatus(
      await buyer1A.get(`/api/orders/${encodeURIComponent(orderB.id)}`),
      404,
      `GET ${SITE_A_HOST}/api/orders/${orderB.id} (same user, cross-site)`
    );
  });

  it("IDOR between two different buyers on the same site A returns 403/404", async () => {
    const browser2 = new Browser("buyer-2-idor");
    const buyer2A = browser2.at(SITE_A_HOST);
    await registerBuyer(buyer2A, "iso-buyer2");
    try {
      const readOrder = await buyer2A.get(`/api/orders/${encodeURIComponent(orderA.id)}`);
      expectStatus(readOrder, [403, 404], "Buyer 2 reading Buyer 1 orderA on site A");
      assert.equal(readOrder.text.includes(orderA.order_code), false, "IDOR error must not leak order_code");
      assertNoInternalErrorLeak(readOrder.text, "IDOR GET order");

      const cancelOrder = await buyer2A.post(`/api/orders/${encodeURIComponent(orderA.id)}/cancel`, { reason: "IDOR" });
      expectStatus(cancelOrder, [403, 404], "Buyer 2 cancelling Buyer 1 orderA on site A");
      assertNoInternalErrorLeak(cancelOrder.text, "IDOR cancel order");

      const readPay = await buyer2A.get(`/api/payments/${encodeURIComponent(paymentA)}`);
      expectStatus(readPay, [403, 404], "Buyer 2 reading Buyer 1 paymentA on site A");
      assertNoInternalErrorLeak(readPay.text, "IDOR GET payment");

      const orderPage = await buyer2A.page(`/don-hang/${encodeURIComponent(orderA.id)}`);
      expectStatus(orderPage, [403, 404], "Buyer 2 viewing Buyer 1 /don-hang/[id] on site A");
    } finally {
      await buyer2A.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("unauthenticated writes and private reads on site A return 401 without leaking internals", async () => {
    const anonA = new Browser("anon-401").at(SITE_A_HOST);
    const checks = [
      ["GET /api/cart", () => anonA.get("/api/cart")],
      ["POST /api/cart/items", () => anonA.post("/api/cart/items", { sku_id: skuA.skuId, quantity: 1 })],
      ["PATCH /api/cart/items/[skuId]", () => anonA.patch(`/api/cart/items/${encodeURIComponent(skuA.skuId)}`, { quantity: 1 })],
      ["DELETE /api/cart", () => anonA.del("/api/cart")],
      ["POST /api/checkout", () => anonA.post("/api/checkout", { address_id: addr1A.id, payment_method: "COD" })],
      ["POST /api/checkout/addresses", () => anonA.post("/api/checkout/addresses", { recipient_name: "Anon" })],
      ["POST /api/vouchers/validate", () => anonA.post("/api/vouchers/validate", { code: voucherA.code })],
      ["POST /api/vouchers/preview", () => anonA.post("/api/vouchers/preview", {})],
      ["POST /api/vouchers/[id]/save", () => anonA.post(`/api/vouchers/${encodeURIComponent(voucherA.id)}/save`, {})],
      ["GET /api/orders", () => anonA.get("/api/orders")],
      ["GET /api/orders/[id]", () => anonA.get(`/api/orders/${encodeURIComponent(orderA.id)}`)],
      ["POST /api/orders/[id]/cancel", () => anonA.post(`/api/orders/${encodeURIComponent(orderA.id)}/cancel`, {})],
      ["GET /api/payments/[id]", () => anonA.get(`/api/payments/${encodeURIComponent(paymentA)}`)],
      ["PATCH /api/auth/me", () => anonA.patch("/api/auth/me", { fullName: "Unauth Edit" })],
    ];
    for (const [label, fn] of checks) {
      const res = await fn();
      expectStatus(res, 401, `Unauthenticated ${SITE_A_HOST} ${label}`);
      assertNoInternalErrorLeak(res.text, `Unauthenticated ${label}`);
    }

    // E18: POST /api/checkout/shipping-quote is intentionally public for guest PDP fee estimation,
    // must still scope to this site's shop and never leak internal/secret fields.
    const pubQuote = await anonA.post("/api/checkout/shipping-quote", {
      to_province_code: "01",
      to_ward_code: "",
      shops: [{ shop_id: siteA.shopId, weight_grams: 400, cod_amount_vnd: 0 }],
    });
    expectStatus(pubQuote, 200, `Public ${SITE_A_HOST} POST /api/checkout/shipping-quote`);
    assertNoInternalErrorLeak(pubQuote.text, "Public shipping-quote");
    assert.equal(pubQuote.text.includes("commission_rate_bps"), false, "Public shipping-quote leaked commission_rate_bps");
    assert.equal(pubQuote.text.includes("access_token"), false, "Public shipping-quote leaked token field");
  });

  it("CSRF rejects Origin of another site, root host, evil origin, missing/null Origin, Fetch-Metadata & spoofed proxy headers with 403", async () => {
    const evilOrigin = "https://evil.example";
    const siteBOrigin = originOf(SITE_B_HOST);
    const rootOrigin = originOf(ROOT_HOST);
    const siteAOrigin = originOf(SITE_A_HOST);

    const csrfChecks = [
      [
        "site B Origin -> site A POST /api/cart/items",
        () => buyer1A.post("/api/cart/items", { sku_id: skuA.skuId, quantity: 1 }, { origin: siteBOrigin }),
      ],
      [
        "root host Origin -> site A POST /api/cart/items",
        () => buyer1A.post("/api/cart/items", { sku_id: skuA.skuId, quantity: 1 }, { origin: rootOrigin }),
      ],
      [
        "site B Origin -> site A POST /api/checkout",
        () =>
          buyer1A.post(
            "/api/checkout",
            { address_id: addr1A.id, from_cart: true, payment_method: "COD" },
            { origin: siteBOrigin, headers: { "Idempotency-Key": newIdempotencyKey("csrf-b") } }
          ),
      ],
      [
        "root host Origin -> site A POST /api/orders/[id]/cancel",
        () => buyer1A.post(`/api/orders/${encodeURIComponent(orderA.id)}/cancel`, { reason: "CSRF" }, { origin: rootOrigin }),
      ],
      [
        "evil Origin -> site A PATCH /api/auth/me",
        () => buyer1A.patch("/api/auth/me", { fullName: "Hijacked" }, { origin: evilOrigin }),
      ],
      [
        "site B Origin -> site A POST /api/auth/logout",
        () => buyer1A.post("/api/auth/logout", {}, { origin: siteBOrigin }),
      ],
      [
        "missing Origin & Referer -> site A POST /api/cart/items",
        () => buyer1A.post("/api/cart/items", { sku_id: skuA.skuId, quantity: 1 }, { origin: null }),
      ],
      [
        "Origin: null -> site A POST /api/cart/items",
        () => buyer1A.post("/api/cart/items", { sku_id: skuA.skuId, quantity: 1 }, { origin: "null" }),
      ],
      [
        "Sec-Fetch-Site: cross-site -> site A POST /api/cart/items",
        () =>
          buyer1A.post(
            "/api/cart/items",
            { sku_id: skuA.skuId, quantity: 1 },
            { origin: siteAOrigin, headers: { "sec-fetch-site": "cross-site" } }
          ),
      ],
      [
        "Sec-Fetch-Site: same-site (sibling subdomain) -> site A POST /api/cart/items",
        () =>
          buyer1A.post(
            "/api/cart/items",
            { sku_id: skuA.skuId, quantity: 1 },
            { origin: siteAOrigin, headers: { "sec-fetch-site": "same-site" } }
          ),
      ],
      [
        "spoofed X-Forwarded-Host matching evil Origin -> site A POST /api/cart/items",
        () =>
          buyer1A.post(
            "/api/cart/items",
            { sku_id: skuA.skuId, quantity: 1 },
            {
              origin: evilOrigin,
              headers: { "x-forwarded-host": "evil.example", "x-forwarded-proto": "https" },
            }
          ),
      ],
    ];

    for (const [label, fn] of csrfChecks) {
      const res = await fn();
      expectStatus(res, 403, `CSRF ${label}`);
      assert.equal(res.data?.error?.code, "BAD_ORIGIN", `CSRF ${label}: expected BAD_ORIGIN`);
      assertNoInternalErrorLeak(res.text, `CSRF ${label}`);
    }

    // Blocked CSRF logout must NOT have killed buyer1's session on site A.
    const stillAuthed = expectStatus(await buyer1A.get("/api/auth/me"), 200, "buyer1 /api/auth/me after blocked CSRF logout");
    assert.equal(stillAuthed.user?.id, buyer1.user.id);

    // Unauth redirect on site A ignores spoofed X-Forwarded-Host.
    const anonSpoof = new Browser("spoof-redir").at(SITE_A_HOST);
    const redir = await anonSpoof.page("/don-hang", {
      headers: { "x-forwarded-host": "evil.example", "x-forwarded-proto": "https" },
    });
    expectStatus(redir, [302, 303, 307, 308], "unauth redirect with spoofed X-Forwarded-Host");
    assert.equal(redir.location.includes("evil.example"), false, `redirect must ignore spoofed X-Forwarded-Host: ${redir.location}`);
  });

  it("checkout body tampering (MARKETPLACE / foreign storefront_shop_id / platform discounts / foreign SKU items) is ignored or rejected", async () => {
    const cartA = await fillCart(buyer1A, skuA.skuId, 1);
    const { shippingChoices } = await quoteShipping(buyer1A, addr1A, cartA.shop_groups, true);

    // 1. Tamper sales_channel=MARKETPLACE and storefront_shop_id=shopB on Site A:
    //    MUST either be rejected (400/404) OR ignored so the order is STILL STOREFRONT for shop A.
    const tamperedChannel = await checkout(buyer1A, {
      addressId: addr1A.id,
      shippingChoices,
      paymentMethod: "COD",
      key: newIdempotencyKey("tamper-channel"),
      extra: {
        sales_channel: "MARKETPLACE",
        storefront_shop_id: siteB.shopId,
        checkout_channel_hint: "marketplace",
        shop_id: siteB.shopId,
      },
    });
    expectStatus(tamperedChannel, [201, 400, 404], `POST ${SITE_A_HOST}/api/checkout with tampered sales_channel/shop`);
    if (tamperedChannel.status === 201) {
      const data = tamperedChannel.data;
      assert.equal(data.sales_channel, "STOREFRONT", "tampered sales_channel=MARKETPLACE must be forced to STOREFRONT");
      assert.equal(data.storefront_shop_id, siteA.shopId, "tampered storefront_shop_id must be forced to site A's shop");
      assert.equal(data.orders.length, 1);
      assert.equal(data.orders[0].shop_id, siteA.shopId);
      assert.equal(data.orders[0].sales_channel, "STOREFRONT");
      assert.equal(data.orders[0].storefront_shop_id, siteA.shopId);

      const detail = expectStatus(
        await buyer1A.get(`/api/orders/${encodeURIComponent(data.orders[0].id)}`),
        200,
        "GET tampered order detail on site A"
      );
      assert.equal(detail.order.sales_channel ?? detail.order.salesChannel, "STOREFRONT");
      assert.equal(detail.order.storefront_shop_id ?? detail.order.storefrontShopId, siteA.shopId);
    }

    // 2. Tamper foreign shop voucher / platform voucher / freeship / coins on Site A -> rejected (400/404) or zero discount.
    const cartA2 = await fillCart(buyer1A, skuA.skuId, 1);
    const shipA2 = await quoteShipping(buyer1A, addr1A, cartA2.shop_groups, true);
    const tamperedDiscounts = await checkout(buyer1A, {
      addressId: addr1A.id,
      shippingChoices: shipA2.shippingChoices,
      paymentMethod: "COD",
      key: newIdempotencyKey("tamper-discounts"),
      extra: {
        ...(platformVoucher ? { platform_voucher_id: platformVoucher.id } : {}),
        freeship_voucher_id: FAKE_ID,
        coins_to_use: 10000,
        shop_voucher_ids: { [siteB.shopId]: voucherB.id },
      },
    });
    expectStatus(tamperedDiscounts, [201, 400, 404], `POST ${SITE_A_HOST}/api/checkout with foreign/platform discounts`);
    if (tamperedDiscounts.status === 201) {
      const detail = expectStatus(
        await buyer1A.get(`/api/orders/${encodeURIComponent(tamperedDiscounts.data.orders[0].id)}`),
        200,
        "GET tampered-discount order detail on site A"
      );
      assert.equal(Number(detail.order.platformVoucherDiscount || 0), 0, "platform voucher must not apply on site");
      assert.equal(Number(detail.order.freeshipDiscount || 0), 0, "freeship voucher must not apply on site");
      assert.equal(Number(detail.order.coinsDiscount || 0), 0, "coins must not apply on site");
      assert.equal(Number(detail.order.shopVoucherDiscount || 0), 0, "foreign shop voucher must not apply on site");
    }

    // 3. Direct-item checkout (`from_cart: false`) smuggling Shop B's SKU on Site A -> rejected (400/404).
    const smuggledSku = await buyer1A.post(
      "/api/checkout",
      {
        address_id: addr1A.id,
        from_cart: false,
        items: [{ sku_id: skuB.skuId, quantity: 1 }],
        payment_method: "COD",
        shipping_choices: [],
      },
      { headers: { "Idempotency-Key": newIdempotencyKey("tamper-sku") } }
    );
    expectStatus(smuggledSku, [400, 404], `POST ${SITE_A_HOST}/api/checkout smuggling Shop B SKU`);
    assertNoInternalErrorLeak(smuggledSku.text, "smuggled Shop B SKU checkout on site A");
  });
});
