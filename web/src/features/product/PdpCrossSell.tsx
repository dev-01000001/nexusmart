"use client";

import React, { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { getSku, type CartComboSelection, type Product, type Shop, type SizeCode } from "@/data";
import { useStore } from "@/context/StoreContext";
import { Button, Price, ProductCard } from "@/components";
import { formatVnd } from "@/lib/format";
import styles from "./PdpCrossSell.module.css";

export interface PdpBundleSectionProps {
  product: Product;
  shop: Shop;
  allProducts: Product[];
  shops: Shop[];
  selectedColorCode: string;
  selectedSize: SizeCode;
  comboSelections?: CartComboSelection[];
}

function getDefaultInStockSize(p: Product): SizeCode {
  const firstColor = p.colors[0]?.code ?? "";
  for (const preferred of ["L", "M", ...p.sizes] as SizeCode[]) {
    if (!p.sizes.includes(preferred)) continue;
    const sku = getSku(p, firstColor, preferred);
    if (sku && sku.stock > 0) return preferred;
  }
  return p.sizes[0] ?? "M";
}

export function PdpBundleSection({
  product,
  shop,
  allProducts,
  shops,
  selectedColorCode,
  selectedSize,
}: PdpBundleSectionProps) {
  const router = useRouter();
  const { addToCart, showToast } = useStore();
  const [adding, setAdding] = useState(false);

  const productById = useMemo(() => new Map(allProducts.map((p) => [p.id, p])), [allProducts]);
  const shopById = useMemo(() => new Map(shops.map((s) => [s.id, s])), [shops]);

  const bundleProducts = useMemo(() => {
    const ids = product.bundleIds ?? [];
    const list: Product[] = [];
    for (const id of ids) {
      if (id === product.id) continue;
      const found = productById.get(id);
      if (found) list.push(found);
    }
    return list;
  }, [product, productById]);

  const [checkedIds, setCheckedIds] = useState<Record<string, boolean>>(() => {
    const init: Record<string, boolean> = {};
    for (const id of product.bundleIds ?? []) {
      init[id] = true;
    }
    return init;
  });

  const [pickedSizes, setPickedSizes] = useState<Record<string, SizeCode>>({});

  if (bundleProducts.length === 0) return null;

  const isChecked = (id: string) => (id in checkedIds ? checkedIds[id] : true);
  const getSizeFor = (p: Product): SizeCode => pickedSizes[p.id] ?? getDefaultInStockSize(p);

  const selectedBundleItems = bundleProducts.filter((p) => isChecked(p.id));
  const currentSku = getSku(product, selectedColorCode, selectedSize);
  const currentPrice = currentSku?.price ?? product.price;
  const totalSelectedCount = 1 + selectedBundleItems.length;
  const combinedTotal =
    currentPrice + selectedBundleItems.reduce((sum, item) => sum + item.price, 0);
  const sameShopCount = selectedBundleItems.filter((item) => item.shopId === product.shopId).length;

  const handleToggleItem = (id: string) => {
    setCheckedIds((prev) => ({
      ...prev,
      [id]: !(id in prev ? prev[id] : true),
    }));
  };

  const handleAddBundle = async () => {
    if (adding || !currentSku || currentSku.stock <= 0 || !currentSku.skuId) return;
    setAdding(true);
    try {
      let addedCount = 0;
      const mainRes = await addToCart({
        skuId: currentSku.skuId,
        quantity: 1,
        productName: product.shortName,
        size: selectedSize,
      });
      if (mainRes.unauthenticated) {
        router.push(`/dang-nhap?next=${encodeURIComponent(`/san-pham/${product.slug}`)}`);
        return;
      }
      if (mainRes.ok) addedCount += 1;

      for (const item of selectedBundleItems) {
        const sz = getSizeFor(item);
        const colorCode = item.colors[0]?.code;
        if (!colorCode) continue;
        const itemSku = getSku(item, colorCode, sz);
        if (!itemSku || itemSku.stock <= 0 || !itemSku.skuId) continue;
        const res = await addToCart({
          skuId: itemSku.skuId,
          quantity: 1,
          productName: item.shortName,
          size: sz,
        });
        if (res.unauthenticated) {
          router.push(`/dang-nhap?next=${encodeURIComponent(`/san-pham/${product.slug}`)}`);
          return;
        }
        if (res.ok) addedCount += 1;
      }

      if (addedCount > 1) {
        showToast(`Đã thêm bộ ${addedCount} sản phẩm vào giỏ hàng.`);
      }
    } finally {
      setAdding(false);
    }
  };

  const canAddMain = Boolean(currentSku && currentSku.stock > 0 && currentSku.skuId);

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
            const itemShop = shopById.get(item.shopId);
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
                    {sameShop
                      ? `Cùng xưởng ${shop.shortName} · Gộp ship`
                      : (itemShop?.shortName ?? "NexusMart")}
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
                        {item.sizes.map((sz) => {
                          const sku = item.colors[0]
                            ? getSku(item, item.colors[0].code, sz)
                            : undefined;
                          const oos = !sku || sku.stock <= 0;
                          return (
                            <option key={sz} value={sz} disabled={oos}>
                              Size {sz}
                              {oos ? " (Hết hàng)" : ""}
                            </option>
                          );
                        })}
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
            disabled={!canAddMain || adding}
            onClick={handleAddBundle}
          >
            {adding ? "Đang thêm vào giỏ..." : `Thêm ${totalSelectedCount} món vào giỏ`}
          </Button>
        </aside>
      </div>
    </section>
  );
}

