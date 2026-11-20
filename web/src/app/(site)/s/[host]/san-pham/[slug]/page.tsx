import React from "react";
import type { Metadata } from "next";
import { headers } from "next/headers";
import { notFound } from "next/navigation";
import { APP_ORIGINS, getSiteRootDomain } from "@/lib/server/config";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { loadPublicSiteProduct } from "@/lib/server/sites";
import { deriveMarketplaceOrigin, deriveSiteOrigin } from "@/lib/site/host";
import { buildSiteProductMetadata } from "@/lib/site/seo";
import { SitePdpView, SiteShell } from "@/features/site";

interface PageProps {
  params: Promise<{ host: string; slug: string }>;
}

async function resolveOrigins(siteHost: string) {
  const h = await headers();
  const rawHost = resolveRequestHost(h, siteHost);
  const proto = resolveRequestProto(h, "http://localhost");
  const marketplaceOrigin = deriveMarketplaceOrigin({
    rawHost,
    proto,
    rootDomain: getSiteRootDomain(),
    appOrigin: APP_ORIGINS[0],
  });
  const siteOrigin = deriveSiteOrigin({ siteHost, rawHost, proto });
  return { marketplaceOrigin, siteOrigin };
}

export async function generateMetadata({ params }: PageProps): Promise<Metadata> {
  const { host, slug } = await params;
  const decodedHost = decodeURIComponent(host);
  const decodedSlug = decodeURIComponent(slug);
  const data = await loadPublicSiteProduct(decodedHost, decodedSlug);
  if (!data) {
    return {
      title: "Không tìm thấy sản phẩm",
      robots: { index: false, follow: false },
    };
  }

  const effectiveHost = data.bundle.payload.site.primary_host || decodedHost;
  const { marketplaceOrigin, siteOrigin } = await resolveOrigins(effectiveHost);

  return buildSiteProductMetadata({
    config: data.bundle.payload.config,
    productName: data.product.name,
    productSlug: data.product.slug,
    productDescription: data.product.description,
    marketplaceOrigin,
    siteOrigin,
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
  const effectiveHost = payload.site.primary_host || decodedHost;
  const { marketplaceOrigin } = await resolveOrigins(effectiveHost);
  const brandName = payload.config.theme.brand_name?.trim() || payload.shop.name;

  return (
    <SiteShell
      config={payload.config}
      shopName={payload.shop.name}
      shopId={payload.shop.id}
      siteHost={effectiveHost}
      activeNav="products"
      marketplaceOrigin={marketplaceOrigin}
    >
      <SitePdpView
        brandName={brandName}
        shopId={payload.shop.id}
        siteHost={effectiveHost}
        marketplaceOrigin={marketplaceOrigin}
        product={product}
        shopVouchers={vouchers}
        relatedProducts={relatedProducts}
        liveFlashItem={liveFlashItem}
      />
    </SiteShell>
  );
}
