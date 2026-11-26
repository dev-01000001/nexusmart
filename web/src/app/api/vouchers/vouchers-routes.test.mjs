// Run: node --test src/app/api/vouchers/vouchers-routes.test.mjs
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
globalThis.__NM_VOUCHERS_ROUTES_TEST__ = mockState;

const VIRTUAL_MODULES = {
  "virtual:react": `
    export function cache(fn) { return fn; }
    export default { cache };
  `,
  "virtual:next-headers": `
    export async function headers() {
      return globalThis.__NM_VOUCHERS_ROUTES_TEST__.headers;
    }
    export async function cookies() {
      return {
        get(name) {
          const tok = globalThis.__NM_VOUCHERS_ROUTES_TEST__.accessToken;
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

const validateRoute = await import("./validate/route.ts");
const previewRoute = await import("./preview/route.ts");
const { filterPreviewableCartGroups, isPreviewableCartItem } =
  await import("../../../lib/server/storefront.ts");

const SITE_A_HOST = "shop-a.nexusmart.localhost";
const SHOP_A_ID = "shop-a";

function jsonRes(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function makePostReq(pathname, body) {
  return new Request(`http://${SITE_A_HOST}${pathname}`, {
    method: "POST",
    headers: {
      host: SITE_A_HOST,
      origin: `http://${SITE_A_HOST}`,
      "content-type": "application/json",
    },
    body: JSON.stringify(body),
  });
}

function makeVoucher(overrides = {}) {
  return {
    id: "v-shop-a-1",
    code: "SHOP250",
    name: "Giảm 30.000đ đơn từ 250.000đ",
    scope: "SHOP",
    shop_id: SHOP_A_ID,
    discount_type: "FIXED",
    value_vnd: 30000,
    discount_percent: 0,
    max_discount_vnd: 30000,
    min_order_vnd: 250000,
    quota: 100,
    used_count: 10,
    remaining_quota: 90,
    per_user_limit: 1,
    budget_vnd: 0,
    used_budget_vnd: 0,
    ends_at: "2099-01-01T00:00:00Z",
    is_active: true,
    saved: true,
    eligible: true,
    ineligible_reason: "",
    ...overrides,
  };
}

/**
 * Mirrors gateway `/api/v1/promotions/preview` (D4):
 * - rejects empty shop_items / quantity <= 0 with 400 INVALID_ARGUMENT
 * - rejects inactive SKU with 409 SKU_INACTIVE
 * - computes authoritative subtotal from `skuCatalog` prices (ignoring client subtotal/unit_price)
 */
