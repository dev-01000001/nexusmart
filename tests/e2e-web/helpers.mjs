import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(__dirname, "..", "..");

function loadDotEnvFallback() {
  const envPath = path.join(REPO_ROOT, ".env");
  const parsed = {};
  if (!fs.existsSync(envPath)) return parsed;
  const content = fs.readFileSync(envPath, "utf8");
  for (const rawLine of content.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) continue;
    const eq = line.indexOf("=");
    if (eq <= 0) continue;
    const key = line.slice(0, eq).trim();
    let val = line.slice(eq + 1).trim();
    if (
      (val.startsWith('"') && val.endsWith('"')) ||
      (val.startsWith("'") && val.endsWith("'"))
    ) {
      val = val.slice(1, -1);
    }
    parsed[key] = val;
  }
  return parsed;
}

const dotEnv = loadDotEnvFallback();

function envValue(name, fallback = "") {
  const fromProc = process.env[name];
  if (fromProc !== undefined && fromProc !== "") return fromProc;
  const fromFile = dotEnv[name];
  if (fromFile !== undefined && fromFile !== "") return fromFile;
  return fallback;
}

const webPort = envValue("WEB_PORT", "3000");
const mailpitPort = envValue("MAILPIT_UI_PORT", "8025");
const gatewayPort = envValue("GATEWAY_PORT", "8080");

export const config = {
  webUrl: envValue("WEB_URL", `http://localhost:${webPort}`).replace(/\/+$/, ""),
  mailpitUrl: envValue("MAILPIT_URL", `http://localhost:${mailpitPort}`).replace(/\/+$/, ""),
  gatewayUrl: envValue("GATEWAY_URL", `http://localhost:${gatewayPort}`).replace(/\/+$/, ""),
  paymentWebhookSecret: envValue(
    "PAYMENT_WEBHOOK_SECRET",
    "dev-only-payment-webhook-hmac-secret"
  ),
  requestTimeoutMs: 15_000,
  pollTimeoutMs: 25_000,
  pollIntervalMs: 300,
};

config.webOrigin = envValue("APP_ORIGIN", "") || new URL(config.webUrl).origin;

export const ACCESS_COOKIE = "nm_access_token";
export const REFRESH_COOKIE = "nm_refresh_token";
const MUTATING_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"]);

let seqCounter = 0;

export function uniqueEmail(label) {
  seqCounter += 1;
  const tag = crypto.randomBytes(4).toString("hex");
  return `e2eweb-${label}-${Date.now()}-${seqCounter}-${tag}@nexusmart.test`;
}

export function uniquePhone() {
  const n = crypto.randomInt(10_000_000, 99_999_999);
  return `09${n}`;
}

export function normalizeStatus(raw) {
  let u = String(raw ?? "")
    .trim()
    .toUpperCase();
  for (const prefix of [
    "ORDER_STATUS_",
    "PAYMENT_STATUS_",
    "PAYMENT_METHOD_",
    "ORDER_PAYMENT_METHOD_",
    "USER_STATUS_",
    "USER_ROLE_",
    "SHOP_STATUS_",
    "PRODUCT_STATUS_",
    "SHIPMENT_STATUS_",
  ]) {
    if (u.startsWith(prefix)) {
      u = u.slice(prefix.length);
    }
  }
  return u;
}

export function parseSetCookie(raw) {
  const parts = raw.split(";").map((p) => p.trim());
  const first = parts[0] ?? "";
  const eq = first.indexOf("=");
  if (eq <= 0) return null;

  const name = first.slice(0, eq).trim();
  const value = first.slice(eq + 1).trim();
  const meta = {
    name,
    value,
    raw,
    httpOnly: false,
    secure: false,
    sameSite: null,
    path: null,
    domain: null,
    maxAge: null,
    expires: null,
  };

  for (const attr of parts.slice(1)) {
    const attrEq = attr.indexOf("=");
    const k = (attrEq === -1 ? attr : attr.slice(0, attrEq)).trim().toLowerCase();
    const v = attrEq === -1 ? "" : attr.slice(attrEq + 1).trim();
    if (k === "httponly") meta.httpOnly = true;
    else if (k === "secure") meta.secure = true;
    else if (k === "samesite") meta.sameSite = v.toLowerCase();
    else if (k === "path") meta.path = v;
    else if (k === "domain") meta.domain = v;
    else if (k === "max-age") meta.maxAge = Number.parseInt(v, 10);
    else if (k === "expires") meta.expires = v;
  }

  return meta;
}

