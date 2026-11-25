import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GwAdminProductExtended } from "@/features/admin/adminApiTypes";
import { isValidAdminPathId } from "@/features/admin/AdminReturnsHelpers";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const GET = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertCapability("products.read");
    const { id } = await ctx.params;
    if (!isValidAdminPathId(id)) {
      throw new GatewayError("invalid", "INVALID_ID");
    }

    const data = await gw<{ product: GwAdminProductExtended }>(
      `/api/v1/admin/products/${encodeURIComponent(id.trim())}`
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ID: "Mã sản phẩm không hợp lệ.",
    },
  }
);
