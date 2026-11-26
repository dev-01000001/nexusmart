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

  return (
    <UiScope>
      <a
        href="#main-content"
        className="fixed top-0 -left-[9999px] z-[90] rounded-md bg-topbar px-4 py-2.5 text-sm font-medium text-white focus:top-3 focus:left-4"
        onClick={handleSkipToMain}
      >
        Bỏ qua đến nội dung
      </a>

      <header
        className={cn(
          "fixed inset-x-0 top-0 z-40 transition-[transform,translate,background-color] duration-[280ms] ease-[cubic-bezier(0.22,1,0.36,1)] focus-within:translate-y-0",
          hidden && !drawerOpen && "-translate-y-[105%] duration-[210ms]",
          isHome
            ? cn(
                "h-[var(--home-header-h-sm)] font-[family-name:var(--home-font)] text-[var(--home-ink)] min-[961px]:h-[var(--home-header-h)] [&_:focus-visible]:outline-2 [&_:focus-visible]:outline-offset-2 [&_:focus-visible]:outline-[var(--home-accent)]",
                surfaceState === "transparent"
                  ? "border-b border-transparent bg-transparent"
                  : "border-b border-[var(--home-border)] bg-[var(--home-band)]"
              )
            : cn(
                "h-16 border-b border-border text-foreground min-[961px]:h-[68px]",
                surfaceState === "flat" ? "bg-background" : "bg-card shadow-card"
              )
        )}
        data-surface={surfaceState}
        data-home={isHome ? "true" : undefined}
        data-hidden={hidden && !drawerOpen ? "true" : "false"}
      >
        <div
          className={cn(
            "mx-auto grid h-full items-center",
            isHome
              ? "w-[min(var(--home-max),calc(100%-2*var(--home-gutter)))] grid-cols-[auto_1fr] gap-[var(--space-3)] px-0 min-[961px]:grid-cols-[auto_1fr_auto] min-[961px]:gap-[var(--space-8)]"
              : "w-full grid-cols-[auto_1fr] gap-4 px-4 sm:px-6 min-[961px]:grid-cols-[1fr_auto_1fr] lg:px-8"
          )}
        >
          <Link
            href="/"
            className={cn(
              "inline-flex min-h-[36px] w-fit items-center py-1 transition-opacity duration-200 hover:opacity-[0.78]",
              isHome ? "gap-[var(--space-2)] text-[var(--home-ink)]" : "gap-2.5 text-foreground"
            )}
            aria-label="NexusMart trang chủ"
          >
            <NexusMark
              size={isHome ? 22 : 20}
              accentColor={isHome ? "var(--home-accent)" : "var(--ui-brand)"}
            />
            <span
              className={cn(
                "leading-none font-semibold",
                isHome
                  ? "font-[family-name:var(--home-font)] text-[length:var(--home-fs-h3)] tracking-[-0.025em]"
                  : "text-base tracking-tight"
              )}
            >
              NexusMart
            </span>
          </Link>

          <nav
            className={cn(
              "hidden items-center min-[961px]:flex",
              isHome ? "justify-start gap-[var(--space-7)]" : "justify-center gap-7"
            )}
            aria-label="Điều hướng nền tảng"
          >
            {NAV_ITEMS.map((item) => {
              const active = isActive(item.matchPath);
              return (
                <Link
                  key={item.href}
                  href={item.href}
                  className={cn(
                    "relative whitespace-nowrap transition-colors duration-200",
                    isHome
                      ? cn(
                          "px-[var(--space-1)] py-[var(--space-2)] text-[length:var(--home-fs-body)] font-[450] text-[var(--home-ink)] hover:opacity-100",
                          active ? "opacity-100" : "opacity-[0.92]"
                        )
                      : cn(
                          "rounded-md px-2 py-1.5 text-sm font-medium hover:text-brand",
                          active ? "font-semibold text-brand" : "text-muted-foreground"
                        )
                  )}
                >
                  {item.label}
                </Link>
              );
            })}
          </nav>

          <div
            className={cn(
              "hidden items-center justify-end min-[961px]:flex",
              isHome ? "gap-[var(--space-5)]" : "gap-3"
            )}
          >
            {isAuthenticated && user ? (
              <>
                {isSeller &&
                  (isHome ? (
                    <Link
                      href="/seller"
                      className="rounded-full px-[var(--space-3)] py-[var(--space-2)] text-[length:var(--home-fs-body)] font-[450] whitespace-nowrap text-[var(--home-ink)] transition-colors duration-200 hover:bg-[var(--home-glass-strong)]"
                    >
                      Kênh người bán
                    </Link>
                  ) : (
                    <Button variant="ghost" size="sm" asChild>
                      <Link href="/seller">Kênh người bán</Link>
                    </Button>
                  ))}
                {isAdmin &&
                  (isHome ? (
                    <Link
                      href="/admin"
                      className="rounded-full px-[var(--space-3)] py-[var(--space-2)] text-[length:var(--home-fs-body)] font-[450] whitespace-nowrap text-[var(--home-ink)] transition-colors duration-200 hover:bg-[var(--home-glass-strong)]"
                    >
                      Cổng quản trị
                    </Link>
                  ) : (
                    <Button variant="ghost" size="sm" asChild>
                      <Link href="/admin">Cổng quản trị</Link>
                    </Button>
                  ))}

                <DropdownMenu open={userMenuOpen} onOpenChange={setUserMenuOpen}>
                  <DropdownMenuTrigger asChild>
                    <button
                      type="button"
                      className={cn(
                        "inline-flex items-center gap-2 rounded-full text-sm font-medium transition-colors duration-200",
                        isHome
                          ? "min-h-[var(--home-btn-header-h)] border border-[var(--home-border)] bg-[var(--home-glass-strong)] px-3.5 text-[var(--home-ink)] hover:bg-[var(--home-border-strong)]"
                          : "h-10 border border-border bg-secondary px-3.5 text-foreground hover:bg-accent"
                      )}
                      aria-label="Tài khoản của bạn"
                    >
                      <User className="size-4 shrink-0" aria-hidden="true" />
                      <span className="max-w-[130px] truncate">{displayName}</span>
                      <ChevronDown
                        className={cn(
                          "size-3.5 shrink-0 transition-transform duration-200",
                          userMenuOpen && "rotate-180"
                        )}
                        aria-hidden="true"
                      />
                    </button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent
                    align="end"
                    sideOffset={8}
                    className={cn(
                      "w-60",
                      isHome &&
                        "border-[var(--home-border)] bg-[var(--home-card)] text-[var(--home-ink)] shadow-[var(--home-shadow-float)]"
                    )}
                  >
                    <DropdownMenuLabel className="flex flex-col gap-0.5 px-2.5 py-2 font-normal">
                      <span
                        className={cn(
                          "truncate text-sm font-semibold",
                          isHome ? "text-[var(--home-ink)]" : "text-foreground"
                        )}
                      >
                        {user.fullName || "Tài khoản"}
                      </span>
                      <span
                        className={cn(
                          "truncate text-xs",
                          isHome ? "text-[var(--home-ink-2)]" : "text-muted-foreground"
                        )}
                      >
                        {user.email}
                      </span>
                    </DropdownMenuLabel>
                    <DropdownMenuSeparator className={cn(isHome && "bg-[var(--home-border)]")} />
                    {isSeller && (
                      <DropdownMenuItem
                        asChild
                        className={cn(
                          "cursor-pointer",
                          isHome &&
                            "text-[var(--home-ink)] focus:bg-[var(--home-glass-strong)] focus:text-[var(--home-accent)]"
                        )}
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
                        className={cn(
                          "cursor-pointer",
                          isHome &&
                            "text-[var(--home-ink)] focus:bg-[var(--home-glass-strong)] focus:text-[var(--home-accent)]"
                        )}
                      >
                        <Link href="/admin">
                          <Shield className="size-4" aria-hidden="true" />
                          Cổng quản trị
                        </Link>
                      </DropdownMenuItem>
                    )}
                    <DropdownMenuItem
                      asChild
                      className={cn(
                        "cursor-pointer",
                        isHome &&
                          "text-[var(--home-ink)] focus:bg-[var(--home-glass-strong)] focus:text-[var(--home-accent)]"
                      )}
                    >
                      <Link href="/#danh-ba-gian-hang">
                        <Store className="size-4" aria-hidden="true" />
                        Danh bạ gian hàng
                      </Link>
                    </DropdownMenuItem>
                    <DropdownMenuSeparator className={cn(isHome && "bg-[var(--home-border)]")} />
                    <DropdownMenuItem
                      variant="destructive"
                      className={cn(
                        "cursor-pointer",
                        isHome &&
                          "text-red-300 focus:bg-[var(--home-glass)] focus:text-red-200 *:[svg]:text-red-300!"
                      )}
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

                {!isSeller &&
                  !isAdmin &&
                  (isHome ? (
                    <Link
                      href="/dang-ky?role=seller"
                      className="inline-flex min-h-[var(--home-btn-header-h)] items-center justify-center rounded-full bg-[var(--home-ink)] px-[var(--space-5)] py-[var(--space-2)] text-[length:var(--home-fs-body)] font-[550] whitespace-nowrap text-[var(--home-bg)] transition-[background-color,transform] duration-200 hover:bg-[var(--bg-2)] active:scale-[0.98]"
                    >
                      Mở gian hàng
                    </Link>
                  ) : (
                    <Button asChild>
                      <Link href="/dang-ky?role=seller">Mở gian hàng</Link>
                    </Button>
                  ))}
              </>
            ) : isHome ? (
              <>
                <Link
                  href="/dang-nhap"
                  className="rounded-full px-[var(--space-3)] py-[var(--space-2)] text-[length:var(--home-fs-body)] font-[450] whitespace-nowrap text-[var(--home-ink)] transition-colors duration-200 hover:bg-[var(--home-glass-strong)]"
                >
                  Đăng nhập
                </Link>
                <Link
                  href="/dang-ky?role=seller"
                  className="inline-flex min-h-[var(--home-btn-header-h)] items-center justify-center rounded-full bg-[var(--home-ink)] px-[var(--space-5)] py-[var(--space-2)] text-[length:var(--home-fs-body)] font-[550] whitespace-nowrap text-[var(--home-bg)] transition-[background-color,transform] duration-200 hover:bg-[var(--bg-2)] active:scale-[0.98]"
                >
                  Mở gian hàng
                </Link>
              </>
            ) : (
              <>
                <Button variant="ghost" asChild>
                  <Link href="/dang-nhap">Đăng nhập</Link>
                </Button>
                <Button asChild>
                  <Link href="/dang-ky?role=seller">Mở gian hàng</Link>
                </Button>
              </>
            )}
          </div>

          <div className="flex items-center justify-end gap-2 min-[961px]:hidden">
            {isHome ? (
              <Link
                href="/dang-ky?role=seller"
                className="inline-flex min-h-[44px] items-center justify-center rounded-[var(--radius-control)] bg-transparent px-[var(--space-2)] text-[length:var(--home-fs-small)] font-medium whitespace-nowrap text-[var(--home-ink)] underline underline-offset-[var(--space-1)] hover:bg-[var(--home-glass)]"
              >
                Mở gian hàng
              </Link>
            ) : (
              <Button size="sm" className="min-h-10" asChild>
                <Link href="/dang-ky?role=seller">Mở gian hàng</Link>
              </Button>
            )}

            <Sheet open={drawerOpen} onOpenChange={setDrawerOpen}>
              <SheetTrigger asChild>
                <button
                  type="button"
                  className={cn(
                    "inline-flex size-[44px] items-center justify-center rounded-full transition-[background-color,transform] duration-200 active:scale-95",
                    isHome
                      ? "bg-transparent text-[var(--home-ink)] hover:bg-[var(--home-glass)]"
                      : "bg-secondary text-foreground hover:bg-accent"
                  )}
                  aria-label="Mở trình đơn điều hướng"
                >
                  <Menu className="size-[18px]" strokeWidth={1.75} aria-hidden="true" />
                </button>
              </SheetTrigger>

              <SheetContent
                side="right"
                showCloseButton={false}
                className={cn(
                  "flex w-full max-w-sm flex-col justify-between p-5",
                  isHome
                    ? "border-[var(--home-border)] bg-[var(--home-band)] font-[family-name:var(--home-font)] text-[var(--home-ink)]"
                    : "border-border bg-card text-foreground"
                )}
              >
                <div>
                  <SheetHeader className="flex-row items-center justify-between p-0">
                    <SheetTitle asChild>
                      <Link
                        href="/"
                        className={cn(
                          "inline-flex min-h-10 items-center gap-2.5 text-base font-semibold tracking-tight",
                          isHome ? "text-[var(--home-ink)]" : "text-foreground"
                        )}
                        onClick={() => setDrawerOpen(false)}
                      >
                        <NexusMark
                          size={20}
                          accentColor={isHome ? "var(--home-accent)" : "var(--ui-brand)"}
                        />
                        <span>NexusMart</span>
                      </Link>
                    </SheetTitle>
                    <button
                      type="button"
                      onClick={() => setDrawerOpen(false)}
                      className={cn(
                        "inline-flex size-10 items-center justify-center rounded-full transition-colors",
                        isHome
                          ? "bg-[var(--home-glass-strong)] text-[var(--home-ink)] hover:bg-[var(--home-border-strong)]"
                          : "bg-secondary text-foreground hover:bg-accent"
                      )}
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
                          className={cn(
                            "flex min-h-12 items-center rounded-lg px-3 text-lg font-medium transition-colors",
                            isHome
                              ? "text-[var(--home-ink)] hover:bg-[var(--home-glass)] hover:text-[var(--home-accent)]"
                              : active
                                ? "bg-accent font-semibold text-brand"
                                : "text-foreground hover:bg-accent"
                          )}
                          onClick={() => setDrawerOpen(false)}
                        >
                          {item.label}
                        </Link>
                      );
                    })}
                  </nav>
                </div>

                <div className="flex flex-col gap-2.5 border-t border-border/50 pt-5">
                  {isAuthenticated && user ? (
                    <>
                      <p
                        className={cn(
                          "mb-1 truncate text-xs",
                          isHome ? "text-[var(--home-ink-2)]" : "text-muted-foreground"
                        )}
                      >
                        {user.email}
                      </p>
                      {isSeller && (
                        <Button
                          size="lg"
                          className={cn(
                            "min-h-11 w-full",
                            isHome &&
                              "rounded-full bg-[var(--home-ink)] text-[var(--home-bg)] hover:bg-[var(--bg-2)]"
                          )}
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
                          className={cn(
                            "min-h-11 w-full",
                            isHome &&
                              "rounded-full bg-[var(--home-ink)] text-[var(--home-bg)] hover:bg-[var(--bg-2)]"
                          )}
                          asChild
                        >
                          <Link href="/admin" onClick={() => setDrawerOpen(false)}>
                            Cổng quản trị
                          </Link>
                        </Button>
                      )}
                      <Button
                        type="button"
                        variant={isHome ? "outline" : "secondary"}
                        size="lg"
                        className={cn(
                          "min-h-11 w-full",
                          isHome &&
                            "rounded-full border-[var(--home-border-strong)] bg-[var(--home-glass-strong)] text-[var(--home-ink)] shadow-none hover:bg-[var(--home-border-strong)] hover:text-[var(--home-ink)]"
                        )}
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
                        className={cn(
                          "min-h-11 w-full",
                          isHome &&
                            "rounded-full bg-[var(--home-ink)] text-[var(--home-bg)] hover:bg-[var(--bg-2)]"
                        )}
                        asChild
                      >
                        <Link href="/dang-ky?role=seller" onClick={() => setDrawerOpen(false)}>
                          Mở gian hàng
                        </Link>
                      </Button>
                      <Button
                        variant={isHome ? "outline" : "secondary"}
                        size="lg"
                        className={cn(
                          "min-h-11 w-full",
                          isHome &&
                            "rounded-full border-[var(--home-border-strong)] bg-[var(--home-glass-strong)] text-[var(--home-ink)] shadow-none hover:bg-[var(--home-border-strong)] hover:text-[var(--home-ink)]"
                        )}
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
