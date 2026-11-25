import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { registerHooks } from "node:module";
import { dirname, join, resolve } from "node:path";
import { describe, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const WEB_ROOT = resolve(__dirname, "..");
const WEB_SRC = resolve(WEB_ROOT, "src");

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.startsWith("@/")) {
      const rel = specifier.slice(2);
      const tsPath = resolve(WEB_SRC, `${rel}.ts`);
      if (existsSync(tsPath)) {
        return nextResolve(pathToFileURL(tsPath).href, context);
      }
      const indexTsPath = resolve(WEB_SRC, rel, "index.ts");
      if (existsSync(indexTsPath)) {
        return nextResolve(pathToFileURL(indexTsPath).href, context);
      }
    }
    if (
      (specifier.startsWith("./") || specifier.startsWith("../")) &&
      !/\.[a-z]+$/i.test(specifier) &&
      context.parentURL
    ) {
      const parentDir = dirname(fileURLToPath(context.parentURL));
      const tsPath = resolve(parentDir, `${specifier}.ts`);
      if (existsSync(tsPath)) {
        return nextResolve(pathToFileURL(tsPath).href, context);
      }
      const indexTsPath = resolve(parentDir, specifier, "index.ts");
      if (existsSync(indexTsPath)) {
        return nextResolve(pathToFileURL(indexTsPath).href, context);
      }
    }
    return nextResolve(specifier, context);
  },
});

const apiMod = await import("../src/lib/api.ts");
const catalogMapperMod = await import("../src/lib/catalog-mapper.ts");
const checkoutHelpersMod = await import("../src/features/checkout/helpers.ts");
const checkoutMessagesMod = await import("../src/features/checkout/messages.ts");
const sellerTypesMod = await import("../src/features/seller/types.ts");
const adminTypesMod = await import("../src/features/admin/types.ts");
const errorMapMod = await import("../src/lib/server/error-map.ts");

function makeProduct(idx, overrides = {}) {
  const id = `prod-${idx}`;
  const skuId = `sku-${idx}`;
  return {
    id,
    shop_id: overrides.shop_id ?? "shop-1",
    category_id: "cat-1",
    name: `Áo thun Cotton #${idx}`,
    slug: `ao-thun-cotton-${idx}`,
    description: "Sản phẩm thử nghiệm",
    images: ["/products/ao-thun-cotton-trang.png"],
    status: "APPROVED",
    rejection_reason: "",
    min_price_vnd: 200_000,
    max_price_vnd: 200_000,
    skus: [
      {
        id: skuId,
        product_id: id,
        shop_id: overrides.shop_id ?? "shop-1",
        product_name: `Áo thun Cotton #${idx}`,
        sku_code: `SKU-${idx}`,
        name: "Trắng / M",
        attributes: { color: "Trắng", size: "M" },
        price_vnd: 200_000,
        stock: 30,
        reserved_stock: 0,
        available_stock: 30,
        version: 1,
        weight_grams: 250,
        image_url: "/products/ao-thun-cotton-trang.png",
        category_id: "cat-1",
        is_active: true,
      },
    ],
    ...overrides,
  };
}

function collectFilesRecursive(dir, exts) {
  const out = [];
  if (!existsSync(dir)) return out;
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    const st = statSync(full);
    if (st.isDirectory()) {
      out.push(...collectFilesRecursive(full, exts));
    } else if (exts.some((ext) => full.endsWith(ext))) {
      out.push(full);
    }
  }
  return out;
}

