import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import type { ConfirmProductImportJobResponse } from "@/lib/a8-types";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";

interface RouteCtx {
  params: Promise<{ jobId: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertSellerSession();
    const { jobId } = await ctx.params;
    const cleanJobId = assertValidId(jobId, "INVALID_IMPORT_JOB_ID");

    const data = await gw<ConfirmProductImportJobResponse>(
      `/api/v1/seller/products/import/${encodeURIComponent(cleanJobId)}/confirm`,
      { method: "POST" }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_IMPORT_JOB_ID: "Mã phiên nhập CSV không hợp lệ.",
    },
  }
);
