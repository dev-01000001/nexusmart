import React from "react";
import { ErrorState, PageHeader } from "@/components/app";
import { roleHasCapability } from "@/lib/admin-rbac";
import { formatVnDateOffset } from "@/lib/format";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { ADMIN_ROLES, requireRole, type Session } from "@/lib/server/session";
import type {
  GatewayOrder,
  GatewayPlatformReport,
  GatewayPlatformReportResponse,
  GatewayReturnRequest,
  PaginatedResponse,
} from "@/lib/gateway-types";
import {
  normalizeAdminOrder,
  type GwAdminOrder,
  type GwAdminProductExtended,
  type GwAdminShop,
  type GwAuditLog,
  type GwLedgerSummary,
} from "@/features/admin/adminApiTypes";
import { AdminOverviewClient } from "@/features/admin/AdminOverviewClient";

export const dynamic = "force-dynamic";

export default async function AdminOverviewPage() {
  let session: Session;
  try {
    session = await requireRole(...ADMIN_ROLES);
  } catch (err) {
    if (err instanceof GatewayError) {
      return (
        <div className="space-y-5">
          <PageHeader title="Tổng quan quản trị" />
          <ErrorState
            title="Chưa thể xác minh phiên quản trị"
            description={SAFE_MESSAGES[err.kind]}
            retryHref="/admin"
          />
        </div>
      );
    }
    throw err;
  }
  const role = session.user.role;
  const serverNowMs = Date.now();
  const defaultToDate = formatVnDateOffset(0, serverNowMs);
  const defaultFromDate = formatVnDateOffset(-29, serverNowMs);

  const canShops = roleHasCapability(role, "shops.read");
  const canProducts = roleHasCapability(role, "products.read");
  const canReturns = roleHasCapability(role, "returns.read");
  const canOrders = roleHasCapability(role, "orders.read");
  const canLedger = roleHasCapability(role, "ledger.read");
  const canAudit = roleHasCapability(role, "audit_logs.read");
  const canReports = roleHasCapability(role, "reports.read");

  const [
    shopsRes,
    productsRes,
    returnsRes,
    ordersRes,
    needsReviewRes,
    ledgerRes,
    auditRes,
    reportRes,
  ] = await Promise.all([
    canShops
      ? gw<PaginatedResponse<GwAdminShop>>("/api/v1/admin/shops", {
          query: { page: 1, page_size: 100 },
        })
          .then((d) => ({ items: d.items ?? [], err: false }))
          .catch(() => ({ items: [] as GwAdminShop[], err: true }))
      : Promise.resolve({ items: [] as GwAdminShop[], err: false }),
    canProducts
      ? gw<PaginatedResponse<GwAdminProductExtended>>("/api/v1/admin/products", {
          query: { status: "PENDING", page: 1, page_size: 20 },
        })
          .then((d) => ({ items: d.items ?? [], err: false }))
          .catch(() => ({ items: [] as GwAdminProductExtended[], err: true }))
      : Promise.resolve({ items: [] as GwAdminProductExtended[], err: false }),
    canReturns
      ? gw<PaginatedResponse<GatewayReturnRequest>>("/api/v1/admin/returns", {
          query: { status: "ESCALATED", page: 1, page_size: 20 },
        })
          .then((d) => ({ items: d.items ?? [], err: false }))
          .catch(() => ({ items: [] as GatewayReturnRequest[], err: true }))
      : Promise.resolve({ items: [] as GatewayReturnRequest[], err: false }),
    canOrders
      ? gw<PaginatedResponse<GwAdminOrder>>("/api/v1/admin/orders", {
          query: { page: 1, page_size: 50 },
        })
          .then((d) => ({ items: (d.items ?? []).map(normalizeAdminOrder), err: false }))
          .catch(() => ({ items: [] as GwAdminOrder[], err: true }))
      : Promise.resolve({ items: [] as GwAdminOrder[], err: false }),
    canOrders
      ? gw<PaginatedResponse<GatewayOrder>>("/api/v1/admin/orders/needs-review", {
          query: { page: 1, page_size: 20 },
        })
          .then((d) => ({ items: d.items ?? [], total: d.total ?? 0 }))
          .catch(() => ({ items: [] as GatewayOrder[], total: 0 }))
      : Promise.resolve({ items: [] as GatewayOrder[], total: 0 }),
    canLedger
      ? gw<GwLedgerSummary>("/api/v1/admin/ledger/summary")
          .then((d) => ({ data: d, err: false }))
          .catch(() => ({ data: null as GwLedgerSummary | null, err: true }))
      : Promise.resolve({ data: null as GwLedgerSummary | null, err: false }),
    canAudit
      ? gw<PaginatedResponse<GwAuditLog>>("/api/v1/admin/audit-logs", {
          query: { page: 1, page_size: 20 },
        })
          .then((d) => ({ items: d.items ?? [], err: false }))
          .catch(() => ({ items: [] as GwAuditLog[], err: true }))
      : Promise.resolve({ items: [] as GwAuditLog[], err: false }),
    canReports
      ? gw<GatewayPlatformReportResponse>("/api/v1/admin/reports", {
          query: { from_date: defaultFromDate, to_date: defaultToDate, top_limit: 5 },
        })
          .then((d) => d.report ?? null)
          .catch(() => null as GatewayPlatformReport | null)
      : Promise.resolve(null as GatewayPlatformReport | null),
  ]);

  const anyError =
    canShops && shopsRes.err && canOrders && ordersRes.err && canLedger && ledgerRes.err;

  return (
    <AdminOverviewClient
      shops={shopsRes.items}
      pendingProducts={productsRes.items}
      escalatedReturns={returnsRes.items}
      orders={ordersRes.items}
      needsReviewOrders={needsReviewRes.items}
      needsReviewTotal={needsReviewRes.total}
      ledger={ledgerRes.data}
      auditLogs={auditRes.items}
      initialReport={reportRes}
      initialReportFrom={defaultFromDate}
      initialReportTo={defaultToDate}
      loadError={anyError ? "Không thể tải số liệu tổng quan quản trị lúc này." : null}
      serverNowMs={serverNowMs}
    />
  );
}
