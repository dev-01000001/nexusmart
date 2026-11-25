import React from "react";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerPromotionsClient } from "@/features/seller/SellerPromotionsClient";
import {
  loadFlashSales,
  loadSellerDiscountPrograms,
  loadSellerFlashSaleItems,
  loadSellerProducts,
  loadSellerVouchers,
  requireSellerPage,
} from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerPromotionsPage() {
  await requireSellerPage();

  const [vouchersRes, flashRes, flashItemsRes, programsRes, productsRes] = await Promise.all([
    loadSellerVouchers(),
    loadFlashSales(),
    loadSellerFlashSaleItems(),
    loadSellerDiscountPrograms(),
    loadSellerProducts(100),
  ]);

  const loadError = vouchersRes.error ?? productsRes.error;

  return (
    <SellerSuspense>
      <SellerPromotionsClient
        initialVouchers={vouchersRes.data ?? []}
        initialFlashSales={flashRes.data ?? []}
        initialFlashItems={flashItemsRes.data ?? []}
        initialDiscountPrograms={programsRes.data ?? []}
        shopProducts={productsRes.data?.items ?? []}
        loadError={loadError}
        serverNowMs={Date.now()}
      />
    </SellerSuspense>
  );
}
