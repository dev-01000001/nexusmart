import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { fetchBuyerOrder } from "@/features/account/orders.server";

type Params = { params: Promise<{ id: string }> };

export const GET = apiRoute<Params>(
  async (_req, ctx) => {
    const session = await assertSession();
    const { id } = await ctx.params;
    const { order } = await fetchBuyerOrder(id, session);
    return Response.json({ order }, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      not_found: "Không tìm thấy đơn hàng này.",
      forbidden: "Bạn không có quyền xem đơn hàng này.",
    },
  }
);
