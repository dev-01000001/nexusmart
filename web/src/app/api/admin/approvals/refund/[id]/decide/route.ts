import type { GatewayRefund } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import { parseEntityId } from "@/features/admin/AdminConfigValidators";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface DecideRefundBody {
  approve?: unknown;
  note?: unknown;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("refunds.approve");
    const { id } = await ctx.params;
    const cleanId = parseEntityId(id);
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_REFUND_ID");
    }

    const raw = await readJson<DecideRefundBody>(req);
    if (typeof raw.approve !== "boolean") {
      throw new GatewayError("invalid", "INVALID_DECISION");
    }
    const note = typeof raw.note === "string" ? raw.note.trim().slice(0, 1000) : "";
    if (!raw.approve && note.length < 3) {
      throw new GatewayError("invalid", "NOTE_REQUIRED");
    }

    const data = await gw<{ refund: GatewayRefund }>(
      `/api/v1/admin/approvals/refund/${encodeURIComponent(cleanId)}/decide`,
      {
        method: "POST",
        body: { approve: raw.approve, note },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_REFUND_ID: "Mã lệnh hoàn tiền không hợp lệ.",
      INVALID_DECISION: "Quyết định phê duyệt hoàn tiền không hợp lệ.",
      NOTE_REQUIRED: "Vui lòng nhập lý do từ chối hoàn tiền (tối thiểu 3 ký tự).",
    },
  }
);
