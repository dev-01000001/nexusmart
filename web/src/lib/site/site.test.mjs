// Run: node --test web/src/lib/site/site.test.mjs (Node 22 type stripping)
import test from "node:test";
import assert from "node:assert/strict";
import {
  classifyHost,
  deriveMarketplaceOrigin,
  deriveSiteOrigin,
  isBlockedSitePath,
  isBuyerApiPath,
  isDirectInternalSitePath,
  isProtectedPagePath,
  isReservedSubdomain,
  isSiteApiPath,
  isTenantSiteHost,
  normalizeHostHeader,
  resolveCanonicalHostRedirect,
} from "./host.ts";
import { validateCustomHostname } from "./domains.ts";
import { createSiteHostCache } from "./cache.ts";
import {
  SITE_FONT_STACK,
  buildSiteThemeVars,
  checkThemeContrast,
  contrastRatio,
  resolveSiteFontStack,
  sanitizeSafeHref,
  sanitizeSafeUrl,
} from "./theme.ts";
import {
  ALLOWED_SITE_FONTS,
  MAX_SITE_SECTIONS,
  SITE_FONT_LABELS,
  SITE_THEME_PRESETS,
  createDefaultSiteConfig,
  parseSiteConfig,
} from "./types.ts";
import {
  buildSiteMetadata,
  buildSiteProductMetadata,
  buildSiteRobotsMeta,
  hasSubstantiveSiteContent,
} from "./seo.ts";

test("normalizeHostHeader lowercases, validates ports (1-65535), strips at most one trailing dot, handles IPv6, rejects malformed", () => {
  assert.deepEqual(normalizeHostHeader("Bach-Dang.NexusMart.Localhost.:3300"), {
    hostname: "bach-dang.nexusmart.localhost",
    port: "3300",
  });
  assert.deepEqual(normalizeHostHeader("Shop.Brand.VN."), {
    hostname: "shop.brand.vn",
    port: "",
  });
  assert.deepEqual(normalizeHostHeader("shop.brand.vn.:1"), {
    hostname: "shop.brand.vn",
    port: "1",
  });
  assert.deepEqual(normalizeHostHeader("shop.brand.vn:65535"), {
    hostname: "shop.brand.vn",
    port: "65535",
  });
  assert.deepEqual(normalizeHostHeader("[::1]:3300"), { hostname: "::1", port: "3300" });
  assert.deepEqual(normalizeHostHeader("[::1]:65535"), { hostname: "::1", port: "65535" });
  assert.deepEqual(normalizeHostHeader("::1"), { hostname: "::1", port: "" });
  assert.deepEqual(normalizeHostHeader("127.0.0.1:3300"), { hostname: "127.0.0.1", port: "3300" });

  // Invalid ports, repeated trailing dots, and malformed host strings are rejected.
  for (const malformed of [
    "evil.com/path",
    "host:notaport",
    "shop.brand.vn:0",
    "shop.brand.vn:65536",
    "shop.brand.vn:70000",
    "shop.brand.vn:abc",
    "shop.brand.vn:",
    "[::1]:0",
    "[::1]:70000",
    "[::1]:abc",
    "example.com..",
    "example.com..:3300",
    ".",
    "..",
    "",
  ]) {
    assert.deepEqual(
      normalizeHostHeader(malformed),
      { hostname: "", port: "" },
      `expected malformed host rejected: ${malformed}`
    );
  }
});

