/**
 * NexusMart B1 Search Load Test (k6) — >= 20,000 products, p95 < 200ms, error rate < 0.5%.
 *
 * Traffic mix: 40% keyword, 30% multi-filter, 15% hybrid RRF (mode=auto), 15% suggest.
 * The hybrid branch rotates evenly across realistic storefront shapes WITH shop_id
 * (unfiltered baseline, shop, shop+matching category, shop+size, shop+max_price_vnd=150000),
 * records per-shape duration Trends (p(95)<200), and distinguishes product_fallback vs keyword_only.
 *
 * Prerequisites & Runbook (run from repository root):
 *   1. Start stack + baseline seed:
 *        make up && make seed
 *   2. Populate >= 20,000 public products using the existing bulk generator:
 *        node tools/loadgen/generate-products.mjs --count 20000
 *      (Creates 20,000 APPROVED products with 2–3 SKUs each in local product_db.)
 *   3. Trigger full search reindex (or let k6 setup() do it automatically with AUTO_REINDEX=1):
 *        # Admin login -> POST /api/v1/admin/search/reindex -> poll COMPLETED
 *   4. Run k6 (tune RATE_LIMIT_IP_PER_MIN on gateway if driving high RPS from a single host IP):
 *        k6 run tests/k6/search.js
 *      Or via Docker on the compose network:
 *        docker run --rm -i --network nexusmart_default \
 *          -e GATEWAY_URL=http://gateway:8080 \
 *          grafana/k6 run - < tests/k6/search.js
 *   5. Optional environment overrides:
 *        GATEWAY_URL=http://localhost:8080
 *        MIN_PRODUCTS=20000
 *        AUTO_REINDEX=1               (set 0 to skip automatic reindex when total < MIN_PRODUCTS)
 *        SEARCH_SHOP_ID=              (shop_id for hybrid storefront shapes; default: largest shop from shop_facets)
 *        ADMIN_EMAIL=admin@nexusmart.local
 *        ADMIN_PASSWORD=AdminDevPassword123!
 *        VUS=20
 *        DURATION=60s
 *   6. Cleanup generated loadgen products afterwards:
 *        node tools/loadgen/generate-products.mjs --cleanup
 *        (then re-run POST /api/v1/admin/search/reindex to clean Meilisearch)
 */

import http from "k6/http";
import { check, sleep } from "k6";
import { Trend } from "k6/metrics";

const GATEWAY_URL = (__ENV.GATEWAY_URL || "http://localhost:8080").replace(/\/+$/, "");
const MIN_PRODUCTS = Number(__ENV.MIN_PRODUCTS || "20000");
const AUTO_REINDEX = (__ENV.AUTO_REINDEX || "1") !== "0";
const SEARCH_SHOP_ID = (__ENV.SEARCH_SHOP_ID || "").trim();
const ADMIN_EMAIL = __ENV.ADMIN_EMAIL || __ENV.ADMIN_BOOTSTRAP_EMAIL || "admin@nexusmart.local";
const ADMIN_PASSWORD = __ENV.ADMIN_PASSWORD || __ENV.ADMIN_BOOTSTRAP_PASSWORD || "AdminDevPassword123!";
const VUS = Number(__ENV.VUS || "20");
const DURATION = __ENV.DURATION || "60s";

const keywordDuration = new Trend("search_keyword_duration", true);
const filterDuration = new Trend("search_filter_duration", true);
const hybridDuration = new Trend("search_hybrid_duration", true);
const hybridShapeTrends = {
  unfiltered: new Trend("search_hybrid_unfiltered_duration", true),
  shop: new Trend("search_hybrid_shop_duration", true),
  shop_category: new Trend("search_hybrid_shop_category_duration", true),
  shop_size: new Trend("search_hybrid_shop_size_duration", true),
  shop_price: new Trend("search_hybrid_shop_price_duration", true),
};
const suggestDuration = new Trend("search_suggest_duration", true);

export const options = {
  scenarios: {
    search_mix: {
      executor: "constant-vus",
      vus: VUS,
      duration: DURATION,
    },
  },
  thresholds: {
    http_req_duration: ["p(95)<200"],
    http_req_failed: ["rate<0.005"],
    checks: ["rate>0.995"],
    search_suggest_duration: ["p(95)<80"],
    search_hybrid_duration: ["p(95)<200"],
    search_hybrid_unfiltered_duration: ["p(95)<200"],
    search_hybrid_shop_duration: ["p(95)<200"],
    search_hybrid_shop_category_duration: ["p(95)<200"],
    search_hybrid_shop_size_duration: ["p(95)<200"],
    search_hybrid_shop_price_duration: ["p(95)<200"],
  },
  setupTimeout: "10m",
};

