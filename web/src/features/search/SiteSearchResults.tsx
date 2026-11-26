"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import dynamic from "next/dynamic";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { SearchX, SlidersHorizontal, X } from "lucide-react";
import { EmptyState } from "@/components/app/EmptyState";
import { ErrorState } from "@/components/app/ErrorState";
import { Money } from "@/components/app/Money";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
import { Skeleton } from "@/components/ui/skeleton";
import { formatDiscountPercent, formatInt, formatVnd } from "@/lib/format";
import { searchProducts, suggestSpelling } from "@/lib/search";
import { cn } from "@/lib/utils";
import { SearchFilterControls } from "./SearchFilterControls";
import { SiteSearchBar } from "./SiteSearchBar";
import {
  buildSearchFilterUrl,
  DEFAULT_SEARCH_PAGE_SIZE,
  normalizeSearchResponse,
  normalizeSearchSort,
  parseNonNegativeInt,
  runFallbackProductSearch,
  sanitizeSearchImageUrl,
  SEARCH_PRICE_PRESETS,
  SEARCH_SORT_LABELS,
  VALID_SEARCH_SORTS,
  type NormalizedSearchResponse,
  type SearchFacetBucket,
  type SearchSort,
  type SiteSearchResultsProps,
} from "./searchTypes";

const SearchFilterSheet = dynamic(
  () => import("./SearchFilterSheet").then((mod) => mod.SearchFilterSheet),
  { ssr: false }
);

const FALLBACK_IMAGE = "/banners/fabric-closeup.jpg";

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

