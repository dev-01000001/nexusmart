/**
 * Wave 3b-3 web-b1 behavioral, concurrency & contract verification tests
 * (B1 Search & Discovery + §10-1/2/3 & §10 pricing data).
 * Enforces G9-G14 from .agents/briefs/fix/w3b-plan.md §5 and .agents/briefs/fix/w3b3-web-b1.md.
 */
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { registerHooks } from "node:module";
import { dirname, resolve } from "node:path";
import { describe, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const WEB_ROOT = resolve(__dirname, "..");
const REPO_ROOT = resolve(WEB_ROOT, "..");
const WEB_SRC = resolve(WEB_ROOT, "src");

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.startsWith("@/")) {
      const rel = specifier.slice(2);
      const tsPath = resolve(WEB_SRC, `${rel}.ts`);
      if (existsSync(tsPath)) {
        return nextResolve(pathToFileURL(tsPath).href, context);
      }
      const indexTsPath = resolve(WEB_SRC, rel, "index.ts");
      if (existsSync(indexTsPath)) {
        return nextResolve(pathToFileURL(indexTsPath).href, context);
      }
    }
    if (
      (specifier.startsWith("./") || specifier.startsWith("../")) &&
      !/\.[a-z]+$/i.test(specifier) &&
      context.parentURL
    ) {
      const parentDir = dirname(fileURLToPath(context.parentURL));
      const tsPath = resolve(parentDir, `${specifier}.ts`);
      if (existsSync(tsPath)) {
        return nextResolve(pathToFileURL(tsPath).href, context);
      }
      const indexTsPath = resolve(parentDir, specifier, "index.ts");
      if (existsSync(indexTsPath)) {
        return nextResolve(pathToFileURL(indexTsPath).href, context);
      }
    }
    return nextResolve(specifier, context);
  },
});

function readSrc(relPath) {
  return readFileSync(resolve(WEB_SRC, relPath), "utf8");
}

