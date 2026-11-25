import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GatewaySettleRefundResponse } from "@/lib/gateway-types";
import { isValidAdminPathId } from "@/features/admin/AdminReturnsHelpers";
import {
  validateIdempotencyKeyHeader,
  validateSettleRefundInput,
} from "@/features/admin/AdminReconciliationHelpers";

interface RouteCtx {
  params: Promise<{ id: string }>;
}

export const POST = apiRoute<RouteCtx>(
  async (req, ctx) => {
    await assertCapability("refunds.settle");
    const { id } = await ctx.params;
    if (!isValidAdminPathId(id)) {
      throw new GatewayError("invalid", "INVALID_ID");
    }

    const idemKey = validateIdempotencyKeyHeader(req.headers.get("idempotency-key"));
    if (!idemKey) {
      throw new GatewayError("invalid", "MISSING_IDEMPOTENCY_KEY");
    }

    const raw = await readJson<unknown>(req);
    const validated = validateSettleRefundInput(raw);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code, "", "", {
        safeMessage: validated.message,
      });
    }

    const data = await gw<GatewaySettleRefundResponse>(
      `/api/v1/admin/refunds/${encodeURIComponent(id.trim())}/settle`,
      {
        method: "POST",
        idempotencyKey: idemKey,
        body: validated.value,
      }
    );
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_ID: "Mã lệnh hoàn tiền không hợp lệ.",
      MISSING_IDEMPOTENCY_KEY: "Thiếu khóa chống trùng lặp xác nhận tất toán.",
    },
  }
);