const KEYWORD_QUERIES = [
  "ao thun",
  "ao phong",
  "quan short",
  "do lot",
  "ao polo",
  "quan jogger",
  "ao thn",
  "cotton",
  "dryfit",
  "boxer",
];

const HYBRID_QUERIES = [
  { q: "ao thun cotton thoang khi mac mua he", categorySlug: "ao-thun" },
  { q: "quan short the thao co gian chay bo", categorySlug: "quan-short" },
  { q: "ao polo pique cong so toi gian", categorySlug: "ao-polo" },
  { q: "do lot nam soi modal mem min", categorySlug: "do-lot" },
  { q: "quan jogger ni chan cua mac o nha", categorySlug: "quan-jogger" },
];
const HYBRID_MAX_PRICE_VND = 150000;

const SUGGEST_PREFIXES = ["ao", "quan", "cot", "box", "po", "the"];
const COLORS = ["den", "trang", "xamda", "xanhthan", "reu", "be"];
const SIZES = ["M", "L", "XL"];
const SORTS = ["relevance", "price_asc", "price_desc", "newest"];

function parseJSON(res) {
  try {
    return res.json();
  } catch (_) {
    return null;
  }
}

function encodeParams(params) {
  const parts = [];
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue;
    parts.push(`${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`);
  }
  return parts.join("&");
}

function queryIndexSummary() {
  const res = http.get(`${GATEWAY_URL}/api/v1/search?page=1&page_size=1`);
  if (res.status !== 200) {
    throw new Error(`setup GET /api/v1/search returned HTTP ${res.status}: ${res.body}`);
  }
  const data = parseJSON(res);
  if (!data || typeof data.total !== "number") {
    throw new Error(`setup GET /api/v1/search invalid payload: ${res.body}`);
  }
  if (data.degraded_mode && data.degraded_mode !== "") {
    throw new Error(`setup GET /api/v1/search is degraded (${data.degraded_mode}); ensure search & meilisearch are healthy`);
  }
  return {
    total: data.total,
    shopFacets: Array.isArray(data.shop_facets) ? data.shop_facets : [],
  };
}

function pickLargestShopFacet(shopFacets) {
  if (!Array.isArray(shopFacets)) return null;
  let best = null;
  for (const facet of shopFacets) {
    if (!facet || !facet.id) continue;
    const count = Number(facet.count) || 0;
    if (!best || count > best.count) {
      best = {
        id: String(facet.id),
        slug: facet.slug ? String(facet.slug) : "",
        count,
      };
    }
  }
  return best;
}

function resolveHybridShopId(initialShopFacets) {
  if (SEARCH_SHOP_ID) {
    console.log(`[setup] hybrid shop_id=${SEARCH_SHOP_ID} (from SEARCH_SHOP_ID)`);
    return SEARCH_SHOP_ID;
  }
  let best = pickLargestShopFacet(initialShopFacets);
  if (!best) {
    const res = http.get(`${GATEWAY_URL}/api/v1/search?q=ao&page=1&page_size=1`);
    const data = parseJSON(res);
    best = pickLargestShopFacet(data && data.shop_facets);
  }
  if (!best) {
    throw new Error("setup could not resolve shop_id from search shop_facets; set SEARCH_SHOP_ID");
  }
  const detail = best.slug ? `slug=${best.slug}, count=${best.count}` : `count=${best.count}`;
  console.log(`[setup] auto-selected hybrid shop_id=${best.id} (${detail})`);
  return best.id;
}

