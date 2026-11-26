import React from "react";
import {
  SELLER_PRODUCTS_PAGE_SIZE,
  deriveSellerProductTabCountsFromCompleteList,
  type SellerProductTabCounts,
} from "@/lib/a8-types";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { SellerProductsClient } from "@/features/seller/SellerProductsClient";
import {
  loadSellerCategories,
  loadSellerProductTabCounts,
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

  const initialProducts = productsRes.data?.items ?? [];
  const initialTotal = productsRes.data?.total ?? 0;
  let initialTabCounts: SellerProductTabCounts | undefined;
  if (!productsRes.error) {
    initialTabCounts =
      deriveSellerProductTabCountsFromCompleteList(initialProducts, initialTotal) ??
      (await loadSellerProductTabCounts(initialTotal));
  }

  return (
    <SellerSuspense>
      <SellerProductsClient
        initialProducts={initialProducts}
        initialTotal={initialTotal}
        initialTabCounts={initialTabCounts}
        categories={categoriesRes.data ?? []}
        loadError={productsRes.error}
      />
    </SellerSuspense>
  );
}
