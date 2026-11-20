import type { MetadataRoute } from "next";
import { headers } from "next/headers";
import { loadCatalog } from "@/lib/catalog";
import { APP_ORIGINS, getSiteRootDomain } from "@/lib/server/config";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { loadPublicSitePageBundle } from "@/lib/server/sites";
import { classifyHost, deriveMarketplaceOrigin, deriveSiteOrigin } from "@/lib/site/host";

export const dynamic = "force-dynamic";

export default async function sitemap(): Promise<MetadataRoute.Sitemap> {
  const h = await headers();
  const rawHost = resolveRequestHost(h, "localhost");
  const proto = resolveRequestProto(h, "http://localhost");
  const rootDomain = getSiteRootDomain();
  const cls = classifyHost(rawHost, rootDomain);
  const now = new Date();

  if (cls.kind === "site") {
    const bundle = await loadPublicSitePageBundle(cls.hostname);
    if (!bundle) return [];

    const siteOrigin = deriveSiteOrigin({
      siteHost: bundle.payload.site.primary_host || cls.hostname,
      rawHost,
      proto,
    });
    const lastModified = bundle.payload.site.published_at
      ? new Date(bundle.payload.site.published_at)
      : now;

    const entries: MetadataRoute.Sitemap = [
      { url: `${siteOrigin}/`, lastModified, changeFrequency: "daily", priority: 1 },
      { url: `${siteOrigin}/san-pham`, lastModified, changeFrequency: "daily", priority: 0.9 },
      { url: `${siteOrigin}/chinh-sach`, lastModified, changeFrequency: "monthly", priority: 0.5 },
      { url: `${siteOrigin}/gioi-thieu`, lastModified, changeFrequency: "monthly", priority: 0.6 },
    ];

    for (const product of bundle.products) {
      entries.push({
        url: `${siteOrigin}/san-pham/${encodeURIComponent(product.slug)}`,
        lastModified,
        changeFrequency: "daily",
        priority: 0.8,
      });
    }
    return entries;
  }

  const marketplaceOrigin = deriveMarketplaceOrigin({
    rawHost,
    proto,
    rootDomain,
    appOrigin: APP_ORIGINS[0],
  });

  const entries: MetadataRoute.Sitemap = [
    { url: `${marketplaceOrigin}/`, lastModified: now, changeFrequency: "daily", priority: 1 },
    {
      url: `${marketplaceOrigin}/danh-muc`,
      lastModified: now,
      changeFrequency: "daily",
      priority: 0.9,
    },
    {
      url: `${marketplaceOrigin}/flash-sale`,
      lastModified: now,
      changeFrequency: "hourly",
      priority: 0.85,
    },
    {
      url: `${marketplaceOrigin}/voucher`,
      lastModified: now,
      changeFrequency: "daily",
      priority: 0.8,
    },
    {
      url: `${marketplaceOrigin}/bo-suu-tap`,
      lastModified: now,
      changeFrequency: "weekly",
      priority: 0.7,
    },
    {
      url: `${marketplaceOrigin}/ve-nexusmart`,
      lastModified: now,
      changeFrequency: "monthly",
      priority: 0.5,
    },
  ];

  const catalog = await loadCatalog().catch(() => ({ products: [], shops: [] }));
  for (const product of catalog.products) {
    entries.push({
      url: `${marketplaceOrigin}/san-pham/${encodeURIComponent(product.slug)}`,
      lastModified: now,
      changeFrequency: "daily",
      priority: 0.8,
    });
  }
  for (const shop of catalog.shops) {
    entries.push({
      url: `${marketplaceOrigin}/gian-hang/${encodeURIComponent(shop.slug)}`,
      lastModified: now,
      changeFrequency: "weekly",
      priority: 0.7,
    });
  }

  return entries;
}
