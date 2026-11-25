import type {
  GatewayActiveShopDiscount,
  GatewayCheckoutRequest,
  GatewayCheckoutResponse,
  GatewayDiscountBreakdown,
  GatewayFlashSale,
  GatewayPaymentMethod,
  GatewayPaymentMethodConfig,
  GatewayShopDiscountBreakdown,
  GatewayShopShippingChoice,
  GatewayVoucher,
} from "@/lib/gateway-types";
import {
  AMBIGUOUS_CHECKOUT_MESSAGE,
  DEFAULT_COD_MAX_REJECTED,
  DEFAULT_COD_MAX_VND,
  isAmbiguousCheckoutSubmitError,
  translateVoucherReason,
} from "./messages.ts";
import { formatVnd } from "../../lib/format.ts";

export { AMBIGUOUS_CHECKOUT_MESSAGE, isAmbiguousCheckoutSubmitError };

export const ACTIVE_SHOP_DISCOUNTS_MAX_SKU_IDS = 200;

export interface ActiveShopDiscountBatchQuery {
  skuIds?: string[];
  shopId?: string;
}

/**
 * Splits active shop discount filter parameters into gateway-compliant batches:
 * - Returns [] when neither non-empty `skuIds` nor `shopId` is provided (gateway returns 400 unfiltered).
 * - Deduplicates `skuIds` and chunks them into batches of <= 200 items.
 */
export function buildActiveShopDiscountBatches(params?: {
  skuIds?: string[];
  shopId?: string;
  chunkSize?: number;
}): ActiveShopDiscountBatchQuery[] {
  const shopId = params?.shopId?.trim() || "";
  const uniqueSkuIds = Array.from(
    new Set((params?.skuIds ?? []).map((id) => id.trim()).filter(Boolean))
  );
  if (uniqueSkuIds.length === 0) {
    return shopId ? [{ shopId }] : [];
  }
  const limit = Math.max(
    1,
    Math.min(
      ACTIVE_SHOP_DISCOUNTS_MAX_SKU_IDS,
      Math.trunc(params?.chunkSize ?? ACTIVE_SHOP_DISCOUNTS_MAX_SKU_IDS)
    )
  );
  const batches: ActiveShopDiscountBatchQuery[] = [];
  for (let i = 0; i < uniqueSkuIds.length; i += limit) {
    const slice = uniqueSkuIds.slice(i, i + limit);
    batches.push(shopId ? { skuIds: slice, shopId } : { skuIds: slice });
  }
  return batches;
}

/**
 * Fetches active shop discounts using chunked gateway requests (<= 200 sku_ids per call).
 * Immediately returns [] without invoking `fetcher` when no filter is supplied.
 */
export async function fetchActiveShopDiscountsWithFetcher(
  params: { skuIds?: string[]; shopId?: string; chunkSize?: number } | undefined,
  fetcher: (batch: ActiveShopDiscountBatchQuery) => Promise<GatewayActiveShopDiscount[]>
): Promise<GatewayActiveShopDiscount[]> {
  const batches = buildActiveShopDiscountBatches(params);
  if (batches.length === 0) return [];
  const settled = await Promise.allSettled(batches.map((b) => fetcher(b)));
  const seenSkuIds = new Set<string>();
  const merged: GatewayActiveShopDiscount[] = [];
  for (let i = 0; i < settled.length; i += 1) {
    const res = settled[i];
    if (res.status !== "fulfilled") {
      console.warn(
        `[promo] active shop discounts batch ${i + 1}/${batches.length} failed (size=${batches[i].skuIds?.length ?? 0})`
      );
      continue;
    }
    for (const disc of res.value) {
      if (!disc?.sku_id || seenSkuIds.has(disc.sku_id)) continue;
      seenSkuIds.add(disc.sku_id);
      merged.push(disc);
    }
  }
  return merged;
}

export interface CalculateFeeItemInput {
  skuId: string;
  quantity: number;
}

export interface CalculateFeePayloadInput {
  shopId: string;
  toProvinceCode: string;
  toWardCode: string;
  items?: CalculateFeeItemInput[];
  paymentMethod?: GatewayPaymentMethod;
  weightGrams?: number;
  codAmountVnd?: number;
}

export interface CalculateFeePayload {
  shop_id: string;
  to_province_code: string;
  to_ward_code: string;
  items?: Array<{ sku_id: string; quantity: number }>;
  payment_method?: GatewayPaymentMethod;
  weight_grams?: number;
  cod_amount_vnd?: number;
}

/**
 * Builds the POST /shipping/calculate-fee payload without empty origin fields.
 * When `items` are supplied, sends authoritative `{ sku_id, quantity }` + `payment_method`
 * so gateway resolves shop default weight and pre-discount COD amount identically to order checkout.
 */
export function buildCalculateFeePayload(input: CalculateFeePayloadInput): CalculateFeePayload {
  if (Array.isArray(input.items) && input.items.length > 0) {
    const merged = new Map<string, number>();
    for (const it of input.items) {
      const skuId = (it.skuId ?? "").trim();
      if (!skuId) continue;
      const qty = Math.min(999, Math.max(1, Math.trunc(it.quantity || 1)));
      merged.set(skuId, Math.min(999, (merged.get(skuId) ?? 0) + qty));
      if (merged.size >= 100) break;
    }
    if (merged.size > 0) {
      return {
        shop_id: input.shopId,
        to_province_code: input.toProvinceCode,
        to_ward_code: input.toWardCode,
        items: Array.from(merged.entries()).map(([sku_id, quantity]) => ({
          sku_id,
          quantity,
        })),
        payment_method: input.paymentMethod ?? "VIETQR",
      };
    }
  }
  return {
    shop_id: input.shopId,
    to_province_code: input.toProvinceCode,
    to_ward_code: input.toWardCode,
    weight_grams: Math.max(0, Math.trunc(input.weightGrams || 0)),
    cod_amount_vnd: Math.max(0, Math.trunc(input.codAmountVnd || 0)),
  };
}

export interface StableReferenceCache {
  fingerprint: string;
  key: string;
}

/**
 * Returns a stable idempotency / reference key for a given input fingerprint:
 * reuses the same UUID across retries on timeout/error, and only mints a new UUID
 * when inputs change or after a previous attempt succeeds (cache cleared to null).
 */
