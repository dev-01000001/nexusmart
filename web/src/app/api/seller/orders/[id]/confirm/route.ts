import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession } from "@/features/seller/sellerServer";
import type { GwSellerOrder } from "@/features/seller/sellerApiTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const data = await gw<{ order: GwSellerOrder }>(
      `/api/v1/seller/orders/${encodeURIComponent(id)}/confirm`,
      { method: "POST" }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      FAILED_PRECONDITION: "Đơn hàng hiện không ở trạng thái chờ gian hàng xác nhận.",
    },
  }
);
