"use client";

import React, { useEffect } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useSession } from "@/components/SessionProvider";
import { useStore } from "@/context/StoreContext";
import { useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import { SITE_NAV_ITEMS, type SiteNavKey } from "./siteNav";
import styles from "./site.module.css";

export { SITE_NAV_ITEMS, type SiteNavKey };

/**
 * Visible pathname on a site host. The middleware rewrites `/<path>` to `/s/<host>/<path>`;
 * strip that internal prefix if the router ever exposes it.
 */
export function useSitePathname(): string {
  const raw = usePathname() || "/";
  const m = raw.match(/^\/s\/[^/]+(\/.*)?$/);
  return m ? m[1] || "/" : raw;
}

function isActive(pathname: string, href: string): boolean {
  if (href === "/") return pathname === "/";
  return pathname === href || pathname.startsWith(`${href}/`);
}

export function SiteNav({ previewMode = false }: { previewMode?: boolean }) {
  const pathname = useSitePathname();
  return (
    <nav className={styles.headerNav} aria-label="Điều hướng cửa hàng">
      {SITE_NAV_ITEMS.map((item) => {
        const active = !previewMode && isActive(pathname, item.href);
        const className = `${styles.navLink} ${active ? styles.navLinkActive : ""}`;
        if (previewMode) {
          return (
            <span key={item.key} className={className}>
              {item.label}
            </span>
          );
        }
        return (
          <Link
            key={item.key}
            href={item.href}
            className={className}
            aria-current={active ? "page" : undefined}
          >
            {item.label}
          </Link>
        );
      })}
    </nav>
  );
}

/** Search, account and cart actions in the site header (live session + cart state). */
export function SiteHeaderActions() {
  const pathname = useSitePathname();
  const { user, isAuthenticated } = useSession();
  const { cartCount } = useStore();
  const loginHref =
    pathname === "/" || pathname.startsWith("/dang-")
      ? "/dang-nhap"
      : `/dang-nhap?next=${encodeURIComponent(pathname)}`;

  return (
    <div className={styles.headerActions}>
      <form action="/tim-kiem" method="get" role="search" className={styles.headerSearch}>
        <label htmlFor="site-search" className="sr-only">
          Tìm trong cửa hàng
        </label>
        <input
          id="site-search"
          name="q"
          type="search"
          placeholder="Tìm sản phẩm"
          className={styles.headerSearchInput}
          enterKeyHint="search"
        />
      </form>
      {isAuthenticated && user ? (
        <>
          <Link href="/don-hang" className={styles.headerTextAction}>
            Đơn hàng
          </Link>
          <Link href="/tai-khoan" className={styles.headerTextAction}>
            Tài khoản
          </Link>
        </>
      ) : (
        <Link href={loginHref} className={styles.headerTextAction}>
          Đăng nhập
        </Link>
      )}
      <Link
        href="/gio-hang"
        className={styles.headerAction}
        aria-label={cartCount > 0 ? `Giỏ hàng, ${cartCount} sản phẩm` : "Giỏ hàng"}
      >
        Giỏ hàng
        {cartCount > 0 && <span className={`${styles.headerCartCount} num`}>{cartCount}</span>}
      </Link>
    </div>
  );
}

/** Toast from the store context (add to cart, voucher saved…). */
export function SiteToast() {
  const { toastMessage, dismissToast } = useStore();

  useEffect(() => {
    if (!toastMessage) return;
    const timer = window.setTimeout(dismissToast, 3600);
    return () => window.clearTimeout(timer);
  }, [toastMessage, dismissToast]);

  if (!toastMessage) return null;
  return (
    <div className={styles.siteToast} role="status" aria-live="polite">
      <span>{toastMessage}</span>
      <button type="button" className={styles.siteToastClose} onClick={dismissToast}>
        Đóng
      </button>
    </div>
  );
}

/** Re-arms `[data-reveal]` / `[data-rise]` observers whenever the visible page changes. */
export function SiteMotion({ depKey }: { depKey?: string | number }) {
  const pathname = useSitePathname();
  useRevealOnScroll(`${pathname}-${depKey ?? ""}`);
  return null;
}
