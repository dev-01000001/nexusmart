import type { MetadataRoute } from "next";
import { headers } from "next/headers";
import { APP_ORIGINS, getSiteRootDomain } from "@/lib/server/config";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { fetchPublishedSiteByHost } from "@/lib/server/sites";
import {
  classifyHost,
  deriveMarketplaceOrigin,
  deriveSiteOrigin,
  isTenantSiteHost,
} from "@/lib/site/host";

export const dynamic = "force-dynamic";

export default async function robots(): Promise<MetadataRoute.Robots> {
  const h = await headers();
  const rawHost = resolveRequestHost(h, "localhost");
  const proto = resolveRequestProto(h, "http://localhost");
  const rootDomain = getSiteRootDomain();
  const cls = classifyHost(rawHost, rootDomain);

  if (cls.kind === "unknown") {
    return {
      rules: [{ userAgent: "*", disallow: "/" }],
    };
  }

  if (isTenantSiteHost(cls)) {
    const buildSiteRobots = (siteOrigin: string): MetadataRoute.Robots => ({
      rules: [
        {
          userAgent: "*",
          allow: "/",
          disallow: [
            "/api/",
            "/seller/",
            "/admin/",
            "/s/",
            "/gio-hang",
            "/thanh-toan",
            "/tai-khoan",
            "/don-hang",
          ],
        },
      ],
      sitemap: `${siteOrigin}/sitemap.xml`,
      host: siteOrigin,
    });

    let payload;
    try {
      payload = await fetchPublishedSiteByHost(cls.hostname);
    } catch {
      // Transient upstream error (429/5xx/timeout): never emit `Disallow: /` for the whole site.
      const siteOrigin = deriveSiteOrigin({
        siteHost: cls.hostname,
        rawHost,
        proto,
      });
      return buildSiteRobots(siteOrigin);
    }
    if (!payload) {
      return {
        rules: [{ userAgent: "*", disallow: "/" }],
      };
    }
    const siteOrigin = deriveSiteOrigin({
      siteHost: payload.site.primary_host || cls.hostname,
      rawHost,
      proto,
    });
    return buildSiteRobots(siteOrigin);
  }

  const platformOrigin = deriveMarketplaceOrigin({
    rawHost,
    proto,
    rootDomain,
    appOrigin: APP_ORIGINS[0],
  });

  return {
    rules: [
      {
        userAgent: "*",
        allow: "/",
        disallow: ["/api/", "/seller/", "/admin/", "/s/"],
      },
    ],
    sitemap: `${platformOrigin}/sitemap.xml`,
    host: platformOrigin,
  };
}
