import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import {
  ALLOWED_SITE_STATUSES,
  parseOptionalPositiveInt,
} from "@/features/admin/AdminConfigValidators";
import {
  getSiteRootDomain,
  normalizeGwSite,
  type RawGwSite,
} from "@/features/seller/site/siteServer";

export const GET = apiRoute(async (req) => {
  await assertCapability("sites.read");
  const q = req.nextUrl.searchParams;
  const rawStatus = (q.get("status") ?? "").trim().toUpperCase();
  const status = (ALLOWED_SITE_STATUSES as readonly string[]).includes(rawStatus)
    ? rawStatus
    : undefined;
  const search = (q.get("q") ?? q.get("query") ?? "").trim().slice(0, 120) || undefined;

  const data = await gw<{
    items?: RawGwSite[];
    sites?: RawGwSite[];
    total?: number;
    page?: number;
    page_size?: number;
    root_domain?: string;
  }>("/api/v1/admin/sites", {
    query: {
      status,
      q: search,
      page: parseOptionalPositiveInt(q.get("page"), 10000),
      page_size: parseOptionalPositiveInt(q.get("page_size"), 100),
    },
  });

  const rawItems = data.items ?? data.sites ?? [];
  const items = rawItems
    .map((item) => normalizeGwSite(item))
    .filter((item): item is NonNullable<typeof item> => item !== null);

  return Response.json(
    {
      items,
      sites: items,
      total: data.total ?? items.length,
      page: data.page ?? 1,
      page_size: data.page_size ?? items.length,
      root_domain: data.root_domain?.trim().toLowerCase() || getSiteRootDomain(),
    },
    { headers: { "Cache-Control": "no-store" } }
  );
});
