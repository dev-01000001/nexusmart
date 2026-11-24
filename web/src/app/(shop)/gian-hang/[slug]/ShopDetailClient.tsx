"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import type { GatewayVoucher } from "@/lib/gateway-types";
import { api, ApiError } from "@/lib/api";
import { formatPromotionErrorMessage } from "@/features/checkout/messages";
import type {
  CatalogCategory,
  CatalogProduct,
  CatalogShop,
  CatalogVoucher,
} from "@/lib/catalog-types";
import { useSession } from "@/context/SessionContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { FABRIC_MEDIA, WORKSHOP_MEDIA } from "@/mock/productEditorial";
import { Button, IconChat, IconSearch, IconShield, ProductCard, StateView } from "@/components";
import { formatVnd } from "@/lib/format";
import { searchProducts, suggestSpelling } from "@/lib/search";
import { SizeFinder } from "@/features/catalog/SizeFinder";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import styles from "./page.module.css";

export interface ShopDetailClientProps {
  shop: CatalogShop;
  otherShops: CatalogShop[];
  categories: CatalogCategory[];
  shopProducts: CatalogProduct[];
  allProducts: CatalogProduct[];
  shopVouchers: CatalogVoucher[];
  platformVouchers: CatalogVoucher[];
}

type SortKey = "bestseller" | "newest" | "price-asc" | "price-desc" | "name-asc";
type ShopTab = "products" | "collections" | "factory" | "sizechart";

