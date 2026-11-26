import type { GatewaySearchReindexStatusResponse } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import { parseEntityId } from "@/features/admin/AdminConfigValidators";

interface RouteCtx {
  params: Promise<{ jobId: string }>;
}

export const GET = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertCapability("search.reindex");
    const { jobId } = await ctx.params;
    const cleanJobId = parseEntityId(jobId);
    if (!cleanJobId) {
      throw new GatewayError("invalid", "INVALID_JOB_ID");
    }

    const data = await gw<GatewaySearchReindexStatusResponse>(
      `/api/v1/admin/search/reindex/${encodeURIComponent(cleanJobId)}`
    );
    return Response.json(
      {
        job_id: data.job_id,
        status: data.status,
        products_indexed: Number(data.products_indexed) || 0,
        embeddings_updated: Number(data.embeddings_updated) || 0,
        error_message: data.error_message ? "Dựng lại chỉ mục thất bại. Vui lòng thử lại sau." : "",
        started_at: data.started_at,
        finished_at: data.finished_at,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_JOB_ID: "Mã tiến trình dựng lại chỉ mục không hợp lệ.",
      NOT_FOUND: "Không tìm thấy tiến trình dựng lại chỉ mục.",
    },
  }
);
