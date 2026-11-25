import React from "react";
import { formatVnDateOffset } from "@/lib/format";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerOverviewClient } from "@/features/seller/SellerOverviewClient";
import { SellerShopOnboarding } from "@/features/seller/SellerShopOnboarding";
import {
  loadMyShop,
  loadSellerAddresses,
  loadSellerBalance,
  loadSellerOrders,
  loadSellerPendingReturnsCount,
  loadSellerProducts,
  loadSellerReport,
  requireSellerPortalSession,
} from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerOverviewPage() {
  const session = await requireSellerPortalSession("/seller");

  const myShopRes = await loadMyShop();
  const shop = myShopRes.data;
  const needsOnboarding =
    session.user.role !== "seller" ||
    !session.user.shopId ||
    !shop ||
    shop.status === "PENDING" ||
    shop.status === "REJECTED";

  if (needsOnboarding) {
    const addressesRes = await loadSellerAddresses();
    return (
      <SellerSuspense>
        <SellerShopOnboarding
          user={session.user}
          initialShop={shop}
          initialAddresses={addressesRes.data ?? []}
        />
      </SellerSuspense>
    );
  }

  const defaultFrom = formatVnDateOffset(-29);
  const defaultTo = formatVnDateOffset(0);

  const [ordersRes, productsRes, balanceRes, reportRes, pendingReturnsCount] = await Promise.all([
    loadSellerOrders(100),
    loadSellerProducts(100),
    loadSellerBalance(),
    loadSellerReport(defaultFrom, defaultTo, 10),
    loadSellerPendingReturnsCount(),
  ]);

  const loadError = ordersRes.error ?? productsRes.error;

  return (
    <SellerSuspense>
      <SellerOverviewClient
        initialOrders={ordersRes.data?.items ?? []}
        initialProducts={productsRes.data?.items ?? []}
        initialBalance={balanceRes.data}
        balanceError={balanceRes.error}
        initialReport={reportRes.data}
        initialReportError={reportRes.error}
        initialFromDate={defaultFrom}
        initialToDate={defaultTo}
        pendingReturnsCount={pendingReturnsCount}
        loadError={loadError}
      />
    </SellerSuspense>
  );
}
