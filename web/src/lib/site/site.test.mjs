// Run: node --import 'data:text/javascript,import{register}from"node:module";import{pathToFileURL}from"node:url";register(pathToFileURL("tools/seed/ts-resolve.mjs"));' --test web/src/lib/site/site.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  buildStorefrontCartUrl,
  classifyHost,
  deriveMarketplaceOrigin,
  deriveSiteOrigin,
  isBlockedSitePath,
  isDirectInternalSitePath,
  isReservedSubdomain,
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
import {
  buildMarketplaceCheckoutPayload,
  resolveCheckoutPayload,
} from "../../features/commerce/checkoutPayload.ts";

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

  // Root marketplace hosts
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

test("blocked site paths and direct /s internal paths are strictly enforced", () => {
  for (const p of ["/s", "/s/", "/s/bach-dang.nexusmart.localhost", "/s/foo/san-pham"]) {
    assert.equal(isDirectInternalSitePath(p), true, `expected direct /s blocked: ${p}`);
    assert.equal(isBlockedSitePath(p), true, `expected site blocked: ${p}`);
  }
  assert.equal(isDirectInternalSitePath("/san-pham"), false);

  for (const blocked of [
    "/api",
    "/api/checkout",
    "/api/v1/sites",
    "/seller",
    "/seller/site",
    "/admin",
    "/admin/sites",
  ]) {
    assert.equal(isBlockedSitePath(blocked), true, `expected blocked on site host: ${blocked}`);
  }

  for (const allowed of [
    "/",
    "/san-pham",
    "/san-pham/ao-thun-cotton-compact-trang",
    "/chinh-sach",
    "/gioi-thieu",
    "/robots.txt",
    "/sitemap.xml",
  ]) {
    assert.equal(isBlockedSitePath(allowed), false, `expected allowed on site host: ${allowed}`);
  }
});

