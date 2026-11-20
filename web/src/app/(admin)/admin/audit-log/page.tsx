import React from "react";
import { AdminAuditLogClient } from "@/features/admin/AdminAuditLogClient";
import { loadAdminAuditLogs, requireAdminPage } from "@/features/admin/adminServer";

export const dynamic = "force-dynamic";

export default async function AdminAuditLogPage() {
  await requireAdminPage();
  const logsRes = await loadAdminAuditLogs(100);

  return <AdminAuditLogClient initialLogs={logsRes.data?.items ?? []} loadError={logsRes.error} />;
}
