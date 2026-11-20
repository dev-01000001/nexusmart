"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import {
  ANNOUNCEMENT_BAR,
  CATEGORIES,
  MEGA_MENU_GROUPS,
  TRENDING_KEYWORDS,
  getShopById,
} from "@/data";
import { getInstantSearchSuggestions } from "@/lib/search";
import { formatVnd } from "@/lib/format";
import { useStore } from "@/context/StoreContext";
import {
  IconBag,
  IconChat,
  IconChevronDown,
  IconClose,
  IconHeart,
  IconMenu,
  IconSearch,
  IconSpark,
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
  const { cartCount, wishlist, openChat, openAi, openCookieModal, chatThreads } = useStore();

  const [drawerOpen, setDrawerOpen] = useState(false);
  const [openMobileGroup, setOpenMobileGroup] = useState<string>("mob-ao");
  const [megaOpen, setMegaOpen] = useState(false);
  const [activeMegaGroupId, setActiveMegaGroupId] = useState<string>(MEGA_MENU_GROUPS[0].id);
  const [announcementIdx, setAnnouncementIdx] = useState(0);
  const [announcementVisible, setAnnouncementVisible] = useState(true);

  const [searchQuery, setSearchQuery] = useState("");
  const [searchOpen, setSearchOpen] = useState(false);
  const [mobileSearchFocus, setMobileSearchFocus] = useState(false);

  const headerRef = useRef<HTMLElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const mobileSearchRef = useRef<HTMLInputElement>(null);
  const megaCloseTimerRef = useRef<number | null>(null);

  const isHome = pathname === "/";
  const wishlistCount = wishlist.length;
  const buyerUnread = chatThreads.reduce((s, t) => s + t.unreadByBuyer, 0);
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

  const instantResult = useMemo(() => getInstantSearchSuggestions(searchQuery, 5), [searchQuery]);

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
        setDrawerOpen(false);
      }
    };
    const onPointerDown = (e: MouseEvent) => {
      if (headerRef.current && !headerRef.current.contains(e.target as Node)) {
        clearMegaCloseTimer();
        setMegaOpen(false);
        setSearchOpen(false);
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
                Freeship từ 249.000₫ · Đổi trả 45 ngày
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
                <Link
                  href="/hoi-vien"
                  className={`${styles.navLink} ${isActive("/hoi-vien") ? styles.navLinkActive : ""}`}
                  onMouseEnter={scheduleMegaClose}
                >
                  Hội viên
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
                  <div className={styles.searchSection}>
                    <p className={styles.dropdownHeading}>Tìm kiếm phổ biến</p>
                    <div className={styles.keywordList}>
                      {instantResult.keywords.slice(0, 6).map((kw) => (
                        <button
                          key={kw.query}
                          type="button"
                          className={styles.keywordChip}
                          onClick={() => handleSelectKeyword(kw.query)}
                        >
                          {kw.label}
                        </button>
                      ))}
                    </div>
                  </div>

                  <div className={styles.searchSection}>
                    <p className={styles.dropdownHeading}>
                      {searchQuery.trim()
                        ? `Sản phẩm khớp với "${searchQuery.trim()}"`
                        : "Sản phẩm xem nhiều"}
                    </p>

                    {instantResult.products.length === 0 ? (
                      <div className={styles.emptySearchBox}>
                        <p className={styles.emptySearchText}>
                          Chưa thấy sản phẩm khớp trực tiếp với từ khóa này.
                        </p>
                        {instantResult.spellingSuggestion && (
                          <button
                            type="button"
                            className={styles.spellingBtn}
                            onClick={() => handleSelectKeyword(instantResult.spellingSuggestion!)}
                          >
                            Thử tìm: {instantResult.spellingSuggestion}
                          </button>
                        )}
                      </div>
                    ) : (
                      <ul className={styles.resultList}>
                        {instantResult.products.map((prod) => {
                          const shop = getShopById(prod.shopId);
                          return (
                            <li key={prod.id}>
                              <Link
                                href={`/san-pham/${prod.slug}`}
                                className={styles.resultItem}
                                onClick={() => setSearchOpen(false)}
                              >
                                <Image
                                  src={prod.image}
                                  alt={prod.name}
                                  width={44}
                                  height={58}
                                  className={styles.resultThumb}
                                />
                                <div className={styles.resultMeta}>
                                  <span className={styles.resultName}>{prod.shortName}</span>
                                  <span className={styles.resultSub}>
                                    {shop?.shortName ?? "NexusMart"} · {prod.gsm}gsm
                                  </span>
                                </div>
                                <div className={styles.resultPriceCol}>
                                  <span className={`${styles.resultPrice} num`}>
                                    {formatVnd(prod.price)}
                                  </span>
                                  {prod.originalPrice && prod.originalPrice > prod.price && (
                                    <span className={`${styles.resultOldPrice} num`}>
                                      {formatVnd(prod.originalPrice)}
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
              {!isHome && (
                <button
                  type="button"
                  className={styles.actionItem}
                  onClick={() => openChat()}
                  aria-label="Nhắn tin với gian hàng"
                  title="Nhắn tin với gian hàng"
                >
                  <IconChat size={16} />
                  <span className={styles.actionLabelWide}>Chat</span>
                  {buyerUnread > 0 && <span className={styles.unreadDot} aria-hidden="true" />}
                </button>
              )}

              <button
                type="button"
                className={styles.actionItem}
                onClick={openAi}
                aria-label="Mở trợ lý chọn size"
                title="Trợ lý AI"
              >
                <IconSpark size={16} />
                <span className={styles.actionLabelWide}>Trợ lý AI</span>
              </button>

              <Link
                href="/tai-khoan"
                className={`${styles.actionItem} ${isActive("/tai-khoan") ? styles.actionItemActive : ""}`}
                aria-label="Tài khoản của tôi"
                title="Tài khoản"
              >
                <IconUser size={17} />
                <span className={styles.actionLabel}>Tài khoản</span>
              </Link>

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
              <Link
                href="/tai-khoan?tab=yeu-thich"
                className={styles.mobileCountLink}
                aria-label={`Yêu thích (${wishlistCount})`}
              >
                <IconHeart size={17} />
                <span className={`${styles.mobileBadge} num`}>{wishlistCount}</span>
              </Link>
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
                    href="/the-qua-tang"
                    className={styles.megaQuickLink}
                    onClick={() => setMegaOpen(false)}
                  >
                    Thẻ quà tặng & Ưu đãi sinh viên
                  </Link>
                  <Link
                    href="/tro-giup"
                    className={styles.megaQuickLink}
                    onClick={() => setMegaOpen(false)}
                  >
                    Trợ giúp & Đổi trả 45 ngày
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

            <div className={styles.drawerKeywords}>
              {TRENDING_KEYWORDS.slice(0, 5).map((kw) => (
                <button
                  key={kw.query}
                  type="button"
                  className={styles.keywordChip}
                  onClick={() => handleSelectKeyword(kw.query)}
                >
                  {kw.label}
                </button>
              ))}
            </div>

            {searchQuery.trim() && instantResult.products.length > 0 && (
              <div className={styles.drawerSection}>
                <p className={styles.dropdownHeading}>Kết quả nhanh</p>
                <ul className={styles.resultList}>
                  {instantResult.products.slice(0, 3).map((prod) => (
                    <li key={prod.id}>
                      <Link
                        href={`/san-pham/${prod.slug}`}
                        className={styles.resultItem}
                        onClick={() => setDrawerOpen(false)}
                      >
                        <Image
                          src={prod.image}
                          alt={prod.name}
                          width={44}
                          height={58}
                          className={styles.resultThumb}
                        />
                        <div className={styles.resultMeta}>
                          <span className={styles.resultName}>{prod.shortName}</span>
                          <span className={styles.resultSub}>{prod.gsm}gsm</span>
                        </div>
                        <span className={`${styles.resultPrice} num`}>{formatVnd(prod.price)}</span>
                      </Link>
                    </li>
                  ))}
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
                href="/tai-khoan"
                className={styles.drawerQuickTile}
                onClick={() => setDrawerOpen(false)}
              >
                Tài khoản
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
              <Link href="/hoi-vien" className={styles.drawerLink}>
                <span>Hội viên Nexus</span>
              </Link>
              <Link href="/the-qua-tang" className={styles.drawerLink}>
                <span>Thẻ quà tặng & Ưu đãi sinh viên</span>
              </Link>
              <Link href="/tap-chi" className={styles.drawerLink}>
                <span>Sổ tay vải & Phối đồ</span>
              </Link>
              <Link href="/tro-giup" className={styles.drawerLink}>
                <span>Trợ giúp & Đổi trả 45 ngày</span>
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
                  openChat();
                }}
              >
                <span>Nhắn tin gian hàng</span>
                {buyerUnread > 0 && <span className="num">{buyerUnread}</span>}
              </button>
              <button
                type="button"
                className={styles.drawerAction}
                onClick={() => {
                  setDrawerOpen(false);
                  openAi();
                }}
              >
                <span>Trợ lý AI chọn size</span>
              </button>
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
              <Link href="/dang-nhap" className={styles.drawerSubLink}>
                Đăng nhập / Đăng ký
              </Link>
              <Link href="/seller" className={styles.drawerSubLink}>
                Kênh người bán
              </Link>
              <Link href="/admin" className={styles.drawerSubLink}>
                Cổng Quản trị
              </Link>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
