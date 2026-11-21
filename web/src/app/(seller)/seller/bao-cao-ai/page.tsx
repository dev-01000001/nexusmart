import React from "react";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { requireSellerPage } from "@/features/seller/sellerServer";
import { SellerAiReportClient } from "./SellerAiReportClient";

export const dynamic = "force-dynamic";

export default async function SellerAiReportPage() {
  const session = await requireSellerPage();

  return (
    <SellerSuspense>
      <SellerAiReportClient hasShop={Boolean(session.user.shopId)} />
    </SellerSuspense>
  );
}
