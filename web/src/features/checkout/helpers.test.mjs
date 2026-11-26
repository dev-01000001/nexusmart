// Unit tests for shared checkout & flash-sale helpers (items 5 & 6).
// Fixtures mirror real gateway JSON from services/gateway/internal/router/handlers_{promotion,order}.go.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { ApiError } from "../../lib/api.ts";
import {
  buildCheckoutIdempotencySignature,
  canMutateCheckoutFlashSelection,
  classifyCheckoutSubmitError,
  clearOptedOutFlashSkus,
  clearPendingCheckoutKey,
  evaluatePaymentMethodForCheckout,
  evaluateShopFlashLines,
  INITIAL_CHECKOUT_SUBMIT_GUARD_STATE,
  PENDING_CHECKOUT_KEY_TTL_MS,
  prunePendingCheckoutKeyForUser,
  readOptedOutFlashSkus,
  readPendingCheckoutKey,
  reduceCheckoutSubmitState,
  resolveCartFlashSaleState,
  resolveDefaultCheckoutMethod,
  resolveDisplayedLinePricing,
  saveOptedOutFlashSkus,
  savePendingCheckoutKey,
  shouldClearPendingKeyOnConfirmedEmptyCart,
} from "./helpers.ts";
import { CHECKOUT_FAILED_BUYER_MESSAGE, translateCheckoutError } from "./messages.ts";

function createMemoryStorage() {
  const map = new Map();
  return {
    getItem(k) {
      return map.has(k) ? map.get(k) : null;
    },
    setItem(k, v) {
      map.set(k, String(v));
    },
    removeItem(k) {
      map.delete(k);
    },
  };
}

// Real GET /api/v1/flash-sales?include_upcoming=false payload shape (handlers_promotion.go)
const LIVE_FLASH_SALES_FIXTURE = [
  {
    id: "fs-window-live",
    name: "Flash trưa 12h",
    starts_at: "2026-11-26T05:00:00Z",
    ends_at: "2026-11-26T08:00:00Z",
    status: "ACTIVE",
    min_discount_percent: 10,
    category_ids: [],
    items: [
      {
        id: "fsi-sku-m",
        flash_sale_id: "fs-window-live",
        shop_id: "shop-a",
        sku_id: "sku-m",
        flash_price_vnd: 149000,
        original_price_vnd: 219000,
        total_slots: 20,
        remaining_slots: 5,
        per_user_limit: 2,
        status: "APPROVED",
        rejection_reason: "",
      },
      {
        id: "fsi-sku-l-soldout",
        flash_sale_id: "fs-window-live",
        shop_id: "shop-a",
        sku_id: "sku-l",
        flash_price_vnd: 159000,
        original_price_vnd: 219000,
        total_slots: 10,
        remaining_slots: 0,
        per_user_limit: 1,
        status: "APPROVED",
        rejection_reason: "",
      },
    ],
  },
];

const NOW_LIVE_MS = Date.parse("2026-11-26T06:00:00Z");

test("Item 5: resolveCartFlashSaleState rehydrates per-SKU flash_sale_item_id after F5 even when in-memory map is empty", () => {
  const res = resolveCartFlashSaleState({
    items: [
      { skuId: "sku-m", quantity: 1, regularUnitPriceVnd: 219000 },
      { skuId: "sku-xl", quantity: 1, regularUnitPriceVnd: 219000 },
    ],
    serverFlashSales: LIVE_FLASH_SALES_FIXTURE,
    storedFlashItemIdBySku: {}, // wiped by F5
    optedOutFlashSkus: {},
    nowMs: NOW_LIVE_MS,
  });

  assert.equal(res.hasFlashSaleItem, true);
  assert.deepEqual(res.activeFlashItemIdBySku, { "sku-m": "fsi-sku-m" });
  assert.equal(res.liveFlashBySku["sku-m"]?.flashPriceVnd, 149000);
  assert.equal(res.liveFlashBySku["sku-m"]?.remainingSlots, 5);
  assert.equal(res.liveFlashBySku["sku-m"]?.perUserLimit, 2);
  // Non-flash SKU never gets another SKU's flash_sale_item_id
  assert.equal(res.activeFlashItemIdBySku["sku-xl"], undefined);
  assert.equal(res.liveFlashBySku["sku-xl"], undefined);

  // COD is strictly blocked when flash sale item is rehydrated after F5
  const codEval = evaluatePaymentMethodForCheckout(
    { method: "COD", enabled: true, min_order_vnd: 0, max_order_vnd: 5_000_000 },
    { totalPayableVnd: 368000, hasFlashSaleItem: res.hasFlashSaleItem, codRejectedCount: 0 }
  );
  assert.equal(codEval.allowed, false);
  assert.equal(codEval.blockCode, "FLASH_SALE");
  assert.equal(
    resolveDefaultCheckoutMethod(
      [
        { method: "COD", enabled: true },
        { method: "VIETQR", enabled: true },
        { method: "ZALOPAY", enabled: true },
      ],
      { hasFlashSaleItem: res.hasFlashSaleItem, currentMethod: "COD" }
    ),
    "VIETQR"
  );
});

