import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { normalizeSellerReturn } from "@/features/seller/sellerReturns";
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

    let note = "";
    const rawBody = await req.text();
    if (rawBody.trim()) {
      let parsed: unknown;
      try {
        parsed = JSON.parse(rawBody);
      } catch {
        throw new GatewayError("invalid", "INVALID_JSON");
      }
      if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
        const rawNote = (parsed as { note?: unknown }).note;
        if (typeof rawNote === "string") {
          note = rawNote.trim().slice(0, 500);
        }
      }
    }

    const res = await gw<Record<string, unknown>>(
      `/api/v1/seller/returns/${encodeURIComponent(returnId)}/approve`,
      {
        method: "POST",
        body: note ? { note } : {},
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
      FAILED_PRECONDITION:
        "Yêu cầu đổi trả này đã được xử lý hoặc không còn ở trạng thái chờ duyệt.",
    },
  }
);
