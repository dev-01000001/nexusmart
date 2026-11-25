import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GwAdminProductExtended } from "@/features/admin/adminApiTypes";
import { isValidAdminPathId } from "@/features/admin/AdminReturnsHelpers";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

interface ReviewProductBody {
  decision?: string;
  reason?: string;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("products.review");
    const { id } = await ctx.params;
    if (!isValidAdminPathId(id)) {
      throw new GatewayError("invalid", "INVALID_ID");
    }

    const raw = await readJson<ReviewProductBody>(req);
    const decision = (raw.decision ?? "").trim().toUpperCase();
    const reason = (raw.reason ?? "").trim();

    if (decision !== "APPROVE" && decision !== "REJECT") {
      throw new GatewayError("invalid", "INVALID_DECISION");
    }
    if (decision === "REJECT" && !reason) {
      throw new GatewayError("invalid", "REASON_REQUIRED");
    }
    if (reason.length > 1000) {
      throw new GatewayError("invalid", "REASON_TOO_LONG");
    }

    const data = await gw<{ product: GwAdminProductExtended }>(
      `/api/v1/admin/products/${encodeURIComponent(id.trim())}/review`,
      {
        method: "POST",
        body: { decision, reason },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ID: "Mã sản phẩm không hợp lệ.",
      INVALID_DECISION: "Quyết định kiểm duyệt sản phẩm không hợp lệ.",
      REASON_REQUIRED: "Vui lòng nhập lý do khi từ chối sản phẩm.",
      REASON_TOO_LONG: "Lý do kiểm duyệt không được vượt quá 1.000 ký tự.",
    },
  }
);
