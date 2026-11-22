import type { GatewayProduct, GatewayPublicShop, PaginatedResponse } from "@/lib/gateway-types";
import { gatewayFetch } from "@/lib/server/gateway";
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

export async function fetchPublicShopsMap(): Promise<Record<string, GatewayPublicShop>> {
  try {
    const res = await gatewayFetch<PaginatedResponse<GatewayPublicShop>>(
      "/shops?page=1&page_size=50",
      {
        token: false,
        cache: "no-store",
      }
    );
    const map: Record<string, GatewayPublicShop> = {};
    for (const shop of res?.items ?? []) {
      map[shop.id] = shop;
    }
    return map;
  } catch {
    return {};
  }
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
