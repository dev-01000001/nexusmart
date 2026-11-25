import http from "node:http";
import https from "node:https";
import { randomBytes } from "node:crypto";
import { config, originOf } from "./config.mjs";

const hostnameOf = (host) => String(host).toLowerCase().replace(/:\d+$/, "");

export const RATE_LIMIT_MAX_WAIT_MS = 35_000;
export const RATE_LIMIT_SSR_DEFAULT_WAIT_MS = 10_000;
export const RATE_LIMIT_SSR_TEXT = "Quá nhiều yêu cầu";

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export function parseRetryAfterMs(headerVal, fallbackMs = 5_000) {
  const raw = Array.isArray(headerVal) ? headerVal[0] : headerVal;
  const s = String(raw ?? "").trim();
  if (!s) return fallbackMs;
  if (/^\d+$/.test(s)) {
    return Math.min(RATE_LIMIT_MAX_WAIT_MS, Math.max(500, Number(s) * 1000));
  }
  const dateMs = Date.parse(s);
  if (Number.isFinite(dateMs)) {
    return Math.min(RATE_LIMIT_MAX_WAIT_MS, Math.max(500, dateMs - Date.now()));
  }
  return fallbackMs;
}

export class RateLimitBlockError extends Error {
  constructor(message, retryAfterMs = null) {
    super(message);
    this.name = "RateLimitBlockError";
    this.retryAfterMs = retryAfterMs;
  }
}

/**
 * Executes a timing-sensitive / atomic test block (e.g. parallel refresh or 5s grace window).
 * If any branch throws RateLimitBlockError, waits Retry-After and retries the ENTIRE block
 * from scratch (with a fresh account), up to `maxRetries` retries.
 */
export async function runAtomicBlockWith429Retry(label, fn, { maxRetries = 2 } = {}) {
  let lastErr = null;
  for (let attempt = 0; attempt <= maxRetries; attempt += 1) {
    try {
      return await fn(attempt);
    } catch (err) {
      if (!(err instanceof RateLimitBlockError)) throw err;
      lastErr = err;
      if (attempt >= maxRetries) break;
      const waitMs = (err.retryAfterMs ?? 10_000) + 1_000;
      console.log(
        `[429] atomic block "${label}" hit rate limit (${err.message}); waiting ${Math.ceil(waitMs / 1000)}s (retry ${attempt + 1}/${maxRetries})`
      );
      await sleep(waitMs);
    }
  }
  throw lastErr;
}

export function parseSetCookie(raw) {
  const parts = String(raw).split(";").map((p) => p.trim());
  const pair = parts[0] ?? "";
  const eq = pair.indexOf("=");
  if (eq < 0) return null;
  const name = pair.slice(0, eq).trim();
  const value = pair.slice(eq + 1).trim();
  let domain = null;
  let path = "/";
  let maxAge = null;
  let expires = null;
  let httpOnly = false;
  let secure = false;
  let sameSite = "";
  for (const attr of parts.slice(1)) {
    const idx = attr.indexOf("=");
    const k = (idx >= 0 ? attr.slice(0, idx) : attr).trim().toLowerCase();
    const v = idx >= 0 ? attr.slice(idx + 1).trim() : "";
    if (k === "domain" && v) domain = v.replace(/^\./, "").toLowerCase();
    else if (k === "path" && v) path = v;
    else if (k === "max-age" && v !== "") maxAge = Number(v);
    else if (k === "expires" && v) expires = v;
    else if (k === "httponly") httpOnly = true;
    else if (k === "secure") secure = true;
    else if (k === "samesite") sameSite = v.toLowerCase();
  }
  return { name, value, domain, path, maxAge, expires, httpOnly, secure, sameSite, raw };
}

/**
 * A browser-like cookie store shared by every host the browser visits, with the RFC 6265
 * domain-matching rules that matter here: a cookie without `Domain=` is host-only (sent back
 * only to the exact host that set it); a cookie with `Domain=d` is sent to `d` and every
 * subdomain of `d`. Ports are ignored, as browsers do. Secure/SameSite are not enforced (the
 * suite talks plain HTTP inside compose); cookie attributes are asserted by the tests.
 */
export class Browser {
  constructor(label = "browser") {
    this.label = label;
    /** @type {Map<string, {name:string,value:string,domain:string,hostOnly:boolean,httpOnly:boolean,secure:boolean,sameSite:string,path:string,maxAge:number|null,raw:string}>} */
    this.cookies = new Map();
  }

  /** A client that sends requests with `Host: <host>` and this browser's cookies. */
  at(host) {
    return new HostClient(this, host);
  }

