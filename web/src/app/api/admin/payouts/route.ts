import type { GatewaySellerPayoutListResponse } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import { isValidAdminPathId } from "@/features/admin/AdminReturnsHelpers";

const ALLOWED_PAYOUT_STATUSES = new Set(["PENDING", "COMPLETED", "REJECTED"]);

export const GET = apiRoute(
  async (req) => {
    await assertCapability("payouts.read");
    const q = req.nextUrl.searchParams;
    const status = (q.get("status") ?? "").trim().toUpperCase();
    if (status && !ALLOWED_PAYOUT_STATUSES.has(status)) {
      throw new GatewayError("invalid", "INVALID_STATUS");
    }
    const shopId = (q.get("shop_id") ?? "").trim();
    if (shopId && !isValidAdminPathId(shopId)) {
      throw new GatewayError("invalid", "INVALID_SHOP_ID");
    }

    const data = await gw<GatewaySellerPayoutListResponse>("/api/v1/admin/payouts", {
      query: {
        status: status || undefined,
        shop_id: shopId || undefined,
        page: q.get("page"),
        page_size: q.get("page_size"),
      },
    });
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_STATUS: "Trạng thái chi trả người bán không hợp lệ.",
      INVALID_SHOP_ID: "Mã gian hàng lọc chi trả không hợp lệ.",
    },
  }
);
