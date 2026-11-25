import React from "react";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerFinanceClient } from "@/features/seller/SellerFinanceClient";
import {
  loadSellerBalance,
  loadSellerBankAccount,
  loadSellerOrders,
  requireSellerPage,
} from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerFinancePage() {
  await requireSellerPage();

  const [balanceRes, bankRes, ordersRes] = await Promise.all([
    loadSellerBalance(),
    loadSellerBankAccount(),
    loadSellerOrders(100),
  ]);

  return (
    <SellerSuspense>
      <SellerFinanceClient
        initialBalance={balanceRes.data}
        balanceError={balanceRes.error}
        initialBankAccount={bankRes.data}
        initialOrders={ordersRes.data?.items ?? []}
        loadError={ordersRes.error}
      />
    </SellerSuspense>
  );
}
