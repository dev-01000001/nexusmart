// Server-only multi-tenant site data loaders & storefront verification helpers.
// Import only from Server Components and Route Handlers (never from client components or Edge middleware).
import { cache } from "react";
import type { FlashSaleItem, FlashSaleSlot } from "@/data/flash-sale";
import { CATEGORIES, PRODUCTS, type CategoryInfo, type Product } from "@/data/products";
import { SHOPS, type Shop } from "@/data/shops";
import { VOUCHERS, type Voucher } from "@/data/vouchers";
import {
  buildFlashSlots,
  markFlashProducts,
  mergeCategories,
  mergeProducts,
  mergeShops,
  mergeVouchers,
  type GwCategory,
  type GwFlashSale,
  type GwProduct,
  type GwShop,
  type GwVoucher,
  type ProductMergeContext,
} from "@/lib/catalog-map";
import {
  parseSiteConfig,
  type PublicSitePayload,
  type SiteConfig,
  type SiteDTO,
  type SitePublicShopDTO,
} from "@/lib/site/types";
import { classifyHost, isTenantSiteHost, normalizeHostHeader } from "@/lib/site/host";
import { GATEWAY_TIMEOUT_MS, GATEWAY_URL, getSiteRootDomain } from "./config";
import { GatewayError, logServer } from "./errors";
import { gw } from "./gateway";
import { resolveRequestHost } from "./http";
import { lookupPublishedSiteHost, publishedSiteHostCache } from "./siteLookup";
import { generateTraceparent } from "./trace";

const SITE_REVALIDATE_SECONDS = 30;

/** The PUBLISHED site serving the current request, derived ONLY from the Host header. */
export interface RequestSite {
  siteId: string;
  shopId: string;
  /** Normalized site hostname (lowercase, no port). */
  host: string;
}

/**
 * Resolve the PUBLISHED site for a request from its `Host` (or `X-Forwarded-Host` only when
 * `TRUST_PROXY_HEADERS=true`). Returns null for the root host, unknown/reserved hosts and
 * draft/suspended sites. Never reads `shop_id`, channel or any other client-supplied hint.
 * `fresh: true` bypasses the short host cache (checkout: a just-suspended site is rejected).
 */
export async function resolveSiteFromRequest(
  source: Request | Pick<Headers, "get">,
  opts: { fresh?: boolean } = {}
): Promise<RequestSite | null> {
  const headers = "headers" in source ? source.headers : source;
  const cls = classifyHost(resolveRequestHost(headers), getSiteRootDomain());
  if (!isTenantSiteHost(cls)) return null;
  const lookup = opts.fresh
    ? await lookupPublishedSiteHost(cls.hostname)
    : await publishedSiteHostCache.resolve(cls.hostname, lookupPublishedSiteHost);
  if (lookup.status === "error") throw new GatewayError("unavailable");
  if (lookup.status !== "found") return null;
  return { siteId: lookup.siteId, shopId: lookup.shopId, host: cls.hostname };
}

/** Route handlers for buyer APIs: the request's site, or 404 on the root host / unknown site. */
export async function requireRequestSite(
  req: Request,
  opts: { fresh?: boolean } = {}
): Promise<RequestSite> {
  const site = await resolveSiteFromRequest(req, opts);
  if (!site) throw new GatewayError("not_found");
  return site;
}

function productHasSku(p: GwProduct | null | undefined, shopId: string, skuId: string): boolean {
  return Boolean(p && p.shop_id === shopId && (p.skus ?? []).some((s) => s.id === skuId));
}

/**
 * 404 unless `skuId` is a SKU of a product owned by `shopId`. `productIdHint` (from the client)
 * only speeds up the lookup: the backend product's `shop_id` and SKU list decide.
 */
export async function assertSkuOfShop(
  shopId: string,
  skuId: string,
  productIdHint?: unknown
): Promise<void> {
  const hint = typeof productIdHint === "string" ? productIdHint.trim() : "";
  if (hint) {
    try {
      const res = await gw<{ product?: GwProduct | null }>(
        `/api/v1/products/${encodeURIComponent(hint)}`,
        { auth: "none" }
      );
      if (productHasSku(res.product, shopId, skuId)) return;
    } catch (err) {
      if (!(err instanceof GatewayError && (err.kind === "not_found" || err.kind === "invalid"))) {
        throw err;
      }
    }
  }
  const products = await fetchShopProductsRaw(shopId, {
    revalidate: SITE_REVALIDATE_SECONDS,
    tags: [`site-shop:${shopId}`],
  });
  if (products.some((p) => productHasSku(p, shopId, skuId))) return;
  // Retry once uncached so newly created SKUs of this shop are never falsely 404'd by TTL cache.
  const freshProducts = await fetchShopProductsRaw(shopId, {});
  if (freshProducts.some((p) => productHasSku(p, shopId, skuId))) return;
  throw new GatewayError("not_found");
}

