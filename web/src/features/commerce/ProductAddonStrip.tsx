"use client";

import React from "react";
import type { Product } from "@/data";
import { ProductCard } from "@/components/ProductCard";
import styles from "./commerce.module.css";

export interface ProductAddonStripProps {
  title: string;
  subtitle?: string;
  products: Product[];
  showTierHint?: boolean;
}

export function ProductAddonStrip({
  title,
  subtitle,
  products,
  showTierHint = false,
}: ProductAddonStripProps) {
  if (products.length === 0) return null;

  return (
    <section className={`${styles.section} ${styles.sectionGap}`} data-reveal="">
      <div className={styles.sectionHead}>
        <h2 className={styles.sectionTitle}>{title}</h2>
        {subtitle && <p className={styles.sectionNote}>{subtitle}</p>}
      </div>

      <div className={styles.addonGrid}>
        {products.map((product) => (
          <div key={product.id} className={styles.addonCard} data-reveal="">
            <ProductCard product={product} showSpec={showTierHint} />
          </div>
        ))}
      </div>
    </section>
  );
}
