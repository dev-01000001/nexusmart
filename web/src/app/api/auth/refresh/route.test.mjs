// Run: node --test src/app/api/auth/refresh/route.test.mjs
import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { registerHooks } from "node:module";
import { dirname, resolve } from "node:path";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const WEB_SRC = resolve(__dirname, "../../../..");

const mockState = {
  cookies: new Map(),
  setCalls: [],
  headers: new Headers(),
};
globalThis.__NM_REFRESH_TEST__ = mockState;

const VIRTUAL_MODULES = {
  "virtual:next-headers": `
    export async function headers() {
      return globalThis.__NM_REFRESH_TEST__.headers;
    }
    export async function cookies() {
      return {
        get(name) {
          const value = globalThis.__NM_REFRESH_TEST__.cookies.get(name);
          return value === undefined ? undefined : { name, value };
        },
        set(name, value, options) {
          globalThis.__NM_REFRESH_TEST__.setCalls.push({ name, value, options });
          if (options?.maxAge === 0) {
            globalThis.__NM_REFRESH_TEST__.cookies.delete(name);
          } else {
            globalThis.__NM_REFRESH_TEST__.cookies.set(name, value);
          }
        },
      };
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
    if (specifier === "next/headers") return { shortCircuit: true, url: "virtual:next-headers" };
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

const { POST } = await import("./route.ts");
const { ACCESS_COOKIE, REFRESH_COOKIE } = await import("../../../../lib/server/config.ts");

function jsonRes(body, status = 200, extraHeaders = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...extraHeaders },
  });
}

function makeRefreshReq() {
  return new Request("http://nexusmart.localhost:3300/api/auth/refresh", {
    method: "POST",
    headers: {
      host: "nexusmart.localhost:3300",
      origin: "http://nexusmart.localhost:3300",
      "sec-fetch-site": "same-origin",
    },
  });
}

function resetCookies(access, refresh) {
  mockState.cookies.clear();
  mockState.setCalls.length = 0;
  if (access !== undefined) mockState.cookies.set(ACCESS_COOKIE, access);
  if (refresh !== undefined) mockState.cookies.set(REFRESH_COOKIE, refresh);
}

test("POST /api/auth/refresh preserves cookies on 429/5xx/network errors and clears cookies only on dead session (401)", async () => {
  const origFetch = globalThis.fetch;
  try {
    // 1. 429 rate-limited: preserves cookies and Retry-After
    const rt429 = `rt-bff-429-${Date.now()}`;
    resetCookies("at-1", rt429);
    globalThis.fetch = async () =>
      jsonRes(
        { error: { code: "RESOURCE_EXHAUSTED", message: "too many requests" } },
        429,
        { "Retry-After": "15" }
      );
    const res429 = await POST(makeRefreshReq(), {});
    assert.equal(res429.status, 429);
    assert.equal(res429.headers.get("retry-after"), "15");
    assert.equal(mockState.cookies.get(REFRESH_COOKIE), rt429, "429 must NOT clear refresh cookie");
    assert.equal(mockState.setCalls.length, 0);

    // 2. 503 unavailable: preserves cookies
    const rt503 = `rt-bff-503-${Date.now()}`;
    resetCookies("at-1", rt503);
    globalThis.fetch = async () =>
      jsonRes({ error: { code: "UNAVAILABLE", message: "service unavailable" } }, 503);
    const res503 = await POST(makeRefreshReq(), {});
    assert.equal(res503.status, 503);
    assert.equal(mockState.cookies.get(REFRESH_COOKIE), rt503, "503 must NOT clear refresh cookie");
    assert.equal(mockState.setCalls.length, 0);

    // 3. Network error: preserves cookies and returns 503
    const rtNet = `rt-bff-net-${Date.now()}`;
    resetCookies("at-1", rtNet);
    globalThis.fetch = async () => {
      throw new TypeError("fetch failed");
    };
    const resNet = await POST(makeRefreshReq(), {});
    assert.equal(resNet.status, 503);
    assert.equal(
      mockState.cookies.get(REFRESH_COOKIE),
      rtNet,
      "network error must NOT clear refresh cookie"
    );
    assert.equal(mockState.setCalls.length, 0);

    // 4. 401 from gateway: clears cookies and returns 401
    const rtDead = `rt-bff-dead-${Date.now()}`;
    resetCookies("at-1", rtDead);
    globalThis.fetch = async () =>
      jsonRes({ error: { code: "UNAUTHENTICATED", message: "refresh token expired" } }, 401);
    const res401 = await POST(makeRefreshReq(), {});
    assert.equal(res401.status, 401);
    assert.equal(mockState.cookies.has(REFRESH_COOKIE), false, "401 must clear refresh cookie");
    assert.equal(mockState.cookies.has(ACCESS_COOKIE), false, "401 must clear access cookie");
  } finally {
    globalThis.fetch = origFetch;
  }
});

test("POST /api/auth/refresh deduplicates concurrent calls via single-flight + 5s grace window", async () => {
  const origFetch = globalThis.fetch;
  const rt = `rt-bff-singleflight-${Date.now()}`;
  let gatewayCalls = 0;

  globalThis.fetch = async () => {
    gatewayCalls += 1;
    await new Promise((r) => setTimeout(r, 20));
    return jsonRes({
      access_token: `at-sf-${gatewayCalls}`,
      refresh_token: `rt-sf-${gatewayCalls}`,
      expires_in_seconds: 900,
    });
  };

  try {
    resetCookies("at-old", rt);
    const [r1, r2] = await Promise.all([POST(makeRefreshReq(), {}), POST(makeRefreshReq(), {})]);
    assert.equal(r1.status, 200);
    assert.equal(r2.status, 200);
    assert.equal(gatewayCalls, 1, "concurrent /api/auth/refresh calls must share one rotation");

    // Within 5s grace window, another request carrying the old rt still hits grace cache.
    resetCookies("at-old", rt);
    const r3 = await POST(makeRefreshReq(), {});
    assert.equal(r3.status, 200);
    assert.equal(gatewayCalls, 1, "5s grace cache must serve same old rt without replaying");
  } finally {
    globalThis.fetch = origFetch;
  }
});
