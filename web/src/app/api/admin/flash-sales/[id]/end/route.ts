import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GwAdminFlashSaleExtended } from "@/features/admin/adminApiTypes";
import { isValidAdminPathId } from "@/features/admin/AdminReturnsHelpers";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertCapability("flash_sales.write");
    const { id } = await ctx.params;
    if (!isValidAdminPathId(id)) {
      throw new GatewayError("invalid", "INVALID_ID");
    }

    const data = await gw<{
      flash_sale?: GwAdminFlashSaleExtended;
      window?: GwAdminFlashSaleExtended;
      released_items?: number;
    }>(`/api/v1/admin/flash-sales/${encodeURIComponent(id.trim())}/end`, {
      method: "POST",
    });
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ID: "Mã khung giờ Flash sale không hợp lệ.",
    },
  }
);
