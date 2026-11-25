import type { GatewayProduct, GatewayPublicShop, PaginatedResponse } from "@/lib/gateway-types";
import { gatewayFetch } from "@/lib/server/gateway";
import {
  DEFAULT_SHOP_LOOKUP_CONCURRENCY,
  fetchAllPublicShopsPagesWithFetcher,
  lookupShopsByIdsWithFetcher,
} from "@/lib/shop-lookup";
import type { OrderVisualLookup } from "./types";

export type SettledResult<T> = { ok: true; data: T } | { ok: false; error: unknown };

export async function settleGateway<T>(promise: Promise<T>): Promise<SettledResult<T>> {
  try {
    const data = await promise;
    return { ok: true, data };
  } catch (error) {
    return { ok: false, error };
  }
}

async function fetchPublicShopByIdServer(id: string): Promise<GatewayPublicShop | null> {
  try {
    const res = await gatewayFetch<{ shop?: GatewayPublicShop }>(
      `/shops/${encodeURIComponent(id)}`,
      {
        token: false,
        cache: "no-store",
      }
    );
    return res?.shop ?? null;
  } catch {
    return null;
  }
}

/**
 * Resolves public shop metadata for specific shop IDs via parallel `GET /shops/{id}`
 * with deduplication and concurrency <= 8 (§10-3).
 */
export async function resolvePublicShopsByIds(
  shopIds: readonly string[],
  existing?: Record<string, GatewayPublicShop>
): Promise<Record<string, GatewayPublicShop>> {
  return lookupShopsByIdsWithFetcher(shopIds, fetchPublicShopByIdServer, {
    existing,
    concurrency: DEFAULT_SHOP_LOOKUP_CONCURRENCY,
  });
}

/**
 * Loads public shops map without dropping shop 101+:
 * - When `shopIds` are provided, resolves those exact shops via `GET /shops/{id}` (concurrency <= 8).
 * - When called without arguments (e.g. account/order pages fetching in parallel before orders resolve),
 *   paginates `GET /shops?page=N&page_size=100` across pages (bounded concurrency <= 8).
 */
export async function fetchPublicShopsMap(
  shopIds?: readonly string[]
): Promise<Record<string, GatewayPublicShop>> {
  if (shopIds && shopIds.length > 0) {
    return resolvePublicShopsByIds(shopIds);
  }
  return fetchAllPublicShopsPagesWithFetcher(
    (page, pageSize) =>
      gatewayFetch<PaginatedResponse<GatewayPublicShop>>(
        `/shops?page=${page}&page_size=${pageSize}`,
        {
          token: false,
          cache: "no-store",
        }
      ),
    { concurrency: DEFAULT_SHOP_LOOKUP_CONCURRENCY }
  );
}

export async function fetchOrderVisualLookup(shopId?: string): Promise<OrderVisualLookup> {
  const byProductId: OrderVisualLookup["byProductId"] = {};
  const bySkuId: OrderVisualLookup["bySkuId"] = {};
  const query = shopId
    ? `/products?shop_id=${encodeURIComponent(shopId)}&page=1&page_size=100`
    : "/products?page=1&page_size=100";

  try {
    const res = await gatewayFetch<PaginatedResponse<GatewayProduct>>(query, {
      token: false,
      cache: "no-store",
    });
    for (const prod of res?.items ?? []) {
      const primaryImg = prod.images?.[0];
      byProductId[prod.id] = {
        slug: prod.slug || undefined,
        image: primaryImg || undefined,
      };
      for (const sku of prod.skus ?? []) {
        const skuImg = sku.image_url || primaryImg;
        if (skuImg) {
          bySkuId[sku.id] = skuImg;
        }
      }
    }
  } catch {
    // Best-effort visual lookup — orders still render cleanly without thumbnails.
  }

  return { byProductId, bySkuId };
}
