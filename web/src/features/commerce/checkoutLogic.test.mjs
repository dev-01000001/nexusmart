import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  extractRejectedFlashSaleItemIds,
  mapCartError,
  mapCheckoutError,
  mapVoucherError,
  resolveCheckoutRouteError,
  translatePromotionWarning,
} from "./checkoutErrors.ts";
import {
  buildCheckoutPreviewKey,
  enrichCartGroups,
  formatAddressLine,
  formatCarrierLabel,
  formatServiceLabel,
  formatVoucherHeadline,
  formatVoucherIneligibleReason,
  shortVoucherHint,
  splitVariantParts,
} from "./cartEnrich.ts";

test("translatePromotionWarning maps English promotion warnings to clean Vietnamese without raw internals", () => {
  assert.match(translatePromotionWarning("voucher ABC not found"), /không tồn tại/i);
  assert.match(translatePromotionWarning("voucher XYZ quota exhausted"), /hết lượt/i);
  assert.match(translatePromotionWarning("voucher OLD has expired"), /hết hạn/i);
  assert.match(translatePromotionWarning("per-user limit reached"), /hết lượt áp dụng/i);
  const minMsg = translatePromotionWarning("voucher requires minimum order 350000 VND");
  assert.match(minMsg, /350\.000₫/);
  assert.doesNotMatch(minMsg, /VND/);
  assert.match(translatePromotionWarning("does not belong to shop 1234"), /gian hàng/);
});

test("mapCheckoutError maps out-of-stock, COD limit, voucher exhausted, and empty cart safely", () => {
  const oos = mapCheckoutError(
    "FAILED_PRECONDITION",
    "INSUFFICIENT_STOCK: SKU abc has 0 available, requested 2"
  );
  assert.equal(oos.code, "OUT_OF_STOCK");
  assert.equal(oos.status, 409);
  assert.doesNotMatch(oos.message, /SKU|INSUFFICIENT_STOCK|abc/);

  const cod = mapCheckoutError(
    "FAILED_PRECONDITION",
    "COD_LIMIT_EXCEEDED: order total 6000000 exceeds 5000000"
  );
  assert.equal(cod.code, "COD_LIMIT_EXCEEDED");
  assert.equal(cod.status, 409);
  assert.match(cod.message, /COD/);

  const vOut = mapCheckoutError("RESOURCE_EXHAUSTED", "voucher budget exhausted");
  assert.equal(vOut.code, "VOUCHER_EXHAUSTED");
  assert.equal(vOut.status, 409);

  const empty = mapCheckoutError("FAILED_PRECONDITION", "shopping cart is empty");
  assert.equal(empty.code, "EMPTY_CART");
  assert.equal(empty.status, 409);
});

test("mapCartError and mapVoucherError return clean buyer messages and status codes", () => {
  const oos = mapCartError("FAILED_PRECONDITION", "OUT_OF_STOCK: sku 123 has 0 available stock");
  assert.equal(oos.code, "OUT_OF_STOCK");
  assert.equal(oos.status, 409);

  const exhausted = mapVoucherError("RESOURCE_EXHAUSTED", "voucher is out of quota");
  assert.equal(exhausted.code, "VOUCHER_EXHAUSTED");
  assert.equal(exhausted.status, 409);

  const notFound = mapVoucherError("NOT_FOUND", "voucher not found");
  assert.equal(notFound.code, "VOUCHER_NOT_FOUND");
  assert.equal(notFound.status, 404);
});

