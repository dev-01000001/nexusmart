import assert from "node:assert/strict";
import crypto from "node:crypto";
import { describe, test } from "node:test";
import {
  ACCESS_COOKIE,
  REFRESH_COOKIE,
  RateLimitBlockError,
  WebClient,
  calculateShippingQuote,
  config,
  createBuyerAddress,
  ensureRateBudget,
  listCatalogProducts,
  parseRetryAfterMs,
  pickSingleShopSku,
  probeIpRateLimit,
  registerAndLoginBuyer,
  runAtomicBlockWith429Retry,
} from "./helpers.mjs";

function throwIfRateLimited(res, label) {
  if (res.status === 429) {
    const rawRetry = res.headers.get("retry-after");
    throw new RateLimitBlockError(
      `${label} returned HTTP 429`,
      rawRetry ? parseRetryAfterMs(rawRetry) : null
    );
  }
}

async function throwIfAnonymousRenderDueTo429(label) {
  const probe = await probeIpRateLimit();
  if (probe.status === 429 || (probe.remaining !== null && probe.remaining <= 10)) {
    throw new RateLimitBlockError(
      `${label} rendered anonymously without Set-Cookie under rate limit (status=${probe.status}, remaining=${probe.remaining})`,
      probe.retryAfterMs
    );
  }
}

