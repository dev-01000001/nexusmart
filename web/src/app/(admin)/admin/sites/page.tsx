import React from "react";
import { headers } from "next/headers";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { requireAdminPage } from "@/features/admin/adminServer";
import { loadAdminSitesServer } from "@/features/admin/sites/adminSitesServer";
import { AdminSitesClient } from "@/features/admin/sites/AdminSitesClient";

export const dynamic = "force-dynamic";

export default async function AdminSitesPage() {
  await requireAdminPage();
  const [res, h] = await Promise.all([loadAdminSitesServer(undefined, undefined, 100), headers()]);
  const requestHost = resolveRequestHost(h, "");
  const requestProto = resolveRequestProto(h, `http://${requestHost || "localhost"}`);

  return (
    <AdminSitesClient
      initialSites={res.items}
      rootDomain={res.rootDomain}
      requestHost={requestHost}
      requestProto={requestProto}
      shopNameById={res.shopNameById}
      loadError={res.error}
    />
  );
}
