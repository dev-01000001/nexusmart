// Pure search types, query sanitization, response normalization, and fallback helpers.
// Zero Next.js or DOM imports so Node 22 runs unit tests directly.
import type { CategoryInfo, Product } from "../../data/products";

export const VALID_SEARCH_SORTS = ["relevance", "price_asc", "price_desc", "newest"] as const;
export type SearchSort = (typeof VALID_SEARCH_SORTS)[number];

export const SEARCH_SORT_LABELS: Record<SearchSort, string> = {
  relevance: "Phù hợp nhất",
  price_asc: "Giá tăng dần",
  price_desc: "Giá giảm dần",
  newest: "Mới nhất",
};

export const DEFAULT_SEARCH_PAGE_SIZE = 12;
export const MAX_SEARCH_PAGE_SIZE = 48;
export const MAX_SEARCH_QUERY_LEN = 200;
export const MAX_SUGGEST_QUERY_LEN = 120;
export const MAX_RECENT_SEARCHES = 6;
const MAX_PRICE_VND = 1_000_000_000;
const CONTROL_CHAR_RE = /[\u0000-\u001F\u007F]/g;

export interface SearchQueryParams {
  q: string;
  category: string;
  min_price?: number;
  max_price?: number;
  sort: SearchSort;
  page: number;
  page_size: number;
}

export interface SearchResultItem {
  id: string;
  slug: string;
  shopId: string;
  name: string;
  description: string;
  categorySlug: string;
  categoryName: string;
  priceVnd: number;
  maxPriceVnd?: number;
  originalPriceVnd?: number;
  image: string;
  images: string[];
  totalStock: number;
  inStock: boolean;
  isFlashSale?: boolean;
  fabric?: string;
  gsm?: number;
  score?: number;
  createdAt?: string;
}

export interface SearchFacetBucket {
  value: string;
  label: string;
  count: number;
}

export interface SearchFacets {
  categories: SearchFacetBucket[];
  priceRanges: SearchFacetBucket[];
}

export interface NormalizedSearchResponse {
  items: SearchResultItem[];
  total: number;
  facets: SearchFacets;
  page: number;
  page_size: number;
}

export interface SearchSuggestion {
  text: string;
  kind: "query" | "product" | "category";
  productId?: string;
  slug?: string;
  categorySlug?: string;
  minPriceVnd?: number;
}

export interface NormalizedSuggestResponse {
  suggestions: SearchSuggestion[];
}

export interface SiteSearchBarProps {
  brandName: string;
  shopId?: string;
  initialQuery?: string;
  placeholder?: string;
  /** Gọn hơn cho header desktop hoặc mobile drawer. */
  compact?: boolean;
  /** Gọi sau khi người mua chọn gợi ý hoặc gửi tìm kiếm (vd: đóng mobile drawer). */
  onNavigate?: (query: string) => void;
  className?: string;
}

export interface SiteSearchResultsProps {
  brandName: string;
  shopId: string;
  initialQuery?: string;
  initialCategory?: string;
  initialSort?: SearchSort | string;
  initialMinPrice?: number;
  initialMaxPrice?: number;
  initialPage?: number;
  /** Dữ liệu dự phòng từ bundle của site khi Search service tạm gián đoạn. */
  fallbackProducts?: Product[];
  categories?: CategoryInfo[];
}

export interface PricePreset {
  id: string;
  label: string;
  minPrice?: number;
  maxPrice?: number;
}

export const SEARCH_PRICE_PRESETS: readonly PricePreset[] = [
  { id: "all", label: "Tất cả mức giá" },
  { id: "under-200k", label: "Dưới 200k", maxPrice: 200_000 },
  { id: "200k-400k", label: "200k - 400k", minPrice: 200_000, maxPrice: 400_000 },
  { id: "over-400k", label: "Trên 400k", minPrice: 400_000 },
];

function cleanText(raw: unknown, maxLen: number): string {
  if (typeof raw !== "string") return "";
  return raw.replace(CONTROL_CHAR_RE, " ").replace(/\s+/g, " ").trim().slice(0, maxLen);
}

export function normalizeSearchSort(raw: unknown): SearchSort {
  if (typeof raw === "string") {
    const clean = raw.trim().toLowerCase();
    if ((VALID_SEARCH_SORTS as readonly string[]).includes(clean)) {
      return clean as SearchSort;
    }
  }
  return "relevance";
}

