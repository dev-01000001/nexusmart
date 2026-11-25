import { gwRaw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { applyProxySecurityHeaders } from "@/lib/server/proxy-security";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

interface RouteCtx {
  params: Promise<{ jobId: string }>;
}

export const GET = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertSellerSession();
    const { jobId } = await ctx.params;
    const cleanJobId = assertValidId(jobId, "INVALID_IMPORT_JOB_ID");

    const upstream = await gwRaw(
      `/api/v1/seller/products/import/${encodeURIComponent(cleanJobId)}/errors.csv`,
      {
        method: "GET",
        accept: "text/csv, */*",
      }
    );

    const contentType = upstream.headers.get("content-type") || "text/csv; charset=utf-8";
    const headers = new Headers({
      "Content-Type": contentType,
      "Cache-Control": "no-store",
    });
    const disposition = upstream.headers.get("content-disposition");
    headers.set(
      "Content-Disposition",
      disposition || `attachment; filename="import-errors-${cleanJobId.slice(0, 8)}.csv"`
    );
    applyProxySecurityHeaders(headers, upstream.headers, contentType);

    return new Response(upstream.body, {
      status: upstream.status,
      headers,
    });
  },
  {
    messages: {
      INVALID_IMPORT_JOB_ID: "Mã phiên nhập CSV không hợp lệ.",
    },
  }
);
