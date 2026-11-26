"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useSession } from "@/components/SessionProvider";
import { IconChevronDown, IconClose, IconMenu, IconUser, NexusMark } from "@/components/Icons";
import { useModalFocusTrap } from "@/components/modalFocus";
import styles from "./PlatformHeader.module.css";

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

  const headerRef = useRef<HTMLElement>(null);
  const mobileSheetRef = useRef<HTMLDivElement | null>(null);
  const drawerRestoreFocusRef = useRef(true);
  const lastScrollY = useRef(0);

  const isSeller = user?.role === "seller";
  const isAdmin = user?.role === "admin" || user?.role === "super_admin";
  const displayName = user ? shortName(user.fullName, user.email) : "";
  const isHome = pathname === "/";

  const openDrawer = useCallback(() => {
    drawerRestoreFocusRef.current = true;
    setDrawerOpen(true);
  }, []);

  const blurInsideSheet = useCallback(() => {
    const sheet = mobileSheetRef.current;
    const active = typeof document !== "undefined" ? document.activeElement : null;
    if (sheet && active instanceof HTMLElement && sheet.contains(active)) {
      active.blur();
    }
  }, []);

  const closeDrawer = useCallback(
    (restoreFocus = true) => {
      drawerRestoreFocusRef.current = restoreFocus;
      if (!restoreFocus) {
        blurInsideSheet();
      }
      setDrawerOpen(false);
    },
    [blurInsideSheet]
  );

  useEffect(() => {
    drawerRestoreFocusRef.current = false;
    blurInsideSheet();
    setDrawerOpen(false);
    setUserMenuOpen(false);
    setHidden(false);
  }, [pathname, blurInsideSheet]);

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

  useEffect(() => {
    if (!drawerOpen) return;
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prevOverflow;
    };
  }, [drawerOpen]);

  useModalFocusTrap({
    open: drawerOpen,
    containerRef: mobileSheetRef,
    onEscape: () => closeDrawer(true),
    shouldRestoreFocusRef: drawerRestoreFocusRef,
  });

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setUserMenuOpen(false);
      }
    };
    const onPointerDown = (e: MouseEvent) => {
      if (headerRef.current && !headerRef.current.contains(e.target as Node)) {
        setUserMenuOpen(false);
      }
    };
    document.addEventListener("keydown", onKeyDown);
    document.addEventListener("mousedown", onPointerDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.removeEventListener("mousedown", onPointerDown);
    };
  }, []);

  const isActive = (matchPath?: string) =>
    Boolean(matchPath && (pathname === matchPath || pathname.startsWith(`${matchPath}/`)));

  const handleLogout = async () => {
    setUserMenuOpen(false);
    closeDrawer(false);
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
    <>
      <a href="#main-content" className={styles.skipLink} onClick={handleSkipToMain}>
        Bỏ qua đến nội dung
      </a>

      <header
        ref={headerRef}
        className={styles.headerShell}
        data-surface={surfaceState}
        data-home={isHome ? "true" : undefined}
        data-hidden={hidden && !drawerOpen ? "true" : "false"}
      >
        <div className={styles.inner}>
          <Link href="/" className={styles.brand} aria-label="NexusMart trang chủ">
            <NexusMark
              size={isHome ? 22 : 20}
              accentColor={isHome ? "var(--home-accent)" : undefined}
            />
            <span className={`${styles.brandWord}${isHome ? "" : " type-wide"}`}>NexusMart</span>
          </Link>

          <nav className={styles.navDesktop} aria-label="Điều hướng nền tảng">
            {NAV_ITEMS.map((item) => (
              <Link
                key={item.href}
                href={item.href}
                className={`${styles.navLink} ${isActive(item.matchPath) ? styles.navLinkActive : ""}`}
              >
                {item.label}
              </Link>
            ))}
          </nav>

          <div className={styles.actionsDesktop}>
            {isAuthenticated && user ? (
              <>
                {isSeller && (
                  <Link href="/seller" className={styles.loginLink}>
                    Kênh người bán
                  </Link>
                )}
                {isAdmin && (
                  <Link href="/admin" className={styles.loginLink}>
                    Cổng quản trị
                  </Link>
                )}

                <div className={styles.userMenuWrap}>
                  <button
                    type="button"
                    className={`${styles.accountBtn} ${userMenuOpen ? styles.accountBtnActive : ""}`}
                    aria-expanded={userMenuOpen}
                    aria-haspopup="menu"
                    aria-controls="platform-user-menu"
                    onClick={() => setUserMenuOpen((v) => !v)}
                  >
                    <IconUser size={15} />
                    <span className={styles.userName}>{displayName}</span>
                    <IconChevronDown
                      size={13}
                      className={userMenuOpen ? styles.chevronOpen : styles.chevron}
                    />
                  </button>

                  {userMenuOpen && (
                    <div
                      id="platform-user-menu"
                      className={styles.userDropdown}
                      data-ui="platform-user-menu"
                      role="menu"
                      aria-label="Tài khoản của bạn"
                    >
                      <div className={styles.dropdownHead}>
                        <p className={styles.dropdownName}>{user.fullName || "Tài khoản"}</p>
                        <p className={styles.dropdownEmail}>{user.email}</p>
                      </div>

                      <div className={styles.dropdownList}>
                        {isSeller && (
                          <Link
                            href="/seller"
                            role="menuitem"
                            className={styles.dropdownLink}
                            onClick={() => setUserMenuOpen(false)}
                          >
                            Kênh người bán
                          </Link>
                        )}
                        {isAdmin && (
                          <Link
                            href="/admin"
                            role="menuitem"
                            className={styles.dropdownLink}
                            onClick={() => setUserMenuOpen(false)}
                          >
                            Cổng quản trị
                          </Link>
                        )}
                        <Link
                          href="/#danh-ba-gian-hang"
                          role="menuitem"
                          className={styles.dropdownLink}
                          onClick={() => setUserMenuOpen(false)}
                        >
                          Danh bạ gian hàng
                        </Link>
                      </div>

                      <div className={styles.dropdownFoot}>
                        <button
                          type="button"
                          role="menuitem"
                          className={styles.logoutBtn}
                          onClick={handleLogout}
                          disabled={sessionLoading}
                        >
                          {sessionLoading ? "Đang đăng xuất..." : "Đăng xuất"}
                        </button>
                      </div>
                    </div>
                  )}
                </div>

                {!isSeller && !isAdmin && (
                  <Link href="/dang-ky?role=seller" className={styles.ctaPrimary}>
                    Mở gian hàng
                  </Link>
                )}
              </>
            ) : (
              <>
                <Link
                  href="/dang-nhap"
                  className={`${styles.loginLink} ${isActive("/dang-nhap") ? styles.navLinkActive : ""}`}
                >
                  Đăng nhập
                </Link>
                <Link href="/dang-ky?role=seller" className={styles.ctaPrimary}>
                  Mở gian hàng
                </Link>
              </>
            )}
          </div>

          <div className={styles.mobileCluster}>
            <Link href="/dang-ky?role=seller" className={styles.ctaMobile}>
              Mở gian hàng
            </Link>
            <button
              type="button"
              className={styles.menuBtn}
              onClick={openDrawer}
              aria-expanded={drawerOpen}
              aria-controls="platform-mobile-sheet"
              aria-label="Mở trình đơn điều hướng"
            >
              <IconMenu size={18} />
            </button>
          </div>
        </div>
      </header>

      <div
        ref={mobileSheetRef}
        id="platform-mobile-sheet"
        tabIndex={-1}
        className={styles.sheet}
        data-open={drawerOpen ? "true" : "false"}
        data-home={isHome ? "true" : undefined}
        role="dialog"
        aria-modal="true"
        aria-hidden={!drawerOpen}
        inert={!drawerOpen || undefined}
        aria-label="Điều hướng nền tảng NexusMart"
      >
        <div className={styles.sheetInner}>
          <div className={styles.sheetHead}>
            <Link
              href="/"
              className={styles.sheetBrand}
              tabIndex={drawerOpen ? 0 : -1}
              onClick={() => closeDrawer(false)}
            >
              <NexusMark size={20} accentColor="var(--band-dark-accent)" />
              <span className={`${styles.brandWord} type-wide`}>NexusMart</span>
            </Link>
            <button
              type="button"
              className={styles.sheetClose}
              tabIndex={drawerOpen ? 0 : -1}
              onClick={() => closeDrawer(true)}
              aria-label="Đóng trình đơn"
            >
              <IconClose size={20} />
            </button>
          </div>

          <nav className={styles.sheetNav} aria-label="Điều hướng di động">
            {NAV_ITEMS.map((item, idx) => (
              <Link
                key={item.href}
                href={item.href}
                tabIndex={drawerOpen ? 0 : -1}
                className={`${styles.sheetLink} type-wide`}
                style={{ "--i": idx } as React.CSSProperties}
                onClick={() => closeDrawer(false)}
              >
                {item.label}
              </Link>
            ))}
          </nav>

          <div className={styles.sheetActions}>
            {isAuthenticated && user ? (
              <>
                <p className={styles.sheetUserMeta}>{user.email}</p>
                {isSeller && (
                  <Link
                    href="/seller"
                    tabIndex={drawerOpen ? 0 : -1}
                    className={styles.sheetPrimary}
                    onClick={() => closeDrawer(false)}
                  >
                    Kênh người bán
                  </Link>
                )}
                {isAdmin && (
                  <Link
                    href="/admin"
                    tabIndex={drawerOpen ? 0 : -1}
                    className={styles.sheetPrimary}
                    onClick={() => closeDrawer(false)}
                  >
                    Cổng quản trị
                  </Link>
                )}
                <button
                  type="button"
                  tabIndex={drawerOpen ? 0 : -1}
                  className={styles.sheetSecondary}
                  onClick={handleLogout}
                  disabled={sessionLoading}
                >
                  {sessionLoading ? "Đang đăng xuất..." : "Đăng xuất"}
                </button>
              </>
            ) : (
              <>
                <Link
                  href="/dang-ky?role=seller"
                  tabIndex={drawerOpen ? 0 : -1}
                  className={styles.sheetPrimary}
                  onClick={() => closeDrawer(false)}
                >
                  Mở gian hàng
                </Link>
                <Link
                  href="/dang-nhap"
                  tabIndex={drawerOpen ? 0 : -1}
                  className={styles.sheetSecondary}
                  onClick={() => closeDrawer(false)}
                >
                  Đăng nhập
                </Link>
              </>
            )}
          </div>
        </div>
      </div>
    </>
  );
}
