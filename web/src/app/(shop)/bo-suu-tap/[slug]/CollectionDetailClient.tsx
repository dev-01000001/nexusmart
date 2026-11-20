"use client";

import React, { useMemo, useRef, useState } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { COLLECTIONS, getCollectionBySlug, type Product, type Shop, type SizeCode } from "@/data";
import { MODEL_SHOTS } from "@/data/media";
import { Button, ProductCard, StateView } from "@/components";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import styles from "./page.module.css";

export interface CollectionDetailClientProps {
  slug: string;
  products: Product[];
  shops: Shop[];
  loadError: string | null;
}

type SortKey = "bestseller" | "newest" | "price-asc" | "price-desc";
const QUICK_SIZES: SizeCode[] = ["S", "M", "L", "XL", "2XL"];

export function CollectionDetailClient({
  slug,
  products,
  shops,
  loadError,
}: CollectionDetailClientProps) {
  const searchParams = useSearchParams();
  const router = useRouter();
  const rootRef = useRef<HTMLDivElement>(null);

  const stateParam = searchParams.get("state");
  const collection = useMemo(() => getCollectionBySlug(slug), [slug]);

  const productById = useMemo(() => new Map(products.map((p) => [p.id, p])), [products]);
  const shopById = useMemo(() => new Map(shops.map((s) => [s.id, s])), [shops]);

  const [activeSubTabId, setActiveSubTabId] = useState<string>(collection?.subTabs[0]?.id ?? "all");
  const [selectedSize, setSelectedSize] = useState<SizeCode | "">("");
  const [sortBy, setSortBy] = useState<SortKey>("bestseller");

  const activeSubTab = useMemo(() => {
    if (!collection) return undefined;
    return collection.subTabs.find((t) => t.id === activeSubTabId) ?? collection.subTabs[0];
  }, [activeSubTabId, collection]);

  const displayedProducts = useMemo<Product[]>(() => {
    if (!collection) return [];
    const ids = activeSubTab?.productIds ?? collection.productIds;
    let list = ids.map((id) => productById.get(id)).filter((p): p is Product => Boolean(p));

    if (selectedSize) {
      list = list.filter((p) => p.skus.some((s) => s.size === selectedSize && s.stock > 0));
    }

    const sorted = [...list];
    if (sortBy === "price-asc") {
      sorted.sort((a, b) => a.price - b.price);
    } else if (sortBy === "price-desc") {
      sorted.sort((a, b) => b.price - a.price);
    } else if (sortBy === "newest") {
      sorted.sort((a, b) => {
        const aTime = a.createdAt ? Date.parse(a.createdAt) : 0;
        const bTime = b.createdAt ? Date.parse(b.createdAt) : 0;
        return bTime - aTime;
      });
    } else {
      sorted.sort((a, b) => (a.salesRank ?? 99) - (b.salesRank ?? 99));
    }
    return sorted;
  }, [activeSubTab, collection, productById, selectedSize, sortBy]);

  useRevealObserver(
    rootRef,
    `${slug}|${activeSubTabId}|${selectedSize}|${sortBy}|${stateParam ?? ""}`
  );

  if (stateParam === "loading") {
    return (
      <div className={styles.page}>
        <StateView kind="loading" title="Đang tải sản phẩm trong bộ sưu tập..." rows={6} />
      </div>
    );
  }

  if (stateParam === "error" || (loadError && products.length === 0)) {
    return (
      <div className={styles.page}>
        <StateView
          kind="error"
          title="Không thể tải bộ sưu tập lúc này"
          message={loadError ?? "Dữ liệu bộ sưu tập tạm thời gián đoạn. Vui lòng tải lại trang."}
          actionLabel="Tải lại bộ sưu tập"
          onAction={() => {
            if (stateParam === "error") {
              router.replace(`/bo-suu-tap/${slug}`);
            } else {
              router.refresh();
            }
          }}
        />
      </div>
    );
  }

  if (stateParam === "empty" || !collection) {
    return (
      <div className={styles.page}>
        <StateView
          kind="empty"
          title="Không tìm thấy bộ sưu tập này"
          message="Bộ sưu tập có thể đã kết thúc hoặc đổi đường dẫn. Bạn có thể duyệt 5 bộ sưu tập đang mở."
          actionLabel="Xem tất cả bộ sưu tập"
          actionHref="/bo-suu-tap"
        />
      </div>
    );
  }

  const collectionProducts = collection.productIds
    .map((id) => productById.get(id))
    .filter((p): p is Product => Boolean(p));

  const companionModels = collectionProducts
    .filter((p) => (p.onModelImage ?? p.image) !== collection.heroImage)
    .slice(0, 2);

  const otherCollections = COLLECTIONS.filter((c) => c.slug !== collection.slug);
  const gridClass =
    displayedProducts.length === 2
      ? `${styles.productGrid} ${styles.productGridTwo}`
      : styles.productGrid;

  return (
    <div ref={rootRef} className={styles.page}>
      <nav className={styles.breadcrumb} aria-label="Đường dẫn">
        <Link href="/" className={styles.crumbLink}>
          Trang chủ
        </Link>
        <span aria-hidden="true">/</span>
        <Link href="/bo-suu-tap" className={styles.crumbLink}>
          Bộ sưu tập
        </Link>
        <span aria-hidden="true">/</span>
        <span className={styles.crumbCurrent}>{collection.title}</span>
      </nav>

      <section className={styles.heroGrid} aria-label={`Giới thiệu bộ sưu tập ${collection.title}`}>
        <div className={styles.heroInfo}>
          <h1 className={`${styles.title} lines`}>
            <span className="line">
              <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
                {collection.title}
              </span>
            </span>
          </h1>
          <p className={styles.subtitle} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
            {collection.subtitle}
          </p>
          <p className={styles.fabricNote} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
            {collection.seasonLabel} · {collection.fabricHighlight}
          </p>

          <ul
            className={styles.benefitList}
            data-rise=""
            style={{ "--i": 2 } as React.CSSProperties}
          >
            {collection.benefitPoints.map((pt) => (
              <li key={pt} className={styles.benefitItem}>
                {pt}
              </li>
            ))}
          </ul>

          <div
            className={styles.heroActions}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <Button variant="primary" size="lg" href="#collection-grid-heading">
              Xem {collectionProducts.length || collection.productIds.length} sản phẩm
            </Button>
            {collection.slug === "soi-xanh-nexus" && (
              <Button variant="secondary" size="lg" href="/chien-dich/soi-xanh-nexus">
                Xem tiến độ quỹ Sợi Xanh
              </Button>
            )}
          </div>
        </div>

        <div
          className={styles.heroVisuals}
          data-rise=""
          style={{ "--i": 2 } as React.CSSProperties}
        >
          <div className={styles.heroBannerFrame}>
            <Image
              src={collection.heroImage}
              alt={collection.title}
              fill
              priority
              sizes="(max-width: 1080px) 100vw, 50vw"
              className={styles.heroBannerImg}
            />
          </div>
          {companionModels.length > 0 && (
            <div className={styles.heroModelRow}>
              {companionModels.map((prod) => {
                const src = prod.onModelImage ?? prod.image;
                const isModel = src.startsWith("/models/");
                return (
                  <Link
                    key={prod.id}
                    href={`/san-pham/${prod.slug}`}
                    className={styles.heroSideTile}
                  >
                    <Image
                      src={src}
                      alt={MODEL_SHOTS[prod.id]?.alt ?? prod.name}
                      fill
                      priority
                      sizes="(max-width: 1080px) 50vw, 24vw"
                      className={isModel ? styles.heroModelImg : styles.heroBannerImg}
                    />
                  </Link>
                );
              })}
            </div>
          )}
        </div>
      </section>

      <section className={styles.productsSection} aria-labelledby="collection-grid-heading">
        <div
          className={styles.sectionHeader}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          <h2 id="collection-grid-heading" className={styles.sectionTitle}>
            Lọc nhanh chất liệu &amp; chọn mua
          </h2>
          <span className={styles.countMeta}>{displayedProducts.length} sản phẩm hiển thị</span>
        </div>

        <div
          className={styles.subTabsBar}
          role="tablist"
          aria-label="Tab danh mục con và chất liệu"
        >
          {collection.subTabs.map((tab) => {
            const active = activeSubTab?.id === tab.id;
            const liveCount = tab.productIds.filter((id) => productById.has(id)).length;
            return (
              <button
                key={tab.id}
                type="button"
                role="tab"
                aria-selected={active}
                className={`${styles.tabBtn} ${active ? styles.tabBtnActive : ""}`}
                onClick={() => setActiveSubTabId(tab.id)}
              >
                {tab.label} ({liveCount})
              </button>
            );
          })}
        </div>

        {activeSubTab && <p className={styles.subTabDesc}>{activeSubTab.description}</p>}

        <div className={styles.toolbar}>
          <div className={styles.sizeQuickRow} role="group" aria-label="Lọc nhanh kích cỡ">
            <span className={styles.toolbarLabel}>Size còn hàng:</span>
            <button
              type="button"
              className={`${styles.sizeBtn} ${selectedSize === "" ? styles.sizeBtnActive : ""}`}
              onClick={() => setSelectedSize("")}
              aria-pressed={selectedSize === ""}
            >
              Tất cả
            </button>
            {QUICK_SIZES.map((sz) => (
              <button
                key={sz}
                type="button"
                className={`${styles.sizeBtn} ${selectedSize === sz ? styles.sizeBtnActive : ""}`}
                onClick={() => setSelectedSize((cur) => (cur === sz ? "" : sz))}
                aria-pressed={selectedSize === sz}
              >
                {sz}
              </button>
            ))}
          </div>

          <div className={styles.sortWrap}>
            <label htmlFor="collection-sort" className={styles.toolbarLabel}>
              Sắp xếp
            </label>
            <select
              id="collection-sort"
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

        {displayedProducts.length === 0 ? (
          <StateView
            kind="empty"
            title={
              selectedSize
                ? `Chưa có mẫu còn hàng size ${selectedSize} trong nhóm này`
                : "Chưa có sản phẩm trong nhóm này"
            }
            actionLabel={selectedSize ? "Xem lại tất cả size" : "Xem toàn bộ danh mục"}
            onAction={selectedSize ? () => setSelectedSize("") : undefined}
            actionHref={selectedSize ? undefined : "/danh-muc"}
          />
        ) : (
          <div className={gridClass}>
            {displayedProducts.map((product, idx) => (
              <div
                key={product.id}
                data-cat-reveal=""
                style={{ "--stagger": idx % 4 } as React.CSSProperties}
              >
                <ProductCard
                  product={product}
                  shop={shopById.get(product.shopId)}
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
          </div>
        )}
      </section>

      <section
        className={styles.careSection}
        aria-labelledby="collection-care-heading"
        data-cat-reveal=""
        style={{ "--stagger": 0 } as React.CSSProperties}
      >
        <h2 id="collection-care-heading" className={styles.careHeading}>
          Lưu ý bảo quản cho bộ sưu tập
        </h2>
        <ul className={styles.careList}>
          {collection.careTips.map((tip) => (
            <li key={tip} className={styles.careItem}>
              {tip}
            </li>
          ))}
        </ul>
      </section>

      <section className={styles.otherSection} aria-labelledby="other-collections-heading">
        <h2
          id="other-collections-heading"
          className={styles.sectionTitle}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          Các bộ sưu tập khác
        </h2>
        <div className={styles.otherGrid}>
          {otherCollections.map((other, idx) => (
            <Link
              key={other.slug}
              href={`/bo-suu-tap/${other.slug}`}
              className={styles.otherItem}
              data-cat-reveal=""
              style={{ "--stagger": idx % 4 } as React.CSSProperties}
            >
              <div className={styles.otherMedia}>
                <Image
                  src={other.heroImage}
                  alt={other.title}
                  fill
                  sizes="(max-width: 1080px) 50vw, 24vw"
                  className={styles.heroBannerImg}
                />
              </div>
              <span className={styles.otherMeta}>
                {other.seasonLabel} · {other.productIds.length} SP
              </span>
              <span className={styles.otherTitle}>{other.title}</span>
            </Link>
          ))}
        </div>
      </section>
    </div>
  );
}
