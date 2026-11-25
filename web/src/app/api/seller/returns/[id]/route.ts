import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import type { GwSellerReturnRequest } from "@/features/seller/sellerApiTypes";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const GET = apiRoute<RouteCtx>(async (_req, ctx) => {
  const session = await assertSellerSession();
  const { id } = await ctx.params;
  const returnId = assertValidId(id, "INVALID_RETURN_ID");
  const data = await gw<{ return_request: GwSellerReturnRequest | null }>(
    `/api/v1/seller/returns/${encodeURIComponent(returnId)}`
  );
  if (!data.return_request || data.return_request.shop_id !== session.user.shopId) {
    throw new GatewayError("not_found");
  }
  return Response.json(
    { return_request: data.return_request },
    { headers: { "Cache-Control": "no-store" } }
  );
});
