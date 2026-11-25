import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { MAX_BFF_BODY_BYTES, readBoundedBody } from "@/lib/server/proxy-security";
import { assertCapability } from "@/lib/server/session";
import { parseSalesChannelFilter } from "@/lib/salesChannel";
import type { GwReconciliationReportExtended } from "@/features/admin/adminApiTypes";

interface ReconcileBody {
  date?: unknown;
  sales_channel?: unknown;
}

export const POST = apiRoute(
  async (req) => {
    await assertCapability("reconciliation.write");
    let date = "";
    let rawChannel: unknown = req.nextUrl.searchParams.get("sales_channel");

    const bounded = await readBoundedBody(req, MAX_BFF_BODY_BYTES);
    if (!bounded.ok) {
      throw new GatewayError("payload_too_large", "PAYLOAD_TOO_LARGE");
    }
    if (bounded.body.byteLength > 0) {
      let parsed: ReconcileBody | null = null;
      try {
        parsed = JSON.parse(new TextDecoder().decode(bounded.body)) as ReconcileBody | null;
      } catch {
        throw new GatewayError("invalid", "INVALID_JSON");
      }
      if (parsed && typeof parsed.date === "string") {
        date = parsed.date.trim();
      }
      if (parsed && "sales_channel" in parsed && parsed.sales_channel !== undefined) {
        rawChannel = parsed.sales_channel;
      }
    }

    if (date && !/^\d{4}-\d{2}-\d{2}$/.test(date)) {
      throw new GatewayError("invalid", "INVALID_DATE");
    }

    const salesChannel = parseSalesChannelFilter(rawChannel);
    if (salesChannel === null) {
      throw new GatewayError("invalid", "INVALID_SALES_CHANNEL");
    }

    const data = await gw<GwReconciliationReportExtended>("/api/v1/admin/payments/reconcile", {
      method: "POST",
      body: salesChannel ? { date, sales_channel: salesChannel } : { date },
      timeoutMs: 60_000,
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
