"use client";

import React, { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useSession } from "@/components/SessionProvider";
import { IconClose, IconMenu } from "@/components/Icons";
import { useStore } from "@/context/StoreContext";
import { useRevealOnScroll } from "@/features/commerce/useRevealOnScroll";
import { SiteSearchBar } from "@/features/search";
import { SITE_NAV_ITEMS, type SiteNavKey } from "./siteNav";
import styles from "./site.module.css";
import tplStyles from "./siteTemplates.module.css";

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
    <nav
      className={`${styles.headerNav} ${previewMode ? styles.headerNavPreview : ""}`}
      aria-label="Điều hướng cửa hàng"
    >
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

export interface SiteHeaderActionsProps {
  brandName?: string;
  shopId?: string;
  /** When true (default), renders Swarm G `SiteSearchBar`; when false, falls back to plain search form. */
  showSearchSlot?: boolean;
  /** Hide inline desktop search input on ultra-minimal centered mastheads where space is tight. */
  hideDesktopSearch?: boolean;
}

/** Search, account and cart actions in the site header (desktop row + compact mobile drawer). */
export function SiteHeaderActions({
  brandName = "Cửa hàng",
  shopId,
  showSearchSlot = true,
  hideDesktopSearch = false,
}: SiteHeaderActionsProps) {
  const pathname = useSitePathname();
  const { user, isAuthenticated } = useSession();
  const { cartCount } = useStore();
  const [drawerOpen, setDrawerOpen] = useState(false);

  const loginHref =
    pathname === "/" || pathname.startsWith("/dang-")
      ? "/dang-nhap"
      : `/dang-nhap?next=${encodeURIComponent(pathname)}`;

  useEffect(() => {
    setDrawerOpen(false);
  }, [pathname]);

  useEffect(() => {
    if (!drawerOpen) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setDrawerOpen(false);
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [drawerOpen]);

  return (
    <>
      <div className={styles.headerActionsDesktop}>
        {!hideDesktopSearch &&
          (showSearchSlot ? (
            <div className={tplStyles.headerSearchSlot}>
              <SiteSearchBar brandName={brandName} shopId={shopId} compact />
            </div>
          ) : (
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
          ))}
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

      <div className={styles.headerActionsMobile}>
        <Link
          href="/gio-hang"
          className={styles.headerActionCompact}
          aria-label={cartCount > 0 ? `Giỏ hàng, ${cartCount} sản phẩm` : "Giỏ hàng"}
        >
          Giỏ hàng
          {cartCount > 0 && <span className={`${styles.headerCartCount} num`}>{cartCount}</span>}
        </Link>
        <button
          type="button"
          className={styles.mobileMenuBtn}
          onClick={() => setDrawerOpen(true)}
          aria-expanded={drawerOpen}
          aria-label="Mở trình đơn cửa hàng"
        >
          <IconMenu size={18} />
          <span>Menu</span>
        </button>
      </div>

      {drawerOpen && (
        <div
          className={styles.siteDrawerBackdrop}
          role="presentation"
          onClick={(e) => {
            if (e.target === e.currentTarget) setDrawerOpen(false);
          }}
        >
          <div
            className={styles.siteDrawerPanel}
            data-ui="site-drawer"
            role="dialog"
            aria-modal="true"
            aria-label={`Điều hướng ${brandName}`}
          >
            <div className={styles.siteDrawerHead}>
              <span className={styles.siteDrawerBrand}>{brandName}</span>
              <button
                type="button"
                className={styles.siteDrawerClose}
                onClick={() => setDrawerOpen(false)}
                aria-label="Đóng trình đơn"
              >
                <IconClose size={18} />
              </button>
            </div>

            {showSearchSlot ? (
              <SiteSearchBar
                brandName={brandName}
                shopId={shopId}
                compact
                onNavigate={() => setDrawerOpen(false)}
              />
            ) : (
              <form
                action="/tim-kiem"
                method="get"
                role="search"
                className={styles.siteDrawerSearch}
                onSubmit={() => setDrawerOpen(false)}
              >
                <label htmlFor="site-drawer-search" className="sr-only">
                  Tìm sản phẩm trong {brandName}
                </label>
                <input
                  id="site-drawer-search"
                  name="q"
                  type="search"
                  placeholder="Tìm sản phẩm..."
                  className={styles.siteDrawerSearchInput}
                  enterKeyHint="search"
                />
                <button type="submit" className={styles.siteDrawerSearchBtn}>
                  Tìm
                </button>
              </form>
            )}

            <nav className={styles.siteDrawerNav} aria-label="Điều hướng di động">
              {SITE_NAV_ITEMS.map((item) => (
                <Link
                  key={item.key}
                  href={item.href}
                  className={`${styles.siteDrawerLink} ${isActive(pathname, item.href) ? styles.siteDrawerLinkActive : ""}`}
                  onClick={() => setDrawerOpen(false)}
                >
                  {item.label}
                </Link>
              ))}
              <Link
                href="/chinh-sach"
                className={`${styles.siteDrawerLink} ${isActive(pathname, "/chinh-sach") ? styles.siteDrawerLinkActive : ""}`}
                onClick={() => setDrawerOpen(false)}
              >
                Chính sách mua hàng
              </Link>
            </nav>

            <div className={styles.siteDrawerActions}>
              {isAuthenticated && user ? (
                <>
                  <Link
                    href="/don-hang"
                    className={styles.siteDrawerPrimary}
                    onClick={() => setDrawerOpen(false)}
                  >
                    Đơn hàng của tôi
                  </Link>
                  <Link
                    href="/tai-khoan"
                    className={styles.siteDrawerSecondary}
                    onClick={() => setDrawerOpen(false)}
                  >
                    Tài khoản
                  </Link>
                </>
              ) : (
                <>
                  <Link
                    href={loginHref}
                    className={styles.siteDrawerPrimary}
                    onClick={() => setDrawerOpen(false)}
                  >
                    Đăng nhập
                  </Link>
                  <Link
                    href="/dang-ky"
                    className={styles.siteDrawerSecondary}
                    onClick={() => setDrawerOpen(false)}
                  >
                    Tạo tài khoản
                  </Link>
                </>
              )}
              <Link
                href="/tra-cuu-don-hang"
                className={styles.siteDrawerSecondary}
                onClick={() => setDrawerOpen(false)}
              >
                Tra cứu đơn hàng
              </Link>
            </div>
          </div>
        </div>
      )}
    </>
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
