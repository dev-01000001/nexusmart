import type { GatewaySellerPayoutResponse } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import { validateSellerPayoutDecisionInput } from "@/features/admin/AdminReconciliationHelpers";
import { isValidAdminPathId } from "@/features/admin/AdminReturnsHelpers";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("payouts.decide");
    const { id } = await ctx.params;
    const cleanId = (id ?? "").trim();
    if (!isValidAdminPathId(cleanId)) {
      throw new GatewayError("invalid", "INVALID_PAYOUT_ID");
    }

    const raw = await readJson<unknown>(req);
    const validated = validateSellerPayoutDecisionInput(raw);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code, "", "", {
        safeMessage: validated.message,
      });
    }

    const data = await gw<GatewaySellerPayoutResponse>(
      `/api/v1/admin/payouts/${encodeURIComponent(cleanId)}/decision`,
      {
        method: "POST",
        body: validated.value,
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_PAYOUT_ID: "Mã yêu cầu rút tiền không hợp lệ.",
      FAILED_PRECONDITION: "Yêu cầu rút tiền này không còn ở trạng thái chờ duyệt.",
    },
  }
);
