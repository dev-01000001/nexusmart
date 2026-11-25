/**
 * NexusMart B1 Search Load Test (k6) — >= 20,000 products, p95 < 200ms, error rate < 0.5%.
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
const ADMIN_EMAIL = __ENV.ADMIN_EMAIL || __ENV.ADMIN_BOOTSTRAP_EMAIL || "admin@nexusmart.local";
const ADMIN_PASSWORD = __ENV.ADMIN_PASSWORD || __ENV.ADMIN_BOOTSTRAP_PASSWORD || "AdminDevPassword123!";
const VUS = Number(__ENV.VUS || "20");
const DURATION = __ENV.DURATION || "60s";

const keywordDuration = new Trend("search_keyword_duration", true);
const filterDuration = new Trend("search_filter_duration", true);
const hybridDuration = new Trend("search_hybrid_duration", true);
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
  "ao thun cotton thoang khi mac mua he",
  "quan short the thao co gian chay bo",
  "ao polo pique cong so toi gian",
  "do lot nam soi modal mem min",
  "quan jogger ni chan cua mac o nha",
];

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

function queryIndexedTotal() {
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
  return data.total;
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
      if (queryIndexedTotal() >= MIN_PRODUCTS) {
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

export function setup() {
  let total = queryIndexedTotal();
  if (total < MIN_PRODUCTS && AUTO_REINDEX) {
    triggerAndWaitReindex();
    total = queryIndexedTotal();
  }
  if (total < MIN_PRODUCTS) {
    throw new Error(
      `Indexed product count is ${total}, below required MIN_PRODUCTS=${MIN_PRODUCTS}. ` +
        `Populate products first with: node tools/loadgen/generate-products.mjs --count ${MIN_PRODUCTS}`
    );
  }

  const catRes = http.get(`${GATEWAY_URL}/api/v1/categories`);
  const catData = parseJSON(catRes);
  const categoryIds = (catData && Array.isArray(catData.categories) ? catData.categories : [])
    .map((c) => c.id)
    .filter(Boolean);

  return {
    indexedTotal: total,
    categoryIds,
  };
}

function pick(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
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
    // 15% >=3-word hybrid RRF search
    const qs = encodeParams({
      q: pick(HYBRID_QUERIES),
      mode: "auto",
      sort_by: "relevance",
      page: 1,
      page_size: 20,
    });
    const res = http.get(`${GATEWAY_URL}/api/v1/search?${qs}`, {
      tags: { endpoint: "search_hybrid" },
    });
    hybridDuration.add(res.timings.duration);
    const body = parseJSON(res);
    check(res, {
      "hybrid status 200": (x) => x.status === 200,
      "hybrid not degraded": () => Boolean(body && body.degraded_mode === ""),
      "hybrid used_vector true": () => Boolean(body && body.used_vector === true),
    });
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
