// Run: node --test src/middleware.test.mjs
import assert from "node:assert/strict";
import { existsSync, readdirSync } from "node:fs";
import { createRequire, registerHooks } from "node:module";
import { dirname, join, resolve } from "node:path";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const WEB_SRC = __dirname;
const WEB_ROOT = resolve(__dirname, "..");
const WEB_PUBLIC = resolve(WEB_ROOT, "public");

const require = createRequire(import.meta.url);
const { getMiddlewareMatchers } = require(
  resolve(WEB_ROOT, "node_modules/next/dist/build/analysis/get-page-static-info.js")
);
const { getMiddlewareRouteMatcher } = require(
  resolve(WEB_ROOT, "node_modules/next/dist/shared/lib/router/utils/middleware-route-matcher.js")
);

const VIRTUAL_MODULES = {
  "virtual:next-server": `
    class MockCookieJar {
      constructor(initial = []) {
        this._map = new Map(initial);
        this.mutations = [];
      }
      get(name) {
        const value = this._map.get(name);
        return value === undefined ? undefined : { name, value };
      }
      set(name, value, options = {}) {
        this._map.set(name, value);
        this.mutations.push({ name, value, options });
      }
    }
    export class NextResponse extends Response {
      constructor(body, init = {}) {
        super(body, init);
        this.cookies = new MockCookieJar();
      }
      static next(init = {}) {
        const res = new NextResponse(null, { status: 200 });
        res._action = "next";
        res._requestHeaders = init?.request?.headers ?? null;
        return res;
      }
      static rewrite(url, init = {}) {
        const res = new NextResponse(null, { status: 200 });
        res._action = "rewrite";
        res._rewriteUrl = String(url);
        res._requestHeaders = init?.request?.headers ?? null;
        return res;
      }
      static redirect(url, init = 307) {
        const status = typeof init === "number" ? init : (init?.status ?? 307);
        const headers = new Headers(typeof init === "object" ? init?.headers : undefined);
        headers.set("location", String(url));
        const res = new NextResponse(null, { status, headers });
        res._action = "redirect";
        return res;
      }
    }
  `,
};

function resolveTsFile(basePath) {
  for (const candidate of [
    `${basePath}.ts`,
    `${basePath}.tsx`,
    resolve(basePath, "index.ts"),
    resolve(basePath, "index.tsx"),
  ]) {
    if (existsSync(candidate)) return pathToFileURL(candidate).href;
  }
  return null;
}

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === "next/server") return { shortCircuit: true, url: "virtual:next-server" };
    if (specifier.startsWith("@/")) {
      const resolved = resolveTsFile(resolve(WEB_SRC, specifier.slice(2)));
      if (resolved) return nextResolve(resolved, context);
    }
    if (
      (specifier.startsWith("./") || specifier.startsWith("../")) &&
      !/\.[a-z]+$/i.test(specifier) &&
      context.parentURL?.startsWith("file:")
    ) {
      const parentDir = dirname(fileURLToPath(context.parentURL));
      const resolved = resolveTsFile(resolve(parentDir, specifier));
      if (resolved) return nextResolve(resolved, context);
    }
    return nextResolve(specifier, context);
  },
  load(url, context, nextLoad) {
    if (url in VIRTUAL_MODULES) {
      return { shortCircuit: true, format: "module", source: VIRTUAL_MODULES[url] };
    }
    return nextLoad(url, context);
  },
});

const { config, middleware } = await import("./middleware.ts");

function walkPublicFiles(dir, prefix = "") {
  const files = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const rel = prefix ? `${prefix}/${entry.name}` : entry.name;
    if (entry.isDirectory()) {
      files.push(...walkPublicFiles(join(dir, entry.name), rel));
    } else {
      files.push(`/${rel}`);
    }
  }
  return files.sort();
}

function makeNextRequest(urlStr, host, method = "GET", cookieMap = {}, extraHeaders = {}) {
  const url = new URL(urlStr);
  const nextUrl = Object.assign(new URL(urlStr), {
    clone() {
      return new URL(this.href);
    },
  });
  const headers = new Headers({ host, ...extraHeaders });
  const cookieHeader = Object.entries(cookieMap)
    .map(([k, v]) => `${k}=${v}`)
    .join("; ");
  if (cookieHeader) headers.set("cookie", cookieHeader);
  return {
    method,
    url: url.href,
    nextUrl,
    headers,
    cookies: {
      get(name) {
        const value = cookieMap[name];
        return value === undefined ? undefined : { name, value };
      },
    },
  };
}

