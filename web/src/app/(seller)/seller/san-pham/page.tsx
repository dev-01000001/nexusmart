import React from "react";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerProductsClient } from "@/features/seller/SellerProductsClient";
import {
  loadCategories,
  loadSellerProducts,
  requireSellerPage,
} from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerProductsPage() {
  const session = await requireSellerPage();

  if (!session.user.shopId) {
    return (
      <SellerSuspense>
        <SellerProductsClient
          initialProducts={[]}
          categories={[]}
          loadError="Tài khoản người bán chưa có gian hàng hoạt động."
        />
      </SellerSuspense>
    );
  }

  const [productsRes, categoriesRes] = await Promise.all([
    loadSellerProducts(100),
    loadCategories(),
  ]);

  return (
    <SellerSuspense>
      <SellerProductsClient
        initialProducts={productsRes.data?.items ?? []}
        categories={categoriesRes.data ?? []}
        loadError={productsRes.error ?? categoriesRes.error}
      />
    </SellerSuspense>
  );
}