  cookiesFor(host) {
    const h = hostnameOf(host);
    return [...this.cookies.values()].filter((c) =>
      c.hostOnly ? c.domain === h : h === c.domain || h.endsWith(`.${c.domain}`)
    );
  }

  cookieHeaderFor(host) {
    return this.cookiesFor(host)
      .map((c) => `${c.name}=${c.value}`)
      .join("; ");
  }

  getCookie(host, name) {
    const list = this.cookiesFor(host);
    const hit = list.find((c) => c.name === name);
    return hit ? hit.value : undefined;
  }

  getCookieMeta(host, name) {
    const list = this.cookiesFor(host);
    return list.find((c) => c.name === name);
  }

  clearCookies(host) {
    if (!host) {
      this.cookies.clear();
      return;
    }
    const h = hostnameOf(host);
    for (const [k, c] of this.cookies.entries()) {
      if (c.domain === h) this.cookies.delete(k);
    }
  }

  store(host, rawList) {
    const h = hostnameOf(host);
    for (const raw of rawList) {
      const meta = parseSetCookie(raw);
      if (!meta) continue;
      const domain = meta.domain ?? h;
      const hostOnly = !meta.domain;
      const expired =
        (meta.maxAge !== null && meta.maxAge <= 0) ||
        (meta.expires !== null && !Number.isNaN(Date.parse(meta.expires)) && Date.parse(meta.expires) <= Date.now());
      // A host may only set cookies for itself or a parent domain.
      if (!hostOnly && h !== domain && !h.endsWith(`.${domain}`)) continue;
      const key = `${meta.name}|${domain}|${hostOnly}`;
      if (expired || meta.value === "") this.cookies.delete(key);
      else {
        this.cookies.set(key, {
          name: meta.name,
          value: meta.value,
          domain,
          hostOnly,
          httpOnly: meta.httpOnly,
          secure: meta.secure,
          sameSite: meta.sameSite,
          path: meta.path,
          maxAge: meta.maxAge,
          raw: meta.raw,
        });
      }
    }
  }
}

/** Raw request with a verbatim `Host` header (undici `fetch` overwrites `Host`). */
export function rawRequest(
  host,
  path,
  { method = "GET", headers = {}, body, rawBody, contentType, chunked = false, timeoutMs } = {}
) {
  const target = new URL(path, config.webURL);
  const lib = target.protocol === "https:" ? https : http;
  const reqHeaders = {};
  for (const [k, v] of Object.entries(headers)) {
    if (v !== undefined && v !== null) reqHeaders[k.toLowerCase()] = v;
  }
  if (host) reqHeaders.host = host;

  let payload;
  if (rawBody !== undefined) {
    payload = Buffer.isBuffer(rawBody)
      ? rawBody
      : rawBody instanceof Uint8Array
        ? Buffer.from(rawBody)
        : Buffer.from(String(rawBody), "utf8");
    if (contentType && !reqHeaders["content-type"]) reqHeaders["content-type"] = contentType;
    if (!chunked) reqHeaders["content-length"] = payload.byteLength;
    else reqHeaders["transfer-encoding"] = "chunked";
  } else if (body !== undefined) {
    payload = Buffer.isBuffer(body) || body instanceof Uint8Array
      ? Buffer.from(body)
      : Buffer.from(typeof body === "string" ? body : JSON.stringify(body), "utf8");
    if (contentType && !reqHeaders["content-type"]) reqHeaders["content-type"] = contentType;
    else if (!reqHeaders["content-type"]) reqHeaders["content-type"] = "application/json";
    if (!chunked) reqHeaders["content-length"] = payload.byteLength;
    else reqHeaders["transfer-encoding"] = "chunked";
  }

  return new Promise((resolve, reject) => {
    const req = lib.request(
      {
        protocol: target.protocol,
        hostname: target.hostname,
        port: target.port || (target.protocol === "https:" ? 443 : 80),
        path: `${target.pathname}${target.search}`,
        method,
        headers: reqHeaders,
        timeout: timeoutMs ?? config.httpTimeoutMs,
      },
      (res) => {
        const chunks = [];
        res.on("data", (c) => chunks.push(c));
        res.on("end", () => {
          const rawBuffer = Buffer.concat(chunks);
          const text = rawBuffer.toString("utf8");
          let data;
          try {
            data = text ? JSON.parse(text) : undefined;
          } catch {
            data = undefined;
          }
          const setCookie = res.headers["set-cookie"] ?? [];
          resolve({
            status: res.statusCode ?? 0,
            headers: res.headers,
            location: res.headers.location ?? "",
            setCookie: Array.isArray(setCookie) ? setCookie : [setCookie],
            rawBuffer,
            text,
            data,
          });
        });
      }
    );
    req.on("timeout", () => req.destroy(new Error(`${method} ${host}${path} timed out`)));
    req.on("error", reject);
    if (payload !== undefined) {
      if (chunked) {
        const chunkSize = 64 * 1024;
        for (let off = 0; off < payload.byteLength; off += chunkSize) {
          req.write(payload.subarray(off, Math.min(payload.byteLength, off + chunkSize)));
        }
      } else {
        req.write(payload);
      }
    }
    req.end();
  });
}

