import { test } from "node:test";
import assert from "node:assert/strict";
import {
  addRecentSearch,
  buildSearchFilterUrl,
  normalizeSearchResponse,
  normalizeSuggestResponse,
  parseRecentSearches,
  parseSearchQueryParams,
  parseSuggestQuery,
  recentSearchStorageKey,
  runFallbackProductSearch,
  sanitizeSearchImageUrl,
} from "./searchTypes.ts";

test("parseSearchQueryParams sanitizes query, prices, sort, pagination and ignores shop_id", () => {
  const sp = new URLSearchParams({
    q: "  Áo   thun\u0000cotton  ",
    shop_id: "evil-other-shop",
    category: "ao-thun",
    min_price: "150000",
    max_price: "350000",
    sort: "price_asc",
    page: "3",
    page_size: "24",
  });
  const parsed = parseSearchQueryParams(sp);
  assert.equal(parsed.q, "Áo thun cotton");
  assert.equal(parsed.category, "ao-thun");
  assert.equal(parsed.min_price, 150000);
  assert.equal(parsed.max_price, 350000);
  assert.equal(parsed.sort, "price_asc");
  assert.equal(parsed.page, 3);
  assert.equal(parsed.page_size, 24);
  assert.equal("shop_id" in parsed, false);
});

test("parseSearchQueryParams rejects invalid prices, clamps page_size and truncates long q", () => {
  const longQ = "a".repeat(250);
  const parsed = parseSearchQueryParams({
    q: longQ,
    min_price: "-500",
    max_price: "12.5",
    sort: "unknown_sort",
    page: "0",
    page_size: "999",
  });
  assert.equal(parsed.q.length, 200);
  assert.equal(parsed.min_price, undefined);
  assert.equal(parsed.max_price, undefined);
  assert.equal(parsed.sort, "relevance");
  assert.equal(parsed.page, 1);
  assert.equal(parsed.page_size, 48);
});

test("parseSuggestQuery trims and limits to 120 characters", () => {
  assert.equal(parseSuggestQuery("  "), "");
  assert.equal(parseSuggestQuery("  áo   polo  "), "áo polo");
  assert.equal(parseSuggestQuery("x".repeat(160)).length, 120);
});

test("normalizeSearchResponse maps gateway SearchResponse, sanitizes URLs and filters foreign shops", () => {
  const raw = {
    items: [
      {
        id: "p-1",
        slug: "ao-thun-trang",
        shop_id: "shop-a",
        category_slug: "ao-thun",
        category_name: "Áo thun",
        name: "Áo thun Cotton Trắng",
        description: "Cotton 220gsm",
        images: ["javascript:alert(1)", "/products/ao-thun-cotton-trang.png"],
        min_price_vnd: 179000,
        original_price_vnd: 219000,
        total_stock: 15,
        in_stock: true,
      },
      {
        id: "p-foreign",
        shop_id: "shop-b",
        name: "Foreign item",
        min_price_vnd: 99000,
      },
    ],
    total: 1,
    page: 1,
    page_size: 12,
    facets: {
      categories: [{ value: "ao-thun", label: "Áo thun", count: 4 }],
      price_ranges: [{ value: "under_200k", label: "Dưới 200k", count: 2 }],
    },
  };

  const res = normalizeSearchResponse(raw, { shopId: "shop-a" });
  assert.equal(res.items.length, 1);
  assert.equal(res.items[0].id, "p-1");
  assert.equal(res.items[0].slug, "ao-thun-trang");
  assert.equal(res.items[0].image, "/products/ao-thun-cotton-trang.png");
  assert.deepEqual(res.items[0].images, ["/products/ao-thun-cotton-trang.png"]);
  assert.equal(res.items[0].priceVnd, 179000);
  assert.equal(res.items[0].originalPriceVnd, 219000);
  assert.equal(res.facets.categories.length, 1);
  assert.equal(res.facets.categories[0].count, 4);
  assert.equal(res.facets.priceRanges.length, 1);
});

