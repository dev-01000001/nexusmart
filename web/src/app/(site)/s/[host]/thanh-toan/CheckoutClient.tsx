"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import type { Product } from "@/data/products";
import type { Shop } from "@/data/shops";
import { ApiError, api } from "@/lib/api";
import { formatInt, formatSignedVnd, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { useSession } from "@/components/SessionProvider";
import { Button, IconCheck, StateView } from "@/components";
import { AddressForm } from "@/features/commerce/AddressForm";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
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
import shared from "@/features/commerce/commerce.module.css";
import styles from "./page.module.css";

const COD_MAX_VND = 5_000_000;

type BackendPaymentMethod = "COD" | "VIETQR";
type CheckoutStage = "address" | "shipping" | "payment";

export interface CheckoutClientProps {
  brandName: string;
  initialIdempotencyKey: string;
  catalogProducts: Product[];
  catalogShops: Shop[];
  initialAddresses: GwAddress[];
  initialVouchers: GwVoucher[];
  initialError: string | null;
  serverNowMs?: number;
}

function generateClientAttemptKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `chk-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

export function CheckoutClient({
  brandName,
  initialIdempotencyKey,
  catalogProducts,
  catalogShops,
  initialAddresses,
  initialVouchers,
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
  const [quoteState, setQuoteState] = useState<{
    key: string;
    status: "ok" | "error";
    quotes: GwCarrierQuote[];
  } | null>(null);
  const [selectedCarrier, setSelectedCarrier] = useState<{
    carrier_code: string;
    service_code: string;
  } | null>(null);

  const [paymentMethod, setPaymentMethod] = useState<BackendPaymentMethod>("VIETQR");
  const [previewState, setPreviewState] = useState<{
    key: string;
    status: "ok" | "error";
    breakdown: GwDiscountBreakdown | null;
    error: string | null;
  } | null>(null);

  const [promoCodeInput, setPromoCodeInput] = useState<string>("");
  const [promoValidating, setPromoValidating] = useState<boolean>(false);
  const [promoFeedback, setPromoFeedback] = useState<{ ok: boolean; message: string } | null>(null);

  const [formError, setFormError] = useState<string | null>(initialError);
  const [sessionExpired, setSessionExpired] = useState<boolean>(false);
  const [isSubmitting, setIsSubmitting] = useState<boolean>(false);

  const [idempotencyKey, setIdempotencyKey] = useState<string>(initialIdempotencyKey);
  const lastSignatureRef = useRef<string | null>(null);
  const didInitialMetaFetchRef = useRef<boolean>(false);
  const hasAddressesRef = useRef<boolean>(initialAddresses.length > 0);
  hasAddressesRef.current = addresses.length > 0;
  const previewStateRef = useRef(previewState);
  previewStateRef.current = previewState;

  const enrichedGroups = useMemo(
    () => enrichCartGroups(cart?.shop_groups ?? [], catalogProducts, catalogShops),
    [cart?.shop_groups, catalogProducts, catalogShops]
  );
  const activeGroup = enrichedGroups[0] ?? null;
  const cartItems = useMemo(() => activeGroup?.items ?? [], [activeGroup]);

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

  const totalWeightGrams = Math.max(100, activeGroup?.raw.total_weight_grams || 500);
  const rawSubtotal = activeGroup?.raw.subtotal_vnd ?? 0;
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
      ? `${selectedAddressKey}:${selectedProvinceCode}:${selectedWardCode}:${totalWeightGrams}:${codAmountForQuote}`
      : null;

  const quotesPending = Boolean(quoteRequestKey && quoteState?.key !== quoteRequestKey);
  const quotesFailed = Boolean(
    quoteRequestKey && quoteState?.key === quoteRequestKey && quoteState.status === "error"
  );
  const quotes = useMemo<GwCarrierQuote[]>(
    () => (quoteRequestKey && quoteState?.key === quoteRequestKey ? quoteState.quotes : []),
    [quoteRequestKey, quoteState]
  );

  useEffect(() => {
    if (!quoteRequestKey) return;

    let cancelled = false;
    api<{ quotes?: GwCarrierQuote[]; quotes_by_shop?: Record<string, GwCarrierQuote[]> }>(
      "/api/checkout/shipping-quote",
      {
        method: "POST",
        body: {
          to_province_code: selectedProvinceCode,
          to_ward_code: selectedWardCode,
          weight_grams: totalWeightGrams,
          cod_amount_vnd: codAmountForQuote,
        },
      }
    )
      .then((res) => {
        if (cancelled) return;
        const list =
          res.quotes ??
          (res.quotes_by_shop ? Object.values(res.quotes_by_shop)[0] : undefined) ??
          [];
        setQuoteState({ key: quoteRequestKey, status: "ok", quotes: list });
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
        setQuoteState({ key: quoteRequestKey, status: "error", quotes: [] });
        setSelectedCarrier(null);
      });

    return () => {
      cancelled = true;
    };
  }, [
    quoteRequestKey,
    selectedProvinceCode,
    selectedWardCode,
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
  const flashKey = JSON.stringify(flashSaleItemBySku);

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

    api<{ breakdown: GwDiscountBreakdown }>("/api/vouchers/preview", {
      method: "POST",
      body: {
        shipping_fee_vnd: quotedShippingFee ?? 0,
        shop_voucher_id: selectedVoucherId || "",
        flash_sale_item_by_sku: flashSaleItemBySku,
      },
    })
      .then((res) => {
        if (!cancelled) {
          setPreviewState({
            key: previewRequestKey,
            status: "ok",
            breakdown: res.breakdown,
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
      }),
    [selectedAddress?.id, paymentMethod, cartItems, selectedVoucherId, selectedCarrier]
  );

  useEffect(() => {
    if (lastSignatureRef.current === null) {
      lastSignatureRef.current = orderAttemptSignature;
      return;
    }
    if (lastSignatureRef.current !== orderAttemptSignature) {
      lastSignatureRef.current = orderAttemptSignature;
      setIdempotencyKey(generateClientAttemptKey());
    }
  }, [orderAttemptSignature]);

  const shopVouchers = useMemo(
    () => vouchers.filter((v) => v.scope.toUpperCase() === "SHOP" && v.is_active),
    [vouchers]
  );

  const subtotal = breakdown?.total_subtotal_vnd ?? rawSubtotal;
  const flashDiscount = breakdown?.total_flash_sale_discount_vnd ?? 0;
  const shopVoucherTotal = breakdown?.total_shop_voucher_discount_vnd ?? 0;
  const hasAuthoritativeShipping = quotedShippingFee !== null;
  const shippingFeeTotal =
    quotedShippingFee !== null ? (breakdown?.total_shipping_fee_vnd ?? quotedShippingFee) : 0;
  const finalPayable = breakdown?.final_payable_vnd ?? subtotal + shippingFeeTotal;
  const totalQty = cart?.total_items ?? cartItems.reduce((sum, it) => sum + it.raw.quantity, 0);
  const codOverLimit = finalPayable > COD_MAX_VND;
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
      }>("/api/vouchers/validate", {
        method: "POST",
        body: {
          code: clean,
          shipping_fee_vnd: quotedShippingFee ?? 0,
          shop_voucher_id: selectedVoucherId || "",
          flash_sale_item_by_sku: flashSaleItemBySku,
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

  const handlePlaceOrder = async () => {
    if (isSubmitting) return;
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
    if (paymentMethod === "COD" && codOverLimit) {
      setOpenStage("payment");
      setFormError("Đơn vượt 5.000.000₫ cho COD. Chọn chuyển khoản VietQR.");
      return;
    }

    setFormError(null);
    setSessionExpired(false);
    setIsSubmitting(true);

    const flashItems = cartItems
      .filter((it) => Boolean(flashSaleItemBySku[it.raw.sku_id]))
      .map((it) => ({
        sku_id: it.raw.sku_id,
        quantity: it.raw.quantity,
        flash_sale_item_id: flashSaleItemBySku[it.raw.sku_id],
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

      resetVoucherSelection();
      void refreshCart();

      const firstOrderId = res.orders?.[0]?.id ?? "";

      if (
        res.payment_method.toUpperCase() === "VIETQR" &&
        res.payment_id &&
        res.payment_status.toUpperCase() !== "SUCCEEDED"
      ) {
        showToast("Đã tạo đơn. Quét mã VietQR để thanh toán.");
        const q = firstOrderId ? `?order=${encodeURIComponent(firstOrderId)}` : "";
        router.push(`/thanh-toan/cho/${encodeURIComponent(res.payment_id)}${q}`);
        return;
      }

      showToast("Đặt hàng thành công.");
      router.push(firstOrderId ? `/don-hang/${encodeURIComponent(firstOrderId)}` : "/don-hang");
    } catch (err) {
      setIsSubmitting(false);
      if (err instanceof ApiError) {
        if (err.status === 401) {
          setSessionExpired(true);
          setFormError("Phiên đăng nhập đã hết hạn.");
          return;
        }
        setFormError(err.message);
        if (
          err.code === "OUT_OF_STOCK" ||
          err.code === "SKU_INACTIVE" ||
          err.code === "EMPTY_CART"
        ) {
          void refreshCart();
        }
        if (err.code.startsWith("VOUCHER_")) {
          void loadCheckoutMeta();
        }
      } else {
        setFormError("Hệ thống tạm gián đoạn. Bấm thử lại an toàn, không tạo trùng đơn.");
      }
    }
  };

  const renderHeader = () => (
    <section className={shared.pageHeader}>
      <div className={shared.headerLeadGroup}>
        <h1 className={`${shared.pageTitle} lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Thanh toán
            </span>
          </span>
        </h1>
        <p className={shared.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          {brandName}
        </p>
      </div>
      <div className={shared.headerAside} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
        <Button variant="secondary" size="md" href="/gio-hang">
          Giỏ hàng
        </Button>
      </div>
    </section>
  );

  if (sessionLoading || (isAuthenticated && cartLoading && !cart)) {
    return (
      <div className={shared.page}>
        {renderHeader()}
        <section className={shared.section}>
          <StateView
            kind="loading"
            skeletonVariant="detail"
            title="Đang tải thanh toán..."
            rows={4}
          />
        </section>
      </div>
    );
  }

  if (!isAuthenticated) {
    return (
      <div className={shared.page}>
        {renderHeader()}
        <section className={shared.section}>
          <StateView
            kind="empty"
            title="Đăng nhập để hoàn tất đơn hàng."
            actionLabel="Đăng nhập"
            actionHref="/dang-nhap?next=%2Fthanh-toan"
          />
        </section>
      </div>
    );
  }

  if (cartError && !cart) {
    return (
      <div className={shared.page}>
        {renderHeader()}
        <section className={shared.section}>
          <StateView
            kind="error"
            title="Chưa kết nối được trang thanh toán."
            message={cartError}
            actionLabel="Thử lại"
            onAction={() => void refreshCart()}
          />
        </section>
      </div>
    );
  }

  if (!activeGroup || cartItems.length === 0) {
    return (
      <div className={shared.page}>
        {renderHeader()}
        <section className={shared.section}>
          <StateView
            kind="empty"
            title="Chưa có sản phẩm nào để thanh toán."
            actionLabel="Xem sản phẩm"
            actionHref="/san-pham"
          />
        </section>
        {addonProducts.length > 0 && (
          <ProductAddonStrip title={`Gợi ý từ ${brandName}`} products={addonProducts} />
        )}
      </div>
    );
  }

  const addressDone = Boolean(selectedAddress);
  const shippingDone = Boolean(selectedAddress && activeQuote);
  const addressOpen = openStage === "address" || !addressDone;
  const shippingOpen = openStage === "shipping" && addressDone;
  const paymentOpen = openStage === "payment" && addressDone;
  const isCalculating = Boolean(selectedAddress) && (quotesPending || previewPending);

  const payLabel = paymentMethod === "COD" ? "COD" : "VietQR";
  const ctaLabel = `Đặt hàng ${formatVnd(finalPayable)} (${payLabel})`;
  const mobileCtaLabel = `Đặt ${formatVnd(finalPayable)}`;
  const canPlaceOrder =
    !isSubmitting &&
    !isCalculating &&
    !hasStockIssue &&
    Boolean(selectedAddress) &&
    !(paymentMethod === "COD" && codOverLimit);

  return (
    <div className={`${shared.page} ${styles.mobileSpacer}`}>
      {renderHeader()}

      <section className={shared.section}>
        <div className={styles.checkoutGrid}>
          <div className={styles.stageStack}>
            {/* Stage 1: Địa chỉ nhận hàng */}
            <section className={styles.stageCard} aria-labelledby="stage-address-title">
              <div className={styles.stageHeader}>
                <div className={styles.stageHeaderLeft}>
                  <span
                    className={`${styles.stageMarker} ${
                      addressDone && !addressOpen
                        ? styles.stageMarkerDone
                        : addressOpen
                          ? styles.stageMarkerActive
                          : ""
                    }`}
                    aria-hidden="true"
                  >
                    {addressDone && !addressOpen ? (
                      <IconCheck size={14} />
                    ) : addressOpen ? (
                      <span className={styles.stageMarkerActiveDot} />
                    ) : null}
                  </span>

                  <div className={styles.stageTitleGroup}>
                    <h2 id="stage-address-title" className={styles.stageTitle}>
                      Địa chỉ nhận hàng
                    </h2>

                    {!addressOpen && selectedAddress && (
                      <div
                        className={styles.stageCompactSummary}
                        role="radiogroup"
                        aria-label="Chọn địa chỉ nhận hàng"
                      >
                        <button
                          type="button"
                          role="radio"
                          aria-checked="true"
                          className={styles.stageSummaryRadio}
                          onClick={() => setOpenStage("address")}
                        >
                          <span className={styles.stageSummaryMetaRow}>
                            <strong>{selectedAddress.recipient_name}</strong>
                            <span className="num">{selectedAddress.recipient_phone}</span>
                          </span>
                          <span className={styles.stageSummarySub}>
                            {formatAddressLine(
                              selectedAddress.street_address,
                              selectedAddress.ward_name,
                              selectedAddress.province_name
                            )}
                          </span>
                        </button>
                      </div>
                    )}
                  </div>
                </div>

                {!addressOpen && selectedAddress && (
                  <button
                    type="button"
                    className={styles.editBtn}
                    onClick={() => setOpenStage("address")}
                  >
                    Sửa
                  </button>
                )}
              </div>

              {addressOpen && (
                <div className={styles.stageBody}>
                  {addressesLoading && addresses.length === 0 ? (
                    <StateView
                      kind="loading"
                      skeletonVariant="detail"
                      title="Đang tải địa chỉ..."
                      rows={2}
                    />
                  ) : addresses.length > 0 ? (
                    <>
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
                              onClick={() => {
                                setSelectedAddressId(addr.id);
                                setShowNewAddressForm(false);
                                setOpenStage("shipping");
                              }}
                            >
                              <span className={styles.addressTopRow}>
                                <span className={styles.addressName}>
                                  <span>{addr.recipient_name}</span>
                                  <span className={`${styles.addressPhone} num`}>
                                    {addr.recipient_phone}
                                  </span>
                                </span>
                                <span className={shared.formActions}>
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
                                </span>
                              </span>
                              <span className={styles.addressLine}>
                                {formatAddressLine(
                                  addr.street_address,
                                  addr.ward_name,
                                  addr.province_name
                                )}
                              </span>
                            </button>
                          );
                        })}
                      </div>

                      <div className={styles.stageFooterRow}>
                        <Button
                          variant="secondary"
                          size="sm"
                          onClick={() => setShowNewAddressForm((v) => !v)}
                        >
                          {showNewAddressForm ? "Đóng biểu mẫu" : "Thêm địa chỉ mới"}
                        </Button>
                        {!showNewAddressForm && selectedAddress && (
                          <Button variant="ink" size="sm" onClick={() => setOpenStage("shipping")}>
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
            </section>

            {/* Stage 2: Giao hàng */}
            <section
              className={`${styles.stageCard} ${!addressDone ? styles.stageCardMuted : ""}`}
              aria-labelledby="stage-shipping-title"
            >
              <div className={styles.stageHeader}>
                <div className={styles.stageHeaderLeft}>
                  <span
                    className={`${styles.stageMarker} ${
                      shippingDone && !shippingOpen
                        ? styles.stageMarkerDone
                        : shippingOpen
                          ? styles.stageMarkerActive
                          : ""
                    }`}
                    aria-hidden="true"
                  >
                    {shippingDone && !shippingOpen ? (
                      <IconCheck size={14} />
                    ) : shippingOpen ? (
                      <span className={styles.stageMarkerActiveDot} />
                    ) : null}
                  </span>

                  <div className={styles.stageTitleGroup}>
                    <h2 id="stage-shipping-title" className={styles.stageTitle}>
                      Giao hàng
                    </h2>

                    {!addressDone ? (
                      <p className={styles.stageSummaryLine}>Chọn địa chỉ để tính cước.</p>
                    ) : !shippingOpen ? (
                      quotesPending ? (
                        <p className={styles.stageSummaryLine}>Đang tính phí vận chuyển…</p>
                      ) : activeQuote ? (
                        <div
                          className={styles.stageCompactSummary}
                          role="radiogroup"
                          aria-label={`Chọn đơn vị vận chuyển cho ${brandName}`}
                        >
                          <button
                            type="button"
                            role="radio"
                            aria-checked="true"
                            className={styles.stageSummaryRadio}
                            onClick={() => setOpenStage("shipping")}
                          >
                            <span className={styles.stageSummaryMetaRow}>
                              <strong>
                                {formatCarrierLabel(
                                  activeQuote.carrier_code,
                                  activeQuote.carrier_name
                                )}
                              </strong>
                              <span>
                                {formatServiceLabel(
                                  activeQuote.service_code,
                                  activeQuote.service_name
                                )}
                              </span>
                              <span className={`${styles.stageSummaryFee} num`}>
                                {formatVnd(activeQuote.fee_vnd)}
                              </span>
                            </span>
                          </button>
                        </div>
                      ) : (
                        <p className={styles.stageSummaryLine}>
                          {quotesFailed ? "Phí tính khi đặt." : "Tính phí theo địa chỉ nhận hàng."}
                        </p>
                      )
                    ) : null}
                  </div>
                </div>

                {addressDone && !shippingOpen && (
                  <button
                    type="button"
                    className={styles.editBtn}
                    onClick={() => setOpenStage("shipping")}
                  >
                    Sửa
                  </button>
                )}
              </div>

              {shippingOpen && (
                <div className={styles.stageBody}>
                  {quotesPending ? (
                    <p className={styles.addressShipLine}>Đang tính cước vận chuyển…</p>
                  ) : quotes.length > 0 ? (
                    <div
                      className={styles.deliveryOptions}
                      role="radiogroup"
                      aria-label={`Chọn đơn vị vận chuyển cho ${brandName}`}
                    >
                      {quotes.map((q) => {
                        const active =
                          activeQuote?.carrier_code === q.carrier_code &&
                          activeQuote?.service_code === q.service_code;
                        return (
                          <button
                            key={`${q.carrier_code}-${q.service_code}`}
                            type="button"
                            role="radio"
                            aria-checked={active}
                            className={`${styles.deliveryOptionBtn} ${
                              active ? styles.deliveryOptionActive : ""
                            }`}
                            onClick={() => {
                              setSelectedCarrier({
                                carrier_code: q.carrier_code,
                                service_code: q.service_code,
                              });
                              setOpenStage("payment");
                            }}
                          >
                            <span className={styles.deliveryOptionHead}>
                              <span className={styles.paymentTitle}>
                                {formatCarrierLabel(q.carrier_code, q.carrier_name)}
                              </span>
                              <span className={`${styles.deliveryFee} num`}>
                                {formatVnd(q.fee_vnd)}
                              </span>
                            </span>
                            <span className={styles.addressShipLine}>
                              <span>{formatServiceLabel(q.service_code, q.service_name)}</span>
                              <span>
                                Dự kiến <span className="num">{q.estimated_days}</span> ngày
                              </span>
                            </span>
                          </button>
                        );
                      })}
                    </div>
                  ) : (
                    <p className={styles.addressShipLine}>
                      Phí vận chuyển được xác nhận tự động khi đặt đơn.
                    </p>
                  )}

                  <div className={styles.stageFooterRow}>
                    <Button variant="ink" size="sm" onClick={() => setOpenStage("payment")}>
                      Tiếp tục thanh toán
                    </Button>
                  </div>
                </div>
              )}
            </section>

            {/* Stage 3: Phương thức thanh toán */}
            <section
              className={`${styles.stageCard} ${!addressDone ? styles.stageCardMuted : ""}`}
              aria-labelledby="stage-payment-title"
            >
              <div className={styles.stageHeader}>
                <div className={styles.stageHeaderLeft}>
                  <span
                    className={`${styles.stageMarker} ${
                      addressDone && !paymentOpen
                        ? styles.stageMarkerDone
                        : paymentOpen
                          ? styles.stageMarkerActive
                          : ""
                    }`}
                    aria-hidden="true"
                  >
                    {addressDone && !paymentOpen ? (
                      <IconCheck size={14} />
                    ) : paymentOpen ? (
                      <span className={styles.stageMarkerActiveDot} />
                    ) : null}
                  </span>

                  <div className={styles.stageTitleGroup}>
                    <h2 id="stage-payment-title" className={styles.stageTitle}>
                      Thanh toán
                    </h2>
                    {!addressDone ? (
                      <p className={styles.stageSummaryLine}>Chọn sau khi nhập địa chỉ.</p>
                    ) : !paymentOpen ? (
                      <p className={styles.stageSummaryLine}>
                        {paymentMethod === "COD"
                          ? "Thanh toán khi nhận hàng (COD)"
                          : "Chuyển khoản nhanh VietQR"}
                      </p>
                    ) : null}
                  </div>
                </div>

                {addressDone && !paymentOpen && (
                  <button
                    type="button"
                    className={styles.editBtn}
                    onClick={() => setOpenStage("payment")}
                  >
                    Sửa
                  </button>
                )}
              </div>

              {paymentOpen && (
                <div className={styles.stageBody}>
                  <div
                    className={styles.paymentGrid}
                    role="radiogroup"
                    aria-label="Chọn phương thức thanh toán"
                  >
                    <button
                      type="button"
                      role="radio"
                      aria-checked={paymentMethod === "VIETQR"}
                      className={`${styles.paymentTile} ${
                        paymentMethod === "VIETQR" ? styles.paymentTileSelected : ""
                      }`}
                      onClick={() => setPaymentMethod("VIETQR")}
                    >
                      <span className={styles.paymentHead}>
                        <span className={styles.paymentTitle}>Chuyển khoản nhanh VietQR</span>
                        <span className={styles.paymentCheck} aria-hidden="true">
                          <IconCheck size={13} />
                        </span>
                      </span>
                      <span className={styles.paymentDesc}>
                        Quét mã bằng ứng dụng ngân hàng, xác nhận tự động.
                      </span>
                    </button>

                    <button
                      type="button"
                      role="radio"
                      aria-checked={paymentMethod === "COD"}
                      className={`${styles.paymentTile} ${
                        paymentMethod === "COD" ? styles.paymentTileSelected : ""
                      }`}
                      onClick={() => setPaymentMethod("COD")}
                    >
                      <span className={styles.paymentHead}>
                        <span className={styles.paymentTitle}>Thanh toán khi nhận hàng (COD)</span>
                        <span className={styles.paymentCheck} aria-hidden="true">
                          <IconCheck size={13} />
                        </span>
                      </span>
                      <span className={styles.paymentDesc}>
                        Trả tiền khi nhận hàng. Tối đa <span className="num">5.000.000₫</span>.
                      </span>
                      {codOverLimit && (
                        <span className={shared.fieldError}>
                          Đơn vượt 5.000.000₫. Vui lòng chọn VietQR.
                        </span>
                      )}
                    </button>
                  </div>
                </div>
              )}
            </section>
          </div>

          {/* Sticky order summary column */}
          <aside className={styles.reviewCol} aria-label="Chi tiết thanh toán">
            <div className={styles.reviewInner}>
              <h2 className={styles.reviewTitle}>
                Đơn hàng (<span className="num">{totalQty}</span>)
              </h2>

              <div className={styles.shopSliceItems}>
                {cartItems.map((it) => (
                  <div key={it.raw.sku_id} className={styles.miniItem}>
                    <img
                      src={it.image}
                      alt={it.productName}
                      width={896}
                      height={1200}
                      className={styles.miniThumb}
                    />
                    <div className={styles.miniInfo}>
                      <span className={styles.miniName}>{it.productName}</span>
                      <span className={styles.miniMeta}>
                        {splitVariantParts(it.variantLabel).map((part) => (
                          <span key={part}>{part}</span>
                        ))}
                        <span className="num">×{it.raw.quantity}</span>
                      </span>
                    </div>
                    <span className={`${styles.miniPrice} num`}>
                      {formatVnd(it.raw.price_vnd * it.raw.quantity)}
                    </span>
                  </div>
                ))}
              </div>

              <form className={styles.promoSection} onSubmit={handleApplyCode} noValidate>
                <label htmlFor="co-code-input" className={shared.fieldLabel}>
                  Mã giảm giá
                </label>
                <div className={shared.codeInputRow}>
                  <input
                    id="co-code-input"
                    type="text"
                    className={shared.input}
                    aria-describedby={promoFeedback ? "co-code-feedback" : undefined}
                    value={promoCodeInput}
                    onChange={(e) => setPromoCodeInput(e.target.value)}
                    placeholder="Nhập mã"
                  />
                  <Button
                    type="submit"
                    variant="ink"
                    size="md"
                    loading={promoValidating}
                    loadingLabel="Đang kiểm tra"
                  >
                    Áp dụng
                  </Button>
                </div>
                {promoFeedback && (
                  <p
                    id="co-code-feedback"
                    className={`${shared.feedbackBox} ${
                      promoFeedback.ok ? shared.feedbackSuccess : shared.feedbackError
                    }`}
                    role={promoFeedback.ok ? "status" : "alert"}
                  >
                    {promoFeedback.message}
                  </p>
                )}

                {shopVouchers.length > 0 && (
                  <div className={styles.voucherChips}>
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
                          className={`${styles.voucherChip} ${
                            active ? styles.voucherChipActive : ""
                          }`}
                          onClick={() => setSelectedVoucher(active ? null : v.id)}
                          title={
                            eligible
                              ? `Còn ${formatInt(v.remaining_quota)} lượt`
                              : (reason ?? undefined)
                          }
                        >
                          <span className={styles.voucherChipCode}>{v.code}</span>
                          <span className={styles.voucherChipVal}>{formatVoucherHeadline(v)}</span>
                        </button>
                      );
                    })}
                  </div>
                )}
              </form>

              {(previewError || (breakdown?.warnings?.length ?? 0) > 0) && (
                <p className={`${shared.feedbackBox} ${shared.feedbackWarn}`} role="status">
                  {previewError ?? breakdown?.warnings[0]}
                </p>
              )}

              <dl className={styles.ledgerList}>
                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>
                    Tạm tính (<span className="num">{totalQty}</span>)
                  </dt>
                  <dd className="num">{formatVnd(subtotal)}</dd>
                </div>
                {flashDiscount > 0 && (
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Flash sale</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {formatSignedVnd(-flashDiscount)}
                    </dd>
                  </div>
                )}
                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Mã giảm giá</dt>
                  <dd className={`${styles.ledgerDiscount} num`}>
                    {shopVoucherTotal > 0 ? formatSignedVnd(-shopVoucherTotal) : "0₫"}
                  </dd>
                </div>
                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Phí vận chuyển</dt>
                  {!selectedAddress ? (
                    <dd className={styles.ledgerPendingNote}>Tính theo địa chỉ</dd>
                  ) : quotesPending ? (
                    <dd>
                      <span className={styles.feeSkeleton} aria-label="Đang tính phí vận chuyển" />
                    </dd>
                  ) : hasAuthoritativeShipping ? (
                    <dd className="num">{formatVnd(shippingFeeTotal)}</dd>
                  ) : (
                    <dd className={styles.ledgerPendingNote}>Phí tính khi đặt</dd>
                  )}
                </div>
              </dl>
            </div>

            <div className={styles.ctaBand}>
              <div className={styles.grandBand}>
                <span className={styles.grandLabel}>Tổng thanh toán</span>
                {isCalculating ? (
                  <span className={styles.grandSkeleton} aria-label="Đang tính tổng thanh toán" />
                ) : (
                  <span className={`${styles.grandVal} num`}>{formatVnd(finalPayable)}</span>
                )}
              </div>

              {formError && (
                <div className={`${shared.feedbackBox} ${shared.feedbackError}`} role="alert">
                  <p>{formError}</p>
                  {sessionExpired && (
                    <div style={{ marginTop: 10 }}>
                      <Button variant="secondary" size="sm" href="/dang-nhap?next=%2Fthanh-toan">
                        Đăng nhập lại
                      </Button>
                    </div>
                  )}
                </div>
              )}

              <Button
                variant="primary"
                size="lg"
                fullWidth
                className={shared.wrapBtn}
                loading={isSubmitting}
                loadingLabel="Đang tạo đơn hàng"
                disabled={!canPlaceOrder}
                onClick={handlePlaceOrder}
              >
                <span className={styles.stableBtnLabel}>
                  <span className={styles.stableBtnSizer} aria-hidden="true">
                    {ctaLabel}
                  </span>
                  <span className={styles.stableBtnSizer} aria-hidden="true">
                    Đang tính…
                  </span>
                  <span className={styles.stableBtnLive}>
                    {isCalculating ? "Đang tính…" : ctaLabel}
                  </span>
                </span>
              </Button>
            </div>
          </aside>
        </div>
      </section>

      <div className={styles.mobileCheckoutBar} aria-label="Xác nhận đặt hàng nhanh">
        <div>
          <p className={styles.miniMeta}>{payLabel}</p>
          {isCalculating ? (
            <span className={styles.feeSkeleton} aria-label="Đang tính tổng thanh toán" />
          ) : (
            <p className={`${styles.addressName} num`}>{formatVnd(finalPayable)}</p>
          )}
        </div>
        <Button
          variant="primary"
          size="md"
          loading={isSubmitting}
          loadingLabel="Đang đặt"
          disabled={!canPlaceOrder}
          onClick={handlePlaceOrder}
        >
          <span className={styles.stableBtnLabel}>
            <span className={styles.stableBtnSizer} aria-hidden="true">
              {mobileCtaLabel}
            </span>
            <span className={styles.stableBtnSizer} aria-hidden="true">
              Đang tính…
            </span>
            <span className={styles.stableBtnLive}>
              {isCalculating ? "Đang tính…" : mobileCtaLabel}
            </span>
          </span>
        </Button>
      </div>
    </div>
  );
}
