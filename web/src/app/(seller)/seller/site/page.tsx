import React from "react";
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

  const [siteRes, versionsRes, productsRes, categoriesRes] = await Promise.all([
    loadSellerSiteServer(),
    loadSellerSiteVersionsServer(50),
    loadSellerProducts(100),
    loadCategories(),
  ]);

  return (
    <SellerSiteEditorClient
      initialSite={siteRes.site}
      initialShop={siteRes.shop}
      initialVersions={versionsRes.versions}
      products={productsRes.data?.items ?? []}
      categories={categoriesRes.data ?? []}
      rootDomain={siteRes.rootDomain}
      loadError={siteRes.error}
    />
  );
}
