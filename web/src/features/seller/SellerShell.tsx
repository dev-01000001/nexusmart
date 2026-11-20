"use client";

import React, { createContext, useCallback, useContext, useEffect, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  IconArrowUpRight,
  IconChat,
  IconClose,
  IconDashboard,
  IconFileText,
  IconMenu,
  IconPackage,
  IconSliders,
  IconStore,
  IconTag,
  IconWallet,
  NexusMark,
} from "@/components/Icons";
import type { SessionUser } from "@/lib/server/session";
import {
  getShopStatusLabel,
  resolvePresentationShopKey,
  type GwSellerShop,
} from "./sellerApiTypes";
import {
  getPendingInteractionCount,
  getShopMockChatThreads,
  useSellerSession,
} from "./sellerStore";
import styles from "@/app/(seller)/layout.module.css";

interface SellerContextValue {
  user: SessionUser;
  shop: GwSellerShop | null;
  presentationShopKey: string;
  showToast: (message: string) => void;
}

const SellerContext = createContext<SellerContextValue | null>(null);

export function useSellerContext(): SellerContextValue {
  const ctx = useContext(SellerContext);
  if (!ctx) {
    throw new Error("useSellerContext phải được gọi bên trong SellerShell.");
  }
  return ctx;
}

interface SellerNavItem {
  href: string;
  label: string;
  icon: React.ReactNode;
  badge?: number;
}

export interface SellerShellProps {
  user: SessionUser;
  shop: GwSellerShop | null;
  pendingOrdersCount: number;
  children: React.ReactNode;
}

export function SellerShell({ user, shop, pendingOrdersCount, children }: SellerShellProps) {
  const pathname = usePathname();
  const [menuOpen, setMenuOpen] = useState(false);
  const [toastMessage, setToastMessage] = useState<string | null>(null);
  const mockSession = useSellerSession();

  const presentationShopKey = resolvePresentationShopKey(shop?.name);

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

  const pendingInteractionCount = getPendingInteractionCount(presentationShopKey, mockSession);
  const unreadChatCount = getShopMockChatThreads(presentationShopKey, mockSession).reduce(
    (sum, t) => sum + t.unreadBySeller,
    0
  );

  const navItems: SellerNavItem[] = [
    { href: "/seller", label: "Tổng quan", icon: <IconDashboard size={16} /> },
    { href: "/seller/san-pham", label: "Sản phẩm", icon: <IconPackage size={16} /> },
    {
      href: "/seller/don-hang",
      label: "Đơn hàng",
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
  const shopDisplayName = shop?.name || "Gian hàng chưa kích hoạt";
  const shopStatus = shop ? getShopStatusLabel(shop.status) : null;

  return (
    <SellerContext.Provider value={{ user, shop, presentationShopKey, showToast }}>
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

            <div className={styles.switcherWrap}>
              <span className={styles.switcherLabel}>Gian hàng của bạn</span>
              <div className={styles.shopIdentityBox}>
                <span className={styles.shopIdentityName}>{shopDisplayName}</span>
                {shopStatus && <span className={styles.shopIdentityMeta}>{shopStatus.label}</span>}
              </div>
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
              {user.fullName || user.email}
              {shop?.contact_phone ? ` · ${shop.contact_phone}` : ""}
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
    </SellerContext.Provider>
  );
}
