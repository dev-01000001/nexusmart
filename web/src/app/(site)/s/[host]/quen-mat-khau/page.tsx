import React from "react";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { loadPublicSitePageBundle } from "@/lib/server/sites";
import { buildSiteMetadata } from "@/lib/site/seo";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { ForgotPasswordForm } from "@/features/account/AuthForms";
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
    pageTitle: "Quên mật khẩu",
    description: `Khôi phục mật khẩu tài khoản mua hàng tại ${ctx.brandName}.`,
    siteOrigin: ctx.siteOrigin,
    pathname: "/quen-mat-khau",
  });
}

export default async function SiteForgotPasswordPage({ params }: PageProps) {
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
      title="Quên mật khẩu"
      subtitle={`Nhập email đã đăng ký tại ${brandName} để nhận liên kết đặt lại mật khẩu.`}
    >
      <ForgotPasswordForm loginHref="/dang-nhap" registerHref="/dang-ky" />
    </SiteAuthFrame>
  );
}
