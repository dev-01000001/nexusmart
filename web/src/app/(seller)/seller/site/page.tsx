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
  loadSellerSiteServer,
  loadSellerSiteVersionsServer,
} from "@/features/seller/site/siteServer";
import { SellerSiteEditorClient } from "@/features/seller/site/SellerSiteEditorClient";

export const dynamic = "force-dynamic";

export default async function SellerSitePage() {
  await requireSellerPage();

  const [siteRes, versionsRes, domainsRes, productsRes, categoriesRes, h] = await Promise.all([
    loadSellerSiteServer(),
    loadSellerSiteVersionsServer(50),
    loadSellerSiteDomainsServer(),
    loadSellerProducts(100),
    loadCategories(),
    headers(),
  ]);

  const requestHost = resolveRequestHost(h, "");
  const requestProto = resolveRequestProto(h, `http://${requestHost || "localhost"}`);

  return (
    <SellerSiteEditorClient
      initialSite={siteRes.site}
      initialShop={siteRes.shop}
      initialVersions={versionsRes.versions}
      initialDomains={domainsRes.domains}
      domainsLoadError={domainsRes.error}
      products={productsRes.data?.items ?? []}
      categories={categoriesRes.data ?? []}
      rootDomain={siteRes.rootDomain}
      requestHost={requestHost}
      requestProto={requestProto}
      loadError={siteRes.error}
    />
  );
}
