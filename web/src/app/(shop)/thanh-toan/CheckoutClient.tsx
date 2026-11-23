"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import type {
  GatewayActiveShopDiscount,
  GatewayAddress,
  GatewayCalculateFeeResponse,
  GatewayCarrierQuote,
  GatewayCart,
  GatewayCheckoutRequest,
  GatewayCheckoutResponse,
  GatewayDiscountBreakdown,
  GatewayFlashSale,
  GatewayPaymentMethod,
  GatewayPreviewDiscountsResponse,
  GatewayProduct,
  GatewayPublicShop,
  GatewayShopShippingChoice,
  GatewayUser,
  GatewayVoucher,
  GatewayVouchersResponse,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { formatInt, formatSignedVnd, formatVnd, formatVoucherBenefit } from "@/lib/format";
import { useCart } from "@/context/CartContext";
import { Button, IconShield, IconTruck, StateView } from "@/components";
import { AddressForm } from "@/features/commerce/AddressForm";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import type { BuyNowResolvedItem } from "@/features/checkout/types";
import { CheckoutSuggestionStrip } from "@/features/checkout/CheckoutSuggestionStrip";
import {
  DEFAULT_COD_MAX_REJECTED,
  DEFAULT_COD_MAX_VND,
  formatCarrierDisplay,
  formatServiceDisplay,
  formatVoucherCondition,
  translateCheckoutError,
  translateVoucherReason,
} from "@/features/checkout/messages";
import {
  buildCalculateFeePayload,
  classifyCheckoutSubmitError,
  clearCheckoutWarnings,
  computeBuyNowSlicePricing,
  evaluateShopFlashLines,
  inspectFlashSaleStatusForSku,
  isCodAllowedForCheckout,
  isShopProgramItemApplied,
  resolveLiveFlashSaleForSku,
  resolveSkuEffectivePromotion,
  saveCheckoutWarnings,
  translatePromotionWarnings,
} from "@/features/checkout/helpers";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./page.module.css";

export interface CheckoutClientProps {
  user: GatewayUser;
  idempotencyKey: string;
  initialAddresses: GatewayAddress[];
  initialCart: GatewayCart | null;
  initialCoinBalance: number;
  initialVouchers: GatewayVoucher[];
  initialShops: GatewayPublicShop[];
  initialFlashSales?: GatewayFlashSale[];
  initialActiveShopDiscounts?: GatewayActiveShopDiscount[];
  initialError?: string | null;
  buyNowItem: BuyNowResolvedItem | null;
  buyNowError: string | null;
  suggestedProducts: GatewayProduct[];
}

interface CheckoutLineItem {
  skuId: string;
  productId: string;
  productName: string;
  skuName: string;
  imageUrl: string;
  unitPriceVnd: number;
  flashSaleItemId?: string;
  flashPriceVnd?: number;
  flashPerUserLimit?: number;
  hasLiveFlashAvailable?: boolean;
  hasActiveShopDiscount?: boolean;
  flashOptedOut?: boolean;
  shopProgramItemId?: string;
  shopDiscountedPriceVnd?: number;
  shopProgramName?: string;
  quantity: number;
  weightGrams: number;
}

interface CheckoutShopSlice {
  shopId: string;
  shopName: string;
  items: CheckoutLineItem[];
  /** Effective merchandise subtotal after validated flash sale / shop program discounts. */
  subtotalVnd: number;
  /** Gross regular merchandise subtotal before item discounts, required by promotions/preview. */
  regularSubtotalVnd: number;
  /** Expected flash sale discount total (F) for validated live flash items. */
  flashDiscountVnd: number;
  /** Expected shop discount program total for validated active shop discounts. */
  shopProgramDiscountVnd: number;
  weightGrams: number;
}