test("classifyHost identifies root, subdomain site, valid custom domains, and rejects reserved/forbidden/malformed hosts", () => {
  const root = "nexusmart.localhost";

  // Root platform hosts (never classify as custom, never call /sites/by-host)
  for (const h of [
    "nexusmart.localhost",
    "NEXUSMART.LOCALHOST:3300",
    "nexusmart.localhost.",
    "localhost",
    "localhost:3300",
    "web:3000",
    "127.0.0.1:3300",
    "100.93.43.5:3111",
    "[::1]:3300",
    "::1",
  ]) {
    const cls = classifyHost(h, root);
    assert.equal(cls.kind, "root", `expected root for ${h}, got ${cls.kind}`);
    assert.equal(isTenantSiteHost(cls), false);
  }

  // Valid single-label subdomain sites
  const bd = classifyHost("Bach-Dang.NexusMart.Localhost:3300", root);
  assert.equal(bd.kind, "site");
  assert.equal(isTenantSiteHost(bd), true);
  if (bd.kind === "site") {
    assert.equal(bd.subdomain, "bach-dang");
    assert.equal(bd.hostname, "bach-dang.nexusmart.localhost");
    assert.equal(bd.port, "3300");
  }

  const sg = classifyHost("sai-gon.nexusmart.localhost", root);
  assert.equal(sg.kind, "site");
  assert.equal(isTenantSiteHost(sg), true);

  // Valid custom domains (with and without dev ports / trailing dot)
  const customCases = [
    { raw: "hanoi-knit.verified.test", hostname: "hanoi-knit.verified.test", port: "" },
    { raw: "hanoi-knit.verified.test:3300", hostname: "hanoi-knit.verified.test", port: "3300" },
    { raw: "shop.brand.vn", hostname: "shop.brand.vn", port: "" },
    { raw: "www.brand-vn.com", hostname: "www.brand-vn.com", port: "" },
    { raw: "SHOP.BRAND.VN.:3300", hostname: "shop.brand.vn", port: "3300" },
  ];
  for (const c of customCases) {
    const cls = classifyHost(c.raw, root);
    assert.equal(cls.kind, "custom", `expected custom for ${c.raw}, got ${cls.kind}`);
    assert.equal(isTenantSiteHost(cls), true);
    assert.equal(cls.hostname, c.hostname);
    assert.equal(cls.port, c.port);
  }

  const longLabel = `${"a".repeat(64)}.com`;
  const longHost = `${"a".repeat(60)}.${"b".repeat(60)}.${"c".repeat(60)}.${"d".repeat(60)}.${"e".repeat(20)}.com`;

  // Reserved, brand-impersonation, multi-level, local/internal, bad ports, weird chars,
  // numeric TLDs, and malformed hosts -> unknown (404 without calling backend)
  for (const bad of [
    "a.b.nexusmart.localhost",
    "www.nexusmart.localhost",
    "api.nexusmart.localhost",
    "admin.nexusmart.localhost",
    "seller.nexusmart.localhost",
    "s.nexusmart.localhost",
    "vietcombank-uudai.nexusmart.localhost",
    "nexusmart-sale.nexusmart.localhost",
    "xn--phish.nexusmart.localhost",
    "momo.nexusmart.localhost",
    "ab.nexusmart.localhost",
    "-lead.nexusmart.localhost",
    "trail-.nexusmart.localhost",
    "bad_underscore.nexusmart.localhost",
    "foo.localhost",
    "a.b.localhost",
    "shop.local",
    "svc.internal",
    "shop.brand.vn:0",
    "shop.brand.vn:70000",
    "shop.brand.vn:abc",
    "*.brand.vn",
    "bad_host.com",
    "tênmiền.vn",
    "shop.brand.vn/path",
    "shop.brand.vn..",
    "a..b.com",
    ".brand.vn",
    "-lead.com",
    "trail-.com",
    "singlelabel",
    "shop.a",
    longLabel,
    longHost,
    "999.999.999.999",
    "127.1",
    "",
  ]) {
    const cls = classifyHost(bad, root);
    assert.equal(cls.kind, "unknown", `expected unknown for ${bad}, got ${cls.kind}`);
    assert.equal(isTenantSiteHost(cls), false);
  }

  // `nexusmart.localhost` is rejected when rootDomain is a production domain.
  assert.equal(classifyHost("nexusmart.localhost", "nexusmart.vn").kind, "unknown");

  assert.equal(isReservedSubdomain("vietcombank-uudai"), true);
  assert.equal(isReservedSubdomain("bach-dang"), false);
});

test("validateCustomHostname accepts valid external domains and rejects IP, localhost, ports, platform domains, and wildcards", () => {
  for (const [input, expected] of [
    ["hanoi-knit.verified.test", "hanoi-knit.verified.test"],
    ["shop.brand.vn", "shop.brand.vn"],
    ["www.brand-vn.com", "www.brand-vn.com"],
    ["  SHOP.BRAND.VN. ", "shop.brand.vn"],
  ]) {
    const res = validateCustomHostname(input, "nexusmart.localhost");
    assert.equal(res.ok, true, `expected valid custom hostname for ${input}`);
    if (res.ok) assert.equal(res.hostname, expected);
  }

  const rejected = [
    ["", "empty"],
    ["127.0.0.1", "ip_literal"],
    ["100.93.43.5", "ip_literal"],
    ["999.999.999.999", "ip_literal"],
    ["127.1", "ip_literal"],
    ["[::1]", "port_or_ipv6"],
    ["::1", "port_or_ipv6"],
    ["shop.brand.vn:3300", "port_or_ipv6"],
    ["localhost", "forbidden_host"],
    ["foo.localhost", "forbidden_host"],
    ["shop.local", "forbidden_host"],
    ["svc.internal", "forbidden_host"],
    ["nexusmart.localhost", "forbidden_host"],
    ["bach-dang.nexusmart.localhost", "forbidden_host"],
    ["*.brand.vn", "wildcard"],
    ["https://shop.brand.vn", "invalid_chars"],
    ["tênmiền.vn", "non_ascii"],
    ["singlelabel", "no_dot"],
    ["-lead.com", "invalid_label"],
    ["trail-.com", "invalid_label"],
    ["bad_host.com", "invalid_label"],
    ["example.com..", "invalid_label"],
  ];
  for (const [input, reason] of rejected) {
    const res = validateCustomHostname(input, "nexusmart.localhost");
    assert.equal(res.ok, false, `expected invalid custom hostname for ${input}`);
    if (!res.ok) assert.equal(res.reason, reason, `unexpected reason for ${input}`);
  }

  // Production root domain and any subdomain under it are rejected as platform_domain.
  for (const plat of ["nexusmart.vn", "bach-dang.nexusmart.vn", "a.b.nexusmart.vn"]) {
    const res = validateCustomHostname(plat, "nexusmart.vn");
    assert.equal(res.ok, false, `expected platform_domain rejected for ${plat}`);
    if (!res.ok) assert.equal(res.reason, "platform_domain");
  }
});

