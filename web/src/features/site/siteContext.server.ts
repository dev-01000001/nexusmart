// Server-only helpers shared by every page under `app/(site)/s/[host]/` (site host pages).
import { cache } from "react";
import { headers } from "next/headers";
import type { FlashSaleSlot } from "@/data/flash-sale";
import { CATEGORIES, PRODUCTS, type CategoryInfo, type Product } from "@/data/products";
import { SHOPS, type Shop } from "@/data/shops";
import { VOUCHERS, type Voucher } from "@/data/vouchers";
import {
  mergeShops,
  mergeVouchers,
  resolveFreshShopFlashSales,
  type GwCategory,
  type GwFlashSale,
  type GwProduct,
  type GwShop,
} from "@/lib/catalog-map";
import type { GwVoucher as CommerceGwVoucher } from "@/features/commerce/types";
import { APP_ORIGINS, getSiteRootDomain } from "@/lib/server/config";
import { GatewayError, SAFE_MESSAGES, logServer } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { loadShopSiteRenderData } from "@/lib/server/sites";
import { deriveMarketplaceOrigin, deriveSiteOrigin, normalizeHostHeader } from "@/lib/site/host";
import {
  parseSiteConfig,
  type PublicSitePayload,
  type SiteConfig,
  type SiteDTO,
  type SitePublicShopDTO,
} from "@/lib/site/types";
import type { SiteTemplateConfig } from "./templateTypes";
import { normalizeSiteTemplateConfig } from "./templateValidator";

const SITE_REVALIDATE_SECONDS = 30;

interface RawPublicSiteResponse {
  site?: Partial<SiteDTO> & {
    config?: unknown;
    published_config?: unknown;
    config_json?: unknown;
  };
  shop?: Partial<SitePublicShopDTO>;
  config?: unknown;
}

interface PublishedSiteTemplateRecord {
  payload: PublicSitePayload;
  templateConfig: SiteTemplateConfig;
}

function normalizePublishedSiteTemplate(
  raw: RawPublicSiteResponse | null | undefined
): PublishedSiteTemplateRecord | null {
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
  const templateConfig = normalizeSiteTemplateConfig(configSource, shop.name);
  if (!templateConfig) return null;

  const v1Config: SiteConfig = parseSiteConfig(configSource, shop.name) ?? {
    theme: templateConfig.theme,
    sections: [],
  };

  const site: SiteDTO = {
    id: s.id,
    shop_id: s.shop_id,
    shop_name: shop.name,
    subdomain: s.subdomain || "",
    primary_host: (s.primary_host || "").toLowerCase(),
    status: "PUBLISHED",
    config: v1Config,
    published_version_id: s.published_version_id ?? null,
    published_version_number: s.published_version_number,
    published_at: s.published_at ?? null,
    created_at: s.created_at,
    updated_at: s.updated_at,
  };

  return {
    payload: { site, shop, config: v1Config },
    templateConfig,
  };
}

/**
 * Anonymous, tagged fetch of a PUBLISHED site preserving FULL v2 `SiteTemplateConfig`
 * (including `layout`, `sections` with `variant`/`settings`/`blocks`, and `pages.*`),
 * while auto-upgrading legacy v1 configs seamlessly.
 */
export const fetchPublishedSiteTemplateByHost = cache(
  async (rawHost: string): Promise<PublishedSiteTemplateRecord | null> => {
    const { hostname } = normalizeHostHeader(rawHost);
    if (!hostname) return null;

    try {
      const data = await gw<RawPublicSiteResponse>("/api/v1/sites/by-host", {
        auth: "none",
        query: { host: hostname },
        revalidate: SITE_REVALIDATE_SECONDS,
        tags: [`site-host:${hostname}`],
      });
      return normalizePublishedSiteTemplate(data);
    } catch (err) {
      if (err instanceof GatewayError && err.kind === "not_found") {
        return null;
      }
      logServer("warn", "site_template_by_host_fetch_failed", {
        host: hostname,
        kind: err instanceof GatewayError ? err.kind : "unknown",
      });
      return null;
    }
  }
);

