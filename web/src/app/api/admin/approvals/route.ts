import type { GatewayAdminApprovalsResponse } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import {
  ALLOWED_APPROVAL_STATUSES,
  ALLOWED_APPROVAL_TYPES,
  parseEntityId,
  parseOptionalPositiveInt,
} from "@/features/admin/AdminConfigValidators";

export const GET = apiRoute(
  async (req) => {
    await assertCapability("approvals.read");
    const params = req.nextUrl.searchParams;

    const rawType = (params.get("type") ?? "").trim().toLowerCase();
    let type: string | undefined;
    if (rawType) {
      if (!(ALLOWED_APPROVAL_TYPES as readonly string[]).includes(rawType)) {
        throw new GatewayError("invalid", "INVALID_APPROVAL_TYPE");
      }
      type = rawType;
    }

    const rawStatus = (params.get("status") ?? "").trim().toUpperCase();
    let status: string | undefined;
    if (rawStatus) {
      if (!(ALLOWED_APPROVAL_STATUSES as readonly string[]).includes(rawStatus)) {
        throw new GatewayError("invalid", "INVALID_APPROVAL_STATUS");
      }
      status = rawStatus;
    }

    const rawShopId = (params.get("shop_id") ?? "").trim();
    let shopId: string | undefined;
    if (rawShopId) {
      const valid = parseEntityId(rawShopId);
      if (!valid) throw new GatewayError("invalid", "INVALID_SHOP_ID");
      shopId = valid;
    }

    const rawOrderId = (params.get("order_id") ?? "").trim();
    let orderId: string | undefined;
    if (rawOrderId) {
      const valid = parseEntityId(rawOrderId);
      if (!valid) throw new GatewayError("invalid", "INVALID_ORDER_ID");
      orderId = valid;
    }

    const data = await gw<GatewayAdminApprovalsResponse>("/api/v1/admin/approvals", {
      query: {
        type,
        status,
        shop_id: shopId,
        order_id: orderId,
        page: parseOptionalPositiveInt(params.get("page"), 10000),
        page_size: parseOptionalPositiveInt(params.get("page_size"), 100),
      },
    });
    return Response.json(
      {
        shop_approvals: data.shop_approvals ?? [],
        refund_approvals: data.refund_approvals ?? [],
        shop_total: data.shop_total ?? 0,
        refund_total: data.refund_total ?? 0,
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_APPROVAL_TYPE: "Loại yêu cầu phê duyệt không hợp lệ.",
      INVALID_APPROVAL_STATUS: "Trạng thái phê duyệt không hợp lệ.",
      INVALID_SHOP_ID: "Mã gian hàng không hợp lệ.",
      INVALID_ORDER_ID: "Mã đơn hàng không hợp lệ.",
    },
  }
);