function installGatewayMock({ cart, vouchers, skuCatalog, previewOverrides }) {
  const previewCalls = [];
  const origFetch = globalThis.fetch;

  globalThis.fetch = async (rawUrl, init) => {
    const url = new URL(String(rawUrl));
    const method = (init?.method ?? "GET").toUpperCase();
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;

    if (url.pathname === "/api/v1/sites/by-host") {
      return jsonRes({
        site: {
          id: "site-a",
          shop_id: SHOP_A_ID,
          status: "PUBLISHED",
          primary_host: SITE_A_HOST,
        },
        shop: { status: "ACTIVE" },
      });
    }
    if (url.pathname === "/api/v1/users/me") {
      return jsonRes({ user: { id: "buyer-1", role: "buyer", shop_id: "", status: "ACTIVE" } });
    }
    if (url.pathname === "/api/v1/vouchers" && method === "GET") {
      return jsonRes({ vouchers });
    }
    if (url.pathname === "/api/v1/cart" && method === "GET") {
      return jsonRes({ cart });
    }
    if (url.pathname === "/api/v1/promotions/preview" && method === "POST") {
      previewCalls.push(body);
      const shopItems = body?.shop_items ?? [];
      if (shopItems.length === 0) {
        return jsonRes(
          { error: { code: "INVALID_ARGUMENT", message: "shop_items is required" } },
          400
        );
      }

      const shops = [];
      let totalSubtotal = 0;
      let totalVoucherDiscount = 0;
      let totalShipping = 0;

      for (const s of shopItems) {
        if (!Array.isArray(s.items) || s.items.length === 0) {
          return jsonRes(
            {
              error: {
                code: "INVALID_ARGUMENT",
                message: "each shop_items entry must contain at least one item",
              },
            },
            400
          );
        }
        let shopSubtotal = 0;
        for (const it of s.items) {
          if (!it.sku_id) {
            return jsonRes(
              { error: { code: "INVALID_ARGUMENT", message: "sku_id is required" } },
              400
            );
          }
          if (!(it.quantity > 0)) {
            return jsonRes(
              { error: { code: "INVALID_ARGUMENT", message: "quantity must be > 0" } },
              400
            );
          }
          const sku = skuCatalog[it.sku_id];
          if (!sku) {
            return jsonRes(
              { error: { code: "INVALID_ARGUMENT", message: "one or more SKUs are invalid" } },
              400
            );
          }
          if (!sku.is_active) {
            return jsonRes(
              { error: { code: "SKU_INACTIVE", message: `sku ${it.sku_id} is not active` } },
              409
            );
          }
          shopSubtotal += sku.price_vnd * it.quantity;
        }

        const voucherId = body?.shop_voucher_ids?.[s.shop_id] ?? "";
        const voucher = vouchers.find((v) => v.id === voucherId);
        const discount =
          voucher && shopSubtotal >= voucher.min_order_vnd
            ? Math.min(voucher.value_vnd, shopSubtotal)
            : 0;
        const shipping = s.shipping_fee_vnd ?? 0;

        totalSubtotal += shopSubtotal;
        totalVoucherDiscount += discount;
        totalShipping += shipping;
        shops.push({
          shop_id: s.shop_id,
          subtotal_vnd: shopSubtotal,
          flash_sale_discount_vnd: 0,
          shop_program_discount_vnd: 0,
          shop_voucher_id: discount > 0 ? voucherId : "",
          shop_voucher_discount_vnd: discount,
          platform_voucher_discount_vnd: 0,
          freeship_discount_vnd: 0,
          shop_freeship_discount_vnd: 0,
          coins_discount_vnd: 0,
          shipping_fee_vnd: shipping,
          final_shipping_fee_vnd: shipping,
          final_payable_vnd: shopSubtotal - discount + shipping,
        });
      }

      if (typeof previewOverrides === "function") {
        return jsonRes(previewOverrides(body));
      }
      return jsonRes({
        breakdown: {
          shops,
          total_subtotal_vnd: totalSubtotal,
          total_flash_sale_discount_vnd: 0,
          total_shop_program_discount_vnd: 0,
          total_shop_voucher_discount_vnd: totalVoucherDiscount,
          total_platform_voucher_discount_vnd: 0,
          total_freeship_discount_vnd: 0,
          total_shop_freeship_discount_vnd: 0,
          total_coins_discount_vnd: 0,
          total_discount_vnd: totalVoucherDiscount,
          platform_cost_vnd: 0,
          shop_cost_vnd: totalVoucherDiscount,
          total_shipping_fee_vnd: totalShipping,
          final_payable_vnd: totalSubtotal - totalVoucherDiscount + totalShipping,
          warnings: [],
        },
      });
    }

    return jsonRes({ error: { code: "NOT_FOUND", message: `unmocked ${url.pathname}` } }, 404);
  };

  return {
    previewCalls,
    restore() {
      globalThis.fetch = origFetch;
    },
  };
}

