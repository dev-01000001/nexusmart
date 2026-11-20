import React from "react";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerOverviewClient } from "@/features/seller/SellerOverviewClient";
import {
  loadSellerBalance,
  loadSellerOrders,
  loadSellerProducts,
  requireSellerPage,
} from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerOverviewPage() {
  const session = await requireSellerPage();

  if (!session.user.shopId) {
    return (
      <SellerSuspense>
        <SellerOverviewClient
          initialOrders={[]}
          initialProducts={[]}
          initialBalance={null}
          loadError="Tài khoản người bán chưa có gian hàng được kích hoạt."
        />
      </SellerSuspense>
    );
  }

  const [ordersRes, productsRes, balanceRes] = await Promise.all([
    loadSellerOrders(100),
    loadSellerProducts(100),
    loadSellerBalance(),
  ]);

  const loadError = ordersRes.error ?? productsRes.error ?? balanceRes.error;

  return (
    <SellerSuspense>
      <SellerOverviewClient
        initialOrders={ordersRes.data?.items ?? []}
        initialProducts={productsRes.data?.items ?? []}
        initialBalance={balanceRes.data}
        loadError={loadError}
      />
    </SellerSuspense>
  );
}
