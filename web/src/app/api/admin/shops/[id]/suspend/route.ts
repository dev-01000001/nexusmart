import type { GatewayShopSuspensionApproval } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import { parseEntityId } from "@/features/admin/AdminConfigValidators";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface SuspendShopBody {
  reason?: unknown;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("shops.suspend.request");
    const { id } = await ctx.params;
    const cleanId = parseEntityId(id);
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_SHOP_ID");
    }

    const raw = await readJson<SuspendShopBody>(req);
    const reason = typeof raw.reason === "string" ? raw.reason.trim().slice(0, 1000) : "";
    if (reason.length < 4) {
      throw new GatewayError("invalid", "REASON_REQUIRED");
    }

    const data = await gw<{ approval: GatewayShopSuspensionApproval }>(
      `/api/v1/admin/shops/${encodeURIComponent(cleanId)}/suspend`,
      {
        method: "POST",
        body: { reason },
      }
    );
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_SHOP_ID: "Mã gian hàng không hợp lệ.",
      REASON_REQUIRED: "Vui lòng nhập lý do đề xuất tạm khóa gian hàng (tối thiểu 4 ký tự).",
    },
  }
);
