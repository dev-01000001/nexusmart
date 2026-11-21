// Run: node --test web/src/lib/server/security.test.mjs (Node 22 type stripping)
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  GatewayError,
  SAFE_MESSAGES,
  errorFromResponse,
  errorResponse,
  logServer,
} from "./errors.ts";
import {
  assertSameOrigin,
  buildLoginRedirectUrl,
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
  accessExpiresSoon,
  applyAuthCookiesToHeader,
  clearAuthCookies,
  rotateRefreshToken,
  setAuthCookies,
} from "./tokens.ts";
import { toAddressItem, toGatewayAddressBody } from "./address.ts";
import { lookupPublishedSiteHost, parsePublishedSiteHost } from "./siteLookup.ts";
import {
  assertInStorefront,
  assertPaymentInStorefront,
  belongsToStorefront,
  cartHasSku,
  hasPlatformPromoInput,
  isSiteShopVoucher,
  resolveSiteShopVoucherMap,
  scopeCartToShop,
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