export interface SiteRequestContext {
  payload: PublicSitePayload;
  /** Full v2 template config (legacy v1 configs are upgraded losslessly). */
  templateConfig: SiteTemplateConfig;
  /** Normalized site hostname (primary host when the backend reports one). */
  siteHost: string;
  /** Public origin of this site, e.g. `http://bach-dang.nexusmart.localhost:3402`. */
  siteOrigin: string;
  /** Origin of the NexusMart root host. */
  platformOrigin: string;
  brandName: string;
}

/**
 * The PUBLISHED site behind a `[host]` route segment (set by the middleware rewrite from the
 * request `Host`, never from client input). Null when the site is unknown, draft or suspended.
 */
export const loadSiteContext = cache(
  async (hostParam: string): Promise<SiteRequestContext | null> => {
    const decodedHost = decodeURIComponent(hostParam);
    const record = await fetchPublishedSiteTemplateByHost(decodedHost);
    if (!record) return null;

    const { payload, templateConfig } = record;
    const siteHost = payload.site.primary_host || decodedHost;
    const h = await headers();
    const rawHost = resolveRequestHost(h, siteHost);
    const proto = resolveRequestProto(h, "http://localhost");
    return {
      payload,
      templateConfig,
      siteHost,
      siteOrigin: deriveSiteOrigin({ siteHost, rawHost, proto }),
      platformOrigin: deriveMarketplaceOrigin({
        rawHost,
        proto,
        rootDomain: getSiteRootDomain(),
        appOrigin: APP_ORIGINS[0],
      }),
      brandName: templateConfig.theme.brand_name?.trim() || payload.shop.name,
    };
  }
);

export interface SiteTemplateBundle {
  payload: PublicSitePayload;
  templateConfig: SiteTemplateConfig;
  shopProfile: Shop | null;
  categories: CategoryInfo[];
  products: Product[];
  vouchers: Voucher[];
  flashSlots: FlashSaleSlot[];
}

/**
 * Full multi-template storefront bundle for public site pages (`/`, `/san-pham`, `/gio-hang`,
 * `/gioi-thieu`, `/chinh-sach`, `/tim-kiem`), preserving full v2 `SiteTemplateConfig`.
 */
export const loadSiteTemplateBundle = cache(
  async (hostParam: string): Promise<SiteTemplateBundle | null> => {
    const decodedHost = decodeURIComponent(hostParam);
    const record = await fetchPublishedSiteTemplateByHost(decodedHost);
    if (!record) return null;

    const { payload, templateConfig } = record;
    const renderData = await loadShopSiteRenderData(payload.shop, {
      siteId: payload.site.id,
      siteHost: payload.site.primary_host || decodedHost,
    });

    return {
      payload,
      templateConfig,
      ...renderData,
    };
  }
);

/**
 * Live + upcoming flash sales strictly filtered to this site's shop using fresh product fetches
 * (no bundle product cache, bounded batch `sku_ids` queries, excludes hidden/deleted/foreign items).
 */