test("enrichCartGroups pairs backend cart groups with catalog presentation while preserving real prices and stock", () => {
  const groups = enrichCartGroups(
    [
      {
        shop_id: "shop-uuid-1",
        shop_name: "Xưởng Dệt Bạch Đằng",
        subtotal_vnd: 358000,
        total_weight_grams: 440,
        items: [
          {
            sku_id: "sku-uuid-1",
            product_id: "prod-uuid-1",
            shop_id: "shop-uuid-1",
            product_name: "Áo thun Cotton Compact",
            sku_name: "Trắng / L",
            image_url: "",
            price_vnd: 179000,
            added_price_vnd: 179000,
            price_changed: false,
            quantity: 2,
            available_stock: 1,
            weight_grams: 220,
            selected: true,
          },
        ],
      },
    ],
    [
      {
        id: "prod-at-trang",
        backendId: "prod-uuid-1",
        slug: "ao-thun-cotton-compact-trang",
        name: "Áo thun Cotton Compact",
        shortName: "Áo thun",
        categorySlug: "ao-thun",
        categoryName: "Áo thun",
        shopId: "shop-bach-dang",
        gender: "unisex",
        price: 179000,
        image: "/products/ao-thun-cotton-trang.png",
        gallery: ["/products/ao-thun-cotton-trang.png"],
        colors: [
          {
            code: "trang",
            name: "Trắng tinh",
            hex: "#fff",
            image: "/products/ao-thun-cotton-trang.png",
          },
        ],
        sizes: ["L"],
        skus: [
          {
            skuId: "sku-uuid-1",
            skuCode: "BD-AT01-TRANG-L",
            colorCode: "trang",
            size: "L",
            stock: 1,
            price: 179000,
          },
        ],
        fabric: "",
        gsm: 220,
        fit: "",
        origin: "",
        careGuide: [],
        highlights: [],
        description: "",
        sizeChart: [],
      },
    ],
    [
      {
        id: "shop-bach-dang",
        backendId: "shop-uuid-1",
        slug: "xuong-det-bach-dang",
        name: "Xưởng Dệt Bạch Đằng",
        shortName: "Bạch Đằng",
        city: "Hải Phòng",
        provinceCode: "HP",
        specialty: "",
        foundedYear: 2018,
        responseTime: "",
        fulfillmentRate: "",
        returnPolicy: "",
        warehouseAddress: "",
        story: "",
        accentDot: "#0d7480",
        commissionRate: 0.05,
        freeShipThreshold: 199000,
      },
    ]
  );

  assert.equal(groups.length, 1);
  assert.equal(groups[0].shopSlug, "xuong-det-bach-dang");
  assert.equal(groups[0].items[0].productSlug, "ao-thun-cotton-compact-trang");
  assert.equal(groups[0].items[0].variantLabel, "Màu sắc: Trắng tinh · Kích cỡ: L");
  assert.equal(groups[0].items[0].image, "/products/ao-thun-cotton-trang.png");
  assert.equal(groups[0].items[0].exceedsStock, true);
  assert.equal(groups[0].items[0].isOutOfStock, false);
});

test("enrichCartGroups formats custom non-garment SKU attributes and empty default SKUs cleanly", () => {
  const groups = enrichCartGroups(
    [
      {
        shop_id: "shop-gom",
        shop_name: "Gốm Bát Tràng",
        subtotal_vnd: 450000,
        total_weight_grams: 1200,
        items: [
          {
            sku_id: "sku-gom-1",
            product_id: "prod-gom-1",
            shop_id: "shop-gom",
            product_name: "Ấm trà gốm vẽ tay",
            sku_name: "Màu men: Men lam · Quy cách: Bộ 7 món",
            image_url: "",
            price_vnd: 450000,
            added_price_vnd: 450000,
            price_changed: false,
            quantity: 1,
            available_stock: 5,
            weight_grams: 1200,
            selected: true,
          },
          {
            sku_id: "sku-gom-2",
            product_id: "prod-gom-2",
            shop_id: "shop-gom",
            product_name: "Lọ hoa đơn",
            sku_name: "",
            image_url: "",
            price_vnd: 220000,
            added_price_vnd: 220000,
            price_changed: false,
            quantity: 1,
            available_stock: 3,
            weight_grams: 800,
            selected: true,
          },
        ],
      },
    ],
    [],
    [],
    {
      "sku-gom-1": { "Màu men": "Men lam", "Quy cách": "Bộ 7 món" },
      "sku-gom-2": {},
    }
  );
  assert.equal(groups[0].items[0].variantLabel, "Màu men: Men lam · Quy cách: Bộ 7 món");
  assert.equal(groups[0].items[1].variantLabel, "Mặc định");
});

test("voucher formatting and eligibility reasons handle fixed, percent, exhausted, and min-order shop vouchers", () => {
  const pct = {
    id: "v1",
    code: "BACHDANG10",
    name: "Giảm 10%",
    scope: "SHOP",
    shop_id: "shop-uuid-1",
    discount_type: "PERCENT",
    value_vnd: 0,
    discount_percent: 10,
    max_discount_vnd: 50000,
    min_order_vnd: 300000,
    quota: 100,
    used_count: 10,
    remaining_quota: 90,
    per_user_limit: 1,
    budget_vnd: 0,
    used_budget_vnd: 0,
    is_active: true,
    saved: false,
    eligible: true,
    ineligible_reason: "",
  };
  assert.equal(formatVoucherHeadline(pct), "Giảm 10% (tối đa 50.000₫)");
  assert.match(formatVoucherIneligibleReason(pct, 200000) ?? "", /100\.000₫/);
  assert.equal(formatVoucherIneligibleReason(pct, 350000), null);
  assert.match(
    formatVoucherIneligibleReason({ ...pct, remaining_quota: 0 }, 350000) ?? "",
    /hết lượt/i
  );
});