interface RawPublicSiteResponse {
  site?: Partial<SiteDTO> & {
    config?: unknown;
    published_config?: unknown;
    config_json?: unknown;
  };
  shop?: Partial<SitePublicShopDTO>;
  config?: unknown;
}

function normalizePublicSiteResponse(
  raw: RawPublicSiteResponse | null | undefined
): PublicSitePayload | null {
  if (!raw || !raw.site) return null;
  const s = raw.site;
  if (!s.id || !s.shop_id) return null;
  if (String(s.status ?? "").toUpperCase() !== "PUBLISHED") return null;

  const shopStatus = raw.shop?.status ? String(raw.shop.status).toUpperCase() : "ACTIVE";
  if (shopStatus && shopStatus !== "ACTIVE") return null;

  const shop: SitePublicShopDTO = {
    id: raw.shop?.id || s.shop_id,
    name: raw.shop?.name || s.shop_name || "Cửa hàng trên NexusMart",
    description: raw.shop?.description || "",
    status: shopStatus || "ACTIVE",
  };

  const configSource = s.config ?? s.published_config ?? s.config_json ?? raw.config;
  const config: SiteConfig | null = parseSiteConfig(configSource, shop.name);
  if (!config) return null;

  const site: SiteDTO = {
    id: s.id,
    shop_id: s.shop_id,
    shop_name: shop.name,
    subdomain: s.subdomain || "",
    primary_host: (s.primary_host || "").toLowerCase(),
    status: "PUBLISHED",
    config,
    published_version_id: s.published_version_id ?? null,
    published_version_number: s.published_version_number,
    published_at: s.published_at ?? null,
    created_at: s.created_at,
    updated_at: s.updated_at,
  };

  return { site, shop, config };
}

/**
 * Anonymous, tagged fetch of a PUBLISHED site by normalized host.
 * Tagged with `site-host:<host>` so publish/rollback/suspend revalidate it immediately.
 */
export const fetchPublishedSiteByHost = cache(
  async (rawHost: string): Promise<PublicSitePayload | null> => {
    const { hostname } = normalizeHostHeader(rawHost);
    if (!hostname) return null;

    try {
      const data = await gw<RawPublicSiteResponse>("/api/v1/sites/by-host", {
        auth: "none",
        query: { host: hostname },
        revalidate: SITE_REVALIDATE_SECONDS,
        tags: [`site-host:${hostname}`],
      });
      return normalizePublicSiteResponse(data);
    } catch (err) {
      if (err instanceof GatewayError && err.kind === "not_found") {
        return null;
      }
      logServer("warn", "site_by_host_fetch_failed", {
        host: hostname,
        kind: err instanceof GatewayError ? err.kind : "unknown",
        trace_id: err instanceof GatewayError ? err.traceId : "",
      });
      return null;
    }
  }
);

/**
 * Fetch a shop's PUBLISHED site by `shop_id`.
 * When `uncached: true` (checkout BFF verification), bypasses Next Data Cache (`cache: "no-store"`)
 * so newly suspended or unpublished sites are rejected immediately.
 */
export async function fetchPublishedSiteByShop(
  shopId: string,
  opts: { uncached?: boolean; expectedHost?: string } = {}
): Promise<PublicSitePayload | null> {
  const cleanShopId = shopId.trim();
  if (!cleanShopId) return null;

  try {
    let raw: RawPublicSiteResponse;
    if (opts.uncached) {
      const res = await fetch(
        `${GATEWAY_URL}/api/v1/sites/by-shop/${encodeURIComponent(cleanShopId)}`,
        {
          method: "GET",
          headers: { Accept: "application/json", traceparent: generateTraceparent() },
          cache: "no-store",
          signal: AbortSignal.timeout(GATEWAY_TIMEOUT_MS),
        }
      );
      if (res.status === 404) return null;
      if (!res.ok) return null;
      raw = (await res.json()) as RawPublicSiteResponse;
    } else {
      raw = await gw<RawPublicSiteResponse>(
        `/api/v1/sites/by-shop/${encodeURIComponent(cleanShopId)}`,
        {
          auth: "none",
          revalidate: SITE_REVALIDATE_SECONDS,
          tags: [`site-shop:${cleanShopId}`],
        }
      );
    }

    const parsed = normalizePublicSiteResponse(raw);
    if (!parsed) return null;
    if (parsed.site.shop_id !== cleanShopId) return null;

    if (opts.expectedHost) {
      const { hostname: expected } = normalizeHostHeader(opts.expectedHost);
      const { hostname: actual } = normalizeHostHeader(parsed.site.primary_host);
      if (!expected || !actual || expected !== actual) {
        return null;
      }
    }

    return parsed;
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "not_found") {
      return null;
    }
    logServer("warn", "site_by_shop_fetch_failed", {
      shopId: cleanShopId,
      kind: err instanceof GatewayError ? err.kind : "unknown",
      trace_id: err instanceof GatewayError ? err.traceId : "",
    });
    return null;
  }
}