test("normalizeSearchResponse preserves camelCase BFF item fields (priceVnd, originalPriceVnd, maxPriceVnd, totalStock)", () => {
  const bffPayload = {
    items: [
      {
        id: "p-camel-1",
        slug: "ao-thun-cotton-compact-xanh-than",
        shopId: "shop-a",
        categorySlug: "ao-thun",
        categoryName: "Áo thun",
        name: "Áo thun Cotton Compact 220gsm Xanh than",
        description: "Cotton 220gsm",
        image: "/products/ao-thun-cotton-xanh-than.png",
        images: ["/products/ao-thun-cotton-xanh-than.png", "/models/model-ao-thun-xanh-than.jpg"],
        priceVnd: 189000,
        maxPriceVnd: 209000,
        originalPriceVnd: 229000,
        totalStock: 178,
        inStock: true,
      },
    ],
    total: 1,
    page: 1,
    page_size: 12,
    facets: { categories: [], priceRanges: [] },
  };

  const res = normalizeSearchResponse(bffPayload, { shopId: "shop-a" });
  assert.equal(res.items.length, 1);
  assert.equal(res.items[0].priceVnd, 189000);
  assert.equal(res.items[0].maxPriceVnd, 209000);
  assert.equal(res.items[0].originalPriceVnd, 229000);
  assert.equal(res.items[0].totalStock, 178);
  assert.equal(res.items[0].inStock, true);
});

test("normalizeSuggestResponse handles strings and objects, deduplicates and caps at 8", () => {
  const res = normalizeSuggestResponse({
    suggestions: [
      { text: "Áo thun cotton", kind: "product", product_id: "p1", min_price_vnd: 179000 },
      { text: "áo thun cotton", kind: "query" },
      "Quần short",
      { text: "Áo polo", kind: "category", category_slug: "ao-polo" },
    ],
  });
  assert.equal(res.suggestions.length, 3);
  assert.equal(res.suggestions[0].kind, "product");
  assert.equal(res.suggestions[0].minPriceVnd, 179000);
  assert.equal(res.suggestions[1].text, "Quần short");
  assert.equal(res.suggestions[2].categorySlug, "ao-polo");
});

test("sanitizeSearchImageUrl blocks unsafe schemes and protocol-relative URLs", () => {
  assert.equal(sanitizeSearchImageUrl("/products/a.png"), "/products/a.png");
  assert.equal(
    sanitizeSearchImageUrl("https://cdn.nexusmart.vn/img/a.jpg"),
    "https://cdn.nexusmart.vn/img/a.jpg"
  );
  assert.equal(sanitizeSearchImageUrl("http://cdn.nexusmart.vn/img/a.jpg"), "");
  assert.equal(sanitizeSearchImageUrl("javascript:alert(1)"), "");
  assert.equal(sanitizeSearchImageUrl("data:image/png;base64,abc"), "");
  assert.equal(sanitizeSearchImageUrl("//evil.example/a.png"), "");
});