test("resolveCanonicalHostRedirect enforces 308 GET-only subdomain->custom rules in dev and prod", () => {
  const devSubClass = classifyHost("hanoi-knit.nexusmart.localhost:3300", "nexusmart.localhost");
  const devNoPortSubClass = classifyHost("hanoi-knit.nexusmart.localhost", "nexusmart.localhost");
  const prodSubClass = classifyHost("hanoi-knit.nexusmart.vn", "nexusmart.vn");
  const customClass = classifyHost("hanoi-knit.verified.test:3300", "nexusmart.localhost");
  const rootClass = classifyHost("nexusmart.localhost:3300", "nexusmart.localhost");
  const unknownClass = classifyHost("bad_host.com", "nexusmart.localhost");

  // Dev with port: keeps incoming scheme + port + pathname + query string.
  assert.equal(
    resolveCanonicalHostRedirect({
      hostClass: devSubClass,
      primaryHost: "hanoi-knit.verified.test",
      rootDomain: "nexusmart.localhost",
      method: "GET",
      pathname: "/san-pham/ao-len-merino",
      search: "?color=den&size=m",
      proto: "http",
    }),
    "http://hanoi-knit.verified.test:3300/san-pham/ao-len-merino?color=den&size=m"
  );

  // Dev with https + port: keeps https + port.
  assert.equal(
    resolveCanonicalHostRedirect({
      hostClass: devSubClass,
      primaryHost: "hanoi-knit.verified.test",
      rootDomain: "nexusmart.localhost",
      method: "get",
      pathname: "/",
      proto: "https",
    }),
    "https://hanoi-knit.verified.test:3300/"
  );

  // Dev `.localhost` without port: keeps http without port.
  assert.equal(
    resolveCanonicalHostRedirect({
      hostClass: devNoPortSubClass,
      primaryHost: "hanoi-knit.verified.test",
      rootDomain: "nexusmart.localhost",
      method: "GET",
      pathname: "/gioi-thieu",
      proto: "http",
    }),
    "http://hanoi-knit.verified.test/gioi-thieu"
  );

  // Production domain without dev port: always forces https:// even if incoming proto is http.
  assert.equal(
    resolveCanonicalHostRedirect({
      hostClass: prodSubClass,
      primaryHost: "shop.brand.vn",
      rootDomain: "nexusmart.vn",
      method: "GET",
      pathname: "/san-pham",
      search: "?page=2&sort=newest",
      proto: "http",
    }),
    "https://shop.brand.vn/san-pham?page=2&sort=newest"
  );

  // Non-GET methods NEVER redirect.
  for (const method of ["POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"]) {
    assert.equal(
      resolveCanonicalHostRedirect({
        hostClass: devSubClass,
        primaryHost: "hanoi-knit.verified.test",
        rootDomain: "nexusmart.localhost",
        method,
        pathname: "/san-pham",
        proto: "http",
      }),
      null,
      `expected no 308 for method=${method}`
    );
  }

  // API paths, metadata paths, and blocked paths NEVER redirect.
  for (const pathname of [
    "/api/cart",
    "/api/cart/items",
    "/api/auth/me",
    "/api/checkout",
    "/api/seller/site",
    "/robots.txt",
    "/sitemap.xml",
    "/seller",
    "/admin",
    "/s/hanoi-knit.nexusmart.localhost",
  ]) {
    assert.equal(
      resolveCanonicalHostRedirect({
        hostClass: devSubClass,
        primaryHost: "hanoi-knit.verified.test",
        rootDomain: "nexusmart.localhost",
        method: "GET",
        pathname,
        proto: "http",
      }),
      null,
      `expected no 308 for pathname=${pathname}`
    );
  }

  // Already on custom host, root host, or unknown host -> NEVER redirect.
  for (const hostClass of [customClass, rootClass, unknownClass]) {
    assert.equal(
      resolveCanonicalHostRedirect({
        hostClass,
        primaryHost: "hanoi-knit.verified.test",
        rootDomain: "nexusmart.localhost",
        method: "GET",
        pathname: "/",
        proto: "http",
      }),
      null,
      `expected no 308 for hostClass.kind=${hostClass.kind}`
    );
  }

  // Subdomain primaryHost or invalid/localhost/IP primaryHost -> NEVER redirect.
  for (const badPrimary of [
    null,
    undefined,
    "",
    "hanoi-knit.nexusmart.localhost",
    "other-shop.nexusmart.localhost",
    "nexusmart.localhost",
    "localhost",
    "127.0.0.1",
    "foo.local",
    "*.brand.vn",
    "bad_host.com",
  ]) {
    assert.equal(
      resolveCanonicalHostRedirect({
        hostClass: devSubClass,
        primaryHost: badPrimary,
        rootDomain: "nexusmart.localhost",
        method: "GET",
        pathname: "/",
        proto: "http",
      }),
      null,
      `expected no 308 for primaryHost=${String(badPrimary)}`
    );
  }
});

