#!/usr/bin/env node
// Idempotent NexusMart backend seeder via Gateway REST API (zero direct DB writes).
// Single source of truth: imports web/src/data/* directly using Node 24 TypeScript type stripping.

import crypto from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { registerHooks } from "node:module";
import { dirname, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const REPO_ROOT = resolve(__dirname, "../..");
const WEB_SRC_DIR = resolve(REPO_ROOT, "web/src");

// Resolve extensionless relative and @/ TypeScript imports inside web/src/.
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.startsWith("@/")) {
      const rel = specifier.slice(2);
      const tsPath = resolve(WEB_SRC_DIR, `${rel}.ts`);
      if (existsSync(tsPath)) {
        return nextResolve(pathToFileURL(tsPath).href, context);
      }
      const indexTsPath = resolve(WEB_SRC_DIR, rel, "index.ts");
      if (existsSync(indexTsPath)) {
        return nextResolve(pathToFileURL(indexTsPath).href, context);
      }
    }
    if (
      (specifier.startsWith("./") || specifier.startsWith("../")) &&
      !/\.[a-z]+$/i.test(specifier) &&
      context.parentURL
    ) {
      const parentDir = dirname(fileURLToPath(context.parentURL));
      const tsPath = resolve(parentDir, `${specifier}.ts`);
      if (existsSync(tsPath)) {
        return nextResolve(pathToFileURL(tsPath).href, context);
      }
      const indexTsPath = resolve(parentDir, specifier, "index.ts");
      if (existsSync(indexTsPath)) {
        return nextResolve(pathToFileURL(indexTsPath).href, context);
      }
    }
    return nextResolve(specifier, context);
  },
});

function loadDotEnv(filePath) {
  const vars = {};
  if (!existsSync(filePath)) return vars;
  const raw = readFileSync(filePath, "utf8");
  for (const line of raw.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const eq = trimmed.indexOf("=");
    if (eq <= 0) continue;
    const key = trimmed.slice(0, eq).trim();
    let val = trimmed.slice(eq + 1).trim();
    if (
      (val.startsWith('"') && val.endsWith('"')) ||
      (val.startsWith("'") && val.endsWith("'"))
    ) {
      val = val.slice(1, -1);
    }
    vars[key] = val;
  }
  return vars;
}

const dotEnv = loadDotEnv(resolve(REPO_ROOT, ".env"));
const GATEWAY_URL = (
  process.env.SEED_GATEWAY_URL || `http://localhost:${dotEnv.GATEWAY_PORT || "8080"}`
).replace(/\/+$/, "");
const MAILPIT_URL = (
  process.env.MAILPIT_URL || `http://localhost:${dotEnv.MAILPIT_UI_PORT || "8025"}`
).replace(/\/+$/, "");
const ADMIN_EMAIL =
  process.env.ADMIN_BOOTSTRAP_EMAIL || dotEnv.ADMIN_BOOTSTRAP_EMAIL || "admin@nexusmart.local";
const ADMIN_PASSWORD =
  process.env.ADMIN_BOOTSTRAP_PASSWORD || dotEnv.ADMIN_BOOTSTRAP_PASSWORD || "AdminDevPassword123!";
const PAYMENT_WEBHOOK_SECRET =
  process.env.PAYMENT_WEBHOOK_SECRET ||
  dotEnv.PAYMENT_WEBHOOK_SECRET ||
  "dev-only-payment-webhook-hmac-secret";
const SHIPPING_WEBHOOK_SECRET =
  process.env.SHIPPING_WEBHOOK_SECRET ||
  dotEnv.SHIPPING_WEBHOOK_SECRET ||
  "dev-only-shipping-webhook-hmac-secret";
const WEB_BASE_URL = (
  process.env.WEB_BASE_URL ||
  dotEnv.WEB_BASE_URL ||
  `http://localhost:${dotEnv.WEB_PORT || "3000"}`
).replace(/\/+$/, "");

// Dev-only demo credentials (not real secrets; documented in tools/seed/README.md).
const SELLER_DEMO_PASSWORD = "SellerDevPassword123!";
const BUYER_DEMO_PASSWORD = "BuyerDevPassword123!";

const SELLER_ACCOUNT_BY_SHOP_ID = {
  "shop-bach-dang": {
    email: "seller.bachdang@nexusmart.local",
    phone: "0901000001",
    fullName: "Quản lý Xưởng Dệt Bạch Đằng",
  },
  "shop-sai-gon": {
    email: "seller.saigon@nexusmart.local",
    phone: "0901000002",
    fullName: "Quản lý Kho Basic Sài Gòn",
  },
  "shop-da-nang": {
    email: "seller.danang@nexusmart.local",
    phone: "0901000003",
    fullName: "Quản lý Sợi Tự Nhiên Đà Nẵng",
  },
  "shop-ha-noi": {
    email: "seller.hanoi@nexusmart.local",
    phone: "0901000004",
    fullName: "Quản lý Dệt Kim Hà Nội",
  },
};

// Shop commission rates (removed from public web/src/data/shops.ts under D1; values match HEAD).
const SEED_SHOP_COMMISSION_RATE = {
  "shop-bach-dang": 0.05,
  "shop-sai-gon": 0.05,
  "shop-da-nang": 0.055,
  "shop-ha-noi": 0.05,
};

// Maps web/src/data/products.ts subCategory slugs to product attribute predicates
// so child category commission_rate_bps stays synced with owning shop rates in shops.ts (CAT-D3).
const SUBCATEGORY_PRODUCT_MATCHERS = {
  "cotton-compact": (p) => p.material === "cotton-compact",
  "the-thao-dryfit": (p) => p.material === "polyester-dryfit",
  "oversize-260gsm": (p) => p.fitType === "oversize",
  "polo-pique": (p) => p.material === "pique-cotton",
  "polo-det-kim": (p) => p.material === "viscose-knit",
  "short-daily": (p) => p.material === "cotton-twill",
  "short-chay-bo": (p) => Array.isArray(p.sport) && p.sport.includes("chay-bo"),
  "short-linen": (p) => p.material === "linen-cotton",
  "jogger-terry": (p) => p.material === "french-terry",
  "jogger-tech": (p) => p.material === "nylon-tech",
  "boxer-modal": (p) => p.material === "modal",
  "brief-tre": (p) => p.material === "bamboo",
  "tat-co-trung": (p) => Array.isArray(p.occasion) && p.occasion.includes("cong-so"),
  "tat-the-thao": (p) => Array.isArray(p.sport) && p.sport.includes("chay-bo"),
  "ao-khoac-gio": (p) => p.material === "nylon-ripstop",
};

const SHOP_DISCOUNT_PROGRAM_SPECS = [
  {
    shopStaticId: "shop-bach-dang",
    productStaticId: "prod-at-xamda",
    name: "Ưu đãi Xưởng Bạch Đằng — Cotton Compact Xám Đá",
    mode: "percent",
    discountPercent: 12,
    perUserLimit: 2,
    maxSkus: 2,
  },
  {
    shopStaticId: "shop-sai-gon",
    productStaticId: "prod-short-run",
    name: "Giảm giá Kho Basic Sài Gòn — Short Chạy Bộ",
    mode: "price",
    priceRatio: 0.85,
    perUserLimit: 2,
    maxSkus: 2,
  },
  {
    shopStaticId: "shop-da-nang",
    productStaticId: "prod-short-linen",
    name: "Tuần lễ Sợi Tự Nhiên — Short Linen Đà Nẵng",
    mode: "percent",
    discountPercent: 10,
    perUserLimit: 2,
    maxSkus: 2,
  },
  {
    shopStaticId: "shop-ha-noi",
    productStaticId: "prod-tat-cothap",
    name: "Ưu đãi Dệt Kim Hà Nội — Tất Thể Thao Cổ Thấp",
    mode: "price",
    priceRatio: 0.88,
    perUserLimit: 3,
    maxSkus: 2,
  },
];

const UPCOMING_CATEGORY_FEE_SPECS = [
  {
    categorySlug: "ao-thun",
    commission_rate_bps: 520,
    note: "[seed:cat-fee:ao-thun:v1] Điều chỉnh hoa hồng Áo thun dự kiến (5,2%)",
  },
  {
    categorySlug: "ao-polo",
    commission_rate_bps: 580,
    note: "[seed:cat-fee:ao-polo:v1] Điều chỉnh hoa hồng Áo polo dự kiến (5,8%)",
  },
  {
    categorySlug: "tat-phu-kien",
    commission_rate_bps: 550,
    note: "[seed:cat-fee:tat-phu-kien:v1] Điều chỉnh hoa hồng Tất & phụ kiện dự kiến (5,5%)",
  },
];

const UPCOMING_PAYMENT_FEE_SPECS = [
  {
    payment_method: "VIETQR",
    fee_bps: 150,
    note: "[seed:pay-fee:vietqr:v1] Biểu phí thanh toán VietQR dự kiến (1,5%)",
  },
  {
    payment_method: "COD",
    fee_bps: 100,
    note: "[seed:pay-fee:cod:v1] Biểu phí thu hộ COD dự kiến (1,0%)",
  },
];

