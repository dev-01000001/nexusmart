import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import type { GwSellerOrder } from "@/features/seller/sellerApiTypes";
import { normalizeSellerReturns } from "@/features/seller/sellerReturns";
import { assertSellerSession } from "@/features/seller/sellerServer";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const GET = apiRoute<RouteCtx>(async (_req, ctx) => {
  const session = await assertSellerSession();
  const { id } = await ctx.params;
  const orderId = (id ?? "").trim();
  if (!orderId) {
    throw new GatewayError("invalid", "INVALID_ORDER_ID");
  }

  // Verify seller owns this order before reading return requests.
  const orderRes = await gw<{ order: GwSellerOrder | null }>(
    `/api/v1/seller/orders/${encodeURIComponent(orderId)}`
  );
  const order = orderRes.order ?? null;
  if (!order) {
    throw new GatewayError("not_found", "ORDER_NOT_FOUND");
  }
  if (order.shop_id !== session.user.shopId) {
    throw new GatewayError("forbidden", "ORDER_FORBIDDEN");
  }

  const rawReturns = await gw<unknown>(`/api/v1/orders/${encodeURIComponent(orderId)}/returns`);
  const returns = normalizeSellerReturns(rawReturns);

  return Response.json({ returns }, { headers: { "Cache-Control": "no-store" } });
});
