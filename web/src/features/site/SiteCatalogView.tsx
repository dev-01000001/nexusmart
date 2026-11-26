"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import dynamic from "next/dynamic";
import Link from "next/link";
import { LayoutGrid, PackageOpen, SlidersHorizontal, TableProperties, X } from "lucide-react";
import type { CategoryInfo, Product } from "@/data/products";
import type { Voucher } from "@/data/vouchers";
import { EmptyState } from "@/components/app/EmptyState";
import { Money } from "@/components/app/Money";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Pagination,
  PaginationContent,
  PaginationEllipsis,
  PaginationItem,
  PaginationLink,
  PaginationNext,
  PaginationPrevious,
} from "@/components/ui/pagination";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { formatInt } from "@/lib/format";
import type { SitePublicShopDTO } from "@/lib/site/types";
import { cn } from "@/lib/utils";
import { CatalogFilterControls, type CatalogSortKey } from "./catalog/CatalogFilterControls";
import { SiteProductCard } from "./SiteProductCard";
import type { CatalogPageConfig, SiteCardStyle, SiteTemplateConfig } from "./templateTypes";

const SiteRenderer = dynamic(() => import("./SiteRenderer").then((mod) => mod.SiteRenderer));

const CatalogFilterSheet = dynamic(
  () => import("./catalog/CatalogFilterSheet").then((mod) => mod.CatalogFilterSheet),
  { ssr: false }
);

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

