"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import type { CategoryInfo, Product } from "@/data/products";
import type { Voucher } from "@/data/vouchers";
import { formatInt, formatVnd } from "@/lib/format";
import type { SitePublicShopDTO } from "@/lib/site/types";
import { SiteProductCard } from "./SiteProductCard";
import { SiteRenderer } from "./SiteRenderer";
import type { CatalogPageConfig, SiteCardStyle, SiteTemplateConfig } from "./templateTypes";
import styles from "./site.module.css";
import tplStyles from "./siteTemplates.module.css";

export interface SiteCatalogViewProps {
  brandName: string;
  products: Product[];
  categories: CategoryInfo[];
  initialCategory?: string;
  initialQuery?: string;
  initialSort?: string;
  initialPage?: number;
  pageConfig?: CatalogPageConfig;
  templateConfig?: SiteTemplateConfig;
  shop?: SitePublicShopDTO;
  vouchers?: Voucher[];
  previewMode?: boolean;
}

type SortKey = "newest" | "price_asc" | "price_desc";

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

function resolveCatalogGridClass(layout: CatalogPageConfig["layout"], columns: number, count: number): string {
  if (layout === "single-strip") return tplStyles.singleStripList;
  if (layout === "editorial-magazine" || layout === "luxury-curated" || columns === 2) {
    return tplStyles.gridCols2;
  }
  if (columns === 5) return tplStyles.gridCols5;
  if (columns === 3 || layout === "minimal-gallery") return tplStyles.gridCols3;
  if (columns === 1) return tplStyles.singleStripList;
  return count === 3 ? tplStyles.gridCols3 : tplStyles.gridCols4;
}

