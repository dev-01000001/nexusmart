import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { normalizeSellerReturn, validateRejectReason } from "@/features/seller/sellerReturns";
import { assertSellerSession } from "@/features/seller/sellerServer";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const returnId = (id ?? "").trim();
    if (!returnId) {
      throw new GatewayError("invalid", "INVALID_RETURN_ID");
    }

    const body = await readJson<{ reason?: unknown }>(req);
    const validated = validateRejectReason(body?.reason);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code);
    }

    const res = await gw<Record<string, unknown>>(
      `/api/v1/seller/returns/${encodeURIComponent(returnId)}/reject`,
      {
        method: "POST",
        body: { reason: validated.value },
      }
    );

    const normalized = normalizeSellerReturn(res?.return_request ?? res?.return ?? res);
    return Response.json(
      { ok: true, return_request: normalized },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_REASON: "Lý do từ chối cần tối thiểu 5 ký tự và không vượt quá 500 ký tự.",
      FAILED_PRECONDITION: "Yêu cầu đổi trả này đã được xử lý hoặc không còn ở trạng thái chờ duyệt.",
    },
  }
);