const DEMO_ORDER_SPECS = [
  {
    key: "preparing",
    idempotencyKey: "seed:demo-order:preparing:v1",
    shopStaticId: "shop-bach-dang",
    productStaticId: "prod-at-xamda",
    preferredSkuCode: "BD-AT02-XAMDA-L",
    quantity: 1,
    paymentMethod: "VIETQR",
    targetStatus: "PREPARING",
  },
  {
    key: "shipping",
    idempotencyKey: "seed:demo-order:shipping:v1",
    shopStaticId: "shop-ha-noi",
    productStaticId: "prod-tat-cothap",
    preferredSkuCode: "HN-TT17-COMBO-5MAU-L",
    quantity: 1,
    paymentMethod: "COD",
    targetStatus: "SHIPPING",
  },
  {
    key: "completed",
    idempotencyKey: "seed:demo-order:completed:v1",
    shopStaticId: "shop-sai-gon",
    productStaticId: "prod-short-run",
    preferredSkuCode: "SG-SR10-XAMDAM-L",
    quantity: 1,
    paymentMethod: "VIETQR",
    targetStatus: "COMPLETED",
  },
  {
    key: "cancelled",
    idempotencyKey: "seed:demo-order:cancelled:v1",
    shopStaticId: "shop-sai-gon",
    productStaticId: "prod-aokhoac-gio",
    preferredSkuCode: "SG-AK18-XAMTRO-L",
    quantity: 1,
    paymentMethod: "COD",
    targetStatus: "CANCELLED",
    cancelReason: "Thay đổi địa chỉ nhận hàng sang Hà Nội.",
  },
  {
    key: "return_requested",
    idempotencyKey: "seed:demo-order:return-requested:v1",
    shopStaticId: "shop-da-nang",
    productStaticId: "prod-dolot-brief",
    preferredSkuCode: "DN-DL15-COMBO-3MAU-L",
    quantity: 1,
    paymentMethod: "VIETQR",
    targetStatus: "RETURN_REQUESTED",
    returnReason: "Cần đổi size từ M sang L vì hơi kích vai.",
    returnEvidenceUrls: [`${WEB_BASE_URL}/products/do-lot-brief-tre.png`],
  },
  {
    key: "returned",
    idempotencyKey: "seed:demo-order:returned:v1",
    shopStaticId: "shop-ha-noi",
    productStaticId: "prod-jogger-tech",
    preferredSkuCode: "HN-JK13-DEN-L",
    quantity: 1,
    paymentMethod: "VIETQR",
    targetStatus: "RETURNED",
    returnReason: "Sản phẩm bị lỗi đường may bo gấu, yêu cầu hoàn tiền.",
    returnEvidenceUrls: [`${WEB_BASE_URL}/products/quan-jogger-tech-den.png`],
    sellerReturnNote: "Gian hàng xác nhận hoàn tiền ngay cho khách hàng.",
  },
];

const stats = {
  sellers: { created: 0, existing: 0 },
  shops: { created: 0, existing: 0 },
  buyer: { created: 0, existing: 0 },
  buyerPhoneVerified: { created: 0, existing: 0 },
  addresses: { created: 0, existing: 0 },
  coins: { created: 0, existing: 0 },
  categories: { created: 0, existing: 0, updated: 0 },
  childCategories: { created: 0, existing: 0, updated: 0 },
  products: { created: 0, existing: 0 },
  vouchers: { created: 0, existing: 0 },
  flashSales: { created: 0, existing: 0 },
  flashSaleItems: { created: 0, existing: 0 },
  shopDiscountPrograms: { created: 0, existing: 0 },
  categoryFeeSchedules: { created: 0, existing: 0 },
  paymentFeeSchedules: { created: 0, existing: 0 },
  demoOrders: { created: 0, existing: 0 },
};

function sleep(ms) {
  return new Promise((resolvePromise) => setTimeout(resolvePromise, ms));
}

function normalizeText(value) {
  return String(value || "")
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/đ/gi, "d")
    .toLowerCase()
    .replace(/\s+/g, " ")
    .trim();
}

const VN_HM_FORMATTER = new Intl.DateTimeFormat("en-GB", {
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
  timeZone: "Asia/Ho_Chi_Minh",
});

function formatHmVn(ms) {
  return VN_HM_FORMATTER.format(new Date(ms));
}

function cleanPhone(raw) {
  return String(raw || "").replace(/[^0-9+]/g, "");
}

function estimateWeightGrams(product) {
  const gsm = Number.isFinite(product.gsm) ? product.gsm : 200;
  switch (product.categorySlug) {
    case "ao-thun":
      return Math.max(150, Math.round(gsm * 1.1));
    case "ao-polo":
      return Math.max(220, Math.round(gsm * 1.3));
    case "quan-short":
      return Math.max(180, Math.round(gsm * 1.35));
    case "quan-jogger":
      return Math.max(360, Math.round(gsm * 1.65));
    case "do-lot":
      return 220;
    case "tat-phu-kien":
      return product.id.includes("aokhoac") ? 240 : 180;
    default:
      return 250;
  }
}

function getSkuAvailableStock(sku) {
  if (!sku) return 0;
  if (Number.isInteger(sku.available_stock)) {
    return Math.max(0, sku.available_stock);
  }
  const stock = Number.isFinite(sku.stock) ? Math.trunc(sku.stock) : 0;
  const reserved = Number.isFinite(sku.reserved_stock) ? Math.trunc(sku.reserved_stock) : 0;
  return Math.max(0, stock - reserved);
}

/**
 * PRM-D1: Pick the active SKU with highest available_stock and clamp total_slots <= available_stock,
 * per_user_limit <= total_slots, and flash_price_vnd < min_price_30d_vnd.
 */
export function selectFlashSaleSku(product) {
  const skus = Array.isArray(product?.skus) ? product.skus : [];
  let best = null;
  let bestAvail = 0;
  for (const sku of skus) {
    if (sku.is_active === false) continue;
    const avail = getSkuAvailableStock(sku);
    if (avail > bestAvail) {
      best = sku;
      bestAvail = avail;
    }
  }
  if (!best || bestAvail <= 0) return null;
  return { sku: best, availableStock: bestAvail };
}

export function computeFlashSaleRegistration(product, item) {
  const selected = selectFlashSaleSku(product);
  if (!selected) return null;
  const { sku, availableStock } = selected;

  const requestedQuota = Math.max(1, Math.trunc(Number(item.totalQuota) || 1));
  const totalSlots = Math.min(requestedQuota, availableStock);
  const requestedLimit = Math.max(1, Math.trunc(Number(item.limitPerBuyer) || 1));
  const perUserLimit = Math.min(requestedLimit, totalSlots);

  const regularPrice = Math.trunc(Number(sku.price_vnd) || 0);
  const min30dRaw = Math.trunc(Number(sku.min_price_30d_vnd) || 0);
  const minPrice30d = min30dRaw > 0 && min30dRaw < regularPrice ? min30dRaw : regularPrice;
  if (minPrice30d <= 1) return null;

  let flashPriceVnd = Math.trunc(Number(item.flashPrice) || 0);
  if (flashPriceVnd <= 0 || flashPriceVnd >= minPrice30d) {
    const rounded = Math.floor((minPrice30d * 85) / 100 / 1000) * 1000;
    flashPriceVnd = rounded > 0 && rounded < minPrice30d ? rounded : minPrice30d - 1;
  }

  return {
    sku,
    availableStock,
    flash_price_vnd: flashPriceVnd,
    total_slots: totalSlots,
    per_user_limit: perUserLimit,
  };
}

/**
 * CAT-D3: Derive root + 15 child category plan with commission_rate_bps aligned to SEED_SHOP_COMMISSION_RATE.
 * Throws explicitly if any shop is missing from SEED_SHOP_COMMISSION_RATE (never falls back to 0.05).
 */
export function buildCategorySeedPlan(categories, products, shops) {
  const shopBpsById = new Map();
  for (const s of shops || []) {
    const rawRate =
      typeof s?.commissionRate === "number" ? s.commissionRate : SEED_SHOP_COMMISSION_RATE[s?.id];
    if (!Number.isFinite(rawRate) || rawRate < 0) {
      throw new Error(
        `Missing seed commission rate for shop "${s?.id ?? ""}" in SEED_SHOP_COMMISSION_RATE`
      );
    }
    shopBpsById.set(s.id, Math.round(rawRate * 10000));
  }

  const maxBpsForProducts = (list, fallback) => {
    if (list.length === 0) return fallback;
    let best = -1;
    for (const p of list) {
      const bps = shopBpsById.get(p.shopId);
      if (bps === undefined) {
        throw new Error(
          `Missing seed commission rate for shop "${p.shopId}" (product "${p.id}")`
        );
      }
      if (bps > best) best = bps;
    }
    return best >= 0 ? best : fallback;
  };

  const roots = [];
  const children = [];

  for (let i = 0; i < (categories || []).length; i += 1) {
    const cat = categories[i];
    const catProducts = (products || []).filter((p) => p.categorySlug === cat.slug);
    const rootBps = maxBpsForProducts(catProducts, 500);
    const rootOrder = i + 1;

    roots.push({
      slug: cat.slug,
      name: cat.name,
      parentSlug: "",
      commission_rate_bps: rootBps,
      sort_order: rootOrder,
    });

    const subs = Array.isArray(cat.subCategories) ? cat.subCategories : [];
    for (let j = 0; j < subs.length; j += 1) {
      const sub = subs[j];
      const matcher = SUBCATEGORY_PRODUCT_MATCHERS[sub.slug];
      const matched = matcher ? catProducts.filter(matcher) : [];
      const childBps = maxBpsForProducts(matched, rootBps);
      children.push({
        slug: sub.slug,
        name: sub.name,
        parentSlug: cat.slug,
        commission_rate_bps: childBps,
        sort_order: rootOrder * 100 + j + 1,
      });
    }
  }

  return { roots, children };
}

