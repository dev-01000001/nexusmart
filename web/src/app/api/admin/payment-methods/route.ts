import type { GatewayPaymentMethodsResponse } from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import { parsePaymentMethodCode } from "@/features/admin/AdminConfigValidators";

export const GET = apiRoute(
  async (req) => {
    await assertCapability("payment_methods.read");
    const rawMethod = (req.nextUrl.searchParams.get("method") ?? "").trim();
    let method: string | undefined;
    if (rawMethod) {
      const parsed = parsePaymentMethodCode(rawMethod);
      if (!parsed) throw new GatewayError("invalid", "INVALID_PAYMENT_METHOD");
      method = parsed;
    }

    const data = await gw<GatewayPaymentMethodsResponse>("/api/v1/admin/payment-methods", {
      query: { method },
    });
    return Response.json(
      { methods: data.methods ?? [] },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_PAYMENT_METHOD: "Phương thức thanh toán không hợp lệ.",
    },
  }
);
