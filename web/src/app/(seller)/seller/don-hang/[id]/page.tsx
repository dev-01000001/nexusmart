import React from "react";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerOrderDetailClient } from "@/features/seller/SellerOrderDetailClient";
import { loadSellerOrderById, requireSellerPage } from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerOrderDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const session = await requireSellerPage();
  const { id } = await params;

  const orderRes = session.user.shopId
    ? await loadSellerOrderById(id, session.user.shopId)
    : { data: null, error: "Tài khoản người bán chưa có gian hàng hoạt động." };

  return (
    <SellerSuspense>
      <SellerOrderDetailClient initialOrder={orderRes.data} loadError={orderRes.error} />
    </SellerSuspense>
  );
}
