// Run: node --test src/lib/server/sites.test.mjs
// Real gateway JSON tests for live flash sale filtering and PDP non-resurrection (Mục 4).
import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { registerHooks } from "node:module";
import { dirname, resolve } from "node:path";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const WEB_SRC = resolve(__dirname, "../..");

const VIRTUAL_MODULES = {
  "virtual:react": `
    export function cache(fn) { return fn; }
    export default { cache };
  `,
  "virtual:next-headers": `
    export async function headers() { return globalThis.__testNextHeaders ?? new Headers(); }
    export async function cookies() { return { get() { return undefined; } }; }
  `,
  "virtual:next-navigation": `
    export function notFound() { throw new Error("NEXT_NOT_FOUND"); }
    export function redirect(url) { throw new Error("NEXT_REDIRECT:" + url); }
  `,
};

function resolveTsFile(basePath) {
  for (const candidate of [
    `${basePath}.ts`,
    `${basePath}.tsx`,
    resolve(basePath, "index.ts"),
    resolve(basePath, "index.tsx"),
  ]) {
    if (existsSync(candidate)) return pathToFileURL(candidate).href;
  }
  return null;
}

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === "react") return { shortCircuit: true, url: "virtual:react" };
    if (specifier === "next/headers") return { shortCircuit: true, url: "virtual:next-headers" };
    if (specifier === "next/navigation")
      return { shortCircuit: true, url: "virtual:next-navigation" };
    if (specifier.startsWith("@/")) {
      const resolved = resolveTsFile(resolve(WEB_SRC, specifier.slice(2)));
      if (resolved) return nextResolve(resolved, context);
    }
    if (
      (specifier.startsWith("./") || specifier.startsWith("../")) &&
      !/\.[a-z]+$/i.test(specifier) &&
      context.parentURL?.startsWith("file:")
    ) {
      const parentDir = dirname(fileURLToPath(context.parentURL));
      const resolved = resolveTsFile(resolve(parentDir, specifier));
      if (resolved) return nextResolve(resolved, context);
    }
    return nextResolve(specifier, context);
  },
  load(url, context, nextLoad) {
    if (url in VIRTUAL_MODULES) {
      return { shortCircuit: true, format: "module", source: VIRTUAL_MODULES[url] };
    }
    return nextLoad(url, context);
  },
});

const {
  fetchPublishedSiteByHost,
  filterShopLiveFlashSales,
  loadPublicSiteProduct,
  loadShopLiveFlashSales,
  resolvePdpLiveFlashItem,
  resolveSiteFromRequest,
} = await import("./sites.ts");
const { fetchPublishedSiteTemplateByHost } = await import(
  "../../features/site/siteContext.server.ts"
);
const { GatewayError } = await import("./errors.ts");
const { publishedSiteHostCache } = await import("./siteLookup.ts");
const { default: robots } = await import("../../app/robots.ts");
const { default: sitemap } = await import("../../app/sitemap.ts");

const SHOP_A = "shop-a";
const SITE_HOST = "shop-a.nexusmart.localhost";

