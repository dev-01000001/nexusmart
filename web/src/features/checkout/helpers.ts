import type {
  GatewayActiveShopDiscount,
  GatewayFlashSale,
  GatewayShopDiscountBreakdown,
} from "@/lib/gateway-types";
import {
  AMBIGUOUS_CHECKOUT_MESSAGE,
  DEFAULT_COD_MAX_REJECTED,
  DEFAULT_COD_MAX_VND,
  isAmbiguousCheckoutSubmitError,
  translateVoucherReason,
} from "./messages";
import { formatVnd } from "@/lib/format";

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
  const results = await Promise.all(batches.map((b) => fetcher(b)));
  const seenSkuIds = new Set<string>();
  const merged: GatewayActiveShopDiscount[] = [];
  for (const list of results) {
    for (const disc of list) {
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
  paymentMethod?: "COD" | "VIETQR";
  weightGrams?: number;
  codAmountVnd?: number;
}

export interface CalculateFeePayload {
  shop_id: string;
  to_province_code: string;
  to_ward_code: string;
  items?: Array<{ sku_id: string; quantity: number }>;
  payment_method?: "COD" | "VIETQR";
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
 * gateway 429 rate-limits, or unrelated FAILED_PRECONDITION.
 */
export function isFlashLimitOrSlotError(
  err: unknown,
  hasFlashSaleItem: boolean,
  paymentMethod?: "COD" | "VIETQR"
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
    code === "VOUCHER_UNAVAILABLE_ON_COMMIT" ||
    code === "SHOP_PROGRAM_UNAVAILABLE_ON_COMMIT" ||
    code === "SHOP_PROGRAM_PRICE_CHANGED" ||
    code === "INSUFFICIENT_COINS" ||
    code === "COD_LIMIT_EXCEEDED" ||
    code === "COD_REJECTED_LIMIT_EXCEEDED" ||
    /insufficient\s+stock|out\s+of\s+stock|hết\s+hàng|không\s+đủ.*tồn\s+kho|idempotency|trùng\s+mã\s+xử\s+lý|đã\s+tạo\s+đơn/.test(
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
 * ambiguous forward-recovery outcomes (where Idempotency-Key must be preserved and flash opt-out hidden).
 */
export function classifyCheckoutSubmitError(
  err: unknown,
  ctx?: { hasFlashSaleItem?: boolean; paymentMethod?: "COD" | "VIETQR" }
): CheckoutSubmitErrorKind {
  if (err && typeof err === "object") {
    const e = err as { status?: unknown; code?: unknown };
    const status = typeof e.status === "number" ? e.status : undefined;
    const code = typeof e.code === "string" ? e.code.trim().toUpperCase() : "";
    if (status === 401 || code === "UNAUTHENTICATED") {
      return "unauthenticated";
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

/**
 * Checks whether COD is allowed for the current checkout payload.
 * Flash sale orders strictly require VietQR (5-minute payment expiry).
 */
export function isCodAllowedForCheckout(input: {
  hasFlashSaleItem: boolean;
  totalPayableVnd: number;
  codRejectedCount: number;
}): { allowed: boolean; reason: string } {
  if (input.hasFlashSaleItem) {
    return {
      allowed: false,
      reason:
        "Đơn hàng có sản phẩm Flash sale chỉ hỗ trợ chuyển khoản VietQR (hạn thanh toán 5 phút).",
    };
  }
  if (input.codRejectedCount >= DEFAULT_COD_MAX_REJECTED) {
    return {
      allowed: false,
      reason: `Tài khoản có ${input.codRejectedCount} lần từ chối nhận hàng nên tạm không hỗ trợ COD. Vui lòng chọn VietQR.`,
    };
  }
  if (input.totalPayableVnd > DEFAULT_COD_MAX_VND) {
    return {
      allowed: false,
      reason: `Đơn hàng vượt hạn mức COD tối đa ${formatVnd(DEFAULT_COD_MAX_VND)}. Vui lòng chọn VietQR.`,
    };
  }
  return { allowed: true, reason: "" };
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
