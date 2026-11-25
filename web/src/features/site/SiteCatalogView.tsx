"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import type { CategoryInfo, Product } from "@/data/products";
import type { Voucher } from "@/data/vouchers";
import { IconClose, IconFilter } from "@/components/Icons";
import { useModalFocusTrap } from "@/components/modalFocus";
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

const SORT_LABELS: Record<SortKey, string> = {
  newest: "Mới nhất",
  price_asc: "Giá tăng dần",
  price_desc: "Giá giảm dần",
};

function foldVietnamese(input: string): string {
  return input
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/đ/g, "d")
    .replace(/Đ/g, "D")
    .toLowerCase()
    .trim();
}

function resolveCatalogGridClass(
  layout: CatalogPageConfig["layout"],
  columns: number,
  count: number
): string {
  if (layout === "single-strip" || columns === 1) return tplStyles.singleStripList;
  if (count <= 1) return tplStyles.gridSingle;
  if (count === 2) return tplStyles.gridCols2;
  if (layout === "editorial-magazine" || layout === "luxury-curated" || columns === 2) {
    return tplStyles.gridCols2;
  }
  if (count === 4 && (columns === 3 || layout === "sidebar-left" || layout === "minimal-gallery")) {
    return tplStyles.gridCols2;
  }
  if (layout === "minimal-gallery") return tplStyles.gridStagger3;
  if (columns === 5) {
    if (count === 3) return tplStyles.gridCols3;
    if (count === 4) return tplStyles.gridCols4;
    return tplStyles.gridCols5;
  }
  if (columns === 3 || count === 3) return tplStyles.gridCols3;
  return tplStyles.gridCols4;
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
  const showFilters =
    pageConfig?.show_filters ?? templateConfig?.pages.catalog.show_filters ?? true;
  const showSearch = pageConfig?.show_search ?? templateConfig?.pages.catalog.show_search ?? true;
  const showSkuMatrix =
    pageConfig?.show_sku_matrix ??
    templateConfig?.pages.catalog.show_sku_matrix ??
    layout === "sku-table";
  const introTitle =
    pageConfig?.intro_title?.trim() ||
    templateConfig?.pages.catalog.intro_title?.trim() ||
    `Sản phẩm ${brandName}`;
  const introSubtitle =
    pageConfig?.intro_subtitle?.trim() ||
    templateConfig?.pages.catalog.intro_subtitle?.trim() ||
    "";
  const extraSections = pageConfig?.sections ?? templateConfig?.pages.catalog.sections ?? [];

  const cardStyle: SiteCardStyle =
    layout === "single-strip"
      ? "compact-row"
      : layout === "luxury-curated"
        ? "luxury"
        : layout === "editorial-magazine"
          ? "editorial"
          : (templateConfig?.layout.card_style ?? "minimal");

  const pageSize =
    columns === 5 ? 10 : layout === "editorial-magazine" || layout === "luxury-curated" ? 6 : 8;

  const [selectedCategory, setSelectedCategory] = useState<string>(initialCategory);
  const [query, setQuery] = useState<string>(initialQuery);
  const [sort, setSort] = useState<SortKey>(
    initialSort === "price_asc" || initialSort === "price_desc" ? initialSort : "newest"
  );
  const [page, setPage] = useState<number>(Math.max(1, initialPage));
  const [viewMode, setViewMode] = useState<"grid" | "sku-table">(
    layout === "sku-table" || showSkuMatrix ? "sku-table" : "grid"
  );
  const [sheetOpen, setSheetOpen] = useState(false);
  const [sheetClosing, setSheetClosing] = useState(false);
  const filterSheetRef = useRef<HTMLElement | null>(null);
  const sheetCloseTimerRef = useRef<number | null>(null);

  const openSheet = useCallback(() => {
    if (sheetCloseTimerRef.current !== null) {
      window.clearTimeout(sheetCloseTimerRef.current);
      sheetCloseTimerRef.current = null;
    }
    setSheetClosing(false);
    setSheetOpen(true);
  }, []);

  const closeSheet = useCallback(() => {
    if (!sheetOpen || sheetClosing) return;
    setSheetClosing(true);
    sheetCloseTimerRef.current = window.setTimeout(() => {
      sheetCloseTimerRef.current = null;
      setSheetOpen(false);
      setSheetClosing(false);
    }, 180);
  }, [sheetOpen, sheetClosing]);

  useEffect(() => {
    return () => {
      if (sheetCloseTimerRef.current !== null) {
        window.clearTimeout(sheetCloseTimerRef.current);
      }
    };
  }, []);

  useModalFocusTrap({
    open: !previewMode && sheetOpen && !sheetClosing,
    containerRef: filterSheetRef,
    onEscape: closeSheet,
  });

  const categoryLabelMap = useMemo(
    () => new Map<string, string>(categories.map((c) => [c.slug, c.name])),
    [categories]
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
      sorted.sort((a, b) => a.price - b.price);
    } else if (sort === "price_desc") {
      sorted.sort((a, b) => b.price - a.price);
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

  const activeFilterCount =
    (selectedCategory ? 1 : 0) + (query.trim() ? 1 : 0) + (sort !== "newest" ? 1 : 0);
  const sheetFilterCount =
    (showFilters && selectedCategory ? 1 : 0) +
    (showSearch && query.trim() ? 1 : 0) +
    (sort !== "newest" ? 1 : 0);

  const useSidebar = layout === "sidebar-left" || layout === "sku-table";
  const gridClass = resolveCatalogGridClass(layout, columns, pagedProducts.length);

  const resetFilters = () => {
    setSelectedCategory("");
    setQuery("");
    setSort("newest");
    setPage(1);
  };

  const resetSheetFilters = () => {
    if (showFilters) setSelectedCategory("");
    if (showSearch) setQuery("");
    setSort("newest");
    setPage(1);
  };

  const renderViewToggle = () => {
    if (!showSkuMatrix) return null;
    return (
      <div className={tplStyles.viewToggleGroup} role="group" aria-label="Chế độ xem">
        <button
          type="button"
          className={`${tplStyles.viewToggleBtn} ${
            viewMode === "sku-table" ? tplStyles.viewToggleBtnActive : ""
          }`}
          aria-pressed={viewMode === "sku-table"}
          onClick={() => setViewMode("sku-table")}
        >
          Bảng SKU
        </button>
        <button
          type="button"
          className={`${tplStyles.viewToggleBtn} ${
            viewMode === "grid" ? tplStyles.viewToggleBtnActive : ""
          }`}
          aria-pressed={viewMode === "grid"}
          onClick={() => setViewMode("grid")}
        >
          Lưới thẻ
        </button>
      </div>
    );
  };

  const renderFilterFields = (idPrefix: string) => (
    <>
      {showFilters && (
        <div className={tplStyles.catalogSidebarGroup}>
          <span className={tplStyles.catalogSidebarTitle}>Danh mục</span>
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
          <label htmlFor={`${idPrefix}-search`} className={tplStyles.catalogSidebarTitle}>
            Lọc từ khóa
          </label>
          <input
            id={`${idPrefix}-search`}
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
        <label htmlFor={`${idPrefix}-sort`} className={tplStyles.catalogSidebarTitle}>
          Sắp xếp
        </label>
        <select
          id={`${idPrefix}-sort`}
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
    </>
  );

  const renderActiveChips = () => {
    if (activeFilterCount === 0) return null;
    const categoryLabel = categoryLabelMap.get(selectedCategory) ?? selectedCategory;
    return (
      <div className={tplStyles.activeChipBar}>
        {selectedCategory && (
          <button
            type="button"
            className={tplStyles.activeChip}
            aria-label={`Bỏ lọc danh mục ${categoryLabel}`}
            onClick={() => {
              setSelectedCategory("");
              setPage(1);
            }}
          >
            <span>{categoryLabel}</span>
            <IconClose size={13} />
          </button>
        )}
        {query.trim() && (
          <button
            type="button"
            className={tplStyles.activeChip}
            aria-label={`Bỏ lọc từ khóa ${query.trim()}`}
            onClick={() => {
              setQuery("");
              setPage(1);
            }}
          >
            <span>“{query.trim()}”</span>
            <IconClose size={13} />
          </button>
        )}
        {sort !== "newest" && (
          <button
            type="button"
            className={tplStyles.activeChip}
            aria-label={`Bỏ lọc sắp xếp ${SORT_LABELS[sort]}`}
            onClick={() => {
              setSort("newest");
              setPage(1);
            }}
          >
            <span>{SORT_LABELS[sort]}</span>
            <IconClose size={13} />
          </button>
        )}
        <button type="button" className={tplStyles.clearChipBtn} onClick={resetFilters}>
          Xóa lọc
        </button>
      </div>
    );
  };

  const renderSkuMatrixTable = () => (
    <div className={tplStyles.tableScrollWrap}>
      <table className={tplStyles.skuTable}>
        <thead>
          <tr>
            <th>Sản phẩm</th>
            <th>SKU</th>
            <th>Màu</th>
            <th>Size</th>
            <th>Đơn giá</th>
            <th>Tồn</th>
            <th>Đặt mua</th>
          </tr>
        </thead>
        <tbody>
          {skuMatrixRows.map((row) => (
            <tr key={row.key}>
              <td>
                <strong>{row.product.name}</strong>
              </td>
              <td className={`${tplStyles.skuCodeMono} num`}>{row.sku.skuCode}</td>
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
                  {row.sku.stock > 0 ? `Còn ${formatInt(row.sku.stock)}` : "Hết"}
                </span>
              </td>
              <td>
                {previewMode ? (
                  <span className={styles.sectionLink}>Chọn</span>
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
        <div className={styles.emptyPanel}>
          <h2 className={styles.sectionTitle}>Không tìm thấy sản phẩm</h2>
          <p className={styles.prosePara}>Thử bỏ bộ lọc để xem toàn bộ mẫu của {brandName}.</p>
          <button type="button" className={styles.btnPrimary} onClick={resetFilters}>
            Xem tất cả
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
              layoutMode={pagedProducts.length === 1 ? "single-wide" : "default"}
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
          {introSubtitle && (
            <p className={styles.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
              {introSubtitle}
            </p>
          )}
        </div>
      </section>

      <section className={styles.siteSection} style={{ paddingTop: 0 }}>
        <div className={tplStyles.container}>
          {useSidebar ? (
            <div className={tplStyles.catalogSidebarShell}>
              <aside className={tplStyles.catalogSidebar} aria-label="Bộ lọc danh mục">
                {renderFilterFields("sidebar")}
              </aside>

              <div style={{ minWidth: 0 }}>
                <div className={tplStyles.catalogToolbarRow}>
                  <div className={tplStyles.catalogToolbarLeft}>
                    {(showFilters || showSearch) && (
                      <button
                        type="button"
                        className={`${tplStyles.filterSheetBtn} ${tplStyles.filterSheetBtnMobileOnly}`}
                        aria-expanded={sheetOpen}
                        onClick={openSheet}
                      >
                        <IconFilter size={15} />
                        <span>Bộ lọc</span>
                        {activeFilterCount > 0 && (
                          <span className={`${tplStyles.filterCountBadge} num`}>
                            {activeFilterCount}
                          </span>
                        )}
                      </button>
                    )}
                    <span className={`${tplStyles.resultCountText} num`}>
                      {viewMode === "sku-table" && showSkuMatrix
                        ? `${formatInt(skuMatrixRows.length)} SKU (${formatInt(filteredProducts.length)} mẫu)`
                        : `${formatInt(filteredProducts.length)} sản phẩm`}
                    </span>
                  </div>

                  {renderViewToggle()}
                </div>

                {renderActiveChips()}
                {renderResultsArea()}
              </div>
            </div>
          ) : (
            <>
              <div className={tplStyles.catalogToolbarSticky}>
                <div className={tplStyles.catalogToolbarLeft}>
                  {(showFilters || showSearch) && (
                    <button
                      type="button"
                      className={tplStyles.filterSheetBtn}
                      aria-expanded={sheetOpen}
                      onClick={openSheet}
                    >
                      <IconFilter size={15} />
                      <span>Bộ lọc</span>
                      {activeFilterCount > 0 && (
                        <span className={`${tplStyles.filterCountBadge} num`}>
                          {activeFilterCount}
                        </span>
                      )}
                    </button>
                  )}
                  <span className={`${tplStyles.resultCountText} num`}>
                    {formatInt(filteredProducts.length)} sản phẩm
                  </span>
                </div>

                <div className={tplStyles.catalogToolbarRight}>
                  <label htmlFor="site-catalog-sort-bar" className="sr-only">
                    Sắp xếp
                  </label>
                  <select
                    id="site-catalog-sort-bar"
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

                  {renderViewToggle()}
                </div>
              </div>

              {renderActiveChips()}
              {renderResultsArea()}
            </>
          )}
        </div>
      </section>

      {/* Filter Sheet Drawer */}
      {sheetOpen && (
        <div
          className={`${styles.siteDrawerBackdrop} ${
            sheetClosing ? styles.siteDrawerBackdropClosing : ""
          }`}
          role="presentation"
          onClick={(e) => {
            if (e.target === e.currentTarget) closeSheet();
          }}
        >
          <aside
            ref={filterSheetRef}
            tabIndex={-1}
            className={`${tplStyles.filterSheet} ${
              sheetClosing ? styles.siteDrawerPanelClosing : ""
            }`}
            role="dialog"
            aria-modal="true"
            aria-label="Bộ lọc sản phẩm"
          >
            <header className={tplStyles.filterSheetHead}>
              <span className={tplStyles.filterSheetTitle}>Bộ lọc</span>
              <button
                type="button"
                className={styles.siteDrawerClose}
                onClick={closeSheet}
                aria-label="Đóng bộ lọc"
              >
                <IconClose size={18} />
              </button>
            </header>

            <div className={tplStyles.filterSheetBody}>{renderFilterFields("sheet")}</div>

            <footer className={tplStyles.filterSheetFoot}>
              {sheetFilterCount > 0 && (
                <button
                  type="button"
                  className={styles.btnSecondary}
                  onClick={() => {
                    resetSheetFilters();
                    closeSheet();
                  }}
                >
                  Xóa lọc
                </button>
              )}
              <button type="button" className={styles.btnPrimary} onClick={closeSheet}>
                Xem {formatInt(filteredProducts.length)} mẫu
              </button>
            </footer>
          </aside>
        </div>
      )}

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