test("config.matcher matches all app/API/internal routes including static-suffix, %2e, uppercase & traversal attacks, while skipping only _next and real web/public files", () => {
  const compiled = getMiddlewareMatchers(config.matcher, {});
  assert.ok(compiled.length > 0, "config.matcher must compile cleanly via Next.js");
  const matches = getMiddlewareRouteMatcher(compiled);

  // 1. Every real static file currently in web/public MUST be skipped by middleware.
  const publicFiles = walkPublicFiles(WEB_PUBLIC);
  assert.ok(publicFiles.length > 0, "web/public must contain static assets");
  for (const pubPath of publicFiles) {
    assert.equal(matches(pubPath), false, `real web/public file must skip middleware: ${pubPath}`);
  }

  // 2. Next.js internals and app static icons MUST be skipped.
  const allowedStaticSkips = [
    "/_next/static/chunks/main-app.js",
    "/_next/static/css/app-1234abcd.css",
    "/_next/static/media/archivo-latin.woff2",
    "/_next/static/chunks/app/(site)/s/%5Bhost%5D/san-pham/%5Bslug%5D/page-abc.js",
    "/_next/static/chunks/app/%28site%29/s/%5Bhost%5D/page-abc.js",
    "/_next/image",
    "/favicon.ico",
    "/icon.svg",
  ];
  for (const staticPath of allowedStaticSkips) {
    assert.equal(matches(staticPath), false, `static internal path must skip middleware: ${staticPath}`);
  }

  // 3. Real application, API, and host-aware metadata routes MUST run through middleware.
  const realRoutes = [
    "/",
    "/ve-nexusmart",
    "/tro-giup",
    "/dang-nhap",
    "/dang-ky",
    "/san-pham",
    "/san-pham/ao-thun-cotton-compact-trang",
    "/gian-hang/bach-dang",
    "/tim-kiem",
    "/flash-sale",
    "/voucher",
    "/gio-hang",
    "/thanh-toan",
    "/thanh-toan/cho/pay-1",
    "/don-hang",
    "/don-hang/ord-1",
    "/tai-khoan",
    "/seller",
    "/seller/site",
    "/seller/don-hang/ord-1",
    "/admin",
    "/admin/sites",
    "/admin/flash-sale",
    "/api/auth/me",
    "/api/auth/logout",
    "/api/cart",
    "/api/checkout",
    "/api/orders",
    "/api/seller/orders",
    "/api/admin/sites",
    "/robots.txt",
    "/sitemap.xml",
  ];
  for (const route of realRoutes) {
    assert.equal(matches(route), true, `real route must pass through middleware: ${route}`);
  }

  // 4. Attack URLs with static extensions (.json, .js, .css, .png, ...), %2e encoding,
  //    uppercase letters, and path traversal under excluded prefixes MUST pass through middleware.
  const attackUrls = [
    // Direct internal /s/* bypass attempts
    "/s",
    "/s/bach-dang.nexusmart.vn",
    "/s/bach-dang.nexusmart.vn/san-pham/x.json",
    "/s/bach-dang.nexusmart.vn/san-pham/x.js",
    "/s/bach-dang.nexusmart.vn/san-pham/x.css",
    "/s/bach-dang.nexusmart.vn/san-pham/x.png",
    "/s/bach-dang.nexusmart.vn/san-pham/x.svg",
    "/s/bach-dang.nexusmart.vn/san-pham/x.ico",
    "/s/bach-dang.nexusmart.vn/san-pham/x.woff2",
    "/s/bach-dang.nexusmart.vn/san-pham/x.map",
    "/s/bach-dang.nexusmart.vn/san-pham/x%2ejson",
    "/s/bach-dang.nexusmart.vn/san-pham/x%2Ejs",
    "/s/bach-dang.nexusmart.vn/san-pham/X.JSON",
    "/s/bach-dang.nexusmart.vn/san-pham/X.JS",
    "/S/bach-dang.nexusmart.vn/san-pham/x.json",
    // Tenant-blocked /api/seller/* and /api/admin/* bypass attempts
    "/api/seller/orders/x.json",
    "/api/seller/orders/x.js",
    "/api/seller/site/x.css",
    "/api/seller/orders/x%2ejson",
    "/api/seller/orders/X.JSON",
    "/api/admin/sites/x.json",
    "/api/admin/users/x.js",
    "/api/admin/users/x.png",
    "/api/admin/audit-logs/x%2ejson",
    "/api/admin/audit-logs/x%2Ejs",
    "/api/admin/sites/X.JS",
    "/api/cart/items/sku.json",
    "/api/orders/ord-1.js",
    "/API/seller/orders/x.json",
    "/API/admin/sites/x.js",
    // Seller, admin, and storefront page routes with static suffixes / encoding / uppercase
    "/seller/don-hang/x.json",
    "/seller/site/x.js",
    "/seller/san-pham/x%2ejson",
    "/seller/san-pham/X.PNG",
    "/SELLER/don-hang/x.json",
    "/admin/sites/x.json",
    "/admin/flash-sale/x.js",
    "/admin/doi-soat/x%2ejson",
    "/admin/doi-soat/X.CSS",
    "/ADMIN/sites/x.js",
    "/san-pham/ao-x.json",
    "/san-pham/ao-x.js",
    "/san-pham/ao-x.css",
    "/san-pham/ao-x.png",
    "/san-pham/ao-x%2ejson",
    "/san-pham/ao-x%2Ejs",
    "/san-pham/ao-X.JSON",
    "/SAN-PHAM/ao-x.json",
    "/don-hang/ord.json",
    "/tai-khoan/profile.js",
    // Arbitrary root paths with static suffixes not in web/public
    "/x.json",
    "/x.js",
    "/x.css",
    "/x.png",
    // Traversal / smuggling / case / suffix bypasses under excluded static prefixes
    "/products/../s/bach-dang.nexusmart.vn",
    "/products/%2e%2e/s/bach-dang.nexusmart.vn",
    "/products/%2E%2E/api/seller/orders",
    "/products/x.json",
    "/products/x.js",
    "/products/sub/ao-thun.png",
    "/products/ao-thun-cotton-trang.PNG",
    "/PRODUCTS/ao-thun-cotton-trang.png",
    "/products/ao-thun%2epng",
    "/models/%2e%2e/admin",
    "/models/x.json",
    "/banners/../api/admin/sites",
    "/banners/x.js",
    "/atmosphere/x.json",
    "/atmosphere/%2e%2e/s/bach-dang.nexusmart.vn",
    "/_next/static/../s/bach-dang.nexusmart.vn",
    "/_next/static/%2e%2e/api/seller/orders",
    "/_next/static/%2E%2E/admin",
    "/_next/image/../../s/bach-dang.nexusmart.vn",
    "/_next/image.json",
    "/favicon.ico/foo",
    "/favicon.ico.json",
    "/favicon.ico.js",
    "/FAVICON.ICO",
    "/icon.svg/foo",
    "/icon.svg.json",
    "/ICON.SVG",
    "/media-manifest.json/foo",
    "/media-manifest.json.js",
    "/MEDIA-MANIFEST.JSON",
  ];
  for (const attack of attackUrls) {
    assert.equal(matches(attack), true, `attack URL must NOT bypass middleware: ${attack}`);
  }
});

