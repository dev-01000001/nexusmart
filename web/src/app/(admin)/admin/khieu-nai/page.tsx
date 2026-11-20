import React from "react";
import { requireAdminPage } from "@/features/admin/adminServer";
import AdminDisputesClient from "./AdminDisputesClient";

export const dynamic = "force-dynamic";

export default async function AdminDisputesPage() {
  await requireAdminPage();
  return <AdminDisputesClient />;
}