/**
 * Builds a minimal PUT /api/v1/admin/categories/{id} patch containing ONLY changed fields.
 * Gateway sets has_parent_id / has_commission_rate_bps / has_sort_order only when present
 * in JSON; omitting unchanged fields preserves existing parent_id, name, slug, and sort_order.
 */
export function buildCategoryUpdatePatch(existing, desired) {
  const patch = {};
  if (desired.parent_id !== undefined && existing.parent_id !== desired.parent_id) {
    patch.parent_id = desired.parent_id;
  }
  if (
    desired.commission_rate_bps !== undefined &&
    existing.commission_rate_bps !== desired.commission_rate_bps
  ) {
    patch.commission_rate_bps = desired.commission_rate_bps;
  }
  if (desired.sort_order !== undefined && existing.sort_order !== desired.sort_order) {
    patch.sort_order = desired.sort_order;
  }
  return Object.keys(patch).length > 0 ? patch : null;
}

export function isIdempotencyKeyConflict(res) {
  if (!res || res.status !== 409) return false;
  const code = String(res.data?.error?.code || "");
  const message = String(res.data?.error?.message || "");
  return code === "IDEMPOTENCY_KEY_CONFLICT" || message.includes("IDEMPOTENCY_KEY_CONFLICT");
}

/**
 * Constant-format HMAC-SHA256 webhook signer (docs/contracts.md §4.9).
 * Never log the secret or returned signature.
 */
export function signWebhookPayload(secret, rawBody, timestampSec = Math.floor(Date.now() / 1000)) {
  const sig = crypto.createHmac("sha256", String(secret)).update(rawBody, "utf8").digest("hex");
  return {
    "X-Signature": sig,
    "X-Timestamp": String(Math.trunc(timestampSec)),
  };
}

export async function apiRequest(
  method,
  path,
  { token, body, rawBody, headers: extraHeaders = {}, allowStatuses = [] } = {}
) {
  const url = `${GATEWAY_URL}${path}`;
  for (let attempt = 0; attempt < 5; attempt += 1) {
    const headers = { Accept: "application/json", ...extraHeaders };
    if (token) headers.Authorization = `Bearer ${token}`;
    const serialized = rawBody !== undefined ? rawBody : body !== undefined ? JSON.stringify(body) : undefined;
    if (serialized !== undefined && !headers["Content-Type"]) {
      headers["Content-Type"] = "application/json";
    }

    const res = await fetch(url, {
      method,
      headers,
      body: serialized,
    });

    if (res.status === 429 && attempt < 4) {
      const retryAfterSec = Number(res.headers.get("retry-after") || "5");
      const waitMs = Math.max(1000, (Number.isFinite(retryAfterSec) ? retryAfterSec : 5) * 1000);
      console.log(`  [429] Rate limited on ${method} ${path}; waiting ${waitMs}ms...`);
      await sleep(waitMs);
      continue;
    }

    const text = await res.text();
    let data = null;
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = { raw: text };
      }
    }

    if (res.ok || allowStatuses.includes(res.status)) {
      return { status: res.status, ok: res.ok, data };
    }

    const errMsg = data?.error
      ? `${data.error.code || res.status}: ${data.error.message || text}`
      : text || res.statusText;
    throw new Error(`${method} ${path} failed (HTTP ${res.status}): ${errMsg}`);
  }
  throw new Error(`${method} ${path} exceeded retry budget`);
}

function extractOtp(...parts) {
  const combined = parts.filter(Boolean).join("\n");
  const contextual = combined.match(
    /(?:otp|code|verification|mã|xác\s*thực)[^0-9]{0,24}(\d{6})\b/i
  );
  if (contextual) return contextual[1];
  const generic = combined.match(/\b(\d{6})\b/);
  return generic ? generic[1] : "";
}

async function listMailpitForEmail(email) {
  const target = email.trim().toLowerCase();
  const res = await fetch(`${MAILPIT_URL}/api/v1/messages?limit=50`);
  if (!res.ok) throw new Error(`Mailpit list returned HTTP ${res.status}`);
  const messages = (await res.json())?.messages;
  return (Array.isArray(messages) ? messages : []).filter((m) =>
    (Array.isArray(m.To) ? m.To : []).some((r) => String(r?.Address || "").trim().toLowerCase() === target)
  );
}

async function listOtpMailIds(email) {
  return new Set((await listMailpitForEmail(email)).map((m) => m.ID).filter(Boolean));
}

async function waitForOtp(email, sinceMs, timeoutMs = 20000, seenIds = new Set()) {
  const deadline = Date.now() + timeoutMs;
  let lastErr = null;

  while (Date.now() < deadline) {
    try {
      for (const summary of await listMailpitForEmail(email)) {
        if (seenIds.has(summary.ID)) continue;
        const createdMs = Date.parse(summary.Created || "");
        if (Number.isFinite(createdMs) && createdMs < sinceMs - 5000) continue;

        const msgRes = await fetch(`${MAILPIT_URL}/api/v1/message/${summary.ID}`);
        if (!msgRes.ok) continue;
        const msg = await msgRes.json();
        const otp = extractOtp(msg.Subject, msg.Text, msg.HTML, summary.Snippet);
        if (otp) return otp;
      }
    } catch (err) {
      lastErr = err;
    }
    await sleep(350);
  }

  throw new Error(
    `Timed out waiting for OTP email for ${email} from ${MAILPIT_URL}${
      lastErr ? ` (last error: ${lastErr.message})` : ""
    }`
  );
}

export async function ensureBuyerPhoneVerified({
  buyerToken,
  buyerEmail,
  apiFn = apiRequest,
  waitForOtpFn = waitForOtp,
  listSeenFn = listOtpMailIds,
}) {
  const meRes = await apiFn("GET", "/api/v1/users/me", { token: buyerToken });
  if (meRes.data?.user?.phone_verified === true) return "existing";

  const seenIds = await listSeenFn(buyerEmail);
  const beforeOtp = Date.now();
  await apiFn("POST", "/api/v1/users/me/phone/otp", { token: buyerToken, body: {} });
  const otp = await waitForOtpFn(buyerEmail, beforeOtp, 20000, seenIds);
  const verifyRes = await apiFn("POST", "/api/v1/users/me/phone/verify", {
    token: buyerToken,
    body: { otp },
  });
  if (verifyRes.data?.verified !== true) {
    throw new Error("POST /api/v1/users/me/phone/verify did not return verified=true");
  }
  return "created";
}

async function login(email, password) {
  const res = await apiRequest("POST", "/api/v1/auth/login", {
    body: { email, password },
  });
  return {
    token: res.data.access_token,
    refreshToken: res.data.refresh_token,
    user: res.data.user,
  };
}

async function findUserByEmail(adminToken, email) {
  const target = email.trim().toLowerCase();
  const res = await apiRequest(
    "GET",
    `/api/v1/admin/users?query=${encodeURIComponent(target)}&page_size=50`,
    { token: adminToken }
  );
  const items = Array.isArray(res.data?.items) ? res.data.items : [];
  return items.find((u) => String(u.email || "").trim().toLowerCase() === target) || null;
}

async function ensureUser(adminToken, { fullName, email, phone, password }) {
  const existing = await findUserByEmail(adminToken, email);

  if (!existing) {
    const beforeReg = Date.now();
    const regRes = await apiRequest("POST", "/api/v1/auth/register", {
      body: { full_name: fullName, email, phone, password },
      allowStatuses: [409],
    });
    if (regRes.status !== 409) {
      const otp = await waitForOtp(email, beforeReg);
      await apiRequest("POST", "/api/v1/auth/verify-otp", {
        body: { email, otp },
      });
      const session = await login(email, password);
      return { ...session, created: true };
    }
  }

  const current = existing || (await findUserByEmail(adminToken, email));
  if (current && current.status === "PENDING_VERIFICATION") {
    const beforeResend = Date.now();
    await apiRequest("POST", "/api/v1/auth/resend-otp", {
      body: { email },
    });
    const otp = await waitForOtp(email, beforeResend);
    await apiRequest("POST", "/api/v1/auth/verify-otp", {
      body: { email, otp },
    });
  }

  const session = await login(email, password);
  return { ...session, created: false };
}

class ShippingDirectory {
  constructor() {
    this.provinces = [];
    this.wardsByProvince = new Map();
  }

  async load() {
    const res = await apiRequest("GET", "/api/v1/shipping/provinces");
    this.provinces = Array.isArray(res.data?.provinces) ? res.data.provinces : [];
    if (this.provinces.length === 0) {
      throw new Error("GET /api/v1/shipping/provinces returned no provinces");
    }
  }

  async resolveLocation(provinceNameHint, wardHint) {
    const targetProv = normalizeText(provinceNameHint);
    const province =
      this.provinces.find((p) => normalizeText(p.name) === targetProv) ||
      this.provinces.find((p) => normalizeText(p.name).includes(targetProv)) ||
      this.provinces[0];

    if (!this.wardsByProvince.has(province.code)) {
      const res = await apiRequest(
        "GET",
        `/api/v1/shipping/provinces/${encodeURIComponent(province.code)}/wards`
      );
      const wards = Array.isArray(res.data?.wards) ? res.data.wards : [];
      if (wards.length === 0) {
        throw new Error(`No wards returned for province ${province.code} (${province.name})`);
      }
      this.wardsByProvince.set(province.code, wards);
    }

    const wards = this.wardsByProvince.get(province.code);
    const normHint = normalizeText(wardHint);
    const ward =
      (normHint && wards.find((w) => normHint.includes(normalizeText(w.name)))) || wards[0];

    return { province, ward };
  }
}

