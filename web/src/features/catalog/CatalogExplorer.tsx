"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import type {
  FabricFeatureCode,
  FitCode,
  GenderCode,
  MaterialCode,
  OccasionCode,
  PatternCode,
  SportCode,
} from "@/data";
import { api } from "@/lib/api";
import {
  buildActiveShopDiscountsMap,
  buildCategoryDescendantSlugMap,
  buildLiveFlashItemsBySkuId,
  mapGatewayShop,
  resolvePriceBucketRange,
} from "@/lib/catalog-mapper";
import type { CatalogCategory, CatalogProduct, CatalogShop } from "@/lib/catalog-types";
import { formatInt } from "@/lib/format";
import type {
  GatewayActiveShopDiscount,
  GatewayActiveShopDiscountsResponse,
  GatewayFlashSale,
  GatewayFlashSalesResponse,
  GatewaySearchFacetCount,
  GatewaySearchSortBy,
} from "@/lib/gateway-types";
import {
  buildCatalogCountSummary,
  collectSearchHitSkuIds,
  fetchSearchProductsClient,
  mapSearchHitToCatalogProduct,
  normalizeColorFilterParam,
  normalizeSearchSortParam,
  parseInStockParam,
  parseMinRatingX100Param,
  shouldShowCatalogEmptyState,
} from "@/lib/search-client";
import { fetchMissingShopsClient } from "@/lib/shop-lookup";
import { fetchActiveShopDiscountsWithFetcher } from "@/features/checkout/helpers";
import {
  CARE_GUIDE_LIBRARY,
  FABRIC_FEATURE_LABELS,
  FIT_LABELS,
  GENDER_LABELS,
  MATERIAL_LABELS,
  OCCASION_LABELS,
  PATTERN_LABELS,
  SPORT_LABELS,
  getLegacyShopSlugById,
  matchesGenderFilter,
} from "@/mock/productEditorial";
import { Button, IconClose, IconSearch, IconSliders, ProductCard, StateView } from "@/components";
import { useRevealObserver } from "./useRevealObserver";
import styles from "./CatalogExplorer.module.css";

export interface CatalogExplorerProps {
  mode: "catalog" | "search";
  categories: CatalogCategory[];
  shops: CatalogShop[];
  products: CatalogProduct[];
  serverTotal: number;
  page: number;
  pageSize: number;
  categoryFacets: GatewaySearchFacetCount[];
  shopFacets: GatewaySearchFacetCount[];
  didYouMean: string;
  degradedMode: string;
  trendingKeywords: string[];
  error: string | null;
}

type PriceBucket =
  "all" | "under-180" | "180-250" | "250-350" | "over-350" | "under-200" | "200-300" | "over-300";

type FilterGroupKey =
  | "cat"
  | "size"
  | "price"
  | "rating"
  | "in_stock"
  | "color"
  | "shop"
  | "material"
  | "fit"
  | "occasion"
  | "sport"
  | "feature"
  | "gender"
  | "pattern"
  | "origin";

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const QUICK_SIZE_CHIPS = ["XS", "S", "M", "L", "XL", "2XL", "3XL", "4XL"] as const;

interface ColorOption {
  code: string;
  label: string;
  swatch: string;
}

const COLOR_OPTIONS: ColorOption[] = [
  { code: "trang", label: "Trắng", swatch: "#f0efe9" },
  { code: "kem", label: "Kem", swatch: "#ece6d8" },
  { code: "be", label: "Be cát", swatch: "#d6cec1" },
  { code: "xam", label: "Xám muối tiêu", swatch: "#949ea6" },
  { code: "xamda", label: "Xám đá", swatch: "#9aa5ac" },
  { code: "xamtro", label: "Xám tro", swatch: "#808991" },
  { code: "xamdam", label: "Xám đậm", swatch: "#5c666e" },
  { code: "den", label: "Đen", swatch: "#1f2428" },
  { code: "thanchi", label: "Than chì", swatch: "#313a40" },
  { code: "xanhthan", label: "Xanh than", swatch: "#25394a" },
  { code: "xanhda", label: "Xanh đá", swatch: "#546f7d" },
  { code: "covit", label: "Xanh cổ vịt", swatch: "#0d7480" },
  { code: "reu", label: "Xanh rêu", swatch: "#4f6457" },
];

const PRICE_OPTIONS: Array<{ id: PriceBucket; label: string }> = [
  { id: "all", label: "Tất cả mức giá" },
  { id: "under-180", label: "Dưới 180.000₫" },
  { id: "180-250", label: "180.000₫ – 250.000₫" },
  { id: "250-350", label: "250.000₫ – 350.000₫" },
  { id: "over-350", label: "Trên 350.000₫" },
];

const RATING_OPTIONS: Array<{ value: number; label: string }> = [
  { value: 500, label: "5 sao" },
  { value: 400, label: "Từ 4 sao trở lên" },
  { value: 300, label: "Từ 3 sao trở lên" },
];

const ORIGIN_OPTIONS: Array<{ code: string; label: string }> = [
  { code: "HP", label: "Kho Hải Phòng" },
  { code: "HN", label: "Kho Hà Nội" },
  { code: "DN", label: "Kho Đà Nẵng" },
  { code: "HCM", label: "Kho TP. Hồ Chí Minh" },
];

const SUBCATEGORY_FILTER_MAP: Record<string, Record<string, string | null>> = {
  "cotton-compact": { material: "cotton-compact", fit: null, sport: null },
  "the-thao-dryfit": { material: "polyester-dryfit", fit: null, sport: null },
  "oversize-260gsm": { fit: "oversize", material: null, sport: null },
  "polo-pique": { material: "pique-cotton", fit: null, sport: null },
  "polo-det-kim": { material: "viscose-knit", fit: null, sport: null },
  "short-daily": { material: "cotton-twill", fit: null, sport: null },
  "short-chay-bo": { sport: "chay-bo", material: null, fit: null },
  "short-linen": { material: "linen-cotton", fit: null, sport: null },
  "jogger-terry": { material: "french-terry", fit: null, sport: null },
  "jogger-tech": { material: "nylon-tech", fit: null, sport: null },
  "boxer-modal": { material: "modal", fit: null, sport: null },
  "brief-tre": { material: "bamboo", fit: null, sport: null },
  "tat-co-trung": { occasion: "cong-so", material: null, sport: null },
  "tat-the-thao": { sport: "chay-bo", material: null, occasion: null },
  "ao-khoac-gio": { material: "nylon-ripstop", sport: null, occasion: null },
};

function buildPageWindow(currentPage: number, totalPages: number): number[] {
  if (totalPages <= 1) return [1];
  const pages = new Set<number>([1, totalPages]);
  for (let p = Math.max(1, currentPage - 2); p <= Math.min(totalPages, currentPage + 2); p += 1) {
    pages.add(p);
  }
  return Array.from(pages).sort((a, b) => a - b);
}

