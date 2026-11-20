"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import {
  CARE_GUIDE_LIBRARY,
  FABRIC_FEATURE_LABELS,
  FIT_LABELS,
  GENDER_LABELS,
  MATERIAL_LABELS,
  OCCASION_LABELS,
  PATTERN_LABELS,
  SPORT_LABELS,
  TRENDING_KEYWORDS,
  matchesGenderFilter,
  type CategoryInfo,
  type CategorySlug,
  type FabricFeatureCode,
  type FitCode,
  type GenderCode,
  type MaterialCode,
  type OccasionCode,
  type PatternCode,
  type Product,
  type Shop,
  type SizeCode,
  type SportCode,
} from "@/data";
import { searchProducts, suggestSpelling } from "@/lib/search";
import { Button, IconClose, IconSearch, IconSliders, ProductCard, StateView } from "@/components";
import { useRevealObserver } from "./useRevealObserver";
import styles from "./CatalogExplorer.module.css";

export interface CatalogExplorerProps {
  mode: "catalog" | "search";
  products: Product[];
  categories: CategoryInfo[];
  shops: Shop[];
  loadError?: string | null;
}

type SortKey = "bestseller" | "newest" | "price-asc" | "price-desc";
type PriceBucket =
  "all" | "under-180" | "180-250" | "250-350" | "over-350" | "under-200" | "200-300" | "over-300";

type FilterGroupKey =
  | "cat"
  | "size"
  | "price"
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

// 12 products initially: fills 4-col (3 rows), 3-col (4 rows), and 2-col (6 rows) with zero orphans.
const PAGE_STEP = 12;

// Quick size chips XS -> 4XL (syncs with URL; out-of-stock sizes dim cleanly)
const QUICK_SIZE_CHIPS = ["XS", "S", "M", "L", "XL", "2XL", "3XL", "4XL"] as const;
type QuickSizeChip = (typeof QUICK_SIZE_CHIPS)[number];

interface ColorGroup {
  id: string;
  label: string;
  swatch: string;
  codes: string[];
}

const COLOR_GROUPS: ColorGroup[] = [
  {
    id: "trang-kem",
    label: "Trắng & Kem",
    swatch: "#f0efe9",
    codes: ["trang", "kem", "combo-trangxam"],
  },
  {
    id: "xam",
    label: "Xám & Xám đá",
    swatch: "#9aa5ac",
    codes: ["xamda", "xamdam", "xam", "xamtro", "combo-5mau"],
  },
  {
    id: "den-than",
    label: "Đen & Than chì",
    swatch: "#273036",
    codes: ["den", "thanchi", "combo-trungtinh"],
  },
  {
    id: "xanh",
    label: "Xanh than & Xanh đá",
    swatch: "#546f7d",
    codes: ["xanhthan", "xanhda", "combo-xanhxam"],
  },
  { id: "covit", label: "Xanh cổ vịt", swatch: "#0d7480", codes: ["covit"] },
  { id: "be", label: "Be cát", swatch: "#d6cec1", codes: ["be"] },
];

