import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { ADMIN_ROLES, assertSession } from "@/lib/server/session";
import type { GwPayment } from "@/features/commerce/types";

type RouteCtx = { params: Promise<{ id: string }> };

export const GET = apiRoute<RouteCtx>(
  async (_req, { params }) => {
    const session = await assertSession();
    const { id } = await params;
    const cleanId = id.trim();
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }

    const data = await gw<{ payment: GwPayment }>(
      `/api/v1/payments/${encodeURIComponent(cleanId)}`
    );
    const payment = data.payment;
    if (!payment) {
      throw new GatewayError("not_found", "NOT_FOUND");
    }
    if (
      payment.user_id &&
      payment.user_id !== session.user.id &&
      !ADMIN_ROLES.includes(session.user.role)
    ) {
      throw new GatewayError("forbidden", "PERMISSION_DENIED");
    }

    return Response.json({ payment }, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      NOT_FOUND: "Không tìm thấy thông tin giao dịch thanh toán này.",
      PERMISSION_DENIED: "Bạn không có quyền xem giao dịch thanh toán này.",
    },
  }
);
