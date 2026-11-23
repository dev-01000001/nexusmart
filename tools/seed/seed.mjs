#!/usr/bin/env node
// Idempotent NexusMart backend seeder via Gateway REST API.
// Single source of truth: imports web/src/data/* directly using Node 24 TypeScript type stripping.

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

const stats = {
  sellers: { created: 0, existing: 0 },
  shops: { created: 0, existing: 0 },
  buyer: { created: 0, existing: 0 },
  addresses: { created: 0, existing: 0 },
  coins: { created: 0, existing: 0 },
  categories: { created: 0, existing: 0 },
  products: { created: 0, existing: 0 },
  vouchers: { created: 0, existing: 0 },
  flashSales: { created: 0, existing: 0 },
  flashSaleItems: { created: 0, existing: 0 },
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

async function apiRequest(method, path, { token, body, allowStatuses = [] } = {}) {
  const url = `${GATEWAY_URL}${path}`;
  for (let attempt = 0; attempt < 4; attempt += 1) {
    const headers = { Accept: "application/json" };
    if (token) headers.Authorization = `Bearer ${token}`;
    if (body !== undefined) headers["Content-Type"] = "application/json";

    const res = await fetch(url, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });

    if (res.status === 429 && attempt < 3) {
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

async function waitForOtp(email, sinceMs, timeoutMs = 20000) {
  const target = email.trim().toLowerCase();
  const deadline = Date.now() + timeoutMs;
  let lastErr = null;

  while (Date.now() < deadline) {
    try {
      const listRes = await fetch(`${MAILPIT_URL}/api/v1/messages?limit=50`);
      if (!listRes.ok) {
        throw new Error(`Mailpit list returned HTTP ${listRes.status}`);
      }
      const listData = await listRes.json();
      const messages = Array.isArray(listData?.messages) ? listData.messages : [];

      for (const summary of messages) {
        const recipients = Array.isArray(summary.To) ? summary.To : [];
        const matched = recipients.some(
          (r) => String(r?.Address || "").trim().toLowerCase() === target
        );
        if (!matched) continue;

        const createdMs = Date.parse(summary.Created || "");
        if (Number.isFinite(createdMs) && createdMs < sinceMs - 5000) {
          continue;
        }

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

  // 3. Categories (6 categories from web/src/data/products.ts).
  const catListRes = await apiRequest("GET", "/api/v1/categories");
  const existingCategories = Array.isArray(catListRes.data?.categories)
    ? catListRes.data.categories
    : [];
  const categoryBySlug = new Map(existingCategories.map((c) => [c.slug, c]));

  for (let i = 0; i < CATEGORIES.length; i += 1) {
    const cat = CATEGORIES[i];
    if (categoryBySlug.has(cat.slug)) {
      stats.categories.existing += 1;
      continue;
    }
    const createRes = await apiRequest("POST", "/api/v1/admin/categories", {
      token: adminToken,
      body: {
        name: cat.name,
        slug: cat.slug,
        parent_id: "",
        commission_rate_bps: 500,
        sort_order: i + 1,
      },
      allowStatuses: [409],
    });
    if (createRes.status === 409) {
      const refetch = await apiRequest("GET", "/api/v1/categories");
      for (const c of refetch.data?.categories || []) {
        categoryBySlug.set(c.slug, c);
      }
      stats.categories.existing += 1;
    } else {
      categoryBySlug.set(cat.slug, createRes.data.category);
      stats.categories.created += 1;
    }
  }

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
      // Backfill exact web slug if backend supports slug and existing row has a generated/UUID slug.
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
  const voucherListRes = await apiRequest("GET", "/api/v1/vouchers");
  const existingVouchers = Array.isArray(voucherListRes.data?.vouchers)
    ? voucherListRes.data.vouchers
    : [];
  const existingVoucherCodes = new Set(existingVouchers.map((v) => String(v.code).toUpperCase()));

  const nowMs = Date.now();
  const voucherStartsAt = new Date(nowMs - 60 * 60 * 1000).toISOString();
  const minVoucherEndMs = nowMs + 30 * 24 * 60 * 60 * 1000;

  for (const v of VOUCHERS) {
    const codeUpper = v.code.toUpperCase();
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

  // 6. Flash Sales: live slot + upcoming slots anchored to "now" (VN time Asia/Ho_Chi_Minh).
  // Re-running seed keeps existing valid live/upcoming windows without creating duplicates.
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

  // Anchor new windows to clean VN hour boundaries so names match starts_at/ends_at.
  const hourMs = 60 * 60 * 1000;
  const baseHourMs = Math.floor(nowMs / hourMs) * hourMs;
  const activeAndUpcomingSlots = FLASH_SALE_SLOTS.filter((slot) => slot.status !== "ended");
  const slotWindowOffsetsHours = [
    { startH: -1, endH: 12 }, // live slot
    { startH: 12, endH: 18 }, // upcoming slot 1
    { startH: 18, endH: 24 }, // upcoming slot 2
  ];

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

    const existingSkuIds = new Set((backendFlash.items || []).map((it) => it.sku_id));

    for (const item of slot.items) {
      const backendProd = productByStaticId.get(item.productId);
      if (!backendProd || !Array.isArray(backendProd.skus) || backendProd.skus.length === 0) {
        throw new Error(`Cannot seed flash sale item ${item.id}: product ${item.productId} missing`);
      }

      const targetSku = backendProd.skus.find((s) => s.stock > 0) || backendProd.skus[0];
      if (existingSkuIds.has(targetSku.id)) {
        stats.flashSaleItems.existing += 1;
        continue;
      }

      const sellerEntry = sellerByBackendShopId.get(backendProd.shop_id);
      if (!sellerEntry) {
        throw new Error(`Missing seller session for shop ${backendProd.shop_id}`);
      }

      const regRes = await apiRequest("POST", `/api/v1/seller/flash-sales/${backendFlash.id}/items`, {
        token: sellerEntry.token,
        body: {
          sku_id: targetSku.id,
          flash_price_vnd: item.flashPrice,
          total_slots: item.totalQuota,
          per_user_limit: item.limitPerBuyer,
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
      existingSkuIds.add(targetSku.id);
      stats.flashSaleItems.created += 1;
    }
  }

  // 7. Demo Buyer + valid default address + initial coins.
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
  await ensureDefaultAddress(buyerSession.token, shippingDir, {
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

  // 8. Slug capability check against live backend responses.
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

  // 9. Print summary and demo account table.
  console.log("\n=== Seed Summary ===");
  const summaryRows = [
    ["Sellers", stats.sellers],
    ["Shops", stats.shops],
    ["Demo buyer", stats.buyer],
    ["Addresses", stats.addresses],
    ["Buyer initial coins", stats.coins],
    ["Categories", stats.categories],
    ["Products", stats.products],
    ["Vouchers", stats.vouchers],
    ["Flash sale windows", stats.flashSales],
    ["Flash sale items", stats.flashSaleItems],
  ];
  for (const [label, counts] of summaryRows) {
    console.log(
      `  ${label.padEnd(20)} created=${counts.created}  existing=${counts.existing}`
    );
  }
  console.log(
    `  Backend slug fields: product.slug=${productHasSlug} (${productSlugMatches}/${PRODUCTS.length} matched), shop.slug=${shopHasSlug} (${shopSlugMatches}/${SHOPS.length} matched)`
  );

  console.log("\n=== Demo Accounts (Dev Only) ===");
  console.log("| Role | Email | Password | Shop / Notes |");
  console.log("| --- | --- | --- | --- |");
  console.log(`| admin | ${ADMIN_EMAIL} | ${ADMIN_PASSWORD} | Bootstrap admin |`);
  console.log(
    `| buyer | ${DEMO_USER.email} | ${BUYER_DEMO_PASSWORD} | ${DEMO_USER.fullName} (${initialCoins} xu, default address) |`
  );
  for (const staticShop of SHOPS) {
    const entry = sellerByStaticShopId.get(staticShop.id);
    console.log(
      `| seller | ${entry.account.email} | ${SELLER_DEMO_PASSWORD} | ${staticShop.name} (shop_id=${entry.backendShop.id}) |`
    );
  }
}

if (process.argv[1] && resolve(process.argv[1]) === __filename) {
  main().catch((err) => {
    console.error("Seed failed:", err);
    process.exit(1);
  });
}
