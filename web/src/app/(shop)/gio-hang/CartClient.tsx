"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { PLATFORM_FREESHIP_THRESHOLD } from "@/data/vouchers";
import type { Product } from "@/data/products";
import type { Shop } from "@/data/shops";
import { ApiError, api } from "@/lib/api";
import { formatInt, formatSignedVnd, formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { useSession } from "@/components/SessionProvider";
import {
  Button,
  IconChat,
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
import type { GwCoinBalance, GwDiscountBreakdown, GwVoucher } from "@/features/commerce/types";
import shared from "@/features/commerce/commerce.module.css";
import styles from "./page.module.css";

export interface CartClientProps {
  catalogProducts: Product[];
  catalogShops: Shop[];
  initialVouchers: GwVoucher[];
}

export function CartClient({ catalogProducts, catalogShops, initialVouchers }: CartClientProps) {
  const { isAuthenticated, loading: sessionLoading } = useSession();
  const {
    cart,
    cartItems,
    cartLoading,
    cartError,
    refreshCart,
    updateCartItemQty,
    removeCartItem,
    clearCart,
    selectedShopVouchers,
    platformVoucherId,
    freeshipVoucherId,
    useCoins,
    flashSaleItemBySku,
    setShopVoucher,
    setPlatformVoucher,
    setFreeshipVoucher,
    setUseCoins,
    saveVoucher,
    openChat,
    showToast,
  } = useStore();

  const [vouchers, setVouchers] = useState<GwVoucher[]>(initialVouchers);
  const [coinBalance, setCoinBalance] = useState<number>(0);
  const [breakdown, setBreakdown] = useState<GwDiscountBreakdown | null>(null);
  const [previewLoading, setPreviewLoading] = useState<boolean>(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [busySkuId, setBusySkuId] = useState<string | null>(null);
  const [clearing, setClearing] = useState<boolean>(false);
  const [confirmClear, setConfirmClear] = useState<boolean>(false);
  const [manualCode, setManualCode] = useState<string>("");
  const [validatingCode, setValidatingCode] = useState<boolean>(false);
  const [codeFeedback, setCodeFeedback] = useState<{ ok: boolean; message: string } | null>(null);

  const enrichedGroups = useMemo(
    () => enrichCartGroups(cart?.shop_groups ?? [], catalogProducts, catalogShops),
    [cart?.shop_groups, catalogProducts, catalogShops]
  );

  const addonProducts = useMemo(() => {
    const inCartProductIds = new Set(cartItems.map((c) => c.product_id));
    const filtered = catalogProducts.filter(
      (p) => !inCartProductIds.has(p.backendId ?? p.id) && !inCartProductIds.has(p.id)
    );
    return (filtered.length >= 4 ? filtered : catalogProducts).slice(0, 4);
  }, [cartItems, catalogProducts]);

  useRevealOnScroll(
    `cart-${isAuthenticated ? "auth" : "guest"}-${cartItems.length}-${enrichedGroups.length}`
  );

  // Load real vouchers & real coin balance when authenticated
  const loadPromoMeta = useCallback(async () => {
    if (!isAuthenticated) return;
    const [vRes, cRes] = await Promise.allSettled([
      api<{ vouchers: GwVoucher[] }>("/api/vouchers"),
      api<GwCoinBalance>("/api/vouchers/coins"),
    ]);
    if (vRes.status === "fulfilled" && Array.isArray(vRes.value.vouchers)) {
      setVouchers(vRes.value.vouchers);
    }
    if (cRes.status === "fulfilled" && typeof cRes.value.balance === "number") {
      setCoinBalance(Math.max(0, cRes.value.balance));
    }
  }, [isAuthenticated]);

  useEffect(() => {
    void loadPromoMeta();
  }, [loadPromoMeta]);

  // Run authoritative backend promotion preview whenever cart or voucher/coin selections change
  const selectedVouchersKey = JSON.stringify(selectedShopVouchers);
  const flashKey = JSON.stringify(flashSaleItemBySku);

  useEffect(() => {
    if (!isAuthenticated || !cart || cart.shop_groups.length === 0) {
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
  }, [
    isAuthenticated,
    cart,
    selectedVouchersKey,
    platformVoucherId,
    freeshipVoucherId,
    useCoins,
    coinBalance,
    flashKey,
  ]);

  const platformVouchers = useMemo(
    () => vouchers.filter((v) => v.scope.toUpperCase() === "PLATFORM" && v.is_active),
    [vouchers]
  );
  const freeshipVouchers = useMemo(
    () => vouchers.filter((v) => v.scope.toUpperCase() === "FREESHIP" && v.is_active),
    [vouchers]
  );

  const subtotal = breakdown?.total_subtotal_vnd ?? cart?.total_vnd ?? 0;
  const flashDiscount = breakdown?.total_flash_sale_discount_vnd ?? 0;
  const shopVoucherTotal = breakdown?.total_shop_voucher_discount_vnd ?? 0;
  const platformVoucherDiscount = breakdown?.total_platform_voucher_discount_vnd ?? 0;
  const freeshipDiscount = breakdown?.total_freeship_discount_vnd ?? 0;
  const coinsDiscount = breakdown?.total_coins_discount_vnd ?? 0;
  const totalDiscount = breakdown?.total_discount_vnd ?? 0;
  const grandTotal = breakdown?.final_payable_vnd ?? subtotal;
  const previewWarnings = breakdown?.warnings ?? [];

  const totalItemQty = cart?.total_items ?? cartItems.reduce((s, it) => s + it.quantity, 0);
  const hasStockIssue = useMemo(
    () => enrichedGroups.some((g) => g.items.some((it) => it.isOutOfStock || it.exceedsStock)),
    [enrichedGroups]
  );

  const platformShipThreshold = PLATFORM_FREESHIP_THRESHOLD;
  const platformShipRemaining = Math.max(0, platformShipThreshold - subtotal);
  const platformShipUnlocked = subtotal >= platformShipThreshold;
  const platformShipPct = Math.min(
    100,
    Math.round((subtotal / Math.max(1, platformShipThreshold)) * 100)
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
        showToast("Đã xóa toàn bộ sản phẩm trong giỏ hàng.");
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
        scope?: string;
        shop_id?: string;
        discount_vnd: number;
        reason?: string;
        breakdown?: GwDiscountBreakdown;
      }>("/api/vouchers/validate", {
        method: "POST",
        body: {
          code: clean,
          shop_voucher_ids: selectedShopVouchers,
          platform_voucher_id: platformVoucherId || "",
          freeship_voucher_id: freeshipVoucherId || "",
          coins_to_use: useCoins ? coinBalance : 0,
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

      const scope = (res.scope || res.voucher.scope).toUpperCase();
      if (scope === "SHOP" && res.voucher.shop_id) {
        setShopVoucher(res.voucher.shop_id, res.voucher.id);
      } else if (scope === "FREESHIP") {
        setFreeshipVoucher(res.voucher.id);
      } else {
        setPlatformVoucher(res.voucher.id);
      }
      if (res.breakdown) {
        setBreakdown(res.breakdown);
      }

      const discText =
        res.discount_vnd > 0
          ? ` (giảm ${formatVnd(res.discount_vnd)})`
          : scope === "FREESHIP"
            ? " (áp dụng giảm phí vận chuyển ở bước thanh toán)"
            : "";
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

  if (sessionLoading || (isAuthenticated && cartLoading && !cart)) {
    return (
      <div className={shared.page}>
        {renderHeader("Đang cập nhật sản phẩm, tồn kho và mã ưu đãi trong giỏ hàng của bạn.")}
        <section className={shared.section}>
          <h2 className={shared.sectionTitle}>Danh sách sản phẩm</h2>
          <StateView kind="loading" title="Đang tải giỏ hàng của bạn..." rows={4} />
        </section>
        <ProductAddonStrip
          title="Đồ cơ bản bán chạy tại NexusMart"
          subtitle="Sản phẩm dệt may trực tiếp từ các xưởng Việt Nam"
          products={addonProducts}
          showTierHint
        />
      </div>
    );
  }

  if (!isAuthenticated) {
    return (
      <div className={shared.page}>
        {renderHeader(
          "Đăng nhập tài khoản để xem giỏ hàng, giữ đúng số lượng tồn kho và áp mã ưu đãi."
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
            message="Giỏ hàng được lưu an toàn theo tài khoản của bạn. Đăng nhập để thêm sản phẩm, áp mã giảm giá và đặt hàng."
            actionLabel="Đăng nhập ngay"
            actionHref="/dang-nhap?next=%2Fgio-hang"
          />
          <div className={shared.formActions} style={{ marginTop: 16 }}>
            <Button variant="secondary" size="md" href="/dang-ky">
              Đăng ký tài khoản mới
            </Button>
            <Button variant="secondary" size="md" href="/danh-muc">
              Xem danh mục sản phẩm
            </Button>
          </div>
        </section>
        <ProductAddonStrip
          title="Gợi ý bắt đầu cho tủ đồ cơ bản"
          subtitle="Bốn mẫu tiêu chuẩn từ các xưởng dệt Việt Nam"
          products={addonProducts}
          showTierHint
        />
      </div>
    );
  }

  if (cartError && !cart) {
    return (
      <div className={shared.page}>
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
        <ProductAddonStrip title="Gợi ý xem nhanh danh mục cơ bản" products={addonProducts} />
      </div>
    );
  }

  if (enrichedGroups.length === 0) {
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
      {renderHeader("Kiểm tra số lượng, áp mã ưu đãi và chuyển sang bước đặt hàng.")}

      <section className={shared.section}>
        <div className={styles.cartLayout}>
          {/* Left column: Freeship progress, toolbar, crisp white shop sheets */}
          <div className={styles.leftCol}>
            <div
              className={styles.platformShipBar}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <div className={shared.progressBlock}>
                <div className={shared.progressMeta}>
                  <span>
                    {platformShipUnlocked ? (
                      <>
                        Đơn hàng đạt mốc ưu đãi vận chuyển toàn sàn{" "}
                        <strong className="num">{formatVnd(platformShipThreshold)}</strong>
                      </>
                    ) : (
                      <>
                        Mua thêm <strong className="num">{formatVnd(platformShipRemaining)}</strong>{" "}
                        để mở khóa mã miễn phí vận chuyển đơn từ{" "}
                        <span className="num">{formatVnd(platformShipThreshold)}</span>
                      </>
                    )}
                  </span>
                  <span className="num">
                    <strong>{formatVnd(Math.min(subtotal, platformShipThreshold))}</strong> /{" "}
                    {formatVnd(platformShipThreshold)}
                  </span>
                </div>
                <div
                  className={`${shared.progressTrack} ${shared.progressTrackLight}`}
                  aria-hidden="true"
                >
                  <div
                    className={`${shared.progressFill} ${
                      platformShipUnlocked ? shared.progressFillDone : ""
                    }`}
                    style={{ width: `${platformShipPct}%` }}
                  />
                </div>
              </div>
            </div>

            <div
              className={styles.cartToolbar}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <div className={styles.selectBar}>
                <span>
                  Đang có <strong className="num">{totalItemQty}</strong> sản phẩm từ{" "}
                  <strong className="num">{enrichedGroups.length}</strong> gian hàng
                </span>

                {confirmClear ? (
                  <div className={shared.formActions}>
                    <Button variant="danger" size="sm" disabled={clearing} onClick={handleClearAll}>
                      {clearing ? "Đang xóa..." : "Xác nhận xóa hết"}
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

            <div className={styles.shopGroupList}>
              {enrichedGroups.map((group, groupIdx) => {
                const shopVouchers = vouchers.filter(
                  (v) =>
                    v.scope.toUpperCase() === "SHOP" &&
                    v.shop_id === group.raw.shop_id &&
                    v.is_active
                );
                const chosenShopVoucher = selectedShopVouchers[group.raw.shop_id] ?? null;
                const shopBreakdown = breakdown?.shops.find((s) => s.shop_id === group.raw.shop_id);
                const shopVoucherDisc = shopBreakdown?.shop_voucher_discount_vnd ?? 0;
                const shopShipRemaining = Math.max(
                  0,
                  group.freeShipThreshold - group.raw.subtotal_vnd
                );
                const shopShipUnlocked = group.raw.subtotal_vnd >= group.freeShipThreshold;
                const shopShipPct = Math.min(
                  100,
                  Math.round((group.raw.subtotal_vnd / Math.max(1, group.freeShipThreshold)) * 100)
                );

                return (
                  <article
                    key={group.raw.shop_id}
                    className={styles.shopGroup}
                    data-rise=""
                    style={{ "--i": Math.min(groupIdx + 3, 6) } as React.CSSProperties}
                  >
                    <div className={styles.shopHeader}>
                      <div className={styles.shopIdentity}>
                        <span
                          className={styles.shopDot}
                          style={{ background: group.accentDot }}
                          aria-hidden="true"
                        />
                        <Link href={`/gian-hang/${group.shopSlug}`} className={styles.shopLink}>
                          {group.shopName}
                        </Link>
                        {group.shopCity && (
                          <span className={styles.shopCity}>{group.shopCity}</span>
                        )}
                      </div>

                      <button
                        type="button"
                        className={styles.chatShopBtn}
                        onClick={() => openChat(group.raw.shop_id)}
                      >
                        <IconChat size={15} />
                        <span>Chat với xưởng</span>
                      </button>
                    </div>

                    <div className={styles.shopShipStrip}>
                      <div className={shared.progressBlock}>
                        <div className={shared.progressMeta}>
                          <span>
                            {shopShipUnlocked ? (
                              <>
                                Đạt mốc giao hàng ưu đãi của {group.shopShortName} (
                                <span className="num">{formatVnd(group.freeShipThreshold)}</span>)
                              </>
                            ) : (
                              <>
                                Thêm <strong className="num">{formatVnd(shopShipRemaining)}</strong>{" "}
                                tại {group.shopShortName} cho mốc{" "}
                                <span className="num">{formatVnd(group.freeShipThreshold)}</span>
                              </>
                            )}
                          </span>
                          <span className="num">
                            Tạm tính gian hàng: <strong>{formatVnd(group.raw.subtotal_vnd)}</strong>
                          </span>
                        </div>
                        <div className={shared.progressTrack} aria-hidden="true">
                          <div
                            className={`${shared.progressFill} ${
                              shopShipUnlocked ? shared.progressFillDone : ""
                            }`}
                            style={{ width: `${shopShipPct}%` }}
                          />
                        </div>
                      </div>
                    </div>

                    <ul className={styles.itemList}>
                      {group.items.map((item, itemIdx) => {
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
                                {!item.returnEligible && (
                                  <span className={`${shared.statusTag} ${shared.toneNeutral}`}>
                                    Không đổi trả (Outlet / niêm phong)
                                  </span>
                                )}
                              </div>
                              <Price amount={raw.price_vnd} size="sm" />
                              <p className={styles.itemStock}>
                                {item.isOutOfStock ? (
                                  "Hiện tạm hết hàng, vui lòng xóa để tiếp tục đặt đơn"
                                ) : (
                                  <>
                                    Còn <span className="num">{raw.available_stock}</span> sản phẩm
                                    sẵn giao
                                    {raw.quantity >= raw.available_stock ? " (đã đạt tối đa)" : ""}
                                  </>
                                )}
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
                                    isBusy ||
                                    item.isOutOfStock ||
                                    raw.quantity >= raw.available_stock
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

                    {shopVouchers.length > 0 && (
                      <div className={styles.shopVoucherBar}>
                        <div className={styles.shopVoucherHead}>
                          <span className={styles.shopVoucherTitle}>
                            <IconTag size={15} />
                            <span>Ưu đãi của {group.shopShortName}</span>
                          </span>
                          {shopVoucherDisc > 0 && (
                            <span className={`${styles.breakdownDiscount} num`}>
                              {formatSignedVnd(-shopVoucherDisc)}
                            </span>
                          )}
                        </div>

                        <div className={styles.voucherOptions}>
                          {shopVouchers.map((voucher) => {
                            const reason = formatVoucherIneligibleReason(
                              voucher,
                              group.raw.subtotal_vnd
                            );
                            const eligible = reason === null;
                            const isSelected =
                              (chosenShopVoucher === voucher.id ||
                                chosenShopVoucher === voucher.code) &&
                              eligible;

                            return (
                              <button
                                key={voucher.id}
                                type="button"
                                className={`${styles.voucherOption} ${
                                  isSelected ? styles.voucherOptionActive : ""
                                } ${!eligible ? styles.voucherOptionDisabled : ""}`}
                                disabled={!eligible}
                                onClick={() =>
                                  setShopVoucher(group.raw.shop_id, isSelected ? null : voucher.id)
                                }
                              >
                                <div className={styles.voucherCopy}>
                                  <span className={styles.voucherCodeLine}>
                                    {voucher.code} · {formatVoucherHeadline(voucher)}
                                  </span>
                                  <span className={styles.voucherDesc}>
                                    {eligible
                                      ? `${voucher.name || "Áp dụng cho gian hàng"} · Còn ${formatInt(voucher.remaining_quota)} lượt`
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
                      </div>
                    )}
                  </article>
                );
              })}
            </div>
          </div>

          {/* Right column: ONE clean white summary sheet + dark total/CTA block */}
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
                    Phí vận chuyển chính xác sẽ được tính theo địa chỉ giao hàng ở bước tiếp theo.
                  </p>
                  <p className={styles.coinMeta}>Hỗ trợ đồng kiểm · Đổi trả tận nơi 45 ngày.</p>
                </div>
              </div>

              {/* Manual voucher input validated on real backend */}
              <form className={styles.summaryBlock} onSubmit={handleApplyManualCode} noValidate>
                <label htmlFor="cart-manual-code" className={styles.blockHeading}>
                  Nhập mã ưu đãi
                </label>
                <div className={shared.codeInputRow}>
                  <input
                    id="cart-manual-code"
                    type="text"
                    className={shared.input}
                    value={manualCode}
                    onChange={(e) => setManualCode(e.target.value)}
                    placeholder="Nhập mã giảm giá (ví dụ: NEXUS30)"
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

              {/* Platform vouchers */}
              {platformVouchers.length > 0 && (
                <div className={styles.summaryBlock}>
                  <h3 className={styles.blockHeading}>Ưu đãi toàn sàn</h3>
                  <div className={styles.voucherOptions}>
                    {platformVouchers.map((voucher) => {
                      const eligibleAmount = Math.max(
                        0,
                        subtotal - flashDiscount - shopVoucherTotal
                      );
                      const reason = formatVoucherIneligibleReason(voucher, eligibleAmount);
                      const eligible = reason === null;
                      const isSelected =
                        (platformVoucherId === voucher.id || platformVoucherId === voucher.code) &&
                        eligible;

                      return (
                        <button
                          key={voucher.id}
                          type="button"
                          className={`${styles.voucherOption} ${
                            isSelected ? styles.voucherOptionActive : ""
                          } ${!eligible ? styles.voucherOptionDisabled : ""}`}
                          disabled={!eligible}
                          onClick={() => setPlatformVoucher(isSelected ? null : voucher.id)}
                        >
                          <div className={styles.voucherCopy}>
                            <span className={styles.voucherCodeLine}>
                              {voucher.code} · {formatVoucherHeadline(voucher)}
                            </span>
                            <span className={styles.voucherDesc}>
                              {eligible
                                ? `Đơn từ ${formatVnd(voucher.min_order_vnd)} · Còn ${formatInt(voucher.remaining_quota)} lượt`
                                : reason}
                            </span>
                          </div>
                          <span className={styles.voucherActionLabel}>
                            {isSelected
                              ? "Đang dùng"
                              : eligible
                                ? "Chọn mã"
                                : voucher.remaining_quota <= 0
                                  ? "Hết lượt"
                                  : "Chưa đủ"}
                          </span>
                        </button>
                      );
                    })}
                  </div>
                </div>
              )}

              {/* Freeship vouchers */}
              {freeshipVouchers.length > 0 && (
                <div className={styles.summaryBlock}>
                  <h3 className={styles.blockHeading}>Mã giảm phí vận chuyển</h3>
                  <div className={styles.voucherOptions}>
                    {freeshipVouchers.map((voucher) => {
                      const reason = formatVoucherIneligibleReason(voucher, subtotal);
                      const eligible = reason === null;
                      const isSelected =
                        (freeshipVoucherId === voucher.id || freeshipVoucherId === voucher.code) &&
                        eligible;

                      return (
                        <button
                          key={voucher.id}
                          type="button"
                          className={`${styles.voucherOption} ${
                            isSelected ? styles.voucherOptionActive : ""
                          } ${!eligible ? styles.voucherOptionDisabled : ""}`}
                          disabled={!eligible}
                          onClick={() => setFreeshipVoucher(isSelected ? null : voucher.id)}
                        >
                          <div className={styles.voucherCopy}>
                            <span className={styles.voucherCodeLine}>
                              {voucher.code} · {formatVoucherHeadline(voucher)}
                            </span>
                            <span className={styles.voucherDesc}>
                              {eligible
                                ? `Đơn từ ${formatVnd(voucher.min_order_vnd)} · Còn ${formatInt(voucher.remaining_quota)} lượt`
                                : reason}
                            </span>
                          </div>
                          <span className={styles.voucherActionLabel}>
                            {isSelected
                              ? "Đang dùng"
                              : eligible
                                ? "Chọn mã"
                                : voucher.remaining_quota <= 0
                                  ? "Hết lượt"
                                  : "Chưa đủ"}
                          </span>
                        </button>
                      );
                    })}
                  </div>
                </div>
              )}

              {/* Save quick action for unsaved platform vouchers */}
              {platformVouchers.some((v) => !v.saved && v.remaining_quota > 0) && (
                <div className={shared.formActions}>
                  {platformVouchers
                    .filter((v) => !v.saved && v.remaining_quota > 0)
                    .slice(0, 2)
                    .map((v) => (
                      <Button
                        key={`save-${v.id}`}
                        variant="secondary"
                        size="sm"
                        onClick={() => handleSaveVoucher(v)}
                      >
                        Lưu mã {v.code}
                      </Button>
                    ))}
                </div>
              )}

              {/* Real NexusMart Coins */}
              <div className={styles.summaryBlock}>
                <h3 className={styles.blockHeading}>Dùng Xu NexusMart</h3>
                <label className={shared.checkboxRow}>
                  <input
                    type="checkbox"
                    className={shared.checkbox}
                    checked={useCoins && coinBalance > 0}
                    disabled={coinBalance <= 0}
                    onChange={(e) => setUseCoins(e.target.checked)}
                  />
                  <span>
                    Dùng Xu hiện có (<strong className="num">{formatInt(coinBalance)} Xu</strong>)
                  </span>
                </label>
                <p className={styles.coinMeta}>
                  Quy đổi 1 Xu = 1₫. Hệ thống tự động trừ mức Xu tối đa hợp lệ cho đơn hàng.
                </p>
              </div>

              {(previewError || previewWarnings.length > 0) && (
                <div className={`${shared.feedbackBox} ${shared.feedbackWarn}`} role="status">
                  {previewError ?? previewWarnings[0]}
                </div>
              )}

              {/* Authoritative integer VND breakdown */}
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

                <div className={styles.breakdownRow}>
                  <dt className={styles.breakdownLabel}>Voucher gian hàng</dt>
                  <dd className={`${styles.breakdownDiscount} num`}>
                    {shopVoucherTotal > 0 ? formatSignedVnd(-shopVoucherTotal) : "0₫"}
                  </dd>
                </div>

                <div className={styles.breakdownRow}>
                  <dt className={styles.breakdownLabel}>Voucher toàn sàn</dt>
                  <dd className={`${styles.breakdownDiscount} num`}>
                    {platformVoucherDiscount > 0 ? formatSignedVnd(-platformVoucherDiscount) : "0₫"}
                  </dd>
                </div>

                {freeshipDiscount > 0 && (
                  <div className={styles.breakdownRow}>
                    <dt className={styles.breakdownLabel}>Giảm phí vận chuyển</dt>
                    <dd className={`${styles.breakdownDiscount} num`}>
                      {formatSignedVnd(-freeshipDiscount)}
                    </dd>
                  </div>
                )}

                <div className={styles.breakdownRow}>
                  <dt className={styles.breakdownLabel}>Trừ Xu NexusMart</dt>
                  <dd className={`${styles.breakdownDiscount} num`}>
                    {coinsDiscount > 0 ? formatSignedVnd(-coinsDiscount) : "0₫"}
                  </dd>
                </div>
              </dl>
            </div>

            {/* Bold dark total & primary CTA anchor */}
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

      <ProductAddonStrip
        title="Gợi ý mua kèm đồ cơ bản"
        subtitle="Thêm nhanh theo size trực tiếp từ kho các xưởng dệt Việt Nam"
        products={addonProducts}
        showTierHint
      />

      {/* Sticky mobile checkout bar (390px thumb zone) */}
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