export interface ShopSiteRenderData {
  shopProfile: Shop | null;
  categories: CategoryInfo[];
  products: Product[];
  vouchers: Voucher[];
  flashSlots: FlashSaleSlot[];
}

interface PagedProductsResponse {
  items?: GwProduct[] | null;
  total?: number;
}

async function fetchShopProductsRaw(
  shopId: string,
  cacheOpts: { revalidate?: number | false; tags?: string[] }
): Promise<GwProduct[]> {
  const all: GwProduct[] = [];
  const pageSize = 100;
  for (let page = 1; page <= 5; page++) {
    const res = await gw<PagedProductsResponse>("/api/v1/products", {
      auth: "none",
      query: { shop_id: shopId, page, page_size: pageSize },
      ...cacheOpts,
    });
    const batch = (res.items ?? []).filter((p) => p.shop_id === shopId);
    all.push(...batch);
    const total = typeof res.total === "number" ? res.total : batch.length;
    if ((res.items ?? []).length < pageSize || all.length >= total) break;
  }
  return all;
}

/**
 * Load ONLY a single shop's products, categories, flash prices, and `SHOP`-scoped vouchers.
 * Never returns products or vouchers belonging to another shop.
 * Reusable by both public subdomain site pages and Seller draft preview (`/seller/site/xem-truoc`).
 */
export async function loadShopSiteRenderData(
  shop: SitePublicShopDTO,
  opts: { siteId?: string; siteHost?: string; uncached?: boolean } = {}
): Promise<ShopSiteRenderData> {
  const tags: string[] = [];
  if (opts.siteId) tags.push(`site:${opts.siteId}`);
  if (opts.siteHost) tags.push(`site-host:${opts.siteHost.toLowerCase()}`);

  const cacheOpts = opts.uncached
    ? {}
    : {
        revalidate: SITE_REVALIDATE_SECONDS,
        tags: tags.length > 0 ? tags : undefined,
      };

  const [catsRes, productsRes, vouchersRes, flashRes] = await Promise.allSettled([
    gw<{ categories?: GwCategory[] | null }>("/api/v1/categories", {
      auth: "none",
      ...cacheOpts,
    }),
    fetchShopProductsRaw(shop.id, cacheOpts),
    gw<{ vouchers?: GwVoucher[] | null }>("/api/v1/vouchers", {
      auth: "none",
      query: { shop_id: shop.id, scope: "SHOP" },
      ...cacheOpts,
    }),
    gw<{ flash_sales?: GwFlashSale[] | null }>("/api/v1/flash-sales", {
      auth: "none",
      query: { include_upcoming: false },
      ...cacheOpts,
    }),
  ]);

  const gwCats = catsRes.status === "fulfilled" ? (catsRes.value.categories ?? []) : [];
  const categoryIndex = mergeCategories(gwCats, CATEGORIES);

  const gwShop: GwShop = {
    id: shop.id,
    name: shop.name,
    description: shop.description,
    status: shop.status ?? "ACTIVE",
  };
  const mergedShops = mergeShops([gwShop], SHOPS);
  const shopProfile = mergedShops[0] ?? null;

  const shopIdByBackend = new Map<string, string>();
  if (shopProfile) {
    shopIdByBackend.set(shop.id, shopProfile.id);
  }
  const shopIds = new Set<string>(shopProfile ? [shopProfile.id] : [shop.id]);

  // Defense in depth: strictly filter to products belonging ONLY to `shop.id`
  const rawShopProducts =
    productsRes.status === "fulfilled"
      ? productsRes.value.filter((p) => p.shop_id === shop.id)
      : [];

  const mergeCtx: ProductMergeContext = {
    content: PRODUCTS,
    categories: categoryIndex,
    shopIdByBackend,
  };
  let products = mergeProducts(rawShopProducts, mergeCtx).filter(
    (p) => p.shopId === (shopProfile?.id ?? shop.id)
  );

  let flashSlots: FlashSaleSlot[] = [];
  if (flashRes.status === "fulfilled" && (flashRes.value.flash_sales?.length ?? 0) > 0) {
    flashSlots = buildFlashSlots(flashRes.value.flash_sales ?? [], products, Date.now());
    if (flashSlots.length > 0) {
      products = markFlashProducts(products, flashSlots);
    }
  }

  // Defense in depth: keep ONLY `SHOP`-scoped vouchers belonging to `shop.id`
  const rawShopVouchers =
    vouchersRes.status === "fulfilled"
      ? (vouchersRes.value.vouchers ?? []).filter(
          (v) =>
            String(v.scope ?? "").toUpperCase() === "SHOP" &&
            v.shop_id === shop.id &&
            v.is_active !== false
        )
      : [];
  const vouchers = mergeVouchers(rawShopVouchers, VOUCHERS, shopIdByBackend, shopIds).filter(
    (v) => v.scope === "shop" && v.shopId === (shopProfile?.id ?? shop.id)
  );

  // Only list categories that actually have products in this shop
  const usedCategorySlugs = new Set(products.map((p) => p.categorySlug));
  const categories = categoryIndex.categories.filter((c) => usedCategorySlugs.has(c.slug));

  return {
    shopProfile,
    categories,
    products,
    vouchers,
    flashSlots,
  };
}

