import type { MetadataRoute } from "next";
import { headers } from "next/headers";
import {
  directoryContextFromHeaders,
  loadAllDirectorySites,
} from "@/features/platform/directory.server";
import { APP_ORIGINS, getSiteRootDomain } from "@/lib/server/config";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { loadPublicSitePageBundle } from "@/lib/server/sites";
import {
  classifyHost,
  deriveMarketplaceOrigin,
  deriveSiteOrigin,
  isTenantSiteHost,
} from "@/lib/site/host";

export const dynamic = "force-dynamic";

export default async function sitemap(): Promise<MetadataRoute.Sitemap> {
  const h = await headers();
  const rawHost = resolveRequestHost(h, "localhost");
  const proto = resolveRequestProto(h, "http://localhost");
  const rootDomain = getSiteRootDomain();
  const cls = classifyHost(rawHost, rootDomain);
  const now = new Date();

  if (cls.kind === "unknown") {
    return [];
  }

  if (isTenantSiteHost(cls)) {
    const buildCoreEntries = (siteOrigin: string, lastModified: Date): MetadataRoute.Sitemap => [
      { url: `${siteOrigin}/`, lastModified, changeFrequency: "daily", priority: 1 },
      { url: `${siteOrigin}/san-pham`, lastModified, changeFrequency: "daily", priority: 0.9 },
      { url: `${siteOrigin}/chinh-sach`, lastModified, changeFrequency: "monthly", priority: 0.5 },
      { url: `${siteOrigin}/gioi-thieu`, lastModified, changeFrequency: "monthly", priority: 0.6 },
    ];

    let bundle;
    try {
      bundle = await loadPublicSitePageBundle(cls.hostname);
    } catch {
      // Transient upstream error (429/5xx/timeout): keep core storefront URLs instead of returning empty/500.
      const siteOrigin = deriveSiteOrigin({
        siteHost: cls.hostname,
        rawHost,
        proto,
      });
      return buildCoreEntries(siteOrigin, now);
    }
    if (!bundle) return [];

    const siteOrigin = deriveSiteOrigin({
      siteHost: bundle.payload.site.primary_host || cls.hostname,
      rawHost,
      proto,
    });
    const lastModified = bundle.payload.site.published_at
      ? new Date(bundle.payload.site.published_at)
      : now;

    const entries = buildCoreEntries(siteOrigin, lastModified);

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

  const platformOrigin = deriveMarketplaceOrigin({
    rawHost,
    proto,
    rootDomain,
    appOrigin: APP_ORIGINS[0],
  });

  const entries: MetadataRoute.Sitemap = [
    { url: `${platformOrigin}/`, lastModified: now, changeFrequency: "daily", priority: 1 },
    {
      url: `${platformOrigin}/ve-nexusmart`,
      lastModified: now,
      changeFrequency: "monthly",
      priority: 0.7,
    },
    {
      url: `${platformOrigin}/tro-giup`,
      lastModified: now,
      changeFrequency: "monthly",
      priority: 0.6,
    },
  ];

  const dirCtx = directoryContextFromHeaders(h, `${proto}://${rawHost}`);
  const sites = await loadAllDirectorySites(20, dirCtx);
  for (const site of sites) {
    entries.push({
      url: `${site.origin}/`,
      lastModified: now,
      changeFrequency: "daily",
      priority: 0.8,
    });
  }

  return entries;
}
