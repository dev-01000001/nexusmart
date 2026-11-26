import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { loadPublicSitePageBundle } from "@/lib/server/sites";
import { buildSiteMetadata } from "@/lib/site/seo";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { RegisterForm } from "@/features/account/AuthForms";
import { SiteAuthFrame } from "@/features/account/ui/SiteAuthFrame";
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
    pageTitle: "Tạo tài khoản",
    description: `Đăng ký tài khoản mua sắm trực tiếp tại ${ctx.brandName}.`,
    siteOrigin: ctx.siteOrigin,
    pathname: "/dang-ky",
  });
}

export default async function SiteRegisterPage({ params }: PageProps) {
  const { host } = await params;
  const decodedHost = decodeURIComponent(host);
  const bundle = await loadPublicSitePageBundle(decodedHost);
  if (!bundle) notFound();

  const theme = bundle.payload.config.theme;
  const brandName = theme.brand_name?.trim() || bundle.payload.shop.name;
  const featured = bundle.products[1] ?? bundle.products[0];
  const fallbackImg =
    sanitizeSafeUrl(featured?.onModelImage) ||
    sanitizeSafeUrl(featured?.image) ||
    "/banners/fabric-closeup.jpg";

  return (
    <SiteAuthFrame
      brandName={brandName}
      logoUrl={theme.logo_url}
      bannerUrl={theme.banner_url}
      fallbackBannerUrl={fallbackImg}
      tagline={theme.tagline}
      title="Tạo tài khoản"
      subtitle={`Xác thực email một lần tại ${brandName}.`}
    >
      <RegisterForm variant="site" loginHref="/dang-nhap" />
    </SiteAuthFrame>
  );
}
