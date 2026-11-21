"use client";

import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import {
  IconArrowUpRight,
  IconClose,
  IconDashboard,
  IconFileText,
  IconFlag,
  IconHome,
  IconMenu,
  IconSearch,
  IconShield,
  IconSliders,
  IconStore,
  IconTag,
  IconWallet,
  NexusMark,
} from "@/components/Icons";
import type { SessionUser } from "@/lib/server/session";
import styles from "@/app/(admin)/layout.module.css";

interface AdminContextValue {
  user: SessionUser;
  showToast: (message: string) => void;
}

const AdminContext = createContext<AdminContextValue | null>(null);

export function useAdminContext(): AdminContextValue {
  const ctx = useContext(AdminContext);
  if (!ctx) {
    throw new Error("useAdminContext phải được gọi bên trong AdminShell.");
  }
  return ctx;
}

interface AdminNavItem {
  href: string;
  label: string;
  icon: React.ReactNode;
  badge?: number;
  alertBadge?: boolean;
}

function getInitials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "AD";
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return `${parts[0][0] ?? ""}${parts[parts.length - 1][0] ?? ""}`.toUpperCase();
}

export interface AdminShellProps {
  user: SessionUser;
  pendingShopApprovals: number;
  pendingProductReviews: number;
  openComplaintsCount?: number;
  ledgerUnbalanced: boolean;
  children: React.ReactNode;
}

