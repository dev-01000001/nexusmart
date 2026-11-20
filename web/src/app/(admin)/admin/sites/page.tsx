import React from "react";
import { requireAdminPage } from "@/features/admin/adminServer";
import { loadAdminSitesServer } from "@/features/admin/sites/adminSitesServer";
import { AdminSitesClient } from "@/features/admin/sites/AdminSitesClient";

export const dynamic = "force-dynamic";

export default async function AdminSitesPage() {
  await requireAdminPage();
  const res = await loadAdminSitesServer(undefined, undefined, 100);

  return (
    <AdminSitesClient
      initialSites={res.items}
      rootDomain={res.rootDomain}
      shopNameById={res.shopNameById}
      loadError={res.error}
    />
  );
}
