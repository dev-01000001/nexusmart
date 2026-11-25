import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import type { GwSellerReturnRequest } from "@/features/seller/sellerApiTypes";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const returnId = assertValidId(id, "INVALID_RETURN_ID");

    const data = await gw<{ return_request: GwSellerReturnRequest }>(
      `/api/v1/seller/returns/${encodeURIComponent(returnId)}/confirm-received`,
      { method: "POST" }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_RETURN_ID: "Mã yêu cầu trả hàng không hợp lệ.",
    },
  }
);
