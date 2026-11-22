import React from "react";
import { notFound } from "next/navigation";
import type {
  GatewayOrder,
  GatewayOrderTimelineEvent,
  GatewayPublicShop,
  GatewayShipmentResponse,
} from "@/lib/gateway-types";
import { requireUser } from "@/lib/server/auth";
import { GatewayError, gatewayFetch } from "@/lib/server/gateway";
import { OrderDetailClient } from "@/features/account/OrderDetailClient";
import { fetchOrderVisualLookup } from "@/features/account/serverData";

export const dynamic = "force-dynamic";

type OrderDetailParams = Promise<{ id: string }>;
type OrderDetailSearchParams = Promise<{ state?: string }>;

export default async function OrderDetailPage({
  params,
  searchParams,
}: {
  params: OrderDetailParams;
  searchParams: OrderDetailSearchParams;
}) {
  const [{ id }, sp] = await Promise.all([params, searchParams]);
  const forcedState =
    sp.state === "loading" || sp.state === "empty" || sp.state === "error" ? sp.state : null;

  try {
    await requireUser(`/don-hang/${id}`);
  } catch (err) {
    if (err instanceof GatewayError) {
      return (
        <OrderDetailClient
          initialOrder={null}
          initialTimeline={[]}
          initialShipment={null}
          shop={null}
          visuals={{ byProductId: {}, bySkuId: {} }}
          orderId={id}
          initialError={err.message}
        />
      );
    }
    throw err;
  }

  if (forcedState) {
    return (
      <OrderDetailClient
        initialOrder={null}
        initialTimeline={[]}
        initialShipment={null}
        shop={null}
        visuals={{ byProductId: {}, bySkuId: {} }}
        orderId={id}
        forcedState={forcedState}
      />
    );
  }

  let order: GatewayOrder | null = null;
  let initialError: string | null = null;

  try {
    const res = await gatewayFetch<{ order: GatewayOrder }>(`/orders/${encodeURIComponent(id)}`);
    order = res?.order ?? null;
  } catch (err) {
    if (err instanceof GatewayError) {
      if (err.status === 403 || err.status === 404 || err.status === 400) {
        notFound();
      }
      initialError = err.message;
    } else {
      initialError = "Chưa thể tải chi tiết đơn hàng lúc này. Vui lòng thử lại sau ít phút.";
    }
  }

  if (!order && !initialError) {
    notFound();
  }

  if (!order) {
    return (
      <OrderDetailClient
        initialOrder={null}
        initialTimeline={[]}
        initialShipment={null}
        shop={null}
        visuals={{ byProductId: {}, bySkuId: {} }}
        orderId={id}
        initialError={initialError}
      />
    );
  }

  const encodedId = encodeURIComponent(order.id);
  const [timelineRes, shipmentRes, shopRes, visuals] = await Promise.all([
    gatewayFetch<{ events: GatewayOrderTimelineEvent[] }>(`/orders/${encodedId}/timeline`).catch(
      () => null
    ),
    gatewayFetch<GatewayShipmentResponse>(`/shipping/shipments/${encodedId}`).catch(() => null),
    gatewayFetch<{ shop: GatewayPublicShop }>(`/shops/${encodeURIComponent(order.shop_id)}`, {
      token: false,
      cache: "no-store",
    }).catch(() => null),
    fetchOrderVisualLookup(order.shop_id),
  ]);

  const timeline =
    timelineRes && Array.isArray(timelineRes.events) && timelineRes.events.length > 0
      ? timelineRes.events
      : Array.isArray(order.timeline)
        ? order.timeline
        : [];

  return (
    <OrderDetailClient
      initialOrder={order}
      initialTimeline={timeline}
      initialShipment={shipmentRes?.shipment ? shipmentRes : null}
      shop={shopRes?.shop ?? null}
      visuals={visuals}
      orderId={order.id}
    />
  );
}