const FIT_BIAS_DESC: Record<"slim" | "regular" | "relaxed", string> = {
  slim: "Form ôm gọn (Slim / Tapered)",
  regular: "Form tiêu chuẩn (Regular)",
  relaxed: "Form rộng thoáng (Relaxed / Vận động)",
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

function getCurrentReturnPath(fallbackPathname: string): string {
  if (typeof window !== "undefined") {
    return `${window.location.pathname}${window.location.search}`;
  }
  return fallbackPathname || "/";
}

export function ShopDetailClient({
  shop,
  otherShops,
  categories,
  shopProducts,
  allProducts,
  shopVouchers,
  platformVouchers,
}: ShopDetailClientProps) {
  const router = useRouter();
  const pathname = usePathname();
  const rootRef = useRef<HTMLDivElement>(null);
  const loomVideoRef = useRef<HTMLVideoElement>(null);

  const { isAuthenticated } = useSession();
  const { openChat, showToast } = useMockStore();

  const displayedVouchers = useMemo(
    () => [...shopVouchers, ...platformVouchers],
    [shopVouchers, platformVouchers]
  );

  const [activeTab, setActiveTab] = useState<ShopTab>("products");
  const [activeCat, setActiveCat] = useState<string>("all");
  const [sortBy, setSortBy] = useState<SortKey>("bestseller");
  const [shopQuery, setShopQuery] = useState("");
  const [copiedCode, setCopiedCode] = useState<string | null>(null);
  const [savingVoucherId, setSavingVoucherId] = useState<string | null>(null);
  const [savedVoucherIds, setSavedVoucherIds] = useState<Record<string, boolean>>(() => {
    const initial: Record<string, boolean> = {};
    for (const v of displayedVouchers) {
      if (v.saved) initial[v.id] = true;
    }
    return initial;
  });

  const shopCategories = useMemo(() => {
    const present = new Set(shopProducts.map((p) => p.rawCategorySlug));
    return categories.filter((c) => present.has(c.slug));
  }, [categories, shopProducts]);

  const filteredProducts = useMemo(() => {
    const searched = shopQuery.trim()
      ? (searchProducts(shopQuery, shopProducts) as CatalogProduct[])
      : shopProducts;
    const base =
      activeCat === "all" ? searched : searched.filter((p) => p.rawCategorySlug === activeCat);
    const list = [...base];
    if (sortBy === "price-asc") {
      list.sort((a, b) => a.price - b.price);
    } else if (sortBy === "price-desc") {
      list.sort((a, b) => b.price - a.price);
    } else if (sortBy === "name-asc") {
      list.sort((a, b) => a.name.localeCompare(b.name, "vi"));
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
    return searchProducts(shopQuery, allProducts).length;
  }, [allProducts, filteredProducts.length, shopQuery]);

  const shopSpellingHint = useMemo(() => {
    if (!shopQuery.trim() || filteredProducts.length > 0) return null;
    return suggestSpelling(shopQuery);
  }, [filteredProducts.length, shopQuery]);

  const allProductsBySlug = useMemo(
    () => new Map(allProducts.map((p) => [p.slug, p])),
    [allProducts]
  );

  useRevealObserver(rootRef, `${shop.slug}|${activeTab}|${activeCat}|${sortBy}|${shopQuery}`);

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
  }, [shop.slug, activeTab, filteredProducts.length]);

  const showCraftFillTile = filteredProducts.length >= 5 && filteredProducts.length % 4 === 1;
  const visibleOtherShops = otherShops.slice(0, 3);
  const chatShopId = shop.editorialShopId ?? shop.id;

  const handleSaveVoucher = async (voucher: CatalogVoucher) => {
    if (savingVoucherId === voucher.id || savedVoucherIds[voucher.id]) return;

    if (!isAuthenticated) {
      router.push(`/dang-nhap?next=${encodeURIComponent(getCurrentReturnPath(pathname))}`);
      return;
    }

    setSavingVoucherId(voucher.id);
    try {
      await api<{ saved: boolean; voucher: GatewayVoucher }>(
        `vouchers/${encodeURIComponent(voucher.id)}/save`,
        {
          method: "POST",
          json: { code: voucher.code },
        }
      );
      setSavedVoucherIds((prev) => ({ ...prev, [voucher.id]: true }));
      showToast(`Đã lưu mã ${voucher.code} vào ví voucher.`);
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        router.push(`/dang-nhap?next=${encodeURIComponent(getCurrentReturnPath(pathname))}`);
        return;
      }
      showToast(
        formatPromotionErrorMessage(err, "save_voucher", "Không thể lưu mã ưu đãi lúc này.")
      );
    } finally {
      setSavingVoucherId(null);
    }
  };

  const handleCopy = (code: string) => {
    if (typeof navigator !== "undefined" && navigator.clipboard) {
      navigator.clipboard.writeText(code).catch(() => {});
    }
    setCopiedCode(code);
    showToast(`Đã sao chép mã ${code}.`);
  };

  const editorialCollections = shop.editorialCollections ?? [];

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
          {shop.hasEditorialProfile && (
            <p
              className={styles.specialty}
              data-rise=""
              style={{ "--i": 2 } as React.CSSProperties}
            >
              Chuyên dòng: {shop.specialty}.
            </p>
          )}
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
            <Button variant="primary" size="lg" onClick={() => openChat(chatShopId)}>
              <IconChat size={16} />
              <span>Chat với người bán</span>
            </Button>
            <Button
              variant="secondary"
              size="lg"
              href={`/danh-muc?shop=${encodeURIComponent(shop.slug)}`}
            >
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
              src={shop.heroBanner.src}
              alt={shop.heroBanner.alt}
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
              <dt className={styles.factKey}>
                {shop.hasEditorialProfile ? "Thành lập" : "Trạng thái"}
              </dt>
              <dd className={styles.factVal}>
                {shop.hasEditorialProfile ? `Năm ${shop.foundedYear}` : "Đang hoạt động"}
              </dd>
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
              <dd className={styles.factVal}>{shopProducts.length} mẫu đang mở bán</dd>
            </div>
            <div className={styles.factItem}>
              <dt className={styles.factKey}>Đổi trả {shop.returnDays ?? 7} ngày</dt>
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

      {/* Vouchers — real backend shop & platform vouchers with POST /vouchers/{id}/save */}
      {displayedVouchers.length > 0 && (
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
              const saved = Boolean(savedVoucherIds[v.id]);
              const saving = savingVoucherId === v.id;
              const outOfQuota = v.remainingQuota <= 0;
              return (
                <div
                  key={v.id}
                  className={styles.voucherRow}
                  data-cat-reveal=""
                  style={{ "--stagger": idx + 1 } as React.CSSProperties}
                >
                  <div className={styles.voucherInfo}>
                    <div className={styles.voucherLineTop}>
                      <span className={styles.voucherCode}>{v.code}</span>
                      <span className={styles.voucherTitle}>{v.name}</span>
                    </div>
                    <p className={styles.voucherDesc}>{v.description}</p>
                    <span className={styles.voucherMeta}>
                      Đơn từ {formatVnd(v.minOrderVnd)} · còn {v.remainingQuota}/{v.quota} lượt
                    </span>
                  </div>

                  <div className={styles.voucherActions}>
                    <Button
                      variant={saved ? "ink" : "primary"}
                      size="sm"
                      disabled={outOfQuota || saving || saved}
                      onClick={() => void handleSaveVoucher(v)}
                    >
                      {outOfQuota
                        ? "Hết lượt"
                        : saving
                          ? "Đang lưu..."
                          : saved
                            ? "Đã lưu"
                            : "Lưu mã"}
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
      )}

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
                { id: "products", label: `Sản phẩm (${shopProducts.length})`, visible: true },
                {
                  id: "collections",
                  label: `Bộ sưu tập của xưởng (${editorialCollections.length})`,
                  visible: editorialCollections.length > 0,
                },
                { id: "factory", label: "Hồ sơ xưởng dệt", visible: Boolean(shop.factoryProfile) },
                {
                  id: "sizechart",
                  label: "Bảng size của xưởng",
                  visible: Boolean(shop.sizeCharts),
                },
              ] as const
            )
              .filter((tab) => tab.visible)
              .map((tab) => {
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
                  const count = shopProducts.filter((p) => p.rawCategorySlug === cat.slug).length;
                  const active = activeCat === cat.slug;
                  return (
                    <button
                      key={cat.id}
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
                    <option value="name-asc">Tên A–Z</option>
                  </select>
                </div>
              </div>
            </div>

            {filteredProducts.length === 0 ? (
              <div className={styles.emptySearchPanel}>
                <h3 className={styles.emptyHeading}>Không tìm thấy mẫu khớp trong {shop.name}</h3>
                <p className={styles.emptyBody}>
                  {shopQuery.trim()
                    ? `Gian hàng hiện chưa có sản phẩm khớp với "${shopQuery.trim()}". Bạn có thể mở rộng tìm kiếm sang toàn bộ các xưởng trên sàn.`
                    : "Gian hàng hiện chưa có sản phẩm trong nhóm này."}
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

                {showCraftFillTile && shop.hasEditorialProfile && (
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
                        {shop.factoryProfile && (
                          <Button
                            variant="primary"
                            size="md"
                            onClick={() => setActiveTab("factory")}
                          >
                            Xem hồ sơ xưởng dệt
                          </Button>
                        )}
                        {shop.sizeCharts && (
                          <Button
                            variant="secondary"
                            size="md"
                            onClick={() => setActiveTab("sizechart")}
                          >
                            Xem bảng size riêng
                          </Button>
                        )}
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
            {editorialCollections.length > 0 ? (
              <div className={styles.collectionsStack}>
                {editorialCollections.map((col) => {
                  const items = col.productSlugs
                    .map((pSlug) => allProductsBySlug.get(pSlug))
                    .filter((p): p is CatalogProduct => Boolean(p));
                  if (items.length === 0) return null;
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
              <SizeFinder
                defaultShopId={shop.id}
                products={allProducts}
                shops={[shop, ...otherShops]}
              />
            </div>
          </div>
        )}
      </section>

      {/* Unboxed 3-column typographic directory — zero card-rows */}
      {visibleOtherShops.length > 0 && (
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
            {visibleOtherShops.map((other, idx) => (
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
      )}
    </div>
  );
}
