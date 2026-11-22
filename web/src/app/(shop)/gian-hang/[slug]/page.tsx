import React, { Suspense } from "react";
import { notFound } from "next/navigation";
import { StateView } from "@/components";
import { loadShopDetailData } from "@/lib/catalog";
import { ShopDetailClient } from "./ShopDetailClient";
import styles from "./page.module.css";

export const dynamic = "force-dynamic";

interface ShopPageProps {
  params: Promise<{ slug: string }>;
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}

function readParam(val: string | string[] | undefined): string | null {
  if (Array.isArray(val)) return val[0] ?? null;
  return val ?? null;
}

export default async function ShopPage({ params, searchParams }: ShopPageProps) {
  const [{ slug }, resolvedSearch] = await Promise.all([params, searchParams]);
  const stateParam = readParam(resolvedSearch.state);

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
          actionHref={`/gian-hang/${encodeURIComponent(slug)}`}
        />
      </div>
    );
  }

  if (stateParam === "empty") {
    return (
      <div className={styles.page}>
        <StateView
          kind="empty"
          title="Không tìm thấy gian hàng xưởng dệt này"
          message="Gian hàng có thể đã thay đổi đường dẫn. Bạn có thể xem toàn bộ sản phẩm từ các xưởng đang hoạt động."
          actionLabel="Xem toàn bộ danh mục"
          actionHref="/danh-muc"
        />
      </div>
    );
  }

  const data = await loadShopDetailData(slug);

  if (data.notFound) {
    notFound();
  }

  if (data.error || !data.shop) {
    return (
      <div className={styles.page}>
        <StateView
          kind="error"
          title="Không thể tải thông tin gian hàng"
          message={data.error ?? "Dữ liệu gian hàng tạm thời không phản hồi. Vui lòng thử tải lại."}
          actionLabel="Tải lại gian hàng"
          actionHref={`/gian-hang/${encodeURIComponent(slug)}`}
        />
      </div>
    );
  }

  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải gian hàng..." rows={6} />
        </div>
      }
    >
      <ShopDetailClient
        key={data.shop.id}
        shop={data.shop}
        otherShops={data.otherShops}
        categories={data.categories}
        shopProducts={data.shopProducts}
        allProducts={data.allProducts}
        shopVouchers={data.shopVouchers}
        platformVouchers={data.platformVouchers}
      />
    </Suspense>
  );
}
