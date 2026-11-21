#!/usr/bin/env node
/**
 * Idempotent backend seed tool for NexusMart (Phase 0b + Phase 2/2b + Swarm A/B/D/T).
 *
 * Reads the canonical storefront catalog from web/src/data/* and seeds via the
 * real gateway REST API + Mailpit OTP verification (+ catalog-gen gRPC for 20k scale):
 *   - 1 demo admin (bootstrapped), 1 demo buyer (+ default address & welcome coins),
 *     4 demo showcase seller accounts (primary demo seller owns Xưởng Dệt Bạch Đằng)
 *     + 1 dedicated 5th catalog seller (seller-catalog@nexusmart.local, NO published site)
 *   - 4 verified ACTIVE showcase shops matching web/src/data/shops.ts + 1 catalog shop
 *   - 6 categories matching web/src/data/products.ts CATEGORIES
 *   - 18 APPROVED showcase products with all 159 SKUs, integer VND prices, stock (with
 *     Phase 2b flash-sale reservation headroom + >= 25 regular available units
 *     while keeping the 6 intentional out-of-stock SKUs at 0), and image paths
 *     matching web/public/products and web/public/models
 *   - 11 vouchers matching web/src/data/vouchers.ts (platform, freeship, shop)
 *   - 3 active/upcoming flash sale slots (12:00 live, 18:00 & 21:00 upcoming)
 *     matching web/src/data/flash-sale.ts, retiring stale pre-Phase-2b windows
 *     missing product_db flash_sale_stock_reservations (PLT-01)
 *   - 4 clearly distinct PUBLISHED subdomain storefront sites (bach-dang, sai-gon,
 *     da-nang, ha-noi), upgraded to schema_version: 2 templates when available
 *     (plus custom domains on ha-noi [ACTIVE] and da-nang [PENDING])
 *   - Chat sample data (auto-reply + realistic 3-message Vietnamese threads on
 *     bach-dang and sai-gon) when Chat service is enabled
 *   - Returns & Complaint sample data (1 pending return on bach-dang + 1 open
 *     admin complaint on sai-gon for demo buyer) when Order depth routes are enabled
 *   - Large catalog (default 20,000 approved products via tools/catalog-gen on
 *     seller-catalog@nexusmart.local; set SEED_CATALOG_COUNT=0 to disable)
 *
 * Safe to run repeatedly: existing entities are detected and reused with zero
 * duplicates. A site is only re-published when its currently published config
 * differs from the spec, so repeated runs never create extra site versions.
 * Exits 0 on success.
 */
import { spawnSync } from "node:child_process";
import { createHmac } from "node:crypto";
import { register } from "node:module";
import { homedir } from "node:os";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

register(new URL("./ts-resolve.mjs", import.meta.url));

const { SHOPS } = await import("../../web/src/data/shops.ts");
const { CATEGORIES, PRODUCTS } = await import("../../web/src/data/products.ts");
const { VOUCHERS, INITIAL_USER_COINS } = await import("../../web/src/data/vouchers.ts");
const { FLASH_SALE_SLOTS } = await import("../../web/src/data/flash-sale.ts");

const env = (key, fallback) => (process.env[key] ?? "").trim() || fallback;
const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "../..");

const GATEWAY_URL = (
  env("GATEWAY_URL", "") ||
  (process.env.GATEWAY_PORT ? `http://localhost:${process.env.GATEWAY_PORT}` : "http://localhost:28080")
).replace(/\/+$/, "");

const MAILPIT_URL = (
  env("MAILPIT_URL", "") ||
  `http://localhost:${env("MAILPIT_UI_PORT", "8025")}`
).replace(/\/+$/, "");

const HTTP_TIMEOUT_MS = Number(env("SEED_HTTP_TIMEOUT_MS", "20000"));
const OTP_TIMEOUT_MS = Number(env("SEED_OTP_TIMEOUT_MS", "30000"));
const SITE_ROOT_DOMAIN = env("SITE_ROOT_DOMAIN", "nexusmart.localhost").toLowerCase();
const SHIPPING_WEBHOOK_SECRET = env(
  "SHIPPING_WEBHOOK_SECRET",
  "dev-only-shipping-webhook-hmac-secret"
);
const FLASH_SALE_REGULAR_STOCK_BUFFER = 25;

const rawSeedCatalogCount = process.env.SEED_CATALOG_COUNT;
const parsedSeedCatalogCount =
  rawSeedCatalogCount !== undefined && rawSeedCatalogCount.trim() !== ""
    ? Number.parseInt(rawSeedCatalogCount.trim(), 10)
    : 20000;
const SEED_CATALOG_COUNT =
  Number.isFinite(parsedSeedCatalogCount) && parsedSeedCatalogCount >= 0
    ? parsedSeedCatalogCount
    : 20000;

const ADMIN_CREDS = {
  email: env("ADMIN_EMAIL", env("ADMIN_BOOTSTRAP_EMAIL", "admin@nexusmart.local")),
  password: env("ADMIN_PASSWORD", env("ADMIN_BOOTSTRAP_PASSWORD", "AdminDevPassword123!")),
};

const DEMO_BUYER = {
  fullName: "Nguyễn Minh Quân",
  email: env("SEED_BUYER_EMAIL", "buyer@nexusmart.local"),
  phone: env("SEED_BUYER_PHONE", "0901234501"),
  password: env("SEED_BUYER_PASSWORD", "BuyerDemo123!"),
};

const SELLER_PASSWORD = env("SEED_SELLER_PASSWORD", "SellerDemo123!");

const SHOP_SELLERS = {
  "shop-bach-dang": {
    fullName: "Trần Quốc Bảo",
    email: env("SEED_SELLER_EMAIL", "seller@nexusmart.local"),
    phone: "0901234511",
    password: SELLER_PASSWORD,
  },
  "shop-sai-gon": {
    fullName: "Lê Hoàng Nam",
    email: env("SEED_SELLER_SAIGON_EMAIL", "seller-saigon@nexusmart.local"),
    phone: "0901234512",
    password: SELLER_PASSWORD,
  },
  "shop-da-nang": {
    fullName: "Phạm Thảo Vy",
    email: env("SEED_SELLER_DANANG_EMAIL", "seller-danang@nexusmart.local"),
    phone: "0901234513",
    password: SELLER_PASSWORD,
  },
  "shop-ha-noi": {
    fullName: "Nguyễn Đức Anh",
    email: env("SEED_SELLER_HANOI_EMAIL", "seller-hanoi@nexusmart.local"),
    phone: "0901234514",
    password: SELLER_PASSWORD,
  },
};

const CATALOG_SELLER = {
  fullName: "Phạm Minh Tuấn",
  email: env("SEED_SELLER_CATALOG_EMAIL", "seller-catalog@nexusmart.local"),
  phone: env("SEED_SELLER_CATALOG_PHONE", "0901234515"),
  password: SELLER_PASSWORD,
};

const CATALOG_SHOP_SPEC = {
  name: "Kho Đại Lý NexusMart",
  slug: "kho-dai-ly-nexusmart",
  city: "Thành phố Hồ Chí Minh",
  warehouseAddress: "Số 88 Đường Nguyễn Hữu Cảnh, Phường Thạnh Mỹ Tây, Thành phố Hồ Chí Minh",
  description:
    "Kho tổng đại lý đa ngành hàng cung cấp danh mục quy mô lớn phục vụ tìm kiếm và kiểm thử trên NexusMart.",
};

const SHOWCASE_SITE_TEMPLATES = {
  "shop-bach-dang": "craft-atelier",
  "shop-sai-gon": "sport-active",
  "shop-da-nang": "minimal-fashion",
  "shop-ha-noi": "luxury-maison",
};

const CATEGORY_WEIGHT_GRAMS = {
  "ao-thun": 240,
  "ao-polo": 280,
  "quan-short": 300,
  "quan-jogger": 460,
  "do-lot": 220,
  "tat-phu-kien": 210,
};

const OTP_CONTEXTUAL_RE = /(?:otp|code|verification|mã|xác\s*thực)[^0-9]{0,24}(\d{6})\b/i;
const OTP_GENERIC_RE = /\b(\d{6})\b/;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function normalizeVi(str) {
  return String(str ?? "")
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/đ/gi, "d")
    .toLowerCase()
    .replace(/\s+/g, " ")
    .trim();
}

async function gwRequest(
  method,
  path,
  { token, body, headers: extraHeaders, allowedStatuses = [200, 201], maxRetries = 4 } = {}
) {
  const headers = { accept: "application/json", ...(extraHeaders ?? {}) };
  if (token) headers.authorization = `Bearer ${token}`;
  let payload;
  if (body !== undefined) {
    headers["content-type"] = "application/json";
    payload = JSON.stringify(body);
  }

  for (let attempt = 0; attempt <= maxRetries; attempt++) {
    const res = await fetch(`${GATEWAY_URL}${path}`, {
      method,
      headers,
      body: payload,
      signal: AbortSignal.timeout(HTTP_TIMEOUT_MS),
    });
    const text = await res.text();
    let data = null;
    try {
      data = text ? JSON.parse(text) : null;
    } catch {
      data = null;
    }

    if (res.status === 429 && attempt < maxRetries) {
      const retrySec = Math.max(1, Number(res.headers.get("retry-after") || "2"));
      console.log(`  [rate-limit] 429 on ${method} ${path}, retrying in ${retrySec}s...`);
      await sleep(retrySec * 1000 + 200);
      continue;
    }

    if (allowedStatuses.includes(res.status)) {
      return { status: res.status, data, text };
    }
    throw new Error(`${method} ${path} failed: HTTP ${res.status}: ${text.slice(0, 500)}`);
  }
  throw new Error(`${method} ${path}: exhausted retries`);
}

async function waitForMailpitOTP(email, sentAfterMs) {
  const deadline = Date.now() + OTP_TIMEOUT_MS;
  const target = email.toLowerCase();

  while (Date.now() < deadline) {
    const searchUrl = `${MAILPIT_URL}/api/v1/search?query=${encodeURIComponent(`to:"${target}"`)}&limit=10`;
    const listRes = await fetch(searchUrl, { signal: AbortSignal.timeout(HTTP_TIMEOUT_MS) });
    if (listRes.ok) {
      const list = await listRes.json();
      for (const m of list.messages ?? []) {
        const createdMs = Date.parse(m.Created || "");
        if (!Number.isNaN(createdMs) && createdMs < sentAfterMs - 5000) {
          continue;
        }
        const msgRes = await fetch(`${MAILPIT_URL}/api/v1/message/${m.ID}`, {
          signal: AbortSignal.timeout(HTTP_TIMEOUT_MS),
        });
        if (!msgRes.ok) continue;
        const full = await msgRes.json();
        const combined = `${full.Subject ?? ""}\n${full.Text || full.HTML || ""}`;
        const match = combined.match(OTP_CONTEXTUAL_RE) ?? combined.match(OTP_GENERIC_RE);
        if (match) return match[1];
      }
    }
    await sleep(300);
  }
  throw new Error(`Timed out waiting for OTP email to ${email} at ${MAILPIT_URL}`);
}

async function login(email, password) {
  const { data } = await gwRequest("POST", "/api/v1/auth/login", {
    body: { email, password },
    allowedStatuses: [200],
  });
  if (!data?.access_token) {
    throw new Error(`Login for ${email} returned no access_token`);
  }
  return data;
}

async function findUserByEmail(adminToken, email) {
  const { data } = await gwRequest(
    "GET",
    `/api/v1/admin/users?query=${encodeURIComponent(email)}&page=1&page_size=20`,
    { token: adminToken, allowedStatuses: [200] }
  );
  return (data?.items ?? []).find((u) => u.email?.toLowerCase() === email.toLowerCase()) ?? null;
}

async function ensureVerifiedUser(adminToken, spec) {
  const existing = await findUserByEmail(adminToken, spec.email);
  let created = false;

  if (!existing) {
    const sentAfter = Date.now();
    await gwRequest("POST", "/api/v1/auth/register", {
      body: {
        full_name: spec.fullName,
        email: spec.email,
        phone: spec.phone,
        password: spec.password,
      },
      allowedStatuses: [200, 201],
    });
    const otp = await waitForMailpitOTP(spec.email, sentAfter);
    await gwRequest("POST", "/api/v1/auth/verify-otp", {
      body: { email: spec.email, otp },
      allowedStatuses: [200],
    });
    created = true;
  } else if (existing.status === "PENDING_VERIFICATION") {
    const sentAfter = Date.now();
    await gwRequest("POST", "/api/v1/auth/resend-otp", {
      body: { email: spec.email },
      allowedStatuses: [200],
    });
    const otp = await waitForMailpitOTP(spec.email, sentAfter);
    await gwRequest("POST", "/api/v1/auth/verify-otp", {
      body: { email: spec.email, otp },
      allowedStatuses: [200],
    });
  } else if (existing.status === "LOCKED") {
    await gwRequest("PATCH", `/api/v1/admin/users/${encodeURIComponent(existing.id)}/status`, {
      token: adminToken,
      body: { status: "ACTIVE", reason: "Seed reactivate demo account" },
      allowedStatuses: [200],
    });
  }

  const session = await login(spec.email, spec.password);
  return {
    created,
    accessToken: session.access_token,
    refreshToken: session.refresh_token,
    user: session.user,
  };
}

async function loadLocationHelper() {
  const { data } = await gwRequest("GET", "/api/v1/shipping/provinces", { allowedStatuses: [200] });
  const provinces = data?.provinces ?? [];
  if (provinces.length === 0) {
    throw new Error("GET /api/v1/shipping/provinces returned 0 provinces");
  }
  const wardsCache = new Map();

  async function resolveProvinceAndWard(cityHint, addressHint = "") {
    const needle = normalizeVi(cityHint).replace(/^(thanh pho|tinh)\s+/, "");
    const province =
      provinces.find((p) => normalizeVi(p.name) === normalizeVi(cityHint)) ||
      provinces.find((p) => normalizeVi(p.name).includes(needle)) ||
      provinces[0];

    let wards = wardsCache.get(province.code);
    if (!wards) {
      const wRes = await gwRequest(
        "GET",
        `/api/v1/shipping/provinces/${encodeURIComponent(province.code)}/wards`,
        { allowedStatuses: [200] }
      );
      wards = wRes.data?.wards ?? [];
      wardsCache.set(province.code, wards);
    }
    if (wards.length === 0) {
      throw new Error(`No wards returned for province ${province.code} (${province.name})`);
    }
    const normAddr = normalizeVi(addressHint);
    const ward =
      (normAddr &&
        wards.find((w) => {
          const short = normalizeVi(w.name).replace(/^(phuong|xa|thi tran)\s+/, "");
          return short && normAddr.includes(short);
        })) ||
      wards[0];

    return { province, ward };
  }

  return { resolveProvinceAndWard };
}

