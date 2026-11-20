import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { parseSalesChannelFilter } from "@/lib/salesChannel";
import { fetchBuyerOrders } from "@/features/account/orders.server";

export const GET = apiRoute(
  async (req) => {
    await assertSession();
    const { searchParams } = req.nextUrl;
    const status = searchParams.get("status")?.trim() || undefined;
    const salesChannel = parseSalesChannelFilter(searchParams.get("sales_channel"));
    if (salesChannel === null) {
      throw new GatewayError("invalid", "INVALID_SALES_CHANNEL");
    }
    const page = Number(searchParams.get("page")) || 1;
    const pageSize = Number(searchParams.get("page_size") ?? searchParams.get("pageSize")) || 50;

    const {
      items,
      total,
      page: curPage,
      pageSize: curSize,
    } = await fetchBuyerOrders({
      status,
      salesChannel: salesChannel || undefined,
      page,
      pageSize,
      includeExtras: false,
    });

    return Response.json(
      { items, page: curPage, pageSize: curSize, total },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_SALES_CHANNEL: "Kênh bán không hợp lệ.",
    },
  }
);
