import type {
  GatewayActiveShopDiscount,
  GatewayCategory,
  GatewayFlashSale,
  GatewayProduct,
  GatewayProductReviewsResponse,
  GatewayProductSlugResponse,
  GatewayProvince,
  GatewayPublicShop,
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
    const activeShopDiscounts =
      homeSkuIds.length > 0
        ? await fetchActiveShopDiscounts({ skuIds: homeSkuIds }).catch(
            () => [] as GatewayActiveShopDiscount[]
          )
        : [];

    const categoriesById = new Map(categories.map((c) => [c.id, c]));
    const shopsById = new Map(shops.map((s) => [s.id, s]));
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
      flashSlots,
      serverNowMs,
      error: null,
    };
  } catch (err) {
    return {
      categories: [],
      shops: [],
      products: [],
      flashSlots: [],
      serverNowMs,
      error: toSafeErrorMessage(err),
    };
  }
}

export interface CatalogExplorerServerData {
  categories: CatalogCategory[];
  shops: CatalogShop[];
  allProducts: CatalogProduct[];
  serverFilteredProducts: CatalogProduct[];
  hasServerFilter: boolean;
  serverTotal: number;
  error: string | null;
}

function readSingleParam(val: string | string[] | undefined): string {
  if (Array.isArray(val)) return val[0] ?? "";
  return val ?? "";
}

function toBackendSortBy(sortParam: string): string | undefined {
  switch (sortParam) {
    case "newest":
      return "newest";
    case "price-asc":
    case "price_asc":
      return "price_asc";
    case "price-desc":
    case "price_desc":
      return "price_desc";
    case "name-asc":
    case "name_asc":
      return "name_asc";
    default:
      return undefined;
  }
}

export async function loadCatalogExplorerData(
  rawSearchParams: Record<string, string | string[] | undefined>
): Promise<CatalogExplorerServerData> {
  const catParam = readSingleParam(rawSearchParams.cat).trim();
  const shopParam = readSingleParam(rawSearchParams.shop).trim();
  const priceParam = readSingleParam(rawSearchParams.price).trim();
  const qParam = readSingleParam(rawSearchParams.q).trim();
  const sortParam = readSingleParam(rawSearchParams.sort).trim();

  const priceRange = resolvePriceBucketRange(priceParam);
  const backendSortBy = toBackendSortBy(sortParam);

  let shopId: string | undefined;
  let shopSlug: string | undefined;
  if (shopParam) {
    if (UUID_PATTERN.test(shopParam)) {
      shopId = shopParam;
    } else {
      shopSlug = getLegacyShopSlugById(shopParam) ?? shopParam;
    }
  }

  const hasServerFilter = Boolean(
    catParam ||
    shopId ||
    shopSlug ||
    qParam ||
    priceRange.minPriceVnd ||
    priceRange.maxPriceVnd ||
    backendSortBy
  );

  try {
    // ponytail: tải tối đa 100 sản phẩm thật để giữ 13 nhóm facet & tìm bỏ dấu/đồng nghĩa phía client;
    // khi có Search service (Tháng 4–6) sẽ chuyển toàn bộ facet & synonym lên server.
    const [categories, shops, allPage, filteredPage, rawFlashSales] = await Promise.all([
      fetchCategories(),
      fetchPublicShops(),
      fetchRawProducts({ page: 1, pageSize: 100 }),
      hasServerFilter
        ? fetchRawProducts({
            categorySlug: catParam || undefined,
            shopId,
            shopSlug,
            keyword: qParam || undefined,
            minPriceVnd: priceRange.minPriceVnd,
            maxPriceVnd: priceRange.maxPriceVnd,
            sortBy: backendSortBy,
            page: 1,
            pageSize: 100,
          })
        : Promise.resolve(null),
      fetchRawFlashSales(false).catch(() => [] as GatewayFlashSale[]),
    ]);
    const explorerSkuIds = collectProductSkuIds([...allPage.items, ...(filteredPage?.items ?? [])]);
    const activeShopDiscounts =
      explorerSkuIds.length > 0
        ? await fetchActiveShopDiscounts({ skuIds: explorerSkuIds }).catch(
            () => [] as GatewayActiveShopDiscount[]
          )
        : [];

    const nowMs = Date.now();
    const categoriesById = new Map(categories.map((c) => [c.id, c]));
    const shopsById = new Map(shops.map((s) => [s.id, s]));
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

    const allProducts = allPage.items.map((p) => mapGatewayProduct(p, mapCtx));
    const serverFilteredProducts = filteredPage
      ? filteredPage.items.map((p) => mapGatewayProduct(p, mapCtx))
      : allProducts;

    // Order shops so shops with catalog products come first
    const countByShop = new Map<string, number>();
    for (const p of allProducts) {
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
      allProducts,
      serverFilteredProducts,
      hasServerFilter,
      serverTotal: filteredPage ? filteredPage.total : allPage.total,
      error: null,
    };
  } catch (err) {
    return {
      categories: [],
      shops: [],
      allProducts: [],
      serverFilteredProducts: [],
      hasServerFilter,
      serverTotal: 0,
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
      const activeShopDiscounts =
        skuIds.length > 0
          ? await fetchActiveShopDiscounts({ skuIds }).catch(
              () => [] as GatewayActiveShopDiscount[]
            )
          : [];

      const categoriesById = new Map(categories.map((c) => [c.id, c]));
      const shopsById = new Map(publicShops.map((s) => [s.id, s]));
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
    const activeShopDiscounts =
      pdpDiscountSkuIds.length > 0
        ? await fetchActiveShopDiscounts({ skuIds: pdpDiscountSkuIds }).catch(
            () => [] as GatewayActiveShopDiscount[]
          )
        : [];

    const categoriesById = new Map(categories.map((c) => [c.id, c]));
    const shopsById = new Map(publicShops.map((s) => [s.id, s]));
    if (shopFromId) {
      shopsById.set(shopFromId.id, shopFromId);
    }

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
      activeShopDiscounts,
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
      fetchActiveShopDiscounts({ shopId: shop.id }).catch(() => [] as GatewayActiveShopDiscount[]),
    ]);

    const nowMs = Date.now();
    const categoriesById = new Map(categories.map((c) => [c.id, c]));
    const shopsById = new Map(allShops.map((s) => [s.id, s]));
    shopsById.set(shop.id, shop);
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
