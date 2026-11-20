// Server-only catalog loaders calling the NexusMart gateway through web/src/lib/server/gateway.ts.
// Import only from Server Components or Route Handlers.
import { cache } from "react";
import { CATEGORIES, PRODUCTS, type CategoryInfo, type Product } from "../data/products";
import { SHOPS, type Shop } from "../data/shops";
import { VOUCHERS, type Voucher } from "../data/vouchers";
import type { FlashSaleItem, FlashSaleSlot } from "../data/flash-sale";
import {
  buildCatalogLookups,
  buildFlashSlots,
  createTtlCache,
  filterStorefrontShops,
  findProductByParam,
  findShopByParam,
  markFlashProducts,
  mergeCategories,
  mergeProducts,
  mergeShops,
  mergeVouchers,
  type CatalogLookups,
  type CategoryIndex,
  type GwCategory,
  type GwFlashSale,
  type GwProduct,
  type GwShop,
  type GwVoucher,
  type ProductMergeContext,
} from "./catalog-map";
import { GatewayError, SAFE_MESSAGES, logServer } from "./server/errors";
import { gw } from "./server/gateway";

const PAGE_SIZE = 100;
const MAX_PAGES = 10;

// Short in-process TTL caches for public catalog lists to prevent BFF fanout from
// saturating the gateway IP rate limit. Live PDP single-product stock
// (`/api/v1/products/{id}`), vouchers, and flash-sale quotas remain uncached.
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

const fetchFlashSalesRaw = cache(async (): Promise<GwFlashSale[]> => {
  const res = await gw<{ flash_sales?: GwFlashSale[] | null }>("/api/v1/flash-sales", {
    auth: "none",
    query: { include_upcoming: true },
  });
  return res.flash_sales ?? [];
});

