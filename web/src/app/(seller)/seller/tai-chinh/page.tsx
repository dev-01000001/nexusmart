import React from "react";
import type { GatewaySellerPayoutListResponse } from "@/lib/gateway-types";
import { gw } from "@/lib/server/gateway";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerFinanceClient } from "@/features/seller/SellerFinanceClient";
import {
  loadSellerBalance,
  loadSellerBankAccount,
  loadSellerOrders,
  requireSellerPage,
} from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerFinancePage() {
  await requireSellerPage();

  const [balanceRes, bankRes, ordersRes, payoutsRes] = await Promise.all([
    loadSellerBalance(),
    loadSellerBankAccount(),
    loadSellerOrders(100),
    gw<GatewaySellerPayoutListResponse>("/api/v1/seller/payouts", {
      query: { page: 1, page_size: 20 },
    })
      .then((data) => ({ data, error: null as string | null }))
      .catch(() => ({
        data: null as GatewaySellerPayoutListResponse | null,
        error: "Không thể tải lịch sử rút tiền lúc này.",
      })),
  ]);

  return (
    <SellerSuspense>
      <SellerFinanceClient
        initialBalance={balanceRes.data}
        balanceError={balanceRes.error}
        initialBankAccount={bankRes.data}
        initialOrders={ordersRes.data?.items ?? []}
        initialPayouts={payoutsRes.data?.items ?? []}
        initialPayoutsTotal={payoutsRes.data?.total ?? 0}
        payoutsLoadError={payoutsRes.error}
        loadError={ordersRes.error}
      />
    </SellerSuspense>
  );
}