describe("Wave 3b-3 web-b1 — B1 Search, §10-1/2/3 & §10 pricing data", () => {
  test("1. [RBAC] admin-rbac.ts includes search.reindex matching pkg/common/auth/capabilities.json 1:1", async () => {
    const rbac = await import("../src/lib/admin-rbac.ts");
    const canonical = JSON.parse(
      readFileSync(resolve(REPO_ROOT, "pkg/common/auth/capabilities.json"), "utf8")
    );

    assert.ok(
      "search.reindex" in rbac.CAPABILITY_MATRIX,
      "CAPABILITY_MATRIX must include search.reindex"
    );
    assert.deepEqual(
      [...rbac.CAPABILITY_MATRIX["search.reindex"]].sort(),
      [...canonical.capabilities["search.reindex"]].sort()
    );
    assert.deepEqual(
      Object.keys(rbac.CAPABILITY_MATRIX).sort(),
      Object.keys(canonical.capabilities).sort()
    );
  });

  test("2. [§10-2] tools/loadgen/generate-products.mjs references existing do-lot-boxer-modal-3.png and valid product images", () => {
    const loadgenSrc = readFileSync(
      resolve(REPO_ROOT, "tools/loadgen/generate-products.mjs"),
      "utf8"
    );
    assert.equal(
      loadgenSrc.includes('"/products/do-lot-boxer-modal.png"'),
      false,
      "broken image path /products/do-lot-boxer-modal.png must be removed"
    );
    assert.ok(
      loadgenSrc.includes('"/products/do-lot-boxer-modal-3.png"'),
      "do-lot must point to /products/do-lot-boxer-modal-3.png"
    );

    const imageBlockMatch = loadgenSrc.match(/const IMAGE_BY_CATEGORY = \{([\s\S]*?)\};/);
    assert.ok(imageBlockMatch, "IMAGE_BY_CATEGORY must exist in generate-products.mjs");
    const paths = Array.from(imageBlockMatch[1].matchAll(/"(\/products\/[^"]+)"/g)).map(
      (m) => m[1]
    );
    assert.ok(paths.length >= 6, `expected >= 6 category images, got ${paths.length}`);
    for (const rel of paths) {
      const fullPath = resolve(WEB_ROOT, "public", rel.slice(1));
      assert.equal(existsSync(fullPath), true, `missing product asset on disk: ${rel}`);
    }
  });

  test("3. [§10-3] shop-lookup.ts deduplicates IDs, skips existing, enforces concurrency <= 8, and tolerates 404s", async () => {
    const shopLookup = await import("../src/lib/shop-lookup.ts");

    assert.equal(shopLookup.MAX_SHOP_LOOKUP_CONCURRENCY, 8);

    const existing = {
      "shop-001": {
        id: "shop-001",
        name: "Xưởng Dệt Số 1",
        slug: "xuong-det-so-1",
        description: "",
        status: "ACTIVE",
      },
    };

    const requestedIds = [
      "shop-001",
      " shop-001 ",
      "",
      "   ",
      ...Array.from({ length: 28 }, (_, i) => `shop-${101 + i}`),
      "shop-105",
      "shop-105",
      "shop-missing-404",
      "shop-network-err",
    ];

    let inFlight = 0;
    let peakInFlight = 0;
    const callCountById = new Map();

    const result = await shopLookup.lookupShopsByIdsWithFetcher(
      requestedIds,
      async (id) => {
        callCountById.set(id, (callCountById.get(id) ?? 0) + 1);
        inFlight += 1;
        if (inFlight > peakInFlight) peakInFlight = inFlight;
        await new Promise((r) => setTimeout(r, 8));
        inFlight -= 1;
        if (id === "shop-missing-404") return null;
        if (id === "shop-network-err") throw new Error("transient failure");
        return {
          id,
          name: `Gian hàng thật ${id}`,
          slug: id,
          description: "",
          status: "ACTIVE",
        };
      },
      { existing, concurrency: 32 } // even if caller asks for > 8, must clamp to <= 8
    );

    assert.ok(peakInFlight <= 8, `peak concurrency ${peakInFlight} exceeded cap 8`);
    assert.ok(peakInFlight >= 2, `expected parallel lookups, got peak ${peakInFlight}`);
    assert.equal(callCountById.get("shop-001") ?? 0, 0, "must not re-fetch existing shop");
    assert.equal(callCountById.get("shop-105"), 1, "must deduplicate duplicate shop IDs");
    assert.equal(result["shop-001"]?.name, "Xưởng Dệt Số 1");
    assert.equal(result["shop-101"]?.name, "Gian hàng thật shop-101");
    assert.equal(result["shop-128"]?.name, "Gian hàng thật shop-128");
    assert.equal(result["shop-missing-404"], undefined);
    assert.equal(result["shop-network-err"], undefined);

    // Verify all 5 §10-3 call sites wire up shop-lookup
    for (const rel of [
      "lib/catalog.ts",
      "app/(shop)/gio-hang/page.tsx",
      "app/(shop)/thanh-toan/page.tsx",
      "app/(shop)/thanh-toan/cho-thanh-toan/[paymentId]/page.tsx",
      "features/account/serverData.ts",
    ]) {
      const src = readSrc(rel);
      assert.ok(
        src.includes("shop-lookup"),
        `${rel} must use @/lib/shop-lookup for 101+ shop resolution`
      );
    }
  });

  test("4. [B1/G9/G13/G14] search-client.ts query builder, pricing precedence, hit mapping & degraded fallback", async () => {
    const searchClient = await import("../src/lib/search-client.ts");

    assert.equal(searchClient.SEARCH_SUGGEST_DEBOUNCE_MS, 200);
    assert.equal(searchClient.SEARCH_HISTORY_MAX_ITEMS, 50);

    const qs = searchClient.buildSearchQueryString({
      q: "ao thun cotton",
      categorySlug: "ao-thun",
      shopSlug: "xuong-det-bach-dang",
      minPriceVnd: 180_000,
      maxPriceVnd: 350_000,
      minRatingX100: 400,
      inStockOnly: true,
      sortBy: "rating_desc",
      mode: "auto",
      size: "L",
      color: "trang",
      page: 3,
      pageSize: 24,
    });
    const parsed = new URLSearchParams(qs);
    assert.equal(parsed.get("q"), "ao thun cotton");
    assert.equal(parsed.get("category_slug"), "ao-thun");
    assert.equal(parsed.get("shop_slug"), "xuong-det-bach-dang");
    assert.equal(parsed.get("min_price_vnd"), "180000");
    assert.equal(parsed.get("max_price_vnd"), "350000");
    assert.equal(parsed.get("min_rating_x100"), "400");
    assert.equal(parsed.get("in_stock_only"), "true");
    assert.equal(parsed.get("sort_by"), "rating_desc");
    assert.equal(parsed.get("mode"), "auto");
    assert.equal(parsed.get("size"), "L");
    assert.equal(parsed.get("color"), "trang");
    assert.equal(parsed.get("page"), "3");
    assert.equal(parsed.get("page_size"), "24");

    // Sort normalization maps legacy aliases & all 5 real server sorts
    assert.equal(searchClient.normalizeSearchSortParam("bestseller"), "relevance");
    assert.equal(searchClient.normalizeSearchSortParam("price-asc"), "price_asc");
    assert.equal(searchClient.normalizeSearchSortParam("price-desc"), "price_desc");
    assert.equal(searchClient.normalizeSearchSortParam("newest"), "newest");
    assert.equal(searchClient.normalizeSearchSortParam("rating-desc"), "rating_desc");
    assert.equal(searchClient.normalizeSearchSortParam("rating_desc"), "rating_desc");

    // Single-price rule on SearchProductHit: live flash > active shop discount > base hit price
    const hit = {
      id: "prod-101",
      shop_id: "shop-105",
      shop_name: "", // intentionally empty to verify fallback hydration from shopsById
      shop_slug: "",
      category_id: "cat-1",
      category_slug: "ao-thun",
      category_name: "Áo thun",
      name: "Áo thun cotton 220gsm",
      slug: "ao-thun-cotton-220gsm",
      thumbnail_url: "/products/ao-thun-cotton-trang.png",
      min_price_vnd: 199_000,
      max_price_vnd: 219_000,
      status: "APPROVED",
      rating_avg_x100: 475,
      review_count: 18,
      in_stock: true,
      sku_count: 2,
      score: 12.5,
      description_snippet: "Cotton chải kỹ",
      sku_ids: ["sku-a", "sku-b"],
      default_sku_id: "sku-a",
      colors: ["Trắng", "Đen"],
      sizes: ["M", "L"],
    };

    const flashMap = new Map([
      [
        "sku-a",
        {
          id: "fsi-1",
          flash_sale_id: "fs-1",
          shop_id: "shop-105",
          sku_id: "sku-a",
          flash_price_vnd: 129_000,
          original_price_vnd: 199_000,
          total_slots: 20,
          remaining_slots: 7,
          per_user_limit: 2,
          status: "APPROVED",
          rejection_reason: "",
        },
      ],
    ]);
    const shopDiscMap = new Map([
      [
        "sku-a",
        {
          item_id: "sdi-1",
          program_id: "sdp-1",
          program_name: "Sale xưởng",
          shop_id: "shop-105",
          sku_id: "sku-a",
          original_price_vnd: 199_000,
          discounted_price_vnd: 159_000,
          discount_percent: 20,
          per_user_limit: 5,
        },
      ],
      [
        "sku-b",
        {
          item_id: "sdi-2",
          program_id: "sdp-1",
          program_name: "Sale xưởng",
          shop_id: "shop-105",
          sku_id: "sku-b",
          original_price_vnd: 219_000,
          discounted_price_vnd: 175_000,
          discount_percent: 20,
          per_user_limit: 5,
        },
      ],
    ]);

    const flashPricing = searchClient.computeSearchHitDisplayPricing(hit, flashMap, shopDiscMap);
    assert.equal(flashPricing.minPriceVnd, 129_000);
    assert.equal(flashPricing.originalPriceVnd, 199_000);
    assert.equal(flashPricing.promoKind, "flash");

    const shopOnlyPricing = searchClient.computeSearchHitDisplayPricing(
      hit,
      new Map(),
      shopDiscMap
    );
    assert.equal(shopOnlyPricing.minPriceVnd, 159_000);
    assert.equal(shopOnlyPricing.maxPriceVnd, 175_000);
    assert.equal(shopOnlyPricing.originalPriceVnd, 199_000);
    assert.equal(shopOnlyPricing.promoKind, "shop_discount");

    const mapped = searchClient.mapSearchHitToCatalogProduct(hit, {
      categoriesById: new Map(),
      shopsById: new Map([
        [
          "shop-105",
          {
            id: "shop-105",
            slug: "xuong-105",
            name: "Xưởng Dệt 105",
            shortName: "Xưởng 105",
            city: "Hà Nội",
            provinceCode: "HN",
            specialty: "",
            foundedYear: 2026,
            responseTime: "",
            fulfillmentRate: "",
            returnPolicy: "",
            warehouseAddress: "",
            story: "",
            accentDot: "#0d7480",
            status: "ACTIVE",
            heroBanner: { src: "/products/ao-thun-cotton-trang.png", alt: "Xưởng 105" },
            hasEditorialProfile: false,
          },
        ],
      ]),
      liveFlashItemsBySkuId: flashMap,
      activeShopDiscountsBySkuId: shopDiscMap,
    });
    assert.equal(mapped.shopName, "Xưởng Dệt 105");
    assert.equal(mapped.ratingAvgX100, 475);
    assert.equal(mapped.reviewCount, 18);
    assert.equal(mapped.price, 129_000);
    assert.equal(mapped.originalPrice, 199_000);
    assert.equal(mapped.isFlashSale, true);
    assert.deepEqual(mapped.sizes, ["M", "L"]);
  });

  test("5. [B1/UC-B12/G14] ShopHeader uses 200ms debounce, /search/suggest, logged-in-only history & zero static TRENDING/SYNONYMS/SPELLING", () => {
    const headerSrc = readSrc("components/ShopHeader.tsx");

    assert.equal(
      headerSrc.includes("TRENDING_KEYWORDS"),
      false,
      "ShopHeader must remove static TRENDING_KEYWORDS"
    );
    assert.equal(
      headerSrc.includes("SEARCH_SYNONYMS"),
      false,
      "ShopHeader must not use static SEARCH_SYNONYMS"
    );
    assert.equal(
      headerSrc.includes("SPELLING_DICTIONARY"),
      false,
      "ShopHeader must not use static SPELLING_DICTIONARY"
    );
    assert.equal(
      headerSrc.includes("suggestSpelling"),
      false,
      "ShopHeader must not use static suggestSpelling"
    );
    assert.ok(
      headerSrc.includes("SEARCH_SUGGEST_DEBOUNCE_MS") || headerSrc.includes("}, 200);"),
      "ShopHeader debounce must be exactly 200ms"
    );
    assert.equal(headerSrc.includes("}, 220);"), false, "old 220ms debounce must be removed");
    assert.ok(
      headerSrc.includes("fetchSearchSuggestClient"),
      "ShopHeader must call GET /api/gw/search/suggest via search-client"
    );
    assert.ok(
      headerSrc.includes("fetchSearchHistoryClient") &&
        headerSrc.includes("deleteSearchHistoryItemClient") &&
        headerSrc.includes("clearSearchHistoryClient"),
      "ShopHeader must support search history list, single delete, and clear all"
    );
    assert.ok(
      headerSrc.includes("if (!isAuthenticated)"),
      "ShopHeader must guard search history fetch behind isAuthenticated (zero 401 for guests)"
    );
    assert.ok(
      headerSrc.includes("computeSearchHitDisplayPricing"),
      "ShopHeader must compute real flash/shop discount prices on suggest product hits"
    );
  });

  test("6. [B1/UC-B03/UC-B20/G9/G13] CatalogExplorer & catalog.ts use server /search pagination, 5 real facets, hide fake editorial counts when total > pageSize, and handle product_fallback", () => {
    const catalogSrc = readSrc("lib/catalog.ts");
    const explorerSrc = readSrc("features/catalog/CatalogExplorer.tsx");
    const timKiemSrc = readSrc("app/(shop)/tim-kiem/page.tsx");
    const danhMucSrc = readSrc("app/(shop)/danh-muc/page.tsx");

    // catalog.ts loadCatalogExplorerData must query /search (not fetchRawProducts pageSize 100)
    const explorerLoaderStart = catalogSrc.indexOf("export async function loadCatalogExplorerData");
    const explorerLoaderEnd = catalogSrc.indexOf(
      "export interface DiscontinuedProductNotice",
      explorerLoaderStart
    );
    const explorerLoaderBody = catalogSrc.slice(explorerLoaderStart, explorerLoaderEnd);
    assert.ok(explorerLoaderBody.includes("/search"), "loadCatalogExplorerData must query /search");
    assert.equal(
      explorerLoaderBody.includes("fetchRawProducts({ page: 1, pageSize: 100 })"),
      false,
      "loadCatalogExplorerData must remove pageSize: 100 ProductService ceiling"
    );
    assert.ok(
      explorerLoaderBody.includes("degradedMode") && explorerLoaderBody.includes("didYouMean"),
      "loadCatalogExplorerData must forward degradedMode and didYouMean"
    );

    // tim-kiem & danh-muc pages pass real serverTotal / page / facets / degradedMode
    for (const [label, src] of [
      ["tim-kiem/page.tsx", timKiemSrc],
      ["danh-muc/page.tsx", danhMucSrc],
    ]) {
      assert.ok(src.includes("serverTotal"), `${label} must pass serverTotal`);
      assert.ok(src.includes("categoryFacets"), `${label} must pass categoryFacets`);
      assert.ok(src.includes("shopFacets"), `${label} must pass shopFacets`);
      assert.ok(src.includes("degradedMode"), `${label} must pass degradedMode`);
    }

    // CatalogExplorer must remove static TRENDING_KEYWORDS / searchProducts / suggestSpelling
    assert.equal(
      explorerSrc.includes("TRENDING_KEYWORDS"),
      false,
      "CatalogExplorer must remove static TRENDING_KEYWORDS"
    );
    assert.equal(
      explorerSrc.includes("suggestSpelling"),
      false,
      "CatalogExplorer must use server didYouMean instead of static suggestSpelling"
    );
    assert.equal(
      explorerSrc.includes("searchProducts("),
      false,
      "CatalogExplorer must not run client searchProducts"
    );
    // Must support all 5 sorts + rating + in_stock + server pagination + hiding editorial counts when total > pageSize
    for (const token of [
      "relevance",
      "price_asc",
      "price_desc",
      "newest",
      "rating_desc",
      "min_rating",
      "in_stock",
      "product_fallback",
      "fetchSearchProductsClient",
      "hideEditorialCounts",
    ]) {
      assert.ok(explorerSrc.includes(token), `CatalogExplorer must support ${token}`);
    }
  });

  test("7. [§10-1] HomeCatalogData, page.tsx and HomeClient display real productPage.total instead of products.length", () => {
    const catalogSrc = readSrc("lib/catalog.ts");
    const homePageSrc = readSrc("app/(shop)/page.tsx");
    const homeClientSrc = readSrc("app/(shop)/HomeClient.tsx");

    assert.ok(
      catalogSrc.includes("totalProducts: number"),
      "HomeCatalogData must include totalProducts"
    );
    assert.ok(
      catalogSrc.includes("productPage.total"),
      "loadHomeCatalogData must read productPage.total"
    );
    assert.ok(homePageSrc.includes("totalProducts={data.totalProducts}"));
    assert.equal(
      homeClientSrc.includes("Xem tất cả {products.length} sản phẩm"),
      false,
      "HomeClient must not render products.length in category header CTA"
    );
    assert.equal(
      homeClientSrc.includes("Xem toàn bộ {products.length} sản phẩm"),
      false,
      "HomeClient must not render products.length in catalog footer CTA"
    );
    assert.ok(
      homeClientSrc.includes("totalProducts"),
      "HomeClient must accept and render totalProducts"
    );
  });

  test("8. [§10 pricing] loadShopDetailData loads discounts for allProducts SKUs; cart/checkout/waiting enrich suggestedProducts SKUs", () => {
    const catalogSrc = readSrc("lib/catalog.ts");
    const shopLoaderStart = catalogSrc.indexOf("export async function loadShopDetailData");
    const shopLoaderEnd = catalogSrc.indexOf("export interface FlashSalePageData", shopLoaderStart);
    const shopLoaderBody = catalogSrc.slice(shopLoaderStart, shopLoaderEnd);
    assert.ok(
      shopLoaderBody.includes("allProductsPage.items") &&
        shopLoaderBody.includes("fetchActiveShopDiscounts({ skuIds"),
      "loadShopDetailData must fetch active shop discounts by skuIds across allProducts for SizeFinder"
    );

    const cartSrc = readSrc("app/(shop)/gio-hang/page.tsx");
    const checkoutSrc = readSrc("app/(shop)/thanh-toan/page.tsx");
    const waitingSrc = readSrc("app/(shop)/thanh-toan/cho-thanh-toan/[paymentId]/page.tsx");

    assert.ok(
      cartSrc.includes("suggestedProducts") && cartSrc.includes("enrichProductsWithPromotions"),
      "gio-hang/page.tsx must enrich suggestedProducts with active flash + shop discounts"
    );
    assert.ok(
      checkoutSrc.includes("suggestedProducts") &&
        checkoutSrc.includes("enrichProductsWithPromotions"),
      "thanh-toan/page.tsx must enrich suggestedProducts with active flash + shop discounts"
    );
    assert.ok(
      waitingSrc.includes("suggestedProducts") &&
        waitingSrc.includes("enrichProductsWithPromotions"),
      "cho-thanh-toan/[paymentId]/page.tsx must enrich suggestedProducts with active flash + shop discounts"
    );
  });

  test("8b. [§10-3 + §10 behavior] fetchAllPublicShopsPagesWithFetcher paginates 250 shops (<= 8 concurrent) and enrichProductsWithPromotions enforces flash > shop discount > regular", async () => {
    const shopLookup = await import("../src/lib/shop-lookup.ts");
    const searchClient = await import("../src/lib/search-client.ts");

    let activePages = 0;
    let peakPages = 0;
    const allShopsMap = await shopLookup.fetchAllPublicShopsPagesWithFetcher(
      async (page, pageSize) => {
        activePages += 1;
        if (activePages > peakPages) peakPages = activePages;
        await new Promise((r) => setTimeout(r, 5));
        activePages -= 1;
        const start = (page - 1) * pageSize;
        const end = Math.min(250, start + pageSize);
        const items = [];
        for (let i = start; i < end; i += 1) {
          items.push({
            id: `shop-${i + 1}`,
            name: `Xưởng số ${i + 1}`,
            slug: `xuong-so-${i + 1}`,
            description: "",
            status: "ACTIVE",
          });
        }
        return { items, page, page_size: pageSize, total: 250 };
      },
      { concurrency: 8 }
    );
    assert.ok(peakPages <= 8);
    assert.equal(Object.keys(allShopsMap).length, 250);
    assert.equal(allShopsMap["shop-101"]?.name, "Xưởng số 101");
    assert.equal(allShopsMap["shop-250"]?.name, "Xưởng số 250");

    const baseProd = {
      id: "p-s1",
      shop_id: "shop-105",
      category_id: "cat-1",
      name: "Áo polo pique",
      slug: "ao-polo-pique",
      description: "",
      images: ["/products/ao-polo-pique-kem.png"],
      status: "APPROVED",
      rejection_reason: "",
      min_price_vnd: 250_000,
      max_price_vnd: 250_000,
      skus: [
        {
          id: "sku-s1",
          product_id: "p-s1",
          shop_id: "shop-105",
          product_name: "Áo polo pique",
          sku_code: "POLO-M",
          name: "Kem / M",
          attributes: { color: "Kem", size: "M" },
          price_vnd: 250_000,
          stock: 10,
          reserved_stock: 0,
          available_stock: 10,
          version: 1,
          weight_grams: 280,
          image_url: "/products/ao-polo-pique-kem.png",
          category_id: "cat-1",
          is_active: true,
        },
      ],
    };

    const flashMap = new Map([
      [
        "sku-s1",
        {
          id: "fsi-s1",
          flash_sale_id: "fs-1",
          shop_id: "shop-105",
          sku_id: "sku-s1",
          flash_price_vnd: 179_000,
          original_price_vnd: 250_000,
          total_slots: 10,
          remaining_slots: 4,
          per_user_limit: 1,
          status: "APPROVED",
          rejection_reason: "",
        },
      ],
    ]);
    const shopDiscMap = new Map([
      [
        "sku-s1",
        {
          item_id: "sdi-s1",
          program_id: "sdp-1",
          program_name: "Giảm giá xưởng",
          shop_id: "shop-105",
          sku_id: "sku-s1",
          original_price_vnd: 250_000,
          discounted_price_vnd: 210_000,
          discount_percent: 16,
          per_user_limit: 5,
        },
      ],
    ]);

    const enrichedFlash = searchClient.enrichProductsWithPromotions(
      [baseProd],
      flashMap,
      shopDiscMap
    );
    assert.equal(baseProd.min_price_vnd, 250_000, "must not mutate original GatewayProduct");
    assert.equal(enrichedFlash[0].min_price_vnd, 179_000);
    assert.equal(enrichedFlash[0].effective_price_vnd, 179_000);
    assert.equal(enrichedFlash[0].original_price_vnd, 250_000);
    assert.equal(enrichedFlash[0].promo_kind, "flash");

    const enrichedShopDisc = searchClient.enrichProductsWithPromotions(
      [baseProd],
      new Map(),
      shopDiscMap
    );
    assert.equal(enrichedShopDisc[0].min_price_vnd, 210_000);
    assert.equal(enrichedShopDisc[0].original_price_vnd, 250_000);
    assert.equal(enrichedShopDisc[0].promo_kind, "shop_discount");
  });

  test("9. [E2E Web] tests/e2e-web/14_search_b1.test.mjs exists with self-contained helpers and covers all B1/§10 scenarios", () => {
    const e2ePath = resolve(REPO_ROOT, "tests/e2e-web/14_search_b1.test.mjs");
    assert.equal(existsSync(e2ePath), true, "tests/e2e-web/14_search_b1.test.mjs must exist");
    const src = readFileSync(e2ePath, "utf8");
    for (const token of [
      "/api/gw/search/suggest",
      "/api/gw/search/history",
      "/api/gw/search?",
      "degraded_mode",
      "product_fallback",
      "do-lot-boxer-modal-3.png",
    ]) {
      assert.ok(src.includes(token), `14_search_b1.test.mjs must cover ${token}`);
    }
  });

  test("10. [Review B1] CheckoutSuggestionStrip uses single pre-enriched product pricing path (no 6 extra pricing props, no client fetch, no resolveSuggestionProductPricing)", () => {
    const stripSrc = readSrc("features/checkout/CheckoutSuggestionStrip.tsx");
    const a8Src = readSrc("lib/a8-types.ts");
    const cartSrc = readSrc("app/(shop)/gio-hang/page.tsx");

    for (const forbidden of [
      "flashSales",
      "activeShopDiscounts",
      "liveFlashItemsBySkuId",
      "activeShopDiscountsBySkuId",
      "priceByProductId",
      "discountByProductId",
      "toSkuMap",
      "resolveSuggestionProductPricing",
      "flash-sales",
      "shop-discounts/active",
      "useEffect",
    ]) {
      assert.equal(
        stripSrc.includes(forbidden),
        false,
        `CheckoutSuggestionStrip must remove legacy pricing/fetch symbol: ${forbidden}`
      );
    }
    assert.ok(
      stripSrc.includes("effective_price_vnd") && stripSrc.includes("original_price_vnd"),
      "CheckoutSuggestionStrip must render pre-enriched effective_price_vnd and original_price_vnd"
    );
    assert.ok(
      stripSrc.includes("originalAmount"),
      "CheckoutSuggestionStrip must pass originalAmount to Price component"
    );

    for (const removed of [
      "resolveSuggestionProductPricing",
      "SuggestionPriceOverride",
      "SuggestionProductInput",
    ]) {
      assert.equal(a8Src.includes(removed), false, `a8-types.ts must remove unused ${removed}`);
    }

    assert.equal(
      cartSrc.includes("activeShopDiscountsBySkuId={activeShopDiscountsBySkuId}"),
      false,
      "gio-hang/page.tsx must not pass removed activeShopDiscountsBySkuId prop to CheckoutSuggestionStrip"
    );
  });

  test("11. [Review B2] Attribute filters (size/color) do not block or strip characters such as 'XL (60kg)'", async () => {
    const searchClient = await import("../src/lib/search-client.ts");

    const qs = searchClient.buildSearchQueryString({
      size: "XL (60kg)",
      color: "xam",
      page: 1,
      pageSize: 24,
    });
    const parsed = new URLSearchParams(qs);
    assert.equal(parsed.get("size"), "XL (60kg)", "size must preserve 'XL (60kg)' verbatim");
    assert.equal(parsed.get("color"), "xam", "real color_code 'xam' must not be remapped to xamda");

    const qsParenColor = searchClient.buildSearchQueryString({
      color: "XL (60kg)",
    });
    assert.equal(new URLSearchParams(qsParenColor).get("color"), "XL (60kg)");
  });

  test("12. [Review B3] degraded_mode=product_fallback hides empty facet blocks while keeping results usable", () => {
    const explorerSrc = readSrc("features/catalog/CatalogExplorer.tsx");
    assert.ok(
      explorerSrc.includes("!isProductFallback && sidebarOpen"),
      "CatalogExplorer must hide filter rail during product_fallback"
    );
    assert.ok(
      explorerSrc.includes("!isProductFallback && (") ||
        explorerSrc.includes("{!isProductFallback && <nav"),
      "CatalogExplorer must hide category facet strip during product_fallback"
    );
    assert.ok(
      explorerSrc.includes("!sidebarOpen || isProductFallback"),
      "CatalogExplorer must expand product grid full-width during product_fallback"
    );
  });

  test("13. [Review B4] Color filter uses single color_code per option (split den/thanchi, xam*, xanh*) and removes codes[]", () => {
    const explorerSrc = readSrc("features/catalog/CatalogExplorer.tsx");
    assert.equal(
      explorerSrc.includes("codes:"),
      false,
      "CatalogExplorer COLOR_OPTIONS must drop multi-code `codes` array"
    );
    assert.equal(
      explorerSrc.includes("Đen & Than chì"),
      false,
      "CatalogExplorer must split grouped 'Đen & Than chì' into separate options"
    );
    for (const singleCode of [
      'code: "den"',
      'code: "thanchi"',
      'code: "xam"',
      'code: "xamda"',
      'code: "xamdam"',
      'code: "xamtro"',
      'code: "xanhthan"',
      'code: "xanhda"',
      'code: "trang"',
      'code: "kem"',
      'code: "be"',
      'code: "covit"',
      'code: "reu"',
    ]) {
      assert.ok(
        explorerSrc.includes(singleCode),
        `CatalogExplorer COLOR_OPTIONS must include single color_code entry ${singleCode}`
      );
    }
  });

  test("14. [Review B5] Editorial filter with 0 matches on loaded page does not dead-end when hasMorePages=true, and counts never misstate server total", async () => {
    const searchClient = await import("../src/lib/search-client.ts");
    const explorerSrc = readSrc("features/catalog/CatalogExplorer.tsx");

    // Empty state only when displayedCount === 0 AND !hasMorePages
    assert.equal(
      searchClient.shouldShowCatalogEmptyState({
        forcedEmpty: false,
        displayedCount: 0,
        hasMorePages: true,
      }),
      false,
      "must NOT enter dead-end empty branch when more server pages remain"
    );
    assert.equal(
      searchClient.shouldShowCatalogEmptyState({
        forcedEmpty: false,
        displayedCount: 0,
        hasMorePages: false,
      }),
      true
    );
    assert.equal(
      searchClient.shouldShowCatalogEmptyState({
        forcedEmpty: true,
        displayedCount: 5,
        hasMorePages: true,
      }),
      true
    );

    // Truthful count summary with editorial filter vs without
    const withEd = searchClient.buildCatalogCountSummary({
      hasEditorialFilter: true,
      displayedCount: 0,
      loadedCount: 24,
      shownCountSoFar: 24,
      serverTotal: 20_000,
      page: 1,
    });
    assert.equal(
      withEd.resultsSub.includes("Đang hiện 0 / 20.000"),
      false,
      "must not misstate server total as filtered total when editorial filter is active"
    );
    assert.ok(
      withEd.resultsSub.includes("24") && withEd.resultsSub.includes("20.000"),
      `expected loadedCount (24) and unfiltered serverTotal (20.000) distinction, got: ${withEd.resultsSub}`
    );

    const withoutEd = searchClient.buildCatalogCountSummary({
      hasEditorialFilter: false,
      displayedCount: 24,
      loadedCount: 24,
      shownCountSoFar: 24,
      serverTotal: 20_000,
      page: 1,
    });
    assert.ok(withoutEd.resultsSub.includes("Đang hiện 24 / 20.000 sản phẩm"));

    assert.ok(
      explorerSrc.includes("shouldShowCatalogEmptyState") &&
        explorerSrc.includes("buildCatalogCountSummary"),
      "CatalogExplorer must use shouldShowCatalogEmptyState & buildCatalogCountSummary"
    );
    assert.ok(
      explorerSrc.includes("Chưa có mẫu khớp trong"),
      "CatalogExplorer must render natural Vietnamese notice when 0 editorial matches on current loaded pages but more pages remain"
    );
  });

  test("15. [Review C1] matchGatewaySku matches color_code or folded color name, never substitutes another color/size when OOS, and mapSearchHitToCatalogProduct maps editorial colors by name first", async () => {
    const apiMod = await import("../src/lib/api.ts");
    const searchClient = await import("../src/lib/search-client.ts");

    assert.equal(
      typeof apiMod.matchGatewaySku,
      "function",
      "api.ts must export pure matchGatewaySku helper used by resolveGatewaySkuId"
    );

    // 2 colors with same size M, first color ("Đen than") OUT OF STOCK, second color ("Trắng") IN STOCK
    const skusFirstColorOos = [
      {
        id: "sku-den-m",
        sku_code: "POLO-DEN-M",
        available_stock: 0,
        is_active: true,
        attributes: { color_code: "den", color: "Đen than", size: "M" },
      },
      {
        id: "sku-trang-m",
        sku_code: "POLO-TRANG-M",
        available_stock: 12,
        is_active: true,
        attributes: { color_code: "trang", color: "Trắng", size: "M" },
      },
    ];

    // Must return null (NOT sku-trang-m!) when requested color is OOS even if slugified "denthan" is passed
    assert.equal(
      apiMod.matchGatewaySku(skusFirstColorOos, {
        colorCode: "denthan",
        colorName: "Đen than",
        size: "M",
      }),
      null,
      "must NOT fall back to another color when requested color + size is out of stock"
    );
    assert.equal(
      apiMod.matchGatewaySku(skusFirstColorOos, {
        colorCode: "den",
        colorName: "Đen than",
        size: "M",
      }),
      null
    );
    // Available color still matches cleanly
    assert.equal(
      apiMod.matchGatewaySku(skusFirstColorOos, {
        colorCode: "trang",
        colorName: "Trắng",
        size: "M",
      }),
      "sku-trang-m"
    );

    // Both colors in stock (Trắng listed FIRST): slugified "denthan" + colorName "Đen than" MUST pick sku-den-m
    const skusBothInStock = [
      {
        id: "sku-trang-m",
        sku_code: "POLO-TRANG-M",
        available_stock: 12,
        is_active: true,
        attributes: { color_code: "trang", color: "Trắng", size: "M" },
      },
      {
        id: "sku-den-m",
        sku_code: "POLO-DEN-M",
        available_stock: 5,
        is_active: true,
        attributes: { color_code: "den", color: "Đen than", size: "M" },
      },
      {
        id: "sku-be-l",
        sku_code: "POLO-BE-L",
        available_stock: 4,
        is_active: true,
        attributes: { color_code: "be", color: "Be cát", size: "L" },
      },
    ];
    assert.equal(
      apiMod.matchGatewaySku(skusBothInStock, {
        colorCode: "denthan",
        colorName: "Đen than",
        size: "M",
      }),
      "sku-den-m",
      "must match Đen than by accent-folded attributes.color when colorCode is slugified 'denthan'"
    );
    assert.equal(
      apiMod.matchGatewaySku(skusBothInStock, {
        colorCode: "becat",
        colorName: "Be cát",
        size: "L",
      }),
      "sku-be-l"
    );
    // Accent/case-insensitive name match
    assert.equal(
      apiMod.matchGatewaySku(skusBothInStock, {
        colorName: "den than",
        size: "m",
      }),
      "sku-den-m"
    );
    // Synthetic Cartesian pair that does NOT exist (Đen than × L) must return null (NOT sku-be-l or sku-den-m)
    assert.equal(
      apiMod.matchGatewaySku(skusBothInStock, {
        colorCode: "den",
        colorName: "Đen than",
        size: "L",
      }),
      null,
      "non-existent color x size pair must return null, not a different color or size"
    );

    // Fallback allowed ONLY when caller did not specify real color ("mac-dinh") / real size (undefined)
    assert.equal(
      apiMod.matchGatewaySku(skusBothInStock, {
        colorCode: "mac-dinh",
        colorName: "Mặc định",
        size: "L",
      }),
      "sku-be-l"
    );
    assert.equal(
      apiMod.matchGatewaySku(skusBothInStock, {
        colorCode: "mac-dinh",
        colorName: "Mặc định",
      }),
      "sku-trang-m"
    );
    // Real color without size: picks sellable SKU of THAT color only (never another color)
    assert.equal(
      apiMod.matchGatewaySku(skusBothInStock, {
        colorCode: "denthan",
        colorName: "Đen than",
      }),
      "sku-den-m"
    );
    assert.equal(
      apiMod.matchGatewaySku(skusFirstColorOos, {
        colorCode: "denthan",
        colorName: "Đen than",
      }),
      null
    );

    // mapSearchHitToCatalogProduct looks up editorial color by NAME first -> gets real code "den" instead of "denthan"
    const mappedEditorial = searchClient.mapSearchHitToCatalogProduct(
      {
        id: "prod-polo-detkim",
        shop_id: "shop-1",
        shop_name: "Xưởng Hà Nội",
        shop_slug: "xuong-ha-noi",
        category_id: "cat-polo",
        category_slug: "ao-polo",
        category_name: "Áo polo",
        name: "Áo Polo Dệt kim mịn Đen than",
        slug: "ao-polo-det-kim-min-den-than",
        thumbnail_url: "/products/ao-polo-det-kim-den.png",
        min_price_vnd: 369_000,
        max_price_vnd: 369_000,
        status: "APPROVED",
        rating_avg_x100: 490,
        review_count: 15,
        in_stock: true,
        sku_count: 10,
        score: 1,
        description_snippet: "",
        sku_ids: ["s1", "s2"],
        default_sku_id: "s1",
        colors: ["Đen than", "Xanh đá"],
        sizes: ["M", "L"],
      },
      { categoriesById: new Map(), shopsById: new Map() }
    );
    assert.equal(
      mappedEditorial.colors[0]?.code,
      "den",
      "editorial hit color 'Đen than' must resolve to real editorial code 'den' instead of 'denthan'"
    );
    assert.equal(mappedEditorial.colors[1]?.code, "xanhda");

    // Verify callers (ProductCard & JournalDetailClient) pass colorName
    const cardSrc = readSrc("components/ProductCard.tsx");
    const journalSrc = readSrc("app/(shop)/tap-chi/[slug]/JournalDetailClient.tsx");
    assert.ok(
      cardSrc.includes("colorName"),
      "ProductCard must pass colorName to resolveGatewaySkuId"
    );
    assert.ok(
      journalSrc.includes("colorName"),
      "JournalDetailClient must pass colorName to resolveGatewaySkuId"
    );
  });

  test("16. [Review C2] PDP ?color=<code|slugged-name> resolves exact code, then slugged color name, then default", async () => {
    const searchClient = await import("../src/lib/search-client.ts");
    const pdpSrc = readSrc("app/(shop)/san-pham/[slug]/ProductDetailClient.tsx");

    const colors = [
      { code: "trang", name: "Trắng" },
      { code: "den", name: "Đen than" },
      { code: "be", name: "Be cát" },
      { code: "kem", name: "Kem nhạt" },
    ];
    assert.equal(searchClient.resolveInitialProductColor(colors, "den", "trang"), "den");
    assert.equal(searchClient.resolveInitialProductColor(colors, "denthan", "trang"), "den");
    assert.equal(searchClient.resolveInitialProductColor(colors, "becat", "trang"), "be");
    assert.equal(searchClient.resolveInitialProductColor(colors, "kemnhat", "trang"), "kem");
    assert.equal(searchClient.resolveInitialProductColor(colors, "khongton", "trang"), "trang");
    assert.equal(searchClient.resolveInitialProductColor(colors, null, "trang"), "trang");
    assert.ok(
      pdpSrc.includes("resolveInitialProductColor"),
      "ProductDetailClient must resolve initialColor via resolveInitialProductColor"
    );
  });

  test("17. [Review C3] ShopHeader has zero inline style props and uses CSS module + tokens for search history", () => {
    const headerSrc = readSrc("components/ShopHeader.tsx");
    const headerCss = readSrc("components/ShopHeader.module.css");

    assert.equal(
      headerSrc.includes("style={{"),
      false,
      "ShopHeader.tsx must move inline styles into ShopHeader.module.css"
    );
    for (const cls of [".historyHeader", ".historyList", ".historyChipWrap"]) {
      assert.ok(headerCss.includes(cls), `ShopHeader.module.css must define ${cls}`);
    }
    assert.ok(
      headerCss.includes("var(--space-2)") && headerCss.includes("var(--nav-row)"),
      "ShopHeader.module.css new history styles must use tokens from tokens.css"
    );
  });

  test("18. [Review C5] tests/e2e-web/14_search_b1.test.mjs includes quick-add test for 'Đen than' verifying exact color_code & size in cart", () => {
    const e2eSrc = readFileSync(resolve(REPO_ROOT, "tests/e2e-web/14_search_b1.test.mjs"), "utf8");
    assert.ok(e2eSrc.includes("Đen than"), "14_search_b1.test.mjs must cover 'Đen than' quick-add");
    assert.ok(
      e2eSrc.includes("products/slug/"),
      "14_search_b1.test.mjs must resolve SKU via products/slug/"
    );
    assert.ok(
      e2eSrc.includes("/api/gw/cart/items"),
      "14_search_b1.test.mjs must add resolved SKU to cart"
    );
    assert.ok(
      e2eSrc.includes("color_code"),
      "14_search_b1.test.mjs must assert exact color_code in cart"
    );
  });
});
