import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { parseSalesChannelFilter } from "@/lib/salesChannel";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { normalizeSellerOrder, type GwSellerOrder } from "@/features/seller/sellerApiTypes";

export const GET = apiRoute(
  async (req) => {
    await assertSellerSession();
    const q = req.nextUrl.searchParams;
    const salesChannel = parseSalesChannelFilter(q.get("sales_channel"));
    if (salesChannel === null) {
      throw new GatewayError("invalid", "INVALID_SALES_CHANNEL");
    }

    // Never trust client shop_id; gateway reads shop_id from the seller's JWT.
    const data = await gw<{
      items: GwSellerOrder[];
      page: number;
      page_size: number;
      total: number;
    }>("/api/v1/seller/orders", {
      query: {
        status: q.get("status"),
        sales_channel: salesChannel || undefined,
        page: q.get("page"),
        page_size: q.get("page_size"),
      },
    });
    return Response.json(
      { ...data, items: (data.items ?? []).map(normalizeSellerOrder) },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_SALES_CHANNEL: "Kênh bán không hợp lệ. Chỉ hỗ trợ Sàn NexusMart hoặc Site riêng.",
    },
  }
);