const fetchVouchersRaw = cache(async (): Promise<GwVoucher[]> => {
  try {
    const res = await gw<{ vouchers?: GwVoucher[] | null }>("/api/v1/vouchers", {
      auth: "user",
    });
    return res.vouchers ?? [];
  } catch (err) {
    // Public catalog views must still load vouchers even if the visitor's access cookie expired.
    if (err instanceof GatewayError && err.kind === "unauthenticated") {
      const res = await gw<{ vouchers?: GwVoucher[] | null }>("/api/v1/vouchers", {
        auth: "none",
      });
      return res.vouchers ?? [];
    }
    throw err;
  }
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

export interface CatalogData {
  categories: CategoryInfo[];
  shops: Shop[];
  products: Product[];
  error: string | null;
}

/** Loads real categories, shops, and products (marking active flash-sale items when available). */
export const loadCatalog = cache(async (): Promise<CatalogData> => {
  try {
    const [core, flashResult] = await Promise.all([
      loadCoreInternal(),
      fetchFlashSalesRaw().catch(() => null),
    ]);
    const products = flashResult
      ? markFlashProducts(core.products, buildFlashSlots(flashResult, core.products, Date.now()))
      : core.products;
    return {
      categories: core.categories,
      shops: core.shops,
      products,
      error: null,
    };
  } catch (err) {
    logServer("warn", "catalog_load_failed", {
      kind: err instanceof GatewayError ? err.kind : "unknown",
    });
    return {
      categories: [],
      shops: [],
      products: [],
      error: safeErrorMessage(err),
    };
  }
});

export interface StorefrontData extends CatalogData {
  flashSlots: FlashSaleSlot[];
  vouchers: Voucher[];
  flashError: string | null;
  voucherError: string | null;
}

/** Loads full storefront data for the home page in parallel. */
export const loadStorefront = cache(async (): Promise<StorefrontData> => {
  const [coreRes, flashRes, voucherRes] = await Promise.allSettled([
    loadCoreInternal(),
    fetchFlashSalesRaw(),
    fetchVouchersRaw(),
  ]);

  if (coreRes.status === "rejected") {
    logServer("warn", "storefront_core_failed", {
      kind: coreRes.reason instanceof GatewayError ? coreRes.reason.kind : "unknown",
    });
    return {
      categories: [],
      shops: [],
      products: [],
      flashSlots: [],
      vouchers: [],
      error: safeErrorMessage(coreRes.reason),
      flashError: null,
      voucherError: null,
    };
  }

  const core = coreRes.value;
  const flashSlots =
    flashRes.status === "fulfilled"
      ? buildFlashSlots(flashRes.value, core.products, Date.now())
      : [];
  const flashError = flashRes.status === "rejected" ? safeErrorMessage(flashRes.reason) : null;

  const vouchers =
    voucherRes.status === "fulfilled"
      ? mergeVouchers(voucherRes.value, VOUCHERS, core.shopIdByBackend, core.shopIds)
      : [];
  const voucherError =
    voucherRes.status === "rejected" ? safeErrorMessage(voucherRes.reason) : null;

  const products = flashSlots.length ? markFlashProducts(core.products, flashSlots) : core.products;

  return {
    categories: core.categories,
    shops: core.shops,
    products,
    flashSlots,
    vouchers,
    error: null,
    flashError,
    voucherError,
  };
});

export interface ProductDetailData {
  product: Product | null;
  shop: Shop | null;
  allProducts: Product[];
  shops: Shop[];
  shopVouchers: Voucher[];
  platformVouchers: Voucher[];
  liveFlashItem: FlashSaleItem | null;
  error: string | null;
}

/** Loads a product detail page with a fresh single-product call for live SKU stock. */
export const loadProductDetail = cache(async (slug: string): Promise<ProductDetailData> => {
  const [coreRes, flashRes, voucherRes] = await Promise.allSettled([
    loadCoreInternal(),
    fetchFlashSalesRaw(),
    fetchVouchersRaw(),
  ]);

  if (coreRes.status === "rejected") {
    logServer("warn", "pdp_load_failed", {
      kind: coreRes.reason instanceof GatewayError ? coreRes.reason.kind : "unknown",
    });
    return {
      product: null,
      shop: null,
      allProducts: [],
      shops: [],
      shopVouchers: [],
      platformVouchers: [],
      liveFlashItem: null,
      error: safeErrorMessage(coreRes.reason),
    };
  }

  const core = coreRes.value;
  const matched = findProductByParam(core.products, slug);
  const targetBackendId = matched?.backendId ?? (matched ? undefined : slug);

  let freshProduct: Product | null = matched ?? null;
  if (targetBackendId) {
    try {
      const res = await gw<{ product?: GwProduct | null }>(
        `/api/v1/products/${encodeURIComponent(targetBackendId)}`,
        { auth: "none" }
      );
      if (res.product) {
        const mapped = mergeProducts([res.product], core.mergeCtx)[0];
        if (mapped) freshProduct = mapped;
      }
    } catch (err) {
      if (!(err instanceof GatewayError && err.kind === "not_found")) {
        logServer("warn", "pdp_fresh_fetch_failed", {
          kind: err instanceof GatewayError ? err.kind : "unknown",
        });
        if (!freshProduct) {
          return {
            product: null,
            shop: null,
            allProducts: core.products,
            shops: core.shops,
            shopVouchers: [],
            platformVouchers: [],
            liveFlashItem: null,
            error: safeErrorMessage(err),
          };
        }
      }
    }
  }

  if (!freshProduct) {
    return {
      product: null,
      shop: null,
      allProducts: core.products,
      shops: core.shops,
      shopVouchers: [],
      platformVouchers: [],
      liveFlashItem: null,
      error: null,
    };
  }

  const allProductsBase = core.products.some((p) => p.id === freshProduct.id)
    ? core.products.map((p) => (p.id === freshProduct.id ? freshProduct : p))
    : [freshProduct, ...core.products];

  const flashSlots =
    flashRes.status === "fulfilled"
      ? buildFlashSlots(flashRes.value, allProductsBase, Date.now())
      : [];
  const allProducts = flashSlots.length
    ? markFlashProducts(allProductsBase, flashSlots)
    : allProductsBase;
  const product = allProducts.find((p) => p.id === freshProduct.id) ?? freshProduct;
  const liveSlot = flashSlots.find((s) => s.status === "live");
  const liveFlashItem = liveSlot?.items.find((it) => it.productId === product.id) ?? null;

  const shop = findShopByParam(core.shops, product.shopId) ?? null;
  const vouchers =
    voucherRes.status === "fulfilled"
      ? mergeVouchers(voucherRes.value, VOUCHERS, core.shopIdByBackend, core.shopIds)
      : [];
  const shopVouchers = vouchers.filter((v) => v.scope === "shop" && v.shopId === product.shopId);
  const platformVouchers = vouchers.filter((v) => v.scope === "platform" || v.scope === "freeship");

  return {
    product,
    shop,
    allProducts,
    shops: core.shops,
    shopVouchers,
    platformVouchers,
    liveFlashItem,
    error: null,
  };
});

export interface ShopDetailData {
  shop: Shop | null;
  otherShops: Shop[];
  shopProducts: Product[];
  allProducts: Product[];
  categories: CategoryInfo[];
  shops: Shop[];
  shopVouchers: Voucher[];
  platformVouchers: Voucher[];
  error: string | null;
}

/** Loads a shop storefront page with real shop profile, products, and vouchers. */
export const loadShopDetail = cache(async (slug: string): Promise<ShopDetailData> => {
  const [coreRes, flashRes, voucherRes] = await Promise.allSettled([
    loadCoreInternal(),
    fetchFlashSalesRaw(),
    fetchVouchersRaw(),
  ]);

  if (coreRes.status === "rejected") {
    logServer("warn", "shop_load_failed", {
      kind: coreRes.reason instanceof GatewayError ? coreRes.reason.kind : "unknown",
    });
    return {
      shop: null,
      otherShops: [],
      shopProducts: [],
      allProducts: [],
      categories: [],
      shops: [],
      shopVouchers: [],
      platformVouchers: [],
      error: safeErrorMessage(coreRes.reason),
    };
  }

  const core = coreRes.value;
  let shop = findShopByParam(core.shops, slug) ?? null;

  if (!shop && slug) {
    try {
      const res = await gw<{ shop?: GwShop | null }>(`/api/v1/shops/${encodeURIComponent(slug)}`, {
        auth: "none",
      });
      if (res.shop) {
        shop = mergeShops([res.shop], SHOPS)[0] ?? null;
      }
    } catch (err) {
      if (!(err instanceof GatewayError && err.kind === "not_found")) {
        logServer("warn", "shop_direct_fetch_failed", {
          kind: err instanceof GatewayError ? err.kind : "unknown",
        });
      }
    }
  }

  const flashSlots =
    flashRes.status === "fulfilled"
      ? buildFlashSlots(flashRes.value, core.products, Date.now())
      : [];
  const allProducts = flashSlots.length
    ? markFlashProducts(core.products, flashSlots)
    : core.products;

  if (!shop) {
    return {
      shop: null,
      otherShops: core.shops,
      shopProducts: [],
      allProducts,
      categories: core.categories,
      shops: core.shops,
      shopVouchers: [],
      platformVouchers: [],
      error: null,
    };
  }

  const shopProducts = allProducts.filter((p) => p.shopId === shop.id);
  const otherShops = core.shops.filter((s) => s.id !== shop.id);
  const vouchers =
    voucherRes.status === "fulfilled"
      ? mergeVouchers(voucherRes.value, VOUCHERS, core.shopIdByBackend, core.shopIds)
      : [];
  const shopVouchers = vouchers.filter((v) => v.scope === "shop" && v.shopId === shop.id);
  const platformVouchers = vouchers.filter((v) => v.scope === "platform" || v.scope === "freeship");

  return {
    shop,
    otherShops,
    shopProducts,
    allProducts,
    categories: core.categories,
    shops: core.shops,
    shopVouchers,
    platformVouchers,
    error: null,
  };
});

export interface FlashSalePageData {
  slots: FlashSaleSlot[];
  products: Product[];
  shops: Shop[];
  error: string | null;
}

/** Loads live and upcoming flash sales with real remaining quotas and SKU mappings. */
export const loadFlashSales = cache(async (): Promise<FlashSalePageData> => {
  try {
    const [core, gwSales] = await Promise.all([loadCoreInternal(), fetchFlashSalesRaw()]);
    const slots = buildFlashSlots(gwSales, core.products, Date.now());
    const products = markFlashProducts(core.products, slots);
    return { slots, products, shops: core.shops, error: null };
  } catch (err) {
    logServer("warn", "flash_sale_load_failed", {
      kind: err instanceof GatewayError ? err.kind : "unknown",
    });
    return { slots: [], products: [], shops: [], error: safeErrorMessage(err) };
  }
});

export interface VouchersPageData {
  vouchers: Voucher[];
  shops: Shop[];
  error: string | null;
}

/** Loads real active vouchers (platform, shop, freeship) with live remaining quota. */
export const loadVouchers = cache(async (): Promise<VouchersPageData> => {
  try {
    const [core, gwVouchers] = await Promise.all([loadCoreInternal(), fetchVouchersRaw()]);
    const vouchers = mergeVouchers(gwVouchers, VOUCHERS, core.shopIdByBackend, core.shopIds);
    return { vouchers, shops: core.shops, error: null };
  } catch (err) {
    logServer("warn", "vouchers_load_failed", {
      kind: err instanceof GatewayError ? err.kind : "unknown",
    });
    return { vouchers: [], shops: [], error: safeErrorMessage(err) };
  }
});

export interface CatalogLookupsData {
  lookups: CatalogLookups;
  products: Product[];
  shops: Shop[];
  error: string | null;
}

/**
 * Loads backend catalog lookup tables for server routes (e.g. account OrderLookups, checkout
 * SKU/shop presentation) without calling flash-sales and with short in-process TTL caching.
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
    });
    return {
      lookups: buildCatalogLookups([], []),
      products: [],
      shops: [],
      error: safeErrorMessage(err),
    };
  }
});
