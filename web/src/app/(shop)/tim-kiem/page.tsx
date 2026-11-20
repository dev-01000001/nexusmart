import React, { Suspense } from "react";
import { StateView } from "@/components";
import { CatalogExplorer } from "@/features/catalog/CatalogExplorer";

export default function SearchPage() {
  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải trang tìm kiếm..." rows={5} />
        </div>
      }
    >
      <CatalogExplorer mode="search" />
    </Suspense>
  );
}
