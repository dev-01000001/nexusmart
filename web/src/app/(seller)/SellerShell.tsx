"use client";

import React, { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useMockStore } from "@/mock/MockStoreContext";
import type { GatewayUser } from "@/lib/gateway-types";
import {
  IconArrowUpRight,
  IconClose,
  IconDashboard,
  IconMenu,
  IconPackage,
  IconRefresh,
  IconSliders,
  IconStore,
  IconTag,
  IconWallet,
  NexusMark,
} from "@/components/Icons";
import { SellerProvider, useSellerContext } from "@/features/seller/SellerContext";
import { getShopStatusMeta } from "@/features/seller/types";
import styles from "./layout.module.css";

interface SellerNavItem {
  href: string;
  label: string;
  icon: React.ReactNode;
  badge?: number;
}

function SellerShellInner({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const { user, shop, shopState, pendingOrdersCount, pendingReturnsCount } = useSellerContext();
  const { toastMessage, dismissToast } = useMockStore();
  const [menuOpen, setMenuOpen] = useState(false);

  useEffect(() => {
    setMenuOpen(false);
  }, [pathname]);

  useEffect(() => {
    if (!toastMessage) return;
    const t = window.setTimeout(() => dismissToast(), 3400);
    return () => window.clearTimeout(t);
  }, [toastMessage, dismissToast]);

  const navItems: SellerNavItem[] = [
    { href: "/seller", label: "Tổng quan", icon: <IconDashboard size={16} /> },
    { href: "/seller/san-pham", label: "Sản phẩm", icon: <IconPackage size={16} /> },
    {
      href: "/seller/don-hang",
      label: "Đơn chờ duyệt",
      icon: <IconStore size={16} />,
      badge: pendingOrdersCount > 0 ? pendingOrdersCount : undefined,
    },
    {
      href: "/seller/tra-hang",
      label: "Trả hàng & hoàn tiền",
      icon: <IconRefresh size={16} />,
      badge: pendingReturnsCount > 0 ? pendingReturnsCount : undefined,
    },
    { href: "/seller/khuyen-mai", label: "Khuyến mãi", icon: <IconTag size={16} /> },
    { href: "/seller/ho-so", label: "Hồ sơ & Vận hành", icon: <IconSliders size={16} /> },
    { href: "/seller/tai-chinh", label: "Tài chính", icon: <IconWallet size={16} /> },
  ];

  const isActive = (href: string) =>
    href === "/seller"
      ? pathname === "/seller"
      : pathname === href || pathname.startsWith(`${href}/`);

  const activeTitle = navItems.find((item) => isActive(item.href))?.label ?? "Kênh người bán";

  const shopDisplayName =
    shop?.name ?? (shopState === "loading" ? "Đang tải gian hàng..." : "Chưa liên kết gian hàng");
  const shopStatusMeta = shop ? getShopStatusMeta(shop.status) : null;

  return (
    <div className={styles.shell}>
      <header className={styles.mobileBar}>
        <div className={styles.mobileLeft}>
          <button
            type="button"
            className={styles.mobileMenuBtn}
            onClick={() => setMenuOpen(true)}
            aria-label="Mở trình đơn người bán"
          >
            <IconMenu size={18} />
          </button>
          <span className={styles.mobileTitle}>{activeTitle}</span>
        </div>
        <span className={styles.mobileShop}>{shopDisplayName}</span>
      </header>

      <aside
        className={`${styles.sidebar} ${menuOpen ? styles.sidebarOpen : ""}`}
        aria-label="Điều hướng Kênh người bán"
      >
        <div className={styles.sheetHeader}>
          <span className={styles.brandRow}>
            <NexusMark size={18} />
            <span className={styles.brandName}>Kênh người bán</span>
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

        <div className={styles.brandBlock}>
          <Link href="/seller" className={styles.brandRow}>
            <NexusMark size={18} />
            <span>
              <span className={styles.brandName}>NexusMart</span>{" "}
              <span className={styles.brandScope}>Người bán</span>
            </span>
          </Link>

          <div className={styles.shopIdentityBox}>
            <span className={styles.switcherLabel}>Gian hàng của bạn</span>
            <span className={styles.shopIdentityName}>{shopDisplayName}</span>
            {shopStatusMeta && (
              <span className={styles.shopIdentitySub}>
                {shopStatusMeta.label}
                {shop?.slug ? ` · /${shop.slug}` : ""}
              </span>
            )}
          </div>
        </div>

        <nav className={styles.navGroup} aria-label="Quản lý gian hàng">
          {navItems.map((item) => {
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
                {item.badge != null && <span className={styles.navCount}>{item.badge}</span>}
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
          <p className={styles.shopMeta}>
            {user.full_name} · {user.email}
          </p>
        </div>
      </aside>

      <main className={styles.canvas}>{children}</main>

      {toastMessage && (
        <div className={styles.toast} role="status" aria-live="polite">
          <span>{toastMessage}</span>
          <button type="button" className={styles.toastClose} onClick={dismissToast}>
            Đóng
          </button>
        </div>
      )}
    </div>
  );
}

export function SellerShell({ user, children }: { user: GatewayUser; children: React.ReactNode }) {
  return (
    <SellerProvider user={user}>
      <SellerShellInner>{children}</SellerShellInner>
    </SellerProvider>
  );
}
