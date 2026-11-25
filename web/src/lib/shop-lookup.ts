import { api } from "@/lib/api";
import type { GatewayPublicShop, PaginatedResponse } from "@/lib/gateway-types";

export const DEFAULT_SHOP_LOOKUP_CONCURRENCY = 8;
export const MAX_SHOP_LOOKUP_CONCURRENCY = 8;
const DEFAULT_MAX_SHOP_PAGES = 20;
const SHOP_PAGE_SIZE = 100;

export interface LookupShopsOptions {
  existing?: Record<string, GatewayPublicShop> | Map<string, GatewayPublicShop>;
  concurrency?: number;
}

/**
 * Resolves public shop metadata via parallel `GET /shops/{id}` calls with strict
 * deduplication, cache reuse of `existing` entries, and bounded concurrency (<= 8).
 * Prevents 101+ shops from falling back to "Gian hàng NexusMart" (§10-3).
 */
export async function lookupShopsByIdsWithFetcher(
  shopIds: readonly string[],
  fetchOne: (shopId: string) => Promise<GatewayPublicShop | null>,
  options?: LookupShopsOptions
): Promise<Record<string, GatewayPublicShop>> {
  const out: Record<string, GatewayPublicShop> = {};
  if (options?.existing instanceof Map) {
    for (const [id, shop] of options.existing.entries()) {
      if (id && shop) out[id] = shop;
    }
  } else if (options?.existing) {
    for (const [id, shop] of Object.entries(options.existing)) {
      if (id && shop) out[id] = shop;
    }
  }

  const missingIds: string[] = [];
  const seen = new Set<string>(Object.keys(out));
  for (const rawId of shopIds) {
    const id = (rawId ?? "").trim();
    if (!id || seen.has(id)) continue;
    seen.add(id);
    missingIds.push(id);
  }

  if (missingIds.length === 0) {
    return out;
  }

  const concurrency = Math.max(
    1,
    Math.min(
      MAX_SHOP_LOOKUP_CONCURRENCY,
      Math.trunc(options?.concurrency ?? DEFAULT_SHOP_LOOKUP_CONCURRENCY)
    )
  );

  let cursor = 0;
  const workerCount = Math.min(concurrency, missingIds.length);
  const workers = Array.from({ length: workerCount }, async () => {
    while (true) {
      const idx = cursor;
      cursor += 1;
      if (idx >= missingIds.length) return;
      const targetId = missingIds[idx];
      try {
        const shop = await fetchOne(targetId);
        if (shop && shop.id) {
          out[shop.id] = shop;
        }
      } catch {
        // Best-effort lookup: 404 or transient error for one shop must not block others.
      }
    }
  });

  await Promise.all(workers);
  return out;
}

/**
 * Browser-side helper calling `/api/gw/shops/{id}` in parallel with dedup & concurrency <= 8.
 */
export async function fetchMissingShopsClient(
  shopIds: readonly string[],
  existing?: Record<string, GatewayPublicShop> | Map<string, GatewayPublicShop>,
  concurrency = DEFAULT_SHOP_LOOKUP_CONCURRENCY
): Promise<Record<string, GatewayPublicShop>> {
  return lookupShopsByIdsWithFetcher(
    shopIds,
    async (id) => {
      const res = await api<{ shop?: GatewayPublicShop }>(`shops/${encodeURIComponent(id)}`, {
        retryOn401: false,
      });
      return res?.shop ?? null;
    },
    { existing, concurrency }
  );
}

/**
 * Paginates `GET /shops?page=N&page_size=100` across all pages (up to `maxPages`, bounded
 * concurrency <= 8) for callers that cannot pre-enumerate shop IDs before fetching orders.
 */
export async function fetchAllPublicShopsPagesWithFetcher(
  fetchPage: (page: number, pageSize: number) => Promise<PaginatedResponse<GatewayPublicShop>>,
  options?: { maxPages?: number; concurrency?: number }
): Promise<Record<string, GatewayPublicShop>> {
  const out: Record<string, GatewayPublicShop> = {};
  let first: PaginatedResponse<GatewayPublicShop>;
  try {
    first = await fetchPage(1, SHOP_PAGE_SIZE);
  } catch {
    return out;
  }

  for (const shop of first?.items ?? []) {
    if (shop?.id) out[shop.id] = shop;
  }

  const total = Math.max(first?.items?.length ?? 0, Math.trunc(first?.total ?? 0));
  const maxPages = Math.max(1, Math.trunc(options?.maxPages ?? DEFAULT_MAX_SHOP_PAGES));
  const totalPages = Math.min(maxPages, Math.ceil(total / SHOP_PAGE_SIZE));
  if (totalPages <= 1) {
    return out;
  }

  const remainingPages: number[] = [];
  for (let p = 2; p <= totalPages; p += 1) {
    remainingPages.push(p);
  }

  const concurrency = Math.max(
    1,
    Math.min(
      MAX_SHOP_LOOKUP_CONCURRENCY,
      Math.trunc(options?.concurrency ?? DEFAULT_SHOP_LOOKUP_CONCURRENCY)
    )
  );
  let cursor = 0;
  const workerCount = Math.min(concurrency, remainingPages.length);
  const workers = Array.from({ length: workerCount }, async () => {
    while (true) {
      const idx = cursor;
      cursor += 1;
      if (idx >= remainingPages.length) return;
      const pageNum = remainingPages[idx];
      try {
        const res = await fetchPage(pageNum, SHOP_PAGE_SIZE);
        for (const shop of res?.items ?? []) {
          if (shop?.id) out[shop.id] = shop;
        }
      } catch {
        // Ignore single page failure; preserve shops collected from other pages.
      }
    }
  });

  await Promise.all(workers);
  return out;
}
