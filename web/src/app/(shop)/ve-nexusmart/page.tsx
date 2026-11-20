import React, { Suspense } from "react";
import { loadCatalog } from "@/lib/catalog";
import { StateView } from "@/components";
import { AboutClient } from "./AboutClient";

export const dynamic = "force-dynamic";

export default async function AboutNexusMartPage() {
  const catalog = await loadCatalog();

  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải trang Về NexusMart..." rows={5} />
        </div>
      }
    >
      <AboutClient shops={catalog.shops} products={catalog.products} loadError={catalog.error} />
    </Suspense>
  );
}
