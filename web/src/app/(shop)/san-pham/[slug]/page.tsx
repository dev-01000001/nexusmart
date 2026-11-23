import React, { Suspense } from "react";
import { notFound } from "next/navigation";
import type { SizeCode } from "@/data";
import { StateView } from "@/components";
import { loadProductDetailData } from "@/lib/catalog";
import { ProductDetailClient } from "./ProductDetailClient";
import styles from "./page.module.css";

export const dynamic = "force-dynamic";

interface ProductDetailPageProps {
  params: Promise<{ slug: string }>;
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}

function readParam(val: string | string[] | undefined): string | null {
  if (Array.isArray(val)) return val[0] ?? null;
  return val ?? null;
}

export default async function ProductDetailPage({ params, searchParams }: ProductDetailPageProps) {
  const [{ slug }, resolvedSearch] = await Promise.all([params, searchParams]);
  const stateParam = readParam(resolvedSearch.state);
  const colorQuery = readParam(resolvedSearch.color);
  const sizeQuery = readParam(resolvedSearch.size) as SizeCode | null;

  if (stateParam === "loading") {
    return (
      <div className={styles.page}>
        <StateView kind="loading" title="Đang tải thông tin sản phẩm..." rows={6} />
      </div>
    );
  }

  if (stateParam === "error") {
    return (
      <div className={styles.page}>
        <StateView
          kind="error"
          title="Không thể tải chi tiết sản phẩm"
          message="Kết nối đến kho dữ liệu sản phẩm tạm gián đoạn. Vui lòng thử tải lại."
          actionLabel="Tải lại trang"
          actionHref={`/san-pham/${encodeURIComponent(slug)}`}
        />
      </div>
    );
  }

  if (stateParam === "empty") {
    return (
      <div className={styles.page}>
        <StateView
          kind="empty"
          title="Không tìm thấy sản phẩm này"
          message="Sản phẩm có thể đã ngưng kinh doanh hoặc đường dẫn không còn chính xác."
          actionLabel="Quay lại danh mục"
          actionHref="/danh-muc"
        />
      </div>
    );
  }

  const data = await loadProductDetailData(slug);

  if (data.notFound) {
    notFound();
  }

  if (data.error || !data.product || !data.shop) {
    return (
      <div className={styles.page}>
        <StateView
          kind="error"
          title="Không thể tải chi tiết sản phẩm"
          message={
            data.error ?? "Kết nối đến kho dữ liệu sản phẩm tạm gián đoạn. Vui lòng thử tải lại."
          }
          actionLabel="Tải lại trang"
          actionHref={`/san-pham/${encodeURIComponent(slug)}`}
        />
      </div>
    );
  }

  return (
    <Suspense
      fallback={
        <div className={styles.page}>
          <StateView kind="loading" title="Đang tải chi tiết sản phẩm..." rows={6} />
        </div>
      }
    >
      <ProductDetailClient
        key={data.product.id}
        product={data.product}
        shop={data.shop}
        shopVouchers={data.shopVouchers}
        platformVouchers={data.platformVouchers}
        sameShopProducts={data.sameShopProducts}
        similarProducts={data.similarProducts}
        allProducts={data.allProducts}
        liveFlashBySkuId={data.liveFlashBySkuId}
        activeShopDiscountBySkuId={data.activeShopDiscountBySkuId}
        provinces={data.provinces}
        initialColorQuery={colorQuery}
        initialSizeQuery={sizeQuery}
      />
    </Suspense>
  );
}