const PRICE_OPTIONS: Array<{ id: PriceBucket; label: string }> = [
  { id: "all", label: "Tất cả mức giá" },
  { id: "under-180", label: "Dưới 180.000₫" },
  { id: "180-250", label: "180.000₫ – 250.000₫" },
  { id: "250-350", label: "250.000₫ – 350.000₫" },
  { id: "over-350", label: "Trên 350.000₫" },
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

interface ActiveFilters {
  q: string;
  gender: GenderCode | "";
  cat: CategorySlug | "";
  shop: string;
  origin: string;
  size: string;
  color: string;
  price: PriceBucket;
  material: MaterialCode | "";
  fit: FitCode | "";
  occasion: OccasionCode | "";
  sport: SportCode | "";
  feature: FabricFeatureCode | "";
  pattern: PatternCode | "";
}

function matchPriceBucket(price: number, bucket: PriceBucket): boolean {
  if (bucket === "under-180") return price < 180000;
  if (bucket === "180-250") return price >= 180000 && price <= 250000;
  if (bucket === "250-350") return price > 250000 && price <= 350000;
  if (bucket === "over-350") return price > 350000;
  // Backward-compatible legacy URL buckets
  if (bucket === "under-200") return price < 200000;
  if (bucket === "200-300") return price >= 200000 && price <= 300000;
  if (bucket === "over-300") return price > 300000;
  return true;
}

function applyStructuredFilters(
  basePool: Product[],
  filters: ActiveFilters,
  shopById: Map<string, Shop>,
  omitKey?: keyof ActiveFilters
): Product[] {
  const activeColorGroup =
    omitKey === "color" ? undefined : COLOR_GROUPS.find((g) => g.id === filters.color);

  return basePool.filter((product) => {
    if (omitKey !== "gender" && filters.gender) {
      if (!matchesGenderFilter(product, filters.gender)) return false;
    }
    if (omitKey !== "cat" && filters.cat && product.categorySlug !== filters.cat) {
      return false;
    }
    if (omitKey !== "shop" && filters.shop && product.shopId !== filters.shop) {
      return false;
    }
    if (omitKey !== "origin" && filters.origin) {
      const shop = shopById.get(product.shopId);
      if (shop?.provinceCode !== filters.origin) return false;
    }
    if (omitKey !== "size" && filters.size) {
      const hasStock = product.skus.some(
        (sku) => sku.size === (filters.size as SizeCode) && sku.stock > 0
      );
      if (!hasStock) return false;
    }
    if (activeColorGroup) {
      const hasColor = product.colors.some((c) => activeColorGroup.codes.includes(c.code));
      if (!hasColor) return false;
    }
    if (omitKey !== "price" && !matchPriceBucket(product.price, filters.price)) {
      return false;
    }
    if (omitKey !== "material" && filters.material && product.material !== filters.material) {
      return false;
    }
    if (omitKey !== "fit" && filters.fit && product.fitType !== filters.fit) {
      return false;
    }
    if (omitKey !== "occasion" && filters.occasion) {
      if (!product.occasion?.includes(filters.occasion)) return false;
    }
    if (omitKey !== "sport" && filters.sport) {
      if (!product.sport?.includes(filters.sport)) return false;
    }
    if (omitKey !== "feature" && filters.feature) {
      if (!product.fabricFeatures?.includes(filters.feature)) return false;
    }
    if (omitKey !== "pattern" && filters.pattern && product.pattern !== filters.pattern) {
      return false;
    }
    return true;
  });
}

export function CatalogExplorer({
  mode,
  products,
  categories,
  shops,
  loadError,
}: CatalogExplorerProps) {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const rootRef = useRef<HTMLDivElement>(null);

  const shopById = useMemo(() => new Map(shops.map((s) => [s.id, s])), [shops]);

  const qParam = searchParams.get("q") ?? "";
  const genderParam = (searchParams.get("gender") ?? "") as GenderCode | "";
  const catParam = (searchParams.get("cat") ?? "") as CategorySlug | "";
  const shopParam = searchParams.get("shop") ?? "";
  const originParam = searchParams.get("origin") ?? "";
  const sizeParam = searchParams.get("size") ?? "";
  const colorParam = searchParams.get("color") ?? "";
  const priceParam = (searchParams.get("price") ?? "all") as PriceBucket;
  const materialParam = (searchParams.get("material") ?? "") as MaterialCode | "";
  const fitParam = (searchParams.get("fit") ?? "") as FitCode | "";
  const occasionParam = (searchParams.get("occasion") ?? "") as OccasionCode | "";
  const sportParam = (searchParams.get("sport") ?? "") as SportCode | "";
  const featureParam = (searchParams.get("feature") ?? "") as FabricFeatureCode | "";
  const patternParam = (searchParams.get("pattern") ?? "") as PatternCode | "";
  const sortParam = (searchParams.get("sort") ?? "bestseller") as SortKey;
  const stateParam = searchParams.get("state");

  const [draftQuery, setDraftQuery] = useState(qParam);
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [sheetOpen, setSheetOpen] = useState(false);
  const [visibleCount, setVisibleCount] = useState(PAGE_STEP);

  // Primary groups open by default (Danh mục, Kích cỡ, Mức giá);
  // secondary groups collapsed by default unless active in URL.
  const [openGroups, setOpenGroups] = useState<Record<FilterGroupKey, boolean>>(() => ({
    cat: true,
    size: true,
    price: true,
    color: Boolean(colorParam),
    shop: Boolean(shopParam),
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
    materialParam,
    fitParam,
    occasionParam,
    sportParam,
    featureParam,
    genderParam,
    patternParam,
    originParam,
  ]);

  const filters = useMemo<ActiveFilters>(
    () => ({
      q: qParam,
      gender: genderParam,
      cat: catParam,
      shop: shopParam,
      origin: originParam,
      size: sizeParam,
      color: colorParam,
      price: priceParam,
      material: materialParam,
      fit: fitParam,
      occasion: occasionParam,
      sport: sportParam,
      feature: featureParam,
      pattern: patternParam,
    }),
    [
      qParam,
      genderParam,
      catParam,
      shopParam,
      originParam,
      sizeParam,
      colorParam,
      priceParam,
      materialParam,
      fitParam,
      occasionParam,
      sportParam,
      featureParam,
      patternParam,
    ]
  );

  const filterSignature = `${qParam}|${genderParam}|${catParam}|${shopParam}|${originParam}|${sizeParam}|${colorParam}|${priceParam}|${materialParam}|${fitParam}|${occasionParam}|${sportParam}|${featureParam}|${patternParam}|${sortParam}`;

  useEffect(() => {
    setDraftQuery(qParam);
  }, [qParam]);

  useEffect(() => {
    setVisibleCount(PAGE_STEP);
  }, [filterSignature]);

  const updateParams = useCallback(
    (updates: Record<string, string | null>) => {
      const next = new URLSearchParams(searchParams.toString());
      if (!("state" in updates)) {
        next.delete("state");
      }
      for (const [k, v] of Object.entries(updates)) {
        if (!v || v === "all" || (k === "sort" && v === "bestseller")) {
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

  // Query search pool (supports non-diacritic input & Vietnamese apparel synonyms)
  const searchPool = useMemo(() => {
    const trimmed = qParam.trim();
    if (!trimmed) return products;
    return searchProducts(trimmed, products);
  }, [products, qParam]);

  const filteredProducts = useMemo<Product[]>(() => {
    const list = applyStructuredFilters(searchPool, filters, shopById);
    const sorted = [...list];
    if (sortParam === "price-asc") {
      sorted.sort((a, b) => a.price - b.price);
    } else if (sortParam === "price-desc") {
      sorted.sort((a, b) => b.price - a.price);
    } else if (sortParam === "newest") {
      sorted.sort((a, b) => {
        const aTime = a.createdAt ? Date.parse(a.createdAt) : 0;
        const bTime = b.createdAt ? Date.parse(b.createdAt) : 0;
        return bTime - aTime || Number(Boolean(b.isNew)) - Number(Boolean(a.isNew));
      });
    } else {
      sorted.sort((a, b) => (a.salesRank ?? 99) - (b.salesRank ?? 99));
    }
    return sorted;
  }, [filters, searchPool, shopById, sortParam]);

  const visibleProducts = useMemo(
    () => filteredProducts.slice(0, visibleCount),
    [filteredProducts, visibleCount]
  );

  // Live faceted counts for each filter group
  const facets = useMemo(() => {
    const poolGender = applyStructuredFilters(searchPool, filters, shopById, "gender");
    const poolCat = applyStructuredFilters(searchPool, filters, shopById, "cat");
    const poolSize = applyStructuredFilters(searchPool, filters, shopById, "size");
    const poolColor = applyStructuredFilters(searchPool, filters, shopById, "color");
    const poolPrice = applyStructuredFilters(searchPool, filters, shopById, "price");
    const poolMaterial = applyStructuredFilters(searchPool, filters, shopById, "material");
    const poolFit = applyStructuredFilters(searchPool, filters, shopById, "fit");
    const poolOccasion = applyStructuredFilters(searchPool, filters, shopById, "occasion");
    const poolSport = applyStructuredFilters(searchPool, filters, shopById, "sport");
    const poolFeature = applyStructuredFilters(searchPool, filters, shopById, "feature");
    const poolPattern = applyStructuredFilters(searchPool, filters, shopById, "pattern");
    const poolShop = applyStructuredFilters(searchPool, filters, shopById, "shop");
    const poolOrigin = applyStructuredFilters(searchPool, filters, shopById, "origin");

    const gender: Record<GenderCode, number> = {
      nam: poolGender.filter((p) => matchesGenderFilter(p, "nam")).length,
      nu: poolGender.filter((p) => matchesGenderFilter(p, "nu")).length,
      unisex: poolGender.filter((p) => matchesGenderFilter(p, "unisex")).length,
    };

    const cat: Record<string, number> = {};
    for (const c of categories) {
      cat[c.slug] = poolCat.filter((p) => p.categorySlug === c.slug).length;
    }

    const size: Record<QuickSizeChip, number> = {
      XS: 0,
      S: 0,
      M: 0,
      L: 0,
      XL: 0,
      "2XL": 0,
      "3XL": 0,
      "4XL": 0,
    };
    for (const sz of QUICK_SIZE_CHIPS) {
      size[sz] = poolSize.filter((p) =>
        p.skus.some((sku) => sku.size === (sz as SizeCode) && sku.stock > 0)
      ).length;
    }

    const color: Record<string, number> = {};
    for (const cg of COLOR_GROUPS) {
      color[cg.id] = poolColor.filter((p) =>
        p.colors.some((c) => cg.codes.includes(c.code))
      ).length;
    }

    const price: Record<string, number> = {};
    for (const po of PRICE_OPTIONS) {
      price[po.id] = poolPrice.filter((p) => matchPriceBucket(p.price, po.id)).length;
    }

    const material: Record<string, number> = {};
    for (const key of Object.keys(MATERIAL_LABELS) as MaterialCode[]) {
      material[key] = poolMaterial.filter((p) => p.material === key).length;
    }

    const fit: Record<string, number> = {};
    for (const key of Object.keys(FIT_LABELS) as FitCode[]) {
      fit[key] = poolFit.filter((p) => p.fitType === key).length;
    }

    const occasion: Record<string, number> = {};
    for (const key of Object.keys(OCCASION_LABELS) as OccasionCode[]) {
      occasion[key] = poolOccasion.filter((p) => p.occasion?.includes(key)).length;
    }

    const sport: Record<string, number> = {};
    for (const key of Object.keys(SPORT_LABELS) as SportCode[]) {
      sport[key] = poolSport.filter((p) => p.sport?.includes(key)).length;
    }

    const feature: Record<string, number> = {};
    for (const key of Object.keys(FABRIC_FEATURE_LABELS) as FabricFeatureCode[]) {
      feature[key] = poolFeature.filter((p) => p.fabricFeatures?.includes(key)).length;
    }

    const pattern: Record<string, number> = {};
    for (const key of Object.keys(PATTERN_LABELS) as PatternCode[]) {
      pattern[key] = poolPattern.filter((p) => p.pattern === key).length;
    }

    const shop: Record<string, number> = {};
    for (const s of shops) {
      shop[s.id] = poolShop.filter((p) => p.shopId === s.id).length;
    }

    const origin: Record<string, number> = {};
    for (const o of ORIGIN_OPTIONS) {
      origin[o.code] = poolOrigin.filter(
        (p) => shopById.get(p.shopId)?.provinceCode === o.code
      ).length;
    }

    return {
      gender,
      cat,
      size,
      color,
      price,
      material,
      fit,
      occasion,
      sport,
      feature,
      pattern,
      shop,
      origin,
      allCatTotal: poolCat.length,
    };
  }, [categories, filters, searchPool, shopById, shops]);

  // Marketplace-wide matches if currently filtered by a single shop with 0 results
  const marketplaceFallbackCount = useMemo(() => {
    if (!shopParam || filteredProducts.length > 0) return 0;
    return applyStructuredFilters(searchPool, filters, shopById, "shop").length;
  }, [filteredProducts.length, filters, searchPool, shopById, shopParam]);

  const spellingSuggestion = useMemo(() => {
    if (!qParam.trim() || filteredProducts.length > 0) return null;
    return suggestSpelling(qParam);
  }, [filteredProducts.length, qParam]);

  useRevealObserver(rootRef, `${filterSignature}|${visibleCount}|${stateParam ?? ""}`);

  const activeCategory = categories.find((c) => c.slug === catParam);
  const activeShop = shops.find((s) => s.id === shopParam);
  const activeColor = COLOR_GROUPS.find((c) => c.id === colorParam);
  const activePrice = PRICE_OPTIONS.find((p) => p.id === priceParam && p.id !== "all");
  const activeOrigin = ORIGIN_OPTIONS.find((o) => o.code === originParam);

  const activeFilterChips = [
    qParam ? { key: "q", label: `Từ khóa: ${qParam}` } : null,
    activeCategory ? { key: "cat", label: activeCategory.name } : null,
    sizeParam ? { key: "size", label: `Size ${sizeParam}` } : null,
    activePrice ? { key: "price", label: activePrice.label } : null,
    activeColor ? { key: "color", label: activeColor.label } : null,
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
        : `${products.length} mẫu đồ basic nam nữ từ các xưởng dệt Việt Nam, cập nhật còn hàng theo màu và size.`;

  const shownRatio =
    filteredProducts.length > 0
      ? Math.min(100, Math.round((visibleProducts.length / filteredProducts.length) * 100))
      : 0;

  const remainingToLoad = Math.max(0, filteredProducts.length - visibleProducts.length);

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
            <span className={styles.optionCount}>{facets.allCatTotal}</span>
          </button>
          {categories.map((cat) => {
            const active = catParam === cat.slug;
            const count = facets.cat[cat.slug] ?? 0;
            return (
              <button
                key={cat.slug}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ cat: active ? null : cat.slug })}
                aria-pressed={active}
              >
                <span>{cat.name}</span>
                <span className={styles.optionCount}>{count}</span>
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
            const count = facets.size[sz] ?? 0;
            const active = sizeParam === sz;
            const unavailable = count === 0 && !active;
            return (
              <button
                key={sz}
                type="button"
                disabled={unavailable}
                className={`${styles.railSizeBtn} ${active ? styles.railSizeBtnActive : ""} ${
                  unavailable ? styles.railSizeBtnDisabled : ""
                }`}
                onClick={() => updateParams({ size: active ? null : sz })}
                aria-pressed={active}
                title={
                  unavailable ? `Size ${sz} tạm hết hàng` : `Lọc size ${sz} (${count} mẫu còn hàng)`
                }
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
            const count = facets.price[opt.id] ?? 0;
            return (
              <button
                key={opt.id}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ price: opt.id === "all" ? null : opt.id })}
                aria-pressed={active}
              >
                <span>{opt.label}</span>
                <span className={styles.optionCount}>{count}</span>
              </button>
            );
          })}
        </div>
      )}

      {renderAccordionSection(
        idPrefix,
        "color",
        "Màu sắc",
        Boolean(colorParam),
        <div className={styles.optionList}>
          {COLOR_GROUPS.map((cg) => {
            const active = colorParam === cg.id;
            const count = facets.color[cg.id] ?? 0;
            return (
              <button
                key={cg.id}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ color: active ? null : cg.id })}
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
                <span className={styles.optionCount}>{count}</span>
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
            <span>Tất cả {shops.length} xưởng</span>
            <span className={styles.optionCount}>
              {Object.values(facets.shop).reduce((s, n) => s + n, 0)}
            </span>
          </button>
          {shops.map((shop) => {
            const active = shopParam === shop.id;
            const count = facets.shop[shop.id] ?? 0;
            return (
              <button
                key={shop.id}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ shop: active ? null : shop.id })}
                aria-pressed={active}
              >
                <span>{shop.shortName}</span>
                <span className={styles.optionCount}>{count}</span>
              </button>
            );
          })}
        </div>
      )}

      {renderAccordionSection(
        idPrefix,
        "material",
        "Chất liệu",
        Boolean(materialParam),
        <div className={styles.optionList}>
          {(Object.keys(MATERIAL_LABELS) as MaterialCode[]).map((key) => {
            const active = materialParam === key;
            const count = facets.material[key] ?? 0;
            if (count === 0 && !active) return null;
            return (
              <button
                key={key}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ material: active ? null : key })}
                aria-pressed={active}
              >
                <span>{MATERIAL_LABELS[key]}</span>
                <span className={styles.optionCount}>{count}</span>
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
            const count = facets.fit[key] ?? 0;
            return (
              <button
                key={key}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ fit: active ? null : key })}
                aria-pressed={active}
              >
                <span>{FIT_LABELS[key]}</span>
                <span className={styles.optionCount}>{count}</span>
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
            const count = facets.occasion[key] ?? 0;
            return (
              <button
                key={key}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ occasion: active ? null : key })}
                aria-pressed={active}
              >
                <span>{OCCASION_LABELS[key]}</span>
                <span className={styles.optionCount}>{count}</span>
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
            const count = facets.sport[key] ?? 0;
            return (
              <button
                key={key}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ sport: active ? null : key })}
                aria-pressed={active}
              >
                <span>{SPORT_LABELS[key]}</span>
                <span className={styles.optionCount}>{count}</span>
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
            const count = facets.feature[key] ?? 0;
            return (
              <button
                key={key}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ feature: active ? null : key })}
                aria-pressed={active}
              >
                <span>{FABRIC_FEATURE_LABELS[key]}</span>
                <span className={styles.optionCount}>{count}</span>
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
            const count = facets.gender[g];
            return (
              <button
                key={g}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ gender: active ? null : g })}
                aria-pressed={active}
              >
                <span>{GENDER_LABELS[g]}</span>
                <span className={styles.optionCount}>{count}</span>
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
            const count = facets.pattern[key] ?? 0;
            return (
              <button
                key={key}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ pattern: active ? null : key })}
                aria-pressed={active}
              >
                <span>{PATTERN_LABELS[key]}</span>
                <span className={styles.optionCount}>{count}</span>
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
            const count = facets.origin[opt.code] ?? 0;
            return (
              <button
                key={opt.code}
                type="button"
                className={`${styles.optionBtn} ${active ? styles.optionBtnActive : ""}`}
                onClick={() => updateParams({ origin: active ? null : opt.code })}
                aria-pressed={active}
              >
                <span>{opt.label}</span>
                <span className={styles.optionCount}>{count}</span>
              </button>
            );
          })}
        </div>
      )}
    </>
  );

  return (
    <div ref={rootRef} className={styles.page}>
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
              <span className={styles.countText}>
                Khớp {filteredProducts.length} / {products.length} sản phẩm
              </span>
              <div className={styles.desktopControls}>
                <button
                  type="button"
                  className={styles.sidebarToggleBtn}
                  onClick={() => setSidebarOpen((v) => !v)}
                  aria-expanded={sidebarOpen}
                >
                  <IconSliders size={15} />
                  <span>{sidebarOpen ? "Ẩn bộ lọc" : "Hiện bộ lọc"}</span>
                </button>
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
                    <option value="bestseller">Bán chạy</option>
                    <option value="newest">Mới nhất</option>
                    <option value="price-asc">Giá tăng dần</option>
                    <option value="price-desc">Giá giảm dần</option>
                  </select>
                </div>
              </div>
            </div>
          </div>
        </section>

        {mode === "search" && (
          <div className={styles.trendingStrip} aria-label="Từ khóa xu hướng">
            <span className={styles.trendingLabel}>Tìm nhiều:</span>
            <div className={styles.trendingList}>
              {TRENDING_KEYWORDS.map((kw) => {
                const active = qParam.toLowerCase() === kw.query.toLowerCase();
                return (
                  <button
                    key={kw.query}
                    type="button"
                    className={`${styles.trendingBtn} ${active ? styles.trendingBtnActive : ""}`}
                    onClick={() => {
                      setDraftQuery(kw.query);
                      updateParams({ q: kw.query });
                    }}
                  >
                    <span>{kw.label}</span>
                    {kw.tag && <span className={styles.trendingTag}>{kw.tag}</span>}
                  </button>
                );
              })}
            </div>
          </div>
        )}

        <nav className={styles.catStrip} aria-label="Nhóm danh mục sản phẩm">
          <button
            type="button"
            className={`${styles.catBtn} ${!catParam ? styles.catBtnActive : ""}`}
            onClick={() => updateParams({ cat: null })}
            aria-pressed={!catParam}
          >
            <span>Tất cả</span>
            <span className={styles.catCount}>{facets.allCatTotal}</span>
          </button>
          {categories.map((cat) => {
            const count = facets.cat[cat.slug] ?? 0;
            const active = catParam === cat.slug;
            return (
              <button
                key={cat.slug}
                type="button"
                className={`${styles.catBtn} ${active ? styles.catBtnActive : ""}`}
                onClick={() => updateParams({ cat: active ? null : cat.slug })}
                aria-pressed={active}
              >
                <span>{cat.name}</span>
                <span className={styles.catCount}>{count}</span>
              </button>
            );
          })}
        </nav>

        {activeCategory &&
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

          <select
            className={styles.sortSelect}
            value={sortParam}
            onChange={(e) => updateParams({ sort: e.target.value })}
            aria-label="Sắp xếp sản phẩm"
          >
            <option value="bestseller">Bán chạy</option>
            <option value="newest">Mới nhất</option>
            <option value="price-asc">Giá tăng dần</option>
            <option value="price-desc">Giá giảm dần</option>
          </select>
        </div>

        <section
          className={`${styles.bodyGrid} ${!sidebarOpen ? styles.bodyGridWide : ""}`}
          aria-labelledby="catalog-results-heading"
        >
          {sidebarOpen && (
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
              <span className={styles.resultsSub}>
                Đang hiện {visibleProducts.length} / {filteredProducts.length} sản phẩm
              </span>
            </div>

            {stateParam === "loading" ? (
              <StateView kind="loading" title="Đang tải danh mục sản phẩm..." rows={6} />
            ) : loadError || stateParam === "error" ? (
              <StateView
                kind="error"
                title="Không thể tải danh mục lúc này"
                message={
                  loadError ??
                  "Kết nối danh mục tạm thời gián đoạn. Vui lòng thử tải lại danh sách."
                }
                actionLabel="Tải lại danh mục"
                onAction={() => (loadError ? router.refresh() : updateParams({ state: null }))}
              />
            ) : stateParam === "empty" || filteredProducts.length === 0 ? (
              <div className={styles.emptyBox}>
                <h3 className={styles.emptyHeading}>
                  Chưa tìm thấy sản phẩm khớp với bộ lọc hiện tại
                </h3>
                <p className={styles.emptyMessage}>
                  {qParam
                    ? `Không có mẫu nào khớp trực tiếp với "${qParam}" trong phạm vi lọc đang chọn.`
                    : "Hãy thử nới lỏng mức giá, đổi kích cỡ hoặc xóa bớt bộ lọc để xem thêm mẫu."}
                </p>

                {spellingSuggestion && (
                  <div className={styles.spellingRow}>
                    <span className={styles.spellingPrompt}>Có phải bạn muốn tìm:</span>
                    <button
                      type="button"
                      className={styles.spellingBtn}
                      onClick={() => {
                        setDraftQuery(spellingSuggestion);
                        updateParams({ q: spellingSuggestion });
                      }}
                    >
                      {spellingSuggestion}
                    </button>
                  </div>
                )}

                <div className={styles.emptyActions}>
                  {shopParam && (
                    <Button
                      variant="primary"
                      size="md"
                      onClick={() => updateParams({ shop: null })}
                    >
                      {marketplaceFallbackCount > 0
                        ? `Xem kết quả trên toàn NexusMart (${marketplaceFallbackCount} mẫu)`
                        : "Xem kết quả trên toàn NexusMart"}
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

                <div className={styles.emptyTrending}>
                  <span className={styles.trendingLabel}>Gợi ý từ khóa phổ biến:</span>
                  <div className={styles.trendingList}>
                    {TRENDING_KEYWORDS.slice(0, 6).map((kw) => (
                      <button
                        key={kw.query}
                        type="button"
                        className={styles.trendingBtn}
                        onClick={() => {
                          setDraftQuery(kw.query);
                          router.replace(`${pathname}?q=${encodeURIComponent(kw.query)}`, {
                            scroll: false,
                          });
                        }}
                      >
                        {kw.label}
                      </button>
                    ))}
                  </div>
                </div>
              </div>
            ) : (
              <>
                <div className={styles.productGrid}>
                  {visibleProducts.map((product, idx) => (
                    <div
                      key={product.id}
                      data-cat-reveal=""
                      style={{ "--stagger": idx % 4 } as React.CSSProperties}
                    >
                      <ProductCard
                        product={product}
                        shop={shopById.get(product.shopId)}
                        showSpec={true}
                        showSla={true}
                        showBadges={true}
                        showWishlist={true}
                        showRating={true}
                        enableQuickAdd={true}
                        enableInteractiveSwatches={true}
                        priority={idx < 4}
                        quotaLabel={
                          sizeParam
                            ? `Còn ${product.skus
                                .filter((s) => s.size === (sizeParam as SizeCode))
                                .reduce((acc, s) => acc + s.stock, 0)} size ${sizeParam}`
                            : undefined
                        }
                      />
                    </div>
                  ))}
                </div>

                <div
                  className={styles.loadMoreBar}
                  data-cat-reveal=""
                  style={{ "--stagger": 1 } as React.CSSProperties}
                >
                  <p className={styles.loadMoreMeta}>
                    Đã hiển thị {visibleProducts.length} / {filteredProducts.length} sản phẩm
                  </p>
                  <div
                    className={styles.loadMoreTrack}
                    role="progressbar"
                    aria-valuemin={0}
                    aria-valuemax={filteredProducts.length}
                    aria-valuenow={visibleProducts.length}
                    aria-label="Tiến độ hiển thị sản phẩm"
                  >
                    <div className={styles.loadMoreFill} style={{ width: `${shownRatio}%` }} />
                  </div>
                  {remainingToLoad > 0 && (
                    <Button
                      variant="ink"
                      size="md"
                      onClick={() => setVisibleCount((c) => c + PAGE_STEP)}
                    >
                      Xem thêm {Math.min(PAGE_STEP, remainingToLoad)} sản phẩm
                    </Button>
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

      {sheetOpen && (
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
              Xem {filteredProducts.length} sản phẩm
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
