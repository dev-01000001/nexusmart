import React from "react";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerFinanceClient } from "@/features/seller/SellerFinanceClient";
import {
  loadSellerBalance,
  loadSellerOrders,
  requireSellerPage,
} from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerFinancePage() {
  const session = await requireSellerPage();

  if (!session.user.shopId) {
    return (
      <SellerSuspense>
        <SellerFinanceClient
          initialBalance={null}
          initialOrders={[]}
          loadError="Tài khoản người bán chưa có gian hàng hoạt động."
        />
      </SellerSuspense>
    );
  }

  const [balanceRes, ordersRes] = await Promise.all([loadSellerBalance(), loadSellerOrders(100)]);

  return (
    <SellerSuspense>
      <SellerFinanceClient
        initialBalance={balanceRes.data}
        balanceError={balanceRes.error}
        initialOrders={ordersRes.data?.items ?? []}
        loadError={ordersRes.error}
      />
    </SellerSuspense>
  );
}
