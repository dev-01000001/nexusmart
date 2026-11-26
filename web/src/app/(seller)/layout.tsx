import React from "react";
import { StateView } from "@/components";
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
import styles from "./layout.module.css";

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
        <div className={styles.shell}>
          <main className={styles.canvas}>
            <StateView
              kind="error"
              surface="app"
              title="Chưa thể xác minh phiên người bán"
              message={SAFE_MESSAGES[err.kind]}
              actionLabel="Tải lại"
              actionHref="/seller"
            />
          </main>
        </div>
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
