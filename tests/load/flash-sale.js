import grpc from 'k6/net/grpc';
import { check } from 'k6';
import { Counter } from 'k6/metrics';
import {
  buildSummaryOutput,
  calculateShippingQuote,
  classifyOutcome,
  createApprovedProduct,
  createReliabilityMetrics,
  createTrend,
  ensureCategory,
  ensureSeller,
  envBool,
  envInt,
  envStr,
  fetchProvinceAndWard,
  formatMs,
  jsonRequest,
  loadConfig,
  login,
  metricValue,
  mintAccessToken,
  mustJson,
  parseJson,
  setupBuyerPool,
  shortRunId,
  uuidv4,
  vuSyntheticIp,
} from './lib/common.js';

const CFG = loadConfig();
const MODE = envStr('MODE', 'grpc').toLowerCase();
const TOTAL_REQUESTS = envInt('TOTAL_REQUESTS', 1000);
const TOTAL_SLOTS = envInt('TOTAL_SLOTS', 100);
const VUS = envInt('VUS', MODE === 'grpc' ? 200 : 100);
const PROTO_DIR = envStr('PROTO_DIR', '../../proto');
const BUYER_POOL_SIZE = envInt('BUYER_POOL_SIZE', 20);
const REQUIRE_FULL_SELLOUT = envBool('REQUIRE_FULL_SELLOUT', MODE === 'grpc');
const CLEANUP_FLASH_SALE = envBool('CLEANUP_FLASH_SALE', true);

if (MODE !== 'grpc' && MODE !== 'http') {
  throw new Error(`unsupported MODE=${MODE}; expected 'grpc' or 'http'`);
}

const grpcClient = MODE === 'grpc' ? new grpc.Client() : null;
if (grpcClient) {
  grpcClient.load([PROTO_DIR], 'nexusmart/promotion/v1/promotion.proto');
}
let grpcConnected = false;

const flashSaleSold = new Counter('flash_sale_sold');
const flashSaleRejected = new Counter('flash_sale_rejected');
const flashSaleUnexpectedErrors = new Counter('flash_sale_unexpected_errors');
const reserveDuration = createTrend('flash_sale_reserve_duration');
const reliability = createReliabilityMetrics();

const thresholds = {
  flash_sale_sold: REQUIRE_FULL_SELLOUT ? [`count==${TOTAL_SLOTS}`] : [`count<=${TOTAL_SLOTS}`],
  flash_sale_unexpected_errors: ['count==0'],
};
if (MODE === 'grpc') {
  thresholds.flash_sale_rejected = [`count==${Math.max(0, TOTAL_REQUESTS - TOTAL_SLOTS)}`];
  thresholds.checks = ['rate==1'];
} else {
  thresholds.server_errors_5xx = ['count==0'];
  if (REQUIRE_FULL_SELLOUT) {
    thresholds.checks = ['rate==1'];
  }
}

export const options = {
  scenarios: {
    flash_sale_race: {
      executor: 'shared-iterations',
      vus: VUS,
      iterations: TOTAL_REQUESTS,
      maxDuration: envStr('MAX_DURATION', '120s'),
    },
  },
  setupTimeout: '180s',
  teardownTimeout: '60s',
  thresholds: thresholds,
};

