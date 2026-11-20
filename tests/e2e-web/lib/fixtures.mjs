import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { config } from "./config.mjs";
import { WebSession, expectStatus, uid } from "./http.mjs";
import { waitForOTP } from "./mailpit.mjs";

const SEEDED_SHOP_NAMES = new Set([
  "Xưởng Dệt Bạch Đằng",
  "Dệt Kim Hà Nội",
  "Kho Basic Sài Gòn",
  "Sợi Tự Nhiên Đà Nẵng",
]);

const SEEDED_SHOPS = [
  { shopKey: "bach-dang", skuPrefix: "BD-", shopName: "Xưởng Dệt Bạch Đằng", slug: "ao-thun-cotton-compact-trang" },
  { shopKey: "ha-noi", skuPrefix: "HN-", shopName: "Dệt Kim Hà Nội", slug: "quan-jogger-ni-chan-cua-xam-muoi-tieu" },
  { shopKey: "sai-gon", skuPrefix: "SG-", shopName: "Kho Basic Sài Gòn", slug: "quan-short-daily-cotton-7-inch-be" },
  { shopKey: "da-nang", skuPrefix: "DN-", shopName: "Sợi Tự Nhiên Đà Nẵng", slug: "combo-3-quan-lot-brief-soi-tre" },
];

const DEMO_PASSWORD_SELLER = process.env.SEED_SELLER_PASSWORD || "SellerDemo123!";
const DEMO_ADMIN_EMAIL = process.env.ADMIN_BOOTSTRAP_EMAIL || "admin@nexusmart.local";
const DEMO_ADMIN_PASSWORD = process.env.ADMIN_BOOTSTRAP_PASSWORD || "AdminDevPassword123!";
const DEMO_SELLER_EMAIL = process.env.SEED_SELLER_EMAIL || "seller@nexusmart.local";

export const demoCredentials = {
  seller: { email: DEMO_SELLER_EMAIL, password: DEMO_PASSWORD_SELLER },
  admin: { email: DEMO_ADMIN_EMAIL, password: DEMO_ADMIN_PASSWORD },
};

let seq = 0;

export function uniquePhone() {
  seq += 1;
  const raw = (Date.now() * 17 + Math.floor(Math.random() * 1_000_000) + seq * 9973) % 80_000_000;
  return `09${String(raw + 10_000_000).padStart(8, "0")}`;
}

export function uniqueBuyerInput(prefix = "buyer") {
  const id = uid();
  return {
    fullName: `E2E Buyer ${prefix} ${id}`,
    email: `e2e-web-${prefix}-${id}@nexusmart.local`,
    phone: uniquePhone(),
    password: `WebE2E!${id.slice(-6)}Aa1`,
  };
}

/**
 * Ensures neither token keys nor raw JWT/cookie secrets appear in a BFF response body.
 */
export function assertNoTokenLeak(text, label, extraSecrets = []) {
  assert.doesNotMatch(
    text,
    /"(?:access_token|accessToken|refresh_token|refreshToken)"/i,
    `${label}: response JSON leaked token field name`
  );
  assert.doesNotMatch(
    text,
    /\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b/,
    `${label}: response body leaked a raw JWT`
  );
  for (const secret of extraSecrets) {
    if (secret && secret.length >= 12) {
      assert.equal(
        text.includes(secret),
        false,
        `${label}: response body leaked raw auth cookie value`
      );
    }
  }
}

/**
 * Ensures gateway internals (trace_id, DB errors, gRPC errors) never leak to browser responses.
 */
export function assertNoInternalErrorLeak(text, label) {
  assert.doesNotMatch(
    text,
    /trace_id|pq:|ECONNREFUSED|rpc error:|panic:|at\s+\/app\//i,
    `${label}: response leaked internal error details: ${text.slice(0, 300)}`
  );
}

/**
 * Verifies Set-Cookie headers set both access & refresh cookies with HttpOnly,
 * SameSite=Lax, Path=/, and host-only (no Domain attribute).
 */