export async function loadSiteFlashSales(hostParam: string): Promise<{
  brandName: string;
  shopName: string;
  slots: FlashSaleSlot[];
  products: Product[];
  error: string | null;
} | null> {
  const decodedHost = decodeURIComponent(hostParam);
  const record = await fetchPublishedSiteTemplateByHost(decodedHost);
  if (!record) return null;

  const { payload, templateConfig } = record;
  const shop = payload.shop;
  const shopId = payload.site.shop_id;
  const brandName = templateConfig.theme.brand_name?.trim() || shop.name;

  try {
    const res = await gw<{ flash_sales?: GwFlashSale[] | null }>("/api/v1/flash-sales", {
      auth: "none",
      query: { include_upcoming: true },
    });

    const gwShop: GwShop = {
      id: shopId,
      name: shop.name,
      description: shop.description,
      status: shop.status ?? "ACTIVE",
    };
    const shopProfile = mergeShops([gwShop], SHOPS)[0] ?? null;
    const shopIdByBackend = new Map<string, string>();
    if (shopProfile) {
      shopIdByBackend.set(shopId, shopProfile.id);
    }

    const { slots, products } = await resolveFreshShopFlashSales({
      shopId,
      flashSales: res.flash_sales ?? [],
      shopIdByBackend,
      contentProducts: PRODUCTS,
      contentCategories: CATEGORIES,
      fetchCategories: async () => {
        const catsRes = await gw<{ categories?: GwCategory[] | null }>("/api/v1/categories", {
          auth: "none",
          revalidate: SITE_REVALIDATE_SECONDS,
        });
        return catsRes.categories ?? [];
      },
      fetchProductsBySkuIds: async (skuIds) => {
        const prodRes = await gw<{ items?: GwProduct[] | null }>("/api/v1/products", {
          auth: "none",
          query: { shop_id: shopId, sku_ids: skuIds.join(","), page: 1, page_size: 100 },
        });
        return prodRes.items ?? [];
      },
    });

    return {
      brandName,
      shopName: shop.name,
      slots,
      products,
      error: null,
    };
  } catch (err) {
    return {
      brandName,
      shopName: shop.name,
      slots: [],
      products: [],
      error: err instanceof GatewayError ? SAFE_MESSAGES[err.kind] : SAFE_MESSAGES.unavailable,
    };
  }
}

/**
 * Live `SHOP`-scoped vouchers for this site only, preserving the signed-in buyer's `saved` state.
 */
export async function loadSiteVouchers(hostParam: string): Promise<{
  brandName: string;
  shopId: string;
  vouchers: Voucher[];
  rawVouchers: CommerceGwVoucher[];
  error: string | null;
} | null> {
  const bundle = await loadSiteTemplateBundle(hostParam);
  if (!bundle) return null;

  const shopId = bundle.payload.site.shop_id;
  const brandName = bundle.templateConfig.theme.brand_name?.trim() || bundle.payload.shop.name;

  try {
    let gwVouchers: CommerceGwVoucher[] = [];
    try {
      const res = await gw<{ vouchers?: CommerceGwVoucher[] | null }>("/api/v1/vouchers", {
        auth: "user",
        query: { shop_id: shopId, scope: "SHOP" },
      });
      gwVouchers = res.vouchers ?? [];
    } catch (err) {
      if (err instanceof GatewayError && err.kind === "unauthenticated") {
        const res = await gw<{ vouchers?: CommerceGwVoucher[] | null }>("/api/v1/vouchers", {
          auth: "none",
          query: { shop_id: shopId, scope: "SHOP" },
        });
        gwVouchers = res.vouchers ?? [];
      } else {
        throw err;
      }
    }

    const strictlyShop = gwVouchers.filter(
      (v) =>
        String(v.scope ?? "").toUpperCase() === "SHOP" &&
        v.shop_id === shopId &&
        v.is_active !== false
    );
    const shopIdByBackend = new Map<string, string>();
    const catalogShopId = bundle.shopProfile?.id ?? shopId;
    shopIdByBackend.set(shopId, catalogShopId);
    const vouchers = mergeVouchers(
      strictlyShop,
      VOUCHERS,
      shopIdByBackend,
      new Set([catalogShopId])
    ).filter((v) => v.scope === "shop" && v.shopId === catalogShopId);

    return {
      brandName,
      shopId,
      vouchers,
      rawVouchers: strictlyShop,
      error: null,
    };
  } catch (err) {
    return {
      brandName,
      shopId,
      vouchers: bundle.vouchers,
      rawVouchers: [],
      error: err instanceof GatewayError ? SAFE_MESSAGES[err.kind] : SAFE_MESSAGES.unavailable,
    };
  }
}
