import React from "react";
import { ErrorState } from "@/components/app";
import { UiScope } from "@/components/ui/ui-scope";
import { SELLER_ORDERS_PAGE_SIZE, resolveSellerPendingOrdersCount } from "@/lib/a8-types";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import type { Session } from "@/lib/server/session";
import { SellerShell } from "@/features/seller/SellerShell";
import { extractSellerUnreadChatCount } from "@/features/seller/sellerApiTypes";
import {
  loadMyShop,
  loadSellerOrders,
  loadSellerPendingReturnsCount,
  loadSellerShop,
  requireSellerPortalSession,
} from "@/features/seller/sellerServer";
import { loadSellerSiteSummaryServer } from "@/features/seller/site/siteServer";
import { summarizeSellerSite } from "@/features/seller/site/siteSummary";

export const dynamic = "force-dynamic";

async function loadUnreadChatCount(): Promise<number> {
  try {
    const res = await gw<unknown>("/api/v1/seller/chat/conversations", {
      query: { page: 1, page_size: 1 },
    });
    return extractSellerUnreadChatCount(res);
  } catch {
    return 0;
  }
}

export default async function SellerLayout({ children }: { children: React.ReactNode }) {
  let session: Session;
  try {
    session = await requireSellerPortalSession("/seller");
  } catch (err) {
    if (err instanceof GatewayError) {
      return (
        <UiScope className="flex min-h-svh items-center justify-center bg-background p-6 text-sm text-foreground">
          <main className="w-full max-w-md">
            <ErrorState
              title="Chưa thể xác minh phiên người bán"
              description={SAFE_MESSAGES[err.kind]}
              retryHref="/seller"
            />
          </main>
        </UiScope>
      );
    }
    throw err;
  }
  const hasActiveSellerShop = session.user.role === "seller" && Boolean(session.user.shopId);

  if (!hasActiveSellerShop) {
    const myShopRes = await loadMyShop();
    const fallbackSite = summarizeSellerSite(null, null, null);
    return (
      <SellerShell
        user={session.user}
        shop={myShopRes.data}
        pendingOrdersCount={0}
        pendingReturnsCount={0}
        unreadChatCount={0}
        site={fallbackSite}
      >
        {children}
      </SellerShell>
    );
  }

  const [shopRes, ordersRes, pendingReturnsCount, unreadChatCount, site] = await Promise.all([
    loadSellerShop(),
    loadSellerOrders(SELLER_ORDERS_PAGE_SIZE, 1, true),
    loadSellerPendingReturnsCount(),
    loadUnreadChatCount(),
    loadSellerSiteSummaryServer(),
  ]);

  const pendingOrdersCount = resolveSellerPendingOrdersCount(ordersRes.data);

  return (
    <SellerShell
      user={session.user}
      shop={shopRes.data}
      pendingOrdersCount={pendingOrdersCount}
      pendingReturnsCount={pendingReturnsCount}
      unreadChatCount={unreadChatCount}
      site={site}
    >
      {children}
    </SellerShell>
  );
}