export function assertAuthCookieHeaders(res, label) {
  const rawCookies = res.headers.getSetCookie?.() ?? [];
  assert.ok(rawCookies.length >= 2, `${label}: expected >=2 Set-Cookie headers, got ${rawCookies.length}`);

  const parsed = rawCookies.map((raw) => {
    const parts = raw.split(";").map((p) => p.trim());
    const [pair, ...attrs] = parts;
    const eq = pair.indexOf("=");
    const name = eq >= 0 ? pair.slice(0, eq).trim() : pair;
    const value = eq >= 0 ? pair.slice(eq + 1).trim() : "";
    const attrLower = attrs.map((a) => a.toLowerCase());
    return { raw, name, value, attrLower };
  });

  const access = parsed.find((c) => c.name === "nm_at" || c.name === "__Host-nm_at");
  const refresh = parsed.find((c) => c.name === "nm_rt" || c.name === "__Host-nm_rt");
  assert.ok(access && access.value.length > 10, `${label}: missing access cookie (nm_at / __Host-nm_at)`);
  assert.ok(refresh && refresh.value.length > 10, `${label}: missing refresh cookie (nm_rt / __Host-nm_rt)`);

  for (const cookie of [access, refresh]) {
    assert.ok(
      cookie.attrLower.includes("httponly"),
      `${label}: cookie ${cookie.name} must set HttpOnly (${cookie.raw})`
    );
    assert.ok(
      cookie.attrLower.some((a) => a === "samesite=lax"),
      `${label}: cookie ${cookie.name} must set SameSite=Lax (${cookie.raw})`
    );
    assert.ok(
      cookie.attrLower.some((a) => a === "path=/"),
      `${label}: cookie ${cookie.name} must set Path=/ (${cookie.raw})`
    );
    assert.equal(
      cookie.attrLower.some((a) => a.startsWith("domain=")),
      false,
      `${label}: cookie ${cookie.name} must be host-only without Domain= (${cookie.raw})`
    );
  }

  return { access, refresh };
}

/**
 * Full BFF onboarding for a fresh buyer: register -> Mailpit OTP -> verify-otp -> login -> /api/auth/me.
 */
export async function registerVerifyLoginBuyer(prefix = "buyer") {
  const creds = uniqueBuyerInput(prefix);
  const session = new WebSession(prefix);

  const regRes = await session.post("/api/auth/register", creds);
  const regData = expectStatus(regRes, 201, `POST /api/auth/register (${prefix})`);
  assert.ok(regData.userId, "register must return userId");
  assert.equal(regData.email, creds.email);
  assertNoTokenLeak(regRes.text, `register (${prefix})`);

  const otp = await waitForOTP(creds.email);
  assert.match(otp, /^\d{6}$/, `Mailpit OTP for ${creds.email} must be 6 digits`);

  const verifyRes = await session.post("/api/auth/verify-otp", {
    email: creds.email,
    otp,
  });
  const verifyData = expectStatus(verifyRes, 200, `POST /api/auth/verify-otp (${prefix})`);
  assert.equal(verifyData.verified, true);
  assertNoTokenLeak(verifyRes.text, `verify-otp (${prefix})`);

  const loginRes = await session.post("/api/auth/login", {
    email: creds.email,
    password: creds.password,
  });
  const loginData = expectStatus(loginRes, 200, `POST /api/auth/login (${prefix})`);
  const authCookies = assertAuthCookieHeaders(loginRes, `login (${prefix})`);
  assertNoTokenLeak(loginRes.text, `login (${prefix})`, [
    authCookies.access.value,
    authCookies.refresh.value,
  ]);
  assert.ok(loginData.user?.id, "login must return user.id");
  assert.equal(loginData.user.email, creds.email);
  assert.equal(loginData.user.role, "buyer");

  const meRes = await session.get("/api/auth/me");
  const meData = expectStatus(meRes, 200, `GET /api/auth/me (${prefix})`);
  assertNoTokenLeak(meRes.text, `auth/me (${prefix})`, [
    authCookies.access.value,
    authCookies.refresh.value,
  ]);
  assert.equal(meData.user?.id, loginData.user.id);
  assert.equal(meData.user?.email, creds.email);
  assert.equal(meData.user?.role, "buyer");

  return {
    session,
    user: meData.user,
    creds,
    otp,
    loginRes,
    authCookies,
  };
}

/** Log in an existing demo account (read-only checks only). */
export async function loginAccount(email, password, label = "demo") {
  const session = new WebSession(label);
  const res = await session.post("/api/auth/login", { email, password });
  const data = expectStatus(res, 200, `POST /api/auth/login (${label})`);
  assertAuthCookieHeaders(res, `login (${label})`);
  assertNoTokenLeak(res.text, `login (${label})`, [...session.jar.values()]);
  return { session, user: data.user, res };
}

