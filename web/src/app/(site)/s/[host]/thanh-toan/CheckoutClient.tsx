"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  AlertCircle,
  AlertTriangle,
  Check,
  CreditCard,
  Loader2,
  LogIn,
  MapPin,
  ShoppingBag,
  Tag,
  Truck,
  UserRound,
} from "lucide-react";
import type { Product } from "@/data/products";
import type { Shop } from "@/data/shops";
import type {
  GatewayActiveShopDiscount,
  GatewayFlashSale,
  GatewayPaymentMethod,
  GatewayPaymentMethodConfig,
} from "@/lib/gateway-types";
import { ApiError, api } from "@/lib/api";
import { formatInt, formatSignedVnd, formatVnd } from "@/lib/format";
import { cn } from "@/lib/utils";
import { useStore } from "@/context/StoreContext";
import { useSession } from "@/components/SessionProvider";
import { EmptyState } from "@/components/app/EmptyState";
import { ErrorState } from "@/components/app/ErrorState";
import { StatusBadge } from "@/components/app/StatusBadge";
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import { AddressForm } from "@/features/commerce/AddressForm";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { SiteLoadingSkeleton } from "@/features/commerce/ui/SiteLoadingSkeleton";
import { SitePageFrame, SitePageHeader } from "@/features/commerce/ui/SitePageFrame";
import {
  buildCheckoutIdempotencySignature,
  canMutateCheckoutFlashSelection,
  classifyCheckoutSubmitError,
  clearPendingCheckoutKey,
  evaluatePaymentMethodForCheckout,
  evaluateShopFlashLines,
  INITIAL_CHECKOUT_SUBMIT_GUARD_STATE,
  isTerminalCancelledCheckoutResponse,
  readPendingCheckoutKey,
  reduceCheckoutSubmitState,
  resolveCartFlashSaleState,
  resolveDefaultCheckoutMethod,
  resolveDisplayedLinePricing,
  saveCheckoutWarnings,
  savePendingCheckoutKey,
  shouldClearPendingKeyOnConfirmedEmptyCart,
  type CheckoutSubmitGuardState,
} from "@/features/checkout/helpers";
import {
  AMBIGUOUS_CHECKOUT_MESSAGE,
  CHECKOUT_FAILED_BUYER_MESSAGE,
  translateCheckoutError,
} from "@/features/checkout/messages";
import {
  buildCheckoutPreviewKey,
  enrichCartGroups,
  formatAddressLine,
  formatCarrierLabel,
  formatServiceLabel,
  formatVoucherHeadline,
  formatVoucherIneligibleReason,
  splitVariantParts,
} from "@/features/commerce/cartEnrich";
import type {
  GwAddress,
  GwCarrierQuote,
  GwCheckoutResponse,
  GwDiscountBreakdown,
  GwVoucher,
} from "@/features/commerce/types";

type CheckoutStage = "address" | "shipping" | "payment";

const FALLBACK_PAYMENT_METHODS: GatewayPaymentMethodConfig[] = [
  {
    method: "VIETQR",
    enabled: true,
    min_order_vnd: 0,
    max_order_vnd: 0,
    expiry_minutes: 15,
    version: 1,
  },
  {
    method: "ZALOPAY",
    enabled: true,
    min_order_vnd: 0,
    max_order_vnd: 0,
    expiry_minutes: 15,
    version: 1,
  },
  {
    method: "COD",
    enabled: true,
    min_order_vnd: 0,
    max_order_vnd: 5_000_000,
    expiry_minutes: 0,
    version: 1,
  },
];

const PAYMENT_METHOD_COPY: Record<
  GatewayPaymentMethod,
  { title: string; shortLabel: string; description: string }
> = {
  VIETQR: {
    title: "Chuyển khoản nhanh VietQR",
    shortLabel: "VietQR",
    description: "Quét mã bằng ứng dụng ngân hàng, xác nhận tự động.",
  },
  ZALOPAY: {
    title: "Ví điện tử ZaloPay",
    shortLabel: "ZaloPay",
    description: "Thanh toán qua ứng dụng hoặc cổng thanh toán Ví ZaloPay.",
  },
  COD: {
    title: "Thanh toán khi nhận hàng (COD)",
    shortLabel: "COD",
    description: "Trả tiền trực tiếp cho bưu tá khi nhận hàng.",
  },
};

export interface CheckoutClientProps {
  brandName: string;
  initialIdempotencyKey: string;
  catalogProducts: Product[];
  catalogShops: Shop[];
  initialAddresses: GwAddress[];
  initialVouchers: GwVoucher[];
  initialPaymentMethods?: GatewayPaymentMethodConfig[];
  initialShopDiscounts?: GatewayActiveShopDiscount[];
  initialFlashSales?: GatewayFlashSale[];
  initialError: string | null;
  serverNowMs?: number;
}

function generateClientAttemptKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `chk-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

function quoteValue(carrierCode: string, serviceCode: string): string {
  return `${carrierCode}::${serviceCode}`;
}

export function CheckoutClient({
  brandName,
  initialIdempotencyKey,
  catalogProducts,
  catalogShops,
  initialAddresses,
  initialVouchers,
  initialPaymentMethods = [],
  initialShopDiscounts = [],
  initialFlashSales = [],
  initialError,
  serverNowMs,
}: CheckoutClientProps) {
  const router = useRouter();
  const [anchorNowMs] = useState<number>(() => serverNowMs ?? Date.now());
  const { user, isAuthenticated, loading: sessionLoading } = useSession();
  const {
    cart,
    cartLoading,
    cartError,
    refreshCart,
    selectedVoucherId,
    flashSaleItemBySku,
    optedOutFlashSkus,
    optOutFlashSaleSku,
    optOutFlashSaleSkus,
    restoreFlashSaleSku,
    clearOptedOutFlashSkus,
    setSelectedVoucher,
    resetVoucherSelection,
    showToast,
  } = useStore();

  const [addresses, setAddresses] = useState<GwAddress[]>(initialAddresses);
  const [selectedAddressId, setSelectedAddressId] = useState<string>(
    initialAddresses.find((a) => a.is_default)?.id ?? initialAddresses[0]?.id ?? ""
  );
  const [addressesLoading, setAddressesLoading] = useState<boolean>(false);
  const [showNewAddressForm, setShowNewAddressForm] = useState<boolean>(
    initialAddresses.length === 0
  );
  const [openStage, setOpenStage] = useState<CheckoutStage>(
    initialAddresses.length > 0 ? "payment" : "address"
  );

  const [vouchers, setVouchers] = useState<GwVoucher[]>(initialVouchers);
  const submittingRef = useRef<boolean>(false);
  const [paymentMethods, setPaymentMethods] = useState<GatewayPaymentMethodConfig[]>(
    initialPaymentMethods.length > 0 ? initialPaymentMethods : FALLBACK_PAYMENT_METHODS
  );
  const [quoteState, setQuoteState] = useState<{
    key: string;
    status: "ok" | "error";
    quotes: GwCarrierQuote[];
    isEstimated: boolean;
  } | null>(null);
  const [selectedCarrier, setSelectedCarrier] = useState<{
    carrier_code: string;
    service_code: string;
  } | null>(null);

  const [paymentMethod, setPaymentMethod] = useState<GatewayPaymentMethod>(() => {
    const methods =
      initialPaymentMethods.length > 0 ? initialPaymentMethods : FALLBACK_PAYMENT_METHODS;
    return (
      resolveDefaultCheckoutMethod(methods, { hasFlashSaleItem: false, currentMethod: "VIETQR" }) ??
      "VIETQR"
    );
  });
  const [formErrorCode, setFormErrorCode] = useState<string | null>(null);
  const [previewState, setPreviewState] = useState<{
    key: string;
    status: "ok" | "error";
    breakdown: GwDiscountBreakdown | null;
    rejectedFlashSaleItemIds: string[];
    error: string | null;
  } | null>(null);

  const [promoCodeInput, setPromoCodeInput] = useState<string>("");
  const [promoValidating, setPromoValidating] = useState<boolean>(false);
  const [promoFeedback, setPromoFeedback] = useState<{ ok: boolean; message: string } | null>(null);

  const [formError, setFormError] = useState<string | null>(initialError);
  const [sessionExpired, setSessionExpired] = useState<boolean>(false);
  const [isSubmitting, setIsSubmitting] = useState<boolean>(false);

  const [idempotencyKey, setIdempotencyKey] = useState<string>(initialIdempotencyKey);
  const [submitGuard, setSubmitGuard] = useState<CheckoutSubmitGuardState>(
    INITIAL_CHECKOUT_SUBMIT_GUARD_STATE
  );
  const [pendingOrderCheckHint, setPendingOrderCheckHint] = useState<boolean>(false);
  const submitGuardRef = useRef<CheckoutSubmitGuardState>(INITIAL_CHECKOUT_SUBMIT_GUARD_STATE);
  const lastSignatureRef = useRef<string | null>(null);
  const didInitialMetaFetchRef = useRef<boolean>(false);
  const hasAddressesRef = useRef<boolean>(initialAddresses.length > 0);
  hasAddressesRef.current = addresses.length > 0;
  const previewStateRef = useRef(previewState);
  previewStateRef.current = previewState;

  const pendingKeySig = useMemo(
    () => (user?.id ? buildCheckoutIdempotencySignature({ userId: user.id }) : ""),
    [user?.id]
  );

  // Restore in-flight / ambiguous idempotency key across F5 reloads to prevent duplicate orders.
  useEffect(() => {
    if (!pendingKeySig) return;
    const storedKey = readPendingCheckoutKey(pendingKeySig);
    if (!storedKey) return;
    setIdempotencyKey(storedKey);
    const tr = reduceCheckoutSubmitState(submitGuardRef.current, { type: "hydrated_pending_key" });
    submitGuardRef.current = tr.state;
    setSubmitGuard(tr.state);
  }, [pendingKeySig]);

  const enrichedGroups = useMemo(
    () => enrichCartGroups(cart?.shop_groups ?? [], catalogProducts, catalogShops),
    [cart?.shop_groups, catalogProducts, catalogShops]
  );
  const activeGroup = enrichedGroups[0] ?? null;
  const allCartItems = useMemo(() => activeGroup?.items ?? [], [activeGroup]);
  const cartItems = useMemo(() => allCartItems.filter((it) => !it.isUnavailable), [allCartItems]);
  const unavailableCount = allCartItems.length - cartItems.length;

  // When server authoritatively confirms the cart is now empty after an ambiguous/F5 checkout,
  // remind the buyer to check /don-hang and evict the old pending key so future orders never replay it.
  useEffect(() => {
    if (cartItems.length > 0) {
      setPendingOrderCheckHint(false);
      return;
    }
    if (
      !shouldClearPendingKeyOnConfirmedEmptyCart({
        hasPendingKey: submitGuard.keyUncertainty !== "none",
        isAuthenticated,
        sessionLoading,
        cartLoading,
        cartError,
        cartLoaded: cart !== null,
        availableCartItemCount: cartItems.length,
        isSubmitting,
      })
    ) {
      return;
    }
    setPendingOrderCheckHint(true);
    const tr = reduceCheckoutSubmitState(submitGuardRef.current, { type: "confirmed_empty_cart" });
    submitGuardRef.current = tr.state;
    setSubmitGuard(tr.state);
    if (tr.clearStoredKey) clearPendingCheckoutKey();
    if (tr.rotateKey) setIdempotencyKey(generateClientAttemptKey());
  }, [
    submitGuard.keyUncertainty,
    isAuthenticated,
    sessionLoading,
    cartLoading,
    cartError,
    cart,
    cartItems.length,
    isSubmitting,
  ]);

  const shopDiscountBySku = useMemo(() => {
    const map: Record<string, GatewayActiveShopDiscount> = {};
    for (const d of initialShopDiscounts) {
      if (d.sku_id) map[d.sku_id] = d;
    }
    return map;
  }, [initialShopDiscounts]);

  const flashState = useMemo(
    () =>
      resolveCartFlashSaleState({
        items: cartItems.map((it) => ({
          skuId: it.raw.sku_id,
          quantity: it.raw.quantity,
          regularUnitPriceVnd: it.raw.price_vnd,
        })),
        serverFlashSales: initialFlashSales,
        storedFlashItemIdBySku: flashSaleItemBySku,
        optedOutFlashSkus,
        nowMs: anchorNowMs,
      }),
    [cartItems, initialFlashSales, flashSaleItemBySku, optedOutFlashSkus, anchorNowMs]
  );
  const hasFlashSaleItem = flashState.hasFlashSaleItem;
  const canMutateFlashSelection = canMutateCheckoutFlashSelection(submitGuard);

  // Fetch dynamic payment method config if not hydrated from SSR
  useEffect(() => {
    if (initialPaymentMethods.length > 0) return;
    let active = true;
    api<{ methods?: GatewayPaymentMethodConfig[] }>("/api/payment-methods", { retryOn401: false })
      .then((res) => {
        if (active && Array.isArray(res?.methods) && res.methods.length > 0) {
          setPaymentMethods(res.methods);
        }
      })
      .catch(() => {
        // Keep fallback configs
      });
    return () => {
      active = false;
    };
  }, [initialPaymentMethods.length]);

  useEffect(() => {
    const nextDefault = resolveDefaultCheckoutMethod(paymentMethods, {
      hasFlashSaleItem,
      currentMethod: paymentMethod,
    });
    if (nextDefault && nextDefault !== paymentMethod) {
      setPaymentMethod(nextDefault);
    }
  }, [paymentMethods, hasFlashSaleItem, paymentMethod]);

  const selectedAddress = useMemo(
    () => addresses.find((a) => a.id === selectedAddressId) ?? addresses[0],
    [addresses, selectedAddressId]
  );

  const addonProducts = useMemo(() => {
    const inCart = new Set(cartItems.map((c) => c.raw.product_id));
    const filtered = catalogProducts.filter(
      (p) => !inCart.has(p.backendId ?? p.id) && !inCart.has(p.id)
    );
    return (filtered.length >= 4 ? filtered : catalogProducts).slice(0, 4);
  }, [cartItems, catalogProducts]);

  const loadCheckoutMeta = useCallback(async () => {
    if (!isAuthenticated) return;
    setAddressesLoading(true);
    try {
      const [addrRes, vRes] = await Promise.allSettled([
        api<{ addresses: GwAddress[] }>("/api/checkout/addresses"),
        api<{ vouchers: GwVoucher[] }>("/api/vouchers"),
      ]);
      if (addrRes.status === "fulfilled") {
        const list = addrRes.value.addresses ?? [];
        const hadAddresses = hasAddressesRef.current;
        hasAddressesRef.current = list.length > 0;
        setAddresses(list);
        setSelectedAddressId((prev) => {
          if (prev && list.some((a) => a.id === prev)) return prev;
          return list.find((a) => a.is_default)?.id ?? list[0]?.id ?? "";
        });
        if (list.length === 0) {
          setShowNewAddressForm(true);
          setOpenStage("address");
        } else if (!hadAddresses) {
          setShowNewAddressForm(false);
          setOpenStage((prev) => (prev === "address" ? "payment" : prev));
        }
      }
      if (vRes.status === "fulfilled" && Array.isArray(vRes.value.vouchers)) {
        setVouchers(
          vRes.value.vouchers.filter((v) => v.scope.toUpperCase() === "SHOP" && v.is_active)
        );
      }
    } finally {
      setAddressesLoading(false);
    }
  }, [isAuthenticated]);

  useEffect(() => {
    if (!isAuthenticated) {
      didInitialMetaFetchRef.current = false;
      return;
    }
    if (didInitialMetaFetchRef.current) return;
    didInitialMetaFetchRef.current = true;
    if (initialAddresses.length === 0 || initialVouchers.length === 0) {
      void loadCheckoutMeta();
    }
  }, [isAuthenticated, initialAddresses.length, initialVouchers.length, loadCheckoutMeta]);

  const totalWeightGrams = Math.max(
    100,
    cartItems.reduce(
      (s, it) => s + Math.max(100, it.raw.weight_grams || 200) * it.raw.quantity,
      0
    ) ||
      activeGroup?.raw.total_weight_grams ||
      500
  );
  const rawSubtotal = useMemo(
    () => cartItems.reduce((sum, it) => sum + it.raw.price_vnd * it.raw.quantity, 0),
    [cartItems]
  );
  const activeShopId = activeGroup?.raw.shop_id ?? "";
  const cartItemsSignature = useMemo(
    () => cartItems.map((it) => `${it.raw.sku_id}:${it.raw.quantity}`).join(","),
    [cartItems]
  );

  const selectedAddressKey = selectedAddress?.id ?? "";
  const selectedProvinceCode = selectedAddress?.province_code ?? "";
  const selectedWardCode = selectedAddress?.ward_code ?? "";
  const codAmountForQuote = paymentMethod === "COD" ? rawSubtotal : 0;

  const quoteRequestKey =
    isAuthenticated && selectedAddressKey && activeShopId && cartItems.length > 0
      ? `${selectedAddressKey}:${selectedProvinceCode}:${selectedWardCode}:${cartItemsSignature}:${paymentMethod}`
      : null;

  const quotesPending = Boolean(quoteRequestKey && quoteState?.key !== quoteRequestKey);
  const quotesFailed = Boolean(
    quoteRequestKey && quoteState?.key === quoteRequestKey && quoteState.status === "error"
  );
  const quotesIsEstimated = Boolean(
    quoteRequestKey && quoteState?.key === quoteRequestKey && quoteState.isEstimated
  );
  const quotes = useMemo<GwCarrierQuote[]>(
    () => (quoteRequestKey && quoteState?.key === quoteRequestKey ? quoteState.quotes : []),
    [quoteRequestKey, quoteState]
  );

  useEffect(() => {
    if (!quoteRequestKey) return;

    let cancelled = false;
    api<{
      quotes?: GwCarrierQuote[];
      quotes_by_shop?: Record<string, GwCarrierQuote[]>;
      is_estimated?: boolean;
    }>("/api/checkout/shipping-quote", {
      method: "POST",
      body: {
        to_province_code: selectedProvinceCode,
        to_ward_code: selectedWardCode,
        items: cartItems.map((it) => ({
          sku_id: it.raw.sku_id,
          quantity: it.raw.quantity,
        })),
        payment_method: paymentMethod,
        weight_grams: totalWeightGrams,
        cod_amount_vnd: codAmountForQuote,
      },
    })
      .then((res) => {
        if (cancelled) return;
        const list =
          res.quotes ??
          (res.quotes_by_shop ? Object.values(res.quotes_by_shop)[0] : undefined) ??
          [];
        setQuoteState({
          key: quoteRequestKey,
          status: "ok",
          quotes: list,
          isEstimated: Boolean(res.is_estimated),
        });
        setSelectedCarrier((prev) => {
          const stillValid =
            prev &&
            list.some(
              (q) => q.carrier_code === prev.carrier_code && q.service_code === prev.service_code
            );
          if (stillValid && prev) return prev;
          return list[0]
            ? { carrier_code: list[0].carrier_code, service_code: list[0].service_code }
            : null;
        });
      })
      .catch(() => {
        if (cancelled) return;
        // Backend computes authoritative shipping fee during checkout; unlock submit on error
        setQuoteState({ key: quoteRequestKey, status: "error", quotes: [], isEstimated: false });
        setSelectedCarrier(null);
      });

    return () => {
      cancelled = true;
    };
  }, [
    quoteRequestKey,
    selectedProvinceCode,
    selectedWardCode,
    cartItems,
    paymentMethod,
    totalWeightGrams,
    codAmountForQuote,
  ]);

  const activeQuote = useMemo<GwCarrierQuote | null>(() => {
    if (quotes.length === 0) return null;
    return (
      (selectedCarrier &&
        quotes.find(
          (q) =>
            q.carrier_code === selectedCarrier.carrier_code &&
            q.service_code === selectedCarrier.service_code
        )) ||
      quotes[0] ||
      null
    );
  }, [quotes, selectedCarrier]);

  const quotedShippingFee: number | null = activeQuote ? activeQuote.fee_vnd : null;
  const flashKey = JSON.stringify(flashState.activeFlashItemIdBySku);

  const previewRequestKey =
    isAuthenticated && activeShopId && cartItems.length > 0 && !quotesPending
      ? buildCheckoutPreviewKey({
          shopId: activeShopId,
          rawSubtotal,
          cartItemsSignature,
          quotedShippingFee,
          voucherId: selectedVoucherId,
          flashKey,
        })
      : null;

  const previewPending = Boolean(
    isAuthenticated &&
    activeShopId &&
    cartItems.length > 0 &&
    (quotesPending || (previewRequestKey && previewState?.key !== previewRequestKey))
  );
  const breakdown =
    previewRequestKey && previewState?.key === previewRequestKey && previewState.status === "ok"
      ? previewState.breakdown
      : null;
  const rejectedFlashSaleItemIds = useMemo(
    () =>
      previewRequestKey && previewState?.key === previewRequestKey && previewState.status === "ok"
        ? previewState.rejectedFlashSaleItemIds
        : [],
    [previewRequestKey, previewState]
  );
  const previewError =
    previewRequestKey && previewState?.key === previewRequestKey && previewState.status === "error"
      ? previewState.error
      : null;

  useEffect(() => {
    if (!previewRequestKey) return;
    if (
      previewStateRef.current?.key === previewRequestKey &&
      previewStateRef.current.status === "ok"
    ) {
      return;
    }
    let cancelled = false;

    api<{ breakdown: GwDiscountBreakdown; rejected_flash_sale_item_ids?: string[] }>(
      "/api/vouchers/preview",
      {
        method: "POST",
        body: {
          shipping_fee_vnd: quotedShippingFee ?? 0,
          shop_voucher_id: selectedVoucherId || "",
          flash_sale_item_by_sku: flashState.activeFlashItemIdBySku,
        },
      }
    )
      .then((res) => {
        if (!cancelled) {
          setPreviewState({
            key: previewRequestKey,
            status: "ok",
            breakdown: res.breakdown,
            rejectedFlashSaleItemIds: Array.isArray(res.rejected_flash_sale_item_ids)
              ? res.rejected_flash_sale_item_ids
              : [],
            error: null,
          });
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setPreviewState({
            key: previewRequestKey,
            status: "error",
            breakdown: null,
            rejectedFlashSaleItemIds: [],
            error: err instanceof ApiError ? err.message : "Chưa cập nhật được ưu đãi.",
          });
        }
      });

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [previewRequestKey]);

  const orderAttemptSignature = useMemo(
    () =>
      JSON.stringify({
        addressId: selectedAddress?.id ?? "",
        paymentMethod,
        cart: cartItems.map((it) => `${it.raw.sku_id}:${it.raw.quantity}`),
        voucher: selectedVoucherId ?? "",
        carrier: selectedCarrier,
        flash: flashKey,
      }),
    [selectedAddress?.id, paymentMethod, cartItems, selectedVoucherId, selectedCarrier, flashKey]
  );

  useEffect(() => {
    if (lastSignatureRef.current === null) {
      lastSignatureRef.current = orderAttemptSignature;
      return;
    }
    if (lastSignatureRef.current !== orderAttemptSignature) {
      lastSignatureRef.current = orderAttemptSignature;
      if (submitGuardRef.current.keyUncertainty === "none") {
        setIdempotencyKey(generateClientAttemptKey());
      }
    }
  }, [orderAttemptSignature]);

  const shopVouchers = useMemo(
    () => vouchers.filter((v) => v.scope.toUpperCase() === "SHOP" && v.is_active),
    [vouchers]
  );

  const subtotal = breakdown?.total_subtotal_vnd ?? rawSubtotal;
  const flashDiscount = breakdown?.total_flash_sale_discount_vnd ?? 0;
  const shopProgramDiscount = breakdown?.total_shop_program_discount_vnd ?? 0;
  const shopVoucherTotal = breakdown?.total_shop_voucher_discount_vnd ?? 0;
  const shopFreeshipDiscount = breakdown?.total_shop_freeship_discount_vnd ?? 0;
  const hasAuthoritativeShipping = quotedShippingFee !== null;
  const shippingFeeTotal =
    quotedShippingFee !== null ? (breakdown?.total_shipping_fee_vnd ?? quotedShippingFee) : 0;
  const finalPayable = breakdown?.final_payable_vnd ?? subtotal + shippingFeeTotal;
  const totalQty = cartItems.reduce((sum, it) => sum + it.raw.quantity, 0);
  const codAreaSupported = activeQuote ? activeQuote.cod_supported !== false : undefined;
  const shopBreakdown = breakdown?.shops?.[0] ?? null;
  const flashLineEvals = useMemo(
    () =>
      evaluateShopFlashLines({
        lines: cartItems.map((it) => {
          const lf = flashState.liveFlashBySku[it.raw.sku_id];
          return {
            skuId: it.raw.sku_id,
            quantity: it.raw.quantity,
            regularUnitPriceVnd: it.raw.price_vnd,
            flashSaleItemId: lf?.flashSaleItemId,
            flashUnitPriceVnd: lf?.flashPriceVnd,
            flashPerUserLimit: lf?.perUserLimit,
            hasActiveShopDiscount: Boolean(shopDiscountBySku[it.raw.sku_id]),
          };
        }),
        previewShopFlashDiscountVnd: shopBreakdown ? shopBreakdown.flash_sale_discount_vnd : null,
        previewWarnings: breakdown?.warnings ?? [],
        rejectedFlashSaleItemIds,
      }),
    [
      cartItems,
      flashState.liveFlashBySku,
      shopDiscountBySku,
      shopBreakdown,
      breakdown?.warnings,
      rejectedFlashSaleItemIds,
    ]
  );

  const paymentMethodEvals = useMemo(() => {
    const map = new Map<
      GatewayPaymentMethod,
      {
        config: GatewayPaymentMethodConfig;
        evalResult: ReturnType<typeof evaluatePaymentMethodForCheckout>;
      }
    >();
    const ordered: GatewayPaymentMethod[] = ["VIETQR", "ZALOPAY", "COD"];
    for (const m of ordered) {
      const cfg =
        paymentMethods.find((pm) => String(pm.method).toUpperCase() === m) ??
        FALLBACK_PAYMENT_METHODS.find((pm) => pm.method === m)!;
      map.set(m, {
        config: cfg,
        evalResult: evaluatePaymentMethodForCheckout(cfg, {
          totalPayableVnd: finalPayable,
          hasFlashSaleItem,
          codRejectedCount: user?.codRejectedCount ?? 0,
          phoneVerified: user?.phoneVerified,
          codAreaSupported,
        }),
      });
    }
    return map;
  }, [
    paymentMethods,
    finalPayable,
    hasFlashSaleItem,
    user?.codRejectedCount,
    user?.phoneVerified,
    codAreaSupported,
  ]);

  const activePaymentEval = paymentMethodEvals.get(paymentMethod)?.evalResult ?? {
    allowed: true,
    reason: "",
  };
  const codConfigMaxVnd = paymentMethodEvals.get("COD")?.config.max_order_vnd ?? 0;

  const hasStockIssue = useMemo(
    () => cartItems.some((it) => it.isOutOfStock || it.exceedsStock),
    [cartItems]
  );

  const handleApplyCode = async (e: React.FormEvent) => {
    e.preventDefault();
    const clean = promoCodeInput.trim().toUpperCase();
    if (!clean) {
      setPromoFeedback({ ok: false, message: "Nhập mã giảm giá trước khi áp dụng." });
      return;
    }
    if (promoValidating) return;
    setPromoValidating(true);
    setPromoFeedback(null);

    try {
      const res = await api<{
        valid: boolean;
        voucher?: GwVoucher;
        discount_vnd: number;
        reason?: string;
        breakdown?: GwDiscountBreakdown;
        rejected_flash_sale_item_ids?: string[];
      }>("/api/vouchers/validate", {
        method: "POST",
        body: {
          code: clean,
          shipping_fee_vnd: quotedShippingFee ?? 0,
          shop_voucher_id: selectedVoucherId || "",
          flash_sale_item_by_sku: flashState.activeFlashItemIdBySku,
        },
      });
      if (!res.valid || !res.voucher) {
        setPromoFeedback({
          ok: false,
          message: res.reason || `Mã ${clean} chưa đủ điều kiện áp dụng.`,
        });
        return;
      }
      setSelectedVoucher(res.voucher.id);
      if (
        res.breakdown &&
        isAuthenticated &&
        activeShopId &&
        cartItems.length > 0 &&
        !quotesPending
      ) {
        const nextKey = buildCheckoutPreviewKey({
          shopId: activeShopId,
          rawSubtotal,
          cartItemsSignature,
          quotedShippingFee,
          voucherId: res.voucher.id,
          flashKey,
        });
        const nextState = {
          key: nextKey,
          status: "ok" as const,
          breakdown: res.breakdown,
          rejectedFlashSaleItemIds: Array.isArray(res.rejected_flash_sale_item_ids)
            ? res.rejected_flash_sale_item_ids
            : [],
          error: null,
        };
        previewStateRef.current = nextState;
        setPreviewState(nextState);
      }
      setPromoFeedback({
        ok: true,
        message:
          res.discount_vnd > 0
            ? `Đã áp mã ${res.voucher.code} (-${formatVnd(res.discount_vnd)}).`
            : `Đã chọn mã ${res.voucher.code}.`,
      });
      setPromoCodeInput("");
    } catch (err) {
      setPromoFeedback({
        ok: false,
        message: err instanceof ApiError ? err.message : "Chưa kiểm tra được mã giảm giá.",
      });
    } finally {
      setPromoValidating(false);
    }
  };

  const handleStartNewOrderAttempt = () => {
    const tr = reduceCheckoutSubmitState(submitGuardRef.current, { type: "explicit_new_order" });
    submitGuardRef.current = tr.state;
    setSubmitGuard(tr.state);
    if (tr.clearStoredKey) clearPendingCheckoutKey();
    if (tr.rotateKey) setIdempotencyKey(generateClientAttemptKey());
    setFormError(null);
    setFormErrorCode(null);
  };

  const handleOptOutAllFlashSkus = () => {
    if (!canMutateCheckoutFlashSelection(submitGuardRef.current)) return;
    const skuIds = Object.keys(flashState.activeFlashItemIdBySku);
    if (skuIds.length === 0) return;
    optOutFlashSaleSkus(skuIds);
    setFormError(null);
    setFormErrorCode(null);
    const nextState: CheckoutSubmitGuardState = {
      ...submitGuardRef.current,
      flashBlocked: false,
    };
    submitGuardRef.current = nextState;
    setSubmitGuard(nextState);
  };

  const handlePlaceOrder = async () => {
    if (isSubmitting || submittingRef.current) return;
    if (!selectedAddress) {
      setOpenStage("address");
      setFormError("Chọn hoặc thêm địa chỉ nhận hàng trước khi đặt.");
      return;
    }
    if (!activeGroup || cartItems.length === 0) {
      setFormError("Giỏ hàng đang trống.");
      return;
    }
    if (hasStockIssue) {
      setFormError("Có sản phẩm hết hàng hoặc vượt tồn kho.");
      return;
    }
    // Advisory-only for phone verification (server is source of truth for non-Flash COD);
    // hard-block disabled method, flash-sale COD, rejected limit, or out-of-range amounts.
    if (!activePaymentEval.allowed && activePaymentEval.blockCode !== "PHONE_NOT_VERIFIED") {
      setOpenStage("payment");
      setFormError(activePaymentEval.reason || "Phương thức thanh toán đang chọn không khả dụng.");
      setFormErrorCode(null);
      return;
    }

    submittingRef.current = true;
    setFormError(null);
    setFormErrorCode(null);
    setSessionExpired(false);
    setIsSubmitting(true);

    const startTr = reduceCheckoutSubmitState(submitGuardRef.current, { type: "submit_start" });
    submitGuardRef.current = startTr.state;
    setSubmitGuard(startTr.state);
    if (pendingKeySig) {
      savePendingCheckoutKey({ key: idempotencyKey, sig: pendingKeySig });
    }

    const flashItems = cartItems
      .filter((it) => Boolean(flashState.activeFlashItemIdBySku[it.raw.sku_id]))
      .map((it) => ({
        sku_id: it.raw.sku_id,
        quantity: it.raw.quantity,
        flash_sale_item_id: flashState.activeFlashItemIdBySku[it.raw.sku_id],
      }));

    const checkoutRequestBody: Record<string, unknown> = {
      address_id: selectedAddress.id,
      from_cart: true,
      items: flashItems,
      payment_method: paymentMethod,
    };
    if (selectedVoucherId) {
      checkoutRequestBody.shop_voucher_id = selectedVoucherId;
    }
    if (selectedCarrier) {
      checkoutRequestBody.shipping_choice = selectedCarrier;
    }

    try {
      const res = await api<GwCheckoutResponse>("/api/checkout", {
        method: "POST",
        headers: { "Idempotency-Key": idempotencyKey },
        body: checkoutRequestBody,
      });

      const terminalCancelled = isTerminalCancelledCheckoutResponse(res);
      const okTr = reduceCheckoutSubmitState(submitGuardRef.current, {
        type: "submit_success",
        terminalCancelled,
      });
      submitGuardRef.current = okTr.state;
      setSubmitGuard(okTr.state);
      if (okTr.clearStoredKey) clearPendingCheckoutKey();

      if (terminalCancelled) {
        submittingRef.current = false;
        setIsSubmitting(false);
        setIdempotencyKey(generateClientAttemptKey());
        setFormError(CHECKOUT_FAILED_BUYER_MESSAGE);
        setFormErrorCode("CHECKOUT_FAILED");
        void refreshCart();
        return;
      }

      resetVoucherSelection();
      clearOptedOutFlashSkus();
      void refreshCart();
      if (Array.isArray(res.warnings) && res.warnings.length > 0) {
        saveCheckoutWarnings({
          paymentId: res.payment_id,
          orderIds: (res.orders ?? []).map((o) => o.id),
          checkoutGroupId: res.checkout_group_id,
          warnings: res.warnings,
        });
      }

      const firstOrderId = res.orders?.[0]?.id ?? "";
      const resMethod = res.payment_method.toUpperCase();
      const isOnlinePending =
        (resMethod === "VIETQR" || resMethod === "ZALOPAY") &&
        Boolean(res.payment_id) &&
        res.total_payable_vnd > 0 &&
        res.payment_status.toUpperCase() !== "SUCCEEDED";

      if (isOnlinePending) {
        showToast(
          resMethod === "ZALOPAY"
            ? "Đã tạo đơn. Hoàn tất thanh toán qua Ví ZaloPay."
            : "Đã tạo đơn. Quét mã VietQR để thanh toán."
        );
        const q = firstOrderId ? `?order=${encodeURIComponent(firstOrderId)}` : "";
        router.push(`/thanh-toan/cho/${encodeURIComponent(res.payment_id)}${q}`);
        return;
      }

      showToast("Đặt hàng thành công.");
      router.push(firstOrderId ? `/don-hang/${encodeURIComponent(firstOrderId)}` : "/don-hang");
    } catch (err) {
      submittingRef.current = false;
      setIsSubmitting(false);

      const errKind = classifyCheckoutSubmitError(err, {
        hasFlashSaleItem,
        paymentMethod,
      });
      const errTr = reduceCheckoutSubmitState(submitGuardRef.current, {
        type: "submit_error",
        kind: errKind,
      });
      submitGuardRef.current = errTr.state;
      setSubmitGuard(errTr.state);
      if (errTr.clearStoredKey) clearPendingCheckoutKey();
      if (errTr.rotateKey) setIdempotencyKey(generateClientAttemptKey());

      if (err instanceof ApiError) {
        if (errKind === "unauthenticated") {
          setSessionExpired(true);
          setFormError("Phiên đăng nhập đã hết hạn.");
          return;
        }
        setFormErrorCode(err.code || null);
        setFormError(
          translateCheckoutError(err, {
            paymentMethod,
            totalPayableVnd: finalPayable,
            codRejectedCount: user?.codRejectedCount ?? 0,
            codMaxVnd: codConfigMaxVnd,
            hasVouchers: Boolean(selectedVoucherId),
            isFlashSale: hasFlashSaleItem,
          })
        );
        if (
          err.code === "OUT_OF_STOCK" ||
          err.code === "INSUFFICIENT_STOCK" ||
          err.code === "SKU_INACTIVE" ||
          err.code === "EMPTY_CART" ||
          err.code === "CART_NO_AVAILABLE_ITEMS"
        ) {
          void refreshCart();
        }
        if (err.code.startsWith("VOUCHER_")) {
          void loadCheckoutMeta();
        }
      } else {
        setFormError(AMBIGUOUS_CHECKOUT_MESSAGE);
      }
    }
  };

  const renderHeader = () => (
    <SitePageHeader
      title="Thanh toán"
      subtitle={brandName}
      action={
        <Button asChild variant="outline" className="min-h-10 shadow-none">
          <Link href="/gio-hang">Giỏ hàng</Link>
        </Button>
      }
    />
  );

  if (sessionLoading || (isAuthenticated && cartLoading && !cart)) {
    return (
      <SitePageFrame>
        {renderHeader()}
        <SiteLoadingSkeleton title="Đang tải thanh toán..." rows={4} variant="detail" />
      </SitePageFrame>
    );
  }

  if (!isAuthenticated) {
    return (
      <SitePageFrame>
        {renderHeader()}
        <EmptyState
          icon={LogIn}
          title="Đăng nhập để hoàn tất đơn hàng."
          description={`Đăng nhập để chọn địa chỉ nhận hàng và phương thức thanh toán tại ${brandName}.`}
          className="border border-border shadow-none"
          action={
            <Button asChild className="min-h-10 shadow-none">
              <Link href="/dang-nhap?next=%2Fthanh-toan">Đăng nhập</Link>
            </Button>
          }
        />
      </SitePageFrame>
    );
  }

  if (cartError && !cart) {
    return (
      <SitePageFrame>
        {renderHeader()}
        <ErrorState
          title="Chưa kết nối được trang thanh toán."
          description={cartError}
          onRetry={() => void refreshCart()}
          className="border border-border shadow-none"
        />
      </SitePageFrame>
    );
  }

  if (!activeGroup || cartItems.length === 0) {
    const showOrderCheckNotice = pendingOrderCheckHint || submitGuard.keyUncertainty !== "none";
    return (
      <SitePageFrame>
        {renderHeader()}
        {showOrderCheckNotice ? (
          <EmptyState
            icon={ShoppingBag}
            title="Đơn hàng trước có thể đã được tạo."
            description="Giỏ hàng hiện đã trống. Vui lòng kiểm tra mục Đơn hàng của tôi trước khi đặt lại."
            className="border border-border shadow-none"
            action={
              <Button asChild className="min-h-10 shadow-none">
                <Link href="/don-hang">Đơn hàng của tôi</Link>
              </Button>
            }
          />
        ) : (
          <EmptyState
            icon={ShoppingBag}
            title="Chưa có sản phẩm nào để thanh toán."
            description={`Chọn sản phẩm tại ${brandName} rồi quay lại hoàn tất đơn hàng.`}
            className="border border-border shadow-none"
            action={
              <Button asChild className="min-h-10 shadow-none">
                <Link href="/san-pham">Xem sản phẩm</Link>
              </Button>
            }
          />
        )}
        {addonProducts.length > 0 && (
          <ProductAddonStrip title={`Gợi ý từ ${brandName}`} products={addonProducts} />
        )}
      </SitePageFrame>
    );
  }

  const addressDone = Boolean(selectedAddress);
  const shippingDone = Boolean(selectedAddress && activeQuote);
  const addressOpen = openStage === "address" || !addressDone;
  const shippingOpen = openStage === "shipping" && addressDone;
  const paymentOpen = openStage === "payment" && addressDone;
  const isCalculating = Boolean(selectedAddress) && (quotesPending || previewPending);

  const payLabel = PAYMENT_METHOD_COPY[paymentMethod].shortLabel;
  const ctaLabel = `Đặt hàng ${formatVnd(finalPayable)} (${payLabel})`;
  const canPlaceOrder =
    !isSubmitting &&
    !isCalculating &&
    !hasStockIssue &&
    cartItems.length > 0 &&
    Boolean(selectedAddress) &&
    activePaymentEval.allowed;

  const renderOrderSummaryBody = (inputIdPrefix: string) => (
    <div className="space-y-4">
      <div className="divide-y divide-border">
        {cartItems.map((it) => {
          const skuId = it.raw.sku_id;
          const liveFlash = flashState.liveFlashBySku[skuId] ?? null;
          const availableLiveFlash = flashState.availableLiveFlashBySku[skuId] ?? null;
          const isFlashOptedOut = Boolean(availableLiveFlash) && !liveFlash;
          const flashEval = flashLineEvals[skuId] ?? null;
          const flashInspection = flashState.inspectionBySku[skuId] ?? null;
          const lp = resolveDisplayedLinePricing({
            regularUnitPriceVnd: it.raw.price_vnd,
            quantity: it.raw.quantity,
            isFlashLine: Boolean(liveFlash),
            flashUnitPriceVnd: liveFlash?.flashPriceVnd,
            flashShortfall: Boolean(flashEval?.needsRegularPriceChoice),
            activeShopDiscount: shopDiscountBySku[skuId] ?? null,
            shopBreakdown,
            isInPreview: Boolean(shopBreakdown),
          });
          return (
            <div key={skuId} className="flex items-start gap-3 py-3 first:pt-0 last:pb-0">
              <img
                src={it.image}
                alt={it.productName}
                width={896}
                height={1200}
                className="aspect-[3/4] w-14 shrink-0 rounded-md border border-border bg-muted object-cover"
              />
              <div className="min-w-0 flex-1 space-y-1">
                <p className="line-clamp-2 text-sm leading-snug font-medium text-foreground">
                  {it.productName}
                </p>
                <div className="flex flex-wrap items-center gap-1">
                  {splitVariantParts(it.variantLabel).map((part) => (
                    <Badge
                      key={part}
                      variant="secondary"
                      className="rounded-sm px-1.5 py-0 text-[11px] font-normal"
                    >
                      {part}
                    </Badge>
                  ))}
                  <span className="text-xs text-muted-foreground tabular-nums">
                    ×{it.raw.quantity}
                  </span>
                </div>
                {(liveFlash || isFlashOptedOut) && (
                  <div className="flex flex-wrap items-center gap-1.5 pt-0.5">
                    {liveFlash ? (
                      <>
                        <StatusBadge tone="warning">Flash sale</StatusBadge>
                        <Button
                          type="button"
                          variant="ghost"
                          size="xs"
                          className="h-7 text-xs text-brand hover:text-brand"
                          disabled={isSubmitting || !canMutateFlashSelection}
                          onClick={() => optOutFlashSaleSku(skuId)}
                        >
                          Mua giá thường
                        </Button>
                      </>
                    ) : (
                      <>
                        <StatusBadge tone="neutral">Giá thường</StatusBadge>
                        <Button
                          type="button"
                          variant="ghost"
                          size="xs"
                          className="h-7 text-xs text-brand hover:text-brand"
                          disabled={isSubmitting || !canMutateFlashSelection}
                          onClick={() => restoreFlashSaleSku(skuId)}
                        >
                          Dùng giá Flash
                        </Button>
                      </>
                    )}
                  </div>
                )}
                {flashEval?.reason ? (
                  <p className="text-xs leading-snug font-medium text-destructive">
                    {flashEval.reason}
                  </p>
                ) : flashInspection && flashInspection.state !== "live" ? (
                  <p className="text-xs leading-snug text-muted-foreground">
                    {flashInspection.message}
                  </p>
                ) : null}
              </div>
              <span className="shrink-0 text-sm font-semibold text-foreground tabular-nums">
                {formatVnd(lp.lineTotalVnd)}
              </span>
            </div>
          );
        })}
      </div>

      {unavailableCount > 0 && (
        <Alert
          className="border-badge-warning-ink/25 bg-badge-warning-bg py-2.5 text-badge-warning-ink shadow-none"
          role="status"
        >
          <AlertTriangle className="size-4" aria-hidden="true" />
          <AlertDescription className="text-xs text-badge-warning-ink">
            <strong className="font-semibold tabular-nums">{unavailableCount}</strong> sản phẩm
            không khả dụng trong giỏ hàng đã được bỏ qua.
          </AlertDescription>
        </Alert>
      )}

      <Separator />

      <form className="space-y-2.5" onSubmit={handleApplyCode} noValidate>
        <label
          htmlFor={`${inputIdPrefix}-code-input`}
          className="flex items-center gap-1.5 text-xs font-semibold text-foreground"
        >
          <Tag className="size-3.5 text-muted-foreground" aria-hidden="true" />
          <span>Mã giảm giá</span>
        </label>
        <div className="flex items-stretch gap-2">
          <Input
            id={`${inputIdPrefix}-code-input`}
            type="text"
            className="min-h-10 flex-1 bg-background"
            aria-describedby={promoFeedback ? `${inputIdPrefix}-code-feedback` : undefined}
            value={promoCodeInput}
            onChange={(e) => setPromoCodeInput(e.target.value)}
            placeholder="Nhập mã"
          />
          <Button
            type="submit"
            variant="secondary"
            className="min-h-10 shrink-0 shadow-none"
            disabled={promoValidating}
          >
            {promoValidating ? (
              <>
                <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
                <span>Đang kiểm tra</span>
              </>
            ) : (
              "Áp dụng"
            )}
          </Button>
        </div>
        {promoFeedback && (
          <Alert
            id={`${inputIdPrefix}-code-feedback`}
            variant={promoFeedback.ok ? "default" : "destructive"}
            className={cn(
              "py-2.5 shadow-none",
              promoFeedback.ok &&
                "border-badge-success-ink/25 bg-badge-success-bg text-badge-success-ink"
            )}
            role={promoFeedback.ok ? "status" : "alert"}
          >
            <AlertDescription
              className={cn("text-xs", promoFeedback.ok && "text-badge-success-ink")}
            >
              {promoFeedback.message}
            </AlertDescription>
          </Alert>
        )}

        {shopVouchers.length > 0 && (
          <div className="flex flex-wrap gap-2 pt-0.5">
            {shopVouchers.map((v) => {
              const reason = formatVoucherIneligibleReason(v, subtotal, anchorNowMs);
              const eligible = reason === null;
              const active =
                (selectedVoucherId === v.id || selectedVoucherId === v.code) && eligible;
              return (
                <button
                  key={v.id}
                  type="button"
                  disabled={!eligible}
                  onClick={() => setSelectedVoucher(active ? null : v.id)}
                  title={
                    eligible ? `Còn ${formatInt(v.remaining_quota)} lượt` : (reason ?? undefined)
                  }
                  className={cn(
                    "inline-flex min-h-10 max-w-full flex-col items-start rounded-lg border px-2.5 py-1.5 text-left transition-colors",
                    active
                      ? "border-brand bg-accent text-foreground"
                      : "border-border bg-background text-foreground hover:bg-accent/60",
                    !eligible && "cursor-not-allowed opacity-50"
                  )}
                >
                  <span className="text-xs font-bold tracking-wide">{v.code}</span>
                  <span className="text-[11px] text-muted-foreground">
                    {formatVoucherHeadline(v)}
                  </span>
                </button>
              );
            })}
          </div>
        )}
      </form>

      {(previewError || (breakdown?.warnings?.length ?? 0) > 0) && (
        <Alert
          className="border-badge-warning-ink/25 bg-badge-warning-bg py-2.5 text-badge-warning-ink shadow-none"
          role="status"
        >
          <AlertDescription className="text-xs text-badge-warning-ink">
            {previewError ?? breakdown?.warnings[0]}
          </AlertDescription>
        </Alert>
      )}

      <Separator />

      <dl className="space-y-2 text-sm">
        <div className="flex items-baseline justify-between gap-3">
          <dt className="text-muted-foreground">
            Tạm tính (<span className="tabular-nums">{totalQty}</span>)
          </dt>
          <dd className="font-medium text-foreground tabular-nums">{formatVnd(subtotal)}</dd>
        </div>
        {flashDiscount > 0 && (
          <div className="flex items-baseline justify-between gap-3">
            <dt className="text-muted-foreground">Flash sale</dt>
            <dd className="font-semibold text-badge-success-ink tabular-nums">
              {formatSignedVnd(-flashDiscount)}
            </dd>
          </div>
        )}
        {shopProgramDiscount > 0 && (
          <div className="flex items-baseline justify-between gap-3">
            <dt className="text-muted-foreground">Giảm giá chương trình gian hàng</dt>
            <dd className="font-semibold text-badge-success-ink tabular-nums">
              {formatSignedVnd(-shopProgramDiscount)}
            </dd>
          </div>
        )}
        <div className="flex items-baseline justify-between gap-3">
          <dt className="text-muted-foreground">Mã giảm giá</dt>
          <dd className="font-semibold text-badge-success-ink tabular-nums">
            {shopVoucherTotal > 0 ? formatSignedVnd(-shopVoucherTotal) : "0₫"}
          </dd>
        </div>
        <div className="flex items-baseline justify-between gap-3">
          <dt className="text-muted-foreground">
            Phí vận chuyển{quotesIsEstimated ? " (tạm tính)" : ""}
          </dt>
          {!selectedAddress ? (
            <dd className="text-xs text-muted-foreground">Tính theo địa chỉ</dd>
          ) : quotesPending ? (
            <dd>
              <Skeleton className="h-4 w-20" aria-label="Đang tính phí vận chuyển" />
            </dd>
          ) : hasAuthoritativeShipping ? (
            <dd className="font-medium text-foreground tabular-nums">
              {formatVnd(shippingFeeTotal)}
            </dd>
          ) : (
            <dd className="text-xs text-muted-foreground">Phí tính khi đặt</dd>
          )}
        </div>
        {shopFreeshipDiscount > 0 && (
          <div className="flex items-baseline justify-between gap-3">
            <dt className="text-muted-foreground">Gian hàng hỗ trợ phí ship</dt>
            <dd className="font-semibold text-badge-success-ink tabular-nums">
              {formatSignedVnd(-shopFreeshipDiscount)}
            </dd>
          </div>
        )}
      </dl>
    </div>
  );

  const renderPlaceOrderFooter = () => (
    <div className="space-y-3 border-t border-border bg-muted/40 px-4 py-4 md:px-5">
      <div className="flex items-baseline justify-between gap-3">
        <span className="text-sm font-medium text-muted-foreground">Tổng thanh toán</span>
        {isCalculating ? (
          <Skeleton className="h-7 w-28" aria-label="Đang tính tổng thanh toán" />
        ) : (
          <span className="text-2xl font-bold text-foreground tabular-nums">
            {formatVnd(finalPayable)}
          </span>
        )}
      </div>

      {submitGuard.keyUncertainty !== "none" && !formError && (
        <Alert
          className="border-badge-warning-ink/25 bg-badge-warning-bg text-badge-warning-ink shadow-none"
          role="status"
        >
          <AlertTriangle className="size-4" aria-hidden="true" />
          <AlertDescription className="space-y-2 text-xs text-badge-warning-ink">
            <p>{AMBIGUOUS_CHECKOUT_MESSAGE}</p>
            <div className="flex flex-wrap gap-2">
              <Button asChild variant="outline" size="sm" className="min-h-9 shadow-none">
                <Link href="/don-hang">Đơn hàng của tôi</Link>
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="min-h-9 shadow-none"
                disabled={isSubmitting}
                onClick={handleStartNewOrderAttempt}
              >
                Đặt đơn mới
              </Button>
            </div>
          </AlertDescription>
        </Alert>
      )}

      {formError && (
        <Alert variant="destructive" className="shadow-none" role="alert">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertDescription className="space-y-2 text-xs">
            <p>{formError}</p>
            {sessionExpired && (
              <div className="flex flex-wrap gap-2">
                <Button asChild variant="outline" size="sm" className="min-h-9 shadow-none">
                  <Link href="/dang-nhap?next=%2Fthanh-toan">Đăng nhập lại</Link>
                </Button>
              </div>
            )}
            {formErrorCode === "COD_PHONE_NOT_VERIFIED" && (
              <div className="flex flex-wrap gap-2">
                <Button asChild variant="outline" size="sm" className="min-h-9 shadow-none">
                  <Link href="/tai-khoan#xac-minh-sdt">Xác minh SĐT ngay</Link>
                </Button>
              </div>
            )}
            {submitGuard.keyUncertainty !== "none" && (
              <div className="flex flex-wrap gap-2">
                <Button asChild variant="outline" size="sm" className="min-h-9 shadow-none">
                  <Link href="/don-hang">Đơn hàng của tôi</Link>
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="min-h-9 shadow-none"
                  disabled={isSubmitting}
                  onClick={handleStartNewOrderAttempt}
                >
                  Đặt đơn mới
                </Button>
              </div>
            )}
            {submitGuard.checkoutFailedPrior && submitGuard.keyUncertainty === "none" && (
              <div className="flex flex-wrap gap-2">
                <Button asChild variant="outline" size="sm" className="min-h-9 shadow-none">
                  <Link href="/don-hang">Đơn hàng của tôi</Link>
                </Button>
              </div>
            )}
            {submitGuard.flashBlocked && canMutateFlashSelection && hasFlashSaleItem && (
              <div className="flex flex-wrap gap-2">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="min-h-9 shadow-none"
                  disabled={isSubmitting}
                  onClick={handleOptOutAllFlashSkus}
                >
                  Bỏ Flash sale, mua giá thường
                </Button>
              </div>
            )}
          </AlertDescription>
        </Alert>
      )}

      <Button
        type="button"
        size="lg"
        className="min-h-11 w-full whitespace-normal text-center shadow-none"
        disabled={!canPlaceOrder}
        onClick={() => void handlePlaceOrder()}
      >
        {isSubmitting ? (
          <>
            <Loader2 className="size-4 animate-spin" aria-hidden="true" />
            <span>Đang tạo đơn hàng</span>
          </>
        ) : isCalculating ? (
          "Đang tính…"
        ) : (
          ctaLabel
        )}
      </Button>
    </div>
  );

  return (
    <SitePageFrame>
      {renderHeader()}

      {/* Mobile collapsible order summary accordion at top */}
      <div className="mb-5 lg:hidden">
        <Card className="overflow-hidden border border-border py-0 shadow-none">
          <Accordion type="single" collapsible>
            <AccordionItem value="mobile-summary" className="border-b-0">
              <AccordionTrigger className="min-h-12 items-center px-4 py-3.5 hover:no-underline">
                <span className="flex min-w-0 flex-1 items-center justify-between gap-2 pr-2">
                  <span className="inline-flex items-center gap-2 text-sm font-semibold text-foreground">
                    <ShoppingBag className="size-4 shrink-0 text-brand" aria-hidden="true" />
                    <span>
                      Xem đơn hàng ·{" "}
                      <span className="tabular-nums">
                        {isCalculating ? "Đang tính…" : formatVnd(finalPayable)}
                      </span>
                    </span>
                  </span>
                  <Badge variant="secondary" className="rounded-full tabular-nums">
                    {totalQty} SP
                  </Badge>
                </span>
              </AccordionTrigger>
              <AccordionContent className="border-t border-border px-4 pt-4 pb-4">
                {renderOrderSummaryBody("co-mobile")}
              </AccordionContent>
            </AccordionItem>
          </Accordion>
        </Card>
      </div>

      <div className="grid grid-cols-1 items-start gap-6 lg:grid-cols-[minmax(0,1.35fr)_minmax(360px,0.95fr)] lg:gap-8">
        {/* Left column: contact + 3 checkout stages */}
        <div className="min-w-0 space-y-4">
          {user && (
            <Card className="border border-border py-3.5 shadow-none">
              <CardContent className="flex flex-wrap items-center justify-between gap-2 px-4 md:px-5">
                <div className="flex min-w-0 items-center gap-2.5">
                  <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-secondary text-foreground">
                    <UserRound className="size-4" aria-hidden="true" />
                  </span>
                  <div className="min-w-0">
                    <p className="text-xs font-medium text-muted-foreground">Liên hệ</p>
                    <p className="truncate text-sm font-semibold text-foreground">
                      {user.fullName || user.email}
                      {user.fullName && user.email ? (
                        <span className="font-normal text-muted-foreground"> · {user.email}</span>
                      ) : null}
                    </p>
                  </div>
                </div>
                {user.phone ? (
                  <span className="text-xs text-muted-foreground tabular-nums">{user.phone}</span>
                ) : null}
              </CardContent>
            </Card>
          )}

          {/* Stage 1: Địa chỉ nhận hàng */}
          <Card
            className="border border-border py-0 shadow-none"
            aria-labelledby="stage-address-title"
          >
            <div className="flex items-start justify-between gap-3 px-4 py-4 md:px-5">
              <div className="flex min-w-0 flex-1 items-start gap-3">
                <span
                  className={cn(
                    "mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-full border text-xs font-semibold",
                    addressDone && !addressOpen
                      ? "border-badge-success-ink/30 bg-badge-success-bg text-badge-success-ink"
                      : addressOpen
                        ? "border-brand bg-primary text-primary-foreground"
                        : "border-border bg-muted text-muted-foreground"
                  )}
                  aria-hidden="true"
                >
                  {addressDone && !addressOpen ? (
                    <Check className="size-3.5" />
                  ) : (
                    <MapPin className="size-3.5" />
                  )}
                </span>

                <div className="min-w-0 flex-1 space-y-1">
                  <h2 id="stage-address-title" className="text-base font-semibold text-foreground">
                    Địa chỉ nhận hàng
                  </h2>

                  {!addressOpen && selectedAddress && (
                    <button
                      type="button"
                      aria-expanded={false}
                      aria-controls="stage-address-body"
                      onClick={() => setOpenStage("address")}
                      className="flex w-full flex-col items-start gap-0.5 rounded-md text-left text-sm transition-opacity hover:opacity-80"
                    >
                      <span className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-foreground">
                        <strong className="font-semibold">{selectedAddress.recipient_name}</strong>
                        <span className="text-muted-foreground tabular-nums">
                          {selectedAddress.recipient_phone}
                        </span>
                      </span>
                      <span className="text-xs leading-relaxed text-muted-foreground">
                        {formatAddressLine(
                          selectedAddress.street_address,
                          selectedAddress.ward_name,
                          selectedAddress.province_name
                        )}
                      </span>
                    </button>
                  )}
                </div>
              </div>

              {!addressOpen && selectedAddress && (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  aria-expanded={false}
                  aria-controls="stage-address-body"
                  aria-label="Sửa địa chỉ nhận hàng"
                  className="min-h-10 shrink-0 text-brand shadow-none hover:text-brand sm:min-h-8"
                  onClick={() => setOpenStage("address")}
                >
                  Sửa
                </Button>
              )}
            </div>

            <div id="stage-address-body" hidden={!addressOpen}>
              {addressOpen && (
                <div className="space-y-4 border-t border-border px-4 py-4 md:px-5">
                  {addressesLoading && addresses.length === 0 ? (
                    <SiteLoadingSkeleton title="Đang tải địa chỉ..." rows={2} />
                  ) : addresses.length > 0 ? (
                    <>
                      <RadioGroup
                        role="radiogroup"
                        aria-label="Chọn địa chỉ nhận hàng"
                        value={selectedAddress?.id ?? ""}
                        onValueChange={(nextId) => {
                          setSelectedAddressId(nextId);
                          setShowNewAddressForm(false);
                          setOpenStage("shipping");
                        }}
                        className="gap-2.5"
                      >
                        {addresses.map((addr) => {
                          const isSelected = selectedAddress?.id === addr.id;
                          const itemId = `checkout-addr-${addr.id}`;
                          return (
                            <label
                              key={addr.id}
                              htmlFor={itemId}
                              className={cn(
                                "flex cursor-pointer items-start gap-3 rounded-lg border p-3.5 transition-colors",
                                isSelected
                                  ? "border-brand bg-accent/60"
                                  : "border-border bg-background hover:bg-accent/40"
                              )}
                            >
                              <RadioGroupItem value={addr.id} id={itemId} className="mt-0.5" />
                              <span className="flex min-w-0 flex-1 flex-col gap-1">
                                <span className="flex flex-wrap items-center justify-between gap-2">
                                  <span className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-sm">
                                    <strong className="font-semibold text-foreground">
                                      {addr.recipient_name}
                                    </strong>
                                    <span className="text-muted-foreground tabular-nums">
                                      {addr.recipient_phone}
                                    </span>
                                  </span>
                                  <span className="flex flex-wrap items-center gap-1.5">
                                    {addr.is_default && (
                                      <StatusBadge tone="neutral">Mặc định</StatusBadge>
                                    )}
                                    {isSelected && (
                                      <StatusBadge tone="success">Đang chọn</StatusBadge>
                                    )}
                                  </span>
                                </span>
                                <span className="text-xs leading-relaxed text-muted-foreground">
                                  {formatAddressLine(
                                    addr.street_address,
                                    addr.ward_name,
                                    addr.province_name
                                  )}
                                </span>
                              </span>
                            </label>
                          );
                        })}
                      </RadioGroup>

                      <div className="flex flex-wrap items-center gap-2.5 pt-1">
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          className="min-h-10 shadow-none sm:min-h-9"
                          onClick={() => setShowNewAddressForm((v) => !v)}
                        >
                          {showNewAddressForm ? "Đóng biểu mẫu" : "Thêm địa chỉ mới"}
                        </Button>
                        {!showNewAddressForm && selectedAddress && (
                          <Button
                            type="button"
                            size="sm"
                            className="min-h-10 shadow-none sm:min-h-9"
                            onClick={() => setOpenStage("shipping")}
                          >
                            Tiếp tục giao hàng
                          </Button>
                        )}
                      </div>
                    </>
                  ) : null}

                  {showNewAddressForm && (
                    <AddressForm
                      defaultName={user?.fullName ?? ""}
                      defaultPhone={user?.phone ?? ""}
                      submitLabel="Lưu và giao tới địa chỉ này"
                      onSaved={(created) => {
                        setAddresses((prev) => [
                          created,
                          ...prev.map((a) =>
                            created.is_default ? { ...a, is_default: false } : a
                          ),
                        ]);
                        setSelectedAddressId(created.id);
                        setShowNewAddressForm(false);
                        setFormError(null);
                        setOpenStage("payment");
                      }}
                      onCancel={
                        addresses.length > 0 ? () => setShowNewAddressForm(false) : undefined
                      }
                    />
                  )}
                </div>
              )}
            </div>
          </Card>

          {/* Stage 2: Giao hàng */}
          <Card
            className={cn("border border-border py-0 shadow-none", !addressDone && "opacity-70")}
            aria-labelledby="stage-shipping-title"
          >
            <div className="flex items-start justify-between gap-3 px-4 py-4 md:px-5">
              <div className="flex min-w-0 flex-1 items-start gap-3">
                <span
                  className={cn(
                    "mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-full border text-xs font-semibold",
                    shippingDone && !shippingOpen
                      ? "border-badge-success-ink/30 bg-badge-success-bg text-badge-success-ink"
                      : shippingOpen
                        ? "border-brand bg-primary text-primary-foreground"
                        : "border-border bg-muted text-muted-foreground"
                  )}
                  aria-hidden="true"
                >
                  {shippingDone && !shippingOpen ? (
                    <Check className="size-3.5" />
                  ) : (
                    <Truck className="size-3.5" />
                  )}
                </span>

                <div className="min-w-0 flex-1 space-y-1">
                  <h2 id="stage-shipping-title" className="text-base font-semibold text-foreground">
                    Giao hàng
                  </h2>

                  {!addressDone ? (
                    <p className="text-xs text-muted-foreground">Chọn địa chỉ để tính cước.</p>
                  ) : !shippingOpen ? (
                    quotesPending ? (
                      <p className="text-xs text-muted-foreground">Đang tính phí vận chuyển…</p>
                    ) : activeQuote ? (
                      <button
                        type="button"
                        aria-expanded={false}
                        aria-controls="stage-shipping-body"
                        onClick={() => setOpenStage("shipping")}
                        className="flex w-full flex-wrap items-center gap-x-2.5 gap-y-0.5 rounded-md text-left text-sm transition-opacity hover:opacity-80"
                      >
                        <strong className="font-semibold text-foreground">
                          {formatCarrierLabel(activeQuote.carrier_code, activeQuote.carrier_name)}
                        </strong>
                        <span className="text-muted-foreground">
                          {formatServiceLabel(activeQuote.service_code, activeQuote.service_name)}
                        </span>
                        <span className="font-semibold text-foreground tabular-nums">
                          {formatVnd(activeQuote.fee_vnd)}
                        </span>
                      </button>
                    ) : (
                      <p className="text-xs text-muted-foreground">
                        {quotesFailed ? "Phí tính khi đặt." : "Tính phí theo địa chỉ nhận hàng."}
                      </p>
                    )
                  ) : null}
                </div>
              </div>

              {addressDone && !shippingOpen && (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  aria-expanded={false}
                  aria-controls="stage-shipping-body"
                  aria-label="Sửa đơn vị vận chuyển"
                  className="min-h-10 shrink-0 text-brand shadow-none hover:text-brand sm:min-h-8"
                  onClick={() => setOpenStage("shipping")}
                >
                  Sửa
                </Button>
              )}
            </div>

            <div id="stage-shipping-body" hidden={!shippingOpen}>
              {shippingOpen && (
                <div className="space-y-4 border-t border-border px-4 py-4 md:px-5">
                  {quotesPending ? (
                    <p className="text-sm text-muted-foreground">Đang tính cước vận chuyển…</p>
                  ) : quotes.length > 0 ? (
                    <RadioGroup
                      role="radiogroup"
                      aria-label={`Chọn đơn vị vận chuyển cho ${brandName}`}
                      value={
                        activeQuote
                          ? quoteValue(activeQuote.carrier_code, activeQuote.service_code)
                          : ""
                      }
                      onValueChange={(nextVal) => {
                        const found = quotes.find(
                          (q) => quoteValue(q.carrier_code, q.service_code) === nextVal
                        );
                        if (!found) return;
                        setSelectedCarrier({
                          carrier_code: found.carrier_code,
                          service_code: found.service_code,
                        });
                        setOpenStage("payment");
                      }}
                      className="gap-2.5"
                    >
                      {quotes.map((q) => {
                        const val = quoteValue(q.carrier_code, q.service_code);
                        const active =
                          activeQuote?.carrier_code === q.carrier_code &&
                          activeQuote?.service_code === q.service_code;
                        const itemId = `checkout-ship-${q.carrier_code}-${q.service_code}`;
                        return (
                          <label
                            key={val}
                            htmlFor={itemId}
                            className={cn(
                              "flex cursor-pointer items-start gap-3 rounded-lg border p-3.5 transition-colors",
                              active
                                ? "border-brand bg-accent/60"
                                : "border-border bg-background hover:bg-accent/40"
                            )}
                          >
                            <RadioGroupItem value={val} id={itemId} className="mt-0.5" />
                            <span className="flex min-w-0 flex-1 flex-col gap-1">
                              <span className="flex items-baseline justify-between gap-2">
                                <span className="text-sm font-semibold text-foreground">
                                  {formatCarrierLabel(q.carrier_code, q.carrier_name)}
                                </span>
                                <span className="shrink-0 text-sm font-semibold text-foreground tabular-nums">
                                  {formatVnd(q.fee_vnd)}
                                </span>
                              </span>
                              <span className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                                <span>{formatServiceLabel(q.service_code, q.service_name)}</span>
                                <span>
                                  Dự kiến{" "}
                                  <span className="font-medium tabular-nums">
                                    {q.estimated_days}
                                  </span>{" "}
                                  ngày
                                </span>
                              </span>
                            </span>
                          </label>
                        );
                      })}
                    </RadioGroup>
                  ) : (
                    <p className="text-sm text-muted-foreground">
                      Phí vận chuyển được xác nhận tự động khi đặt đơn.
                    </p>
                  )}

                  <div>
                    <Button
                      type="button"
                      size="sm"
                      className="min-h-10 shadow-none sm:min-h-9"
                      onClick={() => setOpenStage("payment")}
                    >
                      Tiếp tục thanh toán
                    </Button>
                  </div>
                </div>
              )}
            </div>
          </Card>

          {/* Stage 3: Phương thức thanh toán */}
          <Card
            className={cn("border border-border py-0 shadow-none", !addressDone && "opacity-70")}
            aria-labelledby="stage-payment-title"
          >
            <div className="flex items-start justify-between gap-3 px-4 py-4 md:px-5">
              <div className="flex min-w-0 flex-1 items-start gap-3">
                <span
                  className={cn(
                    "mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-full border text-xs font-semibold",
                    addressDone && !paymentOpen
                      ? "border-badge-success-ink/30 bg-badge-success-bg text-badge-success-ink"
                      : paymentOpen
                        ? "border-brand bg-primary text-primary-foreground"
                        : "border-border bg-muted text-muted-foreground"
                  )}
                  aria-hidden="true"
                >
                  {addressDone && !paymentOpen ? (
                    <Check className="size-3.5" />
                  ) : (
                    <CreditCard className="size-3.5" />
                  )}
                </span>

                <div className="min-w-0 flex-1 space-y-1">
                  <h2 id="stage-payment-title" className="text-base font-semibold text-foreground">
                    Thanh toán
                  </h2>
                  {!addressDone ? (
                    <p className="text-xs text-muted-foreground">Chọn sau khi nhập địa chỉ.</p>
                  ) : !paymentOpen ? (
                    <p className="text-sm text-muted-foreground">
                      {PAYMENT_METHOD_COPY[paymentMethod].title}
                    </p>
                  ) : null}
                </div>
              </div>

              {addressDone && !paymentOpen && (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  aria-expanded={false}
                  aria-controls="stage-payment-body"
                  aria-label="Sửa phương thức thanh toán"
                  className="min-h-10 shrink-0 text-brand shadow-none hover:text-brand sm:min-h-8"
                  onClick={() => setOpenStage("payment")}
                >
                  Sửa
                </Button>
              )}
            </div>

            <div id="stage-payment-body" hidden={!paymentOpen}>
              {paymentOpen && (
                <div className="space-y-3 border-t border-border px-4 py-4 md:px-5">
                  <RadioGroup
                    role="radiogroup"
                    aria-label="Chọn phương thức thanh toán"
                    value={paymentMethod}
                    onValueChange={(val) => {
                      const methodKey = val as GatewayPaymentMethod;
                      if (
                        paymentMethodEvals.get(methodKey)?.evalResult.blockCode === "FLASH_SALE"
                      ) {
                        return;
                      }
                      setPaymentMethod(methodKey);
                      setFormError(null);
                      setFormErrorCode(null);
                    }}
                    className="gap-2.5"
                  >
                    {(["VIETQR", "ZALOPAY", "COD"] as const).map((methodKey) => {
                      const entry = paymentMethodEvals.get(methodKey);
                      if (!entry || !entry.config.enabled) return null;
                      const copy = PAYMENT_METHOD_COPY[methodKey];
                      const selected = paymentMethod === methodKey;
                      const { allowed, reason, blockCode } = entry.evalResult;
                      const maxVnd = entry.config.max_order_vnd;
                      const minVnd = entry.config.min_order_vnd;
                      const isDisabled = blockCode === "FLASH_SALE";
                      const itemId = `checkout-pay-${methodKey.toLowerCase()}`;

                      return (
                        <label
                          key={methodKey}
                          htmlFor={itemId}
                          className={cn(
                            "flex items-start gap-3 rounded-lg border p-3.5 transition-colors",
                            isDisabled
                              ? "cursor-not-allowed border-border bg-muted/40 opacity-60"
                              : selected
                                ? "cursor-pointer border-brand bg-accent/60"
                                : "cursor-pointer border-border bg-background hover:bg-accent/40"
                          )}
                        >
                          <RadioGroupItem
                            value={methodKey}
                            id={itemId}
                            disabled={isDisabled}
                            className="mt-0.5"
                          />
                          <span className="flex min-w-0 flex-1 flex-col gap-1">
                            <span className="flex flex-wrap items-center justify-between gap-2">
                              <span className="text-sm font-semibold text-foreground">
                                {copy.title}
                              </span>
                              {selected && <StatusBadge tone="info">Đang chọn</StatusBadge>}
                            </span>
                            <span className="text-xs leading-relaxed text-muted-foreground">
                              {copy.description}
                              {minVnd > 0 && (
                                <>
                                  {" "}
                                  Tối thiểu{" "}
                                  <span className="tabular-nums">{formatVnd(minVnd)}</span>.
                                </>
                              )}
                              {maxVnd > 0 && (
                                <>
                                  {" "}
                                  Tối đa <span className="tabular-nums">{formatVnd(maxVnd)}</span>.
                                </>
                              )}
                            </span>
                            {!allowed && reason && (
                              <span className="text-xs font-medium text-destructive">{reason}</span>
                            )}
                            {blockCode === "PHONE_NOT_VERIFIED" && (
                              <span className="text-xs text-muted-foreground">
                                Cần xác minh SĐT trước khi đặt COD.
                              </span>
                            )}
                          </span>
                        </label>
                      );
                    })}
                  </RadioGroup>

                  {paymentMethodEvals.get("COD")?.evalResult.blockCode === "PHONE_NOT_VERIFIED" && (
                    <div className="pt-1">
                      <Button
                        asChild
                        variant="link"
                        size="sm"
                        className="h-auto min-h-9 px-0 text-brand"
                      >
                        <Link href="/tai-khoan#xac-minh-sdt">
                          Xác minh số điện thoại ngay để bật COD
                        </Link>
                      </Button>
                    </div>
                  )}
                </div>
              )}
            </div>
          </Card>

          {/* Mobile full-width Place Order CTA card */}
          <Card className="overflow-hidden border border-border py-0 shadow-none lg:hidden">
            {renderPlaceOrderFooter()}
          </Card>
        </div>

        {/* Desktop sticky right order summary column */}
        <aside
          className="hidden min-w-0 lg:sticky lg:top-24 lg:block"
          aria-label="Chi tiết thanh toán"
        >
          <Card className="overflow-hidden border border-border py-0 shadow-none">
            <CardHeader className="border-b border-border py-4">
              <CardTitle className="text-base font-semibold text-foreground">
                Đơn hàng (<span className="tabular-nums">{totalQty}</span>)
              </CardTitle>
            </CardHeader>
            <CardContent className="py-4">{renderOrderSummaryBody("co")}</CardContent>
            {renderPlaceOrderFooter()}
          </Card>
        </aside>
      </div>
    </SitePageFrame>
  );
}