test("site vs root API routing, blocked paths, protected pages, and direct /s internal paths are strictly enforced", () => {
  // Direct /s/... is ALWAYS blocked on every host.
  for (const p of ["/s", "/s/", "/s/bach-dang.nexusmart.localhost", "/s/foo/san-pham"]) {
    assert.equal(isDirectInternalSitePath(p), true, `expected direct /s blocked: ${p}`);
    assert.equal(isBlockedSitePath(p), true, `expected site blocked: ${p}`);
  }
  assert.equal(isDirectInternalSitePath("/san-pham"), false);

  // Blocked on site hosts: seller/admin pages & APIs, internal /s, site-templates, batch labels,
  // and broad user/notification endpoints that are not explicitly allowlisted.
  for (const blocked of [
    "/api",
    "/api/seller",
    "/api/seller/site",
    "/api/seller/orders",
    "/api/seller/orders/ord-1/returns",
    "/api/seller/returns/ret-1/approve",
    "/api/seller/notifications",
    "/api/seller/shop",
    "/api/admin",
    "/api/admin/sites",
    "/api/admin/users",
    "/api/admin/users/u-1/role",
    "/api/admin/orders",
    "/api/admin/complaints",
    "/api/admin/audit-logs",
    "/api/site-templates",
    "/api/site-templates/tpl-1",
    "/api/shipping/labels/batch",
    "/api/users",
    "/api/users/u-1",
    "/api/users/me",
    "/api/notifications",
    "/api/notifications/read",
    "/api/v1/sites",
    "/seller",
    "/seller/site",
    "/admin",
    "/admin/sites",
  ]) {
    assert.equal(isBlockedSitePath(blocked), true, `expected blocked on site host: ${blocked}`);
    assert.equal(isSiteApiPath(blocked), false, `expected not a site API: ${blocked}`);
  }

  // Allowed buyer + auth BFF APIs on PUBLISHED site hosts.
  for (const apiPath of [
    "/api/auth/login",
    "/api/auth/register",
    "/api/auth/me",
    "/api/auth/refresh",
    "/api/auth/logout",
    "/api/auth/addresses",
    "/api/auth/forgot-password",
    "/api/auth/reset-password",
    "/api/assistant/chat",
    "/api/assistant/handoff",
    "/api/cart",
    "/api/cart/items",
    "/api/cart/items/sku-1",
    "/api/chat/conversations",
    "/api/chat/conversations/c-1/messages",
    "/api/chat/ws-ticket",
    "/api/checkout",
    "/api/checkout/shipping-quote",
    "/api/checkout/locations",
    "/api/notifications/preferences",
    "/api/orders",
    "/api/orders/lookup",
    "/api/orders/ord-1",
    "/api/orders/ord-1/address",
    "/api/orders/ord-1/cancel",
    "/api/orders/ord-1/redelivery",
    "/api/orders/ord-1/refunds",
    "/api/orders/ord-1/return",
    "/api/orders/ord-1/reviews",
    "/api/payment-methods",
    "/api/payments/pay-1",
    "/api/payments/pay-1/events",
    "/api/payments/pay-1/retry",
    "/api/products/prod-1/review-summary",
    "/api/products/prod-1/reviews",
    "/api/refunds/rf-1/bank-account",
    "/api/returns/ret-1/escalate",
    "/api/returns/ret-1/evidence",
    "/api/search",
    "/api/search/suggest",
    "/api/shipping/addresses/map-legacy",
    "/api/shipping/labels/TRK123",
    "/api/shipping/shipments/ord-1",
    "/api/shop-discounts/active",
    "/api/users/me/phone/otp",
    "/api/users/me/phone/verify",
    "/api/vouchers",
    "/api/vouchers/preview",
    "/api/vouchers/validate",
    "/api/vouchers/v-1/save",
  ]) {
    assert.equal(isBlockedSitePath(apiPath), false, `expected allowed on site host: ${apiPath}`);
    assert.equal(isSiteApiPath(apiPath), true, `expected site API path: ${apiPath}`);
  }

  // Buyer commerce APIs are 404 on the root host, while auth/seller/admin remain available there.
  for (const buyerApi of [
    "/api/assistant/chat",
    "/api/assistant/handoff",
    "/api/cart",
    "/api/cart/items",
    "/api/chat/conversations",
    "/api/chat/ws-ticket",
    "/api/checkout",
    "/api/checkout/addresses",
    "/api/orders",
    "/api/orders/lookup",
    "/api/orders/ord-1",
    "/api/orders/ord-1/return",
    "/api/payments/pay-1",
    "/api/payments/pay-1/events",
    "/api/products/prod-1/review-summary",
    "/api/products/prod-1/reviews",
    "/api/refunds/rf-1/bank-account",
    "/api/returns/ret-1/escalate",
    "/api/search",
    "/api/search/suggest",
    "/api/shop-discounts/active",
    "/api/vouchers",
    "/api/vouchers/validate",
  ]) {
    assert.equal(isBuyerApiPath(buyerApi), true, `expected buyer API blocked on root: ${buyerApi}`);
  }
  for (const rootApi of [
    "/api/auth/login",
    "/api/auth/me",
    "/api/auth/forgot-password",
    "/api/auth/reset-password",
    "/api/notifications/preferences",
    "/api/shipping/labels/TRK123",
    "/api/users/me/phone/otp",
    "/api/seller/site",
    "/api/seller/chat/conversations",
    "/api/seller/assistant/report",
    "/api/seller/returns/ret-1/approve",
    "/api/admin/sites",
    "/api/admin/complaints",
    "/api/admin/categories",
  ]) {
    assert.equal(isBuyerApiPath(rootApi), false, `expected root API allowed: ${rootApi}`);
  }

  // Site pages allowed (rewritten to /s/[host]/...)
  for (const allowed of [
    "/",
    "/san-pham",
    "/san-pham/ao-thun-cotton-compact-trang",
    "/gio-hang",
    "/thanh-toan",
    "/thanh-toan/cho/pay-1",
    "/don-hang",
    "/tai-khoan",
    "/chinh-sach",
    "/gioi-thieu",
    "/robots.txt",
    "/sitemap.xml",
  ]) {
    assert.equal(isBlockedSitePath(allowed), false, `expected allowed on site host: ${allowed}`);
  }

  // Protected pages differ by host kind.
  for (const p of [
    "/tai-khoan",
    "/tai-khoan/dia-chi",
    "/don-hang",
    "/don-hang/o1",
    "/thanh-toan/cho/p1",
  ]) {
    assert.equal(isProtectedPagePath(p, "site"), true, `expected protected on site: ${p}`);
    assert.equal(
      isProtectedPagePath(p, "root"),
      false,
      `expected NOT protected-redirected on root: ${p}`
    );
  }
  assert.equal(isProtectedPagePath("/thanh-toan", "site"), false);
  for (const p of ["/seller", "/seller/site", "/admin", "/admin/sites"]) {
    assert.equal(isProtectedPagePath(p, "root"), true, `expected protected on root: ${p}`);
    assert.equal(isProtectedPagePath(p, "site"), false);
  }
});

