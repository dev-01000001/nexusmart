import React from "react";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { ProductFormView } from "@/features/seller/ProductFormView";
import {
  loadSellerCategories,
  loadSellerProductById,
  requireSellerPage,
} from "@/features/seller/sellerServer";

export const dynamic = "force-dynamic";

export default async function SellerEditProductPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const session = await requireSellerPage();
  const { id } = await params;

  const [productRes, categoriesRes] = await Promise.all([
    loadSellerProductById(id, session.user.shopId),
    loadSellerCategories(),
  ]);

  return (
    <SellerSuspense>
      <ProductFormView
        mode="edit"
        initialProduct={productRes.data}
        categories={categoriesRes.data ?? []}
        loadError={productRes.error ?? categoriesRes.error}
      />
    </SellerSuspense>
  );
}
