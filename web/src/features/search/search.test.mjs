import { test } from "node:test";
import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import { dirname, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
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
  VALID_SEARCH_SORTS,
} from "./searchTypes.ts";

const __dirname = dirname(fileURLToPath(import.meta.url));
const WEB_SRC = resolve(__dirname, "../..");

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === "@/lib/server/gateway") {
      return { url: "mock:///gateway", shortCircuit: true };
    }
    if (specifier === "@/lib/server/sites") {
      return { url: "mock:///sites", shortCircuit: true };
    }
    if (specifier === "@/lib/server/http") {
      return { url: "mock:///http", shortCircuit: true };
    }
    if (specifier === "@/features/search/searchTypes") {
      return nextResolve(pathToFileURL(resolve(__dirname, "searchTypes.ts")).href, context);
    }
    return nextResolve(specifier, context);
  },
  load(url, context, nextLoad) {
    if (url === "mock:///gateway") {
      return {
        format: "module",
        shortCircuit: true,
        source: "export async function gw(path, opts) { return globalThis.__testSearchGw(path, opts); }",
      };
    }
    if (url === "mock:///sites") {
      return {
        format: "module",
        shortCircuit: true,
        source: "export async function requireRequestSite(req) { return globalThis.__testSearchSite(req); }",
      };
    }
    if (url === "mock:///http") {
      return {
        format: "module",
        shortCircuit: true,
        source: "export function apiRoute(handler) { return handler; }",
      };
    }
    return nextLoad(url, context);
  },
});

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

test("parseSearchQueryParams supports all gateway sorts including rating_desc", () => {
  const gatewayAllowed = new Set([
    "relevance",
    "price_asc",
    "price_desc",
    "newest",
    "rating_desc",
  ]);
  for (const s of VALID_SEARCH_SORTS) {
    assert.equal(gatewayAllowed.has(s), true, `sort ${s} must be accepted by gateway`);
  }
  const parsed = parseSearchQueryParams({ sort: "rating_desc", category: "ao-polo" });
  assert.equal(parsed.sort, "rating_desc");
  assert.equal(parsed.category, "ao-polo");
});

