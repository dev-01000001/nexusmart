import React, { Suspense } from "react";
import { COLLECTIONS } from "@/data";
import { StateView } from "@/components";
import { CollectionDetailClient } from "./CollectionDetailClient";

export function generateStaticParams() {
  return COLLECTIONS.map((c) => ({ slug: c.slug }));
}

export default async function CollectionDetailPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;

  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải bộ sưu tập..." rows={6} />
        </div>
      }
    >
      <CollectionDetailClient slug={slug} />
    </Suspense>
  );
}