export interface PublicSitePageBundle {
  payload: PublicSitePayload;
  shopProfile: Shop | null;
  categories: CategoryInfo[];
  products: Product[];
  vouchers: Voucher[];
  flashSlots: FlashSaleSlot[];
}

/**
 * Full loader for public subdomain site pages (`/`, `/san-pham`, `/chinh-sach`, `/gioi-thieu`).
 */
export const loadPublicSitePageBundle = cache(
  async (rawHost: string): Promise<PublicSitePageBundle | null> => {
    const payload = await fetchPublishedSiteByHost(rawHost);
    if (!payload) return null;

    const renderData = await loadShopSiteRenderData(payload.shop, {
      siteId: payload.site.id,
      siteHost: payload.site.primary_host || rawHost,
    });

    return {
      payload,
      ...renderData,
    };
  }
);

/**
 * Load a single product on a subdomain site with live SKU stock, enforcing strict shop ownership.
 * Returns null if the product does not exist or belongs to any other shop.
 */
export async function loadPublicSiteProduct(
  rawHost: string,
  slugOrId: string
): Promise<{
  bundle: PublicSitePageBundle;
  product: Product;
  relatedProducts: Product[];
  liveFlashItem: FlashSaleItem | null;
} | null> {
  const bundle = await loadPublicSitePageBundle(rawHost);
  if (!bundle) return null;

  const cleanParam = slugOrId.trim().toLowerCase();
  const matched = bundle.products.find(
    (p) =>
      p.slug.toLowerCase() === cleanParam ||
      p.id.toLowerCase() === cleanParam ||
      p.backendId?.toLowerCase() === cleanParam
  );

  if (!matched) {
    return null;
  }

  let freshProduct = matched;
  if (matched.backendId) {
    try {
      const res = await gw<{ product?: GwProduct | null }>(
        `/api/v1/products/${encodeURIComponent(matched.backendId)}`,
        { auth: "none" }
      );
      if (res.product) {
        // Strict tenant isolation: reject if backend says product belongs to another shop.
        if (res.product.shop_id !== bundle.payload.site.shop_id) {
          return null;
        }
        const catsRes = await gw<{ categories?: GwCategory[] | null }>("/api/v1/categories", {
          auth: "none",
          revalidate: SITE_REVALIDATE_SECONDS,
        });
        const categoryIndex = mergeCategories(catsRes.categories ?? [], CATEGORIES);
        const shopIdByBackend = new Map<string, string>();
        if (bundle.shopProfile) {
          shopIdByBackend.set(bundle.payload.site.shop_id, bundle.shopProfile.id);
        }
        const merged = mergeProducts([res.product], {
          content: PRODUCTS,
          categories: categoryIndex,
          shopIdByBackend,
        })[0];
        if (merged) {
          freshProduct = {
            ...merged,
            isFlashSale: matched.isFlashSale,
          };
        }
      }
    } catch {
      // Fall back to matched list item if fresh fetch transiently fails
    }
  }

  const liveSlot = bundle.flashSlots.find((s) => s.status === "live");
  const liveFlashItem = liveSlot?.items.find((it) => it.productId === freshProduct.id) ?? null;
  const relatedProducts = bundle.products.filter((p) => p.id !== freshProduct.id).slice(0, 4);

  return {
    bundle,
    product: freshProduct,
    relatedProducts,
    liveFlashItem,
  };
}
