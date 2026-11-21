import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { loadPublicSiteProduct } from "@/lib/server/sites";
import { buildSiteProductMetadata } from "@/lib/site/seo";
import { SitePdpView } from "@/features/site";
import { loadSiteContext } from "@/features/site/siteContext.server";

interface PageProps {
  params: Promise<{ host: string; slug: string }>;
}

export async function generateMetadata({ params }: PageProps): Promise<Metadata> {
  const { host, slug } = await params;
  const decodedHost = decodeURIComponent(host);
  const decodedSlug = decodeURIComponent(slug);
  const [ctx, data] = await Promise.all([
    loadSiteContext(host),
    loadPublicSiteProduct(decodedHost, decodedSlug),
  ]);
  if (!ctx || !data) {
    return {
      title: "Không tìm thấy sản phẩm",
      robots: { index: false, follow: false },
    };
  }

  return buildSiteProductMetadata({
    config: data.bundle.payload.config,
    productName: data.product.name,
    productSlug: data.product.slug,
    productDescription: data.product.description,
    siteOrigin: ctx.siteOrigin,
  });
}

export default async function SubdomainSiteProductDetailPage({ params }: PageProps) {
  const { host, slug } = await params;
  const decodedHost = decodeURIComponent(host);
  const decodedSlug = decodeURIComponent(slug);
  const data = await loadPublicSiteProduct(decodedHost, decodedSlug);
  if (!data) notFound();

  const { bundle, product, relatedProducts, liveFlashItem } = data;
  const { payload, vouchers } = bundle;
  const brandName = payload.config.theme.brand_name?.trim() || payload.shop.name;

  return (
    <SitePdpView
      brandName={brandName}
      product={product}
      shopVouchers={vouchers}
      relatedProducts={relatedProducts}
      liveFlashItem={liveFlashItem}
    />
  );
}
