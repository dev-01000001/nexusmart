import React from "react";
import { SellerShell } from "@/features/seller/SellerShell";
import {
  loadSellerOrders,
  loadSellerShop,
  requireSellerPage,
} from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerLayout({ children }: { children: React.ReactNode }) {
  const session = await requireSellerPage();

  const [shopRes, ordersRes] = session.user.shopId
    ? await Promise.all([loadSellerShop(), loadSellerOrders(50)])
    : [
        { data: null, error: null },
        { data: null, error: null },
      ];

  const pendingOrdersCount = (ordersRes.data?.items ?? []).filter(
    (o) => o.status.toUpperCase() === "CONFIRMED"
  ).length;

  return (
    <SellerShell user={session.user} shop={shopRes.data} pendingOrdersCount={pendingOrdersCount}>
      {children}
    </SellerShell>
  );
}
