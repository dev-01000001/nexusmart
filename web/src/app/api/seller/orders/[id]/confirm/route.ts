import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";
import { normalizeSellerOrder, type GwSellerOrder } from "@/features/seller/sellerApiTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const orderId = assertValidId(id, "INVALID_ORDER_ID");
    const data = await gw<{ order: GwSellerOrder }>(
      `/api/v1/seller/orders/${encodeURIComponent(orderId)}/confirm`,
      { method: "POST" }
    );
    return Response.json(
      { order: data.order ? normalizeSellerOrder(data.order) : data.order },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_ORDER_ID: "Mã đơn hàng không hợp lệ.",
      FAILED_PRECONDITION: "Đơn hàng hiện không ở trạng thái chờ gian hàng xác nhận.",
    },
  }
);
