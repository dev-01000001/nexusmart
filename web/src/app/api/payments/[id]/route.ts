import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { assertPaymentInStorefront } from "@/lib/server/storefront";
import type { GwPayment } from "@/features/commerce/types";

type RouteCtx = { params: Promise<{ id: string }> };

export const GET = apiRoute<RouteCtx>(
  async (req, { params }) => {
    const site = await requireRequestSite(req);
    const session = await assertSession();
    const { id } = await params;
    const cleanId = id.trim();
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }

    const data = await gw<{ payment?: GwPayment | null }>(
      `/api/v1/payments/${encodeURIComponent(cleanId)}`
    );
    // Cross-site payments (or payments belonging to another buyer) return 404.
    const payment = assertPaymentInStorefront(data.payment, site.shopId, session.user.id);

    return Response.json({ payment }, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      not_found: "Không tìm thấy thông tin giao dịch thanh toán này.",
      NOT_FOUND: "Không tìm thấy thông tin giao dịch thanh toán này.",
      PERMISSION_DENIED: "Bạn không có quyền xem giao dịch thanh toán này.",
    },
  }
);
