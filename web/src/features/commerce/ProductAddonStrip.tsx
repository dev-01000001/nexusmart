"use client";

import React from "react";
import type { Product } from "@/data/products";
import { ProductCard } from "@/components/ProductCard";
import styles from "./commerce.module.css";

export interface ProductAddonStripProps {
  title: string;
  subtitle?: string;
  products: Product[];
}

export function ProductAddonStrip({ title, subtitle, products }: ProductAddonStripProps) {
  if (products.length === 0) return null;

  const visible = products.slice(0, 4);

  return (
    <section className={`${styles.section} ${styles.sectionGap}`}>
      <div className={styles.sectionHead}>
        <h2 className={styles.sectionTitle}>{title}</h2>
        {subtitle && <p className={styles.sectionNote}>{subtitle}</p>}
      </div>

      <div className={styles.addonGrid} data-count={visible.length} data-reveal="rows">
        {visible.map((product) => (
          <div key={product.id} className={styles.addonCard}>
            <ProductCard product={product} />
          </div>
        ))}
      </div>
    </section>
  );
}
