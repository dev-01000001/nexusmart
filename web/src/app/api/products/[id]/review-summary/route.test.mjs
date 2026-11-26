// Run: node --test src/app/api/products/[id]/review-summary/route.test.mjs
import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { registerHooks } from "node:module";
import { dirname, resolve } from "node:path";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const WEB_SRC = resolve(__dirname, "../../../../..");

const mockState = {
  headers: new Headers(),
};
globalThis.__NM_BUYER_REV_SUMMARY_TEST__ = mockState;

const VIRTUAL_MODULES = {
  "virtual:react": `
    export function cache(fn) { return fn; }
    export default { cache };
  `,
  "virtual:next-headers": `
    export async function headers() {
      return globalThis.__NM_BUYER_REV_SUMMARY_TEST__.headers;
    }
    export async function cookies() {
      return { get() { return undefined; }, set() { throw new Error("read-only"); } };
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
    if (specifier === "react") return { shortCircuit: true, url: "virtual:react" };
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

const { GET } = await import("./route.ts");

function jsonRes(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const SITE_HOST = "shop-a.nexusmart.localhost";
const PROD_A = "11111111-1111-4111-8111-111111111111";
const PROD_B = "22222222-2222-4222-8222-222222222222";

test("buyer review-summary forwards site.shopId anonymously and propagates gateway 404 for foreign shop", async () => {
  const calls = [];
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (rawUrl, init) => {
    const url = new URL(String(rawUrl));
    calls.push({
      pathname: url.pathname,
      search: url.search,
      auth: init?.headers?.Authorization ?? "",
    });
    if (url.pathname === "/api/v1/sites/by-host") {
      return jsonRes({
        site: { id: "site-a", shop_id: "shop-a", status: "PUBLISHED", primary_host: SITE_HOST },
        shop: { status: "ACTIVE" },
      });
    }
    if (url.pathname === `/api/v1/products/${PROD_A}/review-summary`) {
      if (url.searchParams.get("shop_id") !== "shop-a") {
        return jsonRes({ error: { code: "NOT_FOUND", message: "product not found" } }, 404);
      }
      return jsonRes({
        product_id: PROD_A,
        available: true,
        review_count: 12,
        min_reviews_required: 10,
        average_rating: 4.5,
        summary: "Tốt",
        pros: ["Đẹp"],
        cons: [],
        ai_generated: true,
      });
    }
    return jsonRes({ error: { code: "NOT_FOUND", message: "product not found" } }, 404);
  };
  try {
    const okReq = new Request(`http://${SITE_HOST}/api/products/${PROD_A}/review-summary`, {
      headers: { host: SITE_HOST },
    });
    const okRes = await GET(okReq, { params: Promise.resolve({ id: PROD_A }) });
    assert.equal(okRes.status, 200);
    const revCall = calls.find((c) => c.pathname === `/api/v1/products/${PROD_A}/review-summary`);
    assert.equal(revCall?.search, "?shop_id=shop-a");
    assert.equal(revCall?.auth, "");

    const foreignReq = new Request(`http://${SITE_HOST}/api/products/${PROD_B}/review-summary`, {
      headers: { host: SITE_HOST },
    });
    const foreignRes = await GET(foreignReq, { params: Promise.resolve({ id: PROD_B }) });
    assert.equal(foreignRes.status, 404);
  } finally {
    globalThis.fetch = origFetch;
  }
});
