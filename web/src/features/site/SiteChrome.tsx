"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useSession } from "@/components/SessionProvider";
import { IconBag, IconClose, IconMenu, IconSearch } from "@/components/Icons";
import { useModalFocusTrap } from "@/components/modalFocus";
import { useStore } from "@/context/StoreContext";
import { SiteSearchBar } from "@/features/search";
import { SITE_NAV_ITEMS, stripInternalSitePrefix, type SiteNavKey } from "./siteNav";
import styles from "./site.module.css";

export { SITE_NAV_ITEMS, stripInternalSitePrefix, type SiteNavKey };

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
  /** When true (default), renders Swarm G `SiteSearchBar` inside overlay/drawer; when false, plain form. */
  showSearchSlot?: boolean;
}

/**
 * Header actions: Search icon -> full-screen clip-path search overlay,
 * authenticated account/order links, Cart icon with non-elastic count pop,
 * and mobile slide-over drawer.
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
  const [drawerClosing, setDrawerClosing] = useState(false);
  const [searchState, setSearchState] = useState<"closed" | "open" | "closing">("closed");
  const [badgePop, setBadgePop] = useState(false);

  const prevCartRef = useRef<number>(cartCount);
  const overlayRef = useRef<HTMLDivElement | null>(null);
  const drawerPanelRef = useRef<HTMLDivElement | null>(null);
  const searchRestoreFocusRef = useRef(true);
  const drawerRestoreFocusRef = useRef(true);
  const searchCloseTimerRef = useRef<number | null>(null);
  const drawerCloseTimerRef = useRef<number | null>(null);

  const loginHref =
    pathname === "/" || pathname.startsWith("/dang-")
      ? "/dang-nhap"
      : `/dang-nhap?next=${encodeURIComponent(pathname)}`;

  // Subtle non-elastic badge pulse (scale 1 -> 1.12 -> 1) when items are added to cart.
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
    return () => {
      if (searchCloseTimerRef.current !== null) window.clearTimeout(searchCloseTimerRef.current);
      if (drawerCloseTimerRef.current !== null) window.clearTimeout(drawerCloseTimerRef.current);
    };
  }, []);

  // Close overlays on navigation without stealing focus back to header buttons.
  useEffect(() => {
    searchRestoreFocusRef.current = false;
    drawerRestoreFocusRef.current = false;
    setDrawerOpen(false);
    setDrawerClosing(false);
    setSearchState("closed");
  }, [pathname]);

  const closeSearch = useCallback(
    (restoreFocus = true) => {
      if (searchState !== "open") return;
      searchRestoreFocusRef.current = restoreFocus;
      setSearchState("closing");
      searchCloseTimerRef.current = window.setTimeout(() => {
        searchCloseTimerRef.current = null;
        setSearchState("closed");
      }, 180);
    },
    [searchState]
  );

  const closeDrawer = useCallback(
    (restoreFocus = true) => {
      if (!drawerOpen || drawerClosing) return;
      drawerRestoreFocusRef.current = restoreFocus;
      setDrawerClosing(true);
      drawerCloseTimerRef.current = window.setTimeout(() => {
        drawerCloseTimerRef.current = null;
        setDrawerOpen(false);
        setDrawerClosing(false);
      }, 180);
    },
    [drawerOpen, drawerClosing]
  );

  const openSearchOverlay = () => {
    if (searchCloseTimerRef.current !== null) {
      window.clearTimeout(searchCloseTimerRef.current);
      searchCloseTimerRef.current = null;
    }
    searchRestoreFocusRef.current = true;
    setSearchState("open");
  };

  const openDrawer = () => {
    if (drawerCloseTimerRef.current !== null) {
      window.clearTimeout(drawerCloseTimerRef.current);
      drawerCloseTimerRef.current = null;
    }
    drawerRestoreFocusRef.current = true;
    setDrawerClosing(false);
    setDrawerOpen(true);
  };

  const resolveSearchInitialFocus = useCallback((container: HTMLElement): HTMLElement | null => {
    return container.querySelector<HTMLElement>('input[type="search"], input[role="combobox"]');
  }, []);

  useModalFocusTrap({
    open: searchState === "open",
    containerRef: overlayRef,
    resolveInitialFocus: resolveSearchInitialFocus,
    onEscape: () => closeSearch(true),
    shouldRestoreFocusRef: searchRestoreFocusRef,
  });

  useModalFocusTrap({
    open: drawerOpen && !drawerClosing,
    containerRef: drawerPanelRef,
    onEscape: () => closeDrawer(true),
    shouldRestoreFocusRef: drawerRestoreFocusRef,
  });

  const cartBadge =
    cartCount > 0 ? (
      <span
        className={`${styles.headerCartCount} ${badgePop ? styles.headerCartCountPop : ""} num`}
      >
        {cartCount}
      </span>
    ) : null;

  return (
    <>
      <div className={styles.headerActionsDesktop}>
        <button
          type="button"
          className={styles.headerIconBtn}
          onClick={openSearchOverlay}
          aria-expanded={searchState === "open"}
          aria-label={`Tìm kiếm tại ${brandName}`}
        >
          <IconSearch size={18} />
        </button>

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
          className={styles.headerCartBtn}
          aria-label={cartCount > 0 ? `Giỏ hàng, ${cartCount} sản phẩm` : "Giỏ hàng"}
        >
          <IconBag size={18} />
          <span className={styles.headerCartLabel}>Giỏ</span>
          {cartBadge}
        </Link>
      </div>

      <div className={styles.headerActionsMobile}>
        <button
          type="button"
          className={styles.headerIconBtn}
          onClick={openSearchOverlay}
          aria-expanded={searchState === "open"}
          aria-label={`Tìm kiếm tại ${brandName}`}
        >
          <IconSearch size={18} />
        </button>

        <Link
          href="/gio-hang"
          className={styles.headerCartBtnCompact}
          aria-label={cartCount > 0 ? `Giỏ hàng, ${cartCount} sản phẩm` : "Giỏ hàng"}
        >
          <IconBag size={18} />
          {cartBadge}
        </Link>

        <button
          type="button"
          className={styles.mobileMenuBtn}
          onClick={openDrawer}
          aria-expanded={drawerOpen}
          aria-label="Mở trình đơn cửa hàng"
        >
          <IconMenu size={18} />
        </button>
      </div>

      {searchState !== "closed" && (
        <div
          ref={overlayRef}
          tabIndex={-1}
          className={`${styles.searchOverlay} ${
            searchState === "closing" ? styles.searchOverlayClosing : ""
          }`}
          role="dialog"
          aria-modal="true"
          aria-label={`Tìm kiếm sản phẩm tại ${brandName}`}
          onClick={(e) => {
            if (e.target === e.currentTarget) closeSearch(true);
          }}
        >
          <div className={styles.searchOverlayInner}>
            <div className={styles.searchOverlayTop}>
              <span className={styles.searchOverlayBrand}>{brandName}</span>
              <button
                type="button"
                className={styles.siteDrawerClose}
                onClick={() => closeSearch(true)}
                aria-label="Đóng ô tìm kiếm"
              >
                <IconClose size={20} />
              </button>
            </div>

            <div className={styles.searchOverlayBody}>
              <p className={styles.searchOverlayPrompt}>Tìm trong {brandName}</p>
              {showSearchSlot ? (
                <SiteSearchBar
                  brandName={brandName}
                  shopId={shopId}
                  placeholder="Tên mẫu, chất liệu, màu sắc..."
                  onNavigate={() => closeSearch(false)}
                />
              ) : (
                <form
                  action="/tim-kiem"
                  method="get"
                  role="search"
                  className={styles.siteDrawerSearch}
                  onSubmit={() => closeSearch(false)}
                >
                  <label htmlFor="site-overlay-search" className="sr-only">
                    Tìm sản phẩm trong {brandName}
                  </label>
                  <input
                    id="site-overlay-search"
                    name="q"
                    type="search"
                    placeholder="Tên mẫu, chất liệu..."
                    className={styles.siteDrawerSearchInput}
                    enterKeyHint="search"
                  />
                  <button type="submit" className={styles.siteDrawerSearchBtn}>
                    Tìm
                  </button>
                </form>
              )}

              <div className={styles.searchOverlayLinks}>
                <Link
                  href="/san-pham"
                  className={styles.searchQuickLink}
                  onClick={() => closeSearch(false)}
                >
                  Tất cả sản phẩm
                </Link>
                <Link
                  href="/flash-sale"
                  className={styles.searchQuickLink}
                  onClick={() => closeSearch(false)}
                >
                  Flash sale
                </Link>
                <Link
                  href="/voucher"
                  className={styles.searchQuickLink}
                  onClick={() => closeSearch(false)}
                >
                  Mã giảm giá
                </Link>
              </div>
            </div>
          </div>
        </div>
      )}

      {drawerOpen && (
        <div
          className={`${styles.siteDrawerBackdrop} ${
            drawerClosing ? styles.siteDrawerBackdropClosing : ""
          }`}
          role="presentation"
          onClick={(e) => {
            if (e.target === e.currentTarget) closeDrawer(true);
          }}
        >
          <div
            ref={drawerPanelRef}
            tabIndex={-1}
            className={`${styles.siteDrawerPanel} ${
              drawerClosing ? styles.siteDrawerPanelClosing : ""
            }`}
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
                onClick={() => closeDrawer(true)}
                aria-label="Đóng trình đơn"
              >
                <IconClose size={18} />
              </button>
            </div>

            <nav className={styles.siteDrawerNav} aria-label="Điều hướng di động">
              {SITE_NAV_ITEMS.map((item) => (
                <Link
                  key={item.key}
                  href={item.href}
                  className={`${styles.siteDrawerLink} ${isActive(pathname, item.href) ? styles.siteDrawerLinkActive : ""}`}
                  onClick={() => closeDrawer(false)}
                >
                  {item.label}
                </Link>
              ))}
              <Link
                href="/chinh-sach"
                className={`${styles.siteDrawerLink} ${isActive(pathname, "/chinh-sach") ? styles.siteDrawerLinkActive : ""}`}
                onClick={() => closeDrawer(false)}
              >
                Chính sách
              </Link>
            </nav>

            <div className={styles.siteDrawerActions}>
              {isAuthenticated && user ? (
                <>
                  <Link
                    href="/don-hang"
                    className={styles.siteDrawerPrimary}
                    onClick={() => closeDrawer(false)}
                  >
                    Đơn hàng
                  </Link>
                  <Link
                    href="/tai-khoan"
                    className={styles.siteDrawerSecondary}
                    onClick={() => closeDrawer(false)}
                  >
                    Tài khoản
                  </Link>
                </>
              ) : (
                <>
                  <Link
                    href={loginHref}
                    className={styles.siteDrawerPrimary}
                    onClick={() => closeDrawer(false)}
                  >
                    Đăng nhập
                  </Link>
                  <Link
                    href="/dang-ky"
                    className={styles.siteDrawerSecondary}
                    onClick={() => closeDrawer(false)}
                  >
                    Tạo tài khoản
                  </Link>
                </>
              )}
              <Link
                href="/tra-cuu-don-hang"
                className={styles.siteDrawerSecondary}
                onClick={() => closeDrawer(false)}
              >
                Tra cứu đơn
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
