"use client";

import React, { useState } from "react";
import Link from "next/link";
import type { Product } from "@/data/products";
import { formatInt, formatVnd } from "@/lib/format";
import { SafeActionLink } from "./SafeActionLink";
import { sanitizeSafeImageUrl } from "./templateValidator";
import styles from "./site.module.css";
import tplStyles from "./siteTemplates.module.css";

export interface MultiSkuDirectoryProps {
  title: string;
  subtitle?: string;
  ctaLabel: string;
  ctaHref: string;
  products: Product[];
  fallbackImage: string;
  isFirstHeading: boolean;
  previewMode?: boolean;
}

/**
 * Signature hero for `multi-sku-catalog`:
 * Typographic row-by-row product directory where hovering or focusing any row
 * swaps the anchored preview image on the right.
 */
export function MultiSkuDirectory({
  title,
  subtitle,
  ctaLabel,
  ctaHref,
  products,
  fallbackImage,
  isFirstHeading,
  previewMode = false,
}: MultiSkuDirectoryProps) {
  const listed = products.slice(0, 7);
  const [activeIdx, setActiveIdx] = useState(0);
  const HeadingTag = isFirstHeading ? "h1" : "h2";

  const activeProduct = listed[activeIdx] ?? listed[0];
  const previewImg =
    sanitizeSafeImageUrl(activeProduct?.onModelImage) ||
    sanitizeSafeImageUrl(activeProduct?.image) ||
    fallbackImage;

  const totalSkus = products.reduce((sum, p) => sum + p.skus.length, 0);

  return (
    <div className={tplStyles.skuDirWrap}>
      <div className={tplStyles.skuDirTop}>
        <div className={tplStyles.skuDirHeadCopy}>
          <HeadingTag className={tplStyles.skuDirTitle}>{title}</HeadingTag>
          {subtitle?.trim() && <p className={tplStyles.skuDirSub}>{subtitle}</p>}
        </div>
        <div className={tplStyles.skuDirHeadMeta}>
          <span className={`${tplStyles.skuDirCount} num`}>
            {formatInt(products.length)} mẫu, {formatInt(totalSkus)} SKU
          </span>
          <SafeActionLink
            href={ctaHref}
            fallbackHref="/san-pham"
            label={ctaLabel}
            className={styles.btnPrimary}
            previewMode={previewMode}
          />
        </div>
      </div>

      {listed.length > 0 && (
        <div className={tplStyles.skuDirBody}>
          <ul className={tplStyles.skuDirList} data-reveal="rows">
            {listed.map((prod, idx) => {
              const totalStock = prod.skus.reduce((s, k) => s + Math.max(0, k.stock), 0);
              const sizeRange =
                prod.sizes.length > 1
                  ? `${prod.sizes[0]} tới ${prod.sizes[prod.sizes.length - 1]}`
                  : prod.sizes[0] || "Free";
              const isActive = idx === activeIdx;
              const rowInner = (
                <>
                  <span className={tplStyles.skuDirRowName}>{prod.name}</span>
                  <span className={tplStyles.skuDirRowSpecs}>
                    <span>{prod.colors.length} màu</span>
                    <span>{sizeRange}</span>
                    <span className="num">{formatInt(totalStock)} kho</span>
                  </span>
                  <span className={`${tplStyles.skuDirRowPrice} num`}>{formatVnd(prod.price)}</span>
                </>
              );

              return (
                <li
                  key={prod.id}
                  className={`${tplStyles.skuDirItem} ${isActive ? tplStyles.skuDirItemActive : ""}`}
                  onMouseEnter={() => setActiveIdx(idx)}
                  onFocus={() => setActiveIdx(idx)}
                >
                  {previewMode ? (
                    <button
                      type="button"
                      className={tplStyles.skuDirRowBtn}
                      onClick={() => setActiveIdx(idx)}
                    >
                      {rowInner}
                    </button>
                  ) : (
                    <Link
                      href={`/san-pham/${encodeURIComponent(prod.slug)}`}
                      className={tplStyles.skuDirRowBtn}
                    >
                      {rowInner}
                    </Link>
                  )}
                </li>
              );
            })}
          </ul>

          <aside className={tplStyles.skuDirPreview} aria-live="polite">
            <div className={tplStyles.skuDirPreviewFrame}>
              <img
                key={previewImg}
                src={previewImg}
                alt={activeProduct?.name || title}
                width={896}
                height={1200}
                className={tplStyles.skuDirPreviewImg}
              />
            </div>
            {activeProduct && (
              <div className={tplStyles.skuDirPreviewMeta}>
                <span className={tplStyles.skuDirPreviewName}>{activeProduct.name}</span>
                <span className={`${tplStyles.skuDirPreviewPrice} num`}>
                  {formatVnd(activeProduct.price)}
                </span>
              </div>
            )}
          </aside>
        </div>
      )}
    </div>
  );
}
