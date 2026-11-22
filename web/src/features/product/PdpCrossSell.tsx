"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import type { SizeCode } from "@/data";
import {
  findProductSku,
  isCatalogSku,
  type CatalogProduct,
  type CatalogShop,
} from "@/lib/catalog-types";
import { useCart } from "@/context/CartContext";
import { useSession } from "@/context/SessionContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { getEditorialProductSlugById } from "@/mock/productEditorial";
import { Button, Price, ProductCard } from "@/components";
import { formatVnd } from "@/lib/format";
import styles from "./PdpCrossSell.module.css";

export interface PdpBundleSectionProps {
  product: CatalogProduct;
  shop: CatalogShop;
  bundleProducts: CatalogProduct[];
  selectedColorCode: string;
  selectedSize: SizeCode;
}

function getDefaultInStockSize(p: CatalogProduct): SizeCode {
  const firstColor = p.colors[0]?.code ?? "";
  for (const preferred of ["L", "M", ...p.sizes] as SizeCode[]) {
    if (!p.sizes.includes(preferred)) continue;
    const sku = findProductSku(p, firstColor, preferred);
    if (sku && sku.stock > 0) return preferred;
  }
  return p.sizes[0] ?? "M";
}

function getCurrentReturnPath(fallbackPathname: string): string {
  if (typeof window !== "undefined") {
    return `${window.location.pathname}${window.location.search}`;
  }
  return fallbackPathname || "/";
}

