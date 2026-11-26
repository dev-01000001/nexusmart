"use client";

import React from "react";
import Link from "next/link";
import { Search } from "lucide-react";
import type { CategoryInfo } from "@/data/products";
import { SiteSearchBar } from "@/features/search/SiteSearchBar";
import { cn } from "@/lib/utils";

export interface DenseGridHeroSearchProps {
  title: string;
  subtitle?: string;
  brandName: string;
  shopId?: string;
  categories?: CategoryInfo[];
  productCount: number;
  isFirstHeading: boolean;
  previewMode?: boolean;
}

/**
 * Signature opening for `dense-grid`:
 * No hero banner image — straight into a prominent search bar, quick category pills,
 * and immediate access to the 5-column product wall.
 */
export function DenseGridHeroSearch({
  title,
  subtitle,
  brandName,
  shopId,
  categories = [],
  productCount,
  isFirstHeading,
  previewMode = false,
}: DenseGridHeroSearchProps) {
  const HeadingTag = isFirstHeading ? "h1" : "h2";
  const pillBase =
    "inline-flex min-h-10 shrink-0 items-center whitespace-nowrap rounded-md px-3.5 py-1.5 text-[13px] font-medium transition-colors";
  const pillActive = "bg-primary text-primary-foreground";
  const pillIdle =
    "border border-border bg-background text-foreground hover:bg-primary hover:text-primary-foreground";

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-2">
        <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1.5">
          <HeadingTag className="text-[clamp(22px,2.2vw,30px)] leading-[1.16] font-semibold tracking-[-0.02em] text-foreground">
            {title}
          </HeadingTag>
          {subtitle?.trim() && <p className="text-sm text-muted-foreground">{subtitle}</p>}
        </div>
        <span className="text-[13px] font-semibold text-muted-foreground tabular-nums">
          {productCount} mẫu sẵn kho
        </span>
      </div>

      <div className="w-full max-w-[820px]">
        {previewMode ? (
          <div
            className="flex min-h-12 items-center gap-2.5 rounded-md border border-input bg-background pr-1.5 pl-3.5 text-sm text-muted-foreground"
            aria-hidden="true"
          >
            <Search className="size-[18px] shrink-0" />
            <span className="min-w-0 truncate">
              Tìm nhanh tên mẫu, chất liệu, mã SKU tại {brandName}...
            </span>
            <span className="ml-auto shrink-0 rounded-sm bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground">
              Tìm kiếm
            </span>
          </div>
        ) : (
          <SiteSearchBar
            brandName={brandName}
            shopId={shopId}
            placeholder={`Tìm nhanh trong ${productCount} mẫu của ${brandName}...`}
          />
        )}
      </div>

      {categories.length > 0 && (
        <div
          className="flex items-center gap-2 overflow-x-auto pb-0.5 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
          role="navigation"
          aria-label="Danh mục nhanh"
        >
          {previewMode ? (
            <span className={cn(pillBase, pillActive)}>Tất cả ({productCount})</span>
          ) : (
            <Link href="/san-pham" className={cn(pillBase, pillActive)}>
              Tất cả ({productCount})
            </Link>
          )}
          {categories.slice(0, 7).map((cat) =>
            previewMode ? (
              <span key={cat.slug} className={cn(pillBase, pillIdle)}>
                {cat.name}
              </span>
            ) : (
              <Link
                key={cat.slug}
                href={`/san-pham?cat=${encodeURIComponent(cat.slug)}`}
                className={cn(pillBase, pillIdle)}
              >
                {cat.name}
              </Link>
            )
          )}
        </div>
      )}
    </div>
  );
}
