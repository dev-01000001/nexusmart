import React, { Suspense } from "react";
import { StateView } from "@/components";
import { CatalogExplorer } from "@/features/catalog/CatalogExplorer";
import { loadCatalogExplorerData } from "@/lib/catalog";

export const dynamic = "force-dynamic";

interface SearchPageProps {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}

export default async function SearchPage({ searchParams }: SearchPageProps) {
  const resolvedParams = await searchParams;
  const data = await loadCatalogExplorerData(resolvedParams);

  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải trang tìm kiếm..." rows={5} />
        </div>
      }
    >
      <CatalogExplorer
        mode="search"
        categories={data.categories}
        shops={data.shops}
        products={data.products}
        serverTotal={data.serverTotal}
        page={data.page}
        pageSize={data.pageSize}
        categoryFacets={data.categoryFacets}
        shopFacets={data.shopFacets}
        didYouMean={data.didYouMean}
        degradedMode={data.degradedMode}
        trendingKeywords={data.trendingKeywords}
        error={data.error}
      />
    </Suspense>
  );
}
