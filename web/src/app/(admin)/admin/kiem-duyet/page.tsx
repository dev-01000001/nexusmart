import React from "react";
import { notFound } from "next/navigation";
import { roleHasAnyCapability, roleHasCapability } from "@/lib/admin-rbac";
import { gw } from "@/lib/server/gateway";
import { requireSession } from "@/lib/server/session";
import type { GatewayBannedKeyword, PaginatedResponse } from "@/lib/gateway-types";
import type { GwAdminProductExtended } from "@/features/admin/adminApiTypes";
import AdminModerationClient from "./AdminModerationClient";
import {
  ADMIN_MODERATION_PAGE_SIZE,
  PRODUCT_MODERATION_STATUSES,
  createEmptyProductTabCounts,
  type ProductTabCounts,
} from "./moderationHelpers";

export const dynamic = "force-dynamic";

export default async function AdminModerationPage() {
  const session = await requireSession("/admin/kiem-duyet");
  if (!roleHasAnyCapability(session.user.role, ["products.read", "banned_keywords.read"])) {
    notFound();
  }

  const canReadProducts = roleHasCapability(session.user.role, "products.read");
  const canReadBanned = roleHasCapability(session.user.role, "banned_keywords.read");

  const initialTabCounts: ProductTabCounts = createEmptyProductTabCounts();

  const [productsOutcome, statusCountsOutcomes, bannedOutcome] = await Promise.all([
    canReadProducts
      ? gw<PaginatedResponse<GwAdminProductExtended>>("/api/v1/admin/products", {
          query: { page: 1, page_size: ADMIN_MODERATION_PAGE_SIZE },
        })
          .then((res) => ({
            items: res.items ?? [],
            total: typeof res.total === "number" ? res.total : (res.items ?? []).length,
            error: null as string | null,
          }))
          .catch(() => ({
            items: [] as GwAdminProductExtended[],
            total: 0,
            error: "Không thể tải danh sách sản phẩm kiểm duyệt lúc này.",
          }))
      : Promise.resolve({
          items: [] as GwAdminProductExtended[],
          total: 0,
          error: null as string | null,
        }),
    canReadProducts
      ? Promise.allSettled(
          PRODUCT_MODERATION_STATUSES.map((status) =>
            gw<PaginatedResponse<GwAdminProductExtended>>("/api/v1/admin/products", {
              query: { status, page: 1, page_size: 1 },
            })
          )
        )
      : Promise.resolve([]),
    canReadBanned
      ? gw<PaginatedResponse<GatewayBannedKeyword>>("/api/v1/admin/banned-keywords", {
          query: { page: 1, page_size: 1 },
        })
          .then((res) => res.total ?? 0)
          .catch(() => 0)
      : Promise.resolve(0),
  ]);

  initialTabCounts.all = Math.max(0, Math.trunc(productsOutcome.total));
  statusCountsOutcomes.forEach((outcome, idx) => {
    const statusKey = PRODUCT_MODERATION_STATUSES[idx];
    if (outcome.status === "fulfilled" && typeof outcome.value?.total === "number") {
      initialTabCounts[statusKey] = Math.max(0, Math.trunc(outcome.value.total));
    }
  });

  return (
    <AdminModerationClient
      initialProducts={productsOutcome.items}
      initialTotal={productsOutcome.total}
      initialTabCounts={initialTabCounts}
      initialBannedCount={bannedOutcome}
      loadError={productsOutcome.error}
    />
  );
}