async function ensureDefaultAddress(
  token,
  shippingDir,
  { recipientName, recipientPhone, provinceNameHint, wardHint, streetAddress }
) {
  const listRes = await apiRequest("GET", "/api/v1/users/me/addresses", { token });
  const existing = Array.isArray(listRes.data?.addresses) ? listRes.data.addresses : [];
  if (existing.length > 0) {
    stats.addresses.existing += 1;
    return existing.find((a) => a.is_default) || existing[0];
  }

  const { province, ward } = await shippingDir.resolveLocation(provinceNameHint, wardHint);
  const createRes = await apiRequest("POST", "/api/v1/users/me/addresses", {
    token,
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
  });
  stats.addresses.created += 1;
  return createRes.data.address;
}

async function listAllAdminShops(adminToken) {
  const all = [];
  let page = 1;
  while (true) {
    const res = await apiRequest("GET", `/api/v1/admin/shops?page=${page}&page_size=100`, {
      token: adminToken,
    });
    const items = Array.isArray(res.data?.items) ? res.data.items : [];
    all.push(...items);
    const total = Number(res.data?.total || items.length);
    if (all.length >= total || items.length === 0) break;
    page += 1;
  }
  return all;
}

export async function listAllAdminCategories(adminToken, apiFn = apiRequest) {
  const res = await apiFn("GET", "/api/v1/admin/categories", {
    token: adminToken,
  });
  return Array.isArray(res.data?.categories) ? res.data.categories : [];
}

/**
 * Idempotently seed root + child categories using GET /api/v1/admin/categories so
 * commission_rate_bps comparisons emit zero PUTs on already-seeded databases.
 */
export async function seedCategories({
  adminToken,
  categories,
  products,
  shops,
  apiFn = apiRequest,
}) {
  const existingCategories = await listAllAdminCategories(adminToken, apiFn);
  const categoryBySlug = new Map(existingCategories.map((c) => [c.slug, c]));
  const categoryPlan = buildCategorySeedPlan(categories, products, shops);
  const rootStats = { created: 0, existing: 0, updated: 0 };
  const childStats = { created: 0, existing: 0, updated: 0 };

  const syncExisting = async (slug, existing, desired, statBucket) => {
    const patch = buildCategoryUpdatePatch(existing, desired);
    if (patch) {
      const updRes = await apiFn("PUT", `/api/v1/admin/categories/${existing.id}`, {
        token: adminToken,
        body: patch,
      });
      categoryBySlug.set(slug, updRes.data.category);
      statBucket.updated += 1;
    }
    statBucket.existing += 1;
  };

  for (const root of categoryPlan.roots) {
    const existing = categoryBySlug.get(root.slug);
    if (existing) {
      await syncExisting(
        root.slug,
        existing,
        { commission_rate_bps: root.commission_rate_bps },
        rootStats
      );
      continue;
    }

    const createRes = await apiFn("POST", "/api/v1/admin/categories", {
      token: adminToken,
      body: {
        name: root.name,
        slug: root.slug,
        parent_id: "",
        commission_rate_bps: root.commission_rate_bps,
        sort_order: root.sort_order,
      },
      allowStatuses: [409],
    });
    if (createRes.status === 409) {
      const refetched = await listAllAdminCategories(adminToken, apiFn);
      for (const c of refetched) {
        categoryBySlug.set(c.slug, c);
      }
      const conflicted = categoryBySlug.get(root.slug);
      if (conflicted) {
        await syncExisting(
          root.slug,
          conflicted,
          { commission_rate_bps: root.commission_rate_bps },
          rootStats
        );
      } else {
        rootStats.existing += 1;
      }
    } else {
      categoryBySlug.set(root.slug, createRes.data.category);
      rootStats.created += 1;
    }
  }

  for (const child of categoryPlan.children) {
    const parent = categoryBySlug.get(child.parentSlug);
    if (!parent) {
      throw new Error(`Missing parent category ${child.parentSlug} for child ${child.slug}`);
    }
    const existing = categoryBySlug.get(child.slug);
    if (existing) {
      await syncExisting(
        child.slug,
        existing,
        { parent_id: parent.id, commission_rate_bps: child.commission_rate_bps },
        childStats
      );
      continue;
    }

    const createRes = await apiFn("POST", "/api/v1/admin/categories", {
      token: adminToken,
      body: {
        name: child.name,
        slug: child.slug,
        parent_id: parent.id,
        commission_rate_bps: child.commission_rate_bps,
        sort_order: child.sort_order,
      },
      allowStatuses: [409],
    });
    if (createRes.status === 409) {
      const refetched = await listAllAdminCategories(adminToken, apiFn);
      for (const c of refetched) {
        categoryBySlug.set(c.slug, c);
      }
      const conflicted = categoryBySlug.get(child.slug);
      if (conflicted) {
        await syncExisting(
          child.slug,
          conflicted,
          { parent_id: parent.id, commission_rate_bps: child.commission_rate_bps },
          childStats
        );
      } else {
        childStats.existing += 1;
      }
    } else {
      categoryBySlug.set(child.slug, createRes.data.category);
      childStats.created += 1;
    }
  }

  return {
    categoryBySlug,
    categories: rootStats,
    childCategories: childStats,
  };
}

export async function listAllAdminProducts(adminToken) {
  const all = [];
  let page = 1;
  while (true) {
    const res = await apiRequest("GET", `/api/v1/admin/products?page=${page}&page_size=100`, {
      token: adminToken,
    });
    const items = Array.isArray(res.data?.items) ? res.data.items : [];
    all.push(...items);
    const total = Number(res.data?.total || items.length);
    if (all.length >= total || items.length === 0) break;
    page += 1;
  }
  return all;
}

/**
 * Seed active sample shop discount programs (UC-S18) using both percent & integer VND modes.
 */
export async function seedShopDiscountPrograms({
  sellerByStaticShopId,
  productByStaticId,
  liveFlashSkuIds = new Set(),
  nowMs = Date.now(),
  apiFn = apiRequest,
}) {
  const result = { created: 0, existing: 0 };
  const startsAt = new Date(nowMs - 60 * 60 * 1000).toISOString();
  const endsAt = new Date(nowMs + 30 * 24 * 60 * 60 * 1000).toISOString();

  for (const spec of SHOP_DISCOUNT_PROGRAM_SPECS) {
    const sellerEntry = sellerByStaticShopId.get(spec.shopStaticId);
    const backendProd = productByStaticId.get(spec.productStaticId);
    if (!sellerEntry || !backendProd) continue;

    const listRes = await apiFn("GET", "/api/v1/seller/discount-programs?page=1&page_size=100", {
      token: sellerEntry.token,
    });
    const existingList = Array.isArray(listRes.data?.items) ? listRes.data.items : [];
    const alreadyActive = existingList.find(
      (p) =>
        normalizeText(p.name) === normalizeText(spec.name) &&
        (p.status === "ACTIVE" || p.status === "SCHEDULED")
    );
    if (alreadyActive) {
      result.existing += 1;
      continue;
    }

    const candidateSkus = (backendProd.skus || [])
      .filter((s) => s.is_active !== false && getSkuAvailableStock(s) > 0 && !liveFlashSkuIds.has(s.id))
      .sort((a, b) => getSkuAvailableStock(b) - getSkuAvailableStock(a))
      .slice(0, spec.maxSkus || 2);

    if (candidateSkus.length === 0) continue;

    const items = candidateSkus.map((sku) => {
      if (spec.mode === "percent") {
        return {
          sku_id: sku.id,
          discount_percent: spec.discountPercent,
          per_user_limit: spec.perUserLimit,
        };
      }
      const regular = Math.trunc(Number(sku.price_vnd) || 0);
      const rounded = Math.floor((regular * (spec.priceRatio || 0.85)) / 1000) * 1000;
      const discountedPriceVnd = rounded > 0 && rounded < regular ? rounded : Math.max(1000, regular - 1000);
      return {
        sku_id: sku.id,
        discounted_price_vnd: discountedPriceVnd,
        per_user_limit: spec.perUserLimit,
      };
    });

    const createRes = await apiFn("POST", "/api/v1/seller/discount-programs", {
      token: sellerEntry.token,
      body: {
        name: spec.name,
        starts_at: startsAt,
        ends_at: endsAt,
        items,
      },
      allowStatuses: [409],
    });
    if (createRes.status === 409) {
      result.existing += 1;
    } else {
      result.created += 1;
    }
  }

  return result;
}

/**
 * Seed upcoming category & payment fee schedules (UC-A09).
 * Uses strictly future effective_from so baseline active payment fee (0 bps) stays unchanged.
 */
