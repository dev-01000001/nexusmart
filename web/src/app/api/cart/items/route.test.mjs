// Run: node --test src/app/api/cart/items/route.test.mjs
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
  accessToken: "buyer-token",
  headers: new Headers(),
};
globalThis.__NM_CART_ITEMS_TEST__ = mockState;

const VIRTUAL_MODULES = {
  "virtual:react": `
    export function cache(fn) { return fn; }
    export default { cache };
  `,
  "virtual:next-headers": `
    export async function headers() {
      return globalThis.__NM_CART_ITEMS_TEST__.headers;
    }
    export async function cookies() {
      return {
        get(name) {
          const tok = globalThis.__NM_CART_ITEMS_TEST__.accessToken;
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

const { POST } = await import("./route.ts");
const sitesModule = await import("../../../../lib/server/sites.ts");

function jsonRes(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const SITE_A_HOST = "shop-a.nexusmart.localhost";

test("POST /api/cart/items sends storefront_shop_id to gateway without scanning /api/v1/products (works for 5000+ product shops without product_id)", async () => {
  const calls = [];
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (rawUrl, init) => {
    const url = new URL(String(rawUrl));
    const method = (init?.method ?? "GET").toUpperCase();
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
    calls.push({ method, pathname: url.pathname, body });

    if (url.pathname === "/api/v1/sites/by-host") {
      return jsonRes({
        site: { id: "site-a", shop_id: "shop-a", status: "PUBLISHED", primary_host: SITE_A_HOST },
        shop: { status: "ACTIVE" },
      });
    }
    if (url.pathname === "/api/v1/users/me") {
      return jsonRes({ user: { id: "buyer-1", role: "buyer", shop_id: "", status: "ACTIVE" } });
    }
    if (url.pathname === "/api/v1/products") {
      // Simulate a 5000-product shop where sku-501 is NOT in the first 500 products.
      return jsonRes({ items: [], total: 5000 });
    }
    if (url.pathname === "/api/v1/cart/items" && method === "POST") {
      if (body?.storefront_shop_id !== "shop-a") {
        return jsonRes({ error: { code: "NOT_FOUND", message: "missing storefront_shop_id" } }, 404);
      }
      if (body?.sku_id === "sku-shop-b") {
        return jsonRes({ error: { code: "NOT_FOUND", message: "sku sku-shop-b not found" } }, 404);
      }
      return jsonRes({
        cart: {
          user_id: "buyer-1",
          shop_groups: [
            {
              shop_id: "shop-a",
              shop_name: "Shop A",
              subtotal_vnd: 300000,
              total_weight_grams: 500,
              items: [
                {
                  sku_id: body.sku_id,
                  shop_id: "shop-a",
                  price_vnd: 150000,
                  quantity: body.quantity,
                  weight_grams: 250,
                },
              ],
            },
            {
              shop_id: "shop-b",
              shop_name: "Shop B",
              subtotal_vnd: 999000,
              total_weight_grams: 300,
              items: [
                {
                  sku_id: "sku-b-other",
                  shop_id: "shop-b",
                  price_vnd: 999000,
                  quantity: 1,
                  weight_grams: 300,
                },
              ],
            },
          ],
          total_items: 3,
          total_vnd: 1299000,
        },
        clamped_to_stock: false,
      });
    }
    return jsonRes({ error: { code: "NOT_FOUND", message: `unmocked ${url.pathname}` } }, 404);
  };

  try {
    // 1. Add product #501 SKU without product_id -> 200, passes storefront_shop_id, zero /api/v1/products scans!
    const req = new Request(`http://${SITE_A_HOST}/api/cart/items`, {
      method: "POST",
      headers: {
        host: SITE_A_HOST,
        origin: `http://${SITE_A_HOST}`,
        "content-type": "application/json",
      },
      body: JSON.stringify({ sku_id: "sku-501", quantity: 2 }),
    });
    const res = await POST(req, {});
    assert.equal(res.status, 200);
    const data = await res.json();
    assert.equal(data.cart.shop_groups.length, 1);
    assert.equal(data.cart.shop_groups[0].shop_id, "shop-a");

    assert.equal(
      calls.some((c) => c.pathname.startsWith("/api/v1/products")),
      false,
      "must not scan /api/v1/products from BFF"
    );
    const cartCall = calls.find((c) => c.pathname === "/api/v1/cart/items");
    assert.deepEqual(cartCall?.body, {
      sku_id: "sku-501",
      quantity: 2,
      storefront_shop_id: "shop-a",
    });

    // 2. Foreign shop SKU rejected by gateway with 404 NOT_FOUND -> BFF returns 404 NOT_FOUND.
    const foreignReq = new Request(`http://${SITE_A_HOST}/api/cart/items`, {
      method: "POST",
      headers: {
        host: SITE_A_HOST,
        origin: `http://${SITE_A_HOST}`,
        "content-type": "application/json",
      },
      body: JSON.stringify({ sku_id: "sku-shop-b", quantity: 1 }),
    });
    const foreignRes = await POST(foreignReq, {});
    assert.equal(foreignRes.status, 404);
    const foreignBody = await foreignRes.json();
    assert.equal(foreignBody.error.code, "NOT_FOUND");

    // 3. Obsolete 5-page scan helper assertSkuOfShop is removed from sites.ts.
    assert.equal("assertSkuOfShop" in sitesModule, false);
  } finally {
    globalThis.fetch = origFetch;
  }
});
