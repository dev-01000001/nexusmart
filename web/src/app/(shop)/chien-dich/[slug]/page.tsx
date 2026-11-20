import React, { Suspense } from "react";
import { loadCatalog } from "@/lib/catalog";
import { StateView } from "@/components";
import { CampaignDetailClient } from "./CampaignDetailClient";

export const dynamic = "force-dynamic";

export default async function CampaignDetailPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const [{ slug }, catalog] = await Promise.all([params, loadCatalog()]);

  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải trang chiến dịch NexusMart..." rows={6} />
        </div>
      }
    >
      <CampaignDetailClient
        slug={slug}
        products={catalog.products}
        shops={catalog.shops}
        loadError={catalog.error}
      />
    </Suspense>
  );
}