test("POST /api/vouchers/validate filters unavailable SKU like /preview instead of failing 409 SKU_INACTIVE", async () => {
  const voucher = makeVoucher({
    id: "v-100",
    code: "SHOP100",
    min_order_vnd: 100000,
    value_vnd: 20000,
  });
  const cart = {
    user_id: "buyer-1",
    shop_groups: [
      {
        shop_id: SHOP_A_ID,
        shop_name: "Shop A",
        subtotal_vnd: 200000,
        total_weight_grams: 300,
        items: [
          {
            sku_id: "sku-active",
            product_id: "p-1",
            shop_id: SHOP_A_ID,
            price_vnd: 200000,
            quantity: 1,
            available_stock: 5,
            weight_grams: 300,
            selected: true,
            unavailable: false,
          },
          {
            sku_id: "sku-dead",
            product_id: "p-2",
            shop_id: SHOP_A_ID,
            price_vnd: 0,
            quantity: 1,
            available_stock: 0,
            weight_grams: 0,
            selected: true,
            unavailable: true,
            unavailable_reason: "SKU_INACTIVE",
          },
        ],
      },
    ],
    total_items: 1,
    total_vnd: 200000,
  };
  const skuCatalog = {
    "sku-active": { is_active: true, price_vnd: 200000 },
    "sku-dead": { is_active: false, price_vnd: 150000 },
  };

  const mock = installGatewayMock({ cart, vouchers: [voucher], skuCatalog });
  try {
    const valRes = await validateRoute.POST(
      makePostReq("/api/vouchers/validate", { code: "SHOP100", shipping_fee_vnd: 15000 }),
      {}
    );
    assert.equal(valRes.status, 200);
    const valData = await valRes.json();
    assert.equal(valData.valid, true);
    assert.equal(valData.discount_vnd, 20000);

    const prevRes = await previewRoute.POST(
      makePostReq("/api/vouchers/preview", { shop_voucher_id: "v-100", shipping_fee_vnd: 15000 }),
      {}
    );
    assert.equal(prevRes.status, 200);
    const prevData = await prevRes.json();
    assert.equal(prevData.breakdown.total_shop_voucher_discount_vnd, 20000);

    assert.equal(mock.previewCalls.length, 2);
    assert.deepEqual(mock.previewCalls[0].shop_items, mock.previewCalls[1].shop_items);
    assert.deepEqual(mock.previewCalls[0].shop_items[0].items, [
      { sku_id: "sku-active", quantity: 1, flash_sale_item_id: "" },
    ]);
    assert.equal("unit_price_vnd" in mock.previewCalls[0].shop_items[0].items[0], false);
  } finally {
    mock.restore();
  }
});

test("POST /api/vouchers/validate filters out-of-stock active SKU (available_stock=0) so min_order_vnd cannot produce phantom discount", async () => {
  const voucher = makeVoucher({
    id: "v-250",
    code: "SHOP250",
    min_order_vnd: 250000,
    value_vnd: 30000,
  });
  const cart = {
    user_id: "buyer-1",
    shop_groups: [
      {
        shop_id: SHOP_A_ID,
        shop_name: "Shop A",
        subtotal_vnd: 300000,
        total_weight_grams: 600,
        items: [
          {
            sku_id: "sku-instock",
            product_id: "p-1",
            shop_id: SHOP_A_ID,
            price_vnd: 100000,
            quantity: 1,
            available_stock: 4,
            weight_grams: 200,
            selected: true,
            unavailable: false,
          },
          {
            sku_id: "sku-oos",
            product_id: "p-2",
            shop_id: SHOP_A_ID,
            price_vnd: 200000,
            quantity: 1,
            available_stock: 0,
            weight_grams: 400,
            selected: true,
            unavailable: false,
          },
        ],
      },
    ],
    total_items: 2,
    total_vnd: 300000,
  };
  const skuCatalog = {
    "sku-instock": { is_active: true, price_vnd: 100000 },
    "sku-oos": { is_active: true, price_vnd: 200000 },
  };

  const mock = installGatewayMock({ cart, vouchers: [voucher], skuCatalog });
  try {
    const valRes = await validateRoute.POST(
      makePostReq("/api/vouchers/validate", { code: "SHOP250" }),
      {}
    );
    assert.equal(valRes.status, 200);
    const valData = await valRes.json();
    assert.equal(valData.valid, false);
    assert.equal(valData.discount_vnd, 0);

    const prevRes = await previewRoute.POST(
      makePostReq("/api/vouchers/preview", { shop_voucher_id: "v-250" }),
      {}
    );
    assert.equal(prevRes.status, 200);
    const prevData = await prevRes.json();
    assert.equal(prevData.breakdown.total_subtotal_vnd, 100000);
    assert.equal(prevData.breakdown.total_shop_voucher_discount_vnd, 0);

    assert.equal(mock.previewCalls.length, 2);
    assert.deepEqual(mock.previewCalls[0].shop_items, mock.previewCalls[1].shop_items);
    assert.equal(mock.previewCalls[0].shop_items[0].subtotal_vnd, 100000);
    assert.deepEqual(mock.previewCalls[0].shop_items[0].items, [
      { sku_id: "sku-instock", quantity: 1, flash_sale_item_id: "" },
    ]);
  } finally {
    mock.restore();
  }
});

