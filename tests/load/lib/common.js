import http from 'k6/http';
import crypto from 'k6/crypto';
import encoding from 'k6/encoding';
import { sleep } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';

const OTP_CONTEXTUAL_RE = /(?:otp|code|verification|mã|xác\s*thực)[^0-9]{0,24}(\d{6})\b/i;
const OTP_GENERIC_RE = /\b(\d{6})\b/;

export function envStr(name, fallback = '') {
  const val = __ENV[name];
  if (val === undefined || val === null) {
    return fallback;
  }
  const trimmed = String(val).trim();
  return trimmed === '' ? fallback : trimmed;
}

export function envInt(name, fallback) {
  const raw = envStr(name, '');
  if (raw === '') {
    return fallback;
  }
  const parsed = parseInt(raw, 10);
  return Number.isFinite(parsed) ? parsed : fallback;
}

export function envFloat(name, fallback) {
  const raw = envStr(name, '');
  if (raw === '') {
    return fallback;
  }
  const parsed = parseFloat(raw);
  return Number.isFinite(parsed) ? parsed : fallback;
}

export function envBool(name, fallback = false) {
  const raw = envStr(name, '').toLowerCase();
  if (raw === '') {
    return fallback;
  }
  return raw === '1' || raw === 'true' || raw === 'yes' || raw === 'on';
}

export function loadConfig() {
  return {
    gatewayUrl: envStr('GATEWAY_URL', 'http://127.0.0.1:8080').replace(/\/+$/, ''),
    mailpitUrl: envStr('MAILPIT_URL', 'http://127.0.0.1:8025').replace(/\/+$/, ''),
    promotionGrpcAddr: envStr('PROMOTION_GRPC_ADDR', 'promotion:50054'),
    jwtSecret: envStr('JWT_SECRET', 'dev-only-nexusmart-hs256-secret-change-me'),
    serviceJwtSecret: envStr('SERVICE_JWT_SECRET', 'dev-only-nexusmart-service-hs256-secret-change-me'),
    siteRootDomain: envStr('SITE_ROOT_DOMAIN', 'nexusmart.localhost'),
    adminEmail: envStr('ADMIN_EMAIL', 'admin@nexusmart.local'),
    adminPassword: envStr('ADMIN_PASSWORD', 'AdminDevPassword123!'),
    sellerEmail: envStr('SELLER_EMAIL', 'seller@nexusmart.local'),
    sellerPassword: envStr('SELLER_PASSWORD', 'SellerDemo123!'),
    buyerEmail: envStr('BUYER_EMAIL', 'buyer@nexusmart.local'),
    buyerPassword: envStr('BUYER_PASSWORD', 'BuyerDemo123!'),
    httpTimeout: envStr('HTTP_TIMEOUT', '15s'),
    otpTimeoutSec: envInt('OTP_TIMEOUT_SEC', 20),
    spoofXff: envBool('SPOOF_XFF', false),
  };
}

export function createReliabilityMetrics() {
  return {
    rateLimited429: new Counter('rate_limited_429'),
    rateLimitedRate: new Rate('rate_limited_rate'),
    serverErrors5xx: new Counter('server_errors_5xx'),
    serverErrorRate: new Rate('server_error_rate'),
    clientErrors4xx: new Counter('client_errors_4xx'),
  };
}

export function createTrend(name) {
  return new Trend(name, true);
}

export function uuidv4() {
  const bytes = new Uint8Array(crypto.randomBytes(16));
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  let hex = '';
  for (let i = 0; i < bytes.length; i++) {
    hex += bytes[i].toString(16).padStart(2, '0');
  }
  return (
    hex.slice(0, 8) +
    '-' +
    hex.slice(8, 12) +
    '-' +
    hex.slice(12, 16) +
    '-' +
    hex.slice(16, 20) +
    '-' +
    hex.slice(20)
  );
}

export function shortRunId() {
  return uuidv4().replace(/-/g, '').slice(0, 8);
}

export function uniquePhone(seed) {
  const base = (Date.now() + seed * 7919) % 80000000;
  return '09' + String(base + 10000000).padStart(8, '0');
}

