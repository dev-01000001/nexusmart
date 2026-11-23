import React, { Suspense } from "react";
import { notFound } from "next/navigation";
import { CAMPAIGNS } from "@/data";
import { StateView } from "@/components";
import { CampaignDetailClient } from "./CampaignDetailClient";

export function generateStaticParams() {
  return CAMPAIGNS.map((c) => ({ slug: c.slug }));
}

export default async function CampaignDetailPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  if (!CAMPAIGNS.some((c) => c.slug === slug)) {
    notFound();
  }

  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải trang chiến dịch NexusMart..." rows={6} />
        </div>
      }
    >
      <CampaignDetailClient slug={slug} />
    </Suspense>
  );
}
