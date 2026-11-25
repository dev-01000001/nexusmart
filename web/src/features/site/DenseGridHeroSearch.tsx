"use client";

import React from "react";
import Link from "next/link";
import type { CategoryInfo } from "@/data/products";
import { IconSearch } from "@/components/Icons";
import { SiteSearchBar } from "@/features/search";
import tplStyles from "./siteTemplates.module.css";

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
 * No hero banner image — straight into a prominent search bar, sticky category pills,
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

  return (
    <div className={tplStyles.denseHero}>
      <div className={tplStyles.denseHeroTop}>
        <div className={tplStyles.denseHeroCopy}>
          <HeadingTag className={tplStyles.denseHeroTitle}>{title}</HeadingTag>
          {subtitle?.trim() && <p className={tplStyles.denseHeroSub}>{subtitle}</p>}
        </div>
        <span className={`${tplStyles.denseHeroCount} num`}>{productCount} mẫu sẵn kho</span>
      </div>

      <div className={tplStyles.denseHeroSearchWrap}>
        {previewMode ? (
          <div className={tplStyles.denseHeroSearchMock} aria-hidden="true">
            <IconSearch size={18} />
            <span>Tìm nhanh tên mẫu, chất liệu, mã SKU tại {brandName}...</span>
            <span className={tplStyles.denseHeroSearchMockBtn}>Tìm kiếm</span>
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
        <div className={tplStyles.denseCategoryRail} role="navigation" aria-label="Danh mục nhanh">
          {previewMode ? (
            <span className={`${tplStyles.denseCatPill} ${tplStyles.denseCatPillActive}`}>
              Tất cả ({productCount})
            </span>
          ) : (
            <Link
              href="/san-pham"
              className={`${tplStyles.denseCatPill} ${tplStyles.denseCatPillActive}`}
            >
              Tất cả ({productCount})
            </Link>
          )}
          {categories.slice(0, 7).map((cat) =>
            previewMode ? (
              <span key={cat.slug} className={tplStyles.denseCatPill}>
                {cat.name}
              </span>
            ) : (
              <Link
                key={cat.slug}
                href={`/san-pham?cat=${encodeURIComponent(cat.slug)}`}
                className={tplStyles.denseCatPill}
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