test("deriveMarketplaceOrigin and deriveSiteOrigin preserve proto/port cleanly for subdomain and custom hosts", () => {
  assert.equal(
    deriveMarketplaceOrigin({
      rawHost: "bach-dang.nexusmart.localhost:3300",
      proto: "http",
      rootDomain: "nexusmart.localhost",
    }),
    "http://nexusmart.localhost:3300"
  );
  assert.equal(
    deriveMarketplaceOrigin({
      rawHost: "hanoi-knit.verified.test:3300",
      proto: "http",
      rootDomain: "nexusmart.localhost",
    }),
    "http://nexusmart.localhost:3300"
  );
  assert.equal(
    deriveMarketplaceOrigin({
      rawHost: "shop.brand.vn",
      proto: "https",
      rootDomain: "nexusmart.vn",
    }),
    "https://nexusmart.vn"
  );
  assert.equal(
    deriveMarketplaceOrigin({
      rawHost: "bach-dang.nexusmart.vn",
      proto: "https",
      rootDomain: "nexusmart.vn",
      appOrigin: "https://nexusmart.vn/",
    }),
    "https://nexusmart.vn"
  );
  assert.equal(
    deriveSiteOrigin({
      siteHost: "bach-dang.nexusmart.localhost",
      rawHost: "nexusmart.localhost:3300",
      proto: "http",
    }),
    "http://bach-dang.nexusmart.localhost:3300"
  );
  assert.equal(
    deriveSiteOrigin({
      siteHost: "hanoi-knit.verified.test",
      rawHost: "nexusmart.localhost:3300",
      proto: "http",
    }),
    "http://hanoi-knit.verified.test:3300"
  );
});

