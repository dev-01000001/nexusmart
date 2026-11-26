"use client";

import React, { useEffect, useRef, useState } from "react";
import dynamic from "next/dynamic";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Menu, Search, ShoppingBag } from "lucide-react";
import { useSession } from "@/components/SessionProvider";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useStore } from "@/context/StoreContext";
import { cn } from "@/lib/utils";
import { SITE_NAV_ITEMS, stripInternalSitePrefix, type SiteNavKey } from "./siteNav";

export { SITE_NAV_ITEMS, stripInternalSitePrefix, type SiteNavKey };

const SiteMobileDrawer = dynamic(
  () => import("./shell/SiteMobileDrawer").then((mod) => mod.SiteMobileDrawer),
  { ssr: false }
);

const SiteSearchModal = dynamic(
  () => import("./shell/SiteSearchModal").then((mod) => mod.SiteSearchModal),
  { ssr: false }
);

/**
 * Visible pathname on a site host. The middleware rewrites `/<path>` to `/s/<host>/<path>`;
 * strip that internal prefix if the router ever exposes it.
 */
export function useSitePathname(): string {
  return stripInternalSitePrefix(usePathname());
}

function isActive(pathname: string, href: string): boolean {
  if (href === "/") return pathname === "/";
  return pathname === href || pathname.startsWith(`${href}/`);
}

export function SiteNav({ previewMode = false }: { previewMode?: boolean }) {
  const pathname = useSitePathname();
  return (
    <nav
      className={cn(
        "items-center gap-x-5 gap-y-1.5",
        previewMode ? "flex flex-wrap" : "hidden shrink-0 flex-nowrap lg:flex"
      )}
      aria-label="Điều hướng cửa hàng"
    >
      {SITE_NAV_ITEMS.map((item) => {
        const active = !previewMode && isActive(pathname, item.href);
        const linkCls = cn(
          "relative inline-flex items-center py-1.5 text-[15px] font-medium whitespace-nowrap transition-colors",
          active
            ? "text-brand after:scale-x-100"
            : "text-foreground hover:text-brand after:scale-x-0 hover:after:scale-x-100",
          "after:absolute after:inset-x-0 after:bottom-0.5 after:h-0.5 after:origin-left after:bg-brand after:transition-transform after:duration-200"
        );
        if (previewMode) {
          return (
            <span key={item.key} className={linkCls}>
              {item.label}
            </span>
          );
        }
        return (
          <Link
            key={item.key}
            href={item.href}
            className={linkCls}
            aria-current={active ? "page" : undefined}
          >
            {item.label}
          </Link>
        );
      })}
    </nav>
  );
}

export interface SiteHeaderActionsProps {
  brandName?: string;
  shopId?: string;
  /** When true (default), renders Swarm G `SiteSearchBar` inside search modal; when false, plain form. */
  showSearchSlot?: boolean;
}

/**
 * Header actions: Search button (lazy Dialog), authenticated account/order links,
 * Cart button with live count badge, and mobile left Sheet (lazy-loaded via next/dynamic).
 */
