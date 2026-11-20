import React from "react";
import { loadCatalogLookups } from "@/lib/catalog";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { ADMIN_ROLES, requireSession } from "@/lib/server/session";
import type { GwPayment } from "@/features/commerce/types";
import { PaymentWaitingClient } from "./PaymentWaitingClient";

export const dynamic = "force-dynamic";

export default async function PaymentWaitingPage({
  params,
  searchParams,
}: {
  params: Promise<{ id: string }>;
  searchParams: Promise<{ order?: string }>;
}) {
  const { id } = await params;
  const { order } = await searchParams;
  const session = await requireSession(`/thanh-toan/cho/${encodeURIComponent(id)}`);

  let initialPayment: GwPayment | null = null;
  let initialError: string | null = null;

  try {
    const res = await gw<{ payment: GwPayment }>(`/api/v1/payments/${encodeURIComponent(id)}`);
    const payment = res.payment ?? null;
    if (
      payment &&
      payment.user_id &&
      payment.user_id !== session.user.id &&
      !ADMIN_ROLES.includes(session.user.role)
    ) {
      initialError = "Bạn không có quyền xem giao dịch thanh toán này.";
    } else if (!payment) {
      initialError = "Không tìm thấy thông tin giao dịch thanh toán này.";
    } else {
      initialPayment = payment;
    }
  } catch (err) {
    if (err instanceof GatewayError) {
      initialError =
        err.kind === "not_found"
          ? "Không tìm thấy giao dịch thanh toán này."
          : err.kind === "forbidden"
            ? "Bạn không có quyền xem giao dịch thanh toán này."
            : SAFE_MESSAGES[err.kind];
    } else {
      initialError = SAFE_MESSAGES.unavailable;
    }
  }

  const catalogRes = await loadCatalogLookups().catch(() => ({
    shops: [],
    products: [],
    error: null,
  }));

  const shopNameById: Record<string, string> = {};
  for (const shop of catalogRes.shops) {
    if (shop.backendId) shopNameById[shop.backendId] = shop.name;
    shopNameById[shop.id] = shop.name;
  }

  return (
    <PaymentWaitingClient
      paymentId={id}
      initialPayment={initialPayment}
      fallbackOrderId={order ?? ""}
      initialError={initialError}
      shopNameById={shopNameById}
    />
  );
}
