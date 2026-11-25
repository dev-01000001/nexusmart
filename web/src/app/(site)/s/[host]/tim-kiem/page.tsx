import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { buildSiteMetadata } from "@/lib/site/seo";
import { SiteSearchResults } from "@/features/search";
import { loadSiteContext, loadSiteTemplateBundle } from "@/features/site/siteContext.server";

export const dynamic = "force-dynamic";

interface PageProps {
  params: Promise<{ host: string }>;
  searchParams: Promise<{
    q?: string;
    category?: string;
    cat?: string;
    sort?: string;
    min_price?: string;
    max_price?: string;
    page?: string;
  }>;
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
  const bundle = await loadSiteTemplateBundle(host);
  if (!bundle) notFound();

  const brandName = bundle.templateConfig.theme.brand_name?.trim() || bundle.payload.shop.name;
  const minPrice = sp.min_price ? Number.parseInt(sp.min_price, 10) : undefined;
  const maxPrice = sp.max_price ? Number.parseInt(sp.max_price, 10) : undefined;
  const parsedPage = sp.page ? Number.parseInt(sp.page, 10) : 1;

  return (
    <SiteSearchResults
      brandName={brandName}
      shopId={bundle.payload.site.shop_id}
      initialQuery={sp.q || ""}
      initialCategory={sp.category || sp.cat || ""}
      initialSort={sp.sort || "relevance"}
      initialMinPrice={Number.isFinite(minPrice) ? minPrice : undefined}
      initialMaxPrice={Number.isFinite(maxPrice) ? maxPrice : undefined}
      initialPage={Number.isFinite(parsedPage) && parsedPage > 0 ? parsedPage : 1}
      fallbackProducts={bundle.products}
      categories={bundle.categories}
    />
  );
}
