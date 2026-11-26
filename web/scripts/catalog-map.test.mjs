// Unit tests for src/lib/catalog-map.ts (pure mapping, no I/O).
// Run: node --test scripts/catalog-map.test.mjs   (Node >= 22.18 strips TS types)
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  buildCatalogLookups,
  buildFlashSlots,
  checkLiveVoucher,
  createTtlCache,
  extendCategoryIndexForShopProducts,
  filterStorefrontShops,
  findProductByParam,
  findShopByParam,
  getFlashSaleSkuDetail,
  listFlashSaleSkuOptions,
  markFlashProducts,
  mergeCategories,
  mergeProducts,
  mergeShops,
  mergeVouchers,
  pickDefaultFlashSlot,
  resolveFreshShopFlashSales,
  resolveSiteProductEntry,
  secondsUntilSlotEdge,
} from "../src/lib/catalog-map.ts";

const contentCategories = [
  { slug: "ao-thun", name: "Áo thun", description: "", featuredImage: "" },
  { slug: "ao-polo", name: "Áo polo", description: "", featuredImage: "" },
];

const contentShop = {
  id: "shop-bach-dang",
  slug: "xuong-det-bach-dang",
  name: "Xưởng Dệt Bạch Đằng",
  shortName: "Bạch Đằng",
  city: "Hải Phòng",
  provinceCode: "31",
  specialty: "Áo thun",
  foundedYear: 2012,
  responseTime: "",
  fulfillmentRate: "",
  returnPolicy: "",
  warehouseAddress: "",
  story: "",
  accentDot: "#000",
  commissionRate: 0.05,
};

const contentProduct = {
  id: "prod-at-trang",
  slug: "ao-thun-cotton-trang",
  name: "Áo thun trắng",
  shortName: "Áo thun",
  categorySlug: "ao-thun",
  categoryName: "Áo thun",
  shopId: "shop-bach-dang",
  gender: "unisex",
  price: 179000,
  originalPrice: 219000,
  image: "/products/ao-thun-cotton-trang.png",
  gallery: [],
  colors: [{ code: "trang", name: "Trắng", hex: "#fff", image: "/products/a.png" }],
  sizes: ["S", "M"],
  skus: [
    { skuCode: "BD-AT01-TRANG-S", colorCode: "trang", size: "S", stock: 18, price: 179000 },
    { skuCode: "BD-AT01-TRANG-M", colorCode: "trang", size: "M", stock: 34, price: 179000 },
  ],
  fabric: "Cotton",
  gsm: 220,
  fit: "",
  origin: "",
  careGuide: [],
  highlights: [],
  description: "mock",
  sizeChart: [],
};

const gwCategories = [
  { id: "cat-1", name: "Áo thun", slug: "ao-thun" },
  { id: "cat-e2e", name: "E2E Electronics", slug: "cat-123" },
];

const sku = (id, code, price, avail, attrs = {}) => ({
  id,
  sku_code: code,
  price_vnd: price,
  available_stock: avail,
  attributes: attrs,
  is_active: true,
});

test("mergeCategories keeps storefront slugs only, backend name wins", () => {
  const idx = mergeCategories(gwCategories, contentCategories);
  assert.deepEqual(
    idx.categories.map((c) => c.slug),
    ["ao-thun"]
  );
  assert.equal(idx.slugById.get("cat-1"), "ao-thun");
  assert.equal(idx.slugById.has("cat-e2e"), false);
});

test("mergeShops matches profile by name and keeps generic shops", () => {
  const shops = mergeShops(
    [
      { id: "u-1", name: "xưởng dệt bạch đằng ", status: "ACTIVE" },
      { id: "u-2", name: "Shop Mới", description: "Mô tả", status: "ACTIVE" },
      { id: "u-3", name: "Bị khoá", status: "SUSPENDED" },
    ],
    [contentShop]
  );
  assert.equal(shops.length, 2);
  assert.equal(shops[0].id, "shop-bach-dang");
  assert.equal(shops[0].backendId, "u-1");
  assert.equal(shops[1].id, "u-2");
  assert.equal(shops[1].slug, "u-2");
});

test("mergeProducts: real price/stock/sku ids, content kept, foreign categories dropped", () => {
  const categories = mergeCategories(gwCategories, contentCategories);
  const products = mergeProducts(
    [
      {
        id: "p-1",
        shop_id: "u-1",
        category_id: "cat-1",
        name: "Áo thun trắng (thật)",
        description: "",
        status: "APPROVED",
        skus: [sku("s-1", "BD-AT01-TRANG-S", 169000, 3), sku("s-2", "BD-AT01-TRANG-M", 175000, -1)],
      },
      {
        id: "p-e2e",
        shop_id: "u-9",
        category_id: "cat-e2e",
        name: "E2E",
        skus: [sku("s-9", "X", 1000, 5)],
      },
    ],
    { content: [contentProduct], categories, shopIdByBackend: new Map([["u-1", "shop-bach-dang"]]) }
  );
  assert.equal(products.length, 1);
  const p = products[0];
  assert.equal(p.id, "prod-at-trang");
  assert.equal(p.slug, "ao-thun-cotton-trang");
  assert.equal(p.backendId, "p-1");
  assert.equal(p.name, "Áo thun trắng (thật)");
  assert.equal(p.description, "mock");
  assert.equal(p.price, 169000);
  assert.equal(p.originalPrice, 219000);
  assert.equal(p.fabric, "Cotton");
  assert.deepEqual(
    p.skus.map((s) => [s.skuId, s.stock, s.price]),
    [
      ["s-1", 3, 169000],
      ["s-2", 0, 175000],
    ]
  );
});