async function ensureDefaultAddress(userToken, { recipientName, recipientPhone, city, streetAddress }, resolveLoc) {
  const { data } = await gwRequest("GET", "/api/v1/users/me/addresses", {
    token: userToken,
    allowedStatuses: [200],
  });
  const existing = data?.addresses ?? [];
  if (existing.length > 0) {
    return { address: existing.find((a) => a.is_default) ?? existing[0], created: false };
  }
  const { province, ward } = await resolveLoc(city, streetAddress);
  const created = await gwRequest("POST", "/api/v1/users/me/addresses", {
    token: userToken,
    body: {
      recipient_name: recipientName,
      recipient_phone: recipientPhone,
      province_code: province.code,
      province_name: province.name,
      ward_code: ward.code,
      ward_name: ward.name,
      street_address: streetAddress,
      is_default: true,
    },
    allowedStatuses: [200, 201],
  });
  return { address: created.data.address, created: true };
}

async function listAllAdminShops(adminToken) {
  const all = [];
  for (let page = 1; page <= 20; page++) {
    const { data } = await gwRequest("GET", `/api/v1/admin/shops?page=${page}&page_size=100`, {
      token: adminToken,
      allowedStatuses: [200],
    });
    const items = data?.items ?? [];
    all.push(...items);
    if (items.length < 100 || all.length >= (data?.total ?? 0)) break;
  }
  return all;
}

async function listAllSellerProducts(sellerToken) {
  const all = [];
  for (let page = 1; page <= 20; page++) {
    const { data } = await gwRequest("GET", `/api/v1/seller/products?page=${page}&page_size=100`, {
      token: sellerToken,
      allowedStatuses: [200],
    });
    const items = data?.items ?? [];
    all.push(...items);
    if (items.length < 100 || all.length >= (data?.total ?? 0)) break;
  }
  return all;
}

/**
 * Compute non-overlapping flash sale windows aligned to VN (+07:00) clock labels
 * so slot-12 is always LIVE right now (12:00 -> next day 18:00) and slot-18 /
 * slot-21 are UPCOMING (18:00-21:00 and 21:00-23:59), matching web/src/data/flash-sale.ts.
 */
function buildFlashWindowTimes(nowMs) {
  const VN_OFFSET_MS = 7 * 3600 * 1000;
  const HOUR_MS = 3600 * 1000;
  const DAY_MS = 24 * HOUR_MS;

  const vnNow = new Date(nowMs + VN_OFFSET_MS);
  const vnMidnightUtcMs =
    Date.UTC(vnNow.getUTCFullYear(), vnNow.getUTCMonth(), vnNow.getUTCDate()) - VN_OFFSET_MS;

  let liveStartMs = vnMidnightUtcMs + 12 * HOUR_MS;
  if (nowMs < liveStartMs) {
    liveStartMs -= DAY_MS;
  }
  const liveEndMs = liveStartMs + 30 * HOUR_MS; // next day 18:00 VN (always > now)
  const slot18StartMs = liveEndMs; // 18:00 VN
  const slot18EndMs = slot18StartMs + 3 * HOUR_MS; // 21:00 VN
  const slot21StartMs = slot18EndMs; // 21:00 VN
  const slot21EndMs = slot21StartMs + 2 * HOUR_MS + 59 * 60 * 1000; // 23:59 VN

  return {
    "slot-12": {
      name: "NexusMart Flash Sale 12:00",
      startsAt: new Date(liveStartMs).toISOString(),
      endsAt: new Date(liveEndMs).toISOString(),
    },
    "slot-18": {
      name: "NexusMart Flash Sale 18:00",
      startsAt: new Date(slot18StartMs).toISOString(),
      endsAt: new Date(slot18EndMs).toISOString(),
    },
    "slot-21": {
      name: "NexusMart Flash Sale 21:00",
      startsAt: new Date(slot21StartMs).toISOString(),
      endsAt: new Date(slot21EndMs).toISOString(),
    },
  };
}

function distributeSlots(totalQuota, count) {
  const n = Math.max(1, count);
  const base = Math.floor(totalQuota / n);
  let rem = totalQuota - base * n;
  const out = [];
  for (let i = 0; i < n; i++) {
    const extra = rem > 0 ? 1 : 0;
    if (rem > 0) rem -= 1;
    out.push(Math.max(1, base + extra));
  }
  return out;
}

/**
 * Selects the primary-color in-stock SKUs of a product to register in flash sales.
 * Never selects intentional 0-stock SKUs. Shared between stock planning and flash-sale seeding.
 */
function selectFlashSaleTargetSkus(mockProduct, skuIdByCode = null) {
  const isAvailable = (s) =>
    Math.trunc(s.stock) > 0 && (!skuIdByCode || skuIdByCode.has(s.skuCode));
  const primaryColor = mockProduct.colors[0]?.code;
  const candidateSkus = mockProduct.skus.filter(
    (s) => s.colorCode === primaryColor && isAvailable(s)
  );
  if (candidateSkus.length > 0) return candidateSkus;
  return mockProduct.skus.filter(isAvailable).slice(0, 1);
}

/**
 * Precomputes total flash-sale slot demand per skuCode across all seeded (non-ended) windows
 * so Phase 2b flash-sale stock reservations leave >= FLASH_SALE_REGULAR_STOCK_BUFFER regular units.
 */
function buildFlashDemandBySkuCode(products, flashSlots, windowSpecs) {
  const prodById = new Map(products.map((p) => [p.id, p]));
  const demand = new Map(); // skuCode -> total flash slots across seeded windows

  for (const slot of flashSlots) {
    if (!windowSpecs[slot.id]) continue;
    for (const item of slot.items) {
      const mockProd = prodById.get(item.productId);
      if (!mockProd) continue;
      const targetSkus = selectFlashSaleTargetSkus(mockProd);
      const slotsPerSku = distributeSlots(item.totalQuota, targetSkus.length);
      for (let i = 0; i < targetSkus.length; i++) {
        const code = targetSkus[i].skuCode;
        demand.set(code, (demand.get(code) ?? 0) + slotsPerSku[i]);
      }
    }
  }
  return demand;
}

function computeDesiredSkuStock(mockSku, totalFlashDemand) {
  const baseStock = Math.trunc(mockSku.stock);
  if (baseStock <= 0) return 0; // preserve intentional out-of-stock SKUs
  if (totalFlashDemand <= 0) return baseStock;
  return totalFlashDemand + Math.max(baseStock, FLASH_SALE_REGULAR_STOCK_BUFFER);
}

async function topUpSkuStock(sellerToken, productId, sku, targetStock) {
  let current = sku;
  for (let attempt = 0; attempt < 2; attempt++) {
    const version = Number(current.version ?? 1);
    const res = await gwRequest("PATCH", `/api/v1/seller/skus/${encodeURIComponent(current.id)}/stock`, {
      token: sellerToken,
      body: {
        new_stock: Math.trunc(targetStock),
        expected_version: version > 0 ? version : 1,
      },
      allowedStatuses: [200, 409],
    });
    if (res.status === 200) {
      return res.data?.sku ?? { ...current, stock: Math.trunc(targetStock), version: version + 1 };
    }
    // Reload latest SKU version on optimistic-locking conflict and retry once.
    const prodRes = await gwRequest("GET", `/api/v1/products/${encodeURIComponent(productId)}`, {
      token: sellerToken,
      allowedStatuses: [200],
    });
    const refreshed = (prodRes.data?.product?.skus ?? []).find((s) => s.id === current.id);
    if (!refreshed) {
      throw new Error(`SKU ${current.id} disappeared while reloading for stock top-up`);
    }
    if (Math.trunc(refreshed.stock ?? 0) >= Math.trunc(targetStock)) {
      return refreshed;
    }
    current = refreshed;
  }
  throw new Error(`Failed to top up stock for SKU ${sku.sku_code} (${sku.id}) after conflict retry`);
}

function parseMaybeJson(val) {
  if (val == null) return null;
  if (typeof val === "string") {
    const trimmed = val.trim();
    if (!trimmed) return null;
    try {
      return JSON.parse(trimmed);
    } catch {
      return null;
    }
  }
  return val;
}

function normalizeForCompare(value) {
  if (value === null || value === undefined) return undefined;
  if (Array.isArray(value)) {
    return value.map((item) => normalizeForCompare(item));
  }
  if (typeof value === "object") {
    const out = {};
    for (const key of Object.keys(value).sort()) {
      const normalized = normalizeForCompare(value[key]);
      // Ignore omitted/empty optional string fields so Go `omitempty` round-trips stay equal.
      if (normalized === undefined || normalized === "") continue;
      out[key] = normalized;
    }
    return out;
  }
  return value;
}

function siteConfigsEqual(a, b) {
  const normA = normalizeForCompare(parseMaybeJson(a));
  const normB = normalizeForCompare(parseMaybeJson(b));
  if (!normA || !normB) return false;
  return JSON.stringify(normA) === JSON.stringify(normB);
}

/**
 * Builds the 4 showcase subdomain storefront site specs, one per demo shop. Each theme uses a
 * different palette (light cream/blue, dark charcoal/amber, light sage/green, dark wine/gold)
 * and a different allow-listed font so the sites are clearly distinguishable.
 */
