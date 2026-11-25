"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { ANNOUNCEMENT_BAR, CATEGORIES, MEGA_MENU_GROUPS } from "@/data";
import { api } from "@/lib/api";
import type {
  GatewayActiveShopDiscount,
  GatewayActiveShopDiscountsResponse,
  GatewayFlashSale,
  GatewayFlashSaleItem,
  GatewayFlashSalesResponse,
  GatewayNotificationsResponse,
  GatewaySearchHistoryItem,
  GatewaySearchProductHit,
  GatewaySearchSuggestEntity,
} from "@/lib/gateway-types";
import { buildActiveShopDiscountsMap, buildLiveFlashItemsBySkuId } from "@/lib/catalog-mapper";
import { fetchActiveShopDiscountsWithFetcher } from "@/features/checkout/helpers";
import {
  SEARCH_HISTORY_MAX_ITEMS,
  SEARCH_SUGGEST_DEBOUNCE_MS,
  clearSearchHistoryClient,
  collectSearchHitSkuIds,
  computeSearchHitDisplayPricing,
  deleteSearchHistoryItemClient,
  fetchSearchHistoryClient,
  fetchSearchSuggestClient,
} from "@/lib/search-client";
import { formatVnd } from "@/lib/format";
import { useSession } from "@/context/SessionContext";
import { useCart } from "@/context/CartContext";
import { useMockStore } from "@/mock/MockStoreContext";
import {
  IconBag,
  IconBell,
  IconChevronDown,
  IconClose,
  IconHeart,
  IconMenu,
  IconSearch,
  IconUser,
  NexusMark,
} from "./Icons";
import styles from "./ShopHeader.module.css";

interface MegaEditorialConfig {
  title: string;
  caption: string;
  ctaLabel: string;
  href: string;
  categorySlug: string;
  fallbackImage: string;
}

const MEGA_EDITORIAL_MAP: Record<string, MegaEditorialConfig> = {
  "loai-do": {
    title: "Cơ bản hằng ngày",
    caption: "Cotton chải kỹ 220gsm đứng dáng, thoáng mát và bền màu qua nhiều mùa.",
    ctaLabel: "Xem bộ sưu tập",
    href: "/bo-suu-tap/co-ban-hang-ngay",
    categorySlug: "ao-thun",
    fallbackImage: "/products/ao-thun-cotton-trang.png",
  },
  "hoan-canh": {
    title: "Công sở gọn gàng",
    caption: "Áo Polo Pique mắt chim và dệt kim mịn thay thế sơ mi cứng nhắc.",
    ctaLabel: "Khám phá ngay",
    href: "/bo-suu-tap/cong-so-gon-gang",
    categorySlug: "ao-polo",
    fallbackImage: "/products/ao-polo-pique-xanh-da.png",
  },
  "the-thao": {
    title: "Chạy bộ & Tập luyện",
    caption: "Vải nhẹ 118-145gsm thoát ẩm nhanh, đường may phẳng hạn chế cọ xát.",
    ctaLabel: "Xem đồ vận động",
    href: "/bo-suu-tap/chay-bo-tap-luyen",
    categorySlug: "quan-short",
    fallbackImage: "/products/ao-thun-dryfit-co-vit.png",
  },
  "bo-suu-tap": {
    title: "Sợi Xanh Nexus",
    caption: "Chất liệu sợi tre, gỗ sồi Modal và lanh tự nhiên cùng quỹ đồng phục vùng cao.",
    ctaLabel: "Tìm hiểu chiến dịch",
    href: "/chien-dich/soi-xanh-nexus",
    categorySlug: "quan-jogger",
    fallbackImage: "/products/quan-short-linen-xam-da.png",
  },
};

const FEATURED_COLLECTIONS_LINKS = [
  { label: "Cơ bản hằng ngày", href: "/bo-suu-tap/co-ban-hang-ngay" },
  { label: "Công sở gọn gàng", href: "/bo-suu-tap/cong-so-gon-gang" },
  { label: "Chạy bộ & Tập luyện", href: "/bo-suu-tap/chay-bo-tap-luyen" },
  { label: "Sợi Xanh Nexus", href: "/chien-dich/soi-xanh-nexus" },
];

const MOBILE_CATEGORY_SECTIONS = [
  {
    id: "mob-ao",
    title: "Áo mặc hằng ngày",
    links: [
      { label: "Áo thun Cotton 220gsm", href: "/danh-muc?cat=ao-thun&material=cotton-compact" },
      {
        label: "Áo thun Thể thao Dry-Fit",
        href: "/danh-muc?cat=ao-thun&material=polyester-dryfit",
      },
      { label: "Áo thun Oversize 260gsm", href: "/danh-muc?cat=ao-thun&fit=oversize" },
      { label: "Áo Polo Pique & Dệt kim", href: "/danh-muc?cat=ao-polo" },
      { label: "Áo khoác gió gấp gọn", href: "/danh-muc?cat=tat-phu-kien&material=nylon-ripstop" },
    ],
  },
  {
    id: "mob-quan",
    title: "Quần & Thể thao",
    links: [
      { label: "Quần Short Daily 7 inch", href: "/danh-muc?cat=quan-short&occasion=hang-ngay" },
      { label: "Quần Short Chạy bộ 5 inch", href: "/danh-muc?cat=quan-short&sport=chay-bo" },
      {
        label: "Quần Short Linen pha Cotton",
        href: "/danh-muc?cat=quan-short&material=linen-cotton",
      },
      { label: "Quần Jogger Nỉ chân cua", href: "/danh-muc?cat=quan-jogger&material=french-terry" },
      { label: "Quần Jogger Tech 4 chiều", href: "/danh-muc?cat=quan-jogger&material=nylon-tech" },
    ],
  },
  {
    id: "mob-dolot",
    title: "Đồ lót & Tất",
    links: [
      { label: "Combo 3 Boxer Sợi Modal", href: "/san-pham/combo-3-quan-lot-boxer-modal" },
      { label: "Combo 3 Brief Sợi tre", href: "/san-pham/combo-3-quan-lot-brief-soi-tre" },
      { label: "Combo 5 Tất cổ trung", href: "/san-pham/combo-5-doi-tat-cotton-gan-co-trung" },
      { label: "Combo 4 Tất thể thao", href: "/san-pham/combo-4-doi-tat-the-thao-co-thap" },
    ],
  },
  {
    id: "mob-bst",
    title: "Bộ sưu tập nổi bật",
    links: FEATURED_COLLECTIONS_LINKS,
  },
];

