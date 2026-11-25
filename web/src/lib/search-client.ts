import type { CategorySlug, ProductColor, SizeCode } from "@/data";
import { api } from "@/lib/api";
import { computeProductDisplayPricing, type ProductDisplayPricing } from "@/lib/catalog-mapper";
import type { CatalogCategory, CatalogProduct, CatalogShop, CatalogSku } from "@/lib/catalog-types";
import { formatInt } from "@/lib/format";
import type {
  GatewayActiveShopDiscount,
  GatewayDeleteSearchHistoryResponse,
  GatewayFlashSaleItem,
  GatewayProduct,
  GatewaySearchHistoryItem,
  GatewaySearchHistoryResponse,
  GatewaySearchMode,
  GatewaySearchProductHit,
  GatewaySearchProductsResponse,
  GatewaySearchSortBy,
  GatewaySearchSuggestResponse,
} from "@/lib/gateway-types";
import { getProductEditorialBySlug } from "@/mock/productEditorial";

export const SEARCH_SUGGEST_DEBOUNCE_MS = 200;
export const SEARCH_HISTORY_MAX_ITEMS = 50;
export const CATALOG_PAGE_SIZE = 24;

const DEFAULT_FALLBACK_IMAGE = "/products/ao-thun-cotton-trang.png";

const KNOWN_CATEGORY_SLUGS = new Set<CategorySlug>([
  "ao-thun",
  "ao-polo",
  "quan-short",
  "quan-jogger",
  "do-lot",
  "tat-phu-kien",
]);

const SIZE_ORDER = ["XS", "S", "M", "L", "XL", "2XL", "3XL", "4XL"];

const COLOR_HEX_FALLBACK: Record<string, string> = {
  trang: "#f5f5f2",
  kem: "#ece6d8",
  be: "#d6cec1",
  xamda: "#9aa5ac",
  xamdam: "#5c666e",
  xamtro: "#808991",
  xam: "#949ea6",
  den: "#1f2428",
  thanchi: "#313a40",
  xanhthan: "#25394a",
  xanhda: "#546f7d",
  covit: "#0d7480",
  xanh: "#2f5f73",
  do: "#9c3a3a",
  nau: "#6f5646",
  reu: "#4f6457",
};

const LEGACY_COLOR_GROUP_MAP: Record<string, string> = {
  "trang-kem": "trang",
  "den-than": "den",
};

export interface SearchQueryParams {
  q?: string;
  categoryId?: string;
  categorySlug?: string;
  shopId?: string;
  shopSlug?: string;
  minPriceVnd?: number;
  maxPriceVnd?: number;
  minRatingX100?: number;
  inStockOnly?: boolean;
  sortBy?: GatewaySearchSortBy | string;
  mode?: GatewaySearchMode | string;
  size?: string;
  color?: string;
  page?: number;
  pageSize?: number;
}

export function normalizeSearchSortParam(raw: string | undefined | null): GatewaySearchSortBy {
  const clean = (raw ?? "").trim().toLowerCase();
  switch (clean) {
    case "price_asc":
    case "price-asc":
      return "price_asc";
    case "price_desc":
    case "price-desc":
      return "price_desc";
    case "newest":
      return "newest";
    case "rating_desc":
    case "rating-desc":
      return "rating_desc";
    case "relevance":
    case "bestseller":
    default:
      return "relevance";
  }
}

export function parseMinRatingX100Param(raw: string | undefined | null): number | undefined {
  const clean = (raw ?? "").trim();
  if (!clean) return undefined;
  const num = Number(clean);
  if (!Number.isFinite(num) || num <= 0) return undefined;
  if (num <= 5) {
    return Math.max(0, Math.min(500, Math.round(num * 100)));
  }
  return Math.max(0, Math.min(500, Math.trunc(num)));
}

export function parseInStockParam(raw: string | undefined | null): boolean {
  const clean = (raw ?? "").trim().toLowerCase();
  return clean === "1" || clean === "true";
}