test("mergeProducts: unknown product built from attributes, preserves custom sizes and sizeless SKUs (Item 7)", () => {
  const categories = mergeCategories(gwCategories, contentCategories);
  const [p] = mergeProducts(
    [
      {
        id: "p-new",
        shop_id: "u-2",
        category_id: "cat-1",
        name: "Áo mới",
        images: ["/products/new.png"],
        skus: [
          sku("n-1", "NEW-M", 99000, 4, { color: "den", color_name: "Đen", size: "M" }),
          sku("n-2", "NEW-FREE", 109000, 4, { color: "den", size: "Free" }),
          sku("n-3", "NEW-3XL", 119000, 2, { color: "den", size: "3XL" }),
        ],
      },
    ],
    { content: [contentProduct], categories, shopIdByBackend: new Map() }
  );
  assert.equal(p.id, "p-new");
  assert.equal(p.slug, "p-new");
  assert.equal(p.shopId, "u-2");
  assert.deepEqual(p.sizes, ["M", "3XL", "Free"]);
  assert.equal(p.skus.length, 3);
  assert.equal(p.colors[0].name, "Đen");
  assert.equal(p.price, 99000);

  // Sizeless product (e.g. accessory/bag without attributes.size) must also map cleanly
  const [noSizeProd] = mergeProducts(
    [
      {
        id: "p-bag",
        shop_id: "u-2",
        category_id: "cat-1",
        name: "Túi vải canvas",
        slug: "tui-vai-canvas",
        images: ["/products/bag.png"],
        status: "APPROVED",
        skus: [sku("bag-1", "BAG-01", 159000, 8, { color: "kem", color_name: "Kem" })],
      },
    ],
    { content: [contentProduct], categories, shopIdByBackend: new Map() }
  );
  assert.ok(noSizeProd);
  assert.equal(noSizeProd.slug, "tui-vai-canvas");
  assert.equal(noSizeProd.skus.length, 1);
  assert.equal(noSizeProd.skus[0].skuId, "bag-1");
  assert.deepEqual(noSizeProd.sizes, ["Mặc định"]);

  // Curated product matched by content SKU code must not drop newly added backend SKUs (e.g. 3XL)
  const [curatedPlus] = mergeProducts(
    [
      {
        id: "p-curated",
        shop_id: "u-1",
        category_id: "cat-1",
        name: "Áo thun trắng",
        status: "APPROVED",
        skus: [
          sku("s-1", "BD-AT01-TRANG-S", 179000, 5),
          sku("s-3xl", "BD-AT01-TRANG-3XL", 199000, 7, { color: "trang", size: "3XL" }),
        ],
      },
    ],
    { content: [contentProduct], categories, shopIdByBackend: new Map([["u-1", "shop-bach-dang"]]) }
  );
  assert.ok(curatedPlus);
  assert.equal(curatedPlus.skus.length, 2);
  assert.ok(curatedPlus.skus.some((s) => s.skuId === "s-3xl" && s.size === "3XL"));
  assert.ok(curatedPlus.sizes.includes("3XL"));
});

test("mergeVouchers keeps copy for known codes and real quota", () => {
  const vouchers = mergeVouchers(
    [
      {
        id: "v-1",
        code: "nexus10",
        name: "x",
        scope: "PLATFORM",
        discount_type: "PERCENT",
        value_vnd: 0,
        discount_percent: 10,
        max_discount_vnd: 60000,
        min_order_vnd: 200000,
        quota: 100,
        remaining_quota: 7,
        ends_at: "2026-12-01T00:00:00Z",
        saved: true,
      },
      {
        id: "v-2",
        code: "SHOPNEW",
        name: "Giảm 5.000₫",
        scope: "SHOP",
        shop_id: "u-1",
        discount_type: "FIXED",
        value_vnd: 5000,
        discount_percent: 0,
        max_discount_vnd: 0,
        min_order_vnd: 0,
        quota: 10,
        remaining_quota: 10,
      },
    ],
    [
      {
        code: "NEXUS10",
        title: "Giảm 10%",
        description: "copy",
        scope: "platform",
        discountType: "percent",
        discountValue: 15,
        minOrderValue: 1,
        expiresAt: "",
        remainingUses: 1,
        totalUses: 1,
      },
    ],
    new Map([["u-1", "shop-bach-dang"]])
  );
  assert.equal(vouchers[0].title, "Giảm 10%");
  assert.equal(vouchers[0].discountValue, 10);
  assert.equal(vouchers[0].remainingUses, 7);
  assert.equal(vouchers[0].id, "v-1");
  assert.equal(vouchers[0].saved, true);
  assert.equal(vouchers[1].scope, "shop");
  assert.equal(vouchers[1].shopId, "shop-bach-dang");
  assert.equal(vouchers[1].maxDiscount, undefined);
});

