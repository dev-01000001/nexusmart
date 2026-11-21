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

export const POST = apiRoute<Params>(
  async (req, ctx) => {
    const site = await requireRequestSite(req, { fresh: true });
    const session = await assertSession();
    const { id } = await ctx.params;
    const cleanId = id.trim();
    if (!cleanId) throw new GatewayError("not_found");

    const existing = await gw<{ order?: GwOrder | null }>(
      `/api/v1/orders/${encodeURIComponent(cleanId)}`
    );
    const scoped = assertInStorefront(existing.order, site.shopId);
    assertBuyerOrderOwnership(scoped, session);

    const res = await gw<{ order: GwOrder }>(
      `/api/v1/orders/${encodeURIComponent(cleanId)}/confirm-received`,
      { method: "POST" }
    );
    const catalog = await loadCatalogLookups();

    return Response.json(
      { order: mapGatewayOrder(res.order, catalog.lookups) },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      not_found: "Không tìm thấy đơn hàng này.",
      forbidden: "Bạn không có quyền xác nhận đơn hàng này.",
      FAILED_PRECONDITION: "Đơn hàng chưa ở trạng thái đã giao để xác nhận nhận hàng.",
      conflict: "Không thể xác nhận nhận hàng ở trạng thái hiện tại.",
    },
  }
);