export function AdminShell({
  user,
  pendingShopApprovals,
  pendingProductReviews,
  openComplaintsCount = 0,
  ledgerUnbalanced,
  children,
}: AdminShellProps) {
  const pathname = usePathname();
  const router = useRouter();
  const searchRef = useRef<HTMLInputElement | null>(null);

  const [menuOpen, setMenuOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [toastMessage, setToastMessage] = useState<string | null>(null);

  const showToast = useCallback((message: string) => {
    setToastMessage(message);
  }, []);

  const dismissToast = useCallback(() => {
    setToastMessage(null);
  }, []);

  useEffect(() => {
    setMenuOpen(false);
  }, [pathname]);

  useEffect(() => {
    if (!toastMessage) return;
    const t = window.setTimeout(() => setToastMessage(null), 3600);
    return () => window.clearTimeout(t);
  }, [toastMessage]);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (
        e.key === "/" &&
        document.activeElement?.tagName !== "INPUT" &&
        document.activeElement?.tagName !== "TEXTAREA"
      ) {
        e.preventDefault();
        searchRef.current?.focus();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  const opsNavItems: AdminNavItem[] = [
    { href: "/admin", label: "Tổng quan", icon: <IconDashboard size={16} /> },
    {
      href: "/admin/gian-hang",
      label: "Duyệt gian hàng",
      icon: <IconStore size={16} />,
      badge: pendingShopApprovals > 0 ? pendingShopApprovals : undefined,
    },
    {
      href: "/admin/kiem-duyet",
      label: "Duyệt sản phẩm",
      icon: <IconShield size={16} />,
      badge: pendingProductReviews > 0 ? pendingProductReviews : undefined,
    },
    {
      href: "/admin/sites",
      label: "Site gian hàng",
      icon: <IconHome size={16} />,
    },
    {
      href: "/admin/flash-sale",
      label: "Flash sale",
      icon: <IconTag size={16} />,
    },
    {
      href: "/admin/khieu-nai",
      label: "Khiếu nại",
      icon: <IconFlag size={16} />,
      badge: openComplaintsCount > 0 ? openComplaintsCount : undefined,
    },
  ];

  const financeAuditNavItems: AdminNavItem[] = [
    {
      href: "/admin/doi-soat",
      label: "Đối soát & sổ cái",
      icon: <IconWallet size={16} />,
      badge: ledgerUnbalanced ? 1 : undefined,
      alertBadge: ledgerUnbalanced,
    },
    { href: "/admin/cau-hinh", label: "Cấu hình sàn", icon: <IconSliders size={16} /> },
    { href: "/admin/audit-log", label: "Nhật ký kiểm toán", icon: <IconFileText size={16} /> },
  ];

  const allNavItems = [...opsNavItems, ...financeAuditNavItems];

  const isActive = (href: string) =>
    href === "/admin"
      ? pathname === "/admin"
      : pathname === href || pathname.startsWith(`${href}/`);

  const activeTitle =
    allNavItems.find((item) => isActive(item.href))?.label ?? "Quản trị NexusMart";
  const displayName = user.fullName || user.email || "Quản trị viên";
  const initials = getInitials(displayName);

  const handleSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = query.trim();
    if (!trimmed) return;
    const lower = trimmed.toLowerCase();
    const upper = trimmed.toUpperCase();
    if (upper.startsWith("DS-")) {
      router.push(`/admin/doi-soat?q=${encodeURIComponent(trimmed)}`);
    } else if (upper.startsWith("KN-")) {
      router.push(`/admin/khieu-nai?q=${encodeURIComponent(trimmed)}`);
    } else if (lower.startsWith("admin.") || lower.startsWith("site.") || lower.startsWith("tr-")) {
      router.push(`/admin/audit-log?q=${encodeURIComponent(trimmed)}`);
    } else if (upper.startsWith("SP-") || upper.startsWith("SKU-") || upper.startsWith("NM-")) {
      router.push(`/admin/kiem-duyet?q=${encodeURIComponent(trimmed)}`);
    } else if (upper.startsWith("FS-")) {
      router.push(`/admin/flash-sale?q=${encodeURIComponent(trimmed)}`);
    } else if (lower.includes(".nexusmart") || lower.startsWith("site:")) {
      const siteQuery = lower.startsWith("site:") ? trimmed.slice(5).trim() : trimmed;
      router.push(`/admin/sites?q=${encodeURIComponent(siteQuery || trimmed)}`);
    } else {
      router.push(`/admin/gian-hang?q=${encodeURIComponent(trimmed)}`);
    }
    setMenuOpen(false);
  };

  return (
    <AdminContext.Provider value={{ user, showToast }}>
      <div className={styles.shell}>
        <div className={styles.cornerBrand}>
          <Link href="/admin" className={styles.topBarLeft}>
            <NexusMark size={18} />
            <span className={styles.brandName}>NexusMart</span>
            <span className={styles.brandScope}>Quản trị</span>
          </Link>
        </div>

        <header className={styles.topBar}>
          <div className={styles.topBarLeft}>
            <button
              type="button"
              className={styles.mobileMenuBtn}
              onClick={() => setMenuOpen(true)}
              aria-label="Mở trình đơn quản trị"
            >
              <IconMenu size={18} />
            </button>
            <span className={styles.mobileTitle}>{activeTitle}</span>
          </div>

          <form className={styles.searchForm} onSubmit={handleSearchSubmit} role="search">
            <span className={styles.searchIcon}>
              <IconSearch size={14} />
            </span>
            <input
              ref={searchRef}
              type="search"
              className={styles.searchInput}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Tìm gian hàng, sản phẩm, tên miền site, nhật ký..."
              aria-label="Tìm kiếm trong hệ thống quản trị"
            />
            <span className={styles.searchHint} aria-hidden="true">
              /
            </span>
          </form>

          <div className={styles.topBarRight}>
            <Link href="/admin/audit-log" className={styles.accountBtn}>
              <span className={styles.avatarChip} aria-hidden="true">
                {initials}
              </span>
              <span className={styles.accountName}>{displayName}</span>
            </Link>
          </div>
        </header>

        <aside
          className={`${styles.sidebar} ${menuOpen ? styles.sidebarOpen : ""}`}
          aria-label="Điều hướng Quản trị nền tảng"
        >
          <div className={styles.sheetHeader}>
            <span className={styles.topBarLeft}>
              <NexusMark size={18} />
              <span className={styles.brandName}>NexusMart Quản trị</span>
            </span>
            <button
              type="button"
              className={styles.mobileMenuBtn}
              onClick={() => setMenuOpen(false)}
              aria-label="Đóng trình đơn"
            >
              <IconClose size={18} />
            </button>
          </div>

          <form className={styles.mobileSearchWrap} onSubmit={handleSearchSubmit} role="search">
            <input
              type="search"
              className={styles.searchInput}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Tìm gian hàng, sản phẩm, nhật ký..."
              aria-label="Tìm kiếm quản trị"
            />
          </form>

          <nav className={styles.navGroup} aria-label="Phân hệ quản trị">
            {opsNavItems.map((item) => {
              const active = isActive(item.href);
              return (
                <Link
                  key={item.href}
                  href={item.href}
                  className={`${styles.navItem} ${active ? styles.navItemActive : ""}`}
                  aria-current={active ? "page" : undefined}
                >
                  {item.icon}
                  <span className={styles.navLabel}>{item.label}</span>
                  {item.badge != null && (
                    <span className={item.alertBadge ? styles.navCountAlert : styles.navCount}>
                      {item.badge}
                    </span>
                  )}
                </Link>
              );
            })}

            <span className={styles.groupLabel}>Tài chính &amp; kiểm toán</span>
            {financeAuditNavItems.map((item) => {
              const active = isActive(item.href);
              return (
                <Link
                  key={item.href}
                  href={item.href}
                  className={`${styles.navItem} ${active ? styles.navItemActive : ""}`}
                  aria-current={active ? "page" : undefined}
                >
                  {item.icon}
                  <span className={styles.navLabel}>{item.label}</span>
                  {item.badge != null && (
                    <span className={item.alertBadge ? styles.navCountAlert : styles.navCount}>
                      {item.badge}
                    </span>
                  )}
                </Link>
              );
            })}
          </nav>

          <div className={styles.bottom}>
            <span className={styles.groupLabel}>Chuyển khu vực</span>
            <Link href="/" className={styles.navItem}>
              <IconArrowUpRight size={16} />
              <span className={styles.navLabel}>Trang chủ NexusMart</span>
            </Link>
          </div>
        </aside>

        <main className={styles.sheet}>{children}</main>

        {toastMessage && (
          <div className={styles.toast} role="status" aria-live="polite">
            <span>{toastMessage}</span>
            <button type="button" className={styles.toastClose} onClick={dismissToast}>
              Đóng
            </button>
          </div>
        )}
      </div>
    </AdminContext.Provider>
  );
}
