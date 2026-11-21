import React from "react";
import { AdminShell } from "@/features/admin/AdminShell";
import { normalizeComplaintsListResponse } from "@/features/admin/adminComplaintsTypes";
import {
  loadAdminLedgerSummary,
  loadAdminProducts,
  loadAdminShops,
  requireAdminPage,
} from "@/features/admin/adminServer";
import { gw } from "@/lib/server/gateway";

export const dynamic = "force-dynamic";

export default async function AdminLayout({ children }: { children: React.ReactNode }) {
  const session = await requireAdminPage();

  const [pendingShopsRes, pendingProductsRes, ledgerRes, openComplaintsRes] = await Promise.all([
    loadAdminShops("PENDING", 50),
    loadAdminProducts("PENDING", 50),
    loadAdminLedgerSummary(),
    gw<unknown>("/api/v1/admin/complaints", {
      query: { status: "OPEN", page: 1, page_size: 1 },
    }).catch(() => null),
  ]);

  const pendingShopApprovals = pendingShopsRes.data?.total ?? 0;
  const pendingProductReviews = pendingProductsRes.data?.total ?? 0;
  const openComplaintsCount = openComplaintsRes
    ? normalizeComplaintsListResponse(openComplaintsRes).total
    : 0;
  const ledgerUnbalanced = Boolean(ledgerRes.data && !ledgerRes.data.is_balanced);

  return (
    <AdminShell
      user={session.user}
      pendingShopApprovals={pendingShopApprovals}
      pendingProductReviews={pendingProductReviews}
      openComplaintsCount={openComplaintsCount}
      ledgerUnbalanced={ledgerUnbalanced}
    >
      {children}
    </AdminShell>
  );
}
