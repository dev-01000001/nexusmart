import React from "react";
import {
  loadSellerAddresses,
  loadSellerShippingSettings,
  loadSellerShop,
  requireSellerPage,
} from "@/features/seller/sellerServer";
import { loadSellerSiteSummaryServer } from "@/features/seller/site/siteServer";
import SellerProfileClient from "./SellerProfileClient";

export const dynamic = "force-dynamic";

export default async function SellerProfileOpsPage() {
  const session = await requireSellerPage();

  const [shopRes, addressesRes, shippingSettingsRes, siteSummary] = await Promise.all([
    loadSellerShop(),
    loadSellerAddresses(),
    loadSellerShippingSettings(),
    loadSellerSiteSummaryServer(),
  ]);

  return (
    <SellerProfileClient
      initialUser={session.user}
      shop={shopRes.data}
      shopError={shopRes.error}
      addresses={addressesRes.data ?? []}
      addressesError={addressesRes.error}
      initialShippingSettings={shippingSettingsRes.data}
      shippingSettingsError={shippingSettingsRes.error}
      site={siteSummary}
    />
  );
}