const RATE_LIMIT_DEFAULT_RETRY_AFTER_MS = 5_000;
const RATE_LIMIT_MAX_WAIT_MS = 120_000;

function parseRetryAfterMs(headerVal) {
  const v = String(headerVal ?? "").trim();
  if (!v) return RATE_LIMIT_DEFAULT_RETRY_AFTER_MS;
  if (/^-?\d+$/.test(v)) {
    const sec = Number.parseInt(v, 10);
    return sec > 0 ? sec * 1000 : RATE_LIMIT_DEFAULT_RETRY_AFTER_MS;
  }
  const ts = Date.parse(v);
  if (!Number.isNaN(ts)) {
    const delta = ts - Date.now();
    if (delta > 0) return delta;
  }
  return RATE_LIMIT_DEFAULT_RETRY_AFTER_MS;
}

async function fetchWith429Retry(url, initFactory) {
  let totalWaitedMs = 0;
  const logPath = new URL(url).pathname;

  while (true) {
    const init = initFactory();
    const res = await fetch(url, init);
    if (res.status !== 429) {
      return res;
    }

    const waitMs = parseRetryAfterMs(res.headers.get("retry-after"));
    if (totalWaitedMs + waitMs > RATE_LIMIT_MAX_WAIT_MS) {
      return res;
    }

    await res.body?.cancel().catch(() => {});
    totalWaitedMs += waitMs;
    console.log(
      `[429] ${init.method ?? "GET"} ${logPath} rate limited; waiting ${Math.ceil(waitMs / 1000)}s before retry (total ${Math.ceil(totalWaitedMs / 1000)}s/${RATE_LIMIT_MAX_WAIT_MS / 1000}s)`
    );
    await new Promise((resolve) => setTimeout(resolve, waitMs));
  }
}

export class WebClient {
  constructor(baseUrl = config.webUrl, defaultOrigin = config.webOrigin) {
    this.baseUrl = baseUrl.replace(/\/+$/, "");
    this.defaultOrigin = defaultOrigin;
    this.cookies = new Map();
  }

  getCookie(name) {
    return this.cookies.get(name)?.value;
  }

  getCookieMeta(name) {
    return this.cookies.get(name);
  }

  clearCookies() {
    this.cookies.clear();
  }

  buildCookieHeader() {
    return Array.from(this.cookies.entries())
      .filter(([, meta]) => meta.value !== "")
      .map(([name, meta]) => `${name}=${meta.value}`)
      .join("; ");
  }

  applySetCookies(rawList) {
    const parsedList = [];
    for (const raw of rawList) {
      const meta = parseSetCookie(raw);
      if (!meta) continue;
      parsedList.push(meta);
      const expiredByDate =
        meta.expires && !Number.isNaN(Date.parse(meta.expires)) && Date.parse(meta.expires) <= Date.now();
      if (meta.value === "" || (meta.maxAge !== null && meta.maxAge <= 0) || expiredByDate) {
        this.cookies.delete(meta.name);
      } else {
        this.cookies.set(meta.name, meta);
      }
    }
    return parsedList;
  }

