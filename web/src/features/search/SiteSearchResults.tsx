"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { IconClose, IconFilter, StateView } from "@/components";
import { useModalFocusTrap } from "@/components/modalFocus";
import { formatDiscountPercent, formatInt, formatVnd } from "@/lib/format";
import { searchProducts, suggestSpelling } from "@/lib/search";
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
import styles from "./SiteSearchResults.module.css";

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
  const [sheetClosing, setSheetClosing] = useState(false);
  const filterSheetRef = useRef<HTMLElement | null>(null);
  const sheetCloseTimerRef = useRef<number | null>(null);

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
    open: sheetOpen && !sheetClosing,
    containerRef: filterSheetRef,
    onEscape: closeSheet,
  });

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

  const gridClass =
    result.items.length === 1
      ? styles.productGridSingle
      : result.items.length === 2
        ? styles.productGridTwo
        : result.items.length === 3
          ? styles.productGridThree
          : styles.productGrid;

  return (
    <div className={styles.page}>
      <section className={styles.headerBand}>
        <div className={styles.headerCopy}>
          <h1 className={styles.pageTitle}>
            {query.trim() ? `Kết quả cho “${query.trim()}”` : `Tìm kiếm tại ${brandName}`}
          </h1>
        </div>

        <div className={styles.searchBarWrap}>
          <SiteSearchBar
            brandName={brandName}
            shopId={shopId}
            initialQuery={query}
            placeholder={`Tìm tên mẫu, chất liệu tại ${brandName}...`}
            onNavigate={handleSearchNavigate}
          />
        </div>
      </section>

      <section className={styles.toolbarBand} aria-label="Bộ lọc và sắp xếp">
        <div className={styles.toolbarLeft}>
          <button
            type="button"
            className={styles.filterSheetBtn}
            aria-expanded={sheetOpen}
            onClick={openSheet}
          >
            <IconFilter size={15} />
            <span>Bộ lọc</span>
            {activeFilterCount > 0 && (
              <span className={`${styles.filterBadge} num`}>{activeFilterCount}</span>
            )}
          </button>

          <span className={`${styles.resultCount} num`} aria-live="polite">
            {loading && !hasLoadedOnce ? "Đang tìm..." : `${formatInt(result.total)} kết quả`}
          </span>
        </div>

        <div className={styles.toolbarRight}>
          <label htmlFor="site-search-sort" className="sr-only">
            Sắp xếp
          </label>
          <select
            id="site-search-sort"
            className={styles.sortSelect}
            value={sort}
            onChange={(e) => {
              setSort(normalizeSearchSort(e.target.value));
              setPage(1);
            }}
          >
            {VALID_SEARCH_SORTS.map((key) => (
              <option key={key} value={key}>
                {SEARCH_SORT_LABELS[key]}
              </option>
            ))}
          </select>
        </div>
      </section>

      {hasActiveFilters && (
        <div className={styles.activeChipRow}>
          {query.trim() && (
            <button
              type="button"
              className={styles.activeChip}
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
          {category && (
            <button
              type="button"
              className={styles.activeChip}
              aria-label={`Bỏ lọc danh mục ${categoryLabelBySlug.get(category) ?? category}`}
              onClick={() => {
                setCategory("");
                setPage(1);
              }}
            >
              <span>{categoryLabelBySlug.get(category) ?? category}</span>
              <IconClose size={13} />
            </button>
          )}
          {(minPrice !== undefined || maxPrice !== undefined) && (
            <button
              type="button"
              className={`${styles.activeChip} num`}
              aria-label={`Bỏ lọc khoảng giá ${minPrice !== undefined ? formatVnd(minPrice) : "0₫"} đến ${maxPrice !== undefined ? formatVnd(maxPrice) : "không giới hạn"}`}
              onClick={() => handleSelectPricePreset(undefined, undefined)}
            >
              <span>
                {minPrice !== undefined ? formatVnd(minPrice) : "0₫"} đến{" "}
                {maxPrice !== undefined ? formatVnd(maxPrice) : "∞"}
              </span>
              <IconClose size={13} />
            </button>
          )}
          {sort !== "relevance" && (
            <button
              type="button"
              className={styles.activeChip}
              aria-label={`Bỏ lọc sắp xếp ${SEARCH_SORT_LABELS[sort]}`}
              onClick={() => {
                setSort("relevance");
                setPage(1);
              }}
            >
              <span>{SEARCH_SORT_LABELS[sort]}</span>
              <IconClose size={13} />
            </button>
          )}
          <button type="button" className={styles.resetFiltersBtn} onClick={handleResetAll}>
            Xóa tất cả
          </button>
        </div>
      )}

      {spellingSuggestion && (
        <p className={styles.spellingBanner} role="status">
          <span>Có phải bạn muốn tìm</span>
          <button
            type="button"
            className={styles.spellingBtn}
            onClick={() => handleSearchNavigate(spellingSuggestion)}
          >
            {spellingSuggestion}
          </button>
          <span>?</span>
        </p>
      )}

      <section className={styles.resultsSection} aria-busy={loading}>
        {loading && !hasLoadedOnce ? (
          <StateView
            kind="loading"
            surface="shop"
            skeletonVariant="cards"
            title="Đang tải kết quả..."
            rows={4}
          />
        ) : errorMessage ? (
          <StateView
            kind="error"
            title="Chưa tải được kết quả"
            message={errorMessage}
            actionLabel="Thử lại"
            onAction={() => setRetryTick((t) => t + 1)}
          />
        ) : result.items.length === 0 ? (
          <div className={styles.emptyPanel}>
            <h2 className={styles.emptyTitle}>Không có sản phẩm khớp yêu cầu</h2>
            <p className={styles.emptyBody}>Thử đổi từ khóa ngắn hơn hoặc bỏ bớt bộ lọc.</p>
            <div className={styles.emptyActions}>
              {hasActiveFilters && (
                <button type="button" className={styles.btnPrimary} onClick={handleResetAll}>
                  Xóa bộ lọc
                </button>
              )}
              <Link href="/san-pham" className={styles.btnSecondary}>
                Tất cả sản phẩm
              </Link>
            </div>
          </div>
        ) : (
          <>
            <div className={`${gridClass} ${loading ? styles.productGridLoading : ""}`}>
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

                return (
                  <article
                    key={item.id}
                    className={`${styles.productCard} ${
                      result.items.length === 1 ? styles.productCardWide : ""
                    }`}
                    data-has-hover={secondaryImg ? "true" : "false"}
                  >
                    <Link
                      href={href}
                      className={styles.cardMediaLink}
                      tabIndex={-1}
                      aria-hidden="true"
                    >
                      <img
                        src={primaryImg}
                        alt={item.name}
                        width={896}
                        height={1200}
                        className={styles.cardImgPrimary}
                        loading={idx < 4 ? "eager" : "lazy"}
                      />
                      {secondaryImg && (
                        <img
                          src={secondaryImg}
                          alt=""
                          aria-hidden="true"
                          width={896}
                          height={1200}
                          className={styles.cardImgSecondary}
                          loading="lazy"
                        />
                      )}
                    </Link>

                    <div className={styles.cardBody}>
                      <div className={styles.cardTopMeta}>
                        {item.isFlashSale ? (
                          <span className={styles.cardTagText}>Flash sale</span>
                        ) : hasDiscount && item.originalPriceVnd ? (
                          <span className={`${styles.cardTagText} num`}>
                            {formatDiscountPercent(item.originalPriceVnd, item.priceVnd)}
                          </span>
                        ) : null}
                      </div>

                      <div className={styles.cardPriceRow}>
                        <span className={`${styles.cardPrice} num`}>
                          {formatVnd(item.priceVnd)}
                        </span>
                        {hasDiscount && item.originalPriceVnd && (
                          <span className={`${styles.cardOriginalPrice} num`}>
                            {formatVnd(item.originalPriceVnd)}
                          </span>
                        )}
                        {stockNotice && (
                          <span className={`${styles.cardStock} num`}>{stockNotice}</span>
                        )}
                      </div>

                      {/* Keep title link as the LAST a[href^="/san-pham/"] inside article */}
                      <Link href={href} className={styles.cardName}>
                        {item.name}
                      </Link>
                    </div>
                  </article>
                );
              })}
            </div>

            {totalPages > 1 && (
              <nav className={styles.paginationBar} aria-label="Phân trang kết quả tìm kiếm">
                <span className={`${styles.paginationInfo} num`}>
                  Trang {safePage} / {totalPages}
                </span>
                <div className={styles.paginationControls}>
                  <button
                    type="button"
                    className={styles.pageBtn}
                    disabled={safePage <= 1}
                    onClick={() => setPage((p) => Math.max(1, p - 1))}
                  >
                    Trước
                  </button>

                  {pageNumbers.map((entry, idx) =>
                    entry === "ellipsis" ? (
                      <span key={`e-${idx}`} className={styles.pageEllipsis} aria-hidden="true">
                        …
                      </span>
                    ) : (
                      <button
                        key={entry}
                        type="button"
                        className={`${styles.pageNumBtn} num ${
                          entry === safePage ? styles.pageNumBtnActive : ""
                        }`}
                        aria-current={entry === safePage ? "page" : undefined}
                        onClick={() => setPage(entry)}
                      >
                        {entry}
                      </button>
                    )
                  )}

                  <button
                    type="button"
                    className={styles.pageBtn}
                    disabled={safePage >= totalPages}
                    onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                  >
                    Sau
                  </button>
                </div>
              </nav>
            )}
          </>
        )}
      </section>

      {/* Filter Sheet */}
      {sheetOpen && (
        <div
          className={`${styles.sheetBackdrop} ${sheetClosing ? styles.sheetBackdropClosing : ""}`}
          role="presentation"
          onClick={(e) => {
            if (e.target === e.currentTarget) closeSheet();
          }}
        >
          <aside
            ref={filterSheetRef}
            tabIndex={-1}
            className={`${styles.sheetPanel} ${sheetClosing ? styles.sheetPanelClosing : ""}`}
            role="dialog"
            aria-modal="true"
            aria-label="Bộ lọc kết quả tìm kiếm"
          >
            <header className={styles.sheetHead}>
              <span className={styles.sheetTitle}>Bộ lọc</span>
              <button
                type="button"
                className={styles.sheetClose}
                onClick={closeSheet}
                aria-label="Đóng bộ lọc"
              >
                <IconClose size={18} />
              </button>
            </header>

            <div className={styles.sheetBody}>
              {categoryFacets.length > 0 && (
                <div className={styles.filterSection}>
                  <span className={styles.filterGroupLabel}>Danh mục</span>
                  <div className={styles.chipRow} role="group" aria-label="Lọc theo danh mục">
                    <button
                      type="button"
                      className={`${styles.chip} ${category === "" ? styles.chipActive : ""}`}
                      onClick={() => {
                        setCategory("");
                        setPage(1);
                      }}
                    >
                      Tất cả
                    </button>
                    {categoryFacets.map((facet) => (
                      <button
                        key={facet.value}
                        type="button"
                        className={`${styles.chip} ${category === facet.value ? styles.chipActive : ""}`}
                        onClick={() => {
                          setCategory(facet.value);
                          setPage(1);
                        }}
                      >
                        <span>{facet.label}</span>
                        {facet.count > 0 && (
                          <span className={`${styles.chipCount} num`}>
                            ({formatInt(facet.count)})
                          </span>
                        )}
                      </button>
                    ))}
                  </div>
                </div>
              )}

              <div className={styles.filterSection}>
                <span className={styles.filterGroupLabel}>Khoảng giá</span>
                <div className={styles.chipRow} role="group" aria-label="Chọn nhanh khoảng giá">
                  {SEARCH_PRICE_PRESETS.map((preset) => (
                    <button
                      key={preset.id}
                      type="button"
                      className={`${styles.chip} ${
                        activePresetId === preset.id ? styles.chipActive : ""
                      }`}
                      onClick={() => handleSelectPricePreset(preset.minPrice, preset.maxPrice)}
                    >
                      {preset.label}
                    </button>
                  ))}
                </div>

                <form className={styles.priceForm} onSubmit={handleApplyPrice} noValidate>
                  <label htmlFor="search-min-price" className="sr-only">
                    Giá từ (VND)
                  </label>
                  <input
                    id="search-min-price"
                    type="text"
                    inputMode="numeric"
                    className={`${styles.priceInput} num`}
                    placeholder="Từ (VND)"
                    value={minPriceInput}
                    onChange={(e) => setMinPriceInput(e.target.value)}
                  />
                  <span className={styles.priceRangeSep} aria-hidden="true">
                    đến
                  </span>
                  <label htmlFor="search-max-price" className="sr-only">
                    Giá đến (VND)
                  </label>
                  <input
                    id="search-max-price"
                    type="text"
                    inputMode="numeric"
                    className={`${styles.priceInput} num`}
                    placeholder="Đến (VND)"
                    value={maxPriceInput}
                    onChange={(e) => setMaxPriceInput(e.target.value)}
                  />
                  <button type="submit" className={styles.applyPriceBtn}>
                    Áp dụng
                  </button>
                </form>
                {priceInputError && (
                  <p className={styles.priceError} role="alert">
                    {priceInputError}
                  </p>
                )}
              </div>
            </div>

            <footer className={styles.sheetFoot}>
              {sheetFilterCount > 0 && (
                <button
                  type="button"
                  className={styles.btnSecondary}
                  onClick={() => {
                    handleResetSheetFilters();
                    closeSheet();
                  }}
                >
                  Xóa lọc
                </button>
              )}
              <button type="button" className={styles.btnPrimary} onClick={closeSheet}>
                Xem {formatInt(result.total)} kết quả
              </button>
            </footer>
          </aside>
        </div>
      )}
    </div>
  );
}

export { SiteSearchResults as SiteSearchPage };
