import React from "react";
import { notFound } from "next/navigation";
import { StateView } from "@/components";
import { ADMIN_STAFF_ROLES, canAccessAdminPath, roleHasCapability } from "@/lib/admin-rbac";
import type { GatewayShop, PaginatedResponse } from "@/lib/gateway-types";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { requireRole, type Session } from "@/lib/server/session";
import { AdminShopsClient } from "@/features/admin/AdminShopsClient";
import shared from "@/features/admin/adminShared.module.css";

export const dynamic = "force-dynamic";

function safeLoadError(err: unknown): string {
  if (err instanceof GatewayError) {
    return SAFE_MESSAGES[err.kind];
  }
  return SAFE_MESSAGES.unavailable;
}

export default async function AdminShopApprovalsPage() {
  let session: Session;
  try {
    session = await requireRole(...ADMIN_STAFF_ROLES);
  } catch (err) {
    if (err instanceof GatewayError) {
      return (
        <div className={shared.page}>
          <header className={shared.titleRow}>
            <div className={shared.titleBlock}>
              <h1 className={shared.pageTitle}>Gian hàng</h1>
            </div>
          </header>
          <div className={shared.stateWrap}>
            <StateView
              kind="error"
              surface="app"
              title="Chưa thể xác minh phiên quản trị"
              message={SAFE_MESSAGES[err.kind]}
              actionLabel="Tải lại"
              actionHref="/admin/gian-hang"
            />
          </div>
        </div>
      );
    }
    throw err;
  }
  if (!canAccessAdminPath(session.user.role, "/admin/gian-hang")) {
    notFound();
  }

  const canReadBank = roleHasCapability(session.user.role, "shops.bank.read");

  const [allRes, bankRes] = await Promise.all([
    gw<PaginatedResponse<GatewayShop>>("/api/v1/admin/shops", {
      query: { page: 1, page_size: 100 },
    })
      .then((res) => ({ items: res.items ?? [], error: null as string | null }))
      .catch((err) => ({ items: [] as GatewayShop[], error: safeLoadError(err) })),
    canReadBank
      ? gw<PaginatedResponse<GatewayShop>>("/api/v1/admin/shops", {
          query: { bank_review_pending: "true", page: 1, page_size: 100 },
        })
          .then((res) => ({
            items: res.items ?? [],
            total: res.total ?? (res.items ?? []).length,
          }))
          .catch(() => ({ items: [] as GatewayShop[], total: 0 }))
      : Promise.resolve({ items: [] as GatewayShop[], total: 0 }),
  ]);

  return (
    <AdminShopsClient
      initialShops={allRes.items}
      initialBankPendingShops={bankRes.items}
      initialBankPendingTotal={bankRes.total}
      loadError={allRes.error}
    />
  );
}
