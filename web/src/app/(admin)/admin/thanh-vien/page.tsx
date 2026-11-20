import React from "react";
import {
  loadAdminFlashSales,
  loadAdminVouchers,
  requireAdminPage,
} from "@/features/admin/adminServer";
import AdminLoyaltyClient from "./AdminLoyaltyClient";

export const dynamic = "force-dynamic";

export default async function AdminLoyaltyPage() {
  await requireAdminPage();
  const [vouchersRes, flashSalesRes] = await Promise.all([
    loadAdminVouchers(),
    loadAdminFlashSales(),
  ]);

  return (
    <AdminLoyaltyClient
      initialVouchers={vouchersRes.data ?? []}
      initialFlashSales={flashSalesRes.data ?? []}
      vouchersError={vouchersRes.error}
      flashSalesError={flashSalesRes.error}
    />
  );
}
