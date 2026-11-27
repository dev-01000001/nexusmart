import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import {
  validateEscrowReleaseOrderId,
  type AdminEscrowReleaseResponse,
} from "@/features/admin/AdminReconciliationHelpers";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertCapability("escrow.release");
    const { id } = await ctx.params;
    const validated = validateEscrowReleaseOrderId(id);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code, "", "", {
        safeMessage: validated.message,
      });
    }

    const data = await gw<AdminEscrowReleaseResponse>(
      `/api/v1/admin/orders/${encodeURIComponent(validated.value)}/escrow/release`,
      {
        method: "POST",
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ORDER_ID: "Mã đơn hàng phải đúng định dạng UUID.",
      FAILED_PRECONDITION: "Đơn chưa hoàn tất nên chưa thể giải ngân",
      ALREADY_EXISTS: "Đơn chưa hoàn tất nên chưa thể giải ngân",
    },
  }
);
