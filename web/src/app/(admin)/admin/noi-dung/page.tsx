import React from "react";
import { requireAdminPage } from "@/features/admin/adminServer";
import AdminContentClient from "./AdminContentClient";

export const dynamic = "force-dynamic";

export default async function AdminContentPage() {
  await requireAdminPage();
  return <AdminContentClient />;
}
