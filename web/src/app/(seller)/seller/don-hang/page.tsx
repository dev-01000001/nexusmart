import React from "react";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerOrdersClient } from "@/features/seller/SellerOrdersClient";
import { loadSellerOrders, requireSellerPage } from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerOrdersPage() {
  const session = await requireSellerPage();

  if (!session.user.shopId) {
    return (
      <SellerSuspense>
        <SellerOrdersClient
          initialOrders={[]}
          loadError="Tài khoản người bán chưa có gian hàng hoạt động."
        />
      </SellerSuspense>
    );
  }

  const ordersRes = await loadSellerOrders(100);

  return (
    <SellerSuspense>
      <SellerOrdersClient initialOrders={ordersRes.data?.items ?? []} loadError={ordersRes.error} />
    </SellerSuspense>
  );
}
