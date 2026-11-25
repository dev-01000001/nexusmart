import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import type { GwSellerReturnRequest } from "@/features/seller/sellerApiTypes";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";
import { validateEvidenceUrls } from "@/features/seller/sellerValidation";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface DisputeBody {
  reason?: unknown;
  evidence_urls?: unknown;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const returnId = assertValidId(id, "INVALID_RETURN_ID");

    const raw = await readJson<DisputeBody>(req);
    const reason = typeof raw?.reason === "string" ? raw.reason.trim().slice(0, 1000) : "";
    if (reason.length < 5) {
      throw new GatewayError("invalid", "INVALID_DISPUTE_REASON");
    }
    const evidence = validateEvidenceUrls(raw?.evidence_urls, 5);
    if (!evidence.ok) {
      throw new GatewayError("invalid", evidence.code, "", "", { safeMessage: evidence.message });
    }

    const data = await gw<{ return_request: GwSellerReturnRequest }>(
      `/api/v1/seller/returns/${encodeURIComponent(returnId)}/dispute`,
      {
        method: "POST",
        body: {
          reason,
          evidence_urls: evidence.urls,
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_RETURN_ID: "Mã yêu cầu trả hàng không hợp lệ.",
      INVALID_DISPUTE_REASON: "Vui lòng mô tả rõ lý do khiếu nại hàng hoàn (tối thiểu 5 ký tự).",
    },
  }
);
