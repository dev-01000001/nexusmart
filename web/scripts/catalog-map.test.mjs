// Unit tests for src/lib/catalog-map.ts (pure mapping, no I/O).
// Run: node --test scripts/catalog-map.test.mjs   (Node >= 22.18 strips TS types)
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildFlashSlots,
  markFlashProducts,
  mergeCategories,
  mergeProducts,
  mergeShops,
  mergeVouchers,
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

test("mergeProducts: unknown product built from attributes, invalid sizes skipped", () => {
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
          sku("n-2", "NEW-FREE", 99000, 4, { color: "den", size: "Free" }),
        ],
      },
    ],
    { content: [contentProduct], categories, shopIdByBackend: new Map() }
  );
  assert.equal(p.id, "p-new");
  assert.equal(p.slug, "p-new");
  assert.equal(p.shopId, "u-2");
  assert.deepEqual(p.sizes, ["M"]);
  assert.equal(p.skus.length, 1);
  assert.equal(p.colors[0].name, "Đen");
  assert.equal(p.price, 99000);
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
    [{ code: "NEXUS10", title: "Giảm 10%", description: "copy", scope: "platform", discountType: "percent", discountValue: 15, minOrderValue: 1, expiresAt: "", remainingUses: 1, totalUses: 1 }],
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
          { id: "i-1", sku_id: "s-1", flash_price_vnd: 149000, total_slots: 10, remaining_slots: 4, per_user_limit: 2, status: "APPROVED" },
          { id: "i-2", sku_id: "s-2", flash_price_vnd: 139000, total_slots: 5, remaining_slots: 5, per_user_limit: 1, status: "APPROVED" },
          { id: "i-3", sku_id: "s-x", flash_price_vnd: 1, total_slots: 1, remaining_slots: 1, per_user_limit: 1, status: "APPROVED" },
          { id: "i-4", sku_id: "s-1", flash_price_vnd: 1, total_slots: 1, remaining_slots: 1, per_user_limit: 1, status: "PENDING" },
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
});
