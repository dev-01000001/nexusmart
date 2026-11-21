// Run: node --import 'data:text/javascript,import{register}from"node:module";import{pathToFileURL}from"node:url";register(pathToFileURL("tools/seed/ts-resolve.mjs"));' --test web/src/lib/site/site.test.mjs
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
  normalizeHostHeader,
} from "./host.ts";
import { createSiteHostCache } from "./cache.ts";
import {
  buildSiteThemeVars,
  checkThemeContrast,
  contrastRatio,
  sanitizeSafeHref,
  sanitizeSafeUrl,
} from "./theme.ts";
import {
  MAX_SITE_SECTIONS,
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

test("normalizeHostHeader lowercases, strips port and trailing dot, handles IPv6, rejects malformed", () => {
  assert.deepEqual(normalizeHostHeader("Bach-Dang.NexusMart.Localhost.:3300"), {
    hostname: "bach-dang.nexusmart.localhost",
    port: "3300",
  });
  assert.deepEqual(normalizeHostHeader("[::1]:3300"), { hostname: "::1", port: "3300" });
  assert.deepEqual(normalizeHostHeader("::1"), { hostname: "::1", port: "" });
  assert.deepEqual(normalizeHostHeader("127.0.0.1:3300"), { hostname: "127.0.0.1", port: "3300" });
  assert.deepEqual(normalizeHostHeader("evil.com/path"), { hostname: "", port: "" });
  assert.deepEqual(normalizeHostHeader("host:notaport"), { hostname: "", port: "" });
  assert.deepEqual(normalizeHostHeader(""), { hostname: "", port: "" });
});

test("classifyHost identifies root hosts, valid single-label site hosts, and rejects reserved/unknown/multi-level", () => {
  const root = "nexusmart.localhost";

  // Root platform hosts
  for (const h of [
    "nexusmart.localhost",
    "NEXUSMART.LOCALHOST:3300",
    "nexusmart.localhost.",
    "localhost:3300",
    "web:3000",
    "127.0.0.1:3300",
    "100.93.43.5:3111",
    "[::1]:3300",
    "::1",
  ]) {
    const cls = classifyHost(h, root);
    assert.equal(cls.kind, "root", `expected root for ${h}, got ${cls.kind}`);
  }

  // Valid single-label subdomain sites
  const bd = classifyHost("Bach-Dang.NexusMart.Localhost:3300", root);
  assert.equal(bd.kind, "site");
  if (bd.kind === "site") {
    assert.equal(bd.subdomain, "bach-dang");
    assert.equal(bd.hostname, "bach-dang.nexusmart.localhost");
    assert.equal(bd.port, "3300");
  }

  const sg = classifyHost("sai-gon.nexusmart.localhost", root);
  assert.equal(sg.kind, "site");

  // Reserved, brand-impersonation, foreign, multi-level, and invalid syntax -> unknown (404 without backend)
  for (const bad of [
    "evil.example.com",
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
    "",
  ]) {
    const cls = classifyHost(bad, root);
    assert.equal(cls.kind, "unknown", `expected unknown for ${bad}, got ${cls.kind}`);
  }

  assert.equal(isReservedSubdomain("vietcombank-uudai"), true);
  assert.equal(isReservedSubdomain("bach-dang"), false);
});

test("site vs root API routing, blocked paths, protected pages, and direct /s internal paths are strictly enforced", () => {
  // Direct /s/... is ALWAYS blocked on every host.
  for (const p of ["/s", "/s/", "/s/bach-dang.nexusmart.localhost", "/s/foo/san-pham"]) {
    assert.equal(isDirectInternalSitePath(p), true, `expected direct /s blocked: ${p}`);
    assert.equal(isBlockedSitePath(p), true, `expected site blocked: ${p}`);
  }
  assert.equal(isDirectInternalSitePath("/san-pham"), false);

  // Blocked on site hosts: seller/admin pages & APIs, internal /s, and non-allowlisted /api paths.
  for (const blocked of [
    "/api",
    "/api/seller",
    "/api/seller/site",
    "/api/admin",
    "/api/admin/sites",
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
    "/api/cart",
    "/api/cart/items",
    "/api/cart/items/sku-1",
    "/api/checkout",
    "/api/checkout/shipping-quote",
    "/api/checkout/locations",
    "/api/orders",
    "/api/orders/ord-1",
    "/api/orders/ord-1/cancel",
    "/api/payments/pay-1",
    "/api/payments/pay-1/retry",
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
    "/api/cart",
    "/api/cart/items",
    "/api/checkout",
    "/api/checkout/addresses",
    "/api/orders",
    "/api/orders/ord-1",
    "/api/payments/pay-1",
    "/api/vouchers",
    "/api/vouchers/validate",
  ]) {
    assert.equal(isBuyerApiPath(buyerApi), true, `expected buyer API blocked on root: ${buyerApi}`);
  }
  for (const rootApi of ["/api/auth/login", "/api/auth/me", "/api/seller/site", "/api/admin/sites"]) {
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
  for (const p of ["/tai-khoan", "/tai-khoan/dia-chi", "/don-hang", "/don-hang/o1", "/thanh-toan/cho/p1"]) {
    assert.equal(isProtectedPagePath(p, "site"), true, `expected protected on site: ${p}`);
    assert.equal(isProtectedPagePath(p, "root"), false, `expected NOT protected-redirected on root: ${p}`);
  }
  assert.equal(isProtectedPagePath("/thanh-toan", "site"), false);
  for (const p of ["/seller", "/seller/site", "/admin", "/admin/sites"]) {
    assert.equal(isProtectedPagePath(p, "root"), true, `expected protected on root: ${p}`);
    assert.equal(isProtectedPagePath(p, "site"), false);
  }
});

test("deriveMarketplaceOrigin and deriveSiteOrigin preserve proto/port cleanly", () => {
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

  // Bounded maxEntries=2 evicts oldest
  cache.setNotFound("host-a.nexusmart.localhost");
  cache.setNotFound("host-b.nexusmart.localhost");
  assert.equal(cache.size(), 2);
  cache.setNotFound("host-c.nexusmart.localhost");
  assert.equal(cache.size(), 2);
  assert.equal(cache.get("host-a.nexusmart.localhost"), null);
  assert.notEqual(cache.get("host-c.nexusmart.localhost"), null);
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
  assert.ok(contrastRatio(darkVars["--site-muted"], darkVars["--site-bg"]) >= 4.5);
  assert.ok(contrastRatio(darkVars["--site-muted"], darkVars["--site-surface"]) >= 4.5);

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

  // Site PDP canonical MUST point to the site's OWN URL, never the root host.
  const pdpMeta = buildSiteProductMetadata({
    config: richConfig,
    productName: "Áo thun Cotton Compact",
    productSlug: "ao-thun-cotton-compact-trang",
    siteOrigin: "http://bach-dang.nexusmart.localhost:3300/",
  });
  assert.equal(
    pdpMeta.alternates?.canonical,
    "http://bach-dang.nexusmart.localhost:3300/san-pham/ao-thun-cotton-compact-trang"
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
