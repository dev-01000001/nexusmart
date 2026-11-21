import React from "react";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import {
  normalizeSellerNotifications,
  type SellerNotification,
} from "@/features/seller/sellerReturns";
import {
  loadSellerOrders,
  loadSellerProducts,
  requireSellerPage,
} from "@/features/seller/sellerServer";
import SellerInteractionClient from "./SellerInteractionClient";

export const dynamic = "force-dynamic";

async function loadInitialNotifications(): Promise<{
  items: SellerNotification[];
  error: string | null;
}> {
  try {
    const raw = await gw<unknown>("/api/v1/notifications", {
      query: { page: 1, page_size: 50 },
    });
    const normalized = normalizeSellerNotifications(raw);
    return { items: normalized.items, error: null };
  } catch (err) {
    const message =
      err instanceof GatewayError ? SAFE_MESSAGES[err.kind] : SAFE_MESSAGES.unavailable;
    return { items: [], error: message };
  }
}

export default async function SellerInteractionPage() {
  await requireSellerPage();

  const [ordersRes, productsRes, notifRes] = await Promise.all([
    loadSellerOrders(100),
    loadSellerProducts(100),
    loadInitialNotifications(),
  ]);

  return (
    <SellerInteractionClient
      initialOrders={ordersRes.data?.items ?? []}
      ordersError={ordersRes.error}
      products={productsRes.data?.items ?? []}
      productsError={productsRes.error}
      initialNotifications={notifRes.items}
      notificationsError={notifRes.error}
    />
  );
}