function buildShowcaseSiteSpecs(seededProducts) {
  const productUuid = (mockId) => seededProducts.get(mockId)?.product?.id ?? "";
  const bachDangProductIds = [
    productUuid("prod-at-trang"),
    productUuid("prod-at-xamda"),
    productUuid("prod-at-xanhthan"),
    productUuid("prod-dolot-boxer"),
  ].filter(Boolean);

  const saiGonProductIds = [
    productUuid("prod-at-dryfit"),
    productUuid("prod-short-run"),
    productUuid("prod-aokhoac-gio"),
    productUuid("prod-at-oversize"),
  ].filter(Boolean);

  const daNangProductIds = [
    productUuid("prod-polo-kem"),
    productUuid("prod-polo-xanhda"),
    productUuid("prod-short-linen"),
    productUuid("prod-dolot-brief"),
  ].filter(Boolean);

  const haNoiProductIds = [
    productUuid("prod-polo-detkim"),
    productUuid("prod-jogger-terry"),
    productUuid("prod-jogger-tech"),
    productUuid("prod-tat-cotrung"),
  ].filter(Boolean);

  return [
    {
      mockShopId: "shop-bach-dang",
      subdomain: "bach-dang",
      publishNote: "Xuất bản giao diện mẫu Xưởng Dệt Bạch Đằng",
      config: {
        theme: {
          bg_color: "#F8F6F1",
          surface_color: "#FFFFFF",
          text_color: "#182026",
          accent_color: "#1D4ED8",
          accent_text_color: "#FFFFFF",
          font_family: "work-sans",
          logo_url: "/banners/vietnam-workshop.jpg",
          banner_url: "/banners/campaign-everyday.jpg",
          brand_name: "Xưởng Dệt Bạch Đằng",
          tagline: "Áo thun Cotton Compact 220gsm may trực tiếp tại xưởng Hải Phòng",
        },
        sections: [
          {
            id: "bd-hero",
            type: "hero",
            visible: true,
            props: {
              title: "Cotton Compact 220gsm từ xưởng Hải Phòng",
              subtitle:
                "Dệt kim tròn chủ động, xử lý chống co rút trước khi cắt rập. Mặt vải mịn đứng vai và giữ form cổ áo bền bỉ qua hơn 40 lần giặt máy.",
              image_url: "/banners/campaign-everyday.jpg",
              cta_label: "Xem sản phẩm xưởng",
              cta_href: "/san-pham",
            },
          },
          {
            id: "bd-featured",
            type: "featured_products",
            visible: true,
            props: {
              title: "Sản phẩm chủ lực tại xưởng",
              subtitle:
                "Đồ mặc hằng ngày từ bông chải kỹ 220gsm và sợi gỗ sồi Modal, giá trực tiếp từ Xưởng Dệt Bạch Đằng.",
              product_ids: bachDangProductIds,
              limit: 4,
            },
          },
          {
            id: "bd-vouchers",
            type: "voucher_strip",
            visible: true,
            props: {
              title: "Ưu đãi riêng của Xưởng Dệt Bạch Đằng",
              subtitle: "Lưu mã giảm giá gian hàng và áp dụng trực tiếp khi đặt mua từ cửa hàng.",
              limit: 4,
            },
          },
          {
            id: "bd-collection",
            type: "collection",
            visible: true,
            props: {
              title: "Dòng áo thun Cotton Compact 220gsm",
              subtitle:
                "Ba tông màu cơ bản trắng tinh, xám đá và xanh than dễ phối đồ đi làm lẫn dạo phố.",
              collection_slug: "cotton-220gsm-bach-dang",
              category_slug: "ao-thun",
              image_url: "/banners/fabric-closeup.jpg",
              limit: 4,
            },
          },
          {
            id: "bd-banner",
            type: "banner",
            visible: true,
            props: {
              title: "Kiểm định 3 bước tại nhà máy Hải Phòng",
              subtitle:
                "Mỗi lô vải mộc đều trải qua 3 chu kỳ giặt thử đo co rút, soi mật độ mũi kim và kiểm bền màu trước khi đóng gói.",
              image_url: "/banners/vietnam-workshop.jpg",
              cta_label: "Tìm hiểu xưởng dệt",
              cta_href: "/gioi-thieu",
            },
          },
          {
            id: "bd-story",
            type: "text",
            visible: true,
            props: {
              title: "Triết lý dệt may bền bỉ từ năm 2019",
              body: "Xưởng Dệt Bạch Đằng đặt tại Hải Phòng, chủ động từ khâu dệt kim tròn đến cắt rập và hoàn thiện. Chúng tôi tập trung vào hai dòng sản phẩm cốt lõi là áo thun Cotton Compact 220gsm và đồ lót sợi gỗ sồi Modal may phẳng Flatlock, để mỗi đường kim mũi chỉ đều đồng đều qua từng đợt sản xuất. Khách hàng nhận giao tiêu chuẩn liên tỉnh 1 đến 3 ngày cùng chính sách đổi size hai chiều tận nhà trong 45 ngày.",
              align: "left",
            },
          },
          {
            id: "bd-policies",
            type: "policies",
            visible: true,
            props: {
              title: "Cam kết bán hàng và hậu mãi",
              items: [
                {
                  heading: "Đổi trả miễn phí 45 ngày",
                  detail:
                    "Hỗ trợ đổi size hai chiều tận nơi với bưu tá nhận và giao lại trong một lượt cho sản phẩm còn nguyên tem mác.",
                },
                {
                  heading: "Giao hàng từ kho Hải Phòng",
                  detail:
                    "Đóng gói trong ngày và giao tiêu chuẩn liên tỉnh từ 1 đến 3 ngày trên toàn quốc 34 tỉnh thành.",
                },
                {
                  heading: "Kiểm hàng trước khi nhận",
                  detail:
                    "Cho phép đồng kiểm đúng màu sắc, đúng cỡ và chất lượng đường may đối với mọi đơn thanh toán khi nhận hàng.",
                },
                {
                  heading: "Nguồn sợi minh bạch",
                  detail:
                    "Công bố rõ định lượng 220gsm, thành phần 95% Cotton chải kỹ Compact pha 5% Spandex không chứa chất huỳnh quang.",
                },
              ],
            },
          },
        ],
      },
    },
    {
      mockShopId: "shop-sai-gon",
      subdomain: "sai-gon",
      publishNote: "Xuất bản giao diện mẫu Kho Basic Sài Gòn",
      config: {
        theme: {
          bg_color: "#14181C",
          surface_color: "#1E242B",
          text_color: "#F4F6F8",
          accent_color: "#D97706",
          accent_text_color: "#111418",
          font_family: "be-vietnam-pro",
          logo_url: "/banners/campaign-running.jpg",
          banner_url: "/banners/campaign-running.jpg",
          brand_name: "Kho Basic Sài Gòn",
          tagline: "Đồ chạy bộ Dry-Fit siêu nhẹ và trang phục vận động cho khí hậu miền Nam",
        },
        sections: [
          {
            id: "sg-hero",
            type: "hero",
            visible: true,
            props: {
              title: "Trang phục vận động nhẹ mát Sài Gòn",
              subtitle:
                "Sợi Dry-Fit 145gsm khô nhanh sau 45 phút phơi gió cùng quần short chạy bộ 5 inch siêu nhẹ, thiết kế riêng cho thời tiết nắng nóng.",
              image_url: "/banners/campaign-running.jpg",
              cta_label: "Khám phá đồ thể thao",
              cta_href: "/san-pham",
            },
          },
          {
            id: "sg-featured",
            type: "featured_products",
            visible: true,
            props: {
              title: "Thiết kế bán chạy cho tập luyện",
              subtitle:
                "Từ đường chạy 5K buổi sáng tới bộ đồ basic thoáng nhẹ mặc suốt ngày làm việc tại TP.HCM.",
              product_ids: saiGonProductIds,
              limit: 4,
            },
          },
          {
            id: "sg-collection",
            type: "collection",
            visible: true,
            props: {
              title: "Quần short chạy bộ và mặc hằng ngày",
              subtitle:
                "Cạp giấu dây rút chắc chắn, túi khóa kéo sau lưng giữ gọn điện thoại và chìa khóa khi vận động.",
              collection_slug: "chay-bo-sai-gon",
              category_slug: "quan-short",
              image_url: "/banners/campaign-green-fiber.jpg",
              limit: 4,
            },
          },
          {
            id: "sg-vouchers",
            type: "voucher_strip",
            visible: true,
            props: {
              title: "Mã ưu đãi Kho Basic Sài Gòn",
              subtitle:
                "Nhận ngay mã giảm trực tiếp trên giỏ hàng của xưởng khi đặt trang phục chạy bộ và đồ basic.",
              limit: 4,
            },
          },
          {
            id: "sg-story",
            type: "text",
            visible: true,
            props: {
              title: "Chuẩn đường may 4 kim 6 chỉ tại Tân Thuận",
              body: "Ra đời năm 2020 tại Thành phố Hồ Chí Minh, Kho Basic Sài Gòn tối ưu từng chi tiết nhỏ cho người chạy bộ và tập luyện hằng ngày. Sợi Polyester tái chế Micro-mesh và Polyamide co giãn 4 chiều được đo tốc độ thoát ẩm theo từng lô vải, kết hợp đường may chập phẳng ở nách và đùi để giảm ma sát tối đa trong những buổi chạy dài. Đơn nội thành TP.HCM đặt trước 17 giờ được hỗ trợ giao nhanh trong 2 đến 4 giờ.",
              align: "left",
            },
          },
          {
            id: "sg-policies",
            type: "policies",
            visible: true,
            props: {
              title: "Chính sách giao vận và đổi cỡ",
              items: [
                {
                  heading: "Giao nhanh 2 đến 4 giờ TP.HCM",
                  detail:
                    "Áp dụng cho đơn đặt trước 17 giờ tại khu vực nội thành Thành phố Hồ Chí Minh trực tiếp từ kho Thảo Điền.",
                },
                {
                  heading: "Đổi size tận nhà 45 ngày",
                  detail:
                    "Miễn phí đổi cỡ hai chiều khi mặc chưa vừa vai hoặc chiều dài ống quần, xử lý nhanh trong vòng 24 giờ.",
                },
                {
                  heading: "Sợi tái chế truy xuất lô",
                  detail:
                    "Sử dụng sợi Micro-mesh tái chế nhẹ thoáng và đóng gói trong túi tự hủy sinh học giảm rác thải nhựa.",
                },
                {
                  heading: "Tư vấn cỡ trong 12 phút",
                  detail:
                    "Đội ngũ xưởng hỗ trợ chọn đúng form vận động hoặc oversize theo chiều cao và cân nặng thực tế.",
                },
              ],
            },
          },
        ],
      },
    },
    {
      mockShopId: "shop-da-nang",
      subdomain: "da-nang",
      publishNote: "Xuất bản giao diện mẫu Sợi Tự Nhiên Đà Nẵng",
      customDomain: {
        hostname: "danang.pending.test",
        expectStatus: "PENDING",
      },
      config: {
        theme: {
          bg_color: "#EEF2EA",
          surface_color: "#F9FBF6",
          text_color: "#1F2A22",
          accent_color: "#3F6B4F",
          accent_text_color: "#FFFFFF",
          font_family: "ibm-plex-sans",
          logo_url: "/banners/campaign-green-fiber.jpg",
          banner_url: "/banners/campaign-office.jpg",
          brand_name: "Sợi Tự Nhiên Đà Nẵng",
          tagline: "Áo polo pique, quần short linen và đồ lót sợi tre thoáng mát cho miền Trung nắng gió",
        },
        sections: [
          {
            id: "dn-hero",
            type: "hero",
            visible: true,
            props: {
              eyebrow: "Sợi thực vật từ Đà Nẵng",
              title: "Lanh, tre và cotton pique cho ngày nắng",
              subtitle:
                "Chất liệu sợi thực vật thoáng khí như lanh pha bông và sợi tre thiên nhiên, nhuộm tông trung tính dễ phối từ công sở đến cuối tuần.",
              image_url: "/banners/campaign-office.jpg",
              cta_label: "Xem đồ sợi tự nhiên",
              cta_href: "/san-pham",
            },
          },
          {
            id: "dn-collection",
            type: "collection",
            visible: true,
            props: {
              title: "Polo Pique và Short Linen",
              subtitle: "Thoáng nhẹ cho công sở mùa nóng và du lịch cuối tuần.",
              collection_slug: "cong-so-soi-tu-nhien",
              category_slug: "ao-polo",
              image_url: "/banners/campaign-green-fiber.jpg",
              limit: 3,
            },
          },
          {
            id: "dn-featured",
            type: "featured_products",
            visible: true,
            props: {
              title: "Được chọn nhiều tại xưởng Hòa Khánh",
              subtitle: "Polo cotton pique chải kỹ, short linen pha cotton và combo đồ lót sợi tre.",
              product_ids: daNangProductIds,
              limit: 4,
            },
          },
          {
            id: "dn-text",
            type: "text",
            visible: true,
            props: {
              title: "Giặt mềm enzyme, chạm dịu từ lần mặc đầu",
              body: "Sợi Tự Nhiên Đà Nẵng làm việc với Xưởng Sợi Tự Nhiên Miền Trung tại Khu công nghiệp Hòa Khánh. Sợi lanh và sợi tre được giặt mềm bằng enzyme sinh học, nẹp cổ polo và bo tay dệt rib 1:1 được kiểm tra độ thẳng sau khi hấp hơi, và từng lô sợi được hút ẩm, cân lại trọng lượng trước khi xuất kho.",
              align: "center",
            },
          },
          {
            id: "dn-vouchers",
            type: "voucher_strip",
            visible: true,
            props: {
              title: "Ưu đãi của Sợi Tự Nhiên",
              subtitle: "Lưu mã của gian hàng và áp dụng ngay ở bước thanh toán.",
              limit: 3,
            },
          },
          {
            id: "dn-policies",
            type: "policies",
            visible: true,
            props: {
              title: "Mua sắm an tâm",
              items: [
                {
                  heading: "Đổi trả miễn phí 45 ngày",
                  detail: "Đổi size hai chiều tận nơi cho sản phẩm còn nguyên tem mác.",
                },
                {
                  heading: "Giao nhanh nội thành Đà Nẵng",
                  detail: "Giao nhanh nội thành Đà Nẵng trong 3 giờ, gửi từ kho số 76 đường Bạch Đằng, Phường Hải Châu.",
                },
                {
                  heading: "Miễn phí vận chuyển từ 229.000đ",
                  detail: "Đơn hàng của gian hàng từ 229.000đ được miễn phí giao tiêu chuẩn.",
                },
              ],
            },
          },
        ],
      },
    },
    {
      mockShopId: "shop-ha-noi",
      subdomain: "ha-noi",
      publishNote: "Xuất bản giao diện mẫu Dệt Kim Hà Nội",
      customDomain: {
        hostname: "hanoi-knit.verified.test",
        expectStatus: "ACTIVE",
      },
      config: {
        theme: {
          bg_color: "#2B1A1E",
          surface_color: "#3A2328",
          text_color: "#F7EDE8",
          accent_color: "#E9B949",
          accent_text_color: "#2B1A1E",
          font_family: "plus-jakarta-sans",
          logo_url: "/atmosphere/cotton-twill-slate.jpg",
          banner_url: "/atmosphere/jacquard-loom-poster.jpg",
          brand_name: "Dệt Kim Hà Nội",
          tagline: "Áo polo dệt kim, jogger nỉ chân cua và tất cotton gân theo tay nghề dệt kim miền Bắc",
        },
        sections: [
          {
            id: "hn-banner",
            type: "banner",
            visible: true,
            props: {
              title: "Dệt kim và nỉ chân cua 320gsm",
              subtitle: "Đứng phom cho tiết trời mát và môi trường điều hòa.",
              image_url: "/atmosphere/jacquard-loom-poster.jpg",
              cta_label: "Xem bộ sưu tập",
              cta_href: "/san-pham",
            },
          },
          {
            id: "hn-featured",
            type: "featured_products",
            visible: true,
            props: {
              title: "Hàng dệt kim tiêu biểu",
              subtitle: "Cổ polo đứng dáng, bo gấu jogger dày dặn và tất cotton chải kỹ nối mũi liền mạch.",
              product_ids: haNoiProductIds,
              limit: 4,
            },
          },
          {
            id: "hn-vouchers",
            type: "voucher_strip",
            visible: true,
            props: {
              title: "Mã giảm giá Dệt Kim Hà Nội",
              subtitle: "Áp dụng cho đơn đặt trực tiếp tại gian hàng.",
              limit: 2,
            },
          },
          {
            id: "hn-collection",
            type: "collection",
            visible: true,
            props: {
              title: "Tất cotton gân và thể thao",
              subtitle: "Nối mũi liền mạch, áp dụng chính sách bù lẻ chiếc thất lạc.",
              collection_slug: "combo-tat-cotton-ha-noi",
              category_slug: "tat-phu-kien",
              image_url: "/atmosphere/cotton-twill-slate.jpg",
              limit: 2,
            },
          },
          {
            id: "hn-text",
            type: "text",
            visible: true,
            props: {
              title: "Tay nghề dệt kim từ năm 2018",
              body: "Dệt Kim Hà Nội kế thừa tay nghề dệt kim truyền thống miền Bắc tại Xưởng Dệt kim truyền thống Minh Khai. Mắt dệt áo polo được soi trên bàn sáng, bo gấu jogger và cổ tất được thử giãn qua 500 nhịp kéo, và mũi tất được nối liền mạch, rà kim loại trước khi đóng hộp.",
              align: "left",
            },
          },
          {
            id: "hn-policies",
            type: "policies",
            visible: true,
            props: {
              title: "Chính sách của gian hàng",
              items: [
                {
                  heading: "Đổi trả miễn phí 45 ngày",
                  detail: "Đổi size hai chiều và bù lẻ chiếc tất thất lạc.",
                },
                {
                  heading: "Giao nhanh nội thành Hà Nội",
                  detail: "Giao nhanh nội thành Hà Nội trong 2 đến 4 giờ với đơn đặt trước 17:00.",
                },
                {
                  heading: "Miễn phí vận chuyển từ 199.000đ",
                  detail: "Đơn hàng của gian hàng từ 199.000đ được miễn phí giao tiêu chuẩn.",
                },
              ],
            },
          },
        ],
      },
    },
  ];
}

