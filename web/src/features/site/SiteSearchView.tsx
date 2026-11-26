"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import type { CategoryInfo, Product, SizeCode } from "@/data/products";
import { searchProducts, suggestSpelling } from "@/lib/search";
import { SiteProductCard } from "./SiteProductCard";
import styles from "./site.module.css";

export interface SiteSearchViewProps {
  brandName: string;
  products: Product[];
  categories: CategoryInfo[];
  initialQuery?: string;
  initialCategory?: string;
  initialSort?: string;
}

type SortKey = "relevance" | "price_asc" | "price_desc";
const PAGE_SIZE = 12;
const SIZES: SizeCode[] = ["S", "M", "L", "XL", "2XL"];

export function SiteSearchView({
  brandName,
  products,
  categories,
  initialQuery = "",
  initialCategory = "",
  initialSort = "relevance",
}: SiteSearchViewProps) {
  const router = useRouter();
  const [queryInput, setQueryInput] = useState(initialQuery);
  const [appliedQuery, setAppliedQuery] = useState(initialQuery);
  const [category, setCategory] = useState(initialCategory);
  const [selectedSize, setSelectedSize] = useState<string>("");
  const [sort, setSort] = useState<SortKey>(
    initialSort === "price_asc" || initialSort === "price_desc" ? initialSort : "relevance"
  );
  const [page, setPage] = useState(1);

  const availableSizes = useMemo<string[]>(() => {
    const found: string[] = [];
    const seen = new Set<string>();
    for (const p of products) {
      for (const sz of p.sizes) {
        const clean = String(sz ?? "").trim();
        if (!clean || seen.has(clean)) continue;
        seen.add(clean);
        found.push(clean);
      }
    }
    return found.length > 0 ? found : SIZES;
  }, [products]);

  const matched = useMemo(() => {
    const byQuery = appliedQuery.trim() ? searchProducts(appliedQuery, products) : products;
    const filtered = byQuery.filter((p) => {
      if (category && p.categorySlug !== category) return false;
      if (selectedSize) {
        const hasStock = p.skus.some((s) => s.size === selectedSize && s.stock > 0);
        if (!hasStock) return false;
      }
      return true;
    });
    const out = [...filtered];
    if (sort === "price_asc") out.sort((a, b) => a.price - b.price);
    else if (sort === "price_desc") out.sort((a, b) => b.price - a.price);
    return out;
  }, [products, appliedQuery, category, selectedSize, sort]);

  const suggestion = useMemo(() => {
    if (!appliedQuery.trim() || matched.length > 0) return null;
    return suggestSpelling(appliedQuery);
  }, [appliedQuery, matched.length]);

  const totalPages = Math.max(1, Math.ceil(matched.length / PAGE_SIZE));
  const safePage = Math.min(page, totalPages);
  const paged = useMemo(() => {
    const start = (safePage - 1) * PAGE_SIZE;
    return matched.slice(start, start + PAGE_SIZE);
  }, [matched, safePage]);

  const submitQuery = (nextQ: string) => {
    const clean = nextQ.trim();
    setQueryInput(clean);
    setAppliedQuery(clean);
    setPage(1);
    const params = new URLSearchParams();
    if (clean) params.set("q", clean);
    if (category) params.set("cat", category);
    const qs = params.toString();
    router.replace(qs ? `/tim-kiem?${qs}` : "/tim-kiem", { scroll: false });
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    submitQuery(queryInput);
  };

  const gridClass =
    paged.length === 3 ? `${styles.productGrid} ${styles.productGridThree}` : styles.productGrid;

  return (
    <>
      <section className={styles.pageHeadBand}>
        <h1 className={styles.pageTitle} data-rise="" style={{ "--i": 0 } as React.CSSProperties}>
          {appliedQuery.trim()
            ? `Kết quả cho “${appliedQuery.trim()}”`
            : `Tìm kiếm tại ${brandName}`}
        </h1>
        <p className={styles.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          {appliedQuery.trim()
            ? `Tìm thấy ${matched.length} sản phẩm đang bán tại ${brandName}.`
            : `Tìm nhanh theo tên sản phẩm, chất liệu hoặc kiểu dáng trong ${products.length} mẫu của ${brandName}.`}
        </p>
      </section>

      <section className={styles.siteSection} style={{ paddingTop: 0 }}>
        <form
          className={styles.filterBar}
          onSubmit={handleSubmit}
          role="search"
          data-ui=""
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <div className={styles.filterGroup} style={{ flex: "1 1 300px" }}>
            <label htmlFor="site-search-input" className="sr-only">
              Từ khóa tìm kiếm
            </label>
            <input
              id="site-search-input"
              type="search"
              className={styles.searchInput}
              style={{ flex: "1 1 220px" }}
              placeholder={`Tìm sản phẩm của ${brandName}...`}
              value={queryInput}
              onChange={(e) => setQueryInput(e.target.value)}
            />
            <button type="submit" className={styles.btnPrimary} style={{ minHeight: 40 }}>
              Tìm kiếm
            </button>
          </div>

          <div className={styles.filterGroup}>
            <label htmlFor="site-search-sort" className="sr-only">
              Sắp xếp kết quả
            </label>
            <select
              id="site-search-sort"
              className={styles.sortSelect}
              value={sort}
              onChange={(e) => {
                setSort(e.target.value as SortKey);
                setPage(1);
              }}
            >
              <option value="relevance">Phù hợp nhất</option>
              <option value="price_asc">Giá tăng dần</option>
              <option value="price_desc">Giá giảm dần</option>
            </select>
          </div>
        </form>

        <div className={styles.filterBar} style={{ marginTop: -12 }} data-ui="">
          <div className={styles.filterGroup} role="group" aria-label="Lọc theo danh mục">
            <button
              type="button"
              className={`${styles.filterChip} ${category === "" ? styles.filterChipActive : ""}`}
              onClick={() => {
                setCategory("");
                setPage(1);
              }}
            >
              Tất cả danh mục
            </button>
            {categories.map((cat) => (
              <button
                key={cat.slug}
                type="button"
                className={`${styles.filterChip} ${category === cat.slug ? styles.filterChipActive : ""}`}
                onClick={() => {
                  setCategory(cat.slug);
                  setPage(1);
                }}
              >
                {cat.name}
              </button>
            ))}
          </div>

          <div className={styles.filterGroup} role="group" aria-label="Lọc theo kích cỡ sẵn hàng">
            <button
              type="button"
              className={`${styles.filterChip} ${selectedSize === "" ? styles.filterChipActive : ""}`}
              onClick={() => {
                setSelectedSize("");
                setPage(1);
              }}
            >
              Mọi size
            </button>
            {availableSizes.map((sz) => (
              <button
                key={sz}
                type="button"
                className={`${styles.filterChip} ${selectedSize === sz ? styles.filterChipActive : ""}`}
                onClick={() => {
                  setSelectedSize(selectedSize === sz ? "" : sz);
                  setPage(1);
                }}
              >
                {sz}
              </button>
            ))}
          </div>
        </div>

        {suggestion && (
          <p className={styles.searchSuggestion} role="status">
            <span>Có phải bạn muốn tìm</span>
            <button
              type="button"
              className={styles.searchSuggestBtn}
              onClick={() => submitQuery(suggestion)}
            >
              {suggestion}
            </button>
            <span>?</span>
          </p>
        )}

        {paged.length > 0 ? (
          <>
            <div className={gridClass}>
              {paged.map((product, idx) => (
                <SiteProductCard key={product.id} product={product} index={idx} />
              ))}
            </div>

            {totalPages > 1 && (
              <nav className={styles.paginationBar} aria-label="Phân trang kết quả" data-ui="">
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
            <h2 className={styles.sectionTitle}>Chưa tìm thấy sản phẩm khớp yêu cầu</h2>
            <p className={styles.prosePara}>
              Thử đổi từ khóa ngắn hơn hoặc bỏ bớt bộ lọc để xem toàn bộ sản phẩm của {brandName}.
            </p>
            <div className={styles.heroActions}>
              <button
                type="button"
                className={styles.btnPrimary}
                onClick={() => {
                  setCategory("");
                  setSelectedSize("");
                  submitQuery("");
                }}
              >
                Xóa bộ lọc tìm kiếm
              </button>
              <Link href="/san-pham" className={styles.btnSecondary}>
                Xem tất cả sản phẩm
              </Link>
            </div>
          </div>
        )}
      </section>
    </>
  );
}
