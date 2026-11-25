import React from "react";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerPromotionsClient } from "@/features/seller/SellerPromotionsClient";
import {
  loadFlashSales,
  loadSellerProducts,
  loadSellerVouchers,
  requireSellerPage,
} from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerPromotionsPage() {
  const session = await requireSellerPage();

  if (!session.user.shopId) {
    return (
      <SellerSuspense>
        <SellerPromotionsClient
          initialVouchers={[]}
          initialFlashSales={[]}
          shopProducts={[]}
          loadError="Tài khoản người bán chưa có gian hàng hoạt động."
        />
      </SellerSuspense>
    );
  }

  const [vouchersRes, flashRes, productsRes] = await Promise.all([
    loadSellerVouchers(),
    loadFlashSales(),
    loadSellerProducts(100),
  ]);

  return (
    <SellerSuspense>
      <SellerPromotionsClient
        initialVouchers={vouchersRes.data ?? []}
        initialFlashSales={flashRes.data ?? []}
        shopProducts={productsRes.data?.items ?? []}
        loadError={vouchersRes.error ?? flashRes.error ?? productsRes.error}
        serverNowMs={Date.now()}
      />
    </SellerSuspense>
  );
}
