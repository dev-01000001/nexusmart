import React from "react";
import type { Metadata } from "next";
import { headers } from "next/headers";
import { notFound } from "next/navigation";
import { APP_ORIGINS, getSiteRootDomain } from "@/lib/server/config";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { fetchPublishedSiteByHost, loadPublicSitePageBundle } from "@/lib/server/sites";
import { deriveMarketplaceOrigin, deriveSiteOrigin } from "@/lib/site/host";
import { buildSiteMetadata } from "@/lib/site/seo";
import { SiteCatalogView, SiteShell } from "@/features/site";

interface PageProps {
  params: Promise<{ host: string }>;
  searchParams: Promise<{ cat?: string; q?: string; sort?: string; page?: string }>;
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
  const { host } = await params;
  const decodedHost = decodeURIComponent(host);
  const payload = await fetchPublishedSiteByHost(decodedHost);
  if (!payload) {
    return {
      title: "Không tìm thấy cửa hàng",
      robots: { index: false, follow: false },
    };
  }
  const { siteOrigin } = await resolveOrigins(payload.site.primary_host || decodedHost);
  return buildSiteMetadata({
    config: payload.config,
    pageTitle: "Sản phẩm",
    description: `Danh sách sản phẩm chính hãng của ${payload.config.theme.brand_name || payload.shop.name}.`,
    siteOrigin,
    pathname: "/san-pham",
  });
}

export default async function SubdomainSiteProductsPage({ params, searchParams }: PageProps) {
  const [{ host }, sp] = await Promise.all([params, searchParams]);
  const decodedHost = decodeURIComponent(host);
  const bundle = await loadPublicSitePageBundle(decodedHost);
  if (!bundle) notFound();

  const { payload, products, categories } = bundle;
  const effectiveHost = payload.site.primary_host || decodedHost;
  const { marketplaceOrigin } = await resolveOrigins(effectiveHost);
  const brandName = payload.config.theme.brand_name?.trim() || payload.shop.name;
  const parsedPage = Number.parseInt(sp.page || "1", 10);

  return (
    <SiteShell
      config={payload.config}
      shopName={payload.shop.name}
      shopId={payload.shop.id}
      siteHost={effectiveHost}
      activeNav="products"
      marketplaceOrigin={marketplaceOrigin}
    >
      <SiteCatalogView
        brandName={brandName}
        products={products}
        categories={categories}
        initialCategory={sp.cat || ""}
        initialQuery={sp.q || ""}
        initialSort={sp.sort || "newest"}
        initialPage={Number.isFinite(parsedPage) && parsedPage > 0 ? parsedPage : 1}
      />
    </SiteShell>
  );
}
