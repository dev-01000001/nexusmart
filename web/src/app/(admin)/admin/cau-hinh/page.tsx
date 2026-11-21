import React from "react";
import { AdminConfigClient } from "@/features/admin/AdminConfigClient";
import type {
  AdminCategory,
  AdminUser,
  AdminVoucher,
} from "@/features/admin/adminComplaintsTypes";
import { requireAdminPage } from "@/features/admin/adminServer";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";

export const dynamic = "force-dynamic";

function safeLoadError(err: unknown): string {
  if (err instanceof GatewayError) {
    return SAFE_MESSAGES[err.kind];
  }
  return SAFE_MESSAGES.unavailable;
}

export default async function AdminConfigPage() {
  await requireAdminPage();

  const [categoriesResult, vouchersResult, usersResult] = await Promise.all([
    gw<{ categories: AdminCategory[] }>("/api/v1/categories")
      .then((res) => ({ data: res.categories ?? [], error: null as string | null }))
      .catch((err) => ({ data: [] as AdminCategory[], error: safeLoadError(err) })),

    gw<{ vouchers: AdminVoucher[] }>("/api/v1/vouchers")
      .then((res) => ({
        data: (res.vouchers ?? []).filter((v) => {
          const scope = (v.scope ?? "").toUpperCase();
          return scope === "PLATFORM" || scope === "FREESHIP";
        }),
        error: null as string | null,
      }))
      .catch((err) => ({ data: [] as AdminVoucher[], error: safeLoadError(err) })),

    gw<{ items: AdminUser[]; total: number }>("/api/v1/admin/users", {
      query: { page: 1, page_size: 100 },
    })
      .then((res) => ({
        data: res.items ?? [],
        total: res.total ?? (res.items ?? []).length,
        error: null as string | null,
      }))
      .catch((err) => ({ data: [] as AdminUser[], total: 0, error: safeLoadError(err) })),
  ]);

  return (
    <AdminConfigClient
      initialCategories={categoriesResult.data}
      categoriesError={categoriesResult.error}
      initialVouchers={vouchersResult.data}
      vouchersError={vouchersResult.error}
      initialUsers={usersResult.data}
      usersTotal={usersResult.total}
      usersError={usersResult.error}
    />
  );
}
