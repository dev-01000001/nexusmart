import React from "react";
import { SellerShell } from "@/features/seller/SellerShell";
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

  const pendingOrdersCount = (ordersRes.data?.items ?? []).filter(
    (o) => o.status.toUpperCase() === "CONFIRMED"
  ).length;

  return (
    <SellerShell
      user={session.user}
      shop={shopRes.data}
      pendingOrdersCount={pendingOrdersCount}
      site={site}
    >
      {children}
    </SellerShell>
  );
}