export function parseNonNegativeInt(raw: unknown, max = MAX_PRICE_VND): number | undefined {
  if (raw === null || raw === undefined || raw === "") return undefined;
  if (typeof raw === "number") {
    if (!Number.isFinite(raw) || raw < 0) return undefined;
    return Math.min(max, Math.trunc(raw));
  }
  if (typeof raw !== "string") return undefined;
  const trimmed = raw.trim();
  if (!trimmed || !/^\d+$/.test(trimmed)) return undefined;
  const n = Number.parseInt(trimmed, 10);
  if (!Number.isSafeInteger(n) || n < 0) return undefined;
  return Math.min(max, n);
}

function readParam(
  source: URLSearchParams | Record<string, string | null | undefined>,
  key: string
): string | null {
  if (source instanceof URLSearchParams) {
    return source.get(key);
  }
  const val = source[key];
  return typeof val === "string" ? val : null;
}

/**
 * Parse and sanitize search query parameters. Deliberately ignores any client-supplied `shop_id`
 * because site host isolation always binds `shop_id` from the request Host.
 */
export function parseSearchQueryParams(
  source: URLSearchParams | Record<string, string | null | undefined>
): SearchQueryParams {
  const q = cleanText(readParam(source, "q"), MAX_SEARCH_QUERY_LEN);
  const category = cleanText(
    readParam(source, "category") ?? readParam(source, "cat"),
    100
  );
  const min_price = parseNonNegativeInt(readParam(source, "min_price"));
  const max_price = parseNonNegativeInt(readParam(source, "max_price"));
  const sort = normalizeSearchSort(readParam(source, "sort"));

  const rawPage = parseNonNegativeInt(readParam(source, "page"), 10_000);
  const page = rawPage && rawPage >= 1 ? rawPage : 1;

  const rawPageSize = parseNonNegativeInt(readParam(source, "page_size"), MAX_SEARCH_PAGE_SIZE);
  const page_size =
    rawPageSize && rawPageSize >= 1
      ? Math.min(MAX_SEARCH_PAGE_SIZE, rawPageSize)
      : DEFAULT_SEARCH_PAGE_SIZE;

  return {
    q,
    category,
    ...(min_price !== undefined ? { min_price } : {}),
    ...(max_price !== undefined ? { max_price } : {}),
    sort,
    page,
    page_size,
  };
}

export function parseSuggestQuery(raw: unknown): string {
  return cleanText(raw, MAX_SUGGEST_QUERY_LEN);
}

export function sanitizeSearchImageUrl(raw: unknown): string {
  if (typeof raw !== "string") return "";
  const trimmed = raw.trim();
  if (!trimmed || trimmed.length > 500) return "";
  if (CONTROL_CHAR_RE.test(trimmed) || trimmed.includes("\\")) return "";
  if (trimmed.startsWith("/")) {
    if (trimmed.startsWith("//") || /\s/.test(trimmed)) return "";
    return trimmed;
  }
  try {
    const parsed = new URL(trimmed);
    if (parsed.protocol !== "https:" || !parsed.hostname) return "";
    return parsed.toString();
  } catch {
    return "";
  }
}

function asRecord(val: unknown): Record<string, unknown> | null {
  return val && typeof val === "object" && !Array.isArray(val)
    ? (val as Record<string, unknown>)
    : null;
}

function parseFacetBuckets(raw: unknown): SearchFacetBucket[] {
  if (Array.isArray(raw)) {
    const out: SearchFacetBucket[] = [];
    for (const entry of raw) {
      const obj = asRecord(entry);
      if (!obj) continue;
      const value = cleanText(obj.value ?? obj.slug ?? obj.key ?? obj.id, 100);
      if (!value) continue;
      const label = cleanText(obj.label ?? obj.name ?? value, 120) || value;
      const count = Math.max(0, Math.trunc(Number(obj.count ?? 0) || 0));
      out.push({ value, label, count });
    }
    return out;
  }
  const rec = asRecord(raw);
  if (rec) {
    const out: SearchFacetBucket[] = [];
    for (const [k, v] of Object.entries(rec)) {
      const value = cleanText(k, 100);
      if (!value) continue;
      const count = Math.max(0, Math.trunc(Number(v ?? 0) || 0));
      out.push({ value, label: value, count });
    }
    return out;
  }
  return [];
}

