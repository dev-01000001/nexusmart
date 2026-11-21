import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { loadPublicSitePageBundle } from "@/lib/server/sites";
import { buildSiteMetadata } from "@/lib/site/seo";
import { SiteSearchView } from "@/features/site/SiteSearchView";
import { loadSiteContext } from "@/features/site/siteContext.server";

export const dynamic = "force-dynamic";

interface PageProps {
  params: Promise<{ host: string }>;
  searchParams: Promise<{ q?: string; cat?: string; sort?: string }>;
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
    pageTitle: "Tìm kiếm sản phẩm",
    description: `Tìm kiếm sản phẩm chính hãng tại ${ctx.brandName}.`,
    siteOrigin: ctx.siteOrigin,
    pathname: "/tim-kiem",
  });
}

export default async function SubdomainSiteSearchPage({ params, searchParams }: PageProps) {
  const [{ host }, sp] = await Promise.all([params, searchParams]);
  const decodedHost = decodeURIComponent(host);
  const bundle = await loadPublicSitePageBundle(decodedHost);
  if (!bundle) notFound();

  const brandName = bundle.payload.config.theme.brand_name?.trim() || bundle.payload.shop.name;

  return (
    <SiteSearchView
      brandName={brandName}
      products={bundle.products}
      categories={bundle.categories}
      initialQuery={sp.q || ""}
      initialCategory={sp.cat || ""}
      initialSort={sp.sort || "relevance"}
    />
  );
}