test("buildFlashSlots groups SKU items per product with VN clock and status", () => {
  const products = [
    {
      ...contentProduct,
      skus: [
        { ...contentProduct.skus[0], skuId: "s-1" },
        { ...contentProduct.skus[1], skuId: "s-2" },
      ],
    },
  ];
  const now = Date.parse("2026-11-20T05:30:00Z"); // 12:30 VN
  const slots = buildFlashSlots(
    [
      {
        id: "fs-18",
        starts_at: "2026-11-20T11:00:00Z",
        ends_at: "2026-11-20T14:00:00Z",
        items: [],
      },
      {
        id: "fs-12",
        starts_at: "2026-11-20T05:00:00Z",
        ends_at: "2026-11-20T08:00:00Z",
        items: [
          {
            id: "i-1",
            sku_id: "s-1",
            flash_price_vnd: 149000,
            total_slots: 10,
            remaining_slots: 4,
            per_user_limit: 2,
            status: "APPROVED",
          },
          {
            id: "i-2",
            sku_id: "s-2",
            flash_price_vnd: 139000,
            total_slots: 5,
            remaining_slots: 5,
            per_user_limit: 1,
            status: "APPROVED",
          },
          {
            id: "i-3",
            sku_id: "s-x",
            flash_price_vnd: 1,
            total_slots: 1,
            remaining_slots: 1,
            per_user_limit: 1,
            status: "APPROVED",
          },
          {
            id: "i-4",
            sku_id: "s-1",
            flash_price_vnd: 1,
            total_slots: 1,
            remaining_slots: 1,
            per_user_limit: 1,
            status: "PENDING",
          },
        ],
      },
    ],
    products,
    now
  );
  assert.deepEqual(
    slots.map((s) => [s.id, s.label, s.endTime, s.status]),
    [
      ["fs-12", "12:00", "15:00", "live"],
      ["fs-18", "18:00", "21:00", "upcoming"],
    ]
  );
  const item = slots[0].items[0];
  assert.equal(slots[0].items.length, 1);
  assert.equal(item.productId, "prod-at-trang");
  assert.equal(item.flashPrice, 139000);
  assert.equal(item.totalQuota, 15);
  assert.equal(item.remainingQuota, 9);
  assert.deepEqual(item.itemIdBySku, { "s-1": "i-1", "s-2": "i-2" });
  assert.equal(markFlashProducts(products, slots)[0].isFlashSale, true);
  assert.equal(pickDefaultFlashSlot(slots)?.id, "fs-12");
  assert.equal(pickDefaultFlashSlot(slots, "fs-18")?.id, "fs-18");
  assert.equal(secondsUntilSlotEdge(slots[0], now), 2.5 * 3600);
  assert.equal(secondsUntilSlotEdge(slots[1], now), 5.5 * 3600);
});

test("mergeProducts keeps OUT_OF_STOCK products with 0 available_stock", () => {
  const categories = mergeCategories(gwCategories, contentCategories);
  const products = mergeProducts(
    [
      {
        id: "p-oos",
        shop_id: "u-1",
        category_id: "cat-1",
        name: "Áo hết hàng",
        status: "OUT_OF_STOCK",
        skus: [sku("s-oos", "BD-AT01-TRANG-S", 179000, 0)],
      },
      {
        id: "p-pending",
        shop_id: "u-1",
        category_id: "cat-1",
        name: "Chưa duyệt",
        status: "PENDING",
        skus: [sku("s-pend", "BD-AT01-TRANG-M", 179000, 10)],
      },
    ],
    { content: [contentProduct], categories, shopIdByBackend: new Map([["u-1", "shop-bach-dang"]]) }
  );
  assert.equal(products.length, 1);
  assert.equal(products[0].skus[0].stock, 0);
  assert.equal(products[0].skus[0].skuId, "s-oos");
});

test("buildCatalogLookups and find helpers resolve by slug, id, backendId and skuId", () => {
  const shop = { ...contentShop, backendId: "u-1" };
  const prod = {
    ...contentProduct,
    backendId: "p-1",
    skus: [{ ...contentProduct.skus[0], skuId: "s-1" }],
  };
  assert.equal(findProductByParam([prod], "ao-thun-cotton-trang")?.id, "prod-at-trang");
  assert.equal(findProductByParam([prod], "p-1")?.id, "prod-at-trang");
  assert.equal(findShopByParam([shop], "xuong-det-bach-dang")?.id, "shop-bach-dang");
  assert.equal(findShopByParam([shop], "u-1")?.id, "shop-bach-dang");
  const lookups = buildCatalogLookups([prod], [shop]);
  assert.equal(lookups.product("p-1")?.slug, "ao-thun-cotton-trang");
  assert.equal(lookups.shop("u-1")?.slug, "xuong-det-bach-dang");
  assert.equal(lookups.productBySkuId("s-1")?.id, "prod-at-trang");
});

