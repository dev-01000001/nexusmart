import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import type { GwSellerReturnRequest } from "@/features/seller/sellerApiTypes";
import { assertSellerSession, assertValidId } from "@/features/seller/sellerServer";
import { validateEvidenceUrls } from "@/features/seller/sellerValidation";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface RespondBody {
  decision?: unknown;
  note?: unknown;
  evidence_urls?: unknown;
}

const VALID_DECISIONS = new Set(["ACCEPT_REFUND", "ACCEPT_RETURN", "REJECT"]);

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertSellerSession();
    const { id } = await ctx.params;
    const returnId = assertValidId(id, "INVALID_RETURN_ID");

    const raw = await readJson<RespondBody>(req);
    const decision = typeof raw?.decision === "string" ? raw.decision.trim().toUpperCase() : "";
    if (!VALID_DECISIONS.has(decision)) {
      throw new GatewayError("invalid", "INVALID_RETURN_DECISION");
    }
    const note = typeof raw?.note === "string" ? raw.note.trim().slice(0, 1000) : "";
    if (decision === "REJECT" && note.length < 5) {
      throw new GatewayError("invalid", "INVALID_RETURN_NOTE");
    }
    const evidence = validateEvidenceUrls(raw?.evidence_urls, 5);
    if (!evidence.ok) {
      throw new GatewayError("invalid", evidence.code, "", "", { safeMessage: evidence.message });
    }

    const data = await gw<{ return_request: GwSellerReturnRequest }>(
      `/api/v1/seller/returns/${encodeURIComponent(returnId)}/respond`,
      {
        method: "POST",
        body: {
          decision,
          note,
          evidence_urls: evidence.urls,
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_RETURN_ID: "Mã yêu cầu trả hàng không hợp lệ.",
      INVALID_RETURN_DECISION: "Phương án phản hồi không hợp lệ.",
      INVALID_RETURN_NOTE: "Vui lòng nhập ghi chú/lý do từ chối tối thiểu 5 ký tự.",
    },
  }
);