export async function seedFeeSchedules({
  adminToken,
  categoryBySlug,
  nowMs = Date.now(),
  apiFn = apiRequest,
}) {
  const categoryFees = { created: 0, existing: 0 };
  const paymentFees = { created: 0, existing: 0 };

  const effectiveFromIso = new Date(nowMs + 365 * 24 * 60 * 60 * 1000).toISOString();

  for (const spec of UPCOMING_CATEGORY_FEE_SPECS) {
    const cat = categoryBySlug.get(spec.categorySlug);
    if (!cat) continue;

    const listRes = await apiFn(
      "GET",
      `/api/v1/admin/fees/categories?category_id=${encodeURIComponent(cat.id)}&page=1&page_size=100`,
      { token: adminToken }
    );
    const existing = Array.isArray(listRes.data?.items) ? listRes.data.items : [];
    if (existing.some((item) => item.note === spec.note)) {
      categoryFees.existing += 1;
      continue;
    }

    await apiFn("POST", `/api/v1/admin/fees/categories/${encodeURIComponent(cat.id)}`, {
      token: adminToken,
      body: {
        commission_rate_bps: spec.commission_rate_bps,
        effective_from: effectiveFromIso,
        note: spec.note,
      },
    });
    categoryFees.created += 1;
  }

  const payFeeListRes = await apiFn("GET", "/api/v1/admin/fees/payment?include_history=true", {
    token: adminToken,
  });
  const payRecords = [
    ...(Array.isArray(payFeeListRes.data?.active_configs) ? payFeeListRes.data.active_configs : []),
    ...(Array.isArray(payFeeListRes.data?.history) ? payFeeListRes.data.history : []),
  ];

  for (const spec of UPCOMING_PAYMENT_FEE_SPECS) {
    const found = payRecords.some(
      (rec) => rec.payment_method === spec.payment_method && rec.note === spec.note
    );
    if (found) {
      paymentFees.existing += 1;
      continue;
    }

    await apiFn("POST", "/api/v1/admin/fees/payment", {
      token: adminToken,
      body: {
        payment_method: spec.payment_method,
        fee_bps: spec.fee_bps,
        effective_from: effectiveFromIso,
        note: spec.note,
      },
    });
    paymentFees.created += 1;
  }

  return { categoryFees, paymentFees };
}

async function pollOrderStatus(
  apiFn,
  token,
  orderId,
  allowedStatuses,
  { timeoutMs = 15000, intervalMs = 250 } = {}
) {
  const targetSet = new Set(allowedStatuses);
  const deadline = Date.now() + timeoutMs;
  let lastOrder = null;
  while (Date.now() <= deadline) {
    const res = await apiFn("GET", `/api/v1/orders/${encodeURIComponent(orderId)}`, { token });
    lastOrder = res.data?.order || null;
    if (lastOrder && targetSet.has(lastOrder.status)) {
      return lastOrder;
    }
    await sleep(intervalMs);
  }
  throw new Error(
    `Timed out waiting for order ${orderId} status in [${[...targetSet].join(", ")}]; last=${lastOrder?.status}`
  );
}

async function postSignedWebhook(apiFn, path, payload, secret) {
  const rawBody = JSON.stringify(payload);
  const sigHeaders = signWebhookPayload(secret, rawBody, Math.floor(Date.now() / 1000));
  return apiFn("POST", path, {
    body: payload,
    rawBody,
    headers: sigHeaders,
  });
}

function pickOrderSku(backendProd, preferredSkuCode) {
  const skus = Array.isArray(backendProd?.skus) ? backendProd.skus : [];
  if (preferredSkuCode) {
    const exact = skus.find((s) => s.sku_code === preferredSkuCode && s.is_active !== false);
    if (exact && getSkuAvailableStock(exact) > 0) return exact;
  }
  const active = skus
    .filter((s) => s.is_active !== false && getSkuAvailableStock(s) > 0)
    .sort((a, b) => getSkuAvailableStock(b) - getSkuAvailableStock(a));
  return active[0] || skus[0] || null;
}

/**
 * Seed realistic demo orders across all lifecycle states strictly via Gateway REST API + signed webhooks.
 * Idempotent on replay (reuses deterministic Idempotency-Key and skips already-reached states).
 */
export async function seedDemoOrders({
  buyerToken,
  buyerAddress,
  sellerByStaticShopId,
  productByStaticId,
  paymentWebhookSecret = PAYMENT_WEBHOOK_SECRET,
  shippingWebhookSecret = SHIPPING_WEBHOOK_SECRET,
  apiFn = apiRequest,
  pollIntervalMs = 250,
  pollTimeoutMs = 15000,
}) {
  const result = { created: 0, existing: 0, orders: [] };

  for (const spec of DEMO_ORDER_SPECS) {
    const sellerEntry = sellerByStaticShopId.get(spec.shopStaticId);
    const backendProd = productByStaticId.get(spec.productStaticId);
    if (!sellerEntry || !backendProd) {
      throw new Error(`Cannot seed demo order ${spec.key}: missing shop or product`);
    }
    const sku = pickOrderSku(backendProd, spec.preferredSkuCode);
    if (!sku) {
      throw new Error(`Cannot seed demo order ${spec.key}: no available SKU for ${spec.productStaticId}`);
    }

    const checkoutRes = await apiFn("POST", "/api/v1/checkout", {
      token: buyerToken,
      headers: { "Idempotency-Key": spec.idempotencyKey },
      body: {
        address_id: buyerAddress.id,
        from_cart: false,
        items: [{ sku_id: sku.id, quantity: spec.quantity }],
        payment_method: spec.paymentMethod,
        shipping_choices: [
          {
            shop_id: sellerEntry.backendShop.id,
            carrier_code: "ghn",
            service_code: "STANDARD",
          },
        ],
      },
      allowStatuses: [409],
    });

    if (checkoutRes.status === 409) {
      if (isIdempotencyKeyConflict(checkoutRes)) {
        console.warn(
          `  [warn] Skipping demo order ${spec.key} (${spec.idempotencyKey}): 409 IDEMPOTENCY_KEY_CONFLICT (payload drifted) — counting as existing`
        );
        result.existing += 1;
        result.orders.push({ key: spec.key, id: "", status: "EXISTING_CONFLICT" });
        continue;
      }
      const errCode = checkoutRes.data?.error?.code || "409";
      const errMsg = checkoutRes.data?.error?.message || "conflict";
      throw new Error(`POST /api/v1/checkout failed (HTTP 409): ${errCode}: ${errMsg}`);
    }
    if (checkoutRes.ok === false) {
      const errCode = checkoutRes.data?.error?.code || String(checkoutRes.status || "ERR");
      const errMsg = checkoutRes.data?.error?.message || "checkout failed";
      throw new Error(`POST /api/v1/checkout failed (HTTP ${checkoutRes.status}): ${errCode}: ${errMsg}`);
    }

    const isReplay = Boolean(checkoutRes.data?.idempotent_replay);
    const createdOrders = Array.isArray(checkoutRes.data?.orders) ? checkoutRes.data.orders : [];
    const baseOrder = createdOrders[0];
    if (!baseOrder?.id) {
      throw new Error(`Demo order ${spec.key}: checkout returned no orders`);
    }

    // Read live status so replays skip already-completed transitions cleanly.
    const liveRes = await apiFn("GET", `/api/v1/orders/${encodeURIComponent(baseOrder.id)}`, {
      token: buyerToken,
    });
    let order = liveRes.data?.order || baseOrder;

    if (order.status === spec.targetStatus) {
      result.existing += 1;
      result.orders.push({ key: spec.key, id: order.id, status: order.status });
      continue;
    }

    // Step 1: If VietQR & still PENDING, confirm payment via signed payment webhook.
    if (spec.targetStatus !== "CANCELLED" && order.status === "PENDING") {
      await postSignedWebhook(
        apiFn,
        "/webhooks/payment",
        {
          provider: "vietqr_mock",
          provider_tx_id: `SEED-PAY-${order.checkout_group_id || checkoutRes.data.checkout_group_id}`,
          payment_id: order.payment_id || checkoutRes.data.payment_id,
          order_group_id: order.checkout_group_id || checkoutRes.data.checkout_group_id,
          amount_vnd: order.total_payable_vnd || checkoutRes.data.total_payable_vnd,
          status: "SUCCEEDED",
          reference: `NEXUSMART ${order.checkout_group_id || checkoutRes.data.checkout_group_id}`,
          occurred_at: new Date().toISOString(),
        },
        paymentWebhookSecret
      );
      order = await pollOrderStatus(apiFn, buyerToken, order.id, ["CONFIRMED", "PREPARING", "SHIPPING", "DELIVERED", "COMPLETED", "RETURN_REQUESTED", "RETURNED"], {
        intervalMs: pollIntervalMs,
        timeoutMs: pollTimeoutMs,
      });
    }

    // Step 2: CANCELLED branch.
    if (spec.targetStatus === "CANCELLED") {
      if (order.status !== "CANCELLED") {
        const cancelRes = await apiFn("POST", `/api/v1/orders/${encodeURIComponent(order.id)}/cancel`, {
          token: buyerToken,
          body: { reason: spec.cancelReason || "Khách hủy đơn mẫu" },
        });
        order = cancelRes.data?.order || order;
      }
      if (isReplay) result.existing += 1;
      else result.created += 1;
      result.orders.push({ key: spec.key, id: order.id, status: order.status });
      continue;
    }

    // Step 3: Seller confirms order -> PREPARING.
    if (order.status === "CONFIRMED") {
      const confRes = await apiFn(
        "POST",
        `/api/v1/seller/orders/${encodeURIComponent(order.id)}/confirm`,
        { token: sellerEntry.token, body: {} }
      );
      order = confRes.data?.order || order;
    }

    if (spec.targetStatus === "PREPARING") {
      if (isReplay) result.existing += 1;
      else result.created += 1;
      result.orders.push({ key: spec.key, id: order.id, status: order.status });
      continue;
    }

    // Step 4: Seller creates shipment (attaches tracking_code while in PREPARING).
    if (order.status === "PREPARING" && !order.tracking_code) {
      const shipRes = await apiFn(
        "POST",
        `/api/v1/seller/orders/${encodeURIComponent(order.id)}/ship`,
        {
          token: sellerEntry.token,
          body: { carrier_code: "ghn", service_code: "STANDARD" },
        }
      );
      order = shipRes.data?.order || order;
      if (!order.tracking_code && shipRes.data?.tracking_code) {
        order.tracking_code = shipRes.data.tracking_code;
      }
    }

    // Step 5: Shipping webhook -> SHIPPING or DELIVERED.
    if (spec.targetStatus === "SHIPPING") {
      if (order.status === "PREPARING") {
        await postSignedWebhook(
          apiFn,
          "/webhooks/shipping/ghn",
          {
            event_id: `seed-ship-delivering-${order.id}`,
            tracking_code: order.tracking_code,
            order_id: order.id,
            raw_status: "delivering",
            description: "Bưu tá đang giao kiện hàng mẫu đến người nhận.",
            occurred_at: new Date().toISOString(),
          },
          shippingWebhookSecret
        );
        order = await pollOrderStatus(apiFn, buyerToken, order.id, ["SHIPPING"], {
          intervalMs: pollIntervalMs,
          timeoutMs: pollTimeoutMs,
        });
      }
      if (isReplay) result.existing += 1;
      else result.created += 1;
      result.orders.push({ key: spec.key, id: order.id, status: order.status });
      continue;
    }

    // Step 6: Deliver parcel for COMPLETED / RETURN_REQUESTED / RETURNED.
    if (order.status === "PREPARING" || order.status === "SHIPPING") {
      await postSignedWebhook(
        apiFn,
        "/webhooks/shipping/ghn",
        {
          event_id: `seed-ship-delivered-${order.id}`,
          tracking_code: order.tracking_code,
          order_id: order.id,
          raw_status: "delivered",
          description: "Giao hàng thành công (đơn mẫu).",
          occurred_at: new Date().toISOString(),
        },
        shippingWebhookSecret
      );
      order = await pollOrderStatus(apiFn, buyerToken, order.id, ["DELIVERED", "COMPLETED", "RETURN_REQUESTED", "RETURNED"], {
        intervalMs: pollIntervalMs,
        timeoutMs: pollTimeoutMs,
      });
    }

    // Step 7: COMPLETED branch (buyer confirms received -> triggers escrow & coins reward).
    if (spec.targetStatus === "COMPLETED") {
      if (order.status === "DELIVERED") {
        const compRes = await apiFn(
          "POST",
          `/api/v1/orders/${encodeURIComponent(order.id)}/confirm-received`,
          { token: buyerToken, body: {} }
        );
        order = compRes.data?.order || order;
      }
      if (isReplay) result.existing += 1;
      else result.created += 1;
      result.orders.push({ key: spec.key, id: order.id, status: order.status });
      continue;
    }

    // Step 8: Return workflow (RETURN_REQUESTED & RETURNED).
    let returnRequestId = order.return_request_id || "";
    if (order.status === "DELIVERED") {
      const retRes = await apiFn("POST", `/api/v1/orders/${encodeURIComponent(order.id)}/return`, {
        token: buyerToken,
        body: {
          reason: spec.returnReason || "Yêu cầu trả hàng đơn mẫu.",
          evidence_urls: spec.returnEvidenceUrls || [],
        },
      });
      order = retRes.data?.order || order;
      returnRequestId = retRes.data?.return_request_id || order.return_request_id || "";
    }

    if (spec.targetStatus === "RETURNED" && order.status === "RETURN_REQUESTED") {
      if (!returnRequestId) {
        const retGet = await apiFn("GET", `/api/v1/orders/${encodeURIComponent(order.id)}/return`, {
          token: buyerToken,
        });
        returnRequestId = retGet.data?.return_request?.id || "";
      }
      if (returnRequestId) {
        await apiFn(
          "POST",
          `/api/v1/seller/returns/${encodeURIComponent(returnRequestId)}/respond`,
          {
            token: sellerEntry.token,
            body: {
              decision: "ACCEPT_REFUND",
              note: spec.sellerReturnNote || "Gian hàng chấp nhận hoàn tiền đơn mẫu.",
            },
          }
        );
        order = await pollOrderStatus(apiFn, buyerToken, order.id, ["RETURNED"], {
          intervalMs: pollIntervalMs,
          timeoutMs: pollTimeoutMs,
        });
      }
    }

    if (isReplay) result.existing += 1;
    else result.created += 1;
    result.orders.push({ key: spec.key, id: order.id, status: order.status });
  }

  return result;
}