test("checkLiveVoucher validates real quota, min order, expiry, shop scope and discount cap", () => {
  const now = Date.parse("2026-11-20T05:00:00Z");
  const voucher = {
    id: "v-1",
    code: "NEXUS10",
    title: "Giảm 10%",
    description: "",
    scope: "platform",
    discountType: "percent",
    discountValue: 10,
    maxDiscount: 60000,
    minOrderValue: 500000,
    expiresAt: "2026-12-01T00:00:00Z",
    remainingUses: 5,
    totalUses: 100,
  };
  assert.equal(checkLiveVoucher(voucher, { eligibleAmount: 300000, nowMs: now }).valid, false);
  assert.deepEqual(checkLiveVoucher(voucher, { eligibleAmount: 800000, nowMs: now }), {
    valid: true,
    discount: 60000,
  });
  assert.equal(
    checkLiveVoucher({ ...voucher, remainingUses: 0 }, { eligibleAmount: 800000, nowMs: now })
      .valid,
    false
  );
  assert.equal(
    checkLiveVoucher(
      { ...voucher, expiresAt: "2026-11-19T00:00:00Z" },
      { eligibleAmount: 800000, nowMs: now }
    ).valid,
    false
  );
});

test("filterStorefrontShops, mergeVouchers, and buildFlashSlots isolate storefront data from foreign E2E fixtures", () => {
  const shops = mergeShops(
    [
      { id: "u-1", name: "Xưởng Dệt Bạch Đằng", status: "ACTIVE" },
      { id: "u-new", name: "Xưởng Mới Có Hàng", status: "ACTIVE" },
      { id: "u-e2e", name: "NexusShop-seller1-deadbeef", status: "ACTIVE" },
    ],
    [contentShop]
  );
  const filteredShops = filterStorefrontShops(
    shops,
    [
      { ...contentProduct, shopId: "shop-bach-dang" },
      { ...contentProduct, id: "p-2", shopId: "u-new" },
    ],
    [contentShop]
  );
  assert.deepEqual(
    filteredShops.map((s) => s.id),
    ["shop-bach-dang", "u-new"]
  );

  const allowedShopIds = new Set(filteredShops.map((s) => s.id));
  const vouchers = mergeVouchers(
    [
      {
        id: "v-e2e",
        code: "PLA123",
        name: "E2E PLATFORM Voucher",
        scope: "PLATFORM",
        discount_type: "FIXED",
        value_vnd: 25000,
        discount_percent: 0,
        max_discount_vnd: 25000,
        min_order_vnd: 100000,
        quota: 5,
        remaining_quota: 5,
      },
      {
        id: "v-e2e-shop",
        code: "SHP123",
        name: "Shop khác",
        scope: "SHOP",
        shop_id: "u-e2e",
        discount_type: "FIXED",
        value_vnd: 10000,
        discount_percent: 0,
        max_discount_vnd: 10000,
        min_order_vnd: 0,
        quota: 5,
        remaining_quota: 5,
      },
      {
        id: "v-custom",
        code: "CUSTOM5",
        name: "Ưu đãi mới",
        scope: "PLATFORM",
        discount_type: "FIXED",
        value_vnd: 5000,
        discount_percent: 0,
        max_discount_vnd: 5000,
        min_order_vnd: 0,
        quota: 10,
        remaining_quota: 10,
      },
      {
        id: "v-known",
        code: "NEXUS30",
        name: "Giảm 30k",
        scope: "PLATFORM",
        discount_type: "FIXED",
        value_vnd: 30000,
        discount_percent: 0,
        max_discount_vnd: 30000,
        min_order_vnd: 300000,
        quota: 500,
        remaining_quota: 499,
      },
    ],
    [
      {
        code: "NEXUS30",
        title: "Giảm 30.000₫ toàn sàn",
        description: "desc",
        scope: "platform",
        discountType: "fixed",
        discountValue: 30000,
        minOrderValue: 300000,
        expiresAt: "",
        remainingUses: 500,
        totalUses: 500,
      },
    ],
    new Map([
      ["u-1", "shop-bach-dang"],
      ["u-e2e", "u-e2e"],
    ]),
    allowedShopIds
  );
  assert.deepEqual(
    vouchers.map((v) => v.code),
    ["NEXUS30", "CUSTOM5"]
  );

  const slots = buildFlashSlots(
    [
      {
        id: "fs-e2e",
        name: "E2E Mega Flash Sale deadbeef",
        starts_at: "2026-11-20T05:00:00Z",
        ends_at: "2026-11-20T08:00:00Z",
        items: [
          {
            id: "i-e2e",
            sku_id: "foreign-sku",
            flash_price_vnd: 1000,
            total_slots: 5,
            remaining_slots: 5,
            per_user_limit: 1,
            status: "APPROVED",
          },
        ],
      },
    ],
    [{ ...contentProduct, skus: [{ ...contentProduct.skus[0], skuId: "s-1" }] }],
    Date.parse("2026-11-20T05:30:00Z")
  );
  assert.equal(slots.length, 0);
});

test("createTtlCache deduplicates in-flight calls, respects TTL, and never caches errors", async () => {
  const c = createTtlCache(1000);
  let calls = 0;

  // Concurrent calls share a single loader invocation
  const [a, b] = await Promise.all([
    c.get(async () => {
      calls += 1;
      return "v1";
    }, 100),
    c.get(async () => {
      calls += 1;
      return "v1-dup";
    }, 100),
  ]);
  assert.equal(a, "v1");
  assert.equal(b, "v1");
  assert.equal(calls, 1);

  // Within TTL: served from cache
  assert.equal(
    await c.get(async () => {
      calls += 1;
      return "v2";
    }, 900),
    "v1"
  );
  assert.equal(calls, 1);

  // Expired: error is NOT cached, next call succeeds
  await assert.rejects(
    c.get(async () => {
      calls += 1;
      throw new Error("gw down");
    }, 1200),
    /gw down/
  );
  assert.equal(calls, 2);

  assert.equal(
    await c.get(async () => {
      calls += 1;
      return "v3";
    }, 1201),
    "v3"
  );
  assert.equal(calls, 3);
});

