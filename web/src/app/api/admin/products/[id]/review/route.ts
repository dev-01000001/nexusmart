import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertAdminSession } from "@/features/admin/adminServer";
import type { GwAdminProduct } from "@/features/admin/adminApiTypes";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface ReviewProductBody {
  decision?: string;
  reason?: string;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertAdminSession();
    const { id } = await ctx.params;
    const raw = await readJson<ReviewProductBody>(req);
    const decision = (raw.decision ?? "").trim().toUpperCase();
    const reason = (raw.reason ?? "").trim();

    if (!["APPROVE", "REJECT"].includes(decision)) {
      throw new GatewayError("invalid", "INVALID_DECISION");
    }
    if (decision === "REJECT" && !reason) {
      throw new GatewayError("invalid", "REASON_REQUIRED");
    }

    const data = await gw<{ product: GwAdminProduct }>(
      `/api/v1/admin/products/${encodeURIComponent(id)}/review`,
      {
        method: "POST",
        body: { decision, reason },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_DECISION: "Quyết định kiểm duyệt sản phẩm không hợp lệ.",
      REASON_REQUIRED: "Vui lòng nhập lý do khi từ chối sản phẩm.",
    },
  }
);
