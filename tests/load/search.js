import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';
import {
  buildSummaryOutput,
  classifyOutcome,
  createReliabilityMetrics,
  createTrend,
  discoverSites,
  envBool,
  envFloat,
  envInt,
  envStr,
  formatMs,
  jsonRequest,
  loadConfig,
  metricValue,
  parseJson,
  vuSyntheticIp,
} from './lib/common.js';

const CFG = loadConfig();
const TARGET_VUS = envInt('VUS', 50);
const DURATION = envStr('DURATION', '30s');
const RAMP_UP = envStr('RAMP_UP', TARGET_VUS >= 50 ? '5s' : '2s');
const RAMP_DOWN = envStr('RAMP_DOWN', '2s');
const SLEEP_MS = envInt('SLEEP_MS', 100);
const SUGGEST_RATIO = envFloat('SUGGEST_RATIO', 0.25);
const SEARCH_PATH = envStr('SEARCH_PATH', '/api/v1/search');
const SUGGEST_PATH = envStr('SUGGEST_PATH', '/api/v1/search/suggest');
const FALLBACK_PRODUCTS = envBool('FALLBACK_PRODUCTS', false);

// Paired Vietnamese diacritics & non-diacritics queries plus material/category filters.
const SEARCH_CASES = [
  { q: 'áo thun', sort: 'relevance' },
  { q: 'ao thun', sort: 'relevance' },
  { q: 'cotton', minPrice: 100000, maxPrice: 500000, sort: 'price_asc' },
  { q: 'quần jogger', sort: 'price_desc' },
  { q: 'quan jogger', sort: 'price_desc' },
  { q: 'áo polo', minPrice: 150000, maxPrice: 600000, sort: 'relevance' },
  { q: 'ao polo', minPrice: 150000, maxPrice: 600000, sort: 'price_asc' },
  { q: 'quần short', sort: 'price_asc' },
  { q: 'quan short', sort: 'price_desc' },
  { q: 'thoáng khí', sort: 'relevance' },
  { q: 'thoang khi', sort: 'relevance' },
];

const SUGGEST_PREFIXES = ['áo', 'ao', 'quần', 'quan', 'cot', 'jog', 'polo', 'short'];

const searchDuration = createTrend('search_duration');
const suggestDuration = createTrend('suggest_duration');
const searchRequests = new Counter('search_requests');
const suggestRequests = new Counter('suggest_requests');
const reliability = createReliabilityMetrics();

export const options = {
  scenarios: {
    search_load: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: RAMP_UP, target: TARGET_VUS },
        { duration: DURATION, target: TARGET_VUS },
        { duration: RAMP_DOWN, target: 0 },
      ],
      gracefulRampDown: '3s',
    },
  },
  thresholds: {
    search_duration: ['p(95)<200'],
    suggest_duration: ['p(95)<200'],
    server_errors_5xx: ['count==0'],
  },
};

function encodeQuery(params) {
  const parts = [];
  const keys = Object.keys(params);
  for (let i = 0; i < keys.length; i++) {
    const k = keys[i];
    const v = params[k];
    if (v !== undefined && v !== null && v !== '') {
      parts.push(`${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`);
    }
  }
  return parts.join('&');
}

function buildSearchUrl(mode, searchPath, testCase, shopId) {
  if (mode === 'fallback_products') {
    const params = {
      keyword: testCase.q,
      page: 1,
      page_size: 20,
    };
    if (testCase.minPrice) {
      params.min_price_vnd = testCase.minPrice;
    }
    if (testCase.maxPrice) {
      params.max_price_vnd = testCase.maxPrice;
    }
    if (testCase.sort && testCase.sort !== 'relevance') {
      params.sort_by = testCase.sort;
    }
    if (shopId) {
      params.shop_id = shopId;
    }
    return `${CFG.gatewayUrl}${searchPath}?${encodeQuery(params)}`;
  }

  const params = {
    q: testCase.q,
    sort: testCase.sort || 'relevance',
    page: 1,
    page_size: 20,
  };
  if (testCase.minPrice) {
    params.min_price = testCase.minPrice;
  }
  if (testCase.maxPrice) {
    params.max_price = testCase.maxPrice;
  }
  if (shopId) {
    params.shop_id = shopId;
  }
  return `${CFG.gatewayUrl}${searchPath}?${encodeQuery(params)}`;
}