export function CheckoutClient({
  user,
  idempotencyKey,
  initialAddresses,
  initialCart,
  initialCoinBalance,
  initialVouchers,
  initialShops,
  initialFlashSales = [],
  initialActiveShopDiscounts = [],
  initialError,
  buyNowItem,
  buyNowError,
  suggestedProducts,
}: CheckoutClientProps) {
  const router = useRouter();
  const viewState = usePageStateParam();
  const {
    selectedShopGroups,
    isFullCartSelected,
    cartLoading,
    cartInitialized,
    cartError,
    refreshCart,
    selectedShopVouchers,
    platformVoucherCode,
    freeshipVoucherCode,
    useCoins,
    setShopVoucher,
    setPlatformVoucher,
    setFreeshipVoucher,
    setUseCoins,
    resetPromotions,
    optedOutFlashSkuIds,
    optOutFlashForSku,
    restoreFlashForSku,
  } = useCart();

  const isBuyNowMode = Boolean(buyNowItem || buyNowError);
  const [buyNowOptedOutFlash, setBuyNowOptedOutFlash] = useState<boolean>(false);
  const [checkoutIdempotencyKey, setCheckoutIdempotencyKey] = useState<string>(idempotencyKey);
  const [flashCheckoutBlocked, setFlashCheckoutBlocked] = useState<boolean>(false);
  const [idempotencyConflict, setIdempotencyConflict] = useState<boolean>(false);
  const [ambiguousSubmit, setAmbiguousSubmit] = useState<boolean>(false);

  const [addresses, setAddresses] = useState<GatewayAddress[]>(initialAddresses);
  const [selectedAddressId, setSelectedAddressId] = useState<string>(() => {
    const def = initialAddresses.find((a) => a.is_default) ?? initialAddresses[0];
    return def?.id ?? "";
  });
  const [showNewAddressForm, setShowNewAddressForm] = useState<boolean>(
    initialAddresses.length === 0
  );

  const [paymentMethod, setPaymentMethod] = useState<GatewayPaymentMethod>("VIETQR");

  // Real shipping quotes from POST /shipping/calculate-fee
  const [quotesByShop, setQuotesByShop] = useState<Record<string, GatewayCarrierQuote[]>>({});
  const [selectedChoiceByShop, setSelectedChoiceByShop] = useState<
    Record<string, { carrier_code: string; service_code: string }>
  >({});
  const [shippingLoading, setShippingLoading] = useState<boolean>(false);
  const [shippingError, setShippingError] = useState<string | null>(null);

  // Vouchers & Coins
  const [vouchers, setVouchers] = useState<GatewayVoucher[]>(initialVouchers);
  const [coinBalance] = useState<number>(initialCoinBalance);
  const [promoCodeInput, setPromoCodeInput] = useState("");
  const [applyingPromo, setApplyingPromo] = useState(false);
  const [promoFeedback, setPromoFeedback] = useState<{ ok: boolean; message: string } | null>(null);

  // Live preview from POST /promotions/preview
  const [preview, setPreview] = useState<GatewayDiscountBreakdown | null>(null);
  const [previewLoading, setPreviewLoading] = useState<boolean>(false);

  // Checkout submission state (strictly single-use after success, same idempotencyKey on retry)
  const [formError, setFormError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState<boolean>(false);
  const completedRef = useRef<boolean>(false);
  const previewSeqRef = useRef<number>(0);
  const shippingSeqRef = useRef<number>(0);

  const shopNameById = useMemo(() => {
    const map: Record<string, string> = {};
    for (const s of initialShops) {
      map[s.id] = s.name;
    }
    return map;
  }, [initialShops]);

  const shopDiscountBySkuId = useMemo(() => {
    const map: Record<string, GatewayActiveShopDiscount> = {};
    for (const d of initialActiveShopDiscounts) {
      if (d.sku_id && !map[d.sku_id]) map[d.sku_id] = d;
    }
    return map;
  }, [initialActiveShopDiscounts]);

  const enrichCartLine = useCallback(
    (raw: {
      skuId: string;
      productId: string;
      productName: string;
      skuName: string;
      imageUrl: string;
      unitPriceVnd: number;
      quantity: number;
      weightGrams: number;
    }): CheckoutLineItem => {
      const liveFlash = resolveLiveFlashSaleForSku(initialFlashSales, raw.skuId, raw.unitPriceVnd);
      const activeShopDiscount = shopDiscountBySkuId[raw.skuId] ?? null;
      const hasActiveShopDiscount = Boolean(
        activeShopDiscount &&
        activeShopDiscount.discounted_price_vnd > 0 &&
        activeShopDiscount.discounted_price_vnd < raw.unitPriceVnd
      );
      const flashOptedOut = optedOutFlashSkuIds.has(raw.skuId);
      const promo = resolveSkuEffectivePromotion({
        regularPriceVnd: raw.unitPriceVnd,
        liveFlash,
        activeShopDiscount,
        optOutFlash: flashOptedOut,
      });
      return {
        ...raw,
        flashSaleItemId: promo.flashSaleItemId,
        flashPriceVnd: promo.kind === "flash" ? promo.effectiveUnitPriceVnd : undefined,
        flashPerUserLimit: liveFlash?.perUserLimit,
        hasLiveFlashAvailable: Boolean(liveFlash),
        hasActiveShopDiscount,
        flashOptedOut,
        shopProgramItemId: promo.shopProgramItemId,
        shopDiscountedPriceVnd:
          promo.kind === "shop_discount" ? promo.effectiveUnitPriceVnd : undefined,
        shopProgramName: promo.programName,
      };
    },
    [initialFlashSales, shopDiscountBySkuId, optedOutFlashSkuIds]
  );

  const computeSliceTotals = (items: CheckoutLineItem[]) => {
    let regularSubtotalVnd = 0;
    let flashDiscountVnd = 0;
    let shopProgramDiscountVnd = 0;
    for (const it of items) {
      const reg = it.unitPriceVnd * it.quantity;
      regularSubtotalVnd += reg;
      if (it.flashSaleItemId && it.flashPriceVnd && it.flashPriceVnd < it.unitPriceVnd) {
        flashDiscountVnd += (it.unitPriceVnd - it.flashPriceVnd) * it.quantity;
      } else if (
        it.shopProgramItemId &&
        it.shopDiscountedPriceVnd &&
        it.shopDiscountedPriceVnd < it.unitPriceVnd
      ) {
        shopProgramDiscountVnd += (it.unitPriceVnd - it.shopDiscountedPriceVnd) * it.quantity;
      }
    }
    const subtotalVnd = Math.max(0, regularSubtotalVnd - flashDiscountVnd - shopProgramDiscountVnd);
    return { regularSubtotalVnd, flashDiscountVnd, shopProgramDiscountVnd, subtotalVnd };
  };

  const activeSlices = useMemo<CheckoutShopSlice[]>(() => {
    if (buyNowItem) {
      const useFlash = Boolean(buyNowItem.flashSaleItemId) && !buyNowOptedOutFlash;
      const hasShopDisc = Boolean(
        buyNowItem.shopProgramItemId &&
        typeof buyNowItem.shopDiscountedPriceVnd === "number" &&
        buyNowItem.shopDiscountedPriceVnd > 0 &&
        buyNowItem.shopDiscountedPriceVnd < buyNowItem.unitPriceVnd
      );
      const useShopDisc = !useFlash && hasShopDisc;
      const line: CheckoutLineItem = {
        skuId: buyNowItem.skuId,
        productId: buyNowItem.productId,
        productName: buyNowItem.productName,
        skuName: buyNowItem.skuName,
        imageUrl: buyNowItem.imageUrl,
        unitPriceVnd: buyNowItem.unitPriceVnd,
        flashSaleItemId: useFlash ? buyNowItem.flashSaleItemId : undefined,
        flashPriceVnd: useFlash ? buyNowItem.flashPriceVnd : undefined,
        flashPerUserLimit: buyNowItem.flashPerUserLimit,
        hasLiveFlashAvailable: Boolean(buyNowItem.flashSaleItemId),
        hasActiveShopDiscount: hasShopDisc,
        flashOptedOut: buyNowOptedOutFlash,
        shopProgramItemId: useShopDisc ? buyNowItem.shopProgramItemId : undefined,
        shopDiscountedPriceVnd: useShopDisc ? buyNowItem.shopDiscountedPriceVnd : undefined,
        shopProgramName: useShopDisc ? buyNowItem.shopProgramName : undefined,
        quantity: buyNowItem.quantity,
        weightGrams: buyNowItem.weightGrams,
      };
      const totals = computeSliceTotals([line]);
      return [
        {
          shopId: buyNowItem.shopId,
          shopName: shopNameById[buyNowItem.shopId] || buyNowItem.shopName,
          items: [line],
          ...totals,
          weightGrams: Math.max(0, buyNowItem.weightGrams * buyNowItem.quantity),
        },
      ];
    }
    if (isBuyNowMode) return [];
    if (!cartInitialized && initialCart?.shop_groups?.length) {
      return initialCart.shop_groups
        .map((g) => {
          const items = (g.items ?? [])
            .filter((it) => it.available_stock > 0 && it.quantity > 0)
            .map((it) =>
              enrichCartLine({
                skuId: it.sku_id,
                productId: it.product_id,
                productName: it.product_name,
                skuName: it.sku_name,
                imageUrl: it.image_url || "/products/ao-thun-cotton-trang.png",
                unitPriceVnd: it.price_vnd,
                quantity: it.quantity,
                weightGrams: Math.max(0, Math.trunc(it.weight_grams || 0)),
              })
            );
          if (items.length === 0) return null;
          const totals = computeSliceTotals(items);
          const weightGrams = items.reduce((s, it) => s + it.weightGrams * it.quantity, 0);
          return {
            shopId: g.shop_id,
            shopName: shopNameById[g.shop_id] || g.shop_name || "Gian hàng NexusMart",
            items,
            ...totals,
            weightGrams,
          };
        })
        .filter((s): s is CheckoutShopSlice => s !== null);
    }
    return selectedShopGroups.map((g) => {
      const items = g.items.map((it) =>
        enrichCartLine({
          skuId: it.skuId,
          productId: it.productId,
          productName: it.productName,
          skuName: it.skuName,
          imageUrl: it.imageUrl,
          unitPriceVnd: it.priceVnd,
          quantity: it.quantity,
          weightGrams: Math.max(0, Math.trunc(it.weightGrams || 0)),
        })
      );
      const totals = computeSliceTotals(items);
      return {
        shopId: g.shopId,
        shopName: shopNameById[g.shopId] || g.shopName,
        items,
        ...totals,
        weightGrams: Math.max(0, g.selectedWeightGrams),
      };
    });
  }, [
    buyNowItem,
    buyNowOptedOutFlash,
    isBuyNowMode,
    cartInitialized,
    initialCart,
    selectedShopGroups,
    shopNameById,
    enrichCartLine,
  ]);

  const hasFlashSaleItem = useMemo(
    () => activeSlices.some((slice) => slice.items.some((it) => Boolean(it.flashSaleItemId))),
    [activeSlices]
  );

  // Force VietQR when any item in checkout uses Flash Sale (COD disallowed)
  useEffect(() => {
    if (hasFlashSaleItem && paymentMethod !== "VIETQR") {
      setPaymentMethod("VIETQR");
    }
  }, [hasFlashSaleItem, paymentMethod]);

  const flashFallbackNotices = useMemo(() => {
    const notices: string[] = [];
    if (buyNowItem?.flashFallbackNotice) {
      notices.push(buyNowItem.flashFallbackNotice);
    } else if (!buyNowItem) {
      for (const slice of activeSlices) {
        for (const it of slice.items) {
          if (it.flashSaleItemId) continue;
          const inspect = inspectFlashSaleStatusForSku(initialFlashSales, it.skuId);
          if (inspect.state === "sold_out" || inspect.state === "upcoming") {
            notices.push(`${it.productName}: ${inspect.message}`);
          }
        }
      }
    }
    return Array.from(new Set(notices));
  }, [buyNowItem, activeSlices, initialFlashSales]);

  const totalItemCount = useMemo(
    () =>
      activeSlices.reduce(
        (sum, slice) => sum + slice.items.reduce((s, it) => s + it.quantity, 0),
        0
      ),
    [activeSlices]
  );

  const rawSubtotalVnd = useMemo(
    () => activeSlices.reduce((sum, slice) => sum + slice.subtotalVnd, 0),
    [activeSlices]
  );

  const rawRegularSubtotalVnd = useMemo(
    () => activeSlices.reduce((sum, slice) => sum + slice.regularSubtotalVnd, 0),
    [activeSlices]
  );

  const rawFlashDiscountVnd = useMemo(
    () => activeSlices.reduce((sum, slice) => sum + slice.flashDiscountVnd, 0),
    [activeSlices]
  );

  const rawShopProgramDiscountVnd = useMemo(
    () => activeSlices.reduce((sum, slice) => sum + slice.shopProgramDiscountVnd, 0),
    [activeSlices]
  );

  const selectedAddress = useMemo(
    () => addresses.find((a) => a.id === selectedAddressId) ?? addresses[0] ?? null,
    [addresses, selectedAddressId]
  );

  useRevealOnScroll(
    `${viewState ?? "live"}-${activeSlices.length}-${paymentMethod}-${selectedAddress?.id ?? ""}`
  );

  // Refresh live vouchers based on order amount
  useEffect(() => {
    if (rawSubtotalVnd <= 0) return;
    let active = true;
    const loadVouchers = async () => {
      try {
        const res = await api<GatewayVouchersResponse>(
          `vouchers?order_amount_vnd=${rawSubtotalVnd}`
        );
        if (active && Array.isArray(res?.vouchers)) {
          setVouchers(res.vouchers);
        }
      } catch {
        // Keep initial server-fetched vouchers on transient error
      }
    };
    void loadVouchers();
    return () => {
      active = false;
    };
  }, [rawSubtotalVnd]);

  // Calculate real shipping quotes per shop via authoritative items + payment_method (gateway r6)
  const shippingInputKey = useMemo(
    () =>
      `${selectedAddress?.id ?? ""}|${paymentMethod}|${activeSlices
        .map(
          (s) =>
            `${s.shopId}:${s.items
              .map((it) => `${it.skuId}x${it.quantity}`)
              .sort()
              .join("+")}`
        )
        .join(",")}`,
    [selectedAddress?.id, paymentMethod, activeSlices]
  );

  const loadShippingQuotes = useCallback(async () => {
    const seq = ++shippingSeqRef.current;
    if (!selectedAddress || activeSlices.length === 0) {
      setQuotesByShop({});
      setShippingError(null);
      setShippingLoading(false);
      return;
    }

    setShippingLoading(true);
    setShippingError(null);

    try {
      const results = await Promise.all(
        activeSlices.map(async (slice) => {
          const res = await api<GatewayCalculateFeeResponse>("shipping/calculate-fee", {
            method: "POST",
            json: buildCalculateFeePayload({
              shopId: slice.shopId,
              toProvinceCode: selectedAddress.province_code,
              toWardCode: selectedAddress.ward_code,
              items: slice.items.map((it) => ({
                skuId: it.skuId,
                quantity: it.quantity,
              })),
              paymentMethod,
            }),
          });
          return { shopId: slice.shopId, quotes: res?.quotes ?? [] };
        })
      );
      if (shippingSeqRef.current !== seq) return;

      const nextQuotes: Record<string, GatewayCarrierQuote[]> = {};
      for (const r of results) {
        nextQuotes[r.shopId] = r.quotes;
      }
      setQuotesByShop(nextQuotes);

      setSelectedChoiceByShop((prev) => {
        const next: Record<string, { carrier_code: string; service_code: string }> = {};
        for (const r of results) {
          const existing = prev[r.shopId];
          const match = existing
            ? r.quotes.find(
                (q) =>
                  q.carrier_code === existing.carrier_code &&
                  q.service_code === existing.service_code
              )
            : undefined;
          if (match) {
            next[r.shopId] = {
              carrier_code: match.carrier_code,
              service_code: match.service_code,
            };
          } else if (r.quotes.length > 0) {
            const cheapest = r.quotes.reduce(
              (min, q) => (q.fee_vnd < min.fee_vnd ? q : min),
              r.quotes[0]
            );
            next[r.shopId] = {
              carrier_code: cheapest.carrier_code,
              service_code: cheapest.service_code,
            };
          }
        }
        return next;
      });
    } catch (err) {
      if (shippingSeqRef.current !== seq) return;
      setShippingError(
        getErrorMessage(err, "Chưa thể tính phí vận chuyển lúc này. Vui lòng thử lại.")
      );
    } finally {
      if (shippingSeqRef.current === seq) {
        setShippingLoading(false);
      }
    }
  }, [activeSlices, paymentMethod, selectedAddress]);

  useEffect(() => {
    void loadShippingQuotes();
  }, [shippingInputKey, loadShippingQuotes]);

  const chosenQuoteByShop = useMemo(() => {
    const out: Record<string, GatewayCarrierQuote | undefined> = {};
    for (const slice of activeSlices) {
      const list = quotesByShop[slice.shopId] ?? [];
      const choice = selectedChoiceByShop[slice.shopId];
      out[slice.shopId] =
        list.find(
          (q) =>
            choice &&
            q.carrier_code === choice.carrier_code &&
            q.service_code === choice.service_code
        ) ?? list[0];
    }
    return out;
  }, [activeSlices, quotesByShop, selectedChoiceByShop]);

  const totalShippingFeeVnd = useMemo(
    () => activeSlices.reduce((sum, s) => sum + (chosenQuoteByShop[s.shopId]?.fee_vnd ?? 0), 0),
    [activeSlices, chosenQuoteByShop]
  );

  const activeShopVoucherMap = useMemo(() => {
    const out: Record<string, string> = {};
    for (const slice of activeSlices) {
      const code = selectedShopVouchers[slice.shopId];
      if (code) out[slice.shopId] = code;
    }
    return out;
  }, [activeSlices, selectedShopVouchers]);

  const buildShopDiscountInputs = useCallback(() => {
    return activeSlices.map((slice) => ({
      shop_id: slice.shopId,
      shipping_fee_vnd: chosenQuoteByShop[slice.shopId]?.fee_vnd ?? 0,
      items: slice.items.map((it) => ({
        sku_id: it.skuId,
        quantity: it.quantity,
        ...(it.flashSaleItemId ? { flash_sale_item_id: it.flashSaleItemId } : {}),
      })),
    }));
  }, [activeSlices, chosenQuoteByShop]);

  // Debounced POST /promotions/preview with real shipping fees & flash sale item IDs
  useEffect(() => {
    if (activeSlices.length === 0) {
      setPreview(null);
      setPreviewLoading(false);
      return;
    }

    const seq = ++previewSeqRef.current;
    setPreviewLoading(true);

    const timer = window.setTimeout(() => {
      const runPreview = async () => {
        try {
          const res = await api<GatewayPreviewDiscountsResponse>("promotions/preview", {
            method: "POST",
            json: {
              shop_items: buildShopDiscountInputs(),
              shop_voucher_ids: activeShopVoucherMap,
              platform_voucher_id: platformVoucherCode ?? "",
              freeship_voucher_id: freeshipVoucherCode ?? "",
              coins_to_use: useCoins ? coinBalance : 0,
            },
          });
          if (previewSeqRef.current !== seq) return;
          setPreview(res?.breakdown ?? null);
        } catch {
          if (previewSeqRef.current !== seq) return;
          setPreview(null);
        } finally {
          if (previewSeqRef.current === seq) {
            setPreviewLoading(false);
          }
        }
      };
      void runPreview();
    }, 220);

    return () => window.clearTimeout(timer);
  }, [
    activeSlices,
    buildShopDiscountInputs,
    activeShopVoucherMap,
    platformVoucherCode,
    freeshipVoucherCode,
    useCoins,
    coinBalance,
  ]);

  const platformVouchers = useMemo(
    () => vouchers.filter((v) => v.scope.toUpperCase() === "PLATFORM"),
    [vouchers]
  );
  const freeshipVouchers = useMemo(
    () => vouchers.filter((v) => v.scope.toUpperCase() === "FREESHIP"),
    [vouchers]
  );

  const subtotalDisplay = preview?.total_subtotal_vnd ?? rawRegularSubtotalVnd;
  const flashDiscountDisplay = preview?.total_flash_sale_discount_vnd ?? rawFlashDiscountVnd;
  const shopProgramDiscountDisplay =
    preview?.total_shop_program_discount_vnd ?? rawShopProgramDiscountVnd;
  const shopVoucherDisplay = preview?.total_shop_voucher_discount_vnd ?? 0;
  const shopFreeshipDisplay = preview?.total_shop_freeship_discount_vnd ?? 0;
  const platformVoucherDisplay = preview?.total_platform_voucher_discount_vnd ?? 0;
  const shippingDisplay = preview?.total_shipping_fee_vnd ?? totalShippingFeeVnd;
  const freeshipDisplay = preview?.total_freeship_discount_vnd ?? 0;
  const coinsDisplay = preview?.total_coins_discount_vnd ?? 0;
  const grandTotalVnd =
    preview?.final_payable_vnd ??
    Math.max(
      0,
      rawRegularSubtotalVnd - rawFlashDiscountVnd - rawShopProgramDiscountVnd + totalShippingFeeVnd
    );

  const flashEvalBySkuId = useMemo(() => {
    const merged: Record<string, ReturnType<typeof evaluateShopFlashLines>[string]> = {};
    for (const slice of activeSlices) {
      const shopBreakdown = preview?.shops.find((s) => s.shop_id === slice.shopId);
      const evals = evaluateShopFlashLines({
        lines: slice.items.map((it) => ({
          skuId: it.skuId,
          quantity: it.quantity,
          regularUnitPriceVnd: it.unitPriceVnd,
          flashSaleItemId: it.flashSaleItemId,
          flashUnitPriceVnd: it.flashPriceVnd,
          flashPerUserLimit: it.flashPerUserLimit,
          hasActiveShopDiscount: it.hasActiveShopDiscount,
        })),
        previewShopFlashDiscountVnd: shopBreakdown ? shopBreakdown.flash_sale_discount_vnd : null,
        previewWarnings: preview?.warnings,
      });
      Object.assign(merged, evals);
    }
    return merged;
  }, [activeSlices, preview]);

  const rotateCheckoutIdempotencyKey = useCallback(() => {
    const nextUuid =
      typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
        ? crypto.randomUUID()
        : `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
    setCheckoutIdempotencyKey(`nm-checkout-${nextUuid}`);
  }, []);

  const handleOptOutFlashSku = useCallback(
    (skuId: string) => {
      if (buyNowItem && buyNowItem.skuId === skuId) {
        setBuyNowOptedOutFlash(true);
      } else {
        optOutFlashForSku(skuId);
      }
      setFormError(null);
      setFlashCheckoutBlocked(false);
      setAmbiguousSubmit(false);
      rotateCheckoutIdempotencyKey();
    },
    [buyNowItem, optOutFlashForSku, rotateCheckoutIdempotencyKey]
  );

  const handleRestoreFlashSku = useCallback(
    (skuId: string) => {
      if (buyNowItem && buyNowItem.skuId === skuId) {
        setBuyNowOptedOutFlash(false);
      } else {
        restoreFlashForSku(skuId);
      }
      setFormError(null);
      setFlashCheckoutBlocked(false);
      setAmbiguousSubmit(false);
      rotateCheckoutIdempotencyKey();
    },
    [buyNowItem, restoreFlashForSku, rotateCheckoutIdempotencyKey]
  );

  const handleOptOutAllActiveFlash = useCallback(() => {
    if (buyNowItem) {
      setBuyNowOptedOutFlash(true);
    } else {
      for (const slice of activeSlices) {
        for (const it of slice.items) {
          if (it.flashSaleItemId) {
            optOutFlashForSku(it.skuId);
          }
        }
      }
    }
    setFormError(null);
    setFlashCheckoutBlocked(false);
    setAmbiguousSubmit(false);
    rotateCheckoutIdempotencyKey();
  }, [buyNowItem, activeSlices, optOutFlashForSku, rotateCheckoutIdempotencyKey]);

  const translatedWarnings = useMemo(
    () => translatePromotionWarnings(preview?.warnings),
    [preview?.warnings]
  );

  const hasFlashFallbackShopDiscount = useMemo(
    () =>
      activeSlices.some((slice) =>
        slice.items.some((it) => Boolean(it.flashSaleItemId) && Boolean(it.hasActiveShopDiscount))
      ),
    [activeSlices]
  );

  const handleAddressSaved = (saved: GatewayAddress) => {
    setAddresses((prev) => {
      const exists = prev.some((a) => a.id === saved.id);
      const updated = exists
        ? prev.map((a) =>
            a.id === saved.id ? saved : saved.is_default ? { ...a, is_default: false } : a
          )
        : [saved, ...prev.map((a) => (saved.is_default ? { ...a, is_default: false } : a))];
      return updated;
    });
    setSelectedAddressId(saved.id);
    setShowNewAddressForm(false);
    setFormError(null);
  };

  const handleApplyPromoCode = async (e: React.FormEvent) => {
    e.preventDefault();
    const clean = promoCodeInput.trim().toUpperCase();
    if (!clean) {
      setPromoFeedback({ ok: false, message: "Vui lòng nhập mã ưu đãi." });
      return;
    }

    const matched = vouchers.find((v) => v.code.toUpperCase() === clean);
    if (matched) {
      const scope = matched.scope.toUpperCase();
      if (scope === "SHOP") {
        const targetSlice = activeSlices.find((s) => s.shopId === matched.shop_id);
        if (!targetSlice) {
          setPromoFeedback({
            ok: false,
            message: "Đơn hàng hiện không có sản phẩm thuộc gian hàng áp dụng mã này.",
          });
          return;
        }
        if (targetSlice.subtotalVnd < matched.min_order_vnd) {
          setPromoFeedback({
            ok: false,
            message: `Chưa đạt giá trị đơn tối thiểu ${formatVnd(matched.min_order_vnd)} của gian hàng.`,
          });
          return;
        }
        if (!matched.eligible && matched.ineligible_reason) {
          setPromoFeedback({
            ok: false,
            message: translateVoucherReason(matched.ineligible_reason),
          });
          return;
        }
        setShopVoucher(matched.shop_id, matched.code);
        setPromoFeedback({ ok: true, message: `Đã áp dụng mã gian hàng ${matched.code}.` });
        setPromoCodeInput("");
        return;
      }

      if (!matched.eligible && matched.ineligible_reason) {
        setPromoFeedback({ ok: false, message: translateVoucherReason(matched.ineligible_reason) });
        return;
      }

      if (scope === "FREESHIP") {
        setFreeshipVoucher(matched.code);
        setPromoFeedback({ ok: true, message: `Đã áp dụng mã vận chuyển ${matched.code}.` });
      } else {
        setPlatformVoucher(matched.code);
        setPromoFeedback({ ok: true, message: `Đã áp dụng mã toàn sàn ${matched.code}.` });
      }
      setPromoCodeInput("");
      return;
    }

    setApplyingPromo(true);
    setPromoFeedback(null);
    try {
      const res = await api<GatewayPreviewDiscountsResponse>("promotions/preview", {
        method: "POST",
        json: {
          shop_items: buildShopDiscountInputs(),
          shop_voucher_ids: activeShopVoucherMap,
          platform_voucher_id: clean,
          freeship_voucher_id: freeshipVoucherCode ?? "",
          coins_to_use: useCoins ? coinBalance : 0,
        },
      });
      const bd = res?.breakdown;
      if (bd && bd.total_platform_voucher_discount_vnd > 0) {
        setPlatformVoucher(clean);
        setPromoFeedback({
          ok: true,
          message: `Đã áp dụng mã ${clean} (giảm ${formatVnd(bd.total_platform_voucher_discount_vnd)}).`,
        });
        setPromoCodeInput("");
      } else {
        const warn = bd?.warnings?.[0];
        setPromoFeedback({
          ok: false,
          message: warn
            ? translateVoucherReason(warn)
            : `Mã ${clean} không tồn tại hoặc chưa đủ điều kiện áp dụng.`,
        });
      }
    } catch (err) {
      setPromoFeedback({
        ok: false,
        message: getErrorMessage(err, `Chưa thể kiểm tra mã ${clean} lúc này.`),
      });
    } finally {
      setApplyingPromo(false);
    }
  };

  const handlePlaceOrder = async () => {
    if (isSubmitting || completedRef.current || shippingLoading || previewLoading) return;
    if (activeSlices.length === 0) {
      setFormError("Chưa có sản phẩm nào để đặt hàng.");
      return;
    }
    if (!selectedAddress) {
      setFormError("Vui lòng chọn hoặc thêm địa chỉ nhận hàng trước khi đặt đơn.");
      return;
    }

    const shippingChoices: GatewayShopShippingChoice[] = [];
    for (const slice of activeSlices) {
      const chosen = chosenQuoteByShop[slice.shopId];
      if (chosen) {
        shippingChoices.push({
          shop_id: slice.shopId,
          carrier_code: chosen.carrier_code,
          service_code: chosen.service_code,
        });
      }
    }

    const codCheck = isCodAllowedForCheckout({
      hasFlashSaleItem,
      totalPayableVnd: grandTotalVnd,
      codRejectedCount: user.cod_rejected_count,
    });
    if (paymentMethod === "COD" && hasFlashSaleItem) {
      setFormError(codCheck.reason);
      setPaymentMethod("VIETQR");
      return;
    }

    const fromCart = !buyNowItem && isFullCartSelected;
    const allItemsInput = activeSlices.flatMap((slice) =>
      slice.items.map((it) => ({
        sku_id: it.skuId,
        quantity: it.quantity,
        ...(it.flashSaleItemId ? { flash_sale_item_id: it.flashSaleItemId } : {}),
      }))
    );
    const hasPromoOverrides = allItemsInput.some((it) => Boolean(it.flash_sale_item_id));
    const explicitItems = !fromCart || hasPromoOverrides ? allItemsInput : undefined;

    const effectiveCoins = useCoins
      ? Math.min(coinBalance, preview ? preview.total_coins_discount_vnd : 0)
      : 0;

    const payload: GatewayCheckoutRequest = {
      address_id: selectedAddress.id,
      from_cart: fromCart,
      ...(explicitItems ? { items: explicitItems } : {}),
      ...(Object.keys(activeShopVoucherMap).length > 0
        ? { shop_voucher_ids: activeShopVoucherMap }
        : {}),
      ...(platformVoucherCode ? { platform_voucher_id: platformVoucherCode } : {}),
      ...(freeshipVoucherCode ? { freeship_voucher_id: freeshipVoucherCode } : {}),
      coins_to_use: Math.max(0, Math.trunc(effectiveCoins)),
      payment_method: paymentMethod,
      shipping_choices: shippingChoices,
    };

    setFormError(null);
    setFlashCheckoutBlocked(false);
    setIdempotencyConflict(false);
    setAmbiguousSubmit(false);
    setIsSubmitting(true);

    try {
      const res = await api<GatewayCheckoutResponse>("checkout", {
        method: "POST",
        headers: {
          "Idempotency-Key": checkoutIdempotencyKey,
        },
        json: payload,
      });

      completedRef.current = true;

      if (Array.isArray(res.warnings) && res.warnings.length > 0) {
        saveCheckoutWarnings({
          paymentId: res.payment_id,
          orderIds: Array.isArray(res.orders) ? res.orders.map((o) => o.id).filter(Boolean) : [],
          checkoutGroupId: res.checkout_group_id,
          warnings: res.warnings,
        });
      } else {
        clearCheckoutWarnings();
      }

      if (!buyNowItem) {
        resetPromotions();
        void refreshCart();
      }

      if (res.payment_method === "VIETQR" && res.payment_id && res.total_payable_vnd > 0) {
        router.push(`/thanh-toan/cho-thanh-toan/${encodeURIComponent(res.payment_id)}`);
        return;
      }

      if (Array.isArray(res.orders) && res.orders.length === 1) {
        router.push(`/don-hang/${encodeURIComponent(res.orders[0].id)}`);
        return;
      }

      router.push("/don-hang");
    } catch (err) {
      const errKind = classifyCheckoutSubmitError(err, {
        hasFlashSaleItem,
        paymentMethod,
      });
      if (errKind === "unauthenticated") {
        const nextPath =
          typeof window !== "undefined"
            ? `${window.location.pathname}${window.location.search}`
            : "/thanh-toan";
        router.push(`/dang-nhap?next=${encodeURIComponent(nextPath)}`);
        return;
      }

      const hasVouchers = Boolean(
        platformVoucherCode || freeshipVoucherCode || Object.keys(activeShopVoucherMap).length > 0
      );
      if (errKind === "idempotency_conflict") {
        // NEVER auto-rotate idempotency key: the previous attempt may have already created an order.
        setIdempotencyConflict(true);
      } else if (errKind === "ambiguous_created") {
        // Forward-recovery rule (503/504/ABORTED/CANCELED/network reject): keep the SAME Idempotency-Key,
        // do NOT auto-retry, do NOT rotate key, and do NOT offer the key-rotating flash opt-out button.
        setAmbiguousSubmit(true);
      } else if (errKind === "flash_blocked") {
        setFlashCheckoutBlocked(true);
      }
      setFormError(
        translateCheckoutError(err, {
          paymentMethod,
          totalPayableVnd: grandTotalVnd,
          codRejectedCount: user.cod_rejected_count,
          hasVouchers,
          isFlashSale: hasFlashSaleItem,
        })
      );
      setIsSubmitting(false);
    }
  };

  const renderHeader = (subtitle: string) => (
    <section className={shared.pageHeader}>
      <div className={shared.headerLeadGroup}>
        <h1 className={`${shared.pageTitle} lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Thanh toán đơn hàng
            </span>
          </span>
        </h1>
        <p className={shared.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          {subtitle}
        </p>
      </div>
      <div className={shared.headerAside} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
        <Button variant="secondary" size="md" href="/gio-hang">
          Quay lại giỏ hàng
        </Button>
      </div>
    </section>
  );

  if (
    viewState === "loading" ||
    (!isBuyNowMode && (!cartInitialized || cartLoading) && activeSlices.length === 0)
  ) {
    return (
      <div className={shared.page}>
        {renderHeader("Đang cập nhật địa chỉ nhận hàng, phí vận chuyển và ưu đãi cho đơn của bạn.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Chuẩn bị đơn thanh toán</h2>
          <StateView kind="loading" title="Đang tải thông tin thanh toán..." rows={4} />
        </section>
      </div>
    );
  }

  if (viewState === "error" || initialError || (!isBuyNowMode && cartError)) {
    return (
      <div className={shared.page}>
        {renderHeader("Không thể kết nối trang thanh toán lúc này. Chưa có khoản tiền nào bị trừ.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái thanh toán</h2>
          <StateView
            kind="error"
            title="Thanh toán tạm gián đoạn"
            message={
              initialError ??
              cartError ??
              "Hệ thống xác nhận đơn đang tạm gián đoạn. Vui lòng thử tải lại trang."
            }
            actionLabel="Thử lại"
            onAction={() => {
              void refreshCart();
              router.refresh();
            }}
          />
        </section>
      </div>
    );
  }

  if (buyNowError) {
    return (
      <div className={shared.page}>
        {renderHeader("Không thể tạo đơn mua ngay cho sản phẩm này.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Sản phẩm mua ngay</h2>
          <StateView
            kind="error"
            title="Sản phẩm chưa sẵn sàng để thanh toán"
            message={buyNowError}
            actionLabel="Quay lại danh mục"
            actionHref="/danh-muc"
          />
        </section>
      </div>
    );
  }

  if (viewState === "empty" || activeSlices.length === 0) {
    return (
      <div className={shared.page}>
        {renderHeader("Chưa có sản phẩm nào được chọn từ giỏ hàng để tiến hành thanh toán.")}
        <section
          className={shared.section}
          data-rise=""
          style={{ "--i": 3 } as React.CSSProperties}
        >
          <h2 className={shared.sectionTitle}>Danh sách chờ thanh toán</h2>
          <StateView
            kind="empty"
            title="Chưa có sản phẩm cần thanh toán"
            message="Quay lại giỏ hàng để chọn sản phẩm hoặc khám phá danh mục đồ cơ bản của NexusMart."
            actionLabel="Quay lại giỏ hàng"
            actionHref="/gio-hang"
          />
        </section>
      </div>
    );
  }

  const methodShortLabel = paymentMethod === "COD" ? "COD" : "VietQR";
  const ctaLabel = isSubmitting
    ? "Đang tạo đơn hàng..."
    : `Đặt hàng · ${formatVnd(grandTotalVnd)} (${methodShortLabel})`;

  const codPolicy = isCodAllowedForCheckout({
    hasFlashSaleItem,
    totalPayableVnd: grandTotalVnd,
    codRejectedCount: user.cod_rejected_count,
  });
  const codExceedsLimit = grandTotalVnd > DEFAULT_COD_MAX_VND;
  const codBlockedHistory = user.cod_rejected_count >= DEFAULT_COD_MAX_REJECTED;

  return (
    <div className={`${shared.page} ${styles.mobileSpacer}`}>
      {renderHeader(
        "Chọn địa chỉ giao nhận, đơn vị vận chuyển và phương thức thanh toán để hoàn tất."
      )}

      {flashFallbackNotices.length > 0 && (
        <div className={styles.warningList} role="status">
          {flashFallbackNotices.map((note) => (
            <p key={note} className={`${shared.feedbackBox} ${shared.feedbackWarn}`}>
              {note}
            </p>
          ))}
        </div>
      )}

      {buyNowItem && (
        <div className={styles.buyNowBanner} role="status">
          <span>
            {buyNowItem.flashSaleItemId
              ? "Đang đặt trực tiếp suất ưu đãi Flash sale (không ảnh hưởng sản phẩm trong giỏ)."
              : "Đang đặt mua ngay 1 sản phẩm (không ảnh hưởng sản phẩm khác trong giỏ)."}
          </span>
          <Button variant="secondary" size="sm" href="/gio-hang">
            Chuyển sang giỏ hàng
          </Button>
        </div>
      )}

      <section className={shared.section}>
        <div className={styles.checkoutGrid}>
          {/* Left column: Single clean white sheet with 3 clear steps */}
          <div className={styles.checkoutSheet}>
            {/* Step 1: Saved addresses + AddressForm (Contract 3) */}
            <div
              className={styles.stepBlock}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <div className={styles.blockHead}>
                <h2 className={styles.blockTitle}>Địa chỉ nhận hàng</h2>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => setShowNewAddressForm((v) => !v)}
                >
                  {showNewAddressForm ? "Đóng biểu mẫu" : "Thêm địa chỉ mới"}
                </Button>
              </div>

              {addresses.length > 0 ? (
                <div
                  className={styles.addressList}
                  role="radiogroup"
                  aria-label="Chọn địa chỉ nhận hàng"
                >
                  {addresses.map((addr) => {
                    const isSelected = selectedAddress?.id === addr.id;
                    return (
                      <button
                        key={addr.id}
                        type="button"
                        role="radio"
                        aria-checked={isSelected}
                        className={`${styles.addressOption} ${
                          isSelected ? styles.addressOptionSelected : ""
                        }`}
                        onClick={() => setSelectedAddressId(addr.id)}
                      >
                        <div className={styles.addressTopRow}>
                          <span className={styles.addressName}>
                            {addr.recipient_name} · {addr.recipient_phone}
                          </span>
                          <div className={shared.formActions}>
                            {addr.is_default && (
                              <span className={`${shared.statusTag} ${shared.toneNeutral}`}>
                                Mặc định
                              </span>
                            )}
                            {isSelected && (
                              <span className={`${shared.statusTag} ${shared.tonePos}`}>
                                Đang chọn
                              </span>
                            )}
                          </div>
                        </div>
                        <p className={styles.addressLine}>
                          {addr.street_address}, {addr.ward_name}, {addr.province_name}
                        </p>
                      </button>
                    );
                  })}
                </div>
              ) : (
                <p className={styles.addressShipLine}>
                  Bạn chưa có địa chỉ nhận hàng nào. Hãy thêm địa chỉ mới bên dưới để tính phí giao
                  hàng chính xác.
                </p>
              )}

              {showNewAddressForm && (
                <AddressForm
                  onSaved={handleAddressSaved}
                  onCancel={addresses.length > 0 ? () => setShowNewAddressForm(false) : undefined}
                />
              )}
            </div>

            {/* Step 2: Per-shop shipping fee calculation & carrier selection */}
            <div
              className={styles.stepBlock}
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              <div className={styles.blockHead}>
                <h2 className={styles.blockTitle}>Vận chuyển theo gian hàng</h2>
                {shippingLoading && (
                  <span className={styles.addressShipLine}>Đang tính cước vận chuyển...</span>
                )}
              </div>

              {shippingError && (
                <div className={`${shared.feedbackBox} ${shared.feedbackError}`} role="alert">
                  <p>{shippingError}</p>
                  <div className={shared.formActions}>
                    <Button variant="secondary" size="sm" onClick={() => void loadShippingQuotes()}>
                      Tính lại phí vận chuyển
                    </Button>
                  </div>
                </div>
              )}

              {!selectedAddress ? (
                <p className={styles.addressShipLine}>
                  Chọn địa chỉ nhận hàng ở trên để xem báo giá vận chuyển cho từng gian hàng.
                </p>
              ) : (
                <div className={styles.shopDeliveryList}>
                  {activeSlices.map((slice) => {
                    const quotes = quotesByShop[slice.shopId] ?? [];
                    const chosen = chosenQuoteByShop[slice.shopId];

                    return (
                      <div key={slice.shopId} className={styles.shopDeliveryRow}>
                        <div className={styles.shippingRow}>
                          <div className={styles.shippingInfo}>
                            <IconTruck size={17} className={styles.shippingIcon} />
                            <div>
                              <p className={styles.addressName}>{slice.shopName}</p>
                              <p className={styles.addressShipLine}>
                                Giao tới {selectedAddress.ward_name},{" "}
                                {selectedAddress.province_name}
                              </p>
                            </div>
                          </div>
                          {chosen && (
                            <span className={`${shared.statusTag} ${shared.toneNeutral} num`}>
                              {formatVnd(chosen.fee_vnd)}
                            </span>
                          )}
                        </div>

                        {quotes.length > 0 && (
                          <div
                            className={styles.quoteOptions}
                            role="radiogroup"
                            aria-label={`Chọn đơn vị vận chuyển cho ${slice.shopName}`}
                          >
                            {quotes.map((q) => {
                              const active =
                                chosen?.carrier_code === q.carrier_code &&
                                chosen?.service_code === q.service_code;
                              return (
                                <button
                                  key={`${q.carrier_code}-${q.service_code}`}
                                  type="button"
                                  role="radio"
                                  aria-checked={active}
                                  className={`${styles.quoteBtn} ${
                                    active ? styles.quoteBtnActive : ""
                                  }`}
                                  onClick={() =>
                                    setSelectedChoiceByShop((prev) => ({
                                      ...prev,
                                      [slice.shopId]: {
                                        carrier_code: q.carrier_code,
                                        service_code: q.service_code,
                                      },
                                    }))
                                  }
                                >
                                  <span className={styles.quoteTop}>
                                    <span>
                                      {formatCarrierDisplay(q.carrier_name, q.carrier_code)}
                                    </span>
                                    <span className="num">{formatVnd(q.fee_vnd)}</span>
                                  </span>
                                  <span className={styles.quoteMeta}>
                                    {formatServiceDisplay(q.service_name, q.service_code)} · Dự kiến{" "}
                                    {q.estimated_days > 0 ? `${q.estimated_days} ngày` : "2-4 ngày"}
                                  </span>
                                </button>
                              );
                            })}
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}
            </div>

            {/* Step 3: Real payment methods (COD & VietQR only) */}
            <div
              className={styles.stepBlock}
              data-rise=""
              style={{ "--i": 4 } as React.CSSProperties}
            >
              <h2 className={styles.blockTitle}>Phương thức thanh toán</h2>

              <div
                className={styles.paymentList}
                role="radiogroup"
                aria-label="Chọn phương thức thanh toán"
              >
                <button
                  type="button"
                  role="radio"
                  aria-checked={paymentMethod === "VIETQR"}
                  className={`${styles.paymentOption} ${
                    paymentMethod === "VIETQR" ? styles.paymentOptionSelected : ""
                  }`}
                  onClick={() => setPaymentMethod("VIETQR")}
                >
                  <div className={styles.paymentHead}>
                    <span className={styles.paymentTitle}>Chuyển khoản nhanh VietQR</span>
                    {paymentMethod === "VIETQR" && (
                      <span className={`${shared.statusTag} ${shared.tonePos}`}>Đang chọn</span>
                    )}
                  </div>
                  <p className={styles.paymentDesc}>
                    Sau khi bấm Đặt hàng, hệ thống sẽ tạo mã VietQR riêng cho đơn của bạn để quét
                    bằng ứng dụng ngân hàng và xác nhận tự động, không cần nhập số thẻ.
                    {hasFlashSaleItem
                      ? " Đơn hàng có sản phẩm Flash sale cần thanh toán trong vòng 5 phút."
                      : ""}
                  </p>
                </button>

                <button
                  type="button"
                  role="radio"
                  aria-checked={paymentMethod === "COD"}
                  disabled={hasFlashSaleItem}
                  className={`${styles.paymentOption} ${
                    paymentMethod === "COD" ? styles.paymentOptionSelected : ""
                  }`}
                  onClick={() => {
                    if (!hasFlashSaleItem) setPaymentMethod("COD");
                  }}
                >
                  <div className={styles.paymentHead}>
                    <span className={styles.paymentTitle}>Thanh toán khi nhận hàng (COD)</span>
                    {hasFlashSaleItem ? (
                      <span className={`${shared.statusTag} ${shared.toneWarn}`}>
                        Không áp dụng cho Flash sale
                      </span>
                    ) : paymentMethod === "COD" ? (
                      <span className={`${shared.statusTag} ${shared.tonePos}`}>Đang chọn</span>
                    ) : null}
                  </div>
                  <p className={styles.paymentDesc}>
                    {hasFlashSaleItem
                      ? codPolicy.reason
                      : `Thanh toán tiền mặt trực tiếp cho nhân viên giao hàng khi nhận kiện. Áp dụng cho đơn hàng tối đa ${formatVnd(DEFAULT_COD_MAX_VND)}.`}
                  </p>
                  {paymentMethod === "COD" && codExceedsLimit && (
                    <p className={styles.codAdvisory}>
                      Tổng thanh toán đang vượt mốc {formatVnd(DEFAULT_COD_MAX_VND)}. Nếu hệ thống
                      từ chối COD, bạn có thể chuyển sang Chuyển khoản nhanh VietQR.
                    </p>
                  )}
                  {paymentMethod === "COD" && codBlockedHistory && (
                    <p className={styles.codAdvisory}>
                      Tài khoản có nhiều lần không nhận hàng COD trước đây nên có thể không đặt được
                      COD. Khuyên dùng Chuyển khoản nhanh VietQR.
                    </p>
                  )}
                </button>
              </div>
            </div>
          </div>

          {/* Right column: Order summary, real vouchers/coins, preview breakdown & CTA */}
          <aside
            className={styles.reviewCol}
            aria-label="Chi tiết thanh toán"
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <div className={styles.reviewInner}>
              <h2 className={styles.blockTitle}>Chi tiết đơn thanh toán</h2>

              <div className={styles.reviewItemsGroup}>
                {activeSlices.map((slice) => {
                  const shopBreakdown = preview?.shops.find((s) => s.shop_id === slice.shopId);
                  const shopVouchers = vouchers.filter(
                    (v) => v.scope.toUpperCase() === "SHOP" && v.shop_id === slice.shopId
                  );
                  const chosenShopVoucher = selectedShopVouchers[slice.shopId] ?? null;

                  const sliceDisplayedSubtotal = shopBreakdown
                    ? Math.max(
                        0,
                        shopBreakdown.subtotal_vnd -
                          shopBreakdown.flash_sale_discount_vnd -
                          (shopBreakdown.shop_program_discount_vnd ?? 0)
                      )
                    : slice.subtotalVnd;

                  return (
                    <div key={slice.shopId} className={styles.shopSlice}>
                      <div className={styles.shopSliceHead}>
                        <span>{slice.shopName}</span>
                        <span className="num">{formatVnd(sliceDisplayedSubtotal)}</span>
                      </div>
                      <div className={styles.shopSliceItems}>
                        {slice.items.map((it) => {
                          const itemPricing = computeBuyNowSlicePricing(it);
                          const flashEval = flashEvalBySkuId[it.skuId];
                          const isFlashLine = itemPricing.flashDiscountVnd > 0;
                          const flashShortfall = Boolean(flashEval?.previewShortfall);
                          const shopProgramApplied = Boolean(
                            (!isFlashLine || flashShortfall) &&
                            it.shopProgramItemId &&
                            isShopProgramItemApplied({
                              shopProgramItemId: it.shopProgramItemId,
                              shopBreakdown: shopBreakdown ?? null,
                            })
                          );
                          const shopProgramExceeded = Boolean(
                            !isFlashLine &&
                            it.shopProgramItemId &&
                            shopBreakdown &&
                            !shopProgramApplied
                          );
                          const effectiveUnit =
                            isFlashLine && !flashShortfall
                              ? itemPricing.effectiveUnitPriceVnd
                              : shopProgramApplied &&
                                  it.shopDiscountedPriceVnd &&
                                  it.shopDiscountedPriceVnd < it.unitPriceVnd
                                ? it.shopDiscountedPriceVnd
                                : it.unitPriceVnd;
                          const effectiveLine = effectiveUnit * it.quantity;
                          const showFlashOptOut = Boolean(
                            isFlashLine &&
                            (flashEval?.needsRegularPriceChoice || flashCheckoutBlocked)
                          );
                          return (
                            <div key={it.skuId} className={styles.miniItem}>
                              <img
                                src={it.imageUrl}
                                alt={it.productName}
                                width={896}
                                height={1200}
                                className={styles.miniThumb}
                              />
                              <div className={styles.miniInfo}>
                                <span className={styles.miniName}>{it.productName}</span>
                                <span className={styles.miniMeta}>
                                  {it.skuName ? `${it.skuName} · ` : ""}SL{" "}
                                  <span className="num">{it.quantity}</span>
                                  {isFlashLine && !flashShortfall
                                    ? ` · Flash sale ${formatVnd(effectiveUnit)}`
                                    : shopProgramApplied && effectiveUnit < it.unitPriceVnd
                                      ? ` · ${it.shopProgramName || "Giảm giá shop"} ${formatVnd(effectiveUnit)}`
                                      : ""}
                                </span>
                                {showFlashOptOut && (
                                  <div className={styles.linePromoNotice} role="status">
                                    <span className={styles.linePromoWarn}>
                                      {flashEval?.reason ||
                                        (it.hasActiveShopDiscount
                                          ? "Suất Flash sale đã hết lượt hoặc vượt giới hạn mua/tài khoản. Bỏ Flash để tiếp tục mua theo giá chương trình gian hàng."
                                          : "Suất Flash sale đã hết lượt hoặc vượt giới hạn mua/tài khoản. Chọn Mua giá thường để tiếp tục đặt hàng.")}
                                    </span>
                                    <button
                                      type="button"
                                      className={styles.regularPriceActionBtn}
                                      onClick={() => handleOptOutFlashSku(it.skuId)}
                                    >
                                      {it.hasActiveShopDiscount
                                        ? "Bỏ Flash (dùng giá chương trình gian hàng)"
                                        : "Mua giá thường"}
                                    </button>
                                  </div>
                                )}
                                {it.flashOptedOut && it.hasLiveFlashAvailable && (
                                  <div className={styles.linePromoNotice}>
                                    <span className={styles.miniMeta}>
                                      {shopProgramApplied
                                        ? "Đang áp dụng giá chương trình gian hàng (đã bỏ suất Flash sale)"
                                        : "Đang mua giá thường (đã bỏ suất Flash sale)"}
                                    </span>
                                    <button
                                      type="button"
                                      className={styles.regularPriceActionBtn}
                                      onClick={() => handleRestoreFlashSku(it.skuId)}
                                    >
                                      Dùng lại giá Flash
                                    </button>
                                  </div>
                                )}
                                {shopProgramExceeded && (
                                  <div className={styles.linePromoNotice} role="status">
                                    <span className={styles.linePromoWarn}>
                                      Vượt giới hạn chương trình giảm giá của gian hàng — dòng này
                                      tính giá thường.
                                    </span>
                                  </div>
                                )}
                              </div>
                              <span className={`${styles.miniPrice} num`}>
                                {formatVnd(effectiveLine)}
                              </span>
                            </div>
                          );
                        })}
                      </div>

                      {slice.items.some(
                        (it) => flashEvalBySkuId[it.skuId]?.ambiguousShopShortfall
                      ) && (
                        <div
                          className={`${styles.linePromoNotice} ${styles.shopPromoNotice}`}
                          role="status"
                        >
                          <span className={styles.linePromoWarn}>
                            Một hoặc nhiều sản phẩm Flash sale của gian hàng đã hết lượt hoặc vượt
                            giới hạn mua/tài khoản. Tổng thanh toán đã cập nhật theo giá hệ thống —
                            hãy kiểm tra lại hoặc bỏ suất Flash sale không còn hiệu lực.
                          </span>
                        </div>
                      )}

                      {shopVouchers.length > 0 && (
                        <div className={styles.voucherPickList}>
                          {shopVouchers.map((v) => {
                            const statusActive = !v.status || v.status.toUpperCase() === "ACTIVE";
                            const eligible =
                              v.eligible !== false &&
                              statusActive &&
                              v.remaining_quota > 0 &&
                              v.is_active &&
                              slice.subtotalVnd >= v.min_order_vnd;
                            const ineligibleReason =
                              !eligible && v.ineligible_reason
                                ? translateVoucherReason(v.ineligible_reason)
                                : "";
                            const isSelected = chosenShopVoucher === v.code;
                            return (
                              <button
                                key={v.id}
                                type="button"
                                disabled={!eligible && !isSelected}
                                className={`${styles.voucherPickBtn} ${
                                  isSelected ? styles.voucherPickBtnActive : ""
                                }`}
                                onClick={() =>
                                  setShopVoucher(slice.shopId, isSelected ? null : v.code)
                                }
                              >
                                <span className={styles.voucherPickCopy}>
                                  <span className={styles.voucherPickCode}>
                                    {v.code} · {formatVoucherBenefit(v)}
                                  </span>
                                  <span className={styles.voucherPickDesc}>
                                    {ineligibleReason || formatVoucherCondition(v)}
                                  </span>
                                </span>
                                <span className={styles.voucherPickAction}>
                                  {isSelected ? "Bỏ chọn" : eligible ? "Chọn" : "Chưa đủ"}
                                </span>
                              </button>
                            );
                          })}
                        </div>
                      )}

                      {(shopBreakdown?.shop_program_discount_vnd ?? 0) > 0 && (
                        <div className={styles.ledgerRow}>
                          <span className={styles.ledgerLabel}>
                            Giảm giá chương trình gian hàng
                          </span>
                          <span className={`${styles.ledgerDiscount} num`}>
                            {formatSignedVnd(-(shopBreakdown?.shop_program_discount_vnd ?? 0))}
                          </span>
                        </div>
                      )}

                      {(shopBreakdown?.shop_voucher_discount_vnd ?? 0) > 0 && (
                        <div className={styles.ledgerRow}>
                          <span className={styles.ledgerLabel}>Ưu đãi gian hàng</span>
                          <span className={`${styles.ledgerDiscount} num`}>
                            {formatSignedVnd(-(shopBreakdown?.shop_voucher_discount_vnd ?? 0))}
                          </span>
                        </div>
                      )}

                      {(shopBreakdown?.shop_freeship_discount_vnd ?? 0) > 0 && (
                        <div className={styles.ledgerRow}>
                          <span className={styles.ledgerLabel}>Gian hàng hỗ trợ phí ship</span>
                          <span className={`${styles.ledgerDiscount} num`}>
                            {formatSignedVnd(-(shopBreakdown?.shop_freeship_discount_vnd ?? 0))}
                          </span>
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>

              {/* Manual voucher code + platform/freeship quick picks + real Coins */}
              <form className={styles.promoSection} onSubmit={handleApplyPromoCode} noValidate>
                <label htmlFor="co-code-input" className={shared.fieldLabel}>
                  Mã ưu đãi & Xu NexusMart
                </label>
                <div className={shared.codeInputRow}>
                  <input
                    id="co-code-input"
                    type="text"
                    className={shared.input}
                    value={promoCodeInput}
                    onChange={(e) => setPromoCodeInput(e.target.value)}
                    placeholder="Nhập mã ưu đãi (ví dụ: NEXUS30)"
                  />
                  <Button type="submit" variant="ink" size="md" disabled={applyingPromo}>
                    {applyingPromo ? "Đang kiểm..." : "Áp dụng"}
                  </Button>
                </div>

                {promoFeedback && (
                  <p
                    className={`${shared.feedbackBox} ${
                      promoFeedback.ok ? shared.feedbackSuccess : shared.feedbackError
                    }`}
                    role={promoFeedback.ok ? "status" : "alert"}
                  >
                    {promoFeedback.message}
                  </p>
                )}

                {platformVouchers.length > 0 && (
                  <div className={styles.voucherPickList}>
                    {platformVouchers.map((v) => {
                      const isSelected = platformVoucherCode === v.code;
                      return (
                        <button
                          key={v.id}
                          type="button"
                          disabled={!v.eligible && !isSelected}
                          className={`${styles.voucherPickBtn} ${
                            isSelected ? styles.voucherPickBtnActive : ""
                          }`}
                          onClick={() => setPlatformVoucher(isSelected ? null : v.code)}
                        >
                          <span className={styles.voucherPickCopy}>
                            <span className={styles.voucherPickCode}>
                              {v.code} · {formatVoucherBenefit(v)}
                            </span>
                            <span className={styles.voucherPickDesc}>
                              {v.eligible
                                ? formatVoucherCondition(v)
                                : translateVoucherReason(v.ineligible_reason) ||
                                  formatVoucherCondition(v)}
                            </span>
                          </span>
                          <span className={styles.voucherPickAction}>
                            {isSelected ? "Bỏ chọn" : v.eligible ? "Chọn" : "Chưa đủ"}
                          </span>
                        </button>
                      );
                    })}
                  </div>
                )}

                {freeshipVouchers.length > 0 && (
                  <div className={styles.freeshipRow}>
                    {freeshipVouchers.map((v) => {
                      const active = freeshipVoucherCode === v.code;
                      return (
                        <button
                          key={v.id}
                          type="button"
                          disabled={!v.eligible && !active}
                          className={`${styles.freeshipBtn} ${active ? styles.freeshipBtnActive : ""}`}
                          onClick={() => setFreeshipVoucher(active ? null : v.code)}
                        >
                          {v.code} ({formatVoucherBenefit(v)})
                        </button>
                      );
                    })}
                  </div>
                )}

                <label className={shared.checkboxRow}>
                  <input
                    type="checkbox"
                    className={shared.checkbox}
                    checked={useCoins && coinBalance > 0}
                    disabled={coinBalance <= 0}
                    onChange={(e) => setUseCoins(e.target.checked)}
                  />
                  <span>
                    Dùng Xu NexusMart (có{" "}
                    <strong className="num">{formatInt(coinBalance)} Xu</strong>)
                  </span>
                </label>

                {translatedWarnings.length > 0 && (
                  <div className={styles.warningList} role="status">
                    {translatedWarnings.map((w) => (
                      <p key={w} className={`${shared.feedbackBox} ${shared.feedbackWarn}`}>
                        {w}
                      </p>
                    ))}
                  </div>
                )}
              </form>

              <dl className={styles.ledgerList}>
                {previewLoading && (
                  <p className={styles.miniMeta}>Đang cập nhật tổng thanh toán...</p>
                )}

                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>
                    Tạm tính (<span className="num">{totalItemCount}</span> sản phẩm)
                  </dt>
                  <dd className="num">{formatVnd(subtotalDisplay)}</dd>
                </div>

                {flashDiscountDisplay > 0 && (
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Ưu đãi Flash sale</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {formatSignedVnd(-flashDiscountDisplay)}
                    </dd>
                  </div>
                )}

                {shopProgramDiscountDisplay > 0 && (
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Giảm giá chương trình gian hàng</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {formatSignedVnd(-shopProgramDiscountDisplay)}
                    </dd>
                  </div>
                )}

                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Ưu đãi gian hàng</dt>
                  <dd className={`${styles.ledgerDiscount} num`}>
                    {shopVoucherDisplay > 0 ? formatSignedVnd(-shopVoucherDisplay) : "0₫"}
                  </dd>
                </div>

                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Ưu đãi toàn sàn</dt>
                  <dd className={`${styles.ledgerDiscount} num`}>
                    {platformVoucherDisplay > 0 ? formatSignedVnd(-platformVoucherDisplay) : "0₫"}
                  </dd>
                </div>

                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Phí vận chuyển</dt>
                  <dd className="num">{formatVnd(shippingDisplay)}</dd>
                </div>

                {shopFreeshipDisplay > 0 && (
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Gian hàng hỗ trợ phí ship</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {formatSignedVnd(-shopFreeshipDisplay)}
                    </dd>
                  </div>
                )}

                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Giảm phí vận chuyển</dt>
                  <dd className={`${styles.ledgerDiscount} num`}>
                    {freeshipDisplay > 0 ? formatSignedVnd(-freeshipDisplay) : "0₫"}
                  </dd>
                </div>

                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Trừ Xu NexusMart</dt>
                  <dd className={`${styles.ledgerDiscount} num`}>
                    {coinsDisplay > 0 ? formatSignedVnd(-coinsDisplay) : "0₫"}
                  </dd>
                </div>
              </dl>
            </div>

            <div className={styles.ctaBand}>
              <div className={styles.grandBand}>
                <span className={styles.grandLabel}>Tổng thanh toán</span>
                <span className={`${styles.grandVal} num`}>{formatVnd(grandTotalVnd)}</span>
              </div>

              <div className={styles.assuranceBox}>
                <IconShield size={17} className={styles.assuranceIcon} />
                <span>
                  Xác nhận đơn an toàn: số lượng sản phẩm được giữ ngay khi bạn bấm Đặt hàng.
                </span>
              </div>

              {formError && (
                <div className={`${shared.feedbackBox} ${shared.feedbackError}`} role="alert">
                  <p>{formError}</p>
                  {ambiguousSubmit && !idempotencyConflict && (
                    <div className={`${shared.formActions} ${styles.errorActions}`}>
                      <Button variant="secondary" size="sm" href="/don-hang">
                        Xem Đơn hàng của tôi
                      </Button>
                    </div>
                  )}
                  {idempotencyConflict && (
                    <div className={`${shared.formActions} ${styles.errorActions}`}>
                      <Button variant="secondary" size="sm" href="/don-hang">
                        Kiểm tra Đơn hàng của tôi
                      </Button>
                      <button
                        type="button"
                        className={styles.regularPriceLightBtn}
                        onClick={() => {
                          rotateCheckoutIdempotencyKey();
                          setIdempotencyConflict(false);
                          setFormError(null);
                        }}
                      >
                        Vẫn đặt đơn mới
                      </button>
                    </div>
                  )}
                  {flashCheckoutBlocked &&
                    hasFlashSaleItem &&
                    !ambiguousSubmit &&
                    !idempotencyConflict && (
                      <div className={styles.errorActions}>
                        <button
                          type="button"
                          className={styles.regularPriceLightBtn}
                          onClick={handleOptOutAllActiveFlash}
                        >
                          {hasFlashFallbackShopDiscount
                            ? "Bỏ suất Flash sale (dùng giá chương trình gian hàng / giá thường)"
                            : "Mua giá thường (bỏ suất Flash sale)"}
                        </button>
                      </div>
                    )}
                </div>
              )}

              <Button
                variant="primary"
                size="lg"
                fullWidth
                className={shared.wrapBtn}
                disabled={isSubmitting || completedRef.current || shippingLoading || previewLoading}
                onClick={() => void handlePlaceOrder()}
              >
                {previewLoading ? "Đang cập nhật giá & ưu đãi..." : ctaLabel}
              </Button>
            </div>
          </aside>
        </div>
      </section>

      <CheckoutSuggestionStrip
        title="Đồ cơ bản chuẩn chất liệu"
        subtitle="Gợi ý sản phẩm nổi bật từ các xưởng dệt NexusMart."
        products={suggestedProducts}
        shopNameById={shopNameById}
        showQuickAdd={false}
      />

      {/* Sticky mobile place-order bar */}
      <div className={styles.mobileCheckoutBar} aria-label="Xác nhận đặt hàng nhanh">
        <div>
          <p className={styles.miniMeta}>{methodShortLabel}</p>
          <p className={`${styles.addressName} num`}>{formatVnd(grandTotalVnd)}</p>
        </div>
        <Button
          variant="primary"
          size="md"
          disabled={isSubmitting || completedRef.current || shippingLoading || previewLoading}
          onClick={() => void handlePlaceOrder()}
        >
          {isSubmitting
            ? "Đang tạo..."
            : previewLoading
              ? "Đang tính..."
              : `Đặt · ${formatVnd(grandTotalVnd)}`}
        </Button>
      </div>
    </div>
  );
}