test("createSiteHostCache handles positive TTL, negative TTL, error non-caching, inflight dedupe, and eviction", async () => {
  let clock = 1000;
  const cache = createSiteHostCache({
    positiveTtlMs: 3000,
    negativeTtlMs: 1500,
    maxEntries: 2,
    now: () => clock,
  });

  let calls = 0;
  const hitRes = await cache.resolve("bach-dang.nexusmart.localhost", async () => {
    calls++;
    return {
      status: "found",
      siteId: "site-1",
      shopId: "shop-1",
      primaryHost: "bach-dang.nexusmart.localhost",
    };
  });
  assert.equal(hitRes.status, "found");
  assert.equal(calls, 1);

  // Within positive TTL -> served from cache
  clock += 2000;
  const cachedHit = await cache.resolve("bach-dang.nexusmart.localhost", async () => {
    calls++;
    return { status: "not_found" };
  });
  assert.equal(cachedHit.status, "found");
  assert.equal(calls, 1);

  // Past positive TTL -> re-fetched
  clock += 1500;
  const missRes = await cache.resolve("bach-dang.nexusmart.localhost", async () => {
    calls++;
    return { status: "not_found" };
  });
  assert.equal(missRes.status, "not_found");
  assert.equal(calls, 2);

  // Within negative TTL -> negative-cached without hitting backend
  clock += 1000;
  const cachedMiss = await cache.resolve("bach-dang.nexusmart.localhost", async () => {
    calls++;
    return {
      status: "found",
      siteId: "site-1",
      shopId: "shop-1",
      primaryHost: "bach-dang.nexusmart.localhost",
    };
  });
  assert.equal(cachedMiss.status, "not_found");
  assert.equal(calls, 2);

  // Past negative TTL -> re-fetched
  clock += 600;
  const recovered = await cache.resolve("bach-dang.nexusmart.localhost", async () => {
    calls++;
    return {
      status: "found",
      siteId: "site-1",
      shopId: "shop-1",
      primaryHost: "bach-dang.nexusmart.localhost",
    };
  });
  assert.equal(recovered.status, "found");
  assert.equal(calls, 3);

  // Transient error is NOT cached
  let errCalls = 0;
  const errRes = await cache.resolve("flaky.nexusmart.localhost", async () => {
    errCalls++;
    return { status: "error" };
  });
  assert.equal(errRes.status, "error");
  assert.equal(cache.get("flaky.nexusmart.localhost"), null);
  assert.equal(errCalls, 1);

  // Bounded maxEntries=2 evicts oldest negative entry first, preserving the live positive entry
  cache.setNotFound("host-a.nexusmart.localhost");
  cache.setNotFound("host-b.nexusmart.localhost");
  assert.equal(cache.size(), 2);
  cache.setNotFound("host-c.nexusmart.localhost");
  assert.equal(cache.size(), 2);
  assert.equal(cache.get("host-a.nexusmart.localhost"), null);
  assert.notEqual(cache.get("host-c.nexusmart.localhost"), null);
  assert.equal(cache.get("bach-dang.nexusmart.localhost")?.status, "found");
});

test("createSiteHostCache bounds negative entries at maxNegativeEntries and protects positive hits from unknown custom-host floods", async () => {
  let clock = 10_000;
  const cache = createSiteHostCache({
    positiveTtlMs: 30_000,
    negativeTtlMs: 10_000,
    maxEntries: 6,
    maxNegativeEntries: 3,
    now: () => clock,
  });

  // Seed 3 live sites (subdomain + custom domains)
  const liveHosts = ["bach-dang.nexusmart.localhost", "hanoi-knit.verified.test", "shop.brand.vn"];
  for (let i = 0; i < liveHosts.length; i++) {
    cache.setFound(liveHosts[i], {
      siteId: `site-${i + 1}`,
      shopId: `shop-${i + 1}`,
      primaryHost: liveHosts[i],
    });
  }
  assert.equal(cache.size(), 3);

  // Flood 30 random unknown custom domains
  for (let i = 0; i < 30; i++) {
    const res = await cache.resolve(`unknown-${i}.example.com`, async () => ({
      status: "not_found",
    }));
    assert.equal(res.status, "not_found");
  }

  // Total size is capped at 3 positive + 3 negative = 6; live positive sites are NEVER evicted.
  assert.equal(cache.size(), 6);
  for (const h of liveHosts) {
    assert.equal(cache.get(h)?.status, "found", `expected live positive hit preserved for ${h}`);
  }
  assert.equal(cache.get("unknown-0.example.com"), null);
  assert.equal(cache.get("unknown-26.example.com"), null);
  assert.equal(cache.get("unknown-27.example.com")?.status, "not_found");
  assert.equal(cache.get("unknown-28.example.com")?.status, "not_found");
  assert.equal(cache.get("unknown-29.example.com")?.status, "not_found");

  // Even when live positive count + maxNegativeEntries > maxEntries, negative floods still evict
  // oldest negative entries first instead of touching live positive entries.
  const tightCache = createSiteHostCache({
    positiveTtlMs: 30_000,
    negativeTtlMs: 10_000,
    maxEntries: 5,
    maxNegativeEntries: 4,
    now: () => clock,
  });
  for (let i = 0; i < 4; i++) {
    tightCache.setFound(`live-${i}.verified.test`, {
      siteId: `s-${i}`,
      shopId: `sh-${i}`,
      primaryHost: `live-${i}.verified.test`,
    });
  }
  for (let i = 0; i < 20; i++) {
    tightCache.setNotFound(`flood-${i}.example.com`);
  }
  assert.equal(tightCache.size(), 5);
  for (let i = 0; i < 4; i++) {
    assert.equal(tightCache.get(`live-${i}.verified.test`)?.status, "found");
  }
  assert.equal(tightCache.get("flood-19.example.com")?.status, "not_found");
  assert.equal(tightCache.get("flood-18.example.com"), null);
});