const SORT_LABELS: Record<CatalogSortKey, string> = {
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

function buildCatalogUrl(params: {
  cat?: string;
  q?: string;
  sort?: CatalogSortKey;
  page?: number;
}): string {
  const sp = new URLSearchParams();
  if (params.cat) sp.set("cat", params.cat);
  const cleanQ = params.q?.trim();
  if (cleanQ) sp.set("q", cleanQ);
  if (params.sort && params.sort !== "newest") sp.set("sort", params.sort);
  if (params.page && params.page > 1) sp.set("page", String(params.page));
  const qs = sp.toString();
  return qs ? `/san-pham?${qs}` : "/san-pham";
}

function buildPageNumbers(current: number, total: number): Array<number | "ellipsis"> {
  if (total <= 7) {
    return Array.from({ length: total }, (_, idx) => idx + 1);
  }
  const pages: Array<number | "ellipsis"> = [1];
  const start = Math.max(2, current - 1);
  const end = Math.min(total - 1, current + 1);
  if (start > 2) pages.push("ellipsis");
  for (let p = start; p <= end; p += 1) {
    pages.push(p);
  }
  if (end < total - 1) pages.push("ellipsis");
  pages.push(total);
  return pages;
}

function resolveCatalogGridClass(
  layout: CatalogPageConfig["layout"],
  columns: number,
  count: number
): string {
  if (layout === "single-strip" || columns === 1) return "flex flex-col gap-3.5";
  if (count <= 1) return "grid grid-cols-1";
  if (count === 2) return "grid grid-cols-1 gap-5 sm:grid-cols-2 lg:gap-8";
  if (layout === "editorial-magazine" || layout === "luxury-curated" || columns === 2) {
    return "grid grid-cols-1 gap-5 sm:grid-cols-2 lg:gap-9";
  }
  if (count === 4 && (columns === 3 || layout === "sidebar-left" || layout === "minimal-gallery")) {
    return "grid grid-cols-2 gap-4 lg:gap-7";
  }
  if (layout === "minimal-gallery") {
    return "grid grid-cols-2 items-start gap-4 lg:grid-cols-3 lg:gap-8 lg:[&>*:nth-child(3n+2)]:mt-10";
  }
  if (columns === 5) {
    if (count === 3) return "grid grid-cols-2 gap-3.5 lg:grid-cols-3 lg:gap-5";
    if (count === 4) return "grid grid-cols-2 gap-3.5 md:grid-cols-4 lg:gap-4";
    return "grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5 lg:gap-4";
  }
  if (columns === 3 || count === 3) {
    return "grid grid-cols-2 gap-4 lg:grid-cols-3 lg:gap-6";
  }
  return "grid grid-cols-2 gap-4 md:grid-cols-3 lg:grid-cols-4 lg:gap-6";
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
  const [sort, setSort] = useState<CatalogSortKey>(
    initialSort === "price_asc" || initialSort === "price_desc" ? initialSort : "newest"
  );
  const [page, setPage] = useState<number>(Math.max(1, initialPage));
  const [viewMode, setViewMode] = useState<"grid" | "sku-table">(
    layout === "sku-table" || showSkuMatrix ? "sku-table" : "grid"
  );
  const [sheetOpen, setSheetOpen] = useState(false);
  const [sheetMounted, setSheetMounted] = useState(false);
  const hasMountedRef = useRef(false);

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
  const pageNumbers = useMemo(() => buildPageNumbers(safePage, totalPages), [safePage, totalPages]);

  const pagedProducts = useMemo(() => {
    const start = (safePage - 1) * pageSize;
    return filteredProducts.slice(start, start + pageSize);
  }, [filteredProducts, safePage, pageSize]);

  // Keep query string synchronized cleanly without triggering full server reloads
  useEffect(() => {
    if (previewMode) return;
    if (!hasMountedRef.current) {
      hasMountedRef.current = true;
      return;
    }
    const nextUrl = buildCatalogUrl({
      cat: selectedCategory,
      q: query,
      sort,
      page: safePage,
    });
    if (typeof window !== "undefined") {
      window.history.replaceState(window.history.state, "", nextUrl);
    }
  }, [previewMode, selectedCategory, query, sort, safePage]);

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
  const isTopBar = layout === "top-bar";
  const isCenteredHead = layout === "luxury-curated" || layout === "minimal-gallery";
  const gridClass = resolveCatalogGridClass(layout, columns, pagedProducts.length);

  const handleOpenSheet = () => {
    setSheetMounted(true);
    setSheetOpen(true);
  };

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

  const sharedFilterProps = {
    showFilters,
    showSearch,
    categories,
    totalProductCount: products.length,
    selectedCategory,
    onSelectCategory: (slug: string) => {
      setSelectedCategory(slug);
      setPage(1);
    },
    query,
    onQueryChange: (val: string) => {
      setQuery(val);
      setPage(1);
    },
    sort,
    onSortChange: (next: CatalogSortKey) => {
      setSort(next);
      setPage(1);
    },
  };

  const renderViewToggle = () => {
    if (!showSkuMatrix) return null;
    return (
      <ToggleGroup
        type="single"
        variant="outline"
        value={viewMode}
        onValueChange={(val) => {
          if (val === "sku-table" || val === "grid") setViewMode(val);
        }}
        aria-label="Chế độ xem"
        className="bg-card"
      >
        <ToggleGroupItem
          value="sku-table"
          className="h-10 gap-1.5 px-3 text-sm data-[state=on]:bg-primary data-[state=on]:text-primary-foreground"
        >
          <TableProperties className="size-4" aria-hidden="true" />
          <span>Bảng SKU</span>
        </ToggleGroupItem>
        <ToggleGroupItem
          value="grid"
          className="h-10 gap-1.5 px-3 text-sm data-[state=on]:bg-primary data-[state=on]:text-primary-foreground"
        >
          <LayoutGrid className="size-4" aria-hidden="true" />
          <span>Lưới thẻ</span>
        </ToggleGroupItem>
      </ToggleGroup>
    );
  };

  const renderActiveChips = () => {
    if (activeFilterCount === 0) return null;
    const categoryLabel = categoryLabelMap.get(selectedCategory) ?? selectedCategory;
    return (
      <div className="mb-5 flex flex-wrap items-center gap-2">
        {selectedCategory && (
          <Button
            type="button"
            variant="secondary"
            size="sm"
            className="min-h-9 gap-1.5 rounded-full px-3 text-[13px] shadow-none"
            aria-label={`Bỏ lọc danh mục ${categoryLabel}`}
            onClick={() => {
              setSelectedCategory("");
              setPage(1);
            }}
          >
            <span>{categoryLabel}</span>
            <X className="size-3.5" aria-hidden="true" />
          </Button>
        )}
        {query.trim() && (
          <Button
            type="button"
            variant="secondary"
            size="sm"
            className="min-h-9 gap-1.5 rounded-full px-3 text-[13px] shadow-none"
            aria-label={`Bỏ lọc từ khóa ${query.trim()}`}
            onClick={() => {
              setQuery("");
              setPage(1);
            }}
          >
            <span>“{query.trim()}”</span>
            <X className="size-3.5" aria-hidden="true" />
          </Button>
        )}
        {sort !== "newest" && (
          <Button
            type="button"
            variant="secondary"
            size="sm"
            className="min-h-9 gap-1.5 rounded-full px-3 text-[13px] shadow-none"
            aria-label={`Bỏ lọc sắp xếp ${SORT_LABELS[sort]}`}
            onClick={() => {
              setSort("newest");
              setPage(1);
            }}
          >
            <span>{SORT_LABELS[sort]}</span>
            <X className="size-3.5" aria-hidden="true" />
          </Button>
        )}
        <Button
          type="button"
          variant="link"
          size="sm"
          className="min-h-9 px-2 text-[13px] font-semibold text-brand"
          onClick={resetFilters}
        >
          Xóa lọc
        </Button>
      </div>
    );
  };

  const renderSkuMatrixTable = () => (
    <div className="overflow-hidden rounded-lg border border-border bg-card">
      <Table>
        <TableHeader>
          <TableRow className="bg-muted/50 hover:bg-muted/50">
            <TableHead>Sản phẩm</TableHead>
            <TableHead>SKU</TableHead>
            <TableHead>Màu</TableHead>
            <TableHead>Size</TableHead>
            <TableHead>Đơn giá</TableHead>
            <TableHead>Tồn</TableHead>
            <TableHead>Đặt mua</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {skuMatrixRows.map((row) => (
            <TableRow key={row.key}>
              <TableCell className="font-semibold whitespace-normal text-foreground">
                {row.product.name}
              </TableCell>
              <TableCell className="font-mono text-xs text-muted-foreground">
                {row.sku.skuCode}
              </TableCell>
              <TableCell>
                <span className="inline-flex items-center gap-2">
                  <span
                    className="size-3 shrink-0 rounded-full border border-foreground/25"
                    style={{ backgroundColor: row.colorHex }}
                    aria-hidden="true"
                  />
                  <span>{row.colorName}</span>
                </span>
              </TableCell>
              <TableCell>{row.sku.size}</TableCell>
              <TableCell>
                <Money vnd={row.sku.price} size="sm" />
              </TableCell>
              <TableCell>
                <Badge
                  variant={row.sku.stock > 0 ? "success" : "neutral"}
                  className="rounded-sm tabular-nums"
                >
                  {row.sku.stock > 0 ? `Còn ${formatInt(row.sku.stock)}` : "Hết"}
                </Badge>
              </TableCell>
              <TableCell>
                {previewMode ? (
                  <span className="text-sm font-semibold text-brand">Chọn</span>
                ) : (
                  <Link
                    href={`/san-pham/${encodeURIComponent(row.product.slug)}`}
                    className="text-sm font-semibold text-brand underline-offset-4 hover:underline"
                  >
                    Đặt mua
                  </Link>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );

  const renderResultsArea = () => {
    if (filteredProducts.length === 0) {
      return (
        <EmptyState
          icon={PackageOpen}
          title="Không tìm thấy sản phẩm"
          description={`Thử bỏ bộ lọc để xem toàn bộ mẫu của ${brandName}.`}
          className="rounded-lg border border-border shadow-none"
          action={
            <Button type="button" className="min-h-10 shadow-none" onClick={resetFilters}>
              Xem tất cả
            </Button>
          }
        />
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
          <div className="mt-8 flex flex-col items-center justify-between gap-3 border-t border-border pt-5 sm:flex-row">
            <span className="text-sm text-muted-foreground tabular-nums">
              Trang {safePage} / {totalPages}
            </span>
            <Pagination className="mx-0 w-auto" aria-label="Phân trang sản phẩm">
              <PaginationContent>
                <PaginationItem>
                  <PaginationPrevious
                    href={buildCatalogUrl({
                      cat: selectedCategory,
                      q: query,
                      sort,
                      page: Math.max(1, safePage - 1),
                    })}
                    aria-disabled={safePage <= 1}
                    tabIndex={safePage <= 1 ? -1 : undefined}
                    className={cn("min-h-10", safePage <= 1 && "pointer-events-none opacity-45")}
                    onClick={(e) => {
                      e.preventDefault();
                      if (safePage > 1) setPage((p) => Math.max(1, p - 1));
                    }}
                  />
                </PaginationItem>

                {pageNumbers.map((entry, idx) => (
                  <PaginationItem key={entry === "ellipsis" ? `e-${idx}` : entry}>
                    {entry === "ellipsis" ? (
                      <PaginationEllipsis />
                    ) : (
                      <PaginationLink
                        href={buildCatalogUrl({
                          cat: selectedCategory,
                          q: query,
                          sort,
                          page: entry,
                        })}
                        isActive={entry === safePage}
                        className="min-h-10 min-w-10 tabular-nums"
                        onClick={(e) => {
                          e.preventDefault();
                          setPage(entry);
                        }}
                      >
                        {entry}
                      </PaginationLink>
                    )}
                  </PaginationItem>
                ))}

                <PaginationItem>
                  <PaginationNext
                    href={buildCatalogUrl({
                      cat: selectedCategory,
                      q: query,
                      sort,
                      page: Math.min(totalPages, safePage + 1),
                    })}
                    aria-disabled={safePage >= totalPages}
                    tabIndex={safePage >= totalPages ? -1 : undefined}
                    className={cn(
                      "min-h-10",
                      safePage >= totalPages && "pointer-events-none opacity-45"
                    )}
                    onClick={(e) => {
                      e.preventDefault();
                      if (safePage < totalPages) setPage((p) => Math.min(totalPages, p + 1));
                    }}
                  />
                </PaginationItem>
              </PaginationContent>
            </Pagination>
          </div>
        )}
      </>
    );
  };

  return (
    <div data-catalog-layout={layout} className="w-full text-foreground">
      <section
        className="w-full pt-[clamp(24px,3.2vw,44px)] pb-6"
        aria-labelledby="site-catalog-heading"
      >
        <div
          className={cn(
            "mx-auto flex w-full max-w-[var(--site-max-w,1260px)] flex-col gap-2 px-[clamp(1rem,2.2vw,2rem)]",
            isCenteredHead && "items-center text-center"
          )}
        >
          <h1
            id="site-catalog-heading"
            className={cn(
              "text-[clamp(28px,4vw,46px)] leading-[1.08] tracking-[-0.025em] text-foreground",
              layout === "luxury-curated" ? "font-normal tracking-[0.02em]" : "font-semibold"
            )}
          >
            {introTitle}
          </h1>
          {introSubtitle && (
            <p className="max-w-[62ch] text-[15px] leading-6 text-muted-foreground">
              {introSubtitle}
            </p>
          )}
        </div>
      </section>

      <section className="w-full pb-[clamp(48px,6vw,80px)]">
        <div className="mx-auto w-full max-w-[var(--site-max-w,1260px)] px-[clamp(1rem,2.2vw,2rem)]">
          {useSidebar ? (
            <div className="grid grid-cols-1 items-start gap-7 lg:grid-cols-[248px_minmax(0,1fr)]">
              <aside
                className="sticky top-20 hidden rounded-lg border border-border bg-card p-5 lg:block"
                aria-label="Bộ lọc danh mục"
              >
                <CatalogFilterControls idPrefix="catalog-sidebar" {...sharedFilterProps} />
              </aside>

              <div className="min-w-0">
                <div className="mb-4 flex flex-wrap items-center justify-between gap-3 border-b border-border pb-3.5">
                  <div className="flex flex-wrap items-center gap-3">
                    {(showFilters || showSearch) && (
                      <Button
                        type="button"
                        variant="outline"
                        className="min-h-10 gap-2 shadow-none lg:hidden"
                        aria-expanded={sheetOpen}
                        onClick={handleOpenSheet}
                      >
                        <SlidersHorizontal className="size-4" aria-hidden="true" />
                        <span>Bộ lọc</span>
                        {activeFilterCount > 0 && (
                          <Badge variant="default" className="px-1.5 py-0 text-[11px] tabular-nums">
                            {activeFilterCount}
                          </Badge>
                        )}
                      </Button>
                    )}
                    <span className="text-sm text-muted-foreground tabular-nums">
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
              <div className="sticky top-14 z-20 mb-5 flex flex-wrap items-center justify-between gap-3 border-b border-border bg-background/95 py-3 backdrop-blur-xs">
                <div className="flex flex-wrap items-center gap-2.5">
                  {(showFilters || showSearch) && (
                    <Button
                      type="button"
                      variant="outline"
                      className={cn("min-h-10 gap-2 shadow-none", isTopBar && "lg:hidden")}
                      aria-expanded={sheetOpen}
                      onClick={handleOpenSheet}
                    >
                      <SlidersHorizontal className="size-4" aria-hidden="true" />
                      <span>Bộ lọc</span>
                      {activeFilterCount > 0 && (
                        <Badge variant="default" className="px-1.5 py-0 text-[11px] tabular-nums">
                          {activeFilterCount}
                        </Badge>
                      )}
                    </Button>
                  )}

                  {isTopBar && showFilters && (
                    <div
                      className="hidden flex-wrap items-center gap-1.5 lg:flex"
                      role="group"
                      aria-label="Lọc theo danh mục"
                    >
                      <Button
                        type="button"
                        size="sm"
                        variant={selectedCategory === "" ? "default" : "outline"}
                        className="min-h-9 shadow-none"
                        onClick={() => {
                          setSelectedCategory("");
                          setPage(1);
                        }}
                      >
                        Tất cả ({products.length})
                      </Button>
                      {categories.map((cat) => (
                        <Button
                          key={cat.slug}
                          type="button"
                          size="sm"
                          variant={selectedCategory === cat.slug ? "default" : "outline"}
                          className="min-h-9 shadow-none"
                          onClick={() => {
                            setSelectedCategory(cat.slug);
                            setPage(1);
                          }}
                        >
                          {cat.name}
                        </Button>
                      ))}
                    </div>
                  )}

                  <span className="text-sm text-muted-foreground tabular-nums">
                    {formatInt(filteredProducts.length)} sản phẩm
                  </span>
                </div>

                <div className="flex flex-wrap items-center gap-2.5">
                  {isTopBar && showSearch && (
                    <div className="hidden lg:block">
                      <label htmlFor="site-catalog-topbar-search" className="sr-only">
                        Lọc từ khóa
                      </label>
                      <Input
                        id="site-catalog-topbar-search"
                        type="search"
                        className="h-10 w-52 bg-card"
                        placeholder="Lọc tên, chất liệu..."
                        value={query}
                        onChange={(e) => {
                          setQuery(e.target.value);
                          setPage(1);
                        }}
                      />
                    </div>
                  )}

                  <label htmlFor="site-catalog-sort-bar" className="sr-only">
                    Sắp xếp
                  </label>
                  <Select
                    value={sort}
                    onValueChange={(v) => {
                      setSort(v as CatalogSortKey);
                      setPage(1);
                    }}
                  >
                    <SelectTrigger
                      id="site-catalog-sort-bar"
                      aria-label="Sắp xếp sản phẩm"
                      className="h-10 min-w-[168px] bg-card shadow-none"
                    >
                      <SelectValue placeholder="Sắp xếp" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="newest">Mới nhất</SelectItem>
                      <SelectItem value="price_asc">Giá tăng dần</SelectItem>
                      <SelectItem value="price_desc">Giá giảm dần</SelectItem>
                    </SelectContent>
                  </Select>

                  {renderViewToggle()}
                </div>
              </div>

              {renderActiveChips()}
              {renderResultsArea()}
            </>
          )}
        </div>
      </section>

      {/* Lazy-loaded Filter Sheet Drawer */}
      {sheetMounted && (
        <CatalogFilterSheet
          open={sheetOpen}
          onOpenChange={setSheetOpen}
          sheetFilterCount={sheetFilterCount}
          matchedCount={filteredProducts.length}
          onResetSheetFilters={resetSheetFilters}
          {...sharedFilterProps}
        />
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
