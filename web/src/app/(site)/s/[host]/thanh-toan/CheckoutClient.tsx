"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import type { Product } from "@/data/products";
import type { Shop } from "@/data/shops";
import { ApiError, api } from "@/lib/api";
import { formatInt, formatSignedVnd, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { useSession } from "@/components/SessionProvider";
import { Button, IconShield, IconTruck, StateView } from "@/components";
import { AddressForm } from "@/features/commerce/AddressForm";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import {
  enrichCartGroups,
  formatAddressLine,
  formatCarrierLabel,
  formatServiceLabel,
  formatVoucherHeadline,
  formatVoucherIneligibleReason,
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

export interface CheckoutClientProps {
  brandName: string;
  initialIdempotencyKey: string;
  catalogProducts: Product[];
  catalogShops: Shop[];
  initialAddresses: GwAddress[];
  initialVouchers: GwVoucher[];
  initialError: string | null;
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
}: CheckoutClientProps) {
  const router = useRouter();
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

  const [vouchers, setVouchers] = useState<GwVoucher[]>(initialVouchers);
  const [quotes, setQuotes] = useState<GwCarrierQuote[]>([]);
  const [selectedCarrier, setSelectedCarrier] = useState<{
    carrier_code: string;
    service_code: string;
  } | null>(null);
  const [quotesLoading, setQuotesLoading] = useState<boolean>(false);

  const [paymentMethod, setPaymentMethod] = useState<BackendPaymentMethod>("VIETQR");
  const [breakdown, setBreakdown] = useState<GwDiscountBreakdown | null>(null);
  const [previewLoading, setPreviewLoading] = useState<boolean>(false);
  const [previewError, setPreviewError] = useState<string | null>(null);

  const [promoCodeInput, setPromoCodeInput] = useState<string>("");
  const [promoValidating, setPromoValidating] = useState<boolean>(false);
  const [promoFeedback, setPromoFeedback] = useState<{ ok: boolean; message: string } | null>(null);

  const [formError, setFormError] = useState<string | null>(initialError);
  const [sessionExpired, setSessionExpired] = useState<boolean>(false);
  const [isSubmitting, setIsSubmitting] = useState<boolean>(false);

  const [idempotencyKey, setIdempotencyKey] = useState<string>(initialIdempotencyKey);
  const lastSignatureRef = useRef<string | null>(null);

  // Cart returned by BFF /api/cart is strictly scoped to this site's shop.
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

  useRevealOnScroll(
    `checkout-${isAuthenticated ? "auth" : "guest"}-${cartItems.length}-${paymentMethod}-${addresses.length}`
  );

  // Refresh addresses & shop vouchers client-side if server didn't have them (e.g. after client login).
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
        setAddresses(list);
        setSelectedAddressId((prev) => {
          if (prev && list.some((a) => a.id === prev)) return prev;
          return list.find((a) => a.is_default)?.id ?? list[0]?.id ?? "";
        });
        if (list.length === 0) setShowNewAddressForm(true);
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
    if (isAuthenticated && (addresses.length === 0 || vouchers.length === 0)) {
      void loadCheckoutMeta();
    }
  }, [isAuthenticated, addresses.length, vouchers.length, loadCheckoutMeta]);

  const totalWeightGrams = Math.max(100, activeGroup?.raw.total_weight_grams || 500);
  const rawSubtotal = activeGroup?.raw.subtotal_vnd ?? 0;

  // Fetch real shipping quotes for THIS host's shop without sending shop_id.
  useEffect(() => {
    if (!isAuthenticated || !selectedAddress || !activeGroup || cartItems.length === 0) {
      setQuotes([]);
      return;
    }

    let cancelled = false;
    setQuotesLoading(true);
    api<{ quotes?: GwCarrierQuote[]; quotes_by_shop?: Record<string, GwCarrierQuote[]> }>(
      "/api/checkout/shipping-quote",
      {
        method: "POST",
        body: {
          to_province_code: selectedAddress.province_code,
          to_ward_code: selectedAddress.ward_code,
          weight_grams: totalWeightGrams,
          cod_amount_vnd: paymentMethod === "COD" ? rawSubtotal : 0,
        },
      }
    )
      .then((res) => {
        if (cancelled) return;
        const list =
          res.quotes ??
          (res.quotes_by_shop ? Object.values(res.quotes_by_shop)[0] : undefined) ??
          [];
        setQuotes(list);
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
        // Non-blocking: backend computes authoritative shipping fee during checkout
      })
      .finally(() => {
        if (!cancelled) setQuotesLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [
    isAuthenticated,
    selectedAddress,
    activeGroup,
    cartItems.length,
    totalWeightGrams,
    rawSubtotal,
    paymentMethod,
  ]);

  const selectedShippingFee = useMemo<number>(() => {
    const match =
      (selectedCarrier &&
        quotes.find(
          (q) =>
            q.carrier_code === selectedCarrier.carrier_code &&
            q.service_code === selectedCarrier.service_code
        )) ||
      quotes[0];
    return match ? match.fee_vnd : 0;
  }, [quotes, selectedCarrier]);

  const flashKey = JSON.stringify(flashSaleItemBySku);

  // Authoritative promotion preview for THIS site's shop only (flat shop-agnostic body).
  useEffect(() => {
    if (!isAuthenticated || !activeGroup || cartItems.length === 0) {
      setBreakdown(null);
      return;
    }
    let cancelled = false;
    setPreviewLoading(true);
    setPreviewError(null);

    api<{ breakdown: GwDiscountBreakdown }>("/api/vouchers/preview", {
      method: "POST",
      body: {
        shipping_fee_vnd: selectedShippingFee,
        shop_voucher_id: selectedVoucherId || "",
        flash_sale_item_by_sku: flashSaleItemBySku,
      },
    })
      .then((res) => {
        if (!cancelled) setBreakdown(res.breakdown);
      })
      .catch((err) => {
        if (!cancelled) {
          setPreviewError(
            err instanceof ApiError ? err.message : "Chưa thể cập nhật bảng tính ưu đãi lúc này."
          );
        }
      })
      .finally(() => {
        if (!cancelled) setPreviewLoading(false);
      });

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    isAuthenticated,
    activeGroup,
    cartItems.length,
    selectedShippingFee,
    selectedVoucherId,
    flashKey,
  ]);

  // Preserve server-generated Idempotency-Key across retries of the same inputs; rotate on change.
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
  const shippingFeeTotal = breakdown?.total_shipping_fee_vnd ?? selectedShippingFee;
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
      setPromoFeedback({ ok: false, message: "Vui lòng nhập mã ưu đãi cần kiểm tra." });
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
          shipping_fee_vnd: selectedShippingFee,
          shop_voucher_id: selectedVoucherId || "",
          flash_sale_item_by_sku: flashSaleItemBySku,
        },
      });
      if (!res.valid || !res.voucher) {
        setPromoFeedback({
          ok: false,
          message: res.reason || `Mã ${clean} chưa đủ điều kiện áp dụng tại ${brandName}.`,
        });
        return;
      }
      setSelectedVoucher(res.voucher.id);
      if (res.breakdown) setBreakdown(res.breakdown);
      setPromoFeedback({
        ok: true,
        message:
          res.discount_vnd > 0
            ? `Áp dụng thành công mã ${res.voucher.code} (-${formatVnd(res.discount_vnd)}).`
            : `Đã chọn mã ${res.voucher.code}.`,
      });
      setPromoCodeInput("");
    } catch (err) {
      setPromoFeedback({
        ok: false,
        message: err instanceof ApiError ? err.message : "Không thể kiểm tra mã ưu đãi lúc này.",
      });
    } finally {
      setPromoValidating(false);
    }
  };

  const handlePlaceOrder = async () => {
    if (isSubmitting) return;
    if (!selectedAddress) {
      setFormError("Vui lòng chọn hoặc thêm địa chỉ nhận hàng trước khi đặt hàng.");
      return;
    }
    if (!activeGroup || cartItems.length === 0) {
      setFormError("Giỏ hàng đang trống. Vui lòng chọn sản phẩm trước khi đặt hàng.");
      return;
    }
    if (hasStockIssue) {
      setFormError(
        "Có sản phẩm hết hàng hoặc vượt số lượng tồn kho. Vui lòng kiểm tra lại giỏ hàng."
      );
      return;
    }
    if (paymentMethod === "COD" && codOverLimit) {
      setFormError(
        "Tổng đơn vượt hạn mức 5.000.000₫ cho thanh toán COD. Vui lòng chọn chuyển khoản VietQR."
      );
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

    // Strictly shop-agnostic body: server derives shop & STOREFRONT channel from Host.
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
        showToast("Đã tạo đơn hàng. Mời bạn quét mã VietQR để hoàn tất thanh toán.");
        const q = firstOrderId ? `?order=${encodeURIComponent(firstOrderId)}` : "";
        router.push(`/thanh-toan/cho/${encodeURIComponent(res.payment_id)}${q}`);
        return;
      }

      showToast("Đặt hàng thành công!");
      router.push(firstOrderId ? `/don-hang/${encodeURIComponent(firstOrderId)}` : "/don-hang");
    } catch (err) {
      setIsSubmitting(false);
      if (err instanceof ApiError) {
        if (err.status === 401) {
          setSessionExpired(true);
          setFormError("Phiên đăng nhập đã hết hạn. Vui lòng đăng nhập lại để hoàn tất đặt hàng.");
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
        setFormError(
          "Hệ thống đang tạm gián đoạn. Bạn có thể bấm thử lại an toàn, đơn hàng sẽ không bị tạo trùng."
        );
      }
    }
  };

  const renderHeader = (subtitle: string) => (
    <section className={shared.pageHeader}>
      <div className={shared.headerLeadGroup}>
        <h1 className={`${shared.pageTitle} lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Thanh toán · {brandName}
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

  if (sessionLoading || (isAuthenticated && cartLoading && !cart)) {
    return (
      <div className={shared.page}>
        {renderHeader("Đang cập nhật tồn kho, địa chỉ giao hàng và cước vận chuyển.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Chuẩn bị đơn thanh toán</h2>
          <StateView
            kind="loading"
            title="Đang tải địa chỉ giao hàng và phương thức thanh toán..."
            rows={4}
          />
        </section>
        {addonProducts.length > 0 && (
          <ProductAddonStrip title={`Sản phẩm của ${brandName}`} products={addonProducts} />
        )}
      </div>
    );
  }

  if (!isAuthenticated) {
    return (
      <div className={shared.page}>
        {renderHeader("Đăng nhập tài khoản để chọn địa chỉ nhận hàng và xác nhận đơn.")}
        <section
          className={shared.section}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <h2 className={shared.sectionTitle}>Đăng nhập để thanh toán</h2>
          <StateView
            kind="empty"
            title="Vui lòng đăng nhập để đặt hàng"
            message={`Đăng nhập tài khoản tại ${brandName} để sử dụng sổ địa chỉ, áp mã ưu đãi và theo dõi đơn hàng.`}
            actionLabel="Đăng nhập ngay"
            actionHref="/dang-nhap?next=%2Fthanh-toan"
          />
          <div className={shared.formActions} style={{ marginTop: 16 }}>
            <Button variant="secondary" size="md" href="/dang-ky?next=%2Fthanh-toan">
              Tạo tài khoản mới
            </Button>
          </div>
        </section>
        {addonProducts.length > 0 && (
          <ProductAddonStrip title={`Sản phẩm nổi bật của ${brandName}`} products={addonProducts} />
        )}
      </div>
    );
  }

  if (cartError && !cart) {
    return (
      <div className={shared.page}>
        {renderHeader("Không thể kết nối trang thanh toán lúc này. Chưa có khoản tiền nào bị trừ.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái thanh toán</h2>
          <StateView
            kind="error"
            title="Thanh toán tạm gián đoạn"
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
        {renderHeader("Chưa có sản phẩm nào trong giỏ hàng để tiến hành thanh toán.")}
        <section
          className={shared.section}
          data-rise=""
          style={{ "--i": 3 } as React.CSSProperties}
        >
          <h2 className={shared.sectionTitle}>Danh sách chờ thanh toán</h2>
          <StateView
            kind="empty"
            title="Chưa có sản phẩm cần thanh toán"
            message={`Quay lại giỏ hàng hoặc danh mục sản phẩm của ${brandName} để chọn đồ trước khi đặt đơn.`}
            actionLabel="Xem sản phẩm cửa hàng"
            actionHref="/san-pham"
          />
        </section>
        {addonProducts.length > 0 && (
          <ProductAddonStrip title={`Sản phẩm từ ${brandName}`} products={addonProducts} />
        )}
      </div>
    );
  }

  const payLabel = paymentMethod === "COD" ? "COD đồng kiểm" : "VietQR";
  const ctaLabel = isSubmitting
    ? "Đang tạo đơn hàng..."
    : `Đặt hàng · ${formatVnd(finalPayable)} (${payLabel})`;

  return (
    <div className={`${shared.page} ${styles.mobileSpacer}`}>
      {renderHeader("Chọn địa chỉ giao hàng, đơn vị vận chuyển và phương thức thanh toán.")}

      <section className={shared.section}>
        <div className={styles.checkoutGrid}>
          <div className={styles.checkoutSheet}>
            {/* Step 1: Real Saved Addresses */}
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

              {addressesLoading && addresses.length === 0 ? (
                <StateView kind="loading" title="Đang tải sổ địa chỉ..." rows={2} />
              ) : addresses.length > 0 ? (
                <div
                  className={styles.addressList}
                  role="radiogroup"
                  aria-label="Chọn địa chỉ nhận hàng"
                >
                  {addresses.map((addr, idx) => {
                    const isSelected = selectedAddress?.id === addr.id;
                    return (
                      <button
                        key={addr.id}
                        type="button"
                        role="radio"
                        aria-checked={isSelected}
                        data-rise=""
                        style={{ "--i": Math.min(idx + 2, 5) } as React.CSSProperties}
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
                          {formatAddressLine(
                            addr.street_address,
                            addr.ward_name,
                            addr.province_name
                          )}
                        </p>
                      </button>
                    );
                  })}
                </div>
              ) : (
                <p className={styles.addressShipLine}>
                  Bạn chưa có địa chỉ giao hàng nào. Hãy thêm địa chỉ bên dưới để tiếp tục đặt hàng.
                </p>
              )}

              {showNewAddressForm && (
                <AddressForm
                  defaultName={user?.fullName ?? ""}
                  defaultPhone={user?.phone ?? ""}
                  submitLabel="Lưu và giao tới địa chỉ này"
                  onSaved={(created) => {
                    setAddresses((prev) => [
                      created,
                      ...prev.map((a) => (created.is_default ? { ...a, is_default: false } : a)),
                    ]);
                    setSelectedAddressId(created.id);
                    setShowNewAddressForm(false);
                    setFormError(null);
                  }}
                  onCancel={addresses.length > 0 ? () => setShowNewAddressForm(false) : undefined}
                />
              )}
            </div>

            {/* Step 2: Shipping Quotes for this shop */}
            <div
              className={styles.stepBlock}
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              <h2 className={styles.blockTitle}>Vận chuyển</h2>
              <div className={styles.shippingStack}>
                {quotesLoading && quotes.length === 0 ? (
                  <p className={styles.addressShipLine}>
                    Đang tính cước vận chuyển thực tế theo địa chỉ nhận hàng...
                  </p>
                ) : (
                  <div className={styles.shopDeliveryRow}>
                    <div className={styles.shippingRow}>
                      <div className={styles.shippingInfo}>
                        <IconTruck size={17} className={styles.shippingIcon} />
                        <div>
                          <p className={styles.addressName}>Giao từ {brandName}</p>
                          <p className={styles.addressShipLine}>
                            {selectedAddress
                              ? `Giao tới ${selectedAddress.ward_name}, ${selectedAddress.province_name}`
                              : "Chọn địa chỉ nhận hàng để xem phí vận chuyển"}
                          </p>
                        </div>
                      </div>
                      <span className={`${shared.statusTag} ${shared.toneNeutral} num`}>
                        {selectedShippingFee > 0 ? formatVnd(selectedShippingFee) : "Đang cập nhật"}
                      </span>
                    </div>

                    {quotes.length > 0 && (
                      <div
                        className={styles.deliveryOptions}
                        style={{ marginTop: 12 }}
                        role="radiogroup"
                        aria-label={`Chọn đơn vị vận chuyển cho ${brandName}`}
                      >
                        {quotes.map((q) => {
                          const active =
                            selectedCarrier?.carrier_code === q.carrier_code &&
                            selectedCarrier?.service_code === q.service_code;
                          return (
                            <button
                              key={`${q.carrier_code}-${q.service_code}`}
                              type="button"
                              role="radio"
                              aria-checked={active}
                              className={`${styles.deliveryOptionBtn} ${
                                active ? styles.deliveryOptionActive : ""
                              }`}
                              onClick={() =>
                                setSelectedCarrier({
                                  carrier_code: q.carrier_code,
                                  service_code: q.service_code,
                                })
                              }
                            >
                              <span className={styles.paymentTitle}>
                                {formatCarrierLabel(q.carrier_code, q.carrier_name)} ·{" "}
                                <span className="num">{formatVnd(q.fee_vnd)}</span>
                              </span>
                              <span className={styles.addressShipLine}>
                                {formatServiceLabel(q.service_code, q.service_name)} · Dự kiến{" "}
                                {q.estimated_days} ngày
                              </span>
                            </button>
                          );
                        })}
                      </div>
                    )}
                  </div>
                )}
              </div>
            </div>

            {/* Step 3: Payment methods (COD & VietQR) */}
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
                  aria-checked={paymentMethod === "COD"}
                  data-reveal=""
                  className={`${styles.paymentOption} ${
                    paymentMethod === "COD" ? styles.paymentOptionSelected : ""
                  }`}
                  onClick={() => setPaymentMethod("COD")}
                >
                  <div className={styles.paymentHead}>
                    <span className={styles.paymentTitle}>Thanh toán khi nhận hàng (COD)</span>
                    {paymentMethod === "COD" && (
                      <span className={`${shared.statusTag} ${shared.tonePos}`}>Đang chọn</span>
                    )}
                  </div>
                  <p className={styles.paymentDesc}>
                    Thanh toán trực tiếp cho bưu tá sau khi mở kiểm tra đúng mẫu và đúng size. Áp
                    dụng cho đơn hàng tối đa 5.000.000₫.
                  </p>
                  {codOverLimit && (
                    <p className={shared.fieldError}>
                      Đơn hiện vượt 5.000.000₫. Vui lòng chọn chuyển khoản VietQR.
                    </p>
                  )}
                </button>

                <button
                  type="button"
                  role="radio"
                  aria-checked={paymentMethod === "VIETQR"}
                  data-reveal=""
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
                    Sau khi bấm Đặt hàng, hệ thống tạo mã VietQR riêng cho đơn của bạn để quét bằng
                    ứng dụng ngân hàng và xác nhận tự động, không cần nhập số thẻ.
                  </p>
                </button>
              </div>
            </div>
          </div>

          <aside
            className={styles.reviewCol}
            aria-label="Chi tiết thanh toán"
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <div className={styles.reviewInner}>
              <h2 className={styles.blockTitle}>Chi tiết đơn thanh toán</h2>

              <div className={styles.reviewItemsGroup}>
                <div className={styles.shopSlice} data-reveal="">
                  <div className={styles.shopSliceHead}>
                    <span>{brandName}</span>
                    <span className="num">{formatVnd(rawSubtotal)}</span>
                  </div>
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
                            {it.variantLabel} · SL <span className="num">{it.raw.quantity}</span>
                          </span>
                        </div>
                        <span className={`${styles.miniPrice} num`}>
                          {formatVnd(it.raw.price_vnd * it.raw.quantity)}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
              </div>

              {/* Shop voucher selector & code entry */}
              <form className={styles.promoSection} onSubmit={handleApplyCode} noValidate>
                <label htmlFor="co-code-input" className={shared.fieldLabel}>
                  Mã giảm giá của {brandName}
                </label>
                <div className={shared.codeInputRow}>
                  <input
                    id="co-code-input"
                    type="text"
                    className={shared.input}
                    value={promoCodeInput}
                    onChange={(e) => setPromoCodeInput(e.target.value)}
                    placeholder="Nhập mã giảm giá của cửa hàng"
                  />
                  <Button type="submit" variant="ink" size="md" disabled={promoValidating}>
                    {promoValidating ? "Đang kiểm..." : "Áp dụng"}
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

                {shopVouchers.length > 0 && (
                  <div className={styles.freeshipRow}>
                    {shopVouchers.map((v) => {
                      const reason = formatVoucherIneligibleReason(v, subtotal);
                      const eligible = reason === null;
                      const active =
                        (selectedVoucherId === v.id || selectedVoucherId === v.code) && eligible;
                      return (
                        <button
                          key={v.id}
                          type="button"
                          disabled={!eligible}
                          className={`${styles.freeshipBtn} ${active ? styles.freeshipBtnActive : ""}`}
                          onClick={() => setSelectedVoucher(active ? null : v.id)}
                          title={
                            eligible
                              ? `Còn ${formatInt(v.remaining_quota)} lượt`
                              : (reason ?? undefined)
                          }
                        >
                          {v.code} ({formatVoucherHeadline(v)})
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
                    Tạm tính (<span className="num">{totalQty}</span> sản phẩm)
                  </dt>
                  <dd className="num">{formatVnd(subtotal)}</dd>
                </div>
                {flashDiscount > 0 && (
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Ưu đãi Flash sale</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {formatSignedVnd(-flashDiscount)}
                    </dd>
                  </div>
                )}
                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Mã giảm giá cửa hàng</dt>
                  <dd className={`${styles.ledgerDiscount} num`}>
                    {shopVoucherTotal > 0 ? formatSignedVnd(-shopVoucherTotal) : "0₫"}
                  </dd>
                </div>
                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Phí vận chuyển</dt>
                  <dd className="num">{formatVnd(shippingFeeTotal)}</dd>
                </div>
              </dl>
            </div>

            <div className={styles.ctaBand}>
              <div className={styles.grandBand}>
                <span className={styles.grandLabel}>
                  {previewLoading ? "Đang cập nhật..." : "Tổng thanh toán"}
                </span>
                <span className={`${styles.grandVal} num`}>{formatVnd(finalPayable)}</span>
              </div>

              <div className={styles.assuranceBox}>
                <IconShield size={17} className={styles.assuranceIcon} />
                <span>
                  Xác nhận đơn an toàn. Sản phẩm đúng màu và size được giữ ngay khi bấm đặt hàng.
                </span>
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
                disabled={
                  isSubmitting ||
                  hasStockIssue ||
                  !selectedAddress ||
                  (paymentMethod === "COD" && codOverLimit)
                }
                onClick={handlePlaceOrder}
              >
                {ctaLabel}
              </Button>
            </div>
          </aside>
        </div>
      </section>

      {addonProducts.length > 0 && (
        <ProductAddonStrip
          title={`Sản phẩm khác từ ${brandName}`}
          subtitle="Thêm sản phẩm giao cùng đơn từ cửa hàng"
          products={addonProducts}
        />
      )}

      <div className={styles.mobileCheckoutBar} aria-label="Xác nhận đặt hàng nhanh">
        <div>
          <p className={styles.miniMeta}>{payLabel}</p>
          <p className={`${styles.addressName} num`}>{formatVnd(finalPayable)}</p>
        </div>
        <Button
          variant="primary"
          size="md"
          disabled={
            isSubmitting ||
            hasStockIssue ||
            !selectedAddress ||
            (paymentMethod === "COD" && codOverLimit)
          }
          onClick={handlePlaceOrder}
        >
          {isSubmitting ? "Đang tạo..." : `Đặt · ${formatVnd(finalPayable)}`}
        </Button>
      </div>
    </div>
  );
}
