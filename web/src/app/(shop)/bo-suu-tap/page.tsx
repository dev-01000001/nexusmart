import React, { Suspense } from "react";
import { loadCatalog } from "@/lib/catalog";
import { StateView } from "@/components";
import { CollectionsClient } from "./CollectionsClient";

export const dynamic = "force-dynamic";

export default async function CollectionsPage() {
  const catalog = await loadCatalog();

  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải bộ sưu tập..." rows={5} />
        </div>
      }
    >
      <CollectionsClient products={catalog.products} loadError={catalog.error} />
    </Suspense>
  );
}