test("deriveMarketplaceOrigin, deriveSiteOrigin, and buildStorefrontCartUrl preserve proto/port cleanly", () => {
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

  const handoff = buildStorefrontCartUrl({
    marketplaceOrigin: "http://nexusmart.localhost:3300",
    shopId: "shop-uuid-1",
    siteHost: "bach-dang.nexusmart.localhost",
    skuId: "sku-uuid-9",
    qty: 2,
  });
  const parsed = new URL(handoff);
  assert.equal(parsed.origin, "http://nexusmart.localhost:3300");
  assert.equal(parsed.pathname, "/gio-hang");
  assert.equal(parsed.searchParams.get("shop"), "shop-uuid-1");
  assert.equal(parsed.searchParams.get("channel"), "storefront");
  assert.equal(parsed.searchParams.get("sku"), "sku-uuid-9");
  assert.equal(parsed.searchParams.get("qty"), "2");
  assert.equal(parsed.searchParams.get("site"), "bach-dang.nexusmart.localhost");
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

test("SEO helpers enforce noindex for empty/hidden sites and marketplace canonical on site PDP", () => {
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

  const pdpMeta = buildSiteProductMetadata({
    config: richConfig,
    productName: "Áo thun Cotton Compact",
    productSlug: "ao-thun-cotton-compact-trang",
    marketplaceOrigin: "http://nexusmart.localhost:3300",
    siteOrigin: "http://bach-dang.nexusmart.localhost:3300",
  });
  assert.equal(
    pdpMeta.alternates?.canonical,
    "http://nexusmart.localhost:3300/san-pham/ao-thun-cotton-compact-trang"
  );
  assert.equal(pdpMeta.title, "Áo thun Cotton Compact · Xưởng Dệt Bạch Đằng");
  assert.deepEqual(pdpMeta.robots, { index: true, follow: true });
});

test("BFF checkout payload resolver keeps Phase 1 marketplace lock and verifies published sites for storefront mode", async () => {
  // 1. Phase 1 backward compatibility: raw sales_channel="STOREFRONT" WITHOUT hint is stripped to MARKETPLACE
  let verifyCalls = 0;
  const rawSpoof = await resolveCheckoutPayload(
    {
      address_id: "addr-1",
      from_cart: true,
      payment_method: "COD",
      sales_channel: "STOREFRONT",
      storefront_shop_id: "shop-1",
      platform_voucher_id: "v-plat",
      coins_to_use: 5000,
    },
    async () => {
      verifyCalls++;
      return { shopId: "shop-1", primaryHost: "bach-dang.nexusmart.localhost" };
    }
  );
  assert.equal(verifyCalls, 0, "raw sales_channel without storefront hint must NOT invoke site lookup");
  assert.equal(rawSpoof.ok, true);
  if (rawSpoof.ok) {
    assert.equal(rawSpoof.payload.sales_channel, "MARKETPLACE");
    assert.equal("storefront_shop_id" in rawSpoof.payload, false);
  }

  // Also confirm buildMarketplaceCheckoutPayload remains unchanged
  const mkt = buildMarketplaceCheckoutPayload({
    address_id: "addr-1",
    payment_method: "VIETQR",
    sales_channel: "STOREFRONT",
    storefront_shop_id: "shop-1",
  });
  assert.equal(mkt.ok, true);
  if (mkt.ok) {
    assert.equal(mkt.payload.sales_channel, "MARKETPLACE");
  }

  // 2. Storefront checkout hint with verified PUBLISHED site -> STOREFRONT + platform promos stripped
  const sfOk = await resolveCheckoutPayload(
    {
      address_id: "addr-1",
      from_cart: true,
      payment_method: "VIETQR",
      checkout_channel_hint: "storefront",
      storefront_shop_id: "shop-1",
      storefront_host: "bach-dang.nexusmart.localhost",
      shop_voucher_ids: { "shop-1": "v-shop-1", "shop-2": "v-shop-2" },
      platform_voucher_id: "v-plat",
      freeship_voucher_id: "v-ship",
      coins_to_use: 10000,
      shipping_choices: [
        { shop_id: "shop-1", carrier_code: "GHN", service_code: "STD" },
        { shop_id: "shop-2", carrier_code: "SPX", service_code: "STD" },
      ],
    },
    async (shopId, host) => {
      assert.equal(shopId, "shop-1");
      assert.equal(host, "bach-dang.nexusmart.localhost");
      return { shopId: "shop-1", primaryHost: "bach-dang.nexusmart.localhost" };
    }
  );
  assert.equal(sfOk.ok, true);
  if (sfOk.ok) {
    assert.equal(sfOk.payload.sales_channel, "STOREFRONT");
    assert.equal(sfOk.payload.storefront_shop_id, "shop-1");
    assert.deepEqual(sfOk.payload.shop_voucher_ids, { "shop-1": "v-shop-1" });
    assert.equal(sfOk.payload.platform_voucher_id, "");
    assert.equal(sfOk.payload.freeship_voucher_id, "");
    assert.equal(sfOk.payload.coins_to_use, 0);
    assert.deepEqual(sfOk.payload.shipping_choices, [
      { shop_id: "shop-1", carrier_code: "GHN", service_code: "STD" },
    ]);
  }

  // 3. Storefront checkout hint for unpublished/suspended/unknown shop -> rejected with SITE_NOT_PUBLISHED
  const sfRejected = await resolveCheckoutPayload(
    {
      address_id: "addr-1",
      payment_method: "COD",
      checkout_channel_hint: "storefront",
      storefront_shop_id: "shop-draft",
    },
    async () => null
  );
  assert.deepEqual(sfRejected, { ok: false, code: "SITE_NOT_PUBLISHED" });

  // 4. Storefront checkout hint with missing shop ID -> rejected with SITE_NOT_PUBLISHED
  const sfMissing = await resolveCheckoutPayload(
    {
      address_id: "addr-1",
      payment_method: "COD",
      checkout_channel_hint: "storefront",
    },
    async () => {
      throw new Error("should not be called when shopId is empty");
    }
  );
  assert.deepEqual(sfMissing, { ok: false, code: "SITE_NOT_PUBLISHED" });
});
