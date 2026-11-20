import { config } from "./config.mjs";

// A browser-like client for the BFF: keeps its own cookie jar and sends the
// web origin on write requests the way a same-site page would. Secure/SameSite
// flags are not enforced here (the suite talks plain HTTP inside compose);
// the cookie attributes themselves are asserted in the tests.
export class WebSession {
  constructor(label = "anon") {
    this.label = label;
    this.jar = new Map();
    this.setCookieHeaders = [];
  }

  cookieHeader() {
    return [...this.jar].map(([k, v]) => `${k}=${v}`).join("; ");
  }

  storeCookies(res) {
    const list = res.headers.getSetCookie?.() ?? [];
    for (const raw of list) {
      this.setCookieHeaders.push(raw);
      const [pair, ...attrs] = raw.split(";");
      const eq = pair.indexOf("=");
      if (eq < 0) continue;
      const name = pair.slice(0, eq).trim();
      const value = pair.slice(eq + 1).trim();
      const expired = attrs.some((a) => {
        const [k, v = ""] = a.trim().split("=");
        const key = k.toLowerCase();
        return (key === "max-age" && Number(v) <= 0) || (key === "expires" && Date.parse(v) < Date.now());
      });
      if (expired || value === "") this.jar.delete(name);
      else this.jar.set(name, value);
    }
  }

  /**
   * @param {string} method
   * @param {string} path  BFF path, e.g. /api/cart
   * @param {{json?: unknown, headers?: Record<string,string>, origin?: string|null, cookies?: boolean}} [opts]
   */
  async request(method, path, opts = {}) {
    const headers = { accept: "application/json", ...(opts.headers ?? {}) };
    const write = !["GET", "HEAD", "OPTIONS"].includes(method);
    // origin: undefined → our web origin on writes; null → omit; string → as given.
    const origin = opts.origin === undefined ? (write ? config.webOrigin : undefined) : opts.origin;
    if (origin) headers.origin = origin;
    if (opts.cookies !== false && this.jar.size) headers.cookie = this.cookieHeader();
    let body;
    if (opts.json !== undefined) {
      headers["content-type"] = "application/json";
      body = JSON.stringify(opts.json);
    }
    const res = await fetch(config.webURL + path, {
      method,
      headers,
      body,
      redirect: "manual",
      signal: AbortSignal.timeout(config.httpTimeoutMs),
    });
    this.storeCookies(res);
    const text = await res.text();
    let data;
    try {
      data = text ? JSON.parse(text) : undefined;
    } catch {
      data = undefined;
    }
    return { status: res.status, headers: res.headers, text, data };
  }

  get(path, opts) {
    return this.request("GET", path, opts);
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

export async function pollUntil(desc, fn, { timeoutMs = config.pollTimeoutMs, intervalMs = 400 } = {}) {
  const deadline = Date.now() + timeoutMs;
  let last = "";
  while (Date.now() < deadline) {
    const [done, info, value] = await fn();
    if (done) return value;
    last = info ?? "";
    await new Promise((r) => setTimeout(r, intervalMs));
  }
  throw new Error(`timed out waiting for ${desc}: ${last}`);
}

export const uid = () => `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`;
