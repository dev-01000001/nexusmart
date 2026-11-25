import React from "react";
import { AdminReconciliationClient } from "@/features/admin/AdminReconciliationClient";
import { loadAdminLedgerSummary, requireAdminPage } from "@/features/admin/adminServer";

export const dynamic = "force-dynamic";

export default async function AdminReconciliationPage() {
  await requireAdminPage();
  const ledgerRes = await loadAdminLedgerSummary();

  return (
    <AdminReconciliationClient
      initialLedger={ledgerRes.data}
      loadError={ledgerRes.error}
      serverNowMs={Date.now()}
    />
  );
}