export function ShopHeader() {
  const pathname = usePathname();
  const router = useRouter();
  const { user, isAuthenticated, logout } = useSession();
  const { cartCount } = useCart();
  const { wishlist, openCookieModal } = useMockStore();

  const [drawerOpen, setDrawerOpen] = useState(false);
  const [accountMenuOpen, setAccountMenuOpen] = useState(false);
  const [openMobileGroup, setOpenMobileGroup] = useState<string>("mob-ao");
  const [megaOpen, setMegaOpen] = useState(false);
  const [activeMegaGroupId, setActiveMegaGroupId] = useState<string>(MEGA_MENU_GROUPS[0].id);
  const [announcementIdx, setAnnouncementIdx] = useState(0);
  const [announcementVisible, setAnnouncementVisible] = useState(true);

  const [searchQuery, setSearchQuery] = useState("");
  const [searchOpen, setSearchOpen] = useState(false);
  const [mobileSearchFocus, setMobileSearchFocus] = useState(false);
  const [searchResults, setSearchResults] = useState<GatewaySearchProductHit[]>([]);
  const [suggestKeywords, setSuggestKeywords] = useState<string[]>([]);
  const [suggestCategories, setSuggestCategories] = useState<GatewaySearchSuggestEntity[]>([]);
  const [suggestShops, setSuggestShops] = useState<GatewaySearchSuggestEntity[]>([]);
  const [serverTrending, setServerTrending] = useState<string[]>([]);
  const [searchHistory, setSearchHistory] = useState<GatewaySearchHistoryItem[]>([]);
  const [historyBusyId, setHistoryBusyId] = useState<string | null>(null);
  const [clearingHistory, setClearingHistory] = useState(false);
  const [searchLiveFlashBySkuId, setSearchLiveFlashBySkuId] = useState<
    Map<string, GatewayFlashSaleItem>
  >(() => new Map());
  const [searchShopDiscountsBySkuId, setSearchShopDiscountsBySkuId] = useState<
    Map<string, GatewayActiveShopDiscount>
  >(() => new Map());
  const [searchLoading, setSearchLoading] = useState(false);
  const [searchError, setSearchError] = useState<string | null>(null);
  const [unreadNotifications, setUnreadNotifications] = useState(0);

  const headerRef = useRef<HTMLElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const mobileSearchRef = useRef<HTMLInputElement>(null);
  const megaCloseTimerRef = useRef<number | null>(null);
  const searchReqSeq = useRef(0);
  const historyReqSeq = useRef(0);
  const flashCacheRef = useRef<{ fetchedAt: number; sales: GatewayFlashSale[] } | null>(null);

  const isHome = pathname === "/";
  const wishlistCount = wishlist.length;
  const currentAnnouncement = ANNOUNCEMENT_BAR[announcementIdx % ANNOUNCEMENT_BAR.length];

  const activeMegaGroup = useMemo(
    () => MEGA_MENU_GROUPS.find((g) => g.id === activeMegaGroupId) ?? MEGA_MENU_GROUPS[0],
    [activeMegaGroupId]
  );

  const activeEditorial = useMemo(() => {
    const cfg = MEGA_EDITORIAL_MAP[activeMegaGroup.id] ?? MEGA_EDITORIAL_MAP["loai-do"];
    const cat = CATEGORIES.find((c) => c.slug === cfg.categorySlug);
    return {
      ...cfg,
      image: cat?.featuredImage || cfg.fallbackImage,
    };
  }, [activeMegaGroup.id]);

  // Fetch unread notification count for authenticated users
  useEffect(() => {
    if (!isAuthenticated) {
      setUnreadNotifications(0);
      return;
    }
    let active = true;
    void api<GatewayNotificationsResponse>("/notifications?page=1&page_size=5")
      .then((res) => {
        if (active && typeof res?.unread_count === "number") {
          setUnreadNotifications(Math.max(0, Math.trunc(res.unread_count)));
        }
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [isAuthenticated, pathname]);

  // Load logged-in user's search history (<= 50 items) strictly when authenticated (G14: 0 calls / 0 401 for guests).
  useEffect(() => {
    if (!isAuthenticated) {
      historyReqSeq.current += 1;
      setSearchHistory([]);
      return;
    }
    if (!searchOpen && !drawerOpen) return;
    const seq = ++historyReqSeq.current;
    void fetchSearchHistoryClient(SEARCH_HISTORY_MAX_ITEMS)
      .then((items) => {
        if (historyReqSeq.current === seq) {
          setSearchHistory(items.slice(0, SEARCH_HISTORY_MAX_ITEMS));
        }
      })
      .catch(() => {
        // Best-effort history load; keep current state.
      });
  }, [isAuthenticated, searchOpen, drawerOpen, pathname]);

  // Debounced (200ms) SearchService suggest via GET /api/gw/search/suggest enriched with live flash & shop discounts (UC-B12).
  useEffect(() => {
    if (!searchOpen && !drawerOpen) return;
    const trimmed = searchQuery.trim();
    const seq = ++searchReqSeq.current;
    setSearchLoading(true);
    setSearchError(null);
    const timer = window.setTimeout(() => {
      const runSuggest = async () => {
        try {
          const res = await fetchSearchSuggestClient(trimmed, 8);
          if (searchReqSeq.current !== seq) return;
          const items = Array.isArray(res?.products) ? res.products : [];
          const nextKeywords = Array.isArray(res?.keywords) ? res.keywords : [];
          const nextCategories = Array.isArray(res?.categories) ? res.categories : [];
          const nextShops = Array.isArray(res?.shops) ? res.shops : [];
          const nextTrending = Array.isArray(res?.trending) ? res.trending : [];

          const skuIds = collectSearchHitSkuIds(items);
          if (skuIds.length === 0) {
            setSuggestKeywords(nextKeywords);
            setSuggestCategories(nextCategories);
            setSuggestShops(nextShops);
            setServerTrending(nextTrending);
            setSearchResults(items);
            setSearchLiveFlashBySkuId(new Map());
            setSearchShopDiscountsBySkuId(new Map());
            setSearchError(null);
            return;
          }

          const nowMs = Date.now();
          const cachedFlash = flashCacheRef.current;
          const flashPromise =
            cachedFlash && nowMs - cachedFlash.fetchedAt < 30_000
              ? Promise.resolve(cachedFlash.sales)
              : api<GatewayFlashSalesResponse>("/flash-sales?include_upcoming=false")
                  .then((fsRes) => {
                    const sales = Array.isArray(fsRes?.flash_sales) ? fsRes.flash_sales : [];
                    flashCacheRef.current = { fetchedAt: Date.now(), sales };
                    return sales;
                  })
                  .catch(() => [] as GatewayFlashSale[]);

          const shopDiscPromise = fetchActiveShopDiscountsWithFetcher({ skuIds }, async (batch) => {
            const dqs = new URLSearchParams();
            if (batch.skuIds && batch.skuIds.length > 0) {
              dqs.set("sku_ids", batch.skuIds.join(","));
            }
            const discRes = await api<GatewayActiveShopDiscountsResponse>(
              `/shop-discounts/active?${dqs.toString()}`
            );
            return Array.isArray(discRes?.discounts) ? discRes.discounts : [];
          }).catch(() => [] as GatewayActiveShopDiscount[]);

          const [rawFlashSales, activeDiscounts] = await Promise.all([
            flashPromise,
            shopDiscPromise,
          ]);
          if (searchReqSeq.current !== seq) return;

          const nextFlashMap = buildLiveFlashItemsBySkuId(rawFlashSales, Date.now());
          const nextDiscMap = buildActiveShopDiscountsMap(activeDiscounts);

          // Commit suggest results and live pricing maps together so base prices never flash before discounts
          setSuggestKeywords(nextKeywords);
          setSuggestCategories(nextCategories);
          setSuggestShops(nextShops);
          setServerTrending(nextTrending);
          setSearchResults(items);
          setSearchLiveFlashBySkuId(nextFlashMap);
          setSearchShopDiscountsBySkuId(nextDiscMap);
          setSearchError(null);
        } catch {
          if (searchReqSeq.current !== seq) return;
          setSuggestKeywords([]);
          setSuggestCategories([]);
          setSuggestShops([]);
          setSearchResults([]);
          setSearchLiveFlashBySkuId(new Map());
          setSearchShopDiscountsBySkuId(new Map());
          setSearchError(
            "Không thể tải gợi ý tìm kiếm lúc này. Vui lòng thử lại hoặc nhấn Enter để tìm."
          );
        } finally {
          if (searchReqSeq.current === seq) {
            setSearchLoading(false);
          }
        }
      };
      void runSuggest();
    }, SEARCH_SUGGEST_DEBOUNCE_MS);
    return () => window.clearTimeout(timer);
  }, [searchQuery, searchOpen, drawerOpen]);

  const handleDeleteHistoryItem = async (id: string) => {
    if (!isAuthenticated || historyBusyId === id) return;
    setHistoryBusyId(id);
    const prev = searchHistory;
    setSearchHistory((cur) => cur.filter((item) => item.id !== id));
    try {
      await deleteSearchHistoryItemClient(id);
    } catch {
      setSearchHistory(prev);
    } finally {
      setHistoryBusyId((cur) => (cur === id ? null : cur));
    }
  };

  const handleClearHistory = async () => {
    if (!isAuthenticated || clearingHistory) return;
    setClearingHistory(true);
    const prev = searchHistory;
    setSearchHistory([]);
    try {
      await clearSearchHistoryClient();
    } catch {
      setSearchHistory(prev);
    } finally {
      setClearingHistory(false);
    }
  };

  const clearMegaCloseTimer = () => {
    if (megaCloseTimerRef.current !== null) {
      window.clearTimeout(megaCloseTimerRef.current);
      megaCloseTimerRef.current = null;
    }
  };

  const openMegaMenu = () => {
    clearMegaCloseTimer();
    setSearchOpen(false);
    setMegaOpen(true);
  };

  const scheduleMegaClose = () => {
    clearMegaCloseTimer();
    megaCloseTimerRef.current = window.setTimeout(() => {
      setMegaOpen(false);
      megaCloseTimerRef.current = null;
    }, 160);
  };

  useEffect(() => {
    return () => clearMegaCloseTimer();
  }, []);

  // Close menus on route change
  useEffect(() => {
    clearMegaCloseTimer();
    setDrawerOpen(false);
    setMegaOpen(false);
    setSearchOpen(false);
    setAccountMenuOpen(false);
    setMobileSearchFocus(false);
  }, [pathname]);

  // Focus mobile search input when opened via search icon
  useEffect(() => {
    if (drawerOpen && mobileSearchFocus) {
      window.setTimeout(() => mobileSearchRef.current?.focus(), 60);
    }
  }, [drawerOpen, mobileSearchFocus]);

  // Close dropdowns on Escape or outside click
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        clearMegaCloseTimer();
        setMegaOpen(false);
        setSearchOpen(false);
        setAccountMenuOpen(false);
        setDrawerOpen(false);
      }
    };
    const onPointerDown = (e: MouseEvent) => {
      if (headerRef.current && !headerRef.current.contains(e.target as Node)) {
        clearMegaCloseTimer();
        setMegaOpen(false);
        setSearchOpen(false);
        setAccountMenuOpen(false);
      }
    };
    document.addEventListener("keydown", onKeyDown);
    document.addEventListener("mousedown", onPointerDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.removeEventListener("mousedown", onPointerDown);
    };
  }, []);

  const isActive = (href: string) =>
    href === "/" ? pathname === "/" : pathname === href || pathname.startsWith(`${href}/`);

  const handleSkipToMain = (e: React.MouseEvent<HTMLAnchorElement>) => {
    const mainEl = document.querySelector<HTMLElement>("main");
    if (mainEl) {
      e.preventDefault();
      mainEl.focus();
      mainEl.scrollIntoView({ behavior: "smooth", block: "start" });
    }
  };

  const handleSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const clean = searchQuery.trim();
    setSearchOpen(false);
    setDrawerOpen(false);
    if (clean) {
      router.push(`/tim-kiem?q=${encodeURIComponent(clean)}`);
    } else {
      router.push("/danh-muc");
    }
  };

  const handleSelectKeyword = (query: string) => {
    setSearchQuery(query);
    setSearchOpen(false);
    setDrawerOpen(false);
    router.push(`/tim-kiem?q=${encodeURIComponent(query)}`);
  };

  const handleLogout = async () => {
    setAccountMenuOpen(false);
    setDrawerOpen(false);
    await logout();
    window.location.assign("/");
  };

  const displayName = user?.full_name?.trim() || user?.email || "";
  const isSeller = user?.role === "seller";
  const isAdmin =
    user?.role === "admin" ||
    user?.role === "super_admin" ||
    user?.role === "moderator" ||
    user?.role === "cskh" ||
    user?.role === "accountant" ||
    user?.role === "ops";

  return (
    <>
      <a href="#main-content" className={styles.skipLink} onClick={handleSkipToMain}>
        Bỏ qua đến nội dung
      </a>

      {announcementVisible && !isHome && (
        <div className={styles.announcementBar} role="region" aria-label="Thông báo ưu đãi">
          <div className={styles.announcementInner}>
            <p className={styles.announcementText}>
              <span className={styles.announcementDesktop}>{currentAnnouncement.text}</span>
              <span className={styles.announcementMobile}>
                Ưu đãi từ xưởng dệt · Đổi trả trong 7 ngày
              </span>
              <Link href={currentAnnouncement.href} className={styles.announcementLink}>
                {currentAnnouncement.ctaLabel}
              </Link>
            </p>
            <div className={styles.announcementControls}>
              <button
                type="button"
                className={styles.announcementNext}
                onClick={() => setAnnouncementIdx((i) => (i + 1) % ANNOUNCEMENT_BAR.length)}
                aria-label="Xem thông báo tiếp theo"
              >
                Tiếp
              </button>
              <button
                type="button"
                className={styles.announcementDismiss}
                onClick={() => setAnnouncementVisible(false)}
                aria-label="Ẩn thanh thông báo"
              >
                <IconClose size={14} />
              </button>
            </div>
          </div>
        </div>
      )}

      <header ref={headerRef} className={styles.headerShell} onMouseLeave={scheduleMegaClose}>
        <div className={styles.header}>
          <div className={styles.inner}>
            <div className={styles.leftCluster}>
              <Link href="/" className={styles.brand} aria-label="NexusMart · Trang chủ">
                <NexusMark size={22} />
                <span className={styles.brandWord}>NexusMart</span>
              </Link>

              <nav className={styles.navDesktop} aria-label="Điều hướng chính">
                <button
                  type="button"
                  className={`${styles.navBtn} ${
                    megaOpen || isActive("/danh-muc") ? styles.navLinkActive : ""
                  }`}
                  aria-expanded={megaOpen}
                  aria-controls="nexus-mega-menu"
                  onMouseEnter={openMegaMenu}
                  onFocus={openMegaMenu}
                  onClick={() => {
                    clearMegaCloseTimer();
                    setSearchOpen(false);
                    setMegaOpen((v) => !v);
                  }}
                >
                  <span>Danh mục</span>
                  <IconChevronDown
                    size={14}
                    className={megaOpen ? styles.chevronOpen : styles.chevron}
                  />
                </button>
                <Link
                  href="/bo-suu-tap"
                  className={`${styles.navLink} ${isActive("/bo-suu-tap") ? styles.navLinkActive : ""}`}
                  onMouseEnter={scheduleMegaClose}
                >
                  Bộ sưu tập
                </Link>
                <Link
                  href="/flash-sale"
                  className={`${styles.navLink} ${isActive("/flash-sale") ? styles.navLinkActive : ""}`}
                  onMouseEnter={scheduleMegaClose}
                >
                  Flash sale
                </Link>
                <Link
                  href="/tap-chi"
                  className={`${styles.navLink} ${isActive("/tap-chi") ? styles.navLinkActive : ""}`}
                  onMouseEnter={scheduleMegaClose}
                >
                  Tạp chí
                </Link>
              </nav>
            </div>

            <div className={styles.searchWrap}>
              <form role="search" className={styles.searchForm} onSubmit={handleSearchSubmit}>
                <IconSearch size={16} className={styles.searchIcon} />
                <input
                  ref={searchInputRef}
                  type="search"
                  value={searchQuery}
                  onFocus={() => {
                    clearMegaCloseTimer();
                    setMegaOpen(false);
                    setSearchOpen(true);
                  }}
                  onChange={(e) => {
                    setSearchQuery(e.target.value);
                    setSearchOpen(true);
                  }}
                  placeholder="Tìm áo thun, polo, quần..."
                  aria-label="Tìm kiếm sản phẩm"
                  className={styles.searchInput}
                />
                {searchQuery.trim() && (
                  <button
                    type="button"
                    className={styles.searchClearBtn}
                    onClick={() => {
                      setSearchQuery("");
                      searchInputRef.current?.focus();
                    }}
                    aria-label="Xóa từ khóa tìm kiếm"
                  >
                    <IconClose size={14} />
                  </button>
                )}
              </form>

              {searchOpen && (
                <div
                  className={styles.searchDropdown}
                  data-ui="search-dropdown"
                  role="region"
                  aria-label="Gợi ý tìm kiếm"
                >
                  {isAuthenticated && searchHistory.length > 0 && (
                    <div className={styles.searchSection} data-ui="search-history-section">
                      <div className={styles.historyHeader}>
                        <p className={styles.dropdownHeading}>Tìm kiếm gần đây</p>
                        <button
                          type="button"
                          className={styles.dropdownCloseBtn}
                          disabled={clearingHistory}
                          onClick={() => void handleClearHistory()}
                        >
                          {clearingHistory ? "Đang xóa..." : "Xóa tất cả"}
                        </button>
                      </div>
                      <div className={`${styles.keywordList} ${styles.historyList}`}>
                        {searchHistory.slice(0, SEARCH_HISTORY_MAX_ITEMS).map((item) => (
                          <span
                            key={item.id}
                            data-ui="search-history-item"
                            className={styles.historyChipWrap}
                          >
                            <button
                              type="button"
                              className={styles.keywordChip}
                              onClick={() => handleSelectKeyword(item.query)}
                            >
                              {item.query}
                            </button>
                            <button
                              type="button"
                              className={styles.searchClearBtn}
                              disabled={historyBusyId === item.id}
                              onClick={() => void handleDeleteHistoryItem(item.id)}
                              aria-label={`Xóa lịch sử tìm kiếm ${item.query}`}
                            >
                              <IconClose size={12} />
                            </button>
                          </span>
                        ))}
                      </div>
                    </div>
                  )}

                  {searchQuery.trim() && suggestKeywords.length > 0 && (
                    <div className={styles.searchSection}>
                      <p className={styles.dropdownHeading}>Gợi ý từ khóa</p>
                      <div className={styles.keywordList}>
                        {suggestKeywords.slice(0, 8).map((kw) => (
                          <button
                            key={kw}
                            type="button"
                            className={styles.keywordChip}
                            onClick={() => handleSelectKeyword(kw)}
                          >
                            {kw}
                          </button>
                        ))}
                      </div>
                    </div>
                  )}

                  {serverTrending.length > 0 && (
                    <div className={styles.searchSection}>
                      <p className={styles.dropdownHeading}>Tìm kiếm phổ biến</p>
                      <div className={styles.keywordList}>
                        {serverTrending.slice(0, 8).map((kw) => (
                          <button
                            key={kw}
                            type="button"
                            className={styles.keywordChip}
                            onClick={() => handleSelectKeyword(kw)}
                          >
                            {kw}
                          </button>
                        ))}
                      </div>
                    </div>
                  )}

                  {suggestCategories.length > 0 && (
                    <div className={styles.searchSection}>
                      <p className={styles.dropdownHeading}>Danh mục phù hợp</p>
                      <div className={styles.keywordList}>
                        {suggestCategories.slice(0, 6).map((cat) => (
                          <Link
                            key={cat.id}
                            href={`/danh-muc?cat=${encodeURIComponent(cat.slug)}`}
                            className={styles.keywordChip}
                            onClick={() => setSearchOpen(false)}
                          >
                            {cat.name}
                          </Link>
                        ))}
                      </div>
                    </div>
                  )}

                  {suggestShops.length > 0 && (
                    <div className={styles.searchSection}>
                      <p className={styles.dropdownHeading}>Gian hàng phù hợp</p>
                      <div className={styles.keywordList}>
                        {suggestShops.slice(0, 6).map((shop) => (
                          <Link
                            key={shop.id}
                            href={`/gian-hang/${encodeURIComponent(shop.slug)}`}
                            className={styles.keywordChip}
                            onClick={() => setSearchOpen(false)}
                          >
                            {shop.name}
                          </Link>
                        ))}
                      </div>
                    </div>
                  )}

                  <div className={styles.searchSection}>
                    <p className={styles.dropdownHeading}>
                      {searchQuery.trim()
                        ? `Sản phẩm khớp với "${searchQuery.trim()}"`
                        : "Sản phẩm nổi bật"}
                    </p>

                    {searchLoading ? (
                      <div className={styles.emptySearchBox}>
                        <p className={styles.emptySearchText}>Đang tìm kiếm sản phẩm...</p>
                      </div>
                    ) : searchError ? (
                      <div className={styles.emptySearchBox} role="alert">
                        <p className={styles.emptySearchText}>{searchError}</p>
                      </div>
                    ) : searchResults.length === 0 ? (
                      <div className={styles.emptySearchBox}>
                        <p className={styles.emptySearchText}>
                          Chưa thấy sản phẩm khớp trực tiếp với từ khóa này.
                        </p>
                      </div>
                    ) : (
                      <ul className={styles.resultList}>
                        {searchResults.map((hit) => {
                          const thumb = hit.thumbnail_url || "/products/ao-thun-cotton-trang.png";
                          const prodSlug = hit.slug || hit.id;
                          const pricing = computeSearchHitDisplayPricing(
                            hit,
                            searchLiveFlashBySkuId,
                            searchShopDiscountsBySkuId
                          );
                          const promoLabel =
                            pricing.promoKind === "flash"
                              ? "Flash sale"
                              : pricing.promoKind === "shop_discount"
                                ? "Giảm giá gian hàng"
                                : "";
                          const subBase =
                            hit.shop_name?.trim() ||
                            (hit.sku_count > 0 ? `${hit.sku_count} phân loại` : "NexusMart");
                          return (
                            <li key={hit.id}>
                              <Link
                                href={`/san-pham/${encodeURIComponent(prodSlug)}`}
                                className={styles.resultItem}
                                onClick={() => setSearchOpen(false)}
                              >
                                <Image
                                  src={thumb}
                                  alt={hit.name}
                                  width={44}
                                  height={58}
                                  className={styles.resultThumb}
                                />
                                <div className={styles.resultMeta}>
                                  <span className={styles.resultName}>{hit.name}</span>
                                  <span className={styles.resultSub}>
                                    {subBase}
                                    {promoLabel ? ` · ${promoLabel}` : ""}
                                  </span>
                                </div>
                                <div className={styles.resultPriceCol}>
                                  <span className={`${styles.resultPrice} num`}>
                                    {pricing.maxPriceVnd > pricing.minPriceVnd
                                      ? `${formatVnd(pricing.minPriceVnd)} – ${formatVnd(pricing.maxPriceVnd)}`
                                      : formatVnd(pricing.minPriceVnd)}
                                  </span>
                                  {pricing.originalPriceVnd &&
                                    pricing.originalPriceVnd >
                                      Math.max(pricing.minPriceVnd, pricing.maxPriceVnd) && (
                                      <span className={`${styles.resultOldPrice} num`}>
                                        {formatVnd(pricing.originalPriceVnd)}
                                      </span>
                                    )}
                                </div>
                              </Link>
                            </li>
                          );
                        })}
                      </ul>
                    )}
                  </div>

                  <div className={styles.searchFooter}>
                    <Link
                      href={
                        searchQuery.trim()
                          ? `/tim-kiem?q=${encodeURIComponent(searchQuery.trim())}`
                          : "/danh-muc"
                      }
                      className={styles.searchAllLink}
                      onClick={() => setSearchOpen(false)}
                    >
                      {searchQuery.trim()
                        ? `Xem tất cả kết quả cho "${searchQuery.trim()}"`
                        : "Xem toàn bộ danh mục"}
                    </Link>
                    <button
                      type="button"
                      className={styles.dropdownCloseBtn}
                      onClick={() => setSearchOpen(false)}
                    >
                      Đóng
                    </button>
                  </div>
                </div>
              )}
            </div>

            <div className={styles.actionsDesktop}>
              {isAuthenticated && (
                <Link
                  href="/tai-khoan?tab=thong-bao"
                  className={styles.actionItem}
                  aria-label={`Thông báo (${unreadNotifications} chưa đọc)`}
                  title="Thông báo"
                >
                  <IconBell size={17} />
                  <span className={styles.actionLabelWide}>Thông báo</span>
                  {unreadNotifications > 0 && (
                    <span className={`${styles.countBadge} num`}>
                      {unreadNotifications > 99 ? "99+" : unreadNotifications}
                    </span>
                  )}
                </Link>
              )}

              {isAuthenticated && user ? (
                <div className={styles.accountMenuWrap}>
                  <button
                    type="button"
                    className={`${styles.actionItem} ${
                      accountMenuOpen || isActive("/tai-khoan") ? styles.actionItemActive : ""
                    }`}
                    aria-expanded={accountMenuOpen}
                    aria-haspopup="menu"
                    aria-label={`Tài khoản ${displayName}`}
                    title={displayName}
                    onClick={() => {
                      setSearchOpen(false);
                      setMegaOpen(false);
                      setAccountMenuOpen((v) => !v);
                    }}
                  >
                    <IconUser size={17} />
                    <span className={`${styles.actionLabel} ${styles.userNameLabel}`}>
                      {displayName}
                    </span>
                    <IconChevronDown
                      size={13}
                      className={accountMenuOpen ? styles.chevronOpen : styles.chevron}
                    />
                  </button>

                  {accountMenuOpen && (
                    <div className={styles.accountDropdown} role="menu" aria-label="Menu tài khoản">
                      <div className={styles.accountIdentity}>
                        <span className={styles.accountFullName}>{displayName}</span>
                        <span className={styles.accountEmail}>{user.email}</span>
                      </div>
                      <Link
                        href="/tai-khoan"
                        role="menuitem"
                        className={styles.accountMenuLink}
                        onClick={() => setAccountMenuOpen(false)}
                      >
                        Tài khoản của tôi
                      </Link>
                      <Link
                        href="/don-hang"
                        role="menuitem"
                        className={styles.accountMenuLink}
                        onClick={() => setAccountMenuOpen(false)}
                      >
                        Đơn hàng của tôi
                      </Link>
                      {isSeller && (
                        <Link
                          href="/seller"
                          prefetch={false}
                          role="menuitem"
                          className={styles.accountMenuLink}
                          onClick={() => setAccountMenuOpen(false)}
                        >
                          Kênh người bán
                        </Link>
                      )}
                      {isAdmin && (
                        <Link
                          href="/admin"
                          prefetch={false}
                          role="menuitem"
                          className={styles.accountMenuLink}
                          onClick={() => setAccountMenuOpen(false)}
                        >
                          Quản trị sàn
                        </Link>
                      )}
                      <button
                        type="button"
                        role="menuitem"
                        className={styles.accountLogoutBtn}
                        onClick={() => void handleLogout()}
                      >
                        Đăng xuất
                      </button>
                    </div>
                  )}
                </div>
              ) : (
                <Link
                  href="/dang-nhap"
                  className={`${styles.actionItem} ${isActive("/dang-nhap") ? styles.actionItemActive : ""}`}
                  aria-label="Đăng nhập tài khoản"
                  title="Đăng nhập"
                >
                  <IconUser size={17} />
                  <span className={styles.actionLabel}>Đăng nhập</span>
                </Link>
              )}

              <Link
                href="/tai-khoan?tab=yeu-thich"
                className={styles.actionItem}
                aria-label={`Sản phẩm yêu thích (${wishlistCount})`}
                title="Yêu thích"
              >
                <IconHeart size={17} />
                <span className={styles.actionLabel}>Yêu thích</span>
                <span className={`${styles.countBadge} num`}>{wishlistCount}</span>
              </Link>

              <Link
                href="/gio-hang"
                className={styles.cartAction}
                aria-label={`Giỏ hàng (${cartCount} sản phẩm)`}
              >
                <IconBag size={17} />
                <span>Giỏ hàng</span>
                <span className={`${styles.cartBadge} num`}>{cartCount}</span>
              </Link>
            </div>

            <div className={styles.mobileCluster}>
              <button
                type="button"
                className={styles.mobileIconBtn}
                onClick={() => {
                  setMobileSearchFocus(true);
                  setDrawerOpen(true);
                }}
                aria-label="Tìm kiếm sản phẩm"
              >
                <IconSearch size={18} />
              </button>
              {isAuthenticated && (
                <Link
                  href="/tai-khoan?tab=thong-bao"
                  className={styles.mobileCountLink}
                  aria-label={`Thông báo (${unreadNotifications} chưa đọc)`}
                >
                  <IconBell size={17} />
                  {unreadNotifications > 0 && (
                    <span className={`${styles.mobileBadge} num`}>
                      {unreadNotifications > 99 ? "99+" : unreadNotifications}
                    </span>
                  )}
                </Link>
              )}
              <Link
                href="/gio-hang"
                className={styles.mobileCountLink}
                aria-label={`Giỏ hàng (${cartCount})`}
              >
                <IconBag size={17} />
                <span className={`${styles.mobileBadgeDark} num`}>{cartCount}</span>
              </Link>
              <button
                type="button"
                className={styles.menuTrigger}
                onClick={() => {
                  setMobileSearchFocus(false);
                  setDrawerOpen(true);
                }}
                aria-expanded={drawerOpen}
                aria-label="Mở trình đơn điều hướng"
              >
                <IconMenu size={18} />
                <span>Menu</span>
              </button>
            </div>
          </div>
        </div>

        {megaOpen && (
          <div
            id="nexus-mega-menu"
            className={styles.megaPanel}
            data-ui="mega-menu"
            role="region"
            aria-label="Danh mục sản phẩm"
            onMouseEnter={clearMegaCloseTimer}
            onMouseLeave={scheduleMegaClose}
          >
            <div className={styles.megaInner}>
              <div className={styles.megaSidebar} role="tablist" aria-label="Nhóm danh mục">
                {MEGA_MENU_GROUPS.map((group) => {
                  const selected = group.id === activeMegaGroup.id;
                  return (
                    <button
                      key={group.id}
                      type="button"
                      role="tab"
                      aria-selected={selected}
                      className={`${styles.megaTab} ${selected ? styles.megaTabActive : ""}`}
                      onMouseEnter={() => setActiveMegaGroupId(group.id)}
                      onFocus={() => setActiveMegaGroupId(group.id)}
                      onClick={() => setActiveMegaGroupId(group.id)}
                    >
                      {group.label}
                    </button>
                  );
                })}
                <div className={styles.megaQuickBox}>
                  <Link
                    href="/tra-cuu-don-hang"
                    className={styles.megaQuickLink}
                    onClick={() => setMegaOpen(false)}
                  >
                    Tra cứu đơn hàng
                  </Link>
                  <Link
                    href="/tro-giup"
                    className={styles.megaQuickLink}
                    onClick={() => setMegaOpen(false)}
                  >
                    Trợ giúp & Đổi trả 7 ngày
                  </Link>
                </div>
              </div>

              <div className={styles.megaBody} role="tabpanel">
                <div className={styles.megaTopLine}>
                  <p className={styles.megaSummary}>{activeMegaGroup.summary}</p>
                  <Link
                    href={activeMegaGroup.href}
                    className={styles.megaAllLink}
                    onClick={() => setMegaOpen(false)}
                  >
                    Xem tất cả {activeMegaGroup.label.toLowerCase()}
                  </Link>
                </div>

                <div className={styles.megaColumns}>
                  {activeMegaGroup.columns.map((col) => (
                    <div key={col.id} className={styles.megaCol}>
                      <p className={styles.megaColTitle}>{col.title}</p>
                      <ul className={styles.megaColList}>
                        {col.items.map((item) => (
                          <li key={item.href}>
                            <Link
                              href={item.href}
                              className={styles.megaItemLink}
                              onClick={() => setMegaOpen(false)}
                            >
                              <span>{item.label}</span>
                              {item.note && (
                                <span className={styles.megaItemNote}>{item.note}</span>
                              )}
                            </Link>
                          </li>
                        ))}
                      </ul>
                    </div>
                  ))}
                </div>

                <div className={styles.megaCollectionsBar}>
                  <span className={styles.megaCollectionsTitle}>Bộ sưu tập nổi bật</span>
                  <div className={styles.megaCollectionsList}>
                    {FEATURED_COLLECTIONS_LINKS.map((item) => (
                      <Link
                        key={item.href}
                        href={item.href}
                        className={styles.megaCollectionChip}
                        onClick={() => setMegaOpen(false)}
                      >
                        {item.label}
                      </Link>
                    ))}
                  </div>
                </div>
              </div>

              <Link
                href={activeEditorial.href}
                className={styles.megaFeatureCard}
                onClick={() => setMegaOpen(false)}
              >
                <div className={styles.megaFeatureMedia}>
                  <Image
                    src={activeEditorial.image}
                    alt={activeEditorial.title}
                    fill
                    sizes="260px"
                    className={styles.megaFeatureImg}
                  />
                </div>
                <div className={styles.megaFeatureBody}>
                  <p className={styles.megaFeatureTitle}>{activeEditorial.title}</p>
                  <p className={styles.megaFeatureCaption}>{activeEditorial.caption}</p>
                  <span className={styles.megaFeatureCta}>{activeEditorial.ctaLabel}</span>
                </div>
              </Link>
            </div>
          </div>
        )}
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
            className={styles.drawer}
            role="dialog"
            aria-modal="true"
            aria-label="Điều hướng NexusMart"
          >
            <div className={styles.drawerTop}>
              <Link href="/" className={styles.brand} onClick={() => setDrawerOpen(false)}>
                <NexusMark size={20} />
                <span className={styles.brandWord}>NexusMart</span>
              </Link>
              <button
                type="button"
                className={styles.drawerCloseBtn}
                onClick={() => setDrawerOpen(false)}
                aria-label="Đóng trình đơn"
              >
                <IconClose size={20} />
              </button>
            </div>

            <form role="search" className={styles.drawerSearchForm} onSubmit={handleSearchSubmit}>
              <IconSearch size={16} className={styles.searchIcon} />
              <input
                ref={mobileSearchRef}
                type="search"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                placeholder="Tìm áo thun, polo, quần short..."
                aria-label="Tìm kiếm sản phẩm"
                className={styles.drawerSearchInput}
              />
            </form>

            {isAuthenticated && searchHistory.length > 0 && (
              <div className={styles.drawerSection}>
                <div className={styles.historyHeader}>
                  <p className={styles.dropdownHeading}>Tìm kiếm gần đây</p>
                  <button
                    type="button"
                    className={styles.dropdownCloseBtn}
                    disabled={clearingHistory}
                    onClick={() => void handleClearHistory()}
                  >
                    {clearingHistory ? "Đang xóa..." : "Xóa tất cả"}
                  </button>
                </div>
                <div className={styles.drawerKeywords}>
                  {searchHistory.slice(0, 10).map((item) => (
                    <span key={item.id} className={styles.historyChipWrap}>
                      <button
                        type="button"
                        className={styles.keywordChip}
                        onClick={() => handleSelectKeyword(item.query)}
                      >
                        {item.query}
                      </button>
                      <button
                        type="button"
                        className={styles.searchClearBtn}
                        disabled={historyBusyId === item.id}
                        onClick={() => void handleDeleteHistoryItem(item.id)}
                        aria-label={`Xóa lịch sử tìm kiếm ${item.query}`}
                      >
                        <IconClose size={12} />
                      </button>
                    </span>
                  ))}
                </div>
              </div>
            )}

            {(suggestKeywords.length > 0 || serverTrending.length > 0) && (
              <div className={styles.drawerKeywords}>
                {(searchQuery.trim() && suggestKeywords.length > 0
                  ? suggestKeywords
                  : serverTrending
                )
                  .slice(0, 6)
                  .map((kw) => (
                    <button
                      key={kw}
                      type="button"
                      className={styles.keywordChip}
                      onClick={() => handleSelectKeyword(kw)}
                    >
                      {kw}
                    </button>
                  ))}
              </div>
            )}

            {searchQuery.trim() && searchError && (
              <div className={styles.drawerSection} role="alert">
                <p className={styles.emptySearchText}>{searchError}</p>
              </div>
            )}

            {searchQuery.trim() && searchResults.length > 0 && (
              <div className={styles.drawerSection}>
                <p className={styles.dropdownHeading}>Kết quả nhanh</p>
                <ul className={styles.resultList}>
                  {searchResults.slice(0, 3).map((hit) => {
                    const thumb = hit.thumbnail_url || "/products/ao-thun-cotton-trang.png";
                    const prodSlug = hit.slug || hit.id;
                    const pricing = computeSearchHitDisplayPricing(
                      hit,
                      searchLiveFlashBySkuId,
                      searchShopDiscountsBySkuId
                    );
                    const promoLabel =
                      pricing.promoKind === "flash"
                        ? "Flash sale"
                        : pricing.promoKind === "shop_discount"
                          ? "Giảm giá gian hàng"
                          : "";
                    const subBase =
                      hit.shop_name?.trim() ||
                      (hit.sku_count > 0 ? `${hit.sku_count} phân loại` : "NexusMart");
                    return (
                      <li key={hit.id}>
                        <Link
                          href={`/san-pham/${encodeURIComponent(prodSlug)}`}
                          className={styles.resultItem}
                          onClick={() => setDrawerOpen(false)}
                        >
                          <Image
                            src={thumb}
                            alt={hit.name}
                            width={44}
                            height={58}
                            className={styles.resultThumb}
                          />
                          <div className={styles.resultMeta}>
                            <span className={styles.resultName}>{hit.name}</span>
                            <span className={styles.resultSub}>
                              {subBase}
                              {promoLabel ? ` · ${promoLabel}` : ""}
                            </span>
                          </div>
                          <div className={styles.resultPriceCol}>
                            <span className={`${styles.resultPrice} num`}>
                              {pricing.maxPriceVnd > pricing.minPriceVnd
                                ? `${formatVnd(pricing.minPriceVnd)} – ${formatVnd(pricing.maxPriceVnd)}`
                                : formatVnd(pricing.minPriceVnd)}
                            </span>
                            {pricing.originalPriceVnd &&
                              pricing.originalPriceVnd >
                                Math.max(pricing.minPriceVnd, pricing.maxPriceVnd) && (
                                <span className={`${styles.resultOldPrice} num`}>
                                  {formatVnd(pricing.originalPriceVnd)}
                                </span>
                              )}
                          </div>
                        </Link>
                      </li>
                    );
                  })}
                </ul>
              </div>
            )}

            <div className={styles.drawerQuickGrid}>
              <Link
                href="/"
                className={styles.drawerQuickTile}
                onClick={() => setDrawerOpen(false)}
              >
                Trang chủ
              </Link>
              <Link
                href="/flash-sale"
                className={styles.drawerQuickTile}
                onClick={() => setDrawerOpen(false)}
              >
                Flash sale
              </Link>
              <Link
                href="/tai-khoan?tab=yeu-thich"
                className={styles.drawerQuickTile}
                onClick={() => setDrawerOpen(false)}
              >
                Yêu thích ({wishlistCount})
              </Link>
              <Link
                href={isAuthenticated ? "/tai-khoan" : "/dang-nhap"}
                className={styles.drawerQuickTile}
                onClick={() => setDrawerOpen(false)}
              >
                {isAuthenticated ? displayName : "Đăng nhập"}
              </Link>
            </div>

            <nav className={styles.drawerSection} aria-label="Nhóm danh mục">
              {MOBILE_CATEGORY_SECTIONS.map((group) => {
                const expanded = openMobileGroup === group.id;
                return (
                  <div key={group.id} className={styles.drawerAccordion}>
                    <button
                      type="button"
                      className={styles.drawerAccordionTrigger}
                      aria-expanded={expanded}
                      onClick={() => setOpenMobileGroup(expanded ? "" : group.id)}
                    >
                      <span>{group.title}</span>
                      <IconChevronDown
                        size={15}
                        className={expanded ? styles.chevronOpen : styles.chevron}
                      />
                    </button>
                    {expanded && (
                      <ul className={styles.drawerAccordionList}>
                        {group.links.map((item) => (
                          <li key={item.href}>
                            <Link
                              href={item.href}
                              className={styles.drawerAccordionLink}
                              onClick={() => setDrawerOpen(false)}
                            >
                              {item.label}
                            </Link>
                          </li>
                        ))}
                      </ul>
                    )}
                  </div>
                );
              })}
            </nav>

            <nav className={styles.drawerSection} aria-label="Mua sắm và hỗ trợ">
              <Link href="/danh-muc" className={styles.drawerLink}>
                <span>Tất cả sản phẩm</span>
              </Link>
              <Link href="/gio-hang" className={styles.drawerLink}>
                <span>Giỏ hàng</span>
                <span className={`${styles.cartBadge} num`}>{cartCount}</span>
              </Link>
              <Link href="/don-hang" className={styles.drawerLink}>
                <span>Đơn hàng của tôi</span>
              </Link>
              <Link href="/tra-cuu-don-hang" className={styles.drawerLink}>
                <span>Tra cứu đơn hàng</span>
              </Link>
              <Link href="/tap-chi" className={styles.drawerLink}>
                <span>Sổ tay vải & Phối đồ</span>
              </Link>
              <Link href="/tro-giup" className={styles.drawerLink}>
                <span>Trợ giúp & Đổi trả 7 ngày</span>
              </Link>
              <Link href="/ve-nexusmart" className={styles.drawerLink}>
                <span>Về NexusMart & Xưởng dệt</span>
              </Link>
            </nav>

            <div className={styles.drawerSection}>
              <button
                type="button"
                className={styles.drawerAction}
                onClick={() => {
                  setDrawerOpen(false);
                  openCookieModal();
                }}
              >
                <span>Tùy chọn cookie</span>
              </button>
            </div>

            <div className={styles.drawerFooter}>
              {isAuthenticated && user ? (
                <>
                  <Link href="/tai-khoan" className={styles.drawerSubLink}>
                    {displayName} ({user.email})
                  </Link>
                  <button
                    type="button"
                    className={styles.drawerSubLink}
                    onClick={() => void handleLogout()}
                  >
                    Đăng xuất
                  </button>
                </>
              ) : (
                <Link href="/dang-nhap" className={styles.drawerSubLink}>
                  Đăng nhập / Đăng ký
                </Link>
              )}
              {isSeller && (
                <Link href="/seller" prefetch={false} className={styles.drawerSubLink}>
                  Kênh người bán
                </Link>
              )}
              {isAdmin && (
                <Link href="/admin" prefetch={false} className={styles.drawerSubLink}>
                  Cổng Quản trị
                </Link>
              )}
            </div>
          </div>
        </div>
      )}
    </>
  );
}
