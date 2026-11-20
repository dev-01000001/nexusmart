import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import type { GwAdminShop } from "@/features/admin/adminApiTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface ReviewShopBody {
  decision?: string;
  reason?: string;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertAdminSession();
    const { id } = await ctx.params;
    const raw = await readJson<ReviewShopBody>(req);
    const decision = (raw.decision ?? "").trim().toUpperCase();
    const reason = (raw.reason ?? "").trim();

    if (!["APPROVE", "REJECT", "REQUEST_INFO"].includes(decision)) {
      throw new GatewayError("invalid", "INVALID_DECISION");
    }
    if (decision === "REJECT" && !reason) {
      throw new GatewayError("invalid", "REASON_REQUIRED");
    }

    const data = await gw<{ shop: GwAdminShop }>(
      `/api/v1/admin/shops/${encodeURIComponent(id)}/review`,
      {
        method: "POST",
        body: { decision, reason },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_DECISION: "Quyết định thẩm định không hợp lệ.",
      REASON_REQUIRED: "Vui lòng nhập lý do cụ thể khi từ chối hồ sơ gian hàng.",
    },
  }
);
