import React, { Suspense } from "react";
import { notFound, redirect } from "next/navigation";
import type {
  GatewayOrder,
  GatewayPayment,
  GatewayPaymentResponse,
  GatewayProduct,
  GatewayPublicShop,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { requireUser } from "@/lib/server/auth";
import { GatewayError, gatewayFetch } from "@/lib/server/gateway";
import { StateView } from "@/components";
import shared from "@/features/commerce/commerce.module.css";
import { PaymentWaitingClient } from "./PaymentWaitingClient";

export const dynamic = "force-dynamic";

interface PageProps {
  params: Promise<{ paymentId: string }>;
}

export default async function PaymentWaitingPage({ params }: PageProps) {
  const { paymentId } = await params;
  const cleanId = paymentId?.trim();
  if (!cleanId) {
    notFound();
  }

  const currentPath = `/thanh-toan/cho-thanh-toan/${encodeURIComponent(cleanId)}`;
  await requireUser(currentPath);

  let payment: GatewayPayment | null = null;
  let loadError: string | null = null;

  try {
    const res = await gatewayFetch<GatewayPaymentResponse>(
      `/payments/${encodeURIComponent(cleanId)}`,
      { cache: "no-store" }
    );
    payment = res?.payment ?? null;
    if (!payment) {
      notFound();
    }
  } catch (err) {
    if (err instanceof GatewayError) {
      if (err.status === 401) {
        redirect(`/dang-nhap?next=${encodeURIComponent(currentPath)}`);
      }
      if (err.status === 403 || err.status === 404) {
        notFound();
      }
      loadError = err.message;
    } else {
      loadError = "Hệ thống thanh toán đang tạm bận, vui lòng thử lại sau ít phút.";
    }
  }

  // Best-effort fetch of sub-orders belonging to this payment for order codes & direct links
  const allocatedIds = Array.from(
    new Set(
      (payment?.shop_allocations?.map((a) => a.order_id) ?? []).filter((id): id is string =>
        Boolean(id)
      )
    )
  );
  const orderIds =
    allocatedIds.length > 0 ? allocatedIds : payment?.order_id ? [payment.order_id] : [];

  const [productsRes, shopsRes, ...orderResults] = await Promise.allSettled([
    gatewayFetch<PaginatedResponse<GatewayProduct>>("/products?page_size=4", {
      cache: "no-store",
    }),
    gatewayFetch<PaginatedResponse<GatewayPublicShop>>("/shops?page_size=50", {
      cache: "no-store",
    }),
    ...orderIds.map((orderId) =>
      gatewayFetch<{ order: GatewayOrder }>(`/orders/${encodeURIComponent(orderId)}`, {
        cache: "no-store",
      })
    ),
  ]);

  const initialOrders: GatewayOrder[] = [];
  for (const res of orderResults) {
    if (res.status === "fulfilled" && res.value?.order) {
      initialOrders.push(res.value.order);
    }
  }

  const suggestedProducts: GatewayProduct[] =
    productsRes.status === "fulfilled" ? (productsRes.value?.items ?? []).slice(0, 4) : [];

  const shopNameById: Record<string, string> = {};
  if (shopsRes.status === "fulfilled" && Array.isArray(shopsRes.value?.items)) {
    for (const s of shopsRes.value.items) {
      shopNameById[s.id] = s.name;
    }
  }

  return (
    <Suspense
      fallback={
        <div className={shared.page}>
          <StateView kind="loading" title="Đang tải thông tin thanh toán VietQR..." rows={4} />
        </div>
      }
    >
      <PaymentWaitingClient
        initialPayment={payment}
        initialOrders={initialOrders}
        initialError={loadError}
        suggestedProducts={suggestedProducts}
        shopNameById={shopNameById}
      />
    </Suspense>
  );
}
