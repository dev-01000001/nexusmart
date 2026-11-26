"use client";

import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { AlertCircle, AlertTriangle } from "lucide-react";
import { toast } from "sonner";
import { AppShell, type AppAccount } from "@/components/app";
import { useSession } from "@/components/SessionProvider";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Toaster } from "@/components/ui/sonner";
import { formatDateTimeVi } from "@/lib/format";
import type { SessionUser } from "@/lib/server/session";
import {
  getShopStatusLabel,
  resolvePresentationShopKey,
  type GwSellerShop,
} from "./sellerApiTypes";
import {
  SELLER_CHAT_READ_EVENT,
  applySellerChatReadDelta,
  extractSellerChatReadDelta,
} from "./sellerReturns";
import { isSellerRouteAllowedWhenSuspended } from "./sellerValidation";
import { SellerMobileNav } from "./shell/SellerMobileNav";
import {
  SELLER_SIDEBAR_STORAGE_KEY,
  buildSellerCommandItems,
  buildSellerNavGroups,
} from "./shell/sellerNav";
import type { SellerSiteSummary } from "./site/siteSummary";

interface SellerContextValue {
  user: SessionUser;
  shop: GwSellerShop | null;
  presentationShopKey: string;
  site: SellerSiteSummary;
  showToast: (message: string) => void;
}

const SellerContext = createContext<SellerContextValue | null>(null);

export function useSellerContext(): SellerContextValue {
  const ctx = useContext(SellerContext);
  if (!ctx) {
    throw new Error("useSellerContext phải được gọi bên trong SellerShell.");
  }
  return ctx;
}

export interface SellerShellProps {
  user: SessionUser;
  shop: GwSellerShop | null;
  pendingOrdersCount: number;
  pendingReturnsCount?: number;
  unreadChatCount?: number;
  site: SellerSiteSummary;
  children: React.ReactNode;
}