export function SiteHeaderActions({
  brandName = "Cửa hàng",
  shopId,
  showSearchSlot = true,
}: SiteHeaderActionsProps) {
  const pathname = useSitePathname();
  const { user, isAuthenticated } = useSession();
  const { cartCount } = useStore();

  const [drawerOpen, setDrawerOpen] = useState(false);
  const [drawerMounted, setDrawerMounted] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const [searchMounted, setSearchMounted] = useState(false);
  const [badgePop, setBadgePop] = useState(false);

  const prevCartRef = useRef<number>(cartCount);

  const loginHref =
    pathname === "/" || pathname.startsWith("/dang-")
      ? "/dang-nhap"
      : `/dang-nhap?next=${encodeURIComponent(pathname)}`;

  useEffect(() => {
    if (cartCount > prevCartRef.current) {
      setBadgePop(true);
      const timer = window.setTimeout(() => setBadgePop(false), 260);
      prevCartRef.current = cartCount;
      return () => window.clearTimeout(timer);
    }
    prevCartRef.current = cartCount;
  }, [cartCount]);

  useEffect(() => {
    setDrawerOpen(false);
    setSearchOpen(false);
  }, [pathname]);

  const handleOpenSearch = () => {
    setSearchMounted(true);
    setSearchOpen(true);
  };

  const handleOpenDrawer = () => {
    setDrawerMounted(true);
    setDrawerOpen(true);
  };

  const cartBadge =
    cartCount > 0 ? (
      <Badge
        variant="secondary"
        className={cn(
          "h-5 min-w-5 rounded-full bg-primary-foreground px-1.5 text-xs font-bold text-primary tabular-nums transition-transform duration-200",
          badgePop && "scale-110"
        )}
      >
        {cartCount}
      </Badge>
    ) : null;

  return (
    <>
      <div className="hidden shrink-0 items-center gap-3 lg:flex">
        <Button
          type="button"
          variant="ghost"
          size="icon"
          onClick={handleOpenSearch}
          aria-expanded={searchOpen}
          aria-label={`Tìm kiếm tại ${brandName}`}
          className="size-10 text-foreground hover:bg-accent hover:text-brand"
        >
          <Search className="size-[18px]" aria-hidden="true" />
        </Button>

        {isAuthenticated && user ? (
          <>
            <Link
              href="/don-hang"
              className="inline-flex min-h-10 items-center px-1 text-sm font-medium whitespace-nowrap text-foreground transition-colors hover:text-brand"
            >
              Đơn hàng
            </Link>
            <Link
              href="/tai-khoan"
              className="inline-flex min-h-10 items-center px-1 text-sm font-medium whitespace-nowrap text-foreground transition-colors hover:text-brand"
            >
              Tài khoản
            </Link>
          </>
        ) : (
          <Link
            href={loginHref}
            className="inline-flex min-h-10 items-center px-1 text-sm font-medium whitespace-nowrap text-foreground transition-colors hover:text-brand"
          >
            Đăng nhập
          </Link>
        )}

        <Button
          asChild
          size="default"
          className="h-10 gap-2 px-3.5 font-semibold whitespace-nowrap shadow-none"
        >
          <Link
            href="/gio-hang"
            aria-label={cartCount > 0 ? `Giỏ hàng, ${cartCount} sản phẩm` : "Giỏ hàng"}
          >
            <ShoppingBag className="size-[18px]" aria-hidden="true" />
            <span>Giỏ</span>
            {cartBadge}
          </Link>
        </Button>
      </div>

      <div className="flex shrink-0 items-center gap-1.5 lg:hidden">
        <Button
          type="button"
          variant="ghost"
          size="icon"
          onClick={handleOpenSearch}
          aria-expanded={searchOpen}
          aria-label={`Tìm kiếm tại ${brandName}`}
          className="size-11 text-foreground hover:bg-accent hover:text-brand"
        >
          <Search className="size-[18px]" aria-hidden="true" />
        </Button>

        <Button asChild size="default" className="h-11 min-w-11 gap-1.5 px-2.5 shadow-none">
          <Link
            href="/gio-hang"
            aria-label={cartCount > 0 ? `Giỏ hàng, ${cartCount} sản phẩm` : "Giỏ hàng"}
          >
            <ShoppingBag className="size-[18px]" aria-hidden="true" />
            {cartBadge}
          </Link>
        </Button>

        <Button
          type="button"
          variant="secondary"
          size="icon"
          onClick={handleOpenDrawer}
          aria-expanded={drawerOpen}
          aria-label="Mở trình đơn cửa hàng"
          className="size-11 shadow-none"
        >
          <Menu className="size-[18px]" aria-hidden="true" />
        </Button>
      </div>

      {searchMounted && (
        <SiteSearchModal
          open={searchOpen}
          onOpenChange={setSearchOpen}
          brandName={brandName}
          shopId={shopId}
          showSearchSlot={showSearchSlot}
        />
      )}

      {drawerMounted && (
        <SiteMobileDrawer
          open={drawerOpen}
          onOpenChange={setDrawerOpen}
          brandName={brandName}
          pathname={pathname}
          isAuthenticated={Boolean(isAuthenticated && user)}
          loginHref={loginHref}
        />
      )}
    </>
  );
}

/**
 * Compatibility export: store toasts are now bridged into the lazy-loaded Sonner
 * `<SiteToaster />` mounted in `s/[host]/layout.tsx`.
 */
export function SiteToast() {
  return null;
}