test("formatCarrierLabel and formatServiceLabel replace internal Mock Express copy with natural Vietnamese", () => {
  assert.equal(formatCarrierLabel("MOCK", "Mock Express"), "Giao hàng NexusFast");
  assert.equal(formatCarrierLabel("GHN", "Giao Hàng Nhanh"), "Giao Hàng Nhanh");
  assert.equal(formatServiceLabel("STD", "Mock Express Tiêu Chuẩn"), "Giao tiêu chuẩn");
  assert.equal(formatServiceLabel("EXP", "Mock Express Nhanh"), "Giao nhanh");
  assert.equal(formatServiceLabel("FAST", ""), "Giao nhanh");
});

test("formatAddressLine avoids duplicating ward or province already embedded in street_address", () => {
  assert.equal(
    formatAddressLine("Số 248 Võ Văn Tần", "Phường Xuân Hòa", "Thành phố Hồ Chí Minh"),
    "Số 248 Võ Văn Tần, Phường Xuân Hòa, Thành phố Hồ Chí Minh"
  );
  assert.equal(
    formatAddressLine(
      "Số 12 Hàng Trống, Phường Hoàn Kiếm, Thành phố Hà Nội",
      "Phường Hoàn Kiếm",
      "Thành phố Hà Nội"
    ),
    "Số 12 Hàng Trống, Phường Hoàn Kiếm, Thành phố Hà Nội"
  );
  assert.equal(
    formatAddressLine("Số 12 Hàng Trống, Phường Hoàn Kiếm", "Phường Hoàn Kiếm", "Thành phố Hà Nội"),
    "Số 12 Hàng Trống, Phường Hoàn Kiếm, Thành phố Hà Nội"
  );
});

test("shortVoucherHint covers inactive, expired, quota, per-user limit, and formatted min-order shortfall", () => {
  const base = {
    id: "v1",
    code: "SAVE50",
    name: "Giảm 50k",
    scope: "SHOP",
    shop_id: "shop-1",
    discount_type: "FIXED",
    value_vnd: 50000,
    discount_percent: 0,
    max_discount_vnd: 0,
    min_order_vnd: 300000,
    quota: 50,
    used_count: 5,
    remaining_quota: 45,
    per_user_limit: 1,
    budget_vnd: 0,
    used_budget_vnd: 0,
    is_active: true,
    saved: false,
    eligible: false,
    ineligible_reason: "",
  };
  const now = Date.parse("2026-11-25T12:00:00Z");

  assert.equal(shortVoucherHint({ ...base, is_active: false }, 400000, now), "Hết hiệu lực");
  assert.equal(shortVoucherHint({ ...base, remaining_quota: 0 }, 400000, now), "Hết lượt");
  assert.equal(
    shortVoucherHint({ ...base, ends_at: "2026-11-24T23:59:59Z" }, 400000, now),
    "Hết hạn"
  );
  assert.equal(shortVoucherHint(base, 175000, now), "Thêm 125.000₫");
  assert.equal(
    shortVoucherHint({ ...base, ineligible_reason: "per-user limit reached" }, 400000, now),
    "Đã dùng"
  );
  assert.equal(
    shortVoucherHint({ ...base, ineligible_reason: "voucher expired" }, 400000, now),
    "Hết hạn"
  );
  assert.equal(
    shortVoucherHint({ ...base, ineligible_reason: "voucher inactive" }, 400000, now),
    "Hết hiệu lực"
  );
  assert.equal(shortVoucherHint(base, 400000, now), "Chưa đủ");
});

