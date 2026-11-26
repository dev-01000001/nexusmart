import React from "react";
import { notFound } from "next/navigation";
import { ErrorState, PageHeader } from "@/components/app";
import { ADMIN_STAFF_ROLES, canAccessAdminPath, roleHasCapability } from "@/lib/admin-rbac";
import type { GatewayCategory, GatewayVoucher } from "@/lib/gateway-types";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { requireRole, type Session } from "@/lib/server/session";
import { AdminConfigClient } from "@/features/admin/AdminConfigClient";

export const dynamic = "force-dynamic";

function safeLoadError(err: unknown): string {
  if (err instanceof GatewayError) {
    return SAFE_MESSAGES[err.kind];
  }
  return SAFE_MESSAGES.unavailable;
}

export default async function AdminConfigPage() {
  let session: Session;
  try {
    session = await requireRole(...ADMIN_STAFF_ROLES);
  } catch (err) {
    if (err instanceof GatewayError) {
      return (
        <div className="space-y-5">
          <PageHeader title="Cấu hình sàn" />
          <ErrorState
            title="Chưa thể xác minh phiên quản trị"
            description={SAFE_MESSAGES[err.kind]}
            retryHref="/admin/cau-hinh"
          />
        </div>
      );
    }
    throw err;
  }
  if (!canAccessAdminPath(session.user.role, "/admin/cau-hinh")) {
    notFound();
  }

  const role = session.user.role;
  const canReadCategories = roleHasCapability(role, "categories.read");
  const canReadVouchers = roleHasCapability(role, "vouchers.read");

  // Zero-403: only preload sections the verified staff role has capability to read.
  const [categoriesResult, vouchersResult] = await Promise.all([
    canReadCategories
      ? gw<{ categories: GatewayCategory[] }>("/api/v1/admin/categories")
          .then((res) => ({ data: res.categories ?? [], error: null as string | null }))
          .catch((err) => ({ data: [] as GatewayCategory[], error: safeLoadError(err) }))
      : Promise.resolve({ data: [] as GatewayCategory[], error: null as string | null }),
    canReadVouchers
      ? gw<{ vouchers: GatewayVoucher[] }>("/api/v1/admin/vouchers")
          .then((res) => ({
            data: (res.vouchers ?? []).filter((v) => {
              const scope = (v.scope ?? "").toUpperCase();
              return scope === "PLATFORM" || scope === "FREESHIP";
            }),
            error: null as string | null,
          }))
          .catch((err) => ({ data: [] as GatewayVoucher[], error: safeLoadError(err) }))
      : Promise.resolve({ data: [] as GatewayVoucher[], error: null as string | null }),
  ]);

  return (
    <AdminConfigClient
      initialCategories={categoriesResult.data}
      categoriesError={categoriesResult.error}
      initialVouchers={vouchersResult.data}
      vouchersError={vouchersResult.error}
    />
  );
}