export function parsePageParam(raw: string | undefined | null): number {
  const num = Number.parseInt((raw ?? "").trim(), 10);
  if (!Number.isFinite(num) || num < 1) return 1;
  return num;
}

export function normalizeColorFilterParam(raw: string | undefined | null): string | undefined {
  const clean = (raw ?? "").trim();
  if (!clean) return undefined;
  const lower = clean.toLowerCase();
  return LEGACY_COLOR_GROUP_MAP[lower] ?? clean;
}

export function shouldShowCatalogEmptyState(params: {
  forcedEmpty: boolean;
  displayedCount: number;
  hasMorePages: boolean;
}): boolean {
  return params.forcedEmpty || (params.displayedCount === 0 && !params.hasMorePages);
}

export interface CatalogCountSummary {
  metaText: string;
  resultsSub: string;
  loadMoreMeta: string;
  mobileCta: string;
}

export function buildCatalogCountSummary(params: {
  hasEditorialFilter: boolean;
  displayedCount: number;
  loadedCount: number;
  shownCountSoFar: number;
  serverTotal: number;
  page: number;
}): CatalogCountSummary {
  const safeDisplayed = Math.max(0, Math.trunc(params.displayedCount));
  const safeLoaded = Math.max(0, Math.trunc(params.loadedCount));
  const safeShown = Math.max(0, Math.trunc(params.shownCountSoFar));
  const safeTotal = Math.max(0, Math.trunc(params.serverTotal));
  const safePage = Math.max(1, Math.trunc(params.page));
  const pagePrefix = safePage > 1 ? `Trang ${formatInt(safePage)} · ` : "";

  if (params.hasEditorialFilter) {
    return {
      metaText: `Khớp ${formatInt(safeDisplayed)} / ${formatInt(safeLoaded)} mẫu đã xem (${formatInt(safeTotal)} sản phẩm trước lọc)`,
      resultsSub: `${pagePrefix}Khớp ${formatInt(safeDisplayed)} trong ${formatInt(safeLoaded)} sản phẩm đã xem (${formatInt(safeTotal)} kết quả trước lọc)`,
      loadMoreMeta: `Đã xem ${formatInt(safeShown)} / ${formatInt(safeTotal)} sản phẩm · Khớp ${formatInt(safeDisplayed)} mẫu`,
      mobileCta: `Xem kết quả lọc (${formatInt(safeDisplayed)} mẫu khớp)`,
    };
  }

  return {
    metaText: `Khớp ${formatInt(safeTotal)} sản phẩm`,
    resultsSub: `${pagePrefix}Đang hiện ${formatInt(safeDisplayed)} / ${formatInt(safeTotal)} sản phẩm`,
    loadMoreMeta: `Đã hiển thị ${formatInt(safeShown)} / ${formatInt(safeTotal)} sản phẩm`,
    mobileCta: `Xem ${formatInt(safeTotal)} sản phẩm`,
  };
}

