#!/usr/bin/env node
// Idempotent NexusMart backend seeder via Gateway REST API (zero direct DB writes).
// Single source of truth: imports web/src/data/* directly using Node 24 TypeScript type stripping.

import { spawnSync } from "node:child_process";
import crypto from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { registerHooks } from "node:module";
import { homedir } from "node:os";
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
const envStr = (key, fallback = "") => (process.env[key] ?? "").trim() || fallback;
const GATEWAY_URL = (
  envStr("SEED_GATEWAY_URL") ||
  envStr("GATEWAY_URL") ||
  `http://localhost:${envStr("GATEWAY_PORT") || dotEnv.GATEWAY_PORT || "8080"}`
).replace(/\/+$/, "");
const MAILPIT_URL = (
  envStr("MAILPIT_URL") ||
  `http://localhost:${envStr("MAILPIT_UI_PORT") || dotEnv.MAILPIT_UI_PORT || "8025"}`
).replace(/\/+$/, "");
const ADMIN_EMAIL =
  envStr("ADMIN_BOOTSTRAP_EMAIL") ||
  envStr("ADMIN_EMAIL") ||
  dotEnv.ADMIN_BOOTSTRAP_EMAIL ||
  "admin@nexusmart.local";
const ADMIN_PASSWORD =
  envStr("ADMIN_BOOTSTRAP_PASSWORD") ||
  envStr("ADMIN_PASSWORD") ||
  dotEnv.ADMIN_BOOTSTRAP_PASSWORD ||
  "AdminDevPassword123!";
const PAYMENT_WEBHOOK_SECRET =
  envStr("PAYMENT_WEBHOOK_SECRET") ||
  dotEnv.PAYMENT_WEBHOOK_SECRET ||
  "dev-only-payment-webhook-hmac-secret";
const SHIPPING_WEBHOOK_SECRET =
  envStr("SHIPPING_WEBHOOK_SECRET") ||
  dotEnv.SHIPPING_WEBHOOK_SECRET ||
  "dev-only-shipping-webhook-hmac-secret";
const WEB_BASE_URL = (
  envStr("WEB_BASE_URL") ||
  dotEnv.WEB_BASE_URL ||
  `http://localhost:${envStr("WEB_PORT") || dotEnv.WEB_PORT || "3300"}`
).replace(/\/+$/, "");
const SITE_ROOT_DOMAIN = (
  envStr("SITE_ROOT_DOMAIN") ||
  dotEnv.SITE_ROOT_DOMAIN ||
  "nexusmart.localhost"
).toLowerCase();

const rawSeedCatalogCount = process.env.SEED_CATALOG_COUNT;
const parsedSeedCatalogCount =
  rawSeedCatalogCount !== undefined && rawSeedCatalogCount.trim() !== ""
    ? Number.parseInt(rawSeedCatalogCount.trim(), 10)
    : 20000;
const SEED_CATALOG_COUNT =
  Number.isFinite(parsedSeedCatalogCount) && parsedSeedCatalogCount >= 0
    ? parsedSeedCatalogCount
    : 20000;

// Dev-only demo credentials (not real secrets; documented in tools/seed/README.md).
const SELLER_DEMO_PASSWORD = envStr("SEED_SELLER_PASSWORD", "SellerDemo123!");
const BUYER_DEMO_PASSWORD = envStr("SEED_BUYER_PASSWORD", "BuyerDemo123!");
const DEMO_BUYER_EMAIL = envStr("SEED_BUYER_EMAIL", "buyer@nexusmart.local");
const DEMO_BUYER_PHONE = envStr("SEED_BUYER_PHONE", "");

const SELLER_ACCOUNT_BY_SHOP_ID = {
  "shop-bach-dang": {
    email: envStr("SEED_SELLER_EMAIL", "seller@nexusmart.local"),
    phone: "0901234511",
    fullName: "Quản lý Xưởng Dệt Bạch Đằng",
  },
  "shop-sai-gon": {
    email: envStr("SEED_SELLER_SAIGON_EMAIL", "seller-saigon@nexusmart.local"),
    phone: "0901234512",
    fullName: "Quản lý Kho Basic Sài Gòn",
  },
  "shop-da-nang": {
    email: envStr("SEED_SELLER_DANANG_EMAIL", "seller-danang@nexusmart.local"),
    phone: "0901234513",
    fullName: "Quản lý Sợi Tự Nhiên Đà Nẵng",
  },
  "shop-ha-noi": {
    email: envStr("SEED_SELLER_HANOI_EMAIL", "seller-hanoi@nexusmart.local"),
    phone: "0901234514",
    fullName: "Quản lý Dệt Kim Hà Nội",
  },
};

