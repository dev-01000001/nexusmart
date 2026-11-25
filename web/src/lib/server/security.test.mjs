// Run: node --test web/src/lib/server/security.test.mjs (Node 22 type stripping)
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  GatewayError,
  SAFE_MESSAGES,
  errorFromResponse,
  errorResponse,
  isShopBankAccountNotFoundError,
  logServer,
} from "./errors.ts";
import {
  assertSameOrigin,
  buildLoginRedirectUrl,
  isPrefetchRequest,
  readJson,
  resolveForwardedClientIp,
  resolveRequestHost,
  resolveRequestProto,
} from "./http.ts";
import {
  ACCESS_COOKIE,
  COOKIE_SECURE,
  REFRESH_COOKIE,
  TRUST_PROXY_HEADERS,
  baseCookie,
  buildChatWsUrl,
  buildSiteCspHeader,
  resolveChatWsOrigin,
} from "./config.ts";
import {
  extractTraceId,
  generateTraceparent,
  isValidTraceparent,
  redactLogFields,
  resolveTraceContext,
  scrubLogString,
} from "./trace.ts";
import {
  REFRESH_GRACE_MS,
  accessExpiresSoon,
  applyAuthCookiesToHeader,
  clearAuthCookies,
  invalidateRefreshCache,
  rotateRefreshToken,
  setAuthCookies,
} from "./tokens.ts";
import { toAddressItem, toGatewayAddressBody } from "./address.ts";
import { lookupPublishedSiteHost, parsePublishedSiteHost } from "./siteLookup.ts";
import {
  assertBuyerOrderInStorefront,
  assertInStorefront,
  assertPaymentInStorefront,
  belongsToStorefront,
  buildOrderLookupGatewayBody,
  buildSiteShippingQuotePayload,
  cartHasSku,
  executeSiteOrderLookup,
  hasPlatformPromoInput,
  isSiteShopVoucher,
  resolveSiteShopVoucherMap,
  scopeCartToShop,
  verifySiteRefundAccess,
  verifySiteReturnAccess,
  verifySiteShippingLabelAccess,
} from "./storefront.ts";

function makeReq(url, headers = {}, method = "POST") {
  return new Request(url, { method, headers });
}

test("assertSameOrigin accepts matching same-origin Origin header", () => {
  const req = makeReq("http://127.0.0.1:3201/api/auth/login", {
    host: "127.0.0.1:3201",
    origin: "http://127.0.0.1:3201",
    "sec-fetch-site": "same-origin",
  });
  assert.doesNotThrow(() => assertSameOrigin(req));

  const siteReq = makeReq("http://bach-dang.nexusmart.localhost:3300/api/cart/items", {
    host: "bach-dang.nexusmart.localhost:3300",
    origin: "http://bach-dang.nexusmart.localhost:3300",
    "sec-fetch-site": "same-origin",
  });
  assert.doesNotThrow(() => assertSameOrigin(siteReq));

  // Explicit default port (:80 on http) matches normalized Origin without port.
  const defaultPortReq = makeReq("http://bach-dang.nexusmart.localhost/api/checkout", {
    host: "bach-dang.nexusmart.localhost:80",
    origin: "http://bach-dang.nexusmart.localhost",
  });
  assert.doesNotThrow(() => assertSameOrigin(defaultPortReq));
});