  async request(method, reqPath, options = {}) {
    const upper = method.toUpperCase();
    const url = reqPath.startsWith("http://") || reqPath.startsWith("https://")
      ? reqPath
      : `${this.baseUrl}${reqPath.startsWith("/") ? reqPath : `/${reqPath}`}`;

    const headers = new Headers(options.headers ?? {});
    if (options.accept && !headers.has("Accept")) {
      headers.set("Accept", options.accept);
    }

    if (MUTATING_METHODS.has(upper)) {
      if (options.origin === undefined) {
        if (!headers.has("Origin")) {
          headers.set("Origin", this.defaultOrigin);
        }
      } else if (options.origin === null) {
        headers.delete("Origin");
      } else {
        headers.set("Origin", options.origin);
      }
    } else if (typeof options.origin === "string") {
      headers.set("Origin", options.origin);
    }

    const cookieHeader =
      options.cookieOverride !== undefined ? options.cookieOverride : this.buildCookieHeader();
    if (cookieHeader) {
      headers.set("Cookie", cookieHeader);
    }

    let body;
    if (options.body !== undefined && options.body !== null) {
      if (typeof options.body === "string" || options.body instanceof Uint8Array) {
        body = options.body;
      } else {
        body = JSON.stringify(options.body);
        if (!headers.has("Content-Type")) {
          headers.set("Content-Type", "application/json");
        }
      }
    }

    const res = await fetchWith429Retry(url, () => ({
      method: upper,
      headers,
      body,
      redirect: options.redirect ?? "manual",
      signal: AbortSignal.timeout(options.timeoutMs ?? config.requestTimeoutMs),
    }));

    const rawSetCookies = typeof res.headers.getSetCookie === "function"
      ? res.headers.getSetCookie()
      : [];
    const setCookies =
      options.updateJar === false
        ? rawSetCookies.map(parseSetCookie).filter(Boolean)
        : this.applySetCookies(rawSetCookies);

    const text = await res.text();
    let data = null;
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = null;
      }
    }

    return {
      status: res.status,
      headers: res.headers,
      location: res.headers.get("location"),
      setCookies,
      text,
      data,
    };
  }

  json(method, reqPath, options = {}) {
    return this.request(method, reqPath, {
      accept: "application/json",
      ...options,
    });
  }

  async page(reqPath, options = {}) {
    const startMs = Date.now();
    const maxTotalMs = 150_000;
    const defaultWaitMs = 60_000;
    const logPath = new URL(
      reqPath.startsWith("http://") || reqPath.startsWith("https://")
        ? reqPath
        : `${this.baseUrl}${reqPath.startsWith("/") ? reqPath : `/${reqPath}`}`
    ).pathname;

    while (true) {
      const res = await this.request("GET", reqPath, {
        accept: "text/html,application/xhtml+xml",
        redirect: "manual",
        ...options,
      });
      if (
        res.status !== 200 ||
        options.cookieOverride !== undefined ||
        res.setCookies.length > 0 ||
        !res.text.includes("Bạn thao tác quá nhanh")
      ) {
        return res;
      }
      const rawRetry = res.headers.get("retry-after");
      const waitMs = rawRetry ? parseRetryAfterMs(rawRetry) : defaultWaitMs;
      const elapsedMs = Date.now() - startMs;
      if (elapsedMs + waitMs > maxTotalMs) {
        return res;
      }
      console.log(
        `[429] page GET ${logPath} rendered SSR rate limit; waiting ${Math.ceil(waitMs / 1000)}s before retry (elapsed ${Math.ceil((elapsedMs + waitMs) / 1000)}s/${maxTotalMs / 1000}s)`
      );
      await new Promise((resolve) => setTimeout(resolve, waitMs));
    }
  }
}

const OTP_CONTEXTUAL_RE = /(?:otp|code|verification|mã|xác\s*thực)[^0-9]{0,24}(\d{6})\b/i;
const OTP_GENERIC_RE = /\b(\d{6})\b/;

export function extractOtp(...parts) {
  const combined = parts.filter(Boolean).join("\n");
  const ctxMatch = OTP_CONTEXTUAL_RE.exec(combined);
  if (ctxMatch) return ctxMatch[1];
  const genMatch = OTP_GENERIC_RE.exec(combined);
  if (genMatch) return genMatch[1];
  return "";
}

export async function pollUntil(checkFn, options = {}) {
  const timeoutMs = options.timeoutMs ?? config.pollTimeoutMs;
  const intervalMs = options.intervalMs ?? config.pollIntervalMs;
  const label = options.label ?? "condition";
  const deadline = Date.now() + timeoutMs;
  let lastDetail = "";

  while (true) {
    const result = await checkFn();
    if (result?.done) return result.value;
    lastDetail = result?.detail ?? "";
    if (Date.now() >= deadline) {
      throw new Error(`Timed out after ${timeoutMs}ms waiting for ${label} (last: ${lastDetail})`);
    }
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }
}

export async function waitForOtp(email, options = {}) {
  const target = email.trim().toLowerCase();
  return pollUntil(
    async () => {
      const listRes = await fetch(`${config.mailpitUrl}/api/v1/messages?limit=100`, {
        signal: AbortSignal.timeout(config.requestTimeoutMs),
      });
      if (!listRes.ok) {
        return { done: false, detail: `mailpit list HTTP ${listRes.status}` };
      }
      const list = await listRes.json();
      const messages = Array.isArray(list?.messages) ? list.messages : [];
      for (const summary of messages) {
        const recipients = Array.isArray(summary?.To) ? summary.To : [];
        const matched = recipients.some(
          (r) => String(r?.Address ?? "").trim().toLowerCase() === target
        );
        if (!matched) continue;

        const msgRes = await fetch(
          `${config.mailpitUrl}/api/v1/message/${encodeURIComponent(summary.ID)}`,
          { signal: AbortSignal.timeout(config.requestTimeoutMs) }
        );
        if (!msgRes.ok) continue;
        const full = await msgRes.json();
        const otp = extractOtp(full?.Subject, full?.Text, full?.HTML, summary?.Snippet);
        if (otp) {
          return { done: true, value: otp };
        }
      }
      return { done: false, detail: `no OTP email for ${target} among ${messages.length} messages` };
    },
    {
      timeoutMs: options.timeoutMs ?? config.pollTimeoutMs,
      intervalMs: options.intervalMs ?? config.pollIntervalMs,
      label: `OTP email for ${target}`,
    }
  );
}