test("splitVariantParts and buildCheckoutPreviewKey normalize variant labels and voucher preview keys", () => {
  assert.deepEqual(splitVariantParts("Màu: Trắng tinh · Size L"), ["Màu: Trắng tinh", "Size L"]);
  assert.deepEqual(splitVariantParts("  Phân loại tiêu chuẩn ·  "), ["Phân loại tiêu chuẩn"]);

  const baseInput = {
    shopId: "shop-1",
    rawSubtotal: 358000,
    cartItemsSignature: "sku-1:2",
    quotedShippingFee: 22000,
    voucherId: null,
    flashKey: "{}",
  };
  const keyNoVoucher = buildCheckoutPreviewKey(baseInput);
  const keyWithVoucher = buildCheckoutPreviewKey({ ...baseInput, voucherId: "v-1" });
  const keyPendingQuote = buildCheckoutPreviewKey({ ...baseInput, quotedShippingFee: null });
  assert.notEqual(keyNoVoucher, keyWithVoucher);
  assert.notEqual(keyNoVoucher, keyPendingQuote);
  assert.equal(keyWithVoucher, buildCheckoutPreviewKey({ ...baseInput, voucherId: "v-1" }));
});

test("CheckoutClient uses disclosure buttons (aria-expanded/aria-controls) on collapsed stages and keeps radiogroup only on real choice groups", () => {
  const webSrc = resolve(fileURLToPath(new URL("../..", import.meta.url)));
  const src = readFileSync(
    resolve(webSrc, "app/(site)/s/[host]/thanh-toan/CheckoutClient.tsx"),
    "utf8"
  );

  // Only the 3 real choice lists (address list, delivery quotes, payment methods) may use role="radiogroup".
  const radiogroupCount = (src.match(/role="radiogroup"/g) ?? []).length;
  assert.equal(
    radiogroupCount,
    3,
    `expected exactly 3 real radiogroups in CheckoutClient, found ${radiogroupCount}`
  );
  assert.equal(
    src.includes("stageSummaryRadio"),
    false,
    "collapsed summary buttons must not use fake radio styling/roles"
  );
  assert.ok(src.includes('aria-controls="stage-address-body"'));
  assert.ok(src.includes('aria-controls="stage-shipping-body"'));
  assert.ok(src.includes('aria-controls="stage-payment-body"'));
  assert.ok(src.includes("aria-expanded={false}"));
  assert.ok(src.includes('id="stage-address-body"'));
  assert.ok(src.includes('id="stage-shipping-body"'));
  assert.ok(src.includes('id="stage-payment-body"'));
});

test("Item 6: 429 rate-limit errors return HTTP 429 with rate-limit message (never 409 VOUCHER_EXHAUSTED)", () => {
  // Real gateway 429 from rate limiter middleware (handlers / ratelimit.go)
  const rlMiddleware = mapCheckoutError("RATE_LIMITED", "rate limit exceeded, please retry later");
  assert.equal(rlMiddleware.status, 429);
  assert.equal(rlMiddleware.code, "RATE_LIMITED");
  assert.match(rlMiddleware.message, /quá nhanh/i);
  assert.doesNotMatch(rlMiddleware.message, /mã ưu đãi/i);

  // Real gateway 429 RESOURCE_EXHAUSTED without voucher detail
  const rlResource = mapCheckoutError("RESOURCE_EXHAUSTED", "rate limit exceeded");
  assert.equal(rlResource.status, 429);
  assert.equal(rlResource.code, "RESOURCE_EXHAUSTED");
  assert.match(rlResource.message, /quá nhanh/i);
  assert.doesNotMatch(rlResource.message, /mã ưu đãi/i);

  // Real PromotionService Lua FLASH_SOLD_OUT (gRPC ResourceExhausted -> HTTP 429 RESOURCE_EXHAUSTED)
  const flashSoldOut = mapCheckoutError("RESOURCE_EXHAUSTED", "FLASH_SOLD_OUT: no slots left");
  assert.match(flashSoldOut.message, /Flash sale/i);
  assert.doesNotMatch(flashSoldOut.message, /mã ưu đãi/i);

  // Route-level helper: rate_limited GatewayError delegates to apiRoute (429 + Retry-After) except flash sold out
  const routeRateLimited = resolveCheckoutRouteError({
    kind: "rate_limited",
    status: 429,
    code: "RESOURCE_EXHAUSTED",
    detail: "rate limit exceeded",
    retryAfter: "30",
  });
  assert.equal(routeRateLimited.action, "rethrow");

  const routeFlashSoldOut = resolveCheckoutRouteError({
    kind: "rate_limited",
    status: 429,
    code: "RESOURCE_EXHAUSTED",
    detail: "FLASH_SOLD_OUT: flash sale item sold out",
    retryAfter: undefined,
  });
  assert.equal(routeFlashSoldOut.action, "respond");
  assert.equal(routeFlashSoldOut.status, 429);
  assert.equal(routeFlashSoldOut.code, "RESOURCE_EXHAUSTED");
  assert.match(routeFlashSoldOut.message, /Flash sale/i);

  // Verify api/checkout/route.ts uses resolveCheckoutRouteError (does not map generic rate_limited to 409)
  const webSrc = resolve(fileURLToPath(new URL("../..", import.meta.url)));
  const routeSrc = readFileSync(resolve(webSrc, "app/api/checkout/route.ts"), "utf8");
  assert.ok(
    routeSrc.includes("resolveCheckoutRouteError"),
    "api/checkout/route.ts must delegate GatewayError handling to resolveCheckoutRouteError"
  );
  assert.equal(
    routeSrc.includes('err.kind === "rate_limited"'),
    false,
    "api/checkout/route.ts must not route generic rate_limited through mapCheckoutError"
  );
});