test("WCAG contrast, safe URL sanitization, and CSS theme vars enforce security and readability", () => {
  assert.equal(contrastRatio("#FFFFFF", "#000000"), 21);

  // All curated presets pass WCAG >= 4.5
  for (const preset of SITE_THEME_PRESETS) {
    const check = checkThemeContrast(preset.colors, 4.5);
    assert.equal(check.ok, true, `preset ${preset.id} must pass WCAG >= 4.5`);
  }

  // Low contrast palette rejected
  const badContrast = checkThemeContrast(
    {
      bg_color: "#FFFFFF",
      surface_color: "#FAFAFA",
      text_color: "#CCCCCC",
      accent_color: "#DDDDDD",
      accent_text_color: "#FFFFFF",
    },
    4.5
  );
  assert.equal(badContrast.ok, false);

  // Safe URLs accepted
  assert.equal(sanitizeSafeUrl("/banners/vietnam-workshop.jpg"), "/banners/vietnam-workshop.jpg");
  assert.equal(sanitizeSafeHref("/san-pham?cat=ao-thun"), "/san-pham?cat=ao-thun");
  assert.equal(
    sanitizeSafeUrl("https://cdn.nexusmart.vn/shops/logo.png"),
    "https://cdn.nexusmart.vn/shops/logo.png"
  );

  // Dangerous URLs rejected
  for (const evil of [
    "javascript:alert(1)",
    "JavaScript:alert(document.cookie)",
    "data:text/html,<script>alert(1)</script>",
    "vbscript:msgbox(1)",
    "http://insecure.example.com/img.jpg",
    "//evil.example.com/steal.js",
    "/bad\\path",
    "/bad\npath",
    `https://example.com/${"a".repeat(500)}`,
  ]) {
    assert.equal(sanitizeSafeUrl(evil), "", `expected dangerous URL rejected: ${evil}`);
  }

  // Theme vars apply safely and keep accessible muted contrast on both light and dark palettes
  const darkVars = buildSiteThemeVars({
    bg_color: "#14181C",
    surface_color: "#1E242B",
    text_color: "#F4F6F8",
    accent_color: "#D97706",
    accent_text_color: "#111418",
    font_family: "be-vietnam-pro",
    brand_name: "Kho Basic Sài Gòn",
  });
  assert.equal(darkVars["--site-bg"], "#14181C");
  assert.equal(darkVars["--site-font"], SITE_FONT_STACK);
  assert.ok(contrastRatio(darkVars["--site-muted"], darkVars["--site-bg"]) >= 4.5);
  assert.ok(contrastRatio(darkVars["--site-muted"], darkVars["--site-surface"]) >= 4.5);

  // resolveSiteFontStack always resolves to Archivo regardless of enum value, undefined, or garbage
  assert.equal(SITE_FONT_STACK, "var(--font-archivo), system-ui, sans-serif");
  for (const fontEnum of ALLOWED_SITE_FONTS) {
    assert.equal(
      resolveSiteFontStack(fontEnum),
      SITE_FONT_STACK,
      `expected Archivo stack for enum ${fontEnum}`
    );
    assert.equal(
      SITE_FONT_LABELS[fontEnum],
      "Archivo",
      `expected Archivo label for enum ${fontEnum}`
    );
  }
  for (const weird of [undefined, "", "Comic Sans MS", "Times New Roman", "work_sans", "12345"]) {
    assert.equal(
      resolveSiteFontStack(weird),
      SITE_FONT_STACK,
      `expected Archivo stack for input ${String(weird)}`
    );
  }

  // Malformed hex falls back safely
  const fallbackVars = buildSiteThemeVars({
    bg_color: "red; background: url(javascript:alert(1))",
    surface_color: "#FFFFFF",
    text_color: "#1F3038",
    accent_color: "#0D7480",
    accent_text_color: "#FFFFFF",
    font_family: "work-sans",
    brand_name: "Shop",
  });
  assert.equal(fallbackVars["--site-bg"], "#F7F9FA");

  // parseSiteConfig caps sections and keeps text literal
  const manySections = Array.from({ length: 25 }, (_, i) => ({
    id: `t-${i}`,
    type: "text",
    visible: true,
    props: { title: `Tiêu đề ${i}`, body: "<script>alert(1)</script>" },
  }));
  const parsed = parseSiteConfig(
    JSON.stringify({
      theme: createDefaultSiteConfig("Shop A").theme,
      sections: manySections,
    })
  );
  assert.ok(parsed);
  assert.equal(parsed.sections.length, MAX_SITE_SECTIONS);
  assert.equal(parsed.sections[0].type, "text");
  if (parsed.sections[0].type === "text") {
    assert.equal(parsed.sections[0].props.body, "<script>alert(1)</script>");
  }
});