export function setup() {
  const runId = shortRunId();
  const admin = login(CFG, CFG.adminEmail, CFG.adminPassword);
  const { province, ward } = fetchProvinceAndWard(CFG);
  const seller = ensureSeller(CFG, admin.accessToken, province, ward, runId);
  const category = ensureCategory(CFG, admin.accessToken, runId);

  const normalPriceVnd = 500000;
  const flashPriceVnd = 199000;
  const productStock = Math.max(500, TOTAL_SLOTS * 5);

  const { product, sku } = createApprovedProduct(CFG, seller.accessToken, admin.accessToken, category.id, {
    runId: runId,
    name: `Load Flash Sale Product ${runId}`,
    priceVnd: normalPriceVnd,
    stock: productStock,
    weightGrams: 400,
  });

  // Create an active flash sale window (started 5 minutes ago, ends in 2 hours).
  const nowMs = Date.now();
  const fsCreated = mustJson(
    'POST',
    `${CFG.gatewayUrl}/api/v1/admin/flash-sales`,
    {
      name: `k6 Flash Sale ${runId}`,
      starts_at: new Date(nowMs - 5 * 60 * 1000).toISOString(),
      ends_at: new Date(nowMs + 2 * 60 * 60 * 1000).toISOString(),
    },
    { token: admin.accessToken, timeout: CFG.httpTimeout, tags: { phase: 'setup', endpoint: 'create_flash_sale' } },
    [200, 201]
  );
  const flashSaleId = fsCreated.data && fsCreated.data.flash_sale && fsCreated.data.flash_sale.id;
  if (!flashSaleId) {
    throw new Error('create flash sale returned empty flash_sale.id');
  }

  let buyers = [];
  let carrierCode = 'ghn';
  let serviceCode = 'standard';
  let perUserLimit = envInt('PER_USER_LIMIT', 1);

  if (MODE === 'http') {
    buyers = setupBuyerPool(CFG, BUYER_POOL_SIZE, province, ward, runId, 'k6-fs-buyer');
    if (buyers.length === 0) {
      throw new Error('flash-sale HTTP mode setup failed: buyer pool is empty');
    }
    const quote = calculateShippingQuote(CFG, buyers[0].accessToken, seller.shopId, province, ward, 400, flashPriceVnd);
    carrierCode = quote.carrier_code || 'ghn';
    serviceCode = quote.service_code || 'standard';

    // When HTTP buyer pool has fewer users than TOTAL_SLOTS and PER_USER_LIMIT was not overridden,
    // scale per_user_limit so the pool can contend for all slots without hitting per-user cap first.
    if (envStr('PER_USER_LIMIT', '') === '' && buyers.length < TOTAL_SLOTS) {
      perUserLimit = Math.ceil(TOTAL_SLOTS / buyers.length) + 1;
      console.log(
        `[flash-sale setup] HTTP buyer pool (${buyers.length}) < total_slots (${TOTAL_SLOTS}); ` +
          `auto-setting per_user_limit=${perUserLimit} (override via PER_USER_LIMIT or BUYER_POOL_SIZE)`
      );
    }
  }

  const regItem = mustJson(
    'POST',
    `${CFG.gatewayUrl}/api/v1/seller/flash-sales/${encodeURIComponent(flashSaleId)}/items`,
    {
      sku_id: sku.id,
      flash_price_vnd: flashPriceVnd,
      total_slots: TOTAL_SLOTS,
      per_user_limit: perUserLimit,
    },
    { token: seller.accessToken, timeout: CFG.httpTimeout, tags: { phase: 'setup', endpoint: 'register_fs_item' } },
    [200, 201]
  );
  const itemId = regItem.data && regItem.data.item && regItem.data.item.id;
  if (!itemId) {
    throw new Error('register flash sale item returned empty item.id');
  }

  mustJson(
    'POST',
    `${CFG.gatewayUrl}/api/v1/admin/flash-sale-items/${encodeURIComponent(itemId)}/approve`,
    { approve: true, reason: 'k6 flash sale concurrency load test' },
    { token: admin.accessToken, timeout: CFG.httpTimeout, tags: { phase: 'setup', endpoint: 'approve_fs_item' } },
    [200]
  );

  console.log(
    `[flash-sale setup] mode=${MODE} run=${runId} requests=${TOTAL_REQUESTS} slots=${TOTAL_SLOTS} ` +
      `vus=${VUS} per_user_limit=${perUserLimit} flash_sale=${flashSaleId} item=${itemId}`
  );

  return {
    runId: runId,
    mode: MODE,
    adminToken: admin.accessToken,
    shopId: seller.shopId,
    productId: product.id,
    skuId: sku.id,
    normalPriceVnd: normalPriceVnd,
    flashPriceVnd: flashPriceVnd,
    flashSaleId: flashSaleId,
    itemId: itemId,
    totalSlots: TOTAL_SLOTS,
    carrierCode: carrierCode,
    serviceCode: serviceCode,
    buyers: buyers,
  };
}