test("Item 5: resolveCartFlashSaleState honors opt-out 'Mua giá thường', keeps available live flash for restore, and unlocks COD", () => {
  const storage = createMemoryStorage();
  saveOptedOutFlashSkus("user-1", { "sku-m": true }, storage);
  const restoredOptOuts = readOptedOutFlashSkus("user-1", storage);
  assert.deepEqual(restoredOptOuts, { "sku-m": true });
  // Isolated per userId (switching users never leaks opt-outs)
  assert.deepEqual(readOptedOutFlashSkus("user-2", storage), {});

  const res = resolveCartFlashSaleState({
    items: [{ skuId: "sku-m", quantity: 3, regularUnitPriceVnd: 219000 }],
    serverFlashSales: LIVE_FLASH_SALES_FIXTURE,
    storedFlashItemIdBySku: { "sku-m": "fsi-sku-m" },
    optedOutFlashSkus: restoredOptOuts,
    nowMs: NOW_LIVE_MS,
  });

  assert.equal(res.hasFlashSaleItem, false);
  assert.deepEqual(res.activeFlashItemIdBySku, {});
  assert.equal(res.liveFlashBySku["sku-m"], undefined);
  // Still exposes availableLiveFlashBySku so UI can offer "Dùng giá Flash"
  assert.equal(res.availableLiveFlashBySku["sku-m"]?.flashSaleItemId, "fsi-sku-m");

  const codEval = evaluatePaymentMethodForCheckout(
    { method: "COD", enabled: true, min_order_vnd: 0, max_order_vnd: 5_000_000 },
    { totalPayableVnd: 657000, hasFlashSaleItem: res.hasFlashSaleItem, codRejectedCount: 0 }
  );
  assert.equal(codEval.allowed, true);

  clearOptedOutFlashSkus(storage);
  assert.deepEqual(readOptedOutFlashSkus("user-1", storage), {});
});

test("Item 5: sold-out flash SKU is not sent as flash_sale_item_id and surfaces sold_out inspection; per-SKU pricing & shortfall work together", () => {
  const res = resolveCartFlashSaleState({
    items: [
      { skuId: "sku-l", quantity: 1, regularUnitPriceVnd: 219000 },
      { skuId: "sku-m", quantity: 3, regularUnitPriceVnd: 219000 },
    ],
    serverFlashSales: LIVE_FLASH_SALES_FIXTURE,
    storedFlashItemIdBySku: { "sku-l": "fsi-sku-l-soldout" },
    optedOutFlashSkus: {},
    nowMs: NOW_LIVE_MS,
  });

  assert.equal(res.activeFlashItemIdBySku["sku-l"], undefined);
  assert.equal(res.inspectionBySku["sku-l"]?.state, "sold_out");
  assert.equal(res.activeFlashItemIdBySku["sku-m"], "fsi-sku-m");

  // Quantity 3 exceeds per_user_limit 2 -> evaluateShopFlashLines flags needsRegularPriceChoice
  const evals = evaluateShopFlashLines({
    lines: [
      {
        skuId: "sku-m",
        quantity: 3,
        regularUnitPriceVnd: 219000,
        flashSaleItemId: res.liveFlashBySku["sku-m"].flashSaleItemId,
        flashUnitPriceVnd: res.liveFlashBySku["sku-m"].flashPriceVnd,
        flashPerUserLimit: res.liveFlashBySku["sku-m"].perUserLimit,
      },
    ],
    previewShopFlashDiscountVnd: 0,
    previewWarnings: [],
  });
  assert.equal(evals["sku-m"]?.exceedsQtyLimit, true);
  assert.equal(evals["sku-m"]?.needsRegularPriceChoice, true);

  // When not in shortfall, resolveDisplayedLinePricing uses exact per-SKU flashUnitPriceVnd
  const flashPricing = resolveDisplayedLinePricing({
    regularUnitPriceVnd: 219000,
    quantity: 1,
    isFlashLine: true,
    flashUnitPriceVnd: res.liveFlashBySku["sku-m"].flashPriceVnd,
    flashShortfall: false,
  });
  assert.equal(flashPricing.appliedKind, "flash");
  assert.equal(flashPricing.effectiveUnitPriceVnd, 149000);
  assert.equal(flashPricing.originalUnitPriceVnd, 219000);
});

