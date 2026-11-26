"use client";

import Link from "next/link";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { cn } from "@/lib/utils";
import { SITE_NAV_ITEMS } from "../siteNav";

export interface SiteMobileDrawerProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  brandName: string;
  pathname: string;
  isAuthenticated: boolean;
  loginHref: string;
}

function isActivePath(pathname: string, href: string): boolean {
  if (href === "/") return pathname === "/";
  return pathname === href || pathname.startsWith(`${href}/`);
}

export function SiteMobileDrawer({
  open,
  onOpenChange,
  brandName,
  pathname,
  isAuthenticated,
  loginHref,
}: SiteMobileDrawerProps) {
  const close = () => onOpenChange(false);

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="left"
        className="flex w-[min(340px,88vw)] flex-col gap-0 border-r border-border bg-card p-0 text-card-foreground"
        aria-label={`Điều hướng ${brandName}`}
      >
        <SheetHeader className="border-b border-border px-5 py-4 text-left">
          <SheetTitle className="truncate pr-8 text-base font-semibold text-foreground">
            {brandName}
          </SheetTitle>
        </SheetHeader>

        <nav
          className="flex flex-1 flex-col gap-1.5 overflow-y-auto px-4 py-4"
          aria-label="Điều hướng di động"
        >
          {SITE_NAV_ITEMS.map((item) => {
            const active = isActivePath(pathname, item.href);
            return (
              <Link
                key={item.key}
                href={item.href}
                onClick={close}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "flex min-h-11 items-center rounded-md px-3.5 text-[15px] font-medium transition-colors",
                  active
                    ? "bg-primary text-primary-foreground"
                    : "bg-background text-foreground hover:bg-accent hover:text-accent-foreground"
                )}
              >
                {item.label}
              </Link>
            );
          })}
          <Link
            href="/chinh-sach"
            onClick={close}
            aria-current={isActivePath(pathname, "/chinh-sach") ? "page" : undefined}
            className={cn(
              "flex min-h-11 items-center rounded-md px-3.5 text-[15px] font-medium transition-colors",
              isActivePath(pathname, "/chinh-sach")
                ? "bg-primary text-primary-foreground"
                : "bg-background text-foreground hover:bg-accent hover:text-accent-foreground"
            )}
          >
            Chính sách
          </Link>
        </nav>

        <Separator />

        <div className="flex flex-col gap-2.5 p-4">
          {isAuthenticated ? (
            <>
              <Button asChild className="min-h-11 w-full shadow-none">
                <Link href="/don-hang" onClick={close}>
                  Đơn hàng
                </Link>
              </Button>
              <Button asChild variant="secondary" className="min-h-11 w-full shadow-none">
                <Link href="/tai-khoan" onClick={close}>
                  Tài khoản
                </Link>
              </Button>
            </>
          ) : (
            <>
              <Button asChild className="min-h-11 w-full shadow-none">
                <Link href={loginHref} onClick={close}>
                  Đăng nhập
                </Link>
              </Button>
              <Button asChild variant="secondary" className="min-h-11 w-full shadow-none">
                <Link href="/dang-ky" onClick={close}>
                  Tạo tài khoản
                </Link>
              </Button>
            </>
          )}
          <Button asChild variant="outline" className="min-h-11 w-full shadow-none">
            <Link href="/tra-cuu-don-hang" onClick={close}>
              Tra cứu đơn
            </Link>
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  );
}
