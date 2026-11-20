import React from "react";
import { loadAdminProducts, requireAdminPage } from "@/features/admin/adminServer";
import AdminModerationClient from "./AdminModerationClient";

export const dynamic = "force-dynamic";

export default async function AdminModerationPage() {
  await requireAdminPage();
  const res = await loadAdminProducts(undefined, 100);

  return <AdminModerationClient initialProducts={res.data?.items ?? []} loadError={res.error} />;
}
