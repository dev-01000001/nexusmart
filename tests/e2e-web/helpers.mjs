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

export const SHIPPING_WEBHOOK_SECRET = envValue(
  "SHIPPING_WEBHOOK_SECRET",
  "dev-only-shipping-webhook-hmac-secret"
);

export const config = {
  webUrl: envValue("WEB_URL", `http://localhost:${webPort}`).replace(/\/+$/, ""),
  mailpitUrl: envValue("MAILPIT_URL", `http://localhost:${mailpitPort}`).replace(/\/+$/, ""),
  gatewayUrl: envValue("GATEWAY_URL", `http://localhost:${gatewayPort}`).replace(/\/+$/, ""),
  paymentWebhookSecret: envValue(
    "PAYMENT_WEBHOOK_SECRET",
    "dev-only-payment-webhook-hmac-secret"
  ),
  shippingWebhookSecret: SHIPPING_WEBHOOK_SECRET,
  zaloPayAppId: Number.parseInt(envValue("ZALOPAY_APP_ID", "1"), 10) || 1,
  zaloPayKey2: envValue("ZALOPAY_KEY2", "dev-only-zalopay-mock-key2"),
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

const usedPhones = new Set();

export function uniquePhone() {
  while (true) {
    const n = crypto.randomInt(10_000_000, 99_999_999);
    const phone = `09${n}`;
    if (!usedPhones.has(phone)) {
      usedPhones.add(phone);
      return phone;
    }
  }
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
export const RATE_LIMIT_MAX_WAIT_MS = 150_000;
export const RATE_LIMIT_SSR_DEFAULT_WAIT_MS = 60_000;
export const RATE_LIMIT_SSR_TEXT = "Bạn thao tác quá nhanh";

export function parseRetryAfterMs(headerVal) {
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

function parseOptionalInt(headerVal) {
  const v = String(headerVal ?? "").trim();
  if (!/^\d+$/.test(v)) return null;
  const n = Number.parseInt(v, 10);
  return Number.isFinite(n) ? n : null;
}

/**
 * Probes the gateway's shared per-IP rate-limit bucket (`rl:ip:<ip>`) via a lightweight
 * public BFF proxy endpoint (`/api/gw/shipping/provinces` forwards `x-ratelimit-limit`,
 * `x-ratelimit-remaining`, and `retry-after` without hitting the per-user bucket).
 */
export async function probeIpRateLimit(options = {}) {
  const probePath = options.probePath ?? "/api/gw/shipping/provinces";
  const url = `${config.webUrl}${probePath.startsWith("/") ? probePath : `/${probePath}`}`;
  const res = await fetch(url, {
    method: "GET",
    headers: { Accept: "application/json" },
    redirect: "manual",
    signal: AbortSignal.timeout(options.timeoutMs ?? config.requestTimeoutMs),
  });
  await res.body?.cancel().catch(() => {});

  const limit = parseOptionalInt(res.headers.get("x-ratelimit-limit"));
  const remaining = parseOptionalInt(res.headers.get("x-ratelimit-remaining"));
  const rawRetryAfter = res.headers.get("retry-after");
  const retryAfterMs = rawRetryAfter ? parseRetryAfterMs(rawRetryAfter) : null;
  return {
    status: res.status,
    limit,
    remaining,
    retryAfterMs,
  };
}

/**
 * Ensures at least `min` requests remain in the shared per-IP rate-limit window before
 * entering a bursty or atomic concurrent test block. Reads `x-ratelimit-remaining`
 * (and `Retry-After` on 429) and waits until the sliding window frees enough budget.
 */
export async function ensureRateBudget(min = 25, options = {}) {
  const requested = Math.max(1, Math.trunc(min));
  const maxWaitMs = options.maxWaitMs ?? RATE_LIMIT_MAX_WAIT_MS;
  const label = options.label ? ` [${options.label}]` : "";
  const startMs = Date.now();

  while (true) {
    const probe = await probeIpRateLimit(options);
    if (probe.status !== 429 && probe.remaining === null) {
      return probe;
    }

    const effectiveMin =
      probe.limit !== null && probe.limit > 5
        ? Math.min(requested, probe.limit - 5)
        : requested;
    if (probe.status !== 429 && probe.remaining !== null && probe.remaining >= effectiveMin) {
      return probe;
    }

    const elapsedMs = Date.now() - startMs;
    if (elapsedMs >= maxWaitMs) {
      return probe;
    }

    let waitMs;
    if (probe.status === 429) {
      waitMs = (probe.retryAfterMs ?? 10_000) + 1_500;
    } else if (probe.remaining === 0) {
      waitMs = 12_000;
    } else {
      waitMs = 10_000;
    }
    const sleepMs = Math.min(waitMs, maxWaitMs - elapsedMs);
    console.log(
      `[rate-budget]${label} remaining=${probe.remaining ?? 0}/${probe.limit ?? "?"} < ${effectiveMin} (HTTP ${probe.status}); waiting ${Math.ceil(sleepMs / 1000)}s for window reset (elapsed ${Math.ceil(elapsedMs / 1000)}s/${Math.ceil(maxWaitMs / 1000)}s)`
    );
    await new Promise((resolve) => setTimeout(resolve, sleepMs));
  }
}

export class RateLimitBlockError extends Error {
  constructor(message, retryAfterMs = null) {
    super(message);
    this.name = "RateLimitBlockError";
    this.retryAfterMs = retryAfterMs;
  }
}

/**
 * Executes an atomic / timing-sensitive test block (e.g. parallel refresh or 5s grace window)
 * after ensuring rate-limit budget. If any branch inside the block still encounters 429,
 * waits for the window and retries the ENTIRE block from scratch (with a fresh account),
 * up to `maxRetries` retries (default 2). Non-rate-limit errors fail immediately.
 */
export async function runAtomicBlockWith429Retry(label, fn, options = {}) {
  const minBudget = options.minBudget ?? 30;
  const maxRetries = options.maxRetries ?? 2;
  let lastErr = null;

  for (let attempt = 0; attempt <= maxRetries; attempt++) {
    await ensureRateBudget(minBudget, { label });
    try {
      return await fn(attempt);
    } catch (err) {
      if (!(err instanceof RateLimitBlockError)) {
        throw err;
      }
      lastErr = err;
      if (attempt >= maxRetries) {
        break;
      }
      const waitMs = (err.retryAfterMs ?? 10_000) + 1_500;
      console.log(
        `[429] atomic block "${label}" hit rate limit (${err.message}); waiting ${Math.ceil(waitMs / 1000)}s before retrying entire block with fresh account (retry ${attempt + 1}/${maxRetries})`
      );
      await new Promise((resolve) => setTimeout(resolve, waitMs));
    }
  }

  throw lastErr;
}

export async function fetchWith429Retry(url, initFactory) {
  let totalWaitedMs = 0;
  const logPath = new URL(url).pathname;

  while (true) {
    const init = initFactory();
    const res = await fetch(url, init);
    if (res.status !== 429) {
      return res;
    }

    const waitMs = parseRetryAfterMs(res.headers.get("retry-after")) + 250;
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

    const initFactory = () => ({
      method: upper,
      headers,
      body,
      redirect: options.redirect ?? "manual",
      signal: AbortSignal.timeout(options.timeoutMs ?? config.requestTimeoutMs),
    });
    const res =
      options.retry429 === false
        ? await fetch(url, initFactory())
        : await fetchWith429Retry(url, initFactory);

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
    const maxTotalMs = RATE_LIMIT_MAX_WAIT_MS;
    const defaultWaitMs = RATE_LIMIT_SSR_DEFAULT_WAIT_MS;
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
        options.retry429 === false ||
        res.status !== 200 ||
        options.cookieOverride !== undefined ||
        res.setCookies.length > 0 ||
        !res.text.includes(RATE_LIMIT_SSR_TEXT)
      ) {
        return res;
      }
      const rawRetry = res.headers.get("retry-after");
      const waitMs = (rawRetry ? parseRetryAfterMs(rawRetry) : defaultWaitMs) + 250;
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

export async function snapshotMailpitIds(email) {
  const target = email.trim().toLowerCase();
  const seen = new Set();
  const listRes = await fetch(`${config.mailpitUrl}/api/v1/messages?limit=100`, {
    signal: AbortSignal.timeout(config.requestTimeoutMs),
  });
  assert.equal(listRes.ok, true, `mailpit snapshot expected 2xx, got HTTP ${listRes.status}`);
  const list = await listRes.json();
  for (const summary of list?.messages ?? []) {
    const recipients = Array.isArray(summary?.To) ? summary.To : [];
    if (recipients.some((r) => String(r?.Address ?? "").trim().toLowerCase() === target)) {
      if (summary?.ID) seen.add(summary.ID);
    }
  }
  return seen;
}

export async function waitForRecentOtp(email, seenIds = new Set(), options = {}) {
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
        if (summary?.ID && seenIds.has(summary.ID)) continue;
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
          if (summary?.ID) seenIds.add(summary.ID);
          return { done: true, value: otp };
        }
      }
      return { done: false, detail: `no new OTP email for ${target} among ${messages.length} messages` };
    },
    {
      timeoutMs: options.timeoutMs ?? config.pollTimeoutMs,
      intervalMs: options.intervalMs ?? config.pollIntervalMs,
      label: options.label ?? `new OTP email for ${target}`,
    }
  );
}

