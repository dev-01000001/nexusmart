import { validateReportDateRange } from "@/lib/format";
import { GatewayError } from "@/lib/server/errors";
import { gwRaw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { applyProxySecurityHeaders } from "@/lib/server/proxy-security";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { isValidDateParam } from "@/features/seller/sellerValidation";

export const dynamic = "force-dynamic";

export const GET = apiRoute(
  async (req) => {
    await assertSellerSession();
    const q = req.nextUrl.searchParams;
    const fromDate = (q.get("from_date") ?? q.get("from") ?? "").trim();
    const toDate = (q.get("to_date") ?? q.get("to") ?? "").trim();
    if (fromDate && !isValidDateParam(fromDate)) {
      throw new GatewayError("invalid", "INVALID_REPORT_DATE");
    }
    if (toDate && !isValidDateParam(toDate)) {
      throw new GatewayError("invalid", "INVALID_REPORT_DATE");
    }
    if (fromDate && toDate) {
      const check = validateReportDateRange(fromDate, toDate);
      if (!check.ok) {
        throw new GatewayError("invalid", "INVALID_REPORT_RANGE", "", "", {
          safeMessage: check.message,
        });
      }
    }

    const upstream = await gwRaw("/api/v1/seller/reports/export", {
      method: "GET",
      query: {
        from_date: fromDate || undefined,
        to_date: toDate || undefined,
      },
      accept: "text/csv, */*",
    });

    const contentType = upstream.headers.get("content-type") || "text/csv; charset=utf-8";
    const headers = new Headers({
      "Content-Type": contentType,
      "Cache-Control": "no-store",
    });
    const disposition = upstream.headers.get("content-disposition");
    headers.set(
      "Content-Disposition",
      disposition || `attachment; filename="bao-cao-gian-hang-${fromDate || "export"}.csv"`
    );
    applyProxySecurityHeaders(headers, upstream.headers, contentType);

    return new Response(upstream.body, {
      status: upstream.status,
      headers,
    });
  },
  {
    messages: {
      INVALID_REPORT_DATE: "Ngày báo cáo không đúng định dạng YYYY-MM-DD.",
      INVALID_REPORT_RANGE: "Ngày bắt đầu phải trước hoặc bằng ngày kết thúc.",
    },
  }
);