let cachedLocation = null;

/** Fetches real shipping locations via BFF (cached per process) and creates a valid buyer address via BFF. */
export async function createShippingAddress(session, label = "buyer") {
  if (!cachedLocation) {
    const provRes = await session.get("/api/checkout/locations");
    const provData = expectStatus(provRes, 200, `GET /api/checkout/locations (${label})`);
    assert.ok(Array.isArray(provData.provinces) && provData.provinces.length > 0, "provinces list must be non-empty");
    const province = provData.provinces.find((p) => p.code === "01") ?? provData.provinces[0];

    const wardRes = await session.get(
      `/api/checkout/locations?province_code=${encodeURIComponent(province.code)}`
    );
    const wardData = expectStatus(wardRes, 200, `GET /api/checkout/locations?province_code (${label})`);
    assert.ok(Array.isArray(wardData.wards) && wardData.wards.length > 0, "wards list must be non-empty");
    const ward = wardData.wards[0];
    cachedLocation = { province, ward };
  }

  const { province, ward } = cachedLocation;
  const addrRes = await session.post("/api/checkout/addresses", {
    recipient_name: `Người nhận ${label}`,
    recipient_phone: uniquePhone(),
    province_code: province.code,
    province_name: province.name,
    ward_code: ward.code,
    ward_name: ward.name,
    street_address: `Số 18 Đường E2E ${uid().slice(-4)}`,
    is_default: true,
  });
  const addrData = expectStatus(addrRes, 201, `POST /api/checkout/addresses (${label})`);
  assert.ok(addrData.address?.id, "created address must have id");
  return { address: addrData.address, province, ward };
}

/**
 * Extracts hydrated backend SKU entries (`skuId`, `stock`, `price`) from a rendered
 * storefront product page HTML (`/san-pham/<slug>`).
 */
function extractSkusFromPdpHtml(html) {
  const found = [];
  // Handles both raw JSON and RSC escaped JSON inside Next.js flight scripts.
  const normalized = html.replace(/\\"/g, '"');
  const skuRegex =
    /\{[^{}]*"skuCode":"([^"]+)"[^{}]*"stock":(\d+)[^{}]*"price":(\d+)[^{}]*"skuId":"([0-9a-f-]{36})"[^{}]*\}/gi;
  let m;
  while ((m = skuRegex.exec(normalized)) !== null) {
    found.push({
      skuCode: m[1],
      stock: Number(m[2]),
      price: Number(m[3]),
      skuId: m[4],
    });
  }
  if (found.length > 0) return found;

  // Alternate field order fallback (skuId before stock/price).
  const altRegex = /"skuId":"([0-9a-f-]{36})"/gi;
  while ((m = altRegex.exec(normalized)) !== null) {
    const snippet = normalized.slice(Math.max(0, m.index - 220), m.index + 220);
    const stockMatch = snippet.match(/"stock":(\d+)/);
    const priceMatch = snippet.match(/"price":(\d+)/);
    const codeMatch = snippet.match(/"skuCode":"([^"]+)"/);
    if (stockMatch && priceMatch) {
      found.push({
        skuCode: codeMatch?.[1] ?? "",
        stock: Number(stockMatch[1]),
        price: Number(priceMatch[1]),
        skuId: m[1],
      });
    }
  }
  return found;
}

/**
 * Discovers two in-stock SKUs from TWO DIFFERENT seeded shops, prioritizing the
 * rendered web storefront pages (`/san-pham/<slug>`) so we validate live SSR hydration
 * as well. Falls back to public gateway catalog listing only if needed.
 *
 * Picks SKUs with stock >= minStock and combined price in [350_000, 4_000_000] VND
 * so real seeded vouchers apply and COD stays within the 5,000,000 VND cap.
 */
let cachedTwoShopSkus = null;
const SKU_CACHE_FILE = join(tmpdir(), `nexusmart-e2e-web-skus-${process.ppid}.json`);

/**
 * Extracts and caches 2 in-stock distinct-shop SKUs from an already-fetched storefront
 * PDP HTML response so later test files reuse the exact hydrated SKU IDs without extra fetches.
 */