describe("04. Security, RBAC, CSRF, session guards, token rotation & public SSR pages", () => {
  test("4a. Cross-buyer isolation: buyer A cannot read buyer B's order API or leak data via /don-hang/<id>", async () => {
    await ensureRateBudget(30, { label: "4a" });
    const clientB = new WebClient();
    const clientA = new WebClient();

    const [buyerB, buyerA] = await Promise.all([
      registerAndLoginBuyer(clientB, "owner-b"),
      registerAndLoginBuyer(clientA, "intruder-a"),
    ]);

    const secretTag = crypto.randomBytes(5).toString("hex");
    const secretRecipient = `KhachHangBiMat-${secretTag}`;
    const secretStreet = `999 Pho Bi Mat ${secretTag}`;

    const { address, province, ward } = await createBuyerAddress(clientB, buyerB, {
      recipientName: secretRecipient,
      streetAddress: secretStreet,
    });
    const picked = await pickSingleShopSku(clientB, { minStock: 2 });
    const quote = await calculateShippingQuote(clientB, {
      shopId: picked.product.shop_id,
      province,
      ward,
      weightGrams: picked.sku.weight_grams || 300,
      codAmountVnd: picked.sku.price_vnd,
    });

    const checkoutRes = await clientB.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": `web-rbac-${crypto.randomUUID()}` },
      body: {
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
      },
    });
    assert.ok(
      checkoutRes.status === 201 || checkoutRes.status === 200,
      `buyer B checkout failed (${checkoutRes.status}): ${checkoutRes.text}`
    );
    const orderB = checkoutRes.data?.orders?.[0];
    assert.ok(orderB?.id, `missing buyer B order id: ${checkoutRes.text}`);

    // Buyer A hitting order detail & timeline API must be rejected (403 or 404).
    const apiOrderRes = await clientA.json(
      "GET",
      `/api/gw/orders/${encodeURIComponent(orderB.id)}`
    );
    assert.ok(
      apiOrderRes.status === 403 || apiOrderRes.status === 404,
      `buyer A reading buyer B order expected 403/404, got ${apiOrderRes.status}: ${apiOrderRes.text}`
    );
    assert.ok(
      !apiOrderRes.text.includes(secretRecipient) && !apiOrderRes.text.includes(secretStreet),
      "order error response must not leak buyer B's address data"
    );

    const apiTimelineRes = await clientA.json(
      "GET",
      `/api/gw/orders/${encodeURIComponent(orderB.id)}/timeline`
    );
    assert.ok(
      apiTimelineRes.status === 403 || apiTimelineRes.status === 404,
      `buyer A reading buyer B timeline expected 403/404, got ${apiTimelineRes.status}: ${apiTimelineRes.text}`
    );

    if (checkoutRes.data?.payment_id) {
      const apiPaymentRes = await clientA.json(
        "GET",
        `/api/gw/payments/${encodeURIComponent(checkoutRes.data.payment_id)}`
      );
      assert.ok(
        apiPaymentRes.status === 403 || apiPaymentRes.status === 404,
        `buyer A reading buyer B payment expected 403/404, got ${apiPaymentRes.status}: ${apiPaymentRes.text}`
      );
    }

    // Buyer A loading page /don-hang/<id> of buyer B must not return 5xx or leak buyer B's order data.
    const pageRes = await clientA.page(`/don-hang/${encodeURIComponent(orderB.id)}`);
    assert.ok(
      pageRes.status === 200 || pageRes.status === 403 || pageRes.status === 404,
      `/don-hang/${orderB.id} with buyer A cookie expected 200/403/404, got ${pageRes.status}`
    );
    assert.ok(
      !pageRes.text.includes(secretRecipient),
      `/don-hang/${orderB.id} leaked buyer B recipient name to buyer A`
    );
    assert.ok(
      !pageRes.text.includes(secretStreet),
      `/don-hang/${orderB.id} leaked buyer B street address to buyer A`
    );
    assert.ok(
      !pageRes.text.includes(buyerB.phone),
      `/don-hang/${orderB.id} leaked buyer B phone to buyer A`
    );
    if (orderB.order_code) {
      assert.ok(
        !pageRes.text.includes(orderB.order_code),
        `/don-hang/${orderB.id} leaked buyer B order_code (${orderB.order_code}) to buyer A`
      );
    }
    assert.equal(buyerA.user?.role, "buyer");
  });

  test("4b. Unauthenticated write route (POST /api/gw/cart/items) returns 401", async () => {
    const anon = new WebClient();
    const picked = await pickSingleShopSku(anon, { minStock: 1 });

    const cartRes = await anon.json("POST", "/api/gw/cart/items", {
      body: { sku_id: picked.sku.id, quantity: 1 },
    });
    assert.equal(
      cartRes.status,
      401,
      `unauthenticated POST /api/gw/cart/items expected 401, got ${cartRes.status}: ${cartRes.text}`
    );
    assert.ok(cartRes.data?.error?.code, `expected sanitized error envelope: ${cartRes.text}`);

    const checkoutRes = await anon.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": `web-anon-${crypto.randomUUID()}` },
      body: { address_id: "00000000-0000-0000-0000-000000000000", from_cart: true, payment_method: "COD" },
    });
    assert.equal(
      checkoutRes.status,
      401,
      `unauthenticated POST /api/gw/checkout expected 401, got ${checkoutRes.status}: ${checkoutRes.text}`
    );
  });

  test("4c. CSRF Origin enforcement: foreign Origin -> 403, missing Origin -> 403", async () => {
    await ensureRateBudget(20, { label: "4c" });
    const client = new WebClient();
    const buyer = await registerAndLoginBuyer(client, "csrf");
    const picked = await pickSingleShopSku(client, { minStock: 1 });

    // Foreign Origin on /api/gw/* write -> 403.
    const foreignGwRes = await client.json("POST", "/api/gw/cart/items", {
      origin: "https://evil.example",
      body: { sku_id: picked.sku.id, quantity: 1 },
    });
    assert.equal(
      foreignGwRes.status,
      403,
      `foreign Origin POST /api/gw/cart/items expected 403, got ${foreignGwRes.status}: ${foreignGwRes.text}`
    );
    assert.equal(foreignGwRes.data?.error?.code, "CSRF_FORBIDDEN");

    // Missing Origin on /api/gw/* write -> 403.
    const missingGwRes = await client.json("POST", "/api/gw/cart/items", {
      origin: null,
      body: { sku_id: picked.sku.id, quantity: 1 },
    });
    assert.equal(
      missingGwRes.status,
      403,
      `missing Origin POST /api/gw/cart/items expected 403, got ${missingGwRes.status}: ${missingGwRes.text}`
    );
    assert.equal(missingGwRes.data?.error?.code, "CSRF_FORBIDDEN");

    // Foreign & missing Origin on /api/auth/logout -> 403 and session stays intact.
    const foreignAuthRes = await client.json("POST", "/api/auth/logout", {
      origin: "https://evil.example",
      body: {},
    });
    assert.equal(
      foreignAuthRes.status,
      403,
      `foreign Origin POST /api/auth/logout expected 403, got ${foreignAuthRes.status}: ${foreignAuthRes.text}`
    );

    const missingAuthRes = await client.json("POST", "/api/auth/logout", {
      origin: null,
      body: {},
    });
    assert.equal(
      missingAuthRes.status,
      403,
      `missing Origin POST /api/auth/logout expected 403, got ${missingAuthRes.status}: ${missingAuthRes.text}`
    );

    // Spoofed X-Forwarded-Host / X-Forwarded-Proto matching a foreign Origin must STILL be rejected (403).
    const spoofedLogoutRes = await client.json("POST", "/api/auth/logout", {
      origin: "https://evil.example",
      headers: {
        "X-Forwarded-Host": "evil.example",
        "X-Forwarded-Proto": "https",
      },
      body: {},
    });
    assert.equal(
      spoofedLogoutRes.status,
      403,
      `spoofed XFH/XFP POST /api/auth/logout expected 403, got ${spoofedLogoutRes.status}: ${spoofedLogoutRes.text}`
    );
    assert.equal(spoofedLogoutRes.data?.error?.code, "CSRF_FORBIDDEN");

    const spoofedGwRes = await client.json("POST", "/api/gw/cart/items", {
      origin: "https://evil.example",
      headers: {
        "X-Forwarded-Host": "evil.example",
        "X-Forwarded-Proto": "https",
      },
      body: { sku_id: picked.sku.id, quantity: 1 },
    });
    assert.equal(
      spoofedGwRes.status,
      403,
      `spoofed XFH/XFP POST /api/gw/cart/items expected 403, got ${spoofedGwRes.status}: ${spoofedGwRes.text}`
    );
    assert.equal(spoofedGwRes.data?.error?.code, "CSRF_FORBIDDEN");

    // Confirm CSRF-blocked logout did not terminate the buyer's session.
    const sessionRes = await client.json("GET", "/api/auth/session");
    assert.equal(sessionRes.status, 200);
    assert.equal(sessionRes.data?.user?.email, buyer.email);

    // BFF proxy must block direct /api/gw/auth/* and /api/gw/webhooks/* forwarding.
    const blockedAuthRes = await client.json("POST", "/api/gw/auth/login", {
      body: { email: buyer.email, password: buyer.password },
    });
    assert.equal(blockedAuthRes.status, 403, `expected 403 for /api/gw/auth/login, got ${blockedAuthRes.status}`);
    assert.equal(blockedAuthRes.data?.error?.code, "FORBIDDEN_PATH");

    const blockedWebhookRes = await client.json("POST", "/api/gw/webhooks/payment", {
      body: {},
    });
    assert.equal(
      blockedWebhookRes.status,
      403,
      `expected 403 for /api/gw/webhooks/payment, got ${blockedWebhookRes.status}`
    );
    assert.equal(blockedWebhookRes.data?.error?.code, "FORBIDDEN_PATH");
  });

  test("4d. Unauthenticated /tai-khoan (and protected routes) redirect 307 to /dang-nhap?next=...", async () => {
    const anon = new WebClient();
    for (const protectedPath of ["/tai-khoan", "/don-hang", "/thanh-toan", "/seller", "/admin"]) {
      const res = await anon.page(protectedPath);
      assert.equal(
        res.status,
        307,
        `unauthenticated GET ${protectedPath} expected 307, got ${res.status}`
      );
      assert.ok(res.location, `307 redirect for ${protectedPath} must include Location header`);
      const target = new URL(res.location, config.webUrl);
      assert.equal(
        target.pathname,
        "/dang-nhap",
        `expected redirect to /dang-nhap for ${protectedPath}, got ${res.location}`
      );
      assert.equal(
        target.searchParams.get("next"),
        protectedPath,
        `expected next=${protectedPath}, got ${res.location}`
      );
    }
  });

  test("4e. Authenticated buyer accessing /seller and /admin gets 404", async () => {
    const client = new WebClient();
    const buyer = await registerAndLoginBuyer(client, "rbac-role");

    const adminRes = await client.page("/admin");
    assert.equal(
      adminRes.status,
      404,
      `buyer GET /admin expected 404, got ${adminRes.status} (location=${adminRes.location})`
    );

    const sellerRes = await client.page("/seller");
    assert.equal(
      sellerRes.status,
      404,
      `buyer GET /seller expected 404, got ${sellerRes.status} (location=${sellerRes.location})`
    );

    // Visiting forbidden role pages must not corrupt or break the buyer's active session.
    const sessionRes = await client.json("GET", "/api/auth/session");
    assert.equal(sessionRes.status, 200);
    assert.equal(sessionRes.data?.user?.email, buyer.email);
  });

  test("4f. Product page /san-pham/<real-slug> returns 200 with product name", async () => {
    const anon = new WebClient();
    const products = await listCatalogProducts(anon);
    const product = products.find((p) => p?.slug && p?.name && !p.name.includes("&")) ?? products[0];
    assert.ok(product?.slug && product?.name, "seeded catalog product must have slug and name");

    const pageRes = await anon.page(`/san-pham/${encodeURIComponent(product.slug)}`);
    assert.equal(
      pageRes.status,
      200,
      `GET /san-pham/${product.slug} expected 200, got ${pageRes.status}`
    );
    assert.ok(
      pageRes.text.includes(product.name),
      `GET /san-pham/${product.slug} HTML did not contain product name "${product.name}"`
    );
  });

  test("4g. Refresh token rotation: grace window deduplicates staggered retry, gateway replay revokes family, and logout revokes immediately", async () => {
    // Steps 1-3 share a short (5s) post-rotation grace window; never retry a single step on 429.
    await runAtomicBlockWith429Retry(
      "4g-rotation-grace",
      async () => {
        const client = new WebClient();
        await registerAndLoginBuyer(client, "refresh-reuse");

        const oldRefresh = client.getCookie(REFRESH_COOKIE);
        const oldAccess = client.getCookie(ACCESS_COOKIE);
        assert.ok(oldRefresh, "login must issue refresh cookie");
        assert.ok(oldAccess, "login must issue access cookie");

        await ensureRateBudget(15, { label: "4g-grace-window" });

        // 1. Legitimate refresh rotates both cookies.
        const rotateRes = await client.json("POST", "/api/auth/refresh", { retry429: false });
        throwIfRateLimited(rotateRes, "4g rotate");
        assert.equal(
          rotateRes.status,
          200,
          `POST /api/auth/refresh expected 200, got ${rotateRes.status}: ${rotateRes.text}`
        );
        assert.equal(rotateRes.data?.ok, true);

        const rotatedRefresh = client.getCookie(REFRESH_COOKIE);
        assert.ok(rotatedRefresh, "refresh must set new refresh cookie");
        assert.notEqual(
          rotatedRefresh,
          oldRefresh,
          "refresh must rotate nm_refresh_token to a new value"
        );

        // 2. Staggered request arriving within the short BFF grace window with the just-rotated
        // token receives the same rotated pair without falsely killing the session.
        const graceRes = await client.json("POST", "/api/auth/refresh", {
          cookieOverride: `${REFRESH_COOKIE}=${oldRefresh}`,
          updateJar: false,
          retry429: false,
        });
        throwIfRateLimited(graceRes, "4g grace retry");
        assert.equal(
          graceRes.status,
          200,
          `staggered refresh within grace window expected 200, got ${graceRes.status}: ${graceRes.text}`
        );

        // 3. Replaying oldRefresh directly at the gateway MUST be rejected (401/403) and revoke
        // the entire refresh token family (verified via subsequent refresh with rotatedRefresh).
        const gwReplayRes = await fetch(`${config.gatewayUrl}/api/v1/auth/refresh`, {
          method: "POST",
          headers: { "Content-Type": "application/json", Accept: "application/json" },
          body: JSON.stringify({ refresh_token: oldRefresh }),
          signal: AbortSignal.timeout(config.requestTimeoutMs),
        });
        throwIfRateLimited(gwReplayRes, "4g gateway replay");
        assert.ok(
          gwReplayRes.status === 401 || gwReplayRes.status === 403,
          `gateway replay of old refresh token expected 401/403, got ${gwReplayRes.status}`
        );

        const afterReuseRes = await client.json("POST", "/api/auth/refresh", {
          cookieOverride: `${REFRESH_COOKIE}=${rotatedRefresh}`,
          updateJar: false,
          retry429: false,
        });
        throwIfRateLimited(afterReuseRes, "4g after reuse");
        assert.ok(
          afterReuseRes.status === 401 || afterReuseRes.status === 403,
          `refresh family should be revoked after replay, got ${afterReuseRes.status}: ${afterReuseRes.text}`
        );
      },
      { minBudget: 25, maxRetries: 2 }
    );

    // 4. Logout on a fresh session clears cookies, purges grace cache, and revokes immediately.
    await ensureRateBudget(15, { label: "4g-logout" });
    const logoutClient = new WebClient();
    await registerAndLoginBuyer(logoutClient, "logout");
    const preLogoutRefresh = logoutClient.getCookie(REFRESH_COOKIE);
    const logoutRes = await logoutClient.json("POST", "/api/auth/logout", { body: {} });
    assert.equal(logoutRes.status, 200, `logout expected 200, got ${logoutRes.status}: ${logoutRes.text}`);
    assert.equal(logoutClient.getCookie(ACCESS_COOKIE), undefined, "logout must clear access cookie");
    assert.equal(logoutClient.getCookie(REFRESH_COOKIE), undefined, "logout must clear refresh cookie");

    const postLogoutSession = await logoutClient.json("GET", "/api/auth/session");
    assert.equal(postLogoutSession.status, 200);
    assert.equal(postLogoutSession.data?.user, null, "session must be null after logout");

    const postLogoutRefresh = await logoutClient.json("POST", "/api/auth/refresh", {
      cookieOverride: `${REFRESH_COOKIE}=${preLogoutRefresh}`,
      updateJar: false,
    });
    assert.ok(
      postLogoutRefresh.status === 401 || postLogoutRefresh.status === 403,
      `refresh token revoked by logout must be rejected, got ${postLogoutRefresh.status}`
    );
  });

  test("4h. [B1 catalog] Nonexistent product slug /san-pham/<missing> returns 404", async () => {
    const anon = new WebClient();
    const res = await anon.page(`/san-pham/khong-ton-tai-${crypto.randomUUID()}`);
    assert.equal(res.status, 404, `nonexistent product slug expected 404, got ${res.status}`);
  });

  test("4i. [B1 account] /don-hang/<id> renders real order_code for owner and returns strict 404 for another buyer", async () => {
    await ensureRateBudget(30, { label: "4i" });
    const clientB = new WebClient();
    const clientA = new WebClient();
    const [buyerB] = await Promise.all([
      registerAndLoginBuyer(clientB, "ssr-owner"),
      registerAndLoginBuyer(clientA, "ssr-other"),
    ]);
    const { address, province, ward } = await createBuyerAddress(clientB, buyerB);
    const picked = await pickSingleShopSku(clientB, { minStock: 2 });
    const quote = await calculateShippingQuote(clientB, {
      shopId: picked.product.shop_id,
      province,
      ward,
      weightGrams: picked.sku.weight_grams || 300,
      codAmountVnd: picked.sku.price_vnd,
    });
    const checkoutRes = await clientB.json("POST", "/api/gw/checkout", {
      headers: { "Idempotency-Key": `web-ssr-order-${crypto.randomUUID()}` },
      body: {
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
      },
    });
    const orderB = checkoutRes.data?.orders?.[0];
    assert.ok(orderB?.id && orderB?.order_code);

    const ownerPage = await clientB.page(`/don-hang/${encodeURIComponent(orderB.id)}`);
    assert.equal(ownerPage.status, 200);
    assert.ok(ownerPage.text.includes(orderB.order_code));

    const intruderPage = await clientA.page(`/don-hang/${encodeURIComponent(orderB.id)}`);
    assert.equal(intruderPage.status, 404);
  });

  test("4j. Unified Node refresh state across middleware and /api/*: logout invalidates middleware grace cache, parallel middleware+API refresh deduplicates, and GET /api/auth/session is read-only", async () => {
    // (a) GET / with only R1 rotates & caches -> POST /api/auth/logout with (A1, R1) ->
    //     GET /tai-khoan with R1 within 5s must NOT return A2 (redirects 307 to /dang-nhap),
    //     and A2 must be rejected (401) at /api/gw/users/me.
    await runAtomicBlockWith429Retry(
      "4j-a-logout-invalidates-grace",
      async () => {
        const c1 = new WebClient();
        await registerAndLoginBuyer(c1, "unified-logout");
        const a1 = c1.getCookie(ACCESS_COOKIE);
        const r1 = c1.getCookie(REFRESH_COOKIE);
        assert.ok(a1 && r1);

        await ensureRateBudget(20, { label: "4j-a-atomic" });

        const mwRotate = await c1.page("/", {
          cookieOverride: `${REFRESH_COOKIE}=${r1}`,
          updateJar: false,
          retry429: false,
        });
        throwIfRateLimited(mwRotate, "4j(a) GET /");
        assert.equal(mwRotate.status, 200, `GET / with R1 expected 200, got ${mwRotate.status}`);
        const a2 = mwRotate.setCookies.find((c) => c.name === ACCESS_COOKIE)?.value;
        const r2 = mwRotate.setCookies.find((c) => c.name === REFRESH_COOKIE)?.value;
        if (!a2 || !r2) {
          await throwIfAnonymousRenderDueTo429("4j(a) GET /");
        }
        assert.ok(a2 && r2 && r2 !== r1, "middleware must rotate R1 into (A2, R2)");

        const logoutRes = await c1.json("POST", "/api/auth/logout", {
          cookieOverride: `${ACCESS_COOKIE}=${a1}; ${REFRESH_COOKIE}=${r1}`,
          updateJar: false,
          retry429: false,
          body: {},
        });
        throwIfRateLimited(logoutRes, "4j(a) logout");
        assert.equal(logoutRes.status, 200, `logout expected 200, got ${logoutRes.status}`);

        const postLogoutAccount = await c1.page("/tai-khoan", {
          cookieOverride: `${REFRESH_COOKIE}=${r1}`,
          updateJar: false,
          retry429: false,
        });
        throwIfRateLimited(postLogoutAccount, "4j(a) GET /tai-khoan");
        assert.equal(
          postLogoutAccount.status,
          307,
          `GET /tai-khoan with R1 after logout expected 307 redirect, got ${postLogoutAccount.status}`
        );
        assert.ok(
          postLogoutAccount.location?.includes("/dang-nhap"),
          `expected redirect to /dang-nhap, got ${postLogoutAccount.location}`
        );
        const leakedA2 = postLogoutAccount.setCookies.find((c) => c.name === ACCESS_COOKIE && c.value);
        assert.equal(leakedA2, undefined, "post-logout middleware must not re-issue cached A2");

        const a2UsersMe = await c1.json("GET", "/api/gw/users/me", {
          cookieOverride: `${ACCESS_COOKIE}=${a2}`,
          updateJar: false,
          retry429: false,
        });
        throwIfRateLimited(a2UsersMe, "4j(a) GET /api/gw/users/me");
        assert.equal(
          a2UsersMe.status,
          401,
          `cached A2 after logout expected 401 at /api/gw/users/me, got ${a2UsersMe.status}`
        );
      },
      { minBudget: 35, maxRetries: 2 }
    );

    // (b) Parallel GET / (middleware) + POST /api/auth/refresh (/api route) with the same R1
    //     must share single-flight state so gateway does not revoke the token family.
    //     Neither branch retries 429 independently; if any branch hits 429, retry the ENTIRE block with a fresh account.
    await runAtomicBlockWith429Retry(
      "4j-b-parallel-refresh",
      async () => {
        const c2 = new WebClient();
        await registerAndLoginBuyer(c2, "unified-parallel");
        const parR1 = c2.getCookie(REFRESH_COOKIE);
        assert.ok(parR1);

        await ensureRateBudget(25, { label: "4j-b-atomic" });

        const [parPage, parApi] = await Promise.all([
          c2.page("/", {
            cookieOverride: `${REFRESH_COOKIE}=${parR1}`,
            updateJar: false,
            retry429: false,
          }),
          c2.json("POST", "/api/auth/refresh", {
            cookieOverride: `${REFRESH_COOKIE}=${parR1}`,
            updateJar: false,
            retry429: false,
          }),
        ]);
        if (parPage.status === 429 || parApi.status === 429) {
          const rawRetry =
            parApi.headers.get("retry-after") || parPage.headers.get("retry-after");
          throw new RateLimitBlockError(
            `4j(b) parallel branch 429 (page=${parPage.status}, api=${parApi.status})`,
            rawRetry ? parseRetryAfterMs(rawRetry) : null
          );
        }

        assert.equal(parPage.status, 200, `parallel GET / expected 200, got ${parPage.status}`);
        assert.equal(parApi.status, 200, `parallel POST /api/auth/refresh expected 200, got ${parApi.status}`);
        const parR2Page = parPage.setCookies.find((c) => c.name === REFRESH_COOKIE)?.value;
        const parR2Api = parApi.setCookies.find((c) => c.name === REFRESH_COOKIE)?.value;
        if (!parR2Page) {
          await throwIfAnonymousRenderDueTo429("4j(b) parallel GET /");
        }
        assert.ok(parR2Page && parR2Api, "both parallel responses must set rotated refresh cookie");
        assert.equal(parR2Page, parR2Api, "parallel middleware & API refresh must return the exact same rotated token");

        const followUp = await c2.json("POST", "/api/auth/refresh", {
          cookieOverride: `${REFRESH_COOKIE}=${parR2Api}`,
          updateJar: false,
          retry429: false,
        });
        throwIfRateLimited(followUp, "4j(b) follow-up refresh");
        assert.equal(
          followUp.status,
          200,
          `subsequent refresh with R2 expected 200 (family must not be revoked), got ${followUp.status}: ${followUp.text}`
        );
      },
      { minBudget: 35, maxRetries: 2 }
    );

    // (c) GET /api/auth/session with only refresh cookie must be read-only (no rotation, no Set-Cookie).
    await runAtomicBlockWith429Retry(
      "4j-c-session-readonly",
      async () => {
        const c3 = new WebClient();
        await registerAndLoginBuyer(c3, "session-readonly");
        const roR1 = c3.getCookie(REFRESH_COOKIE);
        assert.ok(roR1);

        await ensureRateBudget(15, { label: "4j-c-atomic" });

        const sessionProbe = await c3.json("GET", "/api/auth/session", {
          cookieOverride: `${REFRESH_COOKIE}=${roR1}`,
          updateJar: false,
          retry429: false,
        });
        throwIfRateLimited(sessionProbe, "4j(c) GET /api/auth/session");
        assert.equal(sessionProbe.status, 200);
        assert.equal(sessionProbe.data?.user, null);
        assert.equal(sessionProbe.data?.can_refresh, true);
        assert.equal(
          sessionProbe.setCookies.length,
          0,
          `GET /api/auth/session must not rotate cookies, got ${sessionProbe.setCookies.length} Set-Cookie headers`
        );

        // Confirm roR1 was NOT rotated by GET /api/auth/session and still works on POST /api/auth/refresh.
        const postProbeRefresh = await c3.json("POST", "/api/auth/refresh", {
          cookieOverride: `${REFRESH_COOKIE}=${roR1}`,
          updateJar: false,
          retry429: false,
        });
        throwIfRateLimited(postProbeRefresh, "4j(c) POST /api/auth/refresh");
        assert.equal(
          postProbeRefresh.status,
          200,
          `POST /api/auth/refresh with unrotated R1 expected 200, got ${postProbeRefresh.status}`
        );
      },
      { minBudget: 20, maxRetries: 2 }
    );
  });
});
