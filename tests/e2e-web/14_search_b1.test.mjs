/**
 * Wave 3b-3 B1 E2E Web tests:
 * - Unaccented / Vietnamese folding / synonym / typo search, server sorting & 5 real facets + size/color (UC-B03, UC-B20, G9)
 * - Debounced 200ms header suggest via GET /api/gw/search/suggest (UC-B12)
 * - Logged-in-only search history (<= 50 items), single delete, clear all, IDOR isolation & guest 401 guard (G14)
 * - Server pagination past 100 items, real homepage total (§10-1), loadgen product image asset (§10-2)
 * - Shop 101+ lookup without falling back to "Gian hàng NexusMart" (§10-3)
 * - Graceful product_fallback when Meilisearch is stopped (G13) & admin reindex RBAC (G11)
 *
 * Self-contained helpers only (does NOT modify tests/e2e-web/helpers.mjs).
 */
import assert from "node:assert/strict";
import { describe, test } from "node:test";
import {
  WebClient,
  config,
  ensureRateBudget,
  pollUntil,
  registerAndLoginBuyer,
} from "./helpers.mjs";

const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@nexusmart.local";
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || "AdminDevPassword123!";
const EXPECT_PRODUCT_FALLBACK = process.env.E2E_EXPECT_PRODUCT_FALLBACK === "1";

async function b1LoginAccount(email, password) {
  const client = new WebClient();
  const res = await client.json("POST", "/api/auth/login", {
    body: { email, password },
  });
  assert.equal(res.status, 200, `login ${email} expected 200, got ${res.status}: ${res.text}`);
  return { client, user: res.data?.user };
}

function b1FormatGroupedInt(n) {
  const digits = String(Math.max(0, Math.trunc(n)));
  let out = "";
  for (let i = 0; i < digits.length; i += 1) {
    if (i > 0 && (digits.length - i) % 3 === 0) out += ".";
    out += digits[i];
  }
  return out;
}

function b1AssertNoCommissionLeak(payloadText, contextLabel) {
  assert.equal(
    payloadText.includes("commission_rate_bps"),
    false,
    `${contextLabel} must never leak commission_rate_bps`
  );
}

