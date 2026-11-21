import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';
import {
  buildSummaryOutput,
  calculateShippingQuote,
  classifyOutcome,
  createApprovedProduct,
  createReliabilityMetrics,
  createTrend,
  discoverProducts,
  ensureCategory,
  ensureSeller,
  envFloat,
  envInt,
  envStr,
  fetchProvinceAndWard,
  formatMs,
  jsonRequest,
  loadConfig,
  login,
  metricValue,
  parseJson,
  setupBuyerPool,
  shortRunId,
  uuidv4,
  vuSyntheticIp,
} from './lib/common.js';

const CFG = loadConfig();
const TARGET_VUS = envInt('VUS', 200);
const DURATION = envStr('DURATION', '30s');
const RAMP_UP = envStr('RAMP_UP', TARGET_VUS >= 50 ? '10s' : '2s');
const RAMP_DOWN = envStr('RAMP_DOWN', TARGET_VUS >= 50 ? '5s' : '1s');
const CHECKOUT_RATIO = envFloat('CHECKOUT_RATIO', 0.2);
const BUYER_POOL_SIZE = envInt('BUYER_POOL_SIZE', 10);
const PRODUCT_STOCK = envInt('PRODUCT_STOCK', 50000);
const SLEEP_MS = envInt('SLEEP_MS', 200);

const readDuration = createTrend('read_duration');
const checkoutDuration = createTrend('checkout_duration');
const checkoutCreated = new Counter('checkout_created');
const readRequests = new Counter('read_requests');
const reliability = createReliabilityMetrics();

export const options = {
  scenarios: {
    checkout_mixed: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: RAMP_UP, target: TARGET_VUS },
        { duration: DURATION, target: TARGET_VUS },
        { duration: RAMP_DOWN, target: 0 },
      ],
      gracefulRampDown: '5s',
    },
  },
  setupTimeout: '180s',
  thresholds: {
    read_duration: ['p(95)<300'],
    checkout_duration: ['p(95)<1000'],
    server_errors_5xx: ['count==0'],
  },
};

export function setup() {
  const runId = shortRunId();
  const admin = login(CFG, CFG.adminEmail, CFG.adminPassword);
  const { province, ward } = fetchProvinceAndWard(CFG);
  const seller = ensureSeller(CFG, admin.accessToken, province, ward, runId);
  const category = ensureCategory(CFG, admin.accessToken, runId);

  // Create an ephemeral high-stock product so load testing never drains seeded catalog SKUs.
  const { product, sku } = createApprovedProduct(CFG, seller.accessToken, admin.accessToken, category.id, {
    runId: runId,
    name: `Load Checkout Product ${runId}`,
    priceVnd: 199000,
    stock: PRODUCT_STOCK,
    weightGrams: 300,
  });

  const buyers = setupBuyerPool(CFG, BUYER_POOL_SIZE, province, ward, runId, 'k6-co-buyer');
  if (buyers.length === 0) {
    throw new Error('checkout setup failed: buyer pool is empty');
  }

  const quote = calculateShippingQuote(CFG, buyers[0].accessToken, seller.shopId, province, ward, 300, 199000);
  const catalog = discoverProducts(CFG, 20);
  const productIds = catalog.items.map((p) => p.id).filter(Boolean);
  if (productIds.indexOf(product.id) === -1) {
    productIds.push(product.id);
  }

  console.log(
    `[checkout setup] run=${runId} vus=${TARGET_VUS} duration=${DURATION} buyers=${buyers.length} ` +
      `sku=${sku.id} stock=${PRODUCT_STOCK} carrier=${quote.carrier_code}/${quote.service_code}`
  );

  return {
    runId: runId,
    shopId: seller.shopId,
    productId: product.id,
    skuId: sku.id,
    productIds: productIds,
    carrierCode: quote.carrier_code || 'ghn',
    serviceCode: quote.service_code || 'standard',
    buyers: buyers,
  };
}