/**
 * Simulates the external VietQR provider calling Gateway `/webhooks/payment` directly
 * with HMAC-SHA256 `X-Signature` and `X-Timestamp`.
 */
export async function postSignedPaymentWebhook(payload, options = {}) {
  const secret = options.secret ?? config.paymentWebhookSecret;
  const rawBody = JSON.stringify(payload);
  const signature =
    options.overrideSignature ??
    crypto.createHmac("sha256", secret).update(rawBody, "utf8").digest("hex");
  const timestamp = String(options.timestampSec ?? Math.floor(Date.now() / 1000));

  const res = await fetchWith429Retry(`${config.gatewayUrl}/webhooks/payment`, () => ({
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Signature": signature,
      "X-Timestamp": timestamp,
    },
    body: rawBody,
    signal: AbortSignal.timeout(config.requestTimeoutMs),
  }));

  const text = await res.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  return { status: res.status, text, data };
}

export async function registerAndLoginBuyer(client, label, options = {}) {
  const email = uniqueEmail(label);
  const phone = uniquePhone();
  const password = options.password ?? "NexusWebE2E123!";
  const fullName = options.fullName ?? `Người mua E2E ${label}`;

  const regRes = await client.json("POST", "/api/auth/register", {
    body: { full_name: fullName, email, phone, password },
  });
  assert.equal(
    regRes.status,
    201,
    `register (${label}) expected 201, got ${regRes.status}: ${regRes.text}`
  );
  assert.ok(regRes.data?.user_id, `register (${label}) missing user_id: ${regRes.text}`);

  const otp = await waitForOtp(email);
  const verifyRes = await client.json("POST", "/api/auth/verify-otp", {
    body: { email, otp },
  });
  assert.equal(
    verifyRes.status,
    200,
    `verify-otp (${label}) expected 200, got ${verifyRes.status}: ${verifyRes.text}`
  );
  assert.equal(verifyRes.data?.verified, true, `verify-otp returned false: ${verifyRes.text}`);

  const loginRes = await client.json("POST", "/api/auth/login", {
    body: { email, password },
  });
  assert.equal(
    loginRes.status,
    200,
    `login (${label}) expected 200, got ${loginRes.status}: ${loginRes.text}`
  );

  return {
    userId: loginRes.data?.user?.id || regRes.data.user_id,
    user: loginRes.data?.user,
    email,
    phone,
    password,
    fullName,
    loginRes,
  };
}

let cachedLocation = null;

export async function getProvinceAndWard(client) {
  if (cachedLocation) return cachedLocation;
  const provRes = await client.json("GET", "/api/gw/shipping/provinces");
  assert.equal(
    provRes.status,
    200,
    `provinces expected 200, got ${provRes.status}: ${provRes.text}`
  );
  const provinces = provRes.data?.provinces ?? [];
  assert.ok(provinces.length >= 34, `expected >= 34 provinces, got ${provinces.length}`);

  const province = provinces[0];
  const wardRes = await client.json(
    "GET",
    `/api/gw/shipping/provinces/${encodeURIComponent(province.code)}/wards`
  );
  assert.equal(
    wardRes.status,
    200,
    `wards expected 200, got ${wardRes.status}: ${wardRes.text}`
  );
  const wards = wardRes.data?.wards ?? [];
  assert.ok(wards.length > 0, `expected >= 1 ward for province ${province.code}`);

  cachedLocation = { province, ward: wards[0] };
  return cachedLocation;
}