export function setup() {
  const probeUrl = `${CFG.gatewayUrl}${SEARCH_PATH}?q=${encodeURIComponent('ao thun')}&page=1&page_size=5`;
  const probeRes = jsonRequest('GET', probeUrl, null, {
    timeout: CFG.httpTimeout,
    tags: { phase: 'setup', endpoint: 'probe_search' },
  });

  let mode = 'search';
  let activeSearchPath = SEARCH_PATH;
  let suggestEnabled = false;

  if (probeRes.status === 404) {
    if (FALLBACK_PRODUCTS) {
      console.warn(
        `[search setup] ${SEARCH_PATH} returned HTTP 404 (Swarm A search route not yet mounted); ` +
          `falling back to /api/v1/products?keyword=... because FALLBACK_PRODUCTS=true`
      );
      mode = 'fallback_products';
      activeSearchPath = '/api/v1/products';
    } else {
      throw new Error(
        `${SEARCH_PATH} returned HTTP 404 (Swarm A search service not mounted on ${CFG.gatewayUrl}). ` +
          `Mount /api/v1/search or re-run with -e FALLBACK_PRODUCTS=true to benchmark /api/v1/products.`
      );
    }
  } else if (probeRes.status === 200) {
    const sugRes = jsonRequest('GET', `${CFG.gatewayUrl}${SUGGEST_PATH}?q=ao`, null, {
      timeout: CFG.httpTimeout,
      tags: { phase: 'setup', endpoint: 'probe_suggest' },
    });
    suggestEnabled = sugRes.status === 200;

    // Spot-check Vietnamese diacritic vs non-diacritic parity once during setup.
    const diacriticRes = jsonRequest(
      'GET',
      `${CFG.gatewayUrl}${SEARCH_PATH}?q=${encodeURIComponent('áo thun')}&page=1&page_size=5`,
      null,
      { timeout: CFG.httpTimeout, tags: { phase: 'setup', endpoint: 'diacritic_check' } }
    );
    const pBody = parseJson(probeRes) || {};
    const dBody = parseJson(diacriticRes) || {};
    console.log(
      `[search setup] ${SEARCH_PATH} live (suggest=${suggestEnabled}) | ` +
        `parity check: "áo thun" total=${dBody.total ?? 'n/a'} vs "ao thun" total=${pBody.total ?? 'n/a'}`
    );
  } else if (probeRes.status !== 429) {
    throw new Error(`probe GET ${probeUrl} returned unexpected HTTP ${probeRes.status}: ${String(probeRes.body).slice(0, 200)}`);
  }

  const { sites } = discoverSites(CFG, 24);
  const shopIds = sites.map((s) => s.shop_id).filter(Boolean);

  console.log(
    `[search setup] mode=${mode} path=${activeSearchPath} suggest=${suggestEnabled} ` +
      `vus=${TARGET_VUS} duration=${DURATION} shops=${shopIds.length}`
  );

  return {
    mode: mode,
    searchPath: activeSearchPath,
    suggestEnabled: suggestEnabled,
    shopIds: shopIds,
  };
}

export default function (data) {
  const xffIp = CFG.spoofXff ? vuSyntheticIp(__VU) : '';

  if (data.suggestEnabled && Math.random() < SUGGEST_RATIO) {
    const prefix = SUGGEST_PREFIXES[(__VU + __ITER) % SUGGEST_PREFIXES.length];
    const shopParam =
      data.shopIds.length > 0 && __ITER % 3 === 0
        ? `&shop_id=${encodeURIComponent(data.shopIds[__ITER % data.shopIds.length])}`
        : '';
    const url = `${CFG.gatewayUrl}${SUGGEST_PATH}?q=${encodeURIComponent(prefix)}${shopParam}`;

    const res = jsonRequest('GET', url, null, {
      xffIp: xffIp,
      timeout: CFG.httpTimeout,
      tags: { group: 'suggest', endpoint: 'search_suggest' },
    });
    const outcome = classifyOutcome(res, reliability);
    if (outcome === 'ok') {
      suggestRequests.add(1);
      suggestDuration.add(res.timings.duration);
    }
    check(res, {
      'suggest: status 200 or 429 rate-limited': (r) => r.status === 200 || r.status === 429,
    });
  } else {
    const testCase = SEARCH_CASES[(__VU + __ITER) % SEARCH_CASES.length];
    const shopId =
      data.shopIds.length > 0 && __ITER % 4 === 0 ? data.shopIds[__ITER % data.shopIds.length] : '';
    const url = buildSearchUrl(data.mode, data.searchPath, testCase, shopId);

    const res = jsonRequest('GET', url, null, {
      xffIp: xffIp,
      timeout: CFG.httpTimeout,
      tags: { group: 'search', endpoint: data.mode === 'search' ? 'search_query' : 'products_fallback' },
    });
    const outcome = classifyOutcome(res, reliability);
    if (outcome === 'ok') {
      searchRequests.add(1);
      searchDuration.add(res.timings.duration);
    }
    check(res, {
      'search: status 200 or 429 rate-limited': (r) => r.status === 200 || r.status === 429,
    });
  }

  if (SLEEP_MS > 0) {
    sleep(SLEEP_MS / 1000);
  }
}

export function handleSummary(data) {
  const sP50 = metricValue(data, 'search_duration', 'med');
  const sP95 = metricValue(data, 'search_duration', 'p(95)');
  const sgP50 = metricValue(data, 'suggest_duration', 'med');
  const sgP95 = metricValue(data, 'suggest_duration', 'p(95)');
  const sCount = metricValue(data, 'search_requests', 'count');
  const sgCount = metricValue(data, 'suggest_requests', 'count');
  const rl429 = metricValue(data, 'rate_limited_429', 'count');
  const err5xx = metricValue(data, 'server_errors_5xx', 'count');
  const err4xx = metricValue(data, 'client_errors_4xx', 'count');

  return buildSummaryOutput(
    `NexusMart Search Load Test (${TARGET_VUS} VUs, ${DURATION})`,
    [
      `Search API  : count=${sCount} | p50=${formatMs(sP50)} | p95=${formatMs(sP95)} (target < 200 ms)`,
      `Suggest API : count=${sgCount} | p50=${formatMs(sgP50)} | p95=${formatMs(sgP95)} (target < 200 ms)`,
      `Reliability : 429_rate_limited=${rl429} | 5xx_server_errors=${err5xx} | 4xx_client_errors=${err4xx}`,
    ],
    data
  );
}
