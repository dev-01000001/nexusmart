"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  BADGE_LABELS,
  FABRIC_FEATURE_LABELS,
  FIT_LABELS,
  GENDER_LABELS,
  GIFT_ORDER_THRESHOLD,
  MATERIAL_LABELS,
  OCCASION_LABELS,
  PLATFORM_FREESHIP_THRESHOLD,
  SPORT_LABELS,
  getSku,
  type CartComboSelection,
  type FlashSaleItem,
  type Product,
  type Shop,
  type SizeCode,
  type Voucher,
} from "@/data";
import { useStore } from "@/context/StoreContext";
import { useMockStore } from "@/mock/MockStoreContext";
import {
  Button,
  IconAlert,
  IconChat,
  IconChevronDown,
  IconGift,
  IconHeart,
  IconMinus,
  IconPlay,
  IconPlus,
  IconShield,
  IconStore,
  IconTruck,
  Price,
  StateView,
} from "@/components";
import { checkLiveVoucher } from "@/lib/catalog-map";
import { formatInt, formatVnd } from "@/lib/format";
import { PROVINCES } from "@/lib/provinces";
import { estimateDelivery, recommendSize } from "@/lib/sizing";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import {
  PdpBackInStock,
  PdpBundleSection,
  PdpGallery,
  PdpQuestions,
  PdpRecommendationsSection,
  PdpReviews,
  PdpSizeModal,
} from "@/features/product";
import styles from "./page.module.css";

export interface ProductDetailClientProps {
  slug: string;
  product: Product | null;
  shop: Shop | null;
  allProducts: Product[];
  shops: Shop[];
  shopVouchers: Voucher[];
  platformVouchers: Voucher[];
  liveFlashItem: FlashSaleItem | null;
  loadError: string | null;
}

interface ProductDetailContentProps {
  product: Product;
  shop: Shop;
  allProducts: Product[];
  shops: Shop[];
  shopVouchers: Voucher[];
  platformVouchers: Voucher[];
  liveFlashItem: FlashSaleItem | null;
  initialColorQuery: string | null;
  initialSizeQuery: SizeCode | null;
}

function resolveInitialSize(
  product: Product,
  colorCode: string,
  sizeQuery: SizeCode | null
): SizeCode {
  if (sizeQuery && product.sizes.includes(sizeQuery)) {
    return sizeQuery;
  }
  const preferredOrder: SizeCode[] = product.sizes.includes("L")
    ? ["L", "M", ...product.sizes]
    : [...product.sizes];
  for (const sz of preferredOrder) {
    if (!product.sizes.includes(sz)) continue;
    const sku = getSku(product, colorCode, sz);
    if (sku && sku.stock > 0) return sz;
  }
  return product.sizes[0] ?? "M";
}