test("POST /api/vouchers/validate filters quantity<=0 lines and short-circuits when no previewable lines remain", async () => {
  const voucher = makeVoucher({
    id: "v-100",
    code: "SHOP100",
    min_order_vnd: 100000,
    value_vnd: 20000,
  });
  const skuCatalog = {
    "sku-active": { is_active: true, price_vnd: 150000 },
    "sku-zero": { is_active: true, price_vnd: 100000 },
    "sku-oos": { is_active: true, price_vnd: 200000 },
    "sku-dead": { is_active: false, price_vnd: 200000 },
  };

  // 1. Mixed valid + quantity<=0 -> keeps only valid SKU (no 400 from gateway).
  const mixedCart = {
    user_id: "buyer-1",
    shop_groups: [
      {
        shop_id: SHOP_A_ID,
        shop_name: "Shop A",
        subtotal_vnd: 150000,
        total_weight_grams: 200,
        items: [
          {
            sku_id: "sku-active",
            shop_id: SHOP_A_ID,
            price_vnd: 150000,
            quantity: 1,
            available_stock: 3,
            weight_grams: 200,
            selected: true,
          },
          {
            sku_id: "sku-zero",
            shop_id: SHOP_A_ID,
            price_vnd: 100000,
            quantity: 0,
            available_stock: 5,
            weight_grams: 100,
            selected: true,
          },
        ],
      },
    ],
    total_items: 1,
    total_vnd: 150000,
  };
  const mockMixed = installGatewayMock({ cart: mixedCart, vouchers: [voucher], skuCatalog });
  try {
    const valRes = await validateRoute.POST(
      makePostReq("/api/vouchers/validate", { code: "SHOP100" }),
      {}
    );
    assert.equal(valRes.status, 200);
    const valData = await valRes.json();
    assert.equal(valData.valid, true);
    assert.equal(valData.discount_vnd, 20000);
    assert.deepEqual(mockMixed.previewCalls[0].shop_items[0].items, [
      { sku_id: "sku-active", quantity: 1, flash_sale_item_id: "" },
    ]);
  } finally {
    mockMixed.restore();
  }

  // 2. Cart with ONLY unavailable / out-of-stock / quantity<=0 lines -> neither route calls gateway preview.
  const unusableCart = {
    user_id: "buyer-1",
    shop_groups: [
      {
        shop_id: SHOP_A_ID,
        shop_name: "Shop A",
        subtotal_vnd: 200000,
        total_weight_grams: 200,
        items: [
          {
            sku_id: "sku-dead",
            shop_id: SHOP_A_ID,
            price_vnd: 0,
            quantity: 1,
            available_stock: 0,
            weight_grams: 0,
            selected: true,
            unavailable: true,
          },
          {
            sku_id: "sku-oos",
            shop_id: SHOP_A_ID,
            price_vnd: 200000,
            quantity: 1,
            available_stock: 0,
            weight_grams: 200,
            selected: true,
            unavailable: false,
          },
          {
            sku_id: "sku-zero",
            shop_id: SHOP_A_ID,
            price_vnd: 100000,
            quantity: 0,
            available_stock: 5,
            weight_grams: 100,
            selected: true,
            unavailable: false,
          },
        ],
      },
    ],
    total_items: 1,
    total_vnd: 200000,
  };
  const mockUnusable = installGatewayMock({ cart: unusableCart, vouchers: [voucher], skuCatalog });
  try {
    const valRes = await validateRoute.POST(
      makePostReq("/api/vouchers/validate", { code: "SHOP100" }),
      {}
    );
    assert.equal(valRes.status, 200);
    const valData = await valRes.json();
    assert.equal(valData.valid, false);

    const prevRes = await previewRoute.POST(
      makePostReq("/api/vouchers/preview", { shop_voucher_id: "v-100" }),
      {}
    );
    assert.equal(prevRes.status, 200);
    const prevData = await prevRes.json();
    assert.deepEqual(prevData.breakdown.shops, []);
    assert.equal(prevData.breakdown.total_subtotal_vnd, 0);

    assert.equal(
      mockUnusable.previewCalls.length,
      0,
      "must not call /api/v1/promotions/preview when no previewable items remain"
    );
  } finally {
    mockUnusable.restore();
  }
});

