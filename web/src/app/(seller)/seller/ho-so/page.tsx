import React from "react";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import type { SellerAddress } from "@/features/seller/sellerReturns";
import { loadSellerShop, requireSellerPage } from "@/features/seller/sellerServer";
import { loadSellerSiteSummaryServer } from "@/features/seller/site/siteServer";
import SellerProfileClient from "./SellerProfileClient";

export const dynamic = "force-dynamic";

async function loadSellerAddresses(): Promise<{
  data: SellerAddress[];
  error: string | null;
}> {
  try {
    const res = await gw<{ addresses?: SellerAddress[] }>("/api/v1/users/me/addresses");
    return { data: res.addresses ?? [], error: null };
  } catch (err) {
    const message =
      err instanceof GatewayError ? SAFE_MESSAGES[err.kind] : SAFE_MESSAGES.unavailable;
    return { data: [], error: message };
  }
}

export default async function SellerProfileOpsPage() {
  const session = await requireSellerPage();

  const [shopRes, addressesRes, siteSummary] = await Promise.all([
    loadSellerShop(),
    loadSellerAddresses(),
    loadSellerSiteSummaryServer(),
  ]);

  return (
    <SellerProfileClient
      initialUser={session.user}
      shop={shopRes.data}
      shopError={shopRes.error}
      addresses={addressesRes.data}
      addressesError={addressesRes.error}
      site={siteSummary}
    />
  );
}
