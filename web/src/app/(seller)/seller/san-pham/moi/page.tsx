import React from "react";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { ProductFormView } from "@/features/seller/ProductFormView";
import { loadCategories, requireSellerPage } from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerNewProductPage() {
  await requireSellerPage();
  const categoriesRes = await loadCategories();
  const defaultSkuStamp = String(Date.now()).slice(-4);

  return (
    <SellerSuspense>
      <ProductFormView
        mode="create"
        categories={categoriesRes.data ?? []}
        loadError={categoriesRes.error}
        defaultSkuStamp={defaultSkuStamp}
      />
    </SellerSuspense>
  );
}