/**
 * Client for one host of the web app, sharing its browser's cookie jar. Writes send the
 * Origin a page on this host would send; `origin: null` omits it, a string overrides it.
 * Honors HTTP 429 + Retry-After automatically unless `retry429: false`.
 */
export class HostClient {
  constructor(browser, host) {
    this.browser = browser;
    this.host = host;
  }

  async request(method, path, opts = {}) {
    const upper = method.toUpperCase();
    const retry429 = opts.retry429 !== false;
    let totalWaitedMs = 0;

    while (true) {
      const headers = { accept: "application/json", ...(opts.headers ?? {}) };
      const write = !["GET", "HEAD", "OPTIONS"].includes(upper);
      const origin =
        opts.origin === undefined ? (write ? originOf(this.host) : undefined) : opts.origin;
      if (origin) headers.origin = origin;
      if (opts.idempotencyKey && !headers["idempotency-key"] && !headers["Idempotency-Key"]) {
        headers["idempotency-key"] = opts.idempotencyKey;
      }

      const cookie =
        opts.cookieOverride !== undefined
          ? opts.cookieOverride
          : opts.cookies === false
            ? ""
            : this.browser.cookieHeaderFor(this.host);
      if (cookie) headers.cookie = cookie;

      const res = await rawRequest(this.host, path, {
        method: upper,
        headers,
        body: opts.json !== undefined ? opts.json : opts.body,
        rawBody: opts.rawBody,
        contentType: opts.contentType,
        chunked: opts.chunked,
        timeoutMs: opts.timeoutMs,
      });

      if (res.status === 429 && retry429) {
        const waitMs = parseRetryAfterMs(res.headers["retry-after"]) + 250;
        if (totalWaitedMs + waitMs <= RATE_LIMIT_MAX_WAIT_MS) {
          totalWaitedMs += waitMs;
          console.log(
            `[429] ${upper} ${this.host}${path} rate limited; waiting ${Math.ceil(waitMs / 1000)}s (${Math.ceil(totalWaitedMs / 1000)}s/${RATE_LIMIT_MAX_WAIT_MS / 1000}s)`
          );
          await sleep(waitMs);
          continue;
        }
      }

      if (opts.updateJar !== false) {
        this.browser.store(this.host, res.setCookie);
      }
      return res;
    }
  }

  get(path, opts) {
    return this.request("GET", path, opts);
  }

  async page(path, opts = {}) {
    const retry429 = opts.retry429 !== false;
    const startMs = Date.now();
    while (true) {
      const res = await this.request("GET", path, {
        ...opts,
        headers: { accept: "text/html,application/xhtml+xml", ...(opts.headers ?? {}) },
      });
      if (
        !retry429 ||
        res.status !== 200 ||
        opts.cookieOverride !== undefined ||
        res.setCookie.length > 0 ||
        !res.text.includes(RATE_LIMIT_SSR_TEXT)
      ) {
        return res;
      }
      const waitMs =
        parseRetryAfterMs(res.headers["retry-after"], RATE_LIMIT_SSR_DEFAULT_WAIT_MS) + 250;
      if (Date.now() - startMs + waitMs > RATE_LIMIT_MAX_WAIT_MS) {
        return res;
      }
      console.log(
        `[429] page GET ${this.host}${path} rendered SSR rate limit; waiting ${Math.ceil(waitMs / 1000)}s`
      );
      await sleep(waitMs);
    }
  }

  post(path, json, opts = {}) {
    return this.request("POST", path, { ...opts, json });
  }
  put(path, json, opts = {}) {
    return this.request("PUT", path, { ...opts, json });
  }
  patch(path, json, opts = {}) {
    return this.request("PATCH", path, { ...opts, json });
  }
  del(path, opts) {
    return this.request("DELETE", path, opts);
  }
}

