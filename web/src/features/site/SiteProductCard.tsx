import React from "react";
import Link from "next/link";
import type { Product } from "@/data/products";
import { formatDiscountPercent, formatVnd } from "@/lib/format";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import styles from "./site.module.css";

export interface SiteProductCardProps {
  product: Product;
  previewMode?: boolean;
  index?: number;
}

export function SiteProductCard({ product, previewMode = false, index = 0 }: SiteProductCardProps) {
  const imageSrc =
    sanitizeSafeUrl(product.onModelImage) ||
    sanitizeSafeUrl(product.image) ||
    "/banners/fabric-closeup.jpg";
  const displayPrice = product.price;
  const comparePrice = product.originalPrice;
  const hasDiscount = typeof comparePrice === "number" && comparePrice > displayPrice;
  const totalStock = product.skus.reduce((sum, sku) => sum + Math.max(0, sku.stock), 0);
  const href = `/san-pham/${encodeURIComponent(product.slug)}`;

  const cardImage = (
    <>
      <img
        src={imageSrc}
        alt={product.name}
        width={896}
        height={1200}
        className={styles.productImg}
        loading={index < 4 ? "eager" : "lazy"}
      />
      {product.isFlashSale ? (
        <span className={styles.productBadge}>Flash sale</span>
      ) : hasDiscount && comparePrice ? (
        <span className={styles.productBadge}>
          {formatDiscountPercent(comparePrice, displayPrice)}
        </span>
      ) : null}
    </>
  );

  return (
    <article className={styles.productCard} data-reveal="">
      {previewMode ? (
        <div className={styles.productImgWrap}>{cardImage}</div>
      ) : (
        <Link href={href} className={styles.productImgWrap}>
          {cardImage}
        </Link>
      )}

      <div className={styles.productBody}>
        {previewMode ? (
          <h3 className={styles.productName}>{product.name}</h3>
        ) : (
          <Link href={href} className={styles.productName}>
            {product.name}
          </Link>
        )}

        {(product.fabric || product.gsm > 0) && (
          <p className={styles.productSpec}>
            {[product.fabric, product.gsm > 0 ? `${product.gsm}gsm` : ""]
              .filter(Boolean)
              .join(" · ")}
          </p>
        )}

        <div className={styles.productPriceRow}>
          <span className={`${styles.productPrice} num`}>{formatVnd(displayPrice)}</span>
          {hasDiscount && comparePrice ? (
            <span className={`${styles.productOriginalPrice} num`}>{formatVnd(comparePrice)}</span>
          ) : (
            <span className={styles.productStockTag}>
              {totalStock > 0 ? "Sẵn hàng" : "Tạm hết hàng"}
            </span>
          )}
        </div>
      </div>
    </article>
  );
}
