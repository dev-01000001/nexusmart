import React from "react";
import { gw } from "@/lib/server/gateway";
import { requireCapability } from "@/lib/server/session";
import type { GatewayReturnRequest, PaginatedResponse } from "@/lib/gateway-types";
import { AdminReturnsClient } from "@/features/admin/AdminReturnsClient";

export const dynamic = "force-dynamic";

export default async function AdminComplaintsPage() {
  await requireCapability("returns.read");

  let initialItems: GatewayReturnRequest[] = [];
  let initialTotal = 0;
  let escalatedCount = 0;
  let evidenceCount = 0;
  let loadError: string | null = null;

  try {
    const [listRes, escRes, eviRes] = await Promise.all([
      gw<PaginatedResponse<GatewayReturnRequest>>("/api/v1/admin/returns", {
        query: { page: 1, page_size: 20 },
      }),
      gw<PaginatedResponse<GatewayReturnRequest>>("/api/v1/admin/returns", {
        query: { status: "ESCALATED", page: 1, page_size: 1 },
      }).catch(() => null),
      gw<PaginatedResponse<GatewayReturnRequest>>("/api/v1/admin/returns", {
        query: { status: "EVIDENCE_REQUESTED", page: 1, page_size: 1 },
      }).catch(() => null),
    ]);

    initialItems = listRes.items ?? [];
    initialTotal = listRes.total ?? initialItems.length;
    escalatedCount = escRes?.total ?? 0;
    evidenceCount = eviRes?.total ?? 0;
  } catch {
    loadError = "Không thể tải danh sách yêu cầu trả hàng và khiếu nại lúc này.";
  }

  return (
    <AdminReturnsClient
      initialItems={initialItems}
      initialTotal={initialTotal}
      initialEscalatedCount={escalatedCount}
      initialEvidenceCount={evidenceCount}
      loadError={loadError}
    />
  );
}