test("normalizeSearchResponse maps real gateway SearchProductsResponse, reads thumbnail_url/description_snippet, hides shop_facets and filters foreign shops", () => {
  const raw = {
    items: [
      {
        id: "p-1",
        shop_id: "shop-a",
        shop_name: "Xưởng Dệt Bạch Đằng",
        shop_slug: "bach-dang",
        category_id: "cat-uuid-1",
        category_slug: "ao-thun",
        category_name: "Áo thun",
        name: "Áo thun Cotton Trắng",
        slug: "ao-thun-trang",
        thumbnail_url: "/products/ao-thun-cotton-trang.png",
        min_price_vnd: 179000,
        max_price_vnd: 219000,
        status: "APPROVED",
        rating_avg_x100: 485,
        review_count: 14,
        in_stock: true,
        sku_count: 5,
        score: 0.94,
        description_snippet: "Cotton 220gsm co giãn thoáng mát",
        sku_ids: ["sku-1", "sku-2"],
        default_sku_id: "sku-1",
        colors: ["Trắng", "Đen"],
        sizes: ["M", "L"],
      },
      {
        id: "p-bad-img",
        shop_id: "shop-a",
        shop_name: "Xưởng Dệt Bạch Đằng",
        shop_slug: "bach-dang",
        category_id: "cat-uuid-1",
        category_slug: "ao-thun",
        category_name: "Áo thun",
        name: "Áo thun Ảnh Xấu",
        slug: "ao-thun-anh-xau",
        thumbnail_url: "javascript:alert(1)",
        min_price_vnd: 159000,
        max_price_vnd: 159000,
        status: "APPROVED",
        rating_avg_x100: 0,
        review_count: 0,
        in_stock: false,
        sku_count: 1,
        score: 0.5,
        description_snippet: "",
        sku_ids: [],
        default_sku_id: "",
        colors: [],
        sizes: [],
      },
      {
        id: "p-foreign",
        shop_id: "shop-b",
        shop_name: "Shop Khác",
        shop_slug: "shop-b",
        category_id: "cat-uuid-1",
        category_slug: "ao-thun",
        category_name: "Áo thun",
        name: "Foreign item",
        slug: "foreign-item",
        thumbnail_url: "/products/other.png",
        min_price_vnd: 99000,
        max_price_vnd: 99000,
        status: "APPROVED",
        rating_avg_x100: 0,
        review_count: 0,
        in_stock: true,
        sku_count: 1,
        score: 0.9,
        description_snippet: "Other shop",
        sku_ids: [],
        default_sku_id: "",
        colors: [],
        sizes: [],
      },
    ],
    total: 2,
    page: 1,
    page_size: 12,
    category_facets: [{ id: "cat-uuid-1", name: "Áo thun", slug: "ao-thun", count: 7 }],
    shop_facets: [{ id: "shop-a", name: "Xưởng Dệt Bạch Đằng", slug: "bach-dang", count: 7 }],
    did_you_mean: "ao thun",
    degraded_mode: "",
    used_vector: true,
  };

  const res = normalizeSearchResponse(raw, { shopId: "shop-a" });
  assert.equal(res.items.length, 2);
  assert.equal(res.items[0].id, "p-1");
  assert.equal(res.items[0].slug, "ao-thun-trang");
  assert.equal(res.items[0].shopId, "shop-a");
  assert.equal(res.items[0].description, "Cotton 220gsm co giãn thoáng mát");
  assert.equal(res.items[0].image, "/products/ao-thun-cotton-trang.png");
  assert.deepEqual(res.items[0].images, ["/products/ao-thun-cotton-trang.png"]);
  assert.equal(res.items[0].priceVnd, 179000);
  assert.equal(res.items[0].maxPriceVnd, 219000);
  assert.equal(res.items[0].inStock, true);
  assert.equal(res.items[0].totalStock, 0);
  assert.equal(res.items[0].ratingAvgX100, 485);
  assert.equal(res.items[0].reviewCount, 14);
  assert.equal(res.items[0].skuCount, 5);
  assert.equal(res.items[0].defaultSkuId, "sku-1");
  assert.deepEqual(res.items[0].skuIds, ["sku-1", "sku-2"]);
  assert.deepEqual(res.items[0].colors, ["Trắng", "Đen"]);
  assert.deepEqual(res.items[0].sizes, ["M", "L"]);

  assert.equal(res.items[1].image, "");
  assert.deepEqual(res.items[1].images, []);
  assert.equal(res.items[1].inStock, false);

  assert.deepEqual(res.facets.categories, [{ value: "ao-thun", label: "Áo thun", count: 7 }]);
  assert.equal("shop_facets" in res, false);
  assert.equal("shopFacets" in res, false);
  assert.equal("shops" in res.facets, false);
  assert.equal(res.didYouMean, "ao thun");
  assert.equal(res.usedVector, true);
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
    facets: { categories: [{ value: "ao-thun", label: "Áo thun", count: 1 }], priceRanges: [] },
  };

  const res = normalizeSearchResponse(bffPayload, { shopId: "shop-a" });
  assert.equal(res.items.length, 1);
  assert.equal(res.items[0].priceVnd, 189000);
  assert.equal(res.items[0].maxPriceVnd, 209000);
  assert.equal(res.items[0].originalPriceVnd, 229000);
  assert.equal(res.items[0].totalStock, 178);
  assert.equal(res.items[0].inStock, true);
  assert.deepEqual(res.facets.categories, [{ value: "ao-thun", label: "Áo thun", count: 1 }]);
});

