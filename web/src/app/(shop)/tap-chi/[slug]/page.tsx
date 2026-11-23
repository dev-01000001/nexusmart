import React, { Suspense } from "react";
import { notFound } from "next/navigation";
import { JOURNAL_ARTICLES } from "@/data";
import { StateView } from "@/components";
import { JournalDetailClient } from "./JournalDetailClient";

export function generateStaticParams() {
  return JOURNAL_ARTICLES.map((a) => ({ slug: a.slug }));
}

export default async function JournalDetailPage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  if (!JOURNAL_ARTICLES.some((a) => a.slug === slug)) {
    notFound();
  }

  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải bài viết Sổ tay vải..." rows={6} />
        </div>
      }
    >
      <JournalDetailClient slug={slug} />
    </Suspense>
  );
}
