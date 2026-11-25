import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { ROOT_HOST, originOf, siteHost } from "./lib/config.mjs";
import {
  Browser,
  RateLimitBlockError,
  expectStatus,
  getAuthCookieValues,
  gw,
  parseRetryAfterMs,
  runAtomicBlockWith429Retry,
  sleep,
} from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  assertAuthCookieHeaders,
  assertNoInternalErrorLeak,
  assertNoTokenLeak,
  cancelOrderBestEffort,
  checkout,
  createShippingAddress,
  fillCart,
  newIdempotencyKey,
  pickSku,
  quoteShipping,
  registerBuyer,
  resolveSite,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);
const SITE_B_HOST = siteHost(SEEDED.saiGon.sub);
const bare = (host) => host.replace(/:\d+$/, "");

describe("12. Security, RBAC, CSRF, IDOR, page guards, refresh rotation/grace/dedup & logout invalidation", () => {
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

  it("1. Cross-buyer IDOR on site host: /api/orders/[id], /api/payments/[id] & SSR /don-hang/[id] deny without PII leak", async () => {
    const b1 = new Browser("sec-idor-b1").at(SITE_A_HOST);
    const b2 = new Browser("sec-idor-b2").at(SITE_A_HOST);
    let orderId = null;

    try {
      const buyer1 = await registerBuyer(b1, "sec-b1");
      const addr1 = await createShippingAddress(b1, "sec-b1", {
        recipientName: "BiMatNguoiMuaMot",
        streetAddress: "999 Hem Bi Mat IDOR",
      });
      const cart = await fillCart(b1, skuA.skuId, 1);
      const { shippingChoices } = await quoteShipping(b1, addr1, cart.shop_groups, false);

      const co = expectStatus(
        await checkout(b1, {
          addressId: addr1.id,
          shippingChoices,
          paymentMethod: "VIETQR",
          key: newIdempotencyKey("sec-idor"),
        }),
        201,
        "buyer1 VietQR checkout"
      );
      orderId = co.orders[0].id;
      const orderCode = co.orders[0].order_code;
      const paymentId = co.payment_id;

      await registerBuyer(b2, "sec-b2");

      const idorOrder = await b2.get(`/api/orders/${encodeURIComponent(orderId)}`);
      expectStatus(idorOrder, [403, 404], "buyer2 GET buyer1 order");
      assert.equal(idorOrder.text.includes("BiMatNguoiMuaMot"), false, "IDOR order leaked buyer1 name");
      assert.equal(idorOrder.text.includes("999 Hem Bi Mat IDOR"), false, "IDOR order leaked buyer1 address");
      assert.equal(idorOrder.text.includes(buyer1.creds.phone), false, "IDOR order leaked buyer1 phone");
      assertNoInternalErrorLeak(idorOrder.text, "IDOR order");

      const idorPay = await b2.get(`/api/payments/${encodeURIComponent(paymentId)}`);
      expectStatus(idorPay, [403, 404], "buyer2 GET buyer1 payment");
      assertNoInternalErrorLeak(idorPay.text, "IDOR payment");

      const idorSsr = await b2.page(`/don-hang/${encodeURIComponent(orderId)}`);
      assert.equal(idorSsr.status, 404, `buyer2 SSR /don-hang/${orderId} must be strict 404, got ${idorSsr.status}`);
      for (const secret of ["BiMatNguoiMuaMot", "999 Hem Bi Mat IDOR", orderCode, buyer1.creds.phone]) {
        assert.equal(idorSsr.text.includes(secret), false, `SSR IDOR leaked "${secret}"`);
      }
    } finally {
      await cancelOrderBestEffort(b1, orderId, "12_security IDOR cleanup");
      await Promise.allSettled([b1.post("/api/auth/logout", {}), b2.post("/api/auth/logout", {})]);
    }
  });

  it("2. 401 guards, CSRF BAD_ORIGIN, spoofed X-Forwarded-Host, and /api/gw/* removal on site & root hosts", async () => {
    const anonA = new Browser("sec-anon-a").at(SITE_A_HOST);
    const anonRoot = new Browser("sec-anon-root").at(ROOT_HOST);

    for (const [method, path, json] of [
      ["GET", "/api/cart"],
      ["POST", "/api/cart/items", { sku_id: skuA.skuId, quantity: 1 }],
      ["GET", "/api/orders"],
      ["POST", "/api/checkout", { from_cart: true, payment_method: "COD" }],
    ]) {
      const r = await anonA.request(method, path, { json });
      expectStatus(r, 401, `anon ${method} ${SITE_A_HOST}${path}`);
      assertNoInternalErrorLeak(r.text, `anon ${path}`);
    }

    // Legacy /api/gw/* catch-all is removed on BOTH site and root hosts.
    for (const client of [anonA, anonRoot]) {
      for (const path of ["/api/gw/products", "/api/gw/orders", "/api/gw/auth/login"]) {
        const r = await client.get(path);
        assert.equal(r.status, 404, `GET ${client.host}${path} must be 404, got ${r.status}`);
      }
    }

    // CSRF enforcement on site writes (missing Origin, evil Origin, other site Origin, Sec-Fetch-Site, spoofed X-Forwarded-Host).
    const b = new Browser("sec-csrf-b").at(SITE_A_HOST);
    try {
      await registerBuyer(b, "sec-csrf");
      for (const [label, opts] of [
        ["missing Origin", { origin: null }],
        ["evil Origin", { origin: "https://evil.example" }],
        ["cross-site host Origin", { origin: originOf(SITE_B_HOST) }],
        ["Sec-Fetch-Site: cross-site", { headers: { "sec-fetch-site": "cross-site" } }],
        [
          "spoofed X-Forwarded-Host matching evil Origin",
          { origin: "https://evil.example", headers: { "x-forwarded-host": "evil.example" } },
        ],
      ]) {
        const r = await b.post("/api/cart/items", { sku_id: skuA.skuId, quantity: 1 }, opts);
        expectStatus(r, 403, `CSRF ${label}`);
        assert.equal(r.data?.error?.code, "BAD_ORIGIN", `CSRF ${label} must return BAD_ORIGIN`);
      }
    } finally {
      await b.post("/api/auth/logout", {}).catch(() => {});
    }
  });

  it("3. Page redirects, role RBAC on root, PDP 200/404 on site, and removed marketplace pages 404 on root", async () => {
    const anonA = new Browser("sec-pg-a").at(SITE_A_HOST);
    const anonRoot = new Browser("sec-pg-root").at(ROOT_HOST);

    for (const path of ["/tai-khoan", "/don-hang"]) {
      const r = await anonA.page(path);
      expectStatus(r, [302, 303, 307, 308], `anon site ${path}`);
      const loc = new URL(r.location, `http://${SITE_A_HOST}`);
      assert.equal(loc.pathname, "/dang-nhap");
      assert.equal(bare(loc.host), bare(SITE_A_HOST));
    }
    for (const path of ["/seller", "/admin"]) {
      const r = await anonRoot.page(path);
      expectStatus(r, [302, 303, 307, 308], `anon root ${path}`);
      const loc = new URL(r.location, `http://${ROOT_HOST}`);
      assert.equal(loc.pathname, "/dang-nhap");
      assert.equal(bare(loc.host), bare(ROOT_HOST));
    }

    // Site PDP 200 & nonexistent PDP 404.
    expectStatus(await anonA.page(`/san-pham/${SEEDED.bachDang.productSlug}`), 200, "site valid PDP");
    assert.equal((await anonA.page("/san-pham/khong-ton-tai-e2e-404")).status, 404, "site unknown PDP must be 404");

    // Removed shared-marketplace pages on root host return 404.
    for (const removed of [
      "/danh-muc",
      "/san-pham",
      "/flash-sale",
      "/hoi-vien",
      "/bo-suu-tap",
      "/tap-chi",
      "/chien-dich",
      "/gio-hang",
      "/thanh-toan",
    ]) {
      const r = await anonRoot.page(removed);
      assert.equal(r.status, 404, `removed marketplace page ${ROOT_HOST}${removed} must return 404, got ${r.status}`);
    }
  });

  it("4. Refresh token rotation, 5s grace window, replay family revocation, concurrent dedup & logout invalidation", async () => {
    await runAtomicBlockWith429Retry("refresh-rotation-grace-replay", async () => {
      await resetRateLimitKeys();
      const browser = new Browser("sec-refresh");
      const site = browser.at(SITE_A_HOST);

      const acct = await registerBuyer(site, "sec-rt");
      const initial = getAuthCookieValues(browser, SITE_A_HOST);
      assert.ok(initial.at && initial.rt, "login must store nm_at & nm_rt");

      // GET /api/auth/me must return user without rotating refresh token or leaking tokens.
      const meRes = await site.get("/api/auth/me", { retry429: false });
      if (meRes.status === 429) {
        throw new RateLimitBlockError("GET /api/auth/me 429", parseRetryAfterMs(meRes.headers["retry-after"]));
      }
      expectStatus(meRes, 200, "GET /api/auth/me");
      assert.equal(meRes.data?.user?.id, acct.user.id);
      assertNoTokenLeak(meRes.text, "GET /api/auth/me", [initial.at, initial.rt]);
      assert.equal(getAuthCookieValues(browser, SITE_A_HOST).rt, initial.rt, "GET /api/auth/me must not rotate nm_rt");

      // Rotate via POST /api/auth/refresh.
      const rotRes = await site.post("/api/auth/refresh", {}, { retry429: false });
      if (rotRes.status === 429) {
        throw new RateLimitBlockError("POST /api/auth/refresh 429", parseRetryAfterMs(rotRes.headers["retry-after"]));
      }
      expectStatus(rotRes, 200, "POST /api/auth/refresh");
      assertAuthCookieHeaders(rotRes, "POST /api/auth/refresh");
      assertNoTokenLeak(rotRes.text, "POST /api/auth/refresh", [initial.at, initial.rt]);

      const rotated = getAuthCookieValues(browser, SITE_A_HOST);
      assert.notEqual(rotated.rt, initial.rt, "refresh rotation must issue a new refresh token");

      // Within the 5s grace window, replaying the previous refresh token succeeds without revoking the family.
      const graceRes = await site.post(
        "/api/auth/refresh",
        {},
        {
          retry429: false,
          cookieOverride: `${initial.rtName}=${initial.rt}`,
          updateJar: false,
        }
      );
      if (graceRes.status === 429) {
        throw new RateLimitBlockError("grace POST /api/auth/refresh 429", parseRetryAfterMs(graceRes.headers["retry-after"]));
      }
      expectStatus(graceRes, 200, "old refresh token replay within 5s grace window must succeed");

      // Current rotated session stays valid immediately after grace hit.
      const meAfterGrace = await site.get("/api/auth/me", { retry429: false });
      if (meAfterGrace.status === 429) {
        throw new RateLimitBlockError("GET /api/auth/me after grace 429", parseRetryAfterMs(meAfterGrace.headers["retry-after"]));
      }
      expectStatus(meAfterGrace, 200, "GET /api/auth/me after grace replay");
      assert.equal(meAfterGrace.data?.user?.id, acct.user.id);

      // Wait beyond the 5s grace window, then replay the stale initial token directly on gateway -> revokes token family.
      await sleep(5_500);
      const replayGw = await gw("POST", "/api/v1/auth/refresh", {
        body: { refresh_token: initial.rt },
        retry429: false,
      });
      if (replayGw.status === 429) {
        throw new RateLimitBlockError("gateway stale refresh replay 429", parseRetryAfterMs(replayGw.headers.get("retry-after")));
      }
      assert.equal(replayGw.status, 401, `stale refresh replay after grace window must return 401, got ${replayGw.status}`);

      // Because family is revoked, the rotated refresh token must also fail with 401.
      const afterRevoke = await site.post("/api/auth/refresh", {}, { retry429: false });
      if (afterRevoke.status === 429) {
        throw new RateLimitBlockError("post-revoke refresh 429", parseRetryAfterMs(afterRevoke.headers["retry-after"]));
      }
      assert.equal(afterRevoke.status, 401, `rotated refresh token must fail 401 after family revocation, got ${afterRevoke.status}`);
    });

    // Logout clears cookies and invalidates the old session token.
    await runAtomicBlockWith429Retry("logout-invalidation-and-concurrent-refresh", async () => {
      await resetRateLimitKeys();
      const browser = new Browser("sec-logout");
      const site = browser.at(SITE_A_HOST);

      await registerBuyer(site, "sec-lo");
      const before = getAuthCookieValues(browser, SITE_A_HOST);

      // Concurrent refresh deduplication (parallel POST /api/auth/refresh + protected SSR with missing access cookie).
      const [r1, r2] = await Promise.all([
        site.post(
          "/api/auth/refresh",
          {},
          { retry429: false, cookieOverride: `${before.rtName}=${before.rt}`, updateJar: false }
        ),
        site.page("/tai-khoan", {
          retry429: false,
          cookieOverride: `${before.rtName}=${before.rt}`,
          updateJar: false,
        }),
      ]);
      if (r1.status === 429 || r2.status === 429) {
        throw new RateLimitBlockError("concurrent refresh 429", 5_000);
      }
      assert.equal(r1.status, 200, `parallel API refresh expected 200, got ${r1.status}`);
      assert.equal(r2.status, 200, `parallel SSR refresh on /tai-khoan expected 200, got ${r2.status}`);

      // Apply the rotated cookies from r1, then log out.
      browser.store(SITE_A_HOST, r1.setCookie);
      const preLogout = getAuthCookieValues(browser, SITE_A_HOST);
      expectStatus(await site.post("/api/auth/logout", {}), 200, "POST /api/auth/logout");
      assert.equal(browser.cookiesFor(SITE_A_HOST).length, 0, "logout must clear all auth cookies");

      // Replaying the pre-logout refresh cookie must fail with 401.
      const replayAfterLogout = await site.post(
        "/api/auth/refresh",
        {},
        {
          retry429: false,
          cookieOverride: `${preLogout.rtName}=${preLogout.rt}`,
          updateJar: false,
        }
      );
      if (replayAfterLogout.status === 429) {
        throw new RateLimitBlockError("post-logout refresh replay 429", parseRetryAfterMs(replayAfterLogout.headers["retry-after"]));
      }
      assert.equal(replayAfterLogout.status, 401, "refresh token must be revoked after logout");
    });
  });
});
