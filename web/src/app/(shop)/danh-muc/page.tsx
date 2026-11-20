import React, { Suspense } from "react";
import { StateView } from "@/components";
import { CatalogExplorer } from "@/features/catalog/CatalogExplorer";
import { loadCatalog } from "@/lib/catalog";

export const dynamic = "force-dynamic";

export default async function CatalogPage() {
  const { products, categories, shops, error } = await loadCatalog();

  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải danh mục sản phẩm..." rows={6} />
        </div>
      }
    >
      <CatalogExplorer
        mode="catalog"
        products={products}
        categories={categories}
        shops={shops}
        loadError={error}
      />
    </Suspense>
  );
}