// Mints an HS256 JWT with the exact claims shape verified by pkg/common/auth & tests/e2e/client.go.
export function mintAccessToken(secret, userId, role = 'buyer', shopId = '', ttlSec = 300) {
  const now = Math.floor(Date.now() / 1000);
  const header = encoding.b64encode(JSON.stringify({ alg: 'HS256', typ: 'JWT' }), 'rawurl');
  const claims = {
    sub: userId,
    role: role,
    iss: 'nexusmart',
    jti: uuidv4(),
    iat: now,
    exp: now + ttlSec,
  };
  if (shopId) {
    claims.shop_id = shopId;
  }
  const payload = encoding.b64encode(JSON.stringify(claims), 'rawurl');
  const unsigned = `${header}.${payload}`;
  const sig = crypto.hmac('sha256', secret, unsigned, 'base64rawurl');
  return `${unsigned}.${sig}`;
}

// Mints a short-lived internal service HS256 JWT signed with SERVICE_JWT_SECRET (D3 / internalRPCs),
// matching tests/e2e/06_flash_sale_test.go promoMintServiceToken.
export function mintServiceToken(secret, serviceName = 'order-service', ttlSec = 300) {
  const now = Math.floor(Date.now() / 1000);
  const header = encoding.b64encode(JSON.stringify({ alg: 'HS256', typ: 'JWT' }), 'rawurl');
  const claims = {
    sub: serviceName,
    role: 'admin',
    service: serviceName,
    iss: 'nexusmart',
    jti: uuidv4(),
    iat: now,
    exp: now + ttlSec,
  };
  const payload = encoding.b64encode(JSON.stringify(claims), 'rawurl');
  const unsigned = `${header}.${payload}`;
  const sig = crypto.hmac('sha256', secret, unsigned, 'base64rawurl');
  return `${unsigned}.${sig}`;
}

export function vuSyntheticIp(vuId) {
  const n = Math.max(1, vuId);
  const octet3 = Math.floor(n / 250) % 250;
  const octet4 = (n % 250) + 1;
  return `10.200.${octet3}.${octet4}`;
}

export function parseJson(res) {
  if (!res || !res.body) {
    return null;
  }
  try {
    return res.json();
  } catch (_) {
    return null;
  }
}

export function jsonRequest(method, url, body = null, opts = {}) {
  const headers = Object.assign({ Accept: 'application/json' }, opts.headers || {});
  if (opts.token) {
    headers.Authorization = `Bearer ${opts.token}`;
  }
  if (opts.xffIp) {
    headers['X-Forwarded-For'] = opts.xffIp;
    headers['X-Real-IP'] = opts.xffIp;
  }
  let payload = null;
  if (body !== null && body !== undefined) {
    headers['Content-Type'] = 'application/json';
    payload = typeof body === 'string' ? body : JSON.stringify(body);
  }
  const params = {
    headers: headers,
    timeout: opts.timeout || '15s',
  };
  if (opts.tags) {
    params.tags = opts.tags;
  }
  return http.request(method, url, payload, params);
}

export function mustJson(method, url, body = null, opts = {}, allowedStatuses = [200]) {
  const maxRetries = opts.maxRetries !== undefined ? opts.maxRetries : 2;
  let lastRes = null;
  for (let attempt = 0; attempt <= maxRetries; attempt++) {
    const res = jsonRequest(method, url, body, opts);
    lastRes = res;
    if (allowedStatuses.indexOf(res.status) !== -1) {
      return { res: res, data: parseJson(res) };
    }
    if (res.status === 429 && attempt < maxRetries) {
      const retryAfter = parseInt((res.headers && (res.headers['Retry-After'] || res.headers['retry-after'])) || '1', 10);
      sleep(Math.min(Math.max(retryAfter || 1, 1), 3));
      continue;
    }
    break;
  }
  const traceId = (lastRes && lastRes.headers && (lastRes.headers['X-Trace-Id'] || lastRes.headers['x-trace-id'])) || '';
  const bodySnippet = lastRes && lastRes.body ? String(lastRes.body).slice(0, 400) : '';
  throw new Error(
    `${method} ${url} returned HTTP ${lastRes ? lastRes.status : 0} (want ${allowedStatuses.join('/')}) trace_id=${traceId} body=${bodySnippet}`
  );
}