export function buildSearchQueryString(params: SearchQueryParams = {}): string {
  const qs = new URLSearchParams();
  const q = params.q?.trim();
  if (q) qs.set("q", q);
  if (params.categoryId?.trim()) qs.set("category_id", params.categoryId.trim());
  if (params.categorySlug?.trim()) qs.set("category_slug", params.categorySlug.trim());
  if (params.shopId?.trim()) qs.set("shop_id", params.shopId.trim());
  if (params.shopSlug?.trim()) qs.set("shop_slug", params.shopSlug.trim());
  if (params.minPriceVnd && params.minPriceVnd > 0) {
    qs.set("min_price_vnd", String(Math.trunc(params.minPriceVnd)));
  }
  if (params.maxPriceVnd && params.maxPriceVnd > 0) {
    qs.set("max_price_vnd", String(Math.trunc(params.maxPriceVnd)));
  }
  if (params.minRatingX100 && params.minRatingX100 > 0) {
    qs.set("min_rating_x100", String(Math.max(0, Math.min(500, Math.trunc(params.minRatingX100)))));
  }
  if (params.inStockOnly) {
    qs.set("in_stock_only", "true");
  }
  if (params.sortBy) {
    qs.set("sort_by", normalizeSearchSortParam(params.sortBy));
  }
  if (params.mode) {
    const mode = params.mode.trim().toLowerCase();
    if (mode === "auto" || mode === "keyword" || mode === "hybrid") {
      qs.set("mode", mode);
    }
  }
  if (params.size?.trim()) {
    qs.set("size", params.size.trim());
  }
  const color = normalizeColorFilterParam(params.color);
  if (color) {
    qs.set("color", color);
  }
  const page = Math.max(1, Math.trunc(params.page ?? 1));
  const pageSize = Math.max(1, Math.min(100, Math.trunc(params.pageSize ?? CATALOG_PAGE_SIZE)));
  qs.set("page", String(page));
  qs.set("page_size", String(pageSize));
  return qs.toString();
}

export function collectSearchHitSkuIds(hits: readonly GatewaySearchProductHit[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const hit of hits) {
    for (const rawId of [...(hit.sku_ids ?? []), hit.default_sku_id ?? ""]) {
      const id = rawId.trim();
      if (!id || seen.has(id)) continue;
      seen.add(id);
      out.push(id);
    }
  }
  return out;
}

/**
 * Computes effective storefront display pricing for a SearchProductHit using its `sku_ids`
 * without calling ProductService (0 N+1 queries).
 * Single-price rule: live flash sale > active shop discount program > base hit price.
 */
export function computeSearchHitDisplayPricing(
  hit: Pick<
    GatewaySearchProductHit,
    "min_price_vnd" | "max_price_vnd" | "sku_ids" | "default_sku_id"
  >,
  liveFlashItemsBySkuId?: Map<string, GatewayFlashSaleItem>,
  activeShopDiscountsBySkuId?: Map<string, GatewayActiveShopDiscount>
): ProductDisplayPricing {
  const baseMin = Math.max(0, Math.trunc(hit.min_price_vnd ?? 0));
  const baseMax = Math.max(baseMin, Math.trunc(hit.max_price_vnd ?? baseMin));

  const skuIds = Array.from(
    new Set([...(hit.sku_ids ?? []), hit.default_sku_id ?? ""].map((s) => s.trim()).filter(Boolean))
  );
  if (skuIds.length === 0) {
    return {
      minPriceVnd: baseMin,
      maxPriceVnd: baseMax,
      originalPriceVnd: undefined,
      promoKind: "none",
    };
  }

  type PromoEntry = {
    effective: number;
    original: number;
    promoKind: "flash" | "shop_discount";
  };
  const promoEntries: PromoEntry[] = [];

  for (const skuId of skuIds) {
    const liveFlash = liveFlashItemsBySkuId?.get(skuId);
    if (
      liveFlash &&
      (liveFlash.remaining_slots ?? 0) > 0 &&
      Math.trunc(liveFlash.flash_price_vnd) > 0
    ) {
      const flashPrice = Math.trunc(liveFlash.flash_price_vnd);
      const regular =
        liveFlash.original_price_vnd && Math.trunc(liveFlash.original_price_vnd) > flashPrice
          ? Math.trunc(liveFlash.original_price_vnd)
          : baseMin;
      if (regular > flashPrice) {
        promoEntries.push({ effective: flashPrice, original: regular, promoKind: "flash" });
        continue;
      }
    }

    const shopDisc = activeShopDiscountsBySkuId?.get(skuId);
    if (shopDisc && Math.trunc(shopDisc.discounted_price_vnd) > 0) {
      const discPrice = Math.trunc(shopDisc.discounted_price_vnd);
      const regular =
        shopDisc.original_price_vnd && Math.trunc(shopDisc.original_price_vnd) > discPrice
          ? Math.trunc(shopDisc.original_price_vnd)
          : baseMin;
      if (regular > discPrice) {
        promoEntries.push({
          effective: discPrice,
          original: regular,
          promoKind: "shop_discount",
        });
      }
    }
  }

  if (promoEntries.length === 0) {
    return {
      minPriceVnd: baseMin,
      maxPriceVnd: baseMax,
      originalPriceVnd: undefined,
      promoKind: "none",
    };
  }

  const promoRank = (kind: "flash" | "shop_discount"): number => (kind === "flash" ? 2 : 1);
  const sorted = [...promoEntries].sort((a, b) => {
    if (a.effective !== b.effective) return a.effective - b.effective;
    if (b.original !== a.original) return b.original - a.original;
    return promoRank(b.promoKind) - promoRank(a.promoKind);
  });

  const cheapest = sorted[0];
  const allSkusDiscounted = promoEntries.length >= skuIds.length;
  const minPriceVnd = allSkusDiscounted
    ? cheapest.effective
    : baseMin > 0
      ? Math.min(baseMin, cheapest.effective)
      : cheapest.effective;
  const maxPriceVnd = allSkusDiscounted
    ? Math.max(...promoEntries.map((e) => e.effective))
    : Math.max(minPriceVnd, baseMax);

  const originalPriceVnd =
    cheapest.effective === minPriceVnd && cheapest.original > minPriceVnd
      ? cheapest.original
      : undefined;
  const promoKind = originalPriceVnd
    ? cheapest.promoKind
    : promoEntries.some((e) => e.promoKind === "flash")
      ? "flash"
      : "shop_discount";

  return { minPriceVnd, maxPriceVnd, originalPriceVnd, promoKind };
}

