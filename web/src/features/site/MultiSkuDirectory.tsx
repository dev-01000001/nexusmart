"use client";

import React, { useState } from "react";
import Link from "next/link";
import type { Product } from "@/data/products";
import { Money } from "@/components/app/Money";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { formatInt } from "@/lib/format";
import { cn } from "@/lib/utils";
import { SafeActionLink } from "./SafeActionLink";
import { sanitizeSafeImageUrl } from "./templateValidator";

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
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
        <div className="flex flex-col gap-1.5">
          <HeadingTag className="text-[clamp(26px,3vw,40px)] leading-[1.1] font-semibold tracking-[-0.02em] text-foreground">
            {title}
          </HeadingTag>
          {subtitle?.trim() && <p className="text-[15px] text-muted-foreground">{subtitle}</p>}
        </div>
        <div className="flex flex-wrap items-center gap-4">
          <Badge
            variant="secondary"
            className="rounded-sm px-2.5 py-1 text-xs font-semibold tabular-nums"
          >
            {formatInt(products.length)} mẫu · {formatInt(totalSkus)} SKU
          </Badge>
          <SafeActionLink
            href={ctaHref}
            fallbackHref="/san-pham"
            label={ctaLabel}
            className={cn(
              buttonVariants({ variant: "default", size: "lg" }),
              "min-h-10 shadow-none"
            )}
            previewMode={previewMode}
          />
        </div>
      </div>

      {listed.length > 0 && (
        <div className="grid grid-cols-1 items-start gap-5 lg:grid-cols-[minmax(0,7.5fr)_minmax(240px,4.5fr)] lg:gap-10">
          <ul
            className="divide-y divide-border overflow-hidden rounded-lg border border-border bg-card"
            data-reveal="rows"
          >
            {listed.map((prod, idx) => {
              const totalStock = prod.skus.reduce((s, k) => s + Math.max(0, k.stock), 0);
              const sizeRange =
                prod.sizes.length > 1
                  ? `${prod.sizes[0]} tới ${prod.sizes[prod.sizes.length - 1]}`
                  : prod.sizes[0] || "Free";
              const isActive = idx === activeIdx;
              const rowBtnCls = cn(
                "grid min-h-[58px] w-full grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 px-4 py-3.5 text-left transition-colors md:grid-cols-[minmax(0,1fr)_auto_auto] md:gap-x-5",
                isActive
                  ? "bg-accent text-accent-foreground"
                  : "text-foreground hover:bg-accent/70 hover:text-accent-foreground"
              );

              const rowInner = (
                <>
                  <span className="truncate text-[15px] font-semibold">{prod.name}</span>
                  <span className="col-span-full flex flex-wrap items-center gap-3 text-xs text-muted-foreground md:col-span-1 md:flex-nowrap">
                    <span>{prod.colors.length} màu</span>
                    <span>{sizeRange}</span>
                    <span className="tabular-nums">{formatInt(totalStock)} kho</span>
                  </span>
                  <Money vnd={prod.price} size="sm" className="justify-self-end" />
                </>
              );

              return (
                <li
                  key={prod.id}
                  onMouseEnter={() => setActiveIdx(idx)}
                  onFocus={() => setActiveIdx(idx)}
                >
                  {previewMode ? (
                    <button type="button" className={rowBtnCls} onClick={() => setActiveIdx(idx)}>
                      {rowInner}
                    </button>
                  ) : (
                    <Link href={`/san-pham/${encodeURIComponent(prod.slug)}`} className={rowBtnCls}>
                      {rowInner}
                    </Link>
                  )}
                </li>
              );
            })}
          </ul>

          <aside
            className="order-first flex flex-col gap-2.5 lg:sticky lg:top-20 lg:order-none"
            aria-live="polite"
          >
            <div className="aspect-[4/5] max-h-[460px] w-full overflow-hidden rounded-lg border border-border bg-card">
              <img
                key={previewImg}
                src={previewImg}
                alt={activeProduct?.name || title}
                width={896}
                height={1200}
                className="size-full object-cover object-[center_16%] transition-opacity duration-200"
              />
            </div>
            {activeProduct && (
              <div className="flex items-baseline justify-between gap-3">
                <span className="truncate text-[15px] font-semibold text-foreground">
                  {activeProduct.name}
                </span>
                <Money vnd={activeProduct.price} size="md" className="shrink-0" />
              </div>
            )}
          </aside>
        </div>
      )}
    </div>
  );
}