function runGrpcIteration(data) {
  if (!grpcConnected) {
    grpcClient.connect(CFG.promotionGrpcAddr, { plaintext: true, timeout: '10s' });
    grpcConnected = true;
  }

  const idx = `${__VU}-${__ITER}`;
  const userId = `k6-fs-user-${data.runId}-${idx}`;
  const orderId = `k6-fs-order-${data.runId}-${idx}`;
  const token = mintAccessToken(CFG.jwtSecret, userId, 'buyer');

  const req = {
    order_id: orderId,
    user_id: userId,
    flash_sale_items: [
      {
        flash_sale_item_id: data.itemId,
        sku_id: data.skuId,
        quantity: 1,
      },
    ],
    shop_items: [
      {
        shop_id: data.shopId,
        subtotal_vnd: data.normalPriceVnd,
        items: [
          {
            sku_id: data.skuId,
            quantity: 1,
            unit_price_vnd: data.normalPriceVnd,
            flash_sale_item_id: data.itemId,
          },
        ],
      },
    ],
  };

  const t0 = Date.now();
  const res = grpcClient.invoke('nexusmart.promotion.v1.PromotionService/ReservePromotion', req, {
    metadata: { authorization: `Bearer ${token}` },
    timeout: '20s',
  });
  reserveDuration.add(Date.now() - t0);

  if (res && res.status === grpc.StatusOK) {
    const reserved = Boolean(res.message && res.message.reserved === true);
    if (reserved) {
      flashSaleSold.add(1);
    } else {
      flashSaleRejected.add(1);
    }
    return;
  }

  // Expected atomic rejection codes when slots are exhausted or user limit reached.
  const status = res ? res.status : -1;
  const expectedRejectCodes = [
    grpc.StatusFailedPrecondition,
    grpc.StatusResourceExhausted,
    grpc.StatusAborted,
    grpc.StatusInvalidArgument,
  ];
  if (expectedRejectCodes.indexOf(status) !== -1) {
    flashSaleRejected.add(1);
  } else {
    flashSaleUnexpectedErrors.add(1);
  }
}

