import React from "react";
import Link from "next/link";
import type { Product } from "@/data/products";
import { formatDiscountPercent, formatInt, formatVnd } from "@/lib/format";
import type { SiteCardStyle } from "./templateTypes";
import { sanitizeSafeImageUrl } from "./templateValidator";
import tplStyles from "./siteTemplates.module.css";

export interface SiteProductCardProps {
  product: Product;
  previewMode?: boolean;
  index?: number;
  cardStyle?: SiteCardStyle;
  /** Shows a short description snippet on editorial / magazine cards. */
  showExcerpt?: boolean;
  /** Expands to a full-row horizontal layout when a section only contains 1 product (zero orphan). */
  layoutMode?: "default" | "single-wide";
}

export function SiteProductCard({
  product,
  previewMode = false,
  index = 0,
  cardStyle = "minimal",
  showExcerpt = false,
  layoutMode = "default",
}: SiteProductCardProps) {
  const candidates = Array.from(
    new Set(
      [
        product.onModelImage,
        product.image,
        ...(product.gallery ?? []),
        ...product.colors.map((c) => c.image),
      ]
        .map((u) => sanitizeSafeImageUrl(u))
        .filter(Boolean)
    )
  );
  const primaryImg = candidates[0] || "/banners/fabric-closeup.jpg";
  const secondaryImg = candidates.length > 1 ? candidates[1] : null;

  const displayPrice = product.price;
  const comparePrice = product.originalPrice;
  const hasDiscount = typeof comparePrice === "number" && comparePrice > displayPrice;
  const discountLabel =
    hasDiscount && comparePrice ? formatDiscountPercent(comparePrice, displayPrice) : "";

  const totalStock = product.skus.reduce((sum, sku) => sum + Math.max(0, sku.stock), 0);
  const stockNotice =
    totalStock <= 0 ? "Tạm hết hàng" : totalStock <= 5 ? `Còn ${formatInt(totalStock)}` : "";

  const availableSizes = Array.from(
    new Set(product.skus.filter((s) => s.stock > 0).map((s) => s.size))
  );
  const href = `/san-pham/${encodeURIComponent(product.slug)}`;
  const isSingleWide = layoutMode === "single-wide";

  const mediaContent = (
    <>
      <img
        src={primaryImg}
        alt={product.name}
        width={896}
        height={1200}
        className={tplStyles.cardImgPrimary}
        loading={index < 4 ? "eager" : "lazy"}
      />
      {secondaryImg && (
        <img
          src={secondaryImg}
          alt=""
          aria-hidden="true"
          width={896}
          height={1200}
          className={tplStyles.cardImgSecondary}
          loading="lazy"
        />
      )}
    </>
  );

  return (
    <article
      className={`${tplStyles.card} ${isSingleWide ? tplStyles.cardSingleWide : ""}`}
      data-card-style={cardStyle}
      data-has-hover={secondaryImg ? "true" : "false"}
    >
      {previewMode ? (
        <div className={tplStyles.cardImgWrap}>{mediaContent}</div>
      ) : (
        <Link href={href} className={tplStyles.cardImgWrap} tabIndex={-1} aria-hidden="true">
          {mediaContent}
        </Link>
      )}

      <div className={tplStyles.cardBody}>
        <div className={tplStyles.cardMetaTop}>
          {product.colors.length > 0 && (
            <div className={tplStyles.cardSwatchRow} aria-label={`${product.colors.length} màu`}>
              {product.colors.slice(0, 5).map((c) => (
                <span
                  key={c.code}
                  className={tplStyles.cardSwatchDot}
                  style={{ backgroundColor: c.hex }}
                  title={c.name}
                />
              ))}
              {product.colors.length > 5 && (
                <span className={`${tplStyles.cardSwatchMore} num`}>
                  +{product.colors.length - 5}
                </span>
              )}
            </div>
          )}

          {product.isFlashSale ? (
            <span className={tplStyles.cardTagText}>Flash sale</span>
          ) : discountLabel ? (
            <span className={`${tplStyles.cardTagText} num`}>{discountLabel}</span>
          ) : null}
        </div>

        {(showExcerpt || isSingleWide) && product.description && (
          <p className={tplStyles.cardExcerpt}>{product.description}</p>
        )}

        {cardStyle === "compact-row" && availableSizes.length > 0 && (
          <div className={tplStyles.cardSkuMetaRow}>
            {availableSizes.slice(0, 5).map((sz) => (
              <span key={sz} className={tplStyles.cardSizeChip}>
                {sz}
              </span>
            ))}
          </div>
        )}

        <div className={tplStyles.cardPriceRow}>
          <span className={`${tplStyles.cardPrice} num`}>{formatVnd(displayPrice)}</span>
          {hasDiscount && comparePrice && (
            <span className={`${tplStyles.cardComparePrice} num`}>{formatVnd(comparePrice)}</span>
          )}
          {stockNotice && (
            <span
              className={`${tplStyles.cardStockLabel} ${
                totalStock <= 0 ? tplStyles.cardStockOut : ""
              } num`}
            >
              {stockNotice}
            </span>
          )}
        </div>

        {/* Keep product title link as the LAST a[href^="/san-pham/"] inside article (buyer-flow E2E invariant). */}
        {previewMode ? (
          <h3 className={tplStyles.cardTitle}>{product.name}</h3>
        ) : (
          <Link href={href} className={tplStyles.cardTitle}>
            {product.name}
          </Link>
        )}
      </div>
    </article>
  );
}
