import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import {
  getSiteRootDomain,
  normalizeGwSite,
  type RawGwSite,
} from "@/features/seller/site/siteServer";

export const GET = apiRoute(async (req) => {
  await assertAdminSession();
  const q = req.nextUrl.searchParams;

  const data = await gw<{
    items?: RawGwSite[];
    sites?: RawGwSite[];
    total?: number;
    page?: number;
    page_size?: number;
    root_domain?: string;
  }>("/api/v1/admin/sites", {
    query: {
      status: q.get("status"),
      q: q.get("q"),
      page: q.get("page"),
      page_size: q.get("page_size"),
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
