import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { loadPublicSitePageBundle } from "@/lib/server/sites";
import { buildSiteMetadata } from "@/lib/site/seo";
import { SiteCatalogView } from "@/features/site";
import { loadSiteContext } from "@/features/site/siteContext.server";

interface PageProps {
  params: Promise<{ host: string }>;
  searchParams: Promise<{ cat?: string; q?: string; sort?: string; page?: string }>;
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
    pageTitle: "Sản phẩm",
    description: `Danh sách sản phẩm chính hãng của ${ctx.brandName}.`,
    siteOrigin: ctx.siteOrigin,
    pathname: "/san-pham",
  });
}

export default async function SubdomainSiteProductsPage({ params, searchParams }: PageProps) {
  const [{ host }, sp] = await Promise.all([params, searchParams]);
  const decodedHost = decodeURIComponent(host);
  const bundle = await loadPublicSitePageBundle(decodedHost);
  if (!bundle) notFound();

  const { payload, products, categories } = bundle;
  const brandName = payload.config.theme.brand_name?.trim() || payload.shop.name;
  const parsedPage = Number.parseInt(sp.page || "1", 10);

  return (
    <SiteCatalogView
      brandName={brandName}
      products={products}
      categories={categories}
      initialCategory={sp.cat || ""}
      initialQuery={sp.q || ""}
      initialSort={sp.sort || "newest"}
      initialPage={Number.isFinite(parsedPage) && parsedPage > 0 ? parsedPage : 1}
    />
  );
}