export async function waitForOtp(email, options = {}) {
  return waitForRecentOtp(email, new Set(), {
    ...options,
    label: options.label ?? `OTP email for ${email.trim().toLowerCase()}`,
  });
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

/**
 * Simulates a shipping carrier calling Gateway `/webhooks/shipping/{carrier}` directly
 * with HMAC-SHA256 `X-Signature` and `X-Timestamp`.
 */
export async function postSignedShippingWebhook(carrier, payload, options = {}) {
  const secret = options.secret ?? SHIPPING_WEBHOOK_SECRET;
  const rawBody = JSON.stringify(payload);
  const signature =
    options.overrideSignature ??
    crypto.createHmac("sha256", secret).update(rawBody, "utf8").digest("hex");
  const timestamp = String(options.timestampSec ?? Math.floor(Date.now() / 1000));

  const res = await fetchWith429Retry(
    `${config.gatewayUrl}/webhooks/shipping/${encodeURIComponent(carrier || "ghn")}`,
    () => ({
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Signature": signature,
        "X-Timestamp": timestamp,
      },
      body: rawBody,
      signal: AbortSignal.timeout(config.requestTimeoutMs),
    })
  );
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

/**
 * Completes real E19 phone OTP verification via Mailpit for a logged-in buyer.
 * Never logs the OTP value.
 */
export async function verifyBuyerPhone(client, buyer) {
  const email = buyer?.email;
  assert.ok(email, "verifyBuyerPhone requires buyer.email");

  const seenIds = await snapshotMailpitIds(email);
  const otpReq = await client.json("POST", "/api/gw/users/me/phone/otp", { body: {} });
  assert.equal(
    otpReq.status,
    200,
    `POST /api/gw/users/me/phone/otp (${email}) expected 200, got ${otpReq.status}: ${otpReq.text}`
  );
  assert.equal(otpReq.data?.sent, true, `phone/otp (${email}) returned sent!=true: ${otpReq.text}`);

  const phoneOtp = await waitForRecentOtp(email, seenIds, {
    label: `phone OTP email for ${email}`,
  });
  const verifyRes = await client.json("POST", "/api/gw/users/me/phone/verify", {
    body: { otp: phoneOtp },
  });
  assert.equal(
    verifyRes.status,
    200,
    `POST /api/gw/users/me/phone/verify (${email}) expected 200, got ${verifyRes.status}: ${verifyRes.text}`
  );
  assert.equal(
    verifyRes.data?.verified,
    true,
    `phone/verify (${email}) expected verified=true: ${verifyRes.text}`
  );
  assert.equal(
    verifyRes.data?.user?.phone_verified,
    true,
    `phone/verify (${email}) expected user.phone_verified=true: ${verifyRes.text}`
  );
  if (buyer && typeof buyer === "object") {
    buyer.user = verifyRes.data.user;
  }
  return verifyRes.data.user;
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

  const account = {
    userId: loginRes.data?.user?.id || regRes.data.user_id,
    user: loginRes.data?.user,
    email,
    phone,
    password,
    fullName,
    loginRes,
  };

  if (options.verifyPhone === true) {
    await verifyBuyerPhone(client, account);
  }

  return account;
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

export const COD_SAFE_TOTAL_CAP_VND = 2_000_000;
const COD_SHIPPING_RESERVE_VND = 200_000;
export const COD_SAFE_SUBTOTAL_CAP_VND = COD_SAFE_TOTAL_CAP_VND - COD_SHIPPING_RESERVE_VND;

const FALLBACK_ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@nexusmart.local";
const FALLBACK_ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || "AdminDevPassword123!";
const FALLBACK_SELLER_PASSWORD = process.env.E2E_SELLER_PASSWORD || "SellerDevPassword123!";
const FALLBACK_SELLER_EMAILS = Array.from(
  new Set([
    process.env.E2E_SELLER_EMAIL || "seller.bachdang@nexusmart.local",
    "seller.saigon@nexusmart.local",
    "seller.danang@nexusmart.local",
    "seller.hanoi@nexusmart.local",
  ])
);

async function loginFallbackAccount(email, password) {
  const client = new WebClient();
  const res = await client.json("POST", "/api/auth/login", {
    body: { email, password },
  });
  assert.equal(res.status, 200, `fallback login ${email} expected 200, got ${res.status}: ${res.text}`);
  return client;
}

/**
 * Self-creates and approves an affordable in-stock product for a seller shop when the catalog
 * lacks active SKUs within the COD total budget (e.g. after Go E2E left expensive test SKUs).
 */
export async function createApprovedCodProduct(
  sellerClient,
  { priceVnd = 250_000, stock = 20, tag = "cod", adminClient = null } = {}
) {
  const admin = adminClient ?? (await loginFallbackAccount(FALLBACK_ADMIN_EMAIL, FALLBACK_ADMIN_PASSWORD));
  const catRes = await sellerClient.json("GET", "/api/gw/categories");
  assert.equal(catRes.status, 200, `fallback categories expected 200, got ${catRes.status}: ${catRes.text}`);
  const categoryId = (catRes.data?.categories ?? [])[0]?.id;
  assert.ok(categoryId, `fallback category_id missing: ${catRes.text}`);

  const suffix = crypto.randomBytes(4).toString("hex");
  const cleanPrice = Math.max(50_000, Math.min(COD_SAFE_SUBTOTAL_CAP_VND - 100_000, Math.trunc(priceVnd)));
  const cleanStock = Math.max(5, Math.trunc(stock));
  const createRes = await sellerClient.json("POST", "/api/gw/seller/products", {
    body: {
      category_id: categoryId,
      name: `E2E COD ${tag} ${suffix}`,
      slug: `e2e-cod-${tag}-${suffix}`,
      description: "Sản phẩm tạo tự động cho kiểm thử COD E2E Web.",
      images: ["/products/ao-thun-cotton-trang.png"],
      skus: [
        {
          sku_code: `COD-${suffix.toUpperCase()}-M`,
          name: "Trắng / M",
          attributes: { color: "Trắng", size: "M" },
          price_vnd: cleanPrice,
          stock: cleanStock,
          weight_grams: 280,
          image_url: "/products/ao-thun-cotton-trang.png",
        },
      ],
    },
  });
  assert.ok(
    createRes.status === 201 || createRes.status === 200,
    `fallback create COD product expected 201/200, got ${createRes.status}: ${createRes.text}`
  );
  const created = createRes.data?.product;
  assert.ok(created?.id && created?.skus?.[0]?.id, `fallback created product missing id/sku: ${createRes.text}`);

  const reviewRes = await admin.json(
    "POST",
    `/api/gw/admin/products/${encodeURIComponent(created.id)}/review`,
    { body: { decision: "APPROVE", reason: "E2E Web auto-approve COD fallback product" } }
  );
  assert.equal(reviewRes.status, 200, `fallback approve COD product expected 200, got ${reviewRes.status}: ${reviewRes.text}`);
  const approved = reviewRes.data?.product ?? created;
  const sku = approved.skus?.[0] ?? created.skus[0];
  return {
    product: approved,
    sku: { ...sku, available_stock: sku.available_stock ?? sku.stock ?? cleanStock },
  };
}

export async function listCatalogProducts(client, { page = 1, pageSize = 100 } = {}) {
  const res = await client.json("GET", `/api/gw/products?page=${page}&page_size=${pageSize}`);
  assert.equal(
    res.status,
    200,
    `GET /api/gw/products expected 200, got ${res.status}: ${res.text}`
  );
  const items = res.data?.items ?? [];
  if (page === 1) {
    assert.ok(items.length > 0, `expected seeded products, got empty list: ${res.text}`);
  }
  return items;
}

function collectAffordableShopCandidates(products, { minStock, maxSkuPriceVnd }) {
  const byShop = new Map();
  for (const product of products) {
    if (!product?.shop_id || !Array.isArray(product.skus)) continue;
    for (const sku of product.skus) {
      if (!sku?.id || sku.is_active === false) continue;
      const stock = sku.available_stock ?? 0;
      const price = sku.price_vnd ?? 0;
      if (stock < minStock || price <= 0 || price > maxSkuPriceVnd) continue;
      const list = byShop.get(product.shop_id) ?? [];
      list.push({ product, sku });
      byShop.set(product.shop_id, list);
    }
  }
  for (const candidates of byShop.values()) {
    // Prefer well-stocked SKUs first, then lower affordable price so leftover expensive E2E SKUs are avoided.
    candidates.sort((a, b) => {
      const aWell = (a.sku.available_stock ?? 0) >= Math.max(minStock, 5) ? 1 : 0;
      const bWell = (b.sku.available_stock ?? 0) >= Math.max(minStock, 5) ? 1 : 0;
      if (bWell !== aWell) return bWell - aWell;
      if (a.sku.price_vnd !== b.sku.price_vnd) return a.sku.price_vnd - b.sku.price_vnd;
      return (b.sku.available_stock ?? 0) - (a.sku.available_stock ?? 0);
    });
  }
  return byShop;
}

function tryBuildTwoShopPair(candA, candB, { minTotalVnd, maxTotalVnd }) {
  const first = { ...candA, quantity: 1 };
  const second = { ...candB, quantity: 1 };
  let total = first.sku.price_vnd + second.sku.price_vnd;
  if (total > maxTotalVnd) return null;

  while (total < minTotalVnd) {
    const firstHeadroom = (first.sku.available_stock ?? 0) - first.quantity;
    const secondHeadroom = (second.sku.available_stock ?? 0) - second.quantity;
    const canIncFirst = firstHeadroom > 0 && total + first.sku.price_vnd <= maxTotalVnd;
    const canIncSecond = secondHeadroom > 0 && total + second.sku.price_vnd <= maxTotalVnd;
    if (!canIncFirst && !canIncSecond) return null;

    if (
      canIncFirst &&
      (!canIncSecond ||
        firstHeadroom > secondHeadroom ||
        (firstHeadroom === secondHeadroom && first.sku.price_vnd <= second.sku.price_vnd))
    ) {
      first.quantity += 1;
      total += first.sku.price_vnd;
    } else {
      second.quantity += 1;
      total += second.sku.price_vnd;
    }
  }

  return total >= minTotalVnd && total <= maxTotalVnd ? [first, second] : null;
}

function findTwoShopPairFromMap(byShop, { minTotalVnd, maxTotalVnd }) {
  const shopEntries = Array.from(byShop.entries());
  for (let i = 0; i < shopEntries.length; i += 1) {
    const [, listA] = shopEntries[i];
    for (let j = i + 1; j < shopEntries.length; j += 1) {
      const [, listB] = shopEntries[j];
      const topA = listA.slice(0, 5);
      const topB = listB.slice(0, 5);
      for (const candA of topA) {
        for (const candB of topB) {
          const pair = tryBuildTwoShopPair(candA, candB, { minTotalVnd, maxTotalVnd });
          if (pair) return pair;
        }
      }
    }
  }
  return null;
}

export async function pickTwoShopSkus(client, options = {}) {
  const minTotalVnd = options.minTotalVnd ?? 0;
  const maxTotalVnd = options.maxTotalVnd ?? COD_SAFE_SUBTOTAL_CAP_VND;
  const minStock = options.minStock ?? 2;

  const page1 = await listCatalogProducts(client, { page: 1, pageSize: 100 });
  let byShop = collectAffordableShopCandidates(page1, {
    minStock,
    maxSkuPriceVnd: maxTotalVnd - 1,
  });
  let pair = findTwoShopPairFromMap(byShop, { minTotalVnd, maxTotalVnd });
  if (pair) return pair;

  // Try page 2 in case page 1 is saturated with expensive or single-shop E2E leftover products.
  const page2 = await listCatalogProducts(client, { page: 2, pageSize: 100 });
  if (page2.length > 0) {
    byShop = collectAffordableShopCandidates([...page1, ...page2], {
      minStock,
      maxSkuPriceVnd: maxTotalVnd - 1,
    });
    pair = findTwoShopPairFromMap(byShop, { minTotalVnd, maxTotalVnd });
    if (pair) return pair;
  }

  // Fallback: self-create affordable approved products on distinct seeded seller shops (never skip).
  const adminClient = await loginFallbackAccount(FALLBACK_ADMIN_EMAIL, FALLBACK_ADMIN_PASSWORD);
  const targetUnitPrice = Math.max(
    180_000,
    Math.min(350_000, Math.ceil(Math.max(minTotalVnd, 360_000) / 2))
  );
  for (const email of FALLBACK_SELLER_EMAILS) {
    const sellerClient = await loginFallbackAccount(email, FALLBACK_SELLER_PASSWORD);
    const shopMeRes = await sellerClient.json("GET", "/api/gw/shops/me");
    const myShopId = shopMeRes.data?.shop?.id;
    if (myShopId && (byShop.get(myShopId)?.length ?? 0) > 0) {
      continue;
    }
    const created = await createApprovedCodProduct(sellerClient, {
      priceVnd: targetUnitPrice,
      stock: Math.max(20, minStock + 5),
      tag: "2shop",
      adminClient,
    });
    const list = byShop.get(created.product.shop_id) ?? [];
    list.unshift(created);
    byShop.set(created.product.shop_id, list);

    pair = findTwoShopPairFromMap(byShop, { minTotalVnd, maxTotalVnd });
    if (pair) return pair;
  }

  assert.fail(
    `unable to resolve 2-shop SKUs within [${minTotalVnd}, ${maxTotalVnd}] VND (minStock=${minStock})`
  );
}

export async function pickSingleShopSku(client, options = {}) {
  const minTotalVnd = options.minTotalVnd ?? 0;
  const maxTotalVnd = options.maxTotalVnd ?? COD_SAFE_SUBTOTAL_CAP_VND;
  const minStock = options.minStock ?? 2;

  const tryPickFrom = (products) => {
    const byShop = collectAffordableShopCandidates(products, {
      minStock,
      maxSkuPriceVnd: maxTotalVnd,
    });
    for (const candidates of byShop.values()) {
      for (const cand of candidates) {
        const qty =
          minTotalVnd > 0 ? Math.max(1, Math.ceil(minTotalVnd / cand.sku.price_vnd)) : 1;
        const total = cand.sku.price_vnd * qty;
        if (qty <= (cand.sku.available_stock ?? 0) && total >= minTotalVnd && total <= maxTotalVnd) {
          return { ...cand, quantity: qty };
        }
      }
    }
    return null;
  };

  const page1 = await listCatalogProducts(client, { page: 1, pageSize: 100 });
  const fromPage1 = tryPickFrom(page1);
  if (fromPage1) return fromPage1;

  const page2 = await listCatalogProducts(client, { page: 2, pageSize: 100 });
  const fromPage2 = tryPickFrom(page2);
  if (fromPage2) return fromPage2;

  // Fallback: self-create 1 affordable approved product on the primary seeded seller shop (never skip).
  const sellerClient = await loginFallbackAccount(
    FALLBACK_SELLER_EMAILS[0],
    FALLBACK_SELLER_PASSWORD
  );
  const unitPrice = Math.max(200_000, Math.min(400_000, minTotalVnd || 250_000));
  const created = await createApprovedCodProduct(sellerClient, {
    priceVnd: unitPrice,
    stock: Math.max(20, minStock + 5),
    tag: "1shop",
  });
  const qty = minTotalVnd > 0 ? Math.max(1, Math.ceil(minTotalVnd / created.sku.price_vnd)) : 1;
  assert.ok(
    created.sku.price_vnd * qty <= maxTotalVnd,
    `fallback single SKU total ${created.sku.price_vnd * qty} exceeds maxTotalVnd ${maxTotalVnd}`
  );
  return { ...created, quantity: qty };
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
