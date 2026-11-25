import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import type { GwOrderShipment, GwSellerOrder } from "@/features/seller/sellerApiTypes";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const GET = apiRoute<RouteCtx>(async (req, ctx) => {
  const session = await assertSellerSession();
  const { id } = await ctx.params;
  const orderId = assertValidId(id, "INVALID_ORDER_ID");

  const existing = await gw<{ order: GwSellerOrder | null }>(
    `/api/v1/seller/orders/${encodeURIComponent(orderId)}`
  );
  if (!existing.order || existing.order.shop_id !== session.user.shopId) {
    throw new GatewayError("not_found");
  }

  const rawDirection = (req.nextUrl.searchParams.get("direction") ?? "").trim().toUpperCase();
  const direction =
    rawDirection === "FORWARD" || rawDirection === "RETURN" ? rawDirection : undefined;

  const data = await gw<{ shipment: GwOrderShipment | null }>(
    `/api/v1/shipping/shipments/${encodeURIComponent(orderId)}`,
    { query: { direction } }
  );
  return Response.json(
    { shipment: data.shipment ?? null },
    { headers: { "Cache-Control": "no-store" } }
  );
});
