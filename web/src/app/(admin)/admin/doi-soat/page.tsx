import React from "react";
import { notFound } from "next/navigation";
import { roleHasAnyCapability, roleHasCapability } from "@/lib/admin-rbac";
import { gw } from "@/lib/server/gateway";
import { requireSession } from "@/lib/server/session";
import type {
  GatewayCarrierConfig,
  GatewayOrder,
  GatewayReconciliationQueueItem,
  GatewayRefund,
  GatewaySellerPayoutListResponse,
  PaginatedResponse,
} from "@/lib/gateway-types";
import type { GwLedgerSummary } from "@/features/admin/adminApiTypes";
import {
  AdminReconciliationClient,
  type ReconciliationMainTab,
} from "@/features/admin/AdminReconciliationClient";

export const dynamic = "force-dynamic";

const ALLOWED_TABS = new Set<ReconciliationMainTab>([
  "ledger",
  "cod",
  "queue",
  "refunds",
  "payouts",
  "needs-review",
]);

interface PageProps {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}

export default async function AdminReconciliationPage({ searchParams }: PageProps) {
  const session = await requireSession("/admin/doi-soat");
  const role = session.user.role;
  if (!roleHasAnyCapability(role, ["ledger.read", "orders.read", "refunds.read", "payouts.read"])) {
    notFound();
  }

  const canReadLedger = roleHasCapability(role, "ledger.read");
  const canReadRefunds = roleHasCapability(role, "refunds.read");
  const canReadPayouts = roleHasCapability(role, "payouts.read");
  const canReadOrders = roleHasCapability(role, "orders.read");
  const canReadCarriers = roleHasCapability(role, "carriers.read");

  const resolvedParams = await searchParams;
  const rawTab = typeof resolvedParams?.tab === "string" ? resolvedParams.tab.trim() : "";
  const requestedTab = ALLOWED_TABS.has(rawTab as ReconciliationMainTab)
    ? (rawTab as ReconciliationMainTab)
    : undefined;

  const [ledgerRes, queueRes, refundsRes, payoutsRes, needsReviewRes, carriersRes] =
    await Promise.all([
      canReadLedger
        ? gw<GwLedgerSummary>("/api/v1/admin/ledger/summary")
            .then((data) => ({ data, error: null as string | null }))
            .catch(() => ({
              data: null as GwLedgerSummary | null,
              error: "Không thể tải số liệu sổ cái đối soát lúc này.",
            }))
        : Promise.resolve({ data: null as GwLedgerSummary | null, error: null as string | null }),
      canReadLedger
        ? gw<PaginatedResponse<GatewayReconciliationQueueItem>>(
            "/api/v1/admin/reconciliation/queue",
            { query: { status: "pending", page: 1, page_size: 20 } }
          ).catch(() => null)
        : Promise.resolve(null),
      canReadRefunds
        ? gw<PaginatedResponse<GatewayRefund>>("/api/v1/admin/refunds", {
            query: { page: 1, page_size: 20 },
          }).catch(() => null)
        : Promise.resolve(null),
      canReadPayouts
        ? gw<GatewaySellerPayoutListResponse>("/api/v1/admin/payouts", {
            query: { status: "PENDING", page: 1, page_size: 20 },
          }).catch(() => null)
        : Promise.resolve(null),
      canReadOrders
        ? gw<PaginatedResponse<GatewayOrder>>("/api/v1/admin/orders/needs-review", {
            query: { page: 1, page_size: 20 },
          }).catch(() => null)
        : Promise.resolve(null),
      canReadCarriers
        ? gw<{ carriers?: GatewayCarrierConfig[] }>("/api/v1/admin/carriers").catch(() => null)
        : Promise.resolve(null),
    ]);

  return (
    <AdminReconciliationClient
      initialTab={requestedTab}
      initialLedger={ledgerRes.data}
      initialQueueItems={queueRes?.items ?? []}
      initialQueueTotal={queueRes?.total ?? 0}
      initialRefunds={refundsRes?.items ?? []}
      initialRefundsTotal={refundsRes?.total ?? 0}
      initialPayouts={payoutsRes?.items ?? []}
      initialPayoutsTotal={payoutsRes?.total ?? 0}
      initialNeedsReviewOrders={needsReviewRes?.items ?? []}
      initialNeedsReviewTotal={needsReviewRes?.total ?? 0}
      carriers={carriersRes?.carriers ?? []}
      loadError={ledgerRes.error}
      serverNowMs={Date.now()}
    />
  );
}
