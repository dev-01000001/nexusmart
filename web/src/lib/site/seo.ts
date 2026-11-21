// Pure SEO helpers for multi-tenant subdomain sites (noindex thin-content rule,
// site-own canonical URL on site PDPs, and shop-branded OpenGraph metadata).
import type { Metadata } from "next";
import type { SiteConfig, SiteSection } from "./types";

function sectionHasSubstantiveContent(section: SiteSection): boolean {
  if (!section.visible) return false;
  switch (section.type) {
    case "hero":
      return section.props.title.trim().length > 0;
    case "featured_products":
      return section.props.title.trim().length > 0;
    case "collection":
      return section.props.title.trim().length > 0;
    case "text":
      return section.props.title.trim().length > 0 || section.props.body.trim().length > 0;
    case "banner":
      return section.props.title.trim().length > 0;
    case "voucher_strip":
      return section.props.title.trim().length > 0;
    case "policies":
      return (
        section.props.items.some(
          (item) => item.heading.trim().length > 0 || item.detail.trim().length > 0
        ) || section.props.title.trim().length > 0
      );
  }
}

/**
 * Thin/empty content rule:
 * A site with zero visible content sections must emit `noindex` so search engines do not
 * index empty/boilerplate storefronts.
 */
export function hasSubstantiveSiteContent(config: SiteConfig | null | undefined): boolean {
  if (!config || !Array.isArray(config.sections)) return false;
  return config.sections.some(sectionHasSubstantiveContent);
}

export function buildSiteRobotsMeta(config: SiteConfig | null | undefined): {
  index: boolean;
  follow: boolean;
} {
  return hasSubstantiveSiteContent(config)
    ? { index: true, follow: true }
    : { index: false, follow: true };
}

export function buildSiteMetadata(opts: {
  config: SiteConfig;
  pageTitle?: string;
  description?: string;
  canonicalUrl?: string;
  siteOrigin?: string;
  pathname?: string;
}): Metadata {
  const brandName = opts.config.theme.brand_name.trim() || "Cửa hàng trên NexusMart";
  const fullTitle = opts.pageTitle ? `${opts.pageTitle} · ${brandName}` : brandName;
  const description =
    opts.description?.trim() ||
    opts.config.theme.tagline?.trim() ||
    `${brandName} — Cửa hàng chính hãng vận hành trên NexusMart.`;
  const robots = buildSiteRobotsMeta(opts.config);
  const ogUrl =
    opts.siteOrigin && opts.pathname
      ? `${opts.siteOrigin.replace(/\/+$/, "")}${opts.pathname}`
      : opts.siteOrigin || undefined;

  const metadata: Metadata = {
    title: fullTitle,
    description,
    robots,
    openGraph: {
      title: fullTitle,
      description,
      siteName: brandName,
      type: "website",
      ...(ogUrl ? { url: ogUrl } : {}),
    },
  };

  if (opts.canonicalUrl) {
    metadata.alternates = { canonical: opts.canonicalUrl };
  }

  return metadata;
}

/**
 * Site PDP metadata: `alternates.canonical` is ALWAYS the product's own URL on this site
 * (`<siteOrigin>/san-pham/<slug>`), since there is no marketplace PDP.
 */
export function buildSiteProductMetadata(opts: {
  config: SiteConfig;
  productName: string;
  productSlug: string;
  productDescription?: string;
  siteOrigin: string;
}): Metadata {
  const pathname = `/san-pham/${encodeURIComponent(opts.productSlug)}`;
  const cleanOrigin = opts.siteOrigin.trim().replace(/\/+$/, "");
  const canonicalUrl = cleanOrigin ? `${cleanOrigin}${pathname}` : pathname;

  return buildSiteMetadata({
    config: opts.config,
    pageTitle: opts.productName,
    description: opts.productDescription,
    canonicalUrl,
    siteOrigin: cleanOrigin || undefined,
    pathname,
  });
}