function ProductDetailContent({
  product,
  shop,
  allProducts,
  shops,
  shopVouchers,
  platformVouchers,
  liveFlashItem,
  initialColorQuery,
  initialSizeQuery,
}: ProductDetailContentProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const rootRef = useRef<HTMLDivElement>(null);
  const fabricVideoRef = useRef<HTMLVideoElement>(null);

  const {
    addToCart,
    openChat,
    setShopVoucher,
    setPlatformVoucher,
    setFreeshipVoucher,
    saveVoucher,
    selectedShopVouchers,
    platformVoucherId,
    freeshipVoucherId,
    showToast,
    toggleWishlist,
    isWishlisted,
    recordProductView,
    bodyProfile,
  } = useStore();
  const { membershipTier } = useMockStore();

  const initialColor =
    initialColorQuery && product.colors.some((c) => c.code === initialColorQuery)
      ? initialColorQuery
      : (product.colors[0]?.code ?? "");
  const initialSize = resolveInitialSize(product, initialColor, initialSizeQuery);
  const initialColorObj = product.colors.find((c) => c.code === initialColor) ?? product.colors[0];

  const [selectedColor, setSelectedColor] = useState<string>(initialColor);
  const [selectedSize, setSelectedSize] = useState<SizeCode>(initialSize);
  const [activeImage, setActiveImage] = useState<string>(initialColorObj?.image ?? product.image);
  const [quantity, setQuantity] = useState<number>(1);
  const [adding, setAdding] = useState<boolean>(false);
  const [copiedCode, setCopiedCode] = useState<string | null>(null);
  const [savedVoucherKeys, setSavedVoucherKeys] = useState<Record<string, boolean>>(() => {
    const init: Record<string, boolean> = {};
    for (const v of [...shopVouchers, ...platformVouchers]) {
      if (v.saved) {
        init[v.code] = true;
        if (v.id) init[v.id] = true;
      }
    }
    return init;
  });
  const [savingCode, setSavingCode] = useState<string | null>(null);
  const [sizeModalState, setSizeModalState] = useState<{
    open: boolean;
    tab: "chart" | "finder";
  }>({ open: false, tab: "chart" });
  const [bisDrawerOpen, setBisDrawerOpen] = useState<boolean>(false);
  const [deliveryProvince, setDeliveryProvince] = useState<string>("HCM");
  const [fabricVideoPlaying, setFabricVideoPlaying] = useState<boolean>(true);
  const [comboSizes, setComboSizes] = useState<Record<string, SizeCode>>(() => {
    const map: Record<string, SizeCode> = {};
    for (const slot of product.comboSlots ?? []) {
      map[slot.slotId] = slot.defaultSize;
    }
    return map;
  });

  useEffect(() => {
    recordProductView(product.id);
  }, [product.id, recordProductView]);

  useEffect(() => {
    const vid = fabricVideoRef.current;
    if (!vid) return;
    const reduced =
      typeof window !== "undefined" &&
      window.matchMedia &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (reduced) {
      vid.pause();
      return;
    }
    vid
      .play()
      .then(() => setFabricVideoPlaying(true))
      .catch(() => setFabricVideoPlaying(false));
  }, []);

  useRevealObserver(rootRef, product.id);

  const effectiveColor = product.colors.some((c) => c.code === selectedColor)
    ? selectedColor
    : (product.colors[0]?.code ?? "");
  const effectiveSize = product.sizes.includes(selectedSize)
    ? selectedSize
    : (product.sizes[0] ?? "M");
  const colorObj = product.colors.find((c) => c.code === effectiveColor) ?? product.colors[0];
  const activeSku = getSku(product, effectiveColor, effectiveSize);
  const skuStock = activeSku?.stock ?? 0;
  const activeSkuId = activeSku?.skuId;
  const canBuy = skuStock > 0 && Boolean(activeSkuId);
  const orderCap = product.maxPerOrder ?? skuStock;
  const maxQty = skuStock > 0 ? Math.max(1, Math.min(skuStock, orderCap)) : 0;
  const clampedQty = skuStock > 0 ? Math.max(1, Math.min(quantity, maxQty)) : 0;

  const activeFlashItemId =
    activeSkuId && liveFlashItem?.itemIdBySku ? liveFlashItem.itemIdBySku[activeSkuId] : undefined;
  const isSkuInLiveFlash = Boolean(activeFlashItemId && (liveFlashItem?.remainingQuota ?? 0) > 0);
  const baseUnitPrice = activeSku?.price ?? product.price;
  const unitPrice =
    isSkuInLiveFlash && liveFlashItem && liveFlashItem.flashPrice < baseUnitPrice
      ? liveFlashItem.flashPrice
      : baseUnitPrice;
  const displayOriginalPrice =
    isSkuInLiveFlash && liveFlashItem && liveFlashItem.flashPrice < baseUnitPrice
      ? baseUnitPrice
      : product.originalPrice;

  const activeTierDiscount = useMemo(() => {
    const tiers = product.tierDiscounts ?? [];
    let bestPct = 0;
    for (const t of tiers) {
      if (clampedQty >= t.minQty && t.discountPercent > bestPct) {
        bestPct = t.discountPercent;
      }
    }
    return bestPct;
  }, [product.tierDiscounts, clampedQty]);

  const rawLineSubtotal = unitPrice * Math.max(1, clampedQty);
  const tierDiscountVnd = Math.floor((rawLineSubtotal * activeTierDiscount) / 100);
  const effectiveSubtotal = Math.max(0, rawLineSubtotal - tierDiscountVnd);

  const wishlisted = isWishlisted(product.id);

  const isOutlet = Boolean(product.badges?.includes("outlet"));
  const hasOutOfStockVariant = product.skus.some((s) => s.stock <= 0);
  const isComboProduct = Boolean(
    product.isCombo && product.comboSlots && product.comboSlots.length > 0
  );

  const shopKey = shop.backendId ?? shop.id;
  const appliedShopVoucher = selectedShopVouchers[shopKey] ?? null;

  const displayedPlatformVouchers = useMemo(() => platformVouchers.slice(0, 4), [platformVouchers]);

  const deliveryEstimate = useMemo(
    () =>
      estimateDelivery({
        shopId: product.shopId,
        provinceCode: deliveryProvince,
        orderSubtotal: effectiveSubtotal,
      }),
    [product.shopId, deliveryProvince, effectiveSubtotal]
  );

  const garmentGroup = useMemo(() => {
    if (product.shopSizeChart) return product.shopSizeChart;
    if (product.categorySlug === "ao-thun" || product.categorySlug === "ao-polo") return "top";
    if (product.categorySlug === "quan-jogger") return "jogger";
    if (product.categorySlug === "do-lot") return "underwear";
    if (product.categorySlug === "tat-phu-kien" && product.id !== "prod-aokhoac-gio") return "sock";
    return product.id === "prod-aokhoac-gio" ? "top" : "bottom";
  }, [product]);

  const quickSizeHint = useMemo(
    () =>
      recommendSize({
        heightCm: bodyProfile.heightCm,
        weightKg: bodyProfile.weightKg,
        shape: bodyProfile.shape,
        preferredFit: bodyProfile.preferredFit,
        garmentGroup,
        shopId: shop.id,
        availableSizes: product.sizes,
      }),
    [bodyProfile, garmentGroup, shop.id, product.sizes]
  );

  const estimatedXu = Math.max(
    100,
    Math.floor((effectiveSubtotal * membershipTier.cashbackRate) / 100) * 100
  );

  const giftThresholdAmount = product.giftThreshold?.minAmount ?? GIFT_ORDER_THRESHOLD;
  const giftRemaining = Math.max(0, giftThresholdAmount - effectiveSubtotal);

  const syncColorUrl = (colorCode: string) => {
    const nextParams = new URLSearchParams(searchParams.toString());
    nextParams.set("color", colorCode);
    nextParams.delete("state");
    router.replace(`/san-pham/${product.slug}?${nextParams.toString()}`, { scroll: false });
  };

  const handleSelectColor = (code: string) => {
    setSelectedColor(code);
    const col = product.colors.find((c) => c.code === code);
    if (col) setActiveImage(col.image);
    const nextSku = getSku(product, code, effectiveSize);
    if (nextSku && nextSku.stock > 0) {
      const cap = Math.min(nextSku.stock, product.maxPerOrder ?? nextSku.stock);
      setQuantity((q) => Math.max(1, Math.min(q, cap)));
    }
    syncColorUrl(code);
  };

  const handleSelectImage = (imgSrc: string) => {
    setActiveImage(imgSrc);
    const matchedColor = product.colors.find((c) => c.image === imgSrc);
    if (matchedColor && matchedColor.code !== effectiveColor) {
      setSelectedColor(matchedColor.code);
      syncColorUrl(matchedColor.code);
    }
  };

  const handleSelectSize = (sz: SizeCode) => {
    setSelectedSize(sz);
    const nextSku = getSku(product, effectiveColor, sz);
    if (nextSku && nextSku.stock > 0) {
      const cap = Math.min(nextSku.stock, product.maxPerOrder ?? nextSku.stock);
      setQuantity((q) => Math.max(1, Math.min(q, cap)));
    }
  };

  const handleApplyAllComboSize = (sz: SizeCode) => {
    if (!product.comboSlots) return;
    const next: Record<string, SizeCode> = {};
    for (const slot of product.comboSlots) {
      next[slot.slotId] = slot.availableSizes.includes(sz) ? sz : slot.defaultSize;
    }
    setComboSizes(next);
  };

  const buildComboSelections = (): CartComboSelection[] | undefined => {
    if (!isComboProduct || !product.comboSlots) return undefined;
    return product.comboSlots.map((slot) => ({
      slotId: slot.slotId,
      label: slot.label,
      size: comboSizes[slot.slotId] ?? slot.defaultSize,
    }));
  };

  const handleAddToCart = async (goToCart: boolean) => {
    if (!canBuy || !activeSkuId || adding) return;
    setAdding(true);
    try {
      const res = await addToCart({
        skuId: activeSkuId,
        quantity: clampedQty,
        flashSaleItemId: isSkuInLiveFlash ? activeFlashItemId : undefined,
        productName: product.shortName,
        size: effectiveSize,
      });
      if (res.unauthenticated) {
        router.push(`/dang-nhap?next=${encodeURIComponent(`/san-pham/${product.slug}`)}`);
        return;
      }
      if (res.ok && goToCart) {
        router.push("/gio-hang");
      }
    } finally {
      setAdding(false);
    }
  };

  const handleCopyVoucher = (code: string) => {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      navigator.clipboard.writeText(code).catch(() => {});
    }
    setCopiedCode(code);
    showToast(`Đã sao chép mã ${code}.`);
  };

  const handleSaveAndApplyVoucher = async (v: Voucher) => {
    if (savingCode) return;
    const idOrCode = v.id ?? v.code;
    setSavingCode(v.code);
    try {
      const res = await saveVoucher(idOrCode);
      if (res.unauthenticated) {
        router.push(`/dang-nhap?next=${encodeURIComponent(`/san-pham/${product.slug}`)}`);
        return;
      }
      if (res.ok) {
        setSavedVoucherKeys((prev) => ({
          ...prev,
          [v.code]: true,
          ...(v.id ? { [v.id]: true } : {}),
        }));
      }
      if (v.scope === "shop") {
        setShopVoucher(shopKey, idOrCode);
      } else if (v.scope === "freeship") {
        setFreeshipVoucher(idOrCode);
      } else {
        setPlatformVoucher(idOrCode);
      }
    } finally {
      setSavingCode(null);
    }
  };

  const handleToggleFabricVideo = () => {
    const vid = fabricVideoRef.current;
    if (!vid) return;
    if (vid.paused) {
      vid
        .play()
        .then(() => setFabricVideoPlaying(true))
        .catch(() => setFabricVideoPlaying(false));
    } else {
      vid.pause();
      setFabricVideoPlaying(false);
    }
  };

  const isTopChart = garmentGroup === "top";
  const isSockChart = garmentGroup === "sock";
  const shopTopRows = isTopChart ? shop.sizeCharts?.top : undefined;

  return (
    <div ref={rootRef} className={styles.page}>
      <nav className={styles.breadcrumb} aria-label="Đường dẫn">
        <Link href="/" className={styles.crumbLink}>
          Trang chủ
        </Link>
        <span aria-hidden="true">/</span>
        <Link href="/danh-muc" className={styles.crumbLink}>
          Danh mục
        </Link>
        <span aria-hidden="true">/</span>
        <Link href={`/danh-muc?cat=${product.categorySlug}`} className={styles.crumbLink}>
          {product.categoryName}
        </Link>
        <span aria-hidden="true">/</span>
        <span className={styles.crumbCurrent}>{product.shortName}</span>
      </nav>

      <section className={styles.mainGrid} aria-label="Thông tin mua sản phẩm">
        <div className={styles.mediaCol}>
          <PdpGallery
            product={product}
            activeImage={activeImage}
            onSelectImage={handleSelectImage}
          />
        </div>

        <div className={styles.infoCol}>
          <h1 className={`${styles.title} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                {product.name}
              </span>
            </span>
          </h1>

          <div
            className={styles.priceBadgeRow}
            data-rise=""
            style={{ "--i": 1 } as React.CSSProperties}
          >
            <Price
              amount={unitPrice}
              originalAmount={displayOriginalPrice}
              size="lg"
              showDiscount={true}
            />
            {((product.badges && product.badges.length > 0) ||
              product.isFlashSale ||
              isSkuInLiveFlash) && (
              <div className={styles.badgeGroup} aria-label="Nhãn sản phẩm">
                {product.badges?.map((b) => (
                  <span
                    key={b}
                    className={`${styles.badgeTag} ${
                      b === "outlet"
                        ? styles.badgeOutlet
                        : b === "pre-order"
                          ? styles.badgePreorder
                          : ""
                    }`}
                  >
                    {BADGE_LABELS[b]}
                  </span>
                ))}
                {(product.isFlashSale || isSkuInLiveFlash) && (
                  <span className={styles.badgeTag}>
                    {isSkuInLiveFlash && liveFlashItem
                      ? `Flash sale · còn ${liveFlashItem.remainingQuota} suất`
                      : "Flash sale"}
                  </span>
                )}
              </div>
            )}
          </div>

          <p className={styles.specStrip} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            {product.fabric && <span>{product.fabric}</span>}
            {product.fabric && product.gsm > 0 && <span aria-hidden="true">·</span>}
            {product.gsm > 0 && <span>{product.gsm}gsm</span>}
            {product.fit && (product.fabric || product.gsm > 0) && (
              <span aria-hidden="true">·</span>
            )}
            {product.fit && <span>{product.fit}</span>}
          </p>

          {product.colors.length > 0 && (
            <div className={styles.selectorBlock}>
              <div className={styles.selectorLabel}>
                <span>Màu sắc</span>
                <span className={styles.selectorSelected}>{colorObj?.name ?? "Mặc định"}</span>
              </div>
              <div className={styles.colorRow} role="group" aria-label="Chọn màu sắc">
                {product.colors.map((c) => {
                  const active = c.code === effectiveColor;
                  return (
                    <button
                      key={c.code}
                      type="button"
                      className={`${styles.colorBtn} ${active ? styles.colorBtnActive : ""}`}
                      onClick={() => handleSelectColor(c.code)}
                      aria-pressed={active}
                    >
                      <span
                        className={styles.swatch}
                        style={{ backgroundColor: c.hex }}
                        aria-hidden="true"
                      />
                      <span>{c.name}</span>
                    </button>
                  );
                })}
              </div>
            </div>
          )}

          <div className={styles.selectorBlock}>
            <div className={styles.selectorLabel}>
              <span>
                Kích cỡ: <strong className={styles.selectorSelected}>Size {effectiveSize}</strong>
              </span>
              <div className={styles.sizeModalTriggers}>
                <button
                  type="button"
                  className={styles.sizeLinkBtn}
                  onClick={() => setSizeModalState({ open: true, tab: "chart" })}
                >
                  Bảng size
                </button>
                <span aria-hidden="true">·</span>
                <button
                  type="button"
                  className={styles.sizeLinkBtn}
                  onClick={() => setSizeModalState({ open: true, tab: "finder" })}
                >
                  Gợi ý size ({quickSizeHint.size})
                </button>
              </div>
            </div>

            <div className={styles.sizeRow} role="group" aria-label="Chọn kích cỡ">
              {product.sizes.map((sz) => {
                const sku = getSku(product, effectiveColor, sz);
                const stock = sku?.stock ?? 0;
                const active = sz === effectiveSize;
                const out = stock <= 0;
                return (
                  <button
                    key={sz}
                    type="button"
                    className={`${styles.sizeBtn} ${active ? styles.sizeBtnActive : ""} ${out ? styles.sizeBtnOut : ""}`}
                    onClick={() => handleSelectSize(sz)}
                    aria-pressed={active}
                    aria-label={out ? `Size ${sz}, hết hàng` : `Size ${sz}, còn ${stock} sản phẩm`}
                  >
                    <span className={styles.sizeCode}>{sz}</span>
                    <span className={styles.sizeQty}>{stock > 0 ? `${stock} sp` : "Hết hàng"}</span>
                  </button>
                );
              })}
            </div>
          </div>

          {product.modelInfo && product.modelInfo.length > 0 && (
            <div className={styles.modelBox} aria-label="Thông tin người mẫu mặc">
              {product.modelInfo.map((m, idx) => (
                <p key={`${m.gender}-${idx}`} className={styles.modelLine}>
                  <strong>{m.gender === "nam" ? "Mẫu nam" : "Mẫu nữ"}:</strong> {m.heightCm} cm ·{" "}
                  {m.weightKg} kg · {m.measurements} · <strong>Mặc size {m.wearingSize}</strong>
                  {m.note ? ` (${m.note})` : ""}
                </p>
              ))}
            </div>
          )}

          {isComboProduct && product.comboSlots && (
            <div className={styles.comboBlock} aria-label="Chọn cỡ riêng từng món trong combo">
              <div className={styles.comboHead}>
                <span className={styles.comboTitle}>
                  Chọn cỡ từng món trong combo ({product.comboSlots.length} món)
                </span>
                <button
                  type="button"
                  className={styles.sizeLinkBtn}
                  onClick={() => handleApplyAllComboSize(effectiveSize)}
                >
                  Áp cỡ {effectiveSize} cho cả bộ
                </button>
              </div>
              <div className={styles.comboList}>
                {product.comboSlots.map((slot) => {
                  const chosen = comboSizes[slot.slotId] ?? slot.defaultSize;
                  return (
                    <div key={slot.slotId} className={styles.comboSlotRow}>
                      <span className={styles.comboSlotLabel}>{slot.label}</span>
                      <div
                        className={styles.comboSlotBtns}
                        role="group"
                        aria-label={`Chọn size cho ${slot.label}`}
                      >
                        {slot.availableSizes.map((sz) => (
                          <button
                            key={sz}
                            type="button"
                            className={`${styles.comboSizeBtn} ${chosen === sz ? styles.comboSizeBtnActive : ""}`}
                            onClick={() =>
                              setComboSizes((prev) => ({ ...prev, [slot.slotId]: sz }))
                            }
                            aria-pressed={chosen === sz}
                          >
                            {sz}
                          </button>
                        ))}
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          )}

          <div className={styles.skuPanel} aria-live="polite">
            <div className={styles.skuLeft}>
              <span className={styles.skuCode}>
                Mã mẫu: {activeSku?.skuCode ?? "Đang cập nhật"}
              </span>
              {product.maxPerOrder && skuStock > 0 && (
                <span className={styles.skuCap}>Giới hạn {product.maxPerOrder} sp/đơn</span>
              )}
            </div>
            <div className={styles.skuRight}>
              {skuStock > 0 ? (
                <span className={styles.skuStock}>
                  Còn {skuStock} chiếc{shop.city ? ` tại kho ${shop.city}` : ""}
                </span>
              ) : (
                <span className={styles.skuStockEmpty}>Tạm hết hàng</span>
              )}
              {skuStock > 0 && hasOutOfStockVariant && (
                <button
                  type="button"
                  className={styles.bisToggleBtn}
                  onClick={() => setBisDrawerOpen((v) => !v)}
                >
                  {bisDrawerOpen ? "Ẩn báo có hàng" : "Báo tôi khi có hàng"}
                </button>
              )}
            </div>
          </div>

          <div className={styles.buyRow}>
            <div className={styles.qtyWishlistRow}>
              <div className={styles.qtyStepper} aria-label="Chọn số lượng">
                <button
                  type="button"
                  className={styles.qtyBtn}
                  disabled={clampedQty <= 1 || !canBuy}
                  onClick={() => setQuantity((q) => Math.max(1, q - 1))}
                  aria-label="Giảm số lượng"
                >
                  <IconMinus size={15} />
                </button>
                <span className={styles.qtyValue}>{canBuy ? clampedQty : 0}</span>
                <button
                  type="button"
                  className={styles.qtyBtn}
                  disabled={clampedQty >= maxQty || !canBuy}
                  onClick={() => setQuantity((q) => Math.min(maxQty, q + 1))}
                  aria-label="Tăng số lượng"
                >
                  <IconPlus size={15} />
                </button>
              </div>

              <button
                type="button"
                className={`${styles.wishlistBtn} ${wishlisted ? styles.wishlistBtnActive : ""}`}
                onClick={() => toggleWishlist(product.id, activeSku?.skuCode)}
                aria-pressed={wishlisted}
                aria-label={
                  wishlisted
                    ? `Bỏ ${product.shortName} khỏi yêu thích`
                    : `Lưu ${product.shortName} vào yêu thích`
                }
              >
                <IconHeart size={16} filled={wishlisted} />
                <span>{wishlisted ? "Đã lưu" : "Yêu thích"}</span>
              </button>
            </div>

            <div className={styles.buyActions}>
              <Button
                variant="primary"
                size="lg"
                disabled={!canBuy || adding}
                onClick={() => handleAddToCart(false)}
                fullWidth
              >
                {!canBuy ? "Tạm hết hàng" : adding ? "Đang thêm..." : "Thêm vào giỏ"}
              </Button>
              <Button
                variant="ink"
                size="lg"
                disabled={!canBuy || adding}
                onClick={() => handleAddToCart(true)}
                fullWidth
              >
                Mua ngay
              </Button>
            </div>
          </div>

          {wishlisted && (
            <p className={styles.wishlistHint}>
              Đã lưu vào danh sách yêu thích để theo dõi giảm giá và tình trạng có hàng.{" "}
              <Link href="/tai-khoan" className={styles.inlineLink}>
                Xem trong tài khoản
              </Link>
            </p>
          )}

          <PdpBackInStock
            product={product}
            activeSku={activeSku}
            colorName={colorObj?.name ?? "Mặc định"}
            size={effectiveSize}
            isOpen={skuStock <= 0 || bisDrawerOpen}
            onClose={() => setBisDrawerOpen(false)}
          />

          {product.tierDiscounts && product.tierDiscounts.length > 0 && (
            <div
              className={styles.tierBlock}
              data-cat-reveal=""
              style={{ "--stagger": 1 } as React.CSSProperties}
            >
              <div className={styles.tierHead}>
                <span className={styles.subBlockTitle}>Mua nhiều giảm thêm</span>
                {activeTierDiscount > 0 && (
                  <span className={styles.tierSaved}>
                    Đang áp giảm {activeTierDiscount}% (-{formatVnd(tierDiscountVnd)})
                  </span>
                )}
              </div>
              <div className={styles.tierRow}>
                {product.tierDiscounts.map((tier) => {
                  const discountedUnit = Math.floor(
                    (unitPrice * (100 - tier.discountPercent)) / 100
                  );
                  const active =
                    clampedQty >= tier.minQty && activeTierDiscount === tier.discountPercent;
                  const canPick = skuStock > 0 && maxQty >= tier.minQty;
                  return (
                    <div
                      key={tier.minQty}
                      className={`${styles.tierItem} ${active ? styles.tierItemActive : ""}`}
                    >
                      <div className={styles.tierItemText}>
                        <span className={styles.tierLabel}>{tier.label}</span>
                        <span className={styles.tierUnit}>Còn {formatVnd(discountedUnit)}/sp</span>
                      </div>
                      <button
                        type="button"
                        className={styles.tierPickBtn}
                        disabled={!canPick}
                        onClick={() => setQuantity(Math.min(maxQty, tier.minQty))}
                      >
                        {active ? `Đang chọn ${clampedQty}` : `Chọn ${tier.minQty}`}
                      </button>
                    </div>
                  );
                })}
              </div>
            </div>
          )}

          <div
            className={styles.voucherBlock}
            data-cat-reveal=""
            style={{ "--stagger": 2 } as React.CSSProperties}
          >
            <div className={styles.voucherHeadRow}>
              <p className={styles.subBlockTitle}>Voucher, quà tặng và hoàn Xu</p>
              <Link href="/voucher" className={styles.inlineLink}>
                Xem kho voucher
              </Link>
            </div>

            {isOutlet && (
              <p className={styles.outletExclusionNote}>
                <IconAlert size={15} />
                <span>
                  Sản phẩm Outlet: không áp dụng voucher giảm phần trăm toàn sàn hoặc ưu đãi chồng.
                </span>
              </p>
            )}

            <div className={styles.perkList}>
              <p className={styles.perkText}>
                <IconGift size={15} className={styles.perkIcon} />
                <strong>Quà tặng ngưỡng đơn:</strong> Từ {formatVnd(giftThresholdAmount)} tặng{" "}
                {product.giftThreshold?.giftName ?? "1 đôi Tất Cotton gân cổ trung"} (trị giá{" "}
                {formatVnd(product.giftThreshold?.giftValue ?? 39000)}).{" "}
                {giftRemaining === 0
                  ? "Số lượng hiện tại đã đủ điều kiện."
                  : `Cần thêm ${formatVnd(giftRemaining)}.`}
              </p>
              <p className={styles.xuLine}>
                Hoàn dự kiến <strong>+{formatInt(estimatedXu)} Xu</strong> cho hạng{" "}
                <strong>{membershipTier.name}</strong> (1 Xu = 1₫, nhân đôi vào Thứ Sáu).
              </p>
            </div>

            {(shopVouchers.length > 0 || displayedPlatformVouchers.length > 0) && (
              <ul className={styles.voucherList} aria-label="Danh sách mã giảm giá">
                {shopVouchers.map((v) => {
                  const idOrCode = v.id ?? v.code;
                  const isApplied =
                    appliedShopVoucher === idOrCode || appliedShopVoucher === v.code;
                  const isSaved = Boolean(
                    savedVoucherKeys[v.code] || (v.id && savedVoucherKeys[v.id])
                  );
                  const check = checkLiveVoucher(v, {
                    eligibleAmount: effectiveSubtotal,
                    shopId: product.shopId,
                    hasOutletItems: isOutlet,
                  });
                  return (
                    <li key={v.id ?? v.code} className={styles.voucherRow}>
                      <div className={styles.voucherLead}>
                        <span
                          className={`${styles.voucherDot} ${styles.voucherDotShop}`}
                          aria-hidden="true"
                        />
                        <div className={styles.voucherInfo}>
                          <span className={styles.voucherName}>{v.title}</span>
                          <span className={styles.voucherMeta}>
                            {v.code} (Mã gian hàng):{" "}
                            {check.valid
                              ? `Áp dụng ngay, còn ${v.remainingUses}/${v.totalUses} lượt`
                              : `${check.reason} (còn ${v.remainingUses} lượt)`}
                          </span>
                        </div>
                      </div>
                      <div className={styles.voucherActions}>
                        <button
                          type="button"
                          className={
                            isApplied ? styles.voucherActionApplied : styles.voucherActionPrimary
                          }
                          disabled={v.remainingUses <= 0 || savingCode === v.code}
                          onClick={() => handleSaveAndApplyVoucher(v)}
                        >
                          {v.remainingUses <= 0
                            ? "Hết lượt"
                            : isApplied
                              ? "Đã áp"
                              : isSaved
                                ? "Áp mã"
                                : "Lưu & áp"}
                        </button>
                        <button
                          type="button"
                          className={styles.voucherActionMuted}
                          onClick={() => handleCopyVoucher(v.code)}
                        >
                          {copiedCode === v.code ? "Đã chép" : "Sao chép"}
                        </button>
                      </div>
                    </li>
                  );
                })}

                {displayedPlatformVouchers.map((v) => {
                  const idOrCode = v.id ?? v.code;
                  const isApplied =
                    v.scope === "platform"
                      ? platformVoucherId === idOrCode || platformVoucherId === v.code
                      : freeshipVoucherId === idOrCode || freeshipVoucherId === v.code;
                  const isSaved = Boolean(
                    savedVoucherKeys[v.code] || (v.id && savedVoucherKeys[v.id])
                  );
                  const blockedByOutlet = Boolean(v.excludeOutlet && isOutlet);
                  const check = checkLiveVoucher(v, {
                    eligibleAmount: effectiveSubtotal,
                    hasOutletItems: isOutlet,
                  });
                  return (
                    <li key={v.id ?? v.code} className={styles.voucherRow}>
                      <div className={styles.voucherLead}>
                        <span
                          className={`${styles.voucherDot} ${
                            v.scope === "freeship"
                              ? styles.voucherDotShip
                              : styles.voucherDotPlatform
                          }`}
                          aria-hidden="true"
                        />
                        <div className={styles.voucherInfo}>
                          <span className={styles.voucherName}>{v.title}</span>
                          <span
                            className={blockedByOutlet ? styles.voucherBlocked : styles.voucherMeta}
                          >
                            {v.code} ({v.scope === "freeship" ? "Freeship sàn" : "Mã toàn sàn"}):{" "}
                            {blockedByOutlet
                              ? "Không áp dụng cho sản phẩm Outlet"
                              : check.valid
                                ? `Đủ điều kiện áp dụng, còn ${v.remainingUses}/${v.totalUses} lượt`
                                : `${check.reason} (còn ${v.remainingUses} lượt)`}
                          </span>
                        </div>
                      </div>
                      <div className={styles.voucherActions}>
                        <button
                          type="button"
                          className={
                            blockedByOutlet || v.remainingUses <= 0
                              ? styles.voucherActionMuted
                              : isApplied
                                ? styles.voucherActionApplied
                                : styles.voucherActionPrimary
                          }
                          disabled={
                            blockedByOutlet || v.remainingUses <= 0 || savingCode === v.code
                          }
                          onClick={() => handleSaveAndApplyVoucher(v)}
                        >
                          {blockedByOutlet
                            ? "Không áp"
                            : v.remainingUses <= 0
                              ? "Hết lượt"
                              : isApplied
                                ? "Đã áp"
                                : isSaved
                                  ? "Đã lưu"
                                  : "Lưu mã"}
                        </button>
                        <button
                          type="button"
                          className={styles.voucherActionMuted}
                          onClick={() => handleCopyVoucher(v.code)}
                        >
                          {copiedCode === v.code ? "Đã chép" : "Sao chép"}
                        </button>
                      </div>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>

          <div
            className={styles.logisticsBlock}
            data-cat-reveal=""
            style={{ "--stagger": 3 } as React.CSSProperties}
          >
            <div className={styles.deliveryHead}>
              <span className={styles.deliveryTitle}>
                <IconTruck size={16} />
                <span>Ước tính giao hàng</span>
              </span>
              <label className={styles.provincePicker}>
                <span className="sr-only">Chọn tỉnh thành giao hàng</span>
                <select
                  value={deliveryProvince}
                  onChange={(e) => setDeliveryProvince(e.target.value)}
                  className={styles.provinceSelect}
                >
                  {PROVINCES.map((prov) => (
                    <option key={prov.code} value={prov.code}>
                      {prov.shortName}
                    </option>
                  ))}
                </select>
              </label>
            </div>

            <div className={styles.deliveryDetails}>
              <p className={styles.deliveryLine}>
                Từ kho <strong>{deliveryEstimate.originCity}</strong> đến{" "}
                <strong>{deliveryEstimate.destinationProvinceName}</strong> (
                {deliveryEstimate.carrier}) · Dự kiến <strong>{deliveryEstimate.etaLabel}</strong>.
              </p>
              <p className={styles.deliveryLine}>
                Cước tiêu chuẩn <strong>{formatVnd(deliveryEstimate.standardFeeVnd)}</strong> ·
                Freeship xưởng từ{" "}
                <strong>{formatVnd(deliveryEstimate.shopFreeShipThreshold)}</strong> · Freeship sàn
                từ <strong>{formatVnd(PLATFORM_FREESHIP_THRESHOLD)}</strong>.
              </p>
              {product.slaText && (
                <p className={styles.deliverySub}>
                  Hạn chuẩn bị hàng: {product.slaText}. {deliveryEstimate.coInspectionNote}
                </p>
              )}
              {deliveryEstimate.fastAvailable && (
                <p className={styles.deliveryFast}>
                  Có giao nhanh nội thành: {deliveryEstimate.fastEtaLabel} (
                  {formatVnd(deliveryEstimate.fastFeeVnd)}).
                </p>
              )}
            </div>

            <div
              className={
                product.returnEligible === false ? styles.returnExcluded : styles.returnBox
              }
            >
              <div className={styles.returnTop}>
                <span className={styles.returnLabel}>
                  {product.returnEligible === false ? (
                    <IconAlert size={16} />
                  ) : (
                    <IconShield size={16} />
                  )}
                  <strong>
                    {product.returnEligible === false
                      ? "Không áp dụng đổi trả"
                      : "Đổi trả miễn phí 45 ngày"}
                  </strong>
                </span>
                <Link href="/tro-giup" className={styles.inlineLink}>
                  Xem chính sách
                </Link>
              </div>
              <p className={styles.returnDesc}>
                {product.returnEligible === false
                  ? (product.returnExclusionReason ??
                    "Sản phẩm thuộc nhóm ngoại lệ (Outlet hoặc pack niêm phong), chỉ hỗ trợ đổi khi có lỗi sản xuất.")
                  : `Shipper đổi size 2 chiều tận nơi, hoàn chuyển khoản định kỳ Thứ 2 và Thứ 5 (tối đa 3 lần/đơn).${
                      product.shopSizeChart === "sock" ? " Hỗ trợ bù lẻ 1 chiếc tất thất lạc." : ""
                    }`}
              </p>
            </div>
          </div>

          <div
            className={styles.shopCard}
            data-cat-reveal=""
            style={{ "--stagger": 4 } as React.CSSProperties}
          >
            <div className={styles.shopTop}>
              <Link href={`/gian-hang/${shop.slug}`} className={styles.shopName}>
                {shop.name}
              </Link>
              {shop.city && <span className={styles.shopCity}>{shop.city}</span>}
            </div>
            {shop.verifiedBadge && <p className={styles.shopBadge}>{shop.verifiedBadge}</p>}
            <p className={styles.shopMeta}>
              {shop.specialty}
              {shop.responseTime ? `. Phản hồi ${shop.responseTime.toLowerCase()}` : ""}
              {shop.fulfillmentRate ? ` · Tỷ lệ hoàn thành ${shop.fulfillmentRate}.` : ""}
            </p>
            <div className={styles.shopActions}>
              <Button variant="secondary" size="sm" href={`/gian-hang/${shop.slug}`}>
                <IconStore size={15} />
                <span>Xem gian hàng</span>
              </Button>
              <Button variant="primary" size="sm" onClick={() => openChat(shop.id)}>
                <IconChat size={15} />
                <span>Chat với người bán</span>
              </Button>
            </div>
          </div>
        </div>
      </section>

      <section className={styles.detailsSection} aria-labelledby="pdp-specs-heading">
        <h2
          id="pdp-specs-heading"
          className={styles.sectionHeading}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          Chất liệu và bảng quy đổi size
        </h2>

        <div className={styles.detailsGrid}>
          <div
            className={styles.specTextCol}
            data-cat-reveal=""
            style={{ "--stagger": 1 } as React.CSSProperties}
          >
            <p className={styles.descText}>{product.description}</p>

            <div className={styles.detailGroup}>
              <h3 className={styles.detailGroupTitle}>Vải, định lượng và phom dáng</h3>
              <ul className={styles.plainList}>
                {product.fabric && (
                  <li>
                    <strong>Tỷ lệ sợi:</strong> {product.fabric}
                  </li>
                )}
                {product.gsm > 0 && (
                  <li>
                    <strong>Định lượng &amp; chất liệu:</strong> {product.gsm}gsm
                    {product.material
                      ? ` (${MATERIAL_LABELS[product.material]})`
                      : product.materialLabel
                        ? ` (${product.materialLabel})`
                        : ""}
                  </li>
                )}
                <li>
                  <strong>Phom dáng &amp; giới tính:</strong> {product.fit || "Tiêu chuẩn"}
                  {product.fitType ? ` · ${FIT_LABELS[product.fitType]}` : ""} ·{" "}
                  {GENDER_LABELS[product.gender]}
                </li>
                {product.occasion && product.occasion.length > 0 && (
                  <li>
                    <strong>Hoàn cảnh mặc:</strong>{" "}
                    {product.occasion.map((o) => OCCASION_LABELS[o]).join(", ")}
                  </li>
                )}
                {product.sport && product.sport.length > 0 && (
                  <li>
                    <strong>Môn vận động:</strong>{" "}
                    {product.sport.map((s) => SPORT_LABELS[s]).join(", ")}
                  </li>
                )}
                <li>
                  <strong>Xuất xưởng:</strong> {product.origin || shop.city || "Việt Nam"} (
                  {shop.name})
                </li>
                {product.highlights.map((h) => (
                  <li key={h}>{h}</li>
                ))}
              </ul>
            </div>

            {product.fabricFeatures && product.fabricFeatures.length > 0 && (
              <div className={styles.detailGroup}>
                <div className={styles.featureHeadRow}>
                  <h3 className={styles.detailGroupTitle}>Tính năng công nghệ vải</h3>
                  <Link href="/chien-dich/cong-nghe-vai" className={styles.inlineLink}>
                    Xem từ điển công nghệ vải
                  </Link>
                </div>
                <div className={styles.featureTags}>
                  {product.fabricFeatures.map((feat) => (
                    <Link
                      key={feat}
                      href={`/danh-muc?feature=${feat}`}
                      className={styles.featureTag}
                    >
                      {FABRIC_FEATURE_LABELS[feat]}
                    </Link>
                  ))}
                  {product.material && (
                    <Link
                      href={`/danh-muc?material=${product.material}`}
                      className={styles.featureTag}
                    >
                      Lọc theo {MATERIAL_LABELS[product.material]}
                    </Link>
                  )}
                </div>
              </div>
            )}

            <details className={styles.careDetails} open>
              <summary className={styles.careSummary}>
                <span>Hướng dẫn bảo quản sợi vải</span>
                <IconChevronDown size={16} className={styles.careChevron} />
              </summary>
              <div className={styles.careBody}>
                {product.careInstructions && product.careInstructions.length > 0 ? (
                  <ul className={styles.careList}>
                    {product.careInstructions.map((item) => (
                      <li key={`${item.code}-${item.label}`} className={styles.careItem}>
                        <strong>{item.label}:</strong> {item.detail}
                      </li>
                    ))}
                  </ul>
                ) : null}
                {product.careGuide.length > 0 && (
                  <ul className={styles.plainList}>
                    {product.careGuide.map((tip) => (
                      <li key={tip}>{tip}</li>
                    ))}
                  </ul>
                )}
              </div>
            </details>

            {product.hasVideo && product.videoUrl && (
              <figure className={styles.fabricVideoFigure}>
                <div className={styles.fabricVideoWrap}>
                  <video
                    ref={fabricVideoRef}
                    src={product.videoUrl}
                    poster={product.videoPoster}
                    muted
                    loop
                    playsInline
                    className={styles.fabricVideo}
                    aria-label={`Cận cảnh quy trình dệt vải cho ${product.shortName}`}
                  />
                  <button
                    type="button"
                    className={styles.fabricVideoToggle}
                    onClick={handleToggleFabricVideo}
                  >
                    <IconPlay size={14} />
                    <span>{fabricVideoPlaying ? "Tạm dừng" : "Phát video"}</span>
                  </button>
                </div>
                <figcaption className={styles.fabricVideoCaption}>
                  Cận cảnh khung dệt và cấu trúc sợi {product.gsm}gsm (tắt tiếng lặp mượt).
                </figcaption>
              </figure>
            )}
          </div>

          <div
            className={styles.sizeChartCol}
            data-cat-reveal=""
            style={{ "--stagger": 2 } as React.CSSProperties}
          >
            <div className={styles.chartHeadRow}>
              <h3 className={styles.detailGroupTitle}>
                {`Bảng thông số size ${shop.shortName} (cm / kg)`}
              </h3>
              <button
                type="button"
                className={styles.sizeLinkBtn}
                onClick={() => setSizeModalState({ open: true, tab: "finder" })}
              >
                Tính size theo số đo
              </button>
            </div>

            {shop.sizeCharts?.sizeNote && (
              <p className={styles.chartShopNote}>{shop.sizeCharts.sizeNote}</p>
            )}

            <div className={styles.tableWrap}>
              <table className={styles.sizeTable}>
                <thead>
                  <tr>
                    <th scope="col">Size</th>
                    <th scope="col">{isSockChart ? "Cỡ giày" : "Cao"}</th>
                    <th scope="col">{isSockChart ? "Chân" : "Nặng"}</th>
                    <th scope="col">{isTopChart ? "Ngực" : isSockChart ? "Co giãn" : "Eo"}</th>
                    {shopTopRows && <th scope="col">Vai</th>}
                    <th scope="col">Dài</th>
                  </tr>
                </thead>
                <tbody>
                  {shopTopRows
                    ? shopTopRows
                        .filter((r) => product.sizes.includes(r.size))
                        .map((row, idx) => (
                          <tr
                            key={row.size}
                            className={
                              row.size === effectiveSize ? styles.sizeRowActive : undefined
                            }
                            data-cat-reveal=""
                            style={{ "--stagger": idx } as React.CSSProperties}
                          >
                            <td>{row.size}</td>
                            <td>
                              {row.heightMin}-{row.heightMax}
                            </td>
                            <td>
                              {row.weightMin}-{row.weightMax}
                            </td>
                            <td>{row.chestOrWaistCm}</td>
                            <td>{row.shoulderCm ?? "-"}</td>
                            <td>{row.lengthCm}</td>
                          </tr>
                        ))
                    : product.sizeChart.map((row, idx) => (
                        <tr
                          key={row.size}
                          className={row.size === effectiveSize ? styles.sizeRowActive : undefined}
                          data-cat-reveal=""
                          style={{ "--stagger": idx } as React.CSSProperties}
                        >
                          <td>{row.size}</td>
                          <td>{row.heightCm}</td>
                          <td>{row.weightKg}</td>
                          <td>{row.chestOrWaistCm}</td>
                          <td>{row.lengthCm}</td>
                        </tr>
                      ))}
                </tbody>
              </table>
            </div>
            <p className={styles.chartNote}>
              Sai số đo thực tế khoảng 1 đến 1,5 cm do co giãn tự nhiên của sợi dệt. Hàng đang chọn
              (Size {effectiveSize}) được tô nền nổi bật trong bảng.
            </p>
          </div>
        </div>
      </section>

      <PdpBundleSection
        product={product}
        shop={shop}
        allProducts={allProducts}
        shops={shops}
        selectedColorCode={effectiveColor}
        selectedSize={effectiveSize}
        comboSelections={buildComboSelections()}
      />

      <PdpReviews
        product={product}
        shop={shop}
        selectedColorName={colorObj?.name ?? "Mặc định"}
        selectedSize={effectiveSize}
      />

      <PdpQuestions product={product} shop={shop} />

      <PdpRecommendationsSection
        product={product}
        shop={shop}
        allProducts={allProducts}
        shops={shops}
      />

      <PdpSizeModal
        open={sizeModalState.open}
        initialTab={sizeModalState.tab}
        onClose={() => setSizeModalState((prev) => ({ ...prev, open: false }))}
        product={product}
        shop={shop}
        selectedSize={effectiveSize}
        onSelectSize={(sz) => {
          handleSelectSize(sz);
          showToast(`Đã chọn Size ${sz} cho ${product.shortName}.`);
        }}
      />
    </div>
  );
}

export function ProductDetailClient({
  slug,
  product,
  shop,
  allProducts,
  shops,
  shopVouchers,
  platformVouchers,
  liveFlashItem,
  loadError,
}: ProductDetailClientProps) {
  const searchParams = useSearchParams();
  const router = useRouter();

  const stateParam = searchParams.get("state");
  const colorQuery = searchParams.get("color");
  const sizeQuery = searchParams.get("size") as SizeCode | null;

  if (stateParam === "loading") {
    return (
      <div className={styles.page}>
        <StateView kind="loading" title="Đang tải thông tin sản phẩm..." rows={6} />
      </div>
    );
  }

  if (loadError || stateParam === "error") {
    return (
      <div className={styles.page}>
        <StateView
          kind="error"
          title="Không thể tải chi tiết sản phẩm"
          message={
            loadError ?? "Kết nối đến kho dữ liệu sản phẩm tạm gián đoạn. Vui lòng thử tải lại."
          }
          actionLabel="Tải lại trang"
          onAction={() => (loadError ? router.refresh() : router.replace(`/san-pham/${slug}`))}
        />
      </div>
    );
  }

  if (stateParam === "empty" || !product || !shop) {
    return (
      <div className={styles.page}>
        <StateView
          kind="empty"
          title="Không tìm thấy sản phẩm này"
          message="Sản phẩm có thể đã ngưng kinh doanh hoặc đường dẫn không còn chính xác."
          actionLabel="Quay lại danh mục"
          actionHref="/danh-muc"
        />
      </div>
    );
  }

  return (
    <ProductDetailContent
      key={product.id}
      product={product}
      shop={shop}
      allProducts={allProducts}
      shops={shops}
      shopVouchers={shopVouchers}
      platformVouchers={platformVouchers}
      liveFlashItem={liveFlashItem}
      initialColorQuery={colorQuery}
      initialSizeQuery={sizeQuery}
    />
  );
}