export function classifyOutcome(res, metrics, isBusinessRejection = null) {
  const status = res ? res.status : 0;
  if (status === 429) {
    metrics.rateLimited429.add(1);
    metrics.rateLimitedRate.add(1);
    metrics.serverErrorRate.add(0);
    return 'rate_limited';
  }
  metrics.rateLimitedRate.add(0);
  if (status === 0 || status >= 500) {
    metrics.serverErrors5xx.add(1);
    metrics.serverErrorRate.add(1);
    return 'server_error';
  }
  metrics.serverErrorRate.add(0);
  if (status >= 200 && status < 300) {
    return 'ok';
  }
  if (isBusinessRejection && isBusinessRejection(res)) {
    return 'rejected';
  }
  metrics.clientErrors4xx.add(1);
  return 'client_error';
}

export function login(cfg, email, password, xffIp = '') {
  const { data } = mustJson(
    'POST',
    `${cfg.gatewayUrl}/api/v1/auth/login`,
    { email: email, password: password },
    { xffIp: xffIp, timeout: cfg.httpTimeout, tags: { phase: 'setup', endpoint: 'auth_login' } },
    [200]
  );
  if (!data || !data.access_token) {
    throw new Error(`login for ${email} returned empty access_token`);
  }
  return {
    accessToken: data.access_token,
    refreshToken: data.refresh_token || '',
    user: data.user || {},
  };
}

export function extractOtp(parts) {
  const combined = parts.filter(Boolean).join('\n');
  let m = OTP_CONTEXTUAL_RE.exec(combined);
  if (m && m[1]) {
    return m[1];
  }
  m = OTP_GENERIC_RE.exec(combined);
  if (m && m[1]) {
    return m[1];
  }
  return '';
}

export function snapshotRecipientMessageIds(cfg, email) {
  const seen = {};
  const searchQuery = encodeURIComponent(`to:"${email}"`);
  const searchUrl = `${cfg.mailpitUrl}/api/v1/search?query=${searchQuery}&limit=20`;
  const mailpitTimeout = envStr('MAILPIT_TIMEOUT', '10s');
  const listRes = jsonRequest('GET', searchUrl, null, {
    timeout: mailpitTimeout,
    tags: { phase: 'setup', endpoint: 'mailpit_snapshot' },
  });
  if (listRes && listRes.status === 200) {
    const list = parseJson(listRes);
    const messages = (list && list.messages) || [];
    for (let i = 0; i < messages.length; i++) {
      if (messages[i] && messages[i].ID) {
        seen[messages[i].ID] = true;
      }
    }
  }
  return seen;
}

export function waitForOtp(cfg, email, timeoutSec = 20, seenIds = null) {
  const deadline = Date.now() + timeoutSec * 1000;
  const searchQuery = encodeURIComponent(`to:"${email}"`);
  const searchUrl = `${cfg.mailpitUrl}/api/v1/search?query=${searchQuery}&limit=10`;
  const mailpitTimeout = envStr('MAILPIT_TIMEOUT', '10s');
  let lastStatus = 0;

  while (Date.now() < deadline) {
    const listRes = jsonRequest('GET', searchUrl, null, {
      timeout: mailpitTimeout,
      tags: { phase: 'setup', endpoint: 'mailpit_search' },
    });
    lastStatus = listRes ? listRes.status : 0;
    if (lastStatus === 200) {
      const list = parseJson(listRes);
      const messages = (list && list.messages) || [];
      for (let i = 0; i < messages.length; i++) {
        const msg = messages[i];
        if (!msg || !msg.ID || (seenIds && seenIds[msg.ID])) {
          continue;
        }
        const fullRes = jsonRequest('GET', `${cfg.mailpitUrl}/api/v1/message/${msg.ID}`, null, {
          timeout: mailpitTimeout,
          tags: { phase: 'setup', endpoint: 'mailpit_get' },
        });
        if (fullRes && fullRes.status === 200) {
          const full = parseJson(fullRes) || {};
          const otp = extractOtp([full.Subject, full.Text, full.HTML, msg.Snippet]);
          if (otp) {
            return otp;
          }
        }
      }
    }
    sleep(0.3);
  }
  throw new Error(
    `timed out after ${timeoutSec}s waiting for OTP email to ${email} via ${searchUrl} (last_status=${lastStatus})`
  );
}

