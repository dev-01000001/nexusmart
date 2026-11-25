import { validateReportDateRange } from "@/lib/format";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import type { GwSellerReport } from "@/features/seller/sellerApiTypes";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { isValidDateParam } from "@/features/seller/sellerValidation";

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

    const rawTop = Number(q.get("top_limit") ?? "10");
    const topLimit = Number.isFinite(rawTop) && rawTop >= 1 ? Math.min(20, Math.trunc(rawTop)) : 10;

    const raw = await gw<{ report?: GwSellerReport } | GwSellerReport>("/api/v1/seller/reports", {
      query: {
        from_date: fromDate || undefined,
        to_date: toDate || undefined,
        top_limit: topLimit,
      },
    });
    const report =
      raw && typeof raw === "object" && "report" in raw && raw.report
        ? raw.report
        : (raw as GwSellerReport);
    return Response.json({ report }, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_REPORT_DATE: "Ngày báo cáo không đúng định dạng YYYY-MM-DD.",
      INVALID_REPORT_RANGE: "Ngày bắt đầu phải trước hoặc bằng ngày kết thúc.",
    },
  }
);