test("filterPreviewableCartGroups scopes to site shop, filters unavailable/OOS/qty<=0, keeps partial-stock lines, and recomputes totals", () => {
  assert.equal(isPreviewableCartItem(null), false);
  assert.equal(isPreviewableCartItem(undefined), false);
  assert.equal(
    isPreviewableCartItem({ unavailable: true, available_stock: 10, quantity: 1 }),
    false
  );
  assert.equal(
    isPreviewableCartItem({ unavailable: false, available_stock: 0, quantity: 1 }),
    false
  );
  assert.equal(
    isPreviewableCartItem({ unavailable: false, available_stock: -1, quantity: 1 }),
    false
  );
  assert.equal(
    isPreviewableCartItem({ unavailable: false, available_stock: 5, quantity: 0 }),
    false
  );
  assert.equal(
    isPreviewableCartItem({ unavailable: false, available_stock: 5, quantity: -2 }),
    false
  );
  assert.equal(
    isPreviewableCartItem({ unavailable: false, available_stock: 2, quantity: 5 }),
    true
  );

  const groups = filterPreviewableCartGroups(
    {
      user_id: "buyer-1",
      shop_groups: [
        {
          shop_id: SHOP_A_ID,
          shop_name: "Shop A",
          subtotal_vnd: 999999,
          total_weight_grams: 9999,
          items: [
            {
              sku_id: "sku-ok",
              shop_id: SHOP_A_ID,
              price_vnd: 120000,
              quantity: 2,
              available_stock: 10,
              weight_grams: 250,
            },
            {
              sku_id: "sku-partial-stock",
              shop_id: SHOP_A_ID,
              price_vnd: 50000,
              quantity: 3,
              available_stock: 1,
              weight_grams: 100,
            },
            {
              sku_id: "sku-dead",
              shop_id: SHOP_A_ID,
              price_vnd: 300000,
              quantity: 1,
              available_stock: 5,
              weight_grams: 400,
              unavailable: true,
            },
            {
              sku_id: "sku-oos",
              shop_id: SHOP_A_ID,
              price_vnd: 400000,
              quantity: 1,
              available_stock: 0,
              weight_grams: 500,
            },
            {
              sku_id: "sku-zero",
              shop_id: SHOP_A_ID,
              price_vnd: 80000,
              quantity: 0,
              available_stock: 5,
              weight_grams: 150,
            },
          ],
        },
        {
          shop_id: "shop-b",
          shop_name: "Shop B",
          subtotal_vnd: 500000,
          total_weight_grams: 500,
          items: [
            {
              sku_id: "sku-b",
              shop_id: "shop-b",
              price_vnd: 500000,
              quantity: 1,
              available_stock: 10,
              weight_grams: 500,
            },
          ],
        },
      ],
      total_items: 7,
      total_vnd: 1499999,
    },
    SHOP_A_ID
  );

  assert.equal(groups.length, 1);
  assert.equal(groups[0].shop_id, SHOP_A_ID);
  assert.deepEqual(
    groups[0].items.map((it) => it.sku_id),
    ["sku-ok", "sku-partial-stock"]
  );
  assert.equal(groups[0].subtotal_vnd, 120000 * 2 + 50000 * 3);
  assert.equal(groups[0].total_weight_grams, 250 * 2 + 100 * 3);
  assert.deepEqual(filterPreviewableCartGroups(null, SHOP_A_ID), []);
});

