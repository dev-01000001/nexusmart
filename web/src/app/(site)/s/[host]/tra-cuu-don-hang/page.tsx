import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { loadPublicSitePageBundle } from "@/lib/server/sites";
import { buildSiteMetadata } from "@/lib/site/seo";
import { OrderLookupView } from "@/features/commerce/OrderLookupView";
import { loadSiteContext } from "@/features/site/siteContext.server";

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
    pageTitle: "Tra cứu đơn hàng",
    description: `Tra cứu trạng thái giao hàng và thanh toán đơn đặt tại ${ctx.brandName}.`,
    siteOrigin: ctx.siteOrigin,
    pathname: "/tra-cuu-don-hang",
  });
}

export default async function SiteOrderLookupPage({ params }: PageProps) {
  const { host } = await params;
  const decodedHost = decodeURIComponent(host);
  const bundle = await loadPublicSitePageBundle(decodedHost);
  if (!bundle) notFound();

  const brandName = bundle.payload.config.theme.brand_name?.trim() || bundle.payload.shop.name;

  return <OrderLookupView brandName={brandName} suggestedProducts={bundle.products.slice(0, 4)} />;
}
