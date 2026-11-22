import React from "react";
import type { GatewayOrder, GatewayOrderStatus, PaginatedResponse } from "@/lib/gateway-types";
import { requireUser } from "@/lib/server/auth";
import { GatewayError, gatewayFetch } from "@/lib/server/gateway";
import { OrdersListClient } from "@/features/account/OrdersListClient";
import { isValidOrderStatus } from "@/features/account/orderMeta";
import {
  fetchOrderVisualLookup,
  fetchPublicShopsMap,
  settleGateway,
} from "@/features/account/serverData";

export const dynamic = "force-dynamic";

const PAGE_SIZE = 10;

type OrdersSearchParams = {
  status?: string;
  page?: string;
  state?: string;
};

export default async function OrdersPage({
  searchParams,
}: {
  searchParams: Promise<OrdersSearchParams>;
}) {
  const sp = await searchParams;
  const rawStatus = typeof sp.status === "string" ? sp.status.toUpperCase() : "";
  const activeStatus: "" | GatewayOrderStatus = isValidOrderStatus(rawStatus) ? rawStatus : "";

  const parsedPage = Number.parseInt(typeof sp.page === "string" ? sp.page : "1", 10);
  const page = Number.isFinite(parsedPage) && parsedPage > 0 ? parsedPage : 1;

  const forcedState =
    sp.state === "loading" || sp.state === "empty" || sp.state === "error" ? sp.state : null;

  const currentParams = new URLSearchParams();
  if (activeStatus) currentParams.set("status", activeStatus);
  if (page > 1) currentParams.set("page", String(page));
  const currentPath = currentParams.toString()
    ? `/don-hang?${currentParams.toString()}`
    : "/don-hang";

  try {
    await requireUser(currentPath);
  } catch (err) {
    if (err instanceof GatewayError) {
      return (
        <OrdersListClient
          orders={[]}
          total={0}
          allOrdersTotal={0}
          page={page}
          pageSize={PAGE_SIZE}
          activeStatus={activeStatus}
          shopsMap={{}}
          visuals={{ byProductId: {}, bySkuId: {} }}
          initialError={err.message}
        />
      );
    }
    throw err;
  }

  if (forcedState) {
    return (
      <OrdersListClient
        orders={[]}
        total={0}
        allOrdersTotal={0}
        page={1}
        pageSize={PAGE_SIZE}
        activeStatus={activeStatus}
        shopsMap={{}}
        visuals={{ byProductId: {}, bySkuId: {} }}
        forcedState={forcedState}
      />
    );
  }

  const orderQuery = new URLSearchParams({
    page: String(page),
    page_size: String(PAGE_SIZE),
  });
  if (activeStatus) {
    orderQuery.set("status", activeStatus);
  }

  let orders: GatewayOrder[] = [];
  let total = 0;
  let allOrdersTotal = 0;
  let initialError: string | null = null;

  const [ordersResult, allTotalResult, shopsMap, visuals] = await Promise.all([
    settleGateway(
      gatewayFetch<PaginatedResponse<GatewayOrder>>(`/orders?${orderQuery.toString()}`)
    ),
    activeStatus
      ? settleGateway(gatewayFetch<PaginatedResponse<GatewayOrder>>("/orders?page=1&page_size=1"))
      : Promise.resolve(null),
    fetchPublicShopsMap(),
    fetchOrderVisualLookup(),
  ]);

  if (!ordersResult.ok) {
    initialError =
      ordersResult.error instanceof GatewayError
        ? ordersResult.error.message
        : "Không thể tải danh sách đơn hàng lúc này. Vui lòng thử lại sau ít phút.";
  } else {
    orders = Array.isArray(ordersResult.data?.items) ? ordersResult.data.items : [];
    total = typeof ordersResult.data?.total === "number" ? ordersResult.data.total : orders.length;
    allOrdersTotal = activeStatus
      ? allTotalResult && allTotalResult.ok && typeof allTotalResult.data?.total === "number"
        ? allTotalResult.data.total
        : total
      : total;
  }

  return (
    <OrdersListClient
      orders={orders}
      total={total}
      allOrdersTotal={allOrdersTotal}
      page={page}
      pageSize={PAGE_SIZE}
      activeStatus={activeStatus}
      shopsMap={shopsMap}
      visuals={visuals}
      initialError={initialError}
    />
  );
}
