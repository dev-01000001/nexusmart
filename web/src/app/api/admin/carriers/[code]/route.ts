import type { GatewayCarrierConfig } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import {
  parseCarrierCode,
  validateCarrierUpdateInput,
} from "@/features/admin/AdminConfigValidators";

interface RouteCtx {
  params: Promise<{ code: string }>;
}

export const PUT = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("carriers.write");
    const { code: rawCode } = await ctx.params;
    const code = parseCarrierCode(rawCode);
    if (!code) {
      throw new GatewayError("invalid", "INVALID_CARRIER_CODE");
    }

    const raw = await readJson<unknown>(req);
    const parsed = validateCarrierUpdateInput(raw);
    if (!parsed.ok) {
      throw new GatewayError("invalid", parsed.code, "", "", { safeMessage: parsed.message });
    }

    const body: Record<string, unknown> = {
      enabled: parsed.value.enabled,
      fallback_rates: parsed.value.fallback_rates,
    };
    if (parsed.value.api_token) {
      body.api_token = parsed.value.api_token;
    }

    const data = await gw<{ carrier: GatewayCarrierConfig }>(
      `/api/v1/admin/carriers/${encodeURIComponent(code)}`,
      {
        method: "PUT",
        body,
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_CARRIER_CODE: "Mã hãng vận chuyển không hợp lệ.",
    },
  }
);