export function verifyBuyerPhone(cfg, buyer, xffIp = '') {
  if (!buyer || !buyer.accessToken) {
    return;
  }
  if (buyer.user && buyer.user.phone_verified === true) {
    return;
  }
  const meRes = jsonRequest('GET', `${cfg.gatewayUrl}/api/v1/users/me`, null, {
    token: buyer.accessToken,
    xffIp: xffIp,
    timeout: cfg.httpTimeout,
    tags: { phase: 'setup', endpoint: 'users_me' },
  });
  if (meRes && meRes.status === 200) {
    const me = parseJson(meRes);
    if (me && me.user && me.user.phone_verified === true) {
      return;
    }
  }

  const seenIds = snapshotRecipientMessageIds(cfg, buyer.email);
  mustJson(
    'POST',
    `${cfg.gatewayUrl}/api/v1/users/me/phone/otp`,
    {},
    { token: buyer.accessToken, xffIp: xffIp, timeout: cfg.httpTimeout, tags: { phase: 'setup', endpoint: 'phone_otp_request' } },
    [200]
  );
  const otp = waitForOtp(cfg, buyer.email, cfg.otpTimeoutSec, seenIds);
  mustJson(
    'POST',
    `${cfg.gatewayUrl}/api/v1/users/me/phone/verify`,
    { otp: otp },
    { token: buyer.accessToken, xffIp: xffIp, timeout: cfg.httpTimeout, tags: { phase: 'setup', endpoint: 'phone_otp_verify' } },
    [200]
  );
}

export function registerVerifyAndLogin(cfg, label, seq = 1, runId = '', xffIp = '') {
  const tag = runId || shortRunId();
  const email = `${label}-${tag}-${seq}@nexusmart.test`;
  const phone = uniquePhone(seq);
  const password = 'NexusLoadPass123!';

  const reg = mustJson(
    'POST',
    `${cfg.gatewayUrl}/api/v1/auth/register`,
    {
      full_name: `Load ${label} ${seq}`,
      email: email,
      phone: phone,
      password: password,
    },
    { xffIp: xffIp, timeout: cfg.httpTimeout, tags: { phase: 'setup', endpoint: 'auth_register' } },
    [200, 201]
  );

  const otp = waitForOtp(cfg, email, cfg.otpTimeoutSec);

  const verify = mustJson(
    'POST',
    `${cfg.gatewayUrl}/api/v1/auth/verify-otp`,
    { email: email, otp: otp },
    { xffIp: xffIp, timeout: cfg.httpTimeout, tags: { phase: 'setup', endpoint: 'auth_verify_otp' } },
    [200]
  );
  if (!verify.data || !verify.data.verified) {
    throw new Error(`verify-otp failed for ${email}`);
  }

  const auth = login(cfg, email, password, xffIp);
  return {
    userId: (auth.user && auth.user.id) || (reg.data && reg.data.user_id) || '',
    email: email,
    phone: phone,
    password: password,
    accessToken: auth.accessToken,
    refreshToken: auth.refreshToken,
    user: auth.user,
  };
}

export function fetchProvinceAndWard(cfg) {
  const prov = mustJson(
    'GET',
    `${cfg.gatewayUrl}/api/v1/shipping/provinces`,
    null,
    { timeout: cfg.httpTimeout, tags: { phase: 'setup', endpoint: 'shipping_provinces' } },
    [200]
  );
  const provinces = (prov.data && prov.data.provinces) || [];
  if (provinces.length === 0) {
    throw new Error('GET /api/v1/shipping/provinces returned 0 provinces');
  }
  const chosenProv = provinces[0];

  const ward = mustJson(
    'GET',
    `${cfg.gatewayUrl}/api/v1/shipping/provinces/${encodeURIComponent(chosenProv.code)}/wards`,
    null,
    { timeout: cfg.httpTimeout, tags: { phase: 'setup', endpoint: 'shipping_wards' } },
    [200]
  );
  const wards = (ward.data && ward.data.wards) || [];
  if (wards.length === 0) {
    throw new Error(`GET /api/v1/shipping/provinces/${chosenProv.code}/wards returned 0 wards`);
  }
  return { province: chosenProv, ward: wards[0] };
}