test("Item 6: CHECKOUT_FAILED rotates idempotency key; ambiguous error preserves key across F5; 429 translates cleanly without voucher false-positive", () => {
  const storage = createMemoryStorage();
  const sig = buildCheckoutIdempotencySignature({ userId: "buyer-1" });
  const key1 = "11111111-1111-4111-8111-111111111111";

  // 1. Before submit: save pending key. 504 DEADLINE_EXCEEDED -> ambiguous_created, key kept in storage!
  savePendingCheckoutKey({ key: key1, sig }, storage);
  const startTr = reduceCheckoutSubmitState(INITIAL_CHECKOUT_SUBMIT_GUARD_STATE, {
    type: "submit_start",
  });
  const err504 = new ApiError(504, "DEADLINE_EXCEEDED", "upstream service timed out");
  const ambKind = classifyCheckoutSubmitError(err504, {
    hasFlashSaleItem: true,
    paymentMethod: "VIETQR",
  });
  assert.equal(ambKind, "ambiguous_created");
  const ambTr = reduceCheckoutSubmitState(startTr.state, { type: "submit_error", kind: ambKind });
  assert.equal(ambTr.rotateKey, false);
  assert.equal(ambTr.clearStoredKey, false);
  assert.equal(ambTr.state.keyUncertainty, "ambiguous");
  assert.equal(canMutateCheckoutFlashSelection(ambTr.state), false);

  // 2. Simulate F5 reload: readPendingCheckoutKey returns the SAME key1 and hydrates ambiguous lock
  assert.equal(readPendingCheckoutKey(sig, storage), key1);
  const hydrated = reduceCheckoutSubmitState(INITIAL_CHECKOUT_SUBMIT_GUARD_STATE, {
    type: "hydrated_pending_key",
  });
  assert.equal(hydrated.state.keyUncertainty, "ambiguous");
  assert.equal(hydrated.rotateKey, false);

  // 3. Retry with key1 receives 409 CHECKOUT_FAILED -> MUST rotate key & clear pending storage
  const errCheckoutFailed = new ApiError(
    409,
    "CHECKOUT_FAILED",
    "CHECKOUT_FAILED (FAILED): checkout attempt failed with reason FAILED; please retry with a new idempotency_key"
  );
  const cfKind = classifyCheckoutSubmitError(errCheckoutFailed, {
    hasFlashSaleItem: true,
    paymentMethod: "VIETQR",
  });
  assert.equal(cfKind, "checkout_failed");
  const cfTr = reduceCheckoutSubmitState(hydrated.state, { type: "submit_error", kind: cfKind });
  assert.equal(cfTr.rotateKey, true);
  assert.equal(cfTr.clearStoredKey, true);
  assert.equal(cfTr.state.keyUncertainty, "none");
  assert.equal(cfTr.state.checkoutFailedPrior, true);
  assert.equal(translateCheckoutError(errCheckoutFailed), CHECKOUT_FAILED_BUYER_MESSAGE);
  clearPendingCheckoutKey(storage);
  assert.equal(readPendingCheckoutKey(sig, storage), null);

  // 4. 429 RATE_LIMITED / RESOURCE_EXHAUSTED (no voucher) translates as rate-limit, not voucher exhausted
  const err429Rate = new ApiError(
    429,
    "RATE_LIMITED",
    "Bạn thao tác quá nhanh, vui lòng thử lại sau ít phút."
  );
  assert.equal(classifyCheckoutSubmitError(err429Rate, { hasFlashSaleItem: false }), "definite_failure");
  assert.match(translateCheckoutError(err429Rate, { hasVouchers: false }), /quá nhanh/i);
  assert.doesNotMatch(translateCheckoutError(err429Rate, { hasVouchers: false }), /mã ưu đãi/i);

  const err429Res = new ApiError(429, "RESOURCE_EXHAUSTED", "");
  assert.match(translateCheckoutError(err429Res, { hasVouchers: false }), /quá nhanh/i);
  assert.doesNotMatch(translateCheckoutError(err429Res, { hasVouchers: false }), /mã ưu đãi/i);
});

