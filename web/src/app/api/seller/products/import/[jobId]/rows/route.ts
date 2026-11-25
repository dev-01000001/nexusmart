import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import type { ProductImportRow } from "@/lib/a8-types";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";

interface RouteCtx {
  params: Promise<{ jobId: string }>;
}

export const GET = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertSellerSession();
    const { jobId } = await ctx.params;
    const cleanJobId = assertValidId(jobId, "INVALID_IMPORT_JOB_ID");
    const q = req.nextUrl.searchParams;

    const data = await gw<{
      items: ProductImportRow[];
      page: number;
      page_size: number;
      total: number;
    }>(`/api/v1/seller/products/import/${encodeURIComponent(cleanJobId)}/rows`, {
      query: {
        status: q.get("status"),
        errors_only: q.get("errors_only"),
        page: q.get("page"),
        page_size: q.get("page_size"),
      },
    });
    return Response.json(
      {
        items: data.items ?? [],
        page: data.page ?? 1,
        page_size: data.page_size ?? 20,
        total: data.total ?? 0,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_IMPORT_JOB_ID: "Mã phiên nhập CSV không hợp lệ.",
    },
  }
);
