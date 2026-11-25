import { GatewayError } from "@/lib/server/errors";
import { apiRoute, readJson } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertCapability } from "@/lib/server/session";
import type { GatewayRefund, GatewayRefundResponse, PaginatedResponse } from "@/lib/gateway-types";
import { isValidAdminPathId } from "@/features/admin/AdminReturnsHelpers";
import {
  validateIdempotencyKeyHeader,
  validateManualRefundInput,
} from "@/features/admin/AdminReconciliationHelpers";

const ALLOWED_REFUND_STATUSES = new Set([
  "PENDING_APPROVAL",
  "PENDING",
  "DONE",
  "COMPLETED",
  "FAILED",
  "REJECTED",
  "MANUAL_REVIEW",
]);

export const GET = apiRoute(
  async (req) => {
    await assertCapability("refunds.read");
    const q = req.nextUrl.searchParams;
    const status = (q.get("status") ?? "").trim().toUpperCase();
    if (status && !ALLOWED_REFUND_STATUSES.has(status)) {
      throw new GatewayError("invalid", "INVALID_STATUS");
    }
    const orderId = (q.get("order_id") ?? "").trim();
    if (orderId && !isValidAdminPathId(orderId)) {
      throw new GatewayError("invalid", "INVALID_ORDER_ID");
    }

    const data = await gw<PaginatedResponse<GatewayRefund>>("/api/v1/admin/refunds", {
      query: {
        status: status || undefined,
        order_id: orderId || undefined,
        page: q.get("page"),
        page_size: q.get("page_size"),
      },
    });
    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_STATUS: "Trạng thái hoàn tiền không hợp lệ.",
      INVALID_ORDER_ID: "Mã đơn hàng lọc hoàn tiền không hợp lệ.",
    },
  }
);

export const POST = apiRoute(
  async (req) => {
    await assertCapability("refunds.create");
    const idemKey = validateIdempotencyKeyHeader(req.headers.get("idempotency-key"));
    if (!idemKey) {
      throw new GatewayError("invalid", "MISSING_IDEMPOTENCY_KEY");
    }

    const raw = await readJson<unknown>(req);
    const validated = validateManualRefundInput(raw);
    if (!validated.ok) {
      throw new GatewayError("invalid", validated.code, "", "", {
        safeMessage: validated.message,
      });
    }

    const { payment_id, order_id, amount_vnd, reason } = validated.value;
    const data = await gw<GatewayRefundResponse>(
      `/api/v1/admin/payments/${encodeURIComponent(payment_id)}/refund`,
      {
        method: "POST",
        idempotencyKey: idemKey,
        body: { order_id, amount_vnd, reason },
      }
    );
    return Response.json(data, { status: 201, headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      MISSING_IDEMPOTENCY_KEY: "Thiếu khóa chống trùng lặp lệnh hoàn tiền.",
    },
  }
);
