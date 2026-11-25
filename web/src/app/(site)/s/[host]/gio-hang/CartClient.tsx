"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import type { Product } from "@/data/products";
import type { Shop } from "@/data/shops";
import type { Voucher } from "@/data/vouchers";
import { ApiError, api } from "@/lib/api";
import { formatSignedVnd, formatVnd } from "@/lib/format";
import type { SitePublicShopDTO } from "@/lib/site/types";
import { useStore } from "@/context/StoreContext";
import { useSession } from "@/components/SessionProvider";
import {
  Button,
  IconCheck,
  IconClose,
  IconMinus,
  IconPlus,
  IconTrash,
  Price,
  StateView,
} from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import {
  enrichCartGroups,
  formatVoucherHeadline,
  formatVoucherIneligibleReason,
  shortVoucherHint,
  splitVariantParts,
} from "@/features/commerce/cartEnrich";
import type { GwDiscountBreakdown, GwVoucher } from "@/features/commerce/types";
import shared from "@/features/commerce/commerce.module.css";
import { SiteRenderer } from "@/features/site/SiteRenderer";
import type { CartPageConfig, SiteTemplateConfig } from "@/features/site/templateTypes";
import styles from "./page.module.css";

const UNDO_MS = 5000;

interface UndoItem {
  key: number;
  skuId: string;
  quantity: number;
  productName: string;
  flashSaleItemId?: string;
}

