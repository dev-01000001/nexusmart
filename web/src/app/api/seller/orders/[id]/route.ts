import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession } from "@/features/seller/sellerServer";
import type { GwSellerOrder } from "@/features/seller/sellerApiTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const GET = apiRoute<RouteCtx>(async (_req, ctx) => {
  const session = await assertSellerSession();
  const { id } = await ctx.params;
  const data = await gw<{ order: GwSellerOrder | null }>(
    `/api/v1/seller/orders/${encodeURIComponent(id)}`
  );
  if (!data.order || data.order.shop_id !== session.user.shopId) {
    throw new GatewayError("not_found");
  }
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
