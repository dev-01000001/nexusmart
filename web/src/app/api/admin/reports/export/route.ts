import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gwRaw } from "@/lib/server/gateway";
import { applyProxySecurityHeaders } from "@/lib/server/proxy-security";
import { assertCapability } from "@/lib/server/session";
import { validateReportDateRange } from "@/lib/format";

export const GET = apiRoute(async (req) => {
  await assertCapability("reports.read");
  const q = req.nextUrl.searchParams;
  const fromDate = (q.get("from_date") ?? q.get("from") ?? "").trim();
  const toDate = (q.get("to_date") ?? q.get("to") ?? "").trim();

  if (fromDate || toDate) {
    if (!fromDate || !toDate) {
      throw new GatewayError("invalid", "INVALID_DATE_RANGE", "", "", {
        safeMessage: "Cần cung cấp đủ ngày bắt đầu và ngày kết thúc khi xuất báo cáo.",
      });
    }
    const check = validateReportDateRange(fromDate, toDate);
    if (!check.ok) {
      throw new GatewayError("invalid", "INVALID_DATE_RANGE", "", "", {
        safeMessage: check.message,
      });
    }
  }

  const upstream = await gwRaw("/api/v1/admin/reports/export", {
    method: "GET",
    query: {
      from_date: fromDate || undefined,
      to_date: toDate || undefined,
    },
    accept: "text/csv",
    timeoutMs: 60_000,
  });

  const contentType = upstream.headers.get("content-type") || "text/csv; charset=utf-8";
  const fallbackName =
    fromDate && toDate
      ? `nexusmart-report-${fromDate}-to-${toDate}.csv`
      : "nexusmart-platform-report.csv";
  const disposition =
    upstream.headers.get("content-disposition") || `attachment; filename="${fallbackName}"`;

  const headers = new Headers({
    "Content-Type": contentType,
    "Content-Disposition": disposition,
    "Cache-Control": "no-store",
  });
  applyProxySecurityHeaders(headers, upstream.headers, contentType);
  return new Response(upstream.body, { status: upstream.status, headers });
});
