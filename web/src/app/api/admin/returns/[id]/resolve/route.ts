import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GatewayReturnRequest } from "@/lib/gateway-types";
import {
  isValidAdminPathId,
  validateReturnResolveInput,
} from "@/features/admin/AdminReturnsHelpers";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("returns.resolve");
    const { id } = await ctx.params;
    if (!isValidAdminPathId(id)) {
      throw new GatewayError("invalid", "INVALID_ID");
    }

    const raw = await readJson<unknown>(req);
    const validated = validateReturnResolveInput(raw);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code, "", "", {
        safeMessage: validated.message,
      });
    }

    const data = await gw<{ return_request: GatewayReturnRequest }>(
      `/api/v1/admin/returns/${encodeURIComponent(id.trim())}/resolve`,
      {
        method: "POST",
        body: validated.value,
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ID: "Mã yêu cầu trả hàng không hợp lệ.",
    },
  }
);
