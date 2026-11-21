// Server-only catalog lookup loader calling the NexusMart gateway through web/src/lib/server/gateway.ts.
// Import only from Server Components or Route Handlers.
import { cache } from "react";
import { CATEGORIES, PRODUCTS, type CategoryInfo, type Product } from "../data/products";
import { SHOPS, type Shop } from "../data/shops";
import {
  buildCatalogLookups,
  createTtlCache,
  filterStorefrontShops,
  mergeCategories,
  mergeProducts,
  mergeShops,
  type CatalogLookups,
  type CategoryIndex,
  type GwCategory,
  type GwProduct,
  type GwShop,
  type ProductMergeContext,
} from "./catalog-map";
import { GatewayError, SAFE_MESSAGES, logServer } from "./server/errors";
import { gw } from "./server/gateway";

const PAGE_SIZE = 100;
const MAX_PAGES = 10;

// Short in-process TTL caches for public catalog lists to prevent BFF fanout from
// saturating the gateway IP rate limit.
const CATEGORIES_TTL_MS = 15_000;
const SHOPS_TTL_MS = 10_000;
const PRODUCTS_LIST_TTL_MS = 5_000;

const categoriesCache = createTtlCache<GwCategory[]>(CATEGORIES_TTL_MS);
const shopsCache = createTtlCache<GwShop[]>(SHOPS_TTL_MS);
const productsListCache = createTtlCache<GwProduct[]>(PRODUCTS_LIST_TTL_MS);

interface PagedResponse<T> {
  items?: T[] | null;
  page?: number;
  page_size?: number;
  total?: number;
}

function safeErrorMessage(err: unknown): string {
  if (err instanceof GatewayError) {
    return SAFE_MESSAGES[err.kind] ?? SAFE_MESSAGES.unavailable;
  }
  return SAFE_MESSAGES.unavailable;
}

async function fetchAllPages<T>(
  path: string,
  extraQuery: Record<string, string | number | boolean | undefined> = {}
): Promise<T[]> {
  const all: T[] = [];
  for (let page = 1; page <= MAX_PAGES; page++) {
    const res = await gw<PagedResponse<T>>(path, {
      auth: "none",
      query: { ...extraQuery, page, page_size: PAGE_SIZE },
    });
    const batch = res.items ?? [];
    all.push(...batch);
    const total = typeof res.total === "number" ? res.total : batch.length;
    if (batch.length < PAGE_SIZE || all.length >= total) break;
  }
  return all;
}

const fetchCategoriesRaw = cache(async (): Promise<GwCategory[]> => {
  return categoriesCache.get(async () => {
    const res = await gw<{ categories?: GwCategory[] | null }>("/api/v1/categories", {
      auth: "none",
    });
    return res.categories ?? [];
  });
});

const fetchShopsRaw = cache(async (): Promise<GwShop[]> => {
  return shopsCache.get(() => fetchAllPages<GwShop>("/api/v1/shops"));
});

const fetchProductsRaw = cache(async (): Promise<GwProduct[]> => {
  return productsListCache.get(() => fetchAllPages<GwProduct>("/api/v1/products"));
});

interface CoreCatalogInternal {
  categories: CategoryInfo[];
  categoryIndex: CategoryIndex;
  shops: Shop[];
  shopIds: Set<string>;
  shopIdByBackend: Map<string, string>;
  products: Product[];
  mergeCtx: ProductMergeContext;
}

const loadCoreInternal = cache(async (): Promise<CoreCatalogInternal> => {
  const [gwCats, gwShops, gwProducts] = await Promise.all([
    fetchCategoriesRaw(),
    fetchShopsRaw(),
    fetchProductsRaw(),
  ]);
  const categoryIndex = mergeCategories(gwCats, CATEGORIES);
  const allShops = mergeShops(gwShops, SHOPS);
  const shopIdByBackend = new Map<string, string>();
  for (const s of allShops) {
    if (s.backendId) shopIdByBackend.set(s.backendId, s.id);
  }
  const mergeCtx: ProductMergeContext = {
    content: PRODUCTS,
    categories: categoryIndex,
    shopIdByBackend,
  };
  const products = mergeProducts(gwProducts, mergeCtx);
  const shops = filterStorefrontShops(allShops, products, SHOPS);
  const shopIds = new Set(shops.map((s) => s.id));
  return {
    categories: categoryIndex.categories,
    categoryIndex,
    shops,
    shopIds,
    shopIdByBackend,
    products,
    mergeCtx,
  };
});

export interface CatalogLookupsData {
  lookups: CatalogLookups;
  products: Product[];
  shops: Shop[];
  error: string | null;
}

/**
 * Loads backend catalog lookup tables for server routes (e.g. account OrderLookups, checkout
 * SKU/shop presentation) with short in-process TTL caching.
 */
export const loadCatalogLookups = cache(async (): Promise<CatalogLookupsData> => {
  try {
    const core = await loadCoreInternal();
    return {
      lookups: buildCatalogLookups(core.products, core.shops),
      products: core.products,
      shops: core.shops,
      error: null,
    };
  } catch (err) {
    logServer("warn", "catalog_lookups_load_failed", {
      kind: err instanceof GatewayError ? err.kind : "unknown",
      trace_id: err instanceof GatewayError ? err.traceId : "",
    });
    return {
      lookups: buildCatalogLookups([], []),
      products: [],
      shops: [],
      error: safeErrorMessage(err),
    };
  }
});
