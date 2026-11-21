"use client";

import React, { useMemo, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { BADGE_LABELS, getSku, type Product, type SizeCode } from "@/data/products";
import type { Shop } from "@/data/shops";
import { formatDiscountPercent } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { IconHeart } from "./Icons";
import { Price } from "./Price";
import styles from "./ProductCard.module.css";

export interface ProductCardProps {
  product: Product;
  shop?: Shop;
  overridePrice?: number;
  overrideOriginalPrice?: number;
  quotaLabel?: string;
  showSpec?: boolean;
  priority?: boolean;
  enableQuickAdd?: boolean;
  enableInteractiveSwatches?: boolean;
  showWishlist?: boolean;
  showBadges?: boolean;
  showSla?: boolean;
  maxVisibleSwatches?: number;
  isFlashSaleItem?: boolean;
  isShockDeal?: boolean;
  flashSaleItemIdBySku?: Record<string, string>;
}

type ProductWithOnModel = Product & {
  onModelImage?: string;
};

function stripInternalSitePrefix(rawPath: string | null): string {
  const path = rawPath || "/";
  const m = path.match(/^\/s\/[^/]+(\/.*)?$/);
  return m ? m[1] || "/" : path;
}

export function ProductCard({
  product,
  shop,
  overridePrice,
  overrideOriginalPrice,
  quotaLabel,
  showSpec = false,
  priority = false,
  enableQuickAdd = true,
  enableInteractiveSwatches = true,
  showWishlist = true,
  showBadges = true,
  showSla = false,
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

  const showQuickOverlay = hoverQuickOpen || focusQuickOpen || touchQuickOpen || addedSize !== null;

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
  const displayOriginal = overrideOriginalPrice ?? product.originalPrice;
  const hasDiscount = displayOriginal != null && displayOriginal > displayPrice;
  const discountLabel = hasDiscount ? formatDiscountPercent(displayOriginal, displayPrice) : null;

  const wishlisted = isWishlisted(product.id);
  const primaryBadge = showBadges ? product.badges?.[0] : undefined;
  const visibleColors = product.colors.slice(0, maxVisibleSwatches);
  const extraColorCount = Math.max(0, product.colors.length - maxVisibleSwatches);
  const isComboProduct = Boolean(
    product.isCombo || (product.comboSlots && product.comboSlots.length > 0)
  );
  const hasAnyInStockForColor = product.sizes.some((sz) => {
    const sku = activeColor ? getSku(product, activeColor.code, sz) : undefined;
    return Boolean(sku && sku.stock > 0 && sku.skuId);
  });

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
        window.setTimeout(() => {
          setAddedSize((cur) => (cur === size ? null : cur));
        }, 1500);
      }
    } finally {
      setAddingSize(null);
    }
  };

  const specParts = [
    shop?.shortName,
    product.fabric || undefined,
    product.gsm > 0 ? `${product.gsm}gsm` : undefined,
    showSpec ? product.fit || undefined : undefined,
  ].filter(Boolean);

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
          href={productHref}
          className={styles.mediaLink}
          onFocus={() => setFocusQuickOpen(true)}
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

        {(discountLabel || primaryBadge) && (
          <div className={styles.badgeCluster}>
            {discountLabel && (
              <span
                className={`${styles.discountBadge} ${
                  isFlashSaleItem || isShockDeal ? styles.discountBadgeAccent : ""
                } num`}
              >
                {discountLabel}
              </span>
            )}
            {primaryBadge && (
              <span
                className={`${styles.badge} ${
                  primaryBadge === "outlet" ? styles.badgeOutlet : ""
                } ${primaryBadge === "pre-order" ? styles.badgePreorder : ""}`}
              >
                {BADGE_LABELS[primaryBadge]}
              </span>
            )}
          </div>
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
            <IconHeart size={15} filled={wishlisted} />
          </button>
        )}

        {enableQuickAdd && showQuickOverlay && (
          <div className={styles.quickOverlay}>
            {isComboProduct ? (
              <Link href={productHref} className={styles.comboLink}>
                Chọn size combo
              </Link>
            ) : !hasAnyInStockForColor ? (
              <Link href={productHref} className={styles.comboLink}>
                Tạm hết hàng
              </Link>
            ) : (
              <>
                <p className={styles.quickOverlayLabel}>
                  {addedSize
                    ? `Đã thêm size ${addedSize}`
                    : addingSize
                      ? "Đang thêm vào giỏ..."
                      : "Thêm nhanh theo size"}
                </p>
                <div
                  className={styles.sizeButtons}
                  role="group"
                  aria-label={`Thêm nhanh ${product.shortName}`}
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
                        title={
                          outOfStock
                            ? `Size ${sz} đã hết hàng`
                            : `Thêm nhanh ${product.shortName} size ${sz} vào giỏ`
                        }
                        aria-label={
                          outOfStock
                            ? `${product.shortName} size ${sz} hết hàng`
                            : `Thêm nhanh size ${sz}`
                        }
                      >
                        {justAdded ? "Đã thêm" : sz}
                      </button>
                    );
                  })}
                </div>
              </>
            )}
          </div>
        )}
      </div>

      <div className={styles.body}>
        {product.colors.length > 0 && (
          <div className={styles.metaRow}>
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
          </div>
        )}

        <Link href={productHref} className={styles.titleLink}>
          {product.name}
        </Link>

        {specParts.length > 0 && (
          <p className={showSpec ? styles.specLine : styles.shopName}>{specParts.join(" · ")}</p>
        )}

        {(showSla || showSpec) && product.slaText && (
          <p className={styles.slaLine}>{product.slaText}</p>
        )}

        <div className={styles.footerRow}>
          <Price
            amount={displayPrice}
            originalAmount={displayOriginal}
            size="sm"
            showDiscount={false}
          />
          {quotaLabel && <span className={styles.quotaText}>{quotaLabel}</span>}
        </div>

        {enableQuickAdd && (
          <div className={styles.touchQuickWrap}>
            {isComboProduct ? (
              <Link href={productHref} className={styles.touchQuickBtn}>
                Chọn size combo
              </Link>
            ) : !hasAnyInStockForColor ? (
              <Link href={productHref} className={styles.touchQuickBtn}>
                Tạm hết hàng
              </Link>
            ) : (
              <button
                type="button"
                className={styles.touchQuickBtn}
                aria-expanded={touchQuickOpen}
                onClick={(e) => {
                  const next = !touchQuickOpen;
                  setTouchQuickOpen(next);
                  if (!next) {
                    setFocusQuickOpen(false);
                    setHoverQuickOpen(false);
                    e.currentTarget.blur();
                  }
                }}
              >
                {addedSize
                  ? `Đã thêm size ${addedSize}`
                  : touchQuickOpen
                    ? "Đóng chọn size"
                    : "Thêm nhanh"}
              </button>
            )}
          </div>
        )}
      </div>
    </article>
  );
}
