import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GatewayReturnRequest, PaginatedResponse } from "@/lib/gateway-types";

const ALLOWED_RETURN_STATUSES = new Set([
  "PENDING",
  "AWAITING_SHIPMENT",
  "IN_TRANSIT",
  "RECEIVED",
  "REFUNDING",
  "REFUNDED",
  "REJECTED",
  "ESCALATED",
  "EVIDENCE_REQUESTED",
  "CLOSED",
]);

function parsePositiveIntParam(raw: string | null, max: number): number | undefined {
  if (!raw || !raw.trim()) return undefined;
  const n = Number(raw.trim());
  if (!Number.isInteger(n) || n < 1) return undefined;
  return Math.min(max, n);
}

export const GET = apiRoute(
  async (req) => {
    await assertCapability("returns.read");
    const q = req.nextUrl.searchParams;
    const rawStatus = (q.get("status") ?? "").trim().toUpperCase();
    if (rawStatus && !ALLOWED_RETURN_STATUSES.has(rawStatus)) {
      throw new GatewayError("invalid", "INVALID_STATUS");
    }

    const data = await gw<PaginatedResponse<GatewayReturnRequest>>("/api/v1/admin/returns", {
      query: {
        status: rawStatus || undefined,
        page: parsePositiveIntParam(q.get("page"), 1000),
        page_size: parsePositiveIntParam(q.get("page_size"), 100),
      },
    });
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_STATUS: "Trạng thái yêu cầu trả hàng không hợp lệ.",
    },
  }
);