export function PdpBundleSection({
  product,
  shop,
  bundleProducts,
  selectedColorCode,
  selectedSize,
}: PdpBundleSectionProps) {
  const router = useRouter();
  const pathname = usePathname();
  const { isAuthenticated } = useSession();
  const { addToCart } = useCart();
  const { showToast } = useMockStore();

  const [checkedIds, setCheckedIds] = useState<Record<string, boolean>>(() => {
    const init: Record<string, boolean> = {};
    for (const item of bundleProducts) {
      init[item.id] = true;
    }
    return init;
  });

  const [pickedSizes, setPickedSizes] = useState<Record<string, SizeCode>>({});
  const [submitting, setSubmitting] = useState(false);

  if (bundleProducts.length === 0) return null;

  const isChecked = (id: string) => (id in checkedIds ? checkedIds[id] : true);
  const getSizeFor = (p: CatalogProduct): SizeCode => pickedSizes[p.id] ?? getDefaultInStockSize(p);

  const selectedBundleItems = bundleProducts.filter((p) => isChecked(p.id));
  const currentSku = findProductSku(product, selectedColorCode, selectedSize);
  const currentPrice = currentSku?.price ?? product.price;
  const totalSelectedCount = 1 + selectedBundleItems.length;
  const combinedTotal =
    currentPrice +
    selectedBundleItems.reduce((sum, item) => {
      const sz = getSizeFor(item);
      const colorCode = item.colors[0]?.code ?? "";
      const sku = findProductSku(item, colorCode, sz);
      return sum + (sku?.price ?? item.price);
    }, 0);
  const sameShopCount = selectedBundleItems.filter((item) => item.shopId === product.shopId).length;

  const handleToggleItem = (id: string) => {
    setCheckedIds((prev) => ({
      ...prev,
      [id]: !(id in prev ? prev[id] : true),
    }));
  };

  const handleAddBundle = async () => {
    if (submitting || !currentSku || currentSku.stock <= 0 || !isCatalogSku(currentSku)) return;

    if (!isAuthenticated) {
      router.push(`/dang-nhap?next=${encodeURIComponent(getCurrentReturnPath(pathname))}`);
      return;
    }

    setSubmitting(true);
    try {
      let addedCount = 0;
      const mainRes = await addToCart({ skuId: currentSku.id, quantity: 1 });
      if (mainRes.ok) {
        addedCount += 1;
      }

      for (const item of selectedBundleItems) {
        const sz = getSizeFor(item);
        const colorCode = item.colors[0]?.code ?? "";
        const itemSku = findProductSku(item, colorCode, sz);
        if (!itemSku || itemSku.stock <= 0 || !isCatalogSku(itemSku)) continue;
        const res = await addToCart({ skuId: itemSku.id, quantity: 1 });
        if (res.ok) addedCount += 1;
      }

      if (addedCount > 0) {
        showToast(`Đã thêm bộ ${addedCount} sản phẩm vào giỏ hàng.`);
      } else {
        showToast("Không thể thêm bộ sản phẩm vào giỏ hàng lúc này.");
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <section className={styles.bundleSection} aria-labelledby="pdp-bundle-heading">
      <div className={styles.sectionHead}>
        <h2
          id="pdp-bundle-heading"
          className={styles.sectionHeading}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          Gợi ý mua kèm và phối bộ
        </h2>
        <span className={styles.headNote}>
          {sameShopCount > 0
            ? `Gộp ship cùng ${shop.shortName}, đạt mốc ${formatVnd(shop.freeShipThreshold ?? 199000)} miễn phí vận chuyển xưởng`
            : "Tích chọn món phối bộ để thêm nhanh vào giỏ hàng"}
        </span>
      </div>

      <div
        className={styles.bundleGrid}
        data-cat-reveal=""
        style={{ "--stagger": 1 } as React.CSSProperties}
      >
        <div className={styles.bundleItems}>
          <div className={styles.bundleRowCurrent}>
            <img
              src={product.image}
              alt={product.shortName}
              width={84}
              height={112}
              loading="lazy"
              className={styles.bundleThumb}
            />
            <div className={styles.bundleInfo}>
              <span className={styles.bundleTag}>Đang xem · {shop.shortName}</span>
              <span className={styles.bundleTitle}>{product.name}</span>
              <span className={styles.bundleMeta}>Size {selectedSize}</span>
              <Price amount={currentPrice} originalAmount={product.originalPrice} size="sm" />
            </div>
          </div>

          {bundleProducts.map((item) => {
            const checked = isChecked(item.id);
            const chosenSize = getSizeFor(item);
            const sameShop = item.shopId === product.shopId;
            return (
              <div
                key={item.id}
                className={`${styles.bundleRow} ${!checked ? styles.bundleRowUnchecked : ""}`}
              >
                <label className={styles.bundleCheckWrap}>
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={() => handleToggleItem(item.id)}
                    className={styles.bundleCheckbox}
                    aria-label={`Chọn mua kèm ${item.shortName}`}
                  />
                </label>

                <Link href={`/san-pham/${item.slug}`} className={styles.bundleThumbLink}>
                  <img
                    src={item.image}
                    alt={item.shortName}
                    width={84}
                    height={112}
                    loading="lazy"
                    className={styles.bundleThumb}
                  />
                </Link>

                <div className={styles.bundleInfo}>
                  <span className={styles.bundleTag}>
                    {sameShop ? `Cùng xưởng ${shop.shortName} · Gộp ship` : item.shopShortName}
                  </span>
                  <Link href={`/san-pham/${item.slug}`} className={styles.bundleTitleLink}>
                    {item.name}
                  </Link>
                  <div className={styles.bundleControls}>
                    <label className={styles.bundleSizeLabel}>
                      <span className="sr-only">Chọn size cho {item.shortName}</span>
                      <select
                        value={chosenSize}
                        onChange={(e) =>
                          setPickedSizes((prev) => ({
                            ...prev,
                            [item.id]: e.target.value as SizeCode,
                          }))
                        }
                        className={styles.bundleSizeSelect}
                      >
                        {item.sizes.map((sz) => (
                          <option key={sz} value={sz}>
                            Size {sz}
                          </option>
                        ))}
                      </select>
                    </label>
                    <Price amount={item.price} originalAmount={item.originalPrice} size="sm" />
                  </div>
                </div>
              </div>
            );
          })}
        </div>

        <aside className={styles.bundleSummary}>
          <div className={styles.summaryTop}>
            <p className={styles.summaryTitle}>Tạm tính bộ {totalSelectedCount} sản phẩm</p>
            <p className={styles.summaryPrice}>{formatVnd(combinedTotal)}</p>
          </div>
          <p className={styles.summaryDesc}>
            {combinedTotal >= 249000
              ? "Bộ phối đã đạt ngưỡng miễn phí vận chuyển toàn sàn 249.000₫."
              : `Thêm ${formatVnd(249000 - combinedTotal)} nữa để đạt miễn phí vận chuyển toàn sàn 249.000₫.`}
          </p>
          <Button
            variant="primary"
            size="lg"
            fullWidth
            disabled={submitting || !currentSku || currentSku.stock <= 0}
            onClick={() => void handleAddBundle()}
          >
            {submitting ? "Đang thêm..." : `Thêm ${totalSelectedCount} món vào giỏ`}
          </Button>
        </aside>
      </div>
    </section>
  );
}

export interface PdpRecommendationsSectionProps {
  product: CatalogProduct;
  shop: CatalogShop;
  sameShopProducts: CatalogProduct[];
  similarProducts: CatalogProduct[];
  allProducts: CatalogProduct[];
}

export function PdpRecommendationsSection({
  product,
  shop,
  sameShopProducts,
  similarProducts,
  allProducts,
}: PdpRecommendationsSectionProps) {
  const { recentlyViewed, clearRecentlyViewed } = useMockStore();

  const recentProducts = useMemo(() => {
    const byId = new Map(allProducts.map((p) => [p.id, p]));
    const bySlug = new Map(allProducts.map((p) => [p.slug, p]));
    const seen = new Set<string>([product.id]);
    const list: CatalogProduct[] = [];

    for (const key of recentlyViewed) {
      const legacySlug = getEditorialProductSlugById(key);
      const found =
        byId.get(key) ?? bySlug.get(key) ?? (legacySlug ? bySlug.get(legacySlug) : undefined);
      if (found && !seen.has(found.id)) {
        seen.add(found.id);
        list.push(found);
      }
      if (list.length >= 4) break;
    }
    return list;
  }, [allProducts, recentlyViewed, product.id]);

  const gridClassFor = (count: number) => {
    if (count === 3) return `${styles.cardGrid} ${styles.cardGridThree}`;
    if (count <= 2) return `${styles.cardGrid} ${styles.cardGridTwo}`;
    return styles.cardGrid;
  };

  return (
    <>
      {sameShopProducts.length > 0 && (
        <section className={styles.recoSection} aria-labelledby="pdp-same-shop-heading">
          <div className={styles.sectionHead}>
            <h2 id="pdp-same-shop-heading" className={styles.sectionHeading}>
              {`Cùng gian hàng ${shop.shortName}`}
            </h2>
            {shop.slug && (
              <Link href={`/gian-hang/${shop.slug}`} className={styles.viewAllLink}>
                Xem toàn bộ {shop.shortName}
              </Link>
            )}
          </div>

          <div className={gridClassFor(sameShopProducts.length)}>
            {sameShopProducts.map((rel) => (
              <div key={rel.id}>
                <ProductCard product={rel} showSpec={true} showSla={true} />
              </div>
            ))}
          </div>
        </section>
      )}

      {similarProducts.length > 0 && (
        <section className={styles.recoSection} aria-labelledby="pdp-similar-heading">
          <div className={styles.sectionHead}>
            <h2 id="pdp-similar-heading" className={styles.sectionHeading}>
              Tương tự trên sàn NexusMart
            </h2>
            <Link href={`/danh-muc?cat=${product.rawCategorySlug}`} className={styles.viewAllLink}>
              Khám phá thêm {product.categoryName}
            </Link>
          </div>

          <div className={gridClassFor(similarProducts.length)}>
            {similarProducts.map((rel) => (
              <div key={rel.id}>
                <ProductCard product={rel} showSpec={true} showSla={true} />
              </div>
            ))}
          </div>
        </section>
      )}

      <section className={styles.recoSection} aria-labelledby="pdp-recent-heading">
        <div className={styles.sectionHead}>
          <h2 id="pdp-recent-heading" className={styles.sectionHeading}>
            Sản phẩm bạn đã xem
          </h2>
          {recentProducts.length > 0 && (
            <button type="button" className={styles.clearRecentBtn} onClick={clearRecentlyViewed}>
              Xóa lịch sử xem
            </button>
          )}
        </div>

        {recentProducts.length === 0 ? (
          <div className={styles.recentEmpty}>
            <p className={styles.recentEmptyText}>
              Bạn chưa xem thêm sản phẩm nào khác trong phiên duyệt này. Các mẫu bạn mở xem tiếp
              theo sẽ tự động xuất hiện tại đây để dễ so sánh chất liệu và giá.
            </p>
            <Button variant="secondary" size="md" href="/danh-muc">
              Xem toàn bộ {allProducts.length || 18} mẫu cơ bản
            </Button>
          </div>
        ) : (
          <div className={gridClassFor(recentProducts.length)}>
            {recentProducts.map((rel) => (
              <div key={rel.id}>
                <ProductCard product={rel} showSpec={true} />
              </div>
            ))}
          </div>
        )}
      </section>
    </>
  );
}
