import { loadCatalogLookups } from "@/lib/catalog";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
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

    const body = await readJson<{ reason?: unknown; evidenceUrls?: unknown }>(req);
    const reason = typeof body?.reason === "string" ? body.reason.trim() : "";
    if (reason.length < 5) {
      throw new GatewayError("invalid", "INVALID_REASON");
    }
    const evidenceUrls = Array.isArray(body?.evidenceUrls)
      ? body.evidenceUrls.filter((u): u is string => typeof u === "string" && u.trim().length > 0)
      : [];

    const existing = await gw<{ order?: GwOrder | null }>(
      `/api/v1/orders/${encodeURIComponent(cleanId)}`
    );
    const scoped = assertInStorefront(existing.order, site.shopId);
    assertBuyerOrderOwnership(scoped, session);

    const res = await gw<{ order: GwOrder; return_request_id: string }>(
      `/api/v1/orders/${encodeURIComponent(cleanId)}/return`,
      {
        method: "POST",
        body: { reason: reason.slice(0, 500), evidence_urls: evidenceUrls },
      }
    );
    const catalog = await loadCatalogLookups();

    return Response.json(
      {
        order: mapGatewayOrder(res.order, catalog.lookups),
        returnRequestId: res.return_request_id,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_REASON: "Vui lòng nhập lý do đổi trả rõ ràng (tối thiểu 5 ký tự).",
      not_found: "Không tìm thấy đơn hàng này.",
      forbidden: "Bạn không có quyền gửi yêu cầu đổi trả cho đơn hàng này.",
      FAILED_PRECONDITION: "Đơn hàng hiện chưa đủ điều kiện hoặc đã quá thời hạn yêu cầu đổi trả.",
      conflict: "Không thể gửi yêu cầu đổi trả ở trạng thái hiện tại.",
    },
  }
);
