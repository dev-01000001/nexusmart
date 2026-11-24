import React from "react";
import { notFound } from "next/navigation";
import type {
  GatewayOrder,
  GatewayOrderReviewsResponse,
  GatewayOrderTimelineEvent,
  GatewayPublicShop,
  GatewayRefund,
  GatewayReturnRequest,
  GatewayShipmentResponse,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { requireUser } from "@/lib/server/auth";
import { GatewayError, gatewayFetch } from "@/lib/server/gateway";
import { OrderDetailClient } from "@/features/account/OrderDetailClient";
import { isUnpaidVietQrOrder } from "@/features/account/orderMeta";
import { fetchOrderVisualLookup, fetchPublicShopsMap } from "@/features/account/serverData";

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
  const shouldFetchGroupSiblings = Boolean(order.checkout_group_id) && isUnpaidVietQrOrder(order);
  const shouldFetchReturn =
    Boolean(order.return_request_id) ||
    order.status === "RETURN_REQUESTED" ||
    order.status === "RETURNED";
  const shouldFetchReviews = order.status === "COMPLETED";
  const [
    timelineRes,
    shipmentRes,
    returnRes,
    returnShipmentRes,
    refundsRes,
    orderReviewsRes,
    shopRes,
    visuals,
    pendingOrdersRes,
    groupShopsMap,
  ] = await Promise.all([
    gatewayFetch<{ events: GatewayOrderTimelineEvent[] }>(`/orders/${encodedId}/timeline`).catch(
      () => null
    ),
    gatewayFetch<GatewayShipmentResponse>(`/shipping/shipments/${encodedId}`).catch(() => null),
    shouldFetchReturn
      ? gatewayFetch<{ return_request: GatewayReturnRequest }>(`/orders/${encodedId}/return`).catch(
          () => null
        )
      : Promise.resolve(null),
    shouldFetchReturn
      ? gatewayFetch<GatewayShipmentResponse>(
          `/shipping/shipments/${encodedId}?direction=RETURN`
        ).catch(() => null)
      : Promise.resolve(null),
    gatewayFetch<PaginatedResponse<GatewayRefund>>(
      `/orders/${encodedId}/refunds?page=1&page_size=20`
    ).catch(() => null),
    shouldFetchReviews
      ? gatewayFetch<GatewayOrderReviewsResponse>(`/orders/${encodedId}/reviews`).catch(() => null)
      : Promise.resolve(null),
    gatewayFetch<{ shop: GatewayPublicShop }>(`/shops/${encodeURIComponent(order.shop_id)}`, {
      token: false,
      cache: "no-store",
    }).catch(() => null),
    fetchOrderVisualLookup(order.shop_id),
    shouldFetchGroupSiblings
      ? gatewayFetch<PaginatedResponse<GatewayOrder>>(
          "/orders?status=PENDING&page=1&page_size=50",
          { cache: "no-store" }
        ).catch(() => null)
      : Promise.resolve(null),
    shouldFetchGroupSiblings ? fetchPublicShopsMap() : Promise.resolve({}),
  ]);

  const initialGroupOrders = shouldFetchGroupSiblings
    ? (pendingOrdersRes?.items ?? []).filter((o) => o.checkout_group_id === order.checkout_group_id)
    : [];

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
      initialReturnRequest={returnRes?.return_request ?? null}
      initialReturnShipment={returnShipmentRes?.shipment ? returnShipmentRes : null}
      initialRefunds={Array.isArray(refundsRes?.items) ? refundsRes.items : []}
      initialReviews={Array.isArray(orderReviewsRes?.reviews) ? orderReviewsRes.reviews : []}
      shop={shopRes?.shop ?? null}
      visuals={visuals}
      orderId={order.id}
      initialGroupOrders={initialGroupOrders}
      groupShopsMap={groupShopsMap}
    />
  );
}
