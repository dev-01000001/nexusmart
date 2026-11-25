// Server-only loader for Admin Site Management (/admin/sites).
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { loadAdminShops } from "@/features/admin/adminServer";
import {
  getSiteRootDomain,
  normalizeGwSite,
  type RawGwSite,
} from "@/features/seller/site/siteServer";
import type { GwSite } from "@/features/seller/site/siteEditorTypes";

export interface AdminSitesLoadResult {
  items: GwSite[];
  total: number;
  rootDomain: string;
  shopNameById: Record<string, string>;
  error: string | null;
}

export async function loadAdminSitesServer(
  status?: string,
  q?: string,
  pageSize = 100
): Promise<AdminSitesLoadResult> {
  const rootDomain = getSiteRootDomain();

  const [sitesRes, shopsRes] = await Promise.all([
    gw<{
      items?: RawGwSite[];
      sites?: RawGwSite[];
      total?: number;
      root_domain?: string;
    }>("/api/v1/admin/sites", {
      query: {
        status: status || undefined,
        q: q || undefined,
        page: 1,
        page_size: pageSize,
      },
    })
      .then((data) => ({ data, error: null as string | null }))
      .catch((err) => ({
        data: null,
        error: err instanceof GatewayError ? SAFE_MESSAGES[err.kind] : SAFE_MESSAGES.unavailable,
      })),
    loadAdminShops(undefined, 200),
  ]);

  const shopNameById: Record<string, string> = {};
  for (const shop of shopsRes.data?.items ?? []) {
    shopNameById[shop.id] = shop.name;
  }

  if (sitesRes.error || !sitesRes.data) {
    return {
      items: [],
      total: 0,
      rootDomain,
      shopNameById,
      error: sitesRes.error ?? SAFE_MESSAGES.unavailable,
    };
  }

  const rawItems = sitesRes.data.items ?? sitesRes.data.sites ?? [];
  const items: GwSite[] = [];
  for (const raw of rawItems) {
    const normalized = normalizeGwSite(raw);
    if (!normalized) continue;
    items.push({
      ...normalized,
      shop_name: normalized.shop_name || shopNameById[normalized.shop_id] || "",
    });
  }

  return {
    items,
    total: sitesRes.data.total ?? items.length,
    rootDomain: sitesRes.data.root_domain?.trim().toLowerCase() || rootDomain,
    shopNameById,
    error: null,
  };
}
