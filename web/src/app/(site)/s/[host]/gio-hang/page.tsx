import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { buildSiteMetadata } from "@/lib/site/seo";
import {
  loadSiteContext,
  loadSiteTemplateBundle,
  loadSiteVouchers,
} from "@/features/site/siteContext.server";
import { CartClient } from "./CartClient";

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
    pageTitle: "Giỏ hàng",
    description: `Giỏ hàng trực tuyến tại ${ctx.brandName}.`,
    siteOrigin: ctx.siteOrigin,
    pathname: "/gio-hang",
  });
}

export default async function SiteCartPage({ params }: PageProps) {
  const { host } = await params;
  const [bundle, voucherData] = await Promise.all([
    loadSiteTemplateBundle(host),
    loadSiteVouchers(host),
  ]);
  if (!bundle) notFound();

  const brandName = bundle.templateConfig.theme.brand_name?.trim() || bundle.payload.shop.name;
  const catalogShops = bundle.shopProfile ? [bundle.shopProfile] : [];

  return (
    <CartClient
      brandName={brandName}
      catalogProducts={bundle.products}
      catalogShops={catalogShops}
      initialVouchers={voucherData?.rawVouchers ?? []}
      cartConfig={bundle.templateConfig.pages.cart}
      templateConfig={bundle.templateConfig}
      shop={bundle.payload.shop}
      vouchers={bundle.vouchers}
      serverNowMs={Date.now()}
    />
  );
}
