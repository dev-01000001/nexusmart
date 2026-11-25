import { randomUUID } from "node:crypto";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { MAX_BFF_BODY_BYTES, readBoundedBody } from "@/lib/server/proxy-security";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { assertPaymentInStorefront } from "@/lib/server/storefront";
import type { GwPayment } from "@/features/commerce/types";

type RouteCtx = { params: Promise<{ id: string }> };

const ALLOWED_RETRY_METHODS = new Set(["VIETQR", "ZALOPAY", "COD"]);

export const POST = apiRoute<RouteCtx>(
  async (req, { params }) => {
    const site = await requireRequestSite(req, { fresh: true });
    const session = await assertSession();
    const { id } = await params;
    const cleanId = id.trim();
    if (!cleanId) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }

    let method = "VIETQR";
    const bounded = await readBoundedBody(req, MAX_BFF_BODY_BYTES);
    if (!bounded.ok) {
      throw new GatewayError("payload_too_large", "PAYLOAD_TOO_LARGE");
    }
    const raw = new TextDecoder().decode(bounded.body);
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

    if (!ALLOWED_RETRY_METHODS.has(method)) {
      throw new GatewayError("conflict", "INVALID_RETRY_METHOD");
    }

    // Verify the payment belongs to THIS site's shop and the current buyer before retrying.
    const existing = await gw<{ payment?: GwPayment | null }>(
      `/api/v1/payments/${encodeURIComponent(cleanId)}`
    );
    assertPaymentInStorefront(existing.payment, site.shopId, session.user.id);

    const headerKey = (req.headers.get("idempotency-key") ?? "").trim();
    const idempotencyKey = headerKey || `retry-${cleanId}-${randomUUID()}`;

    const data = await gw<{
      payment?: GwPayment;
      switched_to_cod?: boolean;
      orders?: unknown[];
    }>(`/api/v1/payments/${encodeURIComponent(cleanId)}/retry`, {
      method: "POST",
      idempotencyKey,
      body: { method },
    });

    return Response.json(data, { headers: { "Cache-Control": "no-store" } });
  },
  {
    messages: {
      INVALID_RETRY_METHOD:
        "Phương thức thanh toán lại không hợp lệ. Vui lòng chọn VietQR, ZaloPay hoặc COD.",
      not_found: "Không tìm thấy giao dịch thanh toán cần tạo lại.",
      NOT_FOUND: "Không tìm thấy giao dịch thanh toán cần tạo lại.",
      PERMISSION_DENIED: "Bạn không có quyền thao tác trên giao dịch thanh toán này.",
    },
  }
);
