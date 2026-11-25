"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { getSku, type Product, type SizeCode } from "@/data/products";
import { stripInternalSitePrefix } from "@/features/site/siteNav";
import { formatDiscountPercent } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { IconClose, IconHeart, IconPlus } from "./Icons";
import { hasOpenModal } from "./modalFocus";
import { Price } from "./Price";
import styles from "./ProductCard.module.css";

export interface ProductCardProps {
  product: Product;
  overridePrice?: number;
  overrideOriginalPrice?: number;
  quotaLabel?: string;
  priority?: boolean;
  enableQuickAdd?: boolean;
  enableInteractiveSwatches?: boolean;
  showWishlist?: boolean;
  showBadges?: boolean;
  maxVisibleSwatches?: number;
  isFlashSaleItem?: boolean;
  isShockDeal?: boolean;
  flashSaleItemIdBySku?: Record<string, string>;
}

type ProductWithOnModel = Product & {
  onModelImage?: string;
};

export function ProductCard({
  product,
  overridePrice,
  overrideOriginalPrice,
  quotaLabel,
  priority = false,
  enableQuickAdd = true,
  enableInteractiveSwatches = true,
  showWishlist = true,
  showBadges = true,
  maxVisibleSwatches = 4,
  isFlashSaleItem = false,
  isShockDeal = false,
  flashSaleItemIdBySku,
}: ProductCardProps) {
  const router = useRouter();
  const rawPathname = usePathname();
  const { addToCart, toggleWishlist, isWishlisted } = useStore();
  const [selectedColorCode, setSelectedColorCode] = useState<string>(product.colors[0]?.code ?? "");
  const [addedSize, setAddedSize] = useState<SizeCode | null>(null);
  const [addingSize, setAddingSize] = useState<SizeCode | null>(null);
  const [hoverQuickOpen, setHoverQuickOpen] = useState(false);
  const [focusQuickOpen, setFocusQuickOpen] = useState(false);
  const [touchQuickOpen, setTouchQuickOpen] = useState(false);
  const addedTimerRef = useRef<number | null>(null);
  const mediaLinkRef = useRef<HTMLAnchorElement | null>(null);
  const touchQuickBtnRef = useRef<HTMLButtonElement | null>(null);
  const quickOverlayRef = useRef<HTMLDivElement | null>(null);
  const sizeButtonsRef = useRef<HTMLDivElement | null>(null);
  const quickCloseBtnRef = useRef<HTMLButtonElement | null>(null);
  const pendingFocusRef = useRef<"tray" | "trigger" | null>(null);
  const suppressNextFocusOpenRef = useRef(false);

  useEffect(() => {
    return () => {
      if (addedTimerRef.current !== null) {
        window.clearTimeout(addedTimerRef.current);
      }
    };
  }, []);

  const activeColor = useMemo(
    () => product.colors.find((c) => c.code === selectedColorCode) ?? product.colors[0],
    [product.colors, selectedColorCode]
  );

  const displayImage = activeColor?.image ?? product.image;
  const productExt = product as ProductWithOnModel;
  const candidateHoverImage = productExt.onModelImage ?? product.gallery[1];
  const hoverImage =
    candidateHoverImage && candidateHoverImage !== displayImage ? candidateHoverImage : null;

  const displayPrice = overridePrice ?? product.price;
  const maxSkuPrice = useMemo(() => {
    if (overridePrice != null || product.skus.length <= 1) return undefined;
    let maxP = displayPrice;
    for (const sku of product.skus) {
      if (sku.price > maxP) maxP = sku.price;
    }
    return maxP > displayPrice ? maxP : undefined;
  }, [overridePrice, product.skus, displayPrice]);
  const displayOriginal = overrideOriginalPrice ?? product.originalPrice;
  const hasDiscount = displayOriginal != null && displayOriginal > displayPrice;
  const discountLabel =
    showBadges && hasDiscount ? formatDiscountPercent(displayOriginal, displayPrice) : null;

  const wishlisted = isWishlisted(product.id);
  const visibleColors = product.colors.slice(0, maxVisibleSwatches);
  const extraColorCount = Math.max(0, product.colors.length - maxVisibleSwatches);

  const activeColorStock = useMemo(() => {
    if (!activeColor) {
      return product.skus.reduce((sum, s) => sum + Math.max(0, s.stock), 0);
    }
    return product.sizes.reduce((sum, sz) => {
      const sku = getSku(product, activeColor.code, sz);
      return sum + (sku ? Math.max(0, sku.stock) : 0);
    }, 0);
  }, [activeColor, product]);

  const hasAnyInStockForColor = activeColorStock > 0;
  const hasQuickAddSku = product.sizes.some((sz) => {
    const sku = activeColor ? getSku(product, activeColor.code, sz) : undefined;
    return Boolean(sku && sku.stock > 0 && sku.skuId);
  });

  // Only show quick-add tray when actionable (live SKU)
  const canQuickAdd = enableQuickAdd && hasQuickAddSku;
  const canShowQuickOverlay =
    canQuickAdd && (hoverQuickOpen || focusQuickOpen || touchQuickOpen || addedSize !== null);
  const accessibleName = product.shortName || product.name;

  useEffect(() => {
    const targetKind = pendingFocusRef.current;
    if (!targetKind) return;
    if (targetKind === "tray" && touchQuickOpen) {
      pendingFocusRef.current = null;
      const target =
        sizeButtonsRef.current?.querySelector<HTMLButtonElement>("button:not(:disabled)") ??
        quickCloseBtnRef.current;
      target?.focus();
    } else if (targetKind === "trigger" && !touchQuickOpen) {
      pendingFocusRef.current = null;
      touchQuickBtnRef.current?.focus();
    }
  }, [touchQuickOpen]);

  useEffect(() => {
    if (!canShowQuickOverlay) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.isComposing || e.defaultPrevented || hasOpenModal()) return;
      const wasTouchOpen = touchQuickOpen;
      const focusInsideOverlay = Boolean(quickOverlayRef.current?.contains(document.activeElement));
      if (focusInsideOverlay) {
        e.preventDefault();
      }
      if (addedTimerRef.current !== null) {
        window.clearTimeout(addedTimerRef.current);
        addedTimerRef.current = null;
      }
      if (wasTouchOpen && (focusInsideOverlay || document.activeElement === document.body)) {
        pendingFocusRef.current = "trigger";
      }
      setTouchQuickOpen(false);
      setFocusQuickOpen(false);
      setHoverQuickOpen(false);
      setAddedSize(null);
      if (!wasTouchOpen && focusInsideOverlay) {
        suppressNextFocusOpenRef.current = true;
        mediaLinkRef.current?.focus();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [canShowQuickOverlay, touchQuickOpen]);

  const closeTouchTray = () => {
    if (addedTimerRef.current !== null) {
      window.clearTimeout(addedTimerRef.current);
      addedTimerRef.current = null;
    }
    pendingFocusRef.current = "trigger";
    setTouchQuickOpen(false);
    setFocusQuickOpen(false);
    setHoverQuickOpen(false);
    setAddedSize(null);
  };

  // Per design.md §4: never show "Sẵn hàng"; only show "Hết hàng" or "Còn N" when N <= 5
  const stockNotice = !hasAnyInStockForColor
    ? "Hết hàng"
    : activeColorStock <= 5
      ? `Còn ${activeColorStock}`
      : null;

  const productHref =
    activeColor && activeColor.code !== product.colors[0]?.code
      ? `/san-pham/${product.slug}?color=${encodeURIComponent(activeColor.code)}`
      : `/san-pham/${product.slug}`;

  const handleQuickAdd = async (size: SizeCode) => {
    if (addingSize !== null) return;
    const colorCode = activeColor?.code ?? product.colors[0]?.code;
    if (!colorCode) return;
    const sku = getSku(product, colorCode, size);
    if (!sku || sku.stock <= 0 || !sku.skuId) return;

    setAddingSize(size);
    try {
      const res = await addToCart({
        skuId: sku.skuId,
        quantity: 1,
        flashSaleItemId: flashSaleItemIdBySku?.[sku.skuId],
        productName: product.shortName,
        size,
      });
      if (res.unauthenticated) {
        const nextUrl = stripInternalSitePrefix(rawPathname);
        router.push(`/dang-nhap?next=${encodeURIComponent(nextUrl)}`);
        return;
      }
      if (res.ok) {
        setAddedSize(size);
        if (addedTimerRef.current !== null) {
          window.clearTimeout(addedTimerRef.current);
        }
        addedTimerRef.current = window.setTimeout(() => {
          addedTimerRef.current = null;
          setAddedSize((cur) => (cur === size ? null : cur));
        }, 1500);
      }
    } finally {
      setAddingSize(null);
    }
  };

  return (
    <article
      className={styles.card}
      onMouseEnter={() => setHoverQuickOpen(true)}
      onMouseLeave={() => setHoverQuickOpen(false)}
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) {
          setFocusQuickOpen(false);
        }
      }}
    >
      <div className={`${styles.mediaWrap} ${hoverImage ? styles.hasHoverImage : ""}`}>
        <Link
          ref={mediaLinkRef}
          href={productHref}
          className={styles.mediaLink}
          onFocus={() => {
            if (suppressNextFocusOpenRef.current) {
              suppressNextFocusOpenRef.current = false;
              return;
            }
            setFocusQuickOpen(true);
          }}
        >
          <Image
            src={displayImage}
            alt={activeColor ? `${product.name} - ${activeColor.name}` : product.name}
            fill
            sizes="(max-width: 640px) 50vw, (max-width: 1024px) 33vw, 25vw"
            priority={priority}
            className={styles.imagePrimary}
          />
          {hoverImage && (
            <Image
              src={hoverImage}
              alt={`${product.name} khi mặc thực tế`}
              fill
              sizes="(max-width: 640px) 50vw, (max-width: 1024px) 33vw, 25vw"
              className={styles.imageHover}
            />
          )}
        </Link>

        {discountLabel && (
          <span
            className={`${styles.discountTag} ${
              isFlashSaleItem || isShockDeal ? styles.discountTagAccent : ""
            } num`}
          >
            {discountLabel}
          </span>
        )}

        {showWishlist && (
          <button
            type="button"
            className={`${styles.wishlistBtn} ${wishlisted ? styles.wishlistActive : ""}`}
            onClick={() => toggleWishlist(product.id)}
            aria-label={
              wishlisted
                ? `Bỏ ${product.shortName} khỏi yêu thích`
                : `Lưu ${product.shortName} vào yêu thích`
            }
            aria-pressed={wishlisted}
          >
            <IconHeart size={18} filled={wishlisted} />
          </button>
        )}

        {canQuickAdd && !touchQuickOpen && (
          <button
            ref={touchQuickBtnRef}
            type="button"
            className={styles.touchQuickBtn}
            onClick={() => {
              pendingFocusRef.current = "tray";
              setTouchQuickOpen(true);
            }}
            aria-label={`Thêm nhanh ${accessibleName}`}
            aria-expanded={false}
          >
            <IconPlus size={18} />
          </button>
        )}

        {canShowQuickOverlay && (
          <div
            ref={quickOverlayRef}
            className={`${styles.quickOverlay} ${
              touchQuickOpen || addedSize !== null ? styles.quickOverlayOpen : ""
            } ${touchQuickOpen ? styles.quickOverlayTouch : ""}`}
          >
            <div className={styles.quickOverlayHead}>
              <p className={styles.quickOverlayLabel}>
                {addedSize ? `Đã thêm ${addedSize}` : addingSize ? "Đang thêm..." : "Chọn size"}
              </p>
              {touchQuickOpen && (
                <button
                  ref={quickCloseBtnRef}
                  type="button"
                  className={styles.quickCloseBtn}
                  onClick={closeTouchTray}
                  aria-label="Đóng chọn size"
                >
                  <IconClose size={15} />
                </button>
              )}
            </div>
            <div
              ref={sizeButtonsRef}
              className={styles.sizeButtons}
              role="group"
              aria-label={`Thêm nhanh ${accessibleName}`}
            >
              {product.sizes.map((sz) => {
                const sku = activeColor ? getSku(product, activeColor.code, sz) : undefined;
                const outOfStock = !sku || sku.stock <= 0 || !sku.skuId;
                const justAdded = addedSize === sz;
                return (
                  <button
                    key={sz}
                    type="button"
                    disabled={outOfStock || addingSize !== null}
                    className={`${styles.sizeBtn} ${justAdded ? styles.sizeBtnAdded : ""}`}
                    onClick={() => handleQuickAdd(sz)}
                    title={outOfStock ? `Size ${sz} hết hàng` : `Thêm size ${sz}`}
                    aria-label={
                      outOfStock ? `${accessibleName} size ${sz} hết hàng` : `Thêm nhanh size ${sz}`
                    }
                  >
                    {justAdded ? "Đã thêm" : sz}
                  </button>
                );
              })}
            </div>
          </div>
        )}
      </div>

      <div className={styles.body}>
        {product.colors.length > 1 && (
          <div
            className={styles.swatches}
            role="group"
            aria-label={`Chọn màu ${product.shortName}`}
          >
            {visibleColors.map((c) => {
              const isActive = c.code === activeColor?.code;
              if (!enableInteractiveSwatches) {
                return (
                  <span
                    key={c.code}
                    className={styles.swatch}
                    style={{ backgroundColor: c.hex }}
                    title={c.name}
                  />
                );
              }
              return (
                <button
                  key={c.code}
                  type="button"
                  className={`${styles.swatchBtn} ${isActive ? styles.swatchBtnActive : ""}`}
                  onClick={() => setSelectedColorCode(c.code)}
                  aria-label={`Xem màu ${c.name}`}
                  aria-pressed={isActive}
                  title={c.name}
                >
                  <span className={styles.swatch} style={{ backgroundColor: c.hex }} />
                </button>
              );
            })}
            {extraColorCount > 0 && (
              <Link
                href={productHref}
                className={styles.swatchMore}
                aria-label={`Xem thêm ${extraColorCount} màu khác`}
              >
                +{extraColorCount}
              </Link>
            )}
          </div>
        )}

        <Link href={productHref} className={styles.titleLink} title={product.name}>
          {product.name}
        </Link>

        <div className={styles.footerRow}>
          <Price
            amount={displayPrice}
            maxAmount={maxSkuPrice}
            originalAmount={displayOriginal}
            size="sm"
            showDiscount={false}
          />
          {quotaLabel ? (
            <span className={styles.quotaText}>{quotaLabel}</span>
          ) : stockNotice ? (
            <span
              className={`${styles.stockText} ${!hasAnyInStockForColor ? styles.stockOut : ""}`}
            >
              {stockNotice}
            </span>
          ) : null}
        </div>
      </div>
    </article>
  );
}
