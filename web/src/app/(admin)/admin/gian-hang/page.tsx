import React from "react";
import { AdminShopsClient } from "@/features/admin/AdminShopsClient";
import { loadAdminShops, requireAdminPage } from "@/features/admin/adminServer";

export const dynamic = "force-dynamic";

export default async function AdminShopApprovalsPage() {
  await requireAdminPage();
  const shopsRes = await loadAdminShops(undefined, 100);

  return <AdminShopsClient initialShops={shopsRes.data?.items ?? []} loadError={shopsRes.error} />;
}
