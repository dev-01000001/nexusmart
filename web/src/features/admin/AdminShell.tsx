"use client";

import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import {
  IconArrowUpRight,
  IconClose,
  IconCommand,
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
import { hasOpenModal, useModalFocusTrap } from "@/components/modalFocus";
import type { SessionUser } from "@/lib/server/session";
import { isEditableElement, useSlidingUnderline } from "./AdminUi";
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

type AdminGroupId = "ops" | "commerce" | "system";

interface AdminNavItem {
  href: string;
  label: string;
  groupId: AdminGroupId;
  groupLabel: string;
  keywords: string;
  icon: React.ReactNode;
  badge?: number;
  alertBadge?: boolean;
}

interface AdminGroupDef {
  id: AdminGroupId;
  label: string;
  defaultHref: string;
  badge?: number;
  alertBadge?: boolean;
  items: AdminNavItem[];
}

function getInitials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "AD";
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return `${parts[0][0] ?? ""}${parts[parts.length - 1][0] ?? ""}`.toUpperCase();
}

function normalizeVi(raw: string): string {
  return raw
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[đĐ]/g, "d")
    .toLowerCase();
}

function resolveQuickSearchTarget(trimmed: string): string {
  const lower = trimmed.toLowerCase();
  const upper = trimmed.toUpperCase();
  if (upper.startsWith("DS-")) {
    return `/admin/doi-soat?q=${encodeURIComponent(trimmed)}`;
  }
  if (upper.startsWith("KN-")) {
    return `/admin/khieu-nai?q=${encodeURIComponent(trimmed)}`;
  }
  if (lower.startsWith("admin.") || lower.startsWith("site.") || lower.startsWith("tr-")) {
    return `/admin/audit-log?q=${encodeURIComponent(trimmed)}`;
  }
  if (upper.startsWith("SP-") || upper.startsWith("SKU-") || upper.startsWith("NM-")) {
    return `/admin/kiem-duyet?q=${encodeURIComponent(trimmed)}`;
  }
  if (upper.startsWith("FS-")) {
    return `/admin/flash-sale?q=${encodeURIComponent(trimmed)}`;
  }
  if (lower.includes(".nexusmart") || lower.startsWith("site:")) {
    const siteQuery = lower.startsWith("site:") ? trimmed.slice(5).trim() : trimmed;
    return `/admin/sites?q=${encodeURIComponent(siteQuery || trimmed)}`;
  }
  return `/admin/gian-hang?q=${encodeURIComponent(trimmed)}`;
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
  const paletteDialogRef = useRef<HTMLDivElement | null>(null);
  const paletteInputRef = useRef<HTMLInputElement | null>(null);
  const paletteOptionRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const paletteRestoreFocusRef = useRef(true);

  const mobileSheetPanelRef = useRef<HTMLElement | null>(null);
  const mobileSearchInputRef = useRef<HTMLInputElement | null>(null);
  const sheetRestoreFocusRef = useRef(true);

  const [sheetOpen, setSheetOpen] = useState(false);
  const [sheetRendered, setSheetRendered] = useState(false);
  const [sheetVisible, setSheetVisible] = useState(false);

  const [paletteOpen, setPaletteOpen] = useState(false);
  const [paletteRendered, setPaletteRendered] = useState(false);
  const [paletteVisible, setPaletteVisible] = useState(false);
  const [paletteQuery, setPaletteQuery] = useState("");
  const [paletteIndex, setPaletteIndex] = useState(0);

  const [mobileSearch, setMobileSearch] = useState("");
  const [toastMessage, setToastMessage] = useState<string | null>(null);

  const showToast = useCallback((message: string) => {
    setToastMessage(message);
  }, []);

  const dismissToast = useCallback(() => {
    setToastMessage(null);
  }, []);

  const openPalette = useCallback(() => {
    paletteRestoreFocusRef.current = true;
    sheetRestoreFocusRef.current = false;
    setSheetOpen(false);
    setPaletteQuery("");
    setPaletteIndex(0);
    setPaletteOpen(true);
  }, []);

  const closePalette = useCallback((restoreFocus = true) => {
    paletteRestoreFocusRef.current = restoreFocus;
    setPaletteOpen(false);
  }, []);

  const openSheet = useCallback(() => {
    sheetRestoreFocusRef.current = true;
    paletteRestoreFocusRef.current = false;
    setPaletteOpen(false);
    setMobileSearch("");
    setSheetOpen(true);
  }, []);

  const closeSheet = useCallback((restoreFocus = true) => {
    sheetRestoreFocusRef.current = restoreFocus;
    setSheetOpen(false);
  }, []);

  useEffect(() => {
    sheetRestoreFocusRef.current = false;
    paletteRestoreFocusRef.current = false;
    setSheetOpen(false);
    setPaletteOpen(false);
  }, [pathname]);

  useEffect(() => {
    if (!toastMessage) return;
    const t = window.setTimeout(() => setToastMessage(null), 3800);
    return () => window.clearTimeout(t);
  }, [toastMessage]);

  useEffect(() => {
    if (sheetOpen) {
      setSheetRendered(true);
      const raf = window.requestAnimationFrame(() => setSheetVisible(true));
      return () => window.cancelAnimationFrame(raf);
    }
    setSheetVisible(false);
    const timer = window.setTimeout(() => setSheetRendered(false), 210);
    return () => window.clearTimeout(timer);
  }, [sheetOpen]);

  useEffect(() => {
    if (paletteOpen) {
      setPaletteRendered(true);
      const raf = window.requestAnimationFrame(() => setPaletteVisible(true));
      return () => window.cancelAnimationFrame(raf);
    }
    setPaletteVisible(false);
    const timer = window.setTimeout(() => setPaletteRendered(false), 180);
    return () => window.clearTimeout(timer);
  }, [paletteOpen]);

  useModalFocusTrap({
    open: sheetOpen,
    containerRef: mobileSheetPanelRef,
    initialFocusRef: mobileSearchInputRef,
    onEscape: () => closeSheet(true),
    shouldRestoreFocusRef: sheetRestoreFocusRef,
  });

  useModalFocusTrap({
    open: paletteOpen,
    containerRef: paletteDialogRef,
    initialFocusRef: paletteInputRef,
    onEscape: () => closePalette(true),
    shouldRestoreFocusRef: paletteRestoreFocusRef,
  });

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (
        (e.metaKey || e.ctrlKey) &&
        !e.altKey &&
        (e.key.toLowerCase() === "k" || e.code === "KeyK")
      ) {
        if (paletteOpen) {
          e.preventDefault();
          e.stopPropagation();
          closePalette(true);
        } else if (!hasOpenModal()) {
          e.preventDefault();
          e.stopPropagation();
          openPalette();
        }
        return;
      }
      if (e.key === "/" && !e.metaKey && !e.ctrlKey && !e.altKey) {
        if (!isEditableElement(e.target) && !paletteOpen && !hasOpenModal()) {
          e.preventDefault();
          openPalette();
        }
      }
    };
    window.addEventListener("keydown", onKeyDown, true);
    return () => window.removeEventListener("keydown", onKeyDown, true);
  }, [paletteOpen, openPalette, closePalette]);

  const groups: AdminGroupDef[] = useMemo(() => {
    const opsUrgent = pendingShopApprovals + pendingProductReviews + openComplaintsCount;
    const opsItems: AdminNavItem[] = [
      {
        href: "/admin",
        label: "Tổng quan",
        groupId: "ops",
        groupLabel: "Điều hành",
        keywords: "tong quan overview dashboard hang doi suc khoe gmv",
        icon: <IconDashboard size={15} />,
      },
      {
        href: "/admin/gian-hang",
        label: "Duyệt gian hàng",
        groupId: "ops",
        groupLabel: "Điều hành",
        keywords: "duyet gian hang shop ho so gpkd",
        icon: <IconStore size={15} />,
        badge: pendingShopApprovals > 0 ? pendingShopApprovals : undefined,
      },
      {
        href: "/admin/kiem-duyet",
        label: "Duyệt sản phẩm",
        groupId: "ops",
        groupLabel: "Điều hành",
        keywords: "duyet san pham kiem duyet moderation sku",
        icon: <IconShield size={15} />,
        badge: pendingProductReviews > 0 ? pendingProductReviews : undefined,
      },
      {
        href: "/admin/khieu-nai",
        label: "Khiếu nại",
        groupId: "ops",
        groupLabel: "Điều hành",
        keywords: "khieu nai tranh chap hoan tien tra hang complaints",
        icon: <IconFlag size={15} />,
        badge: openComplaintsCount > 0 ? openComplaintsCount : undefined,
      },
    ];

    const commerceItems: AdminNavItem[] = [
      {
        href: "/admin/sites",
        label: "Site gian hàng",
        groupId: "commerce",
        groupLabel: "Kinh doanh",
        keywords: "site gian hang ten mien domain subdomain storefront",
        icon: <IconHome size={15} />,
      },
      {
        href: "/admin/flash-sale",
        label: "Flash sale",
        groupId: "commerce",
        groupLabel: "Kinh doanh",
        keywords: "flash sale khung gio suat giam gia khuyen mai",
        icon: <IconTag size={15} />,
      },
    ];

    const systemItems: AdminNavItem[] = [
      {
        href: "/admin/doi-soat",
        label: "Đối soát & sổ cái",
        groupId: "system",
        groupLabel: "Hệ thống",
        keywords: "doi soat so cai ghi kep thanh toan ledger reconcile",
        icon: <IconWallet size={15} />,
        badge: ledgerUnbalanced ? 1 : undefined,
        alertBadge: ledgerUnbalanced,
      },
      {
        href: "/admin/cau-hinh",
        label: "Cấu hình sàn",
        groupId: "system",
        groupLabel: "Hệ thống",
        keywords: "cau hinh san danh muc hoa hong voucher nguoi dung phan quyen rbac",
        icon: <IconSliders size={15} />,
      },
      {
        href: "/admin/audit-log",
        label: "Nhật ký kiểm toán",
        groupId: "system",
        groupLabel: "Hệ thống",
        keywords: "nhat ky kiem toan audit log lich su",
        icon: <IconFileText size={15} />,
      },
    ];

    return [
      {
        id: "ops",
        label: "Điều hành",
        defaultHref: "/admin",
        badge: opsUrgent > 0 ? opsUrgent : undefined,
        items: opsItems,
      },
      {
        id: "commerce",
        label: "Kinh doanh",
        defaultHref: "/admin/sites",
        items: commerceItems,
      },
      {
        id: "system",
        label: "Hệ thống",
        defaultHref: "/admin/doi-soat",
        badge: ledgerUnbalanced ? 1 : undefined,
        alertBadge: ledgerUnbalanced,
        items: systemItems,
      },
    ];
  }, [pendingShopApprovals, pendingProductReviews, openComplaintsCount, ledgerUnbalanced]);

  const allNavItems = useMemo(() => groups.flatMap((g) => g.items), [groups]);

  const isRouteActive = useCallback(
    (href: string) =>
      href === "/admin"
        ? pathname === "/admin"
        : pathname === href || pathname.startsWith(`${href}/`),
    [pathname]
  );

  const activeItem = useMemo(
    () => allNavItems.find((item) => isRouteActive(item.href)) ?? allNavItems[0],
    [allNavItems, isRouteActive]
  );

  const activeGroup = useMemo(
    () => groups.find((g) => g.id === activeItem.groupId) ?? groups[0],
    [groups, activeItem.groupId]
  );

  const tier1Bar = useSlidingUnderline(
    activeGroup.id,
    groups.map((g) => `${g.id}:${g.badge ?? 0}`).join(","),
    "[aria-current]"
  );
  const tier2Bar = useSlidingUnderline(
    activeItem.href,
    `${activeGroup.id}|${activeGroup.items.map((i) => `${i.href}:${i.badge ?? 0}`).join(",")}`,
    "[aria-current]",
    true
  );

  const allSearchOptions = useMemo(
    () => [
      ...allNavItems.map((item) => ({
        href: item.href,
        label: item.label,
        meta: item.groupLabel,
        icon: item.icon,
        badge: item.badge,
        alertBadge: item.alertBadge,
        searchBlob: normalizeVi(`${item.label} ${item.groupLabel} ${item.keywords} ${item.href}`),
      })),
      {
        href: "/",
        label: "Trang chủ NexusMart",
        meta: "Chuyển khu vực",
        icon: <IconArrowUpRight size={15} />,
        badge: undefined,
        alertBadge: false,
        searchBlob: normalizeVi("Trang chủ NexusMart home san"),
      },
    ],
    [allNavItems]
  );

  const paletteMatches = useMemo(() => {
    const tokens = normalizeVi(paletteQuery.trim()).split(/\s+/).filter(Boolean);
    if (tokens.length === 0) return allSearchOptions;
    return allSearchOptions.filter((opt) => tokens.every((t) => opt.searchBlob.includes(t)));
  }, [allSearchOptions, paletteQuery]);

  const mobileFilteredGroups = useMemo(() => {
    const tokens = normalizeVi(mobileSearch.trim()).split(/\s+/).filter(Boolean);
    if (tokens.length === 0) return groups;
    return groups
      .map((group) => ({
        ...group,
        items: group.items.filter((item) => {
          const blob = normalizeVi(
            `${item.label} ${item.groupLabel} ${item.keywords} ${item.href}`
          );
          return tokens.every((t) => blob.includes(t));
        }),
      }))
      .filter((group) => group.items.length > 0);
  }, [groups, mobileSearch]);

  const mobileNavMatches = useMemo(() => {
    if (!mobileSearch.trim()) return [];
    return mobileFilteredGroups.flatMap((group) => group.items);
  }, [mobileFilteredGroups, mobileSearch]);

  const hasDirectSearchFallback = paletteQuery.trim().length > 0;
  const totalPaletteChoices = paletteMatches.length + (hasDirectSearchFallback ? 1 : 0);
  const clampedPaletteIndex =
    totalPaletteChoices > 0 ? Math.min(paletteIndex, totalPaletteChoices - 1) : -1;

  const movePaletteIndex = (delta: 1 | -1) => {
    if (totalPaletteChoices <= 0) return;
    setPaletteIndex((prev) => {
      const base = Math.min(prev, totalPaletteChoices - 1);
      const next = (base + delta + totalPaletteChoices) % totalPaletteChoices;
      window.requestAnimationFrame(() => {
        paletteOptionRefs.current[next]?.scrollIntoView({ block: "nearest" });
      });
      return next;
    });
  };

  const runPaletteChoice = (idx: number) => {
    if (idx >= 0 && idx < paletteMatches.length) {
      const picked = paletteMatches[idx];
      closePalette(false);
      router.push(picked.href);
      return;
    }
    const trimmed = paletteQuery.trim();
    if (trimmed) {
      closePalette(false);
      router.push(resolveQuickSearchTarget(trimmed));
    }
  };

  const handlePaletteDialogKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      movePaletteIndex(1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      movePaletteIndex(-1);
    } else if (e.key === "Enter" && e.target === paletteInputRef.current) {
      e.preventDefault();
      if (clampedPaletteIndex >= 0) {
        runPaletteChoice(clampedPaletteIndex);
      }
    }
  };

  const handleMobileSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = mobileSearch.trim();
    if (!trimmed) return;
    closeSheet(false);
    if (mobileNavMatches.length > 0) {
      router.push(mobileNavMatches[0].href);
      return;
    }
    router.push(resolveQuickSearchTarget(trimmed));
  };

  const sheetMounted = sheetOpen || sheetRendered;
  const paletteMounted = paletteOpen || paletteRendered;

  const displayName = user.fullName || user.email || "Quản trị viên";
  const initials = getInitials(displayName);

  return (
    <AdminContext.Provider value={{ user, showToast }}>
      <div className={styles.shell}>
        <header className={styles.headerWrap}>
          {/* Tier 1: Deep --ink primary group navigation + ⌘K + account */}
          <div className={styles.tierOne}>
            <div className={styles.tierOneInner}>
              <div className={styles.tierOneLeft}>
                <button
                  type="button"
                  className={styles.mobileMenuBtn}
                  onClick={openSheet}
                  aria-label="Mở trình đơn quản trị"
                  aria-expanded={sheetOpen}
                >
                  <IconMenu size={18} />
                </button>

                <Link href="/admin" className={styles.brandLink}>
                  <NexusMark size={18} accentColor="var(--band-dark-accent)" />
                  <span className={styles.brandName}>NexusMart</span>
                  <span className={styles.brandScope}>Quản trị</span>
                </Link>

                <span className={styles.mobileActiveTitle}>{activeItem.label}</span>

                <nav className={styles.groupNav} aria-label="Nhóm phân hệ quản trị">
                  <div
                    ref={tier1Bar.containerRef}
                    className={`${styles.groupTrack} ${tier1Bar.rect.ready ? styles.trackMeasured : ""}`}
                  >
                    {groups.map((group) => {
                      const active = group.id === activeGroup.id;
                      return (
                        <Link
                          key={group.id}
                          href={group.defaultHref}
                          className={`${styles.groupLink} ${active ? styles.groupLinkActive : ""}`}
                          aria-current={active ? "true" : undefined}
                        >
                          <span>{group.label}</span>
                          {group.badge != null && (
                            <span
                              className={
                                group.alertBadge ? styles.groupBadgeAlert : styles.groupBadge
                              }
                            >
                              {group.badge}
                            </span>
                          )}
                        </Link>
                      );
                    })}
                    <span
                      aria-hidden="true"
                      className={`${styles.tierOneIndicator} ${tier1Bar.rect.ready ? styles.indicatorReady : ""} ${tier1Bar.rect.animate ? styles.indicatorAnimate : ""}`}
                      style={{
                        transform: `translate3d(${tier1Bar.rect.x}px, 0, 0) scaleX(${tier1Bar.rect.w})`,
                      }}
                    />
                  </div>
                </nav>
              </div>

              <div className={styles.tierOneRight}>
                <button
                  type="button"
                  className={styles.cmdTrigger}
                  onClick={openPalette}
                  aria-label="Mở tìm nhanh ⌘K"
                >
                  <IconSearch size={14} />
                  <span className={styles.cmdLabel}>Tìm nhanh</span>
                  <kbd className={styles.cmdKbd}>
                    <IconCommand size={12} />
                    <span>K</span>
                  </kbd>
                </button>

                <button
                  type="button"
                  className={styles.mobileSearchBtn}
                  onClick={openPalette}
                  aria-label="Mở tìm nhanh"
                >
                  <IconSearch size={18} />
                </button>

                <Link href="/admin/audit-log" className={styles.accountLink}>
                  <span className={styles.avatarChip} aria-hidden="true">
                    {initials}
                  </span>
                  <span className={styles.accountName}>{displayName}</span>
                </Link>

                <Link
                  href="/"
                  className={styles.homeIconLink}
                  title="Trang chủ NexusMart"
                  aria-label="Trang chủ NexusMart"
                >
                  <IconArrowUpRight size={15} />
                </Link>
              </div>
            </div>
          </div>

          {/* Tier 2: Crisp white sub-navigation for active group's screens with sliding underline */}
          <nav className={styles.tierTwo} aria-label={`Màn hình thuộc ${activeGroup.label}`}>
            <div className={styles.tierTwoInner}>
              <div
                ref={tier2Bar.containerRef}
                className={`${styles.subNavTrack} ${tier2Bar.rect.ready ? styles.trackMeasured : ""}`}
              >
                {activeGroup.items.map((item) => {
                  const active = isRouteActive(item.href);
                  return (
                    <Link
                      key={item.href}
                      href={item.href}
                      className={`${styles.subNavLink} ${active ? styles.subNavLinkActive : ""}`}
                      aria-current={active ? "page" : undefined}
                    >
                      {item.icon}
                      <span>{item.label}</span>
                      {item.badge != null && (
                        <span
                          className={item.alertBadge ? styles.subNavBadgeAlert : styles.subNavBadge}
                        >
                          {item.badge}
                        </span>
                      )}
                    </Link>
                  );
                })}
                <span
                  aria-hidden="true"
                  className={`${styles.tierTwoIndicator} ${tier2Bar.rect.ready ? styles.indicatorReady : ""} ${tier2Bar.rect.animate ? styles.indicatorAnimate : ""}`}
                  style={{
                    transform: `translate3d(${tier2Bar.rect.x}px, 0, 0) scaleX(${tier2Bar.rect.w})`,
                  }}
                />
              </div>
            </div>
          </nav>
        </header>

        {/* White paper sheet framed by --bg-2 */}
        <div className={styles.sheetWrap}>
          <main className={styles.sheet}>{children}</main>
        </div>

        {/* Mobile Bottom Sheet Navigation */}
        {sheetMounted && (
          <div
            className={`${styles.mobileSheetRoot} ${sheetVisible ? styles.mobileSheetOpen : ""}`}
            role="presentation"
          >
            <div
              className={styles.mobileSheetBackdrop}
              onClick={() => closeSheet(true)}
              aria-hidden="true"
            />
            <aside
              ref={mobileSheetPanelRef}
              tabIndex={-1}
              className={styles.mobileSheetPanel}
              role="dialog"
              aria-modal="true"
              aria-label="Điều hướng quản trị"
            >
              <div className={styles.mobileSheetHandle} aria-hidden="true" />
              <div className={styles.mobileSheetHead}>
                <span className={styles.mobileSheetBrand}>
                  <NexusMark size={18} />
                  <span>NexusMart Quản trị</span>
                </span>
                <button
                  type="button"
                  className={styles.mobileSheetClose}
                  onClick={() => closeSheet(true)}
                  aria-label="Đóng trình đơn"
                >
                  <IconClose size={18} />
                </button>
              </div>

              <form className={styles.mobileSheetSearch} onSubmit={handleMobileSearchSubmit}>
                <span className={styles.mobileSheetSearchIcon}>
                  <IconSearch size={15} />
                </span>
                <input
                  ref={mobileSearchInputRef}
                  type="search"
                  className={styles.mobileSheetInput}
                  value={mobileSearch}
                  onChange={(e) => setMobileSearch(e.target.value)}
                  placeholder="Tìm màn hình, mã đơn, gian hàng..."
                  aria-label="Tìm nhanh quản trị"
                />
              </form>

              <div className={styles.mobileSheetBody}>
                {mobileFilteredGroups.map((group) => (
                  <div key={group.id} className={styles.mobileGroupBlock}>
                    <div className={styles.mobileGroupHeading}>{group.label}</div>
                    <div className={styles.mobileGroupList}>
                      {group.items.map((item) => {
                        const active = isRouteActive(item.href);
                        return (
                          <Link
                            key={item.href}
                            href={item.href}
                            className={`${styles.mobileNavLink} ${active ? styles.mobileNavLinkActive : ""}`}
                            aria-current={active ? "page" : undefined}
                            onClick={() => closeSheet(false)}
                          >
                            {item.icon}
                            <span className={styles.mobileNavLabel}>{item.label}</span>
                            {item.badge != null && (
                              <span
                                className={
                                  item.alertBadge ? styles.subNavBadgeAlert : styles.subNavBadge
                                }
                              >
                                {item.badge}
                              </span>
                            )}
                          </Link>
                        );
                      })}
                    </div>
                  </div>
                ))}

                {mobileSearch.trim().length > 0 && (
                  <div className={styles.mobileGroupBlock}>
                    <button
                      type="button"
                      className={styles.mobileNavLink}
                      onClick={() => {
                        const trimmed = mobileSearch.trim();
                        closeSheet(false);
                        router.push(resolveQuickSearchTarget(trimmed));
                      }}
                    >
                      <IconSearch size={15} />
                      <span className={styles.mobileNavLabel}>
                        Tìm &ldquo;{mobileSearch.trim()}&rdquo; trong hồ sơ
                      </span>
                    </button>
                  </div>
                )}

                {mobileSearch.trim().length === 0 && (
                  <div className={styles.mobileGroupBlock}>
                    <Link
                      href="/"
                      className={styles.mobileNavLink}
                      onClick={() => closeSheet(false)}
                    >
                      <IconArrowUpRight size={15} />
                      <span className={styles.mobileNavLabel}>Trang chủ NexusMart</span>
                    </Link>
                  </div>
                )}
              </div>
            </aside>
          </div>
        )}

        {/* ⌘K Command Palette Dialog */}
        {paletteMounted && (
          <div
            className={`${styles.paletteRoot} ${paletteVisible ? styles.paletteOpen : ""}`}
            role="presentation"
          >
            <div
              className={styles.paletteBackdrop}
              onClick={() => closePalette(true)}
              aria-hidden="true"
            />
            <div
              ref={paletteDialogRef}
              tabIndex={-1}
              className={styles.paletteDialog}
              role="dialog"
              aria-modal="true"
              aria-label="Tìm nhanh màn hình và hồ sơ quản trị"
              onKeyDown={handlePaletteDialogKeyDown}
            >
              <div className={styles.paletteInputBar}>
                <IconSearch size={16} className={styles.paletteSearchIcon} />
                <input
                  ref={paletteInputRef}
                  type="text"
                  role="combobox"
                  aria-expanded={paletteOpen}
                  aria-controls="admin-palette-listbox"
                  aria-autocomplete="list"
                  aria-activedescendant={
                    clampedPaletteIndex >= 0
                      ? `admin-palette-opt-${clampedPaletteIndex}`
                      : undefined
                  }
                  className={styles.paletteInput}
                  value={paletteQuery}
                  onChange={(e) => {
                    setPaletteQuery(e.target.value);
                    setPaletteIndex(0);
                  }}
                  placeholder="Nhảy tới màn hình hoặc gõ mã (DS-, KN-, SP-, FS-, site:)..."
                  aria-label="Nhập tên màn hình hoặc mã cần tìm"
                />
                <button
                  type="button"
                  className={styles.paletteEscBtn}
                  onClick={() => closePalette(true)}
                >
                  Esc
                </button>
              </div>

              <div
                id="admin-palette-listbox"
                className={styles.paletteResults}
                role="listbox"
                aria-label="Kết quả gợi ý"
              >
                {paletteMatches.map((opt, idx) => {
                  const selected = idx === clampedPaletteIndex;
                  return (
                    <button
                      key={opt.href}
                      id={`admin-palette-opt-${idx}`}
                      ref={(node) => {
                        paletteOptionRefs.current[idx] = node;
                      }}
                      type="button"
                      role="option"
                      aria-selected={selected}
                      className={`${styles.paletteOption} ${selected ? styles.paletteOptionActive : ""}`}
                      onMouseEnter={() => setPaletteIndex(idx)}
                      onClick={() => runPaletteChoice(idx)}
                    >
                      <span className={styles.paletteOptionLeft}>
                        {opt.icon}
                        <span className={styles.paletteOptionLabel}>{opt.label}</span>
                      </span>
                      <span className={styles.paletteOptionRight}>
                        {opt.badge != null && (
                          <span
                            className={
                              opt.alertBadge ? styles.subNavBadgeAlert : styles.subNavBadge
                            }
                          >
                            {opt.badge}
                          </span>
                        )}
                        <span className={styles.paletteOptionMeta}>{opt.meta}</span>
                      </span>
                    </button>
                  );
                })}

                {hasDirectSearchFallback && (
                  <button
                    id={`admin-palette-opt-${paletteMatches.length}`}
                    ref={(node) => {
                      paletteOptionRefs.current[paletteMatches.length] = node;
                    }}
                    type="button"
                    role="option"
                    aria-selected={clampedPaletteIndex === paletteMatches.length}
                    className={`${styles.paletteOption} ${clampedPaletteIndex === paletteMatches.length ? styles.paletteOptionActive : ""}`}
                    onMouseEnter={() => setPaletteIndex(paletteMatches.length)}
                    onClick={() => runPaletteChoice(paletteMatches.length)}
                  >
                    <span className={styles.paletteOptionLeft}>
                      <IconSearch size={15} />
                      <span className={styles.paletteOptionLabel}>
                        Tìm &ldquo;{paletteQuery.trim()}&rdquo;
                      </span>
                    </span>
                    <span className={styles.paletteOptionMeta}>Chuyển trang lọc</span>
                  </button>
                )}
              </div>

              <div className={styles.paletteFooter}>
                <span>↑↓ chọn</span>
                <span>Enter mở</span>
                <span>Esc đóng</span>
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
    </AdminContext.Provider>
  );
}