export async function createBuyerAddress(client, buyer, overrides = {}) {
  const { province, ward } = await getProvinceAndWard(client);
  const payload = {
    recipient_name: overrides.recipientName ?? buyer.fullName,
    recipient_phone: overrides.recipientPhone ?? buyer.phone,
    province_code: province.code,
    province_name: province.name,
    ward_code: ward.code,
    ward_name: ward.name,
    street_address:
      overrides.streetAddress ?? `123 Đường Lê Lợi E2E ${crypto.randomBytes(3).toString("hex")}`,
    is_default: overrides.isDefault ?? true,
  };

  const res = await client.json("POST", "/api/gw/users/me/addresses", { body: payload });
  assert.ok(
    res.status === 201 || res.status === 200,
    `create address expected 201/200, got ${res.status}: ${res.text}`
  );
  assert.ok(res.data?.address?.id, `create address missing id: ${res.text}`);
  return { address: res.data.address, province, ward };
}

export async function listCatalogProducts(client) {
  const res = await client.json("GET", "/api/gw/products?page=1&page_size=50");
  assert.equal(
    res.status,
    200,
    `GET /api/gw/products expected 200, got ${res.status}: ${res.text}`
  );
  const items = res.data?.items ?? [];
  assert.ok(items.length > 0, `expected seeded products, got empty list: ${res.text}`);
  return items;
}

export async function pickTwoShopSkus(client, options = {}) {
  const minTotalVnd = options.minTotalVnd ?? 0;
  const minStock = options.minStock ?? 2;
  const products = await listCatalogProducts(client);

  const byShop = new Map();
  for (const product of products) {
    if (!product?.shop_id || !Array.isArray(product.skus)) continue;
    for (const sku of product.skus) {
      if (!sku?.id || sku.is_active === false) continue;
      if ((sku.available_stock ?? 0) < minStock || (sku.price_vnd ?? 0) <= 0) continue;
      const list = byShop.get(product.shop_id) ?? [];
      list.push({ product, sku });
      byShop.set(product.shop_id, list);
    }
  }

  const bestPerShop = [];
  for (const candidates of byShop.values()) {
    candidates.sort((a, b) => {
      if (b.sku.available_stock !== a.sku.available_stock) {
        return b.sku.available_stock - a.sku.available_stock;
      }
      return b.sku.price_vnd - a.sku.price_vnd;
    });
    // Prefer candidates with solid stock (>= 5) and higher price when meeting a voucher floor.
    const wellStocked = candidates.filter((c) => c.sku.available_stock >= Math.max(minStock, 5));
    const pool = wellStocked.length > 0 ? wellStocked : candidates;
    if (minTotalVnd > 0) {
      pool.sort((a, b) => b.sku.price_vnd - a.sku.price_vnd);
    }
    bestPerShop.push(pool[0]);
  }

  assert.ok(
    bestPerShop.length >= 2,
    `expected active in-stock SKUs from at least 2 shops, found ${bestPerShop.length}`
  );

  if (minTotalVnd > 0) {
    bestPerShop.sort((a, b) => b.sku.price_vnd - a.sku.price_vnd);
  } else {
    bestPerShop.sort((a, b) => b.sku.available_stock - a.sku.available_stock);
  }

  const first = { ...bestPerShop[0], quantity: 1 };
  const second = { ...bestPerShop[1], quantity: 1 };

  while (
    first.sku.price_vnd * first.quantity + second.sku.price_vnd * second.quantity <
    minTotalVnd
  ) {
    const firstHeadroom = first.sku.available_stock - first.quantity;
    const secondHeadroom = second.sku.available_stock - second.quantity;
    if (firstHeadroom <= 0 && secondHeadroom <= 0) {
      break;
    }
    if (firstHeadroom >= secondHeadroom) {
      first.quantity += 1;
    } else {
      second.quantity += 1;
    }
  }

  return [first, second];
}

export async function pickSingleShopSku(client, options = {}) {
  const [first] = await pickTwoShopSkus(client, { minStock: options.minStock ?? 2 });
  return first;
}

export async function calculateShippingQuote(client, { shopId, province, ward, weightGrams, codAmountVnd }) {
  const res = await client.json("POST", "/api/gw/shipping/calculate-fee", {
    body: {
      shop_id: shopId,
      from_province_code: province.code,
      from_ward_code: ward.code,
      to_province_code: province.code,
      to_ward_code: ward.code,
      weight_grams: Math.max(weightGrams || 300, 100),
      cod_amount_vnd: codAmountVnd ?? 0,
    },
  });
  assert.equal(
    res.status,
    200,
    `calculate-fee for shop ${shopId} expected 200, got ${res.status}: ${res.text}`
  );
  const quotes = res.data?.quotes ?? [];
  assert.ok(quotes.length > 0, `calculate-fee returned 0 quotes: ${res.text}`);
  return quotes.find((q) => String(q.carrier_code).toLowerCase() === "ghn") ?? quotes[0];
}