export function cacheSkusFromPdpHtml(html, { minStock = 5 } = {}) {
  const allExtracted = extractSkusFromPdpHtml(html).filter(
    (s) => s.stock >= minStock && s.price >= 100_000 && s.price <= 2_000_000
  );
  const byShop = new Map();
  for (const shop of SEEDED_SHOPS) {
    const shopSkus = allExtracted
      .filter((s) => s.skuCode.startsWith(shop.skuPrefix))
      .sort((a, b) => b.stock - a.stock);
    if (shopSkus.length > 0) {
      byShop.set(shop.shopKey, {
        ...shopSkus[0],
        slug: shop.slug,
        shopKey: shop.shopKey,
        shopName: shop.shopName,
      });
    }
  }
  const pair = pickBestPair([...byShop.values()]);
  if (pair && pair.length === 2) {
    cachedTwoShopSkus = pair;
    try {
      writeFileSync(SKU_CACHE_FILE, JSON.stringify(pair), "utf8");
    } catch {
      // Best-effort per-run cache.
    }
  }
  return pair;
}

export async function discoverTwoShopSkus({ minStock = 5 } = {}) {
  if (cachedTwoShopSkus) return cachedTwoShopSkus;
  try {
    const raw = readFileSync(SKU_CACHE_FILE, "utf8");
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed) && parsed.length === 2 && parsed[0]?.skuId && parsed[1]?.skuId) {
      cachedTwoShopSkus = parsed;
      return cachedTwoShopSkus;
    }
  } catch {
    // Cache miss on first test file in this run.
  }

  const anon = new WebSession("catalog-probe");
  const byShop = new Map();

  // A single storefront PDP hydrates `allProducts` across all 4 seeded shops, so
  // one GET usually yields in-stock SKUs for every seeded shop without extra gateway fanout.
  for (const cand of SEEDED_SHOPS) {
    const res = await anon.get(`/san-pham/${cand.slug}`, {
      headers: { accept: "text/html" },
    });
    if (res.status !== 200) continue;
    const allExtracted = extractSkusFromPdpHtml(res.text).filter(
      (s) => s.stock >= minStock && s.price >= 100_000 && s.price <= 2_000_000
    );
    for (const shop of SEEDED_SHOPS) {
      if (byShop.has(shop.shopKey)) continue;
      const shopSkus = allExtracted
        .filter((s) => s.skuCode.startsWith(shop.skuPrefix))
        .sort((a, b) => b.stock - a.stock);
      if (shopSkus.length > 0) {
        byShop.set(shop.shopKey, {
          ...shopSkus[0],
          slug: shop.slug,
          shopKey: shop.shopKey,
          shopName: shop.shopName,
        });
      }
    }
    if (pickBestPair([...byShop.values()])) break;
  }

  let pair = pickBestPair([...byShop.values()]);

  if (!pair) {
    // Fallback to public catalog discovery if PDP serialization format changes.
    const [shopsRes, prodRes] = await Promise.all([
      fetch(`${config.gatewayURL}/api/v1/shops?page=1&page_size=50`, {
        signal: AbortSignal.timeout(config.httpTimeoutMs),
      }),
      fetch(`${config.gatewayURL}/api/v1/products?page=1&page_size=100`, {
        signal: AbortSignal.timeout(config.httpTimeoutMs),
      }),
    ]);
    assert.equal(shopsRes.status, 200, "gateway /api/v1/shops fallback must return 200");
    assert.equal(prodRes.status, 200, "gateway /api/v1/products fallback must return 200");
    const shopsJson = await shopsRes.json();
    const prodsJson = await prodRes.json();
    const seededShopMap = new Map(
      (shopsJson.items ?? [])
        .filter((s) => SEEDED_SHOP_NAMES.has(s.name))
        .map((s) => [s.id, s.name])
    );

    const gwByShop = new Map();
    for (const p of prodsJson.items ?? []) {
      const shopName = seededShopMap.get(p.shop_id);
      if (!shopName) continue;
      const best = (p.skus ?? [])
        .filter((k) => k.is_active !== false && k.available_stock >= minStock && k.price_vnd >= 100_000)
        .sort((a, b) => b.available_stock - a.available_stock)[0];
      if (!best) continue;
      const existing = gwByShop.get(p.shop_id);
      if (!existing || best.available_stock > existing.stock) {
        gwByShop.set(p.shop_id, {
          skuId: best.id,
          skuCode: best.sku_code,
          stock: best.available_stock,
          price: best.price_vnd,
          shopId: p.shop_id,
          shopName,
        });
      }
    }
    pair = pickBestPair([...gwByShop.values()]);
  }

  assert.ok(pair && pair.length === 2, "Must discover 2 in-stock SKUs from 2 different seeded shops");
  cachedTwoShopSkus = pair;
  try {
    writeFileSync(SKU_CACHE_FILE, JSON.stringify(pair), "utf8");
  } catch {
    // Best-effort per-run cache across test worker processes.
  }
  return pair;
}