export function SiteSearchResults({
  brandName,
  shopId,
  initialQuery = "",
  initialCategory = "",
  initialSort = "relevance",
  initialMinPrice,
  initialMaxPrice,
  initialPage = 1,
  fallbackProducts,
  categories = [],
}: SiteSearchResultsProps) {
  const router = useRouter();
  const hasMountedRef = useRef(false);

  const [query, setQuery] = useState(initialQuery);
  const [category, setCategory] = useState(initialCategory);
  const [sort, setSort] = useState<SearchSort>(() => normalizeSearchSort(initialSort));
  const [minPrice, setMinPrice] = useState<number | undefined>(() =>
    parseNonNegativeInt(initialMinPrice)
  );
  const [maxPrice, setMaxPrice] = useState<number | undefined>(() =>
    parseNonNegativeInt(initialMaxPrice)
  );
  const [minPriceInput, setMinPriceInput] = useState(
    parseNonNegativeInt(initialMinPrice) !== undefined
      ? String(parseNonNegativeInt(initialMinPrice))
      : ""
  );
  const [maxPriceInput, setMaxPriceInput] = useState(
    parseNonNegativeInt(initialMaxPrice) !== undefined
      ? String(parseNonNegativeInt(initialMaxPrice))
      : ""
  );
  const [priceInputError, setPriceInputError] = useState<string | null>(null);
  const [page, setPage] = useState(() => Math.max(1, Math.trunc(initialPage || 1)));
  const [retryTick, setRetryTick] = useState(0);
  const [sheetOpen, setSheetOpen] = useState(false);
  const [sheetMounted, setSheetMounted] = useState(false);

  const [result, setResult] = useState<NormalizedSearchResponse>(() => {
    if (Array.isArray(fallbackProducts)) {
      return runFallbackProductSearch(
        fallbackProducts,
        {
          q: initialQuery.trim(),
          category: initialCategory.trim(),
          min_price: parseNonNegativeInt(initialMinPrice),
          max_price: parseNonNegativeInt(initialMaxPrice),
          sort: normalizeSearchSort(initialSort),
          page: Math.max(1, Math.trunc(initialPage || 1)),
          page_size: DEFAULT_SEARCH_PAGE_SIZE,
        },
        searchProducts
      );
    }
    return {
      items: [],
      total: 0,
      facets: { categories: [], priceRanges: [] },
      page: Math.max(1, Math.trunc(initialPage || 1)),
      page_size: DEFAULT_SEARCH_PAGE_SIZE,
    };
  });
  const [loading, setLoading] = useState(!Array.isArray(fallbackProducts));
  const [hasLoadedOnce, setHasLoadedOnce] = useState(Array.isArray(fallbackProducts));
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  useEffect(() => {
    setQuery(initialQuery);
    setPage(1);
  }, [initialQuery]);

  // Preserve scroll position on every filter/page change
  useEffect(() => {
    if (!hasMountedRef.current) {
      hasMountedRef.current = true;
      return;
    }
    const nextUrl = buildSearchFilterUrl({
      q: query,
      category,
      minPrice,
      maxPrice,
      sort,
      page,
    });
    router.replace(nextUrl, { scroll: false });
  }, [query, category, minPrice, maxPrice, sort, page, router]);

  useEffect(() => {
    const controller = new AbortController();
    const params = {
      q: query.trim(),
      category: category.trim(),
      ...(minPrice !== undefined ? { min_price: minPrice } : {}),
      ...(maxPrice !== undefined ? { max_price: maxPrice } : {}),
      sort,
      page,
      page_size: DEFAULT_SEARCH_PAGE_SIZE,
    };

    const sp = new URLSearchParams();
    if (params.q) sp.set("q", params.q);
    if (params.category) sp.set("category", params.category);
    if (params.min_price !== undefined) sp.set("min_price", String(params.min_price));
    if (params.max_price !== undefined) sp.set("max_price", String(params.max_price));
    sp.set("sort", params.sort);
    sp.set("page", String(params.page));
    sp.set("page_size", String(params.page_size));

    setLoading(true);
    setErrorMessage(null);

    const run = async () => {
      try {
        const res = await fetch(`/api/search?${sp.toString()}`, {
          method: "GET",
          headers: { Accept: "application/json" },
          cache: "no-store",
          signal: controller.signal,
        });
        if (!res.ok) {
          throw new Error(`HTTP_${res.status}`);
        }
        const raw = (await res.json()) as unknown;
        if (controller.signal.aborted) return;
        const normalized = normalizeSearchResponse(raw, {
          page: params.page,
          page_size: params.page_size,
          shopId,
        });
        setResult(normalized);
        setErrorMessage(null);
        setHasLoadedOnce(true);
      } catch {
        if (controller.signal.aborted) return;
        if (Array.isArray(fallbackProducts)) {
          const localResult = runFallbackProductSearch(fallbackProducts, params, searchProducts);
          setResult(localResult);
          setErrorMessage(null);
          setHasLoadedOnce(true);
        } else {
          setErrorMessage("Không thể kết nối tìm kiếm lúc này.");
          setHasLoadedOnce(true);
        }
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };

    void run();
    return () => controller.abort();
  }, [query, category, minPrice, maxPrice, sort, page, shopId, fallbackProducts, retryTick]);

  const categoryLabelBySlug = useMemo(() => {
    const map = new Map<string, string>();
    for (const c of categories) {
      map.set(c.slug, c.name);
    }
    return map;
  }, [categories]);

  const categoryFacets: SearchFacetBucket[] = useMemo(() => {
    if (result.facets.categories.length > 0) {
      return result.facets.categories.map((bucket) => ({
        ...bucket,
        label: categoryLabelBySlug.get(bucket.value) ?? bucket.label,
      }));
    }
    return categories.map((c) => ({
      value: c.slug,
      label: c.name,
      count: 0,
    }));
  }, [result.facets.categories, categories, categoryLabelBySlug]);

  const spellingSuggestion = useMemo(() => {
    if (loading || errorMessage || result.items.length > 0 || !query.trim()) return null;
    return suggestSpelling(query);
  }, [loading, errorMessage, result.items.length, query]);

  const totalPages = Math.max(1, Math.ceil(result.total / result.page_size));
  const safePage = Math.min(page, totalPages);
  const pageNumbers = useMemo(() => buildPageNumbers(safePage, totalPages), [safePage, totalPages]);

  const sheetFilterCount =
    (category ? 1 : 0) + (minPrice !== undefined || maxPrice !== undefined ? 1 : 0);
  const activeFilterCount = sheetFilterCount + (sort !== "relevance" ? 1 : 0);

  const hasActiveFilters = Boolean(query.trim() || activeFilterCount > 0);

  const handleSearchNavigate = useCallback((nextQ: string) => {
    setQuery(nextQ);
    setPage(1);
  }, []);

  const handleApplyPrice = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const rawMin = minPriceInput.replace(/[.\s,]/g, "").trim();
    const rawMax = maxPriceInput.replace(/[.\s,]/g, "").trim();

    if (rawMin && !/^\d+$/.test(rawMin)) {
      setPriceInputError("Giá tối thiểu phải là số nguyên VND.");
      return;
    }
    if (rawMax && !/^\d+$/.test(rawMax)) {
      setPriceInputError("Giá tối đa phải là số nguyên VND.");
      return;
    }

    const nextMin = parseNonNegativeInt(rawMin);
    const nextMax = parseNonNegativeInt(rawMax);
    if (nextMin !== undefined && nextMax !== undefined && nextMin > nextMax) {
      setPriceInputError("Giá tối thiểu phải nhỏ hơn hoặc bằng giá tối đa.");
      return;
    }

    setPriceInputError(null);
    setMinPrice(nextMin);
    setMaxPrice(nextMax);
    setPage(1);
  };

  const handleSelectPricePreset = (presetMin?: number, presetMax?: number) => {
    setPriceInputError(null);
    setMinPrice(presetMin);
    setMaxPrice(presetMax);
    setMinPriceInput(presetMin !== undefined ? String(presetMin) : "");
    setMaxPriceInput(presetMax !== undefined ? String(presetMax) : "");
    setPage(1);
  };

  const handleResetSheetFilters = () => {
    setCategory("");
    setMinPrice(undefined);
    setMaxPrice(undefined);
    setMinPriceInput("");
    setMaxPriceInput("");
    setPriceInputError(null);
    setPage(1);
  };

  const handleResetAll = () => {
    setQuery("");
    setCategory("");
    setSort("relevance");
    setMinPrice(undefined);
    setMaxPrice(undefined);
    setMinPriceInput("");
    setMaxPriceInput("");
    setPriceInputError(null);
    setPage(1);
  };

  const activePresetId = useMemo(() => {
    const found = SEARCH_PRICE_PRESETS.find(
      (p) => p.minPrice === minPrice && p.maxPrice === maxPrice
    );
    return found?.id ?? "";
  }, [minPrice, maxPrice]);

  const handleOpenFilterSheet = () => {
    setSheetMounted(true);
    setSheetOpen(true);
  };

  const gridClass =
    result.items.length === 1
      ? "grid grid-cols-1"
      : result.items.length === 2
        ? "grid grid-cols-1 gap-5 sm:grid-cols-2 lg:gap-7"
        : result.items.length === 3
          ? "grid grid-cols-2 gap-4 lg:grid-cols-3 lg:gap-6"
          : "grid grid-cols-2 gap-4 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-3 xl:grid-cols-4 lg:gap-6";

  const sharedFilterProps = {
    categoryFacets,
    category,
    onSelectCategory: (next: string) => {
      setCategory(next);
      setPage(1);
    },
    activePresetId,
    onSelectPricePreset: handleSelectPricePreset,
    minPriceInput,
    maxPriceInput,
    onMinPriceInputChange: setMinPriceInput,
    onMaxPriceInputChange: setMaxPriceInput,
    onApplyPrice: handleApplyPrice,
    priceInputError,
  };

  return (
    <div className="mx-auto flex w-full max-w-[var(--site-max-w,1260px)] flex-col gap-6 px-[clamp(1rem,2.2vw,2rem)] pt-[clamp(24px,3.2vw,44px)] pb-[clamp(48px,6vw,80px)] text-foreground">
      <section className="flex flex-col gap-4" aria-labelledby="site-search-heading">
        <div className="flex flex-col gap-1.5">
          <h1
            id="site-search-heading"
            className="text-[clamp(26px,3.6vw,42px)] leading-[1.1] font-semibold tracking-[-0.025em] text-foreground"
          >
            {query.trim() ? `Kết quả cho “${query.trim()}”` : `Tìm kiếm tại ${brandName}`}
          </h1>
        </div>

        <div className="w-full max-w-2xl">
          <SiteSearchBar
            brandName={brandName}
            shopId={shopId}
            initialQuery={query}
            placeholder={`Tìm tên mẫu, chất liệu tại ${brandName}...`}
            onNavigate={handleSearchNavigate}
          />
        </div>
      </section>

      <div className="grid grid-cols-1 items-start gap-7 lg:grid-cols-[248px_minmax(0,1fr)]">
        {/* Desktop left filter sidebar (>= 1024px) */}
        <aside
          className="sticky top-20 hidden rounded-lg border border-border bg-card p-5 lg:block"
          aria-label="Bộ lọc kết quả tìm kiếm"
        >
          <div className="mb-4 flex items-center justify-between gap-2">
            <h2 className="text-sm font-semibold text-foreground">Bộ lọc</h2>
            {sheetFilterCount > 0 && (
              <Button
                type="button"
                variant="link"
                size="sm"
                className="h-auto p-0 text-xs font-semibold text-brand"
                onClick={handleResetSheetFilters}
              >
                Xóa lọc
              </Button>
            )}
          </div>
          <SearchFilterControls idPrefix="search-sidebar" {...sharedFilterProps} />
        </aside>

        {/* Results column */}
        <div className="flex min-w-0 flex-col gap-5">
          <section
            className="flex flex-wrap items-center justify-between gap-3 border-b border-border pb-3.5"
            aria-label="Bộ lọc và sắp xếp"
          >
            <div className="flex flex-wrap items-center gap-3">
              <Button
                type="button"
                variant="outline"
                className="min-h-10 gap-2 shadow-none lg:hidden"
                aria-expanded={sheetOpen}
                onClick={handleOpenFilterSheet}
              >
                <SlidersHorizontal className="size-4" aria-hidden="true" />
                <span>Bộ lọc</span>
                {activeFilterCount > 0 && (
                  <Badge variant="default" className="px-1.5 py-0 text-[11px] tabular-nums">
                    {activeFilterCount}
                  </Badge>
                )}
              </Button>

              <span className="text-sm text-muted-foreground tabular-nums" aria-live="polite">
                {loading && !hasLoadedOnce ? "Đang tìm..." : `${formatInt(result.total)} kết quả`}
              </span>
            </div>

            <div className="flex items-center gap-2">
              <label htmlFor="site-search-sort" className="sr-only">
                Sắp xếp
              </label>
              <Select
                value={sort}
                onValueChange={(next) => {
                  setSort(normalizeSearchSort(next));
                  setPage(1);
                }}
              >
                <SelectTrigger
                  id="site-search-sort"
                  aria-label="Sắp xếp kết quả"
                  className="h-10 min-w-[176px] bg-card shadow-none"
                >
                  <SelectValue placeholder="Sắp xếp" />
                </SelectTrigger>
                <SelectContent>
                  {VALID_SEARCH_SORTS.map((key) => (
                    <SelectItem key={key} value={key}>
                      {SEARCH_SORT_LABELS[key]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </section>

          {hasActiveFilters && (
            <div className="flex flex-wrap items-center gap-2">
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
              {category && (
                <Button
                  type="button"
                  variant="secondary"
                  size="sm"
                  className="min-h-9 gap-1.5 rounded-full px-3 text-[13px] shadow-none"
                  aria-label={`Bỏ lọc danh mục ${categoryLabelBySlug.get(category) ?? category}`}
                  onClick={() => {
                    setCategory("");
                    setPage(1);
                  }}
                >
                  <span>{categoryLabelBySlug.get(category) ?? category}</span>
                  <X className="size-3.5" aria-hidden="true" />
                </Button>
              )}
              {(minPrice !== undefined || maxPrice !== undefined) && (
                <Button
                  type="button"
                  variant="secondary"
                  size="sm"
                  className="min-h-9 gap-1.5 rounded-full px-3 text-[13px] tabular-nums shadow-none"
                  aria-label={`Bỏ lọc khoảng giá ${minPrice !== undefined ? formatVnd(minPrice) : "0₫"} đến ${maxPrice !== undefined ? formatVnd(maxPrice) : "không giới hạn"}`}
                  onClick={() => handleSelectPricePreset(undefined, undefined)}
                >
                  <span>
                    {minPrice !== undefined ? formatVnd(minPrice) : "0₫"} đến{" "}
                    {maxPrice !== undefined ? formatVnd(maxPrice) : "∞"}
                  </span>
                  <X className="size-3.5" aria-hidden="true" />
                </Button>
              )}
              {sort !== "relevance" && (
                <Button
                  type="button"
                  variant="secondary"
                  size="sm"
                  className="min-h-9 gap-1.5 rounded-full px-3 text-[13px] shadow-none"
                  aria-label={`Bỏ lọc sắp xếp ${SEARCH_SORT_LABELS[sort]}`}
                  onClick={() => {
                    setSort("relevance");
                    setPage(1);
                  }}
                >
                  <span>{SEARCH_SORT_LABELS[sort]}</span>
                  <X className="size-3.5" aria-hidden="true" />
                </Button>
              )}
              <Button
                type="button"
                variant="link"
                size="sm"
                className="min-h-9 px-2 text-[13px] font-semibold text-brand"
                onClick={handleResetAll}
              >
                Xóa tất cả
              </Button>
            </div>
          )}

          {spellingSuggestion && (
            <p
              className="flex flex-wrap items-center gap-1.5 rounded-lg border border-border bg-muted px-4 py-3 text-sm text-foreground"
              role="status"
            >
              <span>Có phải bạn muốn tìm</span>
              <Button
                type="button"
                variant="link"
                size="sm"
                className="h-auto p-0 text-sm font-semibold text-brand underline"
                onClick={() => handleSearchNavigate(spellingSuggestion)}
              >
                {spellingSuggestion}
              </Button>
              <span>?</span>
            </p>
          )}

          <section className="flex flex-col gap-7" aria-busy={loading}>
            {loading && !hasLoadedOnce ? (
              <div
                className="grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-4"
                aria-label="Đang tải kết quả tìm kiếm"
              >
                {Array.from({ length: 8 }).map((_, idx) => (
                  <div key={idx} className="flex flex-col gap-2.5">
                    <Skeleton className="aspect-[3/4] w-full rounded-lg" />
                    <Skeleton className="h-4 w-5/6" />
                    <Skeleton className="h-4 w-1/2" />
                  </div>
                ))}
              </div>
            ) : errorMessage ? (
              <ErrorState
                title="Chưa tải được kết quả"
                description={errorMessage}
                onRetry={() => setRetryTick((t) => t + 1)}
                className="rounded-lg border border-border shadow-none"
              />
            ) : result.items.length === 0 ? (
              <EmptyState
                icon={SearchX}
                title="Không có sản phẩm khớp yêu cầu"
                description="Thử đổi từ khóa ngắn hơn hoặc bỏ bớt bộ lọc."
                className="rounded-lg border border-border shadow-none"
                action={
                  <div className="flex flex-wrap items-center justify-center gap-2.5">
                    {hasActiveFilters && (
                      <Button
                        type="button"
                        className="min-h-10 shadow-none"
                        onClick={handleResetAll}
                      >
                        Xóa bộ lọc
                      </Button>
                    )}
                    <Button variant="outline" className="min-h-10 shadow-none" asChild>
                      <Link href="/san-pham">Tất cả sản phẩm</Link>
                    </Button>
                  </div>
                }
              />
            ) : (
              <>
                <div
                  className={cn(gridClass, loading && "opacity-60 transition-opacity duration-150")}
                >
                  {result.items.map((item, idx) => {
                    const href = `/san-pham/${encodeURIComponent(item.slug || item.id)}`;
                    const primaryImg = sanitizeSearchImageUrl(item.image) || FALLBACK_IMAGE;
                    const secondaryImg =
                      item.images.map(sanitizeSearchImageUrl).find((u) => u && u !== primaryImg) ||
                      null;
                    const hasDiscount =
                      typeof item.originalPriceVnd === "number" &&
                      item.originalPriceVnd > item.priceVnd;
                    const stockNotice = !item.inStock
                      ? "Tạm hết hàng"
                      : item.totalStock > 0 && item.totalStock <= 5
                        ? `Còn ${formatInt(item.totalStock)}`
                        : "";
                    const isWide = result.items.length === 1;

                    return (
                      <article
                        key={item.id}
                        className={cn(
                          "group min-w-0",
                          isWide
                            ? "grid grid-cols-1 items-center gap-5 rounded-lg border border-border bg-card p-4 md:grid-cols-[minmax(0,5fr)_minmax(0,7fr)] md:gap-8 md:p-5"
                            : "flex flex-col bg-transparent"
                        )}
                        data-has-hover={secondaryImg ? "true" : "false"}
                      >
                        <Link
                          href={href}
                          className={cn(
                            "relative block w-full overflow-hidden rounded-lg bg-muted",
                            isWide ? "aspect-[4/3]" : "aspect-[3/4]"
                          )}
                          tabIndex={-1}
                          aria-hidden="true"
                        >
                          <img
                            src={primaryImg}
                            alt={item.name}
                            width={896}
                            height={1200}
                            loading={idx < 4 ? "eager" : "lazy"}
                            className={cn(
                              "absolute inset-0 block size-full object-cover object-[center_16%] transition-all duration-300 ease-out",
                              secondaryImg
                                ? "group-hover:opacity-0 group-focus-within:opacity-0"
                                : "group-hover:scale-[1.035]"
                            )}
                          />
                          {secondaryImg && (
                            <img
                              src={secondaryImg}
                              alt=""
                              aria-hidden="true"
                              width={896}
                              height={1200}
                              loading="lazy"
                              className="absolute inset-0 block size-full scale-[1.035] object-cover object-[center_16%] opacity-0 transition-all duration-300 ease-out group-hover:scale-[1.015] group-hover:opacity-100 group-focus-within:scale-[1.015] group-focus-within:opacity-100"
                            />
                          )}
                          {(item.isFlashSale || (hasDiscount && item.originalPriceVnd)) && (
                            <div className="pointer-events-none absolute top-2.5 left-2.5 z-10">
                              {item.isFlashSale ? (
                                <Badge
                                  variant="warning"
                                  className="rounded-sm px-2 py-0.5 text-[11px] font-semibold"
                                >
                                  Flash sale
                                </Badge>
                              ) : item.originalPriceVnd ? (
                                <Badge
                                  variant="default"
                                  className="rounded-sm px-2 py-0.5 text-[11px] font-semibold tabular-nums"
                                >
                                  {formatDiscountPercent(item.originalPriceVnd, item.priceVnd)}
                                </Badge>
                              ) : null}
                            </div>
                          )}
                        </Link>

                        <div
                          className={cn(
                            "flex min-w-0 flex-1 flex-col gap-1.5",
                            isWide ? "p-1" : "px-0.5 pt-3 pb-1"
                          )}
                        >
                          {isWide && item.description && (
                            <p className="line-clamp-2 text-sm leading-5 text-muted-foreground">
                              {item.description}
                            </p>
                          )}

                          <div className="mt-auto flex flex-wrap items-baseline justify-between gap-x-2.5 gap-y-1 pt-0.5">
                            <Money
                              vnd={item.priceVnd}
                              compareAtVnd={hasDiscount ? item.originalPriceVnd : undefined}
                              size={isWide ? "lg" : "md"}
                            />
                            {stockNotice && (
                              <span
                                className={cn(
                                  "text-xs font-medium tabular-nums",
                                  !item.inStock ? "text-destructive" : "text-brand"
                                )}
                              >
                                {stockNotice}
                              </span>
                            )}
                          </div>

                          {/* Keep title link as the LAST a[href^="/san-pham/"] inside article */}
                          <Link
                            href={href}
                            className={cn(
                              "order-first line-clamp-2 text-foreground transition-colors hover:text-brand",
                              isWide
                                ? "text-lg leading-snug font-semibold"
                                : "text-[15px] leading-snug font-medium"
                            )}
                          >
                            {item.name}
                          </Link>
                        </div>
                      </article>
                    );
                  })}
                </div>

                {totalPages > 1 && (
                  <div className="flex flex-col items-center justify-between gap-3 border-t border-border pt-5 sm:flex-row">
                    <span className="text-sm text-muted-foreground tabular-nums">
                      Trang {safePage} / {totalPages}
                    </span>
                    <Pagination className="mx-0 w-auto">
                      <PaginationContent>
                        <PaginationItem>
                          <PaginationPrevious
                            href={buildSearchFilterUrl({
                              q: query,
                              category,
                              minPrice,
                              maxPrice,
                              sort,
                              page: Math.max(1, safePage - 1),
                            })}
                            aria-disabled={safePage <= 1}
                            tabIndex={safePage <= 1 ? -1 : undefined}
                            className={cn(
                              "min-h-10",
                              safePage <= 1 && "pointer-events-none opacity-45"
                            )}
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
                                href={buildSearchFilterUrl({
                                  q: query,
                                  category,
                                  minPrice,
                                  maxPrice,
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
                            href={buildSearchFilterUrl({
                              q: query,
                              category,
                              minPrice,
                              maxPrice,
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
                              if (safePage < totalPages)
                                setPage((p) => Math.min(totalPages, p + 1));
                            }}
                          />
                        </PaginationItem>
                      </PaginationContent>
                    </Pagination>
                  </div>
                )}
              </>
            )}
          </section>
        </div>
      </div>

      {/* Mobile/tablet filter Sheet (lazy-loaded via next/dynamic) */}
      {sheetMounted && (
        <SearchFilterSheet
          open={sheetOpen}
          onOpenChange={setSheetOpen}
          sheetFilterCount={sheetFilterCount}
          totalResults={result.total}
          onResetSheetFilters={handleResetSheetFilters}
          {...sharedFilterProps}
        />
      )}
    </div>
  );
}

export { SiteSearchResults as SiteSearchPage };