test("Item 6 & 5: CheckoutClient wires pending-key F5 persistence, CHECKOUT_FAILED rotation, and flash opt-out recovery", () => {
  const webSrc = resolve(fileURLToPath(new URL("../..", import.meta.url)));
  const checkoutSrc = readFileSync(
    resolve(webSrc, "app/(site)/s/[host]/thanh-toan/CheckoutClient.tsx"),
    "utf8"
  );
  assert.ok(checkoutSrc.includes("reduceCheckoutSubmitState"));
  assert.ok(checkoutSrc.includes("classifyCheckoutSubmitError"));
  assert.ok(checkoutSrc.includes("readPendingCheckoutKey"));
  assert.ok(checkoutSrc.includes("savePendingCheckoutKey"));
  assert.ok(checkoutSrc.includes("clearPendingCheckoutKey"));
  assert.ok(checkoutSrc.includes("resolveCartFlashSaleState"));
  assert.ok(checkoutSrc.includes("evaluateShopFlashLines"));
  assert.ok(checkoutSrc.includes("canMutateCheckoutFlashSelection"));
});

test("Mục 1: translatePromotionWarning handles flash sale before per-user/not-found/shop rules and extractRejectedFlashSaleItemIds parses all real services/promotion strings", () => {
  // Real calculator.go warning strings (services/promotion/internal/calculator/calculator.go:607-630)
  const rawWarnings = [
    "flash sale item fsi-per-user per-user limit (1) exceeded",
    "flash sale item fsi-sold-out is sold out (remaining 0, requested 1)",
    "flash sale item fsi-ended is not active",
    "flash sale item fsi-removed not found",
    "flash sale item fsi-unapproved is not approved",
    "SHOP_SUSPENDED: shop for flash sale item fsi-susp is suspended",
    "flash sale item fsi-sku-mismatch does not match SKU sku-9",
    "flash sale item fsi-shop-mismatch does not belong to shop shop-a",
    // Non-rejection informational warning when flash WON (calculator.go:219) -> MUST NOT be marked rejected
    "flash sale item fsi-winner takes precedence over shop discount program item spi-1 for SKU sku-1",
    // Unrelated voucher warning -> MUST NOT be extracted as flash item
    "voucher SHOP10 per-user limit reached",
  ];

  // Flash sale warnings must NEVER blame "mã ưu đãi" and must never leak English or raw UUIDs
  for (const w of rawWarnings.slice(0, 8)) {
    const vi = translatePromotionWarning(w);
    assert.match(vi, /Flash sale/i, `expected Flash sale copy for: ${w} -> ${vi}`);
    assert.doesNotMatch(vi, /mã ưu đãi|mã gian hàng/i, `must not blame voucher for: ${w} -> ${vi}`);
    assert.doesNotMatch(vi, /fsi-|per-user|sold out|not found|not active/i);
  }
  // Specifically verify per-user flash warning mentions limit/Flash sale (not voucher)
  assert.match(
    translatePromotionWarning("flash sale item fsi-per-user per-user limit (1) exceeded"),
    /giới hạn.*Flash sale|Flash sale.*giới hạn/i
  );

  // Extract rejected IDs strictly from rejected flash warnings, excluding winner and vouchers
  const rejected = extractRejectedFlashSaleItemIds(rawWarnings);
  assert.deepEqual(rejected, [
    "fsi-per-user",
    "fsi-sold-out",
    "fsi-ended",
    "fsi-removed",
    "fsi-unapproved",
    "fsi-susp",
    "fsi-sku-mismatch",
    "fsi-shop-mismatch",
  ]);
});