export function createAddress(cfg, token, phone, province, ward, streetLabel = '123 Le Loi Load Test', xffIp = '') {
  const { data } = mustJson(
    'POST',
    `${cfg.gatewayUrl}/api/v1/users/me/addresses`,
    {
      recipient_name: 'Nguyen Van Load',
      recipient_phone: phone || '0901234567',
      province_code: province.code,
      province_name: province.name,
      ward_code: ward.code,
      ward_name: ward.name,
      street_address: streetLabel,
      is_default: true,
    },
    { token: token, xffIp: xffIp, timeout: cfg.httpTimeout, tags: { phase: 'setup', endpoint: 'create_address' } },
    [200, 201]
  );
  if (!data || !data.address || !data.address.id) {
    throw new Error('createAddress returned empty address.id');
  }
  return data.address;
}

export function ensureBuyerAddress(cfg, buyer, province, ward) {
  const listRes = jsonRequest('GET', `${cfg.gatewayUrl}/api/v1/users/me/addresses`, null, {
    token: buyer.accessToken,
    timeout: cfg.httpTimeout,
    tags: { phase: 'setup', endpoint: 'list_addresses' },
  });
  if (listRes.status === 200) {
    const body = parseJson(listRes);
    const addrs = (body && body.addresses) || [];
    if (addrs.length > 0 && addrs[0].id) {
      return addrs[0];
    }
  }
  return createAddress(cfg, buyer.accessToken, buyer.phone || (buyer.user && buyer.user.phone), province, ward);
}

export function ensureSeller(cfg, adminToken, province, ward, runId = '') {
  const tryRes = jsonRequest(
    'POST',
    `${cfg.gatewayUrl}/api/v1/auth/login`,
    { email: cfg.sellerEmail, password: cfg.sellerPassword },
    { timeout: cfg.httpTimeout, tags: { phase: 'setup', endpoint: 'seller_login' } }
  );
  if (tryRes.status === 200) {
    const body = parseJson(tryRes);
    if (body && body.access_token && body.user && body.user.shop_id) {
      return {
        accessToken: body.access_token,
        userId: body.user.id,
        shopId: body.user.shop_id,
        email: cfg.sellerEmail,
      };
    }
  }

  // Fallback for unseeded stacks: register & approve an ephemeral seller shop.
  const tag = runId || shortRunId();
  const user = registerVerifyAndLogin(cfg, 'k6-seller', 1, tag);
  const addr = createAddress(cfg, user.accessToken, user.phone, province, ward, `Shop Pickup ${tag}`);

  const regShop = mustJson(
    'POST',
    `${cfg.gatewayUrl}/api/v1/shops/register`,
    {
      name: `LoadShop-${tag}`,
      description: 'Ephemeral k6 load-test merchant shop',
      pickup_address_id: addr.id,
      contact_phone: user.phone,
      business_license_doc: 'https://sandbox.nexusmart.local/docs/load-license.pdf',
    },
    { token: user.accessToken, timeout: cfg.httpTimeout, tags: { phase: 'setup', endpoint: 'register_shop' } },
    [200, 201]
  );
  const shopId = regShop.data && regShop.data.shop && regShop.data.shop.id;
  if (!shopId) {
    throw new Error('register shop returned empty shop.id');
  }

  mustJson(
    'POST',
    `${cfg.gatewayUrl}/api/v1/admin/shops/${encodeURIComponent(shopId)}/review`,
    { decision: 'APPROVE', reason: 'k6 load-test auto approval' },
    { token: adminToken, timeout: cfg.httpTimeout, tags: { phase: 'setup', endpoint: 'approve_shop' } },
    [200]
  );

  const relogin = login(cfg, user.email, user.password);
  return {
    accessToken: relogin.accessToken,
    userId: (relogin.user && relogin.user.id) || user.userId,
    shopId: (relogin.user && relogin.user.shop_id) || shopId,
    email: user.email,
  };
}

