"use client";

import React, { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { ChevronDown, LogOut, Menu, Shield, Store, User, X } from "lucide-react";
import { NexusMark } from "@/components/Icons";
import { useSession } from "@/components/SessionProvider";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { UiScope } from "@/components/ui/ui-scope";
import { cn } from "@/lib/utils";

interface NavItem {
  label: string;
  href: string;
  matchPath?: string;
}

const NAV_ITEMS: readonly NavItem[] = [
  { label: "Cửa hàng", href: "/#danh-ba-gian-hang" },
  { label: "Về NexusMart", href: "/ve-nexusmart", matchPath: "/ve-nexusmart" },
  { label: "Trợ giúp", href: "/tro-giup", matchPath: "/tro-giup" },
];

function shortName(fullName: string, email: string): string {
  const clean = fullName.trim();
  if (clean) {
    const parts = clean.split(/\s+/);
    return parts.length <= 2 ? clean : parts.slice(-2).join(" ");
  }
  return email.split("@")[0]?.trim() || "Tài khoản";
}

export function PlatformHeader() {
  const pathname = usePathname();
  const { user, isAuthenticated, loading: sessionLoading, logout } = useSession();

  const [drawerOpen, setDrawerOpen] = useState(false);
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const [atTop, setAtTop] = useState(true);
  const [hidden, setHidden] = useState(false);

  const lastScrollY = useRef(0);

  const isSeller = user?.role === "seller";
  const isAdmin = user?.role === "admin" || user?.role === "super_admin";
  const displayName = user ? shortName(user.fullName, user.email) : "";
  const isHome = pathname === "/";

  useEffect(() => {
    setDrawerOpen(false);
    setUserMenuOpen(false);
    setHidden(false);
  }, [pathname]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    lastScrollY.current = window.scrollY;
    setAtTop(window.scrollY < 24);

    let rafId = 0;
    const onScroll = () => {
      if (rafId) return;
      rafId = window.requestAnimationFrame(() => {
        rafId = 0;
        const y = window.scrollY;
        const delta = y - lastScrollY.current;
        setAtTop(y < 24);

        if (drawerOpen || userMenuOpen) {
          setHidden(false);
        } else if (y < 72) {
          setHidden(false);
        } else if (delta > 6) {
          setHidden(true);
        } else if (delta < -4) {
          setHidden(false);
        }
        lastScrollY.current = y;
      });
    };

    window.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      window.removeEventListener("scroll", onScroll);
      if (rafId) window.cancelAnimationFrame(rafId);
    };
  }, [drawerOpen, userMenuOpen]);

  const isActive = (matchPath?: string) =>
    Boolean(matchPath && (pathname === matchPath || pathname.startsWith(`${matchPath}/`)));

  const handleLogout = async () => {
    setUserMenuOpen(false);
    setDrawerOpen(false);
    await logout("/");
  };

  const handleSkipToMain = (e: React.MouseEvent<HTMLAnchorElement>) => {
    const mainEl = document.querySelector<HTMLElement>("main");
    if (mainEl) {
      e.preventDefault();
      mainEl.focus();
      mainEl.scrollIntoView({ behavior: "smooth", block: "start" });
    }
  };

  const surfaceState = atTop && isHome ? "transparent" : atTop ? "flat" : "solid";
  const isLoginRoute = pathname === "/dang-nhap";

  return (
    <UiScope>
      <a
        href="#main-content"
        className="fixed top-0 -left-[9999px] z-[90] rounded-full bg-[var(--home-ink)] px-5 py-2.5 text-sm font-[550] text-[var(--home-bg)] focus:top-3 focus:left-4"
        onClick={handleSkipToMain}
      >
        Bỏ qua đến nội dung
      </a>

      <header
        className={cn(
          "fixed inset-x-0 top-0 z-40 h-[var(--home-header-h-sm)] font-[family-name:var(--home-font)] text-[var(--home-ink)] transition-[transform,translate,background-color] duration-[280ms] ease-[cubic-bezier(0.22,1,0.36,1)] focus-within:translate-y-0 min-[961px]:h-[var(--home-header-h)] motion-reduce:transition-none [&_:focus-visible]:outline-2 [&_:focus-visible]:outline-offset-2 [&_:focus-visible]:outline-[var(--home-accent)]",
          hidden && !drawerOpen && "-translate-y-[105%] duration-[210ms]",
          isHome
            ? surfaceState === "transparent"
              ? "border-b border-transparent bg-transparent"
              : "border-b border-[var(--home-border)] bg-[var(--home-band)]"
            : surfaceState === "flat"
              ? "border-b border-[var(--home-border)] bg-[rgb(2_9_10_/_0.88)] backdrop-blur-md"
              : "border-b border-[var(--home-border)] bg-[var(--home-band)]"
        )}
        data-surface={surfaceState}
        data-home={isHome ? "true" : undefined}
        data-hidden={hidden && !drawerOpen ? "true" : "false"}
      >
        <div className="mx-auto grid h-full w-[min(var(--home-max),calc(100%-2*var(--home-gutter)))] grid-cols-[auto_1fr] items-center gap-3 px-0 min-[961px]:grid-cols-[auto_1fr_auto] min-[961px]:gap-12">
          <Link
            href="/"
            className="inline-flex min-h-[36px] w-fit items-center gap-2 py-1 text-[var(--home-ink)] transition-opacity duration-200 hover:opacity-[0.78] motion-reduce:transition-none"
            aria-label="NexusMart trang chủ"
          >
            <NexusMark size={22} accentColor="var(--home-accent)" />
            <span className="font-[family-name:var(--home-font)] text-[length:var(--home-fs-h3)] leading-none font-semibold tracking-[-0.025em]">
              NexusMart
            </span>
          </Link>

          <nav
            className="hidden items-center justify-start gap-8 min-[961px]:flex"
            aria-label="Điều hướng nền tảng"
          >
            {NAV_ITEMS.map((item) => {
              const active = isActive(item.matchPath);
              return (
                <Link
                  key={item.href}
                  href={item.href}
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "relative px-1 py-2 text-[length:var(--home-fs-body)] font-[450] whitespace-nowrap transition-colors duration-200 motion-reduce:transition-none",
                    isHome
                      ? cn(
                          "text-[var(--home-ink)] hover:opacity-100",
                          active ? "opacity-100" : "opacity-[0.92]"
                        )
                      : active
                        ? "text-[var(--home-accent)] after:absolute after:inset-x-1 after:bottom-0 after:h-[1.5px] after:rounded-full after:bg-[var(--home-accent)]"
                        : "text-[var(--home-ink)] opacity-[0.84] hover:opacity-100"
                  )}
                >
                  {item.label}
                </Link>
              );
            })}
          </nav>

          <div className="hidden items-center justify-end gap-5 min-[961px]:flex">
            {isAuthenticated && user ? (
              <>
                {isSeller && (
                  <Link
                    href="/seller"
                    className="rounded-full px-3 py-2 text-[length:var(--home-fs-body)] font-[450] whitespace-nowrap text-[var(--home-ink)] transition-colors duration-200 hover:bg-[var(--home-glass-strong)] motion-reduce:transition-none"
                  >
                    Kênh người bán
                  </Link>
                )}
                {isAdmin && (
                  <Link
                    href="/admin"
                    className="rounded-full px-3 py-2 text-[length:var(--home-fs-body)] font-[450] whitespace-nowrap text-[var(--home-ink)] transition-colors duration-200 hover:bg-[var(--home-glass-strong)] motion-reduce:transition-none"
                  >
                    Cổng quản trị
                  </Link>
                )}

                <DropdownMenu open={userMenuOpen} onOpenChange={setUserMenuOpen}>
                  <DropdownMenuTrigger asChild>
                    <button
                      type="button"
                      className="inline-flex min-h-[var(--home-btn-header-h)] items-center gap-2 rounded-full border border-[var(--home-border)] bg-[var(--home-glass-strong)] px-3.5 text-sm font-medium text-[var(--home-ink)] transition-colors duration-200 hover:bg-[var(--home-border-strong)] motion-reduce:transition-none"
                      aria-label="Tài khoản của bạn"
                    >
                      <User className="size-4 shrink-0" aria-hidden="true" />
                      <span className="max-w-[130px] truncate">{displayName}</span>
                      <ChevronDown
                        className={cn(
                          "size-3.5 shrink-0 transition-transform duration-200 motion-reduce:transition-none",
                          userMenuOpen && "rotate-180"
                        )}
                        aria-hidden="true"
                      />
                    </button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent
                    align="end"
                    sideOffset={8}
                    className="w-60 border-[var(--home-border)] bg-[var(--home-card)] text-[var(--home-ink)] shadow-[var(--home-shadow-float)]"
                  >
                    <DropdownMenuLabel className="flex flex-col gap-0.5 px-2.5 py-2 font-normal">
                      <span className="truncate text-sm font-semibold text-[var(--home-ink)]">
                        {user.fullName || "Tài khoản"}
                      </span>
                      <span className="truncate text-xs text-[var(--home-ink-2)]">
                        {user.email}
                      </span>
                    </DropdownMenuLabel>
                    <DropdownMenuSeparator className="bg-[var(--home-border)]" />
                    {isSeller && (
                      <DropdownMenuItem
                        asChild
                        className="cursor-pointer text-[var(--home-ink)] focus:bg-[var(--home-glass-strong)] focus:text-[var(--home-accent)]"
                      >
                        <Link href="/seller">
                          <Store className="size-4" aria-hidden="true" />
                          Kênh người bán
                        </Link>
                      </DropdownMenuItem>
                    )}
                    {isAdmin && (
                      <DropdownMenuItem
                        asChild
                        className="cursor-pointer text-[var(--home-ink)] focus:bg-[var(--home-glass-strong)] focus:text-[var(--home-accent)]"
                      >
                        <Link href="/admin">
                          <Shield className="size-4" aria-hidden="true" />
                          Cổng quản trị
                        </Link>
                      </DropdownMenuItem>
                    )}
                    <DropdownMenuItem
                      asChild
                      className="cursor-pointer text-[var(--home-ink)] focus:bg-[var(--home-glass-strong)] focus:text-[var(--home-accent)]"
                    >
                      <Link href="/#danh-ba-gian-hang">
                        <Store className="size-4" aria-hidden="true" />
                        Danh bạ gian hàng
                      </Link>
                    </DropdownMenuItem>
                    <DropdownMenuSeparator className="bg-[var(--home-border)]" />
                    <DropdownMenuItem
                      variant="destructive"
                      className="cursor-pointer text-red-300 focus:bg-[var(--home-glass)] focus:text-red-200 *:[svg]:text-red-300!"
                      disabled={sessionLoading}
                      onSelect={() => {
                        void handleLogout();
                      }}
                    >
                      <LogOut className="size-4" aria-hidden="true" />
                      {sessionLoading ? "Đang đăng xuất..." : "Đăng xuất"}
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>

                {!isSeller && !isAdmin && (
                  <Link
                    href="/dang-ky?role=seller"
                    className="inline-flex min-h-[var(--home-btn-header-h)] items-center justify-center rounded-full bg-[var(--home-ink)] px-5 py-2 text-[length:var(--home-fs-body)] font-[550] whitespace-nowrap text-[var(--home-bg)] transition-[background-color,transform] duration-200 hover:bg-[#eff3f5] active:scale-[0.98] motion-reduce:transition-none motion-reduce:active:scale-100"
                  >
                    Mở gian hàng
                  </Link>
                )}
              </>
            ) : (
              <>
                <Link
                  href="/dang-nhap"
                  aria-current={isLoginRoute ? "page" : undefined}
                  className={cn(
                    "rounded-full px-3 py-2 text-[length:var(--home-fs-body)] font-[450] whitespace-nowrap transition-colors duration-200 hover:bg-[var(--home-glass-strong)] motion-reduce:transition-none",
                    !isHome && isLoginRoute
                      ? "bg-[var(--home-glass)] text-[var(--home-accent)]"
                      : "text-[var(--home-ink)]"
                  )}
                >
                  Đăng nhập
                </Link>
                <Link
                  href="/dang-ky?role=seller"
                  className="inline-flex min-h-[var(--home-btn-header-h)] items-center justify-center rounded-full bg-[var(--home-ink)] px-5 py-2 text-[length:var(--home-fs-body)] font-[550] whitespace-nowrap text-[var(--home-bg)] transition-[background-color,transform] duration-200 hover:bg-[#eff3f5] active:scale-[0.98] motion-reduce:transition-none motion-reduce:active:scale-100"
                >
                  Mở gian hàng
                </Link>
              </>
            )}
          </div>

          <div className="flex items-center justify-end gap-2 min-[961px]:hidden">
            <Link
              href="/dang-ky?role=seller"
              className="inline-flex min-h-[44px] items-center justify-center rounded-[6px] bg-transparent px-2 text-[length:var(--home-fs-small)] font-medium whitespace-nowrap text-[var(--home-ink)] underline underline-offset-4 hover:bg-[var(--home-glass)]"
            >
              Mở gian hàng
            </Link>

            <Sheet open={drawerOpen} onOpenChange={setDrawerOpen}>
              <SheetTrigger asChild>
                <button
                  type="button"
                  className="inline-flex size-[44px] items-center justify-center rounded-full bg-transparent text-[var(--home-ink)] transition-[background-color,transform] duration-200 hover:bg-[var(--home-glass)] active:scale-95 motion-reduce:transition-none motion-reduce:active:scale-100"
                  aria-label="Mở trình đơn điều hướng"
                >
                  <Menu className="size-[18px]" strokeWidth={1.75} aria-hidden="true" />
                </button>
              </SheetTrigger>

              <SheetContent
                side="right"
                showCloseButton={false}
                className="flex w-full max-w-sm flex-col justify-between border-[var(--home-border)] bg-[var(--home-band)] p-5 font-[family-name:var(--home-font)] text-[var(--home-ink)]"
              >
                <div>
                  <SheetHeader className="flex-row items-center justify-between p-0">
                    <SheetTitle asChild>
                      <Link
                        href="/"
                        className="inline-flex min-h-10 items-center gap-2.5 text-base font-semibold tracking-tight text-[var(--home-ink)]"
                        onClick={() => setDrawerOpen(false)}
                      >
                        <NexusMark size={20} accentColor="var(--home-accent)" />
                        <span>NexusMart</span>
                      </Link>
                    </SheetTitle>
                    <button
                      type="button"
                      onClick={() => setDrawerOpen(false)}
                      className="inline-flex size-10 items-center justify-center rounded-full bg-[var(--home-glass-strong)] text-[var(--home-ink)] transition-colors hover:bg-[var(--home-border-strong)] motion-reduce:transition-none"
                      aria-label="Đóng trình đơn"
                    >
                      <X className="size-5" aria-hidden="true" />
                    </button>
                  </SheetHeader>

                  <nav className="mt-8 flex flex-col gap-1" aria-label="Điều hướng di động">
                    {NAV_ITEMS.map((item) => {
                      const active = isActive(item.matchPath);
                      return (
                        <Link
                          key={item.href}
                          href={item.href}
                          aria-current={active ? "page" : undefined}
                          className={cn(
                            "flex min-h-12 items-center rounded-lg px-3 text-lg font-medium transition-colors motion-reduce:transition-none",
                            active
                              ? "bg-[var(--home-glass)] text-[var(--home-accent)]"
                              : "text-[var(--home-ink)] hover:bg-[var(--home-glass)] hover:text-[var(--home-accent)]"
                          )}
                          onClick={() => setDrawerOpen(false)}
                        >
                          {item.label}
                        </Link>
                      );
                    })}
                  </nav>
                </div>

                <div className="flex flex-col gap-2.5 border-t border-[var(--home-border)] pt-5">
                  {isAuthenticated && user ? (
                    <>
                      <p className="mb-1 truncate text-xs text-[var(--home-ink-2)]">{user.email}</p>
                      {isSeller && (
                        <Button
                          size="lg"
                          className="min-h-11 w-full rounded-full bg-[var(--home-ink)] text-[var(--home-bg)] shadow-none hover:bg-[#eff3f5]"
                          asChild
                        >
                          <Link href="/seller" onClick={() => setDrawerOpen(false)}>
                            Kênh người bán
                          </Link>
                        </Button>
                      )}
                      {isAdmin && (
                        <Button
                          size="lg"
                          className="min-h-11 w-full rounded-full bg-[var(--home-ink)] text-[var(--home-bg)] shadow-none hover:bg-[#eff3f5]"
                          asChild
                        >
                          <Link href="/admin" onClick={() => setDrawerOpen(false)}>
                            Cổng quản trị
                          </Link>
                        </Button>
                      )}
                      <Button
                        type="button"
                        variant="outline"
                        size="lg"
                        className="min-h-11 w-full rounded-full border-[var(--home-border-strong)] bg-[var(--home-glass-strong)] text-[var(--home-ink)] shadow-none hover:bg-[var(--home-border-strong)] hover:text-[var(--home-ink)]"
                        onClick={() => void handleLogout()}
                        disabled={sessionLoading}
                      >
                        {sessionLoading ? "Đang đăng xuất..." : "Đăng xuất"}
                      </Button>
                    </>
                  ) : (
                    <>
                      <Button
                        size="lg"
                        className="min-h-11 w-full rounded-full bg-[var(--home-ink)] text-[var(--home-bg)] shadow-none hover:bg-[#eff3f5]"
                        asChild
                      >
                        <Link href="/dang-ky?role=seller" onClick={() => setDrawerOpen(false)}>
                          Mở gian hàng
                        </Link>
                      </Button>
                      <Button
                        variant="outline"
                        size="lg"
                        className="min-h-11 w-full rounded-full border-[var(--home-border-strong)] bg-[var(--home-glass-strong)] text-[var(--home-ink)] shadow-none hover:bg-[var(--home-border-strong)] hover:text-[var(--home-ink)]"
                        asChild
                      >
                        <Link href="/dang-nhap" onClick={() => setDrawerOpen(false)}>
                          Đăng nhập
                        </Link>
                      </Button>
                    </>
                  )}
                </div>
              </SheetContent>
            </Sheet>
          </div>
        </div>
      </header>
    </UiScope>
  );
}