test("Mục 2: resolveCheckoutRouteError maps 429 voucher quota/budget exhaustion to 409 VOUCHER_EXHAUSTED while keeping real 429 rate-limit and FLASH_SOLD_OUT", () => {
  // Real gateway 429 from PromotionService ReservePromotion Lua (scripts.go:67, promotion.go:2977-2980)
  const voucherOutOfQuota = resolveCheckoutRouteError({
    kind: "rate_limited",
    status: 429,
    code: "RESOURCE_EXHAUSTED",
    detail: "promotion reservation rejected: VOUCHER_OUT_OF_QUOTA (promo:voucher:v-1:remaining)",
    retryAfter: undefined,
  });
  assert.equal(voucherOutOfQuota.action, "respond");
  if (voucherOutOfQuota.action === "respond") {
    assert.equal(voucherOutOfQuota.status, 409);
    assert.equal(voucherOutOfQuota.code, "VOUCHER_EXHAUSTED");
    assert.match(voucherOutOfQuota.message, /hết lượt/i);
  }

  const voucherBudgetExhausted = resolveCheckoutRouteError({
    kind: "rate_limited",
    status: 429,
    code: "RESOURCE_EXHAUSTED",
    detail:
      "promotion reservation rejected: VOUCHER_BUDGET_EXHAUSTED (promo:voucher:v-2:budget_remaining)",
    retryAfter: undefined,
  });
  assert.equal(voucherBudgetExhausted.action, "respond");
  if (voucherBudgetExhausted.action === "respond") {
    assert.equal(voucherBudgetExhausted.status, 409);
    assert.equal(voucherBudgetExhausted.code, "VOUCHER_EXHAUSTED");
    assert.match(voucherBudgetExhausted.message, /hết lượt/i);
  }

  // Real gateway rate limiter (ratelimit.go:258,300) MUST still rethrow to keep 429 + Retry-After
  const realRateLimit = resolveCheckoutRouteError({
    kind: "rate_limited",
    status: 429,
    code: "RATE_LIMITED",
    detail: "rate limit exceeded, please retry later",
    retryAfter: "45",
  });
  assert.equal(realRateLimit.action, "rethrow");

  // FLASH_SOLD_OUT MUST remain 429 RESOURCE_EXHAUSTED with Flash sale copy
  const flashSoldOut = resolveCheckoutRouteError({
    kind: "rate_limited",
    status: 429,
    code: "RESOURCE_EXHAUSTED",
    detail: "promotion reservation rejected: FLASH_SOLD_OUT (promo:flash:fsi-1:remaining)",
    retryAfter: undefined,
  });
  assert.equal(flashSoldOut.action, "respond");
  if (flashSoldOut.action === "respond") {
    assert.equal(flashSoldOut.status, 429);
    assert.equal(flashSoldOut.code, "RESOURCE_EXHAUSTED");
    assert.match(flashSoldOut.message, /Flash sale/i);
  }
});

test("mapCheckoutError maps SHOP_NOT_ACTIVE to 409 with clean Vietnamese message", () => {
  const fromDetail = mapCheckoutError(
    "FAILED_PRECONDITION",
    "SHOP_NOT_ACTIVE: gian hàng shop-1 không tồn tại hoặc không hoạt động"
  );
  assert.equal(fromDetail.code, "SHOP_NOT_ACTIVE");
  assert.equal(fromDetail.status, 409);
  assert.equal(fromDetail.message, "Gian hàng hiện không hoạt động nên không thể đặt hàng.");

  const fromCode = mapCheckoutError("SHOP_NOT_ACTIVE", "shop status pending");
  assert.equal(fromCode.code, "SHOP_NOT_ACTIVE");
  assert.equal(fromCode.status, 409);
  assert.equal(fromCode.message, "Gian hàng hiện không hoạt động nên không thể đặt hàng.");

  const routeDecision = resolveCheckoutRouteError({
    kind: "conflict",
    status: 409,
    code: "FAILED_PRECONDITION",
    detail: "SHOP_NOT_ACTIVE: gian hàng shop-1 hiện không ở trạng thái hoạt động (SHOP_STATUS_PENDING)",
  });
  assert.deepEqual(routeDecision, {
    action: "respond",
    status: 409,
    code: "SHOP_NOT_ACTIVE",
    message: "Gian hàng hiện không hoạt động nên không thể đặt hàng.",
  });

  // SHOP_SUSPENDED remains distinct and unaffected.
  const suspended = mapCheckoutError("SHOP_SUSPENDED", "SHOP_SUSPENDED: shop is suspended");
  assert.equal(suspended.code, "SHOP_SUSPENDED");
  assert.equal(suspended.status, 409);
});