export function SiteCatalogView({
  brandName,
  products,
  categories,
  initialCategory = "",
  initialQuery = "",
  initialSort = "newest",
  initialPage = 1,
  pageConfig,
  templateConfig,
  shop,
  vouchers = [],
  previewMode = false,
}: SiteCatalogViewProps) {
  const layout = pageConfig?.layout ?? templateConfig?.pages.catalog.layout ?? "top-bar";
  const columns = pageConfig?.columns ?? templateConfig?.pages.catalog.columns ?? 4;
  const showFilters = pageConfig?.show_filters ?? templateConfig?.pages.catalog.show_filters ?? true;
  const showSearch = pageConfig?.show_search ?? templateConfig?.pages.catalog.show_search ?? true;
  const showSkuMatrix =
    pageConfig?.show_sku_matrix ??
    templateConfig?.pages.catalog.show_sku_matrix ??
    layout === "sku-table";
  const introTitle =
    pageConfig?.intro_title?.trim() ||
    templateConfig?.pages.catalog.intro_title?.trim() ||
    `Sản phẩm của ${brandName}`;
  const introSubtitle =
    pageConfig?.intro_subtitle?.trim() || templateConfig?.pages.catalog.intro_subtitle?.trim() || "";
  const extraSections = pageConfig?.sections ?? templateConfig?.pages.catalog.sections ?? [];

  const cardStyle: SiteCardStyle =
    layout === "single-strip"
      ? "compact-row"
      : layout === "luxury-curated"
        ? "luxury"
        : layout === "editorial-magazine"
          ? "editorial"
          : (templateConfig?.layout.card_style ?? "minimal");

  const pageSize = columns === 5 ? 10 : layout === "editorial-magazine" || layout === "luxury-curated" ? 6 : 8;

  const [selectedCategory, setSelectedCategory] = useState<string>(initialCategory);
  const [query, setQuery] = useState<string>(initialQuery);
  const [sort, setSort] = useState<SortKey>(
    initialSort === "price_asc" || initialSort === "price_desc" ? initialSort : "newest"
  );
  const [page, setPage] = useState<number>(Math.max(1, initialPage));
  const [viewMode, setViewMode] = useState<"grid" | "sku-table">(
    layout === "sku-table" || showSkuMatrix ? "sku-table" : "grid"
  );

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

  const totalPages = Math.max(1, Math.ceil(filteredProducts.length / pageSize));
  const safePage = Math.min(page, totalPages);
  const pagedProducts = useMemo(() => {
    const start = (safePage - 1) * pageSize;
    return filteredProducts.slice(start, start + pageSize);
  }, [filteredProducts, safePage, pageSize]);

  const skuMatrixRows = useMemo(() => {
    return filteredProducts.flatMap((product) => {
      const colorMap = new Map(product.colors.map((c) => [c.code, c]));
      return product.skus.map((sku) => {
        const color = colorMap.get(sku.colorCode);
        return {
          key: `${product.id}-${sku.skuCode}-${sku.colorCode}-${sku.size}`,
          product,
          sku,
          colorName: color?.name ?? sku.colorCode,
          colorHex: color?.hex ?? "#1F3038",
        };
      });
    });
  }, [filteredProducts]);

  const useSidebar = layout === "sidebar-left" || layout === "sku-table";
  const gridClass = resolveCatalogGridClass(layout, columns, pagedProducts.length);

  const resetFilters = () => {
    setSelectedCategory("");
    setQuery("");
    setSort("newest");
    setPage(1);
  };

  const renderSkuMatrixTable = () => (
    <div className={tplStyles.tableScrollWrap} data-reveal="">
      <table className={tplStyles.skuTable}>
        <thead>
          <tr>
            <th>Sản phẩm</th>
            <th>Mã SKU</th>
            <th>Màu sắc</th>
            <th>Kích cỡ</th>
            <th>Đơn giá</th>
            <th>Tồn kho</th>
            <th>Thao tác</th>
          </tr>
        </thead>
        <tbody>
          {skuMatrixRows.map((row) => (
            <tr key={row.key}>
              <td>
                <strong>{row.product.name}</strong>
              </td>
              <td className={tplStyles.skuCodeMono}>{row.sku.skuCode}</td>
              <td>
                <span className={tplStyles.colorSwatchInline}>
                  <span
                    className={tplStyles.colorDot}
                    style={{ backgroundColor: row.colorHex }}
                    aria-hidden="true"
                  />
                  <span>{row.colorName}</span>
                </span>
              </td>
              <td>{row.sku.size}</td>
              <td className="num">{formatVnd(row.sku.price)}</td>
              <td>
                <span
                  className={`${tplStyles.stockBadge} ${row.sku.stock <= 0 ? tplStyles.stockBadgeOut : ""} num`}
                >
                  {row.sku.stock > 0 ? `Còn ${formatInt(row.sku.stock)}` : "Tạm hết"}
                </span>
              </td>
              <td>
                {previewMode ? (
                  <span className={styles.sectionLink}>Chi tiết</span>
                ) : (
                  <Link
                    href={`/san-pham/${encodeURIComponent(row.product.slug)}`}
                    className={styles.sectionLink}
                  >
                    Đặt mua
                  </Link>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );

  const renderResultsArea = () => {
    if (filteredProducts.length === 0) {
      return (
        <div className={styles.emptyPanel} data-reveal="">
          <h2 className={styles.sectionTitle}>Không tìm thấy sản phẩm phù hợp</h2>
          <p className={styles.prosePara}>
            Thử xóa từ khóa tìm kiếm hoặc chọn lại tất cả danh mục của {brandName}.
          </p>
          <button type="button" className={styles.btnPrimary} onClick={resetFilters}>
            Xem toàn bộ sản phẩm
          </button>
        </div>
      );
    }

    if (showSkuMatrix && viewMode === "sku-table") {
      return renderSkuMatrixTable();
    }

    return (
      <>
        <div className={gridClass}>
          {pagedProducts.map((product, idx) => (
            <SiteProductCard
              key={product.id}
              product={product}
              previewMode={previewMode}
              index={idx}
              cardStyle={cardStyle}
              showExcerpt={layout === "editorial-magazine" || layout === "single-strip"}
            />
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
    );
  };

  return (
    <div data-catalog-layout={layout}>
      <section className={styles.pageHeadBand}>
        <div
          className={`${tplStyles.container} ${
            layout === "luxury-curated" || layout === "minimal-gallery"
              ? tplStyles.catalogLuxuryHead
              : ""
          }`}
        >
          <h1 className={styles.pageTitle} data-rise="" style={{ "--i": 0 } as React.CSSProperties}>
            {introTitle}
          </h1>
          <p className={styles.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
            {introSubtitle ||
              `Hiển thị ${filteredProducts.length} mẫu sẵn bán trực tiếp từ kho của ${brandName}.`}
          </p>
        </div>
      </section>

      <section className={styles.siteSection} style={{ paddingTop: 0 }}>
        <div className={tplStyles.container}>
          {useSidebar ? (
            <div className={tplStyles.catalogSidebarShell}>
              <aside className={tplStyles.catalogSidebar} aria-label="Bộ lọc danh mục">
                {showFilters && (
                  <div className={tplStyles.catalogSidebarGroup}>
                    <span className={tplStyles.catalogSidebarTitle}>Danh mục sản phẩm</span>
                    <button
                      type="button"
                      className={`${tplStyles.catalogSidebarBtn} ${
                        selectedCategory === "" ? tplStyles.catalogSidebarBtnActive : ""
                      }`}
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
                        className={`${tplStyles.catalogSidebarBtn} ${
                          selectedCategory === cat.slug ? tplStyles.catalogSidebarBtnActive : ""
                        }`}
                        onClick={() => {
                          setSelectedCategory(cat.slug);
                          setPage(1);
                        }}
                      >
                        {cat.name}
                      </button>
                    ))}
                  </div>
                )}

                {showSearch && (
                  <div className={tplStyles.catalogSidebarGroup}>
                    <label htmlFor="site-catalog-search" className={tplStyles.catalogSidebarTitle}>
                      Lọc nhanh từ khóa
                    </label>
                    <input
                      id="site-catalog-search"
                      type="search"
                      className={styles.searchInput}
                      style={{ minWidth: 0, width: "100%" }}
                      placeholder="Tên, chất liệu..."
                      value={query}
                      onChange={(e) => {
                        setQuery(e.target.value);
                        setPage(1);
                      }}
                    />
                  </div>
                )}

                <div className={tplStyles.catalogSidebarGroup}>
                  <label htmlFor="site-catalog-sort" className={tplStyles.catalogSidebarTitle}>
                    Sắp xếp
                  </label>
                  <select
                    id="site-catalog-sort"
                    className={styles.sortSelect}
                    style={{ width: "100%" }}
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
              </aside>

              <div style={{ minWidth: 0 }}>
                <div className={tplStyles.catalogToolbarRow}>
                  <span className={`${tplStyles.cardSpec} num`}>
                    {viewMode === "sku-table" && showSkuMatrix
                      ? `${formatInt(skuMatrixRows.length)} biến thể SKU từ ${formatInt(filteredProducts.length)} sản phẩm`
                      : `${formatInt(filteredProducts.length)} sản phẩm`}
                  </span>

                  {showSkuMatrix && (
                    <div className={tplStyles.viewToggleGroup} role="group" aria-label="Chế độ xem">
                      <button
                        type="button"
                        className={`${tplStyles.viewToggleBtn} ${
                          viewMode === "sku-table" ? tplStyles.viewToggleBtnActive : ""
                        }`}
                        onClick={() => setViewMode("sku-table")}
                      >
                        Bảng ma trận SKU
                      </button>
                      <button
                        type="button"
                        className={`${tplStyles.viewToggleBtn} ${
                          viewMode === "grid" ? tplStyles.viewToggleBtnActive : ""
                        }`}
                        onClick={() => setViewMode("grid")}
                      >
                        Lưới thẻ
                      </button>
                    </div>
                  )}
                </div>

                {renderResultsArea()}
              </div>
            </div>
          ) : (
            <>
              {(showFilters || showSearch || showSkuMatrix) && (
                <div
                  className={styles.filterBar}
                  data-ui=""
                  data-rise=""
                  style={{ "--i": 2 } as React.CSSProperties}
                >
                  {showFilters && (
                    <div className={styles.filterGroup} role="group" aria-label="Lọc theo danh mục">
                      <button
                        type="button"
                        className={`${styles.filterChip} ${
                          selectedCategory === "" ? styles.filterChipActive : ""
                        }`}
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
                          className={`${styles.filterChip} ${
                            selectedCategory === cat.slug ? styles.filterChipActive : ""
                          }`}
                          onClick={() => {
                            setSelectedCategory(cat.slug);
                            setPage(1);
                          }}
                        >
                          {cat.name}
                        </button>
                      ))}
                    </div>
                  )}

                  <div className={styles.filterGroup}>
                    {showSearch && (
                      <>
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
                      </>
                    )}

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

                    {showSkuMatrix && (
                      <div className={tplStyles.viewToggleGroup} role="group" aria-label="Chế độ xem">
                        <button
                          type="button"
                          className={`${tplStyles.viewToggleBtn} ${
                            viewMode === "sku-table" ? tplStyles.viewToggleBtnActive : ""
                          }`}
                          onClick={() => setViewMode("sku-table")}
                        >
                          Bảng SKU
                        </button>
                        <button
                          type="button"
                          className={`${tplStyles.viewToggleBtn} ${
                            viewMode === "grid" ? tplStyles.viewToggleBtnActive : ""
                          }`}
                          onClick={() => setViewMode("grid")}
                        >
                          Lưới
                        </button>
                      </div>
                    )}
                  </div>
                </div>
              )}

              {renderResultsArea()}
            </>
          )}
        </div>
      </section>

      {templateConfig && extraSections.length > 0 && (
        <SiteRenderer
          config={templateConfig}
          shop={shop ?? { id: "", name: brandName }}
          products={products}
          vouchers={vouchers}
          categories={categories}
          previewMode={previewMode}
          sectionsOverride={extraSections}
        />
      )}
    </div>
  );
}