test("normalizeSuggestResponse maps real gateway payload: products first, filters foreign shops, omits shops, dedupes and caps at 8", () => {
  const gatewayPayload = {
    keywords: ["Áo thun Cotton Trắng", "áo thun cổ tròn", "áo thun nam", "áo thun nữ"],
    categories: [
      { id: "cat-1", name: "Áo thun", slug: "ao-thun" },
      { id: "cat-2", name: "Áo polo", slug: "ao-polo" },
    ],
    shops: [
      { id: "shop-a", name: "Xưởng Dệt Bạch Đằng", slug: "bach-dang" },
      { id: "shop-b", name: "Xưởng May Sài Gòn", slug: "sai-gon" },
    ],
    trending: ["áo khoác gió", "quần short", "quần jogger", "tất cotton"],
    products: [
      {
        id: "prod-1",
        shop_id: "shop-a",
        name: "Áo thun Cotton Trắng",
        slug: "ao-thun-cotton-trang",
        category_slug: "ao-thun",
        min_price_vnd: 179000,
        thumbnail_url: "/products/ao-thun-cotton-trang.png",
      },
      {
        id: "prod-foreign",
        shop_id: "shop-b",
        name: "Áo thun shop khác",
        slug: "ao-thun-shop-khac",
        category_slug: "ao-thun",
        min_price_vnd: 199000,
      },
      {
        id: "prod-2",
        shop_id: "shop-a",
        name: "Áo Polo Pique Kem",
        slug: "ao-polo-pique-kem",
        category_slug: "ao-polo",
        min_price_vnd: 289000,
      },
    ],
  };

  const res = normalizeSuggestResponse(gatewayPayload, { shopId: "shop-a" });
  assert.equal(res.suggestions.length, 8);

  // Products come first, with productId + slug, and foreign shop product is excluded.
  assert.deepEqual(res.suggestions[0], {
    text: "Áo thun Cotton Trắng",
    kind: "product",
    productId: "prod-1",
    slug: "ao-thun-cotton-trang",
    categorySlug: "ao-thun",
    minPriceVnd: 179000,
  });
  assert.deepEqual(res.suggestions[1], {
    text: "Áo Polo Pique Kem",
    kind: "product",
    productId: "prod-2",
    slug: "ao-polo-pique-kem",
    categorySlug: "ao-polo",
    minPriceVnd: 289000,
  });

  // Categories follow products.
  assert.deepEqual(res.suggestions[2], {
    text: "Áo thun",
    kind: "category",
    categorySlug: "ao-thun",
  });
  assert.deepEqual(res.suggestions[3], {
    text: "Áo polo",
    kind: "category",
    categorySlug: "ao-polo",
  });

  // Keywords (deduplicated against product name) then trending as query suggestions.
  assert.deepEqual(
    res.suggestions.slice(4),
    [
      { text: "áo thun cổ tròn", kind: "query" },
      { text: "áo thun nam", kind: "query" },
      { text: "áo thun nữ", kind: "query" },
      { text: "áo khoác gió", kind: "query" },
    ]
  );

  // Shops and foreign products must never appear.
  const texts = res.suggestions.map((s) => s.text);
  assert.equal(texts.includes("Xưởng Dệt Bạch Đằng"), false);
  assert.equal(texts.includes("Xưởng May Sài Gòn"), false);
  assert.equal(texts.includes("Áo thun shop khác"), false);

  // Idempotent on already-normalized BFF output.
  assert.deepEqual(normalizeSuggestResponse(res), res);
});

