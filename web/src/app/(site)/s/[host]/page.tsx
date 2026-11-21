import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { buildSiteMetadata } from "@/lib/site/seo";
import { SiteRenderer } from "@/features/site";
import { loadSiteContext, loadSiteTemplateBundle } from "@/features/site/siteContext.server";

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
    siteOrigin: ctx.siteOrigin,
    pathname: "/",
  });
}

export default async function SubdomainSiteHomePage({ params }: PageProps) {
  const { host } = await params;
  const bundle = await loadSiteTemplateBundle(host);
  if (!bundle) notFound();

  const { payload, templateConfig, products, vouchers, categories } = bundle;

  return (
    <SiteRenderer
      config={templateConfig}
      shop={payload.shop}
      products={products}
      vouchers={vouchers}
      categories={categories}
    />
  );
}
