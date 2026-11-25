import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GatewayOrder } from "@/lib/gateway-types";
import { isValidAdminPathId } from "@/features/admin/AdminReturnsHelpers";
import { validateResolveOrderReviewInput } from "@/features/admin/AdminReconciliationHelpers";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("orders.review.resolve");
    const { id } = await ctx.params;
    if (!isValidAdminPathId(id)) {
      throw new GatewayError("invalid", "INVALID_ID");
    }

    const raw = await readJson<unknown>(req);
    const validated = validateResolveOrderReviewInput(raw);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code, "", "", {
        safeMessage: validated.message,
      });
    }

    const data = await gw<{ order: GatewayOrder }>(
      `/api/v1/admin/orders/${encodeURIComponent(id.trim())}/resolve-review`,
      {
        method: "POST",
        body: validated.value,
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ID: "Mã đơn hàng cần rà soát không hợp lệ.",
    },
  }
);
