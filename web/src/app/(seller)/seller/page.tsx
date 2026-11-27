import React from "react";
import { formatVnDateOffset } from "@/lib/format";
import { loadSellerSiteDomainsServer } from "@/lib/server/domains";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerOverviewClient } from "@/features/seller/SellerOverviewClient";
import { SellerShopOnboarding } from "@/features/seller/SellerShopOnboarding";
import {
  loadMyShop,
  loadSellerAddresses,
  loadSellerBalance,
  loadSellerBankAccount,
  loadSellerOrders,
  loadSellerPendingReturnsCount,
  loadSellerProducts,
  loadSellerReport,
  requireSellerPortalSession,
} from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerOverviewPage({
  searchParams,
}: {
  searchParams?: Promise<{ approved?: string }>;
}) {
  const [session, sp] = await Promise.all([
    requireSellerPortalSession("/seller"),
    searchParams ?? Promise.resolve<{ approved?: string }>({}),
  ]);

  const myShopRes = await loadMyShop();
  const shop = myShopRes.data;
  const justApproved = sp?.approved === "1" && shop?.status === "ACTIVE";
  const needsOnboarding =
    justApproved ||
    session.user.role !== "seller" ||
    !session.user.shopId ||
    !shop ||
    shop.status === "PENDING" ||
    shop.status === "REJECTED";

  if (needsOnboarding) {
    const addressesRes = justApproved
      ? { data: [] as Awaited<ReturnType<typeof loadSellerAddresses>>["data"] }
      : await loadSellerAddresses();
    return (
      <SellerSuspense>
        <SellerShopOnboarding
          user={session.user}
          initialShop={shop}
          initialAddresses={addressesRes.data ?? []}
        />
      </SellerSuspense>
    );
  }

  const defaultFrom = formatVnDateOffset(-29);
  const defaultTo = formatVnDateOffset(0);

  const [
    ordersRes,
    productsRes,
    balanceRes,
    reportRes,
    pendingReturnsCount,
    bankAccountRes,
    domainsRes,
  ] = await Promise.all([
    loadSellerOrders(100),
    loadSellerProducts(100),
    loadSellerBalance(),
    loadSellerReport(defaultFrom, defaultTo, 10),
    loadSellerPendingReturnsCount(),
    loadSellerBankAccount(),
    loadSellerSiteDomainsServer(),
  ]);

  const loadError = ordersRes.error ?? productsRes.error;
  const productsTotal = productsRes.data?.total ?? productsRes.data?.items?.length ?? 0;

  return (
    <SellerSuspense>
      <SellerOverviewClient
        initialOrders={ordersRes.data?.items ?? []}
        initialProducts={productsRes.data?.items ?? []}
        productsTotal={productsTotal}
        productsError={productsRes.error}
        initialBalance={balanceRes.data}
        balanceError={balanceRes.error}
        initialBankAccount={bankAccountRes.data}
        bankAccountError={bankAccountRes.error}
        initialDomains={domainsRes.domains}
        domainsError={domainsRes.error}
        initialReport={reportRes.data}
        initialReportError={reportRes.error}
        initialFromDate={defaultFrom}
        initialToDate={defaultTo}
        pendingReturnsCount={pendingReturnsCount}
        loadError={loadError}
      />
    </SellerSuspense>
  );
}