/** Returns the currently PUBLISHED config for a host via the public resolver, or null if not live. */
async function fetchPublishedSiteConfig(host) {
  if (!host) return null;
  const res = await gwRequest("GET", `/api/v1/sites/by-host?host=${encodeURIComponent(host)}`, {
    allowedStatuses: [200, 404],
  });
  if (res.status !== 200 || res.data?.site?.status !== "PUBLISHED") return null;
  return res.data.site.config ?? null;
}

/**
 * Ensures the seller's site is PUBLISHED on spec.subdomain with exactly spec.config.
 * Publishes a new version only when the live published config (or subdomain) differs, so
 * repeated runs never create extra site versions. The draft is rewritten only when it differs.
 */
async function ensurePublishedSite(sellerToken, spec) {
  const getRes = await gwRequest("GET", "/api/v1/seller/site", {
    token: sellerToken,
    allowedStatuses: [200, 404],
  });
  const existing = getRes.status === 200 ? (getRes.data?.site ?? null) : null;
  const existingSubdomain = String(existing?.subdomain ?? "").trim().toLowerCase();
  const targetSubdomain = spec.subdomain.trim().toLowerCase();
  const targetHost = `${targetSubdomain}.${SITE_ROOT_DOMAIN}`;

  if (existing?.status === "SUSPENDED") {
    throw new Error(
      `Site ${existingSubdomain || targetSubdomain} is SUSPENDED by admin; unsuspend it before re-running seed`
    );
  }

  const subdomainMatches = existing !== null && existingSubdomain === targetSubdomain;
  if (subdomainMatches && existing.status === "PUBLISHED") {
    const existingPrimaryHost = String(existing.primary_host ?? "").trim().toLowerCase();
    let publishedCfg = await fetchPublishedSiteConfig(existingPrimaryHost || targetHost);
    if (!publishedCfg && existingPrimaryHost && existingPrimaryHost !== targetHost) {
      publishedCfg = await fetchPublishedSiteConfig(targetHost);
    }
    if (siteConfigsEqual(publishedCfg, spec.config)) {
      return { created: false, site: existing };
    }
  }

  // GET /seller/site returns the current draft config.
  const draftMatches = subdomainMatches && siteConfigsEqual(existing.config, spec.config);
  if (!draftMatches) {
    await gwRequest("PUT", "/api/v1/seller/site/draft", {
      token: sellerToken,
      body: {
        subdomain: targetSubdomain,
        config: spec.config,
      },
      allowedStatuses: [200, 201],
    });
  }

  const pubRes = await gwRequest("POST", "/api/v1/seller/site/publish", {
    token: sellerToken,
    body: {
      note: spec.publishNote,
    },
    allowedStatuses: [200, 201],
  });

  const publishedSite = pubRes.data?.site ?? { subdomain: targetSubdomain, status: "PUBLISHED" };
  return { created: true, site: publishedSite };
}

/**
 * Ensures the seller's site has the requested custom domain in the expected status.
 * Idempotent: reuses a matching host, removes any stale CUSTOM domain on another host
 * before creating, and calls /check only when ACTIVE is expected.
 */
async function ensureCustomDomain(sellerToken, { hostname, expectStatus }) {
  const targetHost = String(hostname ?? "").trim().toLowerCase();
  const desiredStatus = String(expectStatus ?? "").trim().toUpperCase();
  if (!targetHost) {
    throw new Error("ensureCustomDomain requires a non-empty hostname");
  }

  const listRes = await gwRequest("GET", "/api/v1/seller/site/domains", {
    token: sellerToken,
    allowedStatuses: [200],
  });
  const domains = Array.isArray(listRes.data?.domains) ? listRes.data.domains : [];

  let domain =
    domains.find((d) => String(d?.host ?? "").trim().toLowerCase() === targetHost) ?? null;
  let created = false;

  if (!domain) {
    const staleCustoms = domains.filter(
      (d) =>
        String(d?.kind ?? "").trim().toUpperCase() === "CUSTOM" &&
        String(d?.host ?? "").trim().toLowerCase() !== targetHost
    );
    for (const stale of staleCustoms) {
      await gwRequest("DELETE", `/api/v1/seller/site/domains/${encodeURIComponent(stale.id)}`, {
        token: sellerToken,
        allowedStatuses: [200, 204],
      });
    }

    const addRes = await gwRequest("POST", "/api/v1/seller/site/domains", {
      token: sellerToken,
      body: { hostname: targetHost },
      allowedStatuses: [200, 201],
    });
    domain = addRes.data?.domain ?? null;
    if (!domain?.id) {
      throw new Error(`POST /api/v1/seller/site/domains for ${targetHost} returned no domain`);
    }
    created = true;
  }

  const statusOf = (d) => String(d?.status ?? "").trim().toUpperCase();

  if (desiredStatus === "ACTIVE") {
    if (statusOf(domain) !== "ACTIVE") {
      const checkRes = await gwRequest(
        "POST",
        `/api/v1/seller/site/domains/${encodeURIComponent(domain.id)}/check`,
        {
          token: sellerToken,
          allowedStatuses: [200],
        }
      );
      domain = checkRes.data?.domain ?? domain;
    }
    if (statusOf(domain) !== "ACTIVE") {
      throw new Error(
        `Custom domain ${targetHost} expected status ACTIVE, got ${domain?.status ?? "unknown"}`
      );
    }

    const activeHost = String(domain.host || targetHost).trim().toLowerCase();
    const byHostRes = await gwRequest(
      "GET",
      `/api/v1/sites/by-host?host=${encodeURIComponent(activeHost)}`,
      { allowedStatuses: [200] }
    );
    if (byHostRes.data?.site?.status !== "PUBLISHED") {
      throw new Error(
        `GET /api/v1/sites/by-host?host=${activeHost} returned unexpected site status ${byHostRes.data?.site?.status ?? "missing"}`
      );
    }
    const primaryHost = String(byHostRes.data?.site?.primary_host || activeHost)
      .trim()
      .toLowerCase();
    return { created, domain, primaryHost };
  }

  if (desiredStatus === "PENDING") {
    if (statusOf(domain) !== "PENDING") {
      throw new Error(
        `Custom domain ${targetHost} expected status PENDING, got ${domain?.status ?? "unknown"}`
      );
    }
  }

  return { created, domain, primaryHost: null };
}

/**
 * Upgrades the 4 showcase site specs to Declarative Theme JSON v2 using fetched
 * platform templates, inheriting each template's `layout`, `pages`, and theme
 * `radius`/`density` while preserving each shop's exact `theme` colors,
 * `font_family`, `logo_url`, `banner_url`, `brand_name`, `tagline`, and `sections`.
 */
function applyTemplatesToShowcaseSpecs(specs, templatesByKey) {
  if (!templatesByKey || templatesByKey.size === 0) return 0;
  let applied = 0;

  for (const spec of specs) {
    const desiredSlug = SHOWCASE_SITE_TEMPLATES[spec.mockShopId];
    if (!desiredSlug) continue;
    const tpl = templatesByKey.get(desiredSlug.toLowerCase());
    const tplConfig = parseMaybeJson(tpl?.config);
    if (!tpl || !tplConfig || tplConfig.schema_version !== 2 || !tplConfig.layout || !tplConfig.pages) {
      continue;
    }

    const templateId = String(tpl.id || tpl.slug || tplConfig.template_id || desiredSlug).trim();
    const v2Theme = {
      ...spec.config.theme,
      ...(tplConfig.theme?.radius ? { radius: tplConfig.theme.radius } : {}),
      ...(tplConfig.theme?.density ? { density: tplConfig.theme.density } : {}),
    };

    spec.templateId = templateId;
    spec.archetype = String(tplConfig.layout.archetype || tpl.archetype || "").trim();
    spec.config = {
      schema_version: 2,
      template_id: templateId,
      theme: v2Theme,
      layout: structuredClone(tplConfig.layout),
      sections: spec.config.sections,
      pages: structuredClone(tplConfig.pages),
    };
    applied += 1;
  }

  return applied;
}

/**
 * Seeds Chat auto-reply configs and realistic 3-message Vietnamese threads on
 * `shop-bach-dang` and `shop-sai-gon` for the demo buyer (Swarm B).
 * Gracefully skips when the Chat service/routes are unavailable (404/502/503).
 */
async function seedChatSampleData({ buyer, seededShops, seededProducts }) {
  const probe = await gwRequest("GET", "/api/v1/chat/conversations?page=1&page_size=1", {
    token: buyer.accessToken,
    allowedStatuses: [200, 404, 502, 503],
  });
  if (probe.status !== 200) {
    return { skipped: true, status: probe.status };
  }

  const result = {
    skipped: false,
    autoRepliesConfigured: 0,
    autoRepliesReused: 0,
    conversationsCreated: 0,
    conversationsReused: 0,
    messagesSent: 0,
    threadsReused: 0,
    shops: {},
  };

  const chatSeeds = [
    {
      mockShopId: "shop-bach-dang",
      subSlug: "bach-dang",
      mockProductId: "prod-at-trang",
      autoReply: {
        enabled: true,
        greeting_message:
          "Xin chào bạn đã ghé Xưởng Dệt Bạch Đằng! Xưởng may trực tiếp tại Hải Phòng sẽ hỗ trợ bạn ngay trong ít phút.",
        offline_enabled: true,
        offline_message:
          "Hiện đang ngoài giờ làm việc (8:00–22:00). Bạn để lại chiều cao, cân nặng hoặc mẫu áo quan tâm, xưởng sẽ phản hồi đầu giờ sáng mai nhé!",
        work_start_hour: 8,
        work_end_hour: 22,
        timezone: "Asia/Ho_Chi_Minh",
        cooldown_minutes: 30,
        keyword_rules: [
          {
            keywords: ["size", "kích cỡ", "chọn cỡ", "cân nặng", "chiều cao"],
            reply:
              "Áo thun Cotton Compact 220gsm chuẩn phom Regular Fit: size M (55–64kg), size L (65–74kg), size XL (75–84kg). Nếu thích mặc rộng vai bạn tăng thêm 1 size nhé!",
          },
          {
            keywords: ["đổi trả", "đổi size", "bảo hành", "hoàn tiền"],
            reply:
              "Xưởng hỗ trợ đổi size miễn phí hai chiều tận nhà trong 45 ngày khi sản phẩm còn nguyên tem mác và chưa qua giặt tẩy.",
          },
          {
            keywords: ["giao hàng", "ship", "vận chuyển", "bao lâu"],
            reply:
              "Đơn hàng xuất từ kho Hải Phòng trong ngày, giao tiêu chuẩn liên tỉnh toàn quốc 1–3 ngày và hỗ trợ đồng kiểm khi nhận hàng.",
          },
        ],
      },
      buyerQuestion:
        "Chào xưởng, mình cao 1m72 nặng 68kg mặc áo thun Cotton Compact 220gsm size nào vừa vặn ạ? Vải có bị co rút sau khi giặt máy không?",
      sellerReply:
        "Chào bạn Quân! Với chiều cao 1m72 nặng 68kg bạn mặc size L là đứng vai gọn đẹp nhất ạ. Vải Cotton Compact 220gsm đã qua 3 chu kỳ xử lý chống co rút trước khi cắt rập nên độ co dưới 2%, bạn yên tâm giặt máy nhé.",
    },
    {
      mockShopId: "shop-sai-gon",
      subSlug: "sai-gon",
      mockProductId: "prod-at-dryfit",
      autoReply: {
        enabled: true,
        greeting_message:
          "Chào bạn đến với Kho Basic Sài Gòn! Cần tư vấn đồ chạy bộ Dry-Fit hay giao nhanh nội thành TP.HCM bạn cứ nhắn nhé.",
        offline_enabled: true,
        offline_message:
          "Kho Basic Sài Gòn làm việc từ 8:00–22:00 hằng ngày. Bạn để lại thông tin mẫu đồ tập và cỡ mong muốn, đội ngũ sẽ phản hồi sớm nhất!",
        work_start_hour: 8,
        work_end_hour: 22,
        timezone: "Asia/Ho_Chi_Minh",
        cooldown_minutes: 30,
        keyword_rules: [
          {
            keywords: ["size", "kích cỡ", "chọn cỡ", "cân nặng"],
            reply:
              "Đồ chạy bộ Dry-Fit 145gsm co giãn 4 chiều: size M (55–64kg), size L (65–74kg), size XL (75–85kg). Nhắn chiều cao và cân nặng để shop gợi ý cỡ chuẩn nhé!",
          },
          {
            keywords: ["đổi trả", "đổi size", "hoàn hàng"],
            reply:
              "Shop hỗ trợ đổi size tận nhà trong 45 ngày nếu mặc chưa vừa vai hoặc chiều dài ống quần, xử lý nhanh trong 24 giờ.",
          },
          {
            keywords: ["giao hàng", "ship", "vận chuyển", "hỏa tốc"],
            reply:
              "Đơn nội thành TP.HCM đặt trước 17:00 hỗ trợ giao nhanh 2–4 giờ từ kho Thảo Điền; giao liên tỉnh toàn quốc 2–3 ngày.",
          },
        ],
      },
      buyerQuestion:
        "Shop ơi áo thun thể thao Dry-Fit này chạy bộ buổi sáng trời nắng có nhanh khô không ạ? Giao ra Hà Nội mất khoảng mấy ngày?",
      sellerReply:
        "Chào bạn! Áo dùng sợi Micro-mesh 145gsm thoát ẩm rất nhanh, phơi gió khoảng 45 phút là khô ráo và đường may phẳng giảm ma sát khi chạy dài. Giao từ TP.HCM ra Hà Nội mất khoảng 2–3 ngày ạ.",
    },
  ];

  for (const cfg of chatSeeds) {
    const shopEntry = seededShops.get(cfg.mockShopId);
    const prodEntry = seededProducts.get(cfg.mockProductId);
    if (!shopEntry || !prodEntry) continue;

    // 1. Idempotently configure seller auto-reply if not already enabled.
    const arGet = await gwRequest("GET", "/api/v1/seller/chat/auto-reply", {
      token: shopEntry.sellerToken,
      allowedStatuses: [200],
    });
    const alreadyEnabled = Boolean(arGet.data?.config?.enabled);
    if (!alreadyEnabled) {
      await gwRequest("PUT", "/api/v1/seller/chat/auto-reply", {
        token: shopEntry.sellerToken,
        body: cfg.autoReply,
        allowedStatuses: [200],
      });
      result.autoRepliesConfigured += 1;
    } else {
      result.autoRepliesReused += 1;
    }

    // 2. Create or get demo buyer <-> shop conversation.
    const siteHost = `${cfg.subSlug}.${SITE_ROOT_DOMAIN}`;
    const convRes = await gwRequest("POST", "/api/v1/chat/conversations", {
      token: buyer.accessToken,
      body: {
        shop_id: shopEntry.shop.id,
        site_host: siteHost,
      },
      allowedStatuses: [200, 201],
    });
    const conv = convRes.data?.conversation;
    if (!conv?.id) {
      throw new Error(`POST /api/v1/chat/conversations for ${cfg.subSlug} returned no conversation`);
    }
    if (convRes.data?.created) {
      result.conversationsCreated += 1;
    } else {
      result.conversationsReused += 1;
    }

    // 3. Seed 3-message Vietnamese thread ONLY when conversation currently has 0 messages.
    const msgListRes = await gwRequest(
      "GET",
      `/api/v1/chat/conversations/${encodeURIComponent(conv.id)}/messages?limit=20`,
      {
        token: buyer.accessToken,
        allowedStatuses: [200],
      }
    );
    const existingItems = msgListRes.data?.items ?? [];
    let threadAction = "reused";
    if (existingItems.length === 0) {
      const prod = prodEntry.product;
      const messagesToSend = [
        {
          token: buyer.accessToken,
          body: {
            kind: "text",
            body: cfg.buyerQuestion,
            client_msg_id: `seed-chat-${cfg.subSlug}-1`,
          },
        },
        {
          token: buyer.accessToken,
          body: {
            kind: "product",
            ref_id: prod.id,
            body: prod.name,
            metadata: {
              product_id: prod.id,
              name: prod.name,
              price_vnd: Math.trunc(prod.min_price_vnd ?? prod.skus?.[0]?.price_vnd ?? 0),
              image_url: prod.images?.[0] ?? "",
            },
            client_msg_id: `seed-chat-${cfg.subSlug}-2`,
          },
        },
        {
          token: shopEntry.sellerToken,
          body: {
            kind: "text",
            body: cfg.sellerReply,
            client_msg_id: `seed-chat-${cfg.subSlug}-3`,
          },
        },
      ];
      for (const msg of messagesToSend) {
        await gwRequest(
          "POST",
          `/api/v1/chat/conversations/${encodeURIComponent(conv.id)}/messages`,
          {
            token: msg.token,
            body: msg.body,
            allowedStatuses: [200, 201],
          }
        );
        result.messagesSent += 1;
      }
      threadAction = "seeded";
    } else {
      result.threadsReused += 1;
    }

    result.shops[cfg.subSlug] = {
      conversation_id: conv.id,
      auto_reply: alreadyEnabled ? "reused" : "configured",
      thread: threadAction,
    };
  }

  return result;
}

