"use client";

import React from "react";
import type { Product } from "@/data/products";
import { SiteProductCard } from "@/features/site/SiteProductCard";
import { cn } from "@/lib/utils";

export interface ProductAddonStripProps {
  title: string;
  subtitle?: string;
  products: Product[];
}

export function ProductAddonStrip({ title, subtitle, products }: ProductAddonStripProps) {
  if (products.length === 0) return null;

  const visible = products.slice(0, 4);
  const gridColsClass =
    visible.length === 1
      ? "grid-cols-1 max-w-md"
      : visible.length === 2
        ? "grid-cols-2 max-w-2xl"
        : visible.length === 3
          ? "grid-cols-2 lg:grid-cols-3"
          : "grid-cols-2 lg:grid-cols-4";

  return (
    <section
      className="mt-10 border-t border-border pt-8"
      aria-labelledby="product-addon-strip-title"
    >
      <div className="mb-5 flex flex-wrap items-baseline justify-between gap-2">
        <h2
          id="product-addon-strip-title"
          className="text-lg font-semibold tracking-tight text-foreground"
        >
          {title}
        </h2>
        {subtitle ? <p className="text-sm text-muted-foreground">{subtitle}</p> : null}
      </div>

      <div className={cn("grid gap-4 sm:gap-5", gridColsClass)}>
        {visible.map((product, idx) => (
          <SiteProductCard key={product.id} product={product} index={idx} />
        ))}
      </div>
    </section>
  );
}
