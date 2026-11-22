import React, { Suspense } from "react";
import { StateView } from "@/components";
import { CatalogExplorer } from "@/features/catalog/CatalogExplorer";
import { loadCatalogExplorerData } from "@/lib/catalog";

export const dynamic = "force-dynamic";

interface CatalogPageProps {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}

export default async function CatalogPage({ searchParams }: CatalogPageProps) {
  const resolvedParams = await searchParams;
  const data = await loadCatalogExplorerData(resolvedParams);

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
        categories={data.categories}
        shops={data.shops}
        allProducts={data.allProducts}
        serverFilteredProducts={data.serverFilteredProducts}
        hasServerFilter={data.hasServerFilter}
        error={data.error}
      />
    </Suspense>
  );
}