/**
 * Signs and sends delivering + delivered carrier webhooks to transition a shipped
 * order from SHIPPING -> DELIVERED via NATS shipment.updated, then polls until DELIVERED.
 */
async function deliverOrderViaShippingWebhook(buyerToken, order) {
  const carrierCode = String(order.carrier_code || "ghn").trim().toLowerCase() || "ghn";
  const trackingCode = String(order.tracking_code || "").trim();
  if (!trackingCode) {
    throw new Error(`Order ${order.id} is missing tracking_code after seller ship`);
  }

  const nowMs = Date.now();
  const steps = [
    {
      eventId: `seed-ship-${order.id}-delivering`,
      rawStatus: "delivering",
      description: "Bưu tá đang giao hàng tới địa chỉ người nhận",
      atMs: nowMs - 2000,
    },
    {
      eventId: `seed-ship-${order.id}-delivered`,
      rawStatus: "delivered",
      description: "Giao hàng thành công cho người nhận",
      atMs: nowMs,
    },
  ];

  for (const step of steps) {
    const rawBody = JSON.stringify({
      event_id: step.eventId,
      tracking_code: trackingCode,
      order_id: order.id,
      raw_status: step.rawStatus,
      description: step.description,
      occurred_at: new Date(step.atMs).toISOString().replace(/\.\d{3}Z$/, "Z"),
    });
    const sig = createHmac("sha256", SHIPPING_WEBHOOK_SECRET).update(rawBody).digest("hex");
    const res = await fetch(`${GATEWAY_URL}/webhooks/shipping/${encodeURIComponent(carrierCode)}`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        accept: "application/json",
        "x-signature": sig,
        "x-timestamp": String(Math.floor(Date.now() / 1000)),
      },
      body: rawBody,
      signal: AbortSignal.timeout(HTTP_TIMEOUT_MS),
    });
    const text = await res.text();
    if (res.status !== 200) {
      throw new Error(
        `POST /webhooks/shipping/${carrierCode} (${step.rawStatus}) failed: HTTP ${res.status}: ${text.slice(0, 400)}`
      );
    }
  }

  const deadline = Date.now() + 15000;
  let latest = order;
  while (Date.now() < deadline) {
    const res = await gwRequest("GET", `/api/v1/orders/${encodeURIComponent(order.id)}`, {
      token: buyerToken,
      allowedStatuses: [200],
    });
    latest = res.data?.order ?? latest;
    const st = String(latest.status ?? "").toUpperCase();
    if (st !== "SHIPPING" && st !== "PREPARING" && st !== "CONFIRMED") {
      return latest;
    }
    await sleep(300);
  }
  throw new Error(
    `Timed out waiting for order ${order.id} to reach DELIVERED after shipping webhook (last status: ${latest.status})`
  );
}

/**
 * Places (idempotently via Idempotency-Key) a small COD STOREFRONT order and
 * advances it cleanly through CONFIRMED -> PREPARING -> SHIPPING -> DELIVERED.
 * Does NOT call confirm-received (which moves to terminal COMPLETED), so the
 * delivered order remains eligible for RequestReturn (DELIVERED -> RETURN_REQUESTED).
 */
async function ensureDeliveredDemoOrder({
  buyerToken,
  buyerAddressId,
  shopEntry,
  sku,
  idempotencyKey,
}) {
  const chkRes = await gwRequest("POST", "/api/v1/checkout", {
    token: buyerToken,
    headers: { "Idempotency-Key": idempotencyKey },
    body: {
      address_id: buyerAddressId,
      from_cart: false,
      items: [{ sku_id: sku.id, quantity: 1 }],
      payment_method: "COD",
      sales_channel: "STOREFRONT",
      storefront_shop_id: shopEntry.shop.id,
    },
    allowedStatuses: [200, 201],
  });
  const createdOrder = chkRes.data?.orders?.[0];
  if (!createdOrder?.id) {
    throw new Error(`POST /api/v1/checkout (${idempotencyKey}) returned no order`);
  }

  // Always fetch live state (idempotent replay snapshots reflect initial creation state).
  const liveRes = await gwRequest("GET", `/api/v1/orders/${encodeURIComponent(createdOrder.id)}`, {
    token: buyerToken,
    allowedStatuses: [200],
  });
  let order = liveRes.data?.order ?? createdOrder;

  if (String(order.status ?? "").toUpperCase() === "CONFIRMED") {
    const confRes = await gwRequest(
      "POST",
      `/api/v1/seller/orders/${encodeURIComponent(order.id)}/confirm`,
      {
        token: shopEntry.sellerToken,
        allowedStatuses: [200],
      }
    );
    order = confRes.data?.order ?? order;
  }

  if (["CONFIRMED", "PREPARING"].includes(String(order.status ?? "").toUpperCase())) {
    const shipRes = await gwRequest(
      "POST",
      `/api/v1/seller/orders/${encodeURIComponent(order.id)}/ship`,
      {
        token: shopEntry.sellerToken,
        body: {
          carrier_code: order.carrier_code || "ghn",
          service_code: order.service_code || "",
        },
        allowedStatuses: [200],
      }
    );
    order = shipRes.data?.order ?? order;
    if (shipRes.data?.tracking_code && !order.tracking_code) {
      order = { ...order, tracking_code: shipRes.data.tracking_code };
    }
  }

  if (String(order.status ?? "").toUpperCase() === "SHIPPING") {
    order = await deliverOrderViaShippingWebhook(buyerToken, order);
  }

  return order;
}

/**
 * Seeds 1 pending return request on `shop-bach-dang` and 1 open admin complaint
 * on `shop-sai-gon` for the demo buyer (Swarm D). Gracefully skips if return routes
 * are unavailable (404/502/503).
 */
async function seedReturnsAndComplaints({
  adminToken,
  buyer,
  buyerDefaultAddress,
  seededShops,
  seededProducts,
  skuById,
  summary,
}) {
  const bachDangShop = seededShops.get("shop-bach-dang");
  const saiGonShop = seededShops.get("shop-sai-gon");
  if (!bachDangShop || !saiGonShop || !buyerDefaultAddress?.id) {
    return { skipped: true, reason: "missing prerequisite shop or buyer default address" };
  }

  const probe = await gwRequest("GET", "/api/v1/seller/returns?page=1&page_size=50", {
    token: bachDangShop.sellerToken,
    allowedStatuses: [200, 404, 502, 503],
  });
  if (probe.status !== 200) {
    return { skipped: true, status: probe.status };
  }

  const ensureDemoOrderSku = async (mockProductId) => {
    const seededProd = seededProducts.get(mockProductId);
    if (!seededProd) {
      throw new Error(`Missing seeded product ${mockProductId} for demo return/complaint order`);
    }
    // Never touch the 6 intentional 0-stock SKUs.
    const mockSku = seededProd.mockProduct.skus.find(
      (s) => Math.trunc(s.stock) > 0 && seededProd.skuIdByCode.has(s.skuCode)
    );
    if (!mockSku) {
      throw new Error(`No positive-stock SKU found for ${mockProductId}`);
    }

    const prodRes = await gwRequest("GET", `/api/v1/products/${encodeURIComponent(seededProd.product.id)}`, {
      token: seededProd.shopEntry.sellerToken,
      allowedStatuses: [200],
    });
    let liveSku = (prodRes.data?.product?.skus ?? []).find((s) => s.sku_code === mockSku.skuCode);
    if (!liveSku) {
      throw new Error(`SKU ${mockSku.skuCode} disappeared on product ${seededProd.product.id}`);
    }

    const curStock = Math.trunc(liveSku.stock ?? 0);
    const curAvailable = Math.trunc(
      liveSku.available_stock ??
        Math.max(
          0,
          curStock - Math.trunc(liveSku.reserved_stock ?? 0) - Math.trunc(liveSku.flash_sale_reserved_stock ?? 0)
        )
    );
    if (curAvailable < 15) {
      const targetStock = curStock + (15 - curAvailable) + 10;
      liveSku = await topUpSkuStock(
        seededProd.shopEntry.sellerToken,
        seededProd.product.id,
        liveSku,
        targetStock
      );
      seededProd.skuByCode.set(mockSku.skuCode, liveSku);
      if (skuById.has(liveSku.id)) {
        skuById.get(liveSku.id).sku = liveSku;
      }
      summary.products.skusToppedUp += 1;
    }

    return { product: seededProd.product, sku: liveSku, shopEntry: seededProd.shopEntry };
  };

  const result = {
    skipped: false,
    pendingReturn: null,
    openComplaint: null,
  };

  // (a) 1 pending return request on bach-dang for demo buyer
  const DEMO_RETURN_REASON =
    "Áo mặc hơi chật vai so với bảng cỡ, mong xưởng hỗ trợ đổi sang size lớn hơn hoặc hoàn tiền.";
  const existingBachDangReturns = probe.data?.items ?? [];
  const existingPendingReturn = existingBachDangReturns.find(
    (r) =>
      r.buyer_id === buyer.user.id &&
      (String(r.status ?? "").toUpperCase() === "PENDING" ||
        String(r.reason ?? "").includes("Áo mặc hơi chật vai"))
  );

  if (existingPendingReturn) {
    result.pendingReturn = {
      action: "reused",
      id: existingPendingReturn.id,
      order_id: existingPendingReturn.order_id,
      status: existingPendingReturn.status,
    };
  } else {
    const { sku } = await ensureDemoOrderSku("prod-at-trang");
    const order = await ensureDeliveredDemoOrder({
      buyerToken: buyer.accessToken,
      buyerAddressId: buyerDefaultAddress.id,
      shopEntry: bachDangShop,
      sku,
      idempotencyKey: "seed-demo-return-order-v1",
    });
    const orderStatus = String(order.status ?? "").toUpperCase();
    if (orderStatus === "DELIVERED") {
      const orderItem = order.items?.[0];
      const retRes = await gwRequest("POST", `/api/v1/orders/${encodeURIComponent(order.id)}/returns`, {
        token: buyer.accessToken,
        body: {
          items: [
            {
              ...(orderItem?.id ? { order_item_id: orderItem.id } : {}),
              sku_id: sku.id,
              quantity: 1,
            },
          ],
          reason: DEMO_RETURN_REASON,
          photos: ["/products/ao-thun-cotton-trang.png"],
        },
        allowedStatuses: [200, 201],
      });
      const rr = retRes.data?.return_request;
      result.pendingReturn = {
        action: "created",
        id: rr?.id || retRes.data?.return_request_id || "",
        order_id: order.id,
        status: rr?.status || "PENDING",
      };
    } else {
      const listRes = await gwRequest("GET", `/api/v1/orders/${encodeURIComponent(order.id)}/returns`, {
        token: buyer.accessToken,
        allowedStatuses: [200],
      });
      const existing = (listRes.data?.items ?? [])[0];
      result.pendingReturn = {
        action: "reused",
        id: existing?.id || "",
        order_id: order.id,
        status: existing?.status || orderStatus,
      };
    }
  }

  // (b) 1 open admin complaint on sai-gon for demo buyer
  const openComplaintsRes = await gwRequest(
    "GET",
    "/api/v1/admin/complaints?status=OPEN&page=1&page_size=50",
    {
      token: adminToken,
      allowedStatuses: [200],
    }
  );
  const existingOpenComplaint = (openComplaintsRes.data?.items ?? []).find(
    (c) =>
      c.buyer_id === buyer.user.id &&
      c.shop_id === saiGonShop.shop.id &&
      String(c.status ?? "").toUpperCase() === "OPEN"
  );

  if (existingOpenComplaint) {
    result.openComplaint = {
      action: "reused",
      id: existingOpenComplaint.id,
      order_id: existingOpenComplaint.order_id,
      status: existingOpenComplaint.status,
    };
  } else {
    const { product, sku } = await ensureDemoOrderSku("prod-at-dryfit");
    const photoUrl = product.images?.[0] || "/products/ao-thun-cotton-trang.png";
    const order = await ensureDeliveredDemoOrder({
      buyerToken: buyer.accessToken,
      buyerAddressId: buyerDefaultAddress.id,
      shopEntry: saiGonShop,
      sku,
      idempotencyKey: "seed-demo-complaint-order-v1",
    });

    let orderStatus = String(order.status ?? "").toUpperCase();
    let returnReq = null;

    if (orderStatus === "DELIVERED") {
      const orderItem = order.items?.[0];
      const retRes = await gwRequest("POST", `/api/v1/orders/${encodeURIComponent(order.id)}/returns`, {
        token: buyer.accessToken,
        body: {
          items: [
            {
              ...(orderItem?.id ? { order_item_id: orderItem.id } : {}),
              sku_id: sku.id,
              quantity: 1,
            },
          ],
          reason:
            "Đường may ở gấu áo bị bung chỉ nhẹ khi mở hộp đồng kiểm, đề nghị gian hàng hỗ trợ đổi trả.",
          photos: [photoUrl],
        },
        allowedStatuses: [200, 201],
      });
      returnReq = retRes.data?.return_request ?? {
        id: retRes.data?.return_request_id,
        status: "PENDING",
      };
      orderStatus = String(retRes.data?.order?.status ?? "RETURN_REQUESTED").toUpperCase();
    } else {
      const retList = await gwRequest("GET", `/api/v1/orders/${encodeURIComponent(order.id)}/returns`, {
        token: buyer.accessToken,
        allowedStatuses: [200],
      });
      returnReq = (retList.data?.items ?? [])[0] ?? null;
    }

    let complaint = null;
    let complaintAction = "reused";

    if (returnReq?.id && String(returnReq.status ?? "").toUpperCase() === "PENDING") {
      const rejRes = await gwRequest(
        "POST",
        `/api/v1/seller/returns/${encodeURIComponent(returnReq.id)}/reject`,
        {
          token: saiGonShop.sellerToken,
          body: {
            reason:
              "Kho kiểm tra ảnh đóng gói trước khi xuất hàng không ghi nhận lỗi chỉ may; sản phẩm đã có dấu hiệu tháo tem.",
          },
          allowedStatuses: [200],
        }
      );
      returnReq = rejRes.data?.return_request ?? returnReq;
      complaint = rejRes.data?.complaint ?? null;
      if (complaint?.id) {
        complaintAction = "created";
      }
    }

    if (!complaint?.id) {
      const byOrderRes = await gwRequest(
        "GET",
        `/api/v1/admin/complaints?order_id=${encodeURIComponent(order.id)}&page=1&page_size=20`,
        {
          token: adminToken,
          allowedStatuses: [200],
        }
      );
      complaint = (byOrderRes.data?.items ?? [])[0] ?? null;
    }

    if (
      !complaint?.id &&
      returnReq?.id &&
      (String(returnReq.status ?? "").toUpperCase() === "REJECTED" || orderStatus === "RETURN_REJECTED")
    ) {
      const cpRes = await gwRequest("POST", `/api/v1/orders/${encodeURIComponent(order.id)}/complaints`, {
        token: buyer.accessToken,
        body: {
          return_request_id: returnReq.id,
          reason:
            "Khiếu nại quyết định từ chối đổi trả của gian hàng, đề nghị quản trị viên xem xét hình ảnh kiểm hàng.",
          evidence_urls: [photoUrl],
        },
        allowedStatuses: [200, 201],
      });
      complaint = cpRes.data?.complaint ?? null;
      if (complaint?.id) {
        complaintAction = "created";
      }
    }

    result.openComplaint = {
      action: complaintAction,
      id: complaint?.id || "",
      order_id: order.id,
      status: complaint?.status || orderStatus,
    };
  }

  return result;
}