test("W3: resolveSiteProductEntry falls back to live slug/id lookup for newly approved or off-page products while isolating shops", async () => {
  const freshGwProduct = {
    id: "p-fresh-1",
    shop_id: "u-1",
    category_id: "cat-sub",
    name: "Áo Mới Duyệt",
    slug: "ao-moi-duyet",
    images: ["/products/new.png"],
    status: "APPROVED",
    skus: [sku("sku-fresh-1", "NEW-M", 250000, 12, { color: "den", color_name: "Đen", size: "M" })],
  };
  const foreignGwProduct = {
    ...freshGwProduct,
    id: "p-foreign",
    shop_id: "u-other",
    slug: "ao-shop-khac",
  };
  const hiddenGwProduct = {
    ...freshGwProduct,
    id: "p-hidden",
    slug: "ao-bi-an",
    status: "HIDDEN",
  };
  const catsWithSub = [
    { id: "cat-1", name: "Áo thun", slug: "ao-thun" },
    { id: "cat-sub", name: "Cotton Compact", slug: "cotton-compact", parent_id: "cat-1" },
  ];
  const baseOpts = {
    bundleProducts: [],
    shopId: "u-1",
    shopIdByBackend: new Map([["u-1", "shop-bach-dang"]]),
    contentProducts: [contentProduct],
    contentCategories,
    fetchCategories: async () => catsWithSub,
  };

  // 1. Newly approved product missing from cached bundle resolves via slug endpoint
  const bySlug = await resolveSiteProductEntry({
    ...baseOpts,
    slugOrId: "ao-moi-duyet",
    fetchBySlug: async (slug) => (slug === "ao-moi-duyet" ? { product: freshGwProduct } : null),
    fetchById: async () => ({ status: "not_found" }),
  });
  assert.equal(bySlug?.kind, "product");
  assert.equal(bySlug?.product.backendId, "p-fresh-1");
  assert.equal(bySlug?.product.slug, "ao-moi-duyet");
  assert.equal(bySlug?.product.shopId, "shop-bach-dang");

  // 2. Resolves via ID fallback when slug lookup misses
  const byId = await resolveSiteProductEntry({
    ...baseOpts,
    slugOrId: "p-fresh-1",
    fetchBySlug: async () => null,
    fetchById: async (id) =>
      id === "p-fresh-1" ? { status: "ok", product: freshGwProduct } : { status: "not_found" },
  });
  assert.equal(byId?.kind, "product");
  assert.equal(byId?.product.backendId, "p-fresh-1");

  // 3. Rejects foreign shop product even when gateway returns it
  const foreign = await resolveSiteProductEntry({
    ...baseOpts,
    slugOrId: "ao-shop-khac",
    fetchBySlug: async () => ({ product: foreignGwProduct }),
    fetchById: async () => ({ status: "not_found" }),
  });
  assert.equal(foreign, null);

  // 4. Rejects HIDDEN / non-sellable product
  const hidden = await resolveSiteProductEntry({
    ...baseOpts,
    slugOrId: "ao-bi-an",
    fetchBySlug: async () => ({ product: hiddenGwProduct }),
    fetchById: async () => ({ status: "not_found" }),
  });
  assert.equal(hidden, null);

  // 5. Discontinued stub is fail-closed (Item 4): requires shop_id matching opts.shopId
  const discOwn = await resolveSiteProductEntry({
    ...baseOpts,
    slugOrId: "old-slug",
    fetchBySlug: async () => ({
      discontinued: true,
      shop_id: "u-1",
      category_id: "cat-1",
      name: "Mẫu cũ",
    }),
    fetchById: async () => ({ status: "not_found" }),
  });
  assert.deepEqual(discOwn, { kind: "discontinued", discontinuedName: "Mẫu cũ" });

  // Missing shop_id in gateway discontinued stub -> fail-closed 404 (null)
  const discMissingShop = await resolveSiteProductEntry({
    ...baseOpts,
    slugOrId: "ao-khoac-shop-b",
    fetchBySlug: async () => ({
      discontinued: true,
      category_id: "cat-1",
      name: "Áo khoác Shop B",
    }),
    fetchById: async () => ({ status: "not_found" }),
  });
  assert.equal(discMissingShop, null);

  // Foreign shop_id in gateway discontinued stub (even without product object) -> 404 (null)
  const discForeignStub = await resolveSiteProductEntry({
    ...baseOpts,
    slugOrId: "ao-khoac-shop-b",
    fetchBySlug: async () => ({
      discontinued: true,
      shop_id: "u-other",
      category_id: "cat-1",
      name: "Áo khoác Shop B",
    }),
    fetchById: async () => ({ status: "not_found" }),
  });
  assert.equal(discForeignStub, null);

  const discForeign = await resolveSiteProductEntry({
    ...baseOpts,
    slugOrId: "foreign-old",
    fetchBySlug: async () => ({
      discontinued: true,
      name: "Mẫu shop khác",
      product: foreignGwProduct,
    }),
    fetchById: async () => ({ status: "not_found" }),
  });
  assert.equal(discForeign, null);
});