test("Mục 1: /api/vouchers/preview & /validate return rejected_flash_sale_item_ids without leaking English warnings, and 2-SKU line totals match final_payable_vnd", async () => {
  const { evaluateShopFlashLines, resolveDisplayedLinePricing } =
    await import("../../../features/checkout/helpers.ts");

  const voucher = makeVoucher({
    id: "v-shop-a-50",
    code: "SHOP50",
    min_order_vnd: 100000,
    value_vnd: 50000,
  });
  const cart = {
    user_id: "buyer-1",
    shop_groups: [
      {
        shop_id: SHOP_A_ID,
        shop_name: "Shop A",
        subtotal_vnd: 900000,
        total_weight_grams: 500,
        items: [
          {
            sku_id: "sku-A",
            product_id: "p-a",
            shop_id: SHOP_A_ID,
            price_vnd: 500000,
            quantity: 1,
            available_stock: 5,
            weight_grams: 250,
            selected: true,
            unavailable: false,
          },
          {
            sku_id: "sku-B",
            product_id: "p-b",
            shop_id: SHOP_A_ID,
            price_vnd: 400000,
            quantity: 1,
            available_stock: 5,
            weight_grams: 250,
            selected: true,
            unavailable: false,
          },
        ],
      },
    ],
    total_items: 2,
    total_vnd: 900000,
  };
  const skuCatalog = {
    "sku-A": { is_active: true, price_vnd: 500000 },
    "sku-B": { is_active: true, price_vnd: 400000 },
  };

  // Real gateway /api/v1/promotions/preview response when fsi-A hit per-user limit while fsi-B succeeded
  const mock = installGatewayMock({
    cart,
    vouchers: [voucher],
    skuCatalog,
    previewOverrides: (reqBody) => {
      const hasVoucher = Boolean(reqBody?.shop_voucher_ids?.[SHOP_A_ID]);
      const voucherDisc = hasVoucher ? 50000 : 0;
      const finalPayable = 900000 - 200000 - voucherDisc;
      return {
        breakdown: {
          shops: [
            {
              shop_id: SHOP_A_ID,
              subtotal_vnd: 900000,
              flash_sale_discount_vnd: 200000,
              shop_program_discount_vnd: 0,
              shop_voucher_id: hasVoucher ? voucher.id : "",
              shop_voucher_discount_vnd: voucherDisc,
              platform_voucher_discount_vnd: 0,
              freeship_discount_vnd: 0,
              shop_freeship_discount_vnd: 0,
              coins_discount_vnd: 0,
              shipping_fee_vnd: 0,
              final_shipping_fee_vnd: 0,
              final_payable_vnd: finalPayable,
            },
          ],
          total_subtotal_vnd: 900000,
          total_flash_sale_discount_vnd: 200000,
          total_shop_program_discount_vnd: 0,
          total_shop_voucher_discount_vnd: voucherDisc,
          total_platform_voucher_discount_vnd: 0,
          total_freeship_discount_vnd: 0,
          total_shop_freeship_discount_vnd: 0,
          total_coins_discount_vnd: 0,
          total_discount_vnd: 200000 + voucherDisc,
          platform_cost_vnd: 0,
          shop_cost_vnd: 200000 + voucherDisc,
          total_shipping_fee_vnd: 0,
          final_payable_vnd: finalPayable,
          warnings: [
            "flash sale item fsi-A per-user limit (1) exceeded",
            "flash sale item fsi-B takes precedence over shop discount program item spi-B for SKU sku-B",
          ],
        },
      };
    },
  });

  try {
    const prevRes = await previewRoute.POST(
      makePostReq("/api/vouchers/preview", {
        shipping_fee_vnd: 0,
        flash_sale_item_by_sku: { "sku-A": "fsi-A", "sku-B": "fsi-B" },
      }),
      {}
    );
    assert.equal(prevRes.status, 200);
    const prevData = await prevRes.json();
    assert.deepEqual(prevData.rejected_flash_sale_item_ids, ["fsi-A"]);
    // Warnings translated to clean Vietnamese — no English leak, no voucher blame
    assert.match(prevData.breakdown.warnings[0], /Flash sale/i);
    assert.doesNotMatch(prevData.breakdown.warnings[0], /mã ưu đãi|per-user|fsi-A/i);
    assert.doesNotMatch(prevData.breakdown.warnings[1], /takes precedence|fsi-B|spi-B/i);

    // End-to-end 2-SKU evaluation: sku-A flagged for regular price, sku-B stays flash -> line sum == 700,000 VND
    const evals = evaluateShopFlashLines({
      lines: [
        {
          skuId: "sku-A",
          quantity: 1,
          regularUnitPriceVnd: 500000,
          flashSaleItemId: "fsi-A",
          flashUnitPriceVnd: 300000,
          flashPerUserLimit: 1,
        },
        {
          skuId: "sku-B",
          quantity: 1,
          regularUnitPriceVnd: 400000,
          flashSaleItemId: "fsi-B",
          flashUnitPriceVnd: 200000,
          flashPerUserLimit: 1,
        },
      ],
      previewShopFlashDiscountVnd: prevData.breakdown.shops[0].flash_sale_discount_vnd,
      previewWarnings: prevData.breakdown.warnings,
      rejectedFlashSaleItemIds: prevData.rejected_flash_sale_item_ids,
    });
    assert.equal(evals["sku-A"]?.needsRegularPriceChoice, true);
    assert.equal(evals["sku-B"]?.needsRegularPriceChoice, false);
    assert.equal(evals["sku-B"]?.ambiguousShopShortfall, false);

    const priceA = resolveDisplayedLinePricing({
      regularUnitPriceVnd: 500000,
      quantity: 1,
      isFlashLine: true,
      flashUnitPriceVnd: 300000,
      flashShortfall: Boolean(evals["sku-A"]?.needsRegularPriceChoice),
    });
    const priceB = resolveDisplayedLinePricing({
      regularUnitPriceVnd: 400000,
      quantity: 1,
      isFlashLine: true,
      flashUnitPriceVnd: 200000,
      flashShortfall: Boolean(evals["sku-B"]?.needsRegularPriceChoice),
    });
    assert.equal(priceA.lineTotalVnd, 500000);
    assert.equal(priceB.lineTotalVnd, 200000);
    assert.equal(priceA.lineTotalVnd + priceB.lineTotalVnd, prevData.breakdown.final_payable_vnd);

    // Validate route also returns rejected_flash_sale_item_ids without English leakage
    const valRes = await validateRoute.POST(
      makePostReq("/api/vouchers/validate", {
        code: "SHOP50",
        shipping_fee_vnd: 0,
        flash_sale_item_by_sku: { "sku-A": "fsi-A", "sku-B": "fsi-B" },
      }),
      {}
    );
    assert.equal(valRes.status, 200);
    const valData = await valRes.json();
    assert.equal(valData.valid, true);
    assert.deepEqual(valData.rejected_flash_sale_item_ids, ["fsi-A"]);
    assert.doesNotMatch(valData.breakdown.warnings[0], /mã ưu đãi|per-user|fsi-A/i);
  } finally {
    mock.restore();
  }
});
