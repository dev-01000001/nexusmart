"use client";

import React, { useMemo, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { BADGE_LABELS, getShopById, type Product, type SizeCode } from "@/data";
import { resolveGatewaySkuId } from "@/lib/api";
import {
  findProductSku,
  isCatalogProduct,
  isCatalogSku,
  type CatalogProduct,
} from "@/lib/catalog-types";
import { formatDiscountPercent, formatRatingFromX100 } from "@/lib/format";
import { useCart } from "@/context/CartContext";
import { useSession } from "@/context/SessionContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { IconHeart, IconStar } from "./Icons";
import { Price } from "./Price";
import styles from "./ProductCard.module.css";

export interface ProductCardProps {
  product: Product | CatalogProduct;
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
  // Additive props for real backend catalog & flash sale direct checkout
  shopName?: string;
  flashSaleItemId?: string;
  flashSaleSkuId?: string;
  flashSaleDisabled?: boolean;
}

type ProductWithOnModel = (Product | CatalogProduct) & {
  onModelImage?: string;
};

function getCurrentReturnPath(fallbackPathname: string): string {
  if (typeof window !== "undefined") {
    return `${window.location.pathname}${window.location.search}`;
  }
  return fallbackPathname || "/";
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
  shopName,
  flashSaleItemId,
  flashSaleSkuId,
  flashSaleDisabled = false,
}: ProductCardProps) {
  const router = useRouter();
  const pathname = usePathname();
  const { isAuthenticated } = useSession();
  const { addToCart } = useCart();
  const { toggleWishlist, isWishlisted, showToast } = useMockStore();

  const [selectedColorCode, setSelectedColorCode] = useState<string>(product.colors[0]?.code ?? "");
  const [addedSize, setAddedSize] = useState<SizeCode | null>(null);
  const [addingSize, setAddingSize] = useState<SizeCode | null>(null);
  const [hoverQuickOpen, setHoverQuickOpen] = useState(false);
  const [focusQuickOpen, setFocusQuickOpen] = useState(false);
  const [touchQuickOpen, setTouchQuickOpen] = useState(false);

  const showQuickOverlay =
    hoverQuickOpen || focusQuickOpen || touchQuickOpen || addedSize !== null || addingSize !== null;

  const catalogProduct = isCatalogProduct(product) ? product : null;
  const legacyShop = catalogProduct ? undefined : getShopById(product.shopId);
  const resolvedShopLabel =
    shopName ??
    catalogProduct?.shopShortName ??
    catalogProduct?.shopName ??
    legacyShop?.shortName ??
    legacyShop?.name ??
    "NexusMart";

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
  const displayMaxPrice =
    overridePrice == null && catalogProduct && catalogProduct.maxPriceVnd > displayPrice
      ? catalogProduct.maxPriceVnd
      : undefined;
  const displayOriginal = overrideOriginalPrice ?? product.originalPrice;
  const hasDiscount = displayOriginal != null && displayOriginal > displayPrice;
  const discountLabel = hasDiscount ? formatDiscountPercent(displayOriginal, displayPrice) : null;

  const wishlisted = isWishlisted(product.id);
  const primaryBadge = showBadges ? product.badges?.[0] : undefined;
  const visibleColors = product.colors.slice(0, maxVisibleSwatches);
  const extraColorCount = Math.max(0, product.colors.length - maxVisibleSwatches);
  const isDirectFlashCheckout = Boolean(isFlashSaleItem && flashSaleItemId && flashSaleSkuId);

  const ratingInfo = useMemo(() => {
    if (!catalogProduct) return null;
    const count = Math.max(0, Math.trunc(catalogProduct.reviewCount ?? 0));
    const avgX100 = Math.max(0, Math.min(500, Math.trunc(catalogProduct.ratingAvgX100 ?? 0)));
    return {
      count,
      formatted: formatRatingFromX100(avgX100),
    };
  }, [catalogProduct]);

  const productHref =
    activeColor && activeColor.code !== product.colors[0]?.code
      ? `/san-pham/${product.slug}?color=${encodeURIComponent(activeColor.code)}`
      : `/san-pham/${product.slug}`;

  const flashCheckoutHref = isDirectFlashCheckout
    ? `/thanh-toan?sku=${encodeURIComponent(flashSaleSkuId!)}&qty=1&flash=${encodeURIComponent(flashSaleItemId!)}`
    : productHref;

  const handleFlashBuyNow = () => {
    if (flashSaleDisabled || !isDirectFlashCheckout) return;
    if (!isAuthenticated) {
      router.push(`/dang-nhap?next=${encodeURIComponent(flashCheckoutHref)}`);
      return;
    }
    router.push(flashCheckoutHref);
  };

  const handleQuickAdd = async (size: SizeCode) => {
    const colorCode = activeColor?.code ?? product.colors[0]?.code;
    if (!colorCode || addingSize !== null) return;

    if (!isAuthenticated) {
      const nextPath = getCurrentReturnPath(pathname);
      router.push(`/dang-nhap?next=${encodeURIComponent(nextPath)}`);
      return;
    }

    const matchedSku = findProductSku(product, colorCode, size);
    if (!matchedSku || matchedSku.stock <= 0) {
      showToast("Phân loại sản phẩm này hiện đã hết hàng.");
      return;
    }

    setAddingSize(size);
    try {
      let realSkuId: string | null = isCatalogSku(matchedSku) ? matchedSku.id : null;

      // Resolve exact backend SKU UUID for search hits / editorial cards without eager N+1 SKU loads
      if (!realSkuId) {
        const hasRealColor = Boolean(activeColor && activeColor.code !== "mac-dinh");
        const hasRealSize = product.sizes.length > 0 && Boolean(size);
        realSkuId = await resolveGatewaySkuId(product.slug, {
          skuCode: matchedSku.skuCode || undefined,
          colorCode: hasRealColor ? colorCode : undefined,
          colorName: hasRealColor ? activeColor?.name : undefined,
          size: hasRealSize ? size : undefined,
        });
        if (!realSkuId) {
          showToast("Phân loại sản phẩm này hiện đã hết hàng.");
          return;
        }
      }

      const res = await addToCart({ skuId: realSkuId, quantity: 1 });
      if (res.ok) {
        setAddedSize(size);
        if (res.clampedToStock) {
          showToast(res.message ?? "Số lượng trong giỏ đã được điều chỉnh theo tồn kho hiện còn.");
        } else if (res.message) {
          showToast(res.message);
        }
        window.setTimeout(() => {
          setAddedSize((cur) => (cur === size ? null : cur));
        }, 1500);
      } else {
        showToast(res.message ?? "Không thể thêm sản phẩm vào giỏ hàng lúc này.");
      }
    } catch {
      showToast("Không thể thêm sản phẩm vào giỏ hàng lúc này.");
    } finally {
      setAddingSize(null);
    }
  };

  const specParts = [
    resolvedShopLabel,
    product.gsm > 0 ? `${product.gsm}gsm` : "",
    product.fit || "",
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
            onClick={() =>
              toggleWishlist(product.id, undefined, {
                name: product.name,
                slug: product.slug,
                price: product.price,
                originalPrice: product.originalPrice,
                image: product.image,
                shopName: resolvedShopLabel,
              })
            }
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
            {isDirectFlashCheckout ? (
              <button
                type="button"
                className={styles.comboLink}
                disabled={flashSaleDisabled}
                onClick={handleFlashBuyNow}
              >
                {flashSaleDisabled ? "Đã hết suất" : "Mua ngay ưu đãi"}
              </button>
            ) : product.sizes.length > 0 ? (
              <>
                <p className={styles.quickOverlayLabel}>
                  {addingSize
                    ? `Đang thêm size ${addingSize}...`
                    : addedSize
                      ? `Đã thêm size ${addedSize}`
                      : "Thêm nhanh theo size"}
                </p>
                <div
                  className={styles.sizeButtons}
                  role="group"
                  aria-label={`Thêm nhanh ${product.shortName}`}
                >
                  {product.sizes.map((sz) => {
                    const sku = activeColor
                      ? findProductSku(product, activeColor.code, sz)
                      : undefined;
                    const outOfStock = !sku || sku.stock <= 0;
                    const justAdded = addedSize === sz;
                    const isAdding = addingSize === sz;
                    return (
                      <button
                        key={sz}
                        type="button"
                        disabled={outOfStock || addingSize !== null}
                        className={`${styles.sizeBtn} ${justAdded ? styles.sizeBtnAdded : ""}`}
                        onClick={() => void handleQuickAdd(sz)}
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
                        {isAdding ? "..." : justAdded ? "Đã thêm" : sz}
                      </button>
                    );
                  })}
                </div>
              </>
            ) : (
              <Link href={productHref} className={styles.comboLink}>
                Xem chi tiết
              </Link>
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

          {showRating &&
            ratingInfo &&
            (ratingInfo.count > 0 ? (
              <span
                className={`${styles.ratingText} num`}
                aria-label={`Đánh giá ${ratingInfo.formatted} trên 5 từ ${ratingInfo.count} đánh giá`}
              >
                <IconStar size={13} filled className={styles.ratingStar} />
                <span>{ratingInfo.formatted}</span>
                <span className={styles.ratingCount}>({ratingInfo.count})</span>
              </span>
            ) : (
              <span className={styles.ratingCount}>Chưa có đánh giá</span>
            ))}
        </div>

        <Link href={productHref} className={styles.titleLink}>
          {product.name}
        </Link>

        {showSpec ? (
          <p className={styles.specLine}>{specParts.join(" · ")}</p>
        ) : (
          <p className={styles.shopName}>{resolvedShopLabel}</p>
        )}

        {(showSla || showSpec) && product.slaText && (
          <p className={styles.slaLine}>{product.slaText}</p>
        )}

        <div className={styles.footerRow}>
          <Price
            amount={displayPrice}
            maxAmount={displayMaxPrice}
            originalAmount={displayOriginal}
            size="sm"
            showDiscount={false}
          />
          {quotaLabel && <span className={styles.quotaText}>{quotaLabel}</span>}
        </div>

        {enableQuickAdd && (
          <div className={styles.touchQuickWrap}>
            {isDirectFlashCheckout ? (
              <button
                type="button"
                className={styles.touchQuickBtn}
                disabled={flashSaleDisabled}
                onClick={handleFlashBuyNow}
              >
                {flashSaleDisabled ? "Đã hết suất" : "Mua ngay ưu đãi"}
              </button>
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