export function ensureCategory(cfg, adminToken, runId = '') {
  const listRes = jsonRequest('GET', `${cfg.gatewayUrl}/api/v1/categories`, null, {
    timeout: cfg.httpTimeout,
    tags: { phase: 'setup', endpoint: 'list_categories' },
  });
  if (listRes.status === 200) {
    const body = parseJson(listRes);
    const cats = (body && body.categories) || [];
    if (cats.length > 0 && cats[0].id) {
      return cats[0];
    }
  }
  const tag = runId || shortRunId();
  const created = mustJson(
    'POST',
    `${cfg.gatewayUrl}/api/v1/admin/categories`,
    {
      name: `Load Category ${tag}`,
      slug: `load-cat-${tag}`,
      commission_rate_bps: 500,
      sort_order: 99,
    },
    { token: adminToken, timeout: cfg.httpTimeout, tags: { phase: 'setup', endpoint: 'create_category' } },
    [200, 201]
  );
  return created.data.category;
}

export function createApprovedProduct(cfg, sellerToken, adminToken, categoryId, opts = {}) {
  const tag = opts.runId || shortRunId();
  const name = opts.name || `Load Test Product ${tag}`;
  const priceVnd = opts.priceVnd || 199000;
  const totalStock = opts.stock || 50000;
  const weightGrams = opts.weightGrams || 300;
  const skuCount = Math.max(1, opts.skuCount || 1);
  const perSkuStock = Math.max(1, Math.ceil(totalStock / skuCount));
  const randSuffix = Math.floor(Math.random() * 10000);

  const skuInputs = [];
  for (let i = 1; i <= skuCount; i++) {
    skuInputs.push({
      sku_code: `K6-${tag.toUpperCase()}-${randSuffix}-${String(i).padStart(2, '0')}`,
      name: skuCount === 1 ? 'Load Variant Standard' : `Load Variant #${i}`,
      attributes: { edition: 'load-test', slot: String(i) },
      price_vnd: priceVnd,
      stock: perSkuStock,
      weight_grams: weightGrams,
      image_url: 'https://sandbox.nexusmart.local/img/k6-sku.png',
    });
  }

  const created = mustJson(
    'POST',
    `${cfg.gatewayUrl}/api/v1/seller/products`,
    {
      category_id: categoryId,
      name: name,
      description: `Ephemeral k6 load-test product (${tag})`,
      images: ['https://sandbox.nexusmart.local/img/k6-load.png'],
      skus: skuInputs,
    },
    { token: sellerToken, timeout: cfg.httpTimeout, tags: { phase: 'setup', endpoint: 'create_product' } },
    [200, 201]
  );

  const product = created.data && created.data.product;
  if (!product || !product.id || !product.skus || product.skus.length === 0) {
    throw new Error('createProduct returned missing product.id or skus');
  }

  const reviewed = mustJson(
    'POST',
    `${cfg.gatewayUrl}/api/v1/admin/products/${encodeURIComponent(product.id)}/review`,
    { decision: 'APPROVE', reason: 'k6 load-test product approval' },
    { token: adminToken, timeout: cfg.httpTimeout, tags: { phase: 'setup', endpoint: 'approve_product' } },
    [200]
  );

  const approvedProduct = (reviewed.data && reviewed.data.product) || product;
  const skus =
    approvedProduct.skus && approvedProduct.skus.length > 0 ? approvedProduct.skus : product.skus;
  return { product: approvedProduct, sku: skus[0], skus: skus };
}

export function discoverProducts(cfg, pageSize = 20) {
  const { data } = mustJson(
    'GET',
    `${cfg.gatewayUrl}/api/v1/products?page=1&page_size=${pageSize}`,
    null,
    { timeout: cfg.httpTimeout, tags: { phase: 'setup', endpoint: 'discover_products' } },
    [200]
  );
  return {
    items: (data && data.items) || [],
    total: (data && data.total) || 0,
  };
}

export function discoverSites(cfg, pageSize = 24) {
  const res = jsonRequest('GET', `${cfg.gatewayUrl}/api/v1/sites?page=1&page_size=${pageSize}`, null, {
    timeout: cfg.httpTimeout,
    tags: { phase: 'setup', endpoint: 'discover_sites' },
  });
  if (res.status !== 200) {
    return { sites: [], total: 0 };
  }
  const data = parseJson(res);
  return {
    sites: (data && data.sites) || [],
    total: (data && data.total) || 0,
  };
}

