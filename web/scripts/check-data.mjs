#!/usr/bin/env node
// Verification suite for NexusMart foundation data integrity (Wave 1 + Wave 2):
// VND formatting, 34-province 2-level model, 18 products & Wave 2 metadata,
// cart/voucher/tier/gift-card/coin math, flash sale & shock deals, order totals & retry sample,
// membership/reviews/QAs/gift-cards, content/CSR statement reconciliation, search & sizing helpers,
// and seller/admin double-entry ledgers.

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
const { VOUCHERS, calculateCartSummary, validateVoucher } = await import("../src/data/vouchers.ts");
const { FLASH_SALE_SLOTS, SHOCK_DEALS, COMBO_OFFERS, FLASH_SALE_RULES } =
  await import("../src/data/flash-sale.ts");
const { INITIAL_ORDERS, ORDER_STATUS_META } = await import("../src/data/orders.ts");
const {
  ANNOUNCEMENT_BAR,
  MEGA_MENU_GROUPS,
  TRENDING_KEYWORDS,
  COLLECTIONS,
  JOURNAL_ARTICLES,
  CAMPAIGNS,
  HELP_FAQ_ITEMS,
} = await import("../src/data/content.ts");
const {
  MEMBERSHIP_TIERS,
  INITIAL_MEMBERSHIP_STATE,
  INITIAL_GIFT_CARDS,
  GIFT_CARD_DENOMINATIONS,
  GIFT_CARD_POLICY,
  EDU_DISCOUNT_CONFIG,
  SAMPLE_REVIEWS,
  SAMPLE_QAS,
  computeReviewSummary,
  getTierBySpend,
} = await import("../src/data/membership.ts");
const { normalizeVietnamese, searchProducts, suggestSpelling } =
  await import("../src/lib/search.ts");
const { recommendSize, estimateDelivery } = await import("../src/lib/sizing.ts");
const { SELLER_WORKSPACES, computeLowStockAlerts } = await import("../src/data/seller.ts");
const {
  ADMIN_SHOP_APPROVALS,
  ADMIN_DISPUTES,
  ADMIN_RECONCILIATION,
  ADMIN_AUDIT_LOGS,
  computeReconciliationSummary,
  computeAdminDailySeries,
} = await import("../src/data/admin.ts");
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
  assert.equal(formatDateTimeVi("2026-11-22T17:30:00Z"), "00:30 · 23/11/2026");
  assert.equal(formatDateVi("2026-11-22T17:30:00Z"), "23/11/2026");
  assert.equal(formatDayMonthVi("2026-11-22T17:30:00Z"), "23/11");
  assert.equal(formatTimeHmVn("2026-11-22T17:30:00Z"), "00:30");
  assert.equal(formatShortTimeDateVi("2026-11-22T17:30:00Z"), "00:30 · 23/11");
  assert.equal(formatVnDayKey("2026-11-22T17:30:00Z"), "2026-11-23");
  assert.equal(formatDateTimeVi("2026-11-22T17:00:00Z"), "00:00 · 23/11/2026");
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