function resolveGoModCache() {
  if (process.env.GOMODCACHE?.trim()) return process.env.GOMODCACHE.trim();
  const res = spawnSync("go", ["env", "GOMODCACHE"], { encoding: "utf8" });
  if (res.status === 0 && res.stdout?.trim()) {
    return res.stdout.trim();
  }
  return resolve(homedir(), "go/pkg/mod");
}

function hasDockerCompose() {
  const res = spawnSync("docker", ["compose", "version"], { stdio: "ignore" });
  return res.status === 0;
}

/**
 * Ensures at least `targetCount` approved products exist in the catalog by
 * invoking Swarm A's `tools/catalog-gen` in gRPC mode targeting the dedicated
 * 5th seller (`seller-catalog@nexusmart.local`, NO published site).
 */
async function seedLargeCatalog({ catalogSeller, targetCount }) {
  const beforeRes = await gwRequest("GET", "/api/v1/products?page=1&page_size=1", {
    allowedStatuses: [200],
  });
  const existingTotal = Math.trunc(beforeRes.data?.total ?? 0);

  if (targetCount <= 0) {
    console.log(`  [catalog] disabled (SEED_CATALOG_COUNT=0; current approved products: ${existingTotal})`);
    return {
      action: "disabled",
      target: 0,
      existing: existingTotal,
      generated: 0,
      total_after: existingTotal,
    };
  }

  if (existingTotal >= targetCount) {
    console.log(
      `  [catalog] reused (${existingTotal} >= ${targetCount} approved products; skipping catalog-gen)`
    );
    return {
      action: "reused",
      target: targetCount,
      existing: existingTotal,
      generated: 0,
      total_after: existingTotal,
    };
  }

  const remaining = targetCount - existingTotal;
  const catalogSeed = existingTotal > 18 ? 20261121 + existingTotal : 20261121;
  const explicitHostGRPC = (process.env.PRODUCT_GRPC_ADDR ?? "").trim();
  const useDockerCompose = !explicitHostGRPC && hasDockerCompose();

  const commonFlags = [
    "--mode=grpc",
    `--seller-emails=${catalogSeller.email}`,
    `--seller-password=${catalogSeller.password}`,
    `--admin-email=${ADMIN_CREDS.email}`,
    `--admin-password=${ADMIN_CREDS.password}`,
    `--count=${remaining}`,
    "--concurrency=24",
    "--approve=true",
    `--seed=${catalogSeed}`,
  ];

  let proc;
  let runnerMode;
  if (useDockerCompose) {
    runnerMode = "docker-compose-e2e";
    const uid = typeof process.getuid === "function" ? process.getuid() : 1000;
    const gid = typeof process.getgid === "function" ? process.getgid() : 1000;
    const goModCache = resolveGoModCache();
    const composeGateway = env("SEED_CATALOG_GATEWAY_URL", "http://gateway:8080");
    const composeGRPC = env("SEED_CATALOG_GRPC_ADDR", "product:50052");

    console.log(
      `==> Seeding ${remaining} catalog products via docker compose e2e (seller=${catalogSeller.email}, grpc=${composeGRPC})`
    );
    proc = spawnSync(
      "docker",
      [
        "compose",
        "--profile",
        "e2e",
        "run",
        "--rm",
        "-T",
        "--no-deps",
        "--user",
        `${uid}:${gid}`,
        "-w",
        "/src/tools/catalog-gen",
        "-e",
        "CGO_ENABLED=0",
        "-e",
        "HOME=/tmp",
        "-e",
        "GOCACHE=/tmp/go-build",
        "e2e",
        "go",
        "run",
        "./cmd/catalog-gen",
        `--product-grpc-addr=${composeGRPC}`,
        `--gateway-url=${composeGateway}`,
        ...commonFlags,
      ],
      {
        cwd: REPO_ROOT,
        env: { ...process.env, GOMODCACHE: goModCache },
        encoding: "utf8",
        stdio: ["ignore", "pipe", "inherit"],
        maxBuffer: 16 * 1024 * 1024,
      }
    );
  } else {
    runnerMode = "direct-go-run";
    const directGRPC = explicitHostGRPC || "localhost:50052";
    console.log(
      `==> Seeding ${remaining} catalog products via direct go run (seller=${catalogSeller.email}, grpc=${directGRPC})`
    );
    proc = spawnSync(
      "go",
      [
        "run",
        "./cmd/catalog-gen",
        `--product-grpc-addr=${directGRPC}`,
        `--gateway-url=${GATEWAY_URL}`,
        ...commonFlags,
      ],
      {
        cwd: resolve(REPO_ROOT, "tools/catalog-gen"),
        env: process.env,
        encoding: "utf8",
        stdio: ["ignore", "pipe", "inherit"],
        maxBuffer: 16 * 1024 * 1024,
      }
    );
  }

  if (proc.error) {
    throw new Error(`Failed to invoke catalog-gen (${runnerMode}): ${proc.error.message}`);
  }
  if (proc.status !== 0) {
    throw new Error(
      `catalog-gen (${runnerMode}) exited with code ${proc.status}: ${(proc.stdout || "").slice(-500)}`
    );
  }

  const parsedSummary = parseMaybeJson(proc.stdout);
  const afterRes = await gwRequest("GET", "/api/v1/products?page=1&page_size=1", {
    allowedStatuses: [200],
  });
  const totalAfter = Math.trunc(afterRes.data?.total ?? existingTotal + remaining);

  return {
    action: "generated",
    runner: runnerMode,
    target: targetCount,
    existing: existingTotal,
    requested: remaining,
    created: parsedSummary?.created ?? remaining,
    approved: parsedSummary?.approved ?? remaining,
    failed: parsedSummary?.failed ?? 0,
    rate_per_sec: parsedSummary?.rate_per_sec ?? null,
    elapsed_ms: parsedSummary?.elapsed_ms ?? null,
    total_after: totalAfter,
  };
}

