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
}

export function SiteProductCard({
  product,
  previewMode = false,
  index = 0,
  cardStyle = "minimal",
  showExcerpt = false,
}: SiteProductCardProps) {
  const imageSrc =
    sanitizeSafeImageUrl(product.onModelImage) ||
    sanitizeSafeImageUrl(product.image) ||
    "/banners/fabric-closeup.jpg";
  const displayPrice = product.price;
  const comparePrice = product.originalPrice;
  const hasDiscount = typeof comparePrice === "number" && comparePrice > displayPrice;
  const totalStock = product.skus.reduce((sum, sku) => sum + Math.max(0, sku.stock), 0);
  const availableSizes = Array.from(
    new Set(product.skus.filter((s) => s.stock > 0).map((s) => s.size))
  );
  const href = `/san-pham/${encodeURIComponent(product.slug)}`;

  const cardImage = (
    <>
      <img
        src={imageSrc}
        alt={product.name}
        width={896}
        height={1200}
        className={tplStyles.cardImg}
        loading={index < 4 ? "eager" : "lazy"}
      />
      {product.isFlashSale ? (
        <span className={tplStyles.cardBadge}>Flash sale</span>
      ) : hasDiscount && comparePrice ? (
        <span className={tplStyles.cardBadge}>
          {formatDiscountPercent(comparePrice, displayPrice)}
        </span>
      ) : null}
    </>
  );

  return (
    <article className={tplStyles.card} data-card-style={cardStyle} data-reveal="">
      {previewMode ? (
        <div className={tplStyles.cardImgWrap}>{cardImage}</div>
      ) : (
        <Link href={href} className={tplStyles.cardImgWrap}>
          {cardImage}
        </Link>
      )}

      <div className={tplStyles.cardBody}>
        {cardStyle === "atelier" && product.gsm > 0 && (
          <span className={tplStyles.cardCraftTag}>{product.gsm}gsm · Chuẩn xưởng</span>
        )}

        {previewMode ? (
          <h3 className={tplStyles.cardTitle}>{product.name}</h3>
        ) : (
          <Link href={href} className={tplStyles.cardTitle}>
            {product.name}
          </Link>
        )}

        {(product.fabric || product.gsm > 0) && (
          <p className={tplStyles.cardSpec}>
            {[product.fabric, product.gsm > 0 && cardStyle !== "atelier" ? `${product.gsm}gsm` : ""]
              .filter(Boolean)
              .join(" · ")}
          </p>
        )}

        {(showExcerpt || cardStyle === "editorial") && product.description && (
          <p className={tplStyles.cardExcerpt}>{product.description}</p>
        )}

        {cardStyle === "compact-row" && availableSizes.length > 0 && (
          <div className={tplStyles.cardSkuMetaRow}>
            {availableSizes.slice(0, 5).map((sz) => (
              <span key={sz} className={tplStyles.cardSizeChip}>
                {sz}
              </span>
            ))}
            <span className={`${tplStyles.cardStockPill} num`}>
              {product.colors.length} màu · {formatInt(totalStock)} sẵn kho
            </span>
          </div>
        )}

        <div className={tplStyles.cardPriceRow}>
          <span className={`${tplStyles.cardPrice} num`}>{formatVnd(displayPrice)}</span>
          {hasDiscount && comparePrice ? (
            <span className={`${tplStyles.cardComparePrice} num`}>{formatVnd(comparePrice)}</span>
          ) : (
            <span className={`${tplStyles.cardStockPill} num`}>
              {totalStock > 0
                ? cardStyle === "dense"
                  ? `Còn ${formatInt(totalStock)}`
                  : "Sẵn hàng"
                : "Tạm hết hàng"}
            </span>
          )}
        </div>
      </div>
    </article>
  );
}