test("assertSameOrigin rejects cross-site, root-to-site, site-to-root, and foreign Origin with 403 BAD_ORIGIN", () => {
  const cases = [
    // Foreign origin
    {
      url: "http://127.0.0.1:3201/api/auth/login",
      host: "127.0.0.1:3201",
      origin: "https://evil.example",
    },
    // Site B calling Site A
    {
      url: "http://bach-dang.nexusmart.localhost:3300/api/cart/items",
      host: "bach-dang.nexusmart.localhost:3300",
      origin: "http://sai-gon.nexusmart.localhost:3300",
    },
    // Root host calling Site A
    {
      url: "http://bach-dang.nexusmart.localhost:3300/api/checkout",
      host: "bach-dang.nexusmart.localhost:3300",
      origin: "http://nexusmart.localhost:3300",
    },
    // Site A calling Root host
    {
      url: "http://nexusmart.localhost:3300/api/seller/site/publish",
      host: "nexusmart.localhost:3300",
      origin: "http://bach-dang.nexusmart.localhost:3300",
    },
    // Port mismatch on same hostname
    {
      url: "http://bach-dang.nexusmart.localhost:3300/api/checkout",
      host: "bach-dang.nexusmart.localhost:3300",
      origin: "http://bach-dang.nexusmart.localhost:3400",
    },
  ];
  for (const c of cases) {
    const req = makeReq(c.url, { host: c.host, origin: c.origin });
    assert.throws(
      () => assertSameOrigin(req),
      (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN",
      `expected 403 BAD_ORIGIN for origin=${c.origin} -> host=${c.host}`
    );
  }
});

test("assertSameOrigin accepts same custom-domain origin and rejects subdomain<->custom CSRF with 403 BAD_ORIGIN", () => {
  // Same custom host passes in dev (with port)
  const sameCustomDev = makeReq("http://hanoi-knit.verified.test:3300/api/cart/items", {
    host: "hanoi-knit.verified.test:3300",
    origin: "http://hanoi-knit.verified.test:3300",
    "sec-fetch-site": "same-origin",
  });
  assert.doesNotThrow(() => assertSameOrigin(sameCustomDev));

  // Same custom host passes behind trusted HTTPS reverse proxy
  const sameCustomProd = makeReq("http://web:3000/api/checkout", {
    host: "web:3000",
    "x-forwarded-host": "shop.brand.vn",
    "x-forwarded-proto": "https",
    origin: "https://shop.brand.vn",
    "sec-fetch-site": "same-origin",
  });
  assert.doesNotThrow(() => assertSameOrigin(sameCustomProd, { trustProxyHeaders: true }));

  const crossHostCases = [
    // Subdomain -> custom domain CSRF
    {
      url: "http://hanoi-knit.verified.test:3300/api/cart/items",
      host: "hanoi-knit.verified.test:3300",
      origin: "http://hanoi-knit.nexusmart.localhost:3300",
    },
    // Custom domain -> subdomain CSRF
    {
      url: "http://hanoi-knit.nexusmart.localhost:3300/api/cart/items",
      host: "hanoi-knit.nexusmart.localhost:3300",
      origin: "http://hanoi-knit.verified.test:3300",
    },
    // Custom domain -> root seller/admin CSRF
    {
      url: "http://nexusmart.localhost:3300/api/seller/site/domains",
      host: "nexusmart.localhost:3300",
      origin: "http://hanoi-knit.verified.test:3300",
    },
    // Root -> custom domain CSRF
    {
      url: "http://hanoi-knit.verified.test:3300/api/checkout",
      host: "hanoi-knit.verified.test:3300",
      origin: "http://nexusmart.localhost:3300",
    },
    // Custom domain B -> custom domain A CSRF
    {
      url: "http://hanoi-knit.verified.test:3300/api/orders",
      host: "hanoi-knit.verified.test:3300",
      origin: "http://other-brand.verified.test:3300",
    },
  ];
  for (const c of crossHostCases) {
    const req = makeReq(c.url, { host: c.host, origin: c.origin });
    assert.throws(
      () => assertSameOrigin(req),
      (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN",
      `expected 403 BAD_ORIGIN for origin=${c.origin} -> host=${c.host}`
    );
  }
});

test("assertSameOrigin rejects Origin: null", () => {
  const req = makeReq("http://127.0.0.1:3201/api/auth/logout", {
    host: "127.0.0.1:3201",
    origin: "null",
  });
  assert.throws(
    () => assertSameOrigin(req),
    (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
  );
});

test("assertSameOrigin rejects cross-site and same-site Sec-Fetch-Site", () => {
  for (const sfs of ["cross-site", "same-site"]) {
    const req = makeReq("http://127.0.0.1:3201/api/auth/login", {
      host: "127.0.0.1:3201",
      origin: "http://127.0.0.1:3201",
      "sec-fetch-site": sfs,
    });
    assert.throws(
      () => assertSameOrigin(req),
      (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
    );
  }
});

test("assertSameOrigin rejects missing Origin and Referer", () => {
  const req = makeReq("http://127.0.0.1:3201/api/auth/login", {
    host: "127.0.0.1:3201",
  });
  assert.throws(
    () => assertSameOrigin(req),
    (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
  );
});

test("assertSameOrigin rejects protocol mismatch between Origin and request", () => {
  const req = makeReq("http://127.0.0.1:3201/api/auth/login", {
    host: "127.0.0.1:3201",
    origin: "https://127.0.0.1:3201",
  });
  assert.throws(
    () => assertSameOrigin(req),
    (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
  );
});

test("assertSameOrigin accepts same-origin Referer only when Origin header is absent", () => {
  const okReq = makeReq("http://bach-dang.nexusmart.localhost:3300/api/auth/logout", {
    host: "bach-dang.nexusmart.localhost:3300",
    referer: "http://bach-dang.nexusmart.localhost:3300/tai-khoan",
  });
  assert.doesNotThrow(() => assertSameOrigin(okReq));

  const badReq = makeReq("http://bach-dang.nexusmart.localhost:3300/api/auth/logout", {
    host: "bach-dang.nexusmart.localhost:3300",
    referer: "http://sai-gon.nexusmart.localhost:3300/tai-khoan",
  });
  assert.throws(
    () => assertSameOrigin(badReq),
    (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
  );
});

test("errorFromResponse and errorResponse never leak gateway internals or trace_id", async () => {
  const fakeRes = new Response(
    JSON.stringify({
      error: {
        code: "INTERNAL_DB_FAILURE",
        message: "pq: password authentication failed at 10.0.0.5:5432",
        trace_id: "abc-trace-secret",
      },
    }),
    { status: 500, headers: { "Content-Type": "application/json" } }
  );
  const gwErr = await errorFromResponse(fakeRes);
  assert.equal(gwErr.status, 503);
  assert.equal(gwErr.code, "UNAVAILABLE");

  const clientRes = errorResponse(gwErr);
  assert.equal(clientRes.status, 503);
  assert.equal(clientRes.headers.get("cache-control"), "no-store");
  const body = await clientRes.json();
  assert.deepEqual(body, {
    error: { code: "UNAVAILABLE", message: SAFE_MESSAGES.unavailable },
  });
  assert.equal(JSON.stringify(body).includes("pq:"), false);
  assert.equal(JSON.stringify(body).includes("trace"), false);
});

test("cookie configuration is HttpOnly, SameSite=Lax, host-only (no Domain)", () => {
  assert.equal(baseCookie.httpOnly, true);
  assert.equal(baseCookie.sameSite, "lax");
  assert.equal(baseCookie.path, "/");
  assert.equal("domain" in baseCookie, false);
  assert.equal(baseCookie.secure, COOKIE_SECURE);

  const written = [];
  const jar = { set: (name, value, options) => written.push({ name, value, options }) };
  setAuthCookies(jar, { accessToken: "at-1", refreshToken: "rt-1", expiresIn: 900 });
  assert.equal(written.length, 2);
  assert.equal(written[0].name, ACCESS_COOKIE);
  assert.equal(written[0].options.httpOnly, true);
  assert.equal(written[0].options.maxAge, 900);
  assert.equal("domain" in written[0].options, false);
  assert.equal(written[1].name, REFRESH_COOKIE);
  assert.equal(written[1].options.httpOnly, true);
  assert.equal("domain" in written[1].options, false);

  written.length = 0;
  clearAuthCookies(jar);
  assert.equal(written[0].options.maxAge, 0);
  assert.equal(written[1].options.maxAge, 0);
  assert.equal("domain" in written[0].options, false);
});

test("accessExpiresSoon detects missing, malformed, expired, and valid JWTs", () => {
  const mkJwt = (exp) => {
    const h = Buffer.from(JSON.stringify({ alg: "HS256" })).toString("base64url");
    const p = Buffer.from(JSON.stringify({ exp })).toString("base64url");
    return `${h}.${p}.sig`;
  };
  const now = Math.floor(Date.now() / 1000);
  assert.equal(accessExpiresSoon(undefined), true);
  assert.equal(accessExpiresSoon("not-a-jwt"), true);
  assert.equal(accessExpiresSoon(mkJwt(now + 10)), true);
  assert.equal(accessExpiresSoon(mkJwt(now + 600)), false);
});

test("applyAuthCookiesToHeader replaces or removes auth cookies while preserving others", () => {
  const orig = `theme=light; ${ACCESS_COOKIE}=old_at; other=1; ${REFRESH_COOKIE}=old_rt`;
  const replaced = applyAuthCookiesToHeader(orig, {
    accessToken: "new_at",
    refreshToken: "new_rt",
    expiresIn: 900,
  });
  assert.equal(replaced, `theme=light; other=1; ${ACCESS_COOKIE}=new_at; ${REFRESH_COOKIE}=new_rt`);

  const cleared = applyAuthCookiesToHeader(orig, null);
  assert.equal(cleared, "theme=light; other=1");
});

test("toGatewayAddressBody normalizes camelCase/snake_case and never includes user_id", () => {
  const payload = toGatewayAddressBody({
    recipientName: "  Nguyễn An ",
    recipientPhone: "0909123456",
    provinceCode: "01",
    provinceName: "Hà Nội",
    wardCode: "00001",
    wardName: "Phường Phúc Xá",
    streetAddress: "12 Phố Huế",
    isDefault: true,
  });
  assert.equal(payload.recipient_name, "Nguyễn An");
  assert.equal(payload.ward_code, "00001");
  assert.equal(payload.is_default, true);
  assert.equal("user_id" in payload, false);

  const item = toAddressItem({
    id: "addr-1",
    user_id: "u-1",
    recipient_name: "Nguyễn An",
    recipient_phone: "0909123456",
    province_code: "01",
    province_name: "Hà Nội",
    ward_code: "00001",
    ward_name: "Phường Phúc Xá",
    street_address: "12 Phố Huế",
    is_default: true,
  });
  assert.equal(item.recipientName, "Nguyễn An");
  assert.equal(item.isDefault, true);
});

test("TRUST_PROXY_HEADERS defaults to false and ignores spoofed x-forwarded-host/proto in CSRF checks", () => {
  assert.equal(TRUST_PROXY_HEADERS, false);

  const spoofedHostReq = makeReq("http://localhost:3300/api/checkout", {
    host: "localhost:3300",
    "x-forwarded-host": "evil.example",
    "x-forwarded-proto": "https",
    origin: "https://evil.example",
  });
  assert.throws(
    () => assertSameOrigin(spoofedHostReq),
    (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
  );

  const spoofedProtoReq = makeReq("http://localhost:3300/api/checkout", {
    host: "localhost:3300",
    "x-forwarded-proto": "https",
    origin: "https://localhost:3300",
  });
  assert.throws(
    () => assertSameOrigin(spoofedProtoReq),
    (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
  );

  // Legitimate request matching Host + http still succeeds even if attacker injects x-forwarded-*.
  const legitReqWithSpoofedHeaders = makeReq("http://localhost:3300/api/checkout", {
    host: "localhost:3300",
    "x-forwarded-host": "evil.example",
    "x-forwarded-proto": "https",
    origin: "http://localhost:3300",
  });
  assert.doesNotThrow(() => assertSameOrigin(legitReqWithSpoofedHeaders));
});

test("assertSameOrigin honors x-forwarded-host/proto ONLY when TRUST_PROXY_HEADERS=true", () => {
  const prev = process.env.TRUST_PROXY_HEADERS;
  try {
    process.env.TRUST_PROXY_HEADERS = "true";

    const proxiedReq = makeReq("http://web:3000/api/checkout", {
      host: "web:3000",
      "x-forwarded-host": "bach-dang.nexusmart.vn, internal-lb",
      "x-forwarded-proto": "https, http",
      origin: "https://bach-dang.nexusmart.vn",
    });
    assert.doesNotThrow(() => assertSameOrigin(proxiedReq));

    // When proxy headers are trusted, the internal container origin no longer matches.
    const internalOriginReq = makeReq("http://web:3000/api/checkout", {
      host: "web:3000",
      "x-forwarded-host": "bach-dang.nexusmart.vn",
      "x-forwarded-proto": "https",
      origin: "http://web:3000",
    });
    assert.throws(
      () => assertSameOrigin(internalOriginReq),
      (err) => err instanceof GatewayError && err.status === 403 && err.code === "BAD_ORIGIN"
    );
  } finally {
    if (prev === undefined) delete process.env.TRUST_PROXY_HEADERS;
    else process.env.TRUST_PROXY_HEADERS = prev;
  }
});

test("buildLoginRedirectUrl keeps site host and honors TRUST_PROXY_HEADERS only when enabled", () => {
  const prev = process.env.TRUST_PROXY_HEADERS;
  try {
    delete process.env.TRUST_PROXY_HEADERS;
    const req = makeReq(
      "http://bach-dang.nexusmart.localhost:3300/thanh-toan/cho/pay-1?tab=qr",
      {
        host: "bach-dang.nexusmart.localhost:3300",
        "x-forwarded-host": "evil.example",
        "x-forwarded-proto": "https",
        "x-forwarded-for": "203.0.113.9, 10.0.0.1",
        "x-real-ip": "203.0.113.10",
      },
      "GET"
    );

    assert.equal(resolveRequestHost(req.headers, "fallback"), "bach-dang.nexusmart.localhost:3300");
    assert.equal(resolveRequestProto(req.headers, req.url), "http");
    assert.equal(resolveForwardedClientIp(req.headers), null);
    assert.equal(
      buildLoginRedirectUrl(req).toString(),
      "http://bach-dang.nexusmart.localhost:3300/dang-nhap?next=%2Fthanh-toan%2Fcho%2Fpay-1%3Ftab%3Dqr"
    );

    process.env.TRUST_PROXY_HEADERS = "true";
    assert.equal(resolveRequestHost(req.headers, "fallback"), "evil.example");
    assert.equal(resolveRequestProto(req.headers, req.url), "https");
    assert.equal(resolveForwardedClientIp(req.headers), "203.0.113.9");
    assert.equal(
      buildLoginRedirectUrl(req).toString(),
      "https://evil.example/dang-nhap?next=%2Fthanh-toan%2Fcho%2Fpay-1%3Ftab%3Dqr"
    );

    // Invalid x-forwarded-proto is rejected even when TRUST_PROXY_HEADERS=true.
    const badProtoHeaders = new Headers({
      host: "localhost:3300",
      "x-forwarded-proto": "javascript",
    });
    assert.equal(resolveRequestProto(badProtoHeaders, "http://localhost:3300/tai-khoan"), "http");
  } finally {
    if (prev === undefined) delete process.env.TRUST_PROXY_HEADERS;
    else process.env.TRUST_PROXY_HEADERS = prev;
  }
});

test("parsePublishedSiteHost accepts PUBLISHED + ACTIVE sites and rejects DRAFT/SUSPENDED/inactive", () => {
  const ok = parsePublishedSiteHost(
    {
      site: {
        id: "site-1",
        shop_id: "shop-a",
        status: "PUBLISHED",
        primary_host: "Bach-Dang.NexusMart.Localhost",
      },
      shop: { status: "ACTIVE" },
    },
    "bach-dang.nexusmart.localhost"
  );
  assert.deepEqual(ok, {
    status: "found",
    siteId: "site-1",
    shopId: "shop-a",
    primaryHost: "bach-dang.nexusmart.localhost",
  });

  for (const bad of [
    null,
    { site: { id: "s1", shop_id: "shop-a", status: "DRAFT" }, shop: { status: "ACTIVE" } },
    { site: { id: "s1", shop_id: "shop-a", status: "SUSPENDED" }, shop: { status: "ACTIVE" } },
    { site: { id: "s1", shop_id: "shop-a", status: "PUBLISHED" }, shop: { status: "SUSPENDED" } },
    { site: { id: "", shop_id: "shop-a", status: "PUBLISHED" }, shop: { status: "ACTIVE" } },
  ]) {
    assert.deepEqual(parsePublishedSiteHost(bad, "bach-dang.nexusmart.localhost"), {
      status: "not_found",
    });
  }
});

test("storefront tenant scoping isolates cart, orders, payments, and vouchers to the site's shop", () => {
  const cart = {
    user_id: "u-1",
    shop_groups: [
      {
        shop_id: "shop-a",
        shop_name: "Shop A",
        subtotal_vnd: 200000,
        total_weight_grams: 400,
        items: [
          {
            sku_id: "sku-a1",
            shop_id: "shop-a",
            price_vnd: 100000,
            quantity: 2,
            weight_grams: 200,
          },
        ],
      },
      {
        shop_id: "shop-b",
        shop_name: "Shop B",
        subtotal_vnd: 350000,
        total_weight_grams: 500,
        items: [
          {
            sku_id: "sku-b1",
            shop_id: "shop-b",
            price_vnd: 350000,
            quantity: 1,
            weight_grams: 500,
          },
        ],
      },
    ],
    total_items: 3,
    total_vnd: 550000,
  };
  const scoped = scopeCartToShop(cart, "shop-a");
  assert.equal(scoped.shop_groups.length, 1);
  assert.equal(scoped.total_items, 2);
  assert.equal(scoped.total_vnd, 200000);
  assert.equal(cartHasSku(scoped, "sku-a1"), true);
  assert.equal(cartHasSku(scoped, "sku-b1"), false);

  assert.equal(
    belongsToStorefront({ sales_channel: "STOREFRONT", storefront_shop_id: "shop-a" }, "shop-a"),
    true
  );
  assert.equal(
    belongsToStorefront({ sales_channel: "STOREFRONT", storefront_shop_id: "shop-b" }, "shop-a"),
    false
  );
  assert.equal(
    belongsToStorefront({ sales_channel: "OTHER", storefront_shop_id: "shop-a" }, "shop-a"),
    false
  );

  // Cross-site order/payment throws 404 (not 403)
  assert.throws(
    () =>
      assertInStorefront({ sales_channel: "STOREFRONT", storefront_shop_id: "shop-b" }, "shop-a"),
    (err) => err instanceof GatewayError && err.status === 404
  );
  assert.throws(
    () =>
      assertPaymentInStorefront(
        {
          payment_id: "p1",
          user_id: "u-other",
          sales_channel: "STOREFRONT",
          storefront_shop_id: "shop-a",
        },
        "shop-a",
        "u-1"
      ),
    (err) => err instanceof GatewayError && err.status === 404
  );
  assert.equal(
    assertPaymentInStorefront(
      {
        payment_id: "p1",
        user_id: "u-1",
        sales_channel: "STOREFRONT",
        storefront_shop_id: "shop-a",
      },
      "shop-a",
      "u-1"
    ).payment_id,
    "p1"
  );

  assert.equal(isSiteShopVoucher({ scope: "SHOP", shop_id: "shop-a" }, "shop-a"), true);
  assert.equal(isSiteShopVoucher({ scope: "SHOP", shop_id: "shop-b" }, "shop-a"), false);
  assert.equal(isSiteShopVoucher({ scope: "PLATFORM", shop_id: "" }, "shop-a"), false);
  assert.equal(isSiteShopVoucher({ scope: "FREESHIP", shop_id: "" }, "shop-a"), false);

  assert.equal(hasPlatformPromoInput({ platform_voucher_id: "v-plat" }), true);
  assert.equal(hasPlatformPromoInput({ freeship_voucher_id: "v-free" }), true);
  assert.equal(hasPlatformPromoInput({ coins_to_use: 1000 }), true);
  assert.equal(
    hasPlatformPromoInput({ platform_voucher_id: "", freeship_voucher_id: "", coins_to_use: 0 }),
    false
  );

  assert.deepEqual(resolveSiteShopVoucherMap({ shop_voucher_id: "v-a" }, "shop-a"), {
    "shop-a": "v-a",
  });
  assert.deepEqual(resolveSiteShopVoucherMap({ shop_voucher_ids: { "shop-a": "v-a" } }, "shop-a"), {
    "shop-a": "v-a",
  });
  assert.throws(
    () => resolveSiteShopVoucherMap({ shop_voucher_ids: { "shop-b": "v-b" } }, "shop-a"),
    (err) =>
      err instanceof GatewayError && err.status === 400 && err.code === "FOREIGN_SHOP_VOUCHER"
  );
});

test("buildChatWsUrl converts gateway origin to ws(s) and attaches short-lived ticket", () => {
  const url = buildChatWsUrl("tk_abc123");
  assert.match(url, /^wss?:\/\/.+\/api\/v1\/chat\/ws\?ticket=tk_abc123$/);
});

test("W3C traceparent resolution and log PII/secret redaction protect telemetry and secrets", () => {
  const generated = generateTraceparent();
  assert.equal(isValidTraceparent(generated), true);
  assert.equal(extractTraceId(generated).length, 32);
  assert.equal(
    isValidTraceparent("00-00000000000000000000000000000000-1111111111111111-01"),
    false
  );

  const incoming = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01";
  const ctx = resolveTraceContext(new Headers({ traceparent: incoming, tracestate: "vendor=1" }));
  assert.equal(ctx.traceparent, incoming);
  assert.equal(ctx.traceId, "4bf92f3577b34da6a3ce929d0e0e4736");
  assert.equal(ctx.tracestate, "vendor=1");

  const scrubbed = scrubLogString(
    "Failed Bearer secret.token user=buyer@example.com phone=0909123456 alt=+84 912 345 678 dot=0987.654.321 postgres://app:SuperPass@db:5432/nm"
  );
  assert.equal(scrubbed.includes("secret.token"), false);
  assert.equal(scrubbed.includes("buyer@example.com"), false);
  assert.equal(scrubbed.includes("0909123456"), false);
  assert.equal(scrubbed.includes("912 345 678"), false);
  assert.equal(scrubbed.includes("0987.654.321"), false);
  assert.equal(scrubbed.includes("SuperPass"), false);

  const redacted = redactLogFields({
    trace_id: "4bf92f3577b34da6a3ce929d0e0e4736",
    email: "buyer@example.com",
    access_token: "abc",
    nested: { recipient_name: "Nguyen An", note: "ok" },
  });
  assert.equal(redacted.trace_id, "4bf92f3577b34da6a3ce929d0e0e4736");
  assert.equal(redacted.email, "[REDACTED]");
  assert.equal(redacted.access_token, "[REDACTED]");
  assert.deepEqual(redacted.nested, { recipient_name: "[REDACTED]", note: "ok" });
});

test("logServer emits JSON with RFC3339 time, trace_id, and redacted PII/secrets in msg and fields", () => {
  const captured = [];
  const origLog = console.log;
  const origErr = console.error;
  console.log = (line) => captured.push(line);
  console.error = (line) => captured.push(line);
  try {
    logServer("warn", "Failed for buyer@example.com phone +84 909 123 456 Bearer top.secret.tok", {
      trace_id: "4bf92f3577b34da6a3ce929d0e0e4736",
      authorization: "Bearer raw-auth",
      phone: "0909123456",
      detail: "jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1MSJ9.sig1234567890",
    });
    assert.equal(captured.length, 1);
    const parsed = JSON.parse(captured[0]);
    assert.equal(parsed.level, "warn");
    assert.equal(parsed.service, "web-bff");
    assert.equal(parsed.trace_id, "4bf92f3577b34da6a3ce929d0e0e4736");
    assert.match(parsed.time, /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/);
    assert.equal(parsed.msg.includes("buyer@example.com"), false);
    assert.equal(parsed.msg.includes("909 123 456"), false);
    assert.equal(parsed.msg.includes("top.secret.tok"), false);
    assert.equal(parsed.authorization, "[REDACTED]");
    assert.equal(parsed.phone, "[REDACTED]");
    assert.equal(parsed.detail.includes("eyJhbGci"), false);

    // errorResponse logs fallback trace_id without leaking it to the browser response.
    captured.length = 0;
    const res = errorResponse(new Error("boom"), {}, "4bf92f3577b34da6a3ce929d0e0e4736");
    assert.equal(res.status, 503);
    assert.equal(captured.length, 1);
    const errLog = JSON.parse(captured[0]);
    assert.equal(errLog.trace_id, "4bf92f3577b34da6a3ce929d0e0e4736");
    assert.equal(errLog.msg, "bff_unhandled_error");
  } finally {
    console.log = origLog;
    console.error = origErr;
  }
});

test("callRefresh and lookupPublishedSiteHost propagate or generate W3C traceparent headers", async () => {
  const seen = [];
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (url, init) => {
    seen.push({ url: String(url), headers: init?.headers ?? {} });
    if (String(url).includes("/api/v1/auth/refresh")) {
      return new Response(
        JSON.stringify({
          access_token: "new-at",
          refresh_token: "new-rt",
          expires_in_seconds: 900,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      );
    }
    return new Response(
      JSON.stringify({
        site: {
          id: "site-1",
          shop_id: "shop-a",
          status: "PUBLISHED",
          primary_host: "shop.nexusmart.localhost",
        },
        shop: { status: "ACTIVE" },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } }
    );
  };
  try {
    const incoming = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01";
    const pair = await rotateRefreshToken("rt-unique-trace-test-1", {
      traceparent: incoming,
      tracestate: "congo=t61rcWkgMzE",
    });
    assert.equal(pair?.accessToken, "new-at");
    assert.equal(seen[0].headers.traceparent, incoming);
    assert.equal(seen[0].headers.tracestate, "congo=t61rcWkgMzE");

    const siteWithTrace = await lookupPublishedSiteHost("shop.nexusmart.localhost", {
      traceparent: incoming,
      tracestate: "vendor=1",
    });
    assert.equal(siteWithTrace.status, "found");
    assert.equal(seen[1].headers.traceparent, incoming);
    assert.equal(seen[1].headers.tracestate, "vendor=1");

    const siteAutoTrace = await lookupPublishedSiteHost("shop.nexusmart.localhost");
    assert.equal(siteAutoTrace.status, "found");
    assert.equal(isValidTraceparent(seen[2].headers.traceparent), true);
  } finally {
    globalThis.fetch = origFetch;
  }
});

test("readJson enforces 2 MiB body cap with 413 PAYLOAD_TOO_LARGE", async () => {
  const bigBody = JSON.stringify({ pad: "x".repeat(2 * 1024 * 1024 + 64) });
  const req = new Request("http://localhost:3300/api/auth/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: bigBody,
  });
  await assert.rejects(
    () => readJson(req),
    (err) => err instanceof GatewayError && err.status === 413 && err.code === "PAYLOAD_TOO_LARGE"
  );
});

test("errorFromResponse preserves Retry-After on 429 and whitelisted quote on 409 ADDRESS_FEE_CHANGED", async () => {
  const res429 = new Response(
    JSON.stringify({ error: { code: "RATE_LIMITED", message: "too many requests" } }),
    {
      status: 429,
      headers: { "Content-Type": "application/json", "Retry-After": "42" },
    }
  );
  const err429 = await errorFromResponse(res429);
  assert.equal(err429.status, 429);
  assert.equal(err429.retryAfter, "42");
  const client429 = errorResponse(err429);
  assert.equal(client429.status, 429);
  assert.equal(client429.headers.get("retry-after"), "42");

  const res409 = new Response(
    JSON.stringify({
      error: { code: "ADDRESS_FEE_CHANGED", message: "raw internal", details: { secret: "x" } },
      quote: {
        old_shipping_fee_vnd: 25000,
        new_shipping_fee_vnd: 35000,
        fee_delta_vnd: 10000,
        new_total_payable_vnd: 235000,
      },
    }),
    { status: 409, headers: { "Content-Type": "application/json" } }
  );
  const err409 = await errorFromResponse(res409);
  const body409 = await errorResponse(err409).json();
  assert.deepEqual(body409.quote, {
    old_shipping_fee_vnd: 25000,
    new_shipping_fee_vnd: 35000,
    fee_delta_vnd: 10000,
    new_total_payable_vnd: 235000,
  });
  assert.equal(JSON.stringify(body409).includes("secret"), false);
});

test("isPrefetchRequest detects browser and Next.js prefetch headers", () => {
  assert.equal(isPrefetchRequest(new Headers({ purpose: "prefetch" })), true);
  assert.equal(isPrefetchRequest(new Headers({ "sec-purpose": "prefetch;prerender" })), true);
  assert.equal(isPrefetchRequest(new Headers({ "next-router-prefetch": "1" })), true);
  assert.equal(isPrefetchRequest(new Headers({ "x-nextjs-data": "1" })), true);
  assert.equal(isPrefetchRequest(new Headers({ accept: "text/html" })), false);
});

test("invalidateRefreshCache evicts cached rotation results so logged-out token cannot be reused", async () => {
  let calls = 0;
  const origFetch = globalThis.fetch;
  globalThis.fetch = async () => {
    calls += 1;
    return new Response(
      JSON.stringify({
        access_token: `at-${calls}`,
        refresh_token: `rt-${calls}`,
        expires_in_seconds: 900,
      }),
      { status: 200, headers: { "Content-Type": "application/json" } }
    );
  };
  try {
    const token = `rt-cache-invalidation-test-${Date.now()}`;
    const first = await rotateRefreshToken(token);
    assert.equal(first?.accessToken, "at-1");
    assert.equal(calls, 1);

    // Cached second call does not hit fetch again
    const cached = await rotateRefreshToken(token);
    assert.equal(cached?.accessToken, "at-1");
    assert.equal(calls, 1);

    // After logout invalidation, logged-out token is purged and permanently blocked from reuse
    const purged = await invalidateRefreshCache(token);
    assert.equal(purged.purgedTokens?.accessToken, "at-1");
    const afterLogout = await rotateRefreshToken(token);
    assert.equal(afterLogout, null);
    assert.equal(calls, 1);

    // A fresh, distinct refresh token still rotates cleanly
    const freshToken = `rt-cache-fresh-test-${Date.now()}`;
    const freshResult = await rotateRefreshToken(freshToken);
    assert.equal(freshResult?.accessToken, "at-2");
    assert.equal(calls, 2);
  } finally {
    globalThis.fetch = origFetch;
  }
});

test("scopeCartToShop excludes unavailable items from payable subtotal and item count", () => {
  const cart = {
    user_id: "u-1",
    shop_groups: [
      {
        shop_id: "shop-a",
        shop_name: "Shop A",
        subtotal_vnd: 500000,
        total_weight_grams: 600,
        items: [
          {
            sku_id: "sku-ok",
            shop_id: "shop-a",
            price_vnd: 200000,
            quantity: 1,
            weight_grams: 200,
          },
          {
            sku_id: "sku-dead",
            shop_id: "shop-a",
            price_vnd: 300000,
            quantity: 1,
            weight_grams: 400,
            unavailable: true,
            unavailable_reason: "Ngừng kinh doanh",
          },
        ],
      },
    ],
    total_items: 2,
    total_vnd: 500000,
  };
  const scoped = scopeCartToShop(cart, "shop-a");
  assert.equal(scoped.shop_groups[0].items.length, 2);
  assert.equal(scoped.shop_groups[0].subtotal_vnd, 200000);
  assert.equal(scoped.total_items, 1);
  assert.equal(scoped.total_vnd, 200000);
});

test("W1: verifySiteReturnAccess derives order_id from gateway GET /returns/{id} and enforces shop + buyer ownership with 404", async () => {
  const buyerSession = { user: { id: "buyer-1", role: "buyer" } };
  const adminSession = { user: { id: "staff-1", role: "admin" } };
  const siteAOrder = {
    id: "ord-a1",
    buyer_id: "buyer-1",
    sales_channel: "STOREFRONT",
    storefront_shop_id: "shop-a",
  };

  // 1. Happy path: server derives order_id from GET /returns/{id}, verifies shop-a + buyer-1.
  const calls = [];
  const ok = await verifySiteReturnAccess({
    returnId: "ret-1",
    shopId: "shop-a",
    session: buyerSession,
    fetchReturn: async (rid) => {
      calls.push(`return:${rid}`);
      return { return_request: { id: rid, order_id: "ord-a1", shop_id: "shop-a" } };
    },
    fetchOrder: async (oid) => {
      calls.push(`order:${oid}`);
      return { order: siteAOrder };
    },
  });
  assert.deepEqual(calls, ["return:ret-1", "order:ord-a1"]);
  assert.equal(ok.order.id, "ord-a1");

  // 2. Admin can access matching shop-a order without buyer_id match.
  const okAdmin = await verifySiteReturnAccess({
    returnId: "ret-1",
    shopId: "shop-a",
    session: adminSession,
    fetchReturn: async () => ({ return_request: { id: "ret-1", order_id: "ord-a1" } }),
    fetchOrder: async () => ({ order: siteAOrder }),
  });
  assert.equal(okAdmin.order.id, "ord-a1");

  // 3. Missing return_request or missing order_id -> 404 (not 403).
  for (const retPayload of [
    null,
    { return_request: null },
    { return_request: { id: "ret-1", order_id: "" } },
    { return_request: { id: "ret-other", order_id: "ord-a1" } },
  ]) {
    await assert.rejects(
      () =>
        verifySiteReturnAccess({
          returnId: "ret-1",
          shopId: "shop-a",
          session: buyerSession,
          fetchReturn: async () => retPayload,
          fetchOrder: async () => ({ order: siteAOrder }),
        }),
      (err) => err instanceof GatewayError && err.status === 404
    );
  }

  // 4. Upstream 403 on GET /returns/{id} or GET /orders/{id} -> 404 (never 403).
  await assert.rejects(
    () =>
      verifySiteReturnAccess({
        returnId: "ret-secret",
        shopId: "shop-a",
        session: buyerSession,
        fetchReturn: async () => {
          throw new GatewayError("forbidden");
        },
        fetchOrder: async () => ({ order: siteAOrder }),
      }),
    (err) => err instanceof GatewayError && err.status === 404
  );
  await assert.rejects(
    () =>
      verifySiteReturnAccess({
        returnId: "ret-1",
        shopId: "shop-a",
        session: buyerSession,
        fetchReturn: async () => ({ return_request: { id: "ret-1", order_id: "ord-secret" } }),
        fetchOrder: async () => {
          throw new GatewayError("forbidden");
        },
      }),
    (err) => err instanceof GatewayError && err.status === 404
  );

  // 5. Return whose order belongs to another shop (shop-b) -> 404 (never 403).
  await assert.rejects(
    () =>
      verifySiteReturnAccess({
        returnId: "ret-b1",
        shopId: "shop-a",
        session: buyerSession,
        fetchReturn: async () => ({ return_request: { id: "ret-b1", order_id: "ord-b1" } }),
        fetchOrder: async () => ({
          order: {
            id: "ord-b1",
            buyer_id: "buyer-1",
            sales_channel: "STOREFRONT",
            storefront_shop_id: "shop-b",
          },
        }),
      }),
    (err) => err instanceof GatewayError && err.status === 404
  );

  // 6. Return whose order belongs to another buyer on shop-a -> 404 (never 403).
  await assert.rejects(
    () =>
      verifySiteReturnAccess({
        returnId: "ret-other-buyer",
        shopId: "shop-a",
        session: buyerSession,
        fetchReturn: async () => ({
          return_request: { id: "ret-other-buyer", order_id: "ord-a2" },
        }),
        fetchOrder: async () => ({
          order: {
            id: "ord-a2",
            buyer_id: "buyer-2",
            sales_channel: "STOREFRONT",
            storefront_shop_id: "shop-a",
          },
        }),
      }),
    (err) => err instanceof GatewayError && err.status === 404
  );

  // 7. Non-authz gateway outage (503) propagates instead of turning into 404.
  await assert.rejects(
    () =>
      verifySiteReturnAccess({
        returnId: "ret-1",
        shopId: "shop-a",
        session: buyerSession,
        fetchReturn: async () => {
          throw new GatewayError("unavailable");
        },
        fetchOrder: async () => ({ order: siteAOrder }),
      }),
    (err) => err instanceof GatewayError && err.status === 503
  );
});

test("W1: verifySiteRefundAccess requires order_id, checks storefront + ownership, and binds refund_id to order refunds with 404", async () => {
  const buyerSession = { user: { id: "buyer-1", role: "buyer" } };
  const siteAOrder = {
    id: "ord-a1",
    buyer_id: "buyer-1",
    sales_channel: "STOREFRONT",
    storefront_shop_id: "shop-a",
  };

  // 1. Happy path: order belongs to shop-a & buyer-1 AND order refunds list contains rf-a1.
  const ok = await verifySiteRefundAccess({
    refundId: "rf-a1",
    orderId: "ord-a1",
    shopId: "shop-a",
    session: buyerSession,
    fetchOrder: async () => ({ order: siteAOrder }),
    fetchOrderRefunds: async () => ({
      items: [{ refund_id: "rf-a1", order_id: "ord-a1" }],
    }),
  });
  assert.equal(ok.order.id, "ord-a1");
  assert.equal(ok.refund.refund_id, "rf-a1");

  // 2. Omitting order_id cannot bypass shop isolation -> 400 ORDER_ID_REQUIRED.
  let fetchedOrder = false;
  await assert.rejects(
    () =>
      verifySiteRefundAccess({
        refundId: "rf-b1",
        orderId: "   ",
        shopId: "shop-a",
        session: buyerSession,
        fetchOrder: async () => {
          fetchedOrder = true;
          return { order: siteAOrder };
        },
        fetchOrderRefunds: async () => ({ items: [] }),
      }),
    (err) => err instanceof GatewayError && err.status === 400 && err.code === "ORDER_ID_REQUIRED"
  );
  assert.equal(fetchedOrder, false);

  // 3. Cross-shop order_id -> 404 (never 403).
  await assert.rejects(
    () =>
      verifySiteRefundAccess({
        refundId: "rf-b1",
        orderId: "ord-b1",
        shopId: "shop-a",
        session: buyerSession,
        fetchOrder: async () => ({
          order: {
            id: "ord-b1",
            buyer_id: "buyer-1",
            sales_channel: "STOREFRONT",
            storefront_shop_id: "shop-b",
          },
        }),
        fetchOrderRefunds: async () => ({ items: [{ refund_id: "rf-b1", order_id: "ord-b1" }] }),
      }),
    (err) => err instanceof GatewayError && err.status === 404
  );

  // 4. Other buyer's order_id on shop-a -> 404 (never 403).
  await assert.rejects(
    () =>
      verifySiteRefundAccess({
        refundId: "rf-a2",
        orderId: "ord-a2",
        shopId: "shop-a",
        session: buyerSession,
        fetchOrder: async () => ({
          order: {
            id: "ord-a2",
            buyer_id: "buyer-2",
            sales_channel: "STOREFRONT",
            storefront_shop_id: "shop-a",
          },
        }),
        fetchOrderRefunds: async () => ({ items: [{ refund_id: "rf-a2", order_id: "ord-a2" }] }),
      }),
    (err) => err instanceof GatewayError && err.status === 404
  );

  // 5. Bypass attempt: valid shop-a order_id paired with foreign refund_id rf-b1 not in order's refunds -> 404.
  await assert.rejects(
    () =>
      verifySiteRefundAccess({
        refundId: "rf-b1",
        orderId: "ord-a1",
        shopId: "shop-a",
        session: buyerSession,
        fetchOrder: async () => ({ order: siteAOrder }),
        fetchOrderRefunds: async () => ({
          items: [{ refund_id: "rf-a1", order_id: "ord-a1" }],
        }),
      }),
    (err) => err instanceof GatewayError && err.status === 404
  );

  // 6. Upstream 403 from order or refunds list -> 404 (never 403).
  await assert.rejects(
    () =>
      verifySiteRefundAccess({
        refundId: "rf-a1",
        orderId: "ord-a1",
        shopId: "shop-a",
        session: buyerSession,
        fetchOrder: async () => ({ order: siteAOrder }),
        fetchOrderRefunds: async () => {
          throw new GatewayError("forbidden");
        },
      }),
    (err) => err instanceof GatewayError && err.status === 404
  );
});

test("W2: executeSiteOrderLookup always sends storefront_shop_id to gateway and strips non-public fields", async () => {
  assert.deepEqual(
    buildOrderLookupGatewayBody({
      orderCode: " NM-1001 ",
      phone: " 0901234567 ",
      storefrontShopId: "shop-a",
    }),
    {
      order_code: "NM-1001",
      phone: "0901234567",
      storefront_shop_id: "shop-a",
    }
  );

  assert.throws(
    () =>
      buildOrderLookupGatewayBody({
        orderCode: "NM-1001",
        phone: "0901234567",
        storefrontShopId: "  ",
      }),
    (err) => err instanceof GatewayError && err.status === 404
  );

  const sentBodies = [];
  const res = await executeSiteOrderLookup({
    orderCode: "NM-1001",
    phone: "0901234567",
    storefrontShopId: "shop-a",
    postLookup: async (body) => {
      sentBodies.push(body);
      return {
        order: {
          order_code: "NM-1001",
          status: "SHIPPING",
          payment_method: "COD",
          payment_status: "PENDING",
          subtotal_vnd: 250000,
          shipping_fee_vnd: 25000,
          total_payable_vnd: 275000,
          carrier_code: "GHN",
          tracking_code: "GHN-1",
          province_name: "Hà Nội",
          ward_name: "Phường Phúc Xá",
          recipient_name_masked: "Ng*** An",
          recipient_phone_masked: "090***567",
          items: [{ product_name: "Áo", sku_name: "M", quantity: 1, line_total_vnd: 250000 }],
          timeline: [{ to_status: "SHIPPING", created_at: "2026-11-25T10:00:00Z" }],
          created_at: "2026-11-25T09:00:00Z",
          // Unexpected sensitive fields must never leak out
          street_address: "123 Secret St",
          label_url: "https://internal/label.html",
        },
      };
    },
  });

  assert.equal(sentBodies.length, 1);
  assert.deepEqual(sentBodies[0], {
    order_code: "NM-1001",
    phone: "0901234567",
    storefront_shop_id: "shop-a",
  });
  assert.equal(res.order.order_code, "NM-1001");
  assert.equal("street_address" in res.order, false);
  assert.equal("label_url" in res.order, false);
});

test("W5: verifySiteShippingLabelAccess enforces order_id, shop isolation, ownership, and FORWARD/RETURN tracking match with 404", async () => {
  const buyerSession = { user: { id: "buyer-1", role: "buyer" } };
  const siteAOrder = {
    id: "ord-a1",
    buyer_id: "buyer-1",
    sales_channel: "STOREFRONT",
    storefront_shop_id: "shop-a",
  };

  // 1. Missing order_id -> 404.
  await assert.rejects(
    () =>
      verifySiteShippingLabelAccess({
        trackingCode: "TRK-A1",
        orderId: "",
        shopId: "shop-a",
        session: buyerSession,
        fetchOrder: async () => ({ order: siteAOrder }),
        fetchShipment: async () => ({ shipment: { tracking_code: "TRK-A1" } }),
      }),
    (err) => err instanceof GatewayError && err.status === 404
  );

  // 2. Order from Site B -> 404 (never 403).
  await assert.rejects(
    () =>
      verifySiteShippingLabelAccess({
        trackingCode: "TRK-B1",
        orderId: "ord-b1",
        shopId: "shop-a",
        session: buyerSession,
        fetchOrder: async () => ({
          order: {
            id: "ord-b1",
            buyer_id: "buyer-1",
            sales_channel: "STOREFRONT",
            storefront_shop_id: "shop-b",
          },
        }),
        fetchShipment: async () => ({ shipment: { tracking_code: "TRK-B1" } }),
      }),
    (err) => err instanceof GatewayError && err.status === 404
  );

  // 3. Other buyer's order on Site A -> 404 (never 403).
  await assert.rejects(
    () =>
      verifySiteShippingLabelAccess({
        trackingCode: "TRK-A2",
        orderId: "ord-a2",
        shopId: "shop-a",
        session: buyerSession,
        fetchOrder: async () => ({
          order: {
            id: "ord-a2",
            buyer_id: "buyer-2",
            sales_channel: "STOREFRONT",
            storefront_shop_id: "shop-a",
          },
        }),
        fetchShipment: async () => ({ shipment: { tracking_code: "TRK-A2" } }),
      }),
    (err) => err instanceof GatewayError && err.status === 404
  );

  // 4. Valid Site A order_id paired with Site B tracking_code -> 404.
  await assert.rejects(
    () =>
      verifySiteShippingLabelAccess({
        trackingCode: "TRK-SITE-B",
        orderId: "ord-a1",
        shopId: "shop-a",
        session: buyerSession,
        fetchOrder: async () => ({ order: siteAOrder }),
        fetchShipment: async (_oid, dir) =>
          dir === "FORWARD"
            ? { shipment: { tracking_code: "TRK-A1" } }
            : { shipment: { tracking_code: "RET-A1" } },
      }),
    (err) => err instanceof GatewayError && err.status === 404
  );

  // 5. Valid Site A order + matching FORWARD shipment -> allowed.
  const okForward = await verifySiteShippingLabelAccess({
    trackingCode: "TRK-A1",
    orderId: "ord-a1",
    shopId: "shop-a",
    session: buyerSession,
    fetchOrder: async () => ({ order: siteAOrder }),
    fetchShipment: async (_oid, dir) =>
      dir === "FORWARD" ? { shipment: { tracking_code: "TRK-A1" } } : null,
  });
  assert.equal(okForward.id, "ord-a1");

  // 6. Valid Site A order + matching RETURN shipment (even if FORWARD fails 404) -> allowed.
  const okReturn = await verifySiteShippingLabelAccess({
    trackingCode: "RET-A1",
    orderId: "ord-a1",
    shopId: "shop-a",
    session: buyerSession,
    fetchOrder: async () => ({ order: siteAOrder }),
    fetchShipment: async (_oid, dir) => {
      if (dir === "FORWARD") throw new GatewayError("not_found");
      return { shipment: { tracking_code: "RET-A1" } };
    },
  });
  assert.equal(okReturn.id, "ord-a1");

  // 7. assertBuyerOrderInStorefront returns 404 on wrong buyer directly.
  assert.throws(
    () =>
      assertBuyerOrderInStorefront(
        {
          id: "ord-a2",
          buyer_id: "buyer-2",
          sales_channel: "STOREFRONT",
          storefront_shop_id: "shop-a",
        },
        "shop-a",
        buyerSession
      ),
    (err) => err instanceof GatewayError && err.status === 404
  );
});

test("W1: separate module imports of tokens.ts share single-flight rotation, 5s grace window, and logout invalidation via globalThis (Symbol.for)", async () => {
  assert.equal(REFRESH_GRACE_MS, 5_000, "post-rotation grace window must be 5 seconds");

  const modA = await import("./tokens.ts?w1-inst=a");
  const modB = await import("./tokens.ts?w1-inst=b");
  assert.notEqual(
    modA.rotateRefreshToken,
    modB.rotateRefreshToken,
    "expected two distinct module evaluations simulating middleware vs route-handler bundles"
  );

  let calls = 0;
  const origFetch = globalThis.fetch;
  globalThis.fetch = async () => {
    calls += 1;
    await new Promise((resolve) => setTimeout(resolve, 25));
    return new Response(
      JSON.stringify({
        access_token: `at-shared-${calls}`,
        refresh_token: `rt-shared-${calls}`,
        expires_in_seconds: 900,
      }),
      { status: 200, headers: { "Content-Type": "application/json" } }
    );
  };
  try {
    const token = `rt-cross-bundle-dedup-${Date.now()}`;
    const [r1, r2] = await Promise.all([
      modA.rotateRefreshToken(token),
      modB.rotateRefreshToken(token),
    ]);
    assert.equal(calls, 1, "concurrent calls across two module imports must share ONE gateway refresh");
    assert.deepEqual(r1, r2);
    assert.equal(r1?.accessToken, "at-shared-1");

    // Grace hit through the other module instance reuses the cached pair.
    const r3 = await modB.rotateRefreshToken(token);
    assert.equal(calls, 1);
    assert.equal(r3?.accessToken, "at-shared-1");

    // Logout invalidation through module A purges the entry for module B immediately.
    const purged = await modA.invalidateRefreshCache(token);
    assert.equal(purged.purgedTokens?.accessToken, "at-shared-1");
    assert.equal(await modB.rotateRefreshToken(token), null);
    assert.equal(calls, 1);
  } finally {
    globalThis.fetch = origFetch;
  }
});

test("W2: buildSiteShippingQuotePayload never forwards client-supplied from_province_code or from_ward_code", () => {
  const fromShops = buildSiteShippingQuotePayload(
    {
      to_province_code: "79",
      to_ward_code: "26734",
      shops: [
        {
          shop_id: "shop-a",
          from_province_code: "79",
          from_ward_code: "26734",
          weight_grams: 500,
          cod_amount_vnd: 0,
        },
      ],
    },
    "shop-a"
  );
  assert.equal("from_province_code" in fromShops, false);
  assert.equal("from_ward_code" in fromShops, false);
  assert.deepEqual(fromShops, {
    shop_id: "shop-a",
    to_province_code: "79",
    to_ward_code: "26734",
    weight_grams: 500,
    cod_amount_vnd: 0,
  });

  const fromTopLevel = buildSiteShippingQuotePayload(
    {
      to_province_code: "01",
      to_ward_code: "00001",
      from_province_code: "01",
      from_ward_code: "00001",
      items: [{ sku_id: "sku-1", quantity: 2 }],
      payment_method: "VIETQR",
    },
    "shop-a"
  );
  assert.equal("from_province_code" in fromTopLevel, false);
  assert.equal("from_ward_code" in fromTopLevel, false);
  assert.deepEqual(fromTopLevel, {
    shop_id: "shop-a",
    to_province_code: "01",
    to_ward_code: "00001",
    items: [{ sku_id: "sku-1", quantity: 2 }],
    payment_method: "VIETQR",
  });
});

test("W6: buildChatWsUrl & resolveChatWsOrigin use public config, never leak internal Docker hosts, and support wss in production", () => {
  // Never leaks internal docker hostname `gateway-bff`
  const composeDefault = buildChatWsUrl("tk-1", {
    NODE_ENV: "production",
    COOKIE_SECURE: "false",
    GATEWAY_URL: "http://gateway-bff:8080",
  });
  assert.equal(composeDefault, "ws://localhost:8080/api/v1/chat/ws?ticket=tk-1");
  assert.equal(composeDefault.includes("gateway-bff"), false);

  // Respects GATEWAY_PORT in dev/compose when public URL not set
  const composeCustomPort = buildChatWsUrl("tk-2", {
    NODE_ENV: "production",
    COOKIE_SECURE: "false",
    GATEWAY_URL: "http://gateway-bff:8080",
    GATEWAY_PORT: "18080",
  });
  assert.equal(composeCustomPort, "ws://localhost:18080/api/v1/chat/ws?ticket=tk-2");
  assert.equal(
    resolveChatWsOrigin({ GATEWAY_URL: "http://gateway-bff:8080", GATEWAY_PORT: "18080" }),
    "ws://localhost:18080"
  );

  // Explicit PUBLIC_GATEWAY_URL converts https -> wss
  const prodGw = buildChatWsUrl("tk-3", {
    NODE_ENV: "production",
    COOKIE_SECURE: "true",
    GATEWAY_URL: "http://gateway-bff:8080",
    PUBLIC_GATEWAY_URL: "https://api.nexusmart.vn",
  });
  assert.equal(prodGw, "wss://api.nexusmart.vn/api/v1/chat/ws?ticket=tk-3");
  assert.equal(
    resolveChatWsOrigin({ PUBLIC_GATEWAY_URL: "https://api.nexusmart.vn" }),
    "wss://api.nexusmart.vn"
  );

  // Explicit CHAT_WS_URL takes highest priority
  const prodWs = buildChatWsUrl("tk-4", {
    CHAT_WS_URL: "wss://ws.nexusmart.vn/api/v1/chat/ws",
    PUBLIC_GATEWAY_URL: "https://api.nexusmart.vn",
  });
  assert.equal(prodWs, "wss://ws.nexusmart.vn/api/v1/chat/ws?ticket=tk-4");

  // Production COOKIE_SECURE=true with APP_ORIGIN derives wss
  const prodAppOrigin = buildChatWsUrl("tk-5", {
    NODE_ENV: "production",
    COOKIE_SECURE: "true",
    GATEWAY_URL: "http://gateway-bff:8080",
    APP_ORIGIN: "https://nexusmart.vn",
  });
  assert.equal(prodAppOrigin, "wss://nexusmart.vn/api/v1/chat/ws?ticket=tk-5");

  // Local host loopback GATEWAY_URL is preserved when GATEWAY_PORT not set
  const localHost = buildChatWsUrl("tk-6", {
    GATEWAY_URL: "http://127.0.0.1:18080",
  });
  assert.equal(localHost, "ws://127.0.0.1:18080/api/v1/chat/ws?ticket=tk-6");

  // Runtime CSP helper keeps connect-src aligned with the resolved WS origin
  assert.ok(
    buildSiteCspHeader({ PUBLIC_GATEWAY_URL: "https://api.nexusmart.vn" }).includes(
      "connect-src 'self' wss://api.nexusmart.vn"
    )
  );
  assert.ok(
    buildSiteCspHeader({ GATEWAY_URL: "http://127.0.0.1:18080" }).includes(
      "connect-src 'self' ws://127.0.0.1:18080"
    )
  );
});

test("W8: isShopBankAccountNotFoundError matches only missing shop bank-account 404 NOT_FOUND", () => {
  assert.equal(
    isShopBankAccountNotFoundError(
      new GatewayError("not_found", "NOT_FOUND", "shop bank account not found")
    ),
    true
  );
  assert.equal(
    isShopBankAccountNotFoundError(new GatewayError("not_found", "NOT_FOUND", "shop not found")),
    false
  );
  assert.equal(
    isShopBankAccountNotFoundError(new GatewayError("not_found", "NOT_FOUND", "")),
    false
  );
  assert.equal(
    isShopBankAccountNotFoundError(new GatewayError("forbidden", "PERMISSION_DENIED", "bank account")),
    false
  );
  assert.equal(
    isShopBankAccountNotFoundError(new GatewayError("unavailable", "UNAVAILABLE", "bank account")),
    false
  );
});
