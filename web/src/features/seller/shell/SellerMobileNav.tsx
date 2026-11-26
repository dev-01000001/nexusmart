"use client";

import * as React from "react";
import Link from "next/link";
import { usePathname, useSearchParams } from "next/navigation";
import { Home, Menu, MessageSquare, Package, ShoppingBag, type LucideIcon } from "lucide-react";
import { resolveActiveNav, type AppNavGroup } from "@/components/app";
import { useSidebar } from "@/components/ui/sidebar";
import { cn } from "@/lib/utils";

interface MobilePrimaryTab {
  href: string;
  label: string;
  icon: LucideIcon;
  badge?: number;
}

const MOBILE_PRIMARY_HREFS = new Set([
  "/seller",
  "/seller/don-hang",
  "/seller/san-pham",
  "/seller/chat",
]);

export interface SellerMobileNavProps {
  navGroups: AppNavGroup[];
  pendingOrdersCount: number;
  pendingReturnsCount: number;
  unreadChatCount: number;
}

function formatMobileBadge(count: number): string {
  return count > 99 ? "99+" : String(count);
}

export function SellerMobileNav({
  navGroups,
  pendingOrdersCount,
  pendingReturnsCount,
  unreadChatCount,
}: SellerMobileNavProps) {
  const pathname = usePathname() || "/seller";
  const searchParams = useSearchParams();
  const rawSearch = searchParams.toString();
  const search = rawSearch ? `?${rawSearch}` : "";
  const { openMobile, setOpenMobile } = useSidebar();

  const allNavItems = React.useMemo(() => navGroups.flatMap((g) => g.items), [navGroups]);
  const { activeHref } = React.useMemo(
    () => resolveActiveNav(allNavItems, pathname, search, "/seller"),
    [allNavItems, pathname, search]
  );

  const primaryTabs: MobilePrimaryTab[] = [
    { href: "/seller", label: "Tổng quan", icon: Home },
    {
      href: "/seller/don-hang",
      label: "Đơn hàng",
      icon: ShoppingBag,
      badge: pendingOrdersCount > 0 ? pendingOrdersCount : undefined,
    },
    { href: "/seller/san-pham", label: "Sản phẩm", icon: Package },
    {
      href: "/seller/chat",
      label: "Tin nhắn",
      icon: MessageSquare,
      badge: unreadChatCount > 0 ? unreadChatCount : undefined,
    },
  ];

  const isOverflowActive =
    openMobile || (activeHref !== null && !MOBILE_PRIMARY_HREFS.has(activeHref));

  return (
    <nav aria-label="Điều hướng nhanh di động" className="grid grid-cols-5">
      {primaryTabs.map((tab) => {
        const Icon = tab.icon;
        const active = activeHref === tab.href;
        return (
          <Link
            key={tab.href}
            href={tab.href}
            aria-current={active ? "page" : undefined}
            onClick={() => setOpenMobile(false)}
            className={cn(
              "flex min-h-14 flex-col items-center justify-center gap-0.5 px-1 py-1.5 text-[11px] font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              active ? "font-semibold text-brand" : "text-muted-foreground hover:text-foreground"
            )}
          >
            <span className="relative inline-flex items-center justify-center">
              <Icon className="size-4 shrink-0" aria-hidden="true" />
              {typeof tab.badge === "number" && tab.badge > 0 ? (
                <span className="absolute -top-1.5 -right-2.5 inline-flex min-w-4 items-center justify-center rounded-full bg-brand px-1 text-[10px] leading-4 font-semibold text-brand-foreground tabular-nums">
                  {formatMobileBadge(tab.badge)}
                </span>
              ) : null}
            </span>
            <span className="max-w-full truncate">{tab.label}</span>
          </Link>
        );
      })}

      <button
        type="button"
        onClick={() => setOpenMobile(!openMobile)}
        aria-expanded={openMobile}
        aria-label="Mở thêm mục quản lý"
        className={cn(
          "flex min-h-14 flex-col items-center justify-center gap-0.5 px-1 py-1.5 text-[11px] font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          isOverflowActive
            ? "font-semibold text-brand"
            : "text-muted-foreground hover:text-foreground"
        )}
      >
        <span className="relative inline-flex items-center justify-center">
          <Menu className="size-4 shrink-0" aria-hidden="true" />
          {pendingReturnsCount > 0 ? (
            <span className="absolute -top-1.5 -right-2.5 inline-flex min-w-4 items-center justify-center rounded-full bg-brand px-1 text-[10px] leading-4 font-semibold text-brand-foreground tabular-nums">
              {formatMobileBadge(pendingReturnsCount)}
            </span>
          ) : null}
        </span>
        <span className="max-w-full truncate">Thêm</span>
      </button>
    </nav>
  );
}