test("W4: resolveFreshShopFlashSales fetches only this shop's approved flash SKUs fresh (no N+1), hides deleted/hidden items, and isolates other shops", async () => {
  const nowMs = Date.parse("2026-11-20T05:30:00Z");
  const flashSales = [
    {
      id: "fs-live",
      name: "E2E 5d Flash live",
      starts_at: "2026-11-20T05:00:00Z",
      ends_at: "2026-11-20T08:00:00Z",
      items: [
        {
          id: "fi-own-live",
          shop_id: "u-1",
          sku_id: "sku-own-new",
          flash_price_vnd: 199000,
          total_slots: 5,
          remaining_slots: 5,
          per_user_limit: 1,
          status: "APPROVED",
        },
        {
          id: "fi-own-deleted",
          shop_id: "u-1",
          sku_id: "sku-own-deleted",
          flash_price_vnd: 150000,
          total_slots: 5,
          remaining_slots: 5,
          per_user_limit: 1,
          status: "APPROVED",
        },
        {
          id: "fi-foreign",
          shop_id: "u-other",
          sku_id: "sku-foreign",
          flash_price_vnd: 100000,
          total_slots: 5,
          remaining_slots: 5,
          per_user_limit: 1,
          status: "APPROVED",
        },
      ],
    },
  ];

  const fetchedBatches = [];
  const res = await resolveFreshShopFlashSales({
    shopId: "u-1",
    flashSales,
    shopIdByBackend: new Map([["u-1", "shop-bach-dang"]]),
    contentProducts: [contentProduct],
    contentCategories,
    nowMs,
    fetchCategories: async () => gwCategories,
    fetchProductsBySkuIds: async (skuIds) => {
      fetchedBatches.push(skuIds);
      // Gateway omits hidden/deleted sku-own-deleted and only returns live product
      return [
        {
          id: "p-own-new",
          shop_id: "u-1",
          category_id: "cat-1",
          name: "Sản phẩm mới duyệt flash",
          slug: "sp-moi-duyet-flash",
          images: ["/products/new.png"],
          status: "APPROVED",
          skus: [sku("sku-own-new", "OWN-M", 300000, 10, { color: "den", size: "M" })],
        },
        // Defense-in-depth: even if backend returned a HIDDEN or foreign product, BFF filters it out
        {
          id: "p-hidden",
          shop_id: "u-1",
          category_id: "cat-1",
          name: "Đã ẩn",
          slug: "da-an",
          images: ["/products/new.png"],
          status: "HIDDEN",
          skus: [sku("sku-own-deleted", "DEL-M", 250000, 10, { color: "den", size: "M" })],
        },
        {
          id: "p-foreign",
          shop_id: "u-other",
          category_id: "cat-1",
          name: "Shop khác",
          slug: "shop-khac",
          images: ["/products/new.png"],
          status: "APPROVED",
          skus: [sku("sku-foreign", "FOR-M", 250000, 10, { color: "den", size: "M" })],
        },
      ];
    },
  });

  // Single bounded batch fetch containing ONLY u-1's SKU IDs (never u-other's sku-foreign)
  assert.deepEqual(fetchedBatches, [["sku-own-new", "sku-own-deleted"]]);
  assert.equal(res.slots.length, 1);
  assert.equal(res.slots[0].items.length, 1);
  assert.equal(res.slots[0].items[0].id, "fi-own-live");
  assert.equal(res.products.length, 1);
  assert.equal(res.products[0].backendId, "p-own-new");
  assert.equal(res.products[0].isFlashSale, true);

  // Zero fetches when a shop has no approved flash items
  let emptyFetchCount = 0;
  const emptyRes = await resolveFreshShopFlashSales({
    shopId: "u-empty",
    flashSales,
    shopIdByBackend: new Map(),
    contentProducts: [contentProduct],
    contentCategories,
    nowMs,
    fetchCategories: async () => gwCategories,
    fetchProductsBySkuIds: async () => {
      emptyFetchCount += 1;
      return [];
    },
  });
  assert.equal(emptyFetchCount, 0);
  assert.deepEqual(emptyRes, { slots: [], products: [] });
});

test("Item 4: resolveSiteProductEntry fail-closes discontinued stub even when matched in cached bundle", async () => {
  const cachedProduct = {
    ...contentProduct,
    backendId: "p-cached-1",
    slug: "ao-cu-da-xoa",
    shopId: "shop-bach-dang",
  };
  const baseOpts = {
    bundleProducts: [cachedProduct],
    shopId: "u-1",
    slugOrId: "ao-cu-da-xoa",
    shopIdByBackend: new Map([["u-1", "shop-bach-dang"]]),
    contentProducts: [contentProduct],
    contentCategories,
    fetchById: async () => ({ status: "not_found" }),
    fetchCategories: async () => gwCategories,
  };

  // Missing shop_id on discontinued stub -> fail closed (null -> 404)
  const missingShop = await resolveSiteProductEntry({
    ...baseOpts,
    fetchBySlug: async () => ({ discontinued: true, category_id: "cat-1", name: "Áo cũ" }),
  });
  assert.equal(missingShop, null);

  // Matching shop_id -> discontinued
  const owned = await resolveSiteProductEntry({
    ...baseOpts,
    fetchBySlug: async () => ({
      discontinued: true,
      shop_id: "u-1",
      category_id: "cat-1",
      name: "Áo cũ",
    }),
  });
  assert.deepEqual(owned, { kind: "discontinued", discontinuedName: "Áo cũ" });
});

