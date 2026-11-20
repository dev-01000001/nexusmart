import React, { Suspense } from "react";
import { StateView } from "@/components";
import { CatalogExplorer } from "@/features/catalog/CatalogExplorer";

export default function CatalogPage() {
  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải danh mục sản phẩm..." rows={6} />
        </div>
      }
    >
      <CatalogExplorer mode="catalog" />
    </Suspense>
  );
}
