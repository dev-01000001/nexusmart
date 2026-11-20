"use client";

import React, { Suspense, useEffect, useMemo, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import {
  CATEGORIES,
  SHOPS,
  VOUCHERS,
  getProductById,
  getProductsByShop,
  getShopBySlug,
  getShopVouchers,
  type CategorySlug,
} from "@/data";
import { FABRIC_MEDIA, MEDIA_BANNERS, WORKSHOP_MEDIA } from "@/data/media";
import { useStore } from "@/context/StoreContext";
import { Button, IconChat, IconSearch, IconShield, ProductCard, StateView } from "@/components";
import { formatVnd } from "@/lib/format";
import { searchProducts, suggestSpelling } from "@/lib/search";
import { SizeFinder } from "@/features/catalog/SizeFinder";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import styles from "./page.module.css";

type SortKey = "bestseller" | "newest" | "price-asc" | "price-desc";
type ShopTab = "products" | "collections" | "factory" | "sizechart";

const FIT_BIAS_DESC: Record<"slim" | "regular" | "relaxed", string> = {
  slim: "Form ôm gọn (Slim / Tapered)",
  regular: "Form tiêu chuẩn (Regular)",
  relaxed: "Form rộng thoáng (Relaxed / Vận động)",
};

const SHOP_BANNER_BY_SLUG: Record<string, { src: string; alt: string }> = {
  "xuong-det-bach-dang": { src: WORKSHOP_MEDIA.src, alt: WORKSHOP_MEDIA.alt },
  "kho-basic-sai-gon": { src: MEDIA_BANNERS.running.src, alt: MEDIA_BANNERS.running.alt },
  "soi-tu-nhien-da-nang": { src: MEDIA_BANNERS.greenFiber.src, alt: MEDIA_BANNERS.greenFiber.alt },
  "det-kim-ha-noi": { src: FABRIC_MEDIA.src, alt: FABRIC_MEDIA.alt },
};

const WORKSHOP_GALLERY = [
  {
    src: WORKSHOP_MEDIA.src,
    alt: WORKSHOP_MEDIA.alt,
    caption: "Kiểm tra cuộn vải cotton và độ đều sợi trực tiếp tại xưởng",
  },
  {
    src: FABRIC_MEDIA.src,
    alt: FABRIC_MEDIA.alt,
    caption: "Cận cảnh mật độ dệt kim tròn và bề mặt vải trước khâu cắt rập",
  },
];

function ShopDetailInner() {
  const params = useParams<{ slug: string }>();
  const searchParams = useSearchParams();
  const router = useRouter();
  const rootRef = useRef<HTMLDivElement>(null);
  const loomVideoRef = useRef<HTMLVideoElement>(null);

  const {
    openChat,
    setShopVoucher,
    setPlatformVoucher,
    setFreeshipVoucher,
    selectedShopVouchers,
    platformVoucherCode,
    freeshipVoucherCode,
    showToast,
  } = useStore();

  const slug = typeof params?.slug === "string" ? params.slug : "";
  const stateParam = searchParams.get("state");

  const shop = useMemo(() => getShopBySlug(slug), [slug]);
  const shopProducts = useMemo(() => (shop ? getProductsByShop(shop.id) : []), [shop]);

  const [activeTab, setActiveTab] = useState<ShopTab>("products");
  const [activeCat, setActiveCat] = useState<CategorySlug | "all">("all");
  const [sortBy, setSortBy] = useState<SortKey>("bestseller");
  const [shopQuery, setShopQuery] = useState("");
  const [copiedCode, setCopiedCode] = useState<string | null>(null);

  const shopCategories = useMemo(() => {
    const present = new Set(shopProducts.map((p) => p.categorySlug));
    return CATEGORIES.filter((c) => present.has(c.slug));
  }, [shopProducts]);

  const filteredProducts = useMemo(() => {
    const searched = shopQuery.trim() ? searchProducts(shopQuery, shopProducts) : shopProducts;
    const base =
      activeCat === "all" ? searched : searched.filter((p) => p.categorySlug === activeCat);
    const list = [...base];
    if (sortBy === "price-asc") {
      list.sort((a, b) => a.price - b.price);
    } else if (sortBy === "price-desc") {
      list.sort((a, b) => b.price - a.price);
    } else if (sortBy === "newest") {
      list.sort((a, b) => {
        const aTime = a.createdAt ? Date.parse(a.createdAt) : 0;
        const bTime = b.createdAt ? Date.parse(b.createdAt) : 0;
        return bTime - aTime || Number(Boolean(b.isNew)) - Number(Boolean(a.isNew));
      });
    } else {
      list.sort((a, b) => (a.salesRank ?? 99) - (b.salesRank ?? 99));
    }
    return list;
  }, [activeCat, shopProducts, shopQuery, sortBy]);

  const marketplaceMatchesCount = useMemo(() => {
    if (!shopQuery.trim() || filteredProducts.length > 0) return 0;
    return searchProducts(shopQuery).length;
  }, [filteredProducts.length, shopQuery]);

  const shopSpellingHint = useMemo(() => {
    if (!shopQuery.trim() || filteredProducts.length > 0) return null;
    return suggestSpelling(shopQuery);
  }, [filteredProducts.length, shopQuery]);

  useRevealObserver(
    rootRef,
    `${slug}|${activeTab}|${activeCat}|${sortBy}|${shopQuery}|${stateParam ?? ""}`
  );

  useEffect(() => {
    const vid = loomVideoRef.current;
    if (!vid || typeof window === "undefined") return;
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    const syncPlayback = () => {
      if (mq.matches) vid.pause();
      else vid.play().catch(() => {});
    };
    syncPlayback();
    mq.addEventListener?.("change", syncPlayback);
    return () => mq.removeEventListener?.("change", syncPlayback);
  }, [slug, activeTab, filteredProducts.length]);

  if (stateParam === "loading") {
    return (
      <div className={styles.page}>
        <StateView
          kind="loading"
          title="Đang tải hồ sơ gian hàng và danh sách sản phẩm..."
          rows={6}
        />
      </div>
    );
  }

  if (stateParam === "error") {
    return (
      <div className={styles.page}>
        <StateView
          kind="error"
          title="Không thể tải thông tin gian hàng"
          message="Dữ liệu gian hàng tạm thời không phản hồi. Vui lòng thử tải lại."
          actionLabel="Tải lại gian hàng"
          onAction={() => router.replace(`/gian-hang/${slug}`)}
        />
      </div>
    );
  }

  if (stateParam === "empty" || !shop) {
    return (
      <div className={styles.page}>
        <StateView
          kind="empty"
          title="Không tìm thấy gian hàng xưởng dệt này"
          message="Gian hàng có thể đã thay đổi đường dẫn. Bạn có thể xem toàn bộ sản phẩm từ bốn xưởng đang hoạt động."
          actionLabel="Xem toàn bộ danh mục"
          actionHref="/danh-muc"
        />
      </div>
    );
  }

  const shopSpecificVouchers = getShopVouchers(shop.id);
  const helperVouchers = VOUCHERS.filter((v) => v.code === "NEXUS30" || v.code === "FREESHIP25");
  const displayedVouchers = [...shopSpecificVouchers, ...helperVouchers];

  const otherShops = SHOPS.filter((s) => s.id !== shop.id);
  const heroBanner = SHOP_BANNER_BY_SLUG[shop.slug] ?? {
    src: WORKSHOP_MEDIA.src,
    alt: WORKSHOP_MEDIA.alt,
  };
  const showCraftFillTile = filteredProducts.length >= 5 && filteredProducts.length % 4 === 1;

  const handleApplyVoucher = (code: string, scope: "shop" | "platform" | "freeship") => {
    if (scope === "shop") setShopVoucher(shop.id, code);
    else if (scope === "platform") setPlatformVoucher(code);
    else setFreeshipVoucher(code);
    showToast(`Đã lưu mã ${code} vào giỏ hàng.`);
  };

  const handleCopy = (code: string) => {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      navigator.clipboard.writeText(code).catch(() => {});
    }
    setCopiedCode(code);
    showToast(`Đã sao chép mã ${code}.`);
  };

  const isVoucherApplied = (code: string, scope: "shop" | "platform" | "freeship") => {
    if (scope === "shop") return selectedShopVouchers[shop.id] === code;
    if (scope === "platform") return platformVoucherCode === code;
    return freeshipVoucherCode === code;
  };

  return (
    <div ref={rootRef} className={styles.page}>
      <nav className={styles.breadcrumb} aria-label="Đường dẫn">
        <Link href="/" className={styles.crumbLink}>
          Trang chủ
        </Link>
        <span aria-hidden="true">/</span>
        <Link href="/danh-muc" className={styles.crumbLink}>
          Danh mục
        </Link>
        <span aria-hidden="true">/</span>
        <span className={styles.crumbCurrent}>{shop.name}</span>
      </nav>

      {/* Flagship Workshop Header */}
      <section className={styles.heroGrid} aria-label="Thông tin gian hàng xưởng dệt">
        <div className={styles.heroLeft}>
          <h1 className={`${styles.shopTitle} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                {shop.name}
              </span>
            </span>
          </h1>
          <p className={styles.story} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
            {shop.story}
          </p>
          <p className={styles.specialty} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            Chuyên dòng: {shop.specialty}.
          </p>
          {shop.verifiedBadge && (
            <p
              className={styles.verifiedRow}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              <IconShield size={16} />
              <span>{shop.verifiedBadge}</span>
            </p>
          )}

          <div
            className={styles.heroActions}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <Button variant="primary" size="lg" onClick={() => openChat(shop.id)}>
              <IconChat size={16} />
              <span>Chat với người bán</span>
            </Button>
            <Button variant="secondary" size="lg" href={`/danh-muc?shop=${shop.id}`}>
              Xem toàn bộ sản phẩm xưởng
            </Button>
          </div>
        </div>

        <aside
          className={styles.heroVisualCol}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <div className={styles.heroBannerFrame}>
            <Image
              src={heroBanner.src}
              alt={heroBanner.alt}
              fill
              priority
              sizes="(max-width: 1080px) 100vw, 48vw"
              className={styles.heroBannerImg}
            />
          </div>

          <dl className={styles.factsBand}>
            <div className={styles.factItem}>
              <dt className={styles.factKey}>Tỉnh / Thành phố</dt>
              <dd className={styles.factVal}>{shop.city}</dd>
            </div>
            <div className={styles.factItem}>
              <dt className={styles.factKey}>Thành lập</dt>
              <dd className={styles.factVal}>Năm {shop.foundedYear}</dd>
            </div>
            <div className={styles.factItem}>
              <dt className={styles.factKey}>Freeship riêng xưởng</dt>
              <dd className={styles.factVal}>Từ {formatVnd(shop.freeShipThreshold ?? 199000)}</dd>
            </div>
            <div className={styles.factItem}>
              <dt className={styles.factKey}>Bàn giao &amp; phản hồi</dt>
              <dd className={styles.factVal}>
                {shop.fulfillmentRate} · {shop.responseTime}
              </dd>
            </div>
            <div className={styles.factItem}>
              <dt className={styles.factKey}>Danh mục hiện có</dt>
              <dd className={styles.factVal}>{shopProducts.length} mẫu · đủ cỡ S–2XL</dd>
            </div>
            <div className={styles.factItem}>
              <dt className={styles.factKey}>Đổi trả {shop.returnDays ?? 45} ngày</dt>
              <dd className={styles.factVal}>
                {shop.twoWayExchange ? "Đổi size 2 chiều tận nơi" : shop.returnPolicy}
              </dd>
            </div>
            {shop.fastDeliveryNote && (
              <div className={styles.factItemFull}>
                <dt className={styles.factKey}>Giao hàng từ kho xưởng</dt>
                <dd className={styles.factVal}>{shop.fastDeliveryNote}</dd>
              </div>
            )}
            <div className={styles.factItemFull}>
              <dt className={styles.factKey}>Địa chỉ kho xuất hàng</dt>
              <dd className={styles.factVal}>{shop.warehouseAddress}</dd>
            </div>
          </dl>
        </aside>
      </section>

      {/* Vouchers — unified accent-tint band (zero repeated grey boxes) */}
      <section className={styles.voucherBand} aria-labelledby="shop-vouchers-heading">
        <h2
          id="shop-vouchers-heading"
          className={styles.sectionHeading}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          Voucher áp dụng tại gian hàng
        </h2>

        <div className={styles.voucherList}>
          {displayedVouchers.map((v, idx) => {
            const applied = isVoucherApplied(v.code, v.scope);
            return (
              <div
                key={v.code}
                className={styles.voucherRow}
                data-cat-reveal=""
                style={{ "--stagger": idx + 1 } as React.CSSProperties}
              >
                <div className={styles.voucherInfo}>
                  <div className={styles.voucherLineTop}>
                    <span className={styles.voucherCode}>{v.code}</span>
                    <span className={styles.voucherTitle}>{v.title}</span>
                  </div>
                  <p className={styles.voucherDesc}>{v.description}</p>
                  <span className={styles.voucherMeta}>
                    Đơn từ {formatVnd(v.minOrderValue)} · còn {v.remainingUses}/{v.totalUses} lượt
                  </span>
                </div>

                <div className={styles.voucherActions}>
                  <Button
                    variant={applied ? "ink" : "primary"}
                    size="sm"
                    onClick={() => handleApplyVoucher(v.code, v.scope)}
                  >
                    {applied ? "Đã áp dụng" : "Lưu vào giỏ"}
                  </Button>
                  <Button variant="secondary" size="sm" onClick={() => handleCopy(v.code)}>
                    {copiedCode === v.code ? "Đã chép" : "Sao chép mã"}
                  </Button>
                </div>
              </div>
            );
          })}
        </div>
      </section>

      {/* Main shop sections tab bar */}
      <section className={styles.catalogSection} aria-labelledby="shop-workspace-heading">
        <div
          className={styles.workspaceHead}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          <h2 id="shop-workspace-heading" className={styles.sectionHeading}>
            Không gian gian hàng {shop.shortName}
          </h2>
          <div className={styles.mainTabs} role="tablist" aria-label="Chuyên mục gian hàng">
            {(
              [
                { id: "products", label: `Sản phẩm (${shopProducts.length})` },
                {
                  id: "collections",
                  label: `Bộ sưu tập của xưởng (${shop.collections?.length ?? 0})`,
                },
                { id: "factory", label: "Hồ sơ xưởng dệt" },
                { id: "sizechart", label: "Bảng size của xưởng" },
              ] as const
            ).map((tab) => {
              const selected = activeTab === tab.id;
              return (
                <button
                  key={tab.id}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  className={`${styles.mainTabBtn} ${selected ? styles.mainTabBtnActive : ""}`}
                  onClick={() => setActiveTab(tab.id)}
                >
                  {tab.label}
                </button>
              );
            })}
          </div>
        </div>

        {activeTab === "products" && (
          <div role="tabpanel" className={styles.tabPanel}>
            <div className={styles.catalogToolbar}>
              <div className={styles.tabs} role="group" aria-label="Lọc danh mục của gian hàng">
                <button
                  type="button"
                  className={`${styles.tabBtn} ${activeCat === "all" ? styles.tabBtnActive : ""}`}
                  onClick={() => setActiveCat("all")}
                  aria-pressed={activeCat === "all"}
                >
                  Tất cả ({shopProducts.length})
                </button>
                {shopCategories.map((cat) => {
                  const count = shopProducts.filter((p) => p.categorySlug === cat.slug).length;
                  const active = activeCat === cat.slug;
                  return (
                    <button
                      key={cat.slug}
                      type="button"
                      className={`${styles.tabBtn} ${active ? styles.tabBtnActive : ""}`}
                      onClick={() => setActiveCat(cat.slug)}
                      aria-pressed={active}
                    >
                      {cat.name} ({count})
                    </button>
                  );
                })}
              </div>

              <div className={styles.shopToolsRight}>
                <div className={styles.shopSearchWrap}>
                  <IconSearch size={15} className={styles.shopSearchIcon} />
                  <input
                    type="search"
                    value={shopQuery}
                    onChange={(e) => setShopQuery(e.target.value)}
                    placeholder={`Tìm trong ${shop.shortName}...`}
                    aria-label={`Tìm sản phẩm trong ${shop.name}`}
                    className={styles.shopSearchInput}
                  />
                </div>

                <div className={styles.sortWrap}>
                  <label htmlFor="shop-sort" className={styles.sortLabel}>
                    Sắp xếp
                  </label>
                  <select
                    id="shop-sort"
                    className={styles.sortSelect}
                    value={sortBy}
                    onChange={(e) => setSortBy(e.target.value as SortKey)}
                  >
                    <option value="bestseller">Bán chạy</option>
                    <option value="newest">Mới nhất</option>
                    <option value="price-asc">Giá tăng dần</option>
                    <option value="price-desc">Giá giảm dần</option>
                  </select>
                </div>
              </div>
            </div>

            {filteredProducts.length === 0 ? (
              <div className={styles.emptySearchPanel}>
                <h3 className={styles.emptyHeading}>Không tìm thấy mẫu khớp trong {shop.name}</h3>
                <p className={styles.emptyBody}>
                  {shopQuery.trim()
                    ? `Gian hàng hiện chưa có sản phẩm khớp với "${shopQuery.trim()}". Bạn có thể mở rộng tìm kiếm sang toàn bộ 4 xưởng trên sàn.`
                    : "Chưa có sản phẩm trong nhóm này."}
                </p>
                {shopSpellingHint && (
                  <button
                    type="button"
                    className={styles.spellingHintBtn}
                    onClick={() => setShopQuery(shopSpellingHint)}
                  >
                    Thử tìm trong xưởng: {shopSpellingHint}
                  </button>
                )}
                <div className={styles.emptyActions}>
                  {shopQuery.trim() && (
                    <Button
                      variant="primary"
                      size="md"
                      href={`/tim-kiem?q=${encodeURIComponent(shopQuery.trim())}`}
                    >
                      {marketplaceMatchesCount > 0
                        ? `Mở rộng ra toàn sàn (${marketplaceMatchesCount} kết quả)`
                        : "Mở rộng ra toàn sàn"}
                    </Button>
                  )}
                  <Button
                    variant="secondary"
                    size="md"
                    onClick={() => {
                      setShopQuery("");
                      setActiveCat("all");
                    }}
                  >
                    Xem toàn bộ sản phẩm xưởng
                  </Button>
                </div>
              </div>
            ) : (
              <div className={styles.productGrid}>
                {filteredProducts.map((product, idx) => (
                  <div
                    key={product.id}
                    data-cat-reveal=""
                    style={{ "--stagger": idx % 4 } as React.CSSProperties}
                  >
                    <ProductCard
                      product={product}
                      showSpec={true}
                      showSla={true}
                      showBadges={true}
                      showWishlist={true}
                      enableQuickAdd={true}
                      enableInteractiveSwatches={true}
                      priority={idx < 4}
                    />
                  </div>
                ))}

                {showCraftFillTile && (
                  <aside
                    className={styles.craftFillCard}
                    data-cat-reveal=""
                    style={{ "--stagger": 2 } as React.CSSProperties}
                  >
                    <div className={styles.craftVideoWrap}>
                      <video
                        ref={loomVideoRef}
                        src={WORKSHOP_MEDIA.loomVideo.src}
                        poster={WORKSHOP_MEDIA.loomVideo.poster}
                        width={WORKSHOP_MEDIA.loomVideo.width}
                        height={WORKSHOP_MEDIA.loomVideo.height}
                        autoPlay
                        muted
                        loop
                        playsInline
                        preload="metadata"
                        className={styles.craftVideo}
                        aria-label={`Quy trình dệt sợi tại ${shop.name}`}
                      />
                    </div>
                    <div className={styles.craftFillBody}>
                      <h3 className={styles.craftFillTitle}>
                        Chuẩn rập &amp; kiểm vải tại {shop.shortName}
                      </h3>
                      <p className={styles.craftFillDesc}>
                        {shop.factoryProfile?.fiberOrigin ?? shop.story}
                      </p>
                      <div className={styles.craftFillActions}>
                        <Button variant="primary" size="md" onClick={() => setActiveTab("factory")}>
                          Xem hồ sơ xưởng dệt
                        </Button>
                        <Button
                          variant="secondary"
                          size="md"
                          onClick={() => setActiveTab("sizechart")}
                        >
                          Xem bảng size riêng
                        </Button>
                      </div>
                    </div>
                  </aside>
                )}
              </div>
            )}
          </div>
        )}

        {activeTab === "collections" && (
          <div role="tabpanel" className={styles.tabPanel}>
            {shop.collections && shop.collections.length > 0 ? (
              <div className={styles.collectionsStack}>
                {shop.collections.map((col) => {
                  const items = col.productIds
                    .map((id) => getProductById(id))
                    .filter((p): p is NonNullable<typeof p> => Boolean(p));
                  return (
                    <div key={col.slug} className={styles.collectionBlock}>
                      <div className={styles.collectionHead}>
                        <h3 className={styles.collectionTitle}>{col.title}</h3>
                        <p className={styles.collectionSub}>{col.subtitle}</p>
                      </div>
                      <div className={styles.productGrid}>
                        {items.map((product, idx) => (
                          <div
                            key={product.id}
                            data-cat-reveal=""
                            style={{ "--stagger": idx % 4 } as React.CSSProperties}
                          >
                            <ProductCard
                              product={product}
                              showSpec={true}
                              showSla={true}
                              enableQuickAdd={true}
                              enableInteractiveSwatches={true}
                            />
                          </div>
                        ))}
                      </div>
                    </div>
                  );
                })}
              </div>
            ) : (
              <StateView
                kind="empty"
                title="Gian hàng đang cập nhật bộ sưu tập riêng"
                actionLabel="Xem toàn bộ sản phẩm"
                onAction={() => setActiveTab("products")}
              />
            )}
          </div>
        )}

        {activeTab === "factory" && shop.factoryProfile && (
          <div role="tabpanel" className={styles.tabPanel}>
            <div className={styles.factoryGrid}>
              <div className={styles.factoryInfoCol}>
                <h3 className={styles.subHeading}>{shop.factoryProfile.factoryName}</h3>
                <p className={styles.factoryText}>
                  Địa chỉ xưởng: {shop.factoryProfile.factoryAddress}.
                </p>
                <p className={styles.factoryText}>
                  Nguồn sợi chính: {shop.factoryProfile.fiberOrigin}.
                </p>

                <dl className={styles.factoryMetrics}>
                  <div className={styles.factItem}>
                    <dt className={styles.metricKey}>Đối tác NexusMart từ</dt>
                    <dd className={styles.metricVal}>Năm {shop.factoryProfile.partnerSince}</dd>
                  </div>
                  <div className={styles.factItem}>
                    <dt className={styles.metricKey}>Công suất sản xuất</dt>
                    <dd className={styles.metricVal}>{shop.factoryProfile.monthlyCapacity}</dd>
                  </div>
                  <div className={styles.factItem}>
                    <dt className={styles.metricKey}>Chính sách đổi trả</dt>
                    <dd className={styles.metricVal}>{shop.returnPolicy}</dd>
                  </div>
                  <div className={styles.factItem}>
                    <dt className={styles.metricKey}>Chuẩn chứng nhận</dt>
                    <dd className={styles.metricVal}>
                      {shop.factoryProfile.certifications.join(" · ")}
                    </dd>
                  </div>
                </dl>

                <div className={styles.qcBlock}>
                  <h4 className={styles.qcHeading}>Quy trình kiểm vải trước khi đóng gói</h4>
                  <ol className={styles.qcList}>
                    {shop.factoryProfile.qcProcess.map((step) => (
                      <li key={step} className={styles.qcItem}>
                        {step}
                      </li>
                    ))}
                  </ol>
                </div>
              </div>

              <div className={styles.factoryVisualCol}>
                {WORKSHOP_GALLERY.map((photo) => (
                  <figure key={photo.src} className={styles.factoryFigure}>
                    <div className={styles.factoryImgFrame}>
                      <Image
                        src={photo.src}
                        alt={photo.alt}
                        fill
                        sizes="(max-width: 1080px) 100vw, 42vw"
                        className={styles.factoryImg}
                      />
                    </div>
                    <figcaption className={styles.factoryCaption}>{photo.caption}</figcaption>
                  </figure>
                ))}
              </div>
            </div>
          </div>
        )}

        {activeTab === "sizechart" && shop.sizeCharts && (
          <div role="tabpanel" className={styles.tabPanel}>
            <div className={styles.sizeChartHeader}>
              <h3 className={styles.subHeading}>
                Bảng thông số rập riêng · {FIT_BIAS_DESC[shop.sizeCharts.fitBias]}
              </h3>
              <p className={styles.factoryText}>{shop.sizeCharts.sizeNote}</p>
            </div>

            <div className={styles.tablesGrid}>
              <div className={styles.tableBox}>
                <h4 className={styles.tableTitle}>Bảng size Áo (cm)</h4>
                <div className={styles.tableWrap}>
                  <table className={styles.sizeTable}>
                    <thead>
                      <tr>
                        <th>Cỡ</th>
                        <th>Chiều cao</th>
                        <th>Cân nặng</th>
                        <th>Vòng ngực</th>
                        <th>Dài áo</th>
                        <th>Rộng vai</th>
                      </tr>
                    </thead>
                    <tbody>
                      {shop.sizeCharts.top.map((row) => (
                        <tr key={row.size}>
                          <td>{row.size}</td>
                          <td>
                            {row.heightMin}-{row.heightMax} cm
                          </td>
                          <td>
                            {row.weightMin}-{row.weightMax} kg
                          </td>
                          <td>{row.chestOrWaistCm} cm</td>
                          <td>{row.lengthCm} cm</td>
                          <td>{row.shoulderCm ?? "Chuẩn"} cm</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>

              <div className={styles.tableBox}>
                <h4 className={styles.tableTitle}>Bảng size Quần &amp; Đồ lót (cm)</h4>
                <div className={styles.tableWrap}>
                  <table className={styles.sizeTable}>
                    <thead>
                      <tr>
                        <th>Cỡ</th>
                        <th>Chiều cao</th>
                        <th>Cân nặng</th>
                        <th>Vòng eo</th>
                        <th>Chiều dài</th>
                      </tr>
                    </thead>
                    <tbody>
                      {shop.sizeCharts.bottom.map((row) => (
                        <tr key={row.size}>
                          <td>{row.size}</td>
                          <td>
                            {row.heightMin}-{row.heightMax} cm
                          </td>
                          <td>
                            {row.weightMin}-{row.weightMax} kg
                          </td>
                          <td>{row.chestOrWaistCm} cm</td>
                          <td>{row.lengthCm} cm</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            </div>

            <div className={styles.embeddedSizeFinder}>
              <SizeFinder defaultShopId={shop.id} />
            </div>
          </div>
        )}
      </section>

      {/* Unboxed 3-column typographic directory — zero card-rows */}
      <section className={styles.otherShopsSection} aria-labelledby="other-shops-heading">
        <h2
          id="other-shops-heading"
          className={styles.sectionHeading}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          Các xưởng dệt khác trên NexusMart
        </h2>

        <div className={styles.otherShopsGrid}>
          {otherShops.map((other, idx) => (
            <Link
              key={other.id}
              href={`/gian-hang/${other.slug}`}
              className={styles.otherShopItem}
              data-cat-reveal=""
              style={{ "--stagger": idx + 1 } as React.CSSProperties}
            >
              <span className={styles.otherShopName}>{other.name}</span>
              <span className={styles.otherShopCity}>{other.city}</span>
              <span className={styles.otherShopSpec}>{other.specialty}</span>
              <span className={styles.otherShopLink}>Xem gian hàng →</span>
            </Link>
          ))}
        </div>
      </section>
    </div>
  );
}

export default function ShopPage() {
  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải gian hàng..." rows={6} />
        </div>
      }
    >
      <ShopDetailInner />
    </Suspense>
  );
}