test("Item 5: buildFlashSlots preserves per-SKU flashPrice, remainingQuota, limitPerBuyer and listFlashSaleSkuOptions never falls back to another SKU", () => {
  const multiSkuProduct = {
    ...contentProduct,
    id: "p-multi",
    colors: [
      { code: "trang", name: "Trắng", hex: "#fff", image: "/products/a.png" },
      { code: "den", name: "Đen", hex: "#111", image: "/products/b.png" },
    ],
    sizes: ["S", "M", "L"],
    skus: [
      { skuId: "sku-trang-s", skuCode: "P-TRANG-S", colorCode: "trang", size: "S", stock: 10, price: 219000 },
      { skuId: "sku-trang-m", skuCode: "P-TRANG-M", colorCode: "trang", size: "M", stock: 10, price: 239000 },
      { skuId: "sku-den-l", skuCode: "P-DEN-L", colorCode: "den", size: "L", stock: 8, price: 259000 },
    ],
  };
  const now = Date.parse("2026-11-26T06:00:00Z");
  const slots = buildFlashSlots(
    [
      {
        id: "fs-live",
        starts_at: "2026-11-26T05:00:00Z",
        ends_at: "2026-11-26T08:00:00Z",
        items: [
          {
            id: "fsi-s",
            shop_id: "u-1",
            sku_id: "sku-trang-s",
            flash_price_vnd: 149000,
            original_price_vnd: 219000,
            total_slots: 10,
            remaining_slots: 2,
            per_user_limit: 1,
            status: "APPROVED",
          },
          {
            id: "fsi-den-l",
            shop_id: "u-1",
            sku_id: "sku-den-l",
            flash_price_vnd: 179000,
            original_price_vnd: 259000,
            total_slots: 15,
            remaining_slots: 7,
            per_user_limit: 3,
            status: "APPROVED",
          },
        ],
      },
    ],
    [multiSkuProduct],
    now
  );

  assert.equal(slots.length, 1);
  const flashItem = slots[0].items[0];
  // Per-SKU lookup returns exact price/quota/limit for each registered SKU
  assert.deepEqual(getFlashSaleSkuDetail(flashItem, "sku-trang-s"), {
    id: "fsi-s",
    skuId: "sku-trang-s",
    flashPrice: 149000,
    originalPrice: 219000,
    totalQuota: 10,
    remainingQuota: 2,
    limitPerBuyer: 1,
  });
  assert.deepEqual(getFlashSaleSkuDetail(flashItem, "sku-den-l"), {
    id: "fsi-den-l",
    skuId: "sku-den-l",
    flashPrice: 179000,
    originalPrice: 259000,
    totalQuota: 15,
    remainingQuota: 7,
    limitPerBuyer: 3,
  });
  // Unregistered SKU returns null (never falls back to flashItem.id!)
  assert.equal(getFlashSaleSkuDetail(flashItem, "sku-trang-m"), null);

  // listFlashSaleSkuOptions returns ONLY flash-registered SKUs across all colors
  const options = listFlashSaleSkuOptions(multiSkuProduct, flashItem);
  assert.deepEqual(
    options.map((o) => [o.skuId, o.detail.id, o.detail.flashPrice]),
    [
      ["sku-trang-s", "fsi-s", 149000],
      ["sku-den-l", "fsi-den-l", 179000],
    ]
  );
  assert.ok(options[0].label.includes("Trắng"));
  assert.ok(options[1].label.includes("Đen"));
});