test("Mục 1: evaluateShopFlashLines attributes shortfall via rejectedFlashSaleItemIds and preserves legacy behavior when omitted", () => {
  const twoLines = [
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
  ];

  // With rejectedFlashSaleItemIds: pinpoints sku-A even though previewWarnings are translated Vietnamese
  const withIds = evaluateShopFlashLines({
    lines: twoLines,
    previewShopFlashDiscountVnd: 200000,
    previewWarnings: ["Sản phẩm đã vượt giới hạn suất Flash sale cho mỗi tài khoản."],
    rejectedFlashSaleItemIds: ["fsi-A"],
  });
  assert.equal(withIds["sku-A"]?.needsRegularPriceChoice, true);
  assert.equal(withIds["sku-A"]?.previewShortfall, true);
  assert.equal(withIds["sku-B"]?.needsRegularPriceChoice, false);
  assert.equal(withIds["sku-B"]?.ambiguousShopShortfall, false);

  // Without rejectedFlashSaleItemIds (legacy caller): falls back to previewWarnings / ambiguous behavior
  const legacyAmbiguous = evaluateShopFlashLines({
    lines: twoLines,
    previewShopFlashDiscountVnd: 200000,
    previewWarnings: [],
  });
  assert.equal(legacyAmbiguous["sku-A"]?.ambiguousShopShortfall, true);
  assert.equal(legacyAmbiguous["sku-B"]?.ambiguousShopShortfall, true);
  assert.equal(legacyAmbiguous["sku-A"]?.needsRegularPriceChoice, false);

  const legacyRawWarning = evaluateShopFlashLines({
    lines: twoLines,
    previewShopFlashDiscountVnd: 200000,
    previewWarnings: ["flash sale item fsi-A per-user limit (1) exceeded"],
  });
  assert.equal(legacyRawWarning["sku-A"]?.needsRegularPriceChoice, true);
  assert.equal(legacyRawWarning["sku-B"]?.needsRegularPriceChoice, false);
});

