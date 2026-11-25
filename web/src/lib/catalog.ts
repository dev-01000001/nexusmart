import type {
  GatewayActiveShopDiscount,
  GatewayCategory,
  GatewayFlashSale,
  GatewayProduct,
  GatewayProductReviewsResponse,
  GatewayProductSlugResponse,
  GatewayProvince,
  GatewayPublicShop,
  GatewaySearchFacetCount,
  GatewaySearchProductsResponse,
  GatewaySearchSuggestResponse,
  GatewayVoucher,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { GatewayError, gatewayFetch } from "@/lib/server/gateway";
import {
  buildActiveShopDiscountsMap,
  buildLiveFlashItemsBySkuId,
  collectProductDetailDiscountSkuIds,
  fetchProductsForFlashSalesWithFetcher,
  mapGatewayCategory,
  mapGatewayFlashSales,
  mapGatewayProduct,
  mapGatewayShop,
  mapGatewayVoucher,
  resolvePriceBucketRange,
} from "@/lib/catalog-mapper";
import type {
  CatalogCategory,
  CatalogFlashSaleItem,
  CatalogFlashSaleSlot,
  CatalogProduct,
  CatalogShop,
  CatalogVoucher,
} from "@/lib/catalog-types";
import {
  CATALOG_PAGE_SIZE,
  buildSearchQueryString,
  collectSearchHitSkuIds,
  mapSearchHitToCatalogProduct,
  normalizeColorFilterParam,
  normalizeSearchSortParam,
  parseInStockParam,
  parseMinRatingX100Param,
  parsePageParam,
} from "@/lib/search-client";
import { DEFAULT_SHOP_LOOKUP_CONCURRENCY, lookupShopsByIdsWithFetcher } from "@/lib/shop-lookup";
import { getLegacyShopSlugById } from "@/mock/productEditorial";
import { fetchActiveShopDiscountsWithFetcher } from "@/features/checkout/helpers";

const UNAVAILABLE_MESSAGE = "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút.";
const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function toSafeErrorMessage(err: unknown): string {
  if (err instanceof GatewayError && err.message) {
    return err.message;
  }
  return UNAVAILABLE_MESSAGE;
}

/**
 * Calls a public or optional-auth gateway endpoint with `cache: "no-store"`.
 * When `optionalAuth` is true and an expired cookie triggers 401, retries anonymously
 * so public storefront browsing never fails due to a stale session cookie.
 */
async function publicGatewayFetch<T>(
  path: string,
  options: { optionalAuth?: boolean } = {}
): Promise<T> {
  if (!options.optionalAuth) {
    return gatewayFetch<T>(path, { token: false, cache: "no-store" });
  }
  try {
    return await gatewayFetch<T>(path, { cache: "no-store" });
  } catch (err) {
    if (err instanceof GatewayError && err.status === 401) {
      return gatewayFetch<T>(path, { token: false, cache: "no-store" });
    }
    throw err;
  }
}

export interface ProductQueryParams {
  categoryId?: string;
  categorySlug?: string;
  shopId?: string;
  shopSlug?: string;
  keyword?: string;
  minPriceVnd?: number;
  maxPriceVnd?: number;
  sortBy?: string;
  skuIds?: string[];
  page?: number;
  pageSize?: number;
}

export async function fetchCategories(): Promise<CatalogCategory[]> {
  const res = await publicGatewayFetch<{ categories: GatewayCategory[] }>("/categories");
  const list = Array.isArray(res?.categories) ? res.categories : [];
  return list
    .map(mapGatewayCategory)
    .sort((a, b) => a.sortOrder - b.sortOrder || a.name.localeCompare(b.name));
}

export async function fetchPublicShops(): Promise<CatalogShop[]> {
  const res = await publicGatewayFetch<PaginatedResponse<GatewayPublicShop>>(
    "/shops?page=1&page_size=100"
  );
  const items = Array.isArray(res?.items) ? res.items : [];
  return items.map(mapGatewayShop);
}

export async function fetchShopBySlug(slug: string): Promise<CatalogShop | null> {
  try {
    const res = await publicGatewayFetch<{ shop: GatewayPublicShop }>(
      `/shops/slug/${encodeURIComponent(slug)}`
    );
    return res?.shop ? mapGatewayShop(res.shop) : null;
  } catch (err) {
    if (err instanceof GatewayError && err.status === 404) {
      return null;
    }
    throw err;
  }
}

export async function fetchShopById(id: string): Promise<CatalogShop | null> {
  try {
    const res = await publicGatewayFetch<{ shop: GatewayPublicShop }>(
      `/shops/${encodeURIComponent(id)}`
    );
    return res?.shop ? mapGatewayShop(res.shop) : null;
  } catch (err) {
    if (err instanceof GatewayError && err.status === 404) {
      return null;
    }
    throw err;
  }
}

async function fetchRawPublicShopById(id: string): Promise<GatewayPublicShop | null> {
  try {
    const res = await publicGatewayFetch<{ shop: GatewayPublicShop }>(
      `/shops/${encodeURIComponent(id)}`
    );
    return res?.shop ?? null;
  } catch {
    return null;
  }
}

async function hydrateMissingShops(
  shopsById: Map<string, CatalogShop>,
  shopIds: readonly string[]
): Promise<void> {
  const missingIds = Array.from(
    new Set(shopIds.map((id) => (id ?? "").trim()).filter((id) => id && !shopsById.has(id)))
  );
  if (missingIds.length === 0) return;
  const resolved = await lookupShopsByIdsWithFetcher(missingIds, fetchRawPublicShopById, {
    concurrency: DEFAULT_SHOP_LOOKUP_CONCURRENCY,
  });
  for (const shop of Object.values(resolved)) {
    shopsById.set(shop.id, mapGatewayShop(shop));
  }
}

export async function fetchRawProducts(
  params: ProductQueryParams = {}
): Promise<PaginatedResponse<GatewayProduct>> {
  const qs = new URLSearchParams();
  if (params.categoryId) qs.set("category_id", params.categoryId);
  if (params.categorySlug) qs.set("category_slug", params.categorySlug);
  if (params.shopId) qs.set("shop_id", params.shopId);
  if (params.shopSlug) qs.set("shop_slug", params.shopSlug);
  if (params.keyword) qs.set("keyword", params.keyword);
  if (params.minPriceVnd && params.minPriceVnd > 0) {
    qs.set("min_price_vnd", String(Math.trunc(params.minPriceVnd)));
  }
  if (params.maxPriceVnd && params.maxPriceVnd > 0) {
    qs.set("max_price_vnd", String(Math.trunc(params.maxPriceVnd)));
  }
  if (params.sortBy) qs.set("sort_by", params.sortBy);
  if (params.skuIds && params.skuIds.length > 0) {
    const cleanSkuIds = Array.from(new Set(params.skuIds.map((id) => id.trim()).filter(Boolean)));
    if (cleanSkuIds.length > 0) {
      qs.set("sku_ids", cleanSkuIds.join(","));
    }
  }
  qs.set("page", String(params.page ?? 1));
  qs.set("page_size", String(params.pageSize ?? 100));

  const queryStr = qs.toString();
  const res = await publicGatewayFetch<PaginatedResponse<GatewayProduct>>(
    `/products${queryStr ? `?${queryStr}` : ""}`,
    { optionalAuth: true }
  );
  return {
    items: Array.isArray(res?.items) ? res.items : [],
    page: res?.page ?? 1,
    page_size: res?.page_size ?? 100,
    total: res?.total ?? 0,
  };
}

export type ProductSlugLookupResult =
  | { kind: "active"; product: GatewayProduct }
  | { kind: "discontinued"; categoryId: string; name: string }
  | { kind: "not_found" };

export async function fetchProductSlugLookup(slug: string): Promise<ProductSlugLookupResult> {
  try {
    const res = await publicGatewayFetch<GatewayProductSlugResponse>(
      `/products/slug/${encodeURIComponent(slug)}`,
      { optionalAuth: true }
    );
    if (res?.discontinued) {
      return {
        kind: "discontinued",
        categoryId: (res.category_id ?? "").trim(),
        name: (res.name ?? "").trim(),
      };
    }
    if (res?.product) {
      return { kind: "active", product: res.product };
    }
    return { kind: "not_found" };
  } catch (err) {
    if (err instanceof GatewayError && err.status === 404) {
      return { kind: "not_found" };
    }
    throw err;
  }
}

export async function fetchRawProductBySlug(slug: string): Promise<GatewayProduct | null> {
  const res = await fetchProductSlugLookup(slug);
  return res.kind === "active" ? res.product : null;
}

export async function fetchProductReviews(
  productId: string,
  pageSize = 10
): Promise<GatewayProductReviewsResponse | null> {
  try {
    return await publicGatewayFetch<GatewayProductReviewsResponse>(
      `/products/${encodeURIComponent(productId)}/reviews?page=1&page_size=${Math.max(1, Math.min(50, Math.trunc(pageSize)))}`
    );
  } catch {
    return null;
  }
}

export async function fetchVouchers(params?: {
  shopId?: string;
  scope?: "PLATFORM" | "SHOP" | "FREESHIP";
  orderAmountVnd?: number;
}): Promise<CatalogVoucher[]> {
  const qs = new URLSearchParams();
  if (params?.shopId) qs.set("shop_id", params.shopId);
  if (params?.scope) qs.set("scope", params.scope);
  if (params?.orderAmountVnd && params.orderAmountVnd > 0) {
    qs.set("order_amount_vnd", String(Math.trunc(params.orderAmountVnd)));
  }
  const queryStr = qs.toString();
  const res = await publicGatewayFetch<{ vouchers: GatewayVoucher[] }>(
    `/vouchers${queryStr ? `?${queryStr}` : ""}`,
    { optionalAuth: true }
  );
  const list = Array.isArray(res?.vouchers) ? res.vouchers : [];
  return list.map(mapGatewayVoucher);
}

export async function fetchRawFlashSales(includeUpcoming = true): Promise<GatewayFlashSale[]> {
  const qs = includeUpcoming ? "?include_upcoming=true" : "";
  const res = await publicGatewayFetch<{ flash_sales: GatewayFlashSale[] }>(`/flash-sales${qs}`);
  return Array.isArray(res?.flash_sales) ? res.flash_sales : [];
}

export async function fetchActiveShopDiscounts(params?: {
  skuIds?: string[];
  shopId?: string;
}): Promise<GatewayActiveShopDiscount[]> {
  return fetchActiveShopDiscountsWithFetcher(params, async (batch) => {
    const qs = new URLSearchParams();
    if (batch.skuIds && batch.skuIds.length > 0) {
      qs.set("sku_ids", batch.skuIds.join(","));
    }
    if (batch.shopId) {
      qs.set("shop_id", batch.shopId);
    }
    const res = await publicGatewayFetch<{ discounts?: GatewayActiveShopDiscount[] }>(
      `/shop-discounts/active?${qs.toString()}`
    );
    return Array.isArray(res?.discounts) ? res.discounts : [];
  });
}

function collectProductSkuIds(products: GatewayProduct[]): string[] {
  const ids: string[] = [];
  for (const p of products) {
    for (const s of p.skus ?? []) {
      if (s.id) ids.push(s.id);
    }
  }
  return ids;
}

export async function fetchShippingProvinces(): Promise<GatewayProvince[]> {
  try {
    const res = await publicGatewayFetch<{ provinces: GatewayProvince[] }>("/shipping/provinces");
    return Array.isArray(res?.provinces) ? res.provinces : [];
  } catch {
    return [];
  }
}

function buildLiveFlashSkuSet(rawSales: GatewayFlashSale[], nowMs: number): Set<string> {
  return new Set(buildLiveFlashItemsBySkuId(rawSales, nowMs).keys());
}

export interface HomeCatalogData {
  categories: CatalogCategory[];
  shops: CatalogShop[];
  products: CatalogProduct[];
  totalProducts: number;
  flashSlots: CatalogFlashSaleSlot[];
  serverNowMs: number;
  error: string | null;
}

export async function loadHomeCatalogData(): Promise<HomeCatalogData> {
  const serverNowMs = Date.now();
  try {
    const [categories, shops, productPage, rawFlashSales] = await Promise.all([
      fetchCategories(),
      fetchPublicShops(),
      fetchRawProducts({ page: 1, pageSize: 100 }),
      fetchRawFlashSales(true).catch(() => [] as GatewayFlashSale[]),
    ]);
    const mergedRawProducts = await fetchProductsForFlashSalesWithFetcher(
      rawFlashSales,
      productPage.items,
      async (batchSkuIds) => {
        const res = await fetchRawProducts({
          skuIds: batchSkuIds,
          page: 1,
          pageSize: 100,
        });
        return res.items;
      }
    ).catch(() => {
      console.warn(
        "[catalog] home flash-sale extra product lookup failed; falling back to top-100"
      );
      return productPage.items;
    });

    const homeSkuIds = collectProductSkuIds(mergedRawProducts);
    const categoriesById = new Map(categories.map((c) => [c.id, c]));
    const shopsById = new Map(shops.map((s) => [s.id, s]));

    const [activeShopDiscounts] = await Promise.all([
      homeSkuIds.length > 0
        ? fetchActiveShopDiscounts({ skuIds: homeSkuIds }).catch(
            () => [] as GatewayActiveShopDiscount[]
          )
        : Promise.resolve([] as GatewayActiveShopDiscount[]),
      hydrateMissingShops(
        shopsById,
        mergedRawProducts.map((p) => p.shop_id)
      ),
    ]);

    const liveFlashItemsBySkuId = buildLiveFlashItemsBySkuId(rawFlashSales, serverNowMs);
    const liveFlashSkuIds = new Set(liveFlashItemsBySkuId.keys());
    const activeShopDiscountsBySkuId = buildActiveShopDiscountsMap(activeShopDiscounts);

    const products = mergedRawProducts.map((p) =>
      mapGatewayProduct(p, {
        categoriesById,
        shopsById,
        flashSkuIds: liveFlashSkuIds,
        liveFlashItemsBySkuId,
        activeShopDiscountsBySkuId,
      })
    );
    const totalProducts = Math.max(products.length, Math.trunc(productPage.total ?? 0));
    const flashSlots = mapGatewayFlashSales(rawFlashSales, products, serverNowMs);

    // Order shops so workshops with live catalog products appear first
    const countByShop = new Map<string, number>();
    for (const p of products) {
      countByShop.set(p.shopId, (countByShop.get(p.shopId) ?? 0) + 1);
    }
    const orderedShops = [...shops].sort((a, b) => {
      const diff = (countByShop.get(b.id) ?? 0) - (countByShop.get(a.id) ?? 0);
      if (diff !== 0) return diff;
      return Number(b.hasEditorialProfile) - Number(a.hasEditorialProfile);
    });

    return {
      categories,
      shops: orderedShops,
      products,
      totalProducts,
      flashSlots,
      serverNowMs,
      error: null,
    };
  } catch (err) {
    return {
      categories: [],
      shops: [],
      products: [],
      totalProducts: 0,
      flashSlots: [],
      serverNowMs,
      error: toSafeErrorMessage(err),
    };
  }
}

export interface CatalogExplorerServerData {
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
  usedVector: boolean;
  trendingKeywords: string[];
  error: string | null;
}

function readSingleParam(val: string | string[] | undefined): string {
  if (Array.isArray(val)) return val[0] ?? "";
  return val ?? "";
}

export async function loadCatalogExplorerData(
  rawSearchParams: Record<string, string | string[] | undefined>
): Promise<CatalogExplorerServerData> {
  const catParam = readSingleParam(rawSearchParams.cat).trim();
  const shopParam = readSingleParam(rawSearchParams.shop).trim();
  const priceParam = readSingleParam(rawSearchParams.price).trim();
  const qParam = readSingleParam(rawSearchParams.q).trim();
  const sortParam = readSingleParam(rawSearchParams.sort).trim();
  const ratingParam =
    readSingleParam(rawSearchParams.min_rating).trim() ||
    readSingleParam(rawSearchParams.rating).trim();
  const inStockParam = readSingleParam(rawSearchParams.in_stock).trim();
  const sizeParam = readSingleParam(rawSearchParams.size).trim();
  const colorParam = readSingleParam(rawSearchParams.color).trim();
  const modeParam = readSingleParam(rawSearchParams.mode).trim();
  const page = parsePageParam(readSingleParam(rawSearchParams.page));
  const rawPageSize = Number.parseInt(readSingleParam(rawSearchParams.page_size), 10);
  const pageSize =
    Number.isFinite(rawPageSize) && rawPageSize >= 1 && rawPageSize <= 100
      ? rawPageSize
      : CATALOG_PAGE_SIZE;

  const priceRange = resolvePriceBucketRange(priceParam);
  const sortBy = normalizeSearchSortParam(sortParam);
  const minRatingX100 = parseMinRatingX100Param(ratingParam);
  const inStockOnly = parseInStockParam(inStockParam);
  const normalizedColor = normalizeColorFilterParam(colorParam);

  let shopId: string | undefined;
  let shopSlug: string | undefined;
  if (shopParam) {
    if (UUID_PATTERN.test(shopParam)) {
      shopId = shopParam;
    } else {
      shopSlug = getLegacyShopSlugById(shopParam) ?? shopParam;
    }
  }

  const searchQs = buildSearchQueryString({
    q: qParam || undefined,
    categorySlug: catParam || undefined,
    shopId,
    shopSlug,
    minPriceVnd: priceRange.minPriceVnd,
    maxPriceVnd: priceRange.maxPriceVnd,
    minRatingX100,
    inStockOnly,
    sortBy,
    mode: modeParam || undefined,
    size: sizeParam || undefined,
    color: normalizedColor,
    page,
    pageSize,
  });

  try {
    const [searchRes, categories, rawFlashSales, suggestRes, activeShopLookup] = await Promise.all([
      publicGatewayFetch<GatewaySearchProductsResponse>(`/search?${searchQs}`, {
        optionalAuth: true,
      }),
      fetchCategories().catch(() => [] as CatalogCategory[]),
      fetchRawFlashSales(false).catch(() => [] as GatewayFlashSale[]),
      publicGatewayFetch<GatewaySearchSuggestResponse>("/search/suggest?limit=8").catch(() => null),
      shopId
        ? fetchShopById(shopId).catch(() => null)
        : shopSlug
          ? fetchShopBySlug(shopSlug).catch(() => null)
          : Promise.resolve(null),
    ]);

    const hits = Array.isArray(searchRes?.items) ? searchRes.items : [];
    const categoryFacets = Array.isArray(searchRes?.category_facets)
      ? searchRes.category_facets
      : [];
    const shopFacets = Array.isArray(searchRes?.shop_facets) ? searchRes.shop_facets : [];
    const trendingKeywords = Array.isArray(suggestRes?.trending) ? suggestRes.trending : [];

    // Seed shopsById from server shop_facets + active shop filter, then hydrate any missing shop_ids
    // (especially when degraded_mode === "product_fallback" or shop 101+ omits shop_name).
    const shopsById = new Map<string, CatalogShop>();
    for (const sf of shopFacets) {
      if (sf.id && sf.name) {
        shopsById.set(
          sf.id,
          mapGatewayShop({
            id: sf.id,
            name: sf.name,
            slug: sf.slug || sf.id,
            description: "",
            status: "ACTIVE",
          })
        );
      }
    }
    for (const hit of hits) {
      if (hit.shop_id && hit.shop_name && !shopsById.has(hit.shop_id)) {
        shopsById.set(
          hit.shop_id,
          mapGatewayShop({
            id: hit.shop_id,
            name: hit.shop_name,
            slug: hit.shop_slug || hit.shop_id,
            description: "",
            status: "ACTIVE",
          })
        );
      }
    }
    if (activeShopLookup) {
      shopsById.set(activeShopLookup.id, activeShopLookup);
    }

    const pageSkuIds = collectSearchHitSkuIds(hits);
    const missingShopIds = Array.from(
      new Set(
        [...hits.map((h) => h.shop_id), ...shopFacets.map((sf) => sf.id)].filter(
          (id) => id && !shopsById.has(id)
        )
      )
    );

    const [activeShopDiscounts] = await Promise.all([
      pageSkuIds.length > 0
        ? fetchActiveShopDiscounts({ skuIds: pageSkuIds }).catch(
            () => [] as GatewayActiveShopDiscount[]
          )
        : Promise.resolve([] as GatewayActiveShopDiscount[]),
      hydrateMissingShops(shopsById, missingShopIds),
    ]);

    const nowMs = Date.now();
    const categoriesById = new Map(categories.map((c) => [c.id, c]));
    const liveFlashItemsBySkuId = buildLiveFlashItemsBySkuId(rawFlashSales, nowMs);
    const activeShopDiscountsBySkuId = buildActiveShopDiscountsMap(activeShopDiscounts);

    const products = hits.map((hit) =>
      mapSearchHitToCatalogProduct(hit, {
        categoriesById,
        shopsById,
        liveFlashItemsBySkuId,
        activeShopDiscountsBySkuId,
      })
    );

    const serverTotal = Math.max(products.length, Math.trunc(searchRes?.total ?? products.length));
    const orderedShops = Array.from(shopsById.values());

    return {
      categories,
      shops: orderedShops,
      products,
      serverTotal,
      page: Math.max(1, Math.trunc(searchRes?.page ?? page)),
      pageSize: Math.max(1, Math.trunc(searchRes?.page_size ?? pageSize)),
      categoryFacets,
      shopFacets,
      didYouMean: typeof searchRes?.did_you_mean === "string" ? searchRes.did_you_mean : "",
      degradedMode: typeof searchRes?.degraded_mode === "string" ? searchRes.degraded_mode : "",
      usedVector: Boolean(searchRes?.used_vector),
      trendingKeywords,
      error: null,
    };
  } catch (err) {
    return {
      categories: [],
      shops: [],
      products: [],
      serverTotal: 0,
      page,
      pageSize,
      categoryFacets: [],
      shopFacets: [],
      didYouMean: "",
      degradedMode: "",
      usedVector: false,
      trendingKeywords: [],
      error: toSafeErrorMessage(err),
    };
  }
}

export interface DiscontinuedProductNotice {
  categoryId: string;
  name: string;
  categoryName: string;
  categorySlug: string;
}

export interface ProductDetailData {
  notFound: boolean;
  discontinued: DiscontinuedProductNotice | null;
  error: string | null;
  product: CatalogProduct | null;
  shop: CatalogShop | null;
  initialReviews: GatewayProductReviewsResponse | null;
  shopVouchers: CatalogVoucher[];
  platformVouchers: CatalogVoucher[];
  sameShopProducts: CatalogProduct[];
  similarProducts: CatalogProduct[];
  bundleProducts: CatalogProduct[];
  allProducts: CatalogProduct[];
  liveFlashBySkuId: Record<string, CatalogFlashSaleItem>;
  activeShopDiscountBySkuId: Record<string, GatewayActiveShopDiscount>;
  provinces: GatewayProvince[];
}

export async function loadProductDetailData(slug: string): Promise<ProductDetailData> {
  const empty: ProductDetailData = {
    notFound: false,
    discontinued: null,
    error: null,
    product: null,
    shop: null,
    initialReviews: null,
    shopVouchers: [],
    platformVouchers: [],
    sameShopProducts: [],
    similarProducts: [],
    bundleProducts: [],
    allProducts: [],
    liveFlashBySkuId: {},
    activeShopDiscountBySkuId: {},
    provinces: [],
  };

  try {
    const lookup = await fetchProductSlugLookup(slug);
    if (lookup.kind === "not_found") {
      return { ...empty, notFound: true };
    }

    const nowMs = Date.now();
    if (lookup.kind === "discontinued") {
      const [categories, publicShops, catProductsPage, rawFlashSales] = await Promise.all([
        fetchCategories().catch(() => [] as CatalogCategory[]),
        fetchPublicShops().catch(() => [] as CatalogShop[]),
        lookup.categoryId
          ? fetchRawProducts({ categoryId: lookup.categoryId, page: 1, pageSize: 8 }).catch(() => ({
              items: [] as GatewayProduct[],
              page: 1,
              page_size: 8,
              total: 0,
            }))
          : Promise.resolve({ items: [] as GatewayProduct[], page: 1, page_size: 8, total: 0 }),
        fetchRawFlashSales(true).catch(() => [] as GatewayFlashSale[]),
      ]);

      const rawCandidates =
        catProductsPage.items.length > 0
          ? catProductsPage.items
          : (
              await fetchRawProducts({ page: 1, pageSize: 8 }).catch(() => ({
                items: [] as GatewayProduct[],
                page: 1,
                page_size: 8,
                total: 0,
              }))
            ).items;
      const skuIds = collectProductSkuIds(rawCandidates);
      const categoriesById = new Map(categories.map((c) => [c.id, c]));
      const shopsById = new Map(publicShops.map((s) => [s.id, s]));
      const [activeShopDiscounts] = await Promise.all([
        skuIds.length > 0
          ? fetchActiveShopDiscounts({ skuIds }).catch(() => [] as GatewayActiveShopDiscount[])
          : Promise.resolve([] as GatewayActiveShopDiscount[]),
        hydrateMissingShops(
          shopsById,
          rawCandidates.map((p) => p.shop_id)
        ),
      ]);

      const liveFlashItemsBySkuId = buildLiveFlashItemsBySkuId(rawFlashSales, nowMs);
      const activeShopDiscountsBySkuId = buildActiveShopDiscountsMap(activeShopDiscounts);

      const similarProducts = rawCandidates.slice(0, 8).map((p) =>
        mapGatewayProduct(p, {
          categoriesById,
          shopsById,
          flashSkuIds: new Set(liveFlashItemsBySkuId.keys()),
          liveFlashItemsBySkuId,
          activeShopDiscountsBySkuId,
        })
      );
      const matchedCat = categoriesById.get(lookup.categoryId);

      return {
        ...empty,
        discontinued: {
          categoryId: lookup.categoryId,
          name: lookup.name,
          categoryName: matchedCat?.name ?? "",
          categorySlug: matchedCat?.slug ?? "",
        },
        similarProducts,
      };
    }

    const rawProduct = lookup.product;

    // Public shop detail fetched directly from GET /shops/{id} (Requirement 6).
    const [
      shopFromId,
      categories,
      publicShops,
      shopVoucherList,
      allVoucherList,
      sameShopPage,
      allProductsPage,
      rawFlashSales,
      provinces,
      initialReviews,
    ] = await Promise.all([
      fetchShopById(rawProduct.shop_id),
      fetchCategories(),
      fetchPublicShops().catch(() => [] as CatalogShop[]),
      fetchVouchers({ shopId: rawProduct.shop_id, scope: "SHOP" }).catch(
        () => [] as CatalogVoucher[]
      ),
      fetchVouchers().catch(() => [] as CatalogVoucher[]),
      fetchRawProducts({ shopId: rawProduct.shop_id, page: 1, pageSize: 20 }).catch(() => ({
        items: [] as GatewayProduct[],
        page: 1,
        page_size: 20,
        total: 0,
      })),
      fetchRawProducts({ page: 1, pageSize: 100 }).catch(() => ({
        items: [] as GatewayProduct[],
        page: 1,
        page_size: 100,
        total: 0,
      })),
      fetchRawFlashSales(true).catch(() => [] as GatewayFlashSale[]),
      fetchShippingProvinces(),
      fetchProductReviews(rawProduct.id, 10),
    ]);

    // P3-2: Same-shop cards only display the first 4 non-self items (price-independent), whereas
    // `allProductsPage.items` must keep active shop discounts for all candidates because:
    // (1) `similarProducts` scoring compares effective prices (`Math.abs(candidate.price - product.price) <= 90_000`), and
    // (2) `PdpRecommendationsSection` renders client-side `recentlyViewed` cards directly from `allProducts`.
    const sameShopCandidates = sameShopPage.items.filter((p) => p.id !== rawProduct.id).slice(0, 4);
    const pdpDiscountSkuIds = collectProductDetailDiscountSkuIds(
      rawProduct,
      sameShopCandidates,
      allProductsPage.items
    );
    const categoriesById = new Map(categories.map((c) => [c.id, c]));
    const shopsById = new Map(publicShops.map((s) => [s.id, s]));
    if (shopFromId) {
      shopsById.set(shopFromId.id, shopFromId);
    }

    const [activeShopDiscounts] = await Promise.all([
      pdpDiscountSkuIds.length > 0
        ? fetchActiveShopDiscounts({ skuIds: pdpDiscountSkuIds }).catch(
            () => [] as GatewayActiveShopDiscount[]
          )
        : Promise.resolve([] as GatewayActiveShopDiscount[]),
      hydrateMissingShops(shopsById, [
        rawProduct.shop_id,
        ...sameShopCandidates.map((p) => p.shop_id),
        ...allProductsPage.items.map((p) => p.shop_id),
      ]),
    ]);

    const fallbackShop: CatalogShop =
      shopFromId ??
      shopsById.get(rawProduct.shop_id) ??
      mapGatewayShop({
        id: rawProduct.shop_id,
        name: "Gian hàng NexusMart",
        slug: "",
        description: "",
        status: "ACTIVE",
      });

    const liveFlashItemsBySkuId = buildLiveFlashItemsBySkuId(rawFlashSales, nowMs);
    const liveFlashSkuIds = new Set(liveFlashItemsBySkuId.keys());
    const activeShopDiscountsBySkuId = buildActiveShopDiscountsMap(activeShopDiscounts);

    const mapCtx = {
      categoriesById,
      shopsById,
      flashSkuIds: liveFlashSkuIds,
      liveFlashItemsBySkuId,
      activeShopDiscountsBySkuId,
    };

    const product = mapGatewayProduct(rawProduct, mapCtx);

    const allProducts = allProductsPage.items.map((p) => mapGatewayProduct(p, mapCtx));
    const productBySlug = new Map(allProducts.map((p) => [p.slug, p]));
    productBySlug.set(product.slug, product);

    const sameShopProducts = sameShopCandidates.map((p) => mapGatewayProduct(p, mapCtx));

    const similarProducts = allProducts
      .filter((p) => p.id !== product.id && p.shopId !== product.shopId)
      .map((candidate) => {
        let score = 0;
        if (candidate.rawCategorySlug === product.rawCategorySlug) score += 4;
        if (candidate.material && candidate.material === product.material) score += 4;
        if (Math.abs(candidate.price - product.price) <= 90_000) score += 2;
        const sharedFeatures = (candidate.fabricFeatures ?? []).filter((f) =>
          (product.fabricFeatures ?? []).includes(f)
        ).length;
        score += sharedFeatures;
        return { candidate, score };
      })
      .sort((a, b) => b.score - a.score)
      .slice(0, 4)
      .map((x) => x.candidate);

    const bundleProducts = (product.bundleSlugs ?? [])
      .map((bSlug) => productBySlug.get(bSlug))
      .filter((p): p is CatalogProduct => Boolean(p && p.id !== product.id));

    const shopVouchers = shopVoucherList.filter(
      (v) => v.scope === "shop" && v.shopId === rawProduct.shop_id && v.isActive
    );
    const platformVouchers = allVoucherList
      .filter((v) => (v.scope === "platform" || v.scope === "freeship") && v.isActive)
      .slice(0, 4);

    // Map live flash sale items for this product's SKUs
    const flashSlots = mapGatewayFlashSales(rawFlashSales, [product], nowMs);
    const liveFlashBySkuId: Record<string, CatalogFlashSaleItem> = {};
    for (const slot of flashSlots) {
      if (slot.status !== "live") continue;
      for (const item of slot.items) {
        if (item.remainingSlots > 0) {
          liveFlashBySkuId[item.skuId] = item;
        }
      }
    }

    const activeShopDiscountBySkuId: Record<string, GatewayActiveShopDiscount> = {};
    for (const sku of product.skus) {
      const disc = activeShopDiscountsBySkuId.get(sku.id);
      if (disc && !liveFlashBySkuId[sku.id]) {
        activeShopDiscountBySkuId[sku.id] = disc;
      }
    }

    return {
      notFound: false,
      discontinued: null,
      error: null,
      product,
      shop: fallbackShop,
      initialReviews,
      shopVouchers,
      platformVouchers,
      sameShopProducts,
      similarProducts,
      bundleProducts,
      allProducts,
      liveFlashBySkuId,
      activeShopDiscountBySkuId,
      provinces,
    };
  } catch (err) {
    return { ...empty, error: toSafeErrorMessage(err) };
  }
}

export interface ShopDetailData {
  notFound: boolean;
  error: string | null;
  shop: CatalogShop | null;
  otherShops: CatalogShop[];
  categories: CatalogCategory[];
  shopProducts: CatalogProduct[];
  allProducts: CatalogProduct[];
  shopVouchers: CatalogVoucher[];
  platformVouchers: CatalogVoucher[];
}

export async function loadShopDetailData(slug: string): Promise<ShopDetailData> {
  const empty: ShopDetailData = {
    notFound: false,
    error: null,
    shop: null,
    otherShops: [],
    categories: [],
    shopProducts: [],
    allProducts: [],
    shopVouchers: [],
    platformVouchers: [],
  };

  try {
    const shop = await fetchShopBySlug(slug);
    if (!shop) {
      return { ...empty, notFound: true };
    }

    const [
      allShops,
      categories,
      shopProductsPage,
      allProductsPage,
      shopVoucherList,
      allVoucherList,
      rawFlashSales,
    ] = await Promise.all([
      fetchPublicShops().catch(() => [] as CatalogShop[]),
      fetchCategories(),
      fetchRawProducts({ shopId: shop.id, page: 1, pageSize: 100 }),
      fetchRawProducts({ page: 1, pageSize: 100 }).catch(() => ({
        items: [] as GatewayProduct[],
        page: 1,
        page_size: 100,
        total: 0,
      })),
      fetchVouchers({ shopId: shop.id, scope: "SHOP" }).catch(() => [] as CatalogVoucher[]),
      fetchVouchers().catch(() => [] as CatalogVoucher[]),
      fetchRawFlashSales(false).catch(() => [] as GatewayFlashSale[]),
    ]);

    const nowMs = Date.now();
    const categoriesById = new Map(categories.map((c) => [c.id, c]));
    const shopsById = new Map(allShops.map((s) => [s.id, s]));
    shopsById.set(shop.id, shop);

    // §10 SizeFinder + §10-3: fetch active shop discounts across ALL SKUs in shopProducts & allProducts
    // (including other workshops) and resolve any shop #101+ names in parallel (concurrency <= 8).
    const allShopDetailSkuIds = collectProductSkuIds([
      ...shopProductsPage.items,
      ...allProductsPage.items,
    ]);
    const [activeShopDiscounts] = await Promise.all([
      allShopDetailSkuIds.length > 0
        ? fetchActiveShopDiscounts({ skuIds: allShopDetailSkuIds }).catch(
            () => [] as GatewayActiveShopDiscount[]
          )
        : Promise.resolve([] as GatewayActiveShopDiscount[]),
      hydrateMissingShops(shopsById, [
        ...shopProductsPage.items.map((p) => p.shop_id),
        ...allProductsPage.items.map((p) => p.shop_id),
      ]),
    ]);
    const liveFlashItemsBySkuId = buildLiveFlashItemsBySkuId(rawFlashSales, nowMs);
    const liveFlashSkuIds = new Set(liveFlashItemsBySkuId.keys());
    const activeShopDiscountsBySkuId = buildActiveShopDiscountsMap(activeShopDiscounts);

    const mapCtx = {
      categoriesById,
      shopsById,
      flashSkuIds: liveFlashSkuIds,
      liveFlashItemsBySkuId,
      activeShopDiscountsBySkuId,
    };

    const shopProducts = shopProductsPage.items.map((p) => mapGatewayProduct(p, mapCtx));
    const allProducts = allProductsPage.items.map((p) => mapGatewayProduct(p, mapCtx));

    const countByShop = new Map<string, number>();
    for (const p of allProducts) {
      countByShop.set(p.shopId, (countByShop.get(p.shopId) ?? 0) + 1);
    }

    const otherShops = allShops
      .filter((s) => s.id !== shop.id)
      .sort((a, b) => {
        const diff = (countByShop.get(b.id) ?? 0) - (countByShop.get(a.id) ?? 0);
        if (diff !== 0) return diff;
        return Number(b.hasEditorialProfile) - Number(a.hasEditorialProfile);
      });

    const shopVouchers = shopVoucherList.filter(
      (v) => v.scope === "shop" && v.shopId === shop.id && v.isActive
    );
    const platformVouchers = allVoucherList
      .filter((v) => (v.scope === "platform" || v.scope === "freeship") && v.isActive)
      .slice(0, 2);

    return {
      notFound: false,
      error: null,
      shop,
      otherShops,
      categories,
      shopProducts,
      allProducts,
      shopVouchers,
      platformVouchers,
    };
  } catch (err) {
    return { ...empty, error: toSafeErrorMessage(err) };
  }
}

export interface FlashSalePageData {
  flashSlots: CatalogFlashSaleSlot[];
  products: CatalogProduct[];
  shops: CatalogShop[];
  serverNowMs: number;
  error: string | null;
}

export async function loadFlashSalePageData(): Promise<FlashSalePageData> {
  const serverNowMs = Date.now();
  try {
    const [rawFlashSales, productPage, categories, shops] = await Promise.all([
      fetchRawFlashSales(true),
      fetchRawProducts({ page: 1, pageSize: 100 }),
      fetchCategories(),
      fetchPublicShops(),
    ]);
    const mergedRawProducts = await fetchProductsForFlashSalesWithFetcher(
      rawFlashSales,
      productPage.items,
      async (batchSkuIds) => {
        const res = await fetchRawProducts({
          skuIds: batchSkuIds,
          page: 1,
          pageSize: 100,
        });
        return res.items;
      }
    ).catch(() => {
      console.warn("[catalog] flash-sale extra product lookup failed; falling back to top-100");
      return productPage.items;
    });

    const categoriesById = new Map(categories.map((c) => [c.id, c]));
    const shopsById = new Map(shops.map((s) => [s.id, s]));
    await hydrateMissingShops(
      shopsById,
      mergedRawProducts.map((p) => p.shop_id)
    );
    const liveFlashItemsBySkuId = buildLiveFlashItemsBySkuId(rawFlashSales, serverNowMs);
    const liveFlashSkuIds = buildLiveFlashSkuSet(rawFlashSales, serverNowMs);

    const products = mergedRawProducts.map((p) =>
      mapGatewayProduct(p, {
        categoriesById,
        shopsById,
        flashSkuIds: liveFlashSkuIds,
        liveFlashItemsBySkuId,
      })
    );
    const flashSlots = mapGatewayFlashSales(rawFlashSales, products, serverNowMs);

    return {
      flashSlots,
      products,
      shops,
      serverNowMs,
      error: null,
    };
  } catch (err) {
    return {
      flashSlots: [],
      products: [],
      shops: [],
      serverNowMs,
      error: toSafeErrorMessage(err),
    };
  }
}
