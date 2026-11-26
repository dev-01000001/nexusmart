"use client";

import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import {
  IconArrowUpRight,
  IconChat,
  IconChevronLeft,
  IconChevronRight,
  IconClose,
  IconDashboard,
  IconFileText,
  IconHome,
  IconMenu,
  IconPackage,
  IconPlus,
  IconRefresh,
  IconSearch,
  IconSliders,
  IconSpark,
  IconStore,
  IconTag,
  IconWallet,
  NexusMark,
} from "@/components/Icons";
import { StateView } from "@/components";
import { useModalFocusTrap } from "@/components/modalFocus";
import { formatDateTimeVi } from "@/lib/format";
import type { SessionUser } from "@/lib/server/session";
import {
  getShopStatusLabel,
  resolvePresentationShopKey,
  type GwSellerShop,
} from "./sellerApiTypes";
import {
  SELLER_CHAT_READ_EVENT,
  applySellerChatReadDelta,
  extractSellerChatReadDelta,
} from "./sellerReturns";
import { isSellerRouteAllowedWhenSuspended } from "./sellerValidation";
import type { SellerSiteSummary } from "./site/siteSummary";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/layout.module.css";

const SIDEBAR_STORAGE_KEY = "nm.seller.sidebarCollapsed";

interface SellerContextValue {
  user: SessionUser;
  shop: GwSellerShop | null;
  presentationShopKey: string;
  site: SellerSiteSummary;
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
  group: string;
  keywords: string;
  icon: React.ReactNode;
  badge?: number;
}

interface QuickCommandItem {
  id: string;
  label: string;
  group: string;
  keywords: string;
  href: string;
  external?: boolean;
  icon: React.ReactNode;
}

function normalizeSearchText(str: string): string {
  return str
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/đ/gi, "d")
    .toLowerCase()
    .trim();
}

function getInitials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "NM";
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return `${parts[0][0] ?? ""}${parts[parts.length - 1][0] ?? ""}`.toUpperCase();
}

export interface SellerShellProps {
  user: SessionUser;
  shop: GwSellerShop | null;
  pendingOrdersCount: number;
  pendingReturnsCount?: number;
  unreadChatCount?: number;
  site: SellerSiteSummary;
  children: React.ReactNode;
}

