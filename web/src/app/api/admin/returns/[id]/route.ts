import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GatewayReturnRequest } from "@/lib/gateway-types";
import { isValidAdminPathId } from "@/features/admin/AdminReturnsHelpers";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const GET = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertCapability("returns.read");
    const { id } = await ctx.params;
    if (!isValidAdminPathId(id)) {
      throw new GatewayError("invalid", "INVALID_ID");
    }

    const data = await gw<{ return_request: GatewayReturnRequest }>(
      `/api/v1/admin/returns/${encodeURIComponent(id.trim())}`
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ID: "Mã yêu cầu trả hàng không hợp lệ.",
    },
  }
);