// 4. Cart + multi-shop + vouchers + coins math (Wave 1 backward-compatible contract)
check("Cart multi-shop voucher, freeship, and coin cap reconciliation", () => {
  assert.ok(VOUCHERS.length >= 8);
  const summary = calculateCartSummary({
    items: [
      {
        skuCode: "BD-AT01-TRANG-L",
        productId: "prod-at-trang",
        productSlug: "ao-thun-cotton-compact-trang",
        productName: "Áo thun Cotton Compact 220gsm Trắng",
        shopId: "shop-bach-dang",
        colorCode: "trang",
        colorName: "Trắng tinh",
        size: "L",
        image: "/products/ao-thun-cotton-trang.png",
        unitPrice: 179000,
        quantity: 2,
        maxStock: 26,
        selected: true,
      },
      {
        skuCode: "DN-PL06-KEM-L",
        productId: "prod-polo-kem",
        productSlug: "ao-polo-pique-cotton-kem-nhat",
        productName: "Áo Polo Pique Cotton Kem nhạt",
        shopId: "shop-da-nang",
        colorCode: "kem",
        colorName: "Kem nhạt",
        size: "L",
        image: "/products/ao-polo-pique-kem.png",
        unitPrice: 289000,
        quantity: 1,
        maxStock: 23,
        selected: true,
      },
    ],
    provinceCode: "HCM",
    selectedShopVouchers: { "shop-bach-dang": "BACHDANG20" },
    platformVoucherCode: "NEXUS30",
    freeshipVoucherCode: "FREESHIP25",
    useCoins: true,
    availableCoins: 42000,
  });

  assert.equal(summary.subtotal, 647000);
  assert.equal(summary.shippingFee, 34000);
  assert.equal(summary.shopVoucherTotal, 20000);
  assert.equal(summary.platformVoucherDiscount, 30000);
  assert.equal(summary.freeshipDiscount, 25000);
  assert.equal(summary.coinDiscount, 42000);
  assert.equal(
    summary.totalDiscount,
    summary.shopVoucherTotal +
      summary.platformVoucherDiscount +
      summary.freeshipDiscount +
      summary.coinDiscount
  );
  assert.equal(summary.grandTotal, summary.subtotal + summary.shippingFee - summary.totalDiscount);
  assert.equal(summary.grandTotal, 564000);
});

// 5. Flash sale quotas, shock deals, combos and product references
check("Flash sale slots, shock deals, combos, and prices reconcile", () => {
  assert.equal(FLASH_SALE_SLOTS.length, 4);
  assert.equal(FLASH_SALE_RULES.maxItemsPerOrder, 3);
  assert.equal(FLASH_SALE_RULES.shockDealMaxPerOrder, 1);
  assert.equal(FLASH_SALE_RULES.shockDealMaxPerDay, 2);

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

  assert.ok(SHOCK_DEALS.length >= 4);
  for (const deal of SHOCK_DEALS) {
    assert.ok(prodIds.has(deal.productId), `Unknown shock deal product ${deal.productId}`);
    assert.equal(deal.limitPerOrder, 1);
    assert.equal(deal.limitPerDay, 2);
    assert.equal(deal.returnEligible, false);
    assert.ok(Number.isInteger(deal.dealPrice) && deal.dealPrice < deal.originalPrice);
  }

  assert.ok(COMBO_OFFERS.length >= 3);
  for (const combo of COMBO_OFFERS) {
    assert.ok(combo.productIds.every((id) => prodIds.has(id)));
    assert.ok(Number.isInteger(combo.comboPrice) && combo.comboPrice < combo.originalTotalPrice);
  }
});

