"use client";

import React, { useMemo, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import {
  BADGE_LABELS,
  computeReviewSummary,
  getShopById,
  getSku,
  type Product,
  type SizeCode,
} from "@/data";
import { formatDiscountPercent } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import { IconHeart, IconStar } from "./Icons";
import { Price } from "./Price";
import styles from "./ProductCard.module.css";

export interface ProductCardProps {
  product: Product;
  overridePrice?: number;
  overrideOriginalPrice?: number;
  quotaLabel?: string;
  showSpec?: boolean;
  priority?: boolean;
  // Interactive options (backward compatible defaults)
  enableQuickAdd?: boolean;
  enableInteractiveSwatches?: boolean;
  showWishlist?: boolean;
  showBadges?: boolean;
  showSla?: boolean;
  showRating?: boolean;
  maxVisibleSwatches?: number;
  isFlashSaleItem?: boolean;
  isShockDeal?: boolean;
}

type ProductWithOnModel = Product & {
  onModelImage?: string;
};

/**
 * Deterministic sample rating fallback for products that do not yet have
 * explicit rows in SAMPLE_REVIEWS (listed as sample UI data in DESIGN_REPORT.md).
 */
function getFallbackSampleRating(product: Product): { average: number; count: number } {
  let hash = 0;
  for (let i = 0; i < product.id.length; i++) {
    hash = (hash * 31 + product.id.charCodeAt(i)) % 997;
  }
  const averages = [4.7, 4.8, 4.9, 4.8, 4.7, 4.9];
  const average = averages[hash % averages.length];
  const rankBoost = product.salesRank ? Math.max(0, 18 - product.salesRank) : 4;
  const count = 12 + (hash % 19) + rankBoost;
  return { average, count };
}

export function ProductCard({
  product,
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
  showRating = true,
  maxVisibleSwatches = 4,
  isFlashSaleItem = false,
  isShockDeal = false,
}: ProductCardProps) {
  const { addToCart, toggleWishlist, isWishlisted, reviews } = useStore();
  const [selectedColorCode, setSelectedColorCode] = useState<string>(product.colors[0]?.code ?? "");
  const [addedSize, setAddedSize] = useState<SizeCode | null>(null);
  const [hoverQuickOpen, setHoverQuickOpen] = useState(false);
  const [focusQuickOpen, setFocusQuickOpen] = useState(false);
  const [touchQuickOpen, setTouchQuickOpen] = useState(false);

  const showQuickOverlay = hoverQuickOpen || focusQuickOpen || touchQuickOpen || addedSize !== null;

  const shop = getShopById(product.shopId);
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

  const ratingInfo = useMemo(() => {
    const summary = computeReviewSummary(reviews, product.id);
    if (summary.count > 0) {
      return { average: summary.average, count: summary.count };
    }
    return getFallbackSampleRating(product);
  }, [reviews, product]);

  const productHref =
    activeColor && activeColor.code !== product.colors[0]?.code
      ? `/san-pham/${product.slug}?color=${encodeURIComponent(activeColor.code)}`
      : `/san-pham/${product.slug}`;

  const handleQuickAdd = (size: SizeCode) => {
    const colorCode = activeColor?.code ?? product.colors[0]?.code;
    if (!colorCode) return;
    const ok = addToCart({
      productId: product.id,
      colorCode,
      size,
      quantity: 1,
      overridePrice,
      isFlashSaleItem,
      isShockDeal,
    });
    if (ok) {
      setAddedSize(size);
      window.setTimeout(() => {
        setAddedSize((cur) => (cur === size ? null : cur));
      }, 1500);
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
            ) : (
              <>
                <p className={styles.quickOverlayLabel}>
                  {addedSize ? `Đã thêm size ${addedSize}` : "Thêm nhanh theo size"}
                </p>
                <div
                  className={styles.sizeButtons}
                  role="group"
                  aria-label={`Thêm nhanh ${product.shortName}`}
                >
                  {product.sizes.map((sz) => {
                    const sku = activeColor ? getSku(product, activeColor.code, sz) : undefined;
                    const outOfStock = !sku || sku.stock <= 0;
                    const justAdded = addedSize === sz;
                    return (
                      <button
                        key={sz}
                        type="button"
                        disabled={outOfStock}
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
        <div className={styles.metaRow}>
          {product.colors.length > 0 && (
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

          {showRating && (
            <span
              className={`${styles.ratingText} num`}
              title="Điểm đánh giá mẫu minh họa giao diện"
              aria-label={`Đánh giá ${ratingInfo.average.toFixed(1)} trên 5 từ ${ratingInfo.count} nhận xét mẫu`}
            >
              <IconStar size={13} filled className={styles.ratingStar} />
              <span>{ratingInfo.average.toFixed(1).replace(".", ",")}</span>
              <span className={styles.ratingCount}>({ratingInfo.count})</span>
            </span>
          )}
        </div>

        <Link href={productHref} className={styles.titleLink}>
          {product.name}
        </Link>

        {showSpec ? (
          <p className={styles.specLine}>
            {shop?.shortName ?? "NexusMart"} · {product.gsm}gsm · {product.fit}
          </p>
        ) : (
          <p className={styles.shopName}>{shop?.shortName ?? shop?.name ?? "NexusMart"}</p>
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
