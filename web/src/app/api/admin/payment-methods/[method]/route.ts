import type { GatewayPaymentMethodConfig } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import {
  parsePaymentMethodCode,
  validatePaymentMethodConfigInput,
} from "@/features/admin/AdminConfigValidators";

interface RouteCtx {
  params: Promise<{ method: string }>;
}

export const PUT = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("payment_methods.write");
    const { method: rawMethod } = await ctx.params;
    const method = parsePaymentMethodCode(rawMethod);
    if (!method) {
      throw new GatewayError("invalid", "INVALID_PAYMENT_METHOD");
    }

    const raw = await readJson<unknown>(req);
    const parsed = validatePaymentMethodConfigInput(raw);
    if (!parsed.ok) {
      throw new GatewayError("invalid", parsed.code, "", "", { safeMessage: parsed.message });
    }

    const data = await gw<{ method: GatewayPaymentMethodConfig }>(
      `/api/v1/admin/payment-methods/${encodeURIComponent(method)}`,
      {
        method: "PUT",
        body: {
          enabled: parsed.value.enabled,
          min_order_vnd: parsed.value.min_order_vnd,
          max_order_vnd: parsed.value.max_order_vnd,
          expiry_minutes: parsed.value.expiry_minutes,
          note: parsed.value.note,
          expected_version: parsed.value.expected_version,
        },
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_PAYMENT_METHOD: "Phương thức thanh toán không hợp lệ.",
    },
  }
);