test("Item 7: custom categories and arbitrary/missing sizes appear across shop catalog, PDP, and flash sale", async () => {
  const gwCatsCustom = [
    { id: "cat-1", name: "Áo thun", slug: "ao-thun" },
    { id: "cat-shoes", name: "Giày chạy bộ", slug: "giay-chay-bo" },
    { id: "cat-acc", name: "Phụ kiện công nghệ", slug: "phu-kien-cong-nghe" },
  ];
  const rawShopProducts = [
    {
      id: "p-shoe-1",
      shop_id: "u-1",
      category_id: "cat-shoes",
      name: "Giày chạy đường dài",
      slug: "giay-chay-duong-dai",
      images: ["/products/shoe.png"],
      status: "APPROVED",
      skus: [
        sku("sku-42", "SHOE-42", 890000, 6, { color: "xam", color_name: "Xám", size: "42" }),
        sku("sku-40", "SHOE-40", 890000, 3, { color: "xam", color_name: "Xám", size: "40" }),
      ],
    },
    {
      id: "p-bottle-1",
      shop_id: "u-1",
      category_id: "cat-acc",
      name: "Bình nước thể thao",
      slug: "binh-nuoc-the-thao",
      images: [],
      status: "APPROVED",
      skus: [sku("sku-bottle", "BOTTLE-500", 129000, 12, {})],
    },
  ];

  // 1. extendCategoryIndexForShopProducts preserves real custom category slugs and names
  const baseIdx = mergeCategories(gwCatsCustom, contentCategories);
  const extendedIdx = extendCategoryIndexForShopProducts(baseIdx, gwCatsCustom, rawShopProducts);
  assert.equal(extendedIdx.slugById.get("cat-shoes"), "giay-chay-bo");
  assert.equal(extendedIdx.slugById.get("cat-acc"), "phu-kien-cong-nghe");
  assert.ok(extendedIdx.categories.some((c) => c.slug === "giay-chay-bo" && c.name === "Giày chạy bộ"));
  assert.ok(
    extendedIdx.categories.some((c) => c.slug === "phu-kien-cong-nghe" && c.name === "Phụ kiện công nghệ")
  );

  // 2. mergeProducts maps both custom-category + numeric-size and sizeless + imageless products
  const mapped = mergeProducts(rawShopProducts, {
    content: [contentProduct],
    categories: extendedIdx,
    shopIdByBackend: new Map([["u-1", "shop-bach-dang"]]),
  });
  assert.equal(mapped.length, 2);
  const shoe = mapped.find((p) => p.backendId === "p-shoe-1");
  const bottle = mapped.find((p) => p.backendId === "p-bottle-1");
  assert.ok(shoe);
  assert.equal(shoe.categorySlug, "giay-chay-bo");
  assert.equal(shoe.categoryName, "Giày chạy bộ");
  assert.deepEqual(shoe.sizes, ["40", "42"]);
  assert.ok(bottle);
  assert.equal(bottle.categorySlug, "phu-kien-cong-nghe");
  assert.deepEqual(bottle.sizes, ["Mặc định"]);
  assert.ok(bottle.image.length > 0, "imageless approved product must get safe fallback image");

  // 3. PDP resolution works for custom-category + arbitrary-size product
  const pdpResolved = await resolveSiteProductEntry({
    bundleProducts: [],
    shopId: "u-1",
    slugOrId: "giay-chay-duong-dai",
    shopIdByBackend: new Map([["u-1", "shop-bach-dang"]]),
    contentProducts: [contentProduct],
    contentCategories,
    fetchBySlug: async () => ({ product: rawShopProducts[0] }),
    fetchById: async () => ({ status: "not_found" }),
    fetchCategories: async () => gwCatsCustom,
  });
  assert.equal(pdpResolved?.kind, "product");
  assert.equal(pdpResolved?.product.categorySlug, "giay-chay-bo");
  assert.deepEqual(pdpResolved?.product.sizes, ["40", "42"]);

  // 4. Flash sale resolution works for custom-category + sizeless product
  const nowMs = Date.parse("2026-11-26T06:00:00Z");
  const flashRes = await resolveFreshShopFlashSales({
    shopId: "u-1",
    flashSales: [
      {
        id: "fs-custom",
        starts_at: "2026-11-26T05:00:00Z",
        ends_at: "2026-11-26T08:00:00Z",
        items: [
          {
            id: "fsi-bottle",
            shop_id: "u-1",
            sku_id: "sku-bottle",
            flash_price_vnd: 99000,
            total_slots: 10,
            remaining_slots: 10,
            per_user_limit: 2,
            status: "APPROVED",
          },
        ],
      },
    ],
    shopIdByBackend: new Map([["u-1", "shop-bach-dang"]]),
    contentProducts: [contentProduct],
    contentCategories,
    nowMs,
    fetchCategories: async () => gwCatsCustom,
    fetchProductsBySkuIds: async () => [rawShopProducts[1]],
  });
  assert.equal(flashRes.products.length, 1);
  assert.equal(flashRes.slots.length, 1);
  assert.equal(flashRes.slots[0].items[0].skuDetailsBySku?.["sku-bottle"]?.flashPrice, 99000);

  // 5. Verify sites.ts wires extendCategoryIndexForShopProducts in loadShopSiteRenderData
  //    and FlashSaleClient/SitePdpView use per-SKU helpers without cross-SKU fallback.
  const webSrc = resolve(fileURLToPath(new URL("../src", import.meta.url)));
  const sitesSrc = readFileSync(resolve(webSrc, "lib/server/sites.ts"), "utf8");
  assert.ok(
    sitesSrc.includes("extendCategoryIndexForShopProducts("),
    "sites.ts loadShopSiteRenderData must extend categories for custom shop products"
  );
  const flashClientSrc = readFileSync(
    resolve(webSrc, "app/(site)/s/[host]/flash-sale/FlashSaleClient.tsx"),
    "utf8"
  );
  assert.ok(flashClientSrc.includes("listFlashSaleSkuOptions"));
  assert.equal(
    flashClientSrc.includes("?? item.id"),
    false,
    "FlashSaleClient must never fall back to product-level item.id for unregistered SKUs"
  );
  const pdpSrc = readFileSync(resolve(webSrc, "features/site/SitePdpView.tsx"), "utf8");
  assert.ok(pdpSrc.includes("getFlashSaleSkuDetail"));
});
