import React from "react";
import { gw } from "@/lib/server/gateway";
import { requireCapability } from "@/lib/server/session";
import type { PaginatedResponse } from "@/lib/gateway-types";
import type { GwAuditLog } from "@/features/admin/adminApiTypes";
import { AdminAuditLogClient } from "@/features/admin/AdminAuditLogClient";

export const dynamic = "force-dynamic";

export default async function AdminAuditLogPage() {
  await requireCapability("audit_logs.read");

  let initialLogs: GwAuditLog[] = [];
  let initialTotal = 0;
  let loadError: string | null = null;

  try {
    const res = await gw<PaginatedResponse<GwAuditLog>>("/api/v1/admin/audit-logs", {
      query: { page: 1, page_size: 20 },
    });
    initialLogs = res.items ?? [];
    initialTotal = res.total ?? initialLogs.length;
  } catch {
    loadError = "Không thể tải nhật ký kiểm toán lúc này.";
  }

  return (
    <AdminAuditLogClient
      initialLogs={initialLogs}
      initialTotal={initialTotal}
      loadError={loadError}
    />
  );
}
