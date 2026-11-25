import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import { parseSalesChannelFilter } from "@/lib/salesChannel";
import type { GwReconciliationRunSummaryExtended } from "@/features/admin/adminApiTypes";

const ALLOWED_RUN_TYPES = new Set(["vietqr", "cod_file"]);

export const GET = apiRoute(
  async (req) => {
    await assertCapability("ledger.read");
    const q = req.nextUrl.searchParams;
    const runType = (q.get("run_type") ?? "").trim().toLowerCase();
    if (runType && !ALLOWED_RUN_TYPES.has(runType)) {
      throw new GatewayError("invalid", "INVALID_RUN_TYPE");
    }
    const salesChannel = parseSalesChannelFilter(q.get("sales_channel"));
    if (salesChannel === null) {
      throw new GatewayError("invalid", "INVALID_SALES_CHANNEL");
    }

    const data = await gw<{
      items: GwReconciliationRunSummaryExtended[];
      page: number;
      page_size: number;
      total: number;
    }>("/api/v1/admin/reconciliation/runs", {
      query: {
        run_type: runType || undefined,
        sales_channel: salesChannel || undefined,
        page: q.get("page"),
        page_size: q.get("page_size"),
      },
    });
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_RUN_TYPE: "Loại phiên đối soát không hợp lệ.",
      INVALID_SALES_CHANNEL: "Kênh bán không hợp lệ. Chỉ hỗ trợ Sàn NexusMart hoặc Site riêng.",
    },
  }
);
