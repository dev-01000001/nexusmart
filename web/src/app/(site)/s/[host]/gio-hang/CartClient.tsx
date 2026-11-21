"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import type { Product } from "@/data/products";
import type { Shop } from "@/data/shops";
import type { Voucher } from "@/data/vouchers";
import { ApiError, api } from "@/lib/api";
import { formatInt, formatSignedVnd, formatVnd } from "@/lib/format";
import type { SitePublicShopDTO } from "@/lib/site/types";
import { useStore } from "@/context/StoreContext";
import { useSession } from "@/components/SessionProvider";
import {
  Button,
  IconMinus,
  IconPlus,
  IconTag,
  IconTrash,
  IconTruck,
  Price,
  StateView,
} from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import {
  enrichCartGroups,
  formatVoucherHeadline,
  formatVoucherIneligibleReason,
} from "@/features/commerce/cartEnrich";
import type { GwDiscountBreakdown, GwVoucher } from "@/features/commerce/types";
import shared from "@/features/commerce/commerce.module.css";
import { SiteRenderer } from "@/features/site/SiteRenderer";
import type { CartPageConfig, SiteTemplateConfig } from "@/features/site/templateTypes";
import styles from "./page.module.css";

export interface CartClientProps {
  brandName: string;
  catalogProducts: Product[];
  catalogShops: Shop[];
  initialVouchers: GwVoucher[];
  cartConfig?: CartPageConfig;
  templateConfig?: SiteTemplateConfig;
  shop?: SitePublicShopDTO;
  vouchers?: Voucher[];
}

