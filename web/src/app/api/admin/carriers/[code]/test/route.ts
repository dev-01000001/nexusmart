import type { GatewayAdminTestCarrierResponse } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import { parseCarrierCode } from "@/features/admin/AdminConfigValidators";

interface RouteCtx {
  params: Promise<{ code: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (_req, ctx) => {
    await assertCapability("carriers.write");
    const { code: rawCode } = await ctx.params;
    const code = parseCarrierCode(rawCode);
    if (!code) {
      throw new GatewayError("invalid", "INVALID_CARRIER_CODE");
    }

    const data = await gw<GatewayAdminTestCarrierResponse>(
      `/api/v1/admin/carriers/${encodeURIComponent(code)}/test`,
      {
        method: "POST",
      }
    );
    return Response.json(
      { ok: Boolean(data.ok), message: typeof data.message === "string" ? data.message : "" },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_CARRIER_CODE: "Mã hãng vận chuyển không hợp lệ.",
    },
  }
);
