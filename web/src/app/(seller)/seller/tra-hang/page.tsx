import React from "react";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerReturnsClient } from "@/features/seller/SellerReturnsClient";
import { loadSellerReturns, requireSellerPage } from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerReturnsPage() {
  const session = await requireSellerPage();

  if (!session.user.shopId) {
    return (
      <SellerSuspense>
        <SellerReturnsClient
          initialReturns={[]}
          loadError="Tài khoản người bán chưa có gian hàng hoạt động."
        />
      </SellerSuspense>
    );
  }

  const res = await loadSellerReturns(undefined, 1, 100);

  return (
    <SellerSuspense>
      <SellerReturnsClient initialReturns={res.data?.items ?? []} loadError={res.error} />
    </SellerSuspense>
  );
}
