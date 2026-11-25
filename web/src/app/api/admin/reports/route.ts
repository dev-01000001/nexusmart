import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import { validateReportDateRange } from "@/lib/format";
import type { GatewayPlatformReportResponse } from "@/lib/gateway-types";

export const GET = apiRoute(async (req) => {
  await assertCapability("reports.read");
  const q = req.nextUrl.searchParams;
  const fromDate = (q.get("from_date") ?? q.get("from") ?? "").trim();
  const toDate = (q.get("to_date") ?? q.get("to") ?? "").trim();

  if (fromDate || toDate) {
    if (!fromDate || !toDate) {
      throw new GatewayError("invalid", "INVALID_DATE_RANGE", "", "", {
        safeMessage: "Cần cung cấp đủ ngày bắt đầu và ngày kết thúc báo cáo.",
      });
    }
    const check = validateReportDateRange(fromDate, toDate);
    if (!check.ok) {
      throw new GatewayError("invalid", "INVALID_DATE_RANGE", "", "", {
        safeMessage: check.message,
      });
    }
  }

  let topLimit: number | undefined;
  const rawTop = (q.get("top_limit") ?? "").trim();
  if (rawTop) {
    const parsed = Number(rawTop);
    if (!Number.isInteger(parsed) || parsed < 1 || parsed > 50) {
      throw new GatewayError("invalid", "INVALID_TOP_LIMIT", "", "", {
        safeMessage: "Số lượng sản phẩm top phải nằm trong khoảng 1 đến 50.",
      });
    }
    topLimit = parsed;
  }

  const data = await gw<GatewayPlatformReportResponse>("/api/v1/admin/reports", {
    query: {
      from_date: fromDate || undefined,
      to_date: toDate || undefined,
      top_limit: topLimit,
    },
  });
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});