test("GET /api/search route forwards gateway query param names (category_slug, min_price_vnd, max_price_vnd, sort_by) and locks shop_id", async () => {
  const { GET } = await import(
    pathToFileURL(resolve(WEB_SRC, "app/api/search/route.ts")).href
  );

  const captured = [];
  globalThis.__testSearchSite = () => ({ siteId: "site-a", shopId: "shop-a", host: "bach-dang.nexusmart.localhost" });
  globalThis.__testSearchGw = async (path, opts) => {
    captured.push({ path, opts });
    return {
      items: [
        {
          id: "p-1",
          shop_id: "shop-a",
          category_slug: "ao-thun",
          category_name: "Áo thun",
          name: "Áo thun Cotton",
          slug: "ao-thun-cotton",
          thumbnail_url: "/products/ao-thun-cotton-trang.png",
          min_price_vnd: 179000,
          max_price_vnd: 199000,
          in_stock: true,
          description_snippet: "Cotton 220gsm",
        },
      ],
      total: 1,
      page: 2,
      page_size: 24,
      category_facets: [{ id: "c-1", name: "Áo thun", slug: "ao-thun", count: 1 }],
      shop_facets: [{ id: "shop-a", name: "Bạch Đằng", slug: "bach-dang", count: 1 }],
      did_you_mean: "",
      degraded_mode: "",
      used_vector: false,
    };
  };

  const req = {
    nextUrl: new URL(
      "http://bach-dang.nexusmart.localhost/api/search?q=ao+thun&shop_id=evil-shop&category=ao-thun&min_price=150000&max_price=350000&sort=price_asc&page=2&page_size=24"
    ),
  };
  const res = await GET(req);
  assert.equal(res.status, 200);
  const body = await res.json();

  assert.equal(captured.length, 1);
  assert.equal(captured[0].path, "/api/v1/search");
  assert.equal(captured[0].opts.auth, "none");
  assert.deepEqual(captured[0].opts.query, {
    q: "ao thun",
    shop_id: "shop-a",
    category_slug: "ao-thun",
    min_price_vnd: 150000,
    max_price_vnd: 350000,
    sort_by: "price_asc",
    page: 2,
    page_size: 24,
  });
  assert.equal("category" in captured[0].opts.query, false);
  assert.equal("min_price" in captured[0].opts.query, false);
  assert.equal("max_price" in captured[0].opts.query, false);
  assert.equal("sort" in captured[0].opts.query, false);

  assert.equal(body.items.length, 1);
  assert.equal(body.items[0].image, "/products/ao-thun-cotton-trang.png");
  assert.equal(body.items[0].description, "Cotton 220gsm");
  assert.equal("shop_facets" in body, false);

  // Verify rating_desc and invalid sort normalization sent via sort_by.
  await GET({
    nextUrl: new URL("http://bach-dang.nexusmart.localhost/api/search?sort=rating_desc"),
  });
  assert.equal(captured[1].opts.query.sort_by, "rating_desc");

  await GET({
    nextUrl: new URL("http://bach-dang.nexusmart.localhost/api/search?sort=unsupported"),
  });
  assert.equal(captured[2].opts.query.sort_by, "relevance");
});

test("GET /api/search/suggest route normalizes gateway suggest payload and filters foreign shop products", async () => {
  const { GET } = await import(
    pathToFileURL(resolve(WEB_SRC, "app/api/search/suggest/route.ts")).href
  );

  globalThis.__testSearchSite = () => ({ siteId: "site-a", shopId: "shop-a", host: "bach-dang.nexusmart.localhost" });
  globalThis.__testSearchGw = async (path, opts) => {
    assert.equal(path, "/api/v1/search/suggest");
    assert.deepEqual(opts.query, { q: "ao", shop_id: "shop-a" });
    return {
      keywords: ["áo thun"],
      categories: [{ id: "c-1", name: "Áo polo", slug: "ao-polo" }],
      shops: [{ id: "shop-a", name: "Xưởng Dệt Bạch Đằng", slug: "bach-dang" }],
      trending: ["áo khoác"],
      products: [
        { id: "p-ok", shop_id: "shop-a", name: "Áo thun Bạch Đằng", slug: "ao-thun-bd", min_price_vnd: 179000 },
        { id: "p-bad", shop_id: "shop-other", name: "Áo thun shop lạ", slug: "ao-thun-la", min_price_vnd: 99000 },
      ],
    };
  };

  const res = await GET({
    nextUrl: new URL("http://bach-dang.nexusmart.localhost/api/search/suggest?q=ao&shop_id=shop-other"),
  });
  assert.equal(res.status, 200);
  const body = await res.json();
  assert.deepEqual(body.suggestions, [
    { text: "Áo thun Bạch Đằng", kind: "product", productId: "p-ok", slug: "ao-thun-bd", minPriceVnd: 179000 },
    { text: "Áo polo", kind: "category", categorySlug: "ao-polo" },
    { text: "áo thun", kind: "query" },
    { text: "áo khoác", kind: "query" },
  ]);
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
