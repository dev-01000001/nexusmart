import React, { Suspense } from "react";
import { StateView } from "@/components";
import { loadShopDetail } from "@/lib/catalog";
import { ShopDetailClient } from "./ShopDetailClient";

export const dynamic = "force-dynamic";

export default async function ShopPage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const data = await loadShopDetail(slug);

  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải gian hàng..." rows={6} />
        </div>
      }
    >
      <ShopDetailClient
        slug={slug}
        shop={data.shop}
        otherShops={data.otherShops}
        shopProducts={data.shopProducts}
        allProducts={data.allProducts}
        categories={data.categories}
        shops={data.shops}
        shopVouchers={data.shopVouchers}
        platformVouchers={data.platformVouchers}
        loadError={data.error}
      />
    </Suspense>
  );
}
