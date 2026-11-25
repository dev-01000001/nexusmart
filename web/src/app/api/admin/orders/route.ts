import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import { parseSalesChannelFilter } from "@/lib/salesChannel";
import { normalizeAdminOrder, type GwAdminOrder } from "@/features/admin/adminApiTypes";
import { isValidAdminPathId } from "@/features/admin/AdminReturnsHelpers";

export const GET = apiRoute(
  async (req) => {
    await assertCapability("orders.read");
    const q = req.nextUrl.searchParams;
    const salesChannel = parseSalesChannelFilter(q.get("sales_channel"));
    if (salesChannel === null) {
      throw new GatewayError("invalid", "INVALID_SALES_CHANNEL");
    }
    const shopId = (q.get("shop_id") ?? "").trim();
    const buyerId = (q.get("buyer_id") ?? "").trim();
    if (shopId && !isValidAdminPathId(shopId)) {
      throw new GatewayError("invalid", "INVALID_FILTER");
    }
    if (buyerId && !isValidAdminPathId(buyerId)) {
      throw new GatewayError("invalid", "INVALID_FILTER");
    }

    const data = await gw<{
      items: GwAdminOrder[];
      page: number;
      page_size: number;
      total: number;
    }>("/api/v1/admin/orders", {
      query: {
        status: (q.get("status") ?? "").trim() || undefined,
        shop_id: shopId || undefined,
        buyer_id: buyerId || undefined,
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
      INVALID_FILTER: "Bộ lọc tìm kiếm đơn hàng không hợp lệ.",
    },
  }
);
