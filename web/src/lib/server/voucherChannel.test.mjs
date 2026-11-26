// Run: node --test src/lib/server/voucherChannel.test.mjs (Node 24 type stripping + registerHooks)
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { registerHooks } from "node:module";
import { dirname, resolve } from "node:path";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import ts from "typescript";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const WEB_SRC = resolve(__dirname, "../..");

const mockState = {
  accessToken: "",
  headers: new Headers(),
};

globalThis.__NM_VOUCHER_TEST_STATE__ = mockState;

const VIRTUAL_MODULES = {
  "virtual:react": `
    export function cache(fn) { return fn; }
    export function createElement(type, props, ...children) {
      return { type, props: { ...(props ?? {}), ...(children.length ? { children } : {}) } };
    }
    export default { cache, createElement };
  `,
  "virtual:next-headers": `
    export async function headers() {
      return globalThis.__NM_VOUCHER_TEST_STATE__.headers;
    }
    export async function cookies() {
      return {
        get(name) {
          const tok = globalThis.__NM_VOUCHER_TEST_STATE__.accessToken;
          if (!tok) return undefined;
          if (name === "nm_at" || name === "__Host-nm_at") return { value: tok };
          return undefined;
        },
        set() {
          throw new Error("read-only cookies in test");
        },
      };
    }
  `,
  "virtual:next-navigation": `
    export function notFound() { throw new Error("NEXT_NOT_FOUND"); }
    export function redirect(url) { throw new Error("NEXT_REDIRECT:" + url); }
  `,
  "virtual:checkout-client": `
    export function CheckoutClient(props) { return { props }; }
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
    if (specifier === "./CheckoutClient" && context.parentURL?.includes("/thanh-toan/page.tsx")) {
      return { shortCircuit: true, url: "virtual:checkout-client" };
    }
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
    if (url.startsWith("file:") && url.endsWith(".tsx")) {
      const src = readFileSync(fileURLToPath(url), "utf8");
      const out = ts.transpileModule(src, {
        compilerOptions: {
          jsx: ts.JsxEmit.React,
          module: ts.ModuleKind.ESNext,
          target: ts.ScriptTarget.ESNext,
        },
        fileName: fileURLToPath(url),
      });
      return { shortCircuit: true, format: "module", source: out.outputText };
    }
    return nextLoad(url, context);
  },
});

const { loadShopSiteRenderData } = await import("./sites.ts");
const { fetchAccountPageData } = await import("../../features/account/account.server.ts");
const { loadSiteVouchers } = await import("../../features/site/siteContext.server.ts");
const { default: SiteCheckoutPage } = await import(
  "../../app/(site)/s/[host]/thanh-toan/page.tsx"
);

const SITE_HOST = "shop-a.nexusmart.localhost";
const SHOP_ID = "11111111-1111-4111-8111-111111111111";

function makeSiteResponse(shopId = SHOP_ID) {
  return {
    site: {
      id: "site-a-1",
      shop_id: shopId,
      shop_name: "Cửa hàng A",
      subdomain: "shop-a",
      primary_host: SITE_HOST,
      status: "PUBLISHED",
      config: {
        schema_version: 2,
        template_id: "atelier",
        theme: { brand_name: "Cửa hàng A" },
      },
    },
    shop: {
      id: shopId,
      name: "Cửa hàng A",
      description: "Mô tả",
      status: "ACTIVE",
    },
  };
}

function makeVoucher(shopId = SHOP_ID) {
  return {
    id: "v-shop-a-1",
    code: "SHOPA10",
    name: "Giảm 20.000đ cho đơn từ 100.000đ",
    scope: "SHOP",
    shop_id: shopId,
    discount_type: "FIXED",
    value_vnd: 20000,
    discount_percent: 0,
    min_order_vnd: 100000,
    max_discount_vnd: 20000,
    quota: 50,
    remaining_quota: 40,
    is_active: true,
    saved: false,
    ends_at: "2099-01-01T00:00:00Z",
  };
}

function jsonRes(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/**
 * Simulates the strict gateway `/api/v1/vouchers` contract (`validateSalesChannelInput`):
 * omitting `sales_channel` defaults to STOREFRONT and requires non-empty `storefront_shop_id` (400).
 */
function installStrictGatewayMock(opts = {}) {
  const voucherCalls = [];
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (rawUrl, init) => {
    const url = new URL(String(rawUrl));
    const authHeader =
      init?.headers && typeof init.headers === "object"
        ? (init.headers.Authorization ?? init.headers.authorization ?? "")
        : "";

    if (url.pathname === "/api/v1/vouchers") {
      voucherCalls.push({
        url,
        searchParams: Object.fromEntries(url.searchParams.entries()),
        hasAuth: Boolean(authHeader),
      });

      const rawChannel = (url.searchParams.get("sales_channel") ?? "").trim().toUpperCase();
      const channel = rawChannel || "STOREFRONT";
      const storefrontShopId = (url.searchParams.get("storefront_shop_id") ?? "").trim();

      if (channel === "STOREFRONT" && !storefrontShopId) {
        return jsonRes(
          {
            error: {
              code: "INVALID_ARGUMENT",
              message: "storefront_shop_id is required when sales_channel is STOREFRONT",
            },
          },
          400
        );
      }
      if (opts.failAuthVouchersWith401 && authHeader) {
        return jsonRes({ error: { code: "UNAUTHENTICATED", message: "token expired" } }, 401);
      }
      return jsonRes({ vouchers: [makeVoucher(SHOP_ID)] });
    }

    if (url.pathname === "/api/v1/sites/by-host") return jsonRes(makeSiteResponse(SHOP_ID));
    if (url.pathname === "/api/v1/categories") return jsonRes({ categories: [] });
    if (url.pathname === "/api/v1/products") return jsonRes({ items: [], total: 0 });
    if (url.pathname === "/api/v1/flash-sales") return jsonRes({ flash_sales: [] });
    if (url.pathname === "/api/v1/payment-methods") return jsonRes({ methods: [] });
    if (url.pathname === "/api/v1/shop-discounts/active") return jsonRes({ discounts: [] });
    if (url.pathname === "/api/v1/users/me") {
      return jsonRes({
        user: {
          id: "buyer-1",
          full_name: "Người mua A",
          email: "buyer@example.com",
          phone: "0901234567",
          role: "buyer",
          phone_verified: true,
        },
      });
    }
    if (url.pathname === "/api/v1/users/me/addresses") return jsonRes({ addresses: [] });
    if (url.pathname === "/api/v1/orders") return jsonRes({ items: [], total: 0 });
    if (url.pathname === "/api/v1/shipping/provinces") return jsonRes({ provinces: [] });
    if (url.pathname === "/api/v1/notifications/preferences") return jsonRes({ preferences: null });

    return jsonRes({ error: { code: "NOT_FOUND", message: `unmocked ${url.pathname}` } }, 404);
  };

  return {
    voucherCalls,
    restore() {
      globalThis.fetch = origFetch;
      mockState.accessToken = "";
    },
  };
}

function assertStorefrontVoucherCall(call, expectedShopId, label) {
  assert.equal(call.searchParams.sales_channel, "STOREFRONT", `${label}: missing sales_channel`);
  assert.equal(
    call.searchParams.storefront_shop_id,
    expectedShopId,
    `${label}: missing storefront_shop_id`
  );
  assert.equal(call.searchParams.shop_id, expectedShopId, `${label}: wrong shop_id`);
  assert.equal(call.searchParams.scope, "SHOP", `${label}: wrong scope`);
}

test("sites.ts loadShopSiteRenderData sends STOREFRONT + storefront_shop_id and does not swallow 400 into []", async () => {
  const mock = installStrictGatewayMock();
  try {
    const data = await loadShopSiteRenderData(
      { id: SHOP_ID, name: "Cửa hàng A", description: "", status: "ACTIVE" },
      { uncached: true }
    );
    assert.equal(mock.voucherCalls.length, 1);
    assertStorefrontVoucherCall(mock.voucherCalls[0], SHOP_ID, "loadShopSiteRenderData");
    assert.equal(data.vouchers.length, 1);
    assert.equal(data.vouchers[0].code, "SHOPA10");
  } finally {
    mock.restore();
  }
});

test("thanh-toan/page.tsx SiteCheckoutPage sends STOREFRONT + storefront_shop_id on SSR voucher load", async () => {
  mockState.accessToken = "buyer-access-token";
  const mock = installStrictGatewayMock();
  try {
    const element = await SiteCheckoutPage({
      params: Promise.resolve({ host: SITE_HOST }),
    });
    const authedCalls = mock.voucherCalls.filter((c) => c.hasAuth);
    assert.equal(authedCalls.length, 1, "expected authenticated SSR /api/v1/vouchers call");
    assertStorefrontVoucherCall(authedCalls[0], SHOP_ID, "SiteCheckoutPage");
    assert.equal(element.props.initialVouchers.length, 1);
    assert.equal(element.props.initialVouchers[0].code, "SHOPA10");
  } finally {
    mock.restore();
  }
});

test("account.server.ts fetchAccountPageData sends STOREFRONT + storefront_shop_id and does not swallow 400 into []", async () => {
  mockState.accessToken = "buyer-access-token";
  const mock = installStrictGatewayMock();
  try {
    const data = await fetchAccountPageData(
      {
        user: {
          id: "buyer-1",
          fullName: "Người mua A",
          email: "buyer@example.com",
          phone: "0901234567",
          role: "buyer",
          shopId: "",
          avatarUrl: "",
          phoneVerified: true,
        },
      },
      { shopId: SHOP_ID, siteProducts: [], siteShop: null }
    );
    assert.equal(mock.voucherCalls.length, 1);
    assertStorefrontVoucherCall(mock.voucherCalls[0], SHOP_ID, "fetchAccountPageData");
    assert.equal(data.vouchers.length, 1);
    assert.equal(data.vouchers[0].code, "SHOPA10");
  } finally {
    mock.restore();
  }
});

test("siteContext.server.ts loadSiteVouchers sends STOREFRONT + storefront_shop_id on auth user branch", async () => {
  mockState.accessToken = "buyer-access-token";
  const mock = installStrictGatewayMock();
  try {
    const data = await loadSiteVouchers(SITE_HOST);
    assert.ok(data);
    const authedCalls = mock.voucherCalls.filter((c) => c.hasAuth);
    assert.equal(authedCalls.length, 1, "expected auth:user /api/v1/vouchers call");
    assertStorefrontVoucherCall(authedCalls[0], SHOP_ID, "loadSiteVouchers(auth:user)");
    assert.equal(data.error, null);
    assert.equal(data.rawVouchers.length, 1);
    assert.equal(data.rawVouchers[0].code, "SHOPA10");
  } finally {
    mock.restore();
  }
});

test("siteContext.server.ts loadSiteVouchers sends STOREFRONT + storefront_shop_id on auth none fallback branch", async () => {
  mockState.accessToken = "expired-buyer-token";
  const mock = installStrictGatewayMock({ failAuthVouchersWith401: true });
  try {
    const data = await loadSiteVouchers(SITE_HOST);
    assert.ok(data);
    // 1) loadShopSiteRenderData (auth:none), 2) loadSiteVouchers auth:user (401), 3) loadSiteVouchers auth:none fallback (200)
    assert.equal(mock.voucherCalls.length, 3, "expected bundle + user(401) + none fallback calls");
    assert.equal(mock.voucherCalls[1].hasAuth, true);
    assertStorefrontVoucherCall(mock.voucherCalls[1], SHOP_ID, "loadSiteVouchers(auth:user->401)");
    assert.equal(mock.voucherCalls[2].hasAuth, false);
    assertStorefrontVoucherCall(mock.voucherCalls[2], SHOP_ID, "loadSiteVouchers(auth:none)");
    assert.equal(data.error, null);
    assert.equal(data.rawVouchers.length, 1);
    assert.equal(data.rawVouchers[0].code, "SHOPA10");
  } finally {
    mock.restore();
  }
});
