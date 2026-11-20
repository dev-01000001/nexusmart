import React from "react";
import { AdminOverviewClient } from "@/features/admin/AdminOverviewClient";
import {
  loadAdminAuditLogs,
  loadAdminLedgerSummary,
  loadAdminOrders,
  loadAdminShops,
  requireAdminPage,
} from "@/features/admin/adminServer";

export const dynamic = "force-dynamic";

export default async function AdminOverviewPage() {
  await requireAdminPage();

  const [shopsRes, ordersRes, ledgerRes, logsRes] = await Promise.all([
    loadAdminShops(undefined, 100),
    loadAdminOrders(100),
    loadAdminLedgerSummary(),
    loadAdminAuditLogs(20),
  ]);

  const loadError = shopsRes.error ?? ordersRes.error ?? ledgerRes.error ?? logsRes.error;

  return (
    <AdminOverviewClient
      shops={shopsRes.data?.items ?? []}
      orders={ordersRes.data?.items ?? []}
      ledger={ledgerRes.data}
      auditLogs={logsRes.data?.items ?? []}
      loadError={loadError}
    />
  );
}
