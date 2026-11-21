// Server-only helpers shared by every page under `app/(site)/s/[host]/` (site host pages).
import { cache } from "react";
import { headers } from "next/headers";
import type { FlashSaleSlot } from "@/data/flash-sale";
import type { Product } from "@/data/products";
import { VOUCHERS, type Voucher } from "@/data/vouchers";
import {
  buildFlashSlots,
  markFlashProducts,
  mergeVouchers,
  type GwFlashSale,
} from "@/lib/catalog-map";
import type { GwVoucher as CommerceGwVoucher } from "@/features/commerce/types";
import { APP_ORIGINS, getSiteRootDomain } from "@/lib/server/config";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { fetchPublishedSiteByHost, loadPublicSitePageBundle } from "@/lib/server/sites";
import { deriveMarketplaceOrigin, deriveSiteOrigin } from "@/lib/site/host";
import type { PublicSitePayload } from "@/lib/site/types";

export interface SiteRequestContext {
  payload: PublicSitePayload;
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
    const payload = await fetchPublishedSiteByHost(decodedHost);
    if (!payload) return null;

    const siteHost = payload.site.primary_host || decodedHost;
    const h = await headers();
    const rawHost = resolveRequestHost(h, siteHost);
    const proto = resolveRequestProto(h, "http://localhost");
    return {
      payload,
      siteHost,
      siteOrigin: deriveSiteOrigin({ siteHost, rawHost, proto }),
      platformOrigin: deriveMarketplaceOrigin({
        rawHost,
        proto,
        rootDomain: getSiteRootDomain(),
        appOrigin: APP_ORIGINS[0],
      }),
      brandName: payload.config.theme.brand_name?.trim() || payload.shop.name,
    };
  }
);

/**
 * Live + upcoming flash sales strictly filtered to this site's products.
 */
export async function loadSiteFlashSales(hostParam: string): Promise<{
  brandName: string;
  shopName: string;
  slots: FlashSaleSlot[];
  products: Product[];
  error: string | null;
} | null> {
  const decodedHost = decodeURIComponent(hostParam);
  const bundle = await loadPublicSitePageBundle(decodedHost);
  if (!bundle) return null;

  const brandName = bundle.payload.config.theme.brand_name?.trim() || bundle.payload.shop.name;
  try {
    const res = await gw<{ flash_sales?: GwFlashSale[] | null }>("/api/v1/flash-sales", {
      auth: "none",
      query: { include_upcoming: true },
    });
    const slots = buildFlashSlots(res.flash_sales ?? [], bundle.products, Date.now());
    const products = slots.length > 0 ? markFlashProducts(bundle.products, slots) : bundle.products;
    return {
      brandName,
      shopName: bundle.payload.shop.name,
      slots,
      products,
      error: null,
    };
  } catch (err) {
    return {
      brandName,
      shopName: bundle.payload.shop.name,
      slots: [],
      products: bundle.products,
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
  const decodedHost = decodeURIComponent(hostParam);
  const bundle = await loadPublicSitePageBundle(decodedHost);
  if (!bundle) return null;

  const shopId = bundle.payload.site.shop_id;
  const brandName = bundle.payload.config.theme.brand_name?.trim() || bundle.payload.shop.name;

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