export function CartClient({
  brandName,
  catalogProducts,
  catalogShops,
  initialVouchers,
  cartConfig,
  templateConfig,
  shop,
  vouchers: siteVouchers = [],
}: CartClientProps) {
  const { isAuthenticated, loading: sessionLoading } = useSession();
  const {
    cart,
    cartLoading,
    cartError,
    refreshCart,
    updateCartItemQty,
    removeCartItem,
    clearCart,
    selectedVoucherId,
    flashSaleItemBySku,
    setSelectedVoucher,
    saveVoucher,
    showToast,
  } = useStore();

  const cartLayout = cartConfig?.layout ?? templateConfig?.pages.cart.layout ?? "split-summary";
  const showVouchers =
    cartConfig?.show_vouchers ?? templateConfig?.pages.cart.show_vouchers ?? true;
  const showAddons = cartConfig?.show_addons ?? templateConfig?.pages.cart.show_addons ?? true;
  const trustNote =
    cartConfig?.trust_note?.trim() || templateConfig?.pages.cart.trust_note?.trim() || "";
  const extraSections = cartConfig?.sections ?? templateConfig?.pages.cart.sections ?? [];

  const [vouchers, setVouchers] = useState<GwVoucher[]>(initialVouchers);
  const [breakdown, setBreakdown] = useState<GwDiscountBreakdown | null>(null);
  const [previewLoading, setPreviewLoading] = useState<boolean>(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [busySkuId, setBusySkuId] = useState<string | null>(null);
  const [clearing, setClearing] = useState<boolean>(false);
  const [confirmClear, setConfirmClear] = useState<boolean>(false);
  const [manualCode, setManualCode] = useState<string>("");
  const [validatingCode, setValidatingCode] = useState<boolean>(false);
  const [codeFeedback, setCodeFeedback] = useState<{ ok: boolean; message: string } | null>(null);

  // Cart returned by BFF /api/cart is strictly scoped to this site's shop.
  const enrichedGroups = useMemo(
    () => enrichCartGroups(cart?.shop_groups ?? [], catalogProducts, catalogShops),
    [cart?.shop_groups, catalogProducts, catalogShops]
  );
  const activeGroup = enrichedGroups[0] ?? null;

  const cartItems = useMemo(() => activeGroup?.items ?? [], [activeGroup]);

  const addonProducts = useMemo(() => {
    const inCartProductIds = new Set(cartItems.map((c) => c.raw.product_id));
    const filtered = catalogProducts.filter(
      (p) => !inCartProductIds.has(p.backendId ?? p.id) && !inCartProductIds.has(p.id)
    );
    return (filtered.length >= 4 ? filtered : catalogProducts).slice(0, 4);
  }, [cartItems, catalogProducts]);

  useRevealOnScroll(
    `cart-${isAuthenticated ? "auth" : "guest"}-${cartItems.length}-${enrichedGroups.length}`
  );

  // Refresh shop vouchers on this host when authenticated (BFF resolves shop from Host).
  const loadShopVouchers = useCallback(async () => {
    if (!isAuthenticated || !showVouchers) return;
    try {
      const vRes = await api<{ vouchers: GwVoucher[] }>("/api/vouchers");
      if (Array.isArray(vRes.vouchers)) {
        setVouchers(vRes.vouchers.filter((v) => v.scope.toUpperCase() === "SHOP" && v.is_active));
      }
    } catch {
      // Keep SSR shop vouchers
    }
  }, [isAuthenticated, showVouchers]);

  useEffect(() => {
    void loadShopVouchers();
  }, [loadShopVouchers]);

  const flashKey = JSON.stringify(flashSaleItemBySku);

  // Authoritative promotion preview scoped to this host's shop only (no shop_id/platform/coins).
  useEffect(() => {
    if (!isAuthenticated || !activeGroup || activeGroup.items.length === 0) {
      setBreakdown(null);
      setPreviewError(null);
      return;
    }

    let cancelled = false;
    setPreviewLoading(true);
    setPreviewError(null);

    api<{ breakdown: GwDiscountBreakdown; warnings_vi?: string[] }>("/api/vouchers/preview", {
      method: "POST",
      body: {
        shop_voucher_id: showVouchers ? selectedVoucherId || "" : "",
        shipping_fee_vnd: 0,
        flash_sale_item_by_sku: flashSaleItemBySku,
      },
    })
      .then((res) => {
        if (!cancelled) setBreakdown(res.breakdown);
      })
      .catch((err) => {
        if (!cancelled) {
          setPreviewError(
            err instanceof ApiError
              ? err.message
              : "Chưa thể tính ưu đãi lúc này. Số tiền sẽ được xác nhận lại ở bước thanh toán."
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
  }, [isAuthenticated, activeGroup, selectedVoucherId, flashKey, showVouchers]);

  const shopVouchers = useMemo(
    () => vouchers.filter((v) => v.scope.toUpperCase() === "SHOP" && v.is_active),
    [vouchers]
  );

  const subtotal = breakdown?.total_subtotal_vnd ?? activeGroup?.raw.subtotal_vnd ?? 0;
  const flashDiscount = breakdown?.total_flash_sale_discount_vnd ?? 0;
  const shopVoucherDiscount = breakdown?.total_shop_voucher_discount_vnd ?? 0;
  const totalDiscount = breakdown?.total_discount_vnd ?? 0;
  const grandTotal = breakdown?.final_payable_vnd ?? subtotal;
  const previewWarnings = breakdown?.warnings ?? [];

  const totalItemQty = cart?.total_items ?? cartItems.reduce((sum, it) => sum + it.raw.quantity, 0);
  const hasStockIssue = useMemo(
    () => cartItems.some((it) => it.isOutOfStock || it.exceedsStock),
    [cartItems]
  );

  const handleQtyChange = async (skuId: string, nextQty: number) => {
    if (busySkuId) return;
    setBusySkuId(skuId);
    try {
      await updateCartItemQty(skuId, nextQty);
    } finally {
      setBusySkuId(null);
    }
  };

  const handleRemove = async (skuId: string, name: string) => {
    if (busySkuId) return;
    setBusySkuId(skuId);
    try {
      const res = await removeCartItem(skuId);
      if (res.ok) showToast(`Đã bỏ ${name} khỏi giỏ hàng.`);
    } finally {
      setBusySkuId(null);
    }
  };

  const handleClearAll = async () => {
    if (clearing) return;
    setClearing(true);
    try {
      const ok = await clearCart();
      if (ok) {
        setConfirmClear(false);
        showToast(`Đã xóa toàn bộ sản phẩm trong giỏ hàng của ${brandName}.`);
      }
    } finally {
      setClearing(false);
    }
  };

  const handleApplyManualCode = async (e: React.FormEvent) => {
    e.preventDefault();
    const clean = manualCode.trim().toUpperCase();
    if (!clean) {
      setCodeFeedback({ ok: false, message: "Vui lòng nhập mã ưu đãi cần áp dụng." });
      return;
    }
    if (validatingCode) return;
    setValidatingCode(true);
    setCodeFeedback(null);
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
          shop_voucher_id: selectedVoucherId || "",
          shipping_fee_vnd: 0,
          flash_sale_item_by_sku: flashSaleItemBySku,
        },
      });

      if (!res.valid || !res.voucher) {
        setCodeFeedback({
          ok: false,
          message: res.reason || `Mã ${clean} chưa đủ điều kiện áp dụng tại ${brandName}.`,
        });
        return;
      }

      setSelectedVoucher(res.voucher.id);
      if (res.breakdown) {
        setBreakdown(res.breakdown);
      }

      const discText = res.discount_vnd > 0 ? ` (giảm ${formatVnd(res.discount_vnd)})` : "";
      setCodeFeedback({
        ok: true,
        message: `Đã áp dụng mã ${res.voucher.code}${discText}.`,
      });
      setManualCode("");
    } catch (err) {
      setCodeFeedback({
        ok: false,
        message: err instanceof ApiError ? err.message : "Chưa thể kiểm tra mã ưu đãi lúc này.",
      });
    } finally {
      setValidatingCode(false);
    }
  };

  const handleSaveVoucher = async (voucher: GwVoucher) => {
    const res = await saveVoucher(voucher.id);
    if (res.ok) {
      setVouchers((prev) =>
        prev.map((item) => (item.id === voucher.id ? { ...item, saved: true } : item))
      );
    }
  };

  const renderExtraSections = () =>
    templateConfig && extraSections.length > 0 ? (
      <SiteRenderer
        config={templateConfig}
        shop={shop ?? { id: "", name: brandName }}
        products={catalogProducts}
        vouchers={siteVouchers}
        sectionsOverride={extraSections}
      />
    ) : null;

  const renderHeader = (subtitle: string) => (
    <section className={shared.pageHeader}>
      <div className={shared.headerLeadGroup}>
        <h1 className={`${shared.pageTitle} lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Giỏ hàng · {brandName}
            </span>
          </span>
        </h1>
        <p className={shared.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          {subtitle}
        </p>
        {trustNote && <p className={styles.cartTrustNote}>{trustNote}</p>}
      </div>
      <div className={shared.headerAside} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
        <Button variant="secondary" size="md" href="/san-pham">
          Tiếp tục chọn sản phẩm
        </Button>
      </div>
    </section>
  );

  if (sessionLoading || (isAuthenticated && cartLoading && !cart)) {
    return (
      <div className={`${shared.page} ${styles.cartShell}`} data-cart-layout={cartLayout}>
        {renderHeader("Đang cập nhật sản phẩm, tồn kho và mã ưu đãi trong giỏ hàng của bạn.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Danh sách sản phẩm</h2>
          <StateView kind="loading" title="Đang tải giỏ hàng của bạn..." rows={4} />
        </section>
        {renderExtraSections()}
        {showAddons && addonProducts.length > 0 && (
          <ProductAddonStrip
            title={`Sản phẩm của ${brandName}`}
            subtitle="Các mẫu đang sẵn hàng tại kho của cửa hàng"
            products={addonProducts}
          />
        )}
      </div>
    );
  }

  if (!isAuthenticated) {
    return (
      <div className={`${shared.page} ${styles.cartShell}`} data-cart-layout={cartLayout}>
        {renderHeader(
          `Đăng nhập tài khoản tại ${brandName} để xem giỏ hàng, giữ số lượng tồn kho và áp mã ưu đãi.`
        )}
        <section
          className={shared.section}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <h2 className={shared.sectionTitle}>Đăng nhập để tiếp tục</h2>
          <StateView
            kind="empty"
            title="Bạn chưa đăng nhập"
            message={`Giỏ hàng của bạn tại ${brandName} được lưu an toàn theo tài khoản. Đăng nhập để xem giỏ hàng và tiến hành đặt đơn.`}
            actionLabel="Đăng nhập ngay"
            actionHref="/dang-nhap?next=%2Fgio-hang"
          />
          <div className={shared.formActions} style={{ marginTop: 16 }}>
            <Button variant="secondary" size="md" href="/dang-ky?next=%2Fgio-hang">
              Tạo tài khoản mới
            </Button>
            <Button variant="secondary" size="md" href="/san-pham">
              Xem sản phẩm của {brandName}
            </Button>
          </div>
        </section>
        {renderExtraSections()}
        {showAddons && addonProducts.length > 0 && (
          <ProductAddonStrip
            title={`Sản phẩm nổi bật của ${brandName}`}
            subtitle="Sản phẩm trực tiếp từ kho của cửa hàng"
            products={addonProducts}
          />
        )}
      </div>
    );
  }

  if (cartError && !cart) {
    return (
      <div className={`${shared.page} ${styles.cartShell}`} data-cart-layout={cartLayout}>
        {renderHeader("Chưa thể kết nối đến giỏ hàng lúc này. Sản phẩm bạn đã chọn vẫn được giữ.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Trạng thái giỏ hàng</h2>
          <StateView
            kind="error"
            title="Chưa thể tải giỏ hàng"
            message={cartError}
            actionLabel="Thử tải lại"
            onAction={() => void refreshCart()}
          />
        </section>
        {renderExtraSections()}
        {showAddons && addonProducts.length > 0 && (
          <ProductAddonStrip title={`Sản phẩm của ${brandName}`} products={addonProducts} />
        )}
      </div>
    );
  }

  if (!activeGroup || cartItems.length === 0) {
    return (
      <div className={`${shared.page} ${styles.cartShell}`} data-cart-layout={cartLayout}>
        {renderHeader(`Bạn chưa có sản phẩm nào của ${brandName} trong giỏ hàng.`)}
        <section
          className={shared.section}
          data-rise=""
          style={{ "--i": 3 } as React.CSSProperties}
        >
          <h2 className={shared.sectionTitle}>Sản phẩm trong giỏ</h2>
          <StateView
            kind="empty"
            title="Giỏ hàng đang trống"
            message={`Chọn sản phẩm yêu thích tại ${brandName} để thêm vào giỏ và đặt hàng.`}
            actionLabel={`Xem sản phẩm của ${brandName}`}
            actionHref="/san-pham"
          />
        </section>
        {renderExtraSections()}
        {showAddons && addonProducts.length > 0 && (
          <ProductAddonStrip
            title={`Sản phẩm từ ${brandName}`}
            subtitle="Thêm nhanh theo size trực tiếp từ thẻ sản phẩm"
            products={addonProducts}
          />
        )}
      </div>
    );
  }

  return (
    <div
      className={`${shared.page} ${styles.mobileSpacer} ${styles.cartShell}`}
      data-cart-layout={cartLayout}
    >
      {renderHeader(
        `Kiểm tra số lượng, áp mã ưu đãi riêng của ${brandName} và tiến hành đặt hàng.`
      )}

      <section className={shared.section}>
        <div className={styles.cartLayout}>
          <div className={styles.leftCol}>
            <div
              className={styles.cartToolbar}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <div className={styles.selectBar}>
                <span>
                  Đang có <strong className="num">{totalItemQty}</strong> sản phẩm trong giỏ của{" "}
                  <strong>{brandName}</strong>
                </span>

                {confirmClear ? (
                  <div className={shared.formActions}>
                    <Button variant="danger" size="sm" disabled={clearing} onClick={handleClearAll}>
                      {clearing ? "Đang xóa..." : "Xác nhận xóa"}
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
            </div>

            <article
              className={styles.shopGroup}
              data-rise=""
              style={{ "--i": 3 } as React.CSSProperties}
            >
              <div className={styles.shopHeader}>
                <div className={styles.shopIdentity}>
                  <span
                    className={styles.shopDot}
                    style={{ background: "var(--accent)" }}
                    aria-hidden="true"
                  />
                  <Link href="/" className={styles.shopLink}>
                    {brandName}
                  </Link>
                  {activeGroup.shopCity && (
                    <span className={styles.shopCity}>{activeGroup.shopCity}</span>
                  )}
                </div>

                <Link href="/san-pham" className={shared.breadcrumbLink}>
                  Chọn thêm sản phẩm
                </Link>
              </div>

              <ul className={styles.itemList}>
                {cartItems.map((item, itemIdx) => {
                  const raw = item.raw;
                  const lineTotal = raw.price_vnd * raw.quantity;
                  const isBusy = busySkuId === raw.sku_id;
                  const isFlash = Boolean(flashSaleItemBySku[raw.sku_id]);

                  return (
                    <li
                      key={raw.sku_id}
                      className={styles.itemRow}
                      data-rise=""
                      style={{ "--i": Math.min(itemIdx + 3, 6) } as React.CSSProperties}
                    >
                      <div className={styles.itemCheck} aria-hidden="true" />

                      <Link href={`/san-pham/${item.productSlug}`} className={styles.itemThumbLink}>
                        <img
                          src={item.image}
                          alt={item.productName}
                          width={896}
                          height={1200}
                          className={styles.itemThumb}
                        />
                      </Link>

                      <div className={styles.itemInfo}>
                        <Link href={`/san-pham/${item.productSlug}`} className={styles.itemName}>
                          {item.productName}
                        </Link>
                        <p className={styles.itemVariant}>{item.variantLabel}</p>
                        <div className={styles.itemTags}>
                          {isFlash && (
                            <span className={`${shared.statusTag} ${shared.toneWarn}`}>
                              Ưu đãi Flash sale
                            </span>
                          )}
                          {raw.price_changed && (
                            <span className={`${shared.statusTag} ${shared.toneWarn}`}>
                              Giá vừa cập nhật (trước đó {formatVnd(raw.added_price_vnd)})
                            </span>
                          )}
                          {item.isOutOfStock && (
                            <span className={`${shared.statusTag} ${shared.toneNeg}`}>
                              Tạm hết hàng
                            </span>
                          )}
                          {item.exceedsStock && (
                            <span className={`${shared.statusTag} ${shared.toneNeg}`}>
                              Chỉ còn {raw.available_stock} sản phẩm trong kho
                            </span>
                          )}
                        </div>
                        <Price amount={raw.price_vnd} size="sm" />
                        <p className={styles.itemStock}>
                          {item.isOutOfStock ? (
                            "Hiện tạm hết hàng, vui lòng xóa để tiếp tục đặt đơn"
                          ) : (
                            <>
                              Còn <span className="num">{raw.available_stock}</span> sản phẩm sẵn
                              giao
                              {raw.quantity >= raw.available_stock ? " (đã đạt tối đa)" : ""}
                            </>
                          )}
                        </p>
                      </div>

                      <div className={styles.itemActionsCol}>
                        <span className={`${styles.lineSubtotal} num`}>{formatVnd(lineTotal)}</span>

                        <div className={styles.stepper} aria-label="Điều chỉnh số lượng">
                          <button
                            type="button"
                            className={styles.stepBtn}
                            disabled={isBusy}
                            onClick={() => handleQtyChange(raw.sku_id, raw.quantity - 1)}
                            aria-label={`Giảm số lượng ${item.productName}`}
                          >
                            <IconMinus size={14} />
                          </button>
                          <span className={`${styles.stepQty} num`}>{raw.quantity}</span>
                          <button
                            type="button"
                            className={styles.stepBtn}
                            disabled={
                              isBusy || item.isOutOfStock || raw.quantity >= raw.available_stock
                            }
                            onClick={() => handleQtyChange(raw.sku_id, raw.quantity + 1)}
                            aria-label={`Tăng số lượng ${item.productName}`}
                          >
                            <IconPlus size={14} />
                          </button>
                        </div>

                        <button
                          type="button"
                          className={styles.removeBtn}
                          disabled={isBusy}
                          onClick={() => handleRemove(raw.sku_id, item.productName)}
                        >
                          <IconTrash size={14} />
                          <span>Xóa</span>
                        </button>
                      </div>
                    </li>
                  );
                })}
              </ul>

              {showVouchers && shopVouchers.length > 0 && (
                <div className={styles.shopVoucherBar}>
                  <div className={styles.shopVoucherHead}>
                    <span className={styles.shopVoucherTitle}>
                      <IconTag size={15} />
                      <span>Ưu đãi của {brandName}</span>
                    </span>
                    {shopVoucherDiscount > 0 && (
                      <span className={`${styles.breakdownDiscount} num`}>
                        {formatSignedVnd(-shopVoucherDiscount)}
                      </span>
                    )}
                  </div>

                  <div className={styles.voucherOptions}>
                    {shopVouchers.map((voucher) => {
                      const reason = formatVoucherIneligibleReason(voucher, subtotal);
                      const eligible = reason === null;
                      const isSelected =
                        (selectedVoucherId === voucher.id || selectedVoucherId === voucher.code) &&
                        eligible;

                      return (
                        <button
                          key={voucher.id}
                          type="button"
                          className={`${styles.voucherOption} ${
                            isSelected ? styles.voucherOptionActive : ""
                          } ${!eligible ? styles.voucherOptionDisabled : ""}`}
                          disabled={!eligible}
                          onClick={() => setSelectedVoucher(isSelected ? null : voucher.id)}
                        >
                          <div className={styles.voucherCopy}>
                            <span className={styles.voucherCodeLine}>
                              {voucher.code} · {formatVoucherHeadline(voucher)}
                            </span>
                            <span className={styles.voucherDesc}>
                              {eligible
                                ? `${voucher.name || `Áp dụng tại ${brandName}`} · Còn ${formatInt(voucher.remaining_quota)} lượt`
                                : reason}
                            </span>
                          </div>
                          <span className={styles.voucherActionLabel}>
                            {isSelected
                              ? "Đang dùng"
                              : eligible
                                ? "Áp dụng"
                                : voucher.remaining_quota <= 0
                                  ? "Hết lượt"
                                  : "Chưa đủ"}
                          </span>
                        </button>
                      );
                    })}
                  </div>

                  {shopVouchers.some((v) => !v.saved && v.remaining_quota > 0) && (
                    <div className={shared.formActions} style={{ marginTop: 12 }}>
                      {shopVouchers
                        .filter((v) => !v.saved && v.remaining_quota > 0)
                        .slice(0, 2)
                        .map((v) => (
                          <Button
                            key={`save-${v.id}`}
                            variant="secondary"
                            size="sm"
                            onClick={() => void handleSaveVoucher(v)}
                          >
                            Lưu mã {v.code}
                          </Button>
                        ))}
                    </div>
                  )}
                </div>
              )}
            </article>
          </div>

          <aside
            className={styles.summaryCol}
            aria-label="Tóm tắt đơn hàng và mã ưu đãi"
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <div className={styles.summaryInner}>
              <h2 className={styles.summaryHeading}>Tóm tắt đơn hàng</h2>

              <div className={styles.destBanner}>
                <IconTruck size={18} className={styles.destIcon} />
                <div>
                  <p>
                    {trustNote ||
                      "Phí vận chuyển chính xác sẽ được tính theo địa chỉ nhận hàng ở bước thanh toán."}
                  </p>
                  <p className={styles.coinMeta}>Hỗ trợ đồng kiểm · Thanh toán COD hoặc VietQR.</p>
                </div>
              </div>

              {showVouchers && (
                <form className={styles.summaryBlock} onSubmit={handleApplyManualCode} noValidate>
                  <label htmlFor="cart-manual-code" className={styles.blockHeading}>
                    Nhập mã giảm giá của {brandName}
                  </label>
                  <div className={shared.codeInputRow}>
                    <input
                      id="cart-manual-code"
                      type="text"
                      className={shared.input}
                      value={manualCode}
                      onChange={(e) => setManualCode(e.target.value)}
                      placeholder="Nhập mã ưu đãi của cửa hàng"
                    />
                    <Button type="submit" variant="ink" size="md" disabled={validatingCode}>
                      {validatingCode ? "Đang kiểm..." : "Áp mã"}
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
                </form>
              )}

              {(previewError || previewWarnings.length > 0) && (
                <div className={`${shared.feedbackBox} ${shared.feedbackWarn}`} role="status">
                  {previewError ?? previewWarnings[0]}
                </div>
              )}

              <dl className={styles.breakdown}>
                <div className={styles.breakdownRow}>
                  <dt className={styles.breakdownLabel}>
                    Tạm tính (<span className="num">{totalItemQty}</span> sản phẩm)
                  </dt>
                  <dd className="num">{formatVnd(subtotal)}</dd>
                </div>

                {flashDiscount > 0 && (
                  <div className={styles.breakdownRow}>
                    <dt className={styles.breakdownLabel}>Ưu đãi Flash sale</dt>
                    <dd className={`${styles.breakdownDiscount} num`}>
                      {formatSignedVnd(-flashDiscount)}
                    </dd>
                  </div>
                )}

                {showVouchers && (
                  <div className={styles.breakdownRow}>
                    <dt className={styles.breakdownLabel}>Mã giảm giá cửa hàng</dt>
                    <dd className={`${styles.breakdownDiscount} num`}>
                      {shopVoucherDiscount > 0 ? formatSignedVnd(-shopVoucherDiscount) : "0₫"}
                    </dd>
                  </div>
                )}
              </dl>
            </div>

            <div className={styles.ctaBand}>
              <div className={styles.grandRow}>
                <span className={styles.grandLabel}>
                  {previewLoading ? "Đang tính lại..." : "Tạm tính thanh toán"}
                </span>
                <span className={`${styles.grandAmount} num`}>{formatVnd(grandTotal)}</span>
              </div>

              {totalDiscount > 0 && (
                <p className={styles.savingsNote}>
                  Đã tiết kiệm: <strong className="num">{formatVnd(totalDiscount)}</strong>
                </p>
              )}

              <p className={styles.cashbackNote}>
                Phí giao hàng sẽ được cộng chính xác sau khi chọn địa chỉ nhận hàng.
              </p>

              {hasStockIssue ? (
                <div className={styles.ctaStack}>
                  <Button variant="primary" size="lg" fullWidth disabled>
                    Tiến hành thanh toán
                  </Button>
                  <p className={styles.ctaHint}>
                    Có sản phẩm hết hàng hoặc vượt số lượng kho. Vui lòng điều chỉnh trước khi thanh
                    toán.
                  </p>
                </div>
              ) : (
                <div className={styles.ctaStack}>
                  <Button
                    variant="primary"
                    size="lg"
                    fullWidth
                    className={shared.wrapBtn}
                    href="/thanh-toan"
                  >
                    Tiến hành thanh toán · {formatVnd(grandTotal)}
                  </Button>
                </div>
              )}
            </div>
          </aside>
        </div>
      </section>

      {renderExtraSections()}

      {showAddons && addonProducts.length > 0 && (
        <ProductAddonStrip
          title={`Sản phẩm khác từ ${brandName}`}
          subtitle="Chọn thêm sản phẩm giao cùng đơn từ cửa hàng"
          products={addonProducts}
        />
      )}

      <div className={styles.mobileStickyBar} aria-label="Thanh toán nhanh">
        <div className={styles.mobileTotalWrap}>
          <span className={styles.mobileTotalLabel}>
            Tạm tính (<span className="num">{totalItemQty}</span> sản phẩm)
          </span>
          <span className={`${styles.mobileTotalVal} num`}>{formatVnd(grandTotal)}</span>
        </div>
        {hasStockIssue ? (
          <Button variant="primary" size="md" disabled>
            Kiểm tra tồn
          </Button>
        ) : (
          <Button variant="primary" size="md" href="/thanh-toan">
            Thanh toán
          </Button>
        )}
      </div>
    </div>
  );
}