async function main() {
  console.log(`NexusMart seed starting against ${GATEWAY_URL} (Mailpit: ${MAILPIT_URL})`);

  // Load single-source-of-truth web sample data directly.
  const { SHOPS } = await import(pathToFileURL(resolve(WEB_SRC_DIR, "data/shops.ts")).href);
  const { CATEGORIES, PRODUCTS } = await import(
    pathToFileURL(resolve(WEB_SRC_DIR, "data/products.ts")).href
  );
  const { VOUCHERS, INITIAL_USER_COINS } = await import(
    pathToFileURL(resolve(WEB_SRC_DIR, "data/vouchers.ts")).href
  );
  const { FLASH_SALE_SLOTS } = await import(
    pathToFileURL(resolve(WEB_SRC_DIR, "data/flash-sale.ts")).href
  );
  const { DEMO_USER, INITIAL_ADDRESSES } = await import(
    pathToFileURL(resolve(WEB_SRC_DIR, "data/orders.ts")).href
  );

  // 1. Admin bootstrap login + shipping directory.
  const adminSession = await login(ADMIN_EMAIL, ADMIN_PASSWORD);
  const adminToken = adminSession.token;
  console.log(`Admin authenticated: ${adminSession.user.email} (${adminSession.user.role})`);

  const shippingDir = new ShippingDirectory();
  await shippingDir.load();

  // 2. Sellers & 4 Shops.
  let existingShops = await listAllAdminShops(adminToken);
  const sellerByStaticShopId = new Map();
  const sellerByBackendShopId = new Map();

  for (const staticShop of SHOPS) {
    const account = SELLER_ACCOUNT_BY_SHOP_ID[staticShop.id];
    if (!account) {
      throw new Error(`No seller demo account mapping for static shop ${staticShop.id}`);
    }

    let sellerSession = await ensureUser(adminToken, {
      fullName: account.fullName,
      email: account.email,
      phone: account.phone,
      password: SELLER_DEMO_PASSWORD,
    });
    if (sellerSession.created) {
      stats.sellers.created += 1;
    } else {
      stats.sellers.existing += 1;
    }

    const pickupAddress = await ensureDefaultAddress(sellerSession.token, shippingDir, {
      recipientName: staticShop.name,
      recipientPhone: account.phone,
      provinceNameHint: staticShop.city,
      wardHint: staticShop.warehouseAddress,
      streetAddress: staticShop.warehouseAddress,
    });

    let backendShop =
      existingShops.find((s) => s.owner_user_id === sellerSession.user.id) ||
      existingShops.find((s) => normalizeText(s.name) === normalizeText(staticShop.name)) ||
      null;

    if (!backendShop) {
      const regShopRes = await apiRequest("POST", "/api/v1/shops/register", {
        token: sellerSession.token,
        body: {
          name: staticShop.name,
          slug: staticShop.slug,
          description: staticShop.story,
          pickup_address_id: pickupAddress.id,
          contact_phone: account.phone,
          business_license_doc: `GPKD-SEED-${staticShop.id.toUpperCase()}`,
        },
        allowStatuses: [409],
      });
      if (regShopRes.status === 409) {
        existingShops = await listAllAdminShops(adminToken);
        backendShop =
          existingShops.find((s) => s.owner_user_id === sellerSession.user.id) ||
          existingShops.find((s) => normalizeText(s.name) === normalizeText(staticShop.name));
        stats.shops.existing += 1;
      } else {
        backendShop = regShopRes.data.shop;
        stats.shops.created += 1;
      }
    } else {
      stats.shops.existing += 1;
    }

    if (!backendShop) {
      throw new Error(`Failed to resolve shop ${staticShop.name}`);
    }

    if (backendShop.status !== "ACTIVE") {
      const reviewRes = await apiRequest("POST", `/api/v1/admin/shops/${backendShop.id}/review`, {
        token: adminToken,
        body: {
          decision: "APPROVE",
          reason: "Seed demo shop approval",
        },
      });
      backendShop = reviewRes.data.shop;
    }

    // Re-login after shop approval so JWT claims carry role=seller and shop_id.
    if (sellerSession.user.role !== "seller" || sellerSession.user.shop_id !== backendShop.id) {
      sellerSession = await login(account.email, SELLER_DEMO_PASSWORD);
    }
    if (sellerSession.user.role !== "seller" || sellerSession.user.shop_id !== backendShop.id) {
      throw new Error(
        `Seller ${account.email} expected role=seller & shop_id=${backendShop.id}, got role=${sellerSession.user.role} shop_id=${sellerSession.user.shop_id}`
      );
    }

    const entry = {
      staticShop,
      backendShop,
      account,
      token: sellerSession.token,
      user: sellerSession.user,
    };
    sellerByStaticShopId.set(staticShop.id, entry);
    sellerByBackendShopId.set(backendShop.id, entry);
  }

  // 3. Categories (CAT-D3: 6 roots with shop-aligned commission_rate_bps + 15 child categories).
  const categorySeedResult = await seedCategories({
    adminToken,
    categories: CATEGORIES,
    products: PRODUCTS,
    shops: SHOPS,
  });
  const { categoryBySlug } = categorySeedResult;
  stats.categories = categorySeedResult.categories;
  stats.childCategories = categorySeedResult.childCategories;

  // 4. Products (18 products with full SKUs, *_vnd prices, stock, weight_grams, relative images).
  const existingProducts = await listAllAdminProducts(adminToken);
  const existingProductByShopName = new Map(
    existingProducts.map((p) => [`${p.shop_id}::${normalizeText(p.name)}`, p])
  );
  const productByStaticId = new Map();

  for (const prod of PRODUCTS) {
    const sellerEntry = sellerByStaticShopId.get(prod.shopId);
    const category = categoryBySlug.get(prod.categorySlug);
    if (!sellerEntry || !category) {
      throw new Error(`Missing shop or category mapping for product ${prod.id}`);
    }

    const key = `${sellerEntry.backendShop.id}::${normalizeText(prod.name)}`;
    let backendProd = existingProductByShopName.get(key) || null;

    if (backendProd) {
      if (typeof backendProd.slug === "string" && backendProd.slug !== prod.slug) {
        const updRes = await apiRequest("PUT", `/api/v1/seller/products/${backendProd.id}`, {
          token: sellerEntry.token,
          body: { slug: prod.slug },
        });
        backendProd = updRes.data.product;
      }
      stats.products.existing += 1;
    } else {
      const colorByCode = new Map(prod.colors.map((c) => [c.code, c]));
      const weightGrams = estimateWeightGrams(prod);
      const skuInputs = prod.skus.map((sku) => {
        const color = colorByCode.get(sku.colorCode);
        const colorName = color?.name || sku.colorCode;
        return {
          sku_code: sku.skuCode,
          name: `${colorName} / ${sku.size}`,
          attributes: {
            color: colorName,
            color_code: sku.colorCode,
            size: sku.size,
          },
          price_vnd: sku.price,
          stock: sku.stock,
          weight_grams: weightGrams,
          image_url: color?.image || prod.image,
        };
      });

      const createRes = await apiRequest("POST", "/api/v1/seller/products", {
        token: sellerEntry.token,
        body: {
          category_id: category.id,
          name: prod.name,
          slug: prod.slug,
          description: prod.description,
          images: prod.gallery,
          skus: skuInputs,
        },
      });
      backendProd = createRes.data.product;
      stats.products.created += 1;
    }

    if (backendProd.status !== "APPROVED") {
      const reviewRes = await apiRequest("POST", `/api/v1/admin/products/${backendProd.id}/review`, {
        token: adminToken,
        body: {
          decision: "APPROVE",
          reason: "Seed demo catalog approval",
        },
      });
      backendProd = reviewRes.data.product;
    }

    productByStaticId.set(prod.id, backendProd);
  }

  // 5. Vouchers (platform + freeship via admin, shop via owning seller).
  // B15: Clean up retired false-promise membership/student vouchers if present on older volumes.
  const RETIRED_VOUCHER_CODES = new Set(["EDU15", "SINHNHAT50", "NEXUSVIP12"]);
  const voucherListRes = await apiRequest("GET", "/api/v1/admin/vouchers?include_inactive=true", {
    token: adminToken,
  });
  const existingVouchers = Array.isArray(voucherListRes.data?.vouchers)
    ? voucherListRes.data.vouchers
    : [];

  for (const existing of existingVouchers) {
    const codeUpper = String(existing?.code || "").toUpperCase();
    const statusUpper = String(existing?.status || "").toUpperCase();
    if (RETIRED_VOUCHER_CODES.has(codeUpper) && statusUpper !== "DELETED" && existing?.id) {
      await apiRequest("DELETE", `/api/v1/admin/vouchers/${encodeURIComponent(existing.id)}`, {
        token: adminToken,
        allowStatuses: [200, 404],
      });
    }
  }

  const existingVoucherCodes = new Set(existingVouchers.map((v) => String(v.code).toUpperCase()));

  const nowMs = Date.now();
  const voucherStartsAt = new Date(nowMs - 60 * 60 * 1000).toISOString();
  const minVoucherEndMs = nowMs + 30 * 24 * 60 * 60 * 1000;

  for (const v of VOUCHERS) {
    const codeUpper = v.code.toUpperCase();
    if (RETIRED_VOUCHER_CODES.has(codeUpper)) {
      continue;
    }
    if (existingVoucherCodes.has(codeUpper)) {
      stats.vouchers.existing += 1;
      continue;
    }

    const parsedEnd = Date.parse(v.expiresAt);
    const endsAt = new Date(
      Math.max(Number.isFinite(parsedEnd) ? parsedEnd : 0, minVoucherEndMs)
    ).toISOString();

    const payload = {
      code: v.code,
      name: v.title,
      scope: v.scope.toUpperCase(),
      discount_type: v.discountType.toUpperCase(),
      value_vnd: v.discountType === "fixed" ? v.discountValue : 0,
      discount_percent: v.discountType === "percent" ? v.discountValue : 0,
      max_discount_vnd: v.discountType === "percent" ? v.maxDiscount || 0 : 0,
      min_order_vnd: v.minOrderValue,
      quota: v.totalUses,
      per_user_limit: 5,
      budget_vnd: 0,
      starts_at: voucherStartsAt,
      ends_at: endsAt,
    };

    let createRes;
    if (v.scope === "shop") {
      const sellerEntry = sellerByStaticShopId.get(v.shopId);
      if (!sellerEntry) {
        throw new Error(`No seller found for shop voucher ${v.code} (${v.shopId})`);
      }
      createRes = await apiRequest("POST", "/api/v1/seller/vouchers", {
        token: sellerEntry.token,
        body: payload,
        allowStatuses: [409],
      });
    } else {
      createRes = await apiRequest("POST", "/api/v1/admin/vouchers", {
        token: adminToken,
        body: payload,
        allowStatuses: [409],
      });
    }

    if (createRes.status === 409) {
      stats.vouchers.existing += 1;
    } else {
      existingVoucherCodes.add(codeUpper);
      stats.vouchers.created += 1;
    }
  }

  // 6. Flash Sales (PRM-D1: highest available_stock SKU, total_slots <= available_stock).
  const fsListRes = await apiRequest("GET", "/api/v1/flash-sales?include_upcoming=true");
  const existingFlashSales = Array.isArray(fsListRes.data?.flash_sales)
    ? fsListRes.data.flash_sales
    : [];

  const existingLive = existingFlashSales
    .filter((fs) => {
      const s = Date.parse(fs.starts_at || "");
      const e = Date.parse(fs.ends_at || "");
      return Number.isFinite(s) && Number.isFinite(e) && s <= nowMs && nowMs < e;
    })
    .sort((a, b) => Date.parse(a.starts_at || "") - Date.parse(b.starts_at || ""));

  const existingUpcoming = existingFlashSales
    .filter((fs) => {
      const s = Date.parse(fs.starts_at || "");
      const e = Date.parse(fs.ends_at || "");
      return Number.isFinite(s) && Number.isFinite(e) && s > nowMs && e > s;
    })
    .sort((a, b) => Date.parse(a.starts_at || "") - Date.parse(b.starts_at || ""));

  const hourMs = 60 * 60 * 1000;
  const baseHourMs = Math.floor(nowMs / hourMs) * hourMs;
  const activeAndUpcomingSlots = FLASH_SALE_SLOTS.filter((slot) => slot.status !== "ended");
  const slotWindowOffsetsHours = [
    { startH: -1, endH: 12 },
    { startH: 12, endH: 18 },
    { startH: 18, endH: 24 },
  ];

  const liveFlashSkuIds = new Set();
  let prevEndMs = 0;

  for (let i = 0; i < activeAndUpcomingSlots.length; i += 1) {
    const slot = activeAndUpcomingSlots[i];
    const windowDef = slotWindowOffsetsHours[i] || {
      startH: 12 + i * 6,
      endH: 18 + i * 6,
    };

    let backendFlash = i === 0 ? existingLive[0] || null : existingUpcoming[i - 1] || null;
    if (backendFlash) {
      stats.flashSales.existing += 1;
      const existingEnd = Date.parse(backendFlash.ends_at || "");
      if (Number.isFinite(existingEnd)) {
        prevEndMs = Math.max(prevEndMs, existingEnd);
      }
    } else {
      const durationMs = (windowDef.endH - windowDef.startH) * hourMs;
      const startsMs =
        i === 0
          ? baseHourMs + windowDef.startH * hourMs
          : Math.max(baseHourMs + windowDef.startH * hourMs, prevEndMs);
      const endsMs = startsMs + durationMs;
      const slotName = `Flash Sale ${formatHmVn(startsMs)}–${formatHmVn(endsMs)}`;

      const createRes = await apiRequest("POST", "/api/v1/admin/flash-sales", {
        token: adminToken,
        body: {
          name: slotName,
          starts_at: new Date(startsMs).toISOString(),
          ends_at: new Date(endsMs).toISOString(),
        },
      });
      backendFlash = createRes.data.flash_sale;
      prevEndMs = Math.max(prevEndMs, endsMs);
      stats.flashSales.created += 1;
    }

    // List all items (including PENDING) in this flash sale so re-runs are idempotent.
    const slotItemsRes = await apiRequest(
      "GET",
      `/api/v1/admin/flash-sale-items?flash_sale_id=${encodeURIComponent(backendFlash.id)}&page=1&page_size=100`,
      { token: adminToken }
    );
    const slotItems = Array.isArray(slotItemsRes.data?.items)
      ? slotItemsRes.data.items
      : backendFlash.items || [];
    const existingBySkuId = new Map(slotItems.map((it) => [it.sku_id, it]));
    if (i === 0) {
      for (const it of slotItems) {
        if (it.status === "APPROVED" || !it.status) liveFlashSkuIds.add(it.sku_id);
      }
    }

    for (const item of slot.items) {
      const backendProd = productByStaticId.get(item.productId);
      if (!backendProd || !Array.isArray(backendProd.skus) || backendProd.skus.length === 0) {
        throw new Error(`Cannot seed flash sale item ${item.id}: product ${item.productId} missing`);
      }

      const existingForProduct = backendProd.skus
        .map((s) => existingBySkuId.get(s.id))
        .find((found) => Boolean(found));
      if (existingForProduct) {
        if (existingForProduct.status === "PENDING") {
          await apiRequest(
            "POST",
            `/api/v1/admin/flash-sale-items/${existingForProduct.id}/approve`,
            {
              token: adminToken,
              body: { approve: true, reason: "Seed demo flash sale approval" },
            }
          );
        }
        if (i === 0) liveFlashSkuIds.add(existingForProduct.sku_id);
        stats.flashSaleItems.existing += 1;
        continue;
      }

      const regPlan = computeFlashSaleRegistration(backendProd, item);
      if (!regPlan) {
        console.log(`  [skip] Flash sale item ${item.id}: no active SKU with available stock`);
        continue;
      }

      const sellerEntry = sellerByBackendShopId.get(backendProd.shop_id);
      if (!sellerEntry) {
        throw new Error(`Missing seller session for shop ${backendProd.shop_id}`);
      }

      const regRes = await apiRequest("POST", `/api/v1/seller/flash-sales/${backendFlash.id}/items`, {
        token: sellerEntry.token,
        body: {
          sku_id: regPlan.sku.id,
          flash_price_vnd: regPlan.flash_price_vnd,
          total_slots: regPlan.total_slots,
          per_user_limit: regPlan.per_user_limit,
        },
        allowStatuses: [409],
      });

      if (regRes.status === 409) {
        stats.flashSaleItems.existing += 1;
        continue;
      }

      const createdItem = regRes.data.item;
      await apiRequest("POST", `/api/v1/admin/flash-sale-items/${createdItem.id}/approve`, {
        token: adminToken,
        body: {
          approve: true,
          reason: "Seed demo flash sale approval",
        },
      });
      existingBySkuId.set(regPlan.sku.id, createdItem);
      if (i === 0) liveFlashSkuIds.add(regPlan.sku.id);
      stats.flashSaleItems.created += 1;
    }
  }

  // 7. Sample Shop Discount Programs (UC-S18).
  const discountProgResult = await seedShopDiscountPrograms({
    sellerByStaticShopId,
    productByStaticId,
    liveFlashSkuIds,
    nowMs,
  });
  stats.shopDiscountPrograms = discountProgResult;

  // 8. Sample Fee Schedules (UC-A09: upcoming category commission & payment fee schedules).
  const feeResult = await seedFeeSchedules({
    adminToken,
    categoryBySlug,
    nowMs,
  });
  stats.categoryFeeSchedules = feeResult.categoryFees;
  stats.paymentFeeSchedules = feeResult.paymentFees;

  // 9. Demo Buyer + valid default address + initial coins.
  const buyerSession = await ensureUser(adminToken, {
    fullName: DEMO_USER.fullName,
    email: DEMO_USER.email,
    phone: cleanPhone(DEMO_USER.phone),
    password: BUYER_DEMO_PASSWORD,
  });
  if (buyerSession.created) {
    stats.buyer.created += 1;
  } else {
    stats.buyer.existing += 1;
  }

  const defaultSampleAddr = INITIAL_ADDRESSES[0];
  const buyerDefaultAddress = await ensureDefaultAddress(buyerSession.token, shippingDir, {
    recipientName: defaultSampleAddr.recipientName,
    recipientPhone: cleanPhone(defaultSampleAddr.phone),
    provinceNameHint: defaultSampleAddr.provinceName,
    wardHint: defaultSampleAddr.wardName,
    streetAddress: defaultSampleAddr.streetDetail,
  });

  const initialCoins = Number.isInteger(DEMO_USER.coins) ? DEMO_USER.coins : INITIAL_USER_COINS;
  if (initialCoins > 0) {
    const coinRes = await apiRequest("POST", "/api/v1/admin/coins/credit", {
      token: adminToken,
      body: {
        user_id: buyerSession.user.id,
        amount: initialCoins,
        reason: "Seed demo buyer initial coins",
        reference_id: "seed:demo-buyer:initial-coins:v1",
      },
    });
    if (coinRes.data?.already_credited) {
      stats.coins.existing += 1;
    } else {
      stats.coins.created += 1;
    }
  }

  // 9b. Verify demo buyer phone via real OTP flow before COD demo orders (E19).
  const phoneStatus = await ensureBuyerPhoneVerified({
    buyerToken: buyerSession.token,
    buyerEmail: DEMO_USER.email,
  });
  stats.buyerPhoneVerified[phoneStatus] += 1;

  // 10. Sample Orders in multiple states via real Gateway REST + signed webhooks (zero DB writes).
  const demoOrderResult = await seedDemoOrders({
    buyerToken: buyerSession.token,
    buyerAddress: buyerDefaultAddress,
    sellerByStaticShopId,
    productByStaticId,
    paymentWebhookSecret: PAYMENT_WEBHOOK_SECRET,
    shippingWebhookSecret: SHIPPING_WEBHOOK_SECRET,
  });
  stats.demoOrders = { created: demoOrderResult.created, existing: demoOrderResult.existing };

  // 11. Slug capability check against live backend responses.
  const sampleBackendProd = productByStaticId.values().next().value;
  const sampleBackendShop = sellerByStaticShopId.values().next().value?.backendShop;
  const productHasSlug = Boolean(sampleBackendProd && "slug" in sampleBackendProd);
  const shopHasSlug = Boolean(sampleBackendShop && "slug" in sampleBackendShop);
  let productSlugMatches = 0;
  for (const prod of PRODUCTS) {
    if (productByStaticId.get(prod.id)?.slug === prod.slug) productSlugMatches += 1;
  }
  let shopSlugMatches = 0;
  for (const staticShop of SHOPS) {
    if (sellerByStaticShopId.get(staticShop.id)?.backendShop?.slug === staticShop.slug) {
      shopSlugMatches += 1;
    }
  }

  // 12. Print summary and demo account reference (never log secrets, tokens, or signatures).
  console.log("\n=== Seed Summary ===");
  const summaryRows = [
    ["Sellers", stats.sellers],
    ["Shops", stats.shops],
    ["Demo buyer", stats.buyer],
    ["Buyer phone verified", stats.buyerPhoneVerified],
    ["Addresses", stats.addresses],
    ["Buyer initial coins", stats.coins],
    ["Root categories", stats.categories],
    ["Child categories", stats.childCategories],
    ["Products", stats.products],
    ["Vouchers", stats.vouchers],
    ["Flash sale windows", stats.flashSales],
    ["Flash sale items", stats.flashSaleItems],
    ["Shop discount progs", stats.shopDiscountPrograms],
    ["Category fee scheds", stats.categoryFeeSchedules],
    ["Payment fee scheds", stats.paymentFeeSchedules],
    ["Demo orders", stats.demoOrders],
  ];
  for (const [label, counts] of summaryRows) {
    const upd = counts.updated ? `  updated=${counts.updated}` : "";
    console.log(
      `  ${label.padEnd(22)} created=${counts.created}  existing=${counts.existing}${upd}`
    );
  }
  console.log(
    `  Demo order states: ${demoOrderResult.orders.map((o) => `${o.key}=${o.status}`).join(", ")}`
  );
  console.log(
    `  Backend slug fields: product.slug=${productHasSlug} (${productSlugMatches}/${PRODUCTS.length} matched), shop.slug=${shopHasSlug} (${shopSlugMatches}/${SHOPS.length} matched)`
  );

  console.log("\n=== Demo Accounts (Dev Only — see tools/seed/README.md for dev passwords) ===");
  console.log("| Role | Email | Shop / Notes |");
  console.log("| --- | --- | --- |");
  console.log(`| admin | ${ADMIN_EMAIL} | Bootstrap admin |`);
  console.log(
    `| buyer | ${DEMO_USER.email} | ${DEMO_USER.fullName} (phone verified, ${initialCoins} xu, default address, ${demoOrderResult.orders.length} demo orders) |`
  );
  for (const staticShop of SHOPS) {
    const entry = sellerByStaticShopId.get(staticShop.id);
    console.log(
      `| seller | ${entry.account.email} | ${staticShop.name} (shop_id=${entry.backendShop.id}) |`
    );
  }
}

if (process.argv[1] && resolve(process.argv[1]) === __filename) {
  main().catch((err) => {
    console.error("Seed failed:", err);
    process.exit(1);
  });
}