function pickBestPair(items) {
  let fallback = null;
  for (let i = 0; i < items.length; i += 1) {
    for (let j = i + 1; j < items.length; j += 1) {
      if (items[i].shopName === items[j].shopName) continue;
      const sum = items[i].price + items[j].price;
      if (sum >= 350_000 && sum <= 4_000_000) {
        return [items[i], items[j]];
      }
      if (!fallback && sum <= 4_000_000) {
        fallback = [items[i], items[j]];
      }
    }
  }
  return fallback;
}

/**
 * Fetches shipping quotes for cart shop_groups via BFF `/api/checkout/shipping-quote`
 * and returns both `shippingChoices` (for checkout) and `shippingFees` (for voucher preview).
 */
export async function getShippingChoicesForCart(session, address, shopGroups, cod = false) {
  const res = await session.post("/api/checkout/shipping-quote", {
    to_province_code: address.province_code,
    to_ward_code: address.ward_code,
    shops: shopGroups.map((g) => ({
      shop_id: g.shop_id,
      weight_grams: Math.max(200, g.total_weight_grams || 400),
      cod_amount_vnd: cod ? g.subtotal_vnd : 0,
    })),
  });
  const data = expectStatus(res, 200, "POST /api/checkout/shipping-quote");
  const quotesByShop = data.quotes_by_shop ?? {};

  const shippingChoices = [];
  const shippingFees = {};
  for (const g of shopGroups) {
    const quotes = quotesByShop[g.shop_id] ?? [];
    assert.ok(quotes.length > 0, `expected shipping quotes for shop ${g.shop_id}`);
    const chosen = quotes[0];
    shippingChoices.push({
      shop_id: g.shop_id,
      carrier_code: chosen.carrier_code,
      service_code: chosen.service_code,
    });
    shippingFees[g.shop_id] = chosen.fee_vnd;
  }

  return { shippingChoices, shippingFees };
}

/**
 * Picks an eligible active PLATFORM voucher via BFF `GET /api/vouchers` whose
 * `min_order_vnd <= cartTotalVnd` and `remaining_quota > 0`.
 */
export async function pickEligiblePlatformVoucher(session, cartTotalVnd) {
  const res = await session.get(`/api/vouchers?scope=PLATFORM&order_amount_vnd=${cartTotalVnd}`);
  const data = expectStatus(res, 200, "GET /api/vouchers");
  const vouchers = data.vouchers ?? [];
  assert.ok(vouchers.length > 0, "expected seeded vouchers from GET /api/vouchers");

  const preferredOrder = ["NEXUS30", "SINHNHAT50", "EDU15", "NEXUS10", "NEXUSVIP12"];
  const eligible = vouchers.filter(
    (v) =>
      v.is_active &&
      v.scope?.toUpperCase() === "PLATFORM" &&
      v.remaining_quota > 0 &&
      v.min_order_vnd <= cartTotalVnd &&
      (!v.ends_at || Date.parse(v.ends_at) > Date.now())
  );

  assert.ok(
    eligible.length > 0,
    `No eligible PLATFORM voucher found for cart total ${cartTotalVnd} VND (available: ${vouchers
      .map((v) => `${v.code}(min=${v.min_order_vnd},rem=${v.remaining_quota})`)
      .join(", ")})`
  );

  eligible.sort((a, b) => {
    const ai = preferredOrder.indexOf(a.code.toUpperCase());
    const bi = preferredOrder.indexOf(b.code.toUpperCase());
    const aRank = ai === -1 ? 999 : ai;
    const bRank = bi === -1 ? 999 : bi;
    return aRank - bRank || b.remaining_quota - a.remaining_quota;
  });

  return eligible[0];
}

export function newIdempotencyKey(prefix = "e2e-web") {
  return `${prefix}-${randomUUID()}`;
}
