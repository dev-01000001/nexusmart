import React from "react";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerOrderDetailClient } from "@/features/seller/SellerOrderDetailClient";
import {
  loadSellerOrderById,
  loadSellerOrderShipment,
  requireSellerPage,
} from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerOrderDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const session = await requireSellerPage();
  const { id } = await params;

  const [orderRes, shipmentRes] = await Promise.all([
    loadSellerOrderById(id, session.user.shopId),
    loadSellerOrderShipment(id),
  ]);

  return (
    <SellerSuspense>
      <SellerOrderDetailClient
        initialOrder={orderRes.data}
        initialShipment={shipmentRes.data}
        loadError={orderRes.error}
      />
    </SellerSuspense>
  );
}
