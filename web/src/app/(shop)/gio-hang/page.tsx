import React from "react";
import { loadCatalogLookups } from "@/lib/catalog";
import { gw } from "@/lib/server/gateway";
import type { GwVoucher } from "@/features/commerce/types";
import { CartClient } from "./CartClient";

export const dynamic = "force-dynamic";

export default async function CartPage() {
  const [catalogRes, vouchersRes] = await Promise.allSettled([
    loadCatalogLookups(),
    gw<{ vouchers: GwVoucher[] }>("/api/v1/vouchers", { auth: "none" }),
  ]);

  const catalogProducts = catalogRes.status === "fulfilled" ? catalogRes.value.products : [];
  const catalogShops = catalogRes.status === "fulfilled" ? catalogRes.value.shops : [];
  const initialVouchers =
    vouchersRes.status === "fulfilled" ? (vouchersRes.value.vouchers ?? []) : [];

  return (
    <CartClient
      catalogProducts={catalogProducts}
      catalogShops={catalogShops}
      initialVouchers={initialVouchers}
    />
  );
}
