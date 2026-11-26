import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { loadPublicSitePageBundle } from "@/lib/server/sites";
import { buildSiteMetadata } from "@/lib/site/seo";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { ResetPasswordForm } from "@/features/account/AuthForms";
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
  const base = buildSiteMetadata({
    config: ctx.payload.config,
    pageTitle: "Đặt lại mật khẩu",
    description: `Đặt lại mật khẩu tài khoản tại ${ctx.brandName}.`,
    siteOrigin: ctx.siteOrigin,
    pathname: "/dat-lai-mat-khau",
  });
  return {
    ...base,
    robots: { index: false, follow: false },
  };
}

export default async function SiteResetPasswordPage({ params }: PageProps) {
  const { host } = await params;
  const decodedHost = decodeURIComponent(host);
  const bundle = await loadPublicSitePageBundle(decodedHost);
  if (!bundle) notFound();

  const theme = bundle.payload.config.theme;
  const brandName = theme.brand_name?.trim() || bundle.payload.shop.name;
  const featured = bundle.products[0];
  const fallbackImg =
    sanitizeSafeUrl(featured?.onModelImage) ||
    sanitizeSafeUrl(featured?.image) ||
    "/banners/vietnam-workshop.jpg";

  return (
    <SiteAuthFrame
      brandName={brandName}
      logoUrl={theme.logo_url}
      bannerUrl={theme.banner_url}
      fallbackBannerUrl={fallbackImg}
      tagline={theme.tagline}
      title="Đặt lại mật khẩu"
      subtitle={`Tạo mật khẩu mới cho tài khoản mua hàng tại ${brandName}.`}
    >
      <ResetPasswordForm loginHref="/dang-nhap" forgotHref="/quen-mat-khau" />
    </SiteAuthFrame>
  );
}
