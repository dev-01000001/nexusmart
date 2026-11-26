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

function resolveTabQuery(rawTab: string | undefined): {
  tab: EditorTab;
  customizeSection: "theme" | "sections";
} {
  switch ((rawTab ?? "").trim().toLowerCase()) {
    case "customize":
    case "theme":
      return { tab: "customize", customizeSection: "theme" };
    case "sections":
      return { tab: "customize", customizeSection: "sections" };
    case "code":
    case "json":
      return { tab: "code", customizeSection: "theme" };
    case "domains":
    case "domain":
      return { tab: "domains", customizeSection: "theme" };
    case "versions":
      return { tab: "versions", customizeSection: "theme" };
    case "templates":
    default:
      return { tab: "templates", customizeSection: "theme" };
  }
}

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
  const { tab: initialTab, customizeSection } = resolveTabQuery(rawTab);

  return (
    <SellerSiteEditorClient
      initialSite={siteRes.site}
      initialShop={siteRes.shop}
      initialVersions={versionsRes.versions}
      initialDomains={domainsRes.domains}
      initialTemplates={templatesRes.templates}
      domainsLoadError={domainsRes.error}
      initialTab={initialTab}
      initialCustomizeSection={customizeSection}
      products={productsRes.data?.items ?? []}
      categories={categoriesRes.data ?? []}
      rootDomain={siteRes.rootDomain}
      requestHost={requestHost}
      requestProto={requestProto}
      loadError={siteRes.error}
    />
  );
}
