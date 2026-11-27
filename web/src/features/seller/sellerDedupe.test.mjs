// Run: cd web && node --disable-warning=MODULE_TYPELESS_PACKAGE_JSON --test src/features/seller/sellerDedupe.test.mjs
import assert from "node:assert/strict";
import { AsyncLocalStorage } from "node:async_hooks";
import { existsSync, readFileSync } from "node:fs";
import { registerHooks } from "node:module";
import { dirname, resolve } from "node:path";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import ts from "typescript";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const WEB_SRC = resolve(__dirname, "../..");

// Mirrors React 19 per-request cache() semantics: memoizes per AsyncLocalStorage request scope
// using Object.is positional argument trees, and executes uncached outside a request scope.
const requestCacheStore = new AsyncLocalStorage();

function runInRequestScope(fn) {
  return requestCacheStore.run(new Map(), fn);
}

function reactCachePolyfill(fn) {
  return (...args) => {
    const scope = requestCacheStore.getStore();
    if (!scope) return fn(...args);
    let root = scope.get(fn);
    if (!root) {
      root = { children: new Map(), hasValue: false, value: undefined };
      scope.set(fn, root);
    }
    let node = root;
    for (const arg of args) {
      let next = node.children.get(arg);
      if (!next) {
        next = { children: new Map(), hasValue: false, value: undefined };
        node.children.set(arg, next);
      }
      node = next;
    }
    if (!node.hasValue) {
      node.hasValue = true;
      node.value = fn(...args);
    }
    return node.value;
  };
}

const mockState = {
  accessToken: "seller-a-token",
  headers: new Headers({ host: "nexusmart.localhost:3300" }),
};

globalThis.__NM_SELLER_DEDUPE_TEST__ = {
  state: mockState,
  cache: reactCachePolyfill,
};