export interface PdpRecommendationsSectionProps {
  product: Product;
  shop: Shop;
  allProducts: Product[];
  shops: Shop[];
}

export function PdpRecommendationsSection({
  product,
  shop,
  allProducts,
  shops,
}: PdpRecommendationsSectionProps) {
  const { recentlyViewed, clearRecentlyViewed } = useStore();

  const productById = useMemo(() => new Map(allProducts.map((p) => [p.id, p])), [allProducts]);
  const shopById = useMemo(() => new Map(shops.map((s) => [s.id, s])), [shops]);

  const sameShopProducts = useMemo(
    () => allProducts.filter((p) => p.id !== product.id && p.shopId === product.shopId).slice(0, 4),
    [allProducts, product.id, product.shopId]
  );

  const similarOnPlatform = useMemo(() => {
    const others = allProducts.filter((p) => p.id !== product.id && p.shopId !== product.shopId);
    const scored = others.map((candidate) => {
      let score = 0;
      if (candidate.categorySlug === product.categorySlug) score += 4;
      if (candidate.material && candidate.material === product.material) score += 4;
      if (Math.abs(candidate.price - product.price) <= 90000) score += 2;
      const sharedFeatures = (candidate.fabricFeatures ?? []).filter((f) =>
        (product.fabricFeatures ?? []).includes(f)
      ).length;
      score += sharedFeatures;
      return { candidate, score };
    });
    scored.sort((a, b) => b.score - a.score);
    return scored.slice(0, 4).map((x) => x.candidate);
  }, [allProducts, product]);

  const recentProducts = useMemo(() => {
    const list: Product[] = [];
    for (const id of recentlyViewed) {
      if (id === product.id) continue;
      const found = productById.get(id);
      if (found) list.push(found);
      if (list.length >= 4) break;
    }
    return list;
  }, [recentlyViewed, product.id, productById]);

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
            <Link href={`/gian-hang/${shop.slug}`} className={styles.viewAllLink}>
              Xem toàn bộ {shop.shortName}
            </Link>
          </div>

          <div className={gridClassFor(sameShopProducts.length)}>
            {sameShopProducts.map((rel) => (
              <div key={rel.id}>
                <ProductCard product={rel} shop={shop} showSpec={true} showSla={true} />
              </div>
            ))}
          </div>
        </section>
      )}

      {similarOnPlatform.length > 0 && (
        <section className={styles.recoSection} aria-labelledby="pdp-similar-heading">
          <div className={styles.sectionHead}>
            <h2 id="pdp-similar-heading" className={styles.sectionHeading}>
              Tương tự trên sàn NexusMart
            </h2>
            <Link href={`/danh-muc?cat=${product.categorySlug}`} className={styles.viewAllLink}>
              Khám phá thêm {product.categoryName}
            </Link>
          </div>

          <div className={gridClassFor(similarOnPlatform.length)}>
            {similarOnPlatform.map((rel) => (
              <div key={rel.id}>
                <ProductCard
                  product={rel}
                  shop={shopById.get(rel.shopId)}
                  showSpec={true}
                  showSla={true}
                />
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
              Xem toàn bộ {allProducts.length} mẫu cơ bản
            </Button>
          </div>
        ) : (
          <div className={gridClassFor(recentProducts.length)}>
            {recentProducts.map((rel) => (
              <div key={rel.id}>
                <ProductCard product={rel} shop={shopById.get(rel.shopId)} showSpec={true} />
              </div>
            ))}
          </div>
        )}
      </section>
    </>
  );
}
