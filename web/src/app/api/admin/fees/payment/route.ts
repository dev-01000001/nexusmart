import type {
  GatewayPaymentFeeConfigItem,
  GatewayPaymentFeeConfigResponse,
} from "@/lib/gateway-types";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertCapability } from "@/lib/server/session";
import {
  parsePaymentMethodCode,
  validatePaymentFeeConfigInput,
} from "@/features/admin/AdminConfigValidators";

export const GET = apiRoute(
  async (req) => {
    await assertCapability("fees.payment.read");
    const params = req.nextUrl.searchParams;
    const rawMethod = (params.get("payment_method") ?? "").trim();
    let paymentMethod: string | undefined;
    if (rawMethod) {
      const parsed = parsePaymentMethodCode(rawMethod);
      if (!parsed) throw new GatewayError("invalid", "INVALID_PAYMENT_METHOD");
      paymentMethod = parsed;
    }
    const rawHistory = (params.get("include_history") ?? "").trim().toLowerCase();
    const includeHistory = rawHistory === "true" || rawHistory === "1" ? "true" : undefined;

    const data = await gw<GatewayPaymentFeeConfigResponse>("/api/v1/admin/fees/payment", {
      query: {
        payment_method: paymentMethod,
        include_history: includeHistory,
      },
    });
    return Response.json(
      {
        active_configs: data.active_configs ?? [],
        history: data.history ?? [],
      },
      { headers: { "Cache-Control": "no-store" } }
    );
  },
  {
    messages: {
      INVALID_PAYMENT_METHOD: "Phương thức thanh toán không hợp lệ.",
    },
  }
);

export const POST = apiRoute(
  async (req) => {
    await assertCapability("fees.payment.write");
    const raw = await readJson<unknown>(req);
    const parsed = validatePaymentFeeConfigInput(raw);
    if (!parsed.ok) {
      throw new GatewayError("invalid", parsed.code, "", "", { safeMessage: parsed.message });
    }

    const data = await gw<{ config: GatewayPaymentFeeConfigItem }>("/api/v1/admin/fees/payment", {
      method: "POST",
      body: {
        payment_method: parsed.value.payment_method,
        fee_bps: parsed.value.fee_bps,
        effective_from: parsed.value.effective_from,
        note: parsed.value.note,
      },
    });
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_PAYMENT_METHOD: "Phương thức thanh toán không hợp lệ.",
    },
  }
);