const VIRTUAL_MODULES = {
  "virtual:react": `
    export const cache = globalThis.__NM_SELLER_DEDUPE_TEST__.cache;
    export function createElement(type, props, ...children) {
      return { type, props: { ...(props ?? {}), ...(children.length ? { children } : {}) } };
    }
    export default { cache, createElement };
  `,
  "virtual:next-headers": `
    export async function headers() {
      return globalThis.__NM_SELLER_DEDUPE_TEST__.state.headers;
    }
    export async function cookies() {
      return {
        get(name) {
          const tok = globalThis.__NM_SELLER_DEDUPE_TEST__.state.accessToken;
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
  "virtual:next-cache": `
    export function revalidateTag() {}
    export function revalidatePath() {}
  `,
  "virtual:next-link": `
    export default function Link(props) { return { component: "Link", props }; }
  `,
  "virtual:stylesheet": `export default {};`,
  "virtual:components-app": `
    export function ErrorState(props) { return { component: "ErrorState", props }; }
    export function EmptyState(props) { return { component: "EmptyState", props }; }
    export function AppShell(props) { return { component: "AppShell", props }; }
  `,
  "virtual:ui-scope": `
    export function UiScope(props) { return { component: "UiScope", props }; }
  `,
  "virtual:seller-shell": `
    export function SellerShell(props) { return { component: "SellerShell", props }; }
  `,
  "virtual:seller-session-sync": `
    export function SellerSessionSync(props) { return { component: "SellerSessionSync", props }; }
  `,
  "virtual:seller-guard": `
    export function SellerSuspense(props) { return { component: "SellerSuspense", props }; }
  `,
  "virtual:seller-orders-client": `
    export function SellerOrdersClient(props) { return { component: "SellerOrdersClient", props }; }
  `,
  "virtual:seller-products-client": `
    export function SellerProductsClient(props) { return { component: "SellerProductsClient", props }; }
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
    if (specifier === "next/cache") return { shortCircuit: true, url: "virtual:next-cache" };
    if (specifier === "next/link") return { shortCircuit: true, url: "virtual:next-link" };
    if (specifier.endsWith(".css")) return { shortCircuit: true, url: "virtual:stylesheet" };
    if (specifier === "@/components/app")
      return { shortCircuit: true, url: "virtual:components-app" };
    if (specifier === "@/components/ui/ui-scope")
      return { shortCircuit: true, url: "virtual:ui-scope" };
    if (specifier === "@/features/seller/SellerShell")
      return { shortCircuit: true, url: "virtual:seller-shell" };
    if (specifier === "@/features/seller/shell/SellerSessionSync")
      return { shortCircuit: true, url: "virtual:seller-session-sync" };
    if (specifier === "@/features/seller/SellerStateGuard")
      return { shortCircuit: true, url: "virtual:seller-guard" };
    if (specifier === "@/features/seller/SellerOrdersClient")
      return { shortCircuit: true, url: "virtual:seller-orders-client" };
    if (specifier === "@/features/seller/SellerProductsClient")
      return { shortCircuit: true, url: "virtual:seller-products-client" };
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

const a8 = await import("../../lib/a8-types.ts");
const { getSession } = await import("../../lib/server/session.ts");
const sellerServer = await import("./sellerServer.ts");
const siteServer = await import("./site/siteServer.ts");
const { default: SellerLayout } = await import("../../app/(seller)/layout.tsx");
const { default: SellerOrdersPage } = await import("../../app/(seller)/seller/don-hang/page.tsx");
const { default: SellerProductsPage } = await import("../../app/(seller)/seller/san-pham/page.tsx");
const productsRoute = await import("../../app/api/seller/products/route.ts");

function jsonRes(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function makeSampleProducts() {
  return [
    {
      id: "p-approved-low",
      shop_id: "shop-a",
      category_id: "cat-1",
      name: "Approved Low Stock (boundary 5)",
      status: "APPROVED",
      skus: [{ id: "s1", is_active: true, available_stock: 5, stock: 5, reserved_stock: 0 }],
    },
    {
      id: "p-approved-ok",
      shop_id: "shop-a",
      category_id: "cat-1",
      name: "Approved Healthy (active 6 + inactive 0)",
      status: "APPROVED",
      skus: [
        { id: "s2", is_active: true, available_stock: 6, stock: 6, reserved_stock: 0 },
        { id: "s3", is_active: false, available_stock: 0, stock: 0, reserved_stock: 0 },
      ],
    },
    {
      id: "p-oos",
      shop_id: "shop-a",
      category_id: "cat-1",
      name: "Out Of Stock",
      status: "OUT_OF_STOCK",
      skus: [{ id: "s4", is_active: true, available_stock: 0, stock: 0, reserved_stock: 0 }],
    },
    {
      id: "p-pending-low",
      shop_id: "shop-a",
      category_id: "cat-1",
      name: "Pending Low (excluded from low_stock)",
      status: "PENDING",
      skus: [{ id: "s5", is_active: true, available_stock: 2, stock: 2, reserved_stock: 0 }],
    },
    {
      id: "p-rejected-low",
      shop_id: "shop-a",
      category_id: "cat-1",
      name: "Rejected Low (excluded from low_stock)",
      status: "REJECTED",
      skus: [{ id: "s6", is_active: true, available_stock: 0, stock: 0, reserved_stock: 0 }],
    },
    {
      id: "p-hidden-low",
      shop_id: "shop-a",
      category_id: "cat-1",
      name: "Hidden Low (excluded from low_stock)",
      status: "HIDDEN",
      skus: [{ id: "s7", is_active: true, available_stock: 1, stock: 1, reserved_stock: 0 }],
    },
  ];
}

function installGatewayMock(overrides = {}) {
  const calls = [];
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (rawUrl, init) => {
    const url = new URL(String(rawUrl));
    const authHeader = init?.headers?.Authorization ?? init?.headers?.authorization ?? "";
    calls.push({
      method: init?.method ?? "GET",
      pathname: url.pathname,
      search: url.search,
      params: Object.fromEntries(url.searchParams.entries()),
      authHeader,
    });

    if (overrides.handler) {
      const custom = await overrides.handler(url, init);
      if (custom) return custom;
    }

    if (url.pathname === "/api/v1/users/me") {
      const isB = authHeader.includes("seller-b-token");
      return jsonRes({
        user: isB
          ? { id: "seller-b-user", role: "seller", shop_id: "shop-b", status: "ACTIVE" }
          : { id: "seller-a-user", role: "seller", shop_id: "shop-a", status: "ACTIVE" },
      });
    }
    if (url.pathname === "/api/v1/seller/shop") {
      return jsonRes({ shop: { id: "shop-a", name: "Shop A", status: "ACTIVE" } });
    }
    if (url.pathname === "/api/v1/seller/orders") {
      return jsonRes({
        items: [
          { id: "ord-1", order_code: "NM-001", shop_id: "shop-a", status: "CONFIRMED", items: [] },
          { id: "ord-2", order_code: "NM-002", shop_id: "shop-a", status: "DELIVERED", items: [] },
        ],
        total: 42,
        status_counts: [
          { status: "CONFIRMED", count: 15 },
          { status: "PREPARING", count: 12 },
          { status: "DELIVERED", count: 15 },
        ],
      });
    }
    if (url.pathname === "/api/v1/seller/returns") {
      return jsonRes({ items: [], total: 3 });
    }
    if (url.pathname === "/api/v1/seller/chat/conversations") {
      return jsonRes({ items: [], total: 0, total_unread: 2 });
    }
    if (url.pathname === "/api/v1/seller/site") {
      return jsonRes({
        site: {
          id: "site-a",
          shop_id: "shop-a",
          subdomain: "shop-a",
          status: "PUBLISHED",
        },
      });
    }
    if (url.pathname === "/api/v1/seller/categories") {
      return jsonRes({
        categories: [{ id: "cat-1", name: "Áo", slug: "ao", parent_id: "", sort_order: 1 }],
      });
    }
    if (url.pathname === "/api/v1/seller/products") {
      const items = makeSampleProducts();
      return jsonRes({ items, total: items.length, page: 1, page_size: 20 });
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

function findReactNodeByProp(node, predicate) {
  if (!node || typeof node !== "object") return null;
  if (predicate(node)) return node;
  const children = node.props?.children;
  if (Array.isArray(children)) {
    for (const c of children) {
      const hit = findReactNodeByProp(c, predicate);
      if (hit) return hit;
    }
  } else if (children && typeof children === "object") {
    return findReactNodeByProp(children, predicate);
  }
  return null;
}

test("1. getSession + seller guards call /api/v1/users/me at most once per request and never cache across requests/users", async () => {
  const mock = installGatewayMock();
  try {
    mockState.accessToken = "seller-a-token";
    const [s1, s2, s3] = await runInRequestScope(async () => {
      const a = await getSession();
      const b = await sellerServer.requireSellerPortalSession("/seller");
      const c = await sellerServer.requireSellerPage();
      return [a, b, c];
    });
    assert.equal(s1?.user.shopId, "shop-a");
    assert.equal(s2.user.shopId, "shop-a");
    assert.equal(s3.user.shopId, "shop-a");
    assert.equal(
      mock.calls.filter((c) => c.pathname === "/api/v1/users/me").length,
      1,
      "same server request must call /api/v1/users/me only once"
    );

    // Next request from a different seller must NOT reuse previous request's session.
    mockState.accessToken = "seller-b-token";
    const s4 = await runInRequestScope(() => sellerServer.requireSellerPage());
    assert.equal(s4.user.id, "seller-b-user");
    assert.equal(s4.user.shopId, "shop-b");
    assert.equal(
      mock.calls.filter((c) => c.pathname === "/api/v1/users/me").length,
      2,
      "separate request must re-verify session against /api/v1/users/me"
    );
  } finally {
    mockState.accessToken = "seller-a-token";
    mock.restore();
  }
});

test("2. /seller/don-hang full SSR (SellerLayout + SellerOrdersPage) deduplicates /api/v1/seller/orders to 1 call and preserves pendingOrdersCount", async () => {
  const mock = installGatewayMock();
  try {
    const { layoutTree, pageTree } = await runInRequestScope(async () => {
      const [l, p] = await Promise.all([SellerLayout({ children: null }), SellerOrdersPage()]);
      return { layoutTree: l, pageTree: p };
    });

    const orderCalls = mock.calls.filter((c) => c.pathname === "/api/v1/seller/orders");
    const meCalls = mock.calls.filter((c) => c.pathname === "/api/v1/users/me");
    assert.equal(meCalls.length, 1, "full SSR /seller/don-hang must call users/me only once");
    assert.equal(
      orderCalls.length,
      1,
      `full SSR /seller/don-hang must deduplicate seller/orders to 1 call, got ${orderCalls.length}: ${JSON.stringify(orderCalls)}`
    );
    assert.equal(orderCalls[0].params.page_size, String(a8.SELLER_ORDERS_PAGE_SIZE));
    assert.equal(orderCalls[0].params.include_status_counts, "true");
    assert.equal(
      mock.calls.length,
      7,
      "full SSR /seller/don-hang should make 7 gateway calls total (was 8)"
    );

    // SellerShell badge reflects full status_counts (15 CONFIRMED + 12 PREPARING = 27), not truncated to page items
    assert.equal(layoutTree.props.pendingOrdersCount, 27);
    const clientNode = findReactNodeByProp(pageTree, (n) => "initialOrders" in (n.props ?? {}));
    assert.ok(clientNode, "SellerOrdersPage must render SellerOrdersClient");
    assert.equal(clientNode.props.initialTotal, 42);
    assert.equal(clientNode.props.initialStatusCounts.length, 3);

    // Fallback when status_counts is omitted/empty
    assert.equal(
      a8.resolveSellerPendingOrdersCount({
        items: [{ status: "CONFIRMED" }, { status: "PREPARING" }, { status: "SHIPPING" }],
      }),
      2
    );

    // Default-parameter normalization: loadSellerOrders(100) and loadSellerOrders(100, 1, false) share 1 call within a request
    mock.calls.length = 0;
    await runInRequestScope(async () => {
      await Promise.all([
        sellerServer.loadSellerOrders(100),
        sellerServer.loadSellerOrders(100, 1, false),
      ]);
    });
    assert.equal(
      mock.calls.filter((c) => c.pathname === "/api/v1/seller/orders").length,
      1,
      "loadSellerOrders must normalize default args before React.cache lookup"
    );

    // A new server request must NOT reuse the previous request's orders cache
    await runInRequestScope(() => sellerServer.loadSellerOrders(100));
    assert.equal(
      mock.calls.filter((c) => c.pathname === "/api/v1/seller/orders").length,
      2,
      "separate requests must never share cached seller/orders data"
    );
  } finally {
    mock.restore();
  }
});

test("3. deriveSellerProductTabCountsFromCompleteList matches backend SQL low_stock & status counts and returns null for multi-page catalogs", () => {
  assert.equal(
    typeof a8.deriveSellerProductTabCountsFromCompleteList,
    "function",
    "a8-types.ts must export deriveSellerProductTabCountsFromCompleteList"
  );

  // Empty shop: all 6 counts are 0
  assert.deepEqual(a8.deriveSellerProductTabCountsFromCompleteList([], 0), {
    all: 0,
    APPROVED: 0,
    PENDING: 0,
    REJECTED: 0,
    HIDDEN: 0,
    LOW_OOS: 0,
  });

  // Complete 6-product shop: LOW_OOS only counts APPROVED/OUT_OF_STOCK with active SKU available_stock <= 5
  const items = makeSampleProducts();
  assert.deepEqual(a8.deriveSellerProductTabCountsFromCompleteList(items, items.length), {
    all: 6,
    APPROVED: 2,
    PENDING: 1,
    REJECTED: 1,
    HIDDEN: 1,
    LOW_OOS: 2,
  });

  // Multi-page shop (total > items.length): must return null so caller falls back to backend totals
  assert.equal(a8.deriveSellerProductTabCountsFromCompleteList(items, 25), null);
});

test("4. /seller/san-pham RSC computes initialTabCounts with 0 extra calls when total<=20 and 0 extra users/me + 5 filter calls when total>20", async () => {
  // Case A: small/medium shop (total = 6 <= 20) -> exactly 3 gateway calls total (1 users/me + 1 products + 1 categories)
  const smallMock = installGatewayMock();
  try {
    const pageTree = await runInRequestScope(() => SellerProductsPage());
    assert.equal(smallMock.calls.filter((c) => c.pathname === "/api/v1/users/me").length, 1);
    assert.equal(smallMock.calls.filter((c) => c.pathname === "/api/v1/seller/products").length, 1);
    assert.equal(
      smallMock.calls.filter((c) => c.pathname === "/api/v1/seller/categories").length,
      1
    );
    assert.equal(
      smallMock.calls.length,
      3,
      "soft nav to /seller/san-pham (<=20 items) must make only 3 gateway calls"
    );

    const clientNode = findReactNodeByProp(pageTree, (n) => "initialProducts" in (n.props ?? {}));
    assert.ok(clientNode, "SellerProductsPage must render SellerProductsClient");
    assert.deepEqual(clientNode.props.initialTabCounts, {
      all: 6,
      APPROVED: 2,
      PENDING: 1,
      REJECTED: 1,
      HIDDEN: 1,
      LOW_OOS: 2,
    });
  } finally {
    smallMock.restore();
  }

  // Case B: multi-page shop (total = 35 > 20) -> 1 users/me, 1 categories, 1 page-1 products + 5 non-all filter counts (no redundant 'all' call!)
  const bigMock = installGatewayMock({
    handler(url) {
      if (url.pathname !== "/api/v1/seller/products") return null;
      const p = url.searchParams;
      if (p.get("low_stock") === "true") {
        // Simulate partial gateway failure on LOW_OOS -> must resolve to null, not 0 (S1)
        return jsonRes({ error: { code: "RATE_LIMITED", message: "slow down" } }, 429);
      }
      const st = p.get("status");
      if (st === "APPROVED") return jsonRes({ items: [], total: 22 });
      if (st === "PENDING") return jsonRes({ items: [], total: 4 });
      if (st === "REJECTED") return jsonRes({ items: [], total: 3 });
      if (st === "HIDDEN") return jsonRes({ items: [], total: 6 });
      return jsonRes({ items: makeSampleProducts(), total: 35, page: 1, page_size: 20 });
    },
  });
  try {
    const pageTree = await runInRequestScope(() => SellerProductsPage());
    assert.equal(bigMock.calls.filter((c) => c.pathname === "/api/v1/users/me").length, 1);
    const prodCalls = bigMock.calls.filter((c) => c.pathname === "/api/v1/seller/products");
    assert.equal(
      prodCalls.length,
      6,
      "multi-page shop must call page-1 list + 5 non-all filter counts (skipping redundant all)"
    );
    const redundantAllCountCall = prodCalls.find(
      (c) => c.params.page_size === "1" && !c.params.status && !c.params.low_stock
    );
    assert.equal(
      redundantAllCountCall,
      undefined,
      "must not re-query 'all' when initialTotal is known"
    );

    const clientNode = findReactNodeByProp(pageTree, (n) => "initialProducts" in (n.props ?? {}));
    assert.deepEqual(clientNode.props.initialTabCounts, {
      all: 35,
      APPROVED: 22,
      PENDING: 4,
      REJECTED: 3,
      HIDDEN: 6,
      LOW_OOS: null,
    });
  } finally {
    bigMock.restore();
  }
});

test("5. GET /api/seller/products?tab_counts_only=true checks session once and deduplicates <=20 catalogs to 1 product call", async () => {
  // Case A: <=20 products -> 1 users/me + 1 seller/products
  const smallMock = installGatewayMock();
  try {
    const req = {
      method: "GET",
      headers: new Headers({ host: "nexusmart.localhost:3300" }),
      nextUrl: new URL("http://nexusmart.localhost:3300/api/seller/products?tab_counts_only=true"),
    };
    const res = await productsRoute.GET(req, {});
    assert.equal(res.status, 200);
    const body = await res.json();
    assert.deepEqual(body.tab_counts, {
      all: 6,
      APPROVED: 2,
      PENDING: 1,
      REJECTED: 1,
      HIDDEN: 1,
      LOW_OOS: 2,
    });
    assert.equal(smallMock.calls.filter((c) => c.pathname === "/api/v1/users/me").length, 1);
    assert.equal(smallMock.calls.filter((c) => c.pathname === "/api/v1/seller/products").length, 1);
  } finally {
    smallMock.restore();
  }

  // Case B: page-1 gateway 503/429 failure must NOT storm 6 extra filter calls
  const failMock = installGatewayMock({
    handler(url) {
      if (url.pathname === "/api/v1/seller/products") {
        return jsonRes({ error: { code: "RATE_LIMITED", message: "slow down" } }, 429);
      }
      return null;
    },
  });
  try {
    const req = {
      method: "GET",
      headers: new Headers({ host: "nexusmart.localhost:3300" }),
      nextUrl: new URL("http://nexusmart.localhost:3300/api/seller/products?tab_counts_only=true"),
    };
    const res = await productsRoute.GET(req, {});
    assert.equal(res.status, 200);
    const body = await res.json();
    assert.deepEqual(body.tab_counts, {
      all: null,
      APPROVED: null,
      PENDING: null,
      REJECTED: null,
      HIDDEN: null,
      LOW_OOS: null,
    });
    assert.equal(
      failMock.calls.filter((c) => c.pathname === "/api/v1/seller/products").length,
      1,
      "must not fan out 6 extra calls when initial seller/products fetch fails"
    );
  } finally {
    failMock.restore();
  }
});

test("6. SellerLayout + SellerSite/SellerShop/PendingReturns share per-request React.cache loaders", async () => {
  const mock = installGatewayMock();
  try {
    await runInRequestScope(async () => {
      await Promise.all([
        sellerServer.loadSellerShop(),
        sellerServer.loadSellerShop(),
        sellerServer.loadSellerPendingReturnsCount(),
        sellerServer.loadSellerPendingReturnsCount(),
        siteServer.loadSellerSiteSummaryServer(),
        siteServer.loadSellerSiteServer(),
      ]);
    });
    assert.equal(mock.calls.filter((c) => c.pathname === "/api/v1/seller/shop").length, 1);
    assert.equal(mock.calls.filter((c) => c.pathname === "/api/v1/seller/returns").length, 1);
    assert.equal(mock.calls.filter((c) => c.pathname === "/api/v1/seller/site").length, 1);
  } finally {
    mock.restore();
  }
});

test("7. SellerProductsClient skips initial loadTabCounts when SSR provides initialTabCounts and uses tab_counts_only=true on client reload", () => {
  const src = readFileSync(resolve(__dirname, "SellerProductsClient.tsx"), "utf8");
  assert.ok(
    src.includes("initialTabCounts"),
    "SellerProductsClient must accept SSR initialTabCounts to avoid mount fetch storm"
  );
  assert.ok(
    src.includes("tab_counts_only=true"),
    "SellerProductsClient loadTabCounts must coalesce client reloads into 1 BFF request"
  );
  assert.ok(
    src.includes("deriveSellerProductTabCountsFromCompleteList"),
    "SellerProductsClient must derive tab counts locally when page 1 holds the complete catalog"
  );
});
