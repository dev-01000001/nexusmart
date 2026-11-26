import React from "react";
import Link from "next/link";
import { AuthSplit } from "@/components/app/AuthSplit";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { SITE_PAGE_CONTAINER } from "./siteStatus";

export interface SiteAuthFrameProps {
  brandName: string;
  logoUrl?: string | null;
  bannerUrl?: string | null;
  fallbackBannerUrl?: string | null;
  tagline?: string | null;
  title: string;
  subtitle?: React.ReactNode;
  children: React.ReactNode;
}

export function SiteAuthFrame({
  brandName,
  logoUrl,
  bannerUrl,
  fallbackBannerUrl = "/banners/vietnam-workshop.jpg",
  tagline,
  title,
  subtitle,
  children,
}: SiteAuthFrameProps) {
  const safeLogo = sanitizeSafeUrl(logoUrl);
  const safeBanner =
    sanitizeSafeUrl(bannerUrl) ||
    sanitizeSafeUrl(fallbackBannerUrl) ||
    "/banners/vietnam-workshop.jpg";

  const logoNode = safeLogo ? (
    <span className="flex size-7 items-center justify-center overflow-hidden rounded-md border border-border bg-muted">
      <img src={safeLogo} alt="" width={28} height={28} className="size-full object-cover" />
    </span>
  ) : undefined;

  return (
    <section className={`${SITE_PAGE_CONTAINER} py-6 md:py-10`}>
      <AuthSplit
        variant="embedded"
        brand={{ name: brandName, href: "/", logo: logoNode }}
        title={title}
        subtitle={subtitle}
        aside={{
          imageSrc: safeBanner,
          caption: tagline?.trim() || `Mua sắm trực tiếp tại gian hàng chính hãng ${brandName}.`,
        }}
        footer={
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
            <Link href="/tra-cuu-don-hang" className="transition-colors hover:text-foreground">
              Tra cứu đơn hàng
            </Link>
            <Link href="/chinh-sach" className="transition-colors hover:text-foreground">
              Chính sách cửa hàng
            </Link>
            <Link href="/san-pham" className="transition-colors hover:text-foreground">
              Sản phẩm
            </Link>
          </div>
        }
      >
        {children}
      </AuthSplit>
    </section>
  );
}