export function CatalogExplorer({
  mode,
  categories,
  shops,
  products,
  serverTotal,
  page,
  pageSize,
  categoryFacets,
  shopFacets,
  didYouMean,
  degradedMode,
  trendingKeywords,
  error,
}: CatalogExplorerProps) {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const rootRef = useRef<HTMLDivElement>(null);

  const qParam = searchParams.get("q") ?? "";
  const catParam = searchParams.get("cat") ?? "";
  const shopParam = searchParams.get("shop") ?? "";
  const priceParam = (searchParams.get("price") ?? "all") as PriceBucket;
  const ratingRawParam = searchParams.get("min_rating") ?? searchParams.get("rating") ?? "";
  const minRatingX100 = parseMinRatingX100Param(ratingRawParam);
  const inStockOnly = parseInStockParam(searchParams.get("in_stock"));
  const sizeParam = searchParams.get("size") ?? "";
  const rawColorParam = searchParams.get("color") ?? "";
  const colorParam = normalizeColorFilterParam(rawColorParam) ?? "";
  const sortParam: GatewaySearchSortBy = normalizeSearchSortParam(searchParams.get("sort"));
  const stateParam = searchParams.get("state");

  // Secondary editorial filters (productEditorial.ts)
  const genderParam = (searchParams.get("gender") ?? "") as GenderCode | "";
  const originParam = searchParams.get("origin") ?? "";
  const materialParam = (searchParams.get("material") ?? "") as MaterialCode | "";
  const fitParam = (searchParams.get("fit") ?? "") as FitCode | "";
  const occasionParam = (searchParams.get("occasion") ?? "") as OccasionCode | "";
  const sportParam = (searchParams.get("sport") ?? "") as SportCode | "";
  const featureParam = (searchParams.get("feature") ?? "") as FabricFeatureCode | "";
  const patternParam = (searchParams.get("pattern") ?? "") as PatternCode | "";

  const [draftQuery, setDraftQuery] = useState(qParam);
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [sheetOpen, setSheetOpen] = useState(false);

  const [loadedProducts, setLoadedProducts] = useState<CatalogProduct[]>(products);
  const [loadedPage, setLoadedPage] = useState<number>(Math.max(1, page));
  const [loadingMore, setLoadingMore] = useState(false);
  const [loadMoreError, setLoadMoreError] = useState<string | null>(null);

  const loadMoreSeqRef = useRef(0);
  const flashCacheRef = useRef<{ fetchedAt: number; sales: GatewayFlashSale[] } | null>(null);

  const safePageSize = Math.max(1, Math.trunc(pageSize || 24));
  const safePage = Math.max(1, Math.trunc(page || 1));
  const totalPages = Math.max(1, Math.ceil(serverTotal / safePageSize));

  // Hide partial client counts on the 10 editorial facets (productEditorial.ts) whenever total > page_size (proposal 3 b1 §4).
  const hideEditorialCounts = serverTotal > safePageSize;

  const filterSignature = [
    qParam,
    catParam,
    shopParam,
    priceParam,
    minRatingX100 ?? "",
    inStockOnly ? "1" : "",
    sizeParam,
    colorParam,
    sortParam,
    safePage,
    genderParam,
    originParam,
    materialParam,
    fitParam,
    occasionParam,
    sportParam,
    featureParam,
    patternParam,
  ].join("|");

  useEffect(() => {
    setDraftQuery(qParam);
  }, [qParam]);

  useEffect(() => {
    loadMoreSeqRef.current += 1;
    setLoadedProducts(products);
    setLoadedPage(safePage);
    setLoadingMore(false);
    setLoadMoreError(null);
  }, [products, safePage, filterSignature]);

  const categoriesById = useMemo(() => new Map(categories.map((c) => [c.id, c])), [categories]);
  const shopsById = useMemo(() => new Map(shops.map((s) => [s.id, s])), [shops]);
  const categoryDescendantsBySlug = useMemo(
    () => buildCategoryDescendantSlugMap(categories),
    [categories]
  );

  // Primary groups open by default (Danh mục, Kích cỡ, Mức giá, Đánh giá, Tình trạng hàng);
  // secondary groups collapsed by default unless active in URL.
  const [openGroups, setOpenGroups] = useState<Record<FilterGroupKey, boolean>>(() => ({
    cat: true,
    size: true,
    price: true,
    rating: true,
    in_stock: true,
    color: Boolean(colorParam),
    shop: Boolean(shopParam) || shopFacets.length > 0,
    material: Boolean(materialParam),
    fit: Boolean(fitParam),
    occasion: Boolean(occasionParam),
    sport: Boolean(sportParam),
    feature: Boolean(featureParam),
    gender: Boolean(genderParam),
    pattern: Boolean(patternParam),
    origin: Boolean(originParam),
  }));

  const toggleGroup = (key: FilterGroupKey) => {
    setOpenGroups((prev) => ({ ...prev, [key]: !prev[key] }));
  };

  useEffect(() => {
    setOpenGroups((prev) => {
      const next = { ...prev };
      let changed = false;
      const activateIfSet: Array<[FilterGroupKey, boolean]> = [
        ["color", Boolean(colorParam)],
        ["shop", Boolean(shopParam)],
        ["rating", Boolean(minRatingX100)],
        ["in_stock", inStockOnly],
        ["material", Boolean(materialParam)],
        ["fit", Boolean(fitParam)],
        ["occasion", Boolean(occasionParam)],
        ["sport", Boolean(sportParam)],
        ["feature", Boolean(featureParam)],
        ["gender", Boolean(genderParam)],
        ["pattern", Boolean(patternParam)],
        ["origin", Boolean(originParam)],
      ];
      for (const [k, isSet] of activateIfSet) {
        if (isSet && !next[k]) {
          next[k] = true;
          changed = true;
        }
      }
      return changed ? next : prev;
    });
  }, [
    colorParam,
    shopParam,
    minRatingX100,
    inStockOnly,
    materialParam,
    fitParam,
    occasionParam,
    sportParam,
    featureParam,
    genderParam,
    patternParam,
    originParam,
  ]);

  const updateParams = useCallback(
    (updates: Record<string, string | null>) => {
      const next = new URLSearchParams(searchParams.toString());
      if (!("state" in updates)) {
        next.delete("state");
      }
      if (!("page" in updates)) {
        next.delete("page");
      }
      if ("min_rating" in updates) {
        next.delete("rating");
      }
      for (const [k, v] of Object.entries(updates)) {
        if (
          !v ||
          v === "all" ||
          (k === "sort" && v === "relevance") ||
          (k === "page" && v === "1")
        ) {
          next.delete(k);
        } else {
          next.set(k, v);
        }
      }
      const qs = next.toString();
      router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false });
    },
    [pathname, router, searchParams]
  );

  const handleSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    updateParams({ q: draftQuery.trim() || null });
  };

  const clearAllFilters = () => {
    setDraftQuery("");
    router.replace(pathname, { scroll: false });
  };

  const hasEditorialFilter = Boolean(
    genderParam ||
    originParam ||
    materialParam ||
    fitParam ||
    occasionParam ||
    sportParam ||
    featureParam ||
    patternParam
  );

  // Server already filtered/sorted by q, cat, shop, price, min_rating, in_stock, size, color, sort.
  // Only apply client-side secondary editorial filtering when an editorial query param is set.
  const displayedProducts = useMemo<CatalogProduct[]>(() => {
    if (!hasEditorialFilter) return loadedProducts;
    return loadedProducts.filter((product) => {
      if (genderParam && !matchesGenderFilter(product, genderParam)) return false;
      if (originParam) {
        const shop = shopsById.get(product.shopId);
        const provCode = shop?.provinceCode ?? product.shopProvinceCode;
        if (provCode !== originParam) return false;
      }
      if (materialParam && product.material !== materialParam) return false;
      if (fitParam && product.fitType !== fitParam) return false;
      if (occasionParam && !product.occasion?.includes(occasionParam)) return false;
      if (sportParam && !product.sport?.includes(sportParam)) return false;
      if (featureParam && !product.fabricFeatures?.includes(featureParam)) return false;
      if (patternParam && product.pattern !== patternParam) return false;
      return true;
    });
  }, [
    featureParam,
    fitParam,
    genderParam,
    hasEditorialFilter,
    loadedProducts,
    materialParam,
    occasionParam,
    originParam,
    patternParam,
    shopsById,
    sportParam,
  ]);

  // Real server category & shop facet counts
  const categoryCountBySlug = useMemo(() => {
    const byId = new Map<string, number>();
    const bySlug = new Map<string, number>();
    for (const f of categoryFacets) {
      if (f.id) byId.set(f.id, Math.max(0, Math.trunc(f.count ?? 0)));
      if (f.slug) bySlug.set(f.slug, Math.max(0, Math.trunc(f.count ?? 0)));
    }
    const out: Record<string, number> = {};
    for (const cat of categories) {
      const direct = byId.get(cat.id) ?? bySlug.get(cat.slug);
      if (direct !== undefined) {
        out[cat.slug] = direct;
        continue;
      }
      const descendants = categoryDescendantsBySlug.get(cat.slug);
      if (descendants && categoryFacets.length > 0) {
        let sum = 0;
        for (const dSlug of descendants) {
          sum += bySlug.get(dSlug) ?? 0;
        }
        out[cat.slug] = sum;
      } else {
        out[cat.slug] = 0;
      }
    }
    return out;
  }, [categories, categoryDescendantsBySlug, categoryFacets]);

  const resolvedShopSlugParam = shopParam ? (getLegacyShopSlugById(shopParam) ?? shopParam) : "";
  const activeShop = useMemo(() => {
    if (!shopParam) return undefined;
    const fromShops = shops.find((s) => s.slug === resolvedShopSlugParam || s.id === shopParam);
    if (fromShops) return fromShops;
    const fromFacet = shopFacets.find(
      (f) => f.slug === resolvedShopSlugParam || f.id === shopParam
    );
    if (fromFacet) {
      return mapGatewayShop({
        id: fromFacet.id,
        name: fromFacet.name,
        slug: fromFacet.slug || fromFacet.id,
        description: "",
        status: "ACTIVE",
      });
    }
    return undefined;
  }, [resolvedShopSlugParam, shopFacets, shopParam, shops]);

  const shopFacetEntries = useMemo(() => {
    const entries: Array<{ id: string; slug: string; name: string; count: number }> = [];
    const seen = new Set<string>();
    for (const f of shopFacets) {
      if (!f.id || seen.has(f.id)) continue;
      seen.add(f.id);
      const shop = shopsById.get(f.id);
      entries.push({
        id: f.id,
        slug: f.slug || shop?.slug || f.id,
        name: shop?.shortName || f.name || shop?.name || "Gian hàng NexusMart",
        count: Math.max(0, Math.trunc(f.count ?? 0)),
      });
    }
    if (activeShop && !seen.has(activeShop.id)) {
      entries.unshift({
        id: activeShop.id,
        slug: activeShop.slug || activeShop.id,
        name: activeShop.shortName || activeShop.name,
        count: serverTotal,
      });
    }
    return entries;
  }, [activeShop, serverTotal, shopFacets, shopsById]);

  // Accurate local editorial facet counts ONLY when all matches fit on the current page (total <= page_size)
  const localEditorialCounts = useMemo(() => {
    if (hideEditorialCounts) return null;
    const pool = loadedProducts;
    const gender: Record<GenderCode, number> = {
      nam: pool.filter((p) => matchesGenderFilter(p, "nam")).length,
      nu: pool.filter((p) => matchesGenderFilter(p, "nu")).length,
      unisex: pool.filter((p) => matchesGenderFilter(p, "unisex")).length,
    };
    const material: Record<string, number> = {};
    for (const key of Object.keys(MATERIAL_LABELS) as MaterialCode[]) {
      material[key] = pool.filter((p) => p.material === key).length;
    }
    const fit: Record<string, number> = {};
    for (const key of Object.keys(FIT_LABELS) as FitCode[]) {
      fit[key] = pool.filter((p) => p.fitType === key).length;
    }
    const occasion: Record<string, number> = {};
    for (const key of Object.keys(OCCASION_LABELS) as OccasionCode[]) {
      occasion[key] = pool.filter((p) => p.occasion?.includes(key)).length;
    }
    const sport: Record<string, number> = {};
    for (const key of Object.keys(SPORT_LABELS) as SportCode[]) {
      sport[key] = pool.filter((p) => p.sport?.includes(key)).length;
    }
    const feature: Record<string, number> = {};
    for (const key of Object.keys(FABRIC_FEATURE_LABELS) as FabricFeatureCode[]) {
      feature[key] = pool.filter((p) => p.fabricFeatures?.includes(key)).length;
    }
    const pattern: Record<string, number> = {};
    for (const key of Object.keys(PATTERN_LABELS) as PatternCode[]) {
      pattern[key] = pool.filter((p) => p.pattern === key).length;
    }
    const origin: Record<string, number> = {};
    for (const o of ORIGIN_OPTIONS) {
      origin[o.code] = pool.filter((p) => {
        const s = shopsById.get(p.shopId);
        return (s?.provinceCode ?? p.shopProvinceCode) === o.code;
      }).length;
    }
    return { gender, material, fit, occasion, sport, feature, pattern, origin };
  }, [hideEditorialCounts, loadedProducts, shopsById]);

  const handleLoadMore = async () => {
    if (loadingMore || loadedPage >= totalPages) return;
    const nextPage = loadedPage + 1;
    const seq = ++loadMoreSeqRef.current;
    setLoadingMore(true);
    setLoadMoreError(null);

    try {
      const priceRange = resolvePriceBucketRange(priceParam);
      let shopId: string | undefined;
      let shopSlug: string | undefined;
      if (shopParam) {
        if (UUID_PATTERN.test(shopParam)) {
          shopId = shopParam;
        } else {
          shopSlug = getLegacyShopSlugById(shopParam) ?? shopParam;
        }
      }

      const nextRes = await fetchSearchProductsClient({
        q: qParam || undefined,
        categorySlug: catParam || undefined,
        shopId,
        shopSlug,
        minPriceVnd: priceRange.minPriceVnd,
        maxPriceVnd: priceRange.maxPriceVnd,
        minRatingX100,
        inStockOnly,
        sortBy: sortParam,
        size: sizeParam || undefined,
        color: colorParam || undefined,
        page: nextPage,
        pageSize: safePageSize,
      });
      if (loadMoreSeqRef.current !== seq) return;

      const hits = nextRes.items;
      const pageSkuIds = collectSearchHitSkuIds(hits);
      const missingShopIds = Array.from(
        new Set(hits.filter((h) => !h.shop_name?.trim() && h.shop_id).map((h) => h.shop_id))
      );

      const nowMs = Date.now();
      const cachedFlash = flashCacheRef.current;
      const flashPromise =
        cachedFlash && nowMs - cachedFlash.fetchedAt < 30_000
          ? Promise.resolve(cachedFlash.sales)
          : api<GatewayFlashSalesResponse>("/flash-sales?include_upcoming=false", {
              retryOn401: false,
            })
              .then((fsRes) => {
                const sales = Array.isArray(fsRes?.flash_sales) ? fsRes.flash_sales : [];
                flashCacheRef.current = { fetchedAt: Date.now(), sales };
                return sales;
              })
              .catch(() => [] as GatewayFlashSale[]);

      const shopDiscPromise =
        pageSkuIds.length > 0
          ? fetchActiveShopDiscountsWithFetcher({ skuIds: pageSkuIds }, async (batch) => {
              const dqs = new URLSearchParams();
              if (batch.skuIds && batch.skuIds.length > 0) {
                dqs.set("sku_ids", batch.skuIds.join(","));
              }
              const discRes = await api<GatewayActiveShopDiscountsResponse>(
                `/shop-discounts/active?${dqs.toString()}`,
                { retryOn401: false }
              );
              return Array.isArray(discRes?.discounts) ? discRes.discounts : [];
            }).catch(() => [] as GatewayActiveShopDiscount[])
          : Promise.resolve([] as GatewayActiveShopDiscount[]);

      const extraShopsPromise =
        missingShopIds.length > 0
          ? fetchMissingShopsClient(missingShopIds).catch(() => ({}))
          : Promise.resolve({});

      const [rawFlashSales, activeDiscounts, extraShopsMap] = await Promise.all([
        flashPromise,
        shopDiscPromise,
        extraShopsPromise,
      ]);
      if (loadMoreSeqRef.current !== seq) return;

      const mergedShopsById = new Map(shopsById);
      for (const s of Object.values(extraShopsMap)) {
        if (s?.id && !mergedShopsById.has(s.id)) {
          mergedShopsById.set(s.id, mapGatewayShop(s));
        }
      }

      const liveFlashItemsBySkuId = buildLiveFlashItemsBySkuId(rawFlashSales, Date.now());
      const activeShopDiscountsBySkuId = buildActiveShopDiscountsMap(activeDiscounts);

      const mappedNext = hits.map((hit) =>
        mapSearchHitToCatalogProduct(hit, {
          categoriesById,
          shopsById: mergedShopsById,
          liveFlashItemsBySkuId,
          activeShopDiscountsBySkuId,
        })
      );

      setLoadedProducts((prev) => {
        const seen = new Set(prev.map((p) => p.id));
        const appended = [...prev];
        for (const item of mappedNext) {
          if (!seen.has(item.id)) {
            seen.add(item.id);
            appended.push(item);
          }
        }
        return appended;
      });
      setLoadedPage(nextPage);
    } catch {
      if (loadMoreSeqRef.current !== seq) return;
      setLoadMoreError("Không thể tải thêm sản phẩm lúc này. Vui lòng thử lại.");
    } finally {
      if (loadMoreSeqRef.current === seq) {
        setLoadingMore(false);
      }
    }
  };

  useRevealObserver(rootRef, `${filterSignature}|${loadedProducts.length}|${stateParam ?? ""}`);

  const activeCategory = categories.find((c) => c.slug === catParam);
  const activeColor = COLOR_OPTIONS.find((c) => c.code === colorParam);
  const activePrice = PRICE_OPTIONS.find((p) => p.id === priceParam && p.id !== "all");
  const activeRating = RATING_OPTIONS.find((r) => r.value === minRatingX100);
  const activeOrigin = ORIGIN_OPTIONS.find((o) => o.code === originParam);

  const activeFilterChips = [
    qParam ? { key: "q", label: `Từ khóa: ${qParam}` } : null,
    activeCategory ? { key: "cat", label: activeCategory.name } : null,
    sizeParam ? { key: "size", label: `Size ${sizeParam}` } : null,
    activePrice ? { key: "price", label: activePrice.label } : null,
    activeRating ? { key: "min_rating", label: activeRating.label } : null,
    inStockOnly ? { key: "in_stock", label: "Còn hàng" } : null,
    activeColor
      ? { key: "color", label: activeColor.label }
      : colorParam
        ? { key: "color", label: `Màu: ${colorParam}` }
        : null,
    activeShop ? { key: "shop", label: activeShop.shortName } : null,
    materialParam ? { key: "material", label: MATERIAL_LABELS[materialParam] } : null,
    fitParam ? { key: "fit", label: FIT_LABELS[fitParam] } : null,
    occasionParam ? { key: "occasion", label: OCCASION_LABELS[occasionParam] } : null,
    sportParam ? { key: "sport", label: SPORT_LABELS[sportParam] } : null,
    featureParam ? { key: "feature", label: FABRIC_FEATURE_LABELS[featureParam] } : null,
    genderParam ? { key: "gender", label: `Giới tính: ${GENDER_LABELS[genderParam]}` } : null,
    patternParam ? { key: "pattern", label: PATTERN_LABELS[patternParam] } : null,
    activeOrigin ? { key: "origin", label: activeOrigin.label } : null,
  ].filter(Boolean) as Array<{ key: string; label: string }>;

  const headingText =
    mode === "search"
      ? "Tìm kiếm sản phẩm"
      : activeCategory
        ? `Danh mục ${activeCategory.name}`
        : "Danh mục đồ basic";

  const subtitleText =
    mode === "search"
      ? "Tìm nhanh theo tên sản phẩm, chất liệu vải, phom dáng hoặc xưởng dệt."
      : activeCategory
        ? activeCategory.description
        : `${formatInt(serverTotal)} sản phẩm đồ cơ bản từ các xưởng dệt Việt Nam, cập nhật còn hàng theo màu và size.`;

  const shownCountSoFar = Math.min(
    serverTotal,
    (safePage - 1) * safePageSize + loadedProducts.length
  );
  const shownRatio =
    serverTotal > 0 ? Math.min(100, Math.round((shownCountSoFar / serverTotal) * 100)) : 0;
  const remainingToLoad = Math.max(0, serverTotal - shownCountSoFar);
  const hasMorePages = loadedPage < totalPages && remainingToLoad > 0;
  const pageWindow = useMemo(() => buildPageWindow(safePage, totalPages), [safePage, totalPages]);

  const isProductFallback = degradedMode === "product_fallback";
  const showEmptyState = shouldShowCatalogEmptyState({
    forcedEmpty: stateParam === "empty",
    displayedCount: displayedProducts.length,
    hasMorePages,
  });
  const countSummary = buildCatalogCountSummary({
    hasEditorialFilter,
    displayedCount: displayedProducts.length,
    loadedCount: loadedProducts.length,
    shownCountSoFar,
    serverTotal,
    page: safePage,
  });

  const renderAccordionSection = (
    idPrefix: string,
    key: FilterGroupKey,
    label: string,
    hasActiveValue: boolean,
    content: React.ReactNode
  ) => {
    const isOpen = openGroups[key];
    const panelId = `${idPrefix}-filter-${key}`;
    return (
      <div className={styles.filterGroup} key={key}>
        <button
          type="button"
          className={styles.filterSummary}
          aria-expanded={isOpen}
          aria-controls={panelId}
          onClick={() => toggleGroup(key)}
        >
          <span className={styles.summaryTitle}>
            <span>{label}</span>
            {hasActiveValue && <span className={styles.summaryActiveDot} aria-label="Đang lọc" />}
          </span>
          <span className={styles.summaryMark} aria-hidden="true">
            {isOpen ? "−" : "+"}
          </span>
        </button>
        {isOpen && (
          <div id={panelId} className={styles.filterPanel}>
            {content}
          </div>
        )}
      </div>
    );
  };

  const renderFilterGroups = (idPrefix: string) => (
    <>
      {/* 5 real server facets + size & color */}
      {renderAccordionSection(
        idPrefix,
        "cat",
        "Danh mục",
        Boolean(catParam),
        <div className={styles.optionList}>
          <button
            type="button"
            className={`${styles.optionBtn} ${!catParam ? styles.optionBtnActive : ""}`}
            onClick={() => updateParams({ cat: null })}
            aria-pressed={!catParam}
          >
            <span>Tất cả sản phẩm</span>
            <span className={styles.optionCount}>{formatInt(serverTotal)}</span>
          </button>
          {categories.map((cat) => {
            const active = catParam === cat.slug;
            const count = categoryCountBySlug[cat.slug];
            return (
              <button
                key={cat.id}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ cat: active ? null : cat.slug })}
                aria-pressed={active}
              >
                <span>{cat.name}</span>
                {categoryFacets.length > 0 && (
                  <span className={styles.optionCount}>{formatInt(count ?? 0)}</span>
                )}
              </button>
            );
          })}
        </div>
      )}

      {renderAccordionSection(
        idPrefix,
        "size",
        "Kích cỡ",
        Boolean(sizeParam),
        <div className={styles.railSizeGrid} role="group" aria-label="Lọc theo kích cỡ">
          {QUICK_SIZE_CHIPS.map((sz) => {
            const active = sizeParam.toUpperCase() === sz;
            return (
              <button
                key={sz}
                type="button"
                className={`${styles.railSizeBtn} ${active ? styles.railSizeBtnActive : ""}`}
                onClick={() => updateParams({ size: active ? null : sz })}
                aria-pressed={active}
                title={`Lọc size ${sz}`}
              >
                {sz}
              </button>
            );
          })}
        </div>
      )}

      {renderAccordionSection(
        idPrefix,
        "price",
        "Mức giá",
        priceParam !== "all",
        <div className={styles.optionList}>
          {PRICE_OPTIONS.map((opt) => {
            const active = priceParam === opt.id;
            return (
              <button
                key={opt.id}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ price: opt.id === "all" ? null : opt.id })}
                aria-pressed={active}
              >
                <span>{opt.label}</span>
              </button>
            );
          })}
        </div>
      )}

      {renderAccordionSection(
        idPrefix,
        "rating",
        "Đánh giá",
        Boolean(minRatingX100),
        <div className={styles.optionList}>
          <button
            type="button"
            className={`${styles.optionBtn} ${!minRatingX100 ? styles.optionBtnActive : ""}`}
            onClick={() => updateParams({ min_rating: null })}
            aria-pressed={!minRatingX100}
          >
            <span>Tất cả đánh giá</span>
          </button>
          {RATING_OPTIONS.map((opt) => {
            const active = minRatingX100 === opt.value;
            return (
              <button
                key={opt.value}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ min_rating: active ? null : String(opt.value) })}
                aria-pressed={active}
              >
                <span>{opt.label}</span>
              </button>
            );
          })}
        </div>
      )}

      {renderAccordionSection(
        idPrefix,
        "in_stock",
        "Tình trạng hàng",
        inStockOnly,
        <div className={styles.optionList}>
          <button
            type="button"
            className={`${styles.optionBtn} ${inStockOnly ? styles.optionBtnActive : ""}`}
            onClick={() => updateParams({ in_stock: inStockOnly ? null : "1" })}
            aria-pressed={inStockOnly}
          >
            <span>Chỉ hiện sản phẩm còn hàng</span>
          </button>
        </div>
      )}

      {renderAccordionSection(
        idPrefix,
        "color",
        "Màu sắc",
        Boolean(colorParam),
        <div className={styles.optionList}>
          {COLOR_OPTIONS.map((cg) => {
            const active = colorParam === cg.code;
            return (
              <button
                key={cg.code}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ color: active ? null : cg.code })}
                aria-pressed={active}
              >
                <span className={styles.optionLeft}>
                  <span
                    className={styles.colorDot}
                    style={{ backgroundColor: cg.swatch }}
                    aria-hidden="true"
                  />
                  <span>{cg.label}</span>
                </span>
              </button>
            );
          })}
        </div>
      )}

      {renderAccordionSection(
        idPrefix,
        "shop",
        "Gian hàng",
        Boolean(shopParam),
        <div className={styles.optionList}>
          <button
            type="button"
            className={`${styles.optionBtn} ${!shopParam ? styles.optionBtnActive : ""}`}
            onClick={() => updateParams({ shop: null })}
            aria-pressed={!shopParam}
          >
            <span>Tất cả xưởng</span>
            <span className={styles.optionCount}>{formatInt(serverTotal)}</span>
          </button>
          {shopFacetEntries.map((shop) => {
            const active =
              activeShop?.id === shop.id ||
              shopParam === shop.id ||
              resolvedShopSlugParam === shop.slug;
            return (
              <button
                key={shop.id}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ shop: active ? null : shop.slug })}
                aria-pressed={active}
              >
                <span>{shop.name}</span>
                <span className={styles.optionCount}>{formatInt(shop.count)}</span>
              </button>
            );
          })}
        </div>
      )}

      {/* Secondary editorial facets (counts hidden when total > page_size per proposal 3 b1 §4) */}
      {renderAccordionSection(
        idPrefix,
        "material",
        "Chất liệu",
        Boolean(materialParam),
        <div className={styles.optionList}>
          {(Object.keys(MATERIAL_LABELS) as MaterialCode[]).map((key) => {
            const active = materialParam === key;
            const count = localEditorialCounts?.material[key];
            if (!hideEditorialCounts && count === 0 && !active) return null;
            return (
              <button
                key={key}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ material: active ? null : key })}
                aria-pressed={active}
              >
                <span>{MATERIAL_LABELS[key]}</span>
                {!hideEditorialCounts && count !== undefined && (
                  <span className={styles.optionCount}>{count}</span>
                )}
              </button>
            );
          })}
        </div>
      )}

      {renderAccordionSection(
        idPrefix,
        "fit",
        "Phom dáng",
        Boolean(fitParam),
        <div className={styles.optionList}>
          {(Object.keys(FIT_LABELS) as FitCode[]).map((key) => {
            const active = fitParam === key;
            const count = localEditorialCounts?.fit[key];
            return (
              <button
                key={key}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ fit: active ? null : key })}
                aria-pressed={active}
              >
                <span>{FIT_LABELS[key]}</span>
                {!hideEditorialCounts && count !== undefined && (
                  <span className={styles.optionCount}>{count}</span>
                )}
              </button>
            );
          })}
        </div>
      )}

      {renderAccordionSection(
        idPrefix,
        "occasion",
        "Hoàn cảnh",
        Boolean(occasionParam),
        <div className={styles.optionList}>
          {(Object.keys(OCCASION_LABELS) as OccasionCode[]).map((key) => {
            const active = occasionParam === key;
            const count = localEditorialCounts?.occasion[key];
            return (
              <button
                key={key}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ occasion: active ? null : key })}
                aria-pressed={active}
              >
                <span>{OCCASION_LABELS[key]}</span>
                {!hideEditorialCounts && count !== undefined && (
                  <span className={styles.optionCount}>{count}</span>
                )}
              </button>
            );
          })}
        </div>
      )}

      {renderAccordionSection(
        idPrefix,
        "sport",
        "Thể thao",
        Boolean(sportParam),
        <div className={styles.optionList}>
          {(Object.keys(SPORT_LABELS) as SportCode[]).map((key) => {
            const active = sportParam === key;
            const count = localEditorialCounts?.sport[key];
            return (
              <button
                key={key}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ sport: active ? null : key })}
                aria-pressed={active}
              >
                <span>{SPORT_LABELS[key]}</span>
                {!hideEditorialCounts && count !== undefined && (
                  <span className={styles.optionCount}>{count}</span>
                )}
              </button>
            );
          })}
        </div>
      )}

      {renderAccordionSection(
        idPrefix,
        "feature",
        "Tính năng vải",
        Boolean(featureParam),
        <div className={styles.optionList}>
          {(Object.keys(FABRIC_FEATURE_LABELS) as FabricFeatureCode[]).map((key) => {
            const active = featureParam === key;
            const count = localEditorialCounts?.feature[key];
            return (
              <button
                key={key}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ feature: active ? null : key })}
                aria-pressed={active}
              >
                <span>{FABRIC_FEATURE_LABELS[key]}</span>
                {!hideEditorialCounts && count !== undefined && (
                  <span className={styles.optionCount}>{count}</span>
                )}
              </button>
            );
          })}
        </div>
      )}

      {renderAccordionSection(
        idPrefix,
        "gender",
        "Giới tính",
        Boolean(genderParam),
        <div className={styles.optionList}>
          {(["nam", "nu", "unisex"] as GenderCode[]).map((g) => {
            const active = genderParam === g;
            const count = localEditorialCounts?.gender[g];
            return (
              <button
                key={g}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ gender: active ? null : g })}
                aria-pressed={active}
              >
                <span>{GENDER_LABELS[g]}</span>
                {!hideEditorialCounts && count !== undefined && (
                  <span className={styles.optionCount}>{count}</span>
                )}
              </button>
            );
          })}
        </div>
      )}

      {renderAccordionSection(
        idPrefix,
        "pattern",
        "Họa tiết",
        Boolean(patternParam),
        <div className={styles.optionList}>
          {(Object.keys(PATTERN_LABELS) as PatternCode[]).map((key) => {
            const active = patternParam === key;
            const count = localEditorialCounts?.pattern[key];
            return (
              <button
                key={key}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ pattern: active ? null : key })}
                aria-pressed={active}
              >
                <span>{PATTERN_LABELS[key]}</span>
                {!hideEditorialCounts && count !== undefined && (
                  <span className={styles.optionCount}>{count}</span>
                )}
              </button>
            );
          })}
        </div>
      )}

      {renderAccordionSection(
        idPrefix,
        "origin",
        "Kho giao hàng",
        Boolean(originParam),
        <div className={styles.optionList}>
          {ORIGIN_OPTIONS.map((opt) => {
            const active = originParam === opt.code;
            const count = localEditorialCounts?.origin[opt.code];
            return (
              <button
                key={opt.code}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ origin: active ? null : opt.code })}
                aria-pressed={active}
              >
                <span>{opt.label}</span>
                {!hideEditorialCounts && count !== undefined && (
                  <span className={styles.optionCount}>{count}</span>
                )}
              </button>
            );
          })}
        </div>
      )}
    </>
  );

  return (
    <div
      ref={rootRef}
      className={styles.page}
      data-degraded-mode={degradedMode || ""}
      data-server-total={serverTotal}
    >
      <div className={styles.mainWrap}>
        <section className={styles.headerGrid} aria-label="Tìm kiếm và lọc danh mục">
          <div className={styles.titleBlock}>
            <h1 className={`${styles.title} lines`}>
              <span className="line">
                <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                  {headingText}
                </span>
              </span>
            </h1>
            <p className={styles.subtitle} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
              {subtitleText}
            </p>
          </div>

          <div
            className={styles.searchBlock}
            data-rise=""
            style={{ "--i": 2 } as React.CSSProperties}
          >
            <form className={styles.searchForm} onSubmit={handleSearchSubmit} role="search">
              <div className={styles.searchInputWrap}>
                <IconSearch size={16} className={styles.searchIcon} />
                <input
                  type="search"
                  className={styles.searchInput}
                  value={draftQuery}
                  onChange={(e) => setDraftQuery(e.target.value)}
                  placeholder="Tìm áo thun, quần short, polo pique, tất..."
                  aria-label="Tìm kiếm sản phẩm"
                />
              </div>
              <Button type="submit" variant="primary" size="md">
                Tìm kiếm
              </Button>
            </form>

            <div className={styles.searchMetaRow}>
              <span className={styles.countText}>{countSummary.metaText}</span>
              <div className={styles.desktopControls}>
                {!isProductFallback && (
                  <button
                    type="button"
                    className={styles.sidebarToggleBtn}
                    onClick={() => setSidebarOpen((v) => !v)}
                    aria-expanded={sidebarOpen}
                  >
                    <IconSliders size={15} />
                    <span>{sidebarOpen ? "Ẩn bộ lọc" : "Hiện bộ lọc"}</span>
                  </button>
                )}
                <div className={styles.sortWrap}>
                  <label htmlFor="catalog-sort-desktop" className={styles.sortLabel}>
                    Sắp xếp
                  </label>
                  <select
                    id="catalog-sort-desktop"
                    className={styles.sortSelect}
                    value={sortParam}
                    onChange={(e) => updateParams({ sort: e.target.value })}
                  >
                    <option value="relevance">Phù hợp nhất</option>
                    <option value="newest">Mới nhất</option>
                    <option value="price_asc">Giá tăng dần</option>
                    <option value="price_desc">Giá giảm dần</option>
                    <option value="rating_desc">Đánh giá cao nhất</option>
                  </select>
                </div>
              </div>
            </div>
          </div>
        </section>

        {mode === "search" && trendingKeywords.length > 0 && (
          <div className={styles.trendingStrip} aria-label="Từ khóa xu hướng">
            <span className={styles.trendingLabel}>Tìm nhiều:</span>
            <div className={styles.trendingList}>
              {trendingKeywords.slice(0, 8).map((kw) => {
                const active = qParam.trim().toLowerCase() === kw.trim().toLowerCase();
                return (
                  <button
                    key={kw}
                    type="button"
                    className={`${styles.trendingBtn} ${active ? styles.trendingBtnActive : ""}`}
                    onClick={() => {
                      setDraftQuery(kw);
                      updateParams({ q: kw });
                    }}
                  >
                    <span>{kw}</span>
                  </button>
                );
              })}
            </div>
          </div>
        )}

        {!isProductFallback && (
          <nav className={styles.catStrip} aria-label="Nhóm danh mục sản phẩm">
            <button
              type="button"
              className={`${styles.catBtn} ${!catParam ? styles.catBtnActive : ""}`}
              onClick={() => updateParams({ cat: null })}
              aria-pressed={!catParam}
            >
              <span>Tất cả</span>
              <span className={styles.catCount}>{formatInt(serverTotal)}</span>
            </button>
            {categories.map((cat) => {
              const count = categoryCountBySlug[cat.slug];
              const active = catParam === cat.slug;
              return (
                <button
                  key={cat.id}
                  type="button"
                  className={`${styles.catBtn} ${active ? styles.catBtnActive : ""}`}
                  onClick={() => updateParams({ cat: active ? null : cat.slug })}
                  aria-pressed={active}
                >
                  <span>{cat.name}</span>
                  {categoryFacets.length > 0 && (
                    <span className={styles.catCount}>{formatInt(count ?? 0)}</span>
                  )}
                </button>
              );
            })}
          </nav>
        )}

        {!isProductFallback &&
          activeCategory &&
          activeCategory.subCategories &&
          activeCategory.subCategories.length > 0 && (
            <div className={styles.subCatBanner} aria-label="Phân nhóm chất liệu trong danh mục">
              {activeCategory.benefitSummary && (
                <p className={styles.subCatSummary}>{activeCategory.benefitSummary}</p>
              )}
              <div className={styles.subCatGrid}>
                {activeCategory.subCategories.map((sub) => {
                  const filterPatch = SUBCATEGORY_FILTER_MAP[sub.slug];
                  return (
                    <button
                      key={sub.slug}
                      type="button"
                      className={styles.subCatCard}
                      onClick={() => {
                        if (filterPatch) updateParams(filterPatch);
                      }}
                    >
                      <span className={styles.subCatName}>{sub.name}</span>
                      <span className={styles.subCatBenefit}>{sub.benefit}</span>
                    </button>
                  );
                })}
              </div>
            </div>
          )}

        {activeFilterChips.length > 0 && (
          <div className={styles.activeBar} aria-label="Bộ lọc đang áp dụng">
            {activeFilterChips.map((chip) => (
              <button
                key={chip.key}
                type="button"
                className={styles.chip}
                onClick={() => {
                  if (chip.key === "q") setDraftQuery("");
                  updateParams({ [chip.key]: null });
                }}
                aria-label={`Bỏ lọc ${chip.label}`}
              >
                <span>{chip.label}</span>
                <IconClose size={13} />
              </button>
            ))}
            <button type="button" className={styles.clearAllBtn} onClick={clearAllFilters}>
              Xóa toàn bộ bộ lọc
            </button>
          </div>
        )}

        <div className={styles.mobileToolbar}>
          {!isProductFallback && (
            <button
              type="button"
              className={styles.mobileFilterBtn}
              onClick={() => setSheetOpen(true)}
            >
              <IconSliders size={16} />
              <span>
                Bộ lọc{activeFilterChips.length > 0 ? ` (${activeFilterChips.length})` : ""}
              </span>
            </button>
          )}

          <select
            className={styles.sortSelect}
            value={sortParam}
            onChange={(e) => updateParams({ sort: e.target.value })}
            aria-label="Sắp xếp sản phẩm"
          >
            <option value="relevance">Phù hợp nhất</option>
            <option value="newest">Mới nhất</option>
            <option value="price_asc">Giá tăng dần</option>
            <option value="price_desc">Giá giảm dần</option>
            <option value="rating_desc">Đánh giá cao nhất</option>
          </select>
        </div>

        <section
          className={`${styles.bodyGrid} ${!sidebarOpen || isProductFallback ? styles.bodyGridWide : ""}`}
          aria-labelledby="catalog-results-heading"
        >
          {!isProductFallback && sidebarOpen && (
            <aside className={styles.rail} aria-label="Bộ lọc sản phẩm">
              <div className={styles.railHead}>
                <span className={styles.railTitle}>Bộ lọc</span>
                {activeFilterChips.length > 0 && (
                  <button type="button" className={styles.railClearBtn} onClick={clearAllFilters}>
                    Đặt lại
                  </button>
                )}
              </div>
              {renderFilterGroups("desktop")}
            </aside>
          )}

          <div className={styles.resultsCol}>
            <div
              className={styles.resultsHead}
              data-cat-reveal=""
              style={{ "--stagger": 0 } as React.CSSProperties}
            >
              <h2 id="catalog-results-heading" className={styles.resultsTitle}>
                {qParam ? `Kết quả cho "${qParam}"` : "Sản phẩm xưởng dệt"}
              </h2>
              <span className={styles.resultsSub}>{countSummary.resultsSub}</span>
            </div>

            {didYouMean && displayedProducts.length > 0 && (
              <div className={styles.spellingRow}>
                <span className={styles.spellingPrompt}>Có phải bạn muốn tìm:</span>
                <button
                  type="button"
                  className={styles.spellingBtn}
                  onClick={() => {
                    setDraftQuery(didYouMean);
                    updateParams({ q: didYouMean });
                  }}
                >
                  {didYouMean}
                </button>
              </div>
            )}

            {stateParam === "loading" ? (
              <StateView kind="loading" title="Đang tải danh mục sản phẩm..." rows={6} />
            ) : stateParam === "error" || (error && !isProductFallback) ? (
              <StateView
                kind="error"
                title="Không thể tải danh mục lúc này"
                message={
                  error ?? "Kết nối danh mục tạm thời gián đoạn. Vui lòng thử tải lại danh sách."
                }
                actionLabel="Tải lại danh mục"
                onAction={() => {
                  if (stateParam === "error") {
                    updateParams({ state: null });
                  } else {
                    router.refresh();
                  }
                }}
              />
            ) : showEmptyState ? (
              <div className={styles.emptyBox}>
                <h3 className={styles.emptyHeading}>
                  Chưa tìm thấy sản phẩm khớp với bộ lọc hiện tại
                </h3>
                <p className={styles.emptyMessage}>
                  {qParam
                    ? `Không có mẫu nào khớp trực tiếp với "${qParam}" trong phạm vi lọc đang chọn.`
                    : "Hãy thử nới lỏng mức giá, đổi kích cỡ hoặc xóa bớt bộ lọc để xem thêm mẫu."}
                </p>

                {didYouMean && (
                  <div className={styles.spellingRow}>
                    <span className={styles.spellingPrompt}>Có phải bạn muốn tìm:</span>
                    <button
                      type="button"
                      className={styles.spellingBtn}
                      onClick={() => {
                        setDraftQuery(didYouMean);
                        updateParams({ q: didYouMean });
                      }}
                    >
                      {didYouMean}
                    </button>
                  </div>
                )}

                <div className={styles.emptyActions}>
                  {safePage > 1 && (
                    <Button
                      variant="secondary"
                      size="md"
                      onClick={() => updateParams({ page: null })}
                    >
                      Quay về trang 1
                    </Button>
                  )}
                  {shopParam && (
                    <Button
                      variant="primary"
                      size="md"
                      onClick={() => updateParams({ shop: null })}
                    >
                      Xem kết quả trên toàn NexusMart
                    </Button>
                  )}
                  <Button
                    variant={shopParam ? "secondary" : "primary"}
                    size="md"
                    onClick={clearAllFilters}
                  >
                    Xóa toàn bộ bộ lọc
                  </Button>
                </div>

                {trendingKeywords.length > 0 && (
                  <div className={styles.emptyTrending}>
                    <span className={styles.trendingLabel}>Gợi ý từ khóa phổ biến:</span>
                    <div className={styles.trendingList}>
                      {trendingKeywords.slice(0, 6).map((kw) => (
                        <button
                          key={kw}
                          type="button"
                          className={styles.trendingBtn}
                          onClick={() => {
                            setDraftQuery(kw);
                            router.replace(`${pathname}?q=${encodeURIComponent(kw)}`, {
                              scroll: false,
                            });
                          }}
                        >
                          {kw}
                        </button>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            ) : (
              <>
                {displayedProducts.length === 0 ? (
                  <div className={styles.emptyBox}>
                    <p className={styles.emptyMessage}>
                      Chưa có mẫu khớp trong {formatInt(loadedProducts.length)} sản phẩm đã xem. Bấm
                      Xem thêm hoặc chuyển trang bên dưới để tiếp tục lọc.
                    </p>
                  </div>
                ) : (
                  <div className={styles.productGrid}>
                    {displayedProducts.map((product, idx) => (
                      <div
                        key={product.id}
                        data-cat-reveal=""
                        style={{ "--stagger": idx % 4 } as React.CSSProperties}
                      >
                        <ProductCard
                          product={product}
                          showSpec={true}
                          showSla={true}
                          showBadges={true}
                          showWishlist={true}
                          showRating={true}
                          enableQuickAdd={true}
                          enableInteractiveSwatches={true}
                          priority={idx < 4}
                          quotaLabel={sizeParam ? `Có size ${sizeParam}` : undefined}
                        />
                      </div>
                    ))}
                  </div>
                )}

                <div
                  className={styles.loadMoreBar}
                  data-cat-reveal=""
                  style={{ "--stagger": 1 } as React.CSSProperties}
                >
                  <p className={styles.loadMoreMeta}>{countSummary.loadMoreMeta}</p>
                  <div
                    className={styles.loadMoreTrack}
                    role="progressbar"
                    aria-valuemin={0}
                    aria-valuemax={serverTotal}
                    aria-valuenow={shownCountSoFar}
                    aria-label="Tiến độ hiển thị sản phẩm"
                  >
                    <div className={styles.loadMoreFill} style={{ width: `${shownRatio}%` }} />
                  </div>
                  {loadMoreError && (
                    <p className={styles.resultsSub} role="alert">
                      {loadMoreError}
                    </p>
                  )}
                  {hasMorePages && (
                    <Button
                      variant="ink"
                      size="md"
                      disabled={loadingMore}
                      onClick={() => void handleLoadMore()}
                    >
                      {loadingMore
                        ? "Đang tải thêm sản phẩm..."
                        : `Xem thêm ${formatInt(Math.min(safePageSize, remainingToLoad))} sản phẩm`}
                    </Button>
                  )}
                  {totalPages > 1 && (
                    <nav
                      aria-label="Phân trang danh sách sản phẩm"
                      className={styles.paginationNav}
                    >
                      <button
                        type="button"
                        className={styles.trendingBtn}
                        disabled={safePage <= 1}
                        onClick={() =>
                          updateParams({ page: safePage - 1 <= 1 ? null : String(safePage - 1) })
                        }
                      >
                        Trang trước
                      </button>
                      {pageWindow.map((p, idx) => {
                        const prev = idx > 0 ? pageWindow[idx - 1] : null;
                        const needsGap = prev !== null && p - prev > 1;
                        return (
                          <React.Fragment key={p}>
                            {needsGap && (
                              <span className={styles.resultsSub} aria-hidden="true">
                                …
                              </span>
                            )}
                            <button
                              type="button"
                              className={`${styles.trendingBtn} ${
                                p === safePage ? styles.trendingBtnActive : ""
                              }`}
                              aria-current={p === safePage ? "page" : undefined}
                              onClick={() => updateParams({ page: p <= 1 ? null : String(p) })}
                            >
                              {formatInt(p)}
                            </button>
                          </React.Fragment>
                        );
                      })}
                      <button
                        type="button"
                        className={styles.trendingBtn}
                        disabled={safePage >= totalPages}
                        onClick={() => updateParams({ page: String(safePage + 1) })}
                      >
                        Trang sau
                      </button>
                    </nav>
                  )}
                </div>
              </>
            )}
          </div>
        </section>
      </div>

      {/* Unboxed tonal care guide section at bottom */}
      <section className={styles.careSection} aria-labelledby="catalog-care-heading">
        <div className={styles.careHead}>
          <h2 id="catalog-care-heading" className={styles.careTitle}>
            Hướng dẫn bảo quản theo chất liệu
          </h2>
          <Link
            href="/tap-chi/meo-giat-ui-giu-co-ao-cotton-va-soi-linen-ben-dep"
            className={styles.careLink}
          >
            Đọc chi tiết trên Sổ tay vải
          </Link>
        </div>

        <div className={styles.careGrid}>
          {CARE_GUIDE_LIBRARY.map((entry, idx) => (
            <div
              key={entry.id}
              className={styles.careColumn}
              data-cat-reveal=""
              style={{ "--stagger": idx } as React.CSSProperties}
            >
              <h3 className={styles.careGroupName}>{entry.fabricGroup}</h3>
              <dl className={styles.careList}>
                <div className={styles.careRow}>
                  <dt className={styles.careKey}>Giặt máy</dt>
                  <dd className={styles.careVal}>{entry.tempRule}</dd>
                </div>
                <div className={styles.careRow}>
                  <dt className={styles.careKey}>Phơi khô</dt>
                  <dd className={styles.careVal}>{entry.dryingRule}</dd>
                </div>
                <div className={styles.careRow}>
                  <dt className={styles.careKey}>Cần tránh</dt>
                  <dd className={styles.careVal}>{entry.avoidRule}</dd>
                </div>
              </dl>
              <p className={styles.careTip}>{entry.tip}</p>
            </div>
          ))}
        </div>
      </section>

      {sheetOpen && !isProductFallback && (
        <div
          className={styles.sheetOverlay}
          role="dialog"
          aria-modal="true"
          aria-label="Bộ lọc sản phẩm"
        >
          <div className={styles.sheetHeader}>
            <p className={styles.sheetTitle}>Bộ lọc sản phẩm ({activeFilterChips.length})</p>
            <button
              type="button"
              className={styles.sheetClose}
              onClick={() => setSheetOpen(false)}
              aria-label="Đóng bộ lọc"
            >
              <IconClose size={20} />
            </button>
          </div>

          <div className={styles.sheetBody}>{renderFilterGroups("mobile")}</div>

          <div className={styles.sheetFooter}>
            <Button
              variant="secondary"
              size="md"
              onClick={() => {
                clearAllFilters();
                setSheetOpen(false);
              }}
            >
              Đặt lại
            </Button>
            <Button variant="primary" size="md" onClick={() => setSheetOpen(false)}>
              {countSummary.mobileCta}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
