import React from "react";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerOrdersClient } from "@/features/seller/SellerOrdersClient";
import {
  loadSellerOrders,
  loadSellerProducts,
  requireSellerPage,
} from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerOrdersPage() {
  const session = await requireSellerPage();

  if (!session.user.shopId) {
    return (
      <SellerSuspense>
        <SellerOrdersClient
          initialOrders={[]}
          productImages={{}}
          loadError="Tài khoản người bán chưa có gian hàng hoạt động."
        />
      </SellerSuspense>
    );
  }

  const [ordersRes, productsRes] = await Promise.all([
    loadSellerOrders(100),
    loadSellerProducts(100),
  ]);

  const productImages: Record<string, string> = {};
  for (const p of productsRes.data?.items ?? []) {
    if (p.images?.[0]) {
      productImages[p.id] = p.images[0];
    }
  }

  return (
    <SellerSuspense>
      <SellerOrdersClient
        initialOrders={ordersRes.data?.items ?? []}
        productImages={productImages}
        loadError={ordersRes.error}
      />
    </SellerSuspense>
  );
}