test("runFallbackProductSearch filters by category, price, sort and paginates", () => {
  const sampleProducts = [
    {
      id: "1",
      slug: "ao-thun-1",
      name: "Áo thun Cotton 1",
      shortName: "AT1",
      categorySlug: "ao-thun",
      categoryName: "Áo thun",
      shopId: "shop-a",
      gender: "unisex",
      price: 180000,
      image: "/products/a.png",
      gallery: [],
      colors: [],
      sizes: ["M"],
      skus: [{ skuCode: "S1", colorCode: "W", size: "M", stock: 5, price: 180000 }],
      fabric: "Cotton",
      gsm: 220,
      fit: "Regular",
      origin: "VN",
      careGuide: [],
      highlights: [],
      description: "Cotton",
      sizeChart: [],
      createdAt: "2026-09-01T00:00:00Z",
    },
    {
      id: "2",
      slug: "ao-polo-2",
      name: "Áo Polo Pique",
      shortName: "AP2",
      categorySlug: "ao-polo",
      categoryName: "Áo polo",
      shopId: "shop-a",
      gender: "nam",
      price: 320000,
      image: "/products/b.png",
      gallery: [],
      colors: [],
      sizes: ["L"],
      skus: [{ skuCode: "S2", colorCode: "B", size: "L", stock: 2, price: 320000 }],
      fabric: "Pique",
      gsm: 210,
      fit: "Regular",
      origin: "VN",
      careGuide: [],
      highlights: [],
      description: "Polo",
      sizeChart: [],
      createdAt: "2026-10-01T00:00:00Z",
    },
    {
      id: "3",
      slug: "ao-thun-3",
      name: "Áo thun Oversize",
      shortName: "AT3",
      categorySlug: "ao-thun",
      categoryName: "Áo thun",
      shopId: "shop-a",
      gender: "unisex",
      price: 250000,
      image: "/products/c.png",
      gallery: [],
      colors: [],
      sizes: ["L"],
      skus: [{ skuCode: "S3", colorCode: "D", size: "L", stock: 0, price: 250000 }],
      fabric: "Cotton",
      gsm: 260,
      fit: "Oversize",
      origin: "VN",
      careGuide: [],
      highlights: [],
      description: "Heavyweight",
      sizeChart: [],
      createdAt: "2026-11-01T00:00:00Z",
    },
  ];

  const identitySearch = (_q, catalog) => catalog;

  const byPriceDesc = runFallbackProductSearch(
    sampleProducts,
    {
      q: "",
      category: "ao-thun",
      min_price: 150000,
      max_price: 300000,
      sort: "price_desc",
      page: 1,
      page_size: 1,
    },
    identitySearch
  );
  assert.equal(byPriceDesc.total, 2);
  assert.equal(byPriceDesc.items.length, 1);
  assert.equal(byPriceDesc.items[0].id, "3");
  assert.equal(byPriceDesc.items[0].inStock, false);
  assert.equal(byPriceDesc.facets.categories.length, 1);
  assert.equal(byPriceDesc.facets.categories[0].count, 2);

  const byNewest = runFallbackProductSearch(
    sampleProducts,
    { q: "", category: "", sort: "newest", page: 1, page_size: 12 },
    identitySearch
  );
  assert.deepEqual(
    byNewest.items.map((i) => i.id),
    ["3", "2", "1"]
  );
});

test("buildSearchFilterUrl omits defaults and encodes query parameters", () => {
  assert.equal(buildSearchFilterUrl({}), "/tim-kiem");
  assert.equal(
    buildSearchFilterUrl({
      q: "áo thun",
      category: "ao-thun",
      minPrice: 100000,
      maxPrice: 300000,
      sort: "price_asc",
      page: 2,
    }),
    "/tim-kiem?q=%C3%A1o+thun&category=ao-thun&min_price=100000&max_price=300000&sort=price_asc&page=2"
  );
});

test("recent search history helpers deduplicate, cap at 6 and scope key by shopId", () => {
  assert.equal(recentSearchStorageKey("shop-123", "Brand"), "nm_recent_search:shop-123");
  assert.equal(
    recentSearchStorageKey(undefined, "Xưởng Bạch Đằng"),
    "nm_recent_search:Xưởng Bạch Đằng"
  );

  let history = [];
  for (const q of ["a", "b", "c", "d", "e", "f", "g", "C"]) {
    history = addRecentSearch(history, q);
  }
  assert.equal(history.length, 6);
  assert.equal(history[0], "C");
  assert.equal(history.includes("c"), false);
  assert.deepEqual(parseRecentSearches(JSON.stringify(history)), history);
  assert.deepEqual(parseRecentSearches("{bad json"), []);
});
