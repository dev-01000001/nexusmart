import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GatewayReconciliationQueueItem, PaginatedResponse } from "@/lib/gateway-types";

const ALLOWED_QUEUE_STATUSES = new Set(["pending", "suspended", "assigned", "refunded"]);

export const GET = apiRoute(
  async (req) => {
    await assertCapability("ledger.read");
    const q = req.nextUrl.searchParams;
    const status = (q.get("status") ?? "").trim().toLowerCase();
    if (status && !ALLOWED_QUEUE_STATUSES.has(status)) {
      throw new GatewayError("invalid", "INVALID_STATUS");
    }

    const data = await gw<PaginatedResponse<GatewayReconciliationQueueItem>>(
      "/api/v1/admin/reconciliation/queue",
      {
        query: {
          status: status || undefined,
          page: q.get("page"),
          page_size: q.get("page_size"),
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_STATUS: "Trạng thái hàng đợi đối soát không hợp lệ.",
    },
  }
);
