import type { GatewayFlashSale } from "@/lib/gateway-types";

export interface CalculateFeePayloadInput {
  shopId: string;
  toProvinceCode: string;
  toWardCode: string;
  weightGrams: number;
  codAmountVnd: number;
}

export interface CalculateFeePayload {
  shop_id: string;
  to_province_code: string;
  to_ward_code: string;
  weight_grams: number;
  cod_amount_vnd: number;
}

/**
 * Builds the POST /shipping/calculate-fee payload without empty origin fields,
 * letting the shipping service resolve the shop's pickup address from `shop_id`.
 */
export function buildCalculateFeePayload(input: CalculateFeePayloadInput): CalculateFeePayload {
  return {
    shop_id: input.shopId,
    to_province_code: input.toProvinceCode,
    to_ward_code: input.toWardCode,
    weight_grams: Math.max(100, Math.trunc(input.weightGrams || 0)),
    cod_amount_vnd: Math.max(0, Math.trunc(input.codAmountVnd || 0)),
  };
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
    if (!Number.isFinite(startMs) || !Number.isFinite(endMs) || nowMs < startMs || nowMs > endMs) {
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
    };
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
