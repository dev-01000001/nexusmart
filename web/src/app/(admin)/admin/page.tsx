import React from "react";
import { AdminOverviewClient } from "@/features/admin/AdminOverviewClient";
import { normalizeComplaintsListResponse } from "@/features/admin/adminComplaintsTypes";
import {
  loadAdminAuditLogs,
  loadAdminLedgerSummary,
  loadAdminOrders,
  loadAdminProducts,
  loadAdminShops,
  requireAdminPage,
} from "@/features/admin/adminServer";
import { gw } from "@/lib/server/gateway";

export const dynamic = "force-dynamic";

export default async function AdminOverviewPage() {
  await requireAdminPage();

  const [shopsRes, productsRes, ordersRes, ledgerRes, logsRes, openComplaintsRaw] =
    await Promise.all([
      loadAdminShops(undefined, 100),
      loadAdminProducts("PENDING", 30),
      loadAdminOrders(100),
      loadAdminLedgerSummary(),
      loadAdminAuditLogs(20),
      gw<unknown>("/api/v1/admin/complaints", {
        query: { status: "OPEN", page: 1, page_size: 20 },
      }).catch(() => null),
    ]);

  const loadError = shopsRes.error ?? ordersRes.error ?? ledgerRes.error ?? logsRes.error;
  const openComplaints = openComplaintsRaw
    ? normalizeComplaintsListResponse(openComplaintsRaw).items
    : [];

  return (
    <AdminOverviewClient
      shops={shopsRes.data?.items ?? []}
      pendingProducts={productsRes.data?.items ?? []}
      openComplaints={openComplaints}
      orders={ordersRes.data?.items ?? []}
      ledger={ledgerRes.data}
      auditLogs={logsRes.data?.items ?? []}
      loadError={loadError}
      serverNowMs={Date.now()}
    />
  );
}