function normalizeSearchItem(raw: unknown): SearchResultItem | null {
  const obj = asRecord(raw);
  if (!obj) return null;
  const id = cleanText(obj.id ?? obj.product_id, 128);
  const name = cleanText(obj.name ?? obj.title, 240);
  if (!id || !name) return null;

  const slug = cleanText(obj.slug ?? obj.product_slug, 200) || id;
  const shopId = cleanText(obj.shop_id ?? obj.shopId, 128);
  const description = cleanText(obj.description, 500);
  const categorySlug = cleanText(
    obj.category_slug ?? obj.categorySlug ?? obj.category_id ?? obj.category,
    100
  );
  const categoryName = cleanText(obj.category_name ?? obj.categoryName, 120);

  const rawPrice = Number(
    obj.min_price_vnd ?? obj.price_vnd ?? obj.price ?? obj.minPriceVnd ?? 0
  );
  const priceVnd = Number.isFinite(rawPrice) && rawPrice > 0 ? Math.trunc(rawPrice) : 0;

  const rawMaxPrice = Number(obj.max_price_vnd ?? obj.maxPriceVnd ?? 0);
  const maxPriceVnd =
    Number.isFinite(rawMaxPrice) && rawMaxPrice > priceVnd ? Math.trunc(rawMaxPrice) : undefined;

  const rawOriginal = Number(obj.original_price_vnd ?? obj.originalPrice ?? 0);
  const originalPriceVnd =
    Number.isFinite(rawOriginal) && rawOriginal > priceVnd ? Math.trunc(rawOriginal) : undefined;

  const imageCandidates: string[] = [];
  if (typeof obj.on_model_image === "string") imageCandidates.push(obj.on_model_image);
  if (typeof obj.image === "string") imageCandidates.push(obj.image);
  if (Array.isArray(obj.images)) {
    for (const img of obj.images) {
      if (typeof img === "string") imageCandidates.push(img);
    }
  }
  const images = Array.from(
    new Set(imageCandidates.map(sanitizeSearchImageUrl).filter(Boolean))
  );
  const image = images[0] ?? "";

  const rawStock = Number(obj.total_stock ?? obj.stock ?? NaN);
  const inStock =
    typeof obj.in_stock === "boolean"
      ? obj.in_stock
      : typeof obj.inStock === "boolean"
        ? obj.inStock
        : Number.isFinite(rawStock)
          ? rawStock > 0
          : true;
  const totalStock = Number.isFinite(rawStock) && rawStock >= 0 ? Math.trunc(rawStock) : inStock ? 1 : 0;

  const rawGsm = Number(obj.gsm ?? 0);
  const gsm = Number.isFinite(rawGsm) && rawGsm > 0 ? Math.trunc(rawGsm) : undefined;
  const fabric = cleanText(obj.fabric, 140) || undefined;
  const rawScore = Number(obj.score ?? NaN);
  const score = Number.isFinite(rawScore) ? rawScore : undefined;
  const createdAt = cleanText(obj.created_at ?? obj.createdAt, 64) || undefined;
  const isFlashSale = Boolean(obj.is_flash_sale ?? obj.isFlashSale);

  return {
    id,
    slug,
    shopId,
    name,
    description,
    categorySlug,
    categoryName,
    priceVnd,
    ...(maxPriceVnd !== undefined ? { maxPriceVnd } : {}),
    ...(originalPriceVnd !== undefined ? { originalPriceVnd } : {}),
    image,
    images,
    totalStock,
    inStock,
    ...(isFlashSale ? { isFlashSale } : {}),
    ...(fabric ? { fabric } : {}),
    ...(gsm ? { gsm } : {}),
    ...(score !== undefined ? { score } : {}),
    ...(createdAt ? { createdAt } : {}),
  };
}

export function normalizeSearchResponse(
  raw: unknown,
  defaults: { page?: number; page_size?: number; shopId?: string } = {}
): NormalizedSearchResponse {
  const fallbackPage = Math.max(1, Math.trunc(defaults.page ?? 1));
  const fallbackPageSize = Math.max(
    1,
    Math.min(MAX_SEARCH_PAGE_SIZE, Math.trunc(defaults.page_size ?? DEFAULT_SEARCH_PAGE_SIZE))
  );
  const obj = asRecord(raw);
  if (!obj) {
    return {
      items: [],
      total: 0,
      facets: { categories: [], priceRanges: [] },
      page: fallbackPage,
      page_size: fallbackPageSize,
    };
  }

  const rawItems = Array.isArray(obj.items)
    ? obj.items
    : Array.isArray(obj.hits)
      ? obj.hits
      : Array.isArray(obj.products)
        ? obj.products
        : [];

  let items = rawItems
    .map(normalizeSearchItem)
    .filter((it): it is SearchResultItem => it !== null);

  if (defaults.shopId) {
    const requiredShop = defaults.shopId;
    items = items.filter((it) => !it.shopId || it.shopId === requiredShop);
  }

  const rawFacets = asRecord(obj.facets);
  const facets: SearchFacets = {
    categories: parseFacetBuckets(
      rawFacets?.categories ?? rawFacets?.category ?? obj.category_facets
    ),
    priceRanges: parseFacetBuckets(rawFacets?.price_ranges ?? rawFacets?.priceRanges),
  };

  const rawTotal = Number(obj.total ?? obj.estimatedTotalHits ?? NaN);
  const total =
    Number.isFinite(rawTotal) && rawTotal >= 0
      ? Math.max(items.length, Math.trunc(rawTotal))
      : items.length;

  const rawPage = Number(obj.page ?? NaN);
  const page = Number.isFinite(rawPage) && rawPage >= 1 ? Math.trunc(rawPage) : fallbackPage;

  const rawPageSize = Number(obj.page_size ?? obj.pageSize ?? NaN);
  const page_size =
    Number.isFinite(rawPageSize) && rawPageSize >= 1
      ? Math.min(MAX_SEARCH_PAGE_SIZE, Math.trunc(rawPageSize))
      : fallbackPageSize;

  return { items, total, facets, page, page_size };
}

