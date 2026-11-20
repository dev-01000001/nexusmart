import type { MetadataRoute } from "next";
import { headers } from "next/headers";
import { APP_ORIGINS, getSiteRootDomain } from "@/lib/server/config";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { fetchPublishedSiteByHost } from "@/lib/server/sites";
import { classifyHost, deriveMarketplaceOrigin, deriveSiteOrigin } from "@/lib/site/host";

export const dynamic = "force-dynamic";

export default async function robots(): Promise<MetadataRoute.Robots> {
  const h = await headers();
  const rawHost = resolveRequestHost(h, "localhost");
  const proto = resolveRequestProto(h, "http://localhost");
  const rootDomain = getSiteRootDomain();
  const cls = classifyHost(rawHost, rootDomain);

  if (cls.kind === "site") {
    const payload = await fetchPublishedSiteByHost(cls.hostname);
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
    return {
      rules: [
        {
          userAgent: "*",
          allow: "/",
          disallow: ["/api/", "/seller/", "/admin/", "/s/"],
        },
      ],
      sitemap: `${siteOrigin}/sitemap.xml`,
      host: siteOrigin,
    };
  }

  const marketplaceOrigin = deriveMarketplaceOrigin({
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
        disallow: [
          "/api/",
          "/seller/",
          "/admin/",
          "/s/",
          "/tai-khoan/",
          "/don-hang/",
          "/gio-hang/",
          "/thanh-toan/",
        ],
      },
    ],
    sitemap: `${marketplaceOrigin}/sitemap.xml`,
    host: marketplaceOrigin,
  };
}
