import { randomUUID } from "node:crypto";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import type { GwPayment } from "@/features/commerce/types";

type RouteCtx = { params: Promise<{ id: string }> };

export const POST = apiRoute<RouteCtx>(
  async (req, { params }) => {
    await assertSession();
    const { id } = await params;
    const cleanId = id.trim();
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }

    let method = "VIETQR";
    const raw = await req.text();
    if (raw.trim()) {
      let parsed: { method?: unknown; payment_method?: unknown };
      try {
        parsed = JSON.parse(raw) as { method?: unknown; payment_method?: unknown };
      } catch {
        throw new GatewayError("invalid", "INVALID_JSON");
      }
      const rawMethod = parsed.method ?? parsed.payment_method;
      if (typeof rawMethod === "string" && rawMethod.trim()) {
        method = rawMethod.trim().toUpperCase();
      }
    }

    if (method !== "VIETQR") {
      throw new GatewayError("conflict", "INVALID_RETRY_METHOD");
    }

    const headerKey = (req.headers.get("idempotency-key") ?? "").trim();
    const idempotencyKey = headerKey || `retry-${cleanId}-${randomUUID()}`;

    const data = await gw<{ payment: GwPayment }>(
      `/api/v1/payments/${encodeURIComponent(cleanId)}/retry`,
      {
        method: "POST",
        idempotencyKey,
        body: { method },
      }
    );

    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_RETRY_METHOD:
        "Thanh toán lại chỉ áp dụng cho mã VietQR mới. Để đổi sang COD, vui lòng tạo đơn mới.",
      FAILED_PRECONDITION: "Giao dịch này hiện không thể tạo lại mã thanh toán.",
      NOT_FOUND: "Không tìm thấy giao dịch thanh toán cần tạo lại.",
      PERMISSION_DENIED: "Bạn không có quyền thao tác trên giao dịch thanh toán này.",
    },
  }
);