/**
 * Shallow-enriches GatewayProduct entries (e.g. CheckoutSuggestionStrip items) with real
 * effective_price_vnd / original_price_vnd / min_price_vnd from live flash & shop discounts (§10).
 */
export function enrichProductsWithPromotions(
  products: readonly GatewayProduct[],
  liveFlashItemsBySkuId?: Map<string, GatewayFlashSaleItem>,
  activeShopDiscountsBySkuId?: Map<string, GatewayActiveShopDiscount>
): GatewayProduct[] {
  return products.map((product) => {
    const pricing = computeProductDisplayPricing(
      product,
      liveFlashItemsBySkuId,
      activeShopDiscountsBySkuId
    );
    return {
      ...product,
      min_price_vnd: pricing.minPriceVnd,
      max_price_vnd: pricing.maxPriceVnd,
      effective_price_vnd: pricing.minPriceVnd,
      original_price_vnd: pricing.originalPriceVnd,
      promo_kind: pricing.promoKind,
    };
  });
}

function normalizeColorName(input: string): string {
  return input
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[đĐ]/g, "d")
    .toLowerCase()
    .replace(/\s+/g, " ")
    .trim();
}

function slugifyColorCode(input: string): string {
  const cleaned = normalizeColorName(input)
    .replace(/[^a-z0-9]+/g, "")
    .trim();
  return cleaned || "mac-dinh";
}

/**
 * Resolves a PDP `?color=` query parameter against a product's available colors:
 * 1) exact or case-insensitive `color.code`,
 * 2) slugified `color.name` (e.g. `?color=denthan` -> `{ code: "den", name: "Đen than" }`),
 * 3) `fallbackCode` when not found.
 */
export function resolveInitialProductColor(
  colors: readonly { code: string; name: string }[],
  colorQuery: string | null | undefined,
  fallbackCode: string
): string {
  const clean = (colorQuery ?? "").trim();
  if (!clean) return fallbackCode;

  const exact = colors.find((c) => c.code === clean);
  if (exact) return exact.code;

  const lower = clean.toLowerCase();
  const byCodeLower = colors.find((c) => c.code.toLowerCase() === lower);
  if (byCodeLower) return byCodeLower.code;

  const querySlug = slugifyColorCode(clean);
  const byNameSlug = colors.find((c) => slugifyColorCode(c.name) === querySlug);
  if (byNameSlug) return byNameSlug.code;

  return fallbackCode;
}

