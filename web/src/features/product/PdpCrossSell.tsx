"use client";

import React, { useMemo } from "react";
import Link from "next/link";
import type { CatalogProduct, CatalogShop } from "@/lib/catalog-types";
import { useMockStore } from "@/mock/MockStoreContext";
import { getEditorialProductSlugById } from "@/mock/productEditorial";
import { Button, ProductCard } from "@/components";
import styles from "./PdpCrossSell.module.css";

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
              {allProducts.length > 0
                ? `Xem toàn bộ ${allProducts.length} sản phẩm`
                : "Xem toàn bộ danh mục"}
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
