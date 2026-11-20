#!/usr/bin/env node
/**
 * Idempotent backend seed tool for NexusMart (Phase 0b + Phase 2/2b).
 *
 * Reads the canonical storefront catalog from web/src/data/* and seeds via the
 * real gateway REST API + Mailpit OTP verification:
 *   - 1 demo admin (bootstrapped), 1 demo buyer (+ default address & welcome coins),
 *     4 demo seller accounts (primary demo seller owns Xưởng Dệt Bạch Đằng)
 *   - 4 verified ACTIVE shops matching web/src/data/shops.ts
 *   - 6 categories matching web/src/data/products.ts CATEGORIES
 *   - 18 APPROVED products with all 159 SKUs, integer VND prices, stock (with
 *     Phase 2b flash-sale reservation headroom + >= 25 regular available units
 *     while keeping the 6 intentional out-of-stock SKUs at 0), and image paths
 *     matching web/public/products and web/public/models
 *   - 11 vouchers matching web/src/data/vouchers.ts (platform, freeship, shop)
 *   - 3 active/upcoming flash sale slots (12:00 live, 18:00 & 21:00 upcoming)
 *     matching web/src/data/flash-sale.ts
 *   - 2 clearly distinct PUBLISHED subdomain storefront sites (bach-dang & sai-gon),
 *     leaving Đà Nẵng and Hà Nội without sites for E2E lifecycle tests
 *
 * Safe to run repeatedly: existing entities are detected and reused with zero
 * duplicates or extra site versions. Exits 0 on success.
 */
import { register } from "node:module";

register(new URL("./ts-resolve.mjs", import.meta.url));

const { SHOPS } = await import("../../web/src/data/shops.ts");
const { CATEGORIES, PRODUCTS } = await import("../../web/src/data/products.ts");
const { VOUCHERS, INITIAL_USER_COINS } = await import("../../web/src/data/vouchers.ts");
const { FLASH_SALE_SLOTS } = await import("../../web/src/data/flash-sale.ts");

const env = (key, fallback) => (process.env[key] ?? "").trim() || fallback;

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
const FLASH_SALE_REGULAR_STOCK_BUFFER = 25;

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

async function gwRequest(method, path, { token, body, allowedStatuses = [200, 201], maxRetries = 4 } = {}) {
  const headers = { accept: "application/json" };
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
 * Builds the 2 showcase subdomain storefront site specs (Xưởng Dệt Bạch Đằng & Kho Basic Sài Gòn).
 * Sợi Tự Nhiên Đà Nẵng and Dệt Kim Hà Nội are intentionally left without sites for QA E2E lifecycle tests.
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
  ];
}

async function ensurePublishedSite(sellerToken, spec) {
  const getRes = await gwRequest("GET", "/api/v1/seller/site", {
    token: sellerToken,
    allowedStatuses: [200, 404],
  });
  const existing = getRes.status === 200 ? (getRes.data?.site ?? null) : null;
  const existingSubdomain = String(existing?.subdomain ?? "").trim().toLowerCase();
  const targetSubdomain = spec.subdomain.trim().toLowerCase();
  const existingPublishedCfg =
    existing?.published_config ?? existing?.published_config_json ?? existing?.config ?? existing?.config_json ?? null;
  const existingDraftCfg =
    existing?.draft_config ?? existing?.draft_config_json ?? existing?.config ?? existing?.config_json ?? null;

  if (
    existing &&
    existing.status === "PUBLISHED" &&
    existingSubdomain === targetSubdomain &&
    siteConfigsEqual(existingPublishedCfg ?? existingDraftCfg, spec.config)
  ) {
    return { created: false, site: existing };
  }

  const draftMatches =
    existing &&
    existingSubdomain === targetSubdomain &&
    siteConfigsEqual(existingDraftCfg, spec.config);

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

  const publishedSite = pubRes.data?.site ?? existing ?? { subdomain: targetSubdomain, status: "PUBLISHED" };
  return { created: true, site: publishedSite };
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
    flashSales: { windowsCreated: 0, windowsReused: 0, itemsCreated: 0, itemsReused: 0 },
    sites: { created: 0, reused: 0, map: {} },
  };

  // 1. Demo buyer (+ default address + welcome coins)
  const buyer = await ensureVerifiedUser(adminToken, DEMO_BUYER);
  summary.users[buyer.created ? "created" : "reused"] += 1;
  await ensureDefaultAddress(
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
      if (skuByCode.has(mockSku.skuCode)) {
        skuCatalogByCode.set(mockSku.skuCode, {
          mockSku,
          productId: backendProd.id,
          shopEntry,
          skuByCode,
        });
      }
    }
  }

  // 5. 11 Vouchers matching VOUCHERS
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
  const existingFlashByName = new Map(
    (fsListRes.data?.flash_sales ?? []).map((fs) => [fs.name, fs])
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
    const existingSkuIds = new Set((existingFlash?.items ?? []).map((it) => it.sku_id));

    for (const item of slot.items) {
      const seededProd = seededProducts.get(item.productId);
      if (!seededProd) continue;
      const targetSkus = selectFlashSaleTargetSkus(seededProd.mockProduct, seededProd.skuIdByCode);
      const slotsPerSku = distributeSlots(item.totalQuota, targetSkus.length);
      for (let idx = 0; idx < targetSkus.length; idx++) {
        const sku = targetSkus[idx];
        const skuId = seededProd.skuIdByCode.get(sku.skuCode);
        if (!existingSkuIds.has(skuId)) {
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
      backendSku.available_stock ?? Math.max(0, curStock - Math.trunc(backendSku.reserved_stock ?? 0))
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

    const existingSkuIds = new Set((flashSale.items ?? []).map((it) => it.sku_id));

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
        if (existingSkuIds.has(skuId)) {
          summary.flashSales.itemsReused += 1;
          continue;
        }

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

        const createdItem = regRes.data.item;
        await gwRequest(
          "POST",
          `/api/v1/admin/flash-sale-items/${encodeURIComponent(createdItem.id)}/approve`,
          {
            token: adminToken,
            body: { approve: true, reason: "Seed flash sale item approval" },
            allowedStatuses: [200],
          }
        );
        existingSkuIds.add(skuId);
        summary.flashSales.itemsCreated += 1;
      }
    }
  }

  // 7. 2 Showcase Published Subdomain Sites (bach-dang & sai-gon; Đà Nẵng & Hà Nội left unpublished for QA E2E)
  const showcaseSiteSpecs = buildShowcaseSiteSpecs(seededProducts);
  for (const spec of showcaseSiteSpecs) {
    const shopEntry = seededShops.get(spec.mockShopId);
    if (!shopEntry) throw new Error(`Missing seeded shop for site ${spec.subdomain} (${spec.mockShopId})`);
    const { created, site } = await ensurePublishedSite(shopEntry.sellerToken, spec);
    summary.sites[created ? "created" : "reused"] += 1;
    summary.sites.map[spec.subdomain] = {
      shop_id: shopEntry.shop.id,
      shop_name: shopEntry.shop.name,
      subdomain: spec.subdomain,
      primary_host: site.primary_host || `${spec.subdomain}.${SITE_ROOT_DOMAIN}`,
      status: site.status || "PUBLISHED",
    };
  }

  console.log("==> Seed complete:");
  console.log(
    `  sites: created=${summary.sites.created}, reused=${summary.sites.reused} (root_domain=${SITE_ROOT_DOMAIN})`
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
