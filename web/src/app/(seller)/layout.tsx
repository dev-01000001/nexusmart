"use client";

import React, { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { SHOPS, getSellerWorkspace, getShopById } from "@/data";
import { useStore } from "@/context/StoreContext";
import {
  IconArrowUpRight,
  IconChat,
  IconClose,
  IconDashboard,
  IconFileText,
  IconMenu,
  IconPackage,
  IconShield,
  IconSliders,
  IconStore,
  IconTag,
  IconWallet,
  NexusMark,
} from "@/components/Icons";
import { getPendingInteractionCount, useSellerSession } from "@/features/seller/sellerStore";
import styles from "./layout.module.css";

interface SellerNavItem {
  href: string;
  label: string;
  icon: React.ReactNode;
  badge?: number;
}

export default function SellerLayout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const { activeSellerShopId, setActiveSellerShopId, chatThreads, toastMessage, dismissToast } =
    useStore();
  const [menuOpen, setMenuOpen] = useState(false);
  const session = useSellerSession();

  useEffect(() => {
    setMenuOpen(false);
  }, [pathname]);

  useEffect(() => {
    if (!toastMessage) return;
    const t = window.setTimeout(() => dismissToast(), 3400);
    return () => window.clearTimeout(t);
  }, [toastMessage, dismissToast]);

  const activeShop = getShopById(activeSellerShopId) ?? SHOPS[0];
  const workspace = getSellerWorkspace(activeShop.id);
  const pendingOrdersCount = workspace.orders.filter((o) => o.status === "cho_xac_nhan").length;
  const pendingInteractionCount = getPendingInteractionCount(activeShop.id, session);
  const unreadChatCount = chatThreads
    .filter((t) => t.shopId === activeShop.id)
    .reduce((sum, t) => sum + t.unreadBySeller, 0);

  const navItems: SellerNavItem[] = [
    { href: "/seller", label: "Tổng quan", icon: <IconDashboard size={16} /> },
    { href: "/seller/san-pham", label: "Sản phẩm", icon: <IconPackage size={16} /> },
    {
      href: "/seller/don-hang",
      label: "Đơn chờ duyệt",
      icon: <IconStore size={16} />,
      badge: pendingOrdersCount > 0 ? pendingOrdersCount : undefined,
    },
    { href: "/seller/khuyen-mai", label: "Khuyến mãi", icon: <IconTag size={16} /> },
    {
      href: "/seller/tuong-tac",
      label: "Tương tác",
      icon: <IconFileText size={16} />,
      badge: pendingInteractionCount > 0 ? pendingInteractionCount : undefined,
    },
    { href: "/seller/ho-so", label: "Hồ sơ & Vận hành", icon: <IconSliders size={16} /> },
    { href: "/seller/tai-chinh", label: "Tài chính", icon: <IconWallet size={16} /> },
    {
      href: "/seller/chat",
      label: "Tin nhắn",
      icon: <IconChat size={16} />,
      badge: unreadChatCount > 0 ? unreadChatCount : undefined,
    },
  ];

  const isActive = (href: string) =>
    href === "/seller"
      ? pathname === "/seller"
      : pathname === href || pathname.startsWith(`${href}/`);

  const activeTitle = navItems.find((item) => isActive(item.href))?.label ?? "Kênh người bán";

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
        <span className={styles.mobileShop}>{activeShop.shortName}</span>
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

          <div className={styles.switcherWrap}>
            <label htmlFor="seller-shop-switcher" className={styles.switcherLabel}>
              Gian hàng đang quản lý
            </label>
            <select
              id="seller-shop-switcher"
              className={styles.switcherSelect}
              value={activeShop.id}
              onChange={(e) => setActiveSellerShopId(e.target.value)}
            >
              {SHOPS.map((shop) => (
                <option key={shop.id} value={shop.id}>
                  {shop.name}
                </option>
              ))}
            </select>
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
          <Link href="/admin" className={styles.navItem}>
            <IconShield size={16} />
            <span className={styles.navLabel}>Quản trị sàn</span>
          </Link>
          <p className={styles.shopMeta}>
            {activeShop.city} · Tỉ lệ giao đúng hạn {activeShop.fulfillmentRate}
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