test("Mục 3: pending checkout key enforces 30-minute TTL, clears on logout/user-switch, clears on confirmed empty cart, and preserves F5 protection while cart has items", () => {
  assert.equal(PENDING_CHECKOUT_KEY_TTL_MS, 30 * 60 * 1000);
  const storage = createMemoryStorage();
  const sigA = buildCheckoutIdempotencySignature({ userId: "buyer-A" });
  const key1 = "22222222-2222-4222-8222-222222222222";
  const t0 = Date.parse("2026-11-26T06:00:00Z");

  // 1. Valid within 30 minutes
  savePendingCheckoutKey({ key: key1, sig: sigA }, storage, t0);
  assert.equal(readPendingCheckoutKey(sigA, storage, t0 + 29 * 60 * 1000), key1);

  // 2. Expired after 30 minutes -> returns null and evicts stale storage key
  assert.equal(readPendingCheckoutKey(sigA, storage, t0 + 30 * 60 * 1000 + 1), null);
  assert.equal(readPendingCheckoutKey(sigA, storage, t0), null);

  // 3. Logout (activeUserId = null) deletes pending key even if same user logs back in
  savePendingCheckoutKey({ key: key1, sig: sigA }, storage, t0);
  prunePendingCheckoutKeyForUser(null, storage, t0 + 1000);
  assert.equal(readPendingCheckoutKey(sigA, storage, t0 + 1000), null);

  // 4. Switching user (buyer-A -> buyer-B) deletes buyer-A's pending key, while same user keeps it on F5
  savePendingCheckoutKey({ key: key1, sig: sigA }, storage, t0);
  prunePendingCheckoutKeyForUser("buyer-A", storage, t0 + 1000);
  assert.equal(readPendingCheckoutKey(sigA, storage, t0 + 1000), key1);
  prunePendingCheckoutKeyForUser("buyer-B", storage, t0 + 2000);
  assert.equal(readPendingCheckoutKey(sigA, storage, t0 + 2000), null);

  // 5. Confirmed-empty-cart guard: MUST NOT clear before cart loads, during loading, on error, while submitting, or when items remain!
  assert.equal(
    shouldClearPendingKeyOnConfirmedEmptyCart({
      hasPendingKey: true,
      isAuthenticated: true,
      sessionLoading: false,
      cartLoading: false,
      cartError: null,
      cartLoaded: false, // F5 initial render before /api/cart returns!
      availableCartItemCount: 0,
      isSubmitting: false,
    }),
    false
  );
  assert.equal(
    shouldClearPendingKeyOnConfirmedEmptyCart({
      hasPendingKey: true,
      isAuthenticated: true,
      sessionLoading: false,
      cartLoading: false,
      cartError: null,
      cartLoaded: true,
      availableCartItemCount: 1, // F5 while cart STILL has items -> MUST keep pending key!
      isSubmitting: false,
    }),
    false
  );
  assert.equal(
    shouldClearPendingKeyOnConfirmedEmptyCart({
      hasPendingKey: true,
      isAuthenticated: true,
      sessionLoading: false,
      cartLoading: false,
      cartError: "Chưa tải được giỏ hàng",
      cartLoaded: true,
      availableCartItemCount: 0,
      isSubmitting: false,
    }),
    false
  );
  assert.equal(
    shouldClearPendingKeyOnConfirmedEmptyCart({
      hasPendingKey: true,
      isAuthenticated: true,
      sessionLoading: false,
      cartLoading: false,
      cartError: null,
      cartLoaded: true,
      availableCartItemCount: 0,
      isSubmitting: true,
    }),
    false
  );
  // Server confirmed empty cart after ambiguous F5 -> clear key + rotate for future order
  assert.equal(
    shouldClearPendingKeyOnConfirmedEmptyCart({
      hasPendingKey: true,
      isAuthenticated: true,
      sessionLoading: false,
      cartLoading: false,
      cartError: null,
      cartLoaded: true,
      availableCartItemCount: 0,
      isSubmitting: false,
    }),
    true
  );

  // State machine transition on confirmed_empty_cart clears keyUncertainty, clears stored key, and rotates key
  const hydrated = reduceCheckoutSubmitState(INITIAL_CHECKOUT_SUBMIT_GUARD_STATE, {
    type: "hydrated_pending_key",
  });
  const afterEmpty = reduceCheckoutSubmitState(hydrated.state, { type: "confirmed_empty_cart" });
  assert.equal(afterEmpty.state.keyUncertainty, "none");
  assert.equal(afterEmpty.clearStoredKey, true);
  assert.equal(afterEmpty.rotateKey, true);

  // Source wiring check for StoreContext.tsx and CheckoutClient.tsx
  const webSrc = resolve(fileURLToPath(new URL("../..", import.meta.url)));
  const storeSrc = readFileSync(resolve(webSrc, "context/StoreContext.tsx"), "utf8");
  const checkoutSrc = readFileSync(
    resolve(webSrc, "app/(site)/s/[host]/thanh-toan/CheckoutClient.tsx"),
    "utf8"
  );
  assert.ok(storeSrc.includes("prunePendingCheckoutKeyForUser"));
  assert.ok(checkoutSrc.includes("shouldClearPendingKeyOnConfirmedEmptyCart"));
  assert.ok(checkoutSrc.includes('actionHref="/don-hang"'));
});
