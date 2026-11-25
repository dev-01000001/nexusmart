import React from "react";
import { notFound } from "next/navigation";
import { roleHasAnyCapability, roleHasCapability } from "@/lib/admin-rbac";
import { gw } from "@/lib/server/gateway";
import { requireSession } from "@/lib/server/session";
import type { GatewayCategory, GatewayVoucher, PaginatedResponse } from "@/lib/gateway-types";
import type { GwAdminFlashSaleExtended, GwAdminShop } from "@/features/admin/adminApiTypes";
import AdminFlashSaleClient from "./AdminFlashSaleClient";

export const dynamic = "force-dynamic";

export default async function AdminFlashSalePage() {
  const session = await requireSession("/admin/flash-sale");
  const role = session.user.role;
  if (!roleHasAnyCapability(role, ["flash_sales.read", "flash_sales.items.approve"])) {
    notFound();
  }

  const canReadFlashSales = roleHasCapability(role, "flash_sales.read");
  const canReadShops = roleHasCapability(role, "shops.read");
  const canReadVouchers = roleHasCapability(role, "vouchers.read");

  const [flashSalesRes, shopsRes, categoriesRes, vouchersRes] = await Promise.all([
    canReadFlashSales
      ? gw<PaginatedResponse<GwAdminFlashSaleExtended>>("/api/v1/admin/flash-sales", {
          query: { page: 1, page_size: 50 },
        })
          .then((d) => ({ items: d.items ?? [], error: null as string | null }))
          .catch(() => ({
            items: [] as GwAdminFlashSaleExtended[],
            error: "Không thể tải danh sách khung giờ Flash sale lúc này.",
          }))
      : Promise.resolve({ items: [] as GwAdminFlashSaleExtended[], error: null as string | null }),
    canReadShops
      ? gw<PaginatedResponse<GwAdminShop>>("/api/v1/admin/shops", {
          query: { page: 1, page_size: 100 },
        }).catch(() => null)
      : Promise.resolve(null),
    gw<{ categories?: GatewayCategory[] }>("/api/v1/categories", { auth: "none" }).catch(
      () => null
    ),
    canReadVouchers
      ? gw<{ vouchers?: GatewayVoucher[] }>("/api/v1/admin/vouchers", {
          query: { include_inactive: "true" },
        }).catch(() => null)
      : Promise.resolve(null),
  ]);

  const shopNames: Record<string, string> = {};
  for (const shop of shopsRes?.items ?? []) {
    shopNames[shop.id] = shop.name;
  }

  return (
    <AdminFlashSaleClient
      initialFlashSales={flashSalesRes.items}
      shopNames={shopNames}
      categories={categoriesRes?.categories ?? []}
      initialVouchers={vouchersRes?.vouchers ?? []}
      loadError={flashSalesRes.error}
      serverNowMs={Date.now()}
    />
  );
}
