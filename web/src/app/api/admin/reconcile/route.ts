import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { parseSalesChannelFilter } from "@/lib/salesChannel";
import { assertAdminSession } from "@/features/admin/adminServer";
import type { GwReconciliationReport } from "@/features/admin/adminApiTypes";

interface ReconcileBody {
  date?: string;
  sales_channel?: unknown;
}

export const POST = apiRoute(
  async (req) => {
    await assertAdminSession();
    let date = "";
    let rawChannel: unknown = req.nextUrl.searchParams.get("sales_channel");
    try {
      const parsed = (await req.json()) as ReconcileBody | null;
      if (parsed && typeof parsed.date === "string") {
        date = parsed.date.trim();
      }
      if (parsed && "sales_channel" in parsed && parsed.sales_channel !== undefined) {
        rawChannel = parsed.sales_channel;
      }
    } catch {
      // Optional JSON body; date defaults to today UTC on payment service.
    }

    if (date && !/^\d{4}-\d{2}-\d{2}$/.test(date)) {
      throw new GatewayError("invalid", "INVALID_DATE");
    }

    const salesChannel = parseSalesChannelFilter(rawChannel);
    if (salesChannel === null) {
      throw new GatewayError("invalid", "INVALID_SALES_CHANNEL");
    }

    const data = await gw<GwReconciliationReport>("/api/v1/admin/payments/reconcile", {
      method: "POST",
      body: salesChannel ? { date, sales_channel: salesChannel } : { date },
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
      INVALID_DATE: "Ngày đối soát cần theo định dạng YYYY-MM-DD.",
      INVALID_SALES_CHANNEL: "Kênh bán không hợp lệ. Chỉ hỗ trợ Sàn NexusMart hoặc Site riêng.",
    },
  }
);
