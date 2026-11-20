import React from "react";
import { AdminShell } from "@/features/admin/AdminShell";
import {
  loadAdminLedgerSummary,
  loadAdminProducts,
  loadAdminShops,
  requireAdminPage,
} from "@/features/admin/adminServer";

export const dynamic = "force-dynamic";

export default async function AdminLayout({ children }: { children: React.ReactNode }) {
  const session = await requireAdminPage();

  const [pendingShopsRes, pendingProductsRes, ledgerRes] = await Promise.all([
    loadAdminShops("PENDING", 50),
    loadAdminProducts("PENDING", 50),
    loadAdminLedgerSummary(),
  ]);

  const pendingShopApprovals = pendingShopsRes.data?.total ?? 0;
  const pendingProductReviews = pendingProductsRes.data?.total ?? 0;
  const ledgerUnbalanced = Boolean(ledgerRes.data && !ledgerRes.data.is_balanced);

  return (
    <AdminShell
      user={session.user}
      pendingShopApprovals={pendingShopApprovals}
      pendingProductReviews={pendingProductReviews}
      ledgerUnbalanced={ledgerUnbalanced}
    >
      {children}
    </AdminShell>
  );
}