function jsonRes(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

test("Mục 4: loadShopLiveFlashSales filters shop_id fail-closed and distinguishes live failure from empty list", async () => {
  // Real GET /api/v1/flash-sales?include_upcoming=false payload (handlers_promotion.go:45-69)
  const mixedGatewayPayload = {
    flash_sales: [
      {
        id: "fs-live-1",
        name: "Flash 12h",
        starts_at: "2026-11-26T05:00:00Z",
        ends_at: "2026-11-26T08:00:00Z",
        status: "ACTIVE",
        min_discount_percent: 10,
        category_ids: [],
        items: [
          {
            id: "fsi-shop-a",
            flash_sale_id: "fs-live-1",
            shop_id: SHOP_A,
            sku_id: "sku-a-1",
            flash_price_vnd: 149000,
            original_price_vnd: 219000,
            total_slots: 10,
            remaining_slots: 4,
            per_user_limit: 1,
            status: "APPROVED",
            rejection_reason: "",
          },
          {
            id: "fsi-missing-shop",
            flash_sale_id: "fs-live-1",
            shop_id: "",
            sku_id: "sku-leak",
            flash_price_vnd: 99000,
            original_price_vnd: 199000,
            total_slots: 10,
            remaining_slots: 4,
            per_user_limit: 1,
            status: "APPROVED",
            rejection_reason: "",
          },
          {
            id: "fsi-other-shop",
            flash_sale_id: "fs-live-1",
            shop_id: "shop-b",
            sku_id: "sku-b-1",
            flash_price_vnd: 120000,
            original_price_vnd: 200000,
            total_slots: 10,
            remaining_slots: 4,
            per_user_limit: 1,
            status: "APPROVED",
            rejection_reason: "",
          },
        ],
      },
      {
        id: "fs-ended-early",
        name: "Flash đã kết thúc sớm",
        starts_at: "2026-11-26T05:00:00Z",
        ends_at: "2026-11-26T08:00:00Z",
        status: "ENDED",
        min_discount_percent: 10,
        category_ids: [],
        items: [
          {
            id: "fsi-ended",
            flash_sale_id: "fs-ended-early",
            shop_id: SHOP_A,
            sku_id: "sku-a-2",
            flash_price_vnd: 139000,
            original_price_vnd: 219000,
            total_slots: 10,
            remaining_slots: 2,
            per_user_limit: 1,
            status: "APPROVED",
            rejection_reason: "",
          },
        ],
      },
    ],
  };

  // Pure fail-closed check: items with empty/missing/other shop_id or ENDED windows are dropped
  const filtered = filterShopLiveFlashSales(mixedGatewayPayload.flash_sales, SHOP_A);
  assert.equal(filtered.length, 1);
  assert.deepEqual(
    filtered[0].items.map((it) => it.id),
    ["fsi-shop-a"]
  );

  const origFetch = globalThis.fetch;
  try {
    // 1. Gateway returns mixed items -> ok: true with strictly filtered shop-a items
    globalThis.fetch = async () => jsonRes(mixedGatewayPayload);
    const okRes = await loadShopLiveFlashSales(SHOP_A);
    assert.equal(okRes.ok, true);
    assert.equal(okRes.sales.length, 1);
    assert.deepEqual(
      okRes.sales[0].items.map((it) => it.id),
      ["fsi-shop-a"]
    );

    // 2. Gateway succeeds with empty list [] -> ok: true, sales: []
    globalThis.fetch = async () => jsonRes({ flash_sales: [] });
    const emptyRes = await loadShopLiveFlashSales(SHOP_A);
    assert.equal(emptyRes.ok, true);
    assert.deepEqual(emptyRes.sales, []);

    // 3. Gateway returns 503 error -> ok: false, sales: [] (distinct from valid empty!)
    globalThis.fetch = async () =>
      jsonRes({ error: { code: "UNAVAILABLE", message: "down" } }, 503);
    const failRes = await loadShopLiveFlashSales(SHOP_A);
    assert.equal(failRes.ok, false);
    assert.deepEqual(failRes.sales, []);
  } finally {
    globalThis.fetch = origFetch;
  }
});

test("Mục 4: PDP does not resurrect ended flash sale from 30s cached bundle when live call returns [] and only falls back when live call fails", async () => {
  const nowMs = Date.parse("2026-11-26T06:00:00Z");
  const product = {
    id: "prod-1",
    backendId: "prod-1",
    slug: "ao-thun-a",
    name: "Áo thun A",
    shortName: "Áo thun A",
    categorySlug: "ao-thun",
    categoryName: "Áo thun",
    shopId: SHOP_A,
    gender: "unisex",
    price: 219000,
    image: "/products/ao.png",
    gallery: ["/products/ao.png"],
    colors: [],
    sizes: ["M"],
    skus: [{ skuId: "sku-a-1", skuCode: "A-M", colorCode: "den", size: "M", stock: 10, price: 219000 }],
    fabric: "",
    gsm: 200,
    fit: "",
    origin: "",
    careGuide: [],
    highlights: [],
    description: "",
    sizeChart: [],
  };

  const cachedLiveItem = {
    id: "fsi-stale",
    productId: "prod-1",
    flashPrice: 149000,
    originalPrice: 219000,
    totalQuota: 10,
    remainingQuota: 4,
    limitPerBuyer: 1,
    itemIdBySku: { "sku-a-1": "fsi-stale" },
  };
  const cachedSlotsActive = [
    {
      id: "fs-cached",
      label: "12:00",
      startTime: "12:00",
      endTime: "15:00",
      startsAt: "2026-11-26T05:00:00Z",
      endsAt: "2026-11-26T08:00:00Z",
      status: "live",
      statusLabel: "Đang mở bán",
      items: [cachedLiveItem],
    },
  ];
  const cachedSlotsAlreadyExpired = [
    {
      ...cachedSlotsActive[0],
      endsAt: "2026-11-26T05:59:59Z", // ended 1s before nowMs even though cached status says "live"
    },
  ];

  // Live succeeded with [] -> MUST return null (do NOT resurrect cachedLiveItem!)
  assert.equal(
    resolvePdpLiveFlashItem({
      liveFlash: { ok: true, sales: [] },
      product,
      cachedFlashSlots: cachedSlotsActive,
      nowMs,
    }),
    null
  );

  // Live failed (ok: false) + cached slot still within window -> falls back to cached item
  const fallback = resolvePdpLiveFlashItem({
    liveFlash: { ok: false, sales: [] },
    product,
    cachedFlashSlots: cachedSlotsActive,
    nowMs,
  });
  assert.equal(fallback?.id, "fsi-stale");

  // Live failed (ok: false) + cached slot endsAt already passed -> MUST NOT resurrect expired slot
  assert.equal(
    resolvePdpLiveFlashItem({
      liveFlash: { ok: false, sales: [] },
      product,
      cachedFlashSlots: cachedSlotsAlreadyExpired,
      nowMs,
    }),
    null
  );

  // End-to-end loadPublicSiteProduct with real gateway JSON:
  // 1st /api/v1/flash-sales (for bundle) returns active flash; 2nd /api/v1/flash-sales (uncached live) returns []
  const origFetch = globalThis.fetch;
  let flashCallCount = 0;
  const futureStart = new Date(Date.now() - 60_000).toISOString();
  const futureEnd = new Date(Date.now() + 3_600_000).toISOString();
  globalThis.fetch = async (rawUrl) => {
    const url = new URL(String(rawUrl));
    if (url.pathname === "/api/v1/sites/by-host") {
      return jsonRes({
        site: {
          id: "site-a",
          shop_id: SHOP_A,
          shop_name: "Shop A",
          subdomain: "shop-a",
          primary_host: SITE_HOST,
          status: "PUBLISHED",
          config: { schema_version: 2, template_id: "atelier", theme: { brand_name: "Shop A" } },
        },
        shop: { id: SHOP_A, name: "Shop A", description: "", status: "ACTIVE" },
      });
    }
    if (url.pathname === "/api/v1/categories") {
      return jsonRes({ categories: [{ id: "cat-1", name: "Áo thun", slug: "ao-thun" }] });
    }
    if (url.pathname === "/api/v1/products") {
      return jsonRes({
        items: [
          {
            id: "prod-1",
            shop_id: SHOP_A,
            category_id: "cat-1",
            name: "Áo thun A",
            slug: "ao-thun-a",
            description: "Mô tả",
            images: ["/products/ao.png"],
            status: "APPROVED",
            skus: [
              {
                id: "sku-a-1",
                sku_code: "A-M",
                name: "Đen / M",
                price_vnd: 219000,
                available_stock: 10,
                is_active: true,
              },
            ],
          },
        ],
        total: 1,
      });
    }
    if (url.pathname === "/api/v1/products/prod-1") {
      return jsonRes({
        product: {
          id: "prod-1",
          shop_id: SHOP_A,
          category_id: "cat-1",
          name: "Áo thun A",
          slug: "ao-thun-a",
          description: "Mô tả",
          images: ["/products/ao.png"],
          status: "APPROVED",
          skus: [
            {
              id: "sku-a-1",
              sku_code: "A-M",
              name: "Đen / M",
              price_vnd: 219000,
              available_stock: 10,
              is_active: true,
            },
          ],
        },
      });
    }
    if (url.pathname === "/api/v1/vouchers") return jsonRes({ vouchers: [] });
    if (url.pathname === "/api/v1/flash-sales") {
      flashCallCount += 1;
      if (flashCallCount === 1) {
        // Cached bundle saw live flash sale
        return jsonRes({
          flash_sales: [
            {
              id: "fs-1",
              name: "Flash 12h",
              starts_at: futureStart,
              ends_at: futureEnd,
              status: "ACTIVE",
              min_discount_percent: 10,
              category_ids: [],
              items: [
                {
                  id: "fsi-stale-1",
                  flash_sale_id: "fs-1",
                  shop_id: SHOP_A,
                  sku_id: "sku-a-1",
                  flash_price_vnd: 149000,
                  original_price_vnd: 219000,
                  total_slots: 10,
                  remaining_slots: 3,
                  per_user_limit: 1,
                  status: "APPROVED",
                  rejection_reason: "",
                },
              ],
            },
          ],
        });
      }
      // Uncached live call sees flash sale ended!
      return jsonRes({ flash_sales: [] });
    }
    if (url.pathname.endsWith("/reviews")) return jsonRes({ items: [], total: 0 });
    if (url.pathname === "/api/v1/shop-discounts/active") return jsonRes({ discounts: [] });
    if (url.pathname === "/api/v1/shipping/provinces") return jsonRes({ provinces: [] });
    return jsonRes({ error: { code: "NOT_FOUND", message: `unmocked ${url.pathname}` } }, 404);
  };

  try {
    const res = await loadPublicSiteProduct(SITE_HOST, "ao-thun-a");
    assert.ok(res && !res.discontinued);
    assert.equal(flashCallCount, 2);
    // Bundle still had cached slot, yet PDP liveFlashItem MUST be null because live call returned []
    assert.equal(res.bundle.flashSlots.length, 1);
    assert.equal(res.liveFlashItem, null);
  } finally {
    globalThis.fetch = origFetch;
  }
});

test("fetchPublishedSiteByHost and fetchPublishedSiteTemplateByHost return null only on not_found and rethrow transient errors", async () => {
  const origFetch = globalThis.fetch;
  const origLog = console.log;
  const origErr = console.error;
  console.log = () => {};
  console.error = () => {};

  try {
    // 1. 404 not_found -> returns null
    globalThis.fetch = async () =>
      jsonRes({ error: { code: "NOT_FOUND", message: "site not found" } }, 404);
    assert.equal(await fetchPublishedSiteByHost("missing-a.nexusmart.localhost"), null);
    assert.equal(await fetchPublishedSiteTemplateByHost("missing-a.nexusmart.localhost"), null);

    // 2. 429 rate_limited -> throws GatewayError (MUST NOT return null -> false 404)
    globalThis.fetch = async () =>
      jsonRes({ error: { code: "RATE_LIMITED", message: "too many requests" } }, 429);
    await assert.rejects(
      () => fetchPublishedSiteByHost("busy-a.nexusmart.localhost"),
      (err) => err instanceof GatewayError && err.kind === "rate_limited"
    );
    await assert.rejects(
      () => fetchPublishedSiteTemplateByHost("busy-a.nexusmart.localhost"),
      (err) => err instanceof GatewayError && err.kind === "rate_limited"
    );

    // 3. 503 unavailable -> throws GatewayError (MUST NOT return null)
    globalThis.fetch = async () =>
      jsonRes({ error: { code: "UNAVAILABLE", message: "upstream down" } }, 503);
    await assert.rejects(
      () => fetchPublishedSiteByHost("down-a.nexusmart.localhost"),
      (err) => err instanceof GatewayError && err.kind === "unavailable"
    );
    await assert.rejects(
      () => fetchPublishedSiteTemplateByHost("down-a.nexusmart.localhost"),
      (err) => err instanceof GatewayError && err.kind === "unavailable"
    );

    // 4. Network error -> throws GatewayError("unavailable")
    globalThis.fetch = async () => {
      throw new TypeError("fetch failed");
    };
    await assert.rejects(
      () => fetchPublishedSiteByHost("net-a.nexusmart.localhost"),
      (err) => err instanceof GatewayError && err.kind === "unavailable"
    );
    await assert.rejects(
      () => fetchPublishedSiteTemplateByHost("net-a.nexusmart.localhost"),
      (err) => err instanceof GatewayError && err.kind === "unavailable"
    );
  } finally {
    globalThis.fetch = origFetch;
    console.log = origLog;
    console.error = origErr;
  }
});

test("resolveSiteFromRequest with fresh: true never uses cached or stale entries (checkout safety)", async () => {
  const origFetch = globalThis.fetch;
  const host = "checkout-fresh.nexusmart.localhost";
  publishedSiteHostCache.clear();

  try {
    // Seed both positive and stale cache with a found entry.
    publishedSiteHostCache.setFound(host, {
      siteId: "site-fresh",
      shopId: "shop-fresh",
      primaryHost: host,
    });

    const req = new Request(`http://${host}/api/checkout`, { headers: { host } });

    // Upstream 429 on fresh lookup -> throws GatewayError("unavailable"), NEVER falls back to stale.
    globalThis.fetch = async () =>
      jsonRes({ error: { code: "RATE_LIMITED", message: "rate" } }, 429);
    await assert.rejects(
      () => resolveSiteFromRequest(req, { fresh: true }),
      (err) => err instanceof GatewayError && err.kind === "unavailable"
    );

    // Upstream 404 on fresh lookup (site just suspended) -> returns null, NEVER returns cached/stale.
    globalThis.fetch = async () =>
      jsonRes({ error: { code: "NOT_FOUND", message: "suspended" } }, 404);
    assert.equal(await resolveSiteFromRequest(req, { fresh: true }), null);
  } finally {
    globalThis.fetch = origFetch;
    publishedSiteHostCache.clear();
  }
});

test("resolveSiteFromRequest forwards X-Forwarded-For to /sites/by-host ONLY when TRUST_PROXY_HEADERS=true", async () => {
  const origFetch = globalThis.fetch;
  const prevTrust = process.env.TRUST_PROXY_HEADERS;
  const seenHeaders = [];
  const host = "xff-check.nexusmart.localhost";
  publishedSiteHostCache.clear();

  globalThis.fetch = async (_url, init) => {
    seenHeaders.push(new Headers(init?.headers));
    return jsonRes({
      site: {
        id: "site-xff",
        shop_id: "shop-xff",
        status: "PUBLISHED",
        primary_host: host,
      },
      shop: { status: "ACTIVE" },
    });
  };

  try {
    const req = new Request(`http://${host}/api/checkout`, {
      headers: {
        host,
        "x-forwarded-for": "203.0.113.9, 10.0.0.1",
      },
    });

    // 1. TRUST_PROXY_HEADERS=true -> fresh & cached-miss lookups forward X-Forwarded-For.
    process.env.TRUST_PROXY_HEADERS = "true";
    const freshHit = await resolveSiteFromRequest(req, { fresh: true });
    assert.equal(freshHit?.shopId, "shop-xff");
    assert.equal(seenHeaders[0].get("x-forwarded-for"), "203.0.113.9, 10.0.0.1");

    const cachedMissHit = await resolveSiteFromRequest(req);
    assert.equal(cachedMissHit?.shopId, "shop-xff");
    assert.equal(seenHeaders[1].get("x-forwarded-for"), "203.0.113.9, 10.0.0.1");

    // Cache hit avoids a duplicate upstream lookup.
    const cachedHit = await resolveSiteFromRequest(req);
    assert.equal(cachedHit?.shopId, "shop-xff");
    assert.equal(seenHeaders.length, 2);

    // 2. TRUST_PROXY_HEADERS=false -> X-Forwarded-For is NOT forwarded.
    process.env.TRUST_PROXY_HEADERS = "false";
    publishedSiteHostCache.clear();
    const untrustedFresh = await resolveSiteFromRequest(req, { fresh: true });
    assert.equal(untrustedFresh?.shopId, "shop-xff");
    assert.equal(seenHeaders[2].get("x-forwarded-for"), null);

    const untrustedCached = await resolveSiteFromRequest(req);
    assert.equal(untrustedCached?.shopId, "shop-xff");
    assert.equal(seenHeaders[3].get("x-forwarded-for"), null);
  } finally {
    if (prevTrust === undefined) delete process.env.TRUST_PROXY_HEADERS;
    else process.env.TRUST_PROXY_HEADERS = prevTrust;
    globalThis.fetch = origFetch;
    publishedSiteHostCache.clear();
  }
});

test("robots.ts and sitemap.ts return safe content on transient error and never Disallow: / for the whole site", async () => {
  const origFetch = globalThis.fetch;
  const origLog = console.log;
  const origErr = console.error;
  console.log = () => {};
  console.error = () => {};

  try {
    globalThis.__testNextHeaders = new Headers({ host: "bach-dang.nexusmart.localhost" });

    // 1. Transient 429 error -> robots allows "/" (NEVER Disallow: "/") and sitemap keeps core URLs.
    globalThis.fetch = async () =>
      jsonRes({ error: { code: "RATE_LIMITED", message: "rate" } }, 429);
    const robotsOnErr = await robots();
    const rulesOnErr = Array.isArray(robotsOnErr.rules) ? robotsOnErr.rules : [robotsOnErr.rules];
    assert.equal(rulesOnErr[0].allow, "/");
    assert.notEqual(rulesOnErr[0].disallow, "/");
    assert.equal(robotsOnErr.sitemap, "http://bach-dang.nexusmart.localhost/sitemap.xml");

    const sitemapOnErr = await sitemap();
    assert.ok(sitemapOnErr.length >= 4);
    assert.equal(sitemapOnErr[0].url, "http://bach-dang.nexusmart.localhost/");

    // 2. Confirmed 404 not_found -> robots returns Disallow: "/" and sitemap returns [].
    globalThis.fetch = async () =>
      jsonRes({ error: { code: "NOT_FOUND", message: "not found" } }, 404);
    const robotsOn404 = await robots();
    const rulesOn404 = Array.isArray(robotsOn404.rules) ? robotsOn404.rules : [robotsOn404.rules];
    assert.equal(rulesOn404[0].disallow, "/");

    const sitemapOn404 = await sitemap();
    assert.deepEqual(sitemapOn404, []);
  } finally {
    delete globalThis.__testNextHeaders;
    globalThis.fetch = origFetch;
    console.log = origLog;
    console.error = origErr;
  }
});
