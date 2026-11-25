import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { normalizeSellerOrder, type GwSellerOrder } from "@/features/seller/sellerApiTypes";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface CancelBody {
  reason?: unknown;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    const session = await assertSellerSession();
    const { id } = await ctx.params;
    const orderId = assertValidId(id, "INVALID_ORDER_ID");

    const raw = await readJson<CancelBody>(req);
    const reason = typeof raw?.reason === "string" ? raw.reason.trim().slice(0, 500) : "";
    if (reason.length < 3) {
      throw new GatewayError("invalid", "INVALID_CANCEL_REASON");
    }

    // Verify seller owns this order before cancelling via gateway.
    const existing = await gw<{ order: GwSellerOrder | null }>(
      `/api/v1/seller/orders/${encodeURIComponent(orderId)}`
    );
    if (!existing.order || existing.order.shop_id !== session.user.shopId) {
      throw new GatewayError("not_found");
    }

    const data = await gw<{ order: GwSellerOrder }>(
      `/api/v1/orders/${encodeURIComponent(orderId)}/cancel`,
      {
        method: "POST",
        body: { reason },
      }
    );
    return Response.json(
      { order: data.order ? normalizeSellerOrder(data.order) : data.order },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_ORDER_ID: "Mã đơn hàng không hợp lệ.",
      INVALID_CANCEL_REASON: "Vui lòng nhập lý do hủy đơn (tối thiểu 3 ký tự).",
    },
  }
);