export function SellerShell({
  user,
  shop,
  pendingOrdersCount,
  pendingReturnsCount = 0,
  unreadChatCount = 0,
  site,
  children,
}: SellerShellProps) {
  const pathname = usePathname();
  const router = useRouter();

  const [collapsed, setCollapsed] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [cmdOpen, setCmdOpen] = useState(false);
  const [cmdQuery, setCmdQuery] = useState("");
  const [cmdIdx, setCmdIdx] = useState(0);
  const [toastMessage, setToastMessage] = useState<string | null>(null);
  const [liveUnreadChatCount, setLiveUnreadChatCount] = useState<number>(() =>
    Math.max(0, Math.trunc(unreadChatCount))
  );

  // Fresh SSR unreadChatCount from layout overrides any client-side delta
  useEffect(() => {
    setLiveUnreadChatCount(Math.max(0, Math.trunc(unreadChatCount)));
  }, [unreadChatCount]);

  useEffect(() => {
    const onChatRead = (event: Event) => {
      const readCount = extractSellerChatReadDelta((event as CustomEvent).detail);
      if (readCount <= 0) return;
      setLiveUnreadChatCount((prev) => applySellerChatReadDelta(prev, readCount));
    };
    window.addEventListener(SELLER_CHAT_READ_EVENT, onChatRead);
    return () => window.removeEventListener(SELLER_CHAT_READ_EVENT, onChatRead);
  }, []);

  const navContainerRef = useRef<HTMLElement | null>(null);
  const itemRefs = useRef<Record<string, HTMLAnchorElement | null>>({});
  const cmdDialogRef = useRef<HTMLDivElement | null>(null);
  const cmdInputRef = useRef<HTMLInputElement | null>(null);
  const [pillPos, setPillPos] = useState<{ top: number; height: number; ready: boolean }>({
    top: 0,
    height: 36,
    ready: false,
  });

  const presentationShopKey = resolvePresentationShopKey(shop?.name);

  // Restore sidebar collapsed preference after mount (avoids SSR hydration mismatch)
  useEffect(() => {
    try {
      const saved = window.localStorage.getItem(SIDEBAR_STORAGE_KEY);
      if (saved === "1") setCollapsed(true);
    } catch {
      // Ignore storage access errors
    }
  }, []);

  const toggleCollapsed = useCallback(() => {
    setCollapsed((prev) => {
      const next = !prev;
      try {
        window.localStorage.setItem(SIDEBAR_STORAGE_KEY, next ? "1" : "0");
      } catch {
        // Ignore storage write errors
      }
      return next;
    });
  }, []);

  const showToast = useCallback((message: string) => {
    setToastMessage(message);
  }, []);

  const dismissToast = useCallback(() => {
    setToastMessage(null);
  }, []);

  useEffect(() => {
    setMenuOpen(false);
    setCmdOpen(false);
  }, [pathname]);

  useEffect(() => {
    if (!toastMessage) return;
    const t = window.setTimeout(() => setToastMessage(null), 3600);
    return () => window.clearTimeout(t);
  }, [toastMessage]);

  // Global Cmd+K / Ctrl+K shortcut for quick navigation
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setCmdOpen((prev) => {
          const next = !prev;
          if (next) {
            setCmdQuery("");
            setCmdIdx(0);
          }
          return next;
        });
      } else if (e.key === "Escape" && menuOpen) {
        setMenuOpen(false);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [menuOpen]);

  useModalFocusTrap({
    open: cmdOpen,
    containerRef: cmdDialogRef,
    initialFocusRef: cmdInputRef,
    onEscape: () => setCmdOpen(false),
  });

  const hasOperationalShop =
    user.role === "seller" &&
    Boolean(user.shopId) &&
    Boolean(shop) &&
    (shop?.status === "ACTIVE" || shop?.status === "SUSPENDED");
  const isSuspended = shop?.status === "SUSPENDED";
  const blockedBySuspension = isSuspended && !isSellerRouteAllowedWhenSuspended(pathname);

  const navGroups = useMemo<Array<{ title: string; items: SellerNavItem[] }>>(() => {
    if (!hasOperationalShop) {
      return [
        {
          title: "Đăng ký gian hàng",
          items: [
            {
              href: "/seller",
              label: "Mở gian hàng",
              group: "Đăng ký gian hàng",
              keywords: "mo gian hang dang ky shop",
              icon: <IconStore size={17} />,
            },
          ],
        },
      ];
    }
    return [
      {
        title: "Vận hành",
        items: [
          {
            href: "/seller",
            label: "Tổng quan",
            group: "Vận hành",
            keywords: "tong quan hom nay dashboard bieu do doanh thu bao cao",
            icon: <IconDashboard size={17} />,
          },
          {
            href: "/seller/don-hang",
            label: "Đơn hàng",
            group: "Vận hành",
            keywords: "don hang xac nhan van chuyen giao hang cod vietqr",
            icon: <IconStore size={17} />,
            badge: pendingOrdersCount > 0 ? pendingOrdersCount : undefined,
          },
          {
            href: "/seller/tra-hang",
            label: "Trả hàng",
            group: "Vận hành",
            keywords: "tra hang hoan tien doi tra tranh chap return",
            icon: <IconRefresh size={17} />,
            badge: pendingReturnsCount > 0 ? pendingReturnsCount : undefined,
          },
          {
            href: "/seller/san-pham",
            label: "Sản phẩm",
            group: "Vận hành",
            keywords: "san pham sku ton kho nhap hang bien the csv",
            icon: <IconPackage size={17} />,
          },
          {
            href: "/seller/khuyen-mai",
            label: "Khuyến mãi",
            group: "Vận hành",
            keywords: "khuyen mai voucher ma giam gia flash sale chuong trinh",
            icon: <IconTag size={17} />,
          },
        ],
      },
      {
        title: "Khách hàng",
        items: [
          {
            href: "/seller/chat",
            label: "Tin nhắn",
            group: "Khách hàng",
            keywords: "tin nhan chat hop thu khach hang hoi thoai",
            icon: <IconChat size={17} />,
            badge: liveUnreadChatCount > 0 ? liveUnreadChatCount : undefined,
          },
          {
            href: "/seller/tuong-tac",
            label: "Tương tác",
            group: "Khách hàng",
            keywords: "tuong tac danh gia ai thong bao",
            icon: <IconFileText size={17} />,
          },
        ],
      },
      {
        title: "Cửa hàng",
        items: [
          {
            href: "/seller/site",
            label: "Site bán hàng",
            group: "Cửa hàng",
            keywords: "site ban hang template mau giao dien ten mien xuat ban",
            icon: <IconHome size={17} />,
          },
          {
            href: "/seller/tai-chinh",
            label: "Tài chính",
            group: "Cửa hàng",
            keywords: "tai chinh so du doi soat so cai hoa hong ngan hang",
            icon: <IconWallet size={17} />,
          },
          {
            href: "/seller/bao-cao-ai",
            label: "Báo cáo AI",
            group: "Cửa hàng",
            keywords: "bao cao ai phan tich doanh thu tro ly",
            icon: <IconSpark size={17} />,
          },
          {
            href: "/seller/ho-so",
            label: "Hồ sơ",
            group: "Cửa hàng",
            keywords: "ho so kho lay hang dia chi van chuyen freeship tra loi tu dong",
            icon: <IconSliders size={17} />,
          },
        ],
      },
    ];
  }, [hasOperationalShop, pendingOrdersCount, pendingReturnsCount, liveUnreadChatCount]);

  const flatNavItems = useMemo(() => navGroups.flatMap((g) => g.items), [navGroups]);

  const isActive = useCallback(
    (href: string) =>
      href === "/seller"
        ? pathname === "/seller"
        : pathname === href || pathname.startsWith(`${href}/`),
    [pathname]
  );

  const activeNavHref = useMemo(
    () => flatNavItems.find((item) => isActive(item.href))?.href ?? "/seller",
    [flatNavItems, isActive]
  );

  const activeTitle =
    flatNavItems.find((item) => item.href === activeNavHref)?.label ?? "Kênh người bán";

  // Measure active nav link for smooth sliding background indicator
  useLayoutEffect(() => {
    const syncPill = () => {
      const container = navContainerRef.current;
      const target = itemRefs.current[activeNavHref];
      if (!container || !target) return;
      const cRect = container.getBoundingClientRect();
      const tRect = target.getBoundingClientRect();
      if (tRect.height === 0) return;
      setPillPos({
        top: Math.round(tRect.top - cRect.top),
        height: Math.round(tRect.height),
        ready: true,
      });
    };

    syncPill();
    const timer = window.setTimeout(syncPill, 220);
    window.addEventListener("resize", syncPill);
    return () => {
      window.clearTimeout(timer);
      window.removeEventListener("resize", syncPill);
    };
  }, [activeNavHref, collapsed]);

  // Quick command items (all seller pages + direct task shortcuts)
  const commandItems = useMemo<QuickCommandItem[]>(() => {
    const base: QuickCommandItem[] = flatNavItems.map((it) => ({
      id: `nav-${it.href}`,
      label: it.label,
      group: it.group,
      keywords: `${it.label} ${it.keywords}`,
      href: it.href,
      icon: it.icon,
    }));

    const shortcuts: QuickCommandItem[] = [
      {
        id: "act-new-product",
        label: "Thêm sản phẩm mới",
        group: "Thao tác nhanh",
        keywords: "them san pham moi tao sku dang ban",
        href: "/seller/san-pham/moi",
        icon: <IconPlus size={16} />,
      },
      {
        id: "act-pending-orders",
        label: "Đơn chờ xác nhận",
        group: "Thao tác nhanh",
        keywords: "don cho xac nhan confirmed xu ly",
        href: "/seller/don-hang?status=CONFIRMED",
        icon: <IconStore size={16} />,
      },
      {
        id: "act-low-stock",
        label: "SKU sắp hết hàng",
        group: "Thao tác nhanh",
        keywords: "sku sap het hang ton kho thap nhap ton",
        href: "/seller/san-pham?filter=low",
        icon: <IconPackage size={16} />,
      },
      {
        id: "act-returns",
        label: "Xử lý yêu cầu trả hàng",
        group: "Thao tác nhanh",
        keywords: "duyet yeu cau doi tra tra hang hoan tien return",
        href: "/seller/tra-hang",
        icon: <IconRefresh size={16} />,
      },
      {
        id: "act-domains",
        label: "Quản lý tên miền riêng",
        group: "Thao tác nhanh",
        keywords: "ten mien rieng dns domain site",
        href: "/seller/site/ten-mien",
        icon: <IconHome size={16} />,
      },
      {
        id: "act-preview",
        label: "Xem trước bản nháp site",
        group: "Thao tác nhanh",
        keywords: "xem truoc ban nhap preview site",
        href: "/seller/site/xem-truoc",
        icon: <IconArrowUpRight size={16} />,
      },
    ];

    if (site.url) {
      shortcuts.push({
        id: "act-live-site",
        label: `Mở site công khai (${site.host})`,
        group: "Cửa hàng",
        keywords: `xem site cong khai ${site.host}`,
        href: site.url,
        external: true,
        icon: <IconArrowUpRight size={16} />,
      });
    }

    return [...base, ...shortcuts];
  }, [flatNavItems, site.url, site.host]);

  const filteredCommands = useMemo(() => {
    const q = normalizeSearchText(cmdQuery);
    if (!q) return commandItems;
    return commandItems.filter((item) => normalizeSearchText(item.keywords).includes(q));
  }, [commandItems, cmdQuery]);

  const clampedCmdIdx =
    filteredCommands.length > 0 ? Math.min(cmdIdx, filteredCommands.length - 1) : 0;

  const executeCommand = useCallback(
    (item: QuickCommandItem) => {
      setCmdOpen(false);
      if (item.external) {
        window.open(item.href, "_blank", "noopener,noreferrer");
      } else {
        router.push(item.href);
      }
    },
    [router]
  );

  const handleCmdKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setCmdIdx((prev) =>
        filteredCommands.length === 0 ? 0 : (prev + 1) % filteredCommands.length
      );
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setCmdIdx((prev) =>
        filteredCommands.length === 0
          ? 0
          : (prev - 1 + filteredCommands.length) % filteredCommands.length
      );
    } else if (e.key === "Enter" && filteredCommands[clampedCmdIdx]) {
      e.preventDefault();
      executeCommand(filteredCommands[clampedCmdIdx]);
    }
  };

  const shopDisplayName = shop?.name || "Gian hàng mới";
  const shopStatus = shop ? getShopStatusLabel(shop.status) : null;
  const userName = user.fullName || user.email;
  const isOverflowTabActive = [
    "/seller/tra-hang",
    "/seller/khuyen-mai",
    "/seller/tuong-tac",
    "/seller/site",
    "/seller/tai-chinh",
    "/seller/bao-cao-ai",
    "/seller/ho-so",
  ].some((p) => isActive(p));

  const statusDotTone =
    shopStatus?.tone === "pos"
      ? styles.statusDotPos
      : shopStatus?.tone === "warn"
        ? styles.statusDotWarn
        : shopStatus?.tone === "neg"
          ? styles.statusDotNeg
          : "";

  return (
    <SellerContext.Provider value={{ user, shop, presentationShopKey, site, showToast }}>
      <div className={styles.shell}>
        {/* Mobile 48px Header: Shop name only (H1 carries page title) */}
        <header className={styles.mobileBar}>
          <Link href="/seller" className={styles.mobileLeft}>
            <NexusMark size={18} />
            <span className={styles.mobileShopName}>{shopDisplayName}</span>
          </Link>
          <div className={styles.mobileRight}>
            <button
              type="button"
              className={styles.mobileIconBtn}
              onClick={() => {
                setCmdQuery("");
                setCmdIdx(0);
                setCmdOpen(true);
              }}
              aria-label="Tìm nhanh trang"
            >
              <IconSearch size={18} />
            </button>
            <button
              type="button"
              className={styles.mobileIconBtn}
              onClick={() => setMenuOpen(true)}
              aria-label="Mở trình đơn người bán"
            >
              <IconMenu size={18} />
            </button>
          </div>
        </header>

        {/* Mobile drawer scrim */}
        {menuOpen && (
          <div
            className={styles.mobileDrawerBackdrop}
            onClick={() => setMenuOpen(false)}
            aria-hidden="true"
          />
        )}

        {/* Collapsible Left Sidebar (236px <-> 64px) */}
        <aside
          className={`${styles.sidebar} ${collapsed ? styles.sidebarCollapsed : ""} ${
            menuOpen ? styles.sidebarOpen : ""
          }`}
          aria-label="Điều hướng Kênh người bán"
        >
          <div className={styles.sheetHeader}>
            <span className={styles.brandLink}>
              <span className={styles.brandMarkWrap}>
                <NexusMark size={16} />
              </span>
              <span className={styles.brandName}>NexusMart</span>
            </span>
            <button
              type="button"
              className={styles.mobileIconBtn}
              onClick={() => setMenuOpen(false)}
              aria-label="Đóng trình đơn"
            >
              <IconClose size={18} />
            </button>
          </div>

          <div className={styles.brandHeader}>
            <Link href="/seller" className={styles.brandLink} title="NexusMart Kênh người bán">
              <span className={styles.brandMarkWrap}>
                <NexusMark size={16} />
              </span>
              <span className={styles.brandText}>
                <span className={styles.brandName}>NexusMart</span>
                <span className={styles.brandScope}>Bán hàng</span>
              </span>
            </Link>
          </div>

          <div className={styles.shopCard} title={shopDisplayName}>
            <span className={styles.shopAvatar} aria-hidden="true">
              {getInitials(shopDisplayName)}
            </span>
            <div className={styles.shopInfo}>
              <span className={styles.shopName}>{shopDisplayName}</span>
              {shopStatus && (
                <span className={styles.shopStatusRow}>
                  <span className={`${styles.statusDot} ${statusDotTone}`} aria-hidden="true" />
                  <span>{shopStatus.label}</span>
                </span>
              )}
            </div>
          </div>

          <nav ref={navContainerRef} className={styles.navContainer} aria-label="Quản lý gian hàng">
            <span
              className={styles.navActivePill}
              aria-hidden="true"
              style={{
                transform: `translateY(${pillPos.top}px)`,
                height: `${pillPos.height}px`,
                opacity: pillPos.ready ? 1 : 0,
              }}
            />
            {navGroups.map((group) => (
              <div key={group.title} className={styles.navSection}>
                <span className={styles.groupLabel}>{group.title}</span>
                {group.items.map((item) => {
                  const active = item.href === activeNavHref;
                  return (
                    <Link
                      key={item.href}
                      ref={(el) => {
                        itemRefs.current[item.href] = el;
                      }}
                      href={item.href}
                      title={collapsed ? item.label : undefined}
                      className={`${styles.navItem} ${active ? styles.navItemActive : ""}`}
                      aria-current={active ? "page" : undefined}
                    >
                      <span className={styles.navIcon}>{item.icon}</span>
                      <span className={styles.navLabel}>{item.label}</span>
                      {item.badge != null && <span className={styles.navCount}>{item.badge}</span>}
                    </Link>
                  );
                })}
              </div>
            ))}
          </nav>

          <div className={styles.sidebarFooter}>
            {site.url ? (
              <a
                href={site.url}
                target="_blank"
                rel="noopener noreferrer"
                className={styles.siteQuickLink}
                title={collapsed ? `Xem site (${site.host})` : undefined}
              >
                <IconArrowUpRight size={17} />
                <span className={styles.navLabel}>Xem site công khai</span>
              </a>
            ) : (
              <Link
                href="/seller/site"
                className={styles.siteQuickLink}
                title={collapsed ? "Thiết lập site" : undefined}
              >
                <IconArrowUpRight size={17} />
                <span className={styles.navLabel}>
                  {site.state === "suspended" ? "Site tạm khóa" : "Mở bán site"}
                </span>
              </Link>
            )}
            <p className={styles.accountLine}>{userName}</p>
          </div>
        </aside>

        {/* Right L-frame top bar + inset white sheet */}
        <div className={styles.mainColumn}>
          <header className={styles.topBar}>
            <div className={styles.topBarLeft}>
              <button
                type="button"
                className={styles.collapseBtn}
                onClick={toggleCollapsed}
                aria-label={collapsed ? "Mở rộng thanh điều hướng" : "Thu gọn thanh điều hướng"}
                aria-expanded={!collapsed}
                title={collapsed ? "Mở rộng (236px)" : "Thu gọn (64px)"}
              >
                {collapsed ? <IconChevronRight size={16} /> : <IconChevronLeft size={16} />}
              </button>
              <span className={styles.topBarBreadcrumb}>{activeTitle}</span>
            </div>

            <button
              type="button"
              className={styles.cmdTrigger}
              onClick={() => {
                setCmdQuery("");
                setCmdIdx(0);
                setCmdOpen(true);
              }}
              aria-label="Tìm và chuyển nhanh trang (Command K)"
            >
              <span className={styles.cmdTriggerLeft}>
                <IconSearch size={14} />
                <span className={styles.cmdTriggerLabel}>Nhảy nhanh đến trang, thao tác...</span>
              </span>
              <kbd className={styles.cmdKbd}>⌘K</kbd>
            </button>

            <div className={styles.topBarRight}>
              {site.url ? (
                <a
                  href={site.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className={styles.topSiteBtn}
                >
                  <span>Xem site</span>
                  <IconArrowUpRight size={13} />
                </a>
              ) : (
                <Link href="/seller/site" className={`${styles.topSiteBtn} ${styles.topSiteWarn}`}>
                  <span>{site.state === "suspended" ? "Site tạm khóa" : "Chưa xuất bản"}</span>
                </Link>
              )}
              <Link href="/seller/ho-so" className={styles.topUserBadge} title={userName}>
                <span className={styles.topUserInitial} aria-hidden="true">
                  {getInitials(userName)}
                </span>
                <span className={styles.topUserName}>{userName}</span>
              </Link>
            </div>
          </header>

          <main className={styles.canvas}>
            {isSuspended && shop && (
              <div className={common.suspendedBanner} role="alert" style={{ marginBottom: 16 }}>
                <span className={common.suspendedBannerTitle}>
                  Gian hàng đang tạm ngưng hoạt động
                </span>
                {shop.suspended_reason && (
                  <span className={common.suspendedBannerText}>Lý do: {shop.suspended_reason}</span>
                )}
                <span className={common.suspendedBannerMeta}>
                  {shop.suspended_at
                    ? `Thời điểm tạm ngưng: ${formatDateTimeVi(shop.suspended_at)} · `
                    : ""}
                  Trong thời gian tạm ngưng, bạn vẫn có thể xử lý đơn hàng, trả hàng và theo dõi tài
                  chính.
                </span>
              </div>
            )}
            {blockedBySuspension ? (
              <StateView
                kind="error"
                surface="app"
                title="Chức năng tạm khóa do gian hàng đang tạm ngưng"
                message="Gian hàng hiện chỉ được phép truy cập Tổng quan, Đơn hàng, Trả hàng và Tài chính để hoàn tất các đơn đang phát sinh."
                actionLabel="Về đơn hàng"
                actionHref="/seller/don-hang"
              />
            ) : (
              children
            )}
          </main>
        </div>

        {/* Mobile 5-item Bottom Bar */}
        <nav className={styles.mobileBottomNav} aria-label="Điều hướng nhanh di động">
          <Link
            href="/seller"
            className={`${styles.mobileTab} ${isActive("/seller") ? styles.mobileTabActive : ""}`}
            aria-current={isActive("/seller") ? "page" : undefined}
          >
            <span className={styles.mobileTabIconWrap}>
              <IconDashboard size={18} />
            </span>
            <span className={styles.mobileTabLabel}>Tổng quan</span>
          </Link>

          <Link
            href="/seller/don-hang"
            className={`${styles.mobileTab} ${isActive("/seller/don-hang") ? styles.mobileTabActive : ""}`}
            aria-current={isActive("/seller/don-hang") ? "page" : undefined}
          >
            <span className={styles.mobileTabIconWrap}>
              <IconStore size={18} />
              {pendingOrdersCount > 0 && (
                <span className={styles.mobileTabBadge}>
                  {pendingOrdersCount > 99 ? "99+" : pendingOrdersCount}
                </span>
              )}
            </span>
            <span className={styles.mobileTabLabel}>Đơn hàng</span>
          </Link>

          <Link
            href="/seller/san-pham"
            className={`${styles.mobileTab} ${isActive("/seller/san-pham") ? styles.mobileTabActive : ""}`}
            aria-current={isActive("/seller/san-pham") ? "page" : undefined}
          >
            <span className={styles.mobileTabIconWrap}>
              <IconPackage size={18} />
            </span>
            <span className={styles.mobileTabLabel}>Sản phẩm</span>
          </Link>

          <Link
            href="/seller/chat"
            className={`${styles.mobileTab} ${isActive("/seller/chat") ? styles.mobileTabActive : ""}`}
            aria-current={isActive("/seller/chat") ? "page" : undefined}
          >
            <span className={styles.mobileTabIconWrap}>
              <IconChat size={18} />
              {liveUnreadChatCount > 0 && (
                <span className={styles.mobileTabBadge}>{liveUnreadChatCount}</span>
              )}
            </span>
            <span className={styles.mobileTabLabel}>Tin nhắn</span>
          </Link>

          <button
            type="button"
            className={`${styles.mobileTab} ${isOverflowTabActive || menuOpen ? styles.mobileTabActive : ""}`}
            onClick={() => setMenuOpen((prev) => !prev)}
            aria-expanded={menuOpen}
            aria-label="Mở thêm mục quản lý"
          >
            <span className={styles.mobileTabIconWrap}>
              <IconMenu size={18} />
              {pendingReturnsCount > 0 && (
                <span className={styles.mobileTabBadge}>{pendingReturnsCount}</span>
              )}
            </span>
            <span className={styles.mobileTabLabel}>Thêm</span>
          </button>
        </nav>

        {/* Quick Jump Command Dialog (Cmd+K / Ctrl+K) */}
        {cmdOpen && (
          <div
            className={styles.cmdBackdrop}
            onClick={(e) => {
              if (e.target === e.currentTarget) setCmdOpen(false);
            }}
          >
            <div
              ref={cmdDialogRef}
              tabIndex={-1}
              className={styles.cmdDialog}
              role="dialog"
              aria-modal="true"
              aria-label="Chuyển nhanh trong Kênh người bán"
            >
              <div className={styles.cmdInputRow}>
                <IconSearch size={16} />
                <input
                  ref={cmdInputRef}
                  type="text"
                  className={styles.cmdInput}
                  value={cmdQuery}
                  onChange={(e) => {
                    setCmdQuery(e.target.value);
                    setCmdIdx(0);
                  }}
                  onKeyDown={handleCmdKeyDown}
                  placeholder="Gõ tên trang hoặc thao tác (ví dụ: Đơn hàng, Thêm sản phẩm, Site)..."
                  aria-label="Lọc danh sách trang"
                />
                <button
                  type="button"
                  className={styles.cmdEscBtn}
                  onClick={() => setCmdOpen(false)}
                >
                  Esc
                </button>
              </div>

              <div className={styles.cmdList} role="listbox" aria-label="Kết quả chuyển nhanh">
                {filteredCommands.length === 0 ? (
                  <div className={styles.cmdEmpty}>Không tìm thấy mục phù hợp.</div>
                ) : (
                  filteredCommands.map((item, idx) => {
                    const selected = idx === clampedCmdIdx;
                    return (
                      <button
                        key={item.id}
                        type="button"
                        role="option"
                        aria-selected={selected}
                        className={`${styles.cmdItem} ${selected ? styles.cmdItemActive : ""}`}
                        onMouseEnter={() => setCmdIdx(idx)}
                        onClick={() => executeCommand(item)}
                      >
                        <span className={styles.cmdItemLeft}>
                          {item.icon}
                          <span className={styles.cmdItemLabel}>{item.label}</span>
                        </span>
                        <span className={styles.cmdItemGroup}>{item.group}</span>
                      </button>
                    );
                  })
                )}
              </div>

              <div className={styles.cmdFoot}>
                <span>↑↓ chọn mục, Enter mở</span>
                <span>⌘K / Ctrl+K</span>
              </div>
            </div>
          </div>
        )}

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