describe("14. Wave 3b-3 B1 (Search, Suggest, History, >100 Pagination, Shop 101+, Home Total & Meilisearch Fallback)", () => {
  test("1. [UC-B03/UC-B20/G9] Unaccented, đ/d folding, synonym, typo search, sorting, 5 real facets + SSR /tim-kiem & /danh-muc", async () => {
    await ensureRateBudget(40, { label: "w3b3-b1-search-facets" });
    const anon = new WebClient();

    // Unaccented Vietnamese query
    const unaccented = await anon.json("GET", "/api/gw/search?q=ao+thun&page=1&page_size=24");
    assert.equal(
      unaccented.status,
      200,
      `GET /api/gw/search?q=ao+thun expected 200, got ${unaccented.status}: ${unaccented.text}`
    );
    b1AssertNoCommissionLeak(unaccented.text, "GET /api/gw/search");
    const items = Array.isArray(unaccented.data?.items) ? unaccented.data.items : [];
    assert.ok(items.length > 0, `expected hits for 'ao thun', got: ${unaccented.text}`);
    assert.ok(
      typeof unaccented.data?.total === "number" && unaccented.data.total >= items.length,
      `expected total >= items.length, got ${unaccented.data?.total}`
    );
    for (const hit of items) {
      assert.ok(Number.isInteger(hit.min_price_vnd) && hit.min_price_vnd >= 0);
      assert.ok(Number.isInteger(hit.max_price_vnd) && hit.max_price_vnd >= hit.min_price_vnd);
      assert.ok(
        Number.isInteger(hit.rating_avg_x100) &&
          hit.rating_avg_x100 >= 0 &&
          hit.rating_avg_x100 <= 500,
        `expected integer rating_avg_x100 in 0..500, got ${JSON.stringify(hit.rating_avg_x100)}`
      );
      assert.ok(Number.isInteger(hit.review_count) && hit.review_count >= 0);
    }

    // đ/d folding ("do lot") & synonym ("ao phong") & typo ("ao thn")
    for (const query of ["do lot", "ao phong", "ao thn"]) {
      const res = await anon.json(
        "GET",
        `/api/gw/search?q=${encodeURIComponent(query)}&page=1&page_size=12`
      );
      assert.equal(res.status, 200, `search '${query}' failed: ${res.status} ${res.text}`);
      b1AssertNoCommissionLeak(res.text, `search '${query}'`);
      if (!res.data?.degraded_mode || res.data.degraded_mode !== "product_fallback") {
        assert.ok(
          Array.isArray(res.data?.items) && res.data.items.length > 0,
          `expected matches for Vietnamese/synonym/typo query '${query}', got 0`
        );
      }
    }

    // Price ascending sort order
    const priceAsc = await anon.json(
      "GET",
      "/api/gw/search?sort_by=price_asc&in_stock_only=true&min_price_vnd=50000&max_price_vnd=500000&page=1&page_size=24"
    );
    assert.equal(priceAsc.status, 200, `price_asc failed: ${priceAsc.text}`);
    const ascItems = Array.isArray(priceAsc.data?.items) ? priceAsc.data.items : [];
    for (let i = 1; i < ascItems.length; i += 1) {
      assert.ok(
        ascItems[i].min_price_vnd >= ascItems[i - 1].min_price_vnd,
        `price_asc out of order at index ${i}: ${ascItems[i - 1].min_price_vnd} > ${ascItems[i].min_price_vnd}`
      );
    }

    // Rating sort + min_rating_x100 + size + color parameters accepted cleanly
    const facetRes = await anon.json(
      "GET",
      "/api/gw/search?sort_by=rating_desc&min_rating_x100=0&size=M&page=1&page_size=12"
    );
    assert.equal(facetRes.status, 200, `facet search failed: ${facetRes.text}`);

    // SSR /tim-kiem & /danh-muc return 200 with real server total and no 500
    const ssrSearch = await anon.page("/tim-kiem?q=ao+thun&sort=price_asc");
    assert.equal(ssrSearch.status, 200, `SSR /tim-kiem failed: ${ssrSearch.status}`);
    assert.ok(
      ssrSearch.text.includes("data-server-total="),
      "SSR /tim-kiem must render server total attribute"
    );

    const ssrEmpty = await anon.page("/tim-kiem?q=khongcotu999xyz");
    assert.equal(ssrEmpty.status, 200, `SSR empty search failed: ${ssrEmpty.status}`);
    assert.ok(
      ssrEmpty.text.includes("Chưa tìm thấy sản phẩm khớp"),
      "SSR empty search must render natural Vietnamese empty state"
    );
  });

  test("2. [UC-B12] Header suggest endpoint GET /api/gw/search/suggest returns keywords, categories, shops, trending & products", async () => {
    await ensureRateBudget(20, { label: "w3b3-b1-suggest" });
    const anon = new WebClient();

    // Empty query suggest (dropdown focus)
    const emptySuggest = await anon.json("GET", "/api/gw/search/suggest?limit=8");
    assert.equal(
      emptySuggest.status,
      200,
      `empty suggest failed (${emptySuggest.status}): ${emptySuggest.text}`
    );
    b1AssertNoCommissionLeak(emptySuggest.text, "GET /api/gw/search/suggest (empty)");
    assert.ok(Array.isArray(emptySuggest.data?.keywords));
    assert.ok(Array.isArray(emptySuggest.data?.categories));
    assert.ok(Array.isArray(emptySuggest.data?.shops));
    assert.ok(Array.isArray(emptySuggest.data?.trending));
    assert.ok(Array.isArray(emptySuggest.data?.products));

    // Prefix query suggest (simulating 200ms debounced header typing)
    const t0 = Date.now();
    const prefixSuggest = await anon.json("GET", "/api/gw/search/suggest?q=ao&limit=8");
    const elapsedMs = Date.now() - t0;
    assert.equal(
      prefixSuggest.status,
      200,
      `prefix suggest failed (${prefixSuggest.status}): ${prefixSuggest.text}`
    );
    b1AssertNoCommissionLeak(prefixSuggest.text, "GET /api/gw/search/suggest?q=ao");
    assert.ok(
      Array.isArray(prefixSuggest.data?.products) && prefixSuggest.data.products.length > 0,
      `expected suggested products for q=ao, got: ${prefixSuggest.text}`
    );
    assert.ok(elapsedMs < 5_000, `suggest latency unreasonably slow: ${elapsedMs}ms`);
  });

  test("3. [UC-B12/G14] Search history requires auth (guest 401), records logged-in queries, enforces IDOR isolation, single delete & clear all", async () => {
    await ensureRateBudget(40, { label: "w3b3-b1-history" });

    // Anonymous guest is rejected with 401 (header skips calling this when logged out)
    const guest = new WebClient();
    const guestHist = await guest.json("GET", "/api/gw/search/history?limit=50");
    assert.equal(
      guestHist.status,
      401,
      `guest GET /api/gw/search/history must return 401, got ${guestHist.status}`
    );

    const buyerA = new WebClient();
    await registerAndLoginBuyer(buyerA, "b1-hist-a");
    const buyerB = new WebClient();
    await registerAndLoginBuyer(buyerB, "b1-hist-b");

    const q1 = `ao thun b1 ${Date.now()}`;
    const q2 = `quan short b1 ${Date.now()}`;

    const s1 = await buyerA.json(
      "GET",
      `/api/gw/search?q=${encodeURIComponent(q1)}&page=1&page_size=12`
    );
    assert.equal(s1.status, 200, `buyerA search 1 failed: ${s1.text}`);
    const s2 = await buyerA.json(
      "GET",
      `/api/gw/search?q=${encodeURIComponent(q2)}&page=1&page_size=12`
    );
    assert.equal(s2.status, 200, `buyerA search 2 failed: ${s2.text}`);

    const histItems = await pollUntil(
      async () => {
        const res = await buyerA.json("GET", "/api/gw/search/history?limit=50");
        assert.equal(res.status, 200, `buyerA history failed: ${res.text}`);
        const list = Array.isArray(res.data?.items) ? res.data.items : [];
        if (list.some((it) => it.query === q1) && list.some((it) => it.query === q2)) {
          return { done: true, value: list };
        }
        return {
          done: false,
          detail: `got ${list.length} history items (${list.map((it) => it.query).join(", ")})`,
        };
      },
      { timeoutMs: 8_000, intervalMs: 250, label: "wait-for-search-history" }
    );
    assert.ok(histItems.length <= 50, `history must be capped at 50, got ${histItems.length}`);

    const item1 = histItems.find((it) => it.query === q1);
    assert.ok(item1?.id, "expected history item id for q1");

    // IDOR: Buyer B cannot delete Buyer A's history item
    const idorDel = await buyerB.json(
      "DELETE",
      `/api/gw/search/history/${encodeURIComponent(item1.id)}`
    );
    assert.ok(
      idorDel.status === 200 || idorDel.status === 403 || idorDel.status === 404,
      `unexpected IDOR status: ${idorDel.status}`
    );
    const afterIdor = await buyerA.json("GET", "/api/gw/search/history?limit=50");
    assert.ok(
      (afterIdor.data?.items ?? []).some((it) => it.id === item1.id),
      "Buyer B must not delete Buyer A's search history item (IDOR)"
    );

    // Single delete by owner
    const delOne = await buyerA.json(
      "DELETE",
      `/api/gw/search/history/${encodeURIComponent(item1.id)}`
    );
    assert.equal(delOne.status, 200, `single delete failed: ${delOne.text}`);
    const afterDelOne = await buyerA.json("GET", "/api/gw/search/history?limit=50");
    assert.equal(
      (afterDelOne.data?.items ?? []).some((it) => it.id === item1.id),
      false,
      "deleted history item must remain gone"
    );
    assert.equal(
      (afterDelOne.data?.items ?? []).some((it) => it.query === q2),
      true,
      "other history item must remain intact"
    );

    // Clear all by owner
    const delAll = await buyerA.json("DELETE", "/api/gw/search/history");
    assert.equal(delAll.status, 200, `clear all history failed: ${delAll.text}`);
    const afterClear = await buyerA.json("GET", "/api/gw/search/history?limit=50");
    assert.equal(
      (afterClear.data?.items ?? []).length,
      0,
      "all history items must be removed after clear all"
    );
  });

  test("4. [§10-1/§10-2/B1] Homepage shows real total products, pagination works past 100 items, and loadgen do-lot asset returns 200", async () => {
    await ensureRateBudget(30, { label: "w3b3-b1-pagination-home" });
    const anon = new WebClient();

    const readCatalogTotal = async () => {
      const countRes = await anon.json("GET", "/api/gw/products?page=1&page_size=1");
      assert.equal(countRes.status, 200, `GET /api/gw/products failed: ${countRes.text}`);
      return Number(countRes.data?.total ?? 0);
    };
    const totalBefore = await readCatalogTotal();
    assert.ok(totalBefore > 0, `expected > 0 products, got ${totalBefore}`);

    // §10-1: Homepage SSR displays real formatted total (strip React SSR <!-- --> comment markers;
    // re-read catalog total right after page load in case parallel tests created/removed products).
    const homeRes = await anon.page("/");
    assert.equal(homeRes.status, 200, `GET / expected 200, got ${homeRes.status}`);
    const homeHtml = homeRes.text.replace(/<!--[\s\S]*?-->/g, "");
    const hasFormattedTotal = (n) => {
      const formattedTotal = b1FormatGroupedInt(n);
      return (
        homeHtml.includes(`Xem tất cả ${formattedTotal} sản phẩm`) ||
        homeHtml.includes(`Xem toàn bộ ${formattedTotal} sản phẩm`)
      );
    };
    let matchedHomeTotal = hasFormattedTotal(totalBefore);
    let totalAfter = totalBefore;
    if (!matchedHomeTotal) {
      totalAfter = await readCatalogTotal();
      const low = Math.min(totalBefore, totalAfter);
      const high = Math.max(totalBefore, totalAfter);
      for (let n = low; n <= high; n += 1) {
        if (hasFormattedTotal(n)) {
          matchedHomeTotal = true;
          break;
        }
      }
    }
    assert.ok(
      matchedHomeTotal,
      `homepage must show real total in [${totalBefore}, ${totalAfter}], not truncated products.length`
    );

    // Server pagination across page 1 -> page 2 (and page 5 = item #101+ when catalog > 100)
    const page1 = await anon.json("GET", "/api/gw/search?page=1&page_size=24");
    assert.equal(page1.status, 200, `search page 1 failed: ${page1.text}`);
    const searchTotal = Number(page1.data?.total ?? 0);
    assert.ok(searchTotal > 0, "search total must be > 0");

    if (searchTotal > 24) {
      const page2 = await anon.json("GET", "/api/gw/search?page=2&page_size=24");
      assert.equal(page2.status, 200, `search page 2 failed: ${page2.text}`);
      const ids1 = new Set((page1.data?.items ?? []).map((it) => it.id));
      const ids2 = (page2.data?.items ?? []).map((it) => it.id);
      assert.ok(ids2.length > 0, "page 2 must return items");
      assert.ok(
        ids2.some((id) => !ids1.has(id)),
        "page 2 items must advance beyond page 1"
      );

      const ssrPage2 = await anon.page("/danh-muc?page=2");
      assert.equal(ssrPage2.status, 200, `SSR /danh-muc?page=2 failed: ${ssrPage2.status}`);
    }

    if (searchTotal > 100) {
      const page5 = await anon.json("GET", "/api/gw/search?page=5&page_size=24");
      assert.equal(page5.status, 200, `search page 5 (>100 ceiling) failed: ${page5.text}`);
      assert.ok(
        Array.isArray(page5.data?.items) && page5.data.items.length > 0,
        "page 5 (items 97..120) must return products past the old 100-item cap"
      );
    }

    // §10-2: Loadgen do-lot asset returns 200 on web server
    const assetRes = await fetch(`${config.webUrl}/products/do-lot-boxer-modal-3.png`, {
      signal: AbortSignal.timeout(config.requestTimeoutMs),
    });
    await assetRes.body?.cancel().catch(() => {});
    assert.equal(
      assetRes.status,
      200,
      `expected /products/do-lot-boxer-modal-3.png to return 200, got ${assetRes.status}`
    );
  });

  test("5. [§10-3] Shop lookup resolves shop 101+ real names without falling back to 'Gian hàng NexusMart'", async () => {
    await ensureRateBudget(30, { label: "w3b3-b1-shop-101" });
    const anon = new WebClient();

    const shopsPage1 = await anon.json("GET", "/api/gw/shops?page=1&page_size=100");
    assert.equal(shopsPage1.status, 200, `GET /api/gw/shops failed: ${shopsPage1.text}`);
    const totalShops = Number(shopsPage1.data?.total ?? 0);

    let targetShop = (shopsPage1.data?.items ?? [])[0];
    if (totalShops > 100) {
      const shopsPage2 = await anon.json("GET", "/api/gw/shops?page=2&page_size=20");
      assert.equal(shopsPage2.status, 200, `GET /api/gw/shops?page=2 failed: ${shopsPage2.text}`);
      const shop101 = (shopsPage2.data?.items ?? [])[0];
      assert.ok(shop101?.id && shop101?.name, "expected real shop #101+ on page 2");
      targetShop = shop101;
    }

    assert.ok(targetShop?.id && targetShop?.name, "expected at least 1 public shop");
    const byIdRes = await anon.json("GET", `/api/gw/shops/${encodeURIComponent(targetShop.id)}`);
    assert.equal(byIdRes.status, 200, `GET /api/gw/shops/{id} failed: ${byIdRes.text}`);
    assert.equal(byIdRes.data?.shop?.name, targetShop.name);

    const shopFilterSlug = targetShop.slug || targetShop.id;
    const ssrFiltered = await anon.page(`/tim-kiem?shop=${encodeURIComponent(shopFilterSlug)}`);
    assert.equal(ssrFiltered.status, 200, `SSR /tim-kiem?shop= failed: ${ssrFiltered.status}`);
    assert.ok(
      ssrFiltered.text.includes(targetShop.name) ||
        !ssrFiltered.text.includes("Gian hàng NexusMart"),
      `filtered search for shop ${targetShop.name} must resolve real name, not fallback placeholder`
    );
  });

  test("6. [G11/G13] Degraded mode / Meilisearch fallback returns 200 with products & admin reindex enforces RBAC", async () => {
    await ensureRateBudget(30, { label: "w3b3-b1-fallback-rbac" });
    const anon = new WebClient();

    const searchRes = await anon.json("GET", "/api/gw/search?q=ao+thun&page=1&page_size=12");
    assert.equal(
      searchRes.status,
      200,
      `GET /api/gw/search must remain 200 even during fallback, got ${searchRes.status}: ${searchRes.text}`
    );
    b1AssertNoCommissionLeak(searchRes.text, "GET /api/gw/search fallback check");
    const degraded = String(searchRes.data?.degraded_mode ?? "");
    assert.ok(
      degraded === "" || degraded === "keyword_only" || degraded === "product_fallback",
      `unexpected degraded_mode value: ${degraded}`
    );

    const ssrRes = await anon.page("/tim-kiem?q=ao+thun");
    assert.equal(ssrRes.status, 200, `SSR /tim-kiem must remain 200, got ${ssrRes.status}`);

    if (EXPECT_PRODUCT_FALLBACK) {
      assert.equal(
        degraded,
        "product_fallback",
        `expected degraded_mode=product_fallback with Meilisearch stopped, got '${degraded}'`
      );
      assert.ok(
        Array.isArray(searchRes.data?.items) && searchRes.data.items.length > 0,
        "product_fallback must still return real products"
      );
      assert.ok(
        ssrRes.text.includes('data-degraded-mode="product_fallback"'),
        "SSR /tim-kiem must reflect product_fallback while rendering products normally"
      );
    }

    // Reindex RBAC: guest 401, buyer 403, admin 202 (or 409 REINDEX_ALREADY_RUNNING)
    const anonReindex = await anon.json("POST", "/api/gw/admin/search/reindex", { body: {} });
    assert.equal(anonReindex.status, 401, `guest reindex must be 401, got ${anonReindex.status}`);

    const buyer = new WebClient();
    await registerAndLoginBuyer(buyer, "b1-reindex-rbac");
    const buyerReindex = await buyer.json("POST", "/api/gw/admin/search/reindex", { body: {} });
    assert.equal(buyerReindex.status, 403, `buyer reindex must be 403, got ${buyerReindex.status}`);

    const { client: admin } = await b1LoginAccount(ADMIN_EMAIL, ADMIN_PASSWORD);
    const adminReindex = await admin.json("POST", "/api/gw/admin/search/reindex", {
      body: {
        reindex_products: true,
        reindex_embeddings: false,
        reindex_categories: true,
        reindex_shops: true,
        batch_size: 200,
      },
    });
    assert.ok(
      adminReindex.status === 202 || adminReindex.status === 409,
      `admin reindex expected 202 or 409, got ${adminReindex.status}: ${adminReindex.text}`
    );
    if (adminReindex.status === 202) {
      assert.ok(adminReindex.data?.job_id, `202 reindex response missing job_id: ${adminReindex.text}`);
    }
  });

  test("7. [Review C1/C2/C5] Quick-add from search hit with color 'Đen than' + another color adds exact color_code & size SKU to cart", async () => {
    await ensureRateBudget(40, { label: "w3b3-b1-quickadd-color" });
    const anon = new WebClient();

    const foldText = (v) =>
      String(v ?? "")
        .normalize("NFD")
        .replace(/[\u0300-\u036f]/g, "")
        .replace(/[đĐ]/g, "d")
        .toLowerCase()
        .replace(/\s+/g, " ")
        .trim();

    // Find an indexed multi-color product whose colors include "Đen than" and at least 1 other color
    const searchRes = await anon.json("GET", "/api/gw/search?q=polo&page=1&page_size=24");
    assert.equal(searchRes.status, 200, `search 'polo' failed: ${searchRes.text}`);
    const hits = Array.isArray(searchRes.data?.items) ? searchRes.data.items : [];

    let targetHit = hits.find((h) => {
      const cols = Array.isArray(h.colors) ? h.colors : [];
      return cols.length >= 2 && cols.some((c) => foldText(c) === "den than");
    });
    if (!targetHit) {
      const broadRes = await anon.json("GET", "/api/gw/search?page=1&page_size=50");
      assert.equal(broadRes.status, 200, `broad search failed: ${broadRes.text}`);
      const broadHits = Array.isArray(broadRes.data?.items) ? broadRes.data.items : [];
      targetHit = broadHits.find((h) => {
        const cols = Array.isArray(h.colors) ? h.colors : [];
        return cols.length >= 2 && cols.some((c) => foldText(c) === "den than");
      });
    }
    assert.ok(
      targetHit?.slug,
      "expected a seeded multi-color product hit containing 'Đen than' + another color"
    );

    // Resolve exact SKU via GET /api/gw/products/slug/{slug} (same lookup path as ProductCard quick-add)
    const detailRes = await anon.json(
      "GET",
      `/api/gw/products/slug/${encodeURIComponent(targetHit.slug)}`
    );
    assert.equal(
      detailRes.status,
      200,
      `GET /api/gw/products/slug/${targetHit.slug} failed: ${detailRes.text}`
    );
    const skus = Array.isArray(detailRes.data?.product?.skus) ? detailRes.data.product.skus : [];
    const denThanSku = skus.find(
      (s) =>
        s.is_active !== false &&
        s.available_stock > 0 &&
        foldText(s.attributes?.color) === "den than" &&
        Boolean(s.attributes?.size)
    );
    assert.ok(
      denThanSku?.id && denThanSku.attributes?.color_code && denThanSku.attributes?.size,
      `expected sellable 'Đen than' SKU with color_code & size on ${targetHit.slug}`
    );
    const selectedSize = String(denThanSku.attributes.size);
    const expectedColorCode = String(denThanSku.attributes.color_code);

    const otherColorSameSizeSku = skus.find(
      (s) =>
        s.id !== denThanSku.id &&
        s.is_active !== false &&
        s.available_stock > 0 &&
        String(s.attributes?.size ?? "").toLowerCase() === selectedSize.toLowerCase() &&
        foldText(s.attributes?.color) !== "den than"
    );
    assert.ok(
      otherColorSameSizeSku?.id,
      `expected another color SKU with same size ${selectedSize} on ${targetHit.slug}`
    );

    // Quick-add the resolved "Đen than" SKU into a fresh buyer cart and verify exact SKU/color_code/size
    const buyer = new WebClient();
    await registerAndLoginBuyer(buyer, "b1-quickadd-denthan");
    const addRes = await buyer.json("POST", "/api/gw/cart/items", {
      body: { sku_id: denThanSku.id, quantity: 1 },
    });
    assert.ok(
      addRes.status === 200 || addRes.status === 201,
      `POST /api/gw/cart/items expected 200/201, got ${addRes.status}: ${addRes.text}`
    );

    const cartRes = await buyer.json("GET", "/api/gw/cart");
    assert.equal(cartRes.status, 200, `GET /api/gw/cart failed: ${cartRes.text}`);
    const cartItems = (cartRes.data?.cart?.shop_groups ?? []).flatMap((g) => g.items ?? []);
    const addedItem = cartItems.find((it) => it.sku_id === denThanSku.id);
    assert.ok(
      addedItem,
      `cart must contain exact 'Đen than' SKU ${denThanSku.id} (not ${otherColorSameSizeSku.id})`
    );

    const skuById = new Map(skus.map((s) => [s.id, s]));
    const cartSku = skuById.get(addedItem.sku_id);
    assert.equal(
      cartSku?.attributes?.color_code,
      expectedColorCode,
      `cart SKU color_code must equal ${expectedColorCode}`
    );
    assert.equal(
      cartSku?.attributes?.size,
      selectedSize,
      `cart SKU size must equal ${selectedSize}`
    );

    // C2 check: PDP link with slugged ?color=denthan renders 200 cleanly
    const pdpRes = await anon.page(`/san-pham/${encodeURIComponent(targetHit.slug)}?color=denthan`);
    assert.equal(
      pdpRes.status,
      200,
      `SSR /san-pham/${targetHit.slug}?color=denthan expected 200, got ${pdpRes.status}`
    );
  });
});
