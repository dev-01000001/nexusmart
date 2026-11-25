import type { GatewayShop, GatewayShopSuspensionApproval } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import { parseEntityId } from "@/features/admin/AdminConfigValidators";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface DecideShopBody {
  decision?: unknown;
  note?: unknown;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("shops.suspend.decide");
    const { id } = await ctx.params;
    const cleanId = parseEntityId(id);
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_APPROVAL_ID");
    }

    const raw = await readJson<DecideShopBody>(req);
    const decision = typeof raw.decision === "string" ? raw.decision.trim().toUpperCase() : "";
    if (decision !== "APPROVE" && decision !== "REJECT") {
      throw new GatewayError("invalid", "INVALID_DECISION");
    }
    const note = typeof raw.note === "string" ? raw.note.trim().slice(0, 1000) : "";
    if (decision === "REJECT" && note.length < 3) {
      throw new GatewayError("invalid", "NOTE_REQUIRED");
    }

    const data = await gw<{ approval: GatewayShopSuspensionApproval; shop: GatewayShop }>(
      `/api/v1/admin/approvals/shop/${encodeURIComponent(cleanId)}/decide`,
      {
        method: "POST",
        body: { decision, note },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_APPROVAL_ID: "Mã yêu cầu phê duyệt không hợp lệ.",
      INVALID_DECISION: "Quyết định phê duyệt không hợp lệ.",
      NOTE_REQUIRED: "Vui lòng nhập lý do từ chối (tối thiểu 3 ký tự).",
    },
  }
);