export function normalizeSuggestResponse(raw: unknown): NormalizedSuggestResponse {
  const obj = asRecord(raw);
  const list = Array.isArray(raw)
    ? raw
    : Array.isArray(obj?.suggestions)
      ? obj.suggestions
      : Array.isArray(obj?.items)
        ? obj.items
        : [];

  const seen = new Set<string>();
  const suggestions: SearchSuggestion[] = [];

  for (const entry of list) {
    let text = "";
    let kind: SearchSuggestion["kind"] = "query";
    let productId: string | undefined;
    let slug: string | undefined;
    let categorySlug: string | undefined;
    let minPriceVnd: number | undefined;

    if (typeof entry === "string") {
      text = cleanText(entry, MAX_SUGGEST_QUERY_LEN);
    } else {
      const rec = asRecord(entry);
      if (!rec) continue;
      text = cleanText(rec.text ?? rec.query ?? rec.label ?? rec.name, MAX_SUGGEST_QUERY_LEN);
      const rawKind = cleanText(rec.kind ?? rec.type, 32).toLowerCase();
      if (rawKind === "product" || rawKind === "category" || rawKind === "query") {
        kind = rawKind;
      }
      const pid = cleanText(rec.product_id ?? rec.productId, 128);
      if (pid) productId = pid;
      const sl = cleanText(rec.slug ?? rec.product_slug, 200);
      if (sl) slug = sl;
      const csl = cleanText(rec.category_slug ?? rec.categorySlug, 100);
      if (csl) categorySlug = csl;
      const price = parseNonNegativeInt(rec.min_price_vnd ?? rec.price_vnd ?? rec.minPriceVnd);
      if (price && price > 0) minPriceVnd = price;
    }

    if (!text) continue;
    const dedupeKey = text.toLowerCase();
    if (seen.has(dedupeKey)) continue;
    seen.add(dedupeKey);

    suggestions.push({
      text,
      kind,
      ...(productId ? { productId } : {}),
      ...(slug ? { slug } : {}),
      ...(categorySlug ? { categorySlug } : {}),
      ...(minPriceVnd !== undefined ? { minPriceVnd } : {}),
    });

    if (suggestions.length >= 8) break;
  }

  return { suggestions };
}

export function buildSearchFilterUrl(params: {
  q?: string;
  category?: string;
  minPrice?: number;
  maxPrice?: number;
  sort?: SearchSort;
  page?: number;
}): string {
  const sp = new URLSearchParams();
  const q = cleanText(params.q, MAX_SEARCH_QUERY_LEN);
  const category = cleanText(params.category, 100);
  if (q) sp.set("q", q);
  if (category) sp.set("category", category);
  if (params.minPrice !== undefined && Number.isFinite(params.minPrice) && params.minPrice >= 0) {
    sp.set("min_price", String(Math.trunc(params.minPrice)));
  }
  if (params.maxPrice !== undefined && Number.isFinite(params.maxPrice) && params.maxPrice >= 0) {
    sp.set("max_price", String(Math.trunc(params.maxPrice)));
  }
  if (params.sort && params.sort !== "relevance") {
    sp.set("sort", params.sort);
  }
  if (params.page && params.page > 1) {
    sp.set("page", String(Math.trunc(params.page)));
  }
  const qs = sp.toString();
  return qs ? `/tim-kiem?${qs}` : "/tim-kiem";
}

