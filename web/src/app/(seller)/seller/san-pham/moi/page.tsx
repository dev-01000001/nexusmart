"use client";

import React from "react";
import { useSearchParams } from "next/navigation";
import { StateView } from "@/components";
import { SellerSuspense } from "@/features/seller/SellerStateGuard";
import { ProductFormView } from "@/features/seller/ProductFormView";
import { parseUiState } from "@/features/seller/sellerStore";

function NewProductInner() {
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));

  if (uiState === "loading") {
    return (
      <StateView
        kind="loading"
        surface="app"
        title="Đang khởi tạo biểu mẫu sản phẩm và bảng size..."
        rows={5}
      />
    );
  }
  if (uiState === "error") {
    return (
      <StateView
        kind="error"
        surface="app"
        title="Không thể khởi tạo biểu mẫu tạo sản phẩm"
        message="Danh mục ngành hàng tạm chưa sẵn sàng. Vui lòng thử tải lại."
        actionLabel="Thử lại"
        actionHref="/seller/san-pham/moi"
      />
    );
  }
  if (uiState === "empty") {
    return (
      <StateView
        kind="empty"
        surface="app"
        title="Chưa chọn danh mục mở bán cho gian hàng"
        message="Quay lại danh sách sản phẩm hoặc cấu hình danh mục trước khi đăng mới."
        actionLabel="Về danh sách sản phẩm"
        actionHref="/seller/san-pham"
      />
    );
  }

  return <ProductFormView mode="create" />;
}

export default function SellerNewProductPage() {
  return (
    <SellerSuspense>
      <NewProductInner />
    </SellerSuspense>
  );
}
