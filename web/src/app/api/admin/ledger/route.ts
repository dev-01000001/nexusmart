import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { parseSalesChannelFilter } from "@/lib/salesChannel";
import { assertAdminSession } from "@/features/admin/adminServer";
import type { GwLedgerSummary } from "@/features/admin/adminApiTypes";

export const GET = apiRoute(
  async (req) => {
    await assertAdminSession();
    const salesChannel = parseSalesChannelFilter(req.nextUrl.searchParams.get("sales_channel"));
    if (salesChannel === null) {
      throw new GatewayError("invalid", "INVALID_SALES_CHANNEL");
    }

    const data = await gw<GwLedgerSummary>("/api/v1/admin/ledger/summary", {
      query: { sales_channel: salesChannel || undefined },
    });
    return Response.json(
      {
        ...data,
        sales_channel: data.sales_channel ?? salesChannel,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_SALES_CHANNEL: "Kênh bán không hợp lệ. Chỉ hỗ trợ Sàn NexusMart hoặc Site riêng.",
    },
  }
);
