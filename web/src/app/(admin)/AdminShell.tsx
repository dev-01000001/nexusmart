"use client";

import React, { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useMockStore } from "@/mock/MockStoreContext";
import type {
  GatewayAdminApprovalsResponse,
  GatewayLedgerSummary,
  GatewayProduct,
  GatewayReturnRequest,
  GatewayShop,
  GatewayUser,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api } from "@/lib/api";
import { canAccessAdminPath, getUserRoleLabel, roleHasCapability } from "@/lib/admin-rbac";
import { StateView } from "@/components";
import {
  IconAlert,
  IconArrowUpRight,
  IconClose,
  IconDashboard,
  IconFileText,
  IconMenu,
  IconSearch,
  IconShield,
  IconSliders,
  IconStore,
  IconTag,
  IconWallet,
  NexusMark,
} from "@/components/Icons";
import { AdminUserProvider } from "@/features/admin/AdminUserContext";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./layout.module.css";

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
  return `${parts[parts.length - 2][0] ?? ""}${parts[parts.length - 1][0] ?? ""}`.toUpperCase();
}

export function AdminShell({ user, children }: { user: GatewayUser; children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const searchRef = useRef<HTMLInputElement | null>(null);
  const { toastMessage, dismissToast } = useMockStore();
  const [menuOpen, setMenuOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [pendingShopsCount, setPendingShopsCount] = useState(0);
  const [pendingProductsCount, setPendingProductsCount] = useState(0);
  const [openReturnsCount, setOpenReturnsCount] = useState(0);
  const [ledgerUnbalanced, setLedgerUnbalanced] = useState(false);
  const [pendingApprovalsCount, setPendingApprovalsCount] = useState(0);

  const canReadShops = roleHasCapability(user.role, "shops.read");
  const canReadProducts = roleHasCapability(user.role, "products.read");
  const canReadReturns = roleHasCapability(user.role, "returns.read");
  const canReadLedger = roleHasCapability(user.role, "ledger.read");
  const canReadApprovals = roleHasCapability(user.role, "approvals.read");
  const hasPathAccess = canAccessAdminPath(user.role, pathname);

  useEffect(() => {
    setMenuOpen(false);
  }, [pathname]);

  useEffect(() => {
    let active = true;
    async function loadBadges() {
      const [shopsRes, prodsRes, returnsRes, ledgerRes, approvalsRes] = await Promise.allSettled([
        canReadShops
          ? api<PaginatedResponse<GatewayShop>>("admin/shops?status=PENDING&page=1&page_size=1")
          : Promise.resolve(null),
        canReadProducts
          ? api<PaginatedResponse<GatewayProduct>>(
              "admin/products?status=PENDING&page=1&page_size=1"
            )
          : Promise.resolve(null),
        canReadReturns
          ? api<PaginatedResponse<GatewayReturnRequest>>(
              "admin/returns?status=ESCALATED&page=1&page_size=1"
            )
          : Promise.resolve(null),
        canReadLedger ? api<GatewayLedgerSummary>("admin/ledger/summary") : Promise.resolve(null),
        canReadApprovals
          ? api<GatewayAdminApprovalsResponse>("admin/approvals?status=PENDING&page=1&page_size=1")
          : Promise.resolve(null),
      ]);
      if (!active) return;
      if (shopsRes.status === "fulfilled" && shopsRes.value) {
        setPendingShopsCount(shopsRes.value.total ?? shopsRes.value.items.length);
      }
      if (prodsRes.status === "fulfilled" && prodsRes.value) {
        setPendingProductsCount(prodsRes.value.total ?? prodsRes.value.items.length);
      }
      if (returnsRes.status === "fulfilled" && returnsRes.value) {
        setOpenReturnsCount(returnsRes.value.total ?? returnsRes.value.items.length);
      }
      if (ledgerRes.status === "fulfilled" && ledgerRes.value) {
        setLedgerUnbalanced(!ledgerRes.value.is_balanced);
      }
      if (approvalsRes.status === "fulfilled" && approvalsRes.value) {
        const shopPending =
          approvalsRes.value.shop_total ?? approvalsRes.value.shop_approvals.length;
        const refundPending =
          approvalsRes.value.refund_total ?? approvalsRes.value.refund_approvals.length;
        setPendingApprovalsCount(shopPending + refundPending);
      }
    }
    void loadBadges();
    return () => {
      active = false;
    };
  }, [pathname, canReadShops, canReadProducts, canReadReturns, canReadLedger, canReadApprovals]);

  useEffect(() => {
    if (!toastMessage) return;
    const t = window.setTimeout(() => dismissToast(), 3400);
    return () => window.clearTimeout(t);
  }, [toastMessage, dismissToast]);

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
      badge: pendingShopsCount > 0 ? pendingShopsCount : undefined,
    },
    {
      href: "/admin/kiem-duyet",
      label: "Kiểm duyệt mở",
      icon: <IconShield size={16} />,
      badge: pendingProductsCount > 0 ? pendingProductsCount : undefined,
    },
    {
      href: "/admin/khieu-nai",
      label: "Khiếu nại mở",
      icon: <IconAlert size={16} />,
      badge: openReturnsCount > 0 ? openReturnsCount : undefined,
    },
    {
      href: "/admin/doi-soat",
      label: "Đối soát thanh toán",
      icon: <IconWallet size={16} />,
      badge: ledgerUnbalanced ? 1 : undefined,
      alertBadge: ledgerUnbalanced,
    },
  ].filter((item) => canAccessAdminPath(user.role, item.href));

  const platformNavItems: AdminNavItem[] = [
    { href: "/admin/thanh-vien", label: "Khuyến mãi & Xu", icon: <IconTag size={16} /> },
    {
      href: "/admin/cau-hinh",
      label: "Cấu hình sàn",
      icon: <IconSliders size={16} />,
      badge: pendingApprovalsCount > 0 ? pendingApprovalsCount : undefined,
    },
    { href: "/admin/audit-log", label: "Nhật ký hệ thống", icon: <IconFileText size={16} /> },
  ].filter((item) => canAccessAdminPath(user.role, item.href));

  const allNavItems = [...opsNavItems, ...platformNavItems];

  const isActive = (href: string) =>
    href === "/admin"
      ? pathname === "/admin"
      : pathname === href || pathname.startsWith(`${href}/`);

  const activeTitle = allNavItems.find((item) => isActive(item.href))?.label ?? "Quản trị sàn";

  const handleSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = query.trim();
    if (!trimmed) return;
    const upper = trimmed.toUpperCase();
    let target = `/admin/gian-hang?q=${encodeURIComponent(trimmed)}`;
    if (upper.startsWith("KN-") && canAccessAdminPath(user.role, "/admin/khieu-nai")) {
      target = `/admin/khieu-nai?q=${encodeURIComponent(trimmed)}`;
    } else if (upper.startsWith("DS-") && canAccessAdminPath(user.role, "/admin/doi-soat")) {
      target = `/admin/doi-soat?q=${encodeURIComponent(trimmed)}`;
    } else if (trimmed.toLowerCase().startsWith("tr-")) {
      target = `/admin/audit-log?q=${encodeURIComponent(trimmed)}`;
    } else if (
      (upper.startsWith("DG-") || upper.startsWith("QA-") || upper.startsWith("MD-")) &&
      canAccessAdminPath(user.role, "/admin/kiem-duyet")
    ) {
      target = `/admin/kiem-duyet?q=${encodeURIComponent(trimmed)}`;
    } else if (
      (upper.startsWith("NXGIFT") || upper.startsWith("FS-") || upper.startsWith("SV-")) &&
      canAccessAdminPath(user.role, "/admin/thanh-vien")
    ) {
      target = `/admin/thanh-vien?q=${encodeURIComponent(trimmed)}`;
    } else if (
      (upper.startsWith("BV-") || upper.startsWith("NH-") || upper.startsWith("DM-")) &&
      canAccessAdminPath(user.role, "/admin/noi-dung")
    ) {
      target = `/admin/noi-dung?q=${encodeURIComponent(trimmed)}`;
    }
    router.push(target);
    setMenuOpen(false);
  };

  const displayName = user.full_name || user.email;
  const roleDisplayLabel = getUserRoleLabel(user.role);

  return (
    <AdminUserProvider user={user}>
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
              placeholder="Tìm gian hàng, sản phẩm, khiếu nại KN-..."
              aria-label="Tìm kiếm trong hệ thống quản trị"
            />
            <span className={styles.searchHint} aria-hidden="true">
              /
            </span>
          </form>

          <div className={styles.topBarRight}>
            <Link href="/admin/audit-log" className={styles.accountBtn}>
              <span className={styles.avatarChip} aria-hidden="true">
                {getInitials(displayName)}
              </span>
              <span className={styles.accountName}>
                {displayName} · {roleDisplayLabel}
              </span>
            </Link>
          </div>
        </header>

        <aside
          className={`${styles.sidebar} ${menuOpen ? styles.sidebarOpen : ""}`}
          aria-label="Điều hướng Quản trị sàn"
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
              placeholder="Tìm gian hàng, sản phẩm, mã..."
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

            <span className={styles.groupLabel}>Quản trị sàn</span>
            {platformNavItems.map((item) => {
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
              <span className={styles.navLabel}>Cửa hàng người mua</span>
            </Link>
          </div>
        </aside>

        <main className={styles.sheet}>
          {hasPathAccess ? (
            children
          ) : (
            <div className={shared.page}>
              <header className={shared.titleRow}>
                <div className={shared.titleBlock}>
                  <h1 className={shared.pageTitle}>Không đủ quyền truy cập</h1>
                  <p className={shared.pageSubtitle}>
                    Vai trò hiện tại ({roleDisplayLabel}) không được phân quyền truy cập mục này.
                  </p>
                </div>
              </header>
              <div className={shared.stateWrap}>
                <StateView
                  kind="error"
                  surface="app"
                  title="Không đủ quyền truy cập phân hệ này"
                  message="Vui lòng quay lại Tổng quan hoặc chọn các phân hệ thuộc phạm vi công việc của bạn trên thanh điều hướng."
                  actionLabel="Về Tổng quan Quản trị"
                  actionHref="/admin"
                />
              </div>
            </div>
          )}
        </main>

        {toastMessage && (
          <div className={styles.toast} role="status" aria-live="polite">
            <span>{toastMessage}</span>
            <button type="button" className={styles.toastClose} onClick={dismissToast}>
              Đóng
            </button>
          </div>
        )}
      </div>
    </AdminUserProvider>
  );
}
