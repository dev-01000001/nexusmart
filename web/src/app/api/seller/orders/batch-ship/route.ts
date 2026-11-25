import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import type { BatchShipOrdersResponse } from "@/lib/a8-types";
import { assertSellerSession } from "@/features/seller/sellerServer";
import { validateResourceIdList } from "@/features/seller/sellerValidation";

interface BatchShipBody {
  order_ids?: unknown;
  carrier_code?: unknown;
  service_code?: unknown;
}

export const POST = apiRoute(
  async (req) => {
    await assertSellerSession();
    const raw = await readJson<BatchShipBody>(req);
    const idsRes = validateResourceIdList(raw?.order_ids, 50);
    if (!idsRes.ok) {
      throw new GatewayError("invalid", idsRes.code);
    }

    const carrierCode =
      typeof raw?.carrier_code === "string" ? raw.carrier_code.trim().slice(0, 64) : "";
    const serviceCode =
      typeof raw?.service_code === "string" ? raw.service_code.trim().slice(0, 64) : "";

    const data = await gw<BatchShipOrdersResponse>("/api/v1/seller/orders/batch-ship", {
      method: "POST",
      body: {
        order_ids: idsRes.ids,
        carrier_code: carrierCode,
        service_code: serviceCode,
      },
    });
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_IDS: "Vui lòng chọn ít nhất một đơn hàng hợp lệ để tạo vận đơn hàng loạt.",
      TOO_MANY_IDS: "Chỉ hỗ trợ tạo vận đơn tối đa 50 đơn hàng mỗi lô.",
    },
  }
);
