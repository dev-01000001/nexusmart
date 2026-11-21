import React from "react";
import {
  loadAdminFlashSales,
  loadAdminShops,
  requireAdminPage,
} from "@/features/admin/adminServer";
import AdminFlashSaleClient from "./AdminFlashSaleClient";

export const dynamic = "force-dynamic";

export default async function AdminFlashSalePage() {
  await requireAdminPage();
  const [flashSalesRes, shopsRes] = await Promise.all([
    loadAdminFlashSales(),
    loadAdminShops(undefined, 100),
  ]);

  const shopNames: Record<string, string> = {};
  for (const shop of shopsRes.data?.items ?? []) {
    shopNames[shop.id] = shop.name;
  }

  return (
    <AdminFlashSaleClient
      initialFlashSales={flashSalesRes.data ?? []}
      shopNames={shopNames}
      loadError={flashSalesRes.error}
    />
  );
}
