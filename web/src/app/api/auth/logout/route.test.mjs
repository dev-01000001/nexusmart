// Run: node --test src/app/api/auth/logout/route.test.mjs
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
globalThis.__NM_LOGOUT_TEST__ = mockState;

const VIRTUAL_MODULES = {
  "virtual:next-headers": `
    export async function headers() {
      return globalThis.__NM_LOGOUT_TEST__.headers;
    }
    export async function cookies() {
      return {
        get(name) {
          const value = globalThis.__NM_LOGOUT_TEST__.cookies.get(name);
          return value === undefined ? undefined : { name, value };
        },
        set(name, value, options) {
          globalThis.__NM_LOGOUT_TEST__.setCalls.push({ name, value, options });
          if (options?.maxAge === 0) {
            globalThis.__NM_LOGOUT_TEST__.cookies.delete(name);
          } else {
            globalThis.__NM_LOGOUT_TEST__.cookies.set(name, value);
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
const { REFRESH_GRACE_MS, rotateRefreshToken } = await import("../../../../lib/server/tokens.ts");

function makeJwt(expOffsetSeconds) {
  const header = Buffer.from(JSON.stringify({ alg: "HS256", typ: "JWT" })).toString("base64url");
  const payload = Buffer.from(
    JSON.stringify({
      sub: "user-1",
      role: "buyer",
      jti: `jti-${Math.random().toString(36).slice(2)}`,
      exp: Math.floor(Date.now() / 1000) + expOffsetSeconds,
    })
  ).toString("base64url");
  return `${header}.${payload}.sig`;
}

function jsonRes(body, status = 200, extraHeaders = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...extraHeaders },
  });
}

function makeLogoutReq() {
  return new Request("http://nexusmart.localhost:3300/api/auth/logout", {
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

test("POST /api/auth/logout revokes refresh-token family when access JWT is expired", async () => {
  const expiredAt = makeJwt(-300);
  const freshAt = makeJwt(900);
  const oldRt = `rt-expired-at-${Date.now()}`;
  const freshRt = `rt-fresh-for-revoke-${Date.now()}`;
  resetCookies(expiredAt, oldRt);

  const calls = [];
  let familyRevoked = false;
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (rawUrl, init) => {
    const url = new URL(String(rawUrl));
    const auth = init?.headers?.Authorization ?? init?.headers?.authorization ?? "";
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : {};
    calls.push({ pathname: url.pathname, auth, body });

    if (url.pathname === "/api/v1/auth/refresh") {
      if (body.refresh_token === oldRt) {
        return jsonRes({
          access_token: freshAt,
          refresh_token: freshRt,
          expires_in_seconds: 900,
        });
      }
      return jsonRes({ error: { code: "UNAUTHENTICATED", message: "invalid refresh token" } }, 401);
    }

    if (url.pathname === "/api/v1/auth/logout") {
      if (auth === `Bearer ${freshAt}`) {
        if (body.refresh_token === freshRt || body.refresh_token === oldRt) {
          familyRevoked = true;
        }
        return jsonRes({ ok: true });
      }
      return jsonRes({ error: { code: "UNAUTHENTICATED", message: "token expired" } }, 401);
    }

    return jsonRes({ error: { code: "NOT_FOUND", message: "unexpected" } }, 404);
  };

  try {
    const res = await POST(makeLogoutReq(), {});
    assert.equal(res.status, 200);
    assert.deepEqual(await res.json(), { ok: true });
    assert.equal(familyRevoked, true, "refresh-token family MUST be revoked on backend");

    // Cookies must be cleared.
    assert.equal(mockState.cookies.has(ACCESS_COOKIE), false);
    assert.equal(mockState.cookies.has(REFRESH_COOKIE), false);

    // Neither old nor rotated refresh token may be reused from grace cache.
    const callsBefore = calls.length;
    assert.equal(await rotateRefreshToken(oldRt), null);
    assert.equal(await rotateRefreshToken(freshRt), null);
    assert.equal(calls.length, callsBefore, "revoked tokens must not hit gateway refresh again");
  } finally {
    globalThis.fetch = origFetch;
  }
});

test("POST /api/auth/logout revokes refresh-token family when access cookie is missing", async () => {
  const freshAt = makeJwt(900);
  const oldRt = `rt-missing-at-${Date.now()}`;
  const freshRt = `rt-missing-at-new-${Date.now()}`;
  resetCookies(undefined, oldRt);

  let familyRevoked = false;
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (rawUrl, init) => {
    const url = new URL(String(rawUrl));
    const auth = init?.headers?.Authorization ?? init?.headers?.authorization ?? "";
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : {};

    if (url.pathname === "/api/v1/auth/refresh" && body.refresh_token === oldRt) {
      return jsonRes({
        access_token: freshAt,
        refresh_token: freshRt,
        expires_in_seconds: 900,
      });
    }
    if (url.pathname === "/api/v1/auth/logout" && auth === `Bearer ${freshAt}`) {
      if (body.refresh_token === freshRt || body.refresh_token === oldRt) {
        familyRevoked = true;
      }
      return jsonRes({ ok: true });
    }
    return jsonRes({ error: { code: "UNAUTHENTICATED", message: "unauthorized" } }, 401);
  };

  try {
    const res = await POST(makeLogoutReq(), {});
    assert.equal(res.status, 200);
    assert.equal(familyRevoked, true, "family must be revoked when nm_at cookie already expired");
    assert.equal(mockState.cookies.has(REFRESH_COOKIE), false);
  } finally {
    globalThis.fetch = origFetch;
  }
});

test("POST /api/auth/logout falls back to refresh + revoke when unexpired-looking access token gets 401 from gateway", async () => {
  const rejectedAt = makeJwt(600);
  const freshAt = makeJwt(900);
  const oldRt = `rt-skew-401-${Date.now()}`;
  const freshRt = `rt-skew-401-new-${Date.now()}`;
  resetCookies(rejectedAt, oldRt);

  const sequence = [];
  let familyRevoked = false;
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (rawUrl, init) => {
    const url = new URL(String(rawUrl));
    const auth = init?.headers?.Authorization ?? init?.headers?.authorization ?? "";
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : {};
    sequence.push(url.pathname);

    if (url.pathname === "/api/v1/auth/logout" && auth === `Bearer ${rejectedAt}`) {
      return jsonRes({ error: { code: "UNAUTHENTICATED", message: "token revoked" } }, 401);
    }
    if (url.pathname === "/api/v1/auth/refresh" && body.refresh_token === oldRt) {
      return jsonRes({
        access_token: freshAt,
        refresh_token: freshRt,
        expires_in_seconds: 900,
      });
    }
    if (
      url.pathname === "/api/v1/auth/logout" &&
      auth === `Bearer ${freshAt}` &&
      body.refresh_token === freshRt
    ) {
      familyRevoked = true;
      return jsonRes({ ok: true });
    }
    return jsonRes({ error: { code: "UNAUTHENTICATED", message: "unauthorized" } }, 401);
  };

  try {
    const res = await POST(makeLogoutReq(), {});
    assert.equal(res.status, 200);
    assert.equal(familyRevoked, true);
    assert.deepEqual(sequence, [
      "/api/v1/auth/logout",
      "/api/v1/auth/refresh",
      "/api/v1/auth/logout",
    ]);
  } finally {
    globalThis.fetch = origFetch;
  }
});

test("POST /api/auth/logout with valid access token calls /api/v1/auth/logout directly without extra refresh", async () => {
  const validAt = makeJwt(600);
  const validRt = `rt-valid-${Date.now()}`;
  resetCookies(validAt, validRt);

  const calls = [];
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (rawUrl, init) => {
    const url = new URL(String(rawUrl));
    const auth = init?.headers?.Authorization ?? init?.headers?.authorization ?? "";
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : {};
    calls.push({ pathname: url.pathname, auth, body });
    if (url.pathname === "/api/v1/auth/logout" && auth === `Bearer ${validAt}`) {
      return jsonRes({ ok: true });
    }
    return jsonRes({ error: { code: "UNAUTHENTICATED", message: "unexpected" } }, 401);
  };

  try {
    const res = await POST(makeLogoutReq(), {});
    assert.equal(res.status, 200);
    assert.equal(calls.length, 1);
    assert.equal(calls[0].pathname, "/api/v1/auth/logout");
    assert.equal(calls[0].body.refresh_token, validRt);
  } finally {
    globalThis.fetch = origFetch;
  }
});

test("POST /api/auth/logout with expired access token revokes grace-cached pair directly without replaying old refresh token", async () => {
  const expiredAt = makeJwt(-120);
  const cachedAt = makeJwt(900);
  const oldRt = `rt-grace-logout-${Date.now()}`;
  const cachedRt = `rt-grace-logout-new-${Date.now()}`;

  const calls = [];
  let revokedCachedPair = false;
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (rawUrl, init) => {
    const url = new URL(String(rawUrl));
    const auth = init?.headers?.Authorization ?? init?.headers?.authorization ?? "";
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : {};
    calls.push({ pathname: url.pathname, auth, body });

    if (url.pathname === "/api/v1/auth/refresh" && body.refresh_token === oldRt) {
      return jsonRes({
        access_token: cachedAt,
        refresh_token: cachedRt,
        expires_in_seconds: 900,
      });
    }
    if (
      url.pathname === "/api/v1/auth/logout" &&
      auth === `Bearer ${cachedAt}` &&
      body.refresh_token === cachedRt
    ) {
      revokedCachedPair = true;
      return jsonRes({ ok: true });
    }
    return jsonRes({ error: { code: "UNAUTHENTICATED", message: "expired" } }, 401);
  };

  try {
    // Populate 5s grace cache first (simulating a parallel request that rotated 1s earlier).
    assert.equal(REFRESH_GRACE_MS, 5_000);
    const rotated = await rotateRefreshToken(oldRt);
    assert.equal(rotated?.accessToken, cachedAt);
    assert.equal(calls.filter((c) => c.pathname === "/api/v1/auth/refresh").length, 1);

    // Logout arrives carrying the old expired cookies.
    resetCookies(expiredAt, oldRt);
    const res = await POST(makeLogoutReq(), {});
    assert.equal(res.status, 200);
    assert.equal(
      revokedCachedPair,
      true,
      "must revoke the grace-cached pair so the family is revoked"
    );
    assert.equal(
      calls.filter((c) => c.pathname === "/api/v1/auth/refresh").length,
      1,
      "must NOT replay oldRt against /api/v1/auth/refresh when grace-cached pair succeeded"
    );
  } finally {
    globalThis.fetch = origFetch;
  }
});

test("POST /api/auth/logout with expired access token cancels concurrent in-flight rotation and revokes rotated pair", async () => {
  const expiredAt = makeJwt(-120);
  const rotatedAt = makeJwt(900);
  const oldRt = `rt-inflight-logout-${Date.now()}`;
  const rotatedRt = `rt-inflight-logout-new-${Date.now()}`;

  let releaseRefresh;
  const refreshGate = new Promise((resolve) => {
    releaseRefresh = resolve;
  });
  let refreshStartedResolve;
  const refreshStarted = new Promise((resolve) => {
    refreshStartedResolve = resolve;
  });

  let refreshCount = 0;
  let revokedRotatedPair = false;
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (rawUrl, init) => {
    const url = new URL(String(rawUrl));
    const auth = init?.headers?.Authorization ?? init?.headers?.authorization ?? "";
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : {};

    if (url.pathname === "/api/v1/auth/refresh") {
      refreshCount += 1;
      refreshStartedResolve();
      await refreshGate;
      return jsonRes({
        access_token: rotatedAt,
        refresh_token: rotatedRt,
        expires_in_seconds: 900,
      });
    }
    if (
      url.pathname === "/api/v1/auth/logout" &&
      auth === `Bearer ${rotatedAt}` &&
      body.refresh_token === rotatedRt
    ) {
      revokedRotatedPair = true;
      return jsonRes({ ok: true });
    }
    return jsonRes({ error: { code: "UNAUTHENTICATED", message: "expired" } }, 401);
  };

  try {
    // Start a concurrent refresh, then trigger logout before refresh resolves.
    const concurrentRefresh = rotateRefreshToken(oldRt);
    await refreshStarted;

    resetCookies(expiredAt, oldRt);
    const logoutPromise = POST(makeLogoutReq(), {});
    releaseRefresh();

    const [refreshRes, logoutRes] = await Promise.all([concurrentRefresh, logoutPromise]);
    assert.equal(refreshRes, null, "cancelled in-flight refresh must not hand tokens back");
    assert.equal(logoutRes.status, 200);
    assert.equal(revokedRotatedPair, true, "rotated pair from cancelled in-flight must be revoked");
    assert.equal(refreshCount, 1, "must NOT issue a second refresh call");
    assert.equal(await rotateRefreshToken(rotatedRt), null, "rotated token must stay cancelled");
  } finally {
    globalThis.fetch = origFetch;
  }
});

test("POST /api/auth/logout preserves 429 Retry-After while still clearing cookies, and clears cookies on 503", async () => {
  const expiredAt = makeJwt(-60);
  const rt429 = `rt-logout-429-${Date.now()}`;
  const rt503 = `rt-logout-503-${Date.now()}`;

  const origFetch = globalThis.fetch;
  globalThis.fetch = async (rawUrl, init) => {
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : {};
    if (body.refresh_token === rt429) {
      return jsonRes(
        { error: { code: "RESOURCE_EXHAUSTED", message: "rate limited" } },
        429,
        { "Retry-After": "30" }
      );
    }
    return jsonRes({ error: { code: "UNAVAILABLE", message: "gateway down" } }, 503);
  };

  try {
    resetCookies(expiredAt, rt429);
    const res429 = await POST(makeLogoutReq(), {});
    assert.equal(res429.status, 429);
    assert.equal(res429.headers.get("retry-after"), "30");
    assert.equal(mockState.cookies.has(REFRESH_COOKIE), false);

    resetCookies(expiredAt, rt503);
    const res503 = await POST(makeLogoutReq(), {});
    assert.equal(res503.status, 200);
    assert.deepEqual(await res503.json(), { ok: true });
    assert.equal(mockState.cookies.has(REFRESH_COOKIE), false);
  } finally {
    globalThis.fetch = origFetch;
  }
});