export function SellerShell({
  user,
  shop,
  pendingOrdersCount,
  pendingReturnsCount = 0,
  unreadChatCount = 0,
  site,
  children,
}: SellerShellProps) {
  const pathname = usePathname() || "/seller";
  const { logout } = useSession();

  const [liveUnreadChatCount, setLiveUnreadChatCount] = useState<number>(() =>
    Math.max(0, Math.trunc(unreadChatCount))
  );

  // Fresh SSR unreadChatCount from layout overrides any client-side delta
  useEffect(() => {
    setLiveUnreadChatCount(Math.max(0, Math.trunc(unreadChatCount)));
  }, [unreadChatCount]);

  useEffect(() => {
    const onChatRead = (event: Event) => {
      const readCount = extractSellerChatReadDelta((event as CustomEvent).detail);
      if (readCount <= 0) return;
      setLiveUnreadChatCount((prev) => applySellerChatReadDelta(prev, readCount));
    };
    window.addEventListener(SELLER_CHAT_READ_EVENT, onChatRead);
    return () => window.removeEventListener(SELLER_CHAT_READ_EVENT, onChatRead);
  }, []);

  const presentationShopKey = resolvePresentationShopKey(shop?.name);

  const showToast = useCallback((message: string) => {
    toast(message);
  }, []);

  const hasOperationalShop =
    user.role === "seller" &&
    Boolean(user.shopId) &&
    Boolean(shop) &&
    (shop?.status === "ACTIVE" || shop?.status === "SUSPENDED");
  const isSuspended = shop?.status === "SUSPENDED";
  const blockedBySuspension = isSuspended && !isSellerRouteAllowedWhenSuspended(pathname);

  const navGroups = useMemo(
    () =>
      buildSellerNavGroups({
        hasOperationalShop,
        pendingOrdersCount,
        pendingReturnsCount,
        unreadChatCount: liveUnreadChatCount,
        site,
      }),
    [hasOperationalShop, pendingOrdersCount, pendingReturnsCount, liveUnreadChatCount, site]
  );

  const commandItems = useMemo(
    () => buildSellerCommandItems({ hasOperationalShop }),
    [hasOperationalShop]
  );

  const shopDisplayName = shop?.name || "Gian hàng mới";
  const shopStatus = shop ? getShopStatusLabel(shop.status) : null;
  const userName = user.fullName || user.email;

  const account = useMemo<AppAccount>(() => {
    const menu: AppAccount["menu"] = [];
    if (hasOperationalShop) {
      if (site.url) {
        menu.push({
          label: `Xem site (${site.host})`,
          onSelect: () => {
            window.open(site.url, "_blank", "noopener,noreferrer");
          },
        });
      } else {
        menu.push({
          label: site.state === "suspended" ? "Site tạm khóa" : "Mở bán site",
          href: "/seller/site",
        });
      }
      menu.push({ label: "Hồ sơ gian hàng", href: "/seller/ho-so" });
    }
    menu.push({ label: "Về trang chủ NexusMart", href: "/" });
    menu.push({
      label: "Đăng xuất",
      destructive: true,
      onSelect: () => {
        void logout("/dang-nhap");
      },
    });

    const subtitleParts = [shopStatus?.label, site.host || userName].filter(Boolean);
    return {
      name: shop ? shopDisplayName : userName,
      subtitle: subtitleParts.join(" · ") || user.email,
      menu,
    };
  }, [
    hasOperationalShop,
    site.url,
    site.host,
    site.state,
    shop,
    shopDisplayName,
    shopStatus?.label,
    userName,
    user.email,
    logout,
  ]);

  const contextValue = useMemo<SellerContextValue>(
    () => ({ user, shop, presentationShopKey, site, showToast }),
    [user, shop, presentationShopKey, site, showToast]
  );

  return (
    <SellerContext.Provider value={contextValue}>
      <AppShell
        area="seller"
        areaLabel="Người bán"
        homeHref="/seller"
        navGroups={navGroups}
        commandItems={commandItems}
        account={account}
        storageKey={SELLER_SIDEBAR_STORAGE_KEY}
        mobileNav={
          hasOperationalShop ? (
            <SellerMobileNav
              navGroups={navGroups}
              pendingOrdersCount={pendingOrdersCount}
              pendingReturnsCount={pendingReturnsCount}
              unreadChatCount={liveUnreadChatCount}
            />
          ) : undefined
        }
      >
        {isSuspended && shop ? (
          <Alert variant="destructive" className="mb-4">
            <AlertTriangle className="size-4" aria-hidden="true" />
            <AlertTitle>Gian hàng đang tạm ngưng hoạt động</AlertTitle>
            <AlertDescription>
              {shop.suspended_reason ? <p>Lý do: {shop.suspended_reason}</p> : null}
              <p>
                {shop.suspended_at
                  ? `Thời điểm tạm ngưng: ${formatDateTimeVi(shop.suspended_at)} · `
                  : ""}
                Trong thời gian tạm ngưng, bạn vẫn có thể xử lý đơn hàng, trả hàng và theo dõi tài
                chính.
              </p>
            </AlertDescription>
          </Alert>
        ) : null}

        {blockedBySuspension ? (
          <div
            role="alert"
            className="flex flex-col items-center justify-center rounded-xl bg-card px-6 py-10 text-center shadow-card"
          >
            <div className="mb-3 flex size-10 items-center justify-center rounded-full bg-badge-critical-bg text-badge-critical-ink">
              <AlertCircle className="size-5" aria-hidden="true" />
            </div>
            <h1 className="text-base font-semibold text-foreground">
              Chức năng tạm khóa do gian hàng đang tạm ngưng
            </h1>
            <p className="mt-1.5 max-w-md text-[13px] leading-[18px] text-muted-foreground">
              Gian hàng hiện chỉ được phép truy cập Tổng quan, Đơn hàng, Trả hàng và Tài chính để
              hoàn tất các đơn đang phát sinh.
            </p>
            <div className="mt-4 flex flex-wrap items-center justify-center gap-2">
              <Button size="sm" asChild>
                <Link href="/seller/don-hang">Về đơn hàng</Link>
              </Button>
              <Button size="sm" variant="outline" asChild>
                <Link href="/seller/tra-hang">Xem trả hàng</Link>
              </Button>
            </div>
          </div>
        ) : (
          children
        )}

        <Toaster />
      </AppShell>
    </SellerContext.Provider>
  );
}
