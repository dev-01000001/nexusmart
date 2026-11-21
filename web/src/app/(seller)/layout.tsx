import React from "react";
import { SellerShell } from "@/features/seller/SellerShell";
import { countPendingReturns } from "@/features/seller/sellerReturns";
import {
  loadSellerOrders,
  loadSellerShop,
  requireSellerPage,
} from "@/features/seller/sellerServer";
import { loadSellerSiteSummaryServer } from "@/features/seller/site/siteServer";
import { summarizeSellerSite } from "@/features/seller/site/siteSummary";

export const dynamic = "force-dynamic";

export default async function SellerLayout({ children }: { children: React.ReactNode }) {
  const session = await requireSellerPage();

  const [shopRes, ordersRes, site] = session.user.shopId
    ? await Promise.all([loadSellerShop(), loadSellerOrders(50), loadSellerSiteSummaryServer()])
    : [{ data: null, error: null }, { data: null, error: null }, summarizeSellerSite(null)];

  const orderItems = ordersRes.data?.items ?? [];
  const pendingOrdersCount = orderItems.filter(
    (o) => o.status.toUpperCase() === "CONFIRMED"
  ).length;
  const pendingReturnsCount = countPendingReturns(orderItems);

  return (
    <SellerShell
      user={session.user}
      shop={shopRes.data}
      pendingOrdersCount={pendingOrdersCount}
      pendingReturnsCount={pendingReturnsCount}
      unreadChatCount={0}
      site={site}
    >
      {children}
    </SellerShell>
  );
}
