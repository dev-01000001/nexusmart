import React from "react";
import { StateView } from "@/components";
import { ADMIN_STAFF_ROLES, roleHasCapability } from "@/lib/admin-rbac";
import type { GatewayAdminApprovalsResponse, GatewayLedgerSummary } from "@/lib/gateway-types";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { requireRole, type Session } from "@/lib/server/session";
import { AdminShell } from "@/features/admin/AdminShell";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./layout.module.css";

export const dynamic = "force-dynamic";

export default async function AdminLayout({ children }: { children: React.ReactNode }) {
  let session: Session;
  try {
    session = await requireRole(...ADMIN_STAFF_ROLES);
  } catch (err) {
    if (err instanceof GatewayError) {
      return (
        <div className={styles.shell}>
          <div className={styles.sheetWrap}>
            <main className={styles.sheet}>
              <div className={shared.page}>
                <header className={shared.titleRow}>
                  <div className={shared.titleBlock}>
                    <h1 className={shared.pageTitle}>Quản trị NexusMart</h1>
                  </div>
                </header>
                <div className={shared.stateWrap}>
                  <StateView
                    kind="error"
                    surface="app"
                    title="Chưa thể xác minh phiên quản trị"
                    message={SAFE_MESSAGES[err.kind]}
                    actionLabel="Tải lại"
                    actionHref="/admin"
                  />
                </div>
              </div>
            </main>
          </div>
        </div>
      );
    }
    throw err;
  }

  const role = session.user.role;
  const canReadShops = roleHasCapability(role, "shops.read");
  const canReadProducts = roleHasCapability(role, "products.read");
  const canReadReturns = roleHasCapability(role, "returns.read");
  const canReadLedger = roleHasCapability(role, "ledger.read");
  const canReadApprovals = roleHasCapability(role, "approvals.read");

  // Zero-403: only query badge counts for capabilities held by the verified staff role.
  const [pendingShopsRes, pendingProductsRes, escalatedReturnsRes, ledgerRes, approvalsRes] =
    await Promise.all([
      canReadShops
        ? gw<{ total?: number; items?: unknown[] }>("/api/v1/admin/shops", {
            query: { status: "PENDING", page: 1, page_size: 1 },
          }).catch(() => null)
        : Promise.resolve(null),
      canReadProducts
        ? gw<{ total?: number; items?: unknown[] }>("/api/v1/admin/products", {
            query: { status: "PENDING", page: 1, page_size: 1 },
          }).catch(() => null)
        : Promise.resolve(null),
      canReadReturns
        ? gw<{ total?: number; items?: unknown[] }>("/api/v1/admin/returns", {
            query: { status: "ESCALATED", page: 1, page_size: 1 },
          }).catch(() => null)
        : Promise.resolve(null),
      canReadLedger
        ? gw<GatewayLedgerSummary>("/api/v1/admin/ledger/summary").catch(() => null)
        : Promise.resolve(null),
      canReadApprovals
        ? gw<GatewayAdminApprovalsResponse>("/api/v1/admin/approvals", {
            query: { status: "PENDING", page: 1, page_size: 1 },
          }).catch(() => null)
        : Promise.resolve(null),
    ]);

  const pendingShopApprovals = pendingShopsRes
    ? (pendingShopsRes.total ?? pendingShopsRes.items?.length ?? 0)
    : 0;
  const pendingProductReviews = pendingProductsRes
    ? (pendingProductsRes.total ?? pendingProductsRes.items?.length ?? 0)
    : 0;
  const openComplaintsCount = escalatedReturnsRes
    ? (escalatedReturnsRes.total ?? escalatedReturnsRes.items?.length ?? 0)
    : 0;
  const ledgerUnbalanced = Boolean(ledgerRes && !ledgerRes.is_balanced);
  const pendingApprovalsCount = approvalsRes
    ? (approvalsRes.shop_total ?? approvalsRes.shop_approvals?.length ?? 0) +
      (approvalsRes.refund_total ?? approvalsRes.refund_approvals?.length ?? 0)
    : 0;

  return (
    <AdminShell
      user={session.user}
      pendingShopApprovals={pendingShopApprovals}
      pendingProductReviews={pendingProductReviews}
      openComplaintsCount={openComplaintsCount}
      ledgerUnbalanced={ledgerUnbalanced}
      pendingApprovalsCount={pendingApprovalsCount}
    >
      {children}
    </AdminShell>
  );
}