/** Fail with the response body attached so the owner can reproduce. */
export function expectStatus(res, allowed, what) {
  const ok = Array.isArray(allowed) ? allowed : [allowed];
  if (!ok.includes(res.status)) {
    throw new Error(`${what}: expected HTTP ${ok.join("/")}, got ${res.status}: ${res.text.slice(0, 600)}`);
  }
  return res.data;
}

export async function pollUntil(
  desc,
  fn,
  { timeoutMs = config.pollTimeoutMs, intervalMs = config.pollIntervalMs } = {}
) {
  const deadline = Date.now() + timeoutMs;
  let last = "";
  while (Date.now() < deadline) {
    const [done, info, value] = await fn();
    if (done) return value;
    last = info ?? "";
    await sleep(intervalMs);
  }
  throw new Error(`timed out waiting for ${desc}: ${last}`);
}

export const uid = () => `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`;

/** Gateway JSON call (test discovery / external actors / gateway-only checks). */
export async function gw(
  method,
  path,
  { token, body, headers: extraHeaders = {}, idempotencyKey, retry429 = true, timeoutMs } = {}
) {
  const upper = method.toUpperCase();
  let totalWaitedMs = 0;
  while (true) {
    const headers = { accept: "application/json", ...extraHeaders };
    if (token) headers.authorization = `Bearer ${token}`;
    if (idempotencyKey) headers["idempotency-key"] = idempotencyKey;
    let payload;
    if (body !== undefined) {
      headers["content-type"] = headers["content-type"] || "application/json";
      payload = typeof body === "string" ? body : JSON.stringify(body);
    }
    const res = await fetch(`${config.gatewayURL}${path}`, {
      method: upper,
      headers,
      body: payload,
      signal: AbortSignal.timeout(timeoutMs ?? config.httpTimeoutMs),
    });
    if (res.status === 429 && retry429) {
      const waitMs = parseRetryAfterMs(res.headers.get("retry-after")) + 250;
      if (totalWaitedMs + waitMs <= RATE_LIMIT_MAX_WAIT_MS) {
        await res.body?.cancel().catch(() => {});
        totalWaitedMs += waitMs;
        await sleep(waitMs);
        continue;
      }
    }
    const text = await res.text();
    let data;
    try {
      data = text ? JSON.parse(text) : undefined;
    } catch {
      data = undefined;
    }
    return { status: res.status, headers: res.headers, text, data };
  }
}

export function normalizeStatus(raw) {
  return String(raw ?? "")
    .trim()
    .toUpperCase()
    .replace(
      /^(?:ORDER_STATUS_|RETURN_STATUS_|SHIPMENT_STATUS_|REFUND_STATUS_|SHOP_STATUS_|PAYMENT_STATUS_|PRODUCT_STATUS_|DISCOUNT_PROGRAM_STATUS_|USER_STATUS_)/,
      ""
    );
}

export function getBffError(res) {
  return {
    code: String(res?.data?.error?.code ?? res?.data?.code ?? ""),
    message: String(res?.data?.error?.message ?? res?.data?.message ?? ""),
  };
}

export function hasObjectKeyDeep(node, targetKey) {
  if (!node || typeof node !== "object") return false;
  if (Array.isArray(node)) {
    return node.some((item) => hasObjectKeyDeep(item, targetKey));
  }
  for (const [k, v] of Object.entries(node)) {
    if (k === targetKey) return true;
    if (hasObjectKeyDeep(v, targetKey)) return true;
  }
  return false;
}

export function buildMultipartCsv(fieldName, filename, csvContent) {
  const boundary = `----NexusMartE2E${randomBytes(8).toString("hex")}`;
  const rawBody = Buffer.from(
    `--${boundary}\r\n` +
      `Content-Disposition: form-data; name="${fieldName}"; filename="${filename}"\r\n` +
      `Content-Type: text/csv; charset=utf-8\r\n\r\n` +
      `${csvContent}\r\n` +
      `--${boundary}--\r\n`,
    "utf8"
  );
  return { rawBody, contentType: `multipart/form-data; boundary=${boundary}` };
}

export function getAuthCookieValues(browser, host) {
  const at = browser.getCookie(host, "__Host-nm_at") ?? browser.getCookie(host, "nm_at") ?? "";
  const rt = browser.getCookie(host, "__Host-nm_rt") ?? browser.getCookie(host, "nm_rt") ?? "";
  const atName = browser.getCookie(host, "__Host-nm_at") ? "__Host-nm_at" : "nm_at";
  const rtName = browser.getCookie(host, "__Host-nm_rt") ? "__Host-nm_rt" : "nm_rt";
  return { at, rt, atName, rtName };
}
