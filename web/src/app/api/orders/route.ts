import { loadCatalogLookups } from "@/lib/catalog";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { STOREFRONT_CHANNEL, belongsToStorefront } from "@/lib/server/storefront";
import { mapGatewayOrder, type GwOrder } from "@/features/account/orderView";

/** Buyer's orders placed on THIS site's shop only (filtered at backend by storefront_shop_id). */
export const GET = apiRoute(async (req) => {
  const site = await requireRequestSite(req);
  await assertSession();

  const { searchParams } = req.nextUrl;
  const status = searchParams.get("status")?.trim() || undefined;
  const page = Math.max(1, Math.trunc(Number(searchParams.get("page")) || 1));
  const pageSize = Math.max(
    1,
    Math.min(
      100,
      Math.trunc(Number(searchParams.get("page_size") ?? searchParams.get("pageSize")) || 50)
    )
  );

  // Never trust client sales_channel / storefront_shop_id / shop_id query params.
  const res = await gw<{
    items?: GwOrder[] | null;
    page?: number;
    page_size?: number;
    total?: number;
  }>("/api/v1/orders", {
    query: {
      status,
      sales_channel: STOREFRONT_CHANNEL,
      storefront_shop_id: site.shopId,
      page,
      page_size: pageSize,
    },
  });

  const rawItems = res.items ?? [];
  const scoped = rawItems.filter((o) => belongsToStorefront(o, site.shopId));
  const total = rawItems.length === scoped.length ? (res.total ?? scoped.length) : scoped.length;

  if (scoped.length === 0) {
    return Response.json(
      { items: [], page: res.page ?? page, pageSize: res.page_size ?? pageSize, total: 0 },
      { headers: { "Cache-Control": "no-store" } }
    );
  }

  const catalog = await loadCatalogLookups();
  const items = scoped.map((o) => mapGatewayOrder(o, catalog.lookups));

  return Response.json(
    { items, page: res.page ?? page, pageSize: res.page_size ?? pageSize, total },
    { headers: { "Cache-Control": "no-store" } }
  );
});