export function resolveStableReferenceKey(input: {
  prefix: string;
  manualRef?: string;
  fingerprint: string;
  cache: StableReferenceCache | null;
  uuidFn?: () => string;
}): { key: string; cache: StableReferenceCache | null } {
  const manual = (input.manualRef ?? "").trim();
  if (manual) {
    return { key: manual, cache: input.cache };
  }
  if (input.cache && input.cache.fingerprint === input.fingerprint && input.cache.key) {
    return { key: input.cache.key, cache: input.cache };
  }
  const nextUuid = input.uuidFn
    ? input.uuidFn()
    : typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
      ? crypto.randomUUID()
      : `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
  const key = `${input.prefix}-${nextUuid}`;
  return { key, cache: { fingerprint: input.fingerprint, key } };
}

/**
 * Exponential backoff for payment SSE reconnect: 5s, 10s, 20s, 40s, capped at 60s.
 */
export function getSseReconnectDelayMs(attempt: number): number {
  const safeAttempt = Math.max(0, Math.trunc(attempt || 0));
  return Math.min(60_000, 5_000 * 2 ** safeAttempt);
}

/**
 * Computes buyer payable amount for a single shop allocation in PaymentWaiting fallback
 * (subtracting platform_discount_vnd per docs/contracts.md).
 */
export function computeShopAllocationPayableVnd(alloc: {
  subtotal_vnd: number;
  shipping_fee_vnd: number;
  platform_discount_vnd?: number;
}): number {
  return Math.max(
    0,
    Math.trunc(alloc.subtotal_vnd || 0) +
      Math.trunc(alloc.shipping_fee_vnd || 0) -
      Math.trunc(alloc.platform_discount_vnd ?? 0)
  );
}

/**
 * Computes maximum selectable quantity on PDP:
 * - Never clamps by shop discount program per_user_limit (over-limit purchases proceed at regular price).
 * - For live flash sale: perUserLimit === 0 means unlimited per user, still capped by remainingSlots & skuStock.
 */
export function computePdpQuantityCap(input: {
  skuStock: number;
  productMaxPerOrder?: number;
  liveFlash?: { perUserLimit: number; remainingSlots: number } | null;
}): number {
  const stock = Math.max(0, Math.trunc(input.skuStock || 0));
  if (stock <= 0) return 0;
  if (input.liveFlash) {
    const slots = Math.max(0, Math.trunc(input.liveFlash.remainingSlots || 0));
    const perUser = Math.max(0, Math.trunc(input.liveFlash.perUserLimit || 0));
    let cap = slots > 0 ? Math.min(stock, slots) : stock;
    if (perUser > 0) {
      cap = Math.min(cap, perUser);
    }
    return Math.max(1, cap);
  }
  const productMax = Math.max(0, Math.trunc(input.productMaxPerOrder || 0));
  return productMax > 0 ? Math.max(1, Math.min(stock, productMax)) : stock;
}

/**
 * Determines whether client-scoped user state (localStorage, warnings, saved vouchers, opt-outs)
 * must be cleared on auth transition:
 * - Guest (null) -> Login (userA): FALSE (preserve guest state)
 * - Logout (userA -> null) or Switch (userA -> userB): TRUE
 */
export function shouldClearUserClientState(
  prevUserId: string | null,
  nextUserId: string | null
): boolean {
  if (!prevUserId) return false;
  return prevUserId !== nextUserId;
}

/**
 * Returns true if an ISO timestamp deadline is valid and at or before `nowMs`.
 */
export function isPaymentDeadlinePassed(
  expiresAt: string | null | undefined,
  nowMs = Date.now()
): boolean {
  if (!expiresAt) return false;
  const expiresMs = Date.parse(expiresAt);
  if (!Number.isFinite(expiresMs)) return false;
  return expiresMs <= nowMs;
}

/**
 * Only active (non-expired) PENDING or FAILED payments may be retried via VietQR.
 * EXPIRED, CANCELLED, SUCCEEDED, and REFUNDED payments cannot be retried.
 */
export function canRetryPayment(rawStatus: string, countdownExpired = false): boolean {
  if (countdownExpired) return false;
  const status = (rawStatus ?? "").trim().toUpperCase();
  return status === "PENDING" || status === "FAILED";
}

export interface ResolvedLiveFlashItem {
  flashSaleItemId: string;
  flashPriceVnd: number;
  remainingSlots: number;
  perUserLimit: number;
  endsAt?: string;
}

/**
 * Validates that a requested flash sale item is live right now, approved, matches the
 * target SKU, still has remaining slots, and strictly discounts the SKU's regular price.
 */
export function resolveLiveFlashSaleItem(
  flashSales: GatewayFlashSale[],
  flashItemId: string,
  skuId: string,
  skuPriceVnd: number,
  nowMs = Date.now()
): ResolvedLiveFlashItem | null {
  const cleanId = flashItemId.trim();
  if (!cleanId || !skuId || skuPriceVnd <= 0) return null;

  for (const sale of flashSales) {
    const startMs = sale.starts_at ? Date.parse(sale.starts_at) : Number.NaN;
    const endMs = sale.ends_at ? Date.parse(sale.ends_at) : Number.NaN;
    if (!Number.isFinite(startMs) || !Number.isFinite(endMs) || nowMs < startMs || nowMs >= endMs) {
      continue;
    }
    const matched = (sale.items ?? []).find((it) => it.id === cleanId);
    if (!matched) continue;

    const status = (matched.status ?? "").trim().toUpperCase();
    const isApproved = status === "" || status === "APPROVED";
    if (!isApproved) return null;
    if (matched.sku_id && matched.sku_id !== skuId) return null;
    if ((matched.remaining_slots ?? 0) <= 0) return null;
    const flashPrice = Math.trunc(matched.flash_price_vnd ?? 0);
    if (flashPrice <= 0 || flashPrice >= skuPriceVnd) return null;

    return {
      flashSaleItemId: matched.id,
      flashPriceVnd: flashPrice,
      remainingSlots: Math.trunc(matched.remaining_slots),
      perUserLimit: Math.max(0, Math.trunc(matched.per_user_limit ?? 0)),
      endsAt: sale.ends_at,
    };
  }

  return null;
}

/**
 * Resolves the currently live, approved, in-stock flash sale item for a SKU (used in Cart & Checkout).
 * Strictly ignores upcoming or ended windows and sold-out items.
 */
export function resolveLiveFlashSaleForSku(
  flashSales: GatewayFlashSale[],
  skuId: string,
  skuPriceVnd: number,
  nowMs = Date.now()
): ResolvedLiveFlashItem | null {
  if (!skuId || skuPriceVnd <= 0) return null;

  for (const sale of flashSales) {
    const startMs = sale.starts_at ? Date.parse(sale.starts_at) : Number.NaN;
    const endMs = sale.ends_at ? Date.parse(sale.ends_at) : Number.NaN;
    if (!Number.isFinite(startMs) || !Number.isFinite(endMs) || nowMs < startMs || nowMs >= endMs) {
      continue;
    }
    for (const item of sale.items ?? []) {
      if (item.sku_id !== skuId) continue;
      const status = (item.status ?? "").trim().toUpperCase();
      if (status !== "" && status !== "APPROVED") continue;
      if ((item.remaining_slots ?? 0) <= 0) continue;
      const flashPrice = Math.trunc(item.flash_price_vnd ?? 0);
      if (flashPrice <= 0 || flashPrice >= skuPriceVnd) continue;
      return {
        flashSaleItemId: item.id,
        flashPriceVnd: flashPrice,
        remainingSlots: Math.trunc(item.remaining_slots),
        perUserLimit: Math.max(0, Math.trunc(item.per_user_limit ?? 0)),
        endsAt: sale.ends_at,
      };
    }
  }
  return null;
}

export interface FlashSaleSkuInspection {
  state: "live" | "sold_out" | "upcoming" | "ended" | "none";
  message: string;
}

/**
 * Inspects why a SKU or requested flash_sale_item_id is or isn't eligible for Flash Sale right now,
 * so Cart & Checkout can clearly inform the buyer and let them purchase at regular price.
 */
export function inspectFlashSaleStatusForSku(
  flashSales: GatewayFlashSale[],
  skuId: string,
  requestedFlashItemId?: string,
  nowMs = Date.now()
): FlashSaleSkuInspection {
  const cleanId = requestedFlashItemId?.trim() || "";
  let foundSoldOut = false;
  let foundUpcoming = false;
  let foundEnded = false;

  for (const sale of flashSales) {
    const startMs = sale.starts_at ? Date.parse(sale.starts_at) : Number.NaN;
    const endMs = sale.ends_at ? Date.parse(sale.ends_at) : Number.NaN;
    const matched = (sale.items ?? []).find((it) => {
      const status = (it.status ?? "").trim().toUpperCase();
      if (status !== "" && status !== "APPROVED") return false;
      if (cleanId) return it.id === cleanId && (!it.sku_id || it.sku_id === skuId);
      return it.sku_id === skuId;
    });
    if (!matched) continue;

    if (Number.isFinite(startMs) && nowMs < startMs) {
      foundUpcoming = true;
      continue;
    }
    if (Number.isFinite(endMs) && nowMs >= endMs) {
      foundEnded = true;
      continue;
    }
    if ((matched.remaining_slots ?? 0) > 0) {
      return { state: "live", message: "" };
    }
    foundSoldOut = true;
  }

  if (foundSoldOut) {
    return {
      state: "sold_out",
      message:
        "Suất ưu đãi Flash sale đã hết lượt — bạn vẫn có thể mua sản phẩm này với giá thường.",
    };
  }
  if (foundUpcoming) {
    return {
      state: "upcoming",
      message: "Khung giờ Flash sale chưa mở bán — sản phẩm đang được tính theo giá thường.",
    };
  }
  if (foundEnded) {
    return {
      state: "ended",
      message: "Khung giờ Flash sale đã kết thúc — sản phẩm đang được tính theo giá thường.",
    };
  }
  if (cleanId) {
    return {
      state: "ended",
      message: "Suất Flash sale không còn hiệu lực — bạn có thể tiếp tục mua với giá thường.",
    };
  }
  return { state: "none", message: "" };
}

export interface EffectiveSkuPromotion {
  kind: "flash" | "shop_discount" | "none";
  effectiveUnitPriceVnd: number;
  originalUnitPriceVnd: number;
  flashSaleItemId?: string;
  shopProgramItemId?: string;
  programName?: string;
  discountPercent?: number;
  perUserLimit?: number;
}

/**
 * Enforces the NexusMart single-price rule on a SKU:
 * live flash sale > active shop discount program > regular price.
 * When `optOutFlash` is true, ONLY flash sale is skipped — server still automatically applies
 * any active shop discount program on that SKU (final application obeys preview's applied_shop_program_item_ids).
 */
export function resolveSkuEffectivePromotion(input: {
  regularPriceVnd: number;
  liveFlash?: ResolvedLiveFlashItem | null;
  activeShopDiscount?: GatewayActiveShopDiscount | null;
  optOutFlash?: boolean;
}): EffectiveSkuPromotion {
  const regular = Math.max(0, Math.trunc(input.regularPriceVnd || 0));
  const flash = input.optOutFlash ? null : input.liveFlash;
  if (
    flash &&
    flash.flashPriceVnd > 0 &&
    flash.flashPriceVnd < regular &&
    flash.remainingSlots > 0
  ) {
    const pct = regular > 0 ? Math.round(((regular - flash.flashPriceVnd) / regular) * 100) : 0;
    return {
      kind: "flash",
      effectiveUnitPriceVnd: Math.trunc(flash.flashPriceVnd),
      originalUnitPriceVnd: regular,
      flashSaleItemId: flash.flashSaleItemId,
      discountPercent: pct,
      perUserLimit: flash.perUserLimit,
    };
  }
  const disc = input.activeShopDiscount;
  if (disc && disc.discounted_price_vnd > 0 && disc.discounted_price_vnd < regular) {
    return {
      kind: "shop_discount",
      effectiveUnitPriceVnd: Math.trunc(disc.discounted_price_vnd),
      originalUnitPriceVnd:
        disc.original_price_vnd > disc.discounted_price_vnd
          ? Math.trunc(disc.original_price_vnd)
          : regular,
      shopProgramItemId: disc.item_id,
      programName: disc.program_name || "Giảm giá gian hàng",
      discountPercent: Math.trunc(disc.discount_percent || 0),
      perUserLimit: Math.max(0, Math.trunc(disc.per_user_limit || 0)),
    };
  }
  return {
    kind: "none",
    effectiveUnitPriceVnd: regular,
    originalUnitPriceVnd: regular,
  };
}

export interface FlashLineLimitEvaluation {
  skuId: string;
  exceedsQtyLimit: boolean;
  previewShortfall: boolean;
  ambiguousShopShortfall: boolean;
  needsRegularPriceChoice: boolean;
  reason: string;
}

/**
 * Evaluates active flash sale lines in a shop slice against per-user quantity limits
 * and preview flash_sale_discount_vnd:
 * - Attributes shortfall strictly to matching `flash_sale_item_id` warnings (or the sole flash line in a shop).
 * - Never blames unaffected sibling flash SKUs in the same shop when explicit warnings identify the culprit.
 * - Flags `ambiguousShopShortfall` when 2+ flash lines share unexplained shortfall without item-specific IDs.
 */
export function evaluateShopFlashLines(input: {
  lines: Array<{
    skuId: string;
    quantity: number;
    regularUnitPriceVnd: number;
    flashSaleItemId?: string;
    flashUnitPriceVnd?: number;
    flashPerUserLimit?: number;
    hasActiveShopDiscount?: boolean;
  }>;
  previewShopFlashDiscountVnd?: number | null;
  previewWarnings?: string[];
}): Record<string, FlashLineLimitEvaluation> {
  const out: Record<string, FlashLineLimitEvaluation> = {};
  const flashLines = input.lines.filter(
    (l) =>
      Boolean(l.flashSaleItemId) &&
      typeof l.flashUnitPriceVnd === "number" &&
      l.flashUnitPriceVnd > 0 &&
      l.flashUnitPriceVnd < l.regularUnitPriceVnd
  );
  if (flashLines.length === 0) return out;

  const expectedShopFlashDiscount = flashLines.reduce(
    (sum, l) => sum + (l.regularUnitPriceVnd - l.flashUnitPriceVnd!) * l.quantity,
    0
  );
  const actualShopShortfallVnd =
    typeof input.previewShopFlashDiscountVnd === "number" && expectedShopFlashDiscount > 0
      ? Math.max(0, expectedShopFlashDiscount - input.previewShopFlashDiscountVnd)
      : 0;
  const warningBlob = (input.previewWarnings ?? []).join(" ").toLowerCase();

  // Pass 1: identify explicitly matched lines (qty > perUserLimit or flashSaleItemId in warnings)
  const explicitFlags = new Map<
    string,
    { exceedsQtyLimit: boolean; explicitItemWarning: boolean; expectedDiscountVnd: number }
  >();
  let explainedShortfallVnd = 0;
  let unexplainedCandidateCount = 0;

  for (const line of flashLines) {
    const perUser = Math.max(0, Math.trunc(line.flashPerUserLimit ?? 0));
    const exceedsQtyLimit = perUser > 0 && line.quantity > perUser;
    const explicitItemWarning = Boolean(
      line.flashSaleItemId && warningBlob.includes(line.flashSaleItemId.toLowerCase())
    );
    const expectedDiscountVnd =
      (line.regularUnitPriceVnd - line.flashUnitPriceVnd!) * line.quantity;
    explicitFlags.set(line.skuId, { exceedsQtyLimit, explicitItemWarning, expectedDiscountVnd });
    if (exceedsQtyLimit || explicitItemWarning) {
      explainedShortfallVnd += expectedDiscountVnd;
    } else {
      unexplainedCandidateCount += 1;
    }
  }

  const unexplainedShortfall = actualShopShortfallVnd > explainedShortfallVnd;

  for (const line of flashLines) {
    const perUser = Math.max(0, Math.trunc(line.flashPerUserLimit ?? 0));
    const flags = explicitFlags.get(line.skuId)!;
    const soleUnexplainedLine =
      unexplainedShortfall &&
      !flags.exceedsQtyLimit &&
      !flags.explicitItemWarning &&
      unexplainedCandidateCount === 1;
    const ambiguousShopShortfall =
      unexplainedShortfall &&
      !flags.exceedsQtyLimit &&
      !flags.explicitItemWarning &&
      unexplainedCandidateCount > 1;
    const previewShortfall = flags.explicitItemWarning || soleUnexplainedLine;
    const needsRegularPriceChoice = flags.exceedsQtyLimit || previewShortfall;
    const fallbackTarget = line.hasActiveShopDiscount
      ? "giá chương trình gian hàng"
      : "Mua giá thường";

    let reason = "";
    if (flags.exceedsQtyLimit) {
      reason = line.hasActiveShopDiscount
        ? `Số lượng chọn (${line.quantity}) vượt giới hạn ${perUser} suất Flash sale/tài khoản. Hãy giảm số lượng hoặc bỏ Flash để dùng ${fallbackTarget}.`
        : `Số lượng chọn (${line.quantity}) vượt giới hạn ${perUser} suất Flash sale/tài khoản. Hãy giảm số lượng hoặc chọn ${fallbackTarget}.`;
    } else if (previewShortfall) {
      reason = line.hasActiveShopDiscount
        ? `Bạn đã dùng hết giới hạn lượt mua Flash sale hoặc suất ưu đãi không còn áp dụng cho sản phẩm này. Bỏ Flash để tiếp tục mua theo ${fallbackTarget}.`
        : "Bạn đã dùng hết giới hạn lượt mua Flash sale hoặc suất ưu đãi không còn áp dụng cho sản phẩm này. Chọn Mua giá thường để tiếp tục đặt hàng.";
    } else if (ambiguousShopShortfall) {
      reason =
        "Một hoặc nhiều suất Flash sale trong gian hàng đã hết lượt hoặc vượt giới hạn tài khoản — hãy kiểm tra số lượng hoặc bỏ suất Flash sale không còn hiệu lực.";
    }

    out[line.skuId] = {
      skuId: line.skuId,
      exceedsQtyLimit: flags.exceedsQtyLimit,
      previewShortfall,
      ambiguousShopShortfall,
      needsRegularPriceChoice,
      reason,
    };
  }
  return out;
}

/**
 * Checks whether a shop discount program item is applied in the server's preview breakdown.
 * Before preview loads, returns true optimistically; once preview arrives, strictly honors
 * `applied_shop_program_item_ids` so lines exceeding per-user limit render at regular price.
 */
export function isShopProgramItemApplied(input: {
  shopProgramItemId?: string;
  shopBreakdown?: Pick<
    GatewayShopDiscountBreakdown,
    "applied_shop_program_item_ids" | "shop_program_discount_vnd"
  > | null;
}): boolean {
  if (!input.shopProgramItemId) return false;
  if (!input.shopBreakdown) return true;
  if (Array.isArray(input.shopBreakdown.applied_shop_program_item_ids)) {
    return input.shopBreakdown.applied_shop_program_item_ids.includes(input.shopProgramItemId);
  }
  return (input.shopBreakdown.shop_program_discount_vnd ?? 0) > 0;
}

export type CheckoutSubmitErrorKind =
  | "unauthenticated"
  | "checkout_failed"
  | "idempotency_conflict"
  | "ambiguous_created"
  | "flash_blocked"
  | "definite_failure";

const FLASH_LIMIT_ERROR_CODES = new Set<string>([
  "FLASH_SALE_NOT_ACTIVE",
  "FLASH_UNAVAILABLE_ON_COMMIT",
  "FLASH_SALE_PRICE_CHANGED",
  "FLASH_SALE_SKU_MISMATCH",
  "FLASH_SALE_SHOP_MISMATCH",
  "FLASH_SALE_INVALID_PRICE",
]);

/**
 * Detects checkout errors genuinely caused by flash sale per-user limit, expired window, or exhausted slots
 * so CheckoutClient can offer an explicit opt-out recovery action.
 * Never returns true for ambiguous/forward-recovery errors, stock errors, idempotency conflicts,
 * CHECKOUT_FAILED, SHOP_PICKUP_ADDRESS_REQUIRED, gateway 429 rate-limits, or unrelated FAILED_PRECONDITION.
 */
export function isFlashLimitOrSlotError(
  err: unknown,
  hasFlashSaleItem: boolean,
  paymentMethod?: GatewayPaymentMethod
): boolean {
  if (!hasFlashSaleItem || !err || typeof err !== "object") return false;
  if (isAmbiguousCheckoutSubmitError(err)) return false;
  const e = err as { status?: number; code?: string; message?: string };
  const code = (e.code ?? "").toUpperCase();
  const msg = (e.message ?? "").toLowerCase();
  if (paymentMethod === "COD" && (code === "FLASH_SALE_COD_NOT_ALLOWED" || msg.includes("cod"))) {
    return false;
  }
  if (
    code === "INSUFFICIENT_STOCK" ||
    code === "SKU_INACTIVE" ||
    code === "SKU_NOT_ACTIVE" ||
    code === "STOCK_VERSION_CONFLICT" ||
    code === "IDEMPOTENCY_KEY_CONFLICT" ||
    code === "CHECKOUT_FAILED" ||
    code === "SHOP_PICKUP_ADDRESS_REQUIRED" ||
    code === "VOUCHER_UNAVAILABLE_ON_COMMIT" ||
    code === "SHOP_PROGRAM_UNAVAILABLE_ON_COMMIT" ||
    code === "SHOP_PROGRAM_PRICE_CHANGED" ||
    code === "INSUFFICIENT_COINS" ||
    code === "COD_LIMIT_EXCEEDED" ||
    code === "COD_REJECTED_LIMIT_EXCEEDED" ||
    /insufficient\s+stock|out\s+of\s+stock|hết\s+hàng|không\s+đủ.*tồn\s+kho|idempotency|trùng\s+mã\s+xử\s+lý|đã\s+tạo\s+đơn|địa\s+chỉ\s+lấy\s+hàng/.test(
      msg
    )
  ) {
    return false;
  }
  if (FLASH_LIMIT_ERROR_CODES.has(code)) return true;
  return /flash|suất\s*flash|suất\s*ưu\s*đãi|per-user.*flash|flash.*limit/.test(msg);
}

/**
 * Pure classifier for POST /checkout failures separating deterministic rejections from
 * ambiguous forward-recovery outcomes (where Idempotency-Key must be preserved and flash opt-out hidden)
 * and 409 CHECKOUT_FAILED (D11: prior group with persisted orders failed/cancelled -> must rotate key).
 */
export function classifyCheckoutSubmitError(
  err: unknown,
  ctx?: { hasFlashSaleItem?: boolean; paymentMethod?: GatewayPaymentMethod }
): CheckoutSubmitErrorKind {
  if (err && typeof err === "object") {
    const e = err as { status?: unknown; code?: unknown };
    const status = typeof e.status === "number" ? e.status : undefined;
    const code = typeof e.code === "string" ? e.code.trim().toUpperCase() : "";
    if (status === 401 || code === "UNAUTHENTICATED") {
      return "unauthenticated";
    }
    if (code === "CHECKOUT_FAILED") {
      return "checkout_failed";
    }
    if (code === "IDEMPOTENCY_KEY_CONFLICT") {
      return "idempotency_conflict";
    }
  }
  if (isAmbiguousCheckoutSubmitError(err)) {
    return "ambiguous_created";
  }
  if (isFlashLimitOrSlotError(err, Boolean(ctx?.hasFlashSaleItem), ctx?.paymentMethod)) {
    return "flash_blocked";
  }
  return "definite_failure";
}

export type CheckoutKeyUncertainty = "none" | "ambiguous" | "conflict";

export interface CheckoutSubmitGuardState {
  keyUncertainty: CheckoutKeyUncertainty;
  flashBlocked: boolean;
  checkoutFailedPrior: boolean;
  cancelledReplayPrior: boolean;
}

export const INITIAL_CHECKOUT_SUBMIT_GUARD_STATE: CheckoutSubmitGuardState = {
  keyUncertainty: "none",
  flashBlocked: false,
  checkoutFailedPrior: false,
  cancelledReplayPrior: false,
};

export type CheckoutSubmitEvent =
  | { type: "submit_start" }
  | { type: "submit_success"; terminalCancelled?: boolean }
  | { type: "submit_error"; kind: CheckoutSubmitErrorKind }
  | { type: "explicit_new_order" }
  | { type: "hydrated_pending_key" };

export interface CheckoutSubmitTransitionResult {
  state: CheckoutSubmitGuardState;
  rotateKey: boolean;
  clearStoredKey: boolean;
}

/**
 * P1-1 pure state machine for checkout Idempotency-Key uncertainty:
 * - Never clears `keyUncertainty` on `submit_start` or subsequent 429/401/400/403/flash_blocked errors.
 * - Clears `keyUncertainty` (and stored key) ONLY on:
 *   1) HTTP 200/201 response (`submit_success`),
 *   2) 409 CHECKOUT_FAILED (`submit_error` kind="checkout_failed", rotating key), or
 *   3) Explicit buyer click on "Đặt đơn mới" (`explicit_new_order`, rotating key).
 */
export function reduceCheckoutSubmitState(
  prev: CheckoutSubmitGuardState,
  event: CheckoutSubmitEvent
): CheckoutSubmitTransitionResult {
  if (event.type === "submit_start") {
    return {
      state: {
        keyUncertainty: prev.keyUncertainty,
        flashBlocked: false,
        checkoutFailedPrior: false,
        cancelledReplayPrior: false,
      },
      rotateKey: false,
      clearStoredKey: false,
    };
  }

  if (event.type === "submit_success") {
    return {
      state: {
        keyUncertainty: "none",
        flashBlocked: false,
        checkoutFailedPrior: false,
        cancelledReplayPrior: Boolean(event.terminalCancelled),
      },
      rotateKey: false,
      clearStoredKey: true,
    };
  }

  if (event.type === "explicit_new_order") {
    return {
      state: { ...INITIAL_CHECKOUT_SUBMIT_GUARD_STATE },
      rotateKey: true,
      clearStoredKey: true,
    };
  }

  if (event.type === "hydrated_pending_key") {
    return {
      state: {
        keyUncertainty: "ambiguous",
        flashBlocked: false,
        checkoutFailedPrior: false,
        cancelledReplayPrior: false,
      },
      rotateKey: false,
      clearStoredKey: false,
    };
  }

  // event.type === "submit_error"
  switch (event.kind) {
    case "checkout_failed":
      return {
        state: {
          keyUncertainty: "none",
          flashBlocked: false,
          checkoutFailedPrior: true,
          cancelledReplayPrior: false,
        },
        rotateKey: true,
        clearStoredKey: true,
      };
    case "idempotency_conflict":
      return {
        state: {
          keyUncertainty: "conflict",
          flashBlocked: false,
          checkoutFailedPrior: false,
          cancelledReplayPrior: false,
        },
        rotateKey: false,
        clearStoredKey: false,
      };
    case "ambiguous_created":
      return {
        state: {
          keyUncertainty: "ambiguous",
          flashBlocked: false,
          checkoutFailedPrior: false,
          cancelledReplayPrior: false,
        },
        rotateKey: false,
        clearStoredKey: false,
      };
    case "flash_blocked":
      return {
        state: {
          keyUncertainty: prev.keyUncertainty,
          flashBlocked: prev.keyUncertainty === "none",
          checkoutFailedPrior: false,
          cancelledReplayPrior: false,
        },
        rotateKey: false,
        clearStoredKey: prev.keyUncertainty === "none",
      };
    case "unauthenticated":
    case "definite_failure":
    default:
      return {
        state: {
          keyUncertainty: prev.keyUncertainty,
          flashBlocked: false,
          checkoutFailedPrior: false,
          cancelledReplayPrior: false,
        },
        rotateKey: false,
        clearStoredKey: prev.keyUncertainty === "none",
      };
  }
}

/**
 * P0-1 / P1-1 guard: blocks Flash opt-out/restore mutations (which rotate checkoutIdempotencyKey)
 * while the current key may have already created an order on server, preventing duplicate orders.
 */
export function canMutateCheckoutFlashSelection(state: {
  keyUncertainty?: CheckoutKeyUncertainty;
  ambiguousSubmit?: boolean;
  idempotencyConflict?: boolean;
}): boolean {
  if (state.keyUncertainty !== undefined) {
    return state.keyUncertainty === "none";
  }
  return !state.ambiguousSubmit && !state.idempotencyConflict;
}

export interface CheckoutItemPayloadInput {
  skuId: string;
  quantity: number;
  flashSaleItemId?: string | null;
}

export function buildCheckoutRequestPayload(input: {
  addressId: string;
  items: readonly CheckoutItemPayloadInput[];
  shopVoucherIds: Record<string, string>;
  platformVoucherCode?: string | null;
  freeshipVoucherCode?: string | null;
  coinsToUse: number;
  paymentMethod: GatewayPaymentMethod;
  shippingChoices: GatewayShopShippingChoice[];
}): GatewayCheckoutRequest {
  return {
    address_id: input.addressId,
    from_cart: false,
    items: input.items.map((it) => ({
      sku_id: it.skuId,
      quantity: it.quantity,
      ...(it.flashSaleItemId ? { flash_sale_item_id: it.flashSaleItemId } : {}),
    })),
    ...(Object.keys(input.shopVoucherIds).length > 0
      ? { shop_voucher_ids: input.shopVoucherIds }
      : {}),
    ...(input.platformVoucherCode ? { platform_voucher_id: input.platformVoucherCode } : {}),
    ...(input.freeshipVoucherCode ? { freeship_voucher_id: input.freeshipVoucherCode } : {}),
    coins_to_use: Math.max(0, Math.trunc(input.coinsToUse)),
    payment_method: input.paymentMethod,
    shipping_choices: input.shippingChoices,
  };
}

export type PostCheckoutCartAction = { kind: "none" } | { kind: "clear_skus"; skuIds: string[] };

export function resolvePostCheckoutCartAction(input: {
  isBuyNow: boolean;
  orderedSkuIds: readonly string[];
}): PostCheckoutCartAction {
  if (input.isBuyNow) return { kind: "none" };
  const skuIds = Array.from(new Set(input.orderedSkuIds.map((id) => id.trim()).filter(Boolean)));
  return skuIds.length > 0 ? { kind: "clear_skus", skuIds } : { kind: "none" };
}

const TERMINAL_PAYMENT_STATUSES = new Set([
  "SUCCEEDED",
  "FAILED",
  "EXPIRED",
  "CANCELLED",
  "REFUNDED",
]);

/**
 * P2-3: Detects terminal payment statuses so SSE closes immediately before EOF triggers `onerror`.
 */
export function isTerminalPaymentStatus(rawStatus: string | null | undefined): boolean {
  const norm = String(rawStatus ?? "")
    .trim()
    .toUpperCase();
  return TERMINAL_PAYMENT_STATUSES.has(norm);
}

/**
 * P1-3: Guards RetryPaymentBox fetchPayment responses against stale/superseded payment IDs.
 */
export function shouldApplyPaymentFetchResult(input: {
  targetId: string;
  activePaymentId: string;
  requestSeq: number;
  latestSeq: number;
}): boolean {
  return (
    Boolean(input.targetId) &&
    input.targetId === input.activePaymentId &&
    input.requestSeq === input.latestSeq
  );
}

const PENDING_CHECKOUT_KEY_STORAGE_KEY = "nm_pending_checkout_key_v1";

export interface StoredPendingCheckoutKey {
  key: string;
  sig: string;
}

/**
 * P1-4: Builds the same-tab checkout idempotency signature (`<userId>:cart` or `<userId>:sku:<skuId>`).
 * Deliberately excludes item quantities / flash_sale_item_id so that if an ambiguous submit actually
 * created an order (consuming the flash slot or stock) and the buyer reloads (F5), the old key is
 * still restored and sent — letting the server replay or return 409 IDEMPOTENCY_KEY_CONFLICT.
 *
 * ponytail: chỉ lưu khoá đang chờ ở sessionStorage trong cùng 1 tab để chống tạo đơn trùng qua F5;
 * hai tab khác nhau cùng mở /thanh-toan chấp nhận dùng khoá riêng (không dựng lock/BroadcastChannel đa tab).
 */
export function buildCheckoutIdempotencySignature(input: {
  userId: string;
  buyNowSkuId?: string | null;
}): string {
  const uid = (input.userId ?? "").trim();
  const sku = (input.buyNowSkuId ?? "").trim();
  return sku ? `${uid}:sku:${sku}` : `${uid}:cart`;
}

export function savePendingCheckoutKey(
  record: StoredPendingCheckoutKey,
  storage?: Pick<Storage, "setItem"> | null
): void {
  const target = storage === undefined ? getDefaultSessionStorage() : storage;
  if (!target || !record.key.trim() || !record.sig.trim()) return;
  try {
    target.setItem(
      PENDING_CHECKOUT_KEY_STORAGE_KEY,
      JSON.stringify({ key: record.key.trim(), sig: record.sig.trim() })
    );
  } catch {
    // Ignore storage access/quota failures
  }
}

export function readPendingCheckoutKey(
  expectedSig: string,
  storage?: Pick<Storage, "getItem"> | null
): string | null {
  const target = storage === undefined ? getDefaultSessionStorage() : storage;
  const cleanSig = (expectedSig ?? "").trim();
  if (!target || !cleanSig) return null;
  try {
    const raw = target.getItem(PENDING_CHECKOUT_KEY_STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<StoredPendingCheckoutKey> | null;
    if (
      !parsed ||
      typeof parsed.key !== "string" ||
      typeof parsed.sig !== "string" ||
      !parsed.key.trim() ||
      parsed.sig !== cleanSig
    ) {
      return null;
    }
    return parsed.key.trim();
  } catch {
    return null;
  }
}

export function clearPendingCheckoutKey(storage?: Pick<Storage, "removeItem"> | null): void {
  const target = storage === undefined ? getDefaultSessionStorage() : storage;
  if (!target) return;
  try {
    target.removeItem(PENDING_CHECKOUT_KEY_STORAGE_KEY);
  } catch {
    // Ignore storage access failures
  }
}

const TERMINAL_FAILED_PAYMENT_STATUSES = new Set(["CANCELLED", "EXPIRED", "FAILED"]);

/**
 * P2-6 guard: detects a 200/201 checkout replay whose persisted orders are already CANCELLED
 * or whose payment status is terminal (CANCELLED/EXPIRED/FAILED) so UI does not navigate to
 * a dead payment-waiting page.
 */
export function isTerminalCancelledCheckoutResponse(
  res: Pick<GatewayCheckoutResponse, "orders" | "payment_status"> | null | undefined
): boolean {
  if (!res) return false;
  const orders = Array.isArray(res.orders) ? res.orders : [];
  const allOrdersCancelled =
    orders.length > 0 &&
    orders.every(
      (o) =>
        String(o?.status ?? "")
          .trim()
          .toUpperCase() === "CANCELLED"
    );
  const payStatus = String(res.payment_status ?? "")
    .trim()
    .toUpperCase();
  return allOrdersCancelled || TERMINAL_FAILED_PAYMENT_STATUSES.has(payStatus);
}

/**
 * P2-9 / P2-2 guard: when POST /promotions/preview failed (`preview === null`):
 * - Always block on 400/409 (e.g., 409 SKU_INACTIVE, 400 INVALID_ARGUMENT) regardless of vouchers.
 * - Block on any preview failure whenever the order uses any promotion (Flash sale, shop discount,
 *   voucher, or coins) so Checkout never silently submits unverified prices or `coins_to_use: 0`.
 */
export function getCheckoutPreviewBlockReason(input: {
  preview: GatewayDiscountBreakdown | null;
  previewError: string | null;
  previewErrorStatus?: number | null;
  hasActiveVouchers: boolean;
  hasFlashSaleItem?: boolean;
  hasShopDiscountItem?: boolean;
  useCoins: boolean;
  coinBalance: number;
}): string | null {
  if (input.preview) return null;
  if (
    input.previewError &&
    (input.previewErrorStatus === 400 || input.previewErrorStatus === 409)
  ) {
    return input.previewError;
  }
  const hasAnyPromotion =
    input.hasActiveVouchers ||
    Boolean(input.hasFlashSaleItem) ||
    Boolean(input.hasShopDiscountItem) ||
    (input.useCoins && input.coinBalance > 0);
  if (hasAnyPromotion) {
    return "Chưa xác thực được ưu đãi hoặc số Xu áp dụng từ hệ thống nên tạm chặn đặt hàng để tránh lệch số tiền. Vui lòng bấm Thử lại hoặc điều chỉnh ưu đãi.";
  }
  return null;
}

/**
 * Shared manual voucher preview probe factory used by both /gio-hang and /thanh-toan.
 */
export function createManualVoucherPreviewFn(input: {
  getShopItems: () => Array<{
    shop_id: string;
    shipping_fee_vnd: number;
    items: Array<{ sku_id: string; quantity: number; flash_sale_item_id?: string }>;
  }>;
  shopVoucherIds: Record<string, string>;
  platformVoucherCode: string | null;
  freeshipVoucherCode: string | null;
  postPreview: (
    payload: Record<string, unknown>
  ) => Promise<{ breakdown?: GatewayDiscountBreakdown | null } | null | undefined>;
}): (probe: ManualVoucherProbeArgs) => Promise<GatewayDiscountBreakdown | null> {
  return async (probe) => {
    const res = await input.postPreview({
      shop_items: input.getShopItems(),
      shop_voucher_ids: probe.shopVoucherIds
        ? { ...input.shopVoucherIds, ...probe.shopVoucherIds }
        : input.shopVoucherIds,
      platform_voucher_id:
        probe.platformVoucherId !== undefined
          ? probe.platformVoucherId
          : (input.platformVoucherCode ?? ""),
      freeship_voucher_id:
        probe.freeshipVoucherId !== undefined
          ? probe.freeshipVoucherId
          : (input.freeshipVoucherCode ?? ""),
      coins_to_use: 0,
    });
    return res?.breakdown ?? null;
  };
}

export interface DisplayedLinePricingInput {
  regularUnitPriceVnd: number;
  quantity: number;
  isFlashLine: boolean;
  flashUnitPriceVnd?: number;
  flashShortfall: boolean;
  activeShopDiscount?: Pick<
    GatewayActiveShopDiscount,
    "item_id" | "discounted_price_vnd" | "original_price_vnd" | "program_name"
  > | null;
  shopBreakdown?: GatewayShopDiscountBreakdown | null;
  isInPreview?: boolean;
}

export interface DisplayedLinePricingResult {
  effectiveUnitPriceVnd: number;
  originalUnitPriceVnd?: number;
  lineTotalVnd: number;
  appliedKind: "flash" | "shop_discount" | "regular";
  shopProgramApplied: boolean;
  shopProgramLimitExceeded: boolean;
  shopProgramName?: string;
}

/**
 * Shared line pricing resolver for both Cart and Checkout (P2-15):
 * obeys single-price rule (Flash > Shop Discount > Regular) and falls back cleanly to the SKU's
 * active shop discount program when a Flash sale line hits shortfall/limit (matching calculator.go).
 */
export function resolveDisplayedLinePricing(
  input: DisplayedLinePricingInput
): DisplayedLinePricingResult {
  const regular = Math.max(0, Math.trunc(input.regularUnitPriceVnd || 0));
  const qty = Math.max(1, Math.trunc(input.quantity || 1));

  if (
    input.isFlashLine &&
    !input.flashShortfall &&
    typeof input.flashUnitPriceVnd === "number" &&
    input.flashUnitPriceVnd > 0 &&
    input.flashUnitPriceVnd < regular
  ) {
    const unit = Math.trunc(input.flashUnitPriceVnd);
    return {
      effectiveUnitPriceVnd: unit,
      originalUnitPriceVnd: regular,
      lineTotalVnd: unit * qty,
      appliedKind: "flash",
      shopProgramApplied: false,
      shopProgramLimitExceeded: false,
    };
  }

  const sd = input.activeShopDiscount;
  const sdPrice = sd ? Math.trunc(sd.discounted_price_vnd || 0) : 0;
  if (sd && sd.item_id && sdPrice > 0 && sdPrice < regular) {
    const checkAgainstPreview = Boolean(input.isInPreview && input.shopBreakdown);
    const applied = checkAgainstPreview
      ? isShopProgramItemApplied({
          shopProgramItemId: sd.item_id,
          shopBreakdown: input.shopBreakdown ?? null,
        })
      : true;
    const limitExceeded = checkAgainstPreview && !applied;
    if (applied) {
      const orig =
        typeof sd.original_price_vnd === "number" && Math.trunc(sd.original_price_vnd) > sdPrice
          ? Math.max(regular, Math.trunc(sd.original_price_vnd))
          : regular;
      return {
        effectiveUnitPriceVnd: sdPrice,
        originalUnitPriceVnd: orig,
        lineTotalVnd: sdPrice * qty,
        appliedKind: "shop_discount",
        shopProgramApplied: true,
        shopProgramLimitExceeded: false,
        shopProgramName: sd.program_name,
      };
    }
    return {
      effectiveUnitPriceVnd: regular,
      originalUnitPriceVnd: undefined,
      lineTotalVnd: regular * qty,
      appliedKind: "regular",
      shopProgramApplied: false,
      shopProgramLimitExceeded: limitExceeded,
      shopProgramName: sd.program_name,
    };
  }

  return {
    effectiveUnitPriceVnd: regular,
    originalUnitPriceVnd: undefined,
    lineTotalVnd: regular * qty,
    appliedKind: "regular",
    shopProgramApplied: false,
    shopProgramLimitExceeded: false,
  };
}

const CHECKOUT_WARNINGS_STORAGE_KEY = "nm_checkout_warnings_v1";
const CHECKOUT_WARNINGS_TTL_MS = 10 * 60 * 1000;

interface StoredCheckoutWarnings {
  paymentId: string;
  orderIds: string[];
  checkoutGroupId: string;
  warnings: string[];
  savedAt: number;
}

function getDefaultSessionStorage(): Storage | null {
  if (typeof window === "undefined") return null;
  try {
    return window.sessionStorage;
  } catch {
    return null;
  }
}

export function translatePromotionWarnings(warnings?: string[] | null): string[] {
  if (!Array.isArray(warnings) || warnings.length === 0) return [];
  return Array.from(new Set(warnings.map((w) => translateVoucherReason(w)).filter(Boolean)));
}

export function saveCheckoutWarnings(
  input: {
    paymentId?: string;
    orderIds?: string[];
    checkoutGroupId?: string;
    warnings?: string[] | null;
  },
  storage?: Pick<Storage, "setItem" | "removeItem"> | null
): void {
  const target = storage === undefined ? getDefaultSessionStorage() : storage;
  if (!target) return;
  const translated = translatePromotionWarnings(input.warnings);
  if (translated.length === 0) {
    try {
      target.removeItem(CHECKOUT_WARNINGS_STORAGE_KEY);
    } catch {
      // Ignore storage access failures
    }
    return;
  }
  const payload: StoredCheckoutWarnings = {
    paymentId: (input.paymentId ?? "").trim(),
    orderIds: Array.from(new Set((input.orderIds ?? []).map((id) => id.trim()).filter(Boolean))),
    checkoutGroupId: (input.checkoutGroupId ?? "").trim(),
    warnings: translated,
    savedAt: Date.now(),
  };
  try {
    target.setItem(CHECKOUT_WARNINGS_STORAGE_KEY, JSON.stringify(payload));
  } catch {
    // Ignore storage quota/access failures
  }
}

export function readCheckoutWarnings(
  query: { paymentId?: string; orderId?: string; orderIds?: string[] },
  storage?: Pick<Storage, "getItem" | "removeItem"> | null
): string[] {
  const target = storage === undefined ? getDefaultSessionStorage() : storage;
  if (!target) return [];
  try {
    const raw = target.getItem(CHECKOUT_WARNINGS_STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as Partial<StoredCheckoutWarnings> | null;
    if (!parsed || !Array.isArray(parsed.warnings) || parsed.warnings.length === 0) return [];
    if (
      typeof parsed.savedAt === "number" &&
      Date.now() - parsed.savedAt > CHECKOUT_WARNINGS_TTL_MS
    ) {
      target.removeItem(CHECKOUT_WARNINGS_STORAGE_KEY);
      return [];
    }
    const storedOrderIds = Array.isArray(parsed.orderIds) ? parsed.orderIds : [];
    const matchPayment = Boolean(
      query.paymentId && parsed.paymentId && parsed.paymentId === query.paymentId
    );
    const matchOrder = Boolean(query.orderId && storedOrderIds.includes(query.orderId));
    const matchAnyOrder = Boolean(
      Array.isArray(query.orderIds) && query.orderIds.some((id) => storedOrderIds.includes(id))
    );
    if (matchPayment || matchOrder || matchAnyOrder) {
      return parsed.warnings.filter((w): w is string => typeof w === "string" && Boolean(w.trim()));
    }
    return [];
  } catch {
    return [];
  }
}

export function clearCheckoutWarnings(storage?: Pick<Storage, "removeItem"> | null): void {
  const target = storage === undefined ? getDefaultSessionStorage() : storage;
  if (!target) return;
  try {
    target.removeItem(CHECKOUT_WARNINGS_STORAGE_KEY);
  } catch {
    // Ignore storage access failures
  }
}

export type CodBlockCode =
  | "FLASH_SALE"
  | "PHONE_NOT_VERIFIED"
  | "AREA_NOT_SUPPORTED"
  | "REJECTED_LIMIT"
  | "AMOUNT_LIMIT"
  | "AMOUNT_BELOW_MIN";

export type PaymentMethodBlockCode = CodBlockCode | "METHOD_DISABLED" | "AMOUNT_EXCEEDED";

/**
 * Checks whether COD is allowed for the current checkout payload.
 * Flash sale orders strictly require online payment (VietQR or ZaloPay, 5-minute payment expiry).
 * When `codMaxVnd` is supplied from GET /api/v1/payment-methods:
 *   - `codMaxVnd === 0` means no upper limit;
 *   - `codMaxVnd > 0` caps COD at `codMaxVnd`.
 * When `codMaxVnd` is omitted (legacy callers/tests), falls back to DEFAULT_COD_MAX_VND.
 */
export function isCodAllowedForCheckout(input: {
  hasFlashSaleItem: boolean;
  totalPayableVnd: number;
  codRejectedCount: number;
  phoneVerified?: boolean;
  codAreaSupported?: boolean;
  codMinVnd?: number;
  codMaxVnd?: number;
}): { allowed: boolean; reason: string; blockCode?: CodBlockCode } {
  if (input.hasFlashSaleItem) {
    return {
      allowed: false,
      blockCode: "FLASH_SALE",
      reason:
        "Đơn hàng có sản phẩm Flash sale chỉ hỗ trợ thanh toán trực tuyến (VietQR hoặc ZaloPay, hạn thanh toán 5 phút).",
    };
  }
  if (input.phoneVerified === false) {
    return {
      allowed: false,
      blockCode: "PHONE_NOT_VERIFIED",
      reason:
        "Vui lòng xác minh số điện thoại trong mục Tài khoản trước khi đặt hàng thanh toán khi nhận hàng (COD).",
    };
  }
  if (input.codAreaSupported === false) {
    return {
      allowed: false,
      blockCode: "AREA_NOT_SUPPORTED",
      reason:
        "Khu vực giao hàng này hiện chưa hỗ trợ thanh toán khi nhận hàng (COD). Vui lòng đổi địa chỉ hoặc chọn thanh toán trực tuyến.",
    };
  }
  if (input.codRejectedCount >= DEFAULT_COD_MAX_REJECTED) {
    return {
      allowed: false,
      blockCode: "REJECTED_LIMIT",
      reason: `Tài khoản có ${input.codRejectedCount} lần từ chối nhận hàng nên tạm không hỗ trợ COD. Vui lòng chọn thanh toán trực tuyến.`,
    };
  }
  const minVnd = typeof input.codMinVnd === "number" && input.codMinVnd > 0 ? input.codMinVnd : 0;
  if (input.totalPayableVnd > 0 && minVnd > 0 && input.totalPayableVnd < minVnd) {
    return {
      allowed: false,
      blockCode: "AMOUNT_BELOW_MIN",
      reason: `Đơn hàng cần tối thiểu ${formatVnd(minVnd)} để thanh toán khi nhận hàng (COD).`,
    };
  }
  const effectiveMaxVnd =
    typeof input.codMaxVnd === "number" ? input.codMaxVnd : DEFAULT_COD_MAX_VND;
  if (effectiveMaxVnd > 0 && input.totalPayableVnd > effectiveMaxVnd) {
    return {
      allowed: false,
      blockCode: "AMOUNT_LIMIT",
      reason: `Đơn hàng vượt hạn mức COD tối đa ${formatVnd(effectiveMaxVnd)}. Vui lòng chọn thanh toán trực tuyến.`,
    };
  }
  return { allowed: true, reason: "" };
}

/**
 * Evaluates a payment method config from GET /api/v1/payment-methods against current checkout inputs (F6).
 */
export function evaluatePaymentMethodForCheckout(
  config: Pick<
    GatewayPaymentMethodConfig,
    "method" | "enabled" | "min_order_vnd" | "max_order_vnd"
  >,
  params: {
    totalPayableVnd: number;
    hasFlashSaleItem: boolean;
    codRejectedCount: number;
    phoneVerified?: boolean;
    codAreaSupported?: boolean;
  }
): { allowed: boolean; reason: string; blockCode?: PaymentMethodBlockCode } {
  const method = String(config.method ?? "")
    .trim()
    .toUpperCase();
  if (!config.enabled) {
    return {
      allowed: false,
      blockCode: "METHOD_DISABLED",
      reason: "Phương thức thanh toán này hiện đang tạm tắt.",
    };
  }
  if (method === "COD") {
    const res = isCodAllowedForCheckout({
      hasFlashSaleItem: params.hasFlashSaleItem,
      totalPayableVnd: params.totalPayableVnd,
      codRejectedCount: params.codRejectedCount,
      phoneVerified: params.phoneVerified,
      codAreaSupported: params.codAreaSupported,
      codMinVnd: Math.max(0, Math.trunc(config.min_order_vnd || 0)),
      codMaxVnd: Math.max(0, Math.trunc(config.max_order_vnd || 0)),
    });
    if (!res.allowed && res.blockCode === "AMOUNT_LIMIT") {
      return { allowed: false, blockCode: "AMOUNT_EXCEEDED", reason: res.reason };
    }
    return res;
  }
  const minVnd = Math.max(0, Math.trunc(config.min_order_vnd || 0));
  const maxVnd = Math.max(0, Math.trunc(config.max_order_vnd || 0));
  if (params.totalPayableVnd > 0 && minVnd > 0 && params.totalPayableVnd < minVnd) {
    return {
      allowed: false,
      blockCode: "AMOUNT_BELOW_MIN",
      reason: `Đơn hàng cần tối thiểu ${formatVnd(minVnd)} để sử dụng phương thức này.`,
    };
  }
  if (maxVnd > 0 && params.totalPayableVnd > maxVnd) {
    return {
      allowed: false,
      blockCode: "AMOUNT_EXCEEDED",
      reason: `Đơn hàng vượt quá hạn mức tối đa ${formatVnd(maxVnd)} của phương thức này.`,
    };
  }
  return { allowed: true, reason: "" };
}

/**
 * Resolves the post-checkout destination route:
 * - Zero-VND orders (total_payable_vnd <= 0) or already-SUCCEEDED payments are immediately CONFIRMED
 *   by the backend and MUST go straight to the order detail/list page (never to VietQR/ZaloPay waiting).
 * - Positive-amount pending online payments (VietQR / ZaloPay) go to `/thanh-toan/cho/[paymentId]`.
 */
export function resolvePostCheckoutHref(
  res: Pick<
    GatewayCheckoutResponse,
    "payment_method" | "payment_id" | "payment_status" | "total_payable_vnd"
  > & { orders?: ReadonlyArray<{ id: string }> }
): string {
  const method = String(res.payment_method ?? "")
    .trim()
    .toUpperCase();
  const isOnline = method === "VIETQR" || method === "ZALOPAY";
  const paymentStatus = String(res.payment_status ?? "")
    .trim()
    .toUpperCase();
  const payable = typeof res.total_payable_vnd === "number" ? res.total_payable_vnd : 0;

  if (isOnline && res.payment_id && payable > 0 && paymentStatus !== "SUCCEEDED") {
    return `/thanh-toan/cho/${encodeURIComponent(res.payment_id)}`;
  }
  if (Array.isArray(res.orders) && res.orders.length === 1 && res.orders[0]?.id) {
    return `/don-hang/${encodeURIComponent(res.orders[0].id)}`;
  }
  return "/don-hang";
}

const CHECKOUT_METHOD_ORDER: readonly GatewayPaymentMethod[] = ["COD", "VIETQR", "ZALOPAY"];

/**
 * Picks the best enabled checkout payment method (keeping currentMethod when still eligible).
 */
export function resolveDefaultCheckoutMethod(
  methods: readonly Pick<GatewayPaymentMethodConfig, "method" | "enabled">[],
  params: { hasFlashSaleItem: boolean; currentMethod?: GatewayPaymentMethod }
): GatewayPaymentMethod | null {
  const enabledSet = new Set<GatewayPaymentMethod>();
  for (const m of methods) {
    if (!m?.enabled) continue;
    const key = String(m.method ?? "")
      .trim()
      .toUpperCase();
    if (key === "COD" || key === "VIETQR" || key === "ZALOPAY") {
      if (params.hasFlashSaleItem && key === "COD") continue;
      enabledSet.add(key);
    }
  }
  if (params.currentMethod && enabledSet.has(params.currentMethod)) {
    return params.currentMethod;
  }
  const priority: readonly GatewayPaymentMethod[] = params.hasFlashSaleItem
    ? ["VIETQR", "ZALOPAY"]
    : CHECKOUT_METHOD_ORDER;
  for (const candidate of priority) {
    if (enabledSet.has(candidate)) return candidate;
  }
  return null;
}

export interface BuyNowSlicePricing {
  regularSubtotalVnd: number;
  flashDiscountVnd: number;
  effectiveUnitPriceVnd: number;
  effectiveSubtotalVnd: number;
}

/**
 * Computes integer VND regular subtotal, flash discount, and post-flash effective subtotal
 * matching OrderService + PromotionService checkout calculation.
 */
export function computeBuyNowSlicePricing(input: {
  unitPriceVnd: number;
  quantity: number;
  flashSaleItemId?: string;
  flashPriceVnd?: number;
}): BuyNowSlicePricing {
  const qty = Math.max(1, Math.trunc(input.quantity || 1));
  const regularUnit = Math.max(0, Math.trunc(input.unitPriceVnd || 0));
  const hasValidFlash = Boolean(
    input.flashSaleItemId &&
    typeof input.flashPriceVnd === "number" &&
    input.flashPriceVnd > 0 &&
    input.flashPriceVnd < regularUnit
  );
  const effectiveUnitPriceVnd = hasValidFlash ? Math.trunc(input.flashPriceVnd!) : regularUnit;
  const regularSubtotalVnd = regularUnit * qty;
  const effectiveSubtotalVnd = effectiveUnitPriceVnd * qty;
  const flashDiscountVnd = Math.max(0, regularSubtotalVnd - effectiveSubtotalVnd);

  return {
    regularSubtotalVnd,
    flashDiscountVnd,
    effectiveUnitPriceVnd,
    effectiveSubtotalVnd,
  };
}

export const GENERIC_UNKNOWN_VOUCHER_MESSAGE =
  "Mã ưu đãi không tồn tại, không còn hiệu lực hoặc không áp dụng cho đơn hàng hiện tại.";

export interface ManualVoucherShopContext {
  shopId: string;
  shopName: string;
  subtotalVnd: number;
  shippingFeeVnd: number;
}

export interface ManualVoucherProbeArgs {
  platformVoucherId?: string;
  freeshipVoucherId?: string;
  shopVoucherIds?: Record<string, string>;
}

export type ManualVoucherResolution =
  | {
      ok: true;
      scope: "PLATFORM" | "FREESHIP" | "SHOP";
      code: string;
      shopId?: string;
      discountVnd?: number;
      message: string;
    }
  | {
      ok: false;
      scope?: "PLATFORM" | "FREESHIP" | "SHOP";
      message: string;
    };

function extractVoucherWarningsForCode(
  warnings: readonly string[] | undefined,
  upperCode: string
): string[] {
  if (!Array.isArray(warnings) || warnings.length === 0) return [];
  const lowerCode = upperCode.toLowerCase();
  return warnings.filter((w) => {
    const lower = (w ?? "").toLowerCase();
    return lower.includes("voucher") && lower.includes(lowerCode);
  });
}

function isHiddenOrUnavailableVoucherWarning(lower: string): boolean {
  return (
    lower.includes("not found") ||
    lower.includes("has been deleted") ||
    lower.includes("is paused") ||
    lower.includes("is inactive") ||
    lower.includes("has not started") ||
    lower.includes("has expired")
  );
}

function parseVoucherScopeHint(lower: string): "PLATFORM" | "FREESHIP" | "SHOP" | null {
  const m = /scope\s+(platform|freeship|shop)\s+does\s+not\s+match/i.exec(lower);
  if (!m) return null;
  const scope = m[1].toUpperCase();
  if (scope === "PLATFORM" || scope === "FREESHIP" || scope === "SHOP") {
    return scope;
  }
  return null;
}

function isZeroDiscountVoucherWarning(lower: string): boolean {
  return lower.includes("provides 0 vnd") || /\b0\s*vnd\s+discount\b/i.test(lower);
}

function isOrderConditionVoucherWarning(lower: string): boolean {
  return (
    lower.includes("minimum order") ||
    lower.includes("shortfall") ||
    lower.includes("category") ||
    lower.includes("categories") ||
    lower.includes("out of quota") ||
    lower.includes("quota") ||
    lower.includes("budget") ||
    lower.includes("per-user") ||
    lower.includes("limit reached") ||
    isZeroDiscountVoucherWarning(lower)
  );
}

/**
 * Resolves a manually entered voucher code across PLATFORM, FREESHIP, and SHOP scopes (P2-3 #3):
 * - Matches visible vouchers first with scope-specific checks.
 * - For codes outside the visible list, probes PLATFORM -> FREESHIP -> SHOP via `previewFn`
 *   (skipping unnecessary probes using scope mismatch hints).
 * - Reports scope-specific eligibility errors for active vouchers in the current cart/order,
 *   while returning a single uniform generic error for not-found, paused, deleted, inactive,
 *   not-started, expired, or other-shop vouchers so hidden codes are never leaked.
 */
export async function resolveManualVoucherCode(input: {
  rawCode: string;
  knownVouchers: readonly GatewayVoucher[];
  shops: readonly ManualVoucherShopContext[];
  hasShippingEstimate: boolean;
  previewFn: (probe: ManualVoucherProbeArgs) => Promise<GatewayDiscountBreakdown | null>;
}): Promise<ManualVoucherResolution> {
  const clean = (input.rawCode ?? "").trim().toUpperCase();
  if (!clean) {
    return { ok: false, message: "Vui lòng nhập mã ưu đãi cần áp dụng." };
  }
  if (input.shops.length === 0) {
    return {
      ok: false,
      message: "Hãy chọn ít nhất 1 sản phẩm trước khi áp dụng mã ưu đãi.",
    };
  }

  const matched = input.knownVouchers.find((v) => v.code.toUpperCase() === clean);
  if (matched) {
    const scope = matched.scope.toUpperCase();
    const statusActive = !matched.status || matched.status.toUpperCase() === "ACTIVE";
    const baseEligible =
      matched.eligible !== false &&
      statusActive &&
      matched.is_active !== false &&
      matched.remaining_quota > 0;

    if (scope === "SHOP") {
      const targetShop = input.shops.find((s) => s.shopId === matched.shop_id);
      if (!targetShop) {
        return {
          ok: false,
          scope: "SHOP",
          message: "Đơn hàng hiện không có sản phẩm thuộc gian hàng áp dụng mã này.",
        };
      }
      if (targetShop.subtotalVnd < matched.min_order_vnd) {
        return {
          ok: false,
          scope: "SHOP",
          message: `Mã gian hàng: Chưa đạt giá trị đơn tối thiểu ${formatVnd(matched.min_order_vnd)} của ${targetShop.shopName}.`,
        };
      }
      if (!baseEligible) {
        return {
          ok: false,
          scope: "SHOP",
          message: `Mã gian hàng: ${translateVoucherReason(matched.ineligible_reason) || `Mã ${matched.code} chưa đủ điều kiện áp dụng.`}`,
        };
      }
      // P2-2: Always confirm known SHOP vouchers through server previewFn (category_ids, per-user limit, post-discount min order, etc.).
      const shopBdKnown = await input.previewFn({
        shopVoucherIds: { [matched.shop_id]: matched.code },
      });
      const appliedShopKnown = (shopBdKnown?.shops ?? []).find(
        (s) => s.shop_id === matched.shop_id && s.shop_voucher_discount_vnd > 0
      );
      if (appliedShopKnown) {
        return {
          ok: true,
          scope: "SHOP",
          shopId: matched.shop_id,
          code: matched.code,
          discountVnd: appliedShopKnown.shop_voucher_discount_vnd,
          message: `Đã áp dụng mã gian hàng ${targetShop.shopName} (${matched.code}, giảm ${formatVnd(appliedShopKnown.shop_voucher_discount_vnd)}).`,
        };
      }
      const knownShopWarnings = extractVoucherWarningsForCode(
        shopBdKnown?.warnings,
        matched.code
      ).filter((w) => !w.toLowerCase().includes("does not belong to shop"));
      const knownShopCond = knownShopWarnings.find((w) =>
        isOrderConditionVoucherWarning(w.toLowerCase())
      );
      return {
        ok: false,
        scope: "SHOP",
        message: `Mã gian hàng: ${knownShopCond ? translateVoucherReason(knownShopCond) : `Mã ${matched.code} chưa đủ điều kiện áp dụng cho sản phẩm của ${targetShop.shopName}.`}`,
      };
    }

    if (scope === "FREESHIP") {
      if (!baseEligible) {
        return {
          ok: false,
          scope: "FREESHIP",
          message: `Mã vận chuyển: ${translateVoucherReason(matched.ineligible_reason) || `Mã ${matched.code} chưa đủ điều kiện áp dụng.`}`,
        };
      }
      // P1-5: Always confirm known FREESHIP vouchers through server previewFn before claiming applied.
      const shipBd = await input.previewFn({ freeshipVoucherId: matched.code });
      if (shipBd && shipBd.total_freeship_discount_vnd > 0) {
        return {
          ok: true,
          scope: "FREESHIP",
          code: matched.code,
          discountVnd: shipBd.total_freeship_discount_vnd,
          message: `Đã áp dụng mã vận chuyển ${matched.code} (giảm ${formatVnd(shipBd.total_freeship_discount_vnd)}).`,
        };
      }
      const shipWarnings = extractVoucherWarningsForCode(shipBd?.warnings, matched.code);
      const shipCond = shipWarnings.find((w) => isOrderConditionVoucherWarning(w.toLowerCase()));
      if (shipCond && isZeroDiscountVoucherWarning(shipCond.toLowerCase())) {
        const totalShipFee = input.shops.reduce((s, sh) => s + Math.max(0, sh.shippingFeeVnd), 0);
        return {
          ok: false,
          scope: "FREESHIP",
          message:
            input.hasShippingEstimate && totalShipFee > 0
              ? "Mã vận chuyển: Phí vận chuyển của đơn đã được hỗ trợ tối đa (0₫)."
              : "Mã vận chuyển: Chưa có phí vận chuyển để áp dụng (hãy chọn địa chỉ giao hàng ở bước thanh toán).",
        };
      }
      return {
        ok: false,
        scope: "FREESHIP",
        message: `Mã vận chuyển: ${shipCond ? translateVoucherReason(shipCond) : `Mã ${matched.code} chưa đủ điều kiện áp dụng cho đơn hàng hiện tại.`}`,
      };
    }

    if (!baseEligible) {
      return {
        ok: false,
        scope: "PLATFORM",
        message: `Mã toàn sàn: ${translateVoucherReason(matched.ineligible_reason) || `Mã ${matched.code} chưa đủ điều kiện áp dụng.`}`,
      };
    }
    // P1-5: Always confirm known PLATFORM vouchers through server previewFn (category_ids, post-flash totals, etc.).
    const platBdKnown = await input.previewFn({ platformVoucherId: matched.code });
    if (platBdKnown && platBdKnown.total_platform_voucher_discount_vnd > 0) {
      return {
        ok: true,
        scope: "PLATFORM",
        code: matched.code,
        discountVnd: platBdKnown.total_platform_voucher_discount_vnd,
        message: `Đã áp dụng mã toàn sàn ${matched.code} (giảm ${formatVnd(platBdKnown.total_platform_voucher_discount_vnd)}).`,
      };
    }
    const knownPlatWarnings = extractVoucherWarningsForCode(platBdKnown?.warnings, matched.code);
    const knownPlatCond = knownPlatWarnings.find((w) =>
      isOrderConditionVoucherWarning(w.toLowerCase())
    );
    return {
      ok: false,
      scope: "PLATFORM",
      message: `Mã toàn sàn: ${knownPlatCond ? translateVoucherReason(knownPlatCond) : `Mã ${matched.code} chưa đủ điều kiện áp dụng cho đơn hàng hiện tại.`}`,
    };
  }

  // Unlisted code: probe PLATFORM first, then narrow to FREESHIP / SHOP.
  const platBd = await input.previewFn({ platformVoucherId: clean });
  if (platBd && platBd.total_platform_voucher_discount_vnd > 0) {
    return {
      ok: true,
      scope: "PLATFORM",
      code: clean,
      discountVnd: platBd.total_platform_voucher_discount_vnd,
      message: `Đã áp dụng mã toàn sàn ${clean} (giảm ${formatVnd(platBd.total_platform_voucher_discount_vnd)}).`,
    };
  }

  const platWarnings = extractVoucherWarningsForCode(platBd?.warnings, clean);
  let scopeHint: "PLATFORM" | "FREESHIP" | "SHOP" | null = null;
  for (const w of platWarnings) {
    const lower = w.toLowerCase();
    if (isHiddenOrUnavailableVoucherWarning(lower)) {
      return { ok: false, message: GENERIC_UNKNOWN_VOUCHER_MESSAGE };
    }
    const hint = parseVoucherScopeHint(lower);
    if (hint) {
      scopeHint = hint;
      break;
    }
  }
  if (!scopeHint) {
    const platCond = platWarnings.find((w) => isOrderConditionVoucherWarning(w.toLowerCase()));
    if (platCond) {
      return {
        ok: false,
        scope: "PLATFORM",
        message: `Mã toàn sàn: ${translateVoucherReason(platCond)}`,
      };
    }
  }

  // Probe FREESHIP when hinted or when PLATFORM probe was inconclusive
  let shipScopeHint: "PLATFORM" | "FREESHIP" | "SHOP" | null = null;
  if (scopeHint === "FREESHIP" || !scopeHint) {
    const shipBd = await input.previewFn({ freeshipVoucherId: clean });
    if (shipBd && shipBd.total_freeship_discount_vnd > 0) {
      return {
        ok: true,
        scope: "FREESHIP",
        code: clean,
        discountVnd: shipBd.total_freeship_discount_vnd,
        message: `Đã áp dụng mã vận chuyển ${clean} (giảm ${formatVnd(shipBd.total_freeship_discount_vnd)}).`,
      };
    }
    const shipWarnings = extractVoucherWarningsForCode(shipBd?.warnings, clean);
    for (const w of shipWarnings) {
      const lower = w.toLowerCase();
      if (isHiddenOrUnavailableVoucherWarning(lower)) {
        return { ok: false, message: GENERIC_UNKNOWN_VOUCHER_MESSAGE };
      }
      const hint = parseVoucherScopeHint(lower);
      if (hint) {
        shipScopeHint = hint;
        break;
      }
    }
    if (!shipScopeHint) {
      const shipCond = shipWarnings.find((w) => isOrderConditionVoucherWarning(w.toLowerCase()));
      if (shipCond) {
        const lower = shipCond.toLowerCase();
        if (isZeroDiscountVoucherWarning(lower)) {
          const totalShipFee = input.shops.reduce((s, sh) => s + Math.max(0, sh.shippingFeeVnd), 0);
          return {
            ok: false,
            scope: "FREESHIP",
            message:
              input.hasShippingEstimate && totalShipFee > 0
                ? "Mã vận chuyển: Phí vận chuyển của đơn đã được hỗ trợ tối đa (0₫)."
                : "Mã vận chuyển: Chưa có phí vận chuyển để áp dụng (hãy chọn địa chỉ giao hàng ở bước thanh toán).",
          };
        }
        return {
          ok: false,
          scope: "FREESHIP",
          message: `Mã vận chuyển: ${translateVoucherReason(shipCond)}`,
        };
      }
    }
  }

  // Probe SHOP across all shops currently selected in cart/checkout
  if (scopeHint === "SHOP" || shipScopeHint === "SHOP" || !scopeHint) {
    const shopVoucherProbe: Record<string, string> = {};
    for (const sh of input.shops) {
      shopVoucherProbe[sh.shopId] = clean;
    }
    const shopBd = await input.previewFn({ shopVoucherIds: shopVoucherProbe });
    const appliedShop = (shopBd?.shops ?? []).find((s) => s.shop_voucher_discount_vnd > 0);
    if (appliedShop) {
      const matchedShop = input.shops.find((s) => s.shopId === appliedShop.shop_id);
      const shopLabel = matchedShop?.shopName ? ` ${matchedShop.shopName}` : "";
      return {
        ok: true,
        scope: "SHOP",
        shopId: appliedShop.shop_id,
        code: clean,
        discountVnd: appliedShop.shop_voucher_discount_vnd,
        message: `Đã áp dụng mã gian hàng${shopLabel} (${clean}, giảm ${formatVnd(appliedShop.shop_voucher_discount_vnd)}).`,
      };
    }

    const shopWarnings = extractVoucherWarningsForCode(shopBd?.warnings, clean).filter(
      (w) => !w.toLowerCase().includes("does not belong to shop")
    );
    for (const w of shopWarnings) {
      if (isHiddenOrUnavailableVoucherWarning(w.toLowerCase())) {
        return { ok: false, message: GENERIC_UNKNOWN_VOUCHER_MESSAGE };
      }
    }
    const shopCond = shopWarnings.find((w) => isOrderConditionVoucherWarning(w.toLowerCase()));
    if (shopCond) {
      return {
        ok: false,
        scope: "SHOP",
        message: `Mã gian hàng: ${translateVoucherReason(shopCond)}`,
      };
    }
  }

  return { ok: false, message: GENERIC_UNKNOWN_VOUCHER_MESSAGE };
}