function triggerAndWaitReindex() {
  const loginRes = http.post(
    `${GATEWAY_URL}/api/v1/auth/login`,
    JSON.stringify({ email: ADMIN_EMAIL, password: ADMIN_PASSWORD }),
    { headers: { "Content-Type": "application/json" } }
  );
  if (loginRes.status !== 200) {
    throw new Error(`setup admin login failed (HTTP ${loginRes.status}): ${loginRes.body}`);
  }
  const loginBody = parseJSON(loginRes);
  const token = loginBody && loginBody.access_token;
  if (!token) {
    throw new Error("setup admin login response missing access_token");
  }
  const authHeaders = {
    Authorization: `Bearer ${token}`,
    "Content-Type": "application/json",
  };

  let jobId = "";
  const triggerRes = http.post(
    `${GATEWAY_URL}/api/v1/admin/search/reindex`,
    JSON.stringify({
      reindex_products: true,
      reindex_embeddings: true,
      reindex_categories: true,
      reindex_shops: true,
      batch_size: 500,
    }),
    { headers: authHeaders }
  );

  if (triggerRes.status === 202) {
    const body = parseJSON(triggerRes);
    jobId = body && body.job_id ? body.job_id : "";
  } else if (triggerRes.status === 409) {
    // Another reindex is already running; poll total until it reaches MIN_PRODUCTS.
    for (let i = 0; i < 180; i++) {
      sleep(2);
      if (queryIndexSummary().total >= MIN_PRODUCTS) {
        return;
      }
    }
    throw new Error("setup timed out waiting for existing reindex job");
  } else {
    throw new Error(`setup POST /api/v1/admin/search/reindex returned HTTP ${triggerRes.status}: ${triggerRes.body}`);
  }

  for (let i = 0; i < 240; i++) {
    sleep(2);
    const stRes = http.get(`${GATEWAY_URL}/api/v1/admin/search/reindex/${encodeURIComponent(jobId)}`, {
      headers: authHeaders,
    });
    if (stRes.status !== 200) {
      continue;
    }
    const st = parseJSON(stRes);
    if (!st) continue;
    if (st.status === "COMPLETED") {
      return;
    }
    if (st.status === "FAILED") {
      throw new Error(`setup reindex job ${jobId} FAILED: ${st.error_message || "unknown"}`);
    }
  }
  throw new Error(`setup reindex job ${jobId} did not finish within timeout`);
}

function buildHybridCategoryPairs(categories) {
  const idBySlug = {};
  for (const c of categories) {
    if (c && c.slug && c.id) {
      idBySlug[String(c.slug)] = String(c.id);
    }
  }
  const pairs = [];
  const missing = [];
  for (const item of HYBRID_QUERIES) {
    const categoryId = idBySlug[item.categorySlug];
    if (categoryId) {
      pairs.push({ q: item.q, categorySlug: item.categorySlug, categoryId });
    } else {
      missing.push(item.categorySlug);
    }
  }
  return { pairs, missing };
}

function buildHybridShapes(hasCategoryPairs) {
  const shapes = ["unfiltered", "shop"];
  if (hasCategoryPairs) {
    shapes.push("shop_category");
  }
  shapes.push("shop_size", "shop_price");
  return shapes;
}

export function setup() {
  let summary = queryIndexSummary();
  if (summary.total < MIN_PRODUCTS && AUTO_REINDEX) {
    triggerAndWaitReindex();
    summary = queryIndexSummary();
  }
  if (summary.total < MIN_PRODUCTS) {
    throw new Error(
      `Indexed product count is ${summary.total}, below required MIN_PRODUCTS=${MIN_PRODUCTS}. ` +
        `Populate products first with: node tools/loadgen/generate-products.mjs --count ${MIN_PRODUCTS}`
    );
  }

  const shopId = resolveHybridShopId(summary.shopFacets);

  const catRes = http.get(`${GATEWAY_URL}/api/v1/categories`);
  const catData = parseJSON(catRes);
  const categories = catData && Array.isArray(catData.categories) ? catData.categories : [];
  const categoryIds = categories.map((c) => c.id).filter(Boolean);
  const { pairs: hybridCategoryPairs, missing: missingSlugs } = buildHybridCategoryPairs(categories);
  if (missingSlugs.length > 0) {
    console.warn(`[setup] skipping missing hybrid category slugs: ${missingSlugs.join(", ")}`);
  }
  const hybridShapes = buildHybridShapes(hybridCategoryPairs.length > 0);
  console.log(
    `[setup] hybrid shapes=${hybridShapes.join(",")} category_pairs=${hybridCategoryPairs.length}/${HYBRID_QUERIES.length}`
  );

  return {
    indexedTotal: summary.total,
    categoryIds,
    shopId,
    hybridCategoryPairs,
    hybridShapes,
  };
}

let hybridSeq = 0;

function pick(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}

