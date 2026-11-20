import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession } from "@/features/seller/sellerServer";
import type { GwSellerOrder } from "@/features/seller/sellerApiTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface ShipBody {
  carrier_code?: string;
  service_code?: string;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    let body: ShipBody = {};
    try {
      const parsed = (await req.json()) as ShipBody | null;
      if (parsed && typeof parsed === "object") body = parsed;
    } catch {
      // Optional JSON body; fall back to order's default carrier/service.
    }

    const data = await gw<{ order: GwSellerOrder; tracking_code: string; label_url: string }>(
      `/api/v1/seller/orders/${encodeURIComponent(id)}/ship`,
      {
        method: "POST",
        body: {
          carrier_code: (body.carrier_code ?? "").trim(),
          service_code: (body.service_code ?? "").trim(),
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      FAILED_PRECONDITION: "Đơn hàng hiện chưa thể bàn giao cho đơn vị vận chuyển.",
    },
  }
);
