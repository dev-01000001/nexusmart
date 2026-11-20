"use client";

import React from "react";
import { useParams, useSearchParams } from "next/navigation";
import { StateView } from "@/components";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { ProductFormView } from "@/features/seller/ProductFormView";
import { parseUiState } from "@/features/seller/sellerStore";

function EditProductInner() {
  const params = useParams<{ id: string }>();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const productId = typeof params?.id === "string" ? params.id : "";

  if (uiState === "loading") {
    return (
      <StateView
        kind="loading"
        surface="app"
        title="Đang tải thông tin sản phẩm và ma trận SKU..."
        rows={5}
      />
    );
  }
  if (uiState === "error") {
    return (
      <StateView
        kind="error"
        surface="app"
        title="Không thể tải chi tiết sản phẩm"
        message="Dịch vụ danh mục đang tạm gián đoạn. Vui lòng thử lại."
        actionLabel="Thử lại"
        actionHref={`/seller/san-pham/${productId}`}
      />
    );
  }
  if (uiState === "empty") {
    return (
      <StateView
        kind="empty"
        surface="app"
        title="Sản phẩm chưa có biến thể SKU nào"
        message="Quay về danh sách sản phẩm để kiểm tra lại mã sản phẩm."
        actionLabel="Về danh sách sản phẩm"
        actionHref="/seller/san-pham"
      />
    );
  }

  return <ProductFormView mode="edit" productId={productId} />;
}

export default function SellerEditProductPage() {
  return (
    <SellerSuspense>
      <EditProductInner />
    </SellerSuspense>
  );
}
