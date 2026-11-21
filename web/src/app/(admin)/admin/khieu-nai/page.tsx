import React from "react";
import { AdminComplaintsClient } from "@/features/admin/AdminComplaintsClient";
import {
  normalizeComplaintsListResponse,
  type AdminComplaint,
} from "@/features/admin/adminComplaintsTypes";
import { requireAdminPage } from "@/features/admin/adminServer";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";

export const dynamic = "force-dynamic";

function safeErrorMessage(err: unknown): string {
  if (err instanceof GatewayError) {
    if (err.kind === "not_found") {
      return "Dịch vụ khiếu nại chưa sẵn sàng trên cổng API lúc này. Vui lòng thử lại sau.";
    }
    return SAFE_MESSAGES[err.kind];
  }
  return SAFE_MESSAGES.unavailable;
}

export default async function AdminComplaintsPage() {
  await requireAdminPage();

  let initialComplaints: AdminComplaint[] = [];
  let loadError: string | null = null;

  try {
    const raw = await gw<unknown>("/api/v1/admin/complaints", {
      query: { page: 1, page_size: 100 },
    });
    initialComplaints = normalizeComplaintsListResponse(raw).items;
  } catch (err) {
    loadError = safeErrorMessage(err);
  }

  return <AdminComplaintsClient initialComplaints={initialComplaints} loadError={loadError} />;
}
