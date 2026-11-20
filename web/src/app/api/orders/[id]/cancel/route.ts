import { loadCatalogLookups } from "@/lib/catalog";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { assertBuyerOrderOwnership } from "@/features/account/orders.server";
import { mapGatewayOrder, type GwOrder } from "@/features/account/orderView";

type Params = { params: Promise<{ id: string }> };

export const POST = apiRoute<Params>(
  async (req, ctx) => {
    const session = await assertSession();
    const { id } = await ctx.params;
    const cleanId = id.trim();
    if (!cleanId) throw new GatewayError("not_found");

    let reason = "";
    const rawText = await req.text();
    if (rawText.trim()) {
      try {
        const parsed = JSON.parse(rawText) as { reason?: unknown };
        if (typeof parsed?.reason === "string") {
          reason = parsed.reason.trim().slice(0, 300);
        }
      } catch {
        throw new GatewayError("invalid", "INVALID_JSON");
      }
    }

    // Defense-in-depth buyer ownership check on top of gateway + order service RBAC.
    const existing = await gw<{ order?: GwOrder | null }>(
      `/api/v1/orders/${encodeURIComponent(cleanId)}`
    );
    assertBuyerOrderOwnership(existing.order, session);

    const res = await gw<{ order: GwOrder }>(
      `/api/v1/orders/${encodeURIComponent(cleanId)}/cancel`,
      {
        method: "POST",
        body: { reason },
      }
    );
    const catalog = await loadCatalogLookups();

    return Response.json(
      { order: mapGatewayOrder(res.order, catalog.lookups) },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      not_found: "Không tìm thấy đơn hàng cần hủy.",
      forbidden: "Bạn không có quyền hủy đơn hàng này.",
      FAILED_PRECONDITION: "Đơn hàng đã chuyển sang bước xử lý tiếp theo nên không thể hủy.",
      conflict: "Đơn hàng hiện không thể hủy. Bạn vui lòng tải lại trang.",
    },
  }
);
