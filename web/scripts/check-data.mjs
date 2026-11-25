#!/usr/bin/env node
// Verification suite for NexusMart active foundation data integrity:
// VND formatting, 34-province 2-level model, 18 products & editorial media,
// voucher/flash-sale/order/seller data, Vietnamese non-diacritic search & sizing helpers,
// and open-redirect sanitization.

import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, resolve } from "node:path";
import { registerHooks } from "node:module";

// Resolve extensionless relative TypeScript imports (e.g. "./products" -> "./products.ts")
registerHooks({
  resolve(specifier, context, nextResolve) {
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

const PUBLIC_DIR = resolve(dirname(fileURLToPath(import.meta.url)), "../public");

const {
  formatVnd,
  formatSignedVnd,
  formatCompactVnd,
  formatDateVi,
  formatDateTimeVi,
  formatDayMonthVi,
  formatTimeHmVn,
  formatShortTimeDateVi,
  formatVnDayKey,
} = await import("../src/lib/format.ts");
const { PROVINCES, getProvinceByCode, getShippingFeeForProvince } =
  await import("../src/lib/provinces.ts");
const { SHOPS } = await import("../src/data/shops.ts");
const { PRODUCTS, CATEGORIES, getTotalStock } = await import("../src/data/products.ts");
const {
  MEDIA_BANNERS,
  CAMPAIGN_BANNERS,
  CATEGORY_TILES_MEDIA,
  CATEGORY_TILE_LIST,
  WORKSHOP_MEDIA,
  FABRIC_MEDIA,
  MODEL_SHOTS,
} = await import("../src/data/media.ts");
const { VOUCHERS, validateVoucher } = await import("../src/data/vouchers.ts");
const { FLASH_SALE_SLOTS, FLASH_SALE_RULES } = await import("../src/data/flash-sale.ts");
const { INITIAL_ORDERS, ORDER_STATUS_META } = await import("../src/data/orders.ts");
const { normalizeVietnamese, searchProducts, suggestSpelling } =
  await import("../src/lib/search.ts");
const { sanitizeNextPath } = await import("../src/lib/redirect.ts");

let passed = 0;
function check(name, fn) {
  fn();
  passed += 1;
  console.log(`PASS  ${name}`);
}

// 1. VND formatting
check("VND integer formatting", () => {
  assert.equal(formatVnd(199000), "199.000₫");
  assert.equal(formatVnd(0), "0₫");
  assert.equal(formatVnd(1250000), "1.250.000₫");
  assert.equal(formatVnd(-50000), "-50.000₫");
  assert.equal(formatSignedVnd(420000), "+420.000₫");
  assert.equal(formatSignedVnd(-150000), "\u2212150.000₫");
  assert.equal(formatSignedVnd(0), "0₫");
  assert.equal(formatCompactVnd(12500000), "12,5 tr₫");
  assert.equal(formatCompactVnd(450000), "450k₫");
});

// 1b. Vietnam timezone (Asia/Ho_Chi_Minh, UTC+7) deterministic date/time formatting across any host TZ
check("Vietnam timezone (Asia/Ho_Chi_Minh) date/time formatting", () => {
  assert.equal(formatDateTimeVi("2026-11-22T17:30:00Z"), "00:30, 23/11/2026");
  assert.equal(formatDateVi("2026-11-22T17:30:00Z"), "23/11/2026");
  assert.equal(formatDayMonthVi("2026-11-22T17:30:00Z"), "23/11");
  assert.equal(formatTimeHmVn("2026-11-22T17:30:00Z"), "00:30");
  assert.equal(formatShortTimeDateVi("2026-11-22T17:30:00Z"), "00:30 23/11");
  assert.equal(formatVnDayKey("2026-11-22T17:30:00Z"), "2026-11-23");
  assert.equal(formatDateTimeVi("2026-11-22T17:00:00Z"), "00:00, 23/11/2026");
  assert.equal(formatDateVi("2026-11-22"), "22/11/2026");
});

// 2. 34 Vietnamese provinces (2-level model, no districts)
check("34 Vietnamese provinces and 2-level ward structure", () => {
  assert.equal(PROVINCES.length, 34, "Must have exactly 34 provinces/cities");
  const codes = new Set(PROVINCES.map((p) => p.code));
  assert.equal(codes.size, 34, "Province codes must be unique");
  for (const p of PROVINCES) {
    assert.ok(p.wards.length >= 5, `${p.name} must list at least 5 wards/communes`);
    assert.ok(
      Number.isInteger(p.shippingFee) && p.shippingFee > 0,
      `${p.name} fee must be integer VND`
    );
    for (const w of p.wards) {
      assert.ok(
        !/^(Quận|Huyện|Thị xã)\b/i.test(w),
        `2-level address must not contain district level: ${w}`
      );
    }
  }
  assert.ok(getProvinceByCode("HCM"));
  assert.equal(getShippingFeeForProvince("HCM", 1), 22000);
  assert.equal(getShippingFeeForProvince("HCM", 2), 34000);
});

// 3. 18 products matching exact asset paths, 6 categories, 4 shops
const EXPECTED_PRODUCT_IMAGES = [
  "/products/ao-thun-cotton-trang.png",
  "/products/ao-thun-cotton-xam-da.png",
  "/products/ao-thun-cotton-xanh-than.png",
  "/products/ao-thun-dryfit-co-vit.png",
  "/products/ao-thun-oversize-than-chi.png",
  "/products/ao-polo-pique-kem.png",
  "/products/ao-polo-pique-xanh-da.png",
  "/products/ao-polo-det-kim-den.png",
  "/products/quan-short-daily-be.png",
  "/products/quan-short-run-xam-dam.png",
  "/products/quan-short-linen-xam-da.png",
  "/products/quan-jogger-terry-xam.png",
  "/products/quan-jogger-tech-den.png",
  "/products/do-lot-boxer-modal-3.png",
  "/products/do-lot-brief-tre-3.png",
  "/products/tat-cotton-co-trung-5.png",
  "/products/tat-the-thao-co-thap-4.png",
  "/products/ao-khoac-gio-xam-tro.png",
];

check("18 products, 6 categories, 4 shops, exact image contract, integer VND", () => {
  assert.equal(SHOPS.length, 4);
  assert.equal(CATEGORIES.length, 6);
  assert.equal(PRODUCTS.length, 18);

  const primaryImages = new Set(PRODUCTS.map((p) => p.image));
  for (const expectedImg of EXPECTED_PRODUCT_IMAGES) {
    assert.ok(primaryImages.has(expectedImg), `Missing product for image: ${expectedImg}`);
  }

  const shopIds = new Set(SHOPS.map((s) => s.id));
  const catSlugs = new Set(CATEGORIES.map((c) => c.slug));
  const usedCats = new Set();
  const usedShops = new Set();

  for (const p of PRODUCTS) {
    assert.ok(shopIds.has(p.shopId), `Invalid shopId on ${p.id}`);
    assert.ok(catSlugs.has(p.categorySlug), `Invalid category on ${p.id}`);
    usedCats.add(p.categorySlug);
    usedShops.add(p.shopId);
    assert.ok(Number.isInteger(p.price) && p.price > 0, `Non-integer price on ${p.id}`);
    if (p.originalPrice != null) {
      assert.ok(
        Number.isInteger(p.originalPrice) && p.originalPrice > p.price,
        `originalPrice must exceed price on ${p.id}`
      );
    }
    assert.ok(p.skus.length > 0, `Product ${p.id} must have SKUs`);
    assert.ok(getTotalStock(p) > 0, `Product ${p.id} must have positive total stock`);
    assert.equal("rating" in p, false, "No fake ratings allowed");
    assert.equal("reviewCount" in p, false, "No fake review counts allowed");
  }
  assert.equal(usedCats.size, 6, "All 6 categories must have products");
  assert.equal(usedShops.size, 4, "All 4 shops must have products");
});

// 4. Shop voucher copy & validation helpers
check("Shop voucher presentation data and validation", () => {
  const shopVouchers = VOUCHERS.filter((v) => v.scope === "shop");
  assert.ok(shopVouchers.length >= 4);
  const okShop = validateVoucher("BACHDANG20", { eligibleAmount: 300000 });
  assert.equal(okShop.valid, true);
  assert.equal(okShop.discount, 20000);
});

// 5. Flash sale quotas and product references
check("Flash sale slots and prices reconcile", () => {
  assert.equal(FLASH_SALE_SLOTS.length, 4);
  assert.equal(FLASH_SALE_RULES.maxItemsPerOrder, 3);

  const prodIds = new Set(PRODUCTS.map((p) => p.id));
  for (const slot of FLASH_SALE_SLOTS) {
    assert.ok(slot.items.length >= 3);
    for (const item of slot.items) {
      assert.ok(prodIds.has(item.productId), `Unknown product in flash sale: ${item.productId}`);
      assert.ok(
        Number.isInteger(item.flashPrice) && item.flashPrice < item.originalPrice,
        `Flash price must be integer below original on ${item.id}`
      );
      assert.ok(
        item.remainingQuota >= 0 && item.remainingQuota <= item.totalQuota,
        `Invalid quota on ${item.id}`
      );
    }
  }
});

// 6. Orders status metadata & sample totals
check("Orders cover all 6 statuses and totals reconcile", () => {
  const seenStatuses = new Set(INITIAL_ORDERS.map((o) => o.status));
  for (const st of Object.keys(ORDER_STATUS_META)) {
    assert.ok(seenStatuses.has(st), `Missing order sample for status ${st}`);
  }
  for (const order of INITIAL_ORDERS) {
    const lineSum = order.items.reduce((s, it) => s + it.unitPrice * it.quantity, 0);
    assert.equal(order.subtotal, lineSum, `Subtotal mismatch on ${order.orderCode}`);
    const expectedTotal =
      order.subtotal +
      order.shippingFee -
      order.shopVoucherDiscount -
      order.platformVoucherDiscount -
      order.freeshipDiscount -
      order.coinDiscount -
      (order.tierDiscount ?? 0) -
      (order.giftCardDiscount ?? 0);
    assert.equal(order.total, expectedTotal, `Grand total mismatch on ${order.orderCode}`);
  }
});

// 7. Product metadata, gallery paths & shop profiles
check("Product metadata, gallery paths, and shop profiles", () => {
  const prodIds = new Set(PRODUCTS.map((p) => p.id));
  const salesRanks = new Set();

  for (const p of PRODUCTS) {
    assert.ok(p.material && p.materialLabel, `Missing material on ${p.id}`);
    assert.ok(p.fitType, `Missing fitType on ${p.id}`);
    assert.ok(p.occasion && p.occasion.length > 0, `Missing occasion on ${p.id}`);
    assert.ok(p.fabricFeatures && p.fabricFeatures.length > 0, `Missing fabricFeatures on ${p.id}`);
    assert.ok(p.slaText, `Missing slaText on ${p.id}`);
    assert.ok(p.modelInfo && p.modelInfo.length > 0, `Missing modelInfo on ${p.id}`);
    assert.ok(p.faqs && p.faqs.length > 0, `Missing faqs on ${p.id}`);
    assert.ok(Number.isInteger(p.salesRank), `Missing salesRank on ${p.id}`);
    salesRanks.add(p.salesRank);

    for (const imgPath of p.gallery) {
      assert.ok(
        existsSync(resolve(PUBLIC_DIR, imgPath.replace(/^\//, ""))),
        `Missing gallery asset ${imgPath} on ${p.id}`
      );
    }
    for (const bid of p.bundleIds ?? []) {
      assert.ok(prodIds.has(bid), `Invalid bundleId ${bid} on ${p.id}`);
    }
  }

  assert.equal(salesRanks.size, 18, "salesRank must be unique across all 18 products");

  for (const s of SHOPS) {
    assert.equal(s.returnDays, 7, `Shop ${s.id} must have 7-day return policy`);
    assert.ok(Number.isInteger(s.freeShipThreshold) && s.freeShipThreshold >= 199000);
    assert.ok(s.factoryProfile && s.factoryProfile.qcProcess.length >= 3);
    assert.ok(s.sizeCharts && s.sizeCharts.top.length === 5 && s.sizeCharts.bottom.length === 5);
  }
});

// 8. Non-diacritic Vietnamese search helpers
check("Vietnamese search synonyms and typo suggestions", () => {
  assert.equal(normalizeVietnamese("Áo Thun Đen"), "ao thun den");
  assert.ok(searchProducts("ao phong").length >= 3, "Synonym ao phong must match áo thun");
  assert.ok(searchProducts("quan dui").length >= 3, "Synonym quan dui must match quần short");
  assert.ok(suggestSpelling("ao thunn"), "Typo ao thunn should yield suggestion");
});

// 10. Editorial media assets on disk
check("Editorial models, banners, category tiles, and workshop media exist", () => {
  const seenModels = new Set();
  for (const p of PRODUCTS) {
    assert.ok(p.onModelImage, `Missing onModelImage on ${p.id}`);
    assert.equal(p.gallery[1], p.onModelImage, `onModelImage must be gallery[1] on ${p.id}`);
    assert.equal(new Set(p.gallery).size, p.gallery.length, `Duplicate gallery image on ${p.id}`);
    assert.ok(
      existsSync(resolve(PUBLIC_DIR, p.onModelImage.replace(/^\//, ""))),
      `Missing onModelImage file ${p.onModelImage} on ${p.id}`
    );
    seenModels.add(p.onModelImage);
    assert.ok(MODEL_SHOTS[p.id], `Missing MODEL_SHOTS entry for ${p.id}`);
    assert.equal(MODEL_SHOTS[p.id].src, p.onModelImage);
  }
  assert.equal(seenModels.size, 18, "All 18 products must have distinct on-model shots");

  for (const cat of CATEGORIES) {
    assert.ok(
      existsSync(resolve(PUBLIC_DIR, cat.featuredImage.replace(/^\//, ""))),
      `Missing category featuredImage ${cat.featuredImage}`
    );
  }

  const allMedia = [
    ...Object.values(MEDIA_BANNERS),
    ...CAMPAIGN_BANNERS,
    ...Object.values(CATEGORY_TILES_MEDIA),
    ...CATEGORY_TILE_LIST,
    WORKSHOP_MEDIA,
    WORKSHOP_MEDIA.greenFiber,
    ...WORKSHOP_MEDIA.gallery,
    FABRIC_MEDIA,
    ...FABRIC_MEDIA.closeups,
    ...Object.values(MODEL_SHOTS),
  ];
  for (const asset of allMedia) {
    assert.ok(asset.src && asset.alt && asset.alt.length >= 10, `Invalid media asset ${asset.src}`);
    assert.ok(
      Number.isInteger(asset.width) &&
        asset.width > 0 &&
        Number.isInteger(asset.height) &&
        asset.height > 0,
      `Invalid media dimensions on ${asset.src}`
    );
    assert.ok(
      existsSync(resolve(PUBLIC_DIR, asset.src.replace(/^\//, ""))),
      `Missing media file on disk: ${asset.src}`
    );
  }
  assert.ok(existsSync(resolve(PUBLIC_DIR, WORKSHOP_MEDIA.loomVideo.src.replace(/^\//, ""))));
  assert.ok(existsSync(resolve(PUBLIC_DIR, WORKSHOP_MEDIA.loomVideo.poster.replace(/^\//, ""))));
});

// 11. Open-redirect sanitization (dot-segment normalization, protocol-relative, absolute URLs, control chars)
check("sanitizeNextPath blocks open-redirect vectors and preserves internal paths", () => {
  const malicious = [
    "/..//evil.com",
    "/.//evil.com",
    "/a/..//evil.com",
    "/%2e%2e//evil.com",
    "/%2e//evil.com",
    "/a/b/../..//evil.com",
    "//evil.com",
    "///evil.com",
    "/\\evil.com",
    "\\\\evil.com",
    "https://evil.com",
    "https://evil.com/dang-nhap",
    "http://evil.com",
    "javascript:alert(1)",
    "data:text/html,<script>alert(1)</script>",
    "evil.com",
    "/tai-khoan\r\nSet-Cookie: x=1",
    "/tai-khoan\n/evil",
    "/tai-khoan\u0000",
    "/\t//evil.com",
  ];
  for (const vector of malicious) {
    assert.equal(
      sanitizeNextPath(vector, "/"),
      "/",
      `Expected fallback "/" for open-redirect vector: ${JSON.stringify(vector)}`
    );
  }

  assert.equal(sanitizeNextPath(null, "/"), "/");
  assert.equal(sanitizeNextPath(undefined, "/"), "/");
  assert.equal(sanitizeNextPath("", "/"), "/");
  assert.equal(sanitizeNextPath("/", "/"), "/");
  assert.equal(sanitizeNextPath("/seller", "/"), "/seller");
  assert.equal(sanitizeNextPath("/admin/doi-soat", "/"), "/admin/doi-soat");
  assert.equal(
    sanitizeNextPath("/thanh-toan?step=2&voucher=NEXUS30#summary", "/"),
    "/thanh-toan?step=2&voucher=NEXUS30#summary"
  );
  assert.equal(
    sanitizeNextPath("/san-pham/ao-thun-cotton?color=trang", "/"),
    "/san-pham/ao-thun-cotton?color=trang"
  );
});

console.log(`\nALL ${passed} DATA INTEGRITY CHECKS PASSED.`);
