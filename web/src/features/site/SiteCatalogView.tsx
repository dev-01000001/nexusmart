"use client";

import React, { useMemo, useState } from "react";
import type { CategoryInfo, Product } from "@/data/products";
import { SiteProductCard } from "./SiteProductCard";
import styles from "./site.module.css";

export interface SiteCatalogViewProps {
  brandName: string;
  products: Product[];
  categories: CategoryInfo[];
  initialCategory?: string;
  initialQuery?: string;
  initialSort?: string;
  initialPage?: number;
}

type SortKey = "newest" | "price_asc" | "price_desc";
const PAGE_SIZE = 8;

function foldVietnamese(input: string): string {
  return input
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/đ/g, "d")
    .replace(/Đ/g, "D")
    .toLowerCase()
    .trim();
}

function effectivePrice(p: Product): number {
  return p.price;
}

export function SiteCatalogView({
  brandName,
  products,
  categories,
  initialCategory = "",
  initialQuery = "",
  initialSort = "newest",
  initialPage = 1,
}: SiteCatalogViewProps) {
  const [selectedCategory, setSelectedCategory] = useState<string>(initialCategory);
  const [query, setQuery] = useState<string>(initialQuery);
  const [sort, setSort] = useState<SortKey>(
    initialSort === "price_asc" || initialSort === "price_desc" ? initialSort : "newest"
  );
  const [page, setPage] = useState<number>(Math.max(1, initialPage));

  const filteredProducts = useMemo(() => {
    const foldedQuery = foldVietnamese(query);
    const list = products.filter((product) => {
      if (selectedCategory && product.categorySlug !== selectedCategory) {
        return false;
      }
      if (!foldedQuery) return true;
      const haystack = foldVietnamese(
        `${product.name} ${product.shortName} ${product.fabric} ${product.categoryName}`
      );
      return haystack.includes(foldedQuery);
    });

    const sorted = [...list];
    if (sort === "price_asc") {
      sorted.sort((a, b) => effectivePrice(a) - effectivePrice(b));
    } else if (sort === "price_desc") {
      sorted.sort((a, b) => effectivePrice(b) - effectivePrice(a));
    }
    return sorted;
  }, [products, selectedCategory, query, sort]);

  const totalPages = Math.max(1, Math.ceil(filteredProducts.length / PAGE_SIZE));
  const safePage = Math.min(page, totalPages);
  const pagedProducts = useMemo(() => {
    const start = (safePage - 1) * PAGE_SIZE;
    return filteredProducts.slice(start, start + PAGE_SIZE);
  }, [filteredProducts, safePage]);

  const gridClass =
    pagedProducts.length === 3
      ? `${styles.productGrid} ${styles.productGridThree}`
      : styles.productGrid;

  return (
    <>
      <section className={styles.pageHeadBand}>
        <h1 className={styles.pageTitle} data-rise="" style={{ "--i": 0 } as React.CSSProperties}>
          Sản phẩm của {brandName}
        </h1>
        <p className={styles.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          Hiển thị {filteredProducts.length} mẫu sẵn bán trực tiếp từ kho của {brandName}.
        </p>
      </section>

      <section className={styles.siteSection} style={{ paddingTop: 0 }}>
        <div
          className={styles.filterBar}
          data-ui=""
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <div className={styles.filterGroup} role="group" aria-label="Lọc theo danh mục">
            <button
              type="button"
              className={`${styles.filterChip} ${selectedCategory === "" ? styles.filterChipActive : ""}`}
              onClick={() => {
                setSelectedCategory("");
                setPage(1);
              }}
            >
              Tất cả ({products.length})
            </button>
            {categories.map((cat) => (
              <button
                key={cat.slug}
                type="button"
                className={`${styles.filterChip} ${selectedCategory === cat.slug ? styles.filterChipActive : ""}`}
                onClick={() => {
                  setSelectedCategory(cat.slug);
                  setPage(1);
                }}
              >
                {cat.name}
              </button>
            ))}
          </div>

          <div className={styles.filterGroup}>
            <label htmlFor="site-catalog-search" className="sr-only">
              Tìm sản phẩm trong cửa hàng
            </label>
            <input
              id="site-catalog-search"
              type="search"
              className={styles.searchInput}
              placeholder="Tìm tên, chất liệu..."
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setPage(1);
              }}
            />

            <label htmlFor="site-catalog-sort" className="sr-only">
              Sắp xếp sản phẩm
            </label>
            <select
              id="site-catalog-sort"
              className={styles.sortSelect}
              value={sort}
              onChange={(e) => {
                setSort(e.target.value as SortKey);
                setPage(1);
              }}
            >
              <option value="newest">Mới nhất</option>
              <option value="price_asc">Giá tăng dần</option>
              <option value="price_desc">Giá giảm dần</option>
            </select>
          </div>
        </div>

        {pagedProducts.length > 0 ? (
          <>
            <div className={gridClass}>
              {pagedProducts.map((product, idx) => (
                <SiteProductCard key={product.id} product={product} index={idx} />
              ))}
            </div>

            {totalPages > 1 && (
              <nav className={styles.paginationBar} aria-label="Phân trang sản phẩm" data-ui="">
                <span className="num">
                  Trang {safePage} / {totalPages}
                </span>
                <div className={styles.paginationBtns}>
                  <button
                    type="button"
                    className={styles.btnSecondary}
                    disabled={safePage <= 1}
                    onClick={() => setPage((p) => Math.max(1, p - 1))}
                  >
                    Trang trước
                  </button>
                  <button
                    type="button"
                    className={styles.btnSecondary}
                    disabled={safePage >= totalPages}
                    onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                  >
                    Trang sau
                  </button>
                </div>
              </nav>
            )}
          </>
        ) : (
          <div className={styles.emptyPanel} data-reveal="">
            <h2 className={styles.sectionTitle}>Không tìm thấy sản phẩm phù hợp</h2>
            <p className={styles.prosePara}>
              Thử xóa từ khóa tìm kiếm hoặc chọn lại tất cả danh mục của {brandName}.
            </p>
            <button
              type="button"
              className={styles.btnPrimary}
              onClick={() => {
                setSelectedCategory("");
                setQuery("");
                setSort("newest");
                setPage(1);
              }}
            >
              Xem toàn bộ sản phẩm
            </button>
          </div>
        )}
      </section>
    </>
  );
}