function buildHybridParams(shape, data) {
  const params = {
    mode: "auto",
    sort_by: "relevance",
    page: 1,
    page_size: 20,
  };
  if (shape === "shop_category") {
    const pair = pick(data.hybridCategoryPairs);
    params.q = pair.q;
    params.shop_id = data.shopId;
    params.category_id = pair.categoryId;
    return params;
  }
  params.q = pick(HYBRID_QUERIES).q;
  if (shape === "shop") {
    params.shop_id = data.shopId;
  } else if (shape === "shop_size") {
    params.shop_id = data.shopId;
    params.size = pick(SIZES);
  } else if (shape === "shop_price") {
    params.shop_id = data.shopId;
    params.max_price_vnd = HYBRID_MAX_PRICE_VND;
  }
  return params;
}

export default function (data) {
  const r = Math.random();

  if (r < 0.4) {
    // 40% keyword / typo / synonym search
    const q = pick(KEYWORD_QUERIES);
    const qs = encodeParams({ q, page: 1, page_size: 20 });
    const res = http.get(`${GATEWAY_URL}/api/v1/search?${qs}`, {
      tags: { endpoint: "search_keyword" },
    });
    keywordDuration.add(res.timings.duration);
    const body = parseJSON(res);
    check(res, {
      "keyword status 200": (x) => x.status === 200,
      "keyword not degraded": () => Boolean(body && body.degraded_mode === ""),
      "keyword omits commission_rate_bps": (x) => !x.body.includes("commission_rate_bps"),
    });
  } else if (r < 0.7) {
    // 30% multi-filter + sort + pagination search
    const params = {
      q: pick(KEYWORD_QUERIES),
      color: pick(COLORS),
      size: pick(SIZES),
      in_stock_only: "true",
      min_price_vnd: 50000,
      max_price_vnd: 800000,
      sort_by: pick(SORTS),
      page: 1 + Math.floor(Math.random() * 3),
      page_size: 20,
    };
    if (data.categoryIds.length > 0 && Math.random() < 0.5) {
      params.category_id = pick(data.categoryIds);
    }
    const qs = encodeParams(params);
    const res = http.get(`${GATEWAY_URL}/api/v1/search?${qs}`, {
      tags: { endpoint: "search_filter" },
    });
    filterDuration.add(res.timings.duration);
    const body = parseJSON(res);
    check(res, {
      "filter status 200": (x) => x.status === 200,
      "filter not degraded": () => Boolean(body && body.degraded_mode === ""),
      "filter has items array": () => Boolean(body && Array.isArray(body.items)),
    });
  } else if (r < 0.85) {
    // 15% >=3-word hybrid RRF search, rotating evenly across storefront shapes
    const shapes = data.hybridShapes;
    const shape = shapes[((typeof __VU === "number" ? __VU : 0) + hybridSeq++) % shapes.length];
    const qs = encodeParams(buildHybridParams(shape, data));
    const res = http.get(`${GATEWAY_URL}/api/v1/search?${qs}`, {
      tags: { endpoint: "search_hybrid", shape },
    });
    hybridDuration.add(res.timings.duration, { shape });
    if (hybridShapeTrends[shape]) {
      hybridShapeTrends[shape].add(res.timings.duration);
    }
    const body = parseJSON(res);
    const degradedMode = body && typeof body.degraded_mode === "string" ? body.degraded_mode : null;
    check(
      res,
      {
        "hybrid status 200": (x) => x.status === 200,
        "hybrid not degraded": () => degradedMode === "",
        "hybrid not product_fallback": () => degradedMode !== "product_fallback",
        "hybrid not keyword_only": () => degradedMode !== "keyword_only",
        "hybrid used_vector true": () => Boolean(body && body.used_vector === true),
      },
      { endpoint: "search_hybrid", shape }
    );
  } else {
    // 15% autocomplete suggest
    const qs = encodeParams({ q: pick(SUGGEST_PREFIXES), limit: 8 });
    const res = http.get(`${GATEWAY_URL}/api/v1/search/suggest?${qs}`, {
      tags: { endpoint: "search_suggest" },
    });
    suggestDuration.add(res.timings.duration);
    const body = parseJSON(res);
    check(res, {
      "suggest status 200": (x) => x.status === 200,
      "suggest valid envelope": () => Boolean(body && Array.isArray(body.keywords) && Array.isArray(body.products)),
    });
  }

  sleep(0.1);
}
