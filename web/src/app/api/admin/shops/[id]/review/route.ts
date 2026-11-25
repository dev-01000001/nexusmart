import type { GatewayShop } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import {
  parseEntityId,
  validateAdminShopReviewInput,
} from "@/features/admin/AdminConfigValidators";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("shops.review");
    const { id } = await ctx.params;
    const cleanId = parseEntityId(id);
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_SHOP_ID");
    }

    const raw = await readJson<unknown>(req);
    const parsed = validateAdminShopReviewInput(raw);
    if (!parsed.ok) {
      throw new GatewayError("invalid", parsed.code, "", "", { safeMessage: parsed.message });
    }

    const data = await gw<{ shop: GatewayShop }>(
      `/api/v1/admin/shops/${encodeURIComponent(cleanId)}/review`,
      {
        method: "POST",
        body: {
          decision: parsed.value.decision,
          reason: parsed.value.reason,
          expected_owner_name: parsed.value.expected_owner_name,
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_SHOP_ID: "Mã gian hàng không hợp lệ.",
      INVALID_DECISION: "Quyết định thẩm định không hợp lệ.",
      REASON_REQUIRED: "Vui lòng nhập lý do cụ thể khi từ chối hoặc yêu cầu bổ sung hồ sơ.",
      EXPECTED_OWNER_NAME_REQUIRED: "Cần đối chiếu họ tên chủ gian hàng trước khi phê duyệt.",
    },
  }
);
