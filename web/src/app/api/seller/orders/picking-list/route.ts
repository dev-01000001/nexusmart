import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import type { PickingListResponse } from "@/lib/a8-types";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { validateResourceIdList } from "@/features/seller/sellerValidation";

interface PickingListBody {
  order_ids?: unknown;
}

export const POST = apiRoute(
  async (req) => {
    await assertSellerSession();
    const raw = await readJson<PickingListBody>(req);
    const idsRes = validateResourceIdList(raw?.order_ids, 50);
    if (!idsRes.ok) {
      throw new GatewayError("invalid", idsRes.code);
    }

    const data = await gw<PickingListResponse>("/api/v1/seller/orders/picking-list", {
      method: "POST",
      body: { order_ids: idsRes.ids },
    });
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_IDS: "Vui lòng chọn ít nhất một đơn hàng hợp lệ để tổng hợp danh sách nhặt hàng.",
      TOO_MANY_IDS: "Chỉ hỗ trợ tổng hợp tối đa 50 đơn hàng mỗi lần.",
    },
  }
);