export function calculateShippingQuote(cfg, token, shopId, province, ward, weightGrams = 300, codAmountVnd = 199000) {
  const { data } = mustJson(
    'POST',
    `${cfg.gatewayUrl}/api/v1/shipping/calculate-fee`,
    {
      shop_id: shopId,
      from_province_code: province.code,
      from_ward_code: ward.code,
      to_province_code: province.code,
      to_ward_code: ward.code,
      weight_grams: weightGrams,
      cod_amount_vnd: codAmountVnd,
    },
    { token: token, timeout: cfg.httpTimeout, tags: { phase: 'setup', endpoint: 'shipping_quote' } },
    [200]
  );
  const quotes = (data && data.quotes) || [];
  if (quotes.length === 0) {
    return { carrier_code: 'ghn', service_code: 'standard', fee_vnd: 30000 };
  }
  const ghn = quotes.find((q) => q && q.carrier_code && q.carrier_code.toLowerCase() === 'ghn');
  return ghn || quotes[0];
}

export function setupBuyerPool(cfg, desiredCount, province, ward, runId, label = 'k6-buyer') {
  const buyers = [];
  const reuseDemo = envBool('REUSE_DEMO_BUYER', false);

  if (reuseDemo || desiredCount <= 0) {
    const demoAuth = login(cfg, cfg.buyerEmail, cfg.buyerPassword);
    verifyBuyerPhone(cfg, { email: cfg.buyerEmail, accessToken: demoAuth.accessToken, user: demoAuth.user });
    const addr = ensureBuyerAddress(cfg, { accessToken: demoAuth.accessToken, user: demoAuth.user }, province, ward);
    buyers.push({
      userId: demoAuth.user.id || '',
      email: cfg.buyerEmail,
      accessToken: demoAuth.accessToken,
      addressId: addr.id,
    });
    return buyers;
  }

  for (let i = 1; i <= desiredCount; i++) {
    const setupXffIp = cfg.spoofXff ? vuSyntheticIp(1000 + i) : '';
    try {
      const b = registerVerifyAndLogin(cfg, label, i, runId, setupXffIp);
      verifyBuyerPhone(cfg, b, setupXffIp);
      const addr = createAddress(
        cfg,
        b.accessToken,
        b.phone,
        province,
        ward,
        `Load Addr ${runId} #${i}`,
        setupXffIp
      );
      buyers.push({
        userId: b.userId,
        email: b.email,
        accessToken: b.accessToken,
        addressId: addr.id,
      });
    } catch (err) {
      if (buyers.length > 0 && String(err).indexOf('HTTP 429') !== -1) {
        console.warn(`[setup] stopped growing buyer pool at ${buyers.length}/${desiredCount} due to gateway 429 rate limit`);
        break;
      }
      if (buyers.length === 0) {
        // Last-resort fallback to seeded demo buyer if Mailpit or registration is unavailable.
        console.warn(`[setup] buyer registration failed (${err}); falling back to demo buyer ${cfg.buyerEmail}`);
        const demoAuth = login(cfg, cfg.buyerEmail, cfg.buyerPassword);
        verifyBuyerPhone(cfg, { email: cfg.buyerEmail, accessToken: demoAuth.accessToken, user: demoAuth.user });
        const addr = ensureBuyerAddress(cfg, { accessToken: demoAuth.accessToken, user: demoAuth.user }, province, ward);
        buyers.push({
          userId: demoAuth.user.id || '',
          email: cfg.buyerEmail,
          accessToken: demoAuth.accessToken,
          addressId: addr.id,
        });
        break;
      }
      throw err;
    }
  }
  return buyers;
}

export function metricValue(data, metricName, stat = 'count') {
  const m = data && data.metrics && data.metrics[metricName];
  if (!m || !m.values) {
    return 0;
  }
  const v = m.values[stat];
  return v === undefined || v === null ? 0 : v;
}

export function formatMs(val) {
  return `${Number(val || 0).toFixed(2)} ms`;
}

export function buildSummaryOutput(title, lines, data) {
  const banner = [
    '',
    `=== ${title} ===`,
    ...lines,
    '========================================',
    '',
  ].join('\n');

  const out = { stdout: banner };
  const summaryPath = envStr('SUMMARY_JSON', '');
  if (summaryPath) {
    out[summaryPath] = JSON.stringify(data, null, 2);
  }
  return out;
}