export function productToSearchItem(product: Product): SearchResultItem {
  const totalStock = (product.skus ?? []).reduce((sum, sku) => sum + Math.max(0, sku.stock), 0);
  const images = Array.from(
    new Set(
      [product.onModelImage, product.image, ...(product.gallery ?? [])]
        .map(sanitizeSearchImageUrl)
        .filter(Boolean)
    )
  );
  return {
    id: product.backendId || product.id,
    slug: product.slug,
    shopId: product.shopId,
    name: product.name,
    description: product.description,
    categorySlug: product.categorySlug,
    categoryName: product.categoryName,
    priceVnd: Math.max(0, Math.trunc(product.price)),
    ...(product.originalPrice && product.originalPrice > product.price
      ? { originalPriceVnd: Math.trunc(product.originalPrice) }
      : {}),
    image: images[0] ?? "",
    images,
    totalStock,
    inStock: totalStock > 0,
    ...(product.isFlashSale ? { isFlashSale: true } : {}),
    ...(product.fabric ? { fabric: product.fabric } : {}),
    ...(product.gsm > 0 ? { gsm: product.gsm } : {}),
    ...(product.createdAt ? { createdAt: product.createdAt } : {}),
  };
}

export function runFallbackProductSearch(
  products: Product[],
  params: SearchQueryParams,
  searchFn: (query: string, catalog: Product[]) => Product[]
): NormalizedSearchResponse {
  const byQuery = params.q ? searchFn(params.q, products) : [...products];

  const byPrice = byQuery.filter((p) => {
    if (params.min_price !== undefined && p.price < params.min_price) return false;
    if (params.max_price !== undefined && p.price > params.max_price) return false;
    return true;
  });

  // Compute category facets before applying the active category filter so users can switch categories.
  const catCounts = new Map<string, { label: string; count: number }>();
  for (const p of byPrice) {
    const existing = catCounts.get(p.categorySlug);
    if (existing) {
      existing.count += 1;
    } else {
      catCounts.set(p.categorySlug, { label: p.categoryName || p.categorySlug, count: 1 });
    }
  }
  const categoryFacets: SearchFacetBucket[] = Array.from(catCounts.entries()).map(
    ([value, info]) => ({
      value,
      label: info.label,
      count: info.count,
    })
  );

  const filtered = params.category
    ? byPrice.filter((p) => p.categorySlug === params.category)
    : byPrice;

  const sorted = [...filtered];
  if (params.sort === "price_asc") {
    sorted.sort((a, b) => a.price - b.price);
  } else if (params.sort === "price_desc") {
    sorted.sort((a, b) => b.price - a.price);
  } else if (params.sort === "newest") {
    sorted.sort((a, b) => {
      const ta = a.createdAt ? Date.parse(a.createdAt) || 0 : 0;
      const tb = b.createdAt ? Date.parse(b.createdAt) || 0 : 0;
      return tb - ta;
    });
  }

  const total = sorted.length;
  const pageSize = Math.max(1, Math.min(MAX_SEARCH_PAGE_SIZE, params.page_size));
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const safePage = Math.max(1, Math.min(params.page, totalPages));
  const start = (safePage - 1) * pageSize;
  const items = sorted.slice(start, start + pageSize).map(productToSearchItem);

  return {
    items,
    total,
    facets: {
      categories: categoryFacets,
      priceRanges: [],
    },
    page: safePage,
    page_size: pageSize,
  };
}

export function recentSearchStorageKey(shopId: string | undefined, brandName: string): string {
  const scope = (shopId || brandName || "site").trim().toLowerCase();
  return `nm_recent_search:${scope}`;
}

export function parseRecentSearches(rawJson: string | null): string[] {
  if (!rawJson) return [];
  try {
    const parsed = JSON.parse(rawJson);
    if (!Array.isArray(parsed)) return [];
    const seen = new Set<string>();
    const out: string[] = [];
    for (const item of parsed) {
      const text = cleanText(item, MAX_SUGGEST_QUERY_LEN);
      if (!text) continue;
      const key = text.toLowerCase();
      if (seen.has(key)) continue;
      seen.add(key);
      out.push(text);
      if (out.length >= MAX_RECENT_SEARCHES) break;
    }
    return out;
  } catch {
    return [];
  }
}

export function addRecentSearch(
  existing: string[],
  query: string,
  maxItems = MAX_RECENT_SEARCHES
): string[] {
  const clean = cleanText(query, MAX_SUGGEST_QUERY_LEN);
  if (!clean) return existing.slice(0, maxItems);
  const lower = clean.toLowerCase();
  const rest = existing.filter((entry) => entry.toLowerCase() !== lower);
  return [clean, ...rest].slice(0, maxItems);
}