function guessHexForColor(code: string, name: string): string {
  if (COLOR_HEX_FALLBACK[code]) return COLOR_HEX_FALLBACK[code];
  const norm = slugifyColorCode(name);
  for (const [key, hex] of Object.entries(COLOR_HEX_FALLBACK)) {
    if (norm.includes(key)) return hex;
  }
  return "#5d6b72";
}

function sortSizes(sizes: string[]): SizeCode[] {
  return [...sizes].sort((a, b) => {
    const ia = SIZE_ORDER.indexOf(a.toUpperCase());
    const ib = SIZE_ORDER.indexOf(b.toUpperCase());
    if (ia !== -1 && ib !== -1) return ia - ib;
    if (ia !== -1) return -1;
    if (ib !== -1) return 1;
    return a.localeCompare(b);
  }) as SizeCode[];
}

export function mapSearchHitToCatalogProduct(
  hit: GatewaySearchProductHit,
  context: {
    categoriesById: Map<string, CatalogCategory>;
    shopsById: Map<string, CatalogShop>;
    liveFlashItemsBySkuId?: Map<string, GatewayFlashSaleItem>;
    activeShopDiscountsBySkuId?: Map<string, GatewayActiveShopDiscount>;
  }
): CatalogProduct {
  const slug = hit.slug?.trim() || hit.id;
  const editorial = getProductEditorialBySlug(slug);
  const category = context.categoriesById.get(hit.category_id);
  const shop = context.shopsById.get(hit.shop_id);

  const primaryImage = hit.thumbnail_url?.trim() || DEFAULT_FALLBACK_IMAGE;
  const onModelImage = editorial?.onModelImage ?? undefined;
  const gallery = onModelImage ? [primaryImage, onModelImage] : [primaryImage];

  const editorialColorByName = new Map(
    (editorial?.colors ?? []).map((c) => [normalizeColorName(c.name), c])
  );
  const editorialColorByCode = new Map(
    (editorial?.colors ?? []).map((c) => [c.code.trim().toLowerCase(), c])
  );
  const rawColors = Array.isArray(hit.colors) ? hit.colors.filter(Boolean) : [];
  const colorMap = new Map<string, ProductColor>();
  for (const rawColor of rawColors) {
    const slugged = slugifyColorCode(rawColor);
    const edColor =
      editorialColorByName.get(normalizeColorName(rawColor)) ?? editorialColorByCode.get(slugged);
    const code = edColor?.code ?? slugged;
    if (colorMap.has(code)) continue;
    colorMap.set(code, {
      code,
      name: edColor?.name ?? rawColor,
      hex: edColor?.hex ?? guessHexForColor(code, rawColor),
      image: edColor?.image ?? primaryImage,
    });
  }
  if (colorMap.size === 0) {
    colorMap.set("mac-dinh", {
      code: "mac-dinh",
      name: "Mặc định",
      hex: "#5d6b72",
      image: primaryImage,
    });
  }
  const colors = Array.from(colorMap.values());

  const uniqueSizes = Array.from(new Set((hit.sizes ?? []).map((s) => s.trim()).filter(Boolean)));
  const sizes = sortSizes(uniqueSizes);

  const pricing = computeSearchHitDisplayPricing(
    hit,
    context.liveFlashItemsBySkuId,
    context.activeShopDiscountsBySkuId
  );

  // Build lightweight SKU entries for ProductCard quick-add without N+1 calls:
  // id is empty unless there is exactly 1 known default SKU, so ProductCard resolves
  // the exact SKU UUID via resolveGatewaySkuId ONLY if the buyer clicks quick-add.
  const singleSkuId =
    (hit.sku_count ?? 0) === 1 && hit.default_sku_id?.trim() ? hit.default_sku_id.trim() : "";
  const syntheticSkus: CatalogSku[] = [];
  const skuSizes = sizes.length > 0 ? sizes : (["M"] as SizeCode[]);
  for (const c of colors) {
    for (const sz of skuSizes) {
      syntheticSkus.push({
        id: singleSkuId,
        skuCode: "",
        skuName: `${c.name} / ${sz}`,
        colorCode: c.code,
        colorName: c.name,
        size: sz,
        stock: hit.in_stock ? 1 : 0,
        price: pricing.minPriceVnd,
        regularPriceVnd: hit.min_price_vnd,
        version: 1,
        weightGrams: 0,
        imageUrl: c.image || primaryImage,
        isActive: hit.in_stock,
        attributes: {
          ...(c.code !== "mac-dinh" ? { color: c.name, color_code: c.code } : {}),
          ...(sizes.length > 0 ? { size: sz } : {}),
        },
      });
    }
  }

  const rawCategorySlug = hit.category_slug?.trim() || category?.slug || "ao-thun";
  const categorySlug: CategorySlug = KNOWN_CATEGORY_SLUGS.has(rawCategorySlug as CategorySlug)
    ? (rawCategorySlug as CategorySlug)
    : "ao-thun";
  const categoryName = hit.category_name?.trim() || category?.name || "Đồ cơ bản";

  const shopName = hit.shop_name?.trim() || shop?.name || "Gian hàng NexusMart";
  const shopShortName = shop?.shortName || shopName;
  const shopSlug = hit.shop_slug?.trim() || shop?.slug || "";

  return {
    id: hit.id,
    slug,
    name: hit.name,
    shortName: editorial?.shortName ?? hit.name,
    categoryId: hit.category_id,
    categorySlug,
    rawCategorySlug,
    categoryName,
    shopId: hit.shop_id,
    shopSlug,
    shopName,
    shopShortName,
    shopProvinceCode: shop?.provinceCode,
    gender: editorial?.gender ?? "unisex",
    price: pricing.minPriceVnd,
    minPriceVnd: pricing.minPriceVnd,
    maxPriceVnd: pricing.maxPriceVnd,
    ratingAvgX100: Math.max(0, Math.min(500, Math.trunc(hit.rating_avg_x100 ?? 0))),
    reviewCount: Math.max(0, Math.trunc(hit.review_count ?? 0)),
    originalPrice: pricing.originalPriceVnd,
    image: primaryImage,
    onModelImage,
    gallery,
    colors,
    sizes,
    skus: syntheticSkus,
    fabric: editorial?.fabric ?? "",
    gsm: editorial?.gsm ?? 0,
    fit: editorial?.fit ?? "",
    origin: editorial?.origin ?? (shop ? `${shop.city} · ${shop.name}` : "Việt Nam"),
    careGuide: editorial?.careGuide ?? [],
    highlights: editorial?.highlights ?? [],
    description: hit.description_snippet?.trim() || "",
    sizeChart: editorial?.sizeChart ?? [],
    isFlashSale: pricing.promoKind === "flash",
    isNew: editorial?.isNew,
    material: editorial?.material,
    materialLabel: editorial?.materialLabel,
    fitType: editorial?.fitType,
    occasion: editorial?.occasion,
    sport: editorial?.sport,
    fabricFeatures: editorial?.fabricFeatures,
    pattern: editorial?.pattern,
    badges: editorial?.badges,
    isCombo: undefined,
    isPack: editorial?.isPack,
    comboSlots: undefined,
    tierDiscounts: undefined,
    bundleIds: undefined,
    bundleSlugs: [],
    giftThreshold: undefined,
    returnEligible: editorial?.returnEligible,
    returnExclusionReason: editorial?.returnExclusionReason,
    slaText: editorial?.slaText,
    maxPerOrder: editorial?.maxPerOrder,
    modelInfo: editorial?.modelInfo,
    careInstructions: editorial?.careInstructions,
    hasVideo: editorial?.hasVideo,
    videoUrl: editorial?.videoUrl,
    videoPoster: editorial?.videoPoster,
    faqs: editorial?.faqs,
    salesRank: editorial?.salesRank,
    createdAt: undefined,
    shopSizeChart: editorial?.shopSizeChart,
    collectionSlugs: editorial?.collectionSlugs,
    status: hit.status || "APPROVED",
    editorialProductId: editorial?.editorialProductId,
    editorialShopId: shop?.editorialShopId,
    hasEditorialContent: Boolean(editorial),
  };
}

