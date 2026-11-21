import React from "react";
import { headers } from "next/headers";
import { loadSellerSiteDomainsServer } from "@/lib/server/domains";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import {
  loadCategories,
  loadSellerProducts,
  requireSellerPage,
} from "@/features/seller/sellerServer";
import {
  loadPlatformTemplatesServer,
  loadSellerSiteServer,
  loadSellerSiteVersionsServer,
} from "@/features/seller/site/siteServer";
import {
  SellerSiteEditorClient,
  type EditorTab,
} from "@/features/seller/site/SellerSiteEditorClient";

export const dynamic = "force-dynamic";

const ALLOWED_TABS = new Set<EditorTab>([
  "templates",
  "theme",
  "sections",
  "code",
  "domains",
  "versions",
]);

interface SellerSitePageProps {
  searchParams?: Promise<{ tab?: string | string[] }>;
}

export default async function SellerSitePage({ searchParams }: SellerSitePageProps) {
  await requireSellerPage();

  const [siteRes, versionsRes, domainsRes, templatesRes, productsRes, categoriesRes, h, sp] =
    await Promise.all([
      loadSellerSiteServer(),
      loadSellerSiteVersionsServer(50),
      loadSellerSiteDomainsServer(),
      loadPlatformTemplatesServer(),
      loadSellerProducts(100),
      loadCategories(),
      headers(),
      searchParams ?? Promise.resolve<{ tab?: string | string[] }>({}),
    ]);

  const requestHost = resolveRequestHost(h, "");
  const requestProto = resolveRequestProto(h, `http://${requestHost || "localhost"}`);
  const rawTab = Array.isArray(sp.tab) ? sp.tab[0] : sp.tab;
  const initialTab: EditorTab =
    rawTab && ALLOWED_TABS.has(rawTab as EditorTab) ? (rawTab as EditorTab) : "templates";

  return (
    <SellerSiteEditorClient
      initialSite={siteRes.site}
      initialShop={siteRes.shop}
      initialVersions={versionsRes.versions}
      initialDomains={domainsRes.domains}
      initialTemplates={templatesRes.templates}
      domainsLoadError={domainsRes.error}
      initialTab={initialTab}
      products={productsRes.data?.items ?? []}
      categories={categoriesRes.data ?? []}
      rootDomain={siteRes.rootDomain}
      requestHost={requestHost}
      requestProto={requestProto}
      loadError={siteRes.error}
    />
  );
}