export interface CartClientProps {
  brandName: string;
  catalogProducts: Product[];
  catalogShops: Shop[];
  initialVouchers: GwVoucher[];
  cartConfig?: CartPageConfig;
  templateConfig?: SiteTemplateConfig;
  shop?: SitePublicShopDTO;
  vouchers?: Voucher[];
  serverNowMs?: number;
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
  serverNowMs,
}: CartClientProps) {
  const [anchorNowMs] = useState<number>(() => serverNowMs ?? Date.now());
  const { isAuthenticated, loading: sessionLoading } = useSession();
  const {
    cart,
    cartLoading,
    cartError,
    refreshCart,
    addToCart,
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
  const [savingVoucherId, setSavingVoucherId] = useState<string | null>(null);
  const [breakdown, setBreakdown] = useState<GwDiscountBreakdown | null>(null);
  const [previewLoading, setPreviewLoading] = useState<boolean>(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [busySkuId, setBusySkuId] = useState<string | null>(null);
  const [undoEntry, setUndoEntry] = useState<UndoItem | null>(null);
  const [undoing, setUndoing] = useState<boolean>(false);
  const undoTimerRef = useRef<number | null>(null);
  const mountedRef = useRef(true);

  const [clearing, setClearing] = useState<boolean>(false);
  const [confirmClear, setConfirmClear] = useState<boolean>(false);
  const [manualCode, setManualCode] = useState<string>("");
  const [validatingCode, setValidatingCode] = useState<boolean>(false);
  const [codeFeedback, setCodeFeedback] = useState<{ ok: boolean; message: string } | null>(null);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      if (undoTimerRef.current !== null) window.clearTimeout(undoTimerRef.current);
    };
  }, []);

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
          setPreviewError(err instanceof ApiError ? err.message : "Chưa tính được ưu đãi lúc này.");
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
  const isCartMutating = Boolean(busySkuId || clearing);

  const startUndoWindow = (item: Omit<UndoItem, "key">) => {
    if (!mountedRef.current) return;
    if (undoTimerRef.current !== null) window.clearTimeout(undoTimerRef.current);
    setUndoEntry({ ...item, key: Date.now() });
    undoTimerRef.current = window.setTimeout(() => {
      if (!mountedRef.current) return;
      setUndoEntry(null);
      undoTimerRef.current = null;
    }, UNDO_MS);
  };

  const dismissUndo = () => {
    if (undoTimerRef.current !== null) window.clearTimeout(undoTimerRef.current);
    undoTimerRef.current = null;
    setUndoEntry(null);
  };

  const handleRemoveWithUndo = async (skuId: string, quantity: number, productName: string) => {
    if (busySkuId || clearing) return;
    const savedFlashId = flashSaleItemBySku[skuId];
    setBusySkuId(skuId);
    try {
      const res = await removeCartItem(skuId);
      if (res.ok && mountedRef.current) {
        startUndoWindow({ skuId, quantity, productName, flashSaleItemId: savedFlashId });
      }
    } finally {
      if (mountedRef.current) {
        setBusySkuId(null);
      }
    }
  };

  const handleUndo = async () => {
    if (!undoEntry || undoing || clearing) return;
    const snapshot = undoEntry;
    setUndoing(true);
    dismissUndo();
    try {
      await addToCart({
        skuId: snapshot.skuId,
        quantity: snapshot.quantity,
        productName: snapshot.productName,
        flashSaleItemId: snapshot.flashSaleItemId,
      });
    } finally {
      if (mountedRef.current) setUndoing(false);
    }
  };

  const handleQtyChange = async (
    skuId: string,
    currentQty: number,
    nextQty: number,
    productName: string
  ) => {
    if (busySkuId || clearing) return;
    if (nextQty <= 0) {
      await handleRemoveWithUndo(skuId, currentQty, productName);
      return;
    }
    setBusySkuId(skuId);
    try {
      await updateCartItemQty(skuId, nextQty);
    } finally {
      if (mountedRef.current) setBusySkuId(null);
    }
  };

  const handleClearAll = async () => {
    if (clearing || busySkuId) return;
    setClearing(true);
    try {
      const ok = await clearCart();
      if (ok && mountedRef.current) {
        setConfirmClear(false);
        dismissUndo();
        showToast("Đã làm trống giỏ hàng.");
      }
    } finally {
      if (mountedRef.current) setClearing(false);
    }
  };

  const handleSaveVoucher = async (voucher: GwVoucher) => {
    if (savingVoucherId) return;
    setSavingVoucherId(voucher.id);
    try {
      const res = await saveVoucher(voucher.id);
      if (res.ok && mountedRef.current) {
        setVouchers((prev) => prev.map((v) => (v.id === voucher.id ? { ...v, saved: true } : v)));
      }
    } finally {
      if (mountedRef.current) setSavingVoucherId(null);
    }
  };

  const handleApplyManualCode = async (e: React.FormEvent) => {
    e.preventDefault();
    const clean = manualCode.trim().toUpperCase();
    if (!clean) {
      setCodeFeedback({ ok: false, message: "Nhập mã ưu đãi trước khi áp dụng." });
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
          message: res.reason || `Mã ${clean} chưa đủ điều kiện áp dụng.`,
        });
        return;
      }

      setSelectedVoucher(res.voucher.id);
      if (res.breakdown) {
        setBreakdown(res.breakdown);
      }

      const discText = res.discount_vnd > 0 ? ` (-${formatVnd(res.discount_vnd)})` : "";
      setCodeFeedback({
        ok: true,
        message: `Đã áp mã ${res.voucher.code}${discText}.`,
      });
      setManualCode("");
    } catch (err) {
      setCodeFeedback({
        ok: false,
        message: err instanceof ApiError ? err.message : "Chưa kiểm tra được mã ưu đãi.",
      });
    } finally {
      setValidatingCode(false);
    }
  };

  const renderUndoBar = () =>
    undoEntry ? (
      <div key={undoEntry.key} className={styles.undoBar} role="status" aria-live="polite">
        <span className={styles.undoText}>
          Đã xóa <strong>{undoEntry.productName}</strong>.
        </span>
        <div className={styles.undoActions}>
          <button
            type="button"
            className={styles.undoBtn}
            disabled={undoing}
            onClick={() => void handleUndo()}
          >
            Hoàn tác
          </button>
          <button
            type="button"
            className={styles.undoDismiss}
            aria-label="Đóng thông báo hoàn tác"
            onClick={dismissUndo}
          >
            <IconClose size={16} />
          </button>
        </div>
        <div className={styles.undoProgress} aria-hidden="true">
          <span className={styles.undoProgressFill} />
        </div>
      </div>
    ) : null;

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

  const renderHeader = (note?: string) => (
    <section className={shared.pageHeader}>
      <div className={shared.headerLeadGroup}>
        <h1 className={`${shared.pageTitle} lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Giỏ hàng
            </span>
          </span>
        </h1>
        {note && (
          <p className={shared.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
            {note}
          </p>
        )}
      </div>
      <div className={shared.headerAside} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
        <Button variant="secondary" size="md" href="/san-pham">
          Tiếp tục mua
        </Button>
      </div>
    </section>
  );

  if (sessionLoading || (isAuthenticated && cartLoading && !cart)) {
    return (
      <div className={`${shared.page} ${styles.cartShell}`} data-cart-layout={cartLayout}>
        {renderHeader(brandName)}
        <section className={shared.section}>
          <StateView kind="loading" skeletonVariant="table" title="Đang tải giỏ hàng..." rows={3} />
        </section>
      </div>
    );
  }

  if (!isAuthenticated) {
    return (
      <div className={`${shared.page} ${styles.cartShell}`} data-cart-layout={cartLayout}>
        {renderHeader(brandName)}
        <section className={shared.section}>
          <StateView
            kind="empty"
            title="Đăng nhập để xem giỏ hàng của bạn."
            actionLabel="Đăng nhập"
            actionHref="/dang-nhap?next=%2Fgio-hang"
          />
        </section>
        {renderExtraSections()}
        {showAddons && addonProducts.length > 0 && (
          <ProductAddonStrip title={`Sản phẩm tại ${brandName}`} products={addonProducts} />
        )}
      </div>
    );
  }

  if (cartError && !cart) {
    return (
      <div className={`${shared.page} ${styles.cartShell}`} data-cart-layout={cartLayout}>
        {renderHeader(brandName)}
        <section className={shared.section}>
          <StateView
            kind="error"
            title="Chưa thể tải giỏ hàng."
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
      <div className={`${shared.page} ${styles.cartShell}`} data-cart-layout={cartLayout}>
        {renderHeader(brandName)}
        {renderUndoBar()}
        <section className={shared.section}>
          <StateView
            kind="empty"
            title="Giỏ hàng đang trống."
            actionLabel="Xem sản phẩm"
            actionHref="/san-pham"
          />
        </section>
        {renderExtraSections()}
        {showAddons && addonProducts.length > 0 && (
          <ProductAddonStrip title={`Gợi ý từ ${brandName}`} products={addonProducts} />
        )}
      </div>
    );
  }

  return (
    <div
      className={`${shared.page} ${styles.mobileSpacer} ${styles.cartShell}`}
      data-cart-layout={cartLayout}
    >
      {renderHeader(`${totalItemQty} sản phẩm tại ${brandName}`)}

      {renderUndoBar()}

      <section className={shared.section}>
        <div className={styles.cartLayout}>
          <div className={styles.leftCol}>
            <article className={styles.shopGroup}>
              <ul className={styles.itemList}>
                {cartItems.map((item) => {
                  const raw = item.raw;
                  const lineTotal = raw.price_vnd * raw.quantity;
                  const isBusy = Boolean(busySkuId) || clearing;
                  const isFlash = Boolean(flashSaleItemBySku[raw.sku_id]);
                  const showLowStock =
                    !item.isOutOfStock && !item.exceedsStock && raw.available_stock <= 5;
                  const variantParts = splitVariantParts(item.variantLabel);

                  return (
                    <li key={raw.sku_id} className={styles.itemSlot}>
                      <div className={styles.itemRow}>
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
                          <Link href={`/san-pham/${item.productSlug}`} className={styles.itemName}>
                            {item.productName}
                          </Link>
                          <p className={styles.itemVariant}>
                            {variantParts.map((part) => (
                              <span key={part}>{part}</span>
                            ))}
                          </p>
                          {raw.quantity > 1 && <Price amount={raw.price_vnd} size="sm" />}

                          {(isFlash ||
                            raw.price_changed ||
                            item.isOutOfStock ||
                            item.exceedsStock ||
                            showLowStock) && (
                            <div className={styles.itemTags}>
                              {isFlash && (
                                <span className={`${shared.statusTag} ${shared.toneWarn}`}>
                                  Flash sale
                                </span>
                              )}
                              {raw.price_changed && (
                                <span className={`${shared.statusTag} ${shared.toneWarn}`}>
                                  Giá mới (trước {formatVnd(raw.added_price_vnd)})
                                </span>
                              )}
                              {item.isOutOfStock && (
                                <span className={`${shared.statusTag} ${shared.toneNeg}`}>
                                  Hết hàng
                                </span>
                              )}
                              {item.exceedsStock && (
                                <span className={`${shared.statusTag} ${shared.toneNeg}`}>
                                  Chỉ còn <span className="num">{raw.available_stock}</span>
                                </span>
                              )}
                              {showLowStock && (
                                <span className={`${shared.statusTag} ${shared.toneWarn}`}>
                                  Còn <span className="num">{raw.available_stock}</span>
                                </span>
                              )}
                            </div>
                          )}
                        </div>

                        <div className={styles.itemActionsCol}>
                          <span className={`${styles.lineSubtotal} num`}>
                            {formatVnd(lineTotal)}
                          </span>

                          <div className={styles.stepper} aria-label="Điều chỉnh số lượng">
                            <button
                              type="button"
                              className={styles.stepBtn}
                              disabled={isBusy}
                              onClick={() =>
                                void handleQtyChange(
                                  raw.sku_id,
                                  raw.quantity,
                                  raw.quantity - 1,
                                  item.productName
                                )
                              }
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
                              onClick={() =>
                                void handleQtyChange(
                                  raw.sku_id,
                                  raw.quantity,
                                  raw.quantity + 1,
                                  item.productName
                                )
                              }
                              aria-label={`Tăng số lượng ${item.productName}`}
                            >
                              <IconPlus size={14} />
                            </button>
                          </div>

                          <button
                            type="button"
                            className={styles.removeBtn}
                            disabled={isBusy}
                            onClick={() =>
                              void handleRemoveWithUndo(raw.sku_id, raw.quantity, item.productName)
                            }
                          >
                            <IconTrash size={14} />
                            <span>Xóa</span>
                          </button>
                        </div>
                      </div>
                    </li>
                  );
                })}
              </ul>

              <div className={styles.cartFooterBar}>
                {confirmClear ? (
                  <div className={shared.formActions}>
                    <Button
                      variant="danger"
                      size="sm"
                      loading={clearing}
                      loadingLabel="Đang xóa"
                      disabled={Boolean(busySkuId)}
                      onClick={handleClearAll}
                    >
                      Xóa tất cả
                    </Button>
                    <Button variant="secondary" size="sm" onClick={() => setConfirmClear(false)}>
                      Giữ lại
                    </Button>
                  </div>
                ) : (
                  <button
                    type="button"
                    className={styles.clearBtn}
                    disabled={Boolean(busySkuId)}
                    onClick={() => setConfirmClear(true)}
                  >
                    Xóa giỏ hàng
                  </button>
                )}
              </div>
            </article>
          </div>

          <aside className={styles.summaryCol} aria-label="Tóm tắt đơn hàng và mã ưu đãi">
            <div className={styles.summaryInner}>
              <h2 className={styles.summaryHeading}>Tóm tắt đơn hàng</h2>
              {trustNote && <p className={styles.trustNote}>{trustNote}</p>}

              {showVouchers && (
                <div className={styles.summaryBlock}>
                  <form onSubmit={handleApplyManualCode} noValidate>
                    <label htmlFor="cart-manual-code" className={styles.blockHeading}>
                      Mã giảm giá
                    </label>
                    <div className={shared.codeInputRow} style={{ marginTop: 8 }}>
                      <input
                        id="cart-manual-code"
                        type="text"
                        className={shared.input}
                        aria-describedby={codeFeedback ? "cart-code-feedback" : undefined}
                        value={manualCode}
                        onChange={(e) => setManualCode(e.target.value)}
                        placeholder="Nhập mã giảm giá"
                      />
                      <Button
                        type="submit"
                        variant="ink"
                        size="md"
                        loading={validatingCode}
                        loadingLabel="Đang kiểm tra"
                      >
                        Áp mã
                      </Button>
                    </div>
                    {codeFeedback && (
                      <p
                        id="cart-code-feedback"
                        className={`${shared.feedbackBox} ${
                          codeFeedback.ok ? shared.feedbackSuccess : shared.feedbackError
                        }`}
                        style={{ marginTop: 8 }}
                        role={codeFeedback.ok ? "status" : "alert"}
                      >
                        {codeFeedback.message}
                      </p>
                    )}
                  </form>

                  {shopVouchers.length > 0 && (
                    <div className={styles.voucherOptions}>
                      {shopVouchers.map((voucher) => {
                        const reason = formatVoucherIneligibleReason(
                          voucher,
                          subtotal,
                          anchorNowMs
                        );
                        const eligible = reason === null;
                        const isSelected =
                          (selectedVoucherId === voucher.id ||
                            selectedVoucherId === voucher.code) &&
                          eligible;

                        const canSave = !voucher.saved && voucher.remaining_quota > 0;
                        const savingThis = savingVoucherId === voucher.id;

                        return (
                          <div key={voucher.id} className={styles.voucherOptionRow}>
                            <button
                              type="button"
                              className={`${styles.voucherOption} ${
                                isSelected ? styles.voucherOptionActive : ""
                              } ${!eligible ? styles.voucherOptionDisabled : ""}`}
                              disabled={!eligible}
                              title={reason ?? undefined}
                              onClick={() => setSelectedVoucher(isSelected ? null : voucher.id)}
                            >
                              <span className={styles.voucherCopy}>
                                <span className={styles.voucherCode}>{voucher.code}</span>
                                <span className={styles.voucherDesc}>
                                  {formatVoucherHeadline(voucher)}
                                </span>
                              </span>
                              {eligible ? (
                                <span className={styles.voucherActionLabel}>
                                  {isSelected ? (
                                    <>
                                      <IconCheck size={14} />
                                      <span>Đang dùng</span>
                                    </>
                                  ) : (
                                    "Dùng"
                                  )}
                                </span>
                              ) : (
                                <span className={`${styles.voucherIneligibleHint} num`}>
                                  {shortVoucherHint(voucher, subtotal, anchorNowMs)}
                                </span>
                              )}
                            </button>
                            {canSave && (
                              <button
                                type="button"
                                className={styles.voucherSaveBtn}
                                disabled={Boolean(savingVoucherId)}
                                onClick={() => void handleSaveVoucher(voucher)}
                              >
                                {savingThis ? "Đang lưu" : "Lưu mã"}
                              </button>
                            )}
                          </div>
                        );
                      })}
                    </div>
                  )}
                </div>
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
                    <dt className={styles.breakdownLabel}>Flash sale</dt>
                    <dd className={`${styles.breakdownDiscount} num`}>
                      {formatSignedVnd(-flashDiscount)}
                    </dd>
                  </div>
                )}

                {showVouchers && (
                  <div className={styles.breakdownRow}>
                    <dt className={styles.breakdownLabel}>Mã giảm giá</dt>
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
                  {previewLoading ? "Đang tính…" : "Tạm tính"}
                </span>
                <span className={`${styles.grandAmount} num`}>{formatVnd(grandTotal)}</span>
              </div>

              {totalDiscount > 0 && (
                <p className={styles.savingsNote}>
                  Tiết kiệm <strong className="num">{formatVnd(totalDiscount)}</strong>
                </p>
              )}

              {hasStockIssue ? (
                <div className={styles.ctaStack}>
                  <Button variant="primary" size="lg" fullWidth disabled>
                    Tiến hành thanh toán
                  </Button>
                  <p className={styles.ctaHint}>
                    Điều chỉnh sản phẩm hết hàng hoặc vượt tồn kho để tiếp tục.
                  </p>
                </div>
              ) : (
                <div className={styles.ctaStack}>
                  <Button
                    variant="primary"
                    size="lg"
                    fullWidth
                    className={shared.wrapBtn}
                    href={isCartMutating ? undefined : "/thanh-toan"}
                    disabled={isCartMutating}
                  >
                    <span className={styles.stableBtnLabel}>
                      <span className={styles.stableBtnSizer} aria-hidden="true">
                        Tiến hành thanh toán {formatVnd(grandTotal)}
                      </span>
                      <span className={styles.stableBtnSizer} aria-hidden="true">
                        Đang tính…
                      </span>
                      <span className={styles.stableBtnLive}>
                        {isCartMutating || previewLoading
                          ? "Đang tính…"
                          : `Tiến hành thanh toán ${formatVnd(grandTotal)}`}
                      </span>
                    </span>
                  </Button>
                </div>
              )}
            </div>
          </aside>
        </div>
      </section>

      {renderExtraSections()}

      {showAddons && addonProducts.length > 0 && (
        <ProductAddonStrip title={`Gợi ý từ ${brandName}`} products={addonProducts} />
      )}

      <div className={styles.mobileStickyBar} aria-label="Thanh toán nhanh">
        <div className={styles.mobileTotalWrap}>
          <span className={styles.mobileTotalLabel}>
            Tạm tính (<span className="num">{totalItemQty}</span>)
          </span>
          <span className={`${styles.mobileTotalVal} num`}>{formatVnd(grandTotal)}</span>
        </div>
        {hasStockIssue ? (
          <Button variant="primary" size="md" disabled>
            Kiểm tra tồn
          </Button>
        ) : (
          <Button
            variant="primary"
            size="md"
            href={isCartMutating ? undefined : "/thanh-toan"}
            disabled={isCartMutating}
          >
            <span className={styles.stableBtnLabel}>
              <span className={styles.stableBtnSizer} aria-hidden="true">
                Thanh toán
              </span>
              <span className={styles.stableBtnSizer} aria-hidden="true">
                Đang tính…
              </span>
              <span className={styles.stableBtnLive}>
                {isCartMutating || previewLoading ? "Đang tính…" : "Thanh toán"}
              </span>
            </span>
          </Button>
        )}
      </div>
    </div>
  );
}