describe("Wave 3a web requirements — unit tests", () => {
  test("1. D8/A3: Flash sale product lookup chunks <= 100 sku_ids, merges products past top 100, and drops inactive/non-public items from count", async () => {
    assert.equal(
      typeof catalogMapperMod.collectFlashSaleSkuIds,
      "function",
      "collectFlashSaleSkuIds must be exported"
    );
    assert.equal(typeof catalogMapperMod.chunkSkuIds, "function", "chunkSkuIds must be exported");
    assert.equal(
      typeof catalogMapperMod.fetchProductsForFlashSalesWithFetcher,
      "function",
      "fetchProductsForFlashSalesWithFetcher must be exported"
    );

    // 100 base products (idx 1..100); flash sale targets SKUs 101..225 (125 missing SKUs > 100 batch size)
    // plus SKU 50 (already in base 100) and SKU 999 (hidden/deleted -> absent from API) and SKU 998 (inactive SKU).
    const base100 = Array.from({ length: 100 }, (_, i) => makeProduct(i + 1));
    const nowMs = Date.parse("2026-11-24T10:00:00Z");
    const rawItems = [];
    rawItems.push({
      id: "fsi-50",
      flash_sale_id: "fs-live",
      shop_id: "shop-1",
      sku_id: "sku-50",
      flash_price_vnd: 149_000,
      original_price_vnd: 200_000,
      total_slots: 10,
      remaining_slots: 8,
      per_user_limit: 2,
      status: "APPROVED",
      rejection_reason: "",
    });
    for (let i = 101; i <= 225; i++) {
      rawItems.push({
        id: `fsi-${i}`,
        flash_sale_id: "fs-live",
        shop_id: "shop-1",
        sku_id: `sku-${i}`,
        flash_price_vnd: 139_000,
        original_price_vnd: 200_000,
        total_slots: 10,
        remaining_slots: 5,
        per_user_limit: 2,
        status: "APPROVED",
        rejection_reason: "",
      });
    }
    // Hidden/deleted product (will NOT be returned by GET /products?sku_ids=)
    rawItems.push({
      id: "fsi-hidden",
      flash_sale_id: "fs-live",
      shop_id: "shop-1",
      sku_id: "sku-999",
      flash_price_vnd: 129_000,
      original_price_vnd: 200_000,
      total_slots: 10,
      remaining_slots: 5,
      per_user_limit: 2,
      status: "APPROVED",
      rejection_reason: "",
    });
    // Product with all SKUs inactive (is_active = false)
    rawItems.push({
      id: "fsi-inactive-sku",
      flash_sale_id: "fs-live",
      shop_id: "shop-1",
      sku_id: "sku-998",
      flash_price_vnd: 129_000,
      original_price_vnd: 200_000,
      total_slots: 10,
      remaining_slots: 5,
      per_user_limit: 2,
      status: "APPROVED",
      rejection_reason: "",
    });
    // Non-approved flash item must be excluded
    rawItems.push({
      id: "fsi-pending",
      flash_sale_id: "fs-live",
      shop_id: "shop-1",
      sku_id: "sku-997",
      flash_price_vnd: 129_000,
      original_price_vnd: 200_000,
      total_slots: 10,
      remaining_slots: 5,
      per_user_limit: 2,
      status: "PENDING",
      rejection_reason: "",
    });

    const rawFlashSales = [
      {
        id: "fs-live",
        name: "Flash 10h",
        starts_at: "2026-11-24T09:00:00Z",
        ends_at: "2026-11-24T12:00:00Z",
        items: rawItems,
      },
    ];

    const collected = catalogMapperMod.collectFlashSaleSkuIds(rawFlashSales);
    assert.ok(!collected.includes("sku-997"), "PENDING flash item must not be collected");
    assert.equal(collected.length, 128, "expected 1 + 125 + 2 approved flash SKU IDs");

    const fetchedBatches = [];
    const mergedRaw = await catalogMapperMod.fetchProductsForFlashSalesWithFetcher(
      rawFlashSales,
      base100,
      async (batchSkuIds) => {
        fetchedBatches.push(batchSkuIds);
        assert.ok(batchSkuIds.length <= 100, `batch exceeded 100 SKUs: ${batchSkuIds.length}`);
        const out = [];
        for (const skuId of batchSkuIds) {
          if (skuId === "sku-999") continue; // hidden/deleted product omitted by API
          if (skuId === "sku-998") {
            const p = makeProduct(998);
            p.skus[0].is_active = false;
            out.push(p);
            continue;
          }
          const num = Number(skuId.replace("sku-", ""));
          if (Number.isFinite(num)) {
            out.push(makeProduct(num));
          }
        }
        return out;
      }
    );

    assert.equal(
      fetchedBatches.length,
      2,
      "127 missing SKUs must split into 2 batches (<= 100 each)"
    );
    assert.equal(fetchedBatches[0].length, 100);
    assert.equal(fetchedBatches[1].length, 27);
    assert.ok(
      !fetchedBatches.flat().includes("sku-50"),
      "SKU already present in base 100 products should not be re-fetched"
    );

    const mappedProducts = mergedRaw.map((p) =>
      catalogMapperMod.mapGatewayProduct(p, {
        categoriesById: new Map(),
        shopsById: new Map(),
      })
    );
    const slots = catalogMapperMod.mapGatewayFlashSales(rawFlashSales, mappedProducts, nowMs);
    assert.equal(slots.length, 1);
    assert.equal(
      slots[0].items.length,
      126,
      "displayed flash items must equal 126 sellable API items (1 in top-100 + 125 fetched via sku_ids; hidden & inactive-SKU dropped and uncounted)"
    );
    assert.ok(
      !slots[0].items.some((it) => it.skuId === "sku-999" || it.skuId === "sku-998"),
      "hidden product and inactive SKU must not appear in slot items"
    );

    // Also verify catalog.ts & thanh-toan/page.tsx wire sku_ids
    const catalogSrc = readFileSync(resolve(WEB_SRC, "lib/catalog.ts"), "utf8");
    assert.ok(catalogSrc.includes("sku_ids"), "catalog.ts must query GET /products?sku_ids=");
    assert.ok(
      catalogSrc.includes("fetchProductsForFlashSalesWithFetcher"),
      "catalog.ts must use fetchProductsForFlashSalesWithFetcher for home & flash-sale loaders"
    );
    const checkoutPageSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/page.tsx"),
      "utf8"
    );
    assert.ok(
      checkoutPageSrc.includes("sku_ids"),
      "thanh-toan/page.tsx Buy Now must look up SKU via /products?sku_ids="
    );
  });

  test("2. P2-3 #3: Manual voucher code probes PLATFORM, FREESHIP, and SHOP scopes with scope-specific errors and zero hidden-code leakage", async () => {
    assert.equal(
      typeof checkoutHelpersMod.resolveManualVoucherCode,
      "function",
      "resolveManualVoucherCode must be exported"
    );
    assert.equal(
      typeof checkoutHelpersMod.GENERIC_UNKNOWN_VOUCHER_MESSAGE,
      "string",
      "GENERIC_UNKNOWN_VOUCHER_MESSAGE must be exported"
    );

    const shops = [
      { shopId: "shop-a", shopName: "Xưởng A", subtotalVnd: 200_000, shippingFeeVnd: 25_000 },
      { shopId: "shop-b", shopName: "Xưởng B", subtotalVnd: 150_000, shippingFeeVnd: 20_000 },
    ];

    const emptyBreakdown = (overrides = {}) => ({
      shops: shops.map((s) => ({
        shop_id: s.shopId,
        subtotal_vnd: s.subtotalVnd,
        flash_sale_discount_vnd: 0,
        shop_voucher_id: "",
        shop_voucher_discount_vnd: 0,
        platform_voucher_discount_vnd: 0,
        freeship_discount_vnd: 0,
        coins_discount_vnd: 0,
        shipping_fee_vnd: s.shippingFeeVnd,
        final_shipping_fee_vnd: s.shippingFeeVnd,
        final_payable_vnd: s.subtotalVnd + s.shippingFeeVnd,
      })),
      total_subtotal_vnd: 350_000,
      total_flash_sale_discount_vnd: 0,
      total_shop_voucher_discount_vnd: 0,
      total_platform_voucher_discount_vnd: 0,
      total_freeship_discount_vnd: 0,
      total_coins_discount_vnd: 0,
      total_discount_vnd: 0,
      platform_cost_vnd: 0,
      shop_cost_vnd: 0,
      total_shipping_fee_vnd: 45_000,
      final_payable_vnd: 395_000,
      warnings: [],
      ...overrides,
    });

    // 2a. Unlisted FREESHIP code: platform probe gets scope mismatch -> freeship probe succeeds
    const freeshipCalls = [];
    const freeshipRes = await checkoutHelpersMod.resolveManualVoucherCode({
      rawCode: "shipvip30",
      knownVouchers: [],
      shops,
      hasShippingEstimate: true,
      previewFn: async (probe) => {
        freeshipCalls.push(probe);
        if (probe.platformVoucherId === "SHIPVIP30") {
          return emptyBreakdown({
            warnings: [
              "shop discount program item p1 per-user limit exceeded",
              "voucher SHIPVIP30 scope FREESHIP does not match required scope PLATFORM",
            ],
          });
        }
        if (probe.freeshipVoucherId === "SHIPVIP30") {
          return emptyBreakdown({ total_freeship_discount_vnd: 25_000 });
        }
        return emptyBreakdown();
      },
    });
    assert.equal(freeshipRes.ok, true);
    assert.equal(freeshipRes.scope, "FREESHIP");
    assert.equal(freeshipRes.code, "SHIPVIP30");
    assert.equal(freeshipCalls.length, 2, "should stop immediately once FREESHIP probe applies");

    // 2b. Unlisted SHOP code belonging to shop-b: platform probe sees SHOP -> shop probe matches shop-b
    const shopRes = await checkoutHelpersMod.resolveManualVoucherCode({
      rawCode: "SHOPB20",
      knownVouchers: [],
      shops,
      hasShippingEstimate: true,
      previewFn: async (probe) => {
        if (probe.platformVoucherId === "SHOPB20") {
          return emptyBreakdown({
            warnings: ["voucher SHOPB20 scope SHOP does not match required scope PLATFORM"],
          });
        }
        if (probe.shopVoucherIds?.["shop-b"] === "SHOPB20") {
          const bd = emptyBreakdown({
            total_shop_voucher_discount_vnd: 20_000,
            warnings: ["voucher SHOPB20 does not belong to shop shop-a"],
          });
          bd.shops[1].shop_voucher_id = "v-shop-b";
          bd.shops[1].shop_voucher_discount_vnd = 20_000;
          return bd;
        }
        return emptyBreakdown();
      },
    });
    assert.equal(shopRes.ok, true);
    assert.equal(shopRes.scope, "SHOP");
    assert.equal(shopRes.shopId, "shop-b");
    assert.equal(shopRes.code, "SHOPB20");

    // 2c. Scope-specific eligibility errors: PLATFORM category mismatch, FREESHIP min order, SHOP min order
    const platCatFail = await checkoutHelpersMod.resolveManualVoucherCode({
      rawCode: "PLATCAT",
      knownVouchers: [],
      shops,
      hasShippingEstimate: true,
      previewFn: async () =>
        emptyBreakdown({
          warnings: ["voucher PLATCAT does not apply to any items in the selected categories"],
        }),
    });
    assert.equal(platCatFail.ok, false);
    assert.equal(platCatFail.scope, "PLATFORM");
    assert.match(platCatFail.message, /toàn sàn/i);
    assert.match(platCatFail.message, /danh mục/i);

    const freeshipMinFail = await checkoutHelpersMod.resolveManualVoucherCode({
      rawCode: "SHIPMIN",
      knownVouchers: [],
      shops,
      hasShippingEstimate: true,
      previewFn: async (probe) => {
        if (probe.platformVoucherId) {
          return emptyBreakdown({
            warnings: ["voucher SHIPMIN scope FREESHIP does not match required scope PLATFORM"],
          });
        }
        return emptyBreakdown({
          warnings: ["voucher SHIPMIN requires minimum order 500000 VND (shortfall 150000 VND)"],
        });
      },
    });
    assert.equal(freeshipMinFail.ok, false);
    assert.equal(freeshipMinFail.scope, "FREESHIP");
    assert.match(freeshipMinFail.message, /vận chuyển/i);
    assert.match(freeshipMinFail.message, /500\.000/);

    const shopMinFail = await checkoutHelpersMod.resolveManualVoucherCode({
      rawCode: "SHOPAMIN",
      knownVouchers: [],
      shops,
      hasShippingEstimate: true,
      previewFn: async (probe) => {
        if (probe.platformVoucherId) {
          return emptyBreakdown({
            warnings: ["voucher SHOPAMIN scope SHOP does not match required scope PLATFORM"],
          });
        }
        return emptyBreakdown({
          warnings: [
            "voucher SHOPAMIN requires minimum order 400000 VND (shortfall 200000 VND)",
            "voucher SHOPAMIN does not belong to shop shop-b",
          ],
        });
      },
    });
    assert.equal(shopMinFail.ok, false);
    assert.equal(shopMinFail.scope, "SHOP");
    assert.match(shopMinFail.message, /gian hàng/i);
    assert.match(shopMinFail.message, /400\.000/);

    // 2d. Anti-enumeration: hidden/paused/deleted/not-started/other-shop/not-found MUST all return identical generic error
    const secretProbes = [
      { code: "NOEXIST99", platWarn: "voucher NOEXIST99 not found" },
      { code: "HIDDENPAUSE", platWarn: "voucher HIDDENPAUSE is paused" },
      { code: "HIDDENDEL", platWarn: "voucher HIDDENDEL has been deleted" },
      { code: "HIDDENFUTURE", platWarn: "voucher HIDDENFUTURE has not started yet" },
      {
        code: "OTHERSHOP",
        platWarn: "voucher OTHERSHOP scope SHOP does not match required scope PLATFORM",
        shopWarns: [
          "voucher OTHERSHOP does not belong to shop shop-a",
          "voucher OTHERSHOP does not belong to shop shop-b",
        ],
      },
    ];
    for (const tc of secretProbes) {
      const res = await checkoutHelpersMod.resolveManualVoucherCode({
        rawCode: tc.code,
        knownVouchers: [],
        shops,
        hasShippingEstimate: true,
        previewFn: async (probe) => {
          if (probe.platformVoucherId) {
            return emptyBreakdown({ warnings: [tc.platWarn] });
          }
          return emptyBreakdown({ warnings: tc.shopWarns ?? [tc.platWarn] });
        },
      });
      assert.equal(res.ok, false, `${tc.code} must fail`);
      assert.equal(
        res.message,
        checkoutHelpersMod.GENERIC_UNKNOWN_VOUCHER_MESSAGE,
        `hidden/unknown code ${tc.code} must return identical generic message without leaking existence/status/scope`
      );
    }

    // Verify Cart and Checkout both wire resolveManualVoucherCode
    const cartSrc = readFileSync(resolve(WEB_SRC, "app/(shop)/gio-hang/page.tsx"), "utf8");
    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    assert.ok(cartSrc.includes("resolveManualVoucherCode"));
    assert.ok(checkoutSrc.includes("resolveManualVoucherCode"));
  });

  test("3. P3-1 & P3-2: Header suggestions display live flash/shop-discount prices & PDP similarProducts fetches discounts across all shops", () => {
    assert.equal(
      typeof catalogMapperMod.computeProductDisplayPricing,
      "function",
      "computeProductDisplayPricing must be exported"
    );
    assert.equal(
      typeof catalogMapperMod.collectProductDetailDiscountSkuIds,
      "function",
      "collectProductDetailDiscountSkuIds must be exported"
    );

    const p1 = makeProduct(1, { shop_id: "shop-a" });
    const p2 = makeProduct(2, { shop_id: "shop-b" });
    p1.skus.push({ ...p1.skus[0], id: "sku-1-xl", price_vnd: 240_000 });

    const flashMap = new Map([
      [
        "sku-1",
        {
          id: "fi-1",
          flash_sale_id: "fs-1",
          shop_id: "shop-a",
          sku_id: "sku-1",
          flash_price_vnd: 139_000,
          original_price_vnd: 200_000,
          total_slots: 10,
          remaining_slots: 4,
          per_user_limit: 2,
          status: "APPROVED",
          rejection_reason: "",
        },
      ],
    ]);
    const shopDiscMap = new Map([
      [
        "sku-1",
        {
          item_id: "sd-1",
          program_id: "sp-1",
          shop_id: "shop-a",
          sku_id: "sku-1",
          original_price_vnd: 200_000,
          discounted_price_vnd: 165_000,
          discount_percent: 18,
          per_user_limit: 2,
        },
      ],
      [
        "sku-2",
        {
          item_id: "sd-2",
          program_id: "sp-2",
          shop_id: "shop-b",
          sku_id: "sku-2",
          original_price_vnd: 200_000,
          discounted_price_vnd: 160_000,
          discount_percent: 20,
          per_user_limit: 2,
        },
      ],
    ]);

    // P3-1: Flash beats shop discount on p1 (139k vs 200k orig, max 240k); shop discount applies on p2 (160k vs 200k)
    const p1Pricing = catalogMapperMod.computeProductDisplayPricing(p1, flashMap, shopDiscMap);
    assert.equal(p1Pricing.minPriceVnd, 139_000);
    assert.equal(p1Pricing.maxPriceVnd, 240_000);
    assert.equal(p1Pricing.originalPriceVnd, 200_000);
    assert.equal(p1Pricing.promoKind, "flash");

    const p2Pricing = catalogMapperMod.computeProductDisplayPricing(p2, flashMap, shopDiscMap);
    assert.equal(p2Pricing.minPriceVnd, 160_000);
    assert.equal(p2Pricing.maxPriceVnd, 160_000);
    assert.equal(p2Pricing.originalPriceVnd, 200_000);
    assert.equal(p2Pricing.promoKind, "shop_discount");

    // Verify ShopHeader uses computeSearchHitDisplayPricing, fetches flash-sales + shop-discounts/active, and guards stale responses via searchReqSeq
    const headerSrc = readFileSync(resolve(WEB_SRC, "components/ShopHeader.tsx"), "utf8");
    assert.ok(headerSrc.includes("computeSearchHitDisplayPricing"));
    assert.ok(headerSrc.includes("flash-sales"));
    assert.ok(headerSrc.includes("shop-discounts/active"));
    assert.ok(
      headerSrc.includes("searchReqSeq"),
      "ShopHeader must guard suggest requests with seq cancellation"
    );

    // P3-2: collectProductDetailDiscountSkuIds includes SKUs from main product, sameShop, AND other-shop similar products
    const otherShopProd = makeProduct(3, { shop_id: "shop-c" });
    const detailSkuIds = catalogMapperMod.collectProductDetailDiscountSkuIds(
      p1,
      [p1],
      [p1, p2, otherShopProd]
    );
    assert.ok(detailSkuIds.includes("sku-1"));
    assert.ok(detailSkuIds.includes("sku-1-xl"));
    assert.ok(detailSkuIds.includes("sku-2"), "must include SKU from shop-b");
    assert.ok(detailSkuIds.includes("sku-3"), "must include SKU from shop-c");

    const catalogSrc = readFileSync(resolve(WEB_SRC, "lib/catalog.ts"), "utf8");
    assert.ok(
      catalogSrc.includes("collectProductDetailDiscountSkuIds"),
      "loadProductDetailData must collect SKU IDs across all candidate shops for similarProducts discounts"
    );
  });

  test("4. D1: Admin fees/categories use /admin/categories; no (shop) file reads commission; seller category select tolerates omitted bps", () => {
    const adminCatSrc = readFileSync(
      resolve(WEB_SRC, "features/admin/AdminCategoriesPanel.tsx"),
      "utf8"
    );
    const adminFeesSrc = readFileSync(
      resolve(WEB_SRC, "features/admin/AdminFeesPanel.tsx"),
      "utf8"
    );

    assert.ok(
      adminCatSrc.includes('"admin/categories"') || adminCatSrc.includes("`admin/categories"),
      "AdminCategoriesPanel must load from /admin/categories"
    );
    assert.ok(
      !/api<[^>]+>\(\s*["']categories["']/.test(adminCatSrc),
      "AdminCategoriesPanel must not call public /categories"
    );

    assert.ok(
      adminFeesSrc.includes('"admin/categories"') || adminFeesSrc.includes("`admin/categories"),
      "AdminFeesPanel must load from /admin/categories"
    );
    assert.ok(
      !/api<[^>]+>\(\s*["']categories["']/.test(adminFeesSrc),
      "AdminFeesPanel must not call public /categories"
    );

    // No file under app/(shop) or shop-facing components/catalog mapper should read commission fields
    const shopFiles = [
      ...collectFilesRecursive(resolve(WEB_SRC, "app/(shop)"), [".ts", ".tsx"]),
      ...collectFilesRecursive(resolve(WEB_SRC, "components"), [".ts", ".tsx"]),
      ...collectFilesRecursive(resolve(WEB_SRC, "features/catalog"), [".ts", ".tsx"]),
      ...collectFilesRecursive(resolve(WEB_SRC, "features/product"), [".ts", ".tsx"]),
      ...collectFilesRecursive(resolve(WEB_SRC, "features/checkout"), [".ts", ".tsx"]),
      ...collectFilesRecursive(resolve(WEB_SRC, "features/commerce"), [".ts", ".tsx"]),
      resolve(WEB_SRC, "lib/catalog.ts"),
      resolve(WEB_SRC, "lib/catalog-mapper.ts"),
      resolve(WEB_SRC, "lib/catalog-types.ts"),
    ];
    for (const file of shopFiles) {
      const src = readFileSync(file, "utf8");
      assert.ok(
        !/commission_rate_bps|commission_bps|commission_vnd|commissionRate/.test(src),
        `${file} must not reference any commission field`
      );
    }

    // CatalogShop mapped from public shop must not carry commissionRate
    const mappedShop = catalogMapperMod.mapGatewayShop({
      id: "shop-1",
      name: "Xưởng Dệt Bạch Đằng",
      slug: "xuong-det-bach-dang",
      description: "Xưởng dệt",
      status: "ACTIVE",
    });
    assert.equal(
      "commissionRate" in mappedShop,
      false,
      "CatalogShop must not carry commissionRate"
    );

    // ProductFormView must not render NaN% when public /categories omits commission_rate_bps
    const productFormSrc = readFileSync(
      resolve(WEB_SRC, "features/seller/ProductFormView.tsx"),
      "utf8"
    );
    assert.ok(
      productFormSrc.includes('typeof cat.commission_rate_bps === "number"'),
      "ProductFormView must guard optional cat.commission_rate_bps"
    );
  });

  test("5. D6: Admin bank review requires bank_account_id and 409 conflict detection normalizes OWNER_NAME_CHANGED & stale bank row", () => {
    assert.equal(
      typeof adminTypesMod.isAdminReviewConflictError,
      "function",
      "isAdminReviewConflictError must be exported"
    );
    assert.equal(
      typeof adminTypesMod.buildAdminBankReviewPayload,
      "function",
      "buildAdminBankReviewPayload must be exported"
    );

    const bankPayload = adminTypesMod.buildAdminBankReviewPayload({
      bankAccountId: "ba-123",
      decision: "APPROVE",
      reason: "",
    });
    assert.equal(bankPayload.bank_account_id, "ba-123");
    assert.equal(bankPayload.decision, "APPROVE");
    assert.throws(
      () =>
        adminTypesMod.buildAdminBankReviewPayload({
          bankAccountId: "  ",
          decision: "APPROVE",
          reason: "",
        }),
      /bank_account_id/i
    );

    const ownerChangedSanitized = errorMapMod.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "OWNER_NAME_CHANGED: shop owner full_name has changed since review started",
      },
    });
    assert.equal(ownerChangedSanitized.code, "OWNER_NAME_CHANGED");
    assert.match(ownerChangedSanitized.message, /dữ liệu đã thay đổi/i);

    const staleBankSanitized = errorMapMod.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "only PENDING_ADMIN_REVIEW bank accounts can be reviewed",
      },
    });
    assert.match(staleBankSanitized.message, /dữ liệu đã thay đổi/i);
    assert.equal(
      adminTypesMod.isAdminReviewConflictError(
        new apiMod.ApiError(409, "OWNER_NAME_CHANGED", ownerChangedSanitized.message)
      ),
      true
    );
    assert.equal(
      adminTypesMod.isAdminReviewConflictError(
        new apiMod.ApiError(409, "FAILED_PRECONDITION", staleBankSanitized.message)
      ),
      true
    );
    assert.equal(
      adminTypesMod.isAdminReviewConflictError(new apiMod.ApiError(400, "INVALID_ARGUMENT", "bad")),
      false
    );
  });

  test("7. D6-a: Clicking APPROVE without displayed owner sends 0 requests; REJECT/REQUEST_INFO work without owner name", async () => {
    assert.equal(typeof adminTypesMod.canApproveShop, "function");
    assert.equal(typeof adminTypesMod.buildAdminShopReviewPayload, "function");
    assert.equal(typeof adminTypesMod.submitAdminShopReviewDecision, "function");

    assert.equal(adminTypesMod.canApproveShop("PENDING", undefined), false);
    assert.equal(adminTypesMod.canApproveShop("PENDING", { loading: true, user: null }), false);
    assert.equal(
      adminTypesMod.canApproveShop("PENDING", { loading: false, user: null, error: "network" }),
      false
    );
    assert.equal(
      adminTypesMod.canApproveShop("PENDING", {
        loading: false,
        user: {
          id: "u-1",
          full_name: "   ",
          email: "a@test",
          phone: "",
          role: "buyer",
          status: "ACTIVE",
          shop_id: "",
          phone_verified: false,
          cod_rejected_count: 0,
          avatar_url: "",
        },
      }),
      false
    );
    assert.equal(
      adminTypesMod.canApproveShop("PENDING", {
        loading: false,
        user: {
          id: "u-1",
          full_name: "Nguyễn Văn A",
          email: "a@test",
          phone: "",
          role: "buyer",
          status: "ACTIVE",
          shop_id: "",
          phone_verified: false,
          cod_rejected_count: 0,
          avatar_url: "",
        },
      }),
      true
    );
    assert.equal(
      adminTypesMod.canApproveShop("ACTIVE", {
        loading: false,
        user: {
          id: "u-1",
          full_name: "Nguyễn Văn A",
          email: "a@test",
          phone: "",
          role: "buyer",
          status: "ACTIVE",
          shop_id: "",
          phone_verified: false,
          cod_rejected_count: 0,
          avatar_url: "",
        },
      }),
      false
    );

    assert.throws(
      () =>
        adminTypesMod.buildAdminShopReviewPayload({
          decision: "APPROVE",
          reason: "",
          expectedOwnerName: "  ",
        }),
      /expected_owner_name/i,
      "APPROVE payload builder must refuse empty expected_owner_name"
    );

    const rejectPayload = adminTypesMod.buildAdminShopReviewPayload({
      decision: "REJECT",
      reason: "Thiếu giấy phép",
      expectedOwnerName: "",
    });
    assert.equal(rejectPayload.decision, "REJECT");
    assert.equal(rejectPayload.expected_owner_name, "");

    const reqInfoPayload = adminTypesMod.buildAdminShopReviewPayload({
      decision: "REQUEST_INFO",
      reason: "Bổ sung ảnh GPKD",
      expectedOwnerName: "",
    });
    assert.equal(reqInfoPayload.decision, "REQUEST_INFO");
    assert.equal(reqInfoPayload.expected_owner_name, "");

    let postCalls = 0;
    let reloadCalls = 0;
    const blockedRes = await adminTypesMod.submitAdminShopReviewDecision({
      shopId: "shop-missing-owner",
      decision: "APPROVE",
      reason: "",
      displayedOwnerName: "",
      postReview: async () => {
        postCalls += 1;
        return {};
      },
      onConflictReload: async () => {
        reloadCalls += 1;
      },
    });
    assert.equal(blockedRes.status, "blocked_missing_owner");
    assert.equal(postCalls, 0, "clicking APPROVE without displayed owner must send 0 requests");
    assert.equal(reloadCalls, 0);
  });

  test("8. D6-b: APPROVE sends exact displayed owner name even when server changed name mid-review (409 -> reload -> admin re-clicks)", async () => {
    let serverOwnerName = "Nguyễn Văn Cũ";
    const ownerLookupPaths = [];
    const mockFetchOwnerByQuery = async (path) => {
      ownerLookupPaths.push(path);
      return {
        items: [
          {
            id: "owner-42",
            full_name: serverOwnerName,
            email: "owner42@test",
            phone: "",
            role: "buyer",
            status: "ACTIVE",
            shop_id: "",
            phone_verified: false,
            cod_rejected_count: 0,
            avatar_url: "",
          },
        ],
        page: 1,
        page_size: 1,
        total: 1,
      };
    };

    const initialOwner = await adminTypesMod.fetchShopOwnerById("owner-42", mockFetchOwnerByQuery);
    assert.equal(initialOwner?.full_name, "Nguyễn Văn Cũ");
    let displayedOwnerName = initialOwner.full_name;

    // Owner updates name on server while admin still sees "Nguyễn Văn Cũ" on the panel
    serverOwnerName = "Nguyễn Văn Mới";

    const reviewRequests = [];
    const mockPostReview = async (path, payload) => {
      reviewRequests.push({ path, payload });
      if (payload.expected_owner_name !== serverOwnerName) {
        const sanitized = errorMapMod.sanitizeGatewayError(409, {
          error: {
            code: "FAILED_PRECONDITION",
            message: "OWNER_NAME_CHANGED: shop owner full_name has changed since review started",
          },
        });
        throw new apiMod.ApiError(409, sanitized.code, sanitized.message);
      }
      return {
        shop: {
          id: "shop-42",
          owner_user_id: "owner-42",
          name: "Xưởng Test",
          slug: "xuong-test",
          description: "",
          pickup_address_id: "addr-1",
          contact_phone: "0900000000",
          business_license_doc: "GPKD-1",
          status: "ACTIVE",
          rejection_reason: "",
        },
      };
    };

    const firstAttempt = await adminTypesMod.submitAdminShopReviewDecision({
      shopId: "shop-42",
      decision: "APPROVE",
      reason: "",
      displayedOwnerName,
      postReview: mockPostReview,
      onConflictReload: async () => {
        const refreshed = await adminTypesMod.fetchShopOwnerById("owner-42", mockFetchOwnerByQuery);
        displayedOwnerName = refreshed?.full_name ?? "";
      },
    });
    assert.equal(firstAttempt.status, "conflict");
    assert.match(firstAttempt.message, /dữ liệu đã thay đổi/i);
    assert.equal(reviewRequests.length, 1, "409 conflict must not auto-retry");
    assert.equal(
      reviewRequests[0].payload.expected_owner_name,
      "Nguyễn Văn Cũ",
      "must send exact name displayed on panel, not re-fetched server name"
    );
    assert.equal(
      displayedOwnerName,
      "Nguyễn Văn Mới",
      "409 conflict reload must refresh displayed owner name to latest server value"
    );

    // Admin inspects new name and clicks APPROVE again -> succeeds
    const secondAttempt = await adminTypesMod.submitAdminShopReviewDecision({
      shopId: "shop-42",
      decision: "APPROVE",
      reason: "",
      displayedOwnerName,
      postReview: mockPostReview,
      onConflictReload: async () => {},
    });
    assert.equal(secondAttempt.status, "ok");
    assert.equal(reviewRequests.length, 2);
    assert.equal(reviewRequests[1].payload.expected_owner_name, "Nguyễn Văn Mới");
    assert.deepEqual(ownerLookupPaths, [
      "admin/users?query=owner-42&page=1&page_size=1",
      "admin/users?query=owner-42&page=1&page_size=1",
    ]);
  });

  test("9. D6-c: Single-owner lookup uses admin/users?query=<id>&page=1&page_size=1, checks exact item.id, and never calls admin/users without query", async () => {
    assert.equal(
      adminTypesMod.fetchShopOwnersById,
      undefined,
      "old paginated fetchShopOwnersById (10x100 users) must be removed"
    );
    assert.equal(typeof adminTypesMod.fetchShopOwnerById, "function");

    const requestedPaths = [];
    const mismatchedOwner = await adminTypesMod.fetchShopOwnerById("owner-42", async (path) => {
      requestedPaths.push(path);
      return {
        items: [
          {
            id: "other-user-999",
            full_name: "Người Khác Trùng Từ Khóa",
            email: "other@test",
            phone: "",
            role: "buyer",
            status: "ACTIVE",
            shop_id: "",
            phone_verified: false,
            cod_rejected_count: 0,
            avatar_url: "",
          },
        ],
        page: 1,
        page_size: 1,
        total: 1,
      };
    });
    assert.deepEqual(requestedPaths, ["admin/users?query=owner-42&page=1&page_size=1"]);
    assert.equal(
      mismatchedOwner,
      null,
      "fetchShopOwnerById must reject returned user when item.id !== owner_user_id"
    );

    const exactOwner = await adminTypesMod.fetchShopOwnerById("owner-42", async (path) => {
      assert.equal(path, "admin/users?query=owner-42&page=1&page_size=1");
      return {
        items: [
          {
            id: "owner-42",
            full_name: "Đúng Chủ Shop",
            email: "owner42@test",
            phone: "",
            role: "buyer",
            status: "ACTIVE",
            shop_id: "",
            phone_verified: false,
            cod_rejected_count: 0,
            avatar_url: "",
          },
        ],
        page: 1,
        page_size: 1,
        total: 1,
      };
    });
    assert.equal(exactOwner?.id, "owner-42");
    assert.equal(exactOwner?.full_name, "Đúng Chủ Shop");

    // /admin/gian-hang must never call admin/users without query= or bulk-load owners
    const gianHangSrc = readFileSync(
      resolve(WEB_SRC, "app/(admin)/admin/gian-hang/page.tsx"),
      "utf8"
    );
    assert.equal(gianHangSrc.includes("fetchShopOwnersById"), false);
    assert.equal(gianHangSrc.includes("loadOwnersForShops"), false);
    assert.equal(
      /admin\/users(?!\?query=)/.test(gianHangSrc),
      false,
      "gian-hang must never call admin/users without query="
    );
    assert.ok(gianHangSrc.includes("fetchShopOwnerById"));
    assert.ok(gianHangSrc.includes("canApproveShop"));
    assert.ok(gianHangSrc.includes("submitAdminShopReviewDecision"));
    assert.ok(gianHangSrc.includes("buildAdminBankReviewPayload"));
    assert.ok(gianHangSrc.includes("isAdminReviewConflictError"));
    assert.ok(/dữ liệu đã thay đổi/i.test(gianHangSrc));
  });

  test("6. D11: 409 CHECKOUT_FAILED rotates key & guides to /don-hang (never on ambiguous/IDEMPOTENCY_KEY_CONFLICT); SHOP_PICKUP_ADDRESS_REQUIRED messages for buyer & seller", () => {
    // 6a. error-map normalizes CHECKOUT_FAILED (<reason>): ... even though message contains "idempotency_key"
    const checkoutFailedSanitized = errorMapMod.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message:
          "CHECKOUT_FAILED (PAYMENT_FAILED): checkout attempt failed with reason PAYMENT_FAILED; please retry with a new idempotency_key",
      },
    });
    assert.equal(
      checkoutFailedSanitized.code,
      "CHECKOUT_FAILED",
      "CHECKOUT_FAILED (<reason>): must normalize to CHECKOUT_FAILED, not FAILED_PRECONDITION"
    );
    assert.ok(
      !/idempotency_key|PAYMENT_FAILED/.test(checkoutFailedSanitized.message),
      "sanitized buyer message must not leak raw English/internal tokens"
    );

    const checkoutFailedErr = new apiMod.ApiError(
      409,
      checkoutFailedSanitized.code,
      checkoutFailedSanitized.message
    );
    const kind = checkoutHelpersMod.classifyCheckoutSubmitError(checkoutFailedErr, {
      hasFlashSaleItem: true,
      paymentMethod: "VIETQR",
    });
    assert.equal(kind, "checkout_failed");
    assert.equal(
      checkoutHelpersMod.shouldRotateCheckoutKeyOnError,
      undefined,
      "ponytail: 1-line wrapper shouldRotateCheckoutKeyOnError must be deleted"
    );
    assert.equal(
      checkoutHelpersMod.isFlashLimitOrSlotError(checkoutFailedErr, true, "VIETQR"),
      false,
      "CHECKOUT_FAILED must not trigger flash opt-out"
    );

    const buyerCheckoutFailedMsg = checkoutMessagesMod.translateCheckoutError(checkoutFailedErr);
    assert.match(buyerCheckoutFailedMsg, /Đơn hàng của tôi/i);

    // 6b. SHOP_PICKUP_ADDRESS_REQUIRED normalized + distinct natural Vietnamese for buyer & seller
    const pickupSanitized = errorMapMod.sanitizeGatewayError(409, {
      error: {
        code: "FAILED_PRECONDITION",
        message: "SHOP_PICKUP_ADDRESS_REQUIRED: shop 1234 has no pickup address configured",
      },
    });
    assert.equal(pickupSanitized.code, "SHOP_PICKUP_ADDRESS_REQUIRED");

    const pickupErr = new apiMod.ApiError(409, pickupSanitized.code, pickupSanitized.message);
    assert.equal(
      checkoutHelpersMod.isFlashLimitOrSlotError(pickupErr, true, "VIETQR"),
      false,
      "SHOP_PICKUP_ADDRESS_REQUIRED must not trigger flash opt-out"
    );

    const buyerPickupMsg = checkoutMessagesMod.translateCheckoutError(pickupErr);
    assert.match(buyerPickupMsg, /địa chỉ lấy hàng/i);
    assert.ok(
      !/SHOP_PICKUP_ADDRESS_REQUIRED|province_code/i.test(buyerPickupMsg),
      "buyer message must be natural Vietnamese without raw codes"
    );

    const sellerPickupMsg = sellerTypesMod.translateSellerShipError(pickupErr);
    assert.match(sellerPickupMsg, /địa chỉ lấy hàng/i);
    assert.match(sellerPickupMsg, /Hồ sơ gian hàng/i);
    assert.notEqual(
      buyerPickupMsg,
      sellerPickupMsg,
      "buyer and seller must have tailored messages for SHOP_PICKUP_ADDRESS_REQUIRED"
    );

    // Verify CheckoutClient handles checkout_failed -> rotate key + /don-hang link, and never rotates on conflict/ambiguous
    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    const helpersSrc = readFileSync(resolve(WEB_SRC, "features/checkout/helpers.ts"), "utf8");
    assert.ok(
      checkoutSrc.includes("reduceCheckoutSubmitState") &&
        helpersSrc.includes('case "checkout_failed":'),
      "CheckoutClient must handle checkout_failed via reduceCheckoutSubmitState (rotating key)"
    );
    assert.ok(checkoutSrc.includes("checkoutFailedPrior"));
    assert.equal(checkoutSrc.includes("shouldRotateCheckoutKeyOnError"), false);
  });

  test("10. D1 (Round 3): Seller ProductFormView loads seller/categories for commission_rate_bps (with missing-field guard) and no seller/admin file reads commission_rate_bps from public routes", () => {
    const productFormPath = resolve(WEB_SRC, "features/seller/ProductFormView.tsx");
    const productFormSrc = readFileSync(productFormPath, "utf8");

    assert.ok(
      productFormSrc.includes('"seller/categories"') ||
        productFormSrc.includes("`seller/categories"),
      "ProductFormView must load categories from seller/categories so commission_rate_bps is present"
    );
    assert.ok(
      !/api<[^>]+>\(\s*["'`]categories["'`]/.test(productFormSrc),
      "ProductFormView must not call public /categories"
    );
    assert.ok(
      productFormSrc.includes('typeof cat.commission_rate_bps === "number"') &&
        productFormSrc.includes("Number.isFinite(cat.commission_rate_bps)") &&
        productFormSrc.includes("(Hoa hồng ") &&
        productFormSrc.includes(": cat.name}"),
      "ProductFormView must keep missing commission_rate_bps fallback while showing (Hoa hồng x.x%)"
    );

    // Exhaustive check: no seller or admin file calls a public categories/products route while reading commission_rate_bps
    const sellerAdminFiles = [
      ...collectFilesRecursive(resolve(WEB_SRC, "app/(seller)"), [".ts", ".tsx"]),
      ...collectFilesRecursive(resolve(WEB_SRC, "features/seller"), [".ts", ".tsx"]),
      ...collectFilesRecursive(resolve(WEB_SRC, "app/(admin)"), [".ts", ".tsx"]),
      ...collectFilesRecursive(resolve(WEB_SRC, "features/admin"), [".ts", ".tsx"]),
    ];
    const publicCatalogApiRe = /api(?:<[^>]+>)?\(\s*["'`](?:categories|products(?:[/?]|["'`]))/;
    for (const file of sellerAdminFiles) {
      const src = readFileSync(file, "utf8");
      if (src.includes("commission_rate_bps")) {
        assert.equal(
          publicCatalogApiRe.test(src),
          false,
          `${file} reads commission_rate_bps and must not call public categories/products routes`
        );
      }
      if (file.includes("app/(admin)") || file.includes("features/admin")) {
        assert.equal(
          src.includes("ProductFormView"),
          false,
          `${file} (admin) does not reuse seller ProductFormView`
        );
      }
    }
  });

  test("11. [P0-1] Per-line Flash opt-out/restore buttons & handlers self-block when ambiguousSubmit || idempotencyConflict and never clear ambiguousSubmit", () => {
    assert.equal(
      typeof checkoutHelpersMod.canMutateCheckoutFlashSelection,
      "function",
      "canMutateCheckoutFlashSelection must be exported"
    );
    assert.equal(
      checkoutHelpersMod.canMutateCheckoutFlashSelection({
        ambiguousSubmit: false,
        idempotencyConflict: false,
      }),
      true
    );
    assert.equal(
      checkoutHelpersMod.canMutateCheckoutFlashSelection({
        ambiguousSubmit: true,
        idempotencyConflict: false,
      }),
      false
    );
    assert.equal(
      checkoutHelpersMod.canMutateCheckoutFlashSelection({
        ambiguousSubmit: false,
        idempotencyConflict: true,
      }),
      false
    );

    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    assert.ok(checkoutSrc.includes("canMutateCheckoutFlashSelection"));

    // All three flash toggle handlers must guard with canMutateCheckoutFlashSelection and NEVER call setAmbiguousSubmit(false)
    for (const fnName of [
      "handleOptOutFlashSku",
      "handleRestoreFlashSku",
      "handleOptOutAllActiveFlash",
    ]) {
      const start = checkoutSrc.indexOf(`const ${fnName} =`);
      assert.ok(start > 0, `missing ${fnName}`);
      const snippet = checkoutSrc.slice(start, start + 650);
      assert.ok(
        snippet.includes("canMutateCheckoutFlashSelection"),
        `${fnName} must self-block via canMutateCheckoutFlashSelection`
      );
      assert.equal(
        snippet.includes("setAmbiguousSubmit(false)"),
        false,
        `${fnName} must NEVER clear ambiguousSubmit`
      );
    }

    // Both per-line buttons (opt-out and restore) must be gated on !ambiguousSubmit && !idempotencyConflict (or canToggleFlash)
    assert.ok(
      /showFlashOptOut[\s\S]{0,160}canToggleFlash/.test(checkoutSrc),
      "per-line Flash opt-out button must be hidden when ambiguousSubmit || idempotencyConflict"
    );
    const restoreAnchor = "it.flashOptedOut && it.hasLiveFlashAvailable";
    const restoreStart = checkoutSrc.indexOf(restoreAnchor);
    const restoreEnd = checkoutSrc.indexOf("Dùng lại giá Flash", restoreStart);
    const restoreBlock =
      restoreStart > 0 && restoreEnd > restoreStart
        ? checkoutSrc.slice(restoreStart, restoreEnd + 40)
        : "";
    assert.ok(
      restoreBlock.includes("canToggleFlash &&") &&
        restoreBlock.includes("disabled={!canToggleFlash}") &&
        restoreBlock.includes("handleRestoreFlashSku"),
      "per-line Flash restore button must be hidden/disabled when ambiguousSubmit || idempotencyConflict"
    );
  });

  test("12. [P1-2, P2-12, P2-17, P3-20] /admin/gian-hang clears mobileOpenId on desktop select, restricts review to PENDING only, guards per-ID seq/submitting, retries transient errors, and fixes duplicate IDs/loading/alert", () => {
    assert.equal(
      typeof adminTypesMod.canReviewShopStatus,
      "function",
      "canReviewShopStatus must be exported"
    );
    assert.equal(adminTypesMod.canReviewShopStatus("PENDING"), true);
    assert.equal(adminTypesMod.canReviewShopStatus("ACTIVE"), false);
    assert.equal(adminTypesMod.canReviewShopStatus("REJECTED"), false);
    assert.equal(adminTypesMod.canReviewShopStatus("SUSPENDED"), false);

    const validOwner = {
      loading: false,
      user: {
        id: "u-1",
        full_name: "Chủ Shop",
        email: "a@test",
        phone: "",
        role: "buyer",
        status: "ACTIVE",
        shop_id: "",
        phone_verified: false,
        cod_rejected_count: 0,
        avatar_url: "",
      },
    };
    assert.equal(adminTypesMod.canApproveShop("PENDING", validOwner), true);
    assert.equal(adminTypesMod.canApproveShop("REJECTED", validOwner), false);
    assert.equal(adminTypesMod.canApproveShop("SUSPENDED", validOwner), false);
    assert.equal(adminTypesMod.canApproveShop("ACTIVE", validOwner), false);

    const gianHangSrc = readFileSync(
      resolve(WEB_SRC, "app/(admin)/admin/gian-hang/page.tsx"),
      "utf8"
    );
    // P1-2: desktop row selection clears mobileOpenId
    assert.ok(
      /setMobileOpenId\(null\)[\s\S]{0,80}setSelectedId\(shop\.id\)|setSelectedId\(shop\.id\)[\s\S]{0,80}setMobileOpenId\(null\)/.test(
        gianHangSrc
      ),
      "desktop row selection must clear mobileOpenId"
    );
    // P2-12: per-ID sequence counters & per-ID submitting records & PENDING-only buttons
    assert.ok(gianHangSrc.includes("ownerReqSeqRef"), "must track per-owner request sequence");
    assert.ok(gianHangSrc.includes("bankReqSeqRef"), "must track per-shop bank request sequence");
    assert.ok(
      gianHangSrc.includes("submittingByShopId"),
      "must track shop submitting state per shopId"
    );
    assert.ok(
      gianHangSrc.includes("bankSubmittingByShopId"),
      "must track bank submitting state per shopId"
    );
    assert.ok(
      gianHangSrc.includes("canReviewShopStatus"),
      "must gate all review buttons on PENDING status"
    );
    // P3-20: transient errors retried on row re-selection via refs
    assert.ok(
      gianHangSrc.includes("ownerByUserIdRef"),
      "must check error state via ref on row switch"
    );
    assert.ok(
      gianHangSrc.includes("bankByShopIdRef"),
      "must check bank error state via ref on row switch"
    );
    // P2-17: desktop/mobile prefixed input IDs, loading state when !bankState, role="alert" on bank error
    assert.ok(gianHangSrc.includes("${panelPrefix}-shop-note-${shop.id}"));
    assert.ok(gianHangSrc.includes("${panelPrefix}-bank-note-${shop.id}"));
    assert.ok(gianHangSrc.includes("!bankState || bankState.loading"));
    assert.ok(
      /role="alert"[^>]*>\s*\{bankState\.error\}/.test(gianHangSrc) ||
        /className=\{shared\.validationError\}\s+role="alert">\s*\{bankState\.error\}/.test(
          gianHangSrc
        )
    );
  });

  test("13. [P1-3] Partial sku_ids batch failure preserves fulfilled batches and logs console.warn without user PII", async () => {
    const warnings = [];
    const origWarn = console.warn;
    console.warn = (...args) => warnings.push(args.join(" "));
    try {
      const base = [makeProduct(1)];
      const rawSales = [
        {
          id: "fs-1",
          name: "Flash",
          starts_at: "2026-11-24T00:00:00Z",
          ends_at: "2026-11-24T23:59:59Z",
          items: [1, 2, 3, 4].map((idx) => ({
            id: `fi-${idx}`,
            flash_sale_id: "fs-1",
            shop_id: "shop-1",
            sku_id: `sku-${idx}`,
            flash_price_vnd: 120_000,
            original_price_vnd: 200_000,
            total_slots: 10,
            remaining_slots: 5,
            per_user_limit: 2,
            status: "APPROVED",
            rejection_reason: "",
          })),
        },
      ];

      // 3 batches (chunkSize=1 for missing sku-2, sku-3, sku-4): batch 2 fails, batches 1 & 3 succeed
      const mergedPartial = await catalogMapperMod.fetchProductsForFlashSalesWithFetcher(
        rawSales,
        base,
        async (batch) => {
          if (batch.includes("sku-3")) throw new Error("503 upstream timeout");
          const idx = Number(batch[0].replace("sku-", ""));
          return [makeProduct(idx)];
        },
        1
      );
      const mergedIds = mergedPartial.map((p) => p.id);
      assert.ok(mergedIds.includes("prod-1"), "must keep base top-100");
      assert.ok(mergedIds.includes("prod-2"), "must keep fulfilled batch 1");
      assert.ok(mergedIds.includes("prod-4"), "must keep fulfilled batch 3");
      assert.equal(mergedIds.includes("prod-3"), false);
      assert.ok(warnings.length >= 1, "failed batch must emit console.warn");

      // All batches fail -> gracefully preserves top-100 baseProducts without throwing
      const mergedAllFail = await catalogMapperMod.fetchProductsForFlashSalesWithFetcher(
        rawSales,
        base,
        async () => {
          throw new Error("503 all down");
        },
        2
      );
      assert.deepEqual(
        mergedAllFail.map((p) => p.id),
        ["prod-1"]
      );

      // fetchActiveShopDiscountsWithFetcher: batch 2 fails -> batch 1 discounts preserved (e.g. PDP main product)
      const discPartial = await checkoutHelpersMod.fetchActiveShopDiscountsWithFetcher(
        { skuIds: ["sku-main", "sku-other-1", "sku-other-2"], chunkSize: 1 },
        async (batch) => {
          if (batch.skuIds?.[0] === "sku-other-1") throw new Error("429 transient");
          return [
            {
              item_id: `item-${batch.skuIds?.[0]}`,
              program_id: "prog-1",
              shop_id: "shop-1",
              sku_id: batch.skuIds?.[0],
              original_price_vnd: 200_000,
              discounted_price_vnd: 160_000,
              discount_percent: 20,
              per_user_limit: 2,
            },
          ];
        }
      );
      const discSkus = discPartial.map((d) => d.sku_id);
      assert.ok(discSkus.includes("sku-main"), "must preserve batch 1 discount for main product");
      assert.ok(discSkus.includes("sku-other-2"), "must preserve batch 3 discount");
    } finally {
      console.warn = origWarn;
    }

    // Verify loadFlashSalePageData has fallback catch + console.warn
    const catalogSrc = readFileSync(resolve(WEB_SRC, "lib/catalog.ts"), "utf8");
    const flashLoaderIdx = catalogSrc.indexOf("export async function loadFlashSalePageData");
    const flashLoaderSrc = catalogSrc.slice(flashLoaderIdx);
    assert.ok(
      flashLoaderSrc.includes(".catch(") && flashLoaderSrc.includes("console.warn"),
      "loadFlashSalePageData must catch flash SKU fetch errors with console.warn fallback to top-100"
    );
  });

  test("14. [P1-5] resolveManualVoucherCode validates known PLATFORM/FREESHIP vouchers via previewFn before reporting applied", async () => {
    const shops = [
      { shopId: "shop-a", shopName: "Xưởng A", subtotalVnd: 150_000, shippingFeeVnd: 25_000 },
    ];
    const knownVouchers = [
      {
        id: "v-plat-cat",
        code: "PLATCAT50",
        name: "Giảm 50k danh mục riêng",
        scope: "PLATFORM",
        shop_id: "",
        discount_type: "FIXED",
        value_vnd: 50_000,
        discount_percent: 0,
        max_discount_vnd: 50_000,
        min_order_vnd: 100_000,
        quota: 100,
        used_count: 1,
        remaining_quota: 99,
        per_user_limit: 1,
        is_active: true,
        status: "ACTIVE",
        eligible: true,
        ineligible_reason: "",
      },
      {
        id: "v-ship-0",
        code: "SHIPZERO",
        name: "Freeship 25k",
        scope: "FREESHIP",
        shop_id: "",
        discount_type: "FIXED",
        value_vnd: 25_000,
        discount_percent: 0,
        max_discount_vnd: 25_000,
        min_order_vnd: 100_000,
        quota: 100,
        used_count: 1,
        remaining_quota: 99,
        per_user_limit: 1,
        is_active: true,
        status: "ACTIVE",
        eligible: true,
        ineligible_reason: "",
      },
    ];
    const baseBd = (overrides = {}) => ({
      shops: [],
      total_subtotal_vnd: 150_000,
      total_flash_sale_discount_vnd: 0,
      total_shop_voucher_discount_vnd: 0,
      total_platform_voucher_discount_vnd: 0,
      total_freeship_discount_vnd: 0,
      total_coins_discount_vnd: 0,
      total_discount_vnd: 0,
      platform_cost_vnd: 0,
      shop_cost_vnd: 0,
      total_shipping_fee_vnd: 25_000,
      final_payable_vnd: 175_000,
      warnings: [],
      ...overrides,
    });

    // Known PLATFORM voucher rejected by server preview (category mismatch -> 0 VND) MUST NOT return ok:true
    let platProbeCount = 0;
    const platRes = await checkoutHelpersMod.resolveManualVoucherCode({
      rawCode: "PLATCAT50",
      knownVouchers,
      shops,
      hasShippingEstimate: true,
      previewFn: async (probe) => {
        platProbeCount += 1;
        assert.equal(probe.platformVoucherId, "PLATCAT50");
        return baseBd({
          total_platform_voucher_discount_vnd: 0,
          warnings: ["voucher PLATCAT50 does not apply to any items in the eligible categories"],
        });
      },
    });
    assert.equal(platProbeCount, 1, "known PLATFORM voucher must call previewFn");
    assert.equal(platRes.ok, false, "must reject when server preview returns 0 VND discount");
    assert.match(platRes.message, /danh mục/i);

    // Known FREESHIP voucher accepted by server preview (> 0 VND) returns ok:true
    let shipProbeCount = 0;
    const shipOk = await checkoutHelpersMod.resolveManualVoucherCode({
      rawCode: "SHIPZERO",
      knownVouchers,
      shops,
      hasShippingEstimate: true,
      previewFn: async (probe) => {
        shipProbeCount += 1;
        assert.equal(probe.freeshipVoucherId, "SHIPZERO");
        return baseBd({ total_freeship_discount_vnd: 25_000 });
      },
    });
    assert.equal(shipProbeCount, 1, "known FREESHIP voucher must call previewFn");
    assert.equal(shipOk.ok, true);
    assert.equal(shipOk.scope, "FREESHIP");

    // Known FREESHIP voucher rejected with 0 VND discount returns ok:false
    const shipZeroFail = await checkoutHelpersMod.resolveManualVoucherCode({
      rawCode: "SHIPZERO",
      knownVouchers,
      shops,
      hasShippingEstimate: true,
      previewFn: async () =>
        baseBd({
          total_freeship_discount_vnd: 0,
          warnings: ["freeship voucher SHIPZERO provides 0 VND discount and was not applied"],
        }),
    });
    assert.equal(shipZeroFail.ok, false);
    assert.equal(shipZeroFail.scope, "FREESHIP");
  });

  test("15. [P2-6, P2-7, P2-9] Cancelled 200 replay does not navigate to dead payment; submittingRef locks double click; preview error blocks checkout when voucher/coins active", () => {
    assert.equal(
      typeof checkoutHelpersMod.isTerminalCancelledCheckoutResponse,
      "function",
      "isTerminalCancelledCheckoutResponse must be exported"
    );
    assert.equal(
      typeof checkoutHelpersMod.getCheckoutPreviewBlockReason,
      "function",
      "getCheckoutPreviewBlockReason must be exported"
    );

    // P2-6
    assert.equal(
      checkoutHelpersMod.isTerminalCancelledCheckoutResponse({
        orders: [{ status: "CANCELLED" }],
        payment_status: "PENDING",
      }),
      true
    );
    assert.equal(
      checkoutHelpersMod.isTerminalCancelledCheckoutResponse({
        orders: [{ status: "PENDING" }],
        payment_status: "EXPIRED",
      }),
      true
    );
    assert.equal(
      checkoutHelpersMod.isTerminalCancelledCheckoutResponse({
        orders: [{ status: "PENDING" }],
        payment_status: "FAILED",
      }),
      true
    );
    assert.equal(
      checkoutHelpersMod.isTerminalCancelledCheckoutResponse({
        orders: [{ status: "PENDING" }],
        payment_status: "PENDING",
      }),
      false
    );

    // P2-9
    const blockedCoins = checkoutHelpersMod.getCheckoutPreviewBlockReason({
      preview: null,
      previewError: "503",
      hasActiveVouchers: false,
      useCoins: true,
      coinBalance: 10_000,
    });
    assert.ok(
      blockedCoins && blockedCoins.length > 10,
      "must block when useCoins active & preview null"
    );
    const blockedVoucher = checkoutHelpersMod.getCheckoutPreviewBlockReason({
      preview: null,
      previewError: null,
      hasActiveVouchers: true,
      useCoins: false,
      coinBalance: 0,
    });
    assert.ok(
      blockedVoucher && blockedVoucher.length > 10,
      "must block when voucher active & preview null"
    );
    assert.equal(
      checkoutHelpersMod.getCheckoutPreviewBlockReason({
        preview: null,
        previewError: "503",
        hasActiveVouchers: false,
        useCoins: false,
        coinBalance: 10_000,
      }),
      null,
      "no voucher/coins selected -> do not block checkout"
    );

    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    assert.ok(checkoutSrc.includes("isTerminalCancelledCheckoutResponse"));
    assert.ok(checkoutSrc.includes("cancelledReplayPrior"));
    assert.ok(
      checkoutSrc.includes("submittingRef"),
      "must use synchronous submittingRef double-click lock"
    );
    assert.ok(checkoutSrc.includes("previewError"), "must store and display previewError");
    assert.ok(checkoutSrc.includes("getCheckoutPreviewBlockReason"));
  });

  test("16. [P2-10] error-map maps 'bank_account_id is required' before generic 'bank_account' 6-19 digit message", () => {
    const missingId = errorMapMod.sanitizeGatewayError(400, {
      error: {
        code: "INVALID_ARGUMENT",
        message: "bank_account_id is required",
      },
    });
    assert.match(missingId.message, /Thiếu mã bản ghi tài khoản ngân hàng/i);
    assert.equal(missingId.message.includes("6–19"), false);

    const badNumber = errorMapMod.sanitizeGatewayError(400, {
      error: {
        code: "INVALID_ARGUMENT",
        message: "invalid bank_account number format",
      },
    });
    assert.match(badNumber.message, /6–19/);
  });

  test("17. [P2-13, P2-14, P2-15, P3-19, ponytail] Flash slot selection, strikethrough rules, cart flash-to-shop-discount fallback, atomic header search, and ponytail cleanup", () => {
    // P2-13: pickPreferredFlashSlot skips empty live slot and selects live slot with items
    assert.equal(typeof catalogMapperMod.pickPreferredFlashSlot, "function");
    const picked = catalogMapperMod.pickPreferredFlashSlot([
      { id: "empty-live", status: "live", items: [] },
      { id: "real-live", status: "live", items: [{ id: "fi-1" }] },
    ]);
    assert.equal(picked?.id, "real-live");

    const flashClientSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/flash-sale/FlashSaleClient.tsx"),
      "utf8"
    );
    const homeClientSrc = readFileSync(resolve(WEB_SRC, "app/(shop)/HomeClient.tsx"), "utf8");
    assert.ok(flashClientSrc.includes("pickPreferredFlashSlot"));
    assert.ok(homeClientSrc.includes("pickPreferredFlashSlot"));
    assert.ok(
      /liveSlot\s*&&\s*liveSlot\.items\.length\s*>\s*0/.test(homeClientSrc),
      "HomeClient hero CTA must require liveSlot.items.length > 0"
    );

    // P2-14: tie at minPriceVnd prefers entry with original; strikethrough only when original > max displayed
    const tieProduct = makeProduct(10, { shop_id: "shop-a" });
    tieProduct.skus = [
      { ...tieProduct.skus[0], id: "sku-reg-150", price_vnd: 150_000 },
      { ...tieProduct.skus[0], id: "sku-disc-150", price_vnd: 200_000 },
    ];
    const discMap = new Map([
      [
        "sku-disc-150",
        {
          item_id: "sd-tie",
          program_id: "sp-tie",
          shop_id: "shop-a",
          sku_id: "sku-disc-150",
          original_price_vnd: 200_000,
          discounted_price_vnd: 150_000,
          discount_percent: 25,
          per_user_limit: 2,
        },
      ],
    ]);
    const tiePricing = catalogMapperMod.computeProductDisplayPricing(
      tieProduct,
      undefined,
      discMap
    );
    assert.equal(tiePricing.minPriceVnd, 150_000);
    assert.equal(tiePricing.maxPriceVnd, 150_000);
    assert.equal(
      tiePricing.originalPriceVnd,
      200_000,
      "tie at minPriceVnd must choose discounted entry with originalPriceVnd regardless of SKU array order"
    );

    const priceSrc = readFileSync(resolve(WEB_SRC, "components/Price.tsx"), "utf8");
    assert.ok(
      priceSrc.includes("upperDisplayed") || priceSrc.includes("originalAmount > maxAmount"),
      "Price.tsx must only show strikethrough when originalAmount > max displayed price"
    );

    // P2-15: shared resolveDisplayedLinePricing falls back from flashShortfall to active shop discount
    assert.equal(typeof checkoutHelpersMod.resolveDisplayedLinePricing, "function");
    const fallbackLine = checkoutHelpersMod.resolveDisplayedLinePricing({
      regularUnitPriceVnd: 200_000,
      quantity: 2,
      isFlashLine: true,
      flashUnitPriceVnd: 130_000,
      flashShortfall: true,
      activeShopDiscount: {
        item_id: "sp-item-1",
        program_id: "sp-1",
        program_name: "Giảm giá hè",
        shop_id: "shop-a",
        sku_id: "sku-1",
        original_price_vnd: 200_000,
        discounted_price_vnd: 170_000,
        discount_percent: 15,
        per_user_limit: 5,
      },
      shopBreakdown: {
        shop_id: "shop-a",
        subtotal_vnd: 400_000,
        flash_sale_discount_vnd: 0,
        shop_program_discount_vnd: 60_000,
        applied_shop_program_item_ids: ["sp-item-1"],
        shop_voucher_id: "",
        shop_voucher_discount_vnd: 0,
        platform_voucher_discount_vnd: 0,
        freeship_discount_vnd: 0,
        coins_discount_vnd: 0,
        shipping_fee_vnd: 20_000,
        final_shipping_fee_vnd: 20_000,
        final_payable_vnd: 360_000,
      },
      isInPreview: true,
    });
    assert.equal(fallbackLine.appliedKind, "shop_discount");
    assert.equal(fallbackLine.effectiveUnitPriceVnd, 170_000);
    assert.equal(fallbackLine.lineTotalVnd, 340_000);

    const cartSrc = readFileSync(resolve(WEB_SRC, "app/(shop)/gio-hang/page.tsx"), "utf8");
    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    assert.ok(cartSrc.includes("resolveDisplayedLinePricing"));
    assert.ok(checkoutSrc.includes("resolveDisplayedLinePricing"));

    // P3-19 & ponytail: shared buildLiveFlashItemsBySkuId/buildActiveShopDiscountsMap, atomic ShopHeader commit, searchError, mobile price range
    assert.equal(typeof catalogMapperMod.buildLiveFlashItemsBySkuId, "function");
    assert.equal(typeof catalogMapperMod.buildActiveShopDiscountsMap, "function");

    const headerSrc = readFileSync(resolve(WEB_SRC, "components/ShopHeader.tsx"), "utf8");
    assert.ok(headerSrc.includes("buildLiveFlashItemsBySkuId"));
    assert.ok(headerSrc.includes("buildActiveShopDiscountsMap"));
    assert.ok(headerSrc.includes("searchError"), "ShopHeader must expose searchError state");
    const itemsAssignIdx = headerSrc.indexOf("const items = Array.isArray(res?.products)");
    const emptySkuGuardIdx = headerSrc.indexOf("if (skuIds.length === 0)", itemsAssignIdx);
    const promiseAllIdx = headerSrc.indexOf("await Promise.all([", emptySkuGuardIdx);
    const seqGuardAfterFetchIdx = headerSrc.indexOf(
      "if (searchReqSeq.current !== seq) return;",
      promiseAllIdx
    );
    const setResultsAfterFetchIdx = headerSrc.indexOf("setSearchResults(items)", promiseAllIdx);
    const earlySection = headerSrc.slice(itemsAssignIdx, emptySkuGuardIdx);
    assert.ok(
      itemsAssignIdx > 0 &&
        emptySkuGuardIdx > itemsAssignIdx &&
        !earlySection.includes("setSearchResults") &&
        promiseAllIdx > emptySkuGuardIdx &&
        seqGuardAfterFetchIdx > promiseAllIdx &&
        seqGuardAfterFetchIdx < setResultsAfterFetchIdx &&
        setResultsAfterFetchIdx > promiseAllIdx &&
        headerSrc.includes("window.clearTimeout(timer)"),
      "ShopHeader must cancel stale seq/timer and never commit setSearchResults(items) before discount maps finish loading when SKUs exist"
    );

    const promoCoinsSrc = readFileSync(
      resolve(WEB_SRC, "features/admin/AdminPromoCoinsPanel.tsx"),
      "utf8"
    );
    assert.equal(
      promoCoinsSrc.includes("catsRes.items"),
      false,
      "ponytail: remove redundant catsRes.items union fallback in AdminPromoCoinsPanel"
    );
  });

  test("18. [P3-21 / D1] data/index.ts does not re-export admin/seller and no public/shared module imports @/data/admin or @/data/seller", () => {
    const dataIndexSrc = readFileSync(resolve(WEB_SRC, "data/index.ts"), "utf8");
    assert.equal(
      /from\s+["']\.\/(admin|seller)["']/.test(dataIndexSrc),
      false,
      "web/src/data/index.ts must not re-export ./admin or ./seller"
    );

    const publicRoots = [
      resolve(WEB_SRC, "app/(shop)"),
      resolve(WEB_SRC, "components"),
      resolve(WEB_SRC, "context"),
      resolve(WEB_SRC, "lib"),
      resolve(WEB_SRC, "mock"),
      resolve(WEB_SRC, "features"),
    ];
    const filesToCheck = [resolve(WEB_SRC, "app/layout.tsx")];
    for (const root of publicRoots) {
      for (const file of collectFilesRecursive(root, [".ts", ".tsx"])) {
        const normalized = file.replace(/\\/g, "/");
        if (normalized.includes("/features/admin/") || normalized.includes("/features/seller/")) {
          continue;
        }
        filesToCheck.push(file);
      }
    }

    const forbiddenImportRe =
      /from\s+["'](?:@\/data\/(?:admin|seller)|(?:\.\.?\/)+data\/(?:admin|seller)|@\/features\/(?:admin|seller)(?:\/[^"']*)?)["']/;
    const violations = [];
    for (const file of filesToCheck) {
      const src = readFileSync(file, "utf8");
      if (forbiddenImportRe.test(src)) {
        violations.push(file.replace(WEB_SRC, "web/src"));
      }
    }
    assert.deepEqual(
      violations,
      [],
      `Public/shared modules must not import @/data/admin, @/data/seller, or admin/seller features: ${violations.join(", ")}`
    );

    const mockStoreSrc = readFileSync(resolve(WEB_SRC, "mock/MockStoreContext.tsx"), "utf8");
    assert.equal(
      /\b(INITIAL_PLATFORM_CONFIG|ADMIN_DISPUTES)\b/.test(mockStoreSrc),
      false,
      "Root MockStoreContext must not bundle admin mock constants (INITIAL_PLATFORM_CONFIG / ADMIN_DISPUTES)"
    );
  });

  test("19. [P1-1] Unified checkout key-uncertainty reducer preserves lock across 429/401/400/403/flash_blocked and clears only on 200, 409 CHECKOUT_FAILED, or explicit new order", () => {
    assert.equal(
      typeof checkoutHelpersMod.reduceCheckoutSubmitState,
      "function",
      "reduceCheckoutSubmitState must be exported"
    );
    const initial = checkoutHelpersMod.INITIAL_CHECKOUT_SUBMIT_GUARD_STATE;
    assert.equal(initial.keyUncertainty, "none");

    const runSequence = (startState, events) => {
      let state = startState;
      let last = { state, rotateKey: false, clearStoredKey: false };
      for (const ev of events) {
        last = checkoutHelpersMod.reduceCheckoutSubmitState(state, ev);
        state = last.state;
      }
      return last;
    };

    const cases = [
      {
        name: "ambiguous -> submit_start -> 429 (definite_failure) keeps lock & blocks flash toggle",
        events: [
          { type: "submit_start" },
          { type: "submit_error", kind: "ambiguous_created" },
          { type: "submit_start" },
          { type: "submit_error", kind: "definite_failure" },
        ],
        expectedUncertainty: "ambiguous",
        canToggleFlash: false,
        rotateKey: false,
        clearStoredKey: false,
        flashBlocked: false,
      },
      {
        name: "ambiguous -> submit_start -> 401 (unauthenticated) keeps lock",
        events: [
          { type: "submit_error", kind: "ambiguous_created" },
          { type: "submit_start" },
          { type: "submit_error", kind: "unauthenticated" },
        ],
        expectedUncertainty: "ambiguous",
        canToggleFlash: false,
        rotateKey: false,
        clearStoredKey: false,
        flashBlocked: false,
      },
      {
        name: "ambiguous -> submit_start -> flash_blocked keeps lock and hides flash opt-out",
        events: [
          { type: "submit_error", kind: "ambiguous_created" },
          { type: "submit_start" },
          { type: "submit_error", kind: "flash_blocked" },
        ],
        expectedUncertainty: "ambiguous",
        canToggleFlash: false,
        rotateKey: false,
        clearStoredKey: false,
        flashBlocked: false,
      },
      {
        name: "ambiguous -> submit_start -> 200 success clears uncertainty & stored key",
        events: [
          { type: "submit_error", kind: "ambiguous_created" },
          { type: "submit_start" },
          { type: "submit_success", terminalCancelled: false },
        ],
        expectedUncertainty: "none",
        canToggleFlash: true,
        rotateKey: false,
        clearStoredKey: true,
        flashBlocked: false,
      },
      {
        name: "ambiguous -> submit_start -> 409 CHECKOUT_FAILED clears lock & rotates key",
        events: [
          { type: "submit_error", kind: "ambiguous_created" },
          { type: "submit_start" },
          { type: "submit_error", kind: "checkout_failed" },
        ],
        expectedUncertainty: "none",
        canToggleFlash: true,
        rotateKey: true,
        clearStoredKey: true,
        flashBlocked: false,
      },
      {
        name: "conflict -> submit_start -> 429 keeps conflict lock & blocks flash toggle",
        events: [
          { type: "submit_error", kind: "idempotency_conflict" },
          { type: "submit_start" },
          { type: "submit_error", kind: "definite_failure" },
        ],
        expectedUncertainty: "conflict",
        canToggleFlash: false,
        rotateKey: false,
        clearStoredKey: false,
        flashBlocked: false,
      },
      {
        name: "ambiguous -> explicit_new_order clears lock, rotates key & clears stored key",
        events: [
          { type: "submit_error", kind: "ambiguous_created" },
          { type: "explicit_new_order" },
        ],
        expectedUncertainty: "none",
        canToggleFlash: true,
        rotateKey: true,
        clearStoredKey: true,
        flashBlocked: false,
      },
      {
        name: "conflict -> explicit_new_order clears lock, rotates key & clears stored key",
        events: [
          { type: "submit_error", kind: "idempotency_conflict" },
          { type: "explicit_new_order" },
        ],
        expectedUncertainty: "none",
        canToggleFlash: true,
        rotateKey: true,
        clearStoredKey: true,
        flashBlocked: false,
      },
      {
        name: "fresh none -> flash_blocked opens flash opt-out and clears stored key without rotating",
        events: [{ type: "submit_start" }, { type: "submit_error", kind: "flash_blocked" }],
        expectedUncertainty: "none",
        canToggleFlash: true,
        rotateKey: false,
        clearStoredKey: true,
        flashBlocked: true,
      },
      {
        name: "fresh none -> definite_failure clears stored key without rotating",
        events: [{ type: "submit_start" }, { type: "submit_error", kind: "definite_failure" }],
        expectedUncertainty: "none",
        canToggleFlash: true,
        rotateKey: false,
        clearStoredKey: true,
        flashBlocked: false,
      },
      {
        name: "fresh none -> 401 unauthenticated clears stored key without rotating",
        events: [{ type: "submit_start" }, { type: "submit_error", kind: "unauthenticated" }],
        expectedUncertainty: "none",
        canToggleFlash: true,
        rotateKey: false,
        clearStoredKey: true,
        flashBlocked: false,
      },
    ];

    for (const tc of cases) {
      const res = runSequence(initial, tc.events);
      assert.equal(res.state.keyUncertainty, tc.expectedUncertainty, `${tc.name}: keyUncertainty`);
      assert.equal(
        checkoutHelpersMod.canMutateCheckoutFlashSelection({
          keyUncertainty: res.state.keyUncertainty,
        }),
        tc.canToggleFlash,
        `${tc.name}: canToggleFlash`
      );
      assert.equal(res.rotateKey, tc.rotateKey, `${tc.name}: rotateKey`);
      assert.equal(res.clearStoredKey, tc.clearStoredKey, `${tc.name}: clearStoredKey`);
      assert.equal(res.state.flashBlocked, tc.flashBlocked, `${tc.name}: flashBlocked`);
    }

    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    assert.ok(
      checkoutSrc.includes("reduceCheckoutSubmitState"),
      "CheckoutClient must use reduceCheckoutSubmitState"
    );
    assert.equal(
      checkoutSrc.includes("setAmbiguousSubmit(false)"),
      false,
      "CheckoutClient must not clear ambiguousSubmit at start of handlePlaceOrder"
    );
    assert.equal(
      checkoutSrc.includes("setIdempotencyConflict(false)"),
      false,
      "CheckoutClient must not clear idempotencyConflict at start of handlePlaceOrder"
    );
    assert.ok(
      checkoutSrc.includes("keyMayHaveCreatedOrder") &&
        /keyMayHaveCreatedOrder[\s\S]{0,650}Xem Đơn hàng của tôi[\s\S]{0,450}Đặt đơn mới/.test(
          checkoutSrc
        ),
      "Uncertain checkout state (including ambiguous) must render both Xem Đơn hàng của tôi and Đặt đơn mới"
    );
  });

  test("20. [P1-2 / R9] Checkout always sends from_cart:false with explicit displayed items and post-checkout clears ordered SKUs for cart (skips Buy Now)", () => {
    assert.equal(
      typeof checkoutHelpersMod.resolvePostCheckoutCartAction,
      "function",
      "resolvePostCheckoutCartAction must be exported"
    );
    assert.equal(
      typeof checkoutHelpersMod.buildCheckoutRequestPayload,
      "function",
      "buildCheckoutRequestPayload must be exported"
    );

    assert.deepEqual(
      checkoutHelpersMod.resolvePostCheckoutCartAction({
        isBuyNow: true,
        orderedSkuIds: ["sku-1"],
      }),
      { kind: "none" }
    );
    assert.deepEqual(
      checkoutHelpersMod.resolvePostCheckoutCartAction({
        isBuyNow: false,
        orderedSkuIds: ["sku-1", "sku-2", "sku-1", " "],
      }),
      { kind: "clear_skus", skuIds: ["sku-1", "sku-2"] }
    );
    assert.deepEqual(
      checkoutHelpersMod.resolvePostCheckoutCartAction({
        isBuyNow: false,
        orderedSkuIds: [],
      }),
      { kind: "none" }
    );

    const payload = checkoutHelpersMod.buildCheckoutRequestPayload({
      addressId: "addr-1",
      items: [
        { skuId: "sku-1", quantity: 2, flashSaleItemId: "fsi-1" },
        { skuId: "sku-2", quantity: 1 },
      ],
      shopVoucherIds: { "shop-1": "SV10" },
      platformVoucherCode: "PV20",
      freeshipVoucherCode: null,
      coinsToUse: 5000.9,
      paymentMethod: "VIETQR",
      shippingChoices: [{ shop_id: "shop-1", carrier_code: "ghn", service_code: "STANDARD" }],
    });
    assert.equal(payload.from_cart, false, "web checkout must always send from_cart: false");
    assert.deepEqual(payload.items, [
      { sku_id: "sku-1", quantity: 2, flash_sale_item_id: "fsi-1" },
      { sku_id: "sku-2", quantity: 1 },
    ]);
    assert.equal(payload.coins_to_use, 5000);
    assert.equal(payload.platform_voucher_id, "PV20");
    assert.equal("freeship_voucher_id" in payload, false);

    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    assert.ok(checkoutSrc.includes("buildCheckoutRequestPayload"));
    assert.ok(checkoutSrc.includes("resolvePostCheckoutCartAction"));
    assert.equal(
      /from_cart|fromCart|isFullCartSelected/.test(checkoutSrc),
      false,
      "CheckoutClient must not branch on from_cart/fromCart/isFullCartSelected"
    );
    assert.ok(
      /clearCart\([\s\S]{0,80}\)\.catch\(/.test(checkoutSrc) ||
        /Promise\.resolve\(clearCart\([\s\S]{0,80}\)\)\.catch\(/.test(checkoutSrc),
      "CheckoutClient must call clearCart(orderedSkuIds) with non-blocking catch"
    );
  });

  test("21. [P1-3 & P2-3] RetryPaymentBox ignores stale payment fetches after VietQR replacement, closes SSE on terminal event, and scopes disconnect refresh per effect", () => {
    assert.equal(
      typeof checkoutHelpersMod.shouldApplyPaymentFetchResult,
      "function",
      "shouldApplyPaymentFetchResult must be exported"
    );
    assert.equal(
      typeof checkoutHelpersMod.isTerminalPaymentStatus,
      "function",
      "isTerminalPaymentStatus must be exported"
    );

    assert.equal(
      checkoutHelpersMod.shouldApplyPaymentFetchResult({
        targetId: "pay-old",
        activePaymentId: "pay-new",
        requestSeq: 2,
        latestSeq: 2,
      }),
      false,
      "stale response for superseded payment ID must be ignored"
    );
    assert.equal(
      checkoutHelpersMod.shouldApplyPaymentFetchResult({
        targetId: "pay-new",
        activePaymentId: "pay-new",
        requestSeq: 1,
        latestSeq: 2,
      }),
      false,
      "older in-flight fetch sequence must be ignored"
    );
    assert.equal(
      checkoutHelpersMod.shouldApplyPaymentFetchResult({
        targetId: "pay-new",
        activePaymentId: "pay-new",
        requestSeq: 2,
        latestSeq: 2,
      }),
      true
    );

    for (const term of ["SUCCEEDED", "FAILED", "EXPIRED", "CANCELLED", "REFUNDED"]) {
      assert.equal(checkoutHelpersMod.isTerminalPaymentStatus(term), true, `${term} is terminal`);
    }
    assert.equal(checkoutHelpersMod.isTerminalPaymentStatus("PENDING"), false);
    assert.equal(checkoutHelpersMod.isTerminalPaymentStatus("UNKNOWN"), false);

    const retrySrc = readFileSync(
      resolve(WEB_SRC, "features/commerce/RetryPaymentBox.tsx"),
      "utf8"
    );
    assert.ok(
      retrySrc.includes("activePaymentIdRef"),
      "RetryPaymentBox must track activePaymentIdRef"
    );
    assert.ok(
      retrySrc.includes("shouldApplyPaymentFetchResult"),
      "RetryPaymentBox must guard fetchPayment with shouldApplyPaymentFetchResult"
    );
    assert.ok(
      retrySrc.includes("isTerminalPaymentStatus"),
      "RetryPaymentBox must close SSE immediately on terminal status"
    );
    const connectIdx = retrySrc.indexOf("const connectSse =");
    const flagDeclIdx = retrySrc.indexOf("let refreshedForThisDisconnect = false;");
    const onOpenIdx = retrySrc.indexOf("nextSource.onopen =");
    assert.ok(
      flagDeclIdx > 0 && connectIdx > 0 && flagDeclIdx < connectIdx,
      "refreshedForThisDisconnect must be declared at useEffect scope (before connectSse)"
    );
    assert.ok(
      onOpenIdx > 0 &&
        retrySrc.slice(onOpenIdx, onOpenIdx + 220).includes("refreshedForThisDisconnect = false"),
      "refreshedForThisDisconnect must reset inside nextSource.onopen"
    );
  });

  test("22. [P1-4 / R9 / R10] Checkout idempotency signature is userId + cart/buyNowSkuId (survives F5 item/flash drift), bps inputs use step={1}, and 404 bank account renders empty state", () => {
    assert.equal(
      typeof checkoutHelpersMod.buildCheckoutIdempotencySignature,
      "function",
      "buildCheckoutIdempotencySignature must be exported"
    );
    const sigCartBeforeF5 = checkoutHelpersMod.buildCheckoutIdempotencySignature({
      userId: "u-1",
      buyNowSkuId: null,
    });
    const sigCartAfterF5ItemDrift = checkoutHelpersMod.buildCheckoutIdempotencySignature({
      userId: "u-1",
    });
    assert.equal(sigCartBeforeF5, "u-1:cart");
    assert.equal(
      sigCartBeforeF5,
      sigCartAfterF5ItemDrift,
      "same user + cart must keep identical signature even when items/flashSaleItemId/qty drift after F5"
    );

    const sigBuyNow = checkoutHelpersMod.buildCheckoutIdempotencySignature({
      userId: "u-1",
      buyNowSkuId: "sku-9",
    });
    assert.equal(sigBuyNow, "u-1:sku:sku-9");

    const store = new Map();
    const mockStorage = {
      getItem: (k) => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => {
        store.set(k, v);
      },
      removeItem: (k) => {
        store.delete(k);
      },
    };
    // Case: same user, same cart — after ambiguous submit + F5 where flashSaleItemId disappears or qty changes,
    // old pending key MUST still be restored so server replays or returns 409 IDEMPOTENCY_KEY_CONFLICT.
    checkoutHelpersMod.savePendingCheckoutKey(
      { key: "nm-checkout-cart-pending", sig: sigCartBeforeF5 },
      mockStorage
    );
    assert.equal(
      checkoutHelpersMod.readPendingCheckoutKey(sigCartAfterF5ItemDrift, mockStorage),
      "nm-checkout-cart-pending",
      "cart key must be restored across F5 item/flash drift for the same user"
    );

    checkoutHelpersMod.savePendingCheckoutKey(
      { key: "nm-checkout-abc", sig: sigBuyNow },
      mockStorage
    );
    assert.equal(
      checkoutHelpersMod.readPendingCheckoutKey(sigBuyNow, mockStorage),
      "nm-checkout-abc"
    );
    assert.equal(
      checkoutHelpersMod.readPendingCheckoutKey("u-2:sku:sku-9", mockStorage),
      null,
      "different user signature must not reuse stored key"
    );
    assert.equal(
      checkoutHelpersMod.readPendingCheckoutKey("u-1:cart", mockStorage),
      null,
      "cart signature must not reuse Buy Now key"
    );
    checkoutHelpersMod.clearPendingCheckoutKey(mockStorage);
    assert.equal(checkoutHelpersMod.readPendingCheckoutKey(sigBuyNow, mockStorage), null);

    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    const helpersSrc = readFileSync(resolve(WEB_SRC, "features/checkout/helpers.ts"), "utf8");
    assert.ok(checkoutSrc.includes("savePendingCheckoutKey"));
    assert.ok(checkoutSrc.includes("readPendingCheckoutKey"));
    assert.ok(checkoutSrc.includes("clearPendingCheckoutKey"));
    assert.ok(
      /buildCheckoutIdempotencySignature\(\{\s*userId:\s*user\.id,\s*buyNowSkuId:/.test(
        checkoutSrc
      ),
      "CheckoutClient must compute checkoutSignature from user.id + buyNowSkuId (NOT items)"
    );
    assert.ok(
      helpersSrc.includes("ponytail:") || checkoutSrc.includes("ponytail:"),
      "must include ponytail: comment documenting two-tab sessionStorage ceiling"
    );

    // P2: bps inputs allow integer step={1} (e.g. 520, 580 bps)
    const feesSrc = readFileSync(resolve(WEB_SRC, "features/admin/AdminFeesPanel.tsx"), "utf8");
    const catsSrc = readFileSync(
      resolve(WEB_SRC, "features/admin/AdminCategoriesPanel.tsx"),
      "utf8"
    );
    assert.equal(feesSrc.includes("step={25}"), false, "AdminFeesPanel bps must not use step={25}");
    assert.equal(
      feesSrc.includes("step={10}"),
      false,
      "AdminFeesPanel payment bps must not use step={10}"
    );
    assert.equal(
      catsSrc.includes("step={50}"),
      false,
      "AdminCategoriesPanel bps must not use step={50}"
    );

    // P3: /admin/gian-hang maps 404 bank-account to empty state, not red error
    const adminShopsSrc = readFileSync(
      resolve(WEB_SRC, "app/(admin)/admin/gian-hang/page.tsx"),
      "utf8"
    );
    assert.ok(
      /err instanceof ApiError && err\.status === 404/.test(adminShopsSrc),
      "/admin/gian-hang must treat 404 bank-account as empty state instead of error"
    );
  });

  test("23. [P2-1 & P2-2] Cart clears stale preview on error; checkout blocks on 400/409 or promo preview failure; known SHOP voucher goes through previewFn; manual code probe uses seq guard and shared preview helper", async () => {
    // P2-2 part 1: getCheckoutPreviewBlockReason blocks on 400/409 or any active promotion (flash/shop discount/voucher/coins)
    assert.ok(
      checkoutHelpersMod.getCheckoutPreviewBlockReason({
        preview: null,
        previewError: "Sản phẩm đã ngừng kinh doanh.",
        previewErrorStatus: 409,
        hasActiveVouchers: false,
        hasFlashSaleItem: false,
        hasShopDiscountItem: false,
        useCoins: false,
        coinBalance: 0,
      }),
      "must block on 409 preview error even with no vouchers/coins"
    );
    assert.ok(
      checkoutHelpersMod.getCheckoutPreviewBlockReason({
        preview: null,
        previewError: "Dữ liệu không hợp lệ.",
        previewErrorStatus: 400,
        hasActiveVouchers: false,
        hasFlashSaleItem: false,
        hasShopDiscountItem: false,
        useCoins: false,
        coinBalance: 0,
      }),
      "must block on 400 preview error even with no vouchers/coins"
    );
    assert.ok(
      checkoutHelpersMod.getCheckoutPreviewBlockReason({
        preview: null,
        previewError: "503",
        previewErrorStatus: 503,
        hasActiveVouchers: false,
        hasFlashSaleItem: true,
        hasShopDiscountItem: false,
        useCoins: false,
        coinBalance: 0,
      }),
      "must block on 503 preview error when order has Flash sale item"
    );
    assert.ok(
      checkoutHelpersMod.getCheckoutPreviewBlockReason({
        preview: null,
        previewError: "503",
        previewErrorStatus: 503,
        hasActiveVouchers: false,
        hasFlashSaleItem: false,
        hasShopDiscountItem: true,
        useCoins: false,
        coinBalance: 0,
      }),
      "must block on 503 preview error when order has shop discount item"
    );

    // P2-2 part 2: known SHOP voucher MUST go through previewFn before reporting applied
    const knownShopVoucher = {
      id: "v-shop-1",
      code: "SHOPCAT30",
      name: "Giảm 30k shop",
      scope: "SHOP",
      shop_id: "shop-a",
      discount_type: "FIXED",
      value_vnd: 30_000,
      discount_percent: 0,
      max_discount_vnd: 30_000,
      min_order_vnd: 100_000,
      quota: 100,
      used_count: 1,
      remaining_quota: 99,
      per_user_limit: 1,
      is_active: true,
      status: "ACTIVE",
      eligible: true,
      ineligible_reason: "",
    };
    const shops = [
      { shopId: "shop-a", shopName: "Xưởng A", subtotalVnd: 200_000, shippingFeeVnd: 20_000 },
    ];
    let shopProbeCalls = 0;
    const shopRejected = await checkoutHelpersMod.resolveManualVoucherCode({
      rawCode: "SHOPCAT30",
      knownVouchers: [knownShopVoucher],
      shops,
      hasShippingEstimate: true,
      previewFn: async (probe) => {
        shopProbeCalls += 1;
        assert.equal(probe.shopVoucherIds?.["shop-a"], "SHOPCAT30");
        return {
          shops: [
            {
              shop_id: "shop-a",
              subtotal_vnd: 200_000,
              flash_sale_discount_vnd: 0,
              shop_voucher_id: "",
              shop_voucher_discount_vnd: 0,
              platform_voucher_discount_vnd: 0,
              freeship_discount_vnd: 0,
              coins_discount_vnd: 0,
              shipping_fee_vnd: 20_000,
              final_shipping_fee_vnd: 20_000,
              final_payable_vnd: 220_000,
            },
          ],
          total_subtotal_vnd: 200_000,
          total_flash_sale_discount_vnd: 0,
          total_shop_voucher_discount_vnd: 0,
          total_platform_voucher_discount_vnd: 0,
          total_freeship_discount_vnd: 0,
          total_coins_discount_vnd: 0,
          total_discount_vnd: 0,
          platform_cost_vnd: 0,
          shop_cost_vnd: 0,
          total_shipping_fee_vnd: 20_000,
          final_payable_vnd: 220_000,
          warnings: ["voucher SHOPCAT30 per-user limit reached"],
        };
      },
    });
    assert.equal(shopProbeCalls, 1, "known SHOP voucher must be verified via previewFn");
    assert.equal(
      shopRejected.ok,
      false,
      "known SHOP voucher rejected by preview must return ok:false"
    );
    assert.equal(shopRejected.scope, "SHOP");

    const shopAccepted = await checkoutHelpersMod.resolveManualVoucherCode({
      rawCode: "SHOPCAT30",
      knownVouchers: [knownShopVoucher],
      shops,
      hasShippingEstimate: true,
      previewFn: async () => ({
        shops: [
          {
            shop_id: "shop-a",
            subtotal_vnd: 200_000,
            flash_sale_discount_vnd: 0,
            shop_voucher_id: "v-shop-1",
            shop_voucher_discount_vnd: 30_000,
            platform_voucher_discount_vnd: 0,
            freeship_discount_vnd: 0,
            coins_discount_vnd: 0,
            shipping_fee_vnd: 20_000,
            final_shipping_fee_vnd: 20_000,
            final_payable_vnd: 190_000,
          },
        ],
        total_subtotal_vnd: 200_000,
        total_flash_sale_discount_vnd: 0,
        total_shop_voucher_discount_vnd: 30_000,
        total_platform_voucher_discount_vnd: 0,
        total_freeship_discount_vnd: 0,
        total_coins_discount_vnd: 0,
        total_discount_vnd: 30_000,
        platform_cost_vnd: 0,
        shop_cost_vnd: 30_000,
        total_shipping_fee_vnd: 20_000,
        final_payable_vnd: 190_000,
        warnings: [],
      }),
    });
    assert.equal(shopAccepted.ok, true);
    assert.equal(shopAccepted.discountVnd, 30_000);

    // Shared preview helper
    assert.equal(
      typeof checkoutHelpersMod.createManualVoucherPreviewFn,
      "function",
      "createManualVoucherPreviewFn must be exported"
    );
    let capturedPayload = null;
    const probeFn = checkoutHelpersMod.createManualVoucherPreviewFn({
      getShopItems: () => [{ shop_id: "shop-a", shipping_fee_vnd: 20_000, items: [] }],
      shopVoucherIds: { "shop-b": "KEEPB" },
      platformVoucherCode: "PLAT1",
      freeshipVoucherCode: "SHIP1",
      postPreview: async (payload) => {
        capturedPayload = payload;
        return { breakdown: null };
      },
    });
    await probeFn({ shopVoucherIds: { "shop-a": "SHOPCAT30" } });
    assert.deepEqual(capturedPayload.shop_voucher_ids, {
      "shop-b": "KEEPB",
      "shop-a": "SHOPCAT30",
    });
    assert.equal(capturedPayload.platform_voucher_id, "PLAT1");
    assert.equal(capturedPayload.freeship_voucher_id, "SHIP1");

    // P2-1 + P2-2 source wiring on Cart and Checkout
    const cartSrc = readFileSync(resolve(WEB_SRC, "app/(shop)/gio-hang/page.tsx"), "utf8");
    const checkoutSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/thanh-toan/CheckoutClient.tsx"),
      "utf8"
    );
    assert.ok(
      /catch\s*\(err\)\s*\{\s*if\s*\(previewReqSeq\.current !== seq\)\s*return;[\s\S]{0,160}setPreview\(null\);\s*if\s*\(!\(err instanceof ApiError && err\.status === 401\)\)/.test(
        cartSrc
      ),
      "gio-hang must clear stale preview (setPreview(null)) on all preview errors, not only 401"
    );
    assert.ok(
      cartSrc.includes("manualPromoSeqRef"),
      "gio-hang must guard manual voucher probe with seq ref"
    );
    assert.ok(
      checkoutSrc.includes("manualPromoSeqRef"),
      "CheckoutClient must guard manual voucher probe with seq ref"
    );
    assert.ok(cartSrc.includes("createManualVoucherPreviewFn"));
    assert.ok(checkoutSrc.includes("createManualVoucherPreviewFn"));
    assert.ok(checkoutSrc.includes("previewErrorStatus"));
  });

  test("24. [P2-4 & P2-5] CatalogSku omits reservedStock from public RSC payload and e2e 08 fixes Buy Now assertion, Test 3 cleanup, and filler rate budget", () => {
    const mapped = catalogMapperMod.mapGatewayProduct(
      {
        id: "prod-r6",
        shop_id: "shop-1",
        category_id: "cat-1",
        name: "Áo test R6",
        slug: "ao-test-r6",
        description: "Desc",
        images: ["/products/ao-thun-cotton-trang.png"],
        status: "APPROVED",
        skus: [
          {
            id: "sku-r6",
            sku_code: "R6-BLK-M",
            name: "Đen / M",
            attributes: { color: "Đen", size: "M" },
            price_vnd: 199_000,
            stock: 50,
            reserved_stock: 7,
            available_stock: 43,
            version: 1,
            weight_grams: 250,
            image_url: "",
            is_active: true,
          },
        ],
      },
      { categoriesById: new Map(), shopsById: new Map() }
    );
    assert.equal(mapped.skus[0].stock, 43, "sellable available stock remains preserved");
    assert.equal(
      "reservedStock" in mapped.skus[0],
      false,
      "CatalogSku must not serialize reservedStock into public RSC payloads"
    );

    const catalogTypesSrc = readFileSync(resolve(WEB_SRC, "lib/catalog-types.ts"), "utf8");
    assert.equal(
      catalogTypesSrc.includes("reservedStock"),
      false,
      "CatalogSku interface must not declare reservedStock"
    );

    const e2eSrc = readFileSync(
      resolve(WEB_SRC, "../../tests/e2e-web/08_w3a_storefront_security.test.mjs"),
      "utf8"
    );
    assert.equal(
      e2eSrc.includes('!buyNowRes.text.includes("Không tìm thấy sản phẩm")'),
      false,
      "dead Buy Now error assertion in e2e 08 must be replaced with real error copy"
    );
    assert.ok(
      e2eSrc.includes("Sản phẩm mua ngay không còn tồn tại") &&
        e2eSrc.includes("Sản phẩm chưa sẵn sàng để thanh toán"),
      "e2e 08 Buy Now must assert against real thanh-toan/page.tsx & CheckoutClient error messages"
    );
    const test3Start = e2eSrc.indexOf('test("3.');
    const test4Start = e2eSrc.indexOf('test("4.');
    const test3Block = e2eSrc.slice(test3Start, test4Start);
    assert.ok(
      test3Block.includes("deleteSellerProduct(seller, product.id)"),
      "e2e 08 Test 3 finally must delete the temporary product rather than only hiding it"
    );
  });

  test("25. [P3-1, P3-2, P3-3 & admin token] Auth/CSRF/gateway errors enforce Cache-Control: no-store; admin bank review bumps seq & uses CSS token; Flash copy & PDP variant clamp fixed", () => {
    // P3-1: Cache-Control: no-store across csrf.ts, gateway.ts, and all /api/auth/*/route.ts
    const csrfSrc = readFileSync(resolve(WEB_SRC, "lib/server/csrf.ts"), "utf8");
    const csrfJsonMatches = csrfSrc.match(/NextResponse\.json\(/g) ?? [];
    const csrfNoStoreMatches = csrfSrc.match(/Cache-Control["']?\s*:\s*["']no-store["']/g) ?? [];
    assert.ok(
      csrfJsonMatches.length >= 2 && csrfNoStoreMatches.length >= csrfJsonMatches.length,
      "verifyCsrfOrigin must set Cache-Control: no-store on every 403 response"
    );

    const gwSrc = readFileSync(resolve(WEB_SRC, "lib/server/gateway.ts"), "utf8");
    const gwErrStart = gwSrc.indexOf("export function gatewayErrorResponse");
    const gwErrEnd = gwSrc.indexOf("export interface GatewayFetchOptions", gwErrStart);
    const gwErrBlock = gwSrc.slice(gwErrStart, gwErrEnd);
    assert.ok(
      (gwErrBlock.match(/no-store/g) ?? []).length >= 2,
      "gatewayErrorResponse must set Cache-Control: no-store on both GatewayError and fallback 503 responses"
    );

    const authFiles = collectFilesRecursive(resolve(WEB_SRC, "app/api/auth"), [".ts"]);
    for (const file of authFiles) {
      const src = readFileSync(file, "utf8");
      const jsonCount = (src.match(/NextResponse\.json\(/g) ?? []).length;
      const hasNoStoreConst =
        /NO_STORE\s*=\s*\{\s*["']Cache-Control["']:\s*["']no-store["']\s*\}/.test(src);
      if (hasNoStoreConst) {
        const usedCount =
          (src.match(/NO_STORE|Cache-Control["']?\s*:\s*["']no-store["']/g) ?? []).length - 1;
        assert.ok(
          usedCount >= jsonCount,
          `${file.replace(WEB_SRC, "web/src")} must attach Cache-Control: no-store to every NextResponse.json`
        );
      } else {
        const noStoreCount = (src.match(/Cache-Control["']?\s*:\s*["']no-store["']/g) ?? []).length;
        assert.ok(
          noStoreCount >= jsonCount,
          `${file.replace(WEB_SRC, "web/src")} has ${jsonCount} NextResponse.json calls but only ${noStoreCount} Cache-Control: no-store headers`
        );
      }
    }

    // P3-2 & admin token: bankReqSeqRef bump in handleBankReview & no #e3e8eb
    const adminShopSrc = readFileSync(
      resolve(WEB_SRC, "app/(admin)/admin/gian-hang/page.tsx"),
      "utf8"
    );
    const adminShopCss = readFileSync(
      resolve(WEB_SRC, "app/(admin)/admin/gian-hang/page.module.css"),
      "utf8"
    );
    const bankReviewStart = adminShopSrc.indexOf("const handleBankReview =");
    const bankReviewBlock = adminShopSrc.slice(bankReviewStart, bankReviewStart + 1800);
    assert.ok(
      bankReviewBlock.includes("bankReqSeqRef.current[shop.id]"),
      "handleBankReview must bump bankReqSeqRef.current[shop.id] before setBankByShopId"
    );
    assert.equal(
      adminShopSrc.includes("#e3e8eb"),
      false,
      "/admin/gian-hang must not hardcode #e3e8eb inline"
    );
    assert.ok(
      adminShopCss.includes("var(--border)"),
      "/admin/gian-hang CSS module must use var(--border) token"
    );

    // P3-3: per-product Flash copy & PDP variant clamp via computePdpQuantityCap
    const homeSrc = readFileSync(resolve(WEB_SRC, "app/(shop)/HomeClient.tsx"), "utf8");
    const flashSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/flash-sale/FlashSaleClient.tsx"),
      "utf8"
    );
    const pdpSrc = readFileSync(
      resolve(WEB_SRC, "app/(shop)/san-pham/[slug]/ProductDetailClient.tsx"),
      "utf8"
    );
    assert.equal(
      homeSrc.includes("Tối đa 3 món"),
      false,
      "HomeClient must not hardcode 3-item Flash limit"
    );
    assert.equal(
      flashSrc.includes("FLASH_SALE_RULES.maxItemsPerOrder"),
      false,
      "FlashSaleClient must not hardcode FLASH_SALE_RULES.maxItemsPerOrder"
    );
    const clampHelperStart = pdpSrc.indexOf("const clampQuantityForVariant =");
    const selectSizeEnd = pdpSrc.indexOf("const handleAddToCart =", clampHelperStart);
    const variantHandlers = pdpSrc.slice(clampHelperStart, selectSizeEnd);
    assert.ok(
      clampHelperStart > 0 &&
        variantHandlers.includes("computePdpQuantityCap") &&
        variantHandlers.includes("handleSelectColor") &&
        variantHandlers.includes("handleSelectImage") &&
        variantHandlers.includes("handleSelectSize"),
      "PDP variant switch handlers must clamp quantity using computePdpQuantityCap (including Flash limits)"
    );
  });

  test("26. [R12] Real seller/admin forms never use step={N} with N !== 1 (excluding sample-only screens), and ProductFormView/AdminCarriersPanel use noValidate over desktop+mobile blocks", () => {
    const sampleOnlyRelPaths = new Set([
      "app/(admin)/admin/cau-hinh/page.tsx",
      "app/(admin)/admin/noi-dung/page.tsx",
      "app/(admin)/admin/thanh-vien/page.tsx",
    ]);

    const scanRoots = [
      resolve(WEB_SRC, "features/seller"),
      resolve(WEB_SRC, "features/admin"),
      resolve(WEB_SRC, "app/(seller)"),
      resolve(WEB_SRC, "app/(admin)"),
    ];
    const files = scanRoots.flatMap((dir) => collectFilesRecursive(dir, [".tsx"]));
    assert.ok(files.length > 0, "must find seller/admin tsx files");

    const stepRe = /\bstep=\{\s*([0-9]+(?:\.[0-9]+)?)\s*\}|\bstep=["']([0-9]+(?:\.[0-9]+)?)["']/g;
    const violations = [];

    for (const file of files) {
      const rel = file.slice(WEB_SRC.length + 1).replace(/\\/g, "/");
      const src = readFileSync(file, "utf8");
      if (sampleOnlyRelPaths.has(rel)) {
        assert.ok(
          src.includes("Bản xem trước, dữ liệu mẫu"),
          `excluded sample file ${rel} must still be marked as sample preview`
        );
        assert.equal(
          /\bapi(?:<[^>]+>)?\(/.test(src),
          false,
          `excluded sample file ${rel} must not call real server api()`
        );
        continue;
      }

      for (const m of src.matchAll(stepRe)) {
        const val = Number(m[1] ?? m[2]);
        if (!Number.isFinite(val) || val > 1 || val !== 1) {
          const line = src.slice(0, m.index).split("\n").length;
          violations.push(`${rel}:${line} (${m[0]})`);
        }
      }
    }

    assert.deepEqual(
      violations,
      [],
      `real seller/admin forms must only use step={1}, found:\n${violations.join("\n")}`
    );

    const productFormSrc = readFileSync(
      resolve(WEB_SRC, "features/seller/ProductFormView.tsx"),
      "utf8"
    );
    assert.ok(
      /<form\b(?:=>|[^>])*\bnoValidate\b/.test(productFormSrc),
      "ProductFormView form must set noValidate so hidden desktop/mobile SKU inputs never silently block submission"
    );
    assert.equal(
      productFormSrc.includes("min={10}"),
      false,
      "ProductFormView weight input must not reject valid weights < 10g"
    );

    const carriersSrc = readFileSync(
      resolve(WEB_SRC, "features/admin/AdminCarriersPanel.tsx"),
      "utf8"
    );
    assert.ok(
      /<form\b(?:=>|[^>])*\bnoValidate\b/.test(carriersSrc),
      "AdminCarriersPanel form must set noValidate over hidden desktop/mobile rate inputs"
    );
  });
});
