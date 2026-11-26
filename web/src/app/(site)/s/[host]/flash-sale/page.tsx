import React, { Suspense } from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { SiteLoadingSkeleton } from "@/features/commerce/ui/SiteLoadingSkeleton";
import { SitePageFrame } from "@/features/commerce/ui/SitePageFrame";
import { buildSiteMetadata } from "@/lib/site/seo";
import { loadSiteContext, loadSiteFlashSales } from "@/features/site/siteContext.server";
import { FlashSaleClient } from "./FlashSaleClient";

export const dynamic = "force-dynamic";

interface PageProps {
  params: Promise<{ host: string }>;
}

export async function generateMetadata({ params }: PageProps): Promise<Metadata> {
  const { host } = await params;
  const ctx = await loadSiteContext(host);
  if (!ctx) {
    return {
      title: "Không tìm thấy cửa hàng",
      robots: { index: false, follow: false },
    };
  }
  return buildSiteMetadata({
    config: ctx.payload.config,
    pageTitle: "Flash sale",
    description: `Khung giờ ưu đãi Flash sale trực tiếp từ ${ctx.brandName}.`,
    siteOrigin: ctx.siteOrigin,
    pathname: "/flash-sale",
  });
}

export default async function SiteFlashSalePage({ params }: PageProps) {
  const { host } = await params;
  const data = await loadSiteFlashSales(host);
  if (!data) notFound();

  return (
    <Suspense
      fallback={
        <SitePageFrame>
          <SiteLoadingSkeleton title="Đang tải khung giờ Flash sale..." rows={4} variant="grid" />
        </SitePageFrame>
      }
    >
      <FlashSaleClient
        brandName={data.brandName}
        slots={data.slots}
        products={data.products}
        loadError={data.error}
        serverNowMs={Date.now()}
      />
    </Suspense>
  );
}
