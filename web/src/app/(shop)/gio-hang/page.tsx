"use client";

import React, { Suspense, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  DEMO_USER,
  FLASH_SALE_RULES,
  GIFT_ORDER_THRESHOLD,
  PLATFORM_FREESHIP_THRESHOLD,
  PRODUCTS,
  VOUCHERS,
  WALLET_PAYMENT_METHOD,
  getProductById,
  getShopById,
  getShopVouchers,
  validateVoucher,
} from "@/data";
import type { PaymentMethod } from "@/data";
import { PROVINCES, getProvinceByCode } from "@/lib/provinces";
import { formatInt, formatSignedVnd, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import {
  Button,
  IconChat,
  IconGift,
  IconMinus,
  IconPlus,
  IconTag,
  IconTrash,
  IconTruck,
  Price,
  StateView,
} from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { usePageStateParam, useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./page.module.css";

type QuickPayChoice = "vietqr" | "cod" | "nexuspay" | "linked_wallet";

const QUICK_PAY_META: Record<QuickPayChoice, { shortLabel: string; method: PaymentMethod }> = {
  vietqr: { shortLabel: "VietQR", method: "vietqr" },
  cod: { shortLabel: "COD đồng kiểm", method: "cod" },
  nexuspay: { shortLabel: "Ví NexusPay", method: WALLET_PAYMENT_METHOD },
  linked_wallet: { shortLabel: "Ví liên kết", method: WALLET_PAYMENT_METHOD },
};

function CartPageContent() {
  const router = useRouter();
  const viewState = usePageStateParam();
  const {
    cartItems,
    cartSummary,
    selectedShopVouchers,
    platformVoucherCode,
    freeshipVoucherCode,
    useCoins,
    user,
    isAuthenticated,
    loginMock,
    addresses,
    selectedAddress,
    selectAddress,
    guestCheckout,
    setGuestCheckout,
    deliveryMethod,
    setDeliveryMethod,
    membership,
    membershipTier,
    applyGiftCardCode,
    updateCartQuantity,
    removeFromCart,
    toggleCartItemSelected,
    toggleShopSelected,
    toggleSelectAllCart,
    setShopVoucher,
    setPlatformVoucher,
    setFreeshipVoucher,
    setUseCoins,
    placeOrder,
    clearCart,
    openChat,
    showToast,
  } = useStore();

  const [confirmClear, setConfirmClear] = useState(false);
  const [manualCode, setManualCode] = useState("");
  const [codeFeedback, setCodeFeedback] = useState<{ ok: boolean; message: string } | null>(null);
  const [quickCheckoutOpen, setQuickCheckoutOpen] = useState(false);
  const [quickPayChoice, setQuickPayChoice] = useState<QuickPayChoice>("vietqr");
  const [quickError, setQuickError] = useState<string | null>(null);

  useRevealOnScroll(
    `${viewState ?? "live"}-${cartItems.length}-${quickCheckoutOpen ? "quick" : "std"}`
  );

  const platformVouchers = useMemo(() => VOUCHERS.filter((v) => v.scope === "platform"), []);
  const freeshipVouchers = useMemo(() => VOUCHERS.filter((v) => v.scope === "freeship"), []);

  const addonProducts = useMemo(() => {
    const inCart = new Set(cartItems.map((c) => c.productId));
    const filtered = PRODUCTS.filter((p) => !inCart.has(p.id));
    return (filtered.length >= 4 ? filtered : PRODUCTS).slice(0, 4);
  }, [cartItems]);

  const allSelected = cartItems.length > 0 && cartItems.every((i) => i.selected);
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

  const flashSaleQty = useMemo(
    () => cartItems.filter((it) => it.isFlashSaleItem).reduce((s, it) => s + it.quantity, 0),
    [cartItems]
  );
  const shockDealQty = useMemo(
    () => cartItems.filter((it) => it.isShockDeal).reduce((s, it) => s + it.quantity, 0),
    [cartItems]
  );

  const effectiveProvinceCode = guestCheckout.enabled
    ? guestCheckout.provinceCode || "HCM"
    : (selectedAddress?.provinceCode ?? "HCM");
  const provinceInfo = getProvinceByCode(effectiveProvinceCode) ?? PROVINCES[0];

  const platformShipThreshold =
    cartSummary.platformFreeShipThreshold ?? PLATFORM_FREESHIP_THRESHOLD;
  const platformShipRemaining =
    cartSummary.platformFreeShipRemaining ??
    Math.max(0, platformShipThreshold - cartSummary.subtotal);
  const platformShipPct = Math.min(
    100,
    Math.round((cartSummary.subtotal / Math.max(1, platformShipThreshold)) * 100)
  );

  const giftRemaining = Math.max(0, GIFT_ORDER_THRESHOLD - cartSummary.subtotal);
  const freeGifts = cartSummary.freeGifts ?? [];
  const selectedQuickPay = QUICK_PAY_META[quickPayChoice];

  const handleApplyManualCode = (e: React.FormEvent) => {
    e.preventDefault();
    const clean = manualCode.trim().toUpperCase();
    if (!clean) {
      setCodeFeedback({ ok: false, message: "Vui lòng nhập mã voucher hoặc mã thẻ quà tặng." });
      return;
    }

    const matchedGift = membership.giftCards.find((gc) => gc.code.toUpperCase() === clean);
    if (matchedGift || clean.startsWith("NEXUS-")) {
      const res = applyGiftCardCode(clean);
      setCodeFeedback(res);
      if (res.ok) setManualCode("");
      return;
    }

    const voucherObj = VOUCHERS.find((v) => v.code.toUpperCase() === clean);
    const matchingShopGroup = voucherObj?.shopId
      ? cartSummary.groups.find((g) => g.shopId === voucherObj.shopId)
      : undefined;

    const eligibleAmount =
      voucherObj?.scope === "shop"
        ? Math.max(
            0,
            (matchingShopGroup?.selectedSubtotal ?? 0) - (matchingShopGroup?.tierDiscount ?? 0)
          )
        : voucherObj?.scope === "freeship"
          ? cartSummary.subtotal
          : afterShopAndTier;

    const check = validateVoucher(clean, {
      eligibleAmount,
      shopId:
        voucherObj?.scope === "shop"
          ? (matchingShopGroup?.shopId ?? cartSummary.groups[0]?.shopId)
          : undefined,
      hasOutletItems: hasOutletSelected,
      eduVerified: membership.eduVerified,
      memberTier: membershipTier.id,
      capAmount: voucherObj?.scope === "freeship" ? cartSummary.shippingFee : undefined,
    });

    if (!check.valid || !check.voucher) {
      setCodeFeedback({
        ok: false,
        message: check.reason ?? `Mã ${clean} chưa đủ điều kiện áp dụng.`,
      });
      return;
    }

    if (check.voucher.scope === "shop" && check.voucher.shopId) {
      if (!matchingShopGroup || matchingShopGroup.selectedSubtotal <= 0) {
        setCodeFeedback({
          ok: false,
          message: `Giỏ hàng chưa có sản phẩm đang chọn thuộc gian hàng áp dụng mã ${check.voucher.code}.`,
        });
        return;
      }
      setShopVoucher(check.voucher.shopId, check.voucher.code);
    } else if (check.voucher.scope === "freeship") {
      setFreeshipVoucher(check.voucher.code);
    } else {
      setPlatformVoucher(check.voucher.code);
    }

    setCodeFeedback({
      ok: true,
      message: `Đã áp dụng mã ${check.voucher.code} (giảm ${formatVnd(check.discount)}).`,
    });
    setManualCode("");
  };

  const handleQuickOrderSubmit = () => {
    if (cartSummary.selectedCount <= 0) {
      setQuickError("Hãy chọn ít nhất 1 sản phẩm trước khi đặt hàng nhanh.");
      return;
    }
    if (guestCheckout.enabled || !isAuthenticated) {
      const cleanName = guestCheckout.fullName.trim();
      const cleanPhone = guestCheckout.phone.replace(/\s+/g, "").trim();
      const cleanStreet = guestCheckout.streetDetail.trim();
      if (cleanName.length < 2) {
        setQuickError("Vui lòng nhập họ tên người nhận để hoàn tất đặt hàng.");
        return;
      }
      if (!/^0\d{9,10}$/.test(cleanPhone)) {
        setQuickError("Số điện thoại cần có 10-11 chữ số, bắt đầu bằng số 0.");
        return;
      }
      if (cleanStreet.length < 5) {
        setQuickError("Vui lòng nhập địa chỉ số nhà, tên đường cụ thể.");
        return;
      }
      setQuickError(null);
      const created = placeOrder({
        paymentMethod: selectedQuickPay.method,
        deliveryMethod,
        guestAddress: {
          recipientName: cleanName,
          phone: guestCheckout.phone.trim(),
          email: guestCheckout.email.trim() || undefined,
          provinceCode: provinceInfo.code,
          provinceName: provinceInfo.name,
          wardName: guestCheckout.wardName || provinceInfo.wards[0] || "",
          streetDetail: cleanStreet,
        },
      });
      if (created.length > 0) {
        router.push(`/don-hang/${created[0].id}`);
      }
      return;
    }

    if (!selectedAddress) {
      setQuickError("Chưa có địa chỉ nhận hàng mặc định. Hãy chuyển sang trang Thanh toán đầy đủ.");
      return;
    }
    setQuickError(null);
    const created = placeOrder({
      addressId: selectedAddress.id,
      paymentMethod: selectedQuickPay.method,
      deliveryMethod,
    });
    if (created.length > 0) {
      router.push(`/don-hang/${created[0].id}`);
    }
  };

  const renderHeader = (subtitle: string) => (
    <section className={shared.pageHeader}>
      <div className={shared.headerLeadGroup}>
        <h1 className={`${shared.pageTitle} lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Giỏ hàng của bạn
            </span>
          </span>
        </h1>
        <p className={shared.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          {subtitle}
        </p>
      </div>
      <div className={shared.headerAside} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
        <Button variant="secondary" size="md" href="/danh-muc">
          Tiếp tục chọn đồ
        </Button>
      </div>
    </section>
  );

  if (viewState === "loading") {
    return (
      <div className={shared.page}>
        {renderHeader("Đang cập nhật sản phẩm, ưu đãi mua nhiều và mã giảm giá trong giỏ hàng.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Danh sách sản phẩm</h2>
          <StateView
            kind="loading"
            title="Đang tính lại giá và ưu đãi theo gian hàng..."
            rows={4}
          />
        </section>
        <ProductAddonStrip
          title="Đồ cơ bản bán chạy tại NexusMart"
          subtitle="Bốn mẫu tiêu chuẩn từ các xưởng dệt Việt Nam"
          products={addonProducts}
          showTierHint
        />
      </div>
    );
  }

  if (viewState === "error") {
    return (
      <div className={shared.page}>
        {renderHeader("Không thể kết nối đến giỏ hàng lúc này. Sản phẩm bạn đã chọn vẫn được giữ.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái giỏ hàng</h2>
          <StateView
            kind="error"
            title="Chưa thể cập nhật giỏ hàng"
            message="Kết nối đang tạm gián đoạn. Vui lòng thử tải lại trang."
            actionLabel="Tải lại giỏ hàng"
            actionHref="/gio-hang"
          />
        </section>
        <ProductAddonStrip title="Gợi ý xem nhanh danh mục cơ bản" products={addonProducts} />
      </div>
    );
  }

  if (viewState === "empty" || cartItems.length === 0) {
    return (
      <div className={shared.page}>
        {renderHeader(
          "Giỏ hàng của bạn hiện chưa có sản phẩm nào. Chọn đồ cơ bản chuẩn chất liệu để bắt đầu."
        )}
        <section
          className={shared.section}
          data-rise=""
          style={{ "--i": 3 } as React.CSSProperties}
        >
          <h2 className={shared.sectionTitle}>Sản phẩm trong giỏ</h2>
          <StateView
            kind="empty"
            title="Giỏ hàng đang trống"
            message="Khám phá áo thun cotton 220gsm, polo pique, quần short và đồ lót sợi tre từ 4 xưởng dệt Việt Nam."
            actionLabel="Khám phá danh mục"
            actionHref="/danh-muc"
          />
        </section>
        <ProductAddonStrip
          title="Gợi ý bắt đầu cho tủ đồ cơ bản"
          subtitle="Thêm nhanh theo size trực tiếp từ thẻ sản phẩm"
          products={addonProducts}
          showTierHint
        />
      </div>
    );
  }

  return (
    <div className={`${shared.page} ${styles.mobileSpacer}`}>
      {renderHeader("Kiểm tra sản phẩm, áp mã ưu đãi và chọn cách thanh toán phù hợp.")}

      <section className={shared.section}>
        <div className={styles.cartLayout}>
          {/* Left column: Freeship accent band, unboxed toolbar, crisp white shop sheets, free gifts */}
          <div className={styles.leftCol}>
            {/* Platform freeship highlight band */}
            <div
              className={styles.platformShipBar}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <div className={shared.progressBlock}>
                <div className={shared.progressMeta}>
                  <span>
                    {cartSummary.platformFreeShipUnlocked ? (
                      <>
                        Đã đạt miễn phí vận chuyển toàn sàn{" "}
                        <strong className="num">{formatVnd(platformShipThreshold)}</strong>
                      </>
                    ) : (
                      <>
                        Mua thêm <strong className="num">{formatVnd(platformShipRemaining)}</strong>{" "}
                        để được miễn phí vận chuyển đơn từ{" "}
                        <span className="num">{formatVnd(platformShipThreshold)}</span>
                      </>
                    )}
                  </span>
                  <span className="num">
                    <strong>
                      {formatVnd(Math.min(cartSummary.subtotal, platformShipThreshold))}
                    </strong>{" "}
                    / {formatVnd(platformShipThreshold)}
                  </span>
                </div>
                <div
                  className={`${shared.progressTrack} ${shared.progressTrackLight}`}
                  aria-hidden="true"
                >
                  <div
                    className={`${shared.progressFill} ${
                      cartSummary.platformFreeShipUnlocked ? shared.progressFillDone : ""
                    }`}
                    style={{ width: `${platformShipPct}%` }}
                  />
                </div>
              </div>
              {giftRemaining > 0 && cartSummary.selectedCount > 0 && (
                <p className={styles.coinMeta}>
                  Mua thêm <strong className="num">{formatVnd(giftRemaining)}</strong> để nhận tặng
                  1 đôi Tất Cotton gân trị giá 39.000₫ ở mốc{" "}
                  <span className="num">{formatVnd(GIFT_ORDER_THRESHOLD)}</span>.
                </p>
              )}
            </div>

            {/* Unboxed select-all & flash-sale quota strip */}
            <div
              className={styles.cartToolbar}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <div className={styles.selectBar}>
                <label className={shared.checkboxRow}>
                  <input
                    type="checkbox"
                    className={shared.checkbox}
                    checked={allSelected}
                    onChange={(e) => toggleSelectAllCart(e.target.checked)}
                  />
                  <span>
                    Chọn tất cả (<strong className="num">{cartItems.length}</strong> sản phẩm)
                  </span>
                </label>

                {confirmClear ? (
                  <div className={shared.formActions}>
                    <Button
                      variant="danger"
                      size="sm"
                      onClick={() => {
                        clearCart();
                        setConfirmClear(false);
                        showToast("Đã xóa toàn bộ sản phẩm trong giỏ hàng.");
                      }}
                    >
                      Xác nhận xóa hết
                    </Button>
                    <Button variant="secondary" size="sm" onClick={() => setConfirmClear(false)}>
                      Giữ lại
                    </Button>
                  </div>
                ) : (
                  <button
                    type="button"
                    className={styles.clearBtn}
                    onClick={() => setConfirmClear(true)}
                  >
                    Xóa toàn bộ giỏ
                  </button>
                )}
              </div>

              {(flashSaleQty > 0 || shockDealQty > 0) && (
                <p className={styles.promoRuleLine}>
                  <span>{FLASH_SALE_RULES.note}</span>
                  <span className="num">
                    Flash sale: <strong>{flashSaleQty}</strong>/{FLASH_SALE_RULES.maxItemsPerOrder}{" "}
                    · Deal sốc: <strong>{shockDealQty}</strong>/
                    {FLASH_SALE_RULES.shockDealMaxPerOrder}
                  </span>
                </p>
              )}
            </div>

            {/* Shop groups: one clean white surface per shop */}
            <div className={styles.shopGroupList}>
              {cartSummary.groups.map((group, groupIdx) => {
                const shop = getShopById(group.shopId);
                const shopVouchers = getShopVouchers(group.shopId);
                const shopAllSelected = group.items.every((it) => it.selected);
                const chosenShopCode = selectedShopVouchers[group.shopId] ?? null;
                const shopShipThreshold =
                  group.freeShipThreshold ?? shop?.freeShipThreshold ?? 199000;
                const shopShipRemaining = group.freeShipRemaining ?? 0;
                const shopShipPct = Math.min(
                  100,
                  Math.round((group.selectedSubtotal / Math.max(1, shopShipThreshold)) * 100)
                );
                const shopTierDiscount = group.tierDiscount ?? 0;
                const shopAfterTier = Math.max(0, group.selectedSubtotal - shopTierDiscount);

                return (
                  <article
                    key={group.shopId}
                    className={styles.shopGroup}
                    data-rise=""
                    style={{ "--i": Math.min(groupIdx + 3, 6) } as React.CSSProperties}
                  >
                    <div className={styles.shopHeader}>
                      <div className={styles.shopIdentity}>
                        <input
                          type="checkbox"
                          className={shared.checkbox}
                          checked={shopAllSelected}
                          onChange={(e) => toggleShopSelected(group.shopId, e.target.checked)}
                          aria-label={`Chọn toàn bộ sản phẩm của ${shop?.name ?? "gian hàng"}`}
                        />
                        <span
                          className={styles.shopDot}
                          style={{ background: shop?.accentDot ?? "var(--accent)" }}
                          aria-hidden="true"
                        />
                        <Link href={`/gian-hang/${shop?.slug ?? ""}`} className={styles.shopLink}>
                          {shop?.name ?? "Gian hàng NexusMart"}
                        </Link>
                        {shop?.city && <span className={styles.shopCity}>{shop.city}</span>}
                      </div>

                      <button
                        type="button"
                        className={styles.chatShopBtn}
                        onClick={() => openChat(group.shopId)}
                      >
                        <IconChat size={15} />
                        <span>Chat với xưởng</span>
                      </button>
                    </div>

                    {/* Per-shop freeship progress bar */}
                    <div className={styles.shopShipStrip}>
                      <div className={shared.progressBlock}>
                        <div className={shared.progressMeta}>
                          <span>
                            {group.freeShipUnlocked ? (
                              <>
                                Đạt ưu đãi vận chuyển của {shop?.shortName} (
                                <span className="num">{formatVnd(shopShipThreshold)}</span>)
                              </>
                            ) : (
                              <>
                                Thêm <strong className="num">{formatVnd(shopShipRemaining)}</strong>{" "}
                                tại {shop?.shortName} để freeship gian hàng{" "}
                                <span className="num">{formatVnd(shopShipThreshold)}</span>
                              </>
                            )}
                          </span>
                          {shopTierDiscount > 0 && (
                            <span className={`${styles.breakdownDiscount} num`}>
                              Giảm mua nhiều: {formatSignedVnd(-shopTierDiscount)}
                            </span>
                          )}
                        </div>
                        <div className={shared.progressTrack} aria-hidden="true">
                          <div
                            className={`${shared.progressFill} ${
                              group.freeShipUnlocked ? shared.progressFillDone : ""
                            }`}
                            style={{ width: `${shopShipPct}%` }}
                          />
                        </div>
                      </div>
                    </div>

                    <ul className={styles.itemList}>
                      {group.items.map((item, itemIdx) => {
                        const lineTotal = item.unitPrice * item.quantity;
                        const prod = getProductById(item.productId);
                        const tierInfo = prod?.tierDiscounts;
                        return (
                          <li
                            key={item.skuCode}
                            className={styles.itemRow}
                            data-rise=""
                            style={{ "--i": Math.min(itemIdx + 3, 6) } as React.CSSProperties}
                          >
                            <div className={styles.itemCheck}>
                              <input
                                type="checkbox"
                                className={shared.checkbox}
                                checked={item.selected}
                                onChange={() => toggleCartItemSelected(item.skuCode)}
                                aria-label={`Chọn ${item.productName}`}
                              />
                            </div>

                            <Link
                              href={`/san-pham/${item.productSlug}`}
                              className={styles.itemThumbLink}
                            >
                              <img
                                src={item.image}
                                alt={item.productName}
                                width={896}
                                height={1200}
                                className={styles.itemThumb}
                              />
                            </Link>

                            <div className={styles.itemInfo}>
                              <Link
                                href={`/san-pham/${item.productSlug}`}
                                className={styles.itemName}
                              >
                                {item.productName}
                              </Link>
                              <p className={styles.itemVariant}>
                                Màu: {item.colorName} · Size {item.size}
                              </p>
                              <div className={styles.itemTags}>
                                {item.isFlashSaleItem && (
                                  <span className={`${shared.statusTag} ${shared.toneWarn}`}>
                                    Flash sale (≤3/đơn)
                                  </span>
                                )}
                                {item.isShockDeal && (
                                  <span className={`${shared.statusTag} ${shared.toneNeg}`}>
                                    Deal sốc (1/đơn)
                                  </span>
                                )}
                                {tierInfo && tierInfo.length > 0 && !item.isShockDeal && (
                                  <span className={`${shared.statusTag} ${shared.tonePos}`}>
                                    {tierInfo.map((t) => t.label).join(" · ")}
                                  </span>
                                )}
                                {prod?.returnEligible === false && (
                                  <span className={`${shared.statusTag} ${shared.toneNeutral}`}>
                                    Không đổi trả (Outlet / niêm phong)
                                  </span>
                                )}
                              </div>
                              <Price
                                amount={item.unitPrice}
                                originalAmount={item.originalPrice}
                                size="sm"
                              />
                              <p className={styles.itemStock}>
                                Còn <span className="num">{item.maxStock}</span> sản phẩm sẵn giao
                                {item.quantity >= item.maxStock ? " (đã đạt tối đa)" : ""}
                              </p>
                            </div>

                            <div className={styles.itemActionsCol}>
                              <span className={`${styles.lineSubtotal} num`}>
                                {formatVnd(lineTotal)}
                              </span>

                              <div className={styles.stepper} aria-label="Điều chỉnh số lượng">
                                <button
                                  type="button"
                                  className={styles.stepBtn}
                                  onClick={() =>
                                    updateCartQuantity(item.skuCode, item.quantity - 1)
                                  }
                                  aria-label="Giảm 1 sản phẩm"
                                >
                                  <IconMinus size={14} />
                                </button>
                                <span className={`${styles.stepQty} num`}>{item.quantity}</span>
                                <button
                                  type="button"
                                  className={styles.stepBtn}
                                  onClick={() =>
                                    updateCartQuantity(item.skuCode, item.quantity + 1)
                                  }
                                  disabled={item.quantity >= item.maxStock}
                                  aria-label="Tăng 1 sản phẩm"
                                >
                                  <IconPlus size={14} />
                                </button>
                              </div>

                              <button
                                type="button"
                                className={styles.removeBtn}
                                onClick={() => {
                                  removeFromCart(item.skuCode);
                                  showToast(`Đã bỏ ${item.productName} khỏi giỏ hàng.`);
                                }}
                              >
                                <IconTrash size={14} />
                                <span>Xóa</span>
                              </button>
                            </div>
                          </li>
                        );
                      })}
                    </ul>

                    {shopVouchers.length > 0 && (
                      <div className={styles.shopVoucherBar}>
                        <div className={styles.shopVoucherHead}>
                          <span className={styles.shopVoucherTitle}>
                            <IconTag size={15} />
                            <span>Voucher của {shop?.shortName}</span>
                          </span>
                          {group.shopVoucherDiscount > 0 && (
                            <span className={`${styles.breakdownDiscount} num`}>
                              {formatSignedVnd(-group.shopVoucherDiscount)}
                            </span>
                          )}
                        </div>

                        <div className={styles.voucherOptions}>
                          {shopVouchers.map((voucher) => {
                            const check = validateVoucher(voucher.code, {
                              eligibleAmount: shopAfterTier,
                              shopId: group.shopId,
                            });
                            const isSelected = chosenShopCode === voucher.code && check.valid;

                            return (
                              <button
                                key={voucher.code}
                                type="button"
                                className={`${styles.voucherOption} ${
                                  isSelected ? styles.voucherOptionActive : ""
                                } ${!check.valid ? styles.voucherOptionDisabled : ""}`}
                                disabled={!check.valid}
                                onClick={() =>
                                  setShopVoucher(group.shopId, isSelected ? null : voucher.code)
                                }
                              >
                                <div className={styles.voucherCopy}>
                                  <span className={styles.voucherCodeLine}>
                                    {voucher.code} · {voucher.title}
                                  </span>
                                  <span className={styles.voucherDesc}>
                                    {check.valid
                                      ? `Áp dụng cho đơn từ ${formatVnd(voucher.minOrderValue)}`
                                      : check.reason}
                                  </span>
                                </div>
                                <span className={styles.voucherActionLabel}>
                                  {isSelected ? "Đang dùng" : check.valid ? "Áp dụng" : "Chưa đủ"}
                                </span>
                              </button>
                            );
                          })}
                        </div>
                      </div>
                    )}
                  </article>
                );
              })}
            </div>

            {/* Automatic Free Gifts block */}
            {freeGifts.length > 0 && (
              <div
                className={styles.giftPanel}
                role="region"
                aria-label="Quà tặng miễn phí trong đơn"
                data-reveal=""
              >
                <div className={styles.giftPanelHead}>
                  <h2 className={styles.giftPanelTitle}>
                    <IconGift size={17} />
                    <span>Quà tặng miễn phí kèm đơn ({freeGifts.length})</span>
                  </h2>
                  <span className={`${shared.statusTag} ${shared.tonePos}`}>0₫</span>
                </div>
                <div className={styles.giftList}>
                  {freeGifts.map((gift) => (
                    <div key={gift.id} className={styles.giftRow}>
                      <img
                        src={gift.image}
                        alt={gift.title}
                        width={896}
                        height={1200}
                        className={styles.giftImg}
                      />
                      <div className={styles.giftMeta}>
                        <span className={styles.giftName}>{gift.title}</span>
                        <span className={styles.giftReason}>{gift.reason}</span>
                      </div>
                      <span className={`${shared.statusTag} ${shared.toneNeutral} num`}>
                        Trị giá {formatVnd(gift.valueVnd)}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>

          {/* Right column: ONE clean white sheet + dark total/CTA block */}
          <aside
            className={styles.summaryCol}
            aria-label="Tóm tắt đơn hàng, mã ưu đãi và đặt hàng nhanh"
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <div className={styles.summaryInner}>
              <h2 className={styles.summaryHeading}>Tóm tắt đơn hàng</h2>

              <div className={styles.destBanner}>
                <IconTruck size={18} className={styles.destIcon} />
                <div>
                  <p>
                    Giao dự kiến tới <strong>{provinceInfo.name}</strong> ({provinceInfo.etaDays})
                  </p>
                  <p className={styles.coinMeta}>Mở kiểm tra khi nhận · Đổi trả 45 ngày tận nơi.</p>
                </div>
              </div>

              {/* Manual voucher / gift card input */}
              <form className={styles.summaryBlock} onSubmit={handleApplyManualCode} noValidate>
                <label htmlFor="cart-manual-code" className={styles.blockHeading}>
                  Nhập mã ưu đãi hoặc thẻ quà tặng
                </label>
                <div className={shared.codeInputRow}>
                  <input
                    id="cart-manual-code"
                    type="text"
                    className={shared.input}
                    value={manualCode}
                    onChange={(e) => setManualCode(e.target.value)}
                    placeholder="Ví dụ: NEXUS30, EDU15, NEXUS-GIFT100"
                  />
                  <Button type="submit" variant="ink" size="md">
                    Áp mã
                  </Button>
                </div>
                {codeFeedback && (
                  <p
                    className={`${shared.feedbackBox} ${
                      codeFeedback.ok ? shared.feedbackSuccess : shared.feedbackError
                    }`}
                    role={codeFeedback.ok ? "status" : "alert"}
                  >
                    {codeFeedback.message}
                  </p>
                )}
                {membership.appliedGiftCardCode && (
                  <div className={`${shared.feedbackBox} ${shared.feedbackSuccess}`}>
                    Đang dùng thẻ quà tặng <strong>{membership.appliedGiftCardCode}</strong> (
                    <span className="num">{formatSignedVnd(-giftCardDiscount)}</span>).{" "}
                    <button
                      type="button"
                      className={shared.breadcrumbLink}
                      onClick={() => applyGiftCardCode(null)}
                    >
                      Gỡ thẻ
                    </button>
                  </div>
                )}
              </form>

              {/* Platform vouchers */}
              <div className={styles.summaryBlock}>
                <h3 className={styles.blockHeading}>Ưu đãi toàn sàn</h3>
                <div className={styles.voucherOptions}>
                  {platformVouchers.map((voucher) => {
                    const check = validateVoucher(voucher.code, {
                      eligibleAmount: afterShopAndTier,
                      hasOutletItems: hasOutletSelected,
                      eduVerified: membership.eduVerified,
                      memberTier: membershipTier.id,
                    });
                    const isSelected = platformVoucherCode === voucher.code && check.valid;

                    return (
                      <button
                        key={voucher.code}
                        type="button"
                        className={`${styles.voucherOption} ${
                          isSelected ? styles.voucherOptionActive : ""
                        } ${!check.valid ? styles.voucherOptionDisabled : ""}`}
                        disabled={!check.valid}
                        onClick={() => setPlatformVoucher(isSelected ? null : voucher.code)}
                      >
                        <div className={styles.voucherCopy}>
                          <span className={styles.voucherCodeLine}>
                            {voucher.code} · {voucher.title}
                          </span>
                          <span className={styles.voucherDesc}>
                            {check.valid ? voucher.description : check.reason}
                          </span>
                        </div>
                        <span className={styles.voucherActionLabel}>
                          {isSelected ? "Đang dùng" : check.valid ? "Chọn mã" : "Chưa đủ"}
                        </span>
                      </button>
                    );
                  })}
                </div>
              </div>

              {/* Freeship vouchers */}
              <div className={styles.summaryBlock}>
                <h3 className={styles.blockHeading}>Mã miễn phí vận chuyển</h3>
                <div className={styles.voucherOptions}>
                  {freeshipVouchers.map((voucher) => {
                    const check = validateVoucher(voucher.code, {
                      eligibleAmount: cartSummary.subtotal,
                      capAmount: cartSummary.shippingFee,
                    });
                    const eligible = cartSummary.shippingFee > 0 && check.valid;
                    const isSelected = freeshipVoucherCode === voucher.code && eligible;

                    return (
                      <button
                        key={voucher.code}
                        type="button"
                        className={`${styles.voucherOption} ${
                          isSelected ? styles.voucherOptionActive : ""
                        } ${!eligible ? styles.voucherOptionDisabled : ""}`}
                        disabled={!eligible}
                        onClick={() => setFreeshipVoucher(isSelected ? null : voucher.code)}
                      >
                        <div className={styles.voucherCopy}>
                          <span className={styles.voucherCodeLine}>
                            {voucher.code} · {voucher.title}
                          </span>
                          <span className={styles.voucherDesc}>
                            {eligible
                              ? `Giảm tối đa ${formatVnd(voucher.discountValue)} phí vận chuyển`
                              : (check.reason ?? "Chưa có phí vận chuyển để trừ")}
                          </span>
                        </div>
                        <span className={styles.voucherActionLabel}>
                          {isSelected ? "Đang dùng" : eligible ? "Chọn mã" : "Chưa đủ"}
                        </span>
                      </button>
                    );
                  })}
                </div>
              </div>

              {/* NexusMart Coins */}
              <div className={styles.summaryBlock}>
                <h3 className={styles.blockHeading}>Dùng Xu Hội viên Nexus</h3>
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
                    (<span className="num">{formatSignedVnd(-cartSummary.maxUsableCoins)}</span>)
                  </span>
                </label>
                <p className={styles.coinMeta}>
                  Hạng <strong>{membershipTier.name}</strong> · Số dư:{" "}
                  <strong className="num">{formatInt(user.coins)} Xu</strong> (1 Xu = 1₫). Trừ tối
                  đa 20% tiền hàng sau voucher.
                </p>
              </div>

              {/* Full integer VND breakdown */}
              <dl className={styles.breakdown}>
                <div className={styles.breakdownRow}>
                  <dt className={styles.breakdownLabel}>
                    Tạm tính (<span className="num">{cartSummary.selectedCount}</span> sản phẩm)
                  </dt>
                  <dd className="num">{formatVnd(cartSummary.subtotal)}</dd>
                </div>

                {tierDiscountTotal > 0 && (
                  <div className={styles.breakdownRow}>
                    <dt className={styles.breakdownLabel}>Giảm mua nhiều / combo</dt>
                    <dd className={`${styles.breakdownDiscount} num`}>
                      {formatSignedVnd(-tierDiscountTotal)}
                    </dd>
                  </div>
                )}

                <div className={styles.breakdownRow}>
                  <dt className={styles.breakdownLabel}>Voucher gian hàng</dt>
                  <dd className={`${styles.breakdownDiscount} num`}>
                    {cartSummary.shopVoucherTotal > 0
                      ? formatSignedVnd(-cartSummary.shopVoucherTotal)
                      : "0₫"}
                  </dd>
                </div>

                <div className={styles.breakdownRow}>
                  <dt className={styles.breakdownLabel}>Voucher toàn sàn</dt>
                  <dd className={`${styles.breakdownDiscount} num`}>
                    {cartSummary.platformVoucherDiscount > 0
                      ? formatSignedVnd(-cartSummary.platformVoucherDiscount)
                      : "0₫"}
                  </dd>
                </div>

                <div className={styles.breakdownRow}>
                  <dt className={styles.breakdownLabel}>Phí vận chuyển dự kiến</dt>
                  <dd className="num">{formatVnd(cartSummary.shippingFee)}</dd>
                </div>

                <div className={styles.breakdownRow}>
                  <dt className={styles.breakdownLabel}>Giảm phí vận chuyển</dt>
                  <dd className={`${styles.breakdownDiscount} num`}>
                    {cartSummary.freeshipDiscount > 0
                      ? formatSignedVnd(-cartSummary.freeshipDiscount)
                      : "0₫"}
                  </dd>
                </div>

                <div className={styles.breakdownRow}>
                  <dt className={styles.breakdownLabel}>Trừ Xu NexusMart</dt>
                  <dd className={`${styles.breakdownDiscount} num`}>
                    {cartSummary.coinDiscount > 0
                      ? formatSignedVnd(-cartSummary.coinDiscount)
                      : "0₫"}
                  </dd>
                </div>

                {giftCardDiscount > 0 && (
                  <div className={styles.breakdownRow}>
                    <dt className={styles.breakdownLabel}>Trừ thẻ quà tặng</dt>
                    <dd className={`${styles.breakdownDiscount} num`}>
                      {formatSignedVnd(-giftCardDiscount)}
                    </dd>
                  </div>
                )}
              </dl>

              {/* 1-page quick checkout toggle & inline form */}
              <div className={styles.summaryBlock}>
                <div className={shared.modeSwitch} role="group" aria-label="Chế độ thanh toán">
                  <button
                    type="button"
                    className={`${shared.modeBtn} ${!quickCheckoutOpen ? shared.modeBtnActive : ""}`}
                    onClick={() => setQuickCheckoutOpen(false)}
                  >
                    Thanh toán đầy đủ (VAT & Thiệp)
                  </button>
                  <button
                    type="button"
                    className={`${shared.modeBtn} ${quickCheckoutOpen ? shared.modeBtnActive : ""}`}
                    onClick={() => setQuickCheckoutOpen(true)}
                  >
                    Đặt nhanh tại đây
                  </button>
                </div>

                {quickCheckoutOpen && (
                  <div className={styles.quickCheckoutBox}>
                    <div className={shared.modeSwitch} role="group" aria-label="Tài khoản đặt hàng">
                      <button
                        type="button"
                        className={`${shared.modeBtn} ${
                          !guestCheckout.enabled && isAuthenticated ? shared.modeBtnActive : ""
                        }`}
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
                        className={`${shared.modeBtn} ${
                          guestCheckout.enabled || !isAuthenticated ? shared.modeBtnActive : ""
                        }`}
                        onClick={() => setGuestCheckout({ enabled: true })}
                      >
                        Đặt không cần tài khoản
                      </button>
                    </div>

                    {guestCheckout.enabled || !isAuthenticated ? (
                      <div className={shared.formGrid}>
                        <div className={shared.field}>
                          <label htmlFor="quick-name" className={shared.fieldLabel}>
                            Họ và tên
                          </label>
                          <input
                            id="quick-name"
                            type="text"
                            className={shared.input}
                            value={guestCheckout.fullName}
                            onChange={(e) => setGuestCheckout({ fullName: e.target.value })}
                            placeholder="Trần Hải Đăng"
                          />
                        </div>
                        <div className={shared.field}>
                          <label htmlFor="quick-phone" className={shared.fieldLabel}>
                            Số điện thoại
                          </label>
                          <input
                            id="quick-phone"
                            type="tel"
                            className={shared.input}
                            value={guestCheckout.phone}
                            onChange={(e) => setGuestCheckout({ phone: e.target.value })}
                            placeholder="0908 123 456"
                          />
                        </div>
                        <div className={shared.field}>
                          <label htmlFor="quick-prov" className={shared.fieldLabel}>
                            Tỉnh / Thành phố
                          </label>
                          <select
                            id="quick-prov"
                            className={shared.select}
                            value={guestCheckout.provinceCode}
                            onChange={(e) => {
                              const nextProv = getProvinceByCode(e.target.value) ?? PROVINCES[0];
                              setGuestCheckout({
                                provinceCode: nextProv.code,
                                wardName: nextProv.wards[0] ?? "",
                              });
                            }}
                          >
                            {PROVINCES.map((p) => (
                              <option key={p.code} value={p.code}>
                                {p.name}
                              </option>
                            ))}
                          </select>
                        </div>
                        <div className={shared.field}>
                          <label htmlFor="quick-ward" className={shared.fieldLabel}>
                            Phường / Xã
                          </label>
                          <select
                            id="quick-ward"
                            className={shared.select}
                            value={guestCheckout.wardName}
                            onChange={(e) => setGuestCheckout({ wardName: e.target.value })}
                          >
                            {provinceInfo.wards.map((w) => (
                              <option key={w} value={w}>
                                {w}
                              </option>
                            ))}
                          </select>
                        </div>
                        <div className={`${shared.field} ${shared.formFull}`}>
                          <label htmlFor="quick-street" className={shared.fieldLabel}>
                            Số nhà, tên đường
                          </label>
                          <input
                            id="quick-street"
                            type="text"
                            className={shared.input}
                            value={guestCheckout.streetDetail}
                            onChange={(e) => setGuestCheckout({ streetDetail: e.target.value })}
                            placeholder="Số 128 Nguyễn Huệ"
                          />
                        </div>
                      </div>
                    ) : (
                      <div className={shared.field}>
                        <label htmlFor="quick-addr" className={shared.fieldLabel}>
                          Địa chỉ nhận hàng đã lưu
                        </label>
                        <select
                          id="quick-addr"
                          className={shared.select}
                          value={selectedAddress?.id ?? ""}
                          onChange={(e) => selectAddress(e.target.value)}
                        >
                          {addresses.map((a) => (
                            <option key={a.id} value={a.id}>
                              {a.recipientName} · {a.wardName}, {a.provinceName}
                            </option>
                          ))}
                        </select>
                      </div>
                    )}

                    <div className={shared.field}>
                      <span className={shared.fieldLabel}>Gói giao hàng</span>
                      <div className={styles.quickMethodGrid}>
                        <button
                          type="button"
                          className={`${styles.quickMethodBtn} ${
                            deliveryMethod === "standard" ? styles.quickMethodActive : ""
                          }`}
                          onClick={() => setDeliveryMethod("standard")}
                        >
                          Tiêu chuẩn (SPX/GHN)
                        </button>
                        <button
                          type="button"
                          className={`${styles.quickMethodBtn} ${
                            deliveryMethod === "fast" ? styles.quickMethodActive : ""
                          }`}
                          onClick={() => setDeliveryMethod("fast")}
                        >
                          Giao nhanh nội thành (+15k)
                        </button>
                      </div>
                    </div>

                    <div className={shared.field}>
                      <span className={shared.fieldLabel}>Phương thức thanh toán</span>
                      <div className={styles.quickMethodGrid}>
                        {(Object.keys(QUICK_PAY_META) as QuickPayChoice[]).map((key) => (
                          <button
                            key={key}
                            type="button"
                            className={`${styles.quickMethodBtn} ${
                              quickPayChoice === key ? styles.quickMethodActive : ""
                            }`}
                            onClick={() => setQuickPayChoice(key)}
                          >
                            {QUICK_PAY_META[key].shortLabel}
                          </button>
                        ))}
                      </div>
                    </div>

                    {quickError && (
                      <p className={shared.fieldError} role="alert">
                        {quickError}
                      </p>
                    )}
                  </div>
                )}
              </div>
            </div>

            {/* Bold dark total & primary CTA anchor */}
            <div className={styles.ctaBand}>
              <div className={styles.grandRow}>
                <span className={styles.grandLabel}>Tổng thanh toán</span>
                <span className={`${styles.grandAmount} num`}>
                  {formatVnd(cartSummary.grandTotal)}
                </span>
              </div>

              {cartSummary.totalDiscount > 0 && (
                <p className={styles.savingsNote}>
                  Đã tiết kiệm:{" "}
                  <strong className="num">{formatVnd(cartSummary.totalDiscount)}</strong>
                </p>
              )}

              {(cartSummary.estimatedCashbackCoins ?? 0) > 0 && (
                <p className={styles.cashbackNote}>
                  Tích thêm{" "}
                  <strong className="num">
                    +{formatInt(cartSummary.estimatedCashbackCoins ?? 0)} Xu
                  </strong>{" "}
                  (hạng {membershipTier.name} · hoàn {membershipTier.cashbackPercentLabel}, x2 Thứ
                  Sáu).
                </p>
              )}

              {cartSummary.selectedCount > 0 ? (
                <div className={styles.ctaStack}>
                  {quickCheckoutOpen ? (
                    <>
                      <Button
                        variant="primary"
                        size="lg"
                        fullWidth
                        className={shared.wrapBtn}
                        onClick={handleQuickOrderSubmit}
                      >
                        Đặt hàng ngay · {formatVnd(cartSummary.grandTotal)} (
                        {selectedQuickPay.shortLabel})
                      </Button>
                      <Link href="/thanh-toan" className={styles.darkSecondaryLink}>
                        Thanh toán đầy đủ (gói quà & hóa đơn VAT)
                      </Link>
                    </>
                  ) : (
                    <Button
                      variant="primary"
                      size="lg"
                      fullWidth
                      className={shared.wrapBtn}
                      href="/thanh-toan"
                    >
                      Thanh toán · {formatVnd(cartSummary.grandTotal)} (
                      {selectedQuickPay.shortLabel})
                    </Button>
                  )}
                </div>
              ) : (
                <div className={styles.ctaStack}>
                  <Button variant="primary" size="lg" fullWidth disabled>
                    Tiến hành thanh toán
                  </Button>
                  <p className={styles.ctaHint}>
                    Hãy chọn ít nhất 1 sản phẩm bên trái để tiếp tục thanh toán.
                  </p>
                </div>
              )}
            </div>
          </aside>
        </div>
      </section>

      <ProductAddonStrip
        title="Gợi ý mua kèm đạt mốc freeship"
        subtitle="Thêm nhanh để đạt miễn phí vận chuyển 249.000₫ hoặc mốc quà tặng 499.000₫"
        products={addonProducts}
        showTierHint
      />

      {/* Sticky mobile checkout bar (390px thumb zone) */}
      <div className={styles.mobileStickyBar} aria-label="Thanh toán nhanh">
        <div className={styles.mobileTotalWrap}>
          <span className={styles.mobileTotalLabel}>
            {selectedQuickPay.shortLabel} (<span className="num">{cartSummary.selectedCount}</span>)
          </span>
          <span className={`${styles.mobileTotalVal} num`}>
            {formatVnd(cartSummary.grandTotal)}
          </span>
        </div>
        {cartSummary.selectedCount > 0 ? (
          quickCheckoutOpen ? (
            <Button variant="primary" size="md" onClick={handleQuickOrderSubmit}>
              Đặt · {formatVnd(cartSummary.grandTotal)}
            </Button>
          ) : (
            <Button variant="primary" size="md" href="/thanh-toan">
              Thanh toán
            </Button>
          )
        ) : (
          <Button variant="primary" size="md" disabled>
            Chọn hàng
          </Button>
        )}
      </div>
    </div>
  );
}

export default function CartPage() {
  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang tải giỏ hàng NexusMart..." rows={4} />
        </div>
      }
    >
      <CartPageContent />
    </Suspense>
  );
}
