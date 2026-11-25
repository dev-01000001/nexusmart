import React from "react";
import { SELLER_PRODUCTS_PAGE_SIZE } from "@/lib/a8-types";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerProductsClient } from "@/features/seller/SellerProductsClient";
import {
  loadSellerCategories,
  loadSellerProducts,
  requireSellerPage,
} from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerProductsPage() {
  await requireSellerPage();

  const [productsRes, categoriesRes] = await Promise.all([
    loadSellerProducts(SELLER_PRODUCTS_PAGE_SIZE, 1),
    loadSellerCategories(),
  ]);

  return (
    <SellerSuspense>
      <SellerProductsClient
        initialProducts={productsRes.data?.items ?? []}
        initialTotal={productsRes.data?.total ?? 0}
        categories={categoriesRes.data ?? []}
        loadError={productsRes.error}
      />
    </SellerSuspense>
  );
}
