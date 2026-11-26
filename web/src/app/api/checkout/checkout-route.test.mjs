// Run: node --test src/app/api/checkout/checkout-route.test.mjs
// Real gateway JSON route tests for POST /api/checkout (Mục 2).
import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { registerHooks } from "node:module";
import { dirname, resolve } from "node:path";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const WEB_SRC = resolve(__dirname, "../../..");

const mockState = {
  accessToken: "buyer-token",
  headers: new Headers(),
};
globalThis.__NM_CHECKOUT_ROUTE_TEST__ = mockState;

const VIRTUAL_MODULES = {
  "virtual:react": `
    export function cache(fn) { return fn; }
    export default { cache };
  `,
  "virtual:next-headers": `
    export async function headers() {
      return globalThis.__NM_CHECKOUT_ROUTE_TEST__.headers;
    }
    export async function cookies() {
      return {
        get(name) {
          const tok = globalThis.__NM_CHECKOUT_ROUTE_TEST__.accessToken;
          if (!tok) return undefined;
          if (name === "nm_at" || name === "__Host-nm_at") return { value: tok };
          return undefined;
        },
        set() { throw new Error("read-only cookies"); },
      };
    }
  `,
  "virtual:next-navigation": `
    export function notFound() { throw new Error("NEXT_NOT_FOUND"); }
    export function redirect(url) { throw new Error("NEXT_REDIRECT:" + url); }
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
    if (specifier === "react") return { shortCircuit: true, url: "virtual:react" };
    if (specifier === "next/headers") return { shortCircuit: true, url: "virtual:next-headers" };
    if (specifier === "next/navigation")
      return { shortCircuit: true, url: "virtual:next-navigation" };
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

const checkoutRoute = await import("./route.ts");

const SITE_HOST = "shop-a.nexusmart.localhost";
const SHOP_ID = "shop-a";

function jsonRes(body, status = 200, extraHeaders = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...extraHeaders },
  });
}

function makeCheckoutReq(body) {
  return new Request(`http://${SITE_HOST}/api/checkout`, {
    method: "POST",
    headers: {
      host: SITE_HOST,
      origin: `http://${SITE_HOST}`,
      "content-type": "application/json",
      "idempotency-key": "11111111-1111-4111-8111-111111111111",
    },
    body: JSON.stringify(body),
  });
}

function installGatewayMock(onCheckoutPost) {
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (rawUrl, init) => {
    const url = new URL(String(rawUrl));
    const method = (init?.method ?? "GET").toUpperCase();
    if (url.pathname === "/api/v1/sites/by-host") {
      return jsonRes({
        site: {
          id: "site-a",
          shop_id: SHOP_ID,
          status: "PUBLISHED",
          primary_host: SITE_HOST,
        },
        shop: { status: "ACTIVE" },
      });
    }
    if (url.pathname === "/api/v1/users/me") {
      return jsonRes({ user: { id: "buyer-1", role: "buyer", shop_id: "", status: "ACTIVE" } });
    }
    if (url.pathname === "/api/v1/checkout" && method === "POST") {
      return onCheckoutPost();
    }
    return jsonRes({ error: { code: "NOT_FOUND", message: `unmocked ${url.pathname}` } }, 404);
  };
  return () => {
    globalThis.fetch = origFetch;
  };
}

test("Mục 2: POST /api/checkout maps gateway 429 VOUCHER_OUT_OF_QUOTA and VOUCHER_BUDGET_EXHAUSTED to 409 VOUCHER_EXHAUSTED", async () => {
  const cases = [
    "promotion reservation rejected: VOUCHER_OUT_OF_QUOTA (promo:voucher:v-1:remaining)",
    "promotion reservation rejected: VOUCHER_BUDGET_EXHAUSTED (promo:voucher:v-1:budget_remaining)",
  ];
  for (const gwMsg of cases) {
    const restore = installGatewayMock(() =>
      jsonRes({ error: { code: "RESOURCE_EXHAUSTED", message: gwMsg, trace_id: "t-v" } }, 429)
    );
    try {
      const res = await checkoutRoute.POST(
        makeCheckoutReq({
          address_id: "addr-1",
          from_cart: true,
          payment_method: "VIETQR",
          shop_voucher_id: "v-1",
        }),
        {}
      );
      assert.equal(res.status, 409, `expected 409 for ${gwMsg}, got ${res.status}`);
      const data = await res.json();
      assert.equal(data.error.code, "VOUCHER_EXHAUSTED");
      assert.match(data.error.message, /hết lượt/i);
    } finally {
      restore();
    }
  }
});

test("Mục 2: POST /api/checkout preserves 429 + Retry-After for real gateway rate-limit and keeps 429 flash copy for FLASH_SOLD_OUT", async () => {
  // Real gateway rate-limiter response (services/gateway/internal/middleware/ratelimit.go:299-300)
  const restoreRl = installGatewayMock(() =>
    jsonRes(
      {
        error: {
          code: "RATE_LIMITED",
          message: "rate limit exceeded, please retry later",
          trace_id: "t-rl",
        },
      },
      429,
      { "Retry-After": "42" }
    )
  );
  try {
    const res = await checkoutRoute.POST(
      makeCheckoutReq({
        address_id: "addr-1",
        from_cart: true,
        payment_method: "VIETQR",
      }),
      {}
    );
    assert.equal(res.status, 429);
    assert.equal(res.headers.get("retry-after"), "42");
    const data = await res.json();
    assert.equal(data.error.code, "RATE_LIMITED");
    assert.doesNotMatch(data.error.message, /mã ưu đãi/i);
  } finally {
    restoreRl();
  }

  // Real FLASH_SOLD_OUT response from PromotionService Lua
  const restoreFlash = installGatewayMock(() =>
    jsonRes(
      {
        error: {
          code: "RESOURCE_EXHAUSTED",
          message: "promotion reservation rejected: FLASH_SOLD_OUT (promo:flash:fsi-1:remaining)",
          trace_id: "t-fs",
        },
      },
      429
    )
  );
  try {
    const res = await checkoutRoute.POST(
      makeCheckoutReq({
        address_id: "addr-1",
        from_cart: true,
        payment_method: "VIETQR",
      }),
      {}
    );
    assert.equal(res.status, 429);
    const data = await res.json();
    assert.equal(data.error.code, "RESOURCE_EXHAUSTED");
    assert.match(data.error.message, /Flash sale/i);
  } finally {
    restoreFlash();
  }
});