test("middleware blocks /s/.../x.json, blocks /api/seller/.../x.json on tenant host, and rewrites /san-pham/x.json on tenant host", async () => {
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (rawUrl) => {
    const url = new URL(String(rawUrl));
    if (url.pathname === "/api/v1/sites/by-host") {
      return new Response(
        JSON.stringify({
          site: {
            id: "site-bd",
            shop_id: "shop-bd",
            status: "PUBLISHED",
            primary_host: "bach-dang.nexusmart.localhost",
          },
          shop: { status: "ACTIVE" },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      );
    }
    return new Response(JSON.stringify({ error: { code: "NOT_FOUND", message: "not found" } }), {
      status: 404,
      headers: { "Content-Type": "application/json" },
    });
  };

  try {
    // 1. Direct internal /s/.../x.json on root host -> 404
    const directRes = await middleware(
      makeNextRequest(
        "http://nexusmart.localhost/s/bach-dang.nexusmart.localhost/san-pham/x.json",
        "nexusmart.localhost"
      )
    );
    assert.equal(directRes.status, 404);

    // 2. Tenant host /api/seller/orders/x.json -> 404
    const blockedApiRes = await middleware(
      makeNextRequest(
        "http://bach-dang.nexusmart.localhost/api/seller/orders/x.json",
        "bach-dang.nexusmart.localhost"
      )
    );
    assert.equal(blockedApiRes.status, 404);

    // 3. Tenant host /san-pham/ao-x.json -> rewritten to /s/bach-dang.nexusmart.localhost/san-pham/ao-x.json
    const rewriteRes = await middleware(
      makeNextRequest(
        "http://bach-dang.nexusmart.localhost/san-pham/ao-x.json",
        "bach-dang.nexusmart.localhost"
      )
    );
    assert.equal(rewriteRes._action, "rewrite");
    assert.equal(
      new URL(rewriteRes._rewriteUrl).pathname,
      "/s/bach-dang.nexusmart.localhost/san-pham/ao-x.json"
    );
  } finally {
    globalThis.fetch = origFetch;
  }
});

test("middleware does NOT redirect or clear cookies when /api/v1/auth/refresh fails with 429/503/network on protected page", async () => {
  const { ACCESS_COOKIE, REFRESH_COOKIE } = await import("./lib/server/config.ts");
  const header = Buffer.from(JSON.stringify({ alg: "HS256", typ: "JWT" })).toString("base64url");
  const expiredPayload = Buffer.from(
    JSON.stringify({ sub: "u1", exp: Math.floor(Date.now() / 1000) - 60 })
  ).toString("base64url");
  const expiredAt = `${header}.${expiredPayload}.sig`;

  const origFetch = globalThis.fetch;
  try {
    // 1. 429 from /api/v1/auth/refresh -> returns 429 + Retry-After, no redirect, no cookie clear
    globalThis.fetch = async () =>
      new Response(JSON.stringify({ error: { code: "RESOURCE_EXHAUSTED", message: "rate" } }), {
        status: 429,
        headers: { "Content-Type": "application/json", "Retry-After": "25" },
      });
    const res429 = await middleware(
      makeNextRequest("http://nexusmart.localhost/seller", "nexusmart.localhost", "GET", {
        [ACCESS_COOKIE]: expiredAt,
        [REFRESH_COOKIE]: `rt-mw-429-${Date.now()}`,
      })
    );
    assert.equal(res429.status, 429);
    assert.equal(res429.headers.get("retry-after"), "25");
    assert.equal(res429.cookies.mutations.length, 0);

    // 2. 503 from /api/v1/auth/refresh -> returns 503, no redirect, no cookie clear
    globalThis.fetch = async () =>
      new Response(JSON.stringify({ error: { code: "UNAVAILABLE", message: "down" } }), {
        status: 503,
        headers: { "Content-Type": "application/json" },
      });
    const res503 = await middleware(
      makeNextRequest("http://nexusmart.localhost/seller", "nexusmart.localhost", "GET", {
        [ACCESS_COOKIE]: expiredAt,
        [REFRESH_COOKIE]: `rt-mw-503-${Date.now()}`,
      })
    );
    assert.equal(res503.status, 503);
    assert.equal(res503.cookies.mutations.length, 0);

    // 3. Network error -> returns 503, no redirect, no cookie clear
    globalThis.fetch = async () => {
      throw new TypeError("fetch failed");
    };
    const resNet = await middleware(
      makeNextRequest("http://nexusmart.localhost/seller", "nexusmart.localhost", "GET", {
        [ACCESS_COOKIE]: expiredAt,
        [REFRESH_COOKIE]: `rt-mw-net-${Date.now()}`,
      })
    );
    assert.equal(resNet.status, 503);
    assert.equal(resNet.cookies.mutations.length, 0);
  } finally {
    globalThis.fetch = origFetch;
  }
});

test("middleware returns 503 + Retry-After: 5 + no-store on site lookup error and 404 on not_found", async () => {
  const { publishedSiteHostCache } = await import("./lib/server/siteLookup.ts");
  publishedSiteHostCache.clear();
  const origFetch = globalThis.fetch;

  try {
    // 1. Upstream 429 on /api/v1/sites/by-host -> 503 with Retry-After: 5 + no-store
    globalThis.fetch = async () =>
      new Response(JSON.stringify({ error: { code: "RATE_LIMITED", message: "rate" } }), {
        status: 429,
        headers: { "Content-Type": "application/json", "Retry-After": "10" },
      });
    const res429 = await middleware(
      makeNextRequest(
        "http://busy-429.nexusmart.localhost/san-pham",
        "busy-429.nexusmart.localhost"
      )
    );
    assert.equal(res429.status, 503);
    assert.equal(res429.headers.get("retry-after"), "5");
    assert.equal(res429.headers.get("cache-control"), "no-store");
    assert.match(res429.headers.get("content-type") ?? "", /^text\/plain/i);
    assert.equal(await res429.text(), "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút.");

    // 2. Upstream 503 / network error -> 503 with Retry-After: 5 + no-store
    globalThis.fetch = async () => {
      throw new TypeError("connect ECONNREFUSED");
    };
    const resNet = await middleware(
      makeNextRequest("http://busy-net.nexusmart.localhost/", "busy-net.nexusmart.localhost")
    );
    assert.equal(resNet.status, 503);
    assert.equal(resNet.headers.get("retry-after"), "5");
    assert.equal(resNet.headers.get("cache-control"), "no-store");
    assert.equal(await resNet.text(), "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút.");

    // 3. Upstream 404 (not_found) -> 404 as before
    globalThis.fetch = async () =>
      new Response(JSON.stringify({ error: { code: "NOT_FOUND", message: "not found" } }), {
        status: 404,
        headers: { "Content-Type": "application/json" },
      });
    const res404 = await middleware(
      makeNextRequest("http://missing-shop.nexusmart.localhost/", "missing-shop.nexusmart.localhost")
    );
    assert.equal(res404.status, 404);
    assert.equal(res404.headers.get("cache-control"), "no-store");
    assert.equal(await res404.text(), "Not Found");

    // 4. Upstream 200 with SUSPENDED site -> 404 (not 503)
    globalThis.fetch = async () =>
      new Response(
        JSON.stringify({
          site: { id: "s1", shop_id: "sh1", status: "SUSPENDED" },
          shop: { status: "ACTIVE" },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      );
    const resSuspended = await middleware(
      makeNextRequest(
        "http://suspended-shop.nexusmart.localhost/",
        "suspended-shop.nexusmart.localhost"
      )
    );
    assert.equal(resSuspended.status, 404);
  } finally {
    globalThis.fetch = origFetch;
    publishedSiteHostCache.clear();
  }
});

test("middleware forwards X-Forwarded-For on /sites/by-host lookup only when TRUST_PROXY_HEADERS=true and always ignores X-Forwarded-Host", async () => {
  const { publishedSiteHostCache } = await import("./lib/server/siteLookup.ts");
  publishedSiteHostCache.clear();
  const origFetch = globalThis.fetch;
  const prevTrust = process.env.TRUST_PROXY_HEADERS;
  const seen = [];

  globalThis.fetch = async (rawUrl, init) => {
    const url = new URL(String(rawUrl));
    seen.push({ url, headers: new Headers(init?.headers) });
    return new Response(
      JSON.stringify({
        site: {
          id: "site-mw-xff",
          shop_id: "shop-mw-xff",
          status: "PUBLISHED",
          primary_host: url.searchParams.get("host"),
        },
        shop: { status: "ACTIVE" },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } }
    );
  };

  try {
    // 1. TRUST_PROXY_HEADERS=true -> forwards XFF on cache miss, ignores spoofed X-Forwarded-Host.
    process.env.TRUST_PROXY_HEADERS = "true";
    const resTrusted = await middleware(
      makeNextRequest(
        "http://alpha.nexusmart.localhost/san-pham",
        "alpha.nexusmart.localhost",
        "GET",
        {},
        {
          "x-forwarded-for": "203.0.113.44, 10.0.0.5",
          "x-forwarded-host": "evil.nexusmart.localhost",
        }
      )
    );
    assert.equal(resTrusted._action, "rewrite");
    assert.equal(seen.length, 1);
    assert.equal(seen[0].url.searchParams.get("host"), "alpha.nexusmart.localhost");
    assert.equal(seen[0].headers.get("x-forwarded-for"), "203.0.113.44, 10.0.0.5");

    // 2. TRUST_PROXY_HEADERS=false -> omits XFF on cache miss.
    process.env.TRUST_PROXY_HEADERS = "false";
    publishedSiteHostCache.clear();
    const resUntrusted = await middleware(
      makeNextRequest(
        "http://beta.nexusmart.localhost/san-pham",
        "beta.nexusmart.localhost",
        "GET",
        {},
        {
          "x-forwarded-for": "203.0.113.44",
          "x-forwarded-host": "evil.nexusmart.localhost",
        }
      )
    );
    assert.equal(resUntrusted._action, "rewrite");
    assert.equal(seen.length, 2);
    assert.equal(seen[1].url.searchParams.get("host"), "beta.nexusmart.localhost");
    assert.equal(seen[1].headers.get("x-forwarded-for"), null);
  } finally {
    if (prevTrust === undefined) delete process.env.TRUST_PROXY_HEADERS;
    else process.env.TRUST_PROXY_HEADERS = prevTrust;
    globalThis.fetch = origFetch;
    publishedSiteHostCache.clear();
  }
});

test("middleware redirects www.<SITE_ROOT_DOMAIN> with 308 preserving pathname, query, and method, using APP_ORIGIN or request protocol fallback", async () => {
  const prevRoot = process.env.SITE_ROOT_DOMAIN;
  const prevAppOrigin = process.env.APP_ORIGIN;
  const prevTrust = process.env.TRUST_PROXY_HEADERS;
  const origFetch = globalThis.fetch;
  let fetchCalls = 0;

  globalThis.fetch = async () => {
    fetchCalls++;
    throw new Error("www redirect must not call backend");
  };

  try {
    process.env.SITE_ROOT_DOMAIN = "nexusmartai.store";
    process.env.APP_ORIGIN = "https://nexusmartai.store";
    delete process.env.TRUST_PROXY_HEADERS;

    // 1. Root path on www -> 308 to APP_ORIGIN
    const resRoot = await middleware(
      makeNextRequest("https://www.nexusmartai.store/", "www.nexusmartai.store")
    );
    assert.equal(resRoot.status, 308);
    assert.equal(resRoot.headers.get("location"), "https://nexusmartai.store/");

    // 2. Path + query string + uppercase Host + port + ignored X-Forwarded-Host -> 308
    const resPathQuery = await middleware(
      makeNextRequest(
        "https://www.nexusmartai.store/ve-nexusmart?ref=ig&tab=2",
        "WWW.NEXUSMARTAI.STORE:443",
        "GET",
        {},
        { "x-forwarded-host": "evil.com" }
      )
    );
    assert.equal(resPathQuery.status, 308);
    assert.equal(
      resPathQuery.headers.get("location"),
      "https://nexusmartai.store/ve-nexusmart?ref=ig&tab=2"
    );

    // 3. /api/* with POST -> 308 (preserves HTTP method & body)
    const resApiPost = await middleware(
      makeNextRequest(
        "https://www.nexusmartai.store/api/auth/login?mode=otp",
        "www.nexusmartai.store",
        "POST"
      )
    );
    assert.equal(resApiPost.status, 308);
    assert.equal(
      resApiPost.headers.get("location"),
      "https://nexusmartai.store/api/auth/login?mode=otp"
    );

    // 4. Trailing slash and comma-separated APP_ORIGIN are normalized
    process.env.APP_ORIGIN = " https://nexusmartai.store/ , https://backup.nexusmartai.store ";
    const resNormalizedOrigin = await middleware(
      makeNextRequest("http://www.nexusmartai.store/tro-giup?a=1", "www.nexusmartai.store")
    );
    assert.equal(resNormalizedOrigin.status, 308);
    assert.equal(resNormalizedOrigin.headers.get("location"), "https://nexusmartai.store/tro-giup?a=1");

    // 5. Protocol-relative pathname stays anchored to apex origin (no open redirect)
    const resDoubleSlash = await middleware(
      makeNextRequest("https://www.nexusmartai.store//evil.com/phish?x=1", "www.nexusmartai.store")
    );
    assert.equal(resDoubleSlash.status, 308);
    assert.equal(
      resDoubleSlash.headers.get("location"),
      "https://nexusmartai.store//evil.com/phish?x=1"
    );

    // 6. Without APP_ORIGIN -> falls back to request protocol + SITE_ROOT_DOMAIN
    delete process.env.APP_ORIGIN;
    const resFallbackHttps = await middleware(
      makeNextRequest("https://www.nexusmartai.store/dang-nhap?next=%2Fseller", "www.nexusmartai.store")
    );
    assert.equal(resFallbackHttps.status, 308);
    assert.equal(
      resFallbackHttps.headers.get("location"),
      "https://nexusmartai.store/dang-nhap?next=%2Fseller"
    );

    const resFallbackHttp = await middleware(
      makeNextRequest("http://www.nexusmartai.store:3300/ve-nexusmart", "www.nexusmartai.store:3300")
    );
    assert.equal(resFallbackHttp.status, 308);
    assert.equal(resFallbackHttp.headers.get("location"), "http://nexusmartai.store/ve-nexusmart");

    // 7. Behind trusted proxy without APP_ORIGIN -> honors X-Forwarded-Proto
    process.env.TRUST_PROXY_HEADERS = "true";
    const resFallbackProxy = await middleware(
      makeNextRequest(
        "http://web:3000/ve-nexusmart?src=cf",
        "www.nexusmartai.store",
        "GET",
        {},
        { "x-forwarded-proto": "https" }
      )
    );
    assert.equal(resFallbackProxy.status, 308);
    assert.equal(
      resFallbackProxy.headers.get("location"),
      "https://nexusmartai.store/ve-nexusmart?src=cf"
    );

    assert.equal(fetchCalls, 0);
  } finally {
    if (prevRoot === undefined) delete process.env.SITE_ROOT_DOMAIN;
    else process.env.SITE_ROOT_DOMAIN = prevRoot;
    if (prevAppOrigin === undefined) delete process.env.APP_ORIGIN;
    else process.env.APP_ORIGIN = prevAppOrigin;
    if (prevTrust === undefined) delete process.env.TRUST_PROXY_HEADERS;
    else process.env.TRUST_PROXY_HEADERS = prevTrust;
    globalThis.fetch = origFetch;
  }
});

test("middleware does NOT www-redirect root host, shop subdomain, wwww., www.evil.com, or spoofed X-Forwarded-Host", async () => {
  const { publishedSiteHostCache } = await import("./lib/server/siteLookup.ts");
  publishedSiteHostCache.clear();
  const prevRoot = process.env.SITE_ROOT_DOMAIN;
  const prevAppOrigin = process.env.APP_ORIGIN;
  const prevTrust = process.env.TRUST_PROXY_HEADERS;
  const origFetch = globalThis.fetch;

  globalThis.fetch = async (rawUrl) => {
    const url = new URL(String(rawUrl));
    if (
      url.pathname === "/api/v1/sites/by-host" &&
      url.searchParams.get("host") === "bach-dang.nexusmartai.store"
    ) {
      return new Response(
        JSON.stringify({
          site: {
            id: "site-bd",
            shop_id: "shop-bd",
            status: "PUBLISHED",
            primary_host: "bach-dang.nexusmartai.store",
          },
          shop: { status: "ACTIVE" },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      );
    }
    return new Response(JSON.stringify({ error: { code: "NOT_FOUND", message: "not found" } }), {
      status: 404,
      headers: { "Content-Type": "application/json" },
    });
  };

  try {
    process.env.SITE_ROOT_DOMAIN = "nexusmartai.store";
    process.env.APP_ORIGIN = "https://nexusmartai.store";
    process.env.TRUST_PROXY_HEADERS = "true";

    // 1. Root host (even with spoofed X-Forwarded-Host: www.<root>) -> NOT redirected
    const resRoot = await middleware(
      makeNextRequest(
        "https://nexusmartai.store/ve-nexusmart",
        "nexusmartai.store",
        "GET",
        {},
        { "x-forwarded-host": "www.nexusmartai.store" }
      )
    );
    assert.equal(resRoot._action, "next");
    assert.equal(resRoot.status, 200);

    // 2. Shop subdomain -> rewritten to /s/<host>, NOT redirected
    const resShop = await middleware(
      makeNextRequest("https://bach-dang.nexusmartai.store/san-pham", "bach-dang.nexusmartai.store")
    );
    assert.equal(resShop._action, "rewrite");
    assert.equal(
      new URL(resShop._rewriteUrl).pathname,
      "/s/bach-dang.nexusmartai.store/san-pham"
    );

    // 3. wwww.<root> -> NOT redirected (unknown site -> 404)
    const resWwww = await middleware(
      makeNextRequest("https://wwww.nexusmartai.store/", "wwww.nexusmartai.store")
    );
    assert.notEqual(resWwww.status, 308);
    assert.equal(resWwww.status, 404);

    // 4. www.evil.com -> NOT redirected (unknown custom domain -> 404)
    const resEvil = await middleware(
      makeNextRequest("https://www.evil.com/", "www.evil.com")
    );
    assert.notEqual(resEvil.status, 308);
    assert.equal(resEvil.status, 404);

    // 5. Multi-level www.bach-dang.<root> and suffix spoof www.<root>.evil.com -> NOT redirected
    const resNested = await middleware(
      makeNextRequest(
        "https://www.bach-dang.nexusmartai.store/",
        "www.bach-dang.nexusmartai.store"
      )
    );
    assert.notEqual(resNested.status, 308);
    assert.equal(resNested.status, 404);

    const resSuffix = await middleware(
      makeNextRequest(
        "https://www.nexusmartai.store.evil.com/",
        "www.nexusmartai.store.evil.com"
      )
    );
    assert.notEqual(resSuffix.status, 308);
    assert.equal(resSuffix.status, 404);
  } finally {
    if (prevRoot === undefined) delete process.env.SITE_ROOT_DOMAIN;
    else process.env.SITE_ROOT_DOMAIN = prevRoot;
    if (prevAppOrigin === undefined) delete process.env.APP_ORIGIN;
    else process.env.APP_ORIGIN = prevAppOrigin;
    if (prevTrust === undefined) delete process.env.TRUST_PROXY_HEADERS;
    else process.env.TRUST_PROXY_HEADERS = prevTrust;
    globalThis.fetch = origFetch;
    publishedSiteHostCache.clear();
  }
});

test("middleware passes /media/* straight through on root host and published tenant hosts without rewriting to /s/[host]", async () => {
  const { publishedSiteHostCache } = await import("./lib/server/siteLookup.ts");
  publishedSiteHostCache.clear();
  const origFetch = globalThis.fetch;
  const mediaPath = `/media/shops/shop-bd/${"a".repeat(64)}.webp`;

  globalThis.fetch = async (rawUrl) => {
    const url = new URL(String(rawUrl));
    if (
      url.pathname === "/api/v1/sites/by-host" &&
      url.searchParams.get("host") === "bach-dang.nexusmart.localhost"
    ) {
      return new Response(
        JSON.stringify({
          site: {
            id: "site-bd",
            shop_id: "shop-bd",
            status: "PUBLISHED",
            primary_host: "shop.bachdang.vn",
          },
          shop: { status: "ACTIVE" },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      );
    }
    return new Response(JSON.stringify({ error: { code: "NOT_FOUND", message: "not found" } }), {
      status: 404,
      headers: { "Content-Type": "application/json" },
    });
  };

  try {
    // 1. Root host /media/* -> NextResponse.next (not rewritten, no redirect)
    const rootRes = await middleware(
      makeNextRequest(`http://nexusmart.localhost${mediaPath}`, "nexusmart.localhost")
    );
    assert.equal(rootRes._action, "next");
    assert.equal(rootRes.status, 200);

    // 2. Published tenant site host /media/* -> NextResponse.next (not rewritten to /s/[host] and not 308 redirected to primary_host)
    const siteRes = await middleware(
      makeNextRequest(
        `http://bach-dang.nexusmart.localhost${mediaPath}`,
        "bach-dang.nexusmart.localhost"
      )
    );
    assert.equal(siteRes._action, "next");
    assert.equal(siteRes.status, 200);

    // 3. Unknown tenant site host /media/* -> 404
    const missingRes = await middleware(
      makeNextRequest(
        `http://unknown-shop.nexusmart.localhost${mediaPath}`,
        "unknown-shop.nexusmart.localhost"
      )
    );
    assert.equal(missingRes.status, 404);
  } finally {
    globalThis.fetch = origFetch;
    publishedSiteHostCache.clear();
  }
});
