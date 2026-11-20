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
  initialIdempotencyKey: string;
  catalogProducts: Product[];
  catalogShops: Shop[];
  initialAddresses: GwAddress[];
  initialVouchers: GwVoucher[];
  initialCoinBalance: number;
  initialError: string | null;
}

function generateClientAttemptKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `chk-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

export function CheckoutClient({
  initialIdempotencyKey,
  catalogProducts,
  catalogShops,
  initialAddresses,
  initialVouchers,
  initialCoinBalance,
  initialError,
}: CheckoutClientProps) {
  const router = useRouter();
  const { user, isAuthenticated, loading: sessionLoading } = useSession();
  const {
    cart,
    cartItems,
    cartLoading,
    cartError,
    refreshCart,
    selectedShopVouchers,
    platformVoucherId,
    freeshipVoucherId,
    useCoins,
    flashSaleItemBySku,
    setShopVoucher,
    setPlatformVoucher,
    setFreeshipVoucher,
    setUseCoins,
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
  const [coinBalance, setCoinBalance] = useState<number>(initialCoinBalance);
  const [quotesByShop, setQuotesByShop] = useState<Record<string, GwCarrierQuote[]>>({});
  const [selectedCarrierByShop, setSelectedCarrierByShop] = useState<
    Record<string, { carrier_code: string; service_code: string }>
  >({});
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

  // Per-attempt server-generated idempotency key: preserved on retry of the same order attempt,
  // refreshed only when the user meaningfully changes order inputs after a key was created.
  const [idempotencyKey, setIdempotencyKey] = useState<string>(initialIdempotencyKey);
  const lastSignatureRef = useRef<string | null>(null);

  const enrichedGroups = useMemo(
    () => enrichCartGroups(cart?.shop_groups ?? [], catalogProducts, catalogShops),
    [cart?.shop_groups, catalogProducts, catalogShops]
  );

  const selectedAddress = useMemo(
    () => addresses.find((a) => a.id === selectedAddressId) ?? addresses[0],
    [addresses, selectedAddressId]
  );

  const addonProducts = useMemo(() => {
    const inCart = new Set(cartItems.map((c) => c.product_id));
    const filtered = catalogProducts.filter(
      (p) => !inCart.has(p.backendId ?? p.id) && !inCart.has(p.id)
    );
    return (filtered.length >= 4 ? filtered : catalogProducts).slice(0, 4);
  }, [cartItems, catalogProducts]);

  useRevealOnScroll(
    `checkout-${isAuthenticated ? "auth" : "guest"}-${enrichedGroups.length}-${paymentMethod}-${addresses.length}`
  );

  // Refresh addresses & coins client-side if server didn't have them (e.g. client navigation after login)
  const loadCheckoutMeta = useCallback(async () => {
    if (!isAuthenticated) return;
    setAddressesLoading(true);
    try {
      const [addrRes, vRes, cRes] = await Promise.allSettled([
        api<{ addresses: GwAddress[] }>("/api/checkout/addresses"),
        api<{ vouchers: GwVoucher[] }>("/api/vouchers"),
        api<{ balance: number }>("/api/vouchers/coins"),
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
        setVouchers(vRes.value.vouchers);
      }
      if (cRes.status === "fulfilled" && typeof cRes.value.balance === "number") {
        setCoinBalance(Math.max(0, cRes.value.balance));
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

  // Fetch real shipping quotes per shop whenever address or cart shop groups change
  const cartShopWeightSig = useMemo(
    () =>
      (cart?.shop_groups ?? [])
        .map((g) => `${g.shop_id}:${g.total_weight_grams}:${g.subtotal_vnd}`)
        .join("|"),
    [cart?.shop_groups]
  );

  useEffect(() => {
    if (!isAuthenticated || !selectedAddress || !cart || cart.shop_groups.length === 0) {
      setQuotesByShop({});
      return;
    }

    let cancelled = false;
    setQuotesLoading(true);
    api<{ quotes_by_shop: Record<string, GwCarrierQuote[]> }>("/api/checkout/shipping-quote", {
      method: "POST",
      body: {
        to_province_code: selectedAddress.province_code,
        to_ward_code: selectedAddress.ward_code,
        shops: cart.shop_groups.map((g) => ({
          shop_id: g.shop_id,
          weight_grams: Math.max(100, g.total_weight_grams || 500),
          cod_amount_vnd: paymentMethod === "COD" ? g.subtotal_vnd : 0,
        })),
      },
    })
      .then((res) => {
        if (cancelled) return;
        const map = res.quotes_by_shop ?? {};
        setQuotesByShop(map);
        setSelectedCarrierByShop((prev) => {
          const next: Record<string, { carrier_code: string; service_code: string }> = {};
          for (const g of cart.shop_groups) {
            const list = map[g.shop_id] ?? [];
            const cur = prev[g.shop_id];
            const stillValid =
              cur &&
              list.some(
                (q) => q.carrier_code === cur.carrier_code && q.service_code === cur.service_code
              );
            if (stillValid && cur) {
              next[g.shop_id] = cur;
            } else if (list[0]) {
              next[g.shop_id] = {
                carrier_code: list[0].carrier_code,
                service_code: list[0].service_code,
              };
            }
          }
          return next;
        });
      })
      .catch(() => {
        // Non-blocking: backend computes authoritative shipping fee during checkout even if quote call fails
      })
      .finally(() => {
        if (!cancelled) setQuotesLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [isAuthenticated, selectedAddress, cart, cartShopWeightSig, paymentMethod]);

  const shippingFeesByShop = useMemo<Record<string, number>>(() => {
    const out: Record<string, number> = {};
    for (const g of cart?.shop_groups ?? []) {
      const list = quotesByShop[g.shop_id] ?? [];
      const chosen = selectedCarrierByShop[g.shop_id];
      const match =
        (chosen &&
          list.find(
            (q) => q.carrier_code === chosen.carrier_code && q.service_code === chosen.service_code
          )) ||
        list[0];
      out[g.shop_id] = match ? match.fee_vnd : 0;
    }
    return out;
  }, [cart?.shop_groups, quotesByShop, selectedCarrierByShop]);

  const shippingFeesKey = JSON.stringify(shippingFeesByShop);
  const shopVouchersKey = JSON.stringify(selectedShopVouchers);
  const flashKey = JSON.stringify(flashSaleItemBySku);

  // Authoritative promotion preview with real per-shop shipping fees
  useEffect(() => {
    if (!isAuthenticated || !cart || cart.shop_groups.length === 0) {
      setBreakdown(null);
      return;
    }
    let cancelled = false;
    setPreviewLoading(true);
    setPreviewError(null);

    api<{ breakdown: GwDiscountBreakdown }>("/api/vouchers/preview", {
      method: "POST",
      body: {
        shipping_fees: shippingFeesByShop,
        shop_voucher_ids: selectedShopVouchers,
        platform_voucher_id: platformVoucherId || "",
        freeship_voucher_id: freeshipVoucherId || "",
        coins_to_use: useCoins ? coinBalance : 0,
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
    cart,
    shippingFeesKey,
    shopVouchersKey,
    platformVoucherId,
    freeshipVoucherId,
    useCoins,
    coinBalance,
    flashKey,
  ]);

  // Keep the same server-generated idempotency key for retries of identical inputs;
  // rotate only when inputs change after the initial render.
  const orderAttemptSignature = useMemo(
    () =>
      JSON.stringify({
        addressId: selectedAddress?.id ?? "",
        paymentMethod,
        cart: cartItems.map((it) => `${it.sku_id}:${it.quantity}`),
        shopVouchers: selectedShopVouchers,
        platformVoucherId,
        freeshipVoucherId,
        useCoins,
        carriers: selectedCarrierByShop,
      }),
    [
      selectedAddress?.id,
      paymentMethod,
      cartItems,
      selectedShopVouchers,
      platformVoucherId,
      freeshipVoucherId,
      useCoins,
      selectedCarrierByShop,
    ]
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

  const freeshipVouchers = useMemo(
    () => vouchers.filter((v) => v.scope.toUpperCase() === "FREESHIP" && v.is_active),
    [vouchers]
  );

  const subtotal = breakdown?.total_subtotal_vnd ?? cart?.total_vnd ?? 0;
  const flashDiscount = breakdown?.total_flash_sale_discount_vnd ?? 0;
  const shopVoucherTotal = breakdown?.total_shop_voucher_discount_vnd ?? 0;
  const platformVoucherDiscount = breakdown?.total_platform_voucher_discount_vnd ?? 0;
  const shippingFeeTotal =
    breakdown?.total_shipping_fee_vnd ??
    Object.values(shippingFeesByShop).reduce((sum, fee) => sum + fee, 0);
  const freeshipDiscount = breakdown?.total_freeship_discount_vnd ?? 0;
  const coinsDiscount = breakdown?.total_coins_discount_vnd ?? 0;
  const finalPayable = breakdown?.final_payable_vnd ?? subtotal + shippingFeeTotal;
  const totalQty = cart?.total_items ?? cartItems.reduce((s, it) => s + it.quantity, 0);
  const codOverLimit = finalPayable > COD_MAX_VND;
  const hasStockIssue = useMemo(
    () => enrichedGroups.some((g) => g.items.some((it) => it.isOutOfStock || it.exceedsStock)),
    [enrichedGroups]
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
        scope?: string;
        discount_vnd: number;
        reason?: string;
        breakdown?: GwDiscountBreakdown;
      }>("/api/vouchers/validate", {
        method: "POST",
        body: {
          code: clean,
          shipping_fees: shippingFeesByShop,
          shop_voucher_ids: selectedShopVouchers,
          platform_voucher_id: platformVoucherId || "",
          freeship_voucher_id: freeshipVoucherId || "",
          coins_to_use: useCoins ? coinBalance : 0,
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
      const scope = (res.scope || res.voucher.scope).toUpperCase();
      if (scope === "SHOP" && res.voucher.shop_id) {
        setShopVoucher(res.voucher.shop_id, res.voucher.id);
      } else if (scope === "FREESHIP") {
        setFreeshipVoucher(res.voucher.id);
      } else {
        setPlatformVoucher(res.voucher.id);
      }
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
    if (enrichedGroups.length === 0) {
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
      .filter((it) => Boolean(flashSaleItemBySku[it.sku_id]))
      .map((it) => ({
        sku_id: it.sku_id,
        quantity: it.quantity,
        flash_sale_item_id: flashSaleItemBySku[it.sku_id],
      }));

    const shippingChoices = Object.entries(selectedCarrierByShop).map(([shopId, choice]) => ({
      shop_id: shopId,
      carrier_code: choice.carrier_code,
      service_code: choice.service_code,
    }));

    try {
      const res = await api<GwCheckoutResponse>("/api/checkout", {
        method: "POST",
        headers: { "Idempotency-Key": idempotencyKey },
        body: {
          address_id: selectedAddress.id,
          from_cart: true,
          items: flashItems,
          shop_voucher_ids: selectedShopVouchers,
          platform_voucher_id: platformVoucherId || "",
          freeship_voucher_id: freeshipVoucherId || "",
          coins_to_use: useCoins ? coinBalance : 0,
          payment_method: paymentMethod,
          shipping_choices: shippingChoices,
        },
      });

      resetVoucherSelection();
      void refreshCart();

      const firstOrderId = res.orders?.[0]?.id ?? "";
      const orderCount = res.orders?.length ?? 0;

      if (
        res.payment_method.toUpperCase() === "VIETQR" &&
        res.payment_id &&
        res.payment_status.toUpperCase() !== "SUCCEEDED"
      ) {
        showToast(
          orderCount > 1
            ? `Đã tạo ${orderCount} đơn hàng. Mời bạn quét mã VietQR để hoàn tất thanh toán.`
            : "Đã tạo đơn hàng. Mời bạn quét mã VietQR để hoàn tất thanh toán."
        );
        const q = firstOrderId ? `?order=${encodeURIComponent(firstOrderId)}` : "";
        router.push(`/thanh-toan/cho/${encodeURIComponent(res.payment_id)}${q}`);
        return;
      }

      showToast(
        orderCount > 1
          ? `Đặt thành công ${orderCount} đơn hàng từ ${orderCount} gian hàng.`
          : "Đặt hàng thành công!"
      );
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
        <ProductAddonStrip title="Đồ cơ bản thường mua kèm" products={addonProducts} />
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
            message="Đăng nhập tài khoản NexusMart để sử dụng sổ địa chỉ, mã ưu đãi và theo dõi thanh toán an toàn."
            actionLabel="Đăng nhập ngay"
            actionHref="/dang-nhap?next=%2Fthanh-toan"
          />
          <div className={shared.formActions} style={{ marginTop: 16 }}>
            <Button variant="secondary" size="md" href="/dang-ky">
              Đăng ký tài khoản mới
            </Button>
          </div>
        </section>
        <ProductAddonStrip title="Đồ cơ bản bán chạy" products={addonProducts} />
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
        <ProductAddonStrip title="Đồ cơ bản thường mua kèm" products={addonProducts} />
      </div>
    );
  }

  if (enrichedGroups.length === 0) {
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
            message="Quay lại giỏ hàng để kiểm tra hoặc khám phá danh mục đồ cơ bản của NexusMart."
            actionLabel="Quay lại giỏ hàng"
            actionHref="/gio-hang"
          />
        </section>
        <ProductAddonStrip title="Thêm nhanh đồ cơ bản để đặt hàng ngay" products={addonProducts} />
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
          {/* Left column: ONE clean white sheet with 3 clear steps */}
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
                          {addr.street_address}, {addr.ward_name}, {addr.province_name}
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

            {/* Step 2: Shipping Quotes per Shop */}
            <div
              className={styles.stepBlock}
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              <h2 className={styles.blockTitle}>Vận chuyển theo gian hàng</h2>
              <div className={styles.shippingStack}>
                {quotesLoading && Object.keys(quotesByShop).length === 0 ? (
                  <p className={styles.addressShipLine}>
                    Đang tính cước vận chuyển thực tế theo địa chỉ nhận hàng...
                  </p>
                ) : (
                  <div className={styles.shopDeliveryList}>
                    {enrichedGroups.map((group) => {
                      const shopId = group.raw.shop_id;
                      const quotes = quotesByShop[shopId] ?? [];
                      const chosen = selectedCarrierByShop[shopId];

                      return (
                        <div key={shopId} className={styles.shopDeliveryRow} data-reveal="">
                          <div className={styles.shippingRow}>
                            <div className={styles.shippingInfo}>
                              <IconTruck size={17} className={styles.shippingIcon} />
                              <div>
                                <p className={styles.addressName}>{group.shopName}</p>
                                <p className={styles.addressShipLine}>
                                  {selectedAddress
                                    ? `Giao tới ${selectedAddress.ward_name}, ${selectedAddress.province_name}`
                                    : "Chọn địa chỉ nhận hàng để xem phí vận chuyển"}
                                </p>
                              </div>
                            </div>
                            <span className={`${shared.statusTag} ${shared.toneNeutral} num`}>
                              {shippingFeesByShop[shopId]
                                ? formatVnd(shippingFeesByShop[shopId])
                                : "Đang cập nhật"}
                            </span>
                          </div>

                          {quotes.length > 0 && (
                            <div
                              className={styles.deliveryOptions}
                              style={{ marginTop: 12 }}
                              role="radiogroup"
                              aria-label={`Chọn đơn vị vận chuyển cho ${group.shopName}`}
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
                                    className={`${styles.deliveryOptionBtn} ${
                                      active ? styles.deliveryOptionActive : ""
                                    }`}
                                    onClick={() =>
                                      setSelectedCarrierByShop((prev) => ({
                                        ...prev,
                                        [shopId]: {
                                          carrier_code: q.carrier_code,
                                          service_code: q.service_code,
                                        },
                                      }))
                                    }
                                  >
                                    <span className={styles.paymentTitle}>
                                      {q.carrier_name || q.carrier_code} ·{" "}
                                      <span className="num">{formatVnd(q.fee_vnd)}</span>
                                    </span>
                                    <span className={styles.addressShipLine}>
                                      {q.service_name || q.service_code} · Dự kiến{" "}
                                      {q.estimated_days} ngày
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

                {freeshipVouchers.length > 0 && (
                  <div className={styles.freeshipRow}>
                    {freeshipVouchers.map((v) => {
                      const reason = formatVoucherIneligibleReason(v, subtotal);
                      const eligible = reason === null;
                      const active =
                        (freeshipVoucherId === v.id || freeshipVoucherId === v.code) && eligible;
                      return (
                        <button
                          key={v.id}
                          type="button"
                          disabled={!eligible}
                          className={`${styles.freeshipBtn} ${active ? styles.freeshipBtnActive : ""}`}
                          onClick={() => setFreeshipVoucher(active ? null : v.id)}
                        >
                          {v.code} ({formatVoucherHeadline(v)})
                        </button>
                      );
                    })}
                  </div>
                )}
              </div>
            </div>

            {/* Step 3: Payment methods (COD & VietQR — real backend methods, no card form) */}
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

          {/* Right column: ONE clean white review sheet + bold dark CTA band */}
          <aside
            className={styles.reviewCol}
            aria-label="Chi tiết thanh toán"
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <div className={styles.reviewInner}>
              <h2 className={styles.blockTitle}>Chi tiết đơn thanh toán</h2>

              <div className={styles.reviewItemsGroup}>
                {enrichedGroups.map((group) => {
                  const shopBreakdown = breakdown?.shops.find(
                    (s) => s.shop_id === group.raw.shop_id
                  );
                  return (
                    <div key={group.raw.shop_id} className={styles.shopSlice} data-reveal="">
                      <div className={styles.shopSliceHead}>
                        <span>{group.shopName}</span>
                        <span className="num">{formatVnd(group.raw.subtotal_vnd)}</span>
                      </div>
                      <div className={styles.shopSliceItems}>
                        {group.items.map((it) => (
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
                                {it.variantLabel} · SL{" "}
                                <span className="num">{it.raw.quantity}</span>
                              </span>
                            </div>
                            <span className={`${styles.miniPrice} num`}>
                              {formatVnd(it.raw.price_vnd * it.raw.quantity)}
                            </span>
                          </div>
                        ))}
                      </div>
                      {(shopBreakdown?.shop_voucher_discount_vnd ?? 0) > 0 && (
                        <div className={styles.ledgerRow}>
                          <span className={styles.ledgerLabel}>Voucher gian hàng</span>
                          <span className={`${styles.ledgerDiscount} num`}>
                            {formatSignedVnd(-(shopBreakdown?.shop_voucher_discount_vnd ?? 0))}
                          </span>
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>

              {/* Inline voucher code & coins */}
              <form className={styles.promoSection} onSubmit={handleApplyCode} noValidate>
                <label htmlFor="co-code-input" className={shared.fieldLabel}>
                  Áp thêm mã ưu đãi
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
                  <dt className={styles.ledgerLabel}>Voucher gian hàng</dt>
                  <dd className={`${styles.ledgerDiscount} num`}>
                    {shopVoucherTotal > 0 ? formatSignedVnd(-shopVoucherTotal) : "0₫"}
                  </dd>
                </div>
                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Voucher toàn sàn</dt>
                  <dd className={`${styles.ledgerDiscount} num`}>
                    {platformVoucherDiscount > 0 ? formatSignedVnd(-platformVoucherDiscount) : "0₫"}
                  </dd>
                </div>
                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Phí vận chuyển</dt>
                  <dd className="num">{formatVnd(shippingFeeTotal)}</dd>
                </div>
                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Giảm phí vận chuyển</dt>
                  <dd className={`${styles.ledgerDiscount} num`}>
                    {freeshipDiscount > 0 ? formatSignedVnd(-freeshipDiscount) : "0₫"}
                  </dd>
                </div>
                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Trừ Xu NexusMart</dt>
                  <dd className={`${styles.ledgerDiscount} num`}>
                    {coinsDiscount > 0 ? formatSignedVnd(-coinsDiscount) : "0₫"}
                  </dd>
                </div>
              </dl>
            </div>

            {/* Bold dark total & primary CTA anchor */}
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

      <ProductAddonStrip
        title="Mua kèm nhanh trước khi đặt hàng"
        subtitle="Tất cotton, đồ lót sợi tre và áo thun cơ bản giao chung đơn"
        products={addonProducts}
      />

      {/* Sticky mobile place-order bar */}
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