// 6. Orders state coverage, retry payment sample & exact total math
check("Orders cover all 6 statuses + retry payment sample and totals reconcile", () => {
  const seenStatuses = new Set(INITIAL_ORDERS.map((o) => o.status));
  for (const st of Object.keys(ORDER_STATUS_META)) {
    assert.ok(seenStatuses.has(st), `Missing order sample for status ${st}`);
  }
  const pendingPay = INITIAL_ORDERS.find((o) => o.paymentStatus === "cho_thanh_toan");
  assert.ok(pendingPay, "Must include a sample order with paymentStatus cho_thanh_toan");
  assert.equal(pendingPay.isPaid, false);

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

// 7. Seller operational metrics & double-entry ledger reconciliation
check("Seller daily KPIs, order receivables, and double-entry ledger reconcile", () => {
  for (const shop of SHOPS) {
    const ws = SELLER_WORKSPACES[shop.id];
    assert.ok(ws, `Missing workspace for ${shop.id}`);

    const sumGmv = ws.daily7d.reduce((s, d) => s + d.gmv, 0);
    const sumOrders = ws.daily7d.reduce((s, d) => s + d.orders, 0);
    const sumNet = ws.daily7d.reduce((s, d) => s + d.netRevenue, 0);
    assert.equal(ws.total7dGmv, sumGmv);
    assert.equal(ws.total7dOrders, sumOrders);
    assert.equal(ws.total7dNetRevenue, sumNet);
    assert.equal(ws.gmvDelta, ws.total7dGmv - ws.prior7dGmv);
    assert.equal(ws.ordersDelta, ws.total7dOrders - ws.prior7dOrders);

    for (const ord of ws.orders) {
      const taxable = ord.subtotal - ord.shopVoucherDiscount;
      assert.equal(ord.netReceivable, taxable - ord.platformFee);
    }

    const totalCredits = ws.ledger.reduce((s, e) => s + e.credit, 0);
    const totalDebits = ws.ledger.reduce((s, e) => s + e.debit, 0);
    assert.equal(
      ws.availableBalance + ws.pendingBalance,
      totalCredits - totalDebits,
      `Double-entry ledger imbalance on ${shop.id}`
    );
  }

  const bd = SELLER_WORKSPACES["shop-bach-dang"];
  for (const d of bd.daily7d) {
    assert.equal(d.netRevenue, Math.round(d.gmv * 0.95) - d.refundAmount);
  }
  assert.ok(computeLowStockAlerts("shop-bach-dang").length > 0);
});

// 8. Admin reconciliation & queues
check("Admin reconciliation double-entry math and queues reconcile", () => {
  assert.ok(ADMIN_SHOP_APPROVALS.length >= 6);
  assert.ok(ADMIN_DISPUTES.length >= 6);
  assert.ok(ADMIN_AUDIT_LOGS.length >= 10);

  for (const row of ADMIN_RECONCILIATION) {
    assert.equal(
      row.sellerPayable,
      row.grossCollected - row.gatewayFee - row.platformCommission - row.refunded,
      `Reconciliation row mismatch on ${row.batchCode}`
    );
  }
  const summary = computeReconciliationSummary(ADMIN_RECONCILIATION);
  assert.equal(
    summary.totalSellerPayable,
    summary.totalGross - summary.totalGatewayFee - summary.totalCommission - summary.totalRefunded
  );
  assert.equal(summary.flaggedBatches, 1);
  assert.equal(summary.totalDiscrepancy, 149000);

  const daily = computeAdminDailySeries(ADMIN_RECONCILIATION);
  assert.equal(
    daily.reduce((s, d) => s + d.grossCollected, 0),
    summary.totalGross
  );
  assert.equal(
    daily.reduce((s, d) => s + d.sellerPayable, 0),
    summary.totalSellerPayable
  );
});

// 9. Wave 2 product metadata, combos, non-returnable exceptions, shop factory profiles & assets
check("Wave 2 product metadata, combos, gallery paths, and shop profiles", () => {
  const prodIds = new Set(PRODUCTS.map((p) => p.id));
  const salesRanks = new Set();
  let comboCount = 0;
  let nonReturnableCount = 0;
  let videoCount = 0;

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

    if (p.gender === "unisex") {
      assert.ok(
        p.modelInfo.some((m) => m.gender === "nam") && p.modelInfo.some((m) => m.gender === "nu"),
        `Unisex product ${p.id} should include both male and female modelInfo`
      );
    }
    if (p.isCombo) {
      comboCount += 1;
      assert.ok(p.comboSlots && p.comboSlots.length >= 2, `Combo ${p.id} needs comboSlots`);
    }
    if (p.returnEligible === false) {
      nonReturnableCount += 1;
      assert.ok(p.returnExclusionReason, `Non-returnable ${p.id} needs reason`);
    }
    if (p.hasVideo) {
      videoCount += 1;
      assert.ok(existsSync(resolve(PUBLIC_DIR, p.videoUrl.replace(/^\//, ""))));
    }
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
  assert.ok(comboCount >= 2, "At least 2 multi-size combo products");
  assert.ok(nonReturnableCount >= 2, "At least 2 non-returnable outlet/sealed pack samples");
  assert.ok(videoCount >= 4, "At least 4 products with video preview");

  for (const s of SHOPS) {
    assert.equal(s.returnDays, 45, `Shop ${s.id} must have 45-day return policy`);
    assert.ok(Number.isInteger(s.freeShipThreshold) && s.freeShipThreshold >= 199000);
    assert.ok(s.factoryProfile && s.factoryProfile.qcProcess.length >= 3);
    assert.ok(s.sizeCharts && s.sizeCharts.top.length === 5 && s.sizeCharts.bottom.length === 5);
  }
});

// 10. Wave 2 tier discounts, voucher validation reasons & gift card cart math
check("Wave 2 tier discounts, voucher validation reasons, and gift cards reconcile", () => {
  const res = calculateCartSummary({
    items: [
      {
        skuCode: "BD-AT01-TRANG-L",
        productId: "prod-at-trang",
        productSlug: "ao-thun-cotton-compact-trang",
        productName: "Áo thun Cotton Compact 220gsm Trắng",
        shopId: "shop-bach-dang",
        colorCode: "trang",
        colorName: "Trắng tinh",
        size: "L",
        image: "/products/ao-thun-cotton-trang.png",
        unitPrice: 179000,
        quantity: 3,
        maxStock: 26,
        selected: true,
      },
    ],
    provinceCode: "HCM",
    selectedShopVouchers: { "shop-bach-dang": "BACHDANG20" },
    platformVoucherCode: "NEXUS30",
    freeshipVoucherCode: "FREESHIP25",
    useCoins: false,
    availableCoins: 42000,
    applyTierDiscounts: true,
    giftCardDiscount: 100000,
    birthdayGiftEligible: true,
  });

  // 3 * 179000 = 537000; 15% tier discount = floor(537000 * 0.15) = 80550
  assert.equal(res.subtotal, 537000);
  assert.equal(res.tierDiscountTotal, 80550);
  assert.equal(res.giftCardDiscount, 100000);
  assert.equal(res.freeGifts.length, 2); // order threshold + birthday gift
  assert.equal(res.grandTotal, res.subtotal + res.shippingFee - res.totalDiscount);

  const eduReject = validateVoucher("EDU15", { eligibleAmount: 300000, eduVerified: false });
  assert.equal(eduReject.valid, false);
  assert.ok(eduReject.reason && eduReject.reason.includes(".edu"));

  const minReject = validateVoucher("NEXUS30", { eligibleAmount: 200000 });
  assert.equal(minReject.valid, false);
  assert.ok(minReject.reason && minReject.reason.includes("150.000₫"));
});

// 11. Membership (Hội viên Nexus), reviews, QAs & gift cards
check("Hội viên Nexus tiers, sample reviews/QAs, and gift cards", () => {
  assert.equal(MEMBERSHIP_TIERS.length, 4);
  assert.deepEqual(
    MEMBERSHIP_TIERS.map((t) => t.minSpend4Q),
    [0, 1000000, 3000000, 6000000]
  );
  assert.deepEqual(
    MEMBERSHIP_TIERS.map((t) => t.cashbackRate),
    [0.01, 0.02, 0.04, 0.06]
  );
  assert.equal(getTierBySpend(INITIAL_MEMBERSHIP_STATE.spend4Q).id, "bac");

  const prodIds = new Set(PRODUCTS.map((p) => p.id));
  assert.ok(SAMPLE_REVIEWS.length >= 10);
  for (const rev of SAMPLE_REVIEWS) {
    assert.ok(prodIds.has(rev.productId), `Invalid review productId ${rev.productId}`);
    assert.equal(rev.isSampleData, true);
    for (const img of rev.images) {
      assert.ok(existsSync(resolve(PUBLIC_DIR, img.replace(/^\//, ""))));
    }
  }
  const revSum = computeReviewSummary(SAMPLE_REVIEWS, "prod-at-trang");
  assert.equal(revSum.count, 3);
  assert.ok(revSum.average >= 4.0);

  assert.ok(SAMPLE_QAS.length >= 4);
  assert.ok(GIFT_CARD_DENOMINATIONS.every((v) => Number.isInteger(v) && v >= 100000));
  assert.ok(GIFT_CARD_POLICY.validityDays < 365);
  assert.ok(INITIAL_GIFT_CARDS.every((gc) => Number.isInteger(gc.remainingVnd)));
  assert.equal(EDU_DISCOUNT_CONFIG.voucherCode, "EDU15");
});

// 12. Content, CSR Sợi Xanh statement math, non-diacritic search & per-shop sizing
check("Content, CSR Sợi Xanh statement math, search synonyms, and per-shop sizing", () => {
  const prodIds = new Set(PRODUCTS.map((p) => p.id));
  assert.ok(ANNOUNCEMENT_BAR.length >= 3);
  assert.equal(MEGA_MENU_GROUPS.length, 4);
  assert.ok(TRENDING_KEYWORDS.length >= 6);
  assert.ok(COLLECTIONS.length >= 5);
  assert.equal(JOURNAL_ARTICLES.length, 4);
  for (const art of JOURNAL_ARTICLES) {
    assert.ok(art.embeddedProductIds.length >= 3);
    assert.ok(art.embeddedProductIds.every((id) => prodIds.has(id)));
  }

  const csr = CAMPAIGNS.find((c) => c.slug === "soi-xanh-nexus");
  assert.ok(csr && csr.csrProgress);
  const statementSum = csr.csrProgress.statementRows.reduce((s, r) => s + r.amountVnd, 0);
  assert.equal(
    statementSum,
    csr.csrProgress.currentRaisedVnd,
    "CSR Sợi Xanh statement rows must sum exactly to currentRaisedVnd"
  );

  const hasPlatformFaq = HELP_FAQ_ITEMS.some((f) => f.scope === "san");
  const hasShopFaq = HELP_FAQ_ITEMS.some((f) => f.scope === "gian-hang");
  assert.ok(hasPlatformFaq && hasShopFaq);

  // Search helper tests
  assert.equal(normalizeVietnamese("Áo Thun Đen"), "ao thun den");
  assert.ok(searchProducts("ao phong").length >= 3, "Synonym ao phong must match áo thun");
  assert.ok(searchProducts("quan dui").length >= 3, "Synonym quan dui must match quần short");
  assert.ok(suggestSpelling("ao thunn"), "Typo ao thunn should yield suggestion");

  // Sizing & delivery helper tests
  const slimRec = recommendSize({
    heightCm: 171,
    weightKg: 66,
    shape: "day",
    garmentGroup: "top",
    shopId: "shop-ha-noi",
  });
  const relaxedRec = recommendSize({
    heightCm: 171,
    weightKg: 66,
    shape: "gay",
    garmentGroup: "top",
    shopId: "shop-sai-gon",
  });
  assert.ok(["S", "M", "L", "XL", "2XL"].includes(slimRec.size));
  assert.ok(["S", "M", "L", "XL", "2XL"].includes(relaxedRec.size));

  const deliv = estimateDelivery({
    shopId: "shop-sai-gon",
    provinceCode: "HCM",
    orderSubtotal: 250000,
    deliveryMethod: "fast",
  });
  assert.equal(deliv.fastAvailable, true);
  assert.equal(deliv.isPlatformFreeShipReached, true);
});

// 13. Wave 3 editorial media: onModelImage, gallery[1], category tiles, banners & workshop assets
check("Wave 3 editorial models, banners, category tiles, and workshop media exist", () => {
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

// 14. Open-redirect sanitization (dot-segment normalization, protocol-relative, absolute URLs, control chars)
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
