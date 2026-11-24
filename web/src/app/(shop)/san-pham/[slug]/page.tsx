import React, { Suspense, cache } from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import type { SizeCode } from "@/data";
import { Button, ProductCard, StateView } from "@/components";
import { loadProductDetailData } from "@/lib/catalog";
import { ProductDetailClient } from "./ProductDetailClient";
import styles from "./page.module.css";

export const dynamic = "force-dynamic";

interface ProductDetailPageProps {
  params: Promise<{ slug: string }>;
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}

const getProductDetailData = cache(loadProductDetailData);

function readParam(val: string | string[] | undefined): string | null {
  if (Array.isArray(val)) return val[0] ?? null;
  return val ?? null;
}

export async function generateMetadata({ params }: ProductDetailPageProps): Promise<Metadata> {
  const { slug } = await params;
  const data = await getProductDetailData(slug);

  if (data.discontinued) {
    const title = data.discontinued.name
      ? `${data.discontinued.name} — Sản phẩm đã ngừng kinh doanh`
      : "Sản phẩm đã ngừng kinh doanh";
    return {
      title,
      robots: { index: false, follow: true },
    };
  }

  if (data.product) {
    return {
      title: data.product.name,
    };
  }

  return {
    title: "Chi tiết sản phẩm",
  };
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

  const data = await getProductDetailData(slug);

  if (data.notFound) {
    notFound();
  }

  if (data.discontinued) {
    const categoryHref = data.discontinued.categorySlug
      ? `/danh-muc?cat=${encodeURIComponent(data.discontinued.categorySlug)}`
      : "/danh-muc";
    return (
      <div className={styles.page}>
        <section className={styles.discontinuedBanner} aria-labelledby="discontinued-heading">
          <p className={styles.discontinuedEyebrow}>Thông báo sản phẩm</p>
          <h1 id="discontinued-heading" className={styles.discontinuedTitle}>
            Sản phẩm đã ngừng kinh doanh
          </h1>
          {data.discontinued.name && (
            <p className={styles.discontinuedName}>{data.discontinued.name}</p>
          )}
          <p className={styles.discontinuedDesc}>
            Sản phẩm này hiện không còn mở bán trên sàn. Bạn có thể tham khảo các lựa chọn tương tự
            đang sẵn hàng bên dưới.
          </p>
          <div className={styles.discontinuedActions}>
            <Button variant="ink" size="md" href={categoryHref}>
              {data.discontinued.categoryName
                ? `Xem danh mục ${data.discontinued.categoryName}`
                : "Xem tất cả sản phẩm"}
            </Button>
            <Button variant="secondary" size="md" href="/">
              Về trang chủ
            </Button>
          </div>
        </section>

        {data.similarProducts.length > 0 && (
          <section className={styles.discontinuedSimilar} aria-labelledby="discontinued-similar-hd">
            <h2 id="discontinued-similar-hd" className={styles.discontinuedSectionTitle}>
              Sản phẩm tương tự
            </h2>
            <div className={styles.discontinuedGrid}>
              {data.similarProducts.map((item) => (
                <ProductCard key={item.id} product={item} showSpec={true} />
              ))}
            </div>
          </section>
        )}
      </div>
    );
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
        initialReviews={data.initialReviews}
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
