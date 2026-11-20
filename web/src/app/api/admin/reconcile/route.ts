import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import type { GwReconciliationReport } from "@/features/admin/adminApiTypes";

interface ReconcileBody {
  date?: string;
}

export const POST = apiRoute(
  async (req) => {
    await assertAdminSession();
    let date = "";
    try {
      const parsed = (await req.json()) as ReconcileBody | null;
      if (parsed && typeof parsed.date === "string") {
        date = parsed.date.trim();
      }
    } catch {
      // Optional date defaults to today UTC on payment service.
    }

    if (date && !/^\d{4}-\d{2}-\d{2}$/.test(date)) {
      throw new GatewayError("invalid", "INVALID_DATE");
    }

    const data = await gw<GwReconciliationReport>("/api/v1/admin/payments/reconcile", {
      method: "POST",
      body: { date },
    });
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_DATE: "Ngày đối soát cần theo định dạng YYYY-MM-DD.",
    },
  }
);