function runReadRequest(data, buyer, xffIp) {
  const r = Math.random();
  let url = `${CFG.gatewayUrl}/api/v1/products?page=1&page_size=20`;
  let endpoint = 'list_products';
  let token = '';

  if (r < 0.25) {
    url = `${CFG.gatewayUrl}/api/v1/products?page=1&page_size=20`;
    endpoint = 'list_products';
  } else if (r < 0.40 && data.productIds.length > 0) {
    const pid = data.productIds[Math.floor(Math.random() * data.productIds.length)];
    url = `${CFG.gatewayUrl}/api/v1/products/${encodeURIComponent(pid)}`;
    endpoint = 'get_product';
  } else if (r < 0.55) {
    url = `${CFG.gatewayUrl}/api/v1/categories`;
    endpoint = 'list_categories';
  } else if (r < 0.70) {
    url = `${CFG.gatewayUrl}/api/v1/sites`;
    endpoint = 'list_sites';
  } else if (r < 0.85) {
    url = `${CFG.gatewayUrl}/api/v1/vouchers`;
    endpoint = 'list_vouchers';
  } else {
    url = `${CFG.gatewayUrl}/api/v1/orders?page=1&page_size=10`;
    endpoint = 'list_orders';
    token = buyer.accessToken;
  }

  const res = jsonRequest('GET', url, null, {
    token: token,
    xffIp: xffIp,
    timeout: CFG.httpTimeout,
    tags: { group: 'read', endpoint: endpoint },
  });

  const outcome = classifyOutcome(res, reliability);
  if (outcome === 'ok') {
    readRequests.add(1);
    readDuration.add(res.timings.duration);
  }

  check(res, {
    'read: status 200 or 429 rate-limited': (resp) => resp.status === 200 || resp.status === 429,
  });
}

function runCheckoutRequest(data, buyer, xffIp) {
  const idemKey = `load-co-${data.runId}-${__VU}-${__ITER}-${uuidv4()}`;
  const payload = {
    address_id: buyer.addressId,
    from_cart: false,
    items: [
      {
        sku_id: data.skuId,
        quantity: 1,
      },
    ],
    payment_method: 'COD',
    shipping_choices: [
      {
        shop_id: data.shopId,
        carrier_code: data.carrierCode,
        service_code: data.serviceCode,
      },
    ],
  };

  const res = jsonRequest('POST', `${CFG.gatewayUrl}/api/v1/checkout`, payload, {
    token: buyer.accessToken,
    xffIp: xffIp,
    headers: { 'Idempotency-Key': idemKey },
    timeout: CFG.httpTimeout,
    tags: { group: 'checkout', endpoint: 'create_checkout' },
  });

  const outcome = classifyOutcome(res, reliability);
  if (outcome === 'ok') {
    checkoutDuration.add(res.timings.duration);
    const body = parseJson(res);
    if (body && Array.isArray(body.orders) && body.orders.length > 0) {
      checkoutCreated.add(1);
    }
  }

  check(res, {
    'checkout: status 201/200 or 429 rate-limited': (resp) =>
      resp.status === 201 || resp.status === 200 || resp.status === 429,
  });
}

export default function (data) {
  const buyer = data.buyers[(__VU - 1) % data.buyers.length];
  const xffIp = CFG.spoofXff ? vuSyntheticIp(__VU) : '';

  if (Math.random() < CHECKOUT_RATIO) {
    runCheckoutRequest(data, buyer, xffIp);
  } else {
    runReadRequest(data, buyer, xffIp);
  }

  if (SLEEP_MS > 0) {
    sleep(SLEEP_MS / 1000);
  }
}

export function handleSummary(data) {
  const readP95 = metricValue(data, 'read_duration', 'p(95)');
  const readP50 = metricValue(data, 'read_duration', 'med');
  const coP95 = metricValue(data, 'checkout_duration', 'p(95)');
  const coP50 = metricValue(data, 'checkout_duration', 'med');
  const reads = metricValue(data, 'read_requests', 'count');
  const orders = metricValue(data, 'checkout_created', 'count');
  const rl429 = metricValue(data, 'rate_limited_429', 'count');
  const err5xx = metricValue(data, 'server_errors_5xx', 'count');
  const err4xx = metricValue(data, 'client_errors_4xx', 'count');

  return buildSummaryOutput(
    `NexusMart Checkout Load Test (${TARGET_VUS} VUs, ${DURATION})`,
    [
      `Read API     : count=${reads} | p50=${formatMs(readP50)} | p95=${formatMs(readP95)} (target < 300 ms)`,
      `Checkout API : orders=${orders} | p50=${formatMs(coP50)} | p95=${formatMs(coP95)} (target < 1000 ms)`,
      `Reliability  : 429_rate_limited=${rl429} | 5xx_server_errors=${err5xx} | 4xx_client_errors=${err4xx}`,
    ],
    data
  );
}
