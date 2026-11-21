"use client";

import React, { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useSession } from "@/components/SessionProvider";
import { IconChevronDown, IconClose, IconMenu, IconUser, NexusMark } from "@/components/Icons";
import styles from "./PlatformHeader.module.css";

interface NavItem {
  label: string;
  href: string;
  matchPath?: string;
}

const NAV_ITEMS: readonly NavItem[] = [
  { label: "Danh bạ gian hàng", href: "/#danh-ba-gian-hang" },
  { label: "Dành cho người bán", href: "/#danh-cho-nguoi-ban" },
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
  const headerRef = useRef<HTMLElement>(null);

  const isSeller = user?.role === "seller";
  const isAdmin = user?.role === "admin" || user?.role === "super_admin";
  const displayName = user ? shortName(user.fullName, user.email) : "";

  useEffect(() => {
    setDrawerOpen(false);
    setUserMenuOpen(false);
  }, [pathname]);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setDrawerOpen(false);
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

  return (
    <>
      <a href="#main-content" className={styles.skipLink} onClick={handleSkipToMain}>
        Bỏ qua đến nội dung
      </a>

      <header ref={headerRef} className={styles.headerShell}>
        <div className={styles.inner}>
          <div className={styles.leftCluster}>
            <Link href="/" className={styles.brand} aria-label="NexusMart trang chủ">
              <NexusMark size={22} />
              <span className={styles.brandWord}>NexusMart</span>
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
          </div>

          <div className={styles.actionsDesktop}>
            {isAuthenticated && user ? (
              <>
                {isSeller && (
                  <Link href="/seller" className={styles.workspaceLink}>
                    Kênh người bán
                  </Link>
                )}
                {isAdmin && (
                  <Link href="/admin" className={styles.workspaceLink}>
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
                    <IconUser size={16} />
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
              onClick={() => setDrawerOpen(true)}
              aria-expanded={drawerOpen}
              aria-label="Mở trình đơn điều hướng"
            >
              <IconMenu size={18} />
              <span>Menu</span>
            </button>
          </div>
        </div>
      </header>

      {drawerOpen && (
        <div
          className={styles.drawerBackdrop}
          role="presentation"
          onClick={(e) => {
            if (e.target === e.currentTarget) setDrawerOpen(false);
          }}
        >
          <div
            className={styles.drawerPanel}
            data-ui="platform-drawer"
            role="dialog"
            aria-modal="true"
            aria-label="Điều hướng nền tảng NexusMart"
          >
            <div className={styles.drawerHead}>
              <Link href="/" className={styles.brand} onClick={() => setDrawerOpen(false)}>
                <NexusMark size={20} />
                <span className={styles.brandWord}>NexusMart</span>
              </Link>
              <button
                type="button"
                className={styles.drawerClose}
                onClick={() => setDrawerOpen(false)}
                aria-label="Đóng trình đơn"
              >
                <IconClose size={18} />
              </button>
            </div>

            <nav className={styles.drawerNav} aria-label="Điều hướng di động">
              {NAV_ITEMS.map((item) => (
                <Link
                  key={item.href}
                  href={item.href}
                  className={styles.drawerLink}
                  onClick={() => setDrawerOpen(false)}
                >
                  {item.label}
                </Link>
              ))}
            </nav>

            <div className={styles.drawerActions}>
              {isAuthenticated && user ? (
                <>
                  <p className={styles.drawerUserMeta}>
                    Đang đăng nhập: <strong>{user.email}</strong>
                  </p>
                  {isSeller && (
                    <Link
                      href="/seller"
                      className={styles.drawerPrimary}
                      onClick={() => setDrawerOpen(false)}
                    >
                      Vào Kênh người bán
                    </Link>
                  )}
                  {isAdmin && (
                    <Link
                      href="/admin"
                      className={styles.drawerPrimary}
                      onClick={() => setDrawerOpen(false)}
                    >
                      Vào Cổng quản trị
                    </Link>
                  )}
                  <button
                    type="button"
                    className={styles.drawerSecondary}
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
                    className={styles.drawerPrimary}
                    onClick={() => setDrawerOpen(false)}
                  >
                    Mở gian hàng
                  </Link>
                  <Link
                    href="/dang-nhap"
                    className={styles.drawerSecondary}
                    onClick={() => setDrawerOpen(false)}
                  >
                    Đăng nhập
                  </Link>
                </>
              )}
            </div>
          </div>
        </div>
      )}
    </>
  );
}