const CATALOG_SELLER = {
  email: envStr("SEED_SELLER_CATALOG_EMAIL", "seller-catalog@nexusmart.local"),
  phone: envStr("SEED_SELLER_CATALOG_PHONE", "0901234515"),
  fullName: "Phạm Minh Tuấn",
  password: SELLER_DEMO_PASSWORD,
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
  sites: { created: 0, existing: 0 },
  chatThreads: { created: 0, existing: 0 },
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

async function listAllSellerProducts(sellerToken, apiFn = apiRequest) {
  const all = [];
  let page = 1;
  while (true) {
    const res = await apiFn("GET", `/api/v1/seller/products?page=${page}&page_size=100`, {
      token: sellerToken,
    });
    const items = Array.isArray(res.data?.items)
      ? res.data.items
      : Array.isArray(res.data?.products)
        ? res.data.products
        : [];
    all.push(...items);
    const total = Number(res.data?.total || items.length);
    if (all.length >= total || items.length === 0) break;
    page += 1;
  }
  return all;
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
        sales_channel: "STOREFRONT",
        storefront_shop_id: sellerEntry.backendShop.id,
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

function buildShowcaseSiteSpecs(productByStaticId) {
  const productUuid = (mockId) => productByStaticId.get(mockId)?.id ?? "";
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

async function fetchPublishedSiteConfig(host, apiFn) {
  if (!host) return null;
  const res = await apiFn("GET", `/api/v1/sites/by-host?host=${encodeURIComponent(host)}`, {
    allowStatuses: [404],
  });
  if (res.status !== 200 || res.data?.site?.status !== "PUBLISHED") return null;
  return res.data.site.config ?? null;
}

async function ensurePublishedSite(sellerToken, spec, siteRootDomain, apiFn) {
  const getRes = await apiFn("GET", "/api/v1/seller/site", {
    token: sellerToken,
    allowStatuses: [404],
  });
  const existing = getRes.status === 200 ? (getRes.data?.site ?? null) : null;
  const existingSubdomain = String(existing?.subdomain ?? "").trim().toLowerCase();
  const targetSubdomain = spec.subdomain.trim().toLowerCase();
  const targetHost = `${targetSubdomain}.${siteRootDomain}`;

  if (existing?.status === "SUSPENDED") {
    throw new Error(
      `Site ${existingSubdomain || targetSubdomain} is SUSPENDED by admin; unsuspend it before re-running seed`
    );
  }

  const subdomainMatches = existing !== null && existingSubdomain === targetSubdomain;
  if (subdomainMatches && existing.status === "PUBLISHED") {
    const existingPrimaryHost = String(existing.primary_host ?? "").trim().toLowerCase();
    let publishedCfg = await fetchPublishedSiteConfig(existingPrimaryHost || targetHost, apiFn);
    if (!publishedCfg && existingPrimaryHost && existingPrimaryHost !== targetHost) {
      publishedCfg = await fetchPublishedSiteConfig(targetHost, apiFn);
    }
    if (siteConfigsEqual(publishedCfg, spec.config)) {
      return { created: false, site: existing };
    }
  }

  const draftMatches = subdomainMatches && siteConfigsEqual(existing.config, spec.config);
  if (!draftMatches) {
    await apiFn("PUT", "/api/v1/seller/site/draft", {
      token: sellerToken,
      body: {
        subdomain: targetSubdomain,
        config: spec.config,
      },
    });
  }

  const pubRes = await apiFn("POST", "/api/v1/seller/site/publish", {
    token: sellerToken,
    body: {
      note: spec.publishNote,
    },
  });

  const publishedSite = pubRes.data?.site ?? { subdomain: targetSubdomain, status: "PUBLISHED" };
  return { created: true, site: publishedSite };
}

async function ensureCustomDomain(sellerToken, { hostname, expectStatus }, apiFn) {
  const targetHost = String(hostname ?? "").trim().toLowerCase();
  const desiredStatus = String(expectStatus ?? "").trim().toUpperCase();
  if (!targetHost) {
    throw new Error("ensureCustomDomain requires a non-empty hostname");
  }

  const listRes = await apiFn("GET", "/api/v1/seller/site/domains", {
    token: sellerToken,
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
      await apiFn("DELETE", `/api/v1/seller/site/domains/${encodeURIComponent(stale.id)}`, {
        token: sellerToken,
        allowStatuses: [204],
      });
    }

    const addRes = await apiFn("POST", "/api/v1/seller/site/domains", {
      token: sellerToken,
      body: { hostname: targetHost },
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
      const checkRes = await apiFn(
        "POST",
        `/api/v1/seller/site/domains/${encodeURIComponent(domain.id)}/check`,
        { token: sellerToken }
      );
      domain = checkRes.data?.domain ?? domain;
    }
    if (statusOf(domain) !== "ACTIVE") {
      throw new Error(
        `Custom domain ${targetHost} expected status ACTIVE, got ${domain?.status ?? "unknown"}`
      );
    }

    const activeHost = String(domain.host || targetHost).trim().toLowerCase();
    const byHostRes = await apiFn(
      "GET",
      `/api/v1/sites/by-host?host=${encodeURIComponent(activeHost)}`
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

  if (desiredStatus === "PENDING" && statusOf(domain) !== "PENDING") {
    throw new Error(
      `Custom domain ${targetHost} expected status PENDING, got ${domain?.status ?? "unknown"}`
    );
  }

  return { created, domain, primaryHost: null };
}

/**
 * Seeds the 4 published storefront subdomain sites, upgrades them to schema_version=2 templates
 * when available, and configures custom domains on ha-noi (ACTIVE) and da-nang (PENDING).
 */
export async function seedShowcaseSites({
  sellerByStaticShopId,
  productByStaticId,
  siteRootDomain = SITE_ROOT_DOMAIN,
  apiFn = apiRequest,
}) {
  const showcaseSiteSpecs = buildShowcaseSiteSpecs(productByStaticId);
  const templates = { skipped: true, available: 0, applied: 0 };

  const tplRes = await apiFn("GET", "/api/v1/site-templates", {
    allowStatuses: [404, 502, 503],
  });
  if (tplRes.status === 200 && Array.isArray(tplRes.data?.templates) && tplRes.data.templates.length > 0) {
    const templatesByKey = new Map();
    for (const tpl of tplRes.data.templates) {
      if (tpl?.slug) templatesByKey.set(String(tpl.slug).trim().toLowerCase(), tpl);
      if (tpl?.id) templatesByKey.set(String(tpl.id).trim().toLowerCase(), tpl);
    }
    templates.skipped = false;
    templates.available = tplRes.data.templates.length;
    templates.applied = applyTemplatesToShowcaseSpecs(showcaseSiteSpecs, templatesByKey);
  }

  const result = { created: 0, existing: 0, templates, sites: {} };
  for (const spec of showcaseSiteSpecs) {
    const sellerEntry = sellerByStaticShopId.get(spec.mockShopId);
    if (!sellerEntry) {
      throw new Error(`Missing seeded shop for site ${spec.subdomain} (${spec.mockShopId})`);
    }
    const { created, site } = await ensurePublishedSite(
      sellerEntry.token,
      spec,
      siteRootDomain,
      apiFn
    );
    if (created) result.created += 1;
    else result.existing += 1;

    const subdomainHost = `${spec.subdomain}.${siteRootDomain}`;
    const siteSummary = {
      shop_id: sellerEntry.backendShop.id,
      subdomain: spec.subdomain,
      primary_host: site.primary_host || subdomainHost,
      status: site.status || "PUBLISHED",
      schema_version: spec.config.schema_version ?? 1,
    };
    if (spec.customDomain) {
      const custom = await ensureCustomDomain(sellerEntry.token, spec.customDomain, apiFn);
      siteSummary.custom_domain = custom.domain.host || spec.customDomain.hostname;
      siteSummary.custom_domain_status = custom.domain.status;
      if (custom.primaryHost) {
        siteSummary.primary_host = custom.primaryHost;
      } else if (spec.customDomain.expectStatus === "PENDING") {
        siteSummary.primary_host = subdomainHost;
      }
    }
    result.sites[spec.subdomain] = siteSummary;
  }

  return result;
}

/**
 * Seeds Chat auto-reply configs and realistic 3-message Vietnamese threads on
 * `shop-bach-dang` and `shop-sai-gon` for the demo buyer. Gracefully skips on 404/502/503.
 */
export async function seedChatSampleData({
  buyerToken,
  sellerByStaticShopId,
  productByStaticId,
  siteRootDomain = SITE_ROOT_DOMAIN,
  apiFn = apiRequest,
}) {
  const probe = await apiFn("GET", "/api/v1/chat/conversations?page=1&page_size=1", {
    token: buyerToken,
    allowStatuses: [404, 502, 503],
  });
  if (probe.status !== 200) {
    return { skipped: true, status: probe.status, created: 0, existing: 0 };
  }

  const result = {
    skipped: false,
    created: 0,
    existing: 0,
    autoRepliesConfigured: 0,
    autoRepliesReused: 0,
    messagesSent: 0,
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
    const sellerEntry = sellerByStaticShopId.get(cfg.mockShopId);
    const prod = productByStaticId.get(cfg.mockProductId);
    if (!sellerEntry || !prod) continue;

    const arGet = await apiFn("GET", "/api/v1/seller/chat/auto-reply", {
      token: sellerEntry.token,
    });
    const alreadyEnabled = Boolean(arGet.data?.config?.enabled);
    if (!alreadyEnabled) {
      await apiFn("PUT", "/api/v1/seller/chat/auto-reply", {
        token: sellerEntry.token,
        body: cfg.autoReply,
      });
      result.autoRepliesConfigured += 1;
    } else {
      result.autoRepliesReused += 1;
    }

    const siteHost = `${cfg.subSlug}.${siteRootDomain}`;
    const convRes = await apiFn("POST", "/api/v1/chat/conversations", {
      token: buyerToken,
      body: {
        shop_id: sellerEntry.backendShop.id,
        site_host: siteHost,
      },
    });
    const conv = convRes.data?.conversation;
    if (!conv?.id) {
      throw new Error(`POST /api/v1/chat/conversations for ${cfg.subSlug} returned no conversation`);
    }

    const msgListRes = await apiFn(
      "GET",
      `/api/v1/chat/conversations/${encodeURIComponent(conv.id)}/messages?limit=20`,
      { token: buyerToken }
    );
    const existingItems = Array.isArray(msgListRes.data?.items) ? msgListRes.data.items : [];
    if (existingItems.length === 0) {
      const messagesToSend = [
        {
          token: buyerToken,
          body: {
            kind: "text",
            body: cfg.buyerQuestion,
            client_msg_id: `seed-chat-${cfg.subSlug}-1`,
          },
        },
        {
          token: buyerToken,
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
          token: sellerEntry.token,
          body: {
            kind: "text",
            body: cfg.sellerReply,
            client_msg_id: `seed-chat-${cfg.subSlug}-3`,
          },
        },
      ];
      for (const msg of messagesToSend) {
        await apiFn(
          "POST",
          `/api/v1/chat/conversations/${encodeURIComponent(conv.id)}/messages`,
          { token: msg.token, body: msg.body }
        );
        result.messagesSent += 1;
      }
      result.created += 1;
    } else {
      result.existing += 1;
    }

    result.shops[cfg.subSlug] = { conversation_id: conv.id };
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

async function seedLargeCatalog({ adminToken, catalogSeller, targetCount }) {
  const beforeRes = await apiRequest("GET", "/api/v1/admin/products?status=APPROVED&page=1&page_size=1", {
    token: adminToken,
  });
  const existingTotal = Math.trunc(beforeRes.data?.total ?? 0);

  if (targetCount <= 0) {
    console.log(`  [catalog] disabled (SEED_CATALOG_COUNT=0; current approved products: ${existingTotal})`);
    return { action: "disabled", target: 0, existing: existingTotal, total_after: existingTotal };
  }
  if (existingTotal >= targetCount) {
    console.log(
      `  [catalog] reused (${existingTotal} >= ${targetCount} approved products; skipping catalog-gen)`
    );
    return { action: "reused", target: targetCount, existing: existingTotal, total_after: existingTotal };
  }

  const remaining = targetCount - existingTotal;
  const catalogSeed = existingTotal > 18 ? 20261121 + existingTotal : 20261121;
  const explicitHostGRPC = envStr("PRODUCT_GRPC_ADDR");
  const useDockerCompose = !explicitHostGRPC && hasDockerCompose();

  const commonFlags = [
    "--mode=grpc",
    `--seller-emails=${catalogSeller.email}`,
    `--seller-password=${catalogSeller.password}`,
    `--admin-email=${ADMIN_EMAIL}`,
    `--admin-password=${ADMIN_PASSWORD}`,
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
    const composeGateway = envStr("SEED_CATALOG_GATEWAY_URL", "http://gateway:8080");
    const composeGRPC = envStr("SEED_CATALOG_GRPC_ADDR", "product:50052");

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

  const afterRes = await apiRequest("GET", "/api/v1/admin/products?status=APPROVED&page=1&page_size=1", {
    token: adminToken,
  });
  const totalAfter = Math.trunc(afterRes.data?.total ?? existingTotal + remaining);
  return {
    action: "generated",
    runner: runnerMode,
    target: targetCount,
    existing: existingTotal,
    requested: remaining,
    total_after: totalAfter,
  };
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

  // 2b. Dedicated 5th seller & ACTIVE catalog shop (NO published site, keeps 4 showcase sites clean).
  let catalogSellerSession = await ensureUser(adminToken, {
    fullName: CATALOG_SELLER.fullName,
    email: CATALOG_SELLER.email,
    phone: CATALOG_SELLER.phone,
    password: CATALOG_SELLER.password,
  });
  if (catalogSellerSession.created) {
    stats.sellers.created += 1;
  } else {
    stats.sellers.existing += 1;
  }
  const catalogPickupAddress = await ensureDefaultAddress(catalogSellerSession.token, shippingDir, {
    recipientName: CATALOG_SELLER.fullName,
    recipientPhone: CATALOG_SELLER.phone,
    provinceNameHint: CATALOG_SHOP_SPEC.city,
    wardHint: CATALOG_SHOP_SPEC.warehouseAddress,
    streetAddress: CATALOG_SHOP_SPEC.warehouseAddress,
  });
  let catalogShop =
    existingShops.find((s) => s.owner_user_id === catalogSellerSession.user.id) ||
    existingShops.find((s) => normalizeText(s.name) === normalizeText(CATALOG_SHOP_SPEC.name)) ||
    null;
  if (!catalogShop) {
    const regCatShop = await apiRequest("POST", "/api/v1/shops/register", {
      token: catalogSellerSession.token,
      body: {
        name: CATALOG_SHOP_SPEC.name,
        slug: CATALOG_SHOP_SPEC.slug,
        description: CATALOG_SHOP_SPEC.description,
        pickup_address_id: catalogPickupAddress.id,
        contact_phone: CATALOG_SELLER.phone,
        business_license_doc: `GPKD-SEED-${CATALOG_SHOP_SPEC.slug.toUpperCase()}`,
      },
      allowStatuses: [409],
    });
    if (regCatShop.status === 409) {
      existingShops = await listAllAdminShops(adminToken);
      catalogShop =
        existingShops.find((s) => s.owner_user_id === catalogSellerSession.user.id) ||
        existingShops.find((s) => normalizeText(s.name) === normalizeText(CATALOG_SHOP_SPEC.name));
      stats.shops.existing += 1;
    } else {
      catalogShop = regCatShop.data.shop;
      stats.shops.created += 1;
    }
  } else {
    stats.shops.existing += 1;
  }
  if (!catalogShop) {
    throw new Error(`Failed to resolve catalog shop ${CATALOG_SHOP_SPEC.name}`);
  }
  if (catalogShop.status !== "ACTIVE") {
    const revCat = await apiRequest("POST", `/api/v1/admin/shops/${catalogShop.id}/review`, {
      token: adminToken,
      body: { decision: "APPROVE", reason: "Seed catalog warehouse approval" },
    });
    catalogShop = revCat.data.shop;
  }
  if (catalogSellerSession.user.role !== "seller" || catalogSellerSession.user.shop_id !== catalogShop.id) {
    catalogSellerSession = await login(CATALOG_SELLER.email, CATALOG_SELLER.password);
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

  // 4. Products (18 showcase products; query per showcase shop so 20k catalog products don't slow re-runs).
  const existingProductByShopName = new Map();
  for (const sellerEntry of sellerByStaticShopId.values()) {
    const shopProds = await listAllSellerProducts(sellerEntry.token);
    for (const p of shopProds) {
      existingProductByShopName.set(`${sellerEntry.backendShop.id}::${normalizeText(p.name)}`, p);
    }
  }
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
  const demoBuyerPhone = DEMO_BUYER_PHONE || cleanPhone(DEMO_USER.phone) || "0901234501";
  const buyerSession = await ensureUser(adminToken, {
    fullName: DEMO_USER.fullName,
    email: DEMO_BUYER_EMAIL,
    phone: demoBuyerPhone,
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
    buyerEmail: DEMO_BUYER_EMAIL,
  });
  stats.buyerPhoneVerified[phoneStatus] += 1;

  // 10. Sample Orders in multiple states via real Gateway REST + signed webhooks (STOREFRONT channel + main returns API).
  const demoOrderResult = await seedDemoOrders({
    buyerToken: buyerSession.token,
    buyerAddress: buyerDefaultAddress,
    sellerByStaticShopId,
    productByStaticId,
    paymentWebhookSecret: PAYMENT_WEBHOOK_SECRET,
    shippingWebhookSecret: SHIPPING_WEBHOOK_SECRET,
  });
  stats.demoOrders = { created: demoOrderResult.created, existing: demoOrderResult.existing };

  // 11. 4 Published Showcase Subdomain Sites (+ v2 templates + custom domains).
  const siteResult = await seedShowcaseSites({
    sellerByStaticShopId,
    productByStaticId,
    siteRootDomain: SITE_ROOT_DOMAIN,
  });
  stats.sites = { created: siteResult.created, existing: siteResult.existing };

  // 12. Chat sample data (auto-reply + 3-message Vietnamese threads on bach-dang & sai-gon).
  const chatResult = await seedChatSampleData({
    buyerToken: buyerSession.token,
    sellerByStaticShopId,
    productByStaticId,
    siteRootDomain: SITE_ROOT_DOMAIN,
  });
  stats.chatThreads = { created: chatResult.created, existing: chatResult.existing };

  // 13. Large catalog (default 20,000 approved products via tools/catalog-gen on 5th catalog shop; set SEED_CATALOG_COUNT=0 to skip).
  const catalogResult = await seedLargeCatalog({
    adminToken,
    catalogSeller: CATALOG_SELLER,
    targetCount: SEED_CATALOG_COUNT,
  });

  // 14. Slug capability check against live backend responses.
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

  // 15. Print summary and demo account reference (never log secrets, tokens, or signatures).
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
    ["Published sites", stats.sites],
    ["Chat sample threads", stats.chatThreads],
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
    `  Sites: root_domain=${SITE_ROOT_DOMAIN}, templates_applied=${siteResult.templates.applied}, chat_skipped=${chatResult.skipped}, catalog=${catalogResult.action} (total=${catalogResult.total_after})`
  );
  console.log(
    `  Backend slug fields: product.slug=${productHasSlug} (${productSlugMatches}/${PRODUCTS.length} matched), shop.slug=${shopHasSlug} (${shopSlugMatches}/${SHOPS.length} matched)`
  );

  console.log("\n=== Demo Accounts (Dev Only — see tools/seed/README.md for dev passwords) ===");
  console.log("| Role | Email | Shop / Notes |");
  console.log("| --- | --- | --- |");
  console.log(`| admin | ${ADMIN_EMAIL} | Bootstrap admin |`);
  console.log(
    `| buyer | ${DEMO_BUYER_EMAIL} | ${DEMO_USER.fullName} (phone verified, ${initialCoins} xu, default address, ${demoOrderResult.orders.length} demo orders) |`
  );
  for (const staticShop of SHOPS) {
    const entry = sellerByStaticShopId.get(staticShop.id);
    console.log(
      `| seller | ${entry.account.email} | ${staticShop.name} (shop_id=${entry.backendShop.id}) |`
    );
  }
  console.log(
    `| seller | ${CATALOG_SELLER.email} | ${CATALOG_SHOP_SPEC.name} (shop_id=${catalogShop.id}, no published site) |`
  );
}

if (process.argv[1] && resolve(process.argv[1]) === __filename) {
  main().catch((err) => {
    console.error("Seed failed:", err);
    process.exit(1);
  });
}