export async function fetchSearchProductsClient(
  params: SearchQueryParams = {}
): Promise<GatewaySearchProductsResponse> {
  const qs = buildSearchQueryString(params);
  const res = await api<GatewaySearchProductsResponse>(`search?${qs}`, { retryOn401: false });
  return {
    items: Array.isArray(res?.items) ? res.items : [],
    total: Math.max(0, Math.trunc(res?.total ?? 0)),
    page: Math.max(1, Math.trunc(res?.page ?? params.page ?? 1)),
    page_size: Math.max(1, Math.trunc(res?.page_size ?? params.pageSize ?? CATALOG_PAGE_SIZE)),
    category_facets: Array.isArray(res?.category_facets) ? res.category_facets : [],
    shop_facets: Array.isArray(res?.shop_facets) ? res.shop_facets : [],
    did_you_mean: typeof res?.did_you_mean === "string" ? res.did_you_mean : "",
    degraded_mode: typeof res?.degraded_mode === "string" ? res.degraded_mode : "",
    used_vector: Boolean(res?.used_vector),
  };
}

export async function fetchSearchSuggestClient(
  query: string,
  limit = 8
): Promise<GatewaySearchSuggestResponse> {
  const qs = new URLSearchParams();
  const clean = query.trim();
  if (clean) qs.set("q", clean);
  qs.set("limit", String(Math.max(1, Math.min(20, Math.trunc(limit)))));
  const res = await api<GatewaySearchSuggestResponse>(`search/suggest?${qs.toString()}`, {
    retryOn401: false,
  });
  return {
    keywords: Array.isArray(res?.keywords) ? res.keywords : [],
    categories: Array.isArray(res?.categories) ? res.categories : [],
    shops: Array.isArray(res?.shops) ? res.shops : [],
    trending: Array.isArray(res?.trending) ? res.trending : [],
    products: Array.isArray(res?.products) ? res.products : [],
  };
}

export async function fetchSearchHistoryClient(
  limit = SEARCH_HISTORY_MAX_ITEMS
): Promise<GatewaySearchHistoryItem[]> {
  const safeLimit = Math.max(1, Math.min(SEARCH_HISTORY_MAX_ITEMS, Math.trunc(limit)));
  const res = await api<GatewaySearchHistoryResponse>(`search/history?limit=${safeLimit}`);
  const items = Array.isArray(res?.items) ? res.items : [];
  return items.slice(0, SEARCH_HISTORY_MAX_ITEMS);
}

export async function deleteSearchHistoryItemClient(id: string): Promise<boolean> {
  const cleanId = id.trim();
  if (!cleanId) return false;
  const res = await api<GatewayDeleteSearchHistoryResponse>(
    `search/history/${encodeURIComponent(cleanId)}`,
    { method: "DELETE" }
  );
  return Boolean(res?.ok ?? true);
}

export async function clearSearchHistoryClient(): Promise<boolean> {
  const res = await api<GatewayDeleteSearchHistoryResponse>("search/history", {
    method: "DELETE",
  });
  return Boolean(res?.ok ?? true);
}