test("SEO helpers enforce noindex for empty/hidden sites and site-own canonical on site PDP", () => {
  const richConfig = createDefaultSiteConfig("Xưởng Dệt Bạch Đằng");
  assert.equal(hasSubstantiveSiteContent(richConfig), true);
  assert.deepEqual(buildSiteRobotsMeta(richConfig), { index: true, follow: true });

  const emptyConfig = { ...richConfig, sections: [] };
  assert.equal(hasSubstantiveSiteContent(emptyConfig), false);
  assert.deepEqual(buildSiteRobotsMeta(emptyConfig), { index: false, follow: true });

  const hiddenConfig = {
    ...richConfig,
    sections: richConfig.sections.map((s) => ({ ...s, visible: false })),
  };
  assert.equal(hasSubstantiveSiteContent(hiddenConfig), false);
  assert.deepEqual(buildSiteRobotsMeta(hiddenConfig), { index: false, follow: true });

  const homeMeta = buildSiteMetadata({
    config: richConfig,
    siteOrigin: "http://bach-dang.nexusmart.localhost:3300",
    pathname: "/",
  });
  assert.equal(homeMeta.title, "Xưởng Dệt Bạch Đằng");
  assert.deepEqual(homeMeta.robots, { index: true, follow: true });
  assert.equal(homeMeta.openGraph?.siteName, "Xưởng Dệt Bạch Đằng");
  assert.equal(homeMeta.alternates?.canonical, "http://bach-dang.nexusmart.localhost:3300/");

  // Custom primary_host emits `<link rel="canonical">` automatically on every public site page.
  const customHomeMeta = buildSiteMetadata({
    config: richConfig,
    siteOrigin: "https://hanoi-knit.verified.test/",
    pathname: "/",
  });
  assert.equal(customHomeMeta.alternates?.canonical, "https://hanoi-knit.verified.test/");
  assert.equal(customHomeMeta.openGraph?.url, "https://hanoi-knit.verified.test/");

  const customCatalogMeta = buildSiteMetadata({
    config: richConfig,
    pageTitle: "Sản phẩm",
    siteOrigin: "https://hanoi-knit.verified.test",
    pathname: "/san-pham",
  });
  assert.equal(
    customCatalogMeta.alternates?.canonical,
    "https://hanoi-knit.verified.test/san-pham"
  );

  // Explicit canonicalUrl overrides the default derived from siteOrigin + pathname.
  const overrideMeta = buildSiteMetadata({
    config: richConfig,
    siteOrigin: "https://hanoi-knit.verified.test",
    pathname: "/san-pham",
    canonicalUrl: "https://hanoi-knit.verified.test/san-pham?cat=ao-len",
  });
  assert.equal(
    overrideMeta.alternates?.canonical,
    "https://hanoi-knit.verified.test/san-pham?cat=ao-len"
  );

  // Site PDP canonical MUST point to the site's OWN URL (custom primary_host when active), never root.
  const pdpMeta = buildSiteProductMetadata({
    config: richConfig,
    productName: "Áo thun Cotton Compact",
    productSlug: "ao-thun-cotton-compact-trang",
    siteOrigin: "https://hanoi-knit.verified.test/",
  });
  assert.equal(
    pdpMeta.alternates?.canonical,
    "https://hanoi-knit.verified.test/san-pham/ao-thun-cotton-compact-trang"
  );
  assert.equal(pdpMeta.title, "Áo thun Cotton Compact · Xưởng Dệt Bạch Đằng");
  assert.deepEqual(pdpMeta.robots, { index: true, follow: true });

  // Empty-section site PDP still emits noindex while keeping site-own canonical.
  const emptyPdpMeta = buildSiteProductMetadata({
    config: emptyConfig,
    productName: "Áo thun Cotton Compact",
    productSlug: "ao-thun-cotton-compact-trang",
    siteOrigin: "http://bach-dang.nexusmart.localhost:3300",
  });
  assert.deepEqual(emptyPdpMeta.robots, { index: false, follow: true });
  assert.equal(
    emptyPdpMeta.alternates?.canonical,
    "http://bach-dang.nexusmart.localhost:3300/san-pham/ao-thun-cotton-compact-trang"
  );
});
