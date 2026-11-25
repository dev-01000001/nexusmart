import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";
import { normalizeSellerOrder, type GwSellerOrder } from "@/features/seller/sellerApiTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const GET = apiRoute<RouteCtx>(async (_req, ctx) => {
  const session = await assertSellerSession();
  const { id } = await ctx.params;
  const orderId = assertValidId(id, "INVALID_ORDER_ID");
  const data = await gw<{ order: GwSellerOrder | null }>(
    `/api/v1/seller/orders/${encodeURIComponent(orderId)}`
  );
  if (!data.order || data.order.shop_id !== session.user.shopId) {
    throw new GatewayError("not_found");
  }
  return Response.json(
    { order: normalizeSellerOrder(data.order) },
    { headers: { "Cache-Control": "no-store" } }
  );
});
