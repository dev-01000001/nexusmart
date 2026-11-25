import { after, before, describe, it } from "node:test";
import assert from "node:assert/strict";
import { ROOT_HOST, config, siteHost } from "./lib/config.mjs";
import { Browser, expectStatus, gw, hasObjectKeyDeep } from "./lib/http.mjs";
import { resetRateLimitKeys } from "./lib/redis.mjs";
import {
  SEEDED,
  assertNoInternalErrorLeak,
  demoCredentials,
  registerBuyer,
  resolveSite,
} from "./lib/fixtures.mjs";

const SITE_A_HOST = siteHost(SEEDED.bachDang.sub);

describe("22. Wave 3b-3 B1 Search (site search & isolation, root search retired 404, gateway filters/suggest/history & admin reindex RBAC)", () => {
  let siteA;
  let siteB;

  before(async () => {
    await resetRateLimitKeys();
    siteA = await resolveSite(SEEDED.bachDang.sub);
    siteB = await resolveSite(SEEDED.saiGon.sub);
  });

  after(async () => {
    await resetRateLimitKeys();
  });

  it("1. Root host retires /danh-muc, /tim-kiem, /api/search*; site host serves /tim-kiem, /api/search, /api/search/suggest & static asset", async () => {
    const anonRoot = new Browser("b1-root").at(ROOT_HOST);
    const anonA = new Browser("b1-site-a").at(SITE_A_HOST);

    // Root host must return 404 for removed marketplace catalog/search pages and APIs.
    for (const path of ["/danh-muc", "/tim-kiem", "/api/search", "/api/search/suggest", "/api/search/history"]) {
      const r = await anonRoot.get(path, { headers: { accept: "*/*" } });
      assert.equal(r.status, 404, `root ${path} must return 404, got ${r.status}`);
    }

    // Site host does not expose /api/search/history (retired on site per DECISIONS.md).
    assert.equal(
      (await anonA.get("/api/search/history")).status,
      404,
      `site ${SITE_A_HOST}/api/search/history must return 404`
    );

    // Site SSR /tim-kiem renders 200.
    const ssr = await anonA.page("/tim-kiem?q=ao");
    expectStatus(ssr, 200, `GET ${SITE_A_HOST}/tim-kiem?q=ao`);
    assertNoInternalErrorLeak(ssr.text, "site /tim-kiem SSR");

    // Site PDP with ?color= query param renders 200 (quick-add multi-color resolution).
    expectStatus(
      await anonA.page(`/san-pham/${SEEDED.bachDang.productSlug}?color=trang`),
      200,
      "site PDP with ?color="
    );

    // Loadgen / seed static product asset returns 200.
    const asset = await anonA.get("/products/do-lot-boxer-modal-3.png", { headers: { accept: "*/*" } });
    expectStatus(asset, 200, "static asset /products/do-lot-boxer-modal-3.png");

    // Site GET /api/search: Vietnamese unaccented query, strict shopId isolation, no commission_rate_bps leak.
    const searchRes = expectStatus(
      await anonA.get(`/api/search?q=ao&shop_id=${encodeURIComponent(siteB.shopId)}&sort=price_asc&page=1&page_size=20`),
      200,
      `GET ${SITE_A_HOST}/api/search?q=ao&sort=price_asc`
    );
    assert.ok(Array.isArray(searchRes.items) && searchRes.items.length > 0, "site /api/search?q=ao must return items");
    assert.equal(hasObjectKeyDeep(searchRes, "commission_rate_bps"), false, "site search leaked commission_rate_bps");
    for (const item of searchRes.items) {
      assert.equal(item.shopId, siteA.shopId, `site search leaked foreign shop ${item.shopId}`);
    }
    // Strict price_asc ordering assertion (surfaces BFF query-param mismatch if sort != sort_by).
    for (let i = 1; i < searchRes.items.length; i += 1) {
      assert.ok(
        searchRes.items[i].priceVnd >= searchRes.items[i - 1].priceVnd,
        `site /api/search?sort=price_asc out of order at index ${i}: ${searchRes.items[i - 1].priceVnd} > ${searchRes.items[i].priceVnd}`
      );
    }

    // Strict suggest non-empty assertion (surfaces BFF normalizeSuggestResponse mismatch if gateway schema unmapped).
    const sug = expectStatus(
      await anonA.get("/api/search/suggest?q=ao"),
      200,
      `GET ${SITE_A_HOST}/api/search/suggest?q=ao`
    );
    assert.ok(
      Array.isArray(sug.suggestions) && sug.suggestions.length > 0,
      `site /api/search/suggest?q=ao expected >= 1 suggestion, got ${JSON.stringify(sug)}`
    );
  });

  it("2. Gateway B1 Search contract: Vietnamese folding, typo/synonym, price/sort/in_stock filters, suggest, history & admin reindex RBAC", async () => {
    // Vietnamese unaccented + sort_by=price_asc + in_stock_only on gateway scoped to siteA.
    const gwSearch = await gw(
      "GET",
      `/api/v1/search?q=${encodeURIComponent("ao thun")}&shop_id=${encodeURIComponent(siteA.shopId)}&sort_by=price_asc&in_stock_only=true&page=1&page_size=20`
    );
    assert.equal(gwSearch.status, 200, `gateway /api/v1/search failed: ${gwSearch.text}`);
    assert.equal(hasObjectKeyDeep(gwSearch.data, "commission_rate_bps"), false);
    const items = gwSearch.data?.items ?? [];
    assert.ok(items.length > 0, "gateway search for 'ao thun' in shop A must return >= 1 hit");
    for (let i = 0; i < items.length; i += 1) {
      assert.equal(items[i].shop_id, siteA.shopId);
      assert.equal(items[i].in_stock, true);
      if (i > 0) {
        assert.ok(items[i].min_price_vnd >= items[i - 1].min_price_vnd, "gateway sort_by=price_asc must be monotonic");
      }
    }
    if (config.expectProductFallback) {
      assert.equal(gwSearch.data?.degraded_mode, "product_fallback");
    }

    // Invalid price range -> 400.
    const badRange = await gw("GET", "/api/v1/search?min_price_vnd=500000&max_price_vnd=100000");
    assert.equal(badRange.status, 400, "min_price_vnd > max_price_vnd must return 400");

    // Gateway suggest returns structured keywords/categories/shops/trending/products.
    const gwSug = await gw(
      "GET",
      `/api/v1/search/suggest?q=${encodeURIComponent("ao")}&shop_id=${encodeURIComponent(siteA.shopId)}&limit=8`
    );
    assert.equal(gwSug.status, 200, `gateway /api/v1/search/suggest failed: ${gwSug.text}`);
    assert.ok(Array.isArray(gwSug.data?.keywords), "suggest missing keywords array");
    assert.ok(Array.isArray(gwSug.data?.products), "suggest missing products array");
    assert.ok(
      gwSug.data.keywords.length > 0 || gwSug.data.products.length > 0,
      `gateway suggest expected >= 1 keyword or product: ${gwSug.text}`
    );

    // Authenticated search history record + list + clear via gateway.
    const buyerA = new Browser("b1-hist-buyer").at(SITE_A_HOST);
    try {
      const acct = await registerBuyer(buyerA, "b1-hist");
      const buyerGwLogin = await gw("POST", "/api/v1/auth/login", {
        body: { email: acct.creds.email, password: acct.creds.password },
      });
      assert.equal(buyerGwLogin.status, 200);
      const buyerToken = buyerGwLogin.data.access_token;

      const histQuery = "ao thun cotton";
      const sWithAuth = await gw("GET", `/api/v1/search?q=${encodeURIComponent(histQuery)}&page=1&page_size=5`, {
        token: buyerToken,
      });
      assert.equal(sWithAuth.status, 200);

      if (!config.expectProductFallback && sWithAuth.data?.degraded_mode !== "product_fallback") {
        const histList = await gw("GET", "/api/v1/search/history?limit=10", { token: buyerToken });
        assert.equal(histList.status, 200, `GET /api/v1/search/history: ${histList.text}`);
        assert.ok(Array.isArray(histList.data?.items), "search history must return items array");

        const clearRes = await gw("DELETE", "/api/v1/search/history", { token: buyerToken });
        assert.ok([200, 204].includes(clearRes.status), `DELETE /api/v1/search/history: ${clearRes.text}`);
      }

      // Admin search reindex RBAC via gateway: anon 401, buyer 403, admin 200/202.
      assert.equal((await gw("POST", "/api/v1/admin/search/reindex", { body: {} })).status, 401);
      assert.equal(
        (await gw("POST", "/api/v1/admin/search/reindex", { token: buyerToken, body: {} })).status,
        403
      );

      const adminGwLogin = await gw("POST", "/api/v1/auth/login", {
        body: { email: demoCredentials.admin.email, password: demoCredentials.admin.password },
      });
      assert.equal(adminGwLogin.status, 200);
      const reindex = await gw("POST", "/api/v1/admin/search/reindex", {
        token: adminGwLogin.data.access_token,
        body: { batch_size: 200 },
      });
      assert.ok(
        [200, 202].includes(reindex.status),
        `admin POST /api/v1/admin/search/reindex expected 200/202, got ${reindex.status}: ${reindex.text}`
      );
    } finally {
      await buyerA.post("/api/auth/logout", {}).catch(() => {});
    }
  });
});
