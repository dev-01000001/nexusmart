// Run: node --test src/app/api/seller/products/[id]/review-summary/route.test.mjs
import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { registerHooks } from "node:module";
import { dirname, resolve } from "node:path";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const WEB_SRC = resolve(__dirname, "../../../../../..");

const mockState = {
  accessToken: "seller-a-token",
  headers: new Headers(),
};
globalThis.__NM_SELLER_REV_SUMMARY_TEST__ = mockState;

const VIRTUAL_MODULES = {
  "virtual:react": `
    export function cache(fn) { return fn; }
    export default { cache };
  `,
  "virtual:next-headers": `
    export async function headers() {
      return globalThis.__NM_SELLER_REV_SUMMARY_TEST__.headers;
    }
    export async function cookies() {
      return {
        get(name) {
          const tok = globalThis.__NM_SELLER_REV_SUMMARY_TEST__.accessToken;
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

const { GET } = await import("./route.ts");

function jsonRes(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const PROD_A = "11111111-1111-4111-8111-111111111111";
const PROD_A_PENDING = "22222222-2222-4222-8222-222222222222";
const PROD_B = "33333333-3333-4333-8333-333333333333";

function installMockFetch() {
  const calls = [];
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (rawUrl, init) => {
    const url = new URL(String(rawUrl));
    calls.push({ method: init?.method ?? "GET", pathname: url.pathname, search: url.search });

    if (url.pathname === "/api/v1/users/me") {
      return jsonRes({
        user: { id: "seller-a-user", role: "seller", shop_id: "shop-a", status: "ACTIVE" },
      });
    }
    if (url.pathname === `/api/v1/seller/products/${PROD_A}`) {
      return jsonRes({ product: { id: PROD_A, shop_id: "shop-a", status: "APPROVED" } });
    }
    if (url.pathname === `/api/v1/seller/products/${PROD_A_PENDING}`) {
      return jsonRes({ product: { id: PROD_A_PENDING, shop_id: "shop-a", status: "PENDING" } });
    }
    if (url.pathname === `/api/v1/seller/products/${PROD_B}`) {
      return jsonRes(
        { error: { code: "PERMISSION_DENIED", message: "cannot access another shop's product" } },
        403
      );
    }
    // Intentionally return 200 on PROD_B review-summary so this test goes RED unless Seller BFF
    // verifies ownership via /api/v1/seller/products/{id} first.
    if (url.pathname === `/api/v1/products/${PROD_B}/review-summary`) {
      return jsonRes({
        product_id: PROD_B,
        available: true,
        review_count: 25,
        min_reviews_required: 10,
        average_rating: 4.8,
        summary: "Leaked Shop B summary",
        pros: ["B pro"],
        cons: [],
        ai_generated: true,
      });
    }
    if (url.pathname === `/api/v1/products/${PROD_A_PENDING}/review-summary`) {
      return jsonRes({ error: { code: "NOT_FOUND", message: "product not found" } }, 404);
    }
    if (url.pathname === `/api/v1/products/${PROD_A}/review-summary`) {
      return jsonRes({
        product_id: PROD_A,
        available: true,
        review_count: 14,
        min_reviews_required: 10,
        average_rating: 4.7,
        summary: "Shop A summary",
        pros: ["Tốt"],
        cons: ["Hơi chật"],
        ai_generated: true,
      });
    }
    return jsonRes({ error: { code: "NOT_FOUND", message: "not found" } }, 404);
  };
  return {
    calls,
    restore() {
      globalThis.fetch = origFetch;
    },
  };
}

test("seller review-summary rejects another shop's product with 404 before calling review-summary", async () => {
  const mock = installMockFetch();
  try {
    const req = new Request(`http://nexusmart.localhost/api/seller/products/${PROD_B}/review-summary`);
    const res = await GET(req, { params: Promise.resolve({ id: PROD_B }) });
    assert.equal(res.status, 404);
    assert.equal(
      mock.calls.some((c) => c.pathname.endsWith("/review-summary")),
      false,
      "must not call gateway /review-summary for another shop's product"
    );
  } finally {
    mock.restore();
  }
});

test("seller review-summary allows own public product and propagates gateway 404 for own non-public product", async () => {
  const mock = installMockFetch();
  try {
    const okReq = new Request(`http://nexusmart.localhost/api/seller/products/${PROD_A}/review-summary`);
    const okRes = await GET(okReq, { params: Promise.resolve({ id: PROD_A }) });
    assert.equal(okRes.status, 200);
    const data = await okRes.json();
    assert.equal(data.summary, "Shop A summary");
    const revCall = mock.calls.find((c) => c.pathname === `/api/v1/products/${PROD_A}/review-summary`);
    assert.ok(revCall, "expected gateway review-summary call");
    assert.equal(revCall.search, "?shop_id=shop-a");

    // Own non-public product passes BFF ownership check and preserves gateway's 404 policy.
    const pendingReq = new Request(
      `http://nexusmart.localhost/api/seller/products/${PROD_A_PENDING}/review-summary`
    );
    const pendingRes = await GET(pendingReq, { params: Promise.resolve({ id: PROD_A_PENDING }) });
    assert.equal(pendingRes.status, 404);
  } finally {
    mock.restore();
  }
});
