import { loadCatalogLookups } from "@/lib/catalog";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { assertInStorefront } from "@/lib/server/storefront";
import { assertBuyerOrderOwnership } from "@/features/account/orders.server";
import { mapGatewayOrder, type GwOrder } from "@/features/account/orderView";

type Params = { params: Promise<{ id: string }> };

export const GET = apiRoute<Params>(
  async (req, ctx) => {
    const site = await requireRequestSite(req);
    const session = await assertSession();
    const { id } = await ctx.params;
    const cleanId = id.trim();
    if (!cleanId) throw new GatewayError("not_found");

    const res = await gw<{ order?: GwOrder | null }>(
      `/api/v1/orders/${encodeURIComponent(cleanId)}`
    );
    // Orders from any other site (or legacy marketplace) are 404 here, even for the same user.
    const raw = assertInStorefront(res.order, site.shopId);
    assertBuyerOrderOwnership(raw, session);

    const catalog = await loadCatalogLookups();
    return Response.json(
      { order: mapGatewayOrder(raw, catalog.lookups) },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      not_found: "Không tìm thấy đơn hàng này.",
      forbidden: "Bạn không có quyền xem đơn hàng này.",
    },
  }
);
