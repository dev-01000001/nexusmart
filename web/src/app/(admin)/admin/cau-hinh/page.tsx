import React from "react";
import { requireAdminPage } from "@/features/admin/adminServer";
import AdminConfigClient from "./AdminConfigClient";

export const dynamic = "force-dynamic";

export default async function AdminPlatformConfigPage() {
  await requireAdminPage();
  return <AdminConfigClient />;
}
