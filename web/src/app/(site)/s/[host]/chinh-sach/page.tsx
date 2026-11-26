import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { buildSiteMetadata } from "@/lib/site/seo";
import { SiteInfoView } from "@/features/site/SiteInfoView";
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
    pageTitle: "Chính sách bán hàng",
    description: `Chính sách giao hàng, đổi trả và bảo hành của ${ctx.brandName}.`,
    siteOrigin: ctx.siteOrigin,
    pathname: "/chinh-sach",
  });
}

export default async function SubdomainSitePoliciesPage({ params }: PageProps) {
  const { host } = await params;
  const bundle = await loadSiteTemplateBundle(host);
  if (!bundle) notFound();

  return (
    <SiteInfoView
      mode="policies"
      templateConfig={bundle.templateConfig}
      shop={bundle.payload.shop}
      shopProfile={bundle.shopProfile}
      products={bundle.products}
      vouchers={bundle.vouchers}
    />
  );
}
