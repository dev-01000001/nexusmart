import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { parseSalesChannelFilter } from "@/lib/salesChannel";
import { assertAdminSession } from "@/features/admin/adminServer";
import { normalizeAdminOrder, type GwAdminOrder } from "@/features/admin/adminApiTypes";

export const GET = apiRoute(
  async (req) => {
    await assertAdminSession();
    const q = req.nextUrl.searchParams;
    const salesChannel = parseSalesChannelFilter(q.get("sales_channel"));
    if (salesChannel === null) {
      throw new GatewayError("invalid", "INVALID_SALES_CHANNEL");
    }

    const data = await gw<{
      items: GwAdminOrder[];
      page: number;
      page_size: number;
      total: number;
    }>("/api/v1/admin/orders", {
      query: {
        status: q.get("status"),
        shop_id: q.get("shop_id"),
        buyer_id: q.get("buyer_id"),
        sales_channel: salesChannel || undefined,
        page: q.get("page"),
        page_size: q.get("page_size"),
      },
    });
    return Response.json(
      { ...data, items: (data.items ?? []).map(normalizeAdminOrder) },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_SALES_CHANNEL: "Kênh bán không hợp lệ. Chỉ hỗ trợ Sàn NexusMart hoặc Site riêng.",
    },
  }
);
