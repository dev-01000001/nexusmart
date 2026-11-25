import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GatewayRefund } from "@/lib/gateway-types";
import { isValidAdminPathId } from "@/features/admin/AdminReturnsHelpers";
import { validateRefundApprovalDecisionInput } from "@/features/admin/AdminReconciliationHelpers";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("refunds.approve");
    const { id } = await ctx.params;
    if (!isValidAdminPathId(id)) {
      throw new GatewayError("invalid", "INVALID_ID");
    }

    const raw = await readJson<unknown>(req);
    const validated = validateRefundApprovalDecisionInput(raw);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code, "", "", {
        safeMessage: validated.message,
      });
    }

    const data = await gw<{ refund: GatewayRefund }>(
      `/api/v1/admin/approvals/refund/${encodeURIComponent(id.trim())}/decide`,
      {
        method: "POST",
        body: validated.value,
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ID: "Mã lệnh hoàn tiền cần duyệt không hợp lệ.",
    },
  }
);