async function main() {
  console.log(`==> Seeding NexusMart backend via ${GATEWAY_URL} (Mailpit: ${MAILPIT_URL})`);

  // 0. Health check + admin login + location lookup
  await gwRequest("GET", "/healthz", { allowedStatuses: [200] });
  const adminSession = await login(ADMIN_CREDS.email, ADMIN_CREDS.password);
  const adminToken = adminSession.access_token;
  const { resolveProvinceAndWard } = await loadLocationHelper();

  const nowMs = Date.now();
  const windowSpecs = buildFlashWindowTimes(nowMs);
  const flashDemandBySkuCode = buildFlashDemandBySkuCode(PRODUCTS, FLASH_SALE_SLOTS, windowSpecs);

  const summary = {
    users: { created: 0, reused: 0 },
    shops: { created: 0, reused: 0, map: {} },
    categories: { created: 0, reused: 0, map: {} },
    products: { created: 0, reused: 0, totalSkus: 0, skusToppedUp: 0 },
    vouchers: { created: 0, reused: 0 },
    flashSales: {
      windowsCreated: 0,
      windowsReused: 0,
      windowsRetired: 0,
      itemsCreated: 0,
      itemsReused: 0,
    },
    templates: { skipped: true, available: 0, applied: 0 },
    sites: { created: 0, reused: 0, map: {} },
    chat: { skipped: true },
    returnsAndComplaints: { skipped: true },
    catalog: { action: "pending", target: SEED_CATALOG_COUNT },
  };

  // 1. Demo buyer (+ default address + welcome coins)
  const buyer = await ensureVerifiedUser(adminToken, DEMO_BUYER);
  summary.users[buyer.created ? "created" : "reused"] += 1;
  const { address: buyerDefaultAddress } = await ensureDefaultAddress(
    buyer.accessToken,
    {
      recipientName: DEMO_BUYER.fullName,
      recipientPhone: DEMO_BUYER.phone,
      city: "Thành phố Hà Nội",
      streetAddress: "Số 24 phố Lý Thường Kiệt, Phường Hoàn Kiếm, Thành phố Hà Nội",
    },
    resolveProvinceAndWard
  );
  const coinRes = await gwRequest("POST", "/api/v1/admin/coins/credit", {
    token: adminToken,
    body: {
      user_id: buyer.user.id,
      amount: INITIAL_USER_COINS,
      reason: "seed_demo_welcome_coins",
      reference_id: "seed:demo-buyer:welcome-coins-v1",
    },
    allowedStatuses: [200, 201],
  });

  // 2. 4 Demo sellers + 4 ACTIVE shops matching SHOPS
  const existingShops = await listAllAdminShops(adminToken);
  const shopByNormName = new Map(existingShops.map((s) => [s.name.trim().toLowerCase(), s]));
  const seededShops = new Map(); // mockShopId -> { shop, sellerToken, sellerUser, sellerCreds }

  for (const mockShop of SHOPS) {
    const sellerCreds = SHOP_SELLERS[mockShop.id];
    if (!sellerCreds) throw new Error(`Missing seller credentials mapping for shop ${mockShop.id}`);

    let sellerSession = await ensureVerifiedUser(adminToken, sellerCreds);
    summary.users[sellerSession.created ? "created" : "reused"] += 1;

    const { address: pickupAddr } = await ensureDefaultAddress(
      sellerSession.accessToken,
      {
        recipientName: sellerCreds.fullName,
        recipientPhone: sellerCreds.phone,
        city: mockShop.city,
        streetAddress: mockShop.warehouseAddress,
      },
      resolveProvinceAndWard
    );

    let backendShop = shopByNormName.get(mockShop.name.trim().toLowerCase());
    if (!backendShop) {
      const regRes = await gwRequest("POST", "/api/v1/shops/register", {
        token: sellerSession.accessToken,
        body: {
          name: mockShop.name,
          description: mockShop.story,
          pickup_address_id: pickupAddr.id,
          contact_phone: sellerCreds.phone,
          business_license_doc: `https://sandbox.nexusmart.local/licenses/${mockShop.slug}.pdf`,
        },
        allowedStatuses: [200, 201],
      });
      backendShop = regRes.data.shop;
      shopByNormName.set(mockShop.name.trim().toLowerCase(), backendShop);
      summary.shops.created += 1;
    } else {
      summary.shops.reused += 1;
    }

    if (backendShop.status !== "ACTIVE") {
      const revRes = await gwRequest("POST", `/api/v1/admin/shops/${encodeURIComponent(backendShop.id)}/review`, {
        token: adminToken,
        body: { decision: "APPROVE", reason: "Seed storefront workshop approval" },
        allowedStatuses: [200],
      });
      backendShop = revRes.data.shop;
    }

    // Re-login if JWT does not yet carry seller role + shop_id
    if (sellerSession.user.role !== "seller" || sellerSession.user.shop_id !== backendShop.id) {
      const fresh = await login(sellerCreds.email, sellerCreds.password);
      sellerSession = { ...sellerSession, accessToken: fresh.access_token, user: fresh.user };
    }
    if (sellerSession.user.shop_id !== backendShop.id) {
      throw new Error(
        `Seller ${sellerCreds.email} has shop_id=${sellerSession.user.shop_id}, expected ${backendShop.id} (${mockShop.name})`
      );
    }

    seededShops.set(mockShop.id, {
      shop: backendShop,
      sellerToken: sellerSession.accessToken,
      sellerUser: sellerSession.user,
      sellerCreds,
    });
    summary.shops.map[mockShop.id] = { id: backendShop.id, name: backendShop.name };
  }

  // 2b. Dedicated 5th demo seller & ACTIVE catalog shop (NO published site so GET /api/v1/sites
  // stays clean with the 4 showcase sites and showcase shops keep their curated 18 products).
  let catalogSellerSession = await ensureVerifiedUser(adminToken, CATALOG_SELLER);
  summary.users[catalogSellerSession.created ? "created" : "reused"] += 1;
  const { address: catalogPickupAddr } = await ensureDefaultAddress(
    catalogSellerSession.accessToken,
    {
      recipientName: CATALOG_SELLER.fullName,
      recipientPhone: CATALOG_SELLER.phone,
      city: CATALOG_SHOP_SPEC.city,
      streetAddress: CATALOG_SHOP_SPEC.warehouseAddress,
    },
    resolveProvinceAndWard
  );
  let catalogShop = shopByNormName.get(CATALOG_SHOP_SPEC.name.trim().toLowerCase());
  if (!catalogShop) {
    const regRes = await gwRequest("POST", "/api/v1/shops/register", {
      token: catalogSellerSession.accessToken,
      body: {
        name: CATALOG_SHOP_SPEC.name,
        description: CATALOG_SHOP_SPEC.description,
        pickup_address_id: catalogPickupAddr.id,
        contact_phone: CATALOG_SELLER.phone,
        business_license_doc: `https://sandbox.nexusmart.local/licenses/${CATALOG_SHOP_SPEC.slug}.pdf`,
      },
      allowedStatuses: [200, 201],
    });
    catalogShop = regRes.data.shop;
    shopByNormName.set(CATALOG_SHOP_SPEC.name.trim().toLowerCase(), catalogShop);
    summary.shops.created += 1;
  } else {
    summary.shops.reused += 1;
  }
  if (catalogShop.status !== "ACTIVE") {
    const revRes = await gwRequest(
      "POST",
      `/api/v1/admin/shops/${encodeURIComponent(catalogShop.id)}/review`,
      {
        token: adminToken,
        body: { decision: "APPROVE", reason: "Seed catalog warehouse approval" },
        allowedStatuses: [200],
      }
    );
    catalogShop = revRes.data.shop;
  }
  if (
    catalogSellerSession.user.role !== "seller" ||
    catalogSellerSession.user.shop_id !== catalogShop.id
  ) {
    const fresh = await login(CATALOG_SELLER.email, CATALOG_SELLER.password);
    catalogSellerSession = {
      ...catalogSellerSession,
      accessToken: fresh.access_token,
      user: fresh.user,
    };
  }
  if (catalogSellerSession.user.shop_id !== catalogShop.id) {
    throw new Error(
      `Catalog seller ${CATALOG_SELLER.email} has shop_id=${catalogSellerSession.user.shop_id}, expected ${catalogShop.id}`
    );
  }
  summary.shops.map["shop-catalog"] = { id: catalogShop.id, name: catalogShop.name };

  // 3. 6 Categories matching CATEGORIES
  const catListRes = await gwRequest("GET", "/api/v1/categories", { allowedStatuses: [200] });
  const catBySlug = new Map((catListRes.data?.categories ?? []).map((c) => [c.slug, c]));

  for (let i = 0; i < CATEGORIES.length; i++) {
    const cat = CATEGORIES[i];
    let backendCat = catBySlug.get(cat.slug);
    if (!backendCat) {
      const res = await gwRequest("POST", "/api/v1/admin/categories", {
        token: adminToken,
        body: {
          name: cat.name,
          slug: cat.slug,
          commission_rate_bps: 500,
          sort_order: i + 1,
        },
        allowedStatuses: [200, 201],
      });
      backendCat = res.data.category;
      catBySlug.set(cat.slug, backendCat);
      summary.categories.created += 1;
    } else {
      summary.categories.reused += 1;
    }
    summary.categories.map[cat.slug] = backendCat.id;
  }

  // 4. 18 Products + all SKUs matching PRODUCTS
  const existingProductsByShop = new Map();
  for (const [mockShopId, entry] of seededShops.entries()) {
    const prods = await listAllSellerProducts(entry.sellerToken);
    const bySkuCode = new Map();
    const byName = new Map();
    for (const p of prods) {
      byName.set(p.name.trim().toLowerCase(), p);
      for (const s of p.skus ?? []) {
        bySkuCode.set(s.sku_code, p);
      }
    }
    existingProductsByShop.set(mockShopId, { bySkuCode, byName });
  }

  const seededProducts = new Map(); // mockProductId -> { product, mockProduct, shopEntry, skuIdByCode, skuByCode }
  const skuCatalogByCode = new Map(); // skuCode -> { mockSku, productId, shopEntry, skuByCode }
  const skuById = new Map(); // skuId -> { sku, productId, skuCode, shopEntry, skuByCode }

  for (const prod of PRODUCTS) {
    const shopEntry = seededShops.get(prod.shopId);
    const cat = catBySlug.get(prod.categorySlug);
    if (!shopEntry) throw new Error(`Missing seeded shop for product ${prod.id} (${prod.shopId})`);
    if (!cat) throw new Error(`Missing seeded category for product ${prod.id} (${prod.categorySlug})`);

    const shopIndex = existingProductsByShop.get(prod.shopId);
    let backendProd =
      prod.skus.map((s) => shopIndex.bySkuCode.get(s.skuCode)).find(Boolean) ??
      shopIndex.byName.get(prod.name.trim().toLowerCase());

    if (!backendProd) {
      const colorByCode = new Map(prod.colors.map((c) => [c.code, c]));
      const images = [...new Set([prod.image, prod.onModelImage, ...(prod.gallery ?? [])].filter(Boolean))];
      const weightGrams = CATEGORY_WEIGHT_GRAMS[prod.categorySlug] ?? 250;

      const skusPayload = prod.skus.map((sku) => {
        const color = colorByCode.get(sku.colorCode);
        const totalFlashDemand = flashDemandBySkuCode.get(sku.skuCode) ?? 0;
        return {
          sku_code: sku.skuCode,
          name: `${color?.name ?? sku.colorCode} / ${sku.size}`,
          attributes: {
            color: sku.colorCode,
            color_name: color?.name ?? sku.colorCode,
            color_hex: color?.hex ?? "#9aa5ac",
            size: sku.size,
          },
          price_vnd: Math.trunc(sku.price),
          stock: computeDesiredSkuStock(sku, totalFlashDemand),
          weight_grams: weightGrams,
          image_url: color?.image ?? prod.image,
        };
      });

      const createRes = await gwRequest("POST", "/api/v1/seller/products", {
        token: shopEntry.sellerToken,
        body: {
          category_id: cat.id,
          name: prod.name,
          description: prod.description,
          images,
          skus: skusPayload,
        },
        allowedStatuses: [200, 201],
      });
      backendProd = createRes.data.product;
      summary.products.created += 1;
    } else {
      summary.products.reused += 1;
    }

    if (backendProd.status !== "APPROVED") {
      const revRes = await gwRequest("POST", `/api/v1/admin/products/${encodeURIComponent(backendProd.id)}/review`, {
        token: adminToken,
        body: { decision: "APPROVE", reason: "Seed storefront product approval" },
        allowedStatuses: [200],
      });
      backendProd = revRes.data.product;
    }

    const skuIdByCode = new Map((backendProd.skus ?? []).map((s) => [s.sku_code, s.id]));
    const skuByCode = new Map((backendProd.skus ?? []).map((s) => [s.sku_code, s]));
    summary.products.totalSkus += backendProd.skus?.length ?? 0;
    seededProducts.set(prod.id, { product: backendProd, mockProduct: prod, shopEntry, skuIdByCode, skuByCode });
    for (const mockSku of prod.skus) {
      const backendSku = skuByCode.get(mockSku.skuCode);
      if (backendSku) {
        skuCatalogByCode.set(mockSku.skuCode, {
          mockSku,
          productId: backendProd.id,
          shopEntry,
          skuByCode,
        });
        skuById.set(backendSku.id, {
          sku: backendSku,
          productId: backendProd.id,
          skuCode: mockSku.skuCode,
          shopEntry,
          skuByCode,
        });
      }
    }
  }

  // 5. 11 Vouchers matching VOUCHERS (every demo shop must own at least one SHOP voucher)
  const shopsWithoutVoucher = SHOPS.filter(
    (s) => !VOUCHERS.some((v) => v.scope === "shop" && v.shopId === s.id)
  ).map((s) => s.id);
  if (shopsWithoutVoucher.length > 0) {
    throw new Error(`Missing SHOP voucher in web/src/data/vouchers.ts for: ${shopsWithoutVoucher.join(", ")}`);
  }
  const vListRes = await gwRequest("GET", "/api/v1/vouchers", {
    token: adminToken,
    allowedStatuses: [200],
  });
  const existingVoucherByCode = new Map(
    (vListRes.data?.vouchers ?? []).map((v) => [String(v.code).toUpperCase(), v])
  );

  const voucherStartsAt = new Date(nowMs - 3600 * 1000).toISOString();
  const minVoucherEndMs = nowMs + 90 * 24 * 3600 * 1000;

  for (const v of VOUCHERS) {
    const code = v.code.toUpperCase();
    if (existingVoucherByCode.has(code)) {
      summary.vouchers.reused += 1;
      continue;
    }

    const parsedExp = Date.parse(v.expiresAt);
    const endsAt = new Date(Math.max(Number.isNaN(parsedExp) ? 0 : parsedExp, minVoucherEndMs)).toISOString();
    const isPercent = v.discountType === "percent";
    const payload = {
      code,
      name: v.title,
      scope: v.scope.toUpperCase(),
      discount_type: isPercent ? "PERCENT" : "FIXED",
      value_vnd: isPercent ? 0 : Math.trunc(v.discountValue),
      discount_percent: isPercent ? Math.trunc(v.discountValue) : 0,
      max_discount_vnd: isPercent ? Math.trunc(v.maxDiscount ?? 0) : Math.trunc(v.discountValue),
      min_order_vnd: Math.trunc(v.minOrderValue),
      quota: Math.trunc(v.totalUses),
      per_user_limit: 5,
      budget_vnd: 0,
      starts_at: voucherStartsAt,
      ends_at: endsAt,
    };

    let endpoint = "/api/v1/admin/vouchers";
    let token = adminToken;
    if (v.scope === "shop") {
      const shopEntry = seededShops.get(v.shopId);
      if (!shopEntry) throw new Error(`Missing seeded shop for voucher ${code} (${v.shopId})`);
      endpoint = "/api/v1/seller/vouchers";
      token = shopEntry.sellerToken;
    }

    const res = await gwRequest("POST", endpoint, {
      token,
      body: payload,
      allowedStatuses: [200, 201, 409],
    });
    if (res.status === 409) {
      summary.vouchers.reused += 1;
    } else {
      existingVoucherByCode.set(code, res.data.voucher);
      summary.vouchers.created += 1;
    }
  }

  // 6. Flash sales (slot-12 live, slot-18 & slot-21 upcoming)
  // Note: slot-09 is "ended" in mock data; promotion service rejects ended windows
  // and ListActiveFlashSales only returns windows with ends_at > NOW().
  const fsListRes = await gwRequest("GET", "/api/v1/flash-sales?include_upcoming=true", {
    allowedStatuses: [200],
  });
  const allActiveFlashSales = fsListRes.data?.flash_sales ?? [];
  const seedWindowSpecByName = new Map(Object.values(windowSpecs).map((w) => [w.name, w]));

  // PLT-01 fix: detect stale pre-Phase-2b flash-sale windows whose APPROVED items
  // with remaining_slots > 0 point to SKUs with flash_sale_reserved_stock === 0
  // (missing product_db.flash_sale_stock_reservations). Retire them cleanly via
  // POST /api/v1/admin/flash-sales/{id}/end before topping up SKU stock and re-seeding.
  const windowsToRetire = new Map(); // windowId -> { fs, reason }
  for (const fs of allActiveFlashSales) {
    if (!seedWindowSpecByName.has(fs.name)) continue;
    const staleItem = (fs.items ?? []).find((it) => {
      if (String(it.status ?? "").toUpperCase() !== "APPROVED") return false;
      if (Math.trunc(it.remaining_slots ?? 0) <= 0) return false;
      const skuEntry = skuById.get(it.sku_id);
      if (!skuEntry) return false;
      return Math.trunc(skuEntry.sku.flash_sale_reserved_stock ?? 0) <= 0;
    });
    if (staleItem) {
      windowsToRetire.set(fs.id, {
        fs,
        reason: `approved item ${staleItem.id} (remaining=${staleItem.remaining_slots}) has SKU ${staleItem.sku_id} with flash_sale_reserved_stock=0`,
      });
    }
  }

  // If retiring any stale window, also retire any remaining seed window whose time range
  // overlaps the freshly computed window range so re-registration never hits 409 overlap.
  if (windowsToRetire.size > 0) {
    const recreatingRanges = [...windowsToRetire.values()]
      .map(({ fs }) => seedWindowSpecByName.get(fs.name))
      .filter(Boolean)
      .map((w) => ({ startMs: Date.parse(w.startsAt), endMs: Date.parse(w.endsAt) }));

    for (const fs of allActiveFlashSales) {
      if (windowsToRetire.has(fs.id) || !seedWindowSpecByName.has(fs.name)) continue;
      const fsStart = Date.parse(fs.starts_at || "");
      const fsEnd = Date.parse(fs.ends_at || "");
      if (Number.isNaN(fsStart) || Number.isNaN(fsEnd)) continue;
      if (recreatingRanges.some((r) => fsStart < r.endMs && fsEnd > r.startMs)) {
        windowsToRetire.set(fs.id, { fs, reason: "overlaps re-created flash sale window" });
      }
    }

    const affectedProducts = new Map(); // productId -> shopEntry
    for (const [winId, { fs, reason }] of windowsToRetire.entries()) {
      console.log(`  [flash-sale] retiring stale window "${fs.name}" (${winId}): ${reason}`);
      await gwRequest("POST", `/api/v1/admin/flash-sales/${encodeURIComponent(winId)}/end`, {
        token: adminToken,
        allowedStatuses: [200, 404, 409],
      });
      summary.flashSales.windowsRetired += 1;
      for (const it of fs.items ?? []) {
        const skuEntry = skuById.get(it.sku_id);
        if (skuEntry) affectedProducts.set(skuEntry.productId, skuEntry.shopEntry);
      }
    }

    // Refresh SKU stock/version/reservations for affected products before stock top-up.
    for (const [productId, shopEntry] of affectedProducts.entries()) {
      const prodRes = await gwRequest("GET", `/api/v1/products/${encodeURIComponent(productId)}`, {
        token: shopEntry.sellerToken,
        allowedStatuses: [200],
      });
      for (const refreshedSku of prodRes.data?.product?.skus ?? []) {
        const entry = skuById.get(refreshedSku.id);
        if (entry) {
          entry.sku = refreshedSku;
          entry.skuByCode.set(refreshedSku.sku_code, refreshedSku);
        }
      }
    }
  }

  const existingFlashByName = new Map(
    allActiveFlashSales
      .filter((fs) => !windowsToRetire.has(fs.id))
      .map((fs) => [fs.name, fs])
  );

  // Under Phase 2b, approving flash-sale items reserves total_slots from SKU stock across all
  // active/upcoming windows. Compute how many slots still need approval right now, and idempotently
  // top up ONLY flash-sale SKUs whose stock or available_stock is below the required floor
  // (never touching the 6 intentional 0-stock non-flash SKUs).
  const pendingFlashDemandBySkuCode = new Map();
  for (const slot of FLASH_SALE_SLOTS) {
    const win = windowSpecs[slot.id];
    if (!win) continue;
    const existingFlash = existingFlashByName.get(win.name);
    const existingApprovedSkuIds = new Set(
      (existingFlash?.items ?? [])
        .filter((it) => String(it.status ?? "").toUpperCase() === "APPROVED")
        .map((it) => it.sku_id)
    );

    for (const item of slot.items) {
      const seededProd = seededProducts.get(item.productId);
      if (!seededProd) continue;
      const targetSkus = selectFlashSaleTargetSkus(seededProd.mockProduct, seededProd.skuIdByCode);
      const slotsPerSku = distributeSlots(item.totalQuota, targetSkus.length);
      for (let idx = 0; idx < targetSkus.length; idx++) {
        const sku = targetSkus[idx];
        const skuId = seededProd.skuIdByCode.get(sku.skuCode);
        if (!existingApprovedSkuIds.has(skuId)) {
          pendingFlashDemandBySkuCode.set(
            sku.skuCode,
            (pendingFlashDemandBySkuCode.get(sku.skuCode) ?? 0) + slotsPerSku[idx]
          );
        }
      }
    }
  }

  for (const [skuCode, totalFlashDemand] of flashDemandBySkuCode.entries()) {
    const entry = skuCatalogByCode.get(skuCode);
    if (!entry || Math.trunc(entry.mockSku.stock) <= 0) continue;
    const backendSku = entry.skuByCode.get(skuCode);
    if (!backendSku) continue;

    const pendingFlash = pendingFlashDemandBySkuCode.get(skuCode) ?? 0;
    const minTotalStock = computeDesiredSkuStock(entry.mockSku, totalFlashDemand);
    const curStock = Math.trunc(backendSku.stock ?? 0);
    const curAvailable = Math.trunc(
      backendSku.available_stock ??
        Math.max(
          0,
          curStock -
            Math.trunc(backendSku.reserved_stock ?? 0) -
            Math.trunc(backendSku.flash_sale_reserved_stock ?? 0)
        )
    );
    const requiredAvailableNow = pendingFlash + FLASH_SALE_REGULAR_STOCK_BUFFER;
    const neededStockForAvailable = curStock + Math.max(0, requiredAvailableNow - curAvailable);
    const targetStock = Math.max(minTotalStock, neededStockForAvailable);

    if (curStock < targetStock) {
      const updatedSku = await topUpSkuStock(
        entry.shopEntry.sellerToken,
        entry.productId,
        backendSku,
        targetStock
      );
      entry.skuByCode.set(skuCode, updatedSku);
      if (skuById.has(updatedSku.id)) {
        skuById.get(updatedSku.id).sku = updatedSku;
      }
      summary.products.skusToppedUp += 1;
    }
  }

  for (const slot of FLASH_SALE_SLOTS) {
    const win = windowSpecs[slot.id];
    if (!win) continue; // skip ended slot-09

    let flashSale = existingFlashByName.get(win.name);
    if (!flashSale) {
      const createRes = await gwRequest("POST", "/api/v1/admin/flash-sales", {
        token: adminToken,
        body: {
          name: win.name,
          starts_at: win.startsAt,
          ends_at: win.endsAt,
        },
        allowedStatuses: [200, 201],
      });
      flashSale = createRes.data.flash_sale;
      flashSale.items = flashSale.items ?? [];
      existingFlashByName.set(win.name, flashSale);
      summary.flashSales.windowsCreated += 1;
    } else {
      summary.flashSales.windowsReused += 1;
    }

    const existingItemBySkuId = new Map((flashSale.items ?? []).map((it) => [it.sku_id, it]));

    for (const item of slot.items) {
      const seededProd = seededProducts.get(item.productId);
      if (!seededProd) continue;

      // Register all in-stock SKUs of the product's primary color so every size works on /flash-sale
      // while staying cleanly within gateway flash-sale & IP rate limits.
      const targetSkus = selectFlashSaleTargetSkus(seededProd.mockProduct, seededProd.skuIdByCode);
      const slotsPerSku = distributeSlots(item.totalQuota, targetSkus.length);

      for (let idx = 0; idx < targetSkus.length; idx++) {
        const sku = targetSkus[idx];
        const skuId = seededProd.skuIdByCode.get(sku.skuCode);
        const existingItem = existingItemBySkuId.get(skuId);
        if (existingItem && String(existingItem.status ?? "").toUpperCase() === "APPROVED") {
          summary.flashSales.itemsReused += 1;
          continue;
        }

        let itemToApprove = existingItem ?? null;
        if (!itemToApprove) {
          const regRes = await gwRequest(
            "POST",
            `/api/v1/seller/flash-sales/${encodeURIComponent(flashSale.id)}/items`,
            {
              token: seededProd.shopEntry.sellerToken,
              body: {
                sku_id: skuId,
                flash_price_vnd: Math.trunc(item.flashPrice),
                total_slots: slotsPerSku[idx],
                per_user_limit: Math.max(1, Math.trunc(item.limitPerBuyer)),
              },
              allowedStatuses: [200, 201, 409],
            }
          );

          if (regRes.status === 409) {
            summary.flashSales.itemsReused += 1;
            continue;
          }
          itemToApprove = regRes.data.item;
        }

        await gwRequest(
          "POST",
          `/api/v1/admin/flash-sale-items/${encodeURIComponent(itemToApprove.id)}/approve`,
          {
            token: adminToken,
            body: { approve: true, reason: "Seed flash sale item approval" },
            allowedStatuses: [200],
          }
        );
        existingItemBySkuId.set(skuId, { ...itemToApprove, status: "APPROVED" });
        summary.flashSales.itemsCreated += 1;
      }
    }
  }

  // 7. 4 Showcase Published Subdomain Sites (one per demo shop, distinct themes & v2 templates) + custom domains
  const showcaseSiteSpecs = buildShowcaseSiteSpecs(seededProducts);
  const tplRes = await gwRequest("GET", "/api/v1/site-templates", {
    allowedStatuses: [200, 404, 502, 503],
  });
  if (tplRes.status === 200 && Array.isArray(tplRes.data?.templates) && tplRes.data.templates.length > 0) {
    const templatesByKey = new Map();
    for (const tpl of tplRes.data.templates) {
      if (tpl?.slug) templatesByKey.set(String(tpl.slug).trim().toLowerCase(), tpl);
      if (tpl?.id) templatesByKey.set(String(tpl.id).trim().toLowerCase(), tpl);
    }
    const applied = applyTemplatesToShowcaseSpecs(showcaseSiteSpecs, templatesByKey);
    summary.templates = {
      skipped: false,
      available: tplRes.data.templates.length,
      applied,
    };
  } else {
    summary.templates = {
      skipped: true,
      status: tplRes.status,
      available: 0,
      applied: 0,
    };
  }

  for (const spec of showcaseSiteSpecs) {
    const shopEntry = seededShops.get(spec.mockShopId);
    if (!shopEntry) throw new Error(`Missing seeded shop for site ${spec.subdomain} (${spec.mockShopId})`);
    const { created, site } = await ensurePublishedSite(shopEntry.sellerToken, spec);
    summary.sites[created ? "created" : "reused"] += 1;
    const subdomainHost = `${spec.subdomain}.${SITE_ROOT_DOMAIN}`;
    const siteSummary = {
      shop_id: shopEntry.shop.id,
      shop_name: shopEntry.shop.name,
      subdomain: spec.subdomain,
      primary_host: site.primary_host || subdomainHost,
      status: site.status || "PUBLISHED",
      action: created ? "published" : "reused",
      schema_version: spec.config.schema_version ?? 1,
      ...(spec.templateId ? { template_id: spec.templateId } : {}),
      ...(spec.archetype ? { archetype: spec.archetype } : {}),
      font_family: spec.config.theme.font_family,
      bg_color: spec.config.theme.bg_color,
      accent_color: spec.config.theme.accent_color,
    };
    if (spec.customDomain) {
      const custom = await ensureCustomDomain(shopEntry.sellerToken, spec.customDomain);
      siteSummary.custom_domain = custom.domain.host || spec.customDomain.hostname;
      siteSummary.custom_domain_status = custom.domain.status;
      siteSummary.custom_domain_action = custom.created ? "created" : "reused";
      if (custom.primaryHost) {
        siteSummary.primary_host = custom.primaryHost;
      } else if (spec.customDomain.expectStatus === "PENDING") {
        siteSummary.primary_host = subdomainHost;
      }
    }
    summary.sites.map[spec.subdomain] = siteSummary;
  }

  // 8. Chat sample data (Swarm B: auto-reply + 3-message Vietnamese threads on bach-dang & sai-gon)
  summary.chat = await seedChatSampleData({ buyer, seededShops, seededProducts });

  // 9. Returns & Complaint sample data (Swarm D: 1 pending return on bach-dang + 1 open admin complaint on sai-gon)
  summary.returnsAndComplaints = await seedReturnsAndComplaints({
    adminToken,
    buyer,
    buyerDefaultAddress,
    seededShops,
    seededProducts,
    skuById,
    summary,
  });

  // 10. Catalog 20,000 products via Swarm A's tools/catalog-gen on dedicated 5th shop (no site)
  summary.catalog = await seedLargeCatalog({
    catalogSeller: CATALOG_SELLER,
    targetCount: SEED_CATALOG_COUNT,
  });

  console.log("==> Seed complete:");
  console.log(
    `  sites: created=${summary.sites.created}, reused=${summary.sites.reused} (root_domain=${SITE_ROOT_DOMAIN}, templates_applied=${summary.templates.applied})`
  );
  console.log(
    JSON.stringify(
      {
        gateway: GATEWAY_URL,
        site_root_domain: SITE_ROOT_DOMAIN,
        accounts: {
          admin: { email: ADMIN_CREDS.email, role: adminSession.user.role, id: adminSession.user.id },
          buyer: {
            email: DEMO_BUYER.email,
            password: DEMO_BUYER.password,
            role: buyer.user.role,
            id: buyer.user.id,
            coins: coinRes.data?.balance ?? INITIAL_USER_COINS,
          },
          seller: {
            email: SHOP_SELLERS["shop-bach-dang"].email,
            password: SHOP_SELLERS["shop-bach-dang"].password,
            role: seededShops.get("shop-bach-dang").sellerUser.role,
            id: seededShops.get("shop-bach-dang").sellerUser.id,
            shop_id: seededShops.get("shop-bach-dang").shop.id,
            shop_name: seededShops.get("shop-bach-dang").shop.name,
          },
          catalogSeller: {
            email: CATALOG_SELLER.email,
            password: CATALOG_SELLER.password,
            role: catalogSellerSession.user.role,
            id: catalogSellerSession.user.id,
            shop_id: catalogShop.id,
            shop_name: catalogShop.name,
          },
        },
        summary,
      },
      null,
      2
    )
  );
}

main().catch((err) => {
  console.error("Seed failed:", err);
  process.exit(1);
});
