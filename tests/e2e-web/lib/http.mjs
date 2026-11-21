import http from "node:http";
import https from "node:https";
import { config, originOf } from "./config.mjs";

const hostnameOf = (host) => String(host).toLowerCase().replace(/:\d+$/, "");

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
    /** @type {Map<string, {name:string,value:string,domain:string,hostOnly:boolean}>} */
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

  store(host, rawList) {
    const h = hostnameOf(host);
    for (const raw of rawList) {
      const [pair, ...attrs] = raw.split(";");
      const eq = pair.indexOf("=");
      if (eq < 0) continue;
      const name = pair.slice(0, eq).trim();
      const value = pair.slice(eq + 1).trim();
      let domain = h;
      let hostOnly = true;
      let expired = false;
      for (const a of attrs) {
        const [k, v = ""] = a.trim().split("=");
        const key = k.toLowerCase();
        if (key === "domain" && v.trim()) {
          domain = v.trim().replace(/^\./, "").toLowerCase();
          hostOnly = false;
        }
        if (key === "max-age" && Number(v) <= 0) expired = true;
        if (key === "expires" && Date.parse(v) < Date.now()) expired = true;
      }
      // A host may only set cookies for itself or a parent domain.
      if (!hostOnly && h !== domain && !h.endsWith(`.${domain}`)) continue;
      const key = `${name}|${domain}|${hostOnly}`;
      if (expired || value === "") this.cookies.delete(key);
      else this.cookies.set(key, { name, value, domain, hostOnly });
    }
  }
}

/** Raw request with a verbatim `Host` header (undici `fetch` overwrites `Host`). */
export function rawRequest(host, path, { method = "GET", headers = {}, body } = {}) {
  const target = new URL(path, config.webURL);
  const lib = target.protocol === "https:" ? https : http;
  const reqHeaders = { ...headers };
  if (host) reqHeaders.host = host;
  let payload;
  if (body !== undefined) {
    payload = typeof body === "string" ? body : JSON.stringify(body);
    if (!reqHeaders["content-type"]) reqHeaders["content-type"] = "application/json";
    reqHeaders["content-length"] = Buffer.byteLength(payload);
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
        timeout: config.httpTimeoutMs,
      },
      (res) => {
        const chunks = [];
        res.on("data", (c) => chunks.push(c));
        res.on("end", () => {
          const text = Buffer.concat(chunks).toString("utf8");
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
            text,
            data,
          });
        });
      }
    );
    req.on("timeout", () => req.destroy(new Error(`${method} ${host}${path} timed out`)));
    req.on("error", reject);
    if (payload !== undefined) req.write(payload);
    req.end();
  });
}

/**
 * Client for one host of the web app, sharing its browser's cookie jar. Writes send the
 * Origin a page on this host would send; `origin: null` omits it, a string overrides it.
 */
export class HostClient {
  constructor(browser, host) {
    this.browser = browser;
    this.host = host;
  }

  async request(method, path, opts = {}) {
    const headers = { accept: "application/json", ...(opts.headers ?? {}) };
    const write = !["GET", "HEAD", "OPTIONS"].includes(method);
    const origin = opts.origin === undefined ? (write ? originOf(this.host) : undefined) : opts.origin;
    if (origin) headers.origin = origin;
    const cookie = opts.cookies === false ? "" : this.browser.cookieHeaderFor(this.host);
    if (cookie) headers.cookie = cookie;
    const res = await rawRequest(this.host, path, { method, headers, body: opts.json });
    this.browser.store(this.host, res.setCookie);
    return res;
  }

  get(path, opts) {
    return this.request("GET", path, opts);
  }
  page(path, opts = {}) {
    return this.request("GET", path, {
      ...opts,
      headers: { accept: "text/html,application/xhtml+xml", ...(opts.headers ?? {}) },
    });
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

/** Gateway JSON call (test discovery / external actors only, never buyer actions). */
export async function gw(method, path, { token, body } = {}) {
  const headers = { accept: "application/json" };
  if (token) headers.authorization = `Bearer ${token}`;
  let payload;
  if (body !== undefined) {
    headers["content-type"] = "application/json";
    payload = JSON.stringify(body);
  }
  const res = await fetch(`${config.gatewayURL}${path}`, {
    method,
    headers,
    body: payload,
    signal: AbortSignal.timeout(config.httpTimeoutMs),
  });
  const text = await res.text();
  let data;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    data = undefined;
  }
  return { status: res.status, text, data };
}