function runHttpIteration(data) {
  const buyer = data.buyers[(__VU + __ITER) % data.buyers.length];
  const xffIp = CFG.spoofXff ? vuSyntheticIp(__VU) : '';
  const idemKey = `k6-fs-${data.runId}-${__VU}-${__ITER}-${uuidv4()}`;

  const payload = {
    address_id: buyer.addressId,
    from_cart: false,
    items: [
      {
        sku_id: data.skuId,
        quantity: 1,
        flash_sale_item_id: data.itemId,
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
    tags: { group: 'flash_sale', endpoint: 'flash_sale_checkout' },
  });

  const outcome = classifyOutcome(res, reliability, (r) => r.status === 400 || r.status === 409 || r.status === 422);
  if (outcome === 'ok') {
    reserveDuration.add(res.timings.duration);
    const body = parseJson(res);
    if (body && Array.isArray(body.orders) && body.orders.length > 0) {
      flashSaleSold.add(1);
    } else {
      flashSaleUnexpectedErrors.add(1);
    }
  } else if (outcome === 'rejected') {
    reserveDuration.add(res.timings.duration);
    flashSaleRejected.add(1);
  } else if (outcome === 'server_error') {
    flashSaleUnexpectedErrors.add(1);
  }
}

export default function (data) {
  if (data.mode === 'grpc') {
    runGrpcIteration(data);
  } else {
    runHttpIteration(data);
  }
}

export function teardown(data) {
  if (grpcClient && grpcConnected) {
    grpcClient.close();
    grpcConnected = false;
  }

  let remainingSlots = -1;
  let totalSlots = data.totalSlots;
  let found = false;

  let listRes = null;
  for (let attempt = 0; attempt < 3; attempt++) {
    listRes = jsonRequest('GET', `${CFG.gatewayUrl}/api/v1/flash-sales`, null, {
      token: data.adminToken,
      timeout: CFG.httpTimeout,
      tags: { phase: 'teardown', endpoint: 'verify_flash_sales' },
    });
    if (listRes.status === 200 || listRes.status !== 429) {
      break;
    }
  }
  if (listRes && listRes.status === 200) {
    const body = parseJson(listRes);
    const sales = (body && body.flash_sales) || [];
    for (let i = 0; i < sales.length; i++) {
      const items = (sales[i] && sales[i].items) || [];
      for (let j = 0; j < items.length; j++) {
        if (items[j] && items[j].id === data.itemId) {
          found = true;
          remainingSlots = items[j].remaining_slots;
          totalSlots = items[j].total_slots;
          break;
        }
      }
      if (found) {
        break;
      }
    }
  } else {
    console.warn(`[flash-sale teardown] GET /api/v1/flash-sales returned HTTP ${listRes.status}`);
  }

  const serverSold = found ? totalSlots - remainingSlots : -1;
  const serverOversell = found ? Math.max(0, -remainingSlots) : -1;

  console.log(
    `[flash-sale verify] item=${data.itemId} found=${found} total_slots=${totalSlots} ` +
      `remaining_slots=${remainingSlots} server_sold=${serverSold} oversell=${serverOversell}`
  );

  check(
    { found: found, remainingSlots: remainingSlots },
    {
      'teardown: flash sale item found': (v) => v.found === true,
      'teardown: zero oversell (remaining_slots >= 0)': (v) => v.found && v.remainingSlots >= 0,
      'teardown: remaining_slots == 0 when full sellout required': (v) =>
        !REQUIRE_FULL_SELLOUT || (v.found && v.remainingSlots === 0),
    }
  );

  if (CLEANUP_FLASH_SALE && data.adminToken && data.flashSaleId) {
    const endRes = jsonRequest(
      'POST',
      `${CFG.gatewayUrl}/api/v1/admin/flash-sales/${encodeURIComponent(data.flashSaleId)}/end`,
      {},
      { token: data.adminToken, timeout: CFG.httpTimeout, tags: { phase: 'teardown', endpoint: 'end_flash_sale' } }
    );
    if (endRes.status !== 200) {
      console.warn(`[flash-sale teardown] end window ${data.flashSaleId} returned HTTP ${endRes.status}`);
    }
  }
}

export function handleSummary(data) {
  const sold = metricValue(data, 'flash_sale_sold', 'count');
  const rejected = metricValue(data, 'flash_sale_rejected', 'count');
  const rl429 = metricValue(data, 'rate_limited_429', 'count');
  const unexpected = metricValue(data, 'flash_sale_unexpected_errors', 'count');
  const oversell = Math.max(0, sold - TOTAL_SLOTS);
  const p50 = metricValue(data, 'flash_sale_reserve_duration', 'med');
  const p95 = metricValue(data, 'flash_sale_reserve_duration', 'p(95)');

  return buildSummaryOutput(
    `NexusMart Flash Sale Race Test (mode=${MODE}, ${TOTAL_REQUESTS} reqs / ${TOTAL_SLOTS} slots)`,
    [
      `Results : sold=${sold}/${TOTAL_SLOTS} | rejected=${rejected} | oversell=${oversell}`,
      `Errors  : 429_rate_limited=${rl429} | unexpected_errors=${unexpected}`,
      `Latency : p50=${formatMs(p50)} | p95=${formatMs(p95)}`,
    ],
    data
  );
}
