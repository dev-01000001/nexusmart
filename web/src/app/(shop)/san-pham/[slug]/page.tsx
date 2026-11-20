import React, { Suspense } from "react";
import { StateView } from "@/components";
import { loadProductDetail } from "@/lib/catalog";
import { ProductDetailClient } from "./ProductDetailClient";
import styles from "./page.module.css";

export const dynamic = "force-dynamic";

export default async function ProductDetailPage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const data = await loadProductDetail(slug);

  return (
    <Suspense
      fallback={
        <div className={styles.page}>
          <StateView kind="loading" title="Đang tải chi tiết sản phẩm..." rows={6} />
        </div>
      }
    >
      <ProductDetailClient
        slug={slug}
        product={data.product}
        shop={data.shop}
        allProducts={data.allProducts}
        shops={data.shops}
        shopVouchers={data.shopVouchers}
        platformVouchers={data.platformVouchers}
        liveFlashItem={data.liveFlashItem}
        loadError={data.error}
      />
    </Suspense>
  );
}
