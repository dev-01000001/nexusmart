"use client";

import React, { Suspense, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import {
  DEMO_USER,
  PAYMENT_METHOD_LABELS,
  PRODUCTS,
  VOUCHERS,
  WALLET_PAYMENT_METHOD,
  getProductById,
  getShopById,
  validateVoucher,
} from "@/data";
import type { PaymentMethod } from "@/data";
import { PROVINCES, REGION_LABELS, getProvinceByCode } from "@/lib/provinces";
import { normalizeVietnamese } from "@/lib/search";
import { estimateDelivery } from "@/lib/sizing";
import { formatInt, formatSignedVnd, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { Button, IconGift, IconShield, IconTruck, StateView } from "@/components";
import { AddressForm } from "@/features/commerce/AddressForm";
import { VietQrPreview } from "@/features/commerce/VietQrPreview";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./page.module.css";

const NEXUSPAY_WALLET_BALANCE = 2500000;

type GatewaySelection = "cod" | "vietqr" | "nexuspay" | "linked_wallet";

const GATEWAY_SHORT: Record<GatewaySelection, { label: string; method: PaymentMethod }> = {
  cod: { label: "COD đồng kiểm", method: "cod" },
  vietqr: { label: "VietQR", method: "vietqr" },
  nexuspay: { label: "Ví NexusPay", method: WALLET_PAYMENT_METHOD },
  linked_wallet: { label: "Ví điện tử liên kết", method: WALLET_PAYMENT_METHOD },
};

function CheckoutPageContent() {
  const router = useRouter();
  const viewState = usePageStateParam();
  const {
    user,
    isAuthenticated,
    loginMock,
    addresses,
    selectedAddress,
    selectAddress,
    guestCheckout,
    setGuestCheckout,
    cartItems,
    cartSummary,
    freeshipVoucherCode,
    setFreeshipVoucher,
    setPlatformVoucher,
    setShopVoucher,
    useCoins,
    setUseCoins,
    deliveryMethod,
    setDeliveryMethod,
    membership,
    membershipTier,
    applyGiftCardCode,
    placeOrder,
    showToast,
  } = useStore();

  const [showNewAddressForm, setShowNewAddressForm] = useState(false);
  const [gatewayChoice, setGatewayChoice] = useState<GatewaySelection>("vietqr");
  const [simulatePaymentPending, setSimulatePaymentPending] = useState(false);
  const [coInspectionEnabled, setCoInspectionEnabled] = useState(true);
  const [orderNote, setOrderNote] = useState("");
  const [giftWrap, setGiftWrap] = useState(false);
  const [giftCardMessage, setGiftCardMessage] = useState("");
  const [vatEnabled, setVatEnabled] = useState(false);
  const [vatTaxCode, setVatTaxCode] = useState("");
  const [vatCompanyName, setVatCompanyName] = useState("");
  const [vatCompanyAddress, setVatCompanyAddress] = useState("");
  const [vatEmail, setVatEmail] = useState(user.email);
  const [promoCodeInput, setPromoCodeInput] = useState("");
  const [promoFeedback, setPromoFeedback] = useState<{ ok: boolean; message: string } | null>(null);
  const [guestProvQuery, setGuestProvQuery] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const isGuestMode = guestCheckout.enabled || !isAuthenticated;
  const activeGateway = GATEWAY_SHORT[gatewayChoice];

  useRevealOnScroll(
    `${viewState ?? "live"}-${cartSummary.selectedCount}-${gatewayChoice}-${isGuestMode}`
  );

  const activeGroups = useMemo(
    () => cartSummary.groups.filter((g) => g.selectedSubtotal > 0),
    [cartSummary.groups]
  );

  const freeshipVouchers = useMemo(() => VOUCHERS.filter((v) => v.scope === "freeship"), []);

  const addonProducts = useMemo(() => {
    const inCart = new Set(cartItems.map((c) => c.productId));
    const basics = PRODUCTS.filter(
      (p) =>
        !inCart.has(p.id) &&
        (p.categorySlug === "tat-phu-kien" ||
          p.categorySlug === "do-lot" ||
          p.categorySlug === "ao-thun")
    );
    return (basics.length >= 4 ? basics : PRODUCTS).slice(0, 4);
  }, [cartItems]);

  const effectiveProvinceCode = isGuestMode
    ? guestCheckout.provinceCode || "HCM"
    : (selectedAddress?.provinceCode ?? "HCM");
  const activeProvince = getProvinceByCode(effectiveProvinceCode) ?? PROVINCES[0];

  const guestProvSuggestions = useMemo(() => {
    const q = normalizeVietnamese(guestProvQuery);
    if (!q) return [];
    return PROVINCES.filter((p) =>
      normalizeVietnamese(`${p.name} ${p.shortName} ${p.code}`).includes(q)
    ).slice(0, 6);
  }, [guestProvQuery]);

  const shopDeliveries = useMemo(
    () =>
      activeGroups.map((g) => ({
        group: g,
        shop: getShopById(g.shopId),
        estimate: estimateDelivery({
          shopId: g.shopId,
          provinceCode: activeProvince.code,
          orderSubtotal: g.selectedSubtotal,
          deliveryMethod,
        }),
      })),
    [activeGroups, activeProvince.code, deliveryMethod]
  );

  const anyShopSupportsFast = useMemo(
    () => shopDeliveries.some((sd) => sd.shop?.fastDeliveryEnabled),
    [shopDeliveries]
  );

  const tierDiscountTotal = cartSummary.tierDiscountTotal ?? 0;
  const giftCardDiscount = cartSummary.giftCardDiscount ?? 0;
  const afterShopAndTier = Math.max(
    0,
    cartSummary.subtotal - tierDiscountTotal - cartSummary.shopVoucherTotal
  );
  const hasOutletSelected = useMemo(
    () =>
      cartItems.some((it) => {
        if (!it.selected) return false;
        const p = getProductById(it.productId);
        return Boolean(p?.badges?.includes("outlet"));
      }),
    [cartItems]
  );

  const transferRef = useMemo(() => {
    const codeSuffix = String((cartSummary.grandTotal % 8999) + 1000);
    return `NM-261120-${codeSuffix}`;
  }, [cartSummary.grandTotal]);

  const handleApplyCode = (e: React.FormEvent) => {
    e.preventDefault();
    const clean = promoCodeInput.trim().toUpperCase();
    if (!clean) {
      setPromoFeedback({ ok: false, message: "Vui lòng nhập mã voucher hoặc thẻ quà tặng." });
      return;
    }

    const matchedGift = membership.giftCards.find((gc) => gc.code.toUpperCase() === clean);
    if (matchedGift || clean.startsWith("NEXUS-")) {
      const res = applyGiftCardCode(clean);
      setPromoFeedback(res);
      if (res.ok) setPromoCodeInput("");
      return;
    }

    const vObj = VOUCHERS.find((v) => v.code.toUpperCase() === clean);
    const matchedGroup = vObj?.shopId
      ? cartSummary.groups.find((g) => g.shopId === vObj.shopId)
      : undefined;

    const eligibleAmount =
      vObj?.scope === "shop"
        ? Math.max(0, (matchedGroup?.selectedSubtotal ?? 0) - (matchedGroup?.tierDiscount ?? 0))
        : vObj?.scope === "freeship"
          ? cartSummary.subtotal
          : afterShopAndTier;

    const check = validateVoucher(clean, {
      eligibleAmount,
      shopId:
        vObj?.scope === "shop" ? (matchedGroup?.shopId ?? activeGroups[0]?.shopId) : undefined,
      hasOutletItems: hasOutletSelected,
      eduVerified: membership.eduVerified,
      memberTier: membershipTier.id,
      capAmount: vObj?.scope === "freeship" ? cartSummary.shippingFee : undefined,
    });

    if (!check.valid || !check.voucher) {
      setPromoFeedback({
        ok: false,
        message: check.reason ?? `Mã ${clean} không đủ điều kiện áp dụng.`,
      });
      return;
    }

    if (check.voucher.scope === "shop" && check.voucher.shopId) {
      setShopVoucher(check.voucher.shopId, check.voucher.code);
    } else if (check.voucher.scope === "freeship") {
      setFreeshipVoucher(check.voucher.code);
    } else {
      setPlatformVoucher(check.voucher.code);
    }
    setPromoFeedback({
      ok: true,
      message: `Áp dụng thành công mã ${check.voucher.code} (-${formatVnd(check.discount)}).`,
    });
    setPromoCodeInput("");
  };

  const handlePlaceOrder = () => {
    if (isSubmitting) return;
    if (activeGroups.length === 0) {
      showToast("Chưa có sản phẩm nào được chọn để đặt hàng.");
      return;
    }

    if (isGuestMode) {
      const cleanName = guestCheckout.fullName.trim();
      const cleanPhone = guestCheckout.phone.replace(/\s+/g, "").trim();
      const cleanStreet = guestCheckout.streetDetail.trim();
      if (cleanName.length < 2) {
        setFormError("Vui lòng nhập họ và tên người nhận.");
        return;
      }
      if (!/^0\d{9,10}$/.test(cleanPhone)) {
        setFormError("Số điện thoại cần gồm 10 hoặc 11 chữ số, bắt đầu bằng số 0.");
        return;
      }
      if (cleanStreet.length < 5) {
        setFormError("Vui lòng nhập số nhà, tên đường cụ thể để giao hàng.");
        return;
      }
    } else if (!selectedAddress) {
      setFormError("Vui lòng chọn hoặc thêm địa chỉ nhận hàng.");
      return;
    }

    if (vatEnabled) {
      if (
        vatTaxCode.trim().length < 10 ||
        vatCompanyName.trim().length < 3 ||
        !vatEmail.includes("@")
      ) {
        setFormError(
          "Vui lòng nhập đủ Mã số thuế (tối thiểu 10 ký tự), Tên đơn vị và Email nhận hóa đơn."
        );
        return;
      }
    }

    setFormError(null);
    setIsSubmitting(true);

    const combinedNote = [
      orderNote.trim(),
      coInspectionEnabled ? "Yêu cầu mở kiểm tra mẫu và size khi nhận." : "",
      gatewayChoice === "linked_wallet" ? "Thanh toán qua ví điện tử liên kết." : "",
    ]
      .filter(Boolean)
      .join(" ");

    const created = placeOrder({
      addressId: isGuestMode ? undefined : selectedAddress?.id,
      guestAddress: isGuestMode
        ? {
            recipientName: guestCheckout.fullName.trim(),
            phone: guestCheckout.phone.trim(),
            email: guestCheckout.email.trim() || undefined,
            provinceCode: activeProvince.code,
            provinceName: activeProvince.name,
            wardName: guestCheckout.wardName || activeProvince.wards[0] || "",
            streetDetail: guestCheckout.streetDetail.trim(),
          }
        : undefined,
      paymentMethod: activeGateway.method,
      deliveryMethod,
      note: combinedNote || undefined,
      giftWrap,
      giftCardMessage: giftWrap ? giftCardMessage.trim() || undefined : undefined,
      vatInfo: vatEnabled
        ? {
            companyName: vatCompanyName.trim(),
            taxCode: vatTaxCode.trim(),
            email: vatEmail.trim(),
            companyAddress: vatCompanyAddress.trim() || undefined,
          }
        : undefined,
      simulatePaymentPending: activeGateway.method !== "cod" && simulatePaymentPending,
    });

    if (created.length > 0) {
      router.push(`/don-hang/${created[0].id}`);
    } else {
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

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        {renderHeader("Đang cập nhật hàng sẵn có và phí giao hàng theo địa chỉ của bạn.")}
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

  if (viewState === "error") {
    return (
      <div className={shared.page}>
        {renderHeader("Không thể kết nối trang thanh toán lúc này. Chưa có khoản tiền nào bị trừ.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái thanh toán</h2>
          <StateView
            kind="error"
            title="Thanh toán tạm gián đoạn"
            message="Hệ thống xác nhận đơn đang tạm gián đoạn. Vui lòng thử tải lại trang."
            actionLabel="Thử lại"
            actionHref="/thanh-toan"
          />
        </section>
        <ProductAddonStrip title="Đồ cơ bản thường mua kèm" products={addonProducts} />
      </div>
    );
  }

  if (viewState === "empty" || activeGroups.length === 0) {
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
        <ProductAddonStrip title="Thêm nhanh đồ cơ bản để đặt hàng ngay" products={addonProducts} />
      </div>
    );
  }

  const ctaLabel = isSubmitting
    ? "Đang tạo đơn hàng..."
    : `Đặt hàng · ${formatVnd(cartSummary.grandTotal)} (${activeGateway.label})`;

  return (
    <div className={`${shared.page} ${styles.mobileSpacer}`}>
      {renderHeader("Điền địa chỉ nhận hàng và chọn phương thức thanh toán để hoàn tất đơn.")}

      <section className={shared.section}>
        <div className={styles.checkoutGrid}>
          {/* Left column: ONE clean white sheet with 3 clear steps */}
          <div className={styles.checkoutSheet}>
            {/* Step 1: Address */}
            <div
              className={styles.stepBlock}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <div className={styles.blockHead}>
                <h2 className={styles.blockTitle}>Địa chỉ nhận hàng</h2>
                <div className={shared.modeSwitch} role="group" aria-label="Hình thức đặt hàng">
                  <button
                    type="button"
                    className={`${shared.modeBtn} ${!isGuestMode ? shared.modeBtnActive : ""}`}
                    onClick={() => {
                      setGuestCheckout({ enabled: false });
                      if (!isAuthenticated) {
                        loginMock(DEMO_USER.email, DEMO_USER.fullName);
                      }
                    }}
                  >
                    {isAuthenticated ? `Tài khoản (${user.fullName})` : "Đăng nhập nhanh"}
                  </button>
                  <button
                    type="button"
                    className={`${shared.modeBtn} ${isGuestMode ? shared.modeBtnActive : ""}`}
                    onClick={() => setGuestCheckout({ enabled: true })}
                  >
                    Đặt không cần tài khoản
                  </button>
                </div>
              </div>

              {isGuestMode ? (
                <div className={styles.guestFormWrap}>
                  <div className={shared.formGrid}>
                    <div className={shared.field}>
                      <label htmlFor="guest-name" className={shared.fieldLabel}>
                        Họ và tên người nhận
                      </label>
                      <input
                        id="guest-name"
                        type="text"
                        className={shared.input}
                        value={guestCheckout.fullName}
                        onChange={(e) => setGuestCheckout({ fullName: e.target.value })}
                        placeholder="Ví dụ: Lê Minh Tâm"
                      />
                    </div>
                    <div className={shared.field}>
                      <label htmlFor="guest-phone" className={shared.fieldLabel}>
                        Số điện thoại nhận hàng
                      </label>
                      <input
                        id="guest-phone"
                        type="tel"
                        className={shared.input}
                        value={guestCheckout.phone}
                        onChange={(e) => setGuestCheckout({ phone: e.target.value })}
                        placeholder="0909 456 789"
                      />
                    </div>
                    <div className={`${shared.field} ${shared.formFull}`}>
                      <label htmlFor="guest-email" className={shared.fieldLabel}>
                        Email nhận xác nhận đơn (không bắt buộc)
                      </label>
                      <input
                        id="guest-email"
                        type="email"
                        className={shared.input}
                        value={guestCheckout.email}
                        onChange={(e) => setGuestCheckout({ email: e.target.value })}
                        placeholder="minhtam@email.vn"
                      />
                    </div>
                    <div className={shared.field}>
                      <label htmlFor="guest-prov-q" className={shared.fieldLabel}>
                        Tỉnh / Thành phố
                      </label>
                      <input
                        id="guest-prov-q"
                        type="search"
                        className={shared.input}
                        value={guestProvQuery}
                        onChange={(e) => setGuestProvQuery(e.target.value)}
                        placeholder="Tìm nhanh: Hà Nội, Đà Nẵng, Cần Thơ..."
                      />
                      {guestProvSuggestions.length > 0 && (
                        <div className={shared.quickSuggestList}>
                          {guestProvSuggestions.map((p) => (
                            <button
                              key={p.code}
                              type="button"
                              className={shared.quickSuggestChip}
                              onClick={() => {
                                setGuestCheckout({
                                  provinceCode: p.code,
                                  wardName: p.wards[0] ?? "",
                                });
                                setGuestProvQuery("");
                              }}
                            >
                              {p.shortName}
                            </button>
                          ))}
                        </div>
                      )}
                      <select
                        id="guest-prov"
                        aria-label="Chọn Tỉnh hoặc Thành phố"
                        className={shared.select}
                        value={activeProvince.code}
                        onChange={(e) => {
                          const nextProv = getProvinceByCode(e.target.value) ?? PROVINCES[0];
                          setGuestCheckout({
                            provinceCode: nextProv.code,
                            wardName: nextProv.wards[0] ?? "",
                          });
                        }}
                      >
                        {(["bac", "trung", "nam"] as const).map((region) => (
                          <optgroup key={region} label={REGION_LABELS[region]}>
                            {PROVINCES.filter((p) => p.region === region).map((p) => (
                              <option key={p.code} value={p.code}>
                                {p.name}
                              </option>
                            ))}
                          </optgroup>
                        ))}
                      </select>
                    </div>
                    <div className={shared.field}>
                      <label htmlFor="guest-ward" className={shared.fieldLabel}>
                        Phường / Xã
                      </label>
                      <select
                        id="guest-ward"
                        className={shared.select}
                        value={guestCheckout.wardName}
                        onChange={(e) => setGuestCheckout({ wardName: e.target.value })}
                      >
                        {activeProvince.wards.map((w) => (
                          <option key={w} value={w}>
                            {w}
                          </option>
                        ))}
                      </select>
                    </div>
                    <div className={`${shared.field} ${shared.formFull}`}>
                      <label htmlFor="guest-street" className={shared.fieldLabel}>
                        Số nhà, tên đường / ngõ
                      </label>
                      <input
                        id="guest-street"
                        type="text"
                        className={shared.input}
                        value={guestCheckout.streetDetail}
                        onChange={(e) => setGuestCheckout({ streetDetail: e.target.value })}
                        placeholder="Số 45 đường Lê Lợi"
                      />
                    </div>
                  </div>
                </div>
              ) : (
                <>
                  <div className={styles.blockHead}>
                    <span className={styles.addressShipLine}>
                      Chọn từ địa chỉ đã lưu hoặc thêm địa chỉ giao hàng mới.
                    </span>
                    <Button
                      variant="secondary"
                      size="sm"
                      onClick={() => setShowNewAddressForm((v) => !v)}
                    >
                      {showNewAddressForm ? "Đóng biểu mẫu" : "Thêm địa chỉ mới"}
                    </Button>
                  </div>

                  <div
                    className={styles.addressList}
                    role="radiogroup"
                    aria-label="Chọn địa chỉ nhận hàng"
                  >
                    {addresses.map((addr, idx) => {
                      const isSelected = selectedAddress?.id === addr.id;
                      const prov = getProvinceByCode(addr.provinceCode);
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
                          onClick={() => selectAddress(addr.id)}
                        >
                          <div className={styles.addressTopRow}>
                            <span className={styles.addressName}>
                              {addr.recipientName} · {addr.phone}
                            </span>
                            <div className={shared.formActions}>
                              {addr.isDefault && (
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
                            {addr.streetDetail}, {addr.wardName}, {addr.provinceName}
                          </p>
                          <p className={styles.addressShipLine}>
                            Giao dự kiến {prov?.etaDays ?? "1-2 ngày"} · Cước cơ bản{" "}
                            <span className="num">{formatVnd(prov?.shippingFee ?? 28000)}</span>
                          </p>
                        </button>
                      );
                    })}
                  </div>

                  {showNewAddressForm && (
                    <AddressForm
                      submitLabel="Lưu và giao tới địa chỉ này"
                      onSaved={() => setShowNewAddressForm(false)}
                      onCancel={() => setShowNewAddressForm(false)}
                    />
                  )}
                </>
              )}
            </div>

            {/* Step 2: Shipping, Gift Wrap & VAT */}
            <div
              className={styles.stepBlock}
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              <h2 className={styles.blockTitle}>Giao hàng, Quà tặng & Hóa đơn VAT</h2>
              <div className={styles.shippingStack}>
                <div
                  className={styles.deliveryOptions}
                  role="radiogroup"
                  aria-label="Chọn gói vận chuyển"
                >
                  <button
                    type="button"
                    role="radio"
                    aria-checked={deliveryMethod === "standard"}
                    className={`${styles.deliveryOptionBtn} ${
                      deliveryMethod === "standard" ? styles.deliveryOptionActive : ""
                    }`}
                    onClick={() => setDeliveryMethod("standard")}
                  >
                    <span className={styles.paymentTitle}>Giao tiêu chuẩn 2-4 ngày</span>
                    <span className={styles.addressShipLine}>
                      SPX / GHN · Toàn quốc · Mở kiểm tra khi nhận
                    </span>
                  </button>

                  <button
                    type="button"
                    role="radio"
                    aria-checked={deliveryMethod === "fast"}
                    disabled={!anyShopSupportsFast}
                    className={`${styles.deliveryOptionBtn} ${
                      deliveryMethod === "fast" ? styles.deliveryOptionActive : ""
                    }`}
                    onClick={() => setDeliveryMethod("fast")}
                  >
                    <span className={styles.paymentTitle}>Giao nhanh nội thành (+15.000₫)</span>
                    <span className={styles.addressShipLine}>
                      {anyShopSupportsFast
                        ? "NexusFast 2-4 giờ với xưởng hỗ trợ cùng tỉnh"
                        : "Các xưởng trong đơn hiện giao tiêu chuẩn"}
                    </span>
                  </button>
                </div>

                {/* Per-shop shipping ETA rows */}
                <div className={styles.shopDeliveryList}>
                  {shopDeliveries.map(({ group, shop, estimate }) => (
                    <div key={group.shopId} className={styles.shopDeliveryRow} data-reveal="">
                      <div className={styles.shippingRow}>
                        <div className={styles.shippingInfo}>
                          <IconTruck size={17} className={styles.shippingIcon} />
                          <div>
                            <p className={styles.addressName}>
                              {shop?.name ?? "Gian hàng"} · {estimate.carrier}
                            </p>
                            <p className={styles.addressShipLine}>
                              Xuất kho {estimate.originCity} tới {estimate.destinationProvinceName}{" "}
                              · Dự kiến {estimate.etaLabel}
                            </p>
                          </div>
                        </div>
                        <span className={`${shared.statusTag} ${shared.toneNeutral}`}>
                          {estimate.fastAvailable ? "Có giao nhanh" : "Tiêu chuẩn"}
                        </span>
                      </div>
                    </div>
                  ))}
                </div>

                <label className={shared.checkboxRow}>
                  <input
                    type="checkbox"
                    className={shared.checkbox}
                    checked={coInspectionEnabled}
                    onChange={(e) => setCoInspectionEnabled(e.target.checked)}
                  />
                  <span>Mở kiểm tra mẫu và size cùng bưu tá khi nhận hàng</span>
                </label>

                <p className={styles.addressShipLine}>
                  Cần giao gấp ngoài khung giờ? Gọi CSKH Hotline{" "}
                  <strong className="num">1900 6845</strong> (8:30 - 22:00 T2 - CN).
                </p>

                <div className={styles.freeshipRow}>
                  {freeshipVouchers.map((v) => {
                    const eligible = cartSummary.subtotal >= v.minOrderValue;
                    const active = freeshipVoucherCode === v.code && eligible;
                    return (
                      <button
                        key={v.code}
                        type="button"
                        disabled={!eligible}
                        className={`${styles.freeshipBtn} ${active ? styles.freeshipBtnActive : ""}`}
                        onClick={() => setFreeshipVoucher(active ? null : v.code)}
                      >
                        {v.code} (Giảm {formatVnd(v.discountValue)})
                      </button>
                    );
                  })}
                </div>

                <div className={shared.field}>
                  <label htmlFor="checkout-note" className={shared.fieldLabel}>
                    Ghi chú giao hàng (không bắt buộc)
                  </label>
                  <textarea
                    id="checkout-note"
                    className={shared.textarea}
                    value={orderNote}
                    onChange={(e) => setOrderNote(e.target.value)}
                    placeholder="Ví dụ: Giao giờ hành chính, gọi trước 15 phút khi đến."
                  />
                </div>

                {/* Gift wrap & card message */}
                <div className={styles.extraToggleGroup} data-reveal="">
                  <label className={shared.checkboxRow}>
                    <input
                      type="checkbox"
                      className={shared.checkbox}
                      checked={giftWrap}
                      onChange={(e) => setGiftWrap(e.target.checked)}
                    />
                    <span>Túi quà NexusMart & Viết thiệp lời nhắn (miễn phí)</span>
                  </label>
                  {giftWrap && (
                    <div className={shared.field}>
                      <label htmlFor="gift-card-msg" className={shared.fieldLabel}>
                        Lời nhắn in trên thiệp tặng
                      </label>
                      <textarea
                        id="gift-card-msg"
                        className={shared.textarea}
                        value={giftCardMessage}
                        onChange={(e) => setGiftCardMessage(e.target.value)}
                        placeholder="Chúc mừng sinh nhật, mặc thật thoải mái nhé!"
                      />
                    </div>
                  )}
                </div>

                {/* VAT invoice toggle */}
                <div className={styles.extraToggleGroup} data-reveal="">
                  <label className={shared.checkboxRow}>
                    <input
                      type="checkbox"
                      className={shared.checkbox}
                      checked={vatEnabled}
                      onChange={(e) => setVatEnabled(e.target.checked)}
                    />
                    <span>Xuất hóa đơn VAT điện tử (gửi bản nháp duyệt trong 24h)</span>
                  </label>
                  {vatEnabled && (
                    <div className={shared.formGrid}>
                      <div className={shared.field}>
                        <label htmlFor="vat-mst" className={shared.fieldLabel}>
                          Mã số thuế (MST)
                        </label>
                        <input
                          id="vat-mst"
                          type="text"
                          className={shared.input}
                          value={vatTaxCode}
                          onChange={(e) => setVatTaxCode(e.target.value)}
                          placeholder="0316482910"
                        />
                      </div>
                      <div className={shared.field}>
                        <label htmlFor="vat-email" className={shared.fieldLabel}>
                          Email nhận bản nháp & hóa đơn
                        </label>
                        <input
                          id="vat-email"
                          type="email"
                          className={shared.input}
                          value={vatEmail}
                          onChange={(e) => setVatEmail(e.target.value)}
                          placeholder="ketoan@congty.vn"
                        />
                      </div>
                      <div className={`${shared.field} ${shared.formFull}`}>
                        <label htmlFor="vat-company" className={shared.fieldLabel}>
                          Tên đơn vị / công ty
                        </label>
                        <input
                          id="vat-company"
                          type="text"
                          className={shared.input}
                          value={vatCompanyName}
                          onChange={(e) => setVatCompanyName(e.target.value)}
                          placeholder="Công ty TNHH Kiến trúc Đông Nam"
                        />
                      </div>
                      <div className={`${shared.field} ${shared.formFull}`}>
                        <label htmlFor="vat-addr" className={shared.fieldLabel}>
                          Địa chỉ xuất hóa đơn (không bắt buộc)
                        </label>
                        <input
                          id="vat-addr"
                          type="text"
                          className={shared.input}
                          value={vatCompanyAddress}
                          onChange={(e) => setVatCompanyAddress(e.target.value)}
                          placeholder="Số 248 Võ Văn Tần, Phường Xuân Hòa, TP. Hồ Chí Minh"
                        />
                        <span className={shared.fieldHint}>
                          Xưởng gửi bản nháp hóa đơn VAT qua email trong 24 giờ để bạn kiểm tra
                          trước khi phát hành chính thức.
                        </span>
                      </div>
                    </div>
                  )}
                </div>
              </div>
            </div>

            {/* Step 3: Payment methods (COD, VietQR, NexusPay, Linked Wallet — no card form) */}
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
                  aria-checked={gatewayChoice === "cod"}
                  data-reveal=""
                  className={`${styles.paymentOption} ${
                    gatewayChoice === "cod" ? styles.paymentOptionSelected : ""
                  }`}
                  onClick={() => setGatewayChoice("cod")}
                >
                  <div className={styles.paymentHead}>
                    <span className={styles.paymentTitle}>{PAYMENT_METHOD_LABELS.cod}</span>
                    {gatewayChoice === "cod" && (
                      <span className={`${shared.statusTag} ${shared.tonePos}`}>Đang chọn</span>
                    )}
                  </div>
                  <p className={styles.paymentDesc}>
                    Thanh toán trực tiếp cho bưu tá sau khi mở kiểm tra đúng mẫu và đúng size.
                  </p>
                </button>

                <div
                  data-reveal=""
                  className={`${styles.paymentOption} ${
                    gatewayChoice === "vietqr" ? styles.paymentOptionSelected : ""
                  }`}
                >
                  <button
                    type="button"
                    role="radio"
                    aria-checked={gatewayChoice === "vietqr"}
                    className={styles.paymentHead}
                    onClick={() => setGatewayChoice("vietqr")}
                  >
                    <span className={styles.paymentTitle}>{PAYMENT_METHOD_LABELS.vietqr}</span>
                    {gatewayChoice === "vietqr" && (
                      <span className={`${shared.statusTag} ${shared.tonePos}`}>Đang chọn</span>
                    )}
                  </button>
                  <p className={styles.paymentDesc}>
                    Quét mã QR bằng ứng dụng ngân hàng để xác nhận đơn ngay lập tức, không cần nhập
                    số thẻ.
                  </p>
                  {gatewayChoice === "vietqr" && (
                    <VietQrPreview amount={cartSummary.grandTotal} reference={transferRef} />
                  )}
                </div>

                <div
                  data-reveal=""
                  className={`${styles.paymentOption} ${
                    gatewayChoice === "nexuspay" ? styles.paymentOptionSelected : ""
                  }`}
                >
                  <button
                    type="button"
                    role="radio"
                    aria-checked={gatewayChoice === "nexuspay"}
                    className={styles.paymentHead}
                    onClick={() => setGatewayChoice("nexuspay")}
                  >
                    <span className={styles.paymentTitle}>
                      {PAYMENT_METHOD_LABELS[WALLET_PAYMENT_METHOD]}
                    </span>
                    {gatewayChoice === "nexuspay" && (
                      <span className={`${shared.statusTag} ${shared.tonePos}`}>Đang chọn</span>
                    )}
                  </button>
                  <p className={styles.paymentDesc}>
                    Thanh toán tức thì bằng số dư ví NexusPay và xác nhận đơn ngay khi bấm Đặt hàng.
                  </p>
                  {gatewayChoice === "nexuspay" && (
                    <div className={styles.walletBox}>
                      <p>
                        Số dư ví NexusPay:{" "}
                        <strong className="num">{formatVnd(NEXUSPAY_WALLET_BALANCE)}</strong>
                      </p>
                      <p>
                        Số tiền thanh toán:{" "}
                        <strong className="num">{formatVnd(cartSummary.grandTotal)}</strong> (đủ số
                        dư xác nhận ngay).
                      </p>
                    </div>
                  )}
                </div>

                <div
                  data-reveal=""
                  className={`${styles.paymentOption} ${
                    gatewayChoice === "linked_wallet" ? styles.paymentOptionSelected : ""
                  }`}
                >
                  <button
                    type="button"
                    role="radio"
                    aria-checked={gatewayChoice === "linked_wallet"}
                    className={styles.paymentHead}
                    onClick={() => setGatewayChoice("linked_wallet")}
                  >
                    <span className={styles.paymentTitle}>Ví điện tử liên kết</span>
                    {gatewayChoice === "linked_wallet" && (
                      <span className={`${shared.statusTag} ${shared.tonePos}`}>Đang chọn</span>
                    )}
                  </button>
                  <p className={styles.paymentDesc}>
                    Xác nhận thanh toán nhanh qua ví điện tử nội địa đã liên kết, không cần nhập số
                    thẻ hay mã CVV.
                  </p>
                </div>
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
                {activeGroups.map((group) => {
                  const shop = getShopById(group.shopId);
                  const selectedItems = group.items.filter((it) => it.selected && it.quantity > 0);
                  return (
                    <div key={group.shopId} className={styles.shopSlice} data-reveal="">
                      <div className={styles.shopSliceHead}>
                        <span>{shop?.name ?? "Gian hàng NexusMart"}</span>
                        <span className="num">{formatVnd(group.selectedSubtotal)}</span>
                      </div>
                      <div className={styles.shopSliceItems}>
                        {selectedItems.map((it) => (
                          <div key={it.skuCode} className={styles.miniItem}>
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
                                {it.colorName} · Size {it.size} · SL{" "}
                                <span className="num">{it.quantity}</span>
                              </span>
                            </div>
                            <span className={`${styles.miniPrice} num`}>
                              {formatVnd(it.unitPrice * it.quantity)}
                            </span>
                          </div>
                        ))}
                      </div>
                      {(group.tierDiscount ?? 0) > 0 && (
                        <div className={styles.ledgerRow}>
                          <span className={styles.ledgerLabel}>Giảm mua nhiều</span>
                          <span className={`${styles.ledgerDiscount} num`}>
                            {formatSignedVnd(-(group.tierDiscount ?? 0))}
                          </span>
                        </div>
                      )}
                      {group.shopVoucherDiscount > 0 && (
                        <div className={styles.ledgerRow}>
                          <span className={styles.ledgerLabel}>
                            Voucher gian hàng ({group.shopVoucherCode})
                          </span>
                          <span className={`${styles.ledgerDiscount} num`}>
                            {formatSignedVnd(-group.shopVoucherDiscount)}
                          </span>
                        </div>
                      )}
                    </div>
                  );
                })}

                {(cartSummary.freeGifts?.length ?? 0) > 0 && (
                  <div className={styles.giftHighlight} data-reveal="">
                    <span className={styles.giftHighlightTitle}>
                      <IconGift size={16} />
                      <span>Quà tặng kèm đơn (0₫)</span>
                    </span>
                    {cartSummary.freeGifts?.map((g) => (
                      <span key={g.id} className={styles.addressLine}>
                        {g.title} ({g.reason})
                      </span>
                    ))}
                  </div>
                )}
              </div>

              {/* Inline voucher / gift card / coins */}
              <form className={styles.promoSection} onSubmit={handleApplyCode} noValidate>
                <label htmlFor="co-code-input" className={shared.fieldLabel}>
                  Áp thêm mã ưu đãi hoặc thẻ quà tặng
                </label>
                <div className={shared.codeInputRow}>
                  <input
                    id="co-code-input"
                    type="text"
                    className={shared.input}
                    value={promoCodeInput}
                    onChange={(e) => setPromoCodeInput(e.target.value)}
                    placeholder="NEXUS30, EDU15, NEXUS-GIFT100"
                  />
                  <Button type="submit" variant="ink" size="md">
                    Áp dụng
                  </Button>
                </div>
                {promoFeedback && (
                  <p
                    className={`${shared.feedbackBox} ${
                      promoFeedback.ok ? shared.feedbackSuccess : shared.feedbackError
                    }`}
                  >
                    {promoFeedback.message}
                  </p>
                )}
                <label className={shared.checkboxRow}>
                  <input
                    type="checkbox"
                    className={shared.checkbox}
                    checked={useCoins && cartSummary.maxUsableCoins > 0}
                    disabled={cartSummary.maxUsableCoins <= 0}
                    onChange={(e) => setUseCoins(e.target.checked)}
                  />
                  <span>
                    Dùng <strong className="num">{formatInt(cartSummary.maxUsableCoins)} Xu</strong>{" "}
                    (tối đa 20% tiền hàng · có{" "}
                    <span className="num">{formatInt(user.coins)} Xu</span>)
                  </span>
                </label>
              </form>

              <dl className={styles.ledgerList}>
                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>
                    Tạm tính (<span className="num">{cartSummary.selectedCount}</span> sản phẩm)
                  </dt>
                  <dd className="num">{formatVnd(cartSummary.subtotal)}</dd>
                </div>
                {tierDiscountTotal > 0 && (
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Giảm mua nhiều</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {formatSignedVnd(-tierDiscountTotal)}
                    </dd>
                  </div>
                )}
                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Voucher gian hàng</dt>
                  <dd className={`${styles.ledgerDiscount} num`}>
                    {cartSummary.shopVoucherTotal > 0
                      ? formatSignedVnd(-cartSummary.shopVoucherTotal)
                      : "0₫"}
                  </dd>
                </div>
                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Voucher toàn sàn</dt>
                  <dd className={`${styles.ledgerDiscount} num`}>
                    {cartSummary.platformVoucherDiscount > 0
                      ? formatSignedVnd(-cartSummary.platformVoucherDiscount)
                      : "0₫"}
                  </dd>
                </div>
                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Phí vận chuyển</dt>
                  <dd className="num">{formatVnd(cartSummary.shippingFee)}</dd>
                </div>
                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Giảm phí vận chuyển</dt>
                  <dd className={`${styles.ledgerDiscount} num`}>
                    {cartSummary.freeshipDiscount > 0
                      ? formatSignedVnd(-cartSummary.freeshipDiscount)
                      : "0₫"}
                  </dd>
                </div>
                <div className={styles.ledgerRow}>
                  <dt className={styles.ledgerLabel}>Trừ Xu NexusMart</dt>
                  <dd className={`${styles.ledgerDiscount} num`}>
                    {cartSummary.coinDiscount > 0
                      ? formatSignedVnd(-cartSummary.coinDiscount)
                      : "0₫"}
                  </dd>
                </div>
                {giftCardDiscount > 0 && (
                  <div className={styles.ledgerRow}>
                    <dt className={styles.ledgerLabel}>Trừ thẻ quà tặng</dt>
                    <dd className={`${styles.ledgerDiscount} num`}>
                      {formatSignedVnd(-giftCardDiscount)}
                    </dd>
                  </div>
                )}
              </dl>
            </div>

            {/* Bold dark total & primary CTA anchor */}
            <div className={styles.ctaBand}>
              <div className={styles.grandBand}>
                <span className={styles.grandLabel}>Tổng thanh toán</span>
                <span className={`${styles.grandVal} num`}>
                  {formatVnd(cartSummary.grandTotal)}
                </span>
              </div>

              {(cartSummary.estimatedCashbackCoins ?? 0) > 0 && !isGuestMode && (
                <p className={styles.cashbackLine}>
                  Hoàn dự kiến{" "}
                  <strong className="num">
                    +{formatInt(cartSummary.estimatedCashbackCoins ?? 0)} Xu
                  </strong>{" "}
                  vào ví Hội viên Nexus ({membershipTier.name}).
                </p>
              )}

              <div className={styles.assuranceBox}>
                <IconShield size={17} className={styles.assuranceIcon} />
                <span>
                  Xác nhận đơn an toàn — sản phẩm đúng màu và size được giữ ngay khi đặt hàng.
                </span>
              </div>

              {formError && (
                <p className={`${shared.feedbackBox} ${shared.feedbackError}`} role="alert">
                  {formError}
                </p>
              )}

              <Button
                variant="primary"
                size="lg"
                fullWidth
                className={shared.wrapBtn}
                disabled={isSubmitting}
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

      {/* Compact collapsed demo switch tucked cleanly away from checkout steps */}
      {activeGateway.method !== "cod" && (
        <section className={styles.demoSection} aria-label="Tùy chọn chế độ demo">
          <details className={styles.demoDetails}>
            <summary className={styles.demoSummary}>Chế độ demo</summary>
            <div className={styles.demoBody}>
              <label className={shared.checkboxRow}>
                <input
                  type="checkbox"
                  className={shared.checkbox}
                  checked={simulatePaymentPending}
                  onChange={(e) => setSimulatePaymentPending(e.target.checked)}
                />
                <span>Tạo đơn ở trạng thái chờ thanh toán (để kiểm tra nút Thanh toán lại)</span>
              </label>
              <p className={styles.addressShipLine}>
                Khi bật, đơn hàng mới sẽ ở trạng thái Chờ thanh toán để bạn thử chọn phương thức và
                thanh toán lại ở trang chi tiết đơn hàng.
              </p>
            </div>
          </details>
        </section>
      )}

      {/* Sticky mobile place-order bar */}
      <div className={styles.mobileCheckoutBar} aria-label="Xác nhận đặt hàng nhanh">
        <div>
          <p className={styles.miniMeta}>{activeGateway.label}</p>
          <p className={`${styles.addressName} num`}>{formatVnd(cartSummary.grandTotal)}</p>
        </div>
        <Button variant="primary" size="md" disabled={isSubmitting} onClick={handlePlaceOrder}>
          {isSubmitting ? "Đang tạo..." : `Đặt · ${formatVnd(cartSummary.grandTotal)}`}
        </Button>
      </div>
    </div>
  );
}

export default function CheckoutPage() {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang chuẩn bị trang thanh toán..." rows={4} />
        </div>
      }
    >
      <CheckoutPageContent />
    </Suspense>
  );
}
