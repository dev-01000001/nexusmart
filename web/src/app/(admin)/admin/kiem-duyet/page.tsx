import React from "react";
import { notFound } from "next/navigation";
import { roleHasAnyCapability, roleHasCapability } from "@/lib/admin-rbac";
import { gw } from "@/lib/server/gateway";
import { requireSession } from "@/lib/server/session";
import type { GatewayBannedKeyword, PaginatedResponse } from "@/lib/gateway-types";
import type { GwAdminProductExtended } from "@/features/admin/adminApiTypes";
import AdminModerationClient from "./AdminModerationClient";

export const dynamic = "force-dynamic";

export default async function AdminModerationPage() {
  const session = await requireSession("/admin/kiem-duyet");
  if (!roleHasAnyCapability(session.user.role, ["products.read", "banned_keywords.read"])) {
    notFound();
  }

  const canReadProducts = roleHasCapability(session.user.role, "products.read");
  const canReadBanned = roleHasCapability(session.user.role, "banned_keywords.read");

  let initialProducts: GwAdminProductExtended[] = [];
  let loadError: string | null = null;
  let initialBannedCount = 0;

  const [productsOutcome, bannedOutcome] = await Promise.all([
    canReadProducts
      ? gw<PaginatedResponse<GwAdminProductExtended>>("/api/v1/admin/products", {
          query: { page: 1, page_size: 100 },
        })
          .then((res) => ({ items: res.items ?? [], error: null as string | null }))
          .catch(() => ({
            items: [] as GwAdminProductExtended[],
            error: "Không thể tải danh sách sản phẩm kiểm duyệt lúc này.",
          }))
      : Promise.resolve({ items: [] as GwAdminProductExtended[], error: null as string | null }),
    canReadBanned
      ? gw<PaginatedResponse<GatewayBannedKeyword>>("/api/v1/admin/banned-keywords", {
          query: { page: 1, page_size: 1 },
        })
          .then((res) => res.total ?? 0)
          .catch(() => 0)
      : Promise.resolve(0),
  ]);

  initialProducts = productsOutcome.items;
  loadError = productsOutcome.error;
  initialBannedCount = bannedOutcome;

  return (
    <AdminModerationClient
      initialProducts={initialProducts}
      initialBannedCount={initialBannedCount}
      loadError={loadError}
    />
  );
}
