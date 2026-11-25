import React from "react";
import type { Metadata } from "next";
import { headers } from "next/headers";
import { Button, StateView } from "@/components";
import { loadSellerSiteDomainsServer } from "@/lib/server/domains";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { requireSellerPage } from "@/features/seller/sellerServer";
import common from "@/features/seller/SellerCommon.module.css";
import { loadSellerSiteServer } from "@/features/seller/site/siteServer";
import { SellerSiteDomainsSection } from "@/features/seller/site/SellerSiteDomainsSection";

export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "Tên miền cửa hàng",
};

export default async function SellerSiteDomainsPage() {
  await requireSellerPage();

  const [siteRes, domainsRes, h] = await Promise.all([
    loadSellerSiteServer(),
    loadSellerSiteDomainsServer(),
    headers(),
  ]);

  const requestHost = resolveRequestHost(h, "");
  const requestProto = resolveRequestProto(h, `http://${requestHost || "localhost"}`);

  if (siteRes.error) {
    return (
      <div className={common.page}>
        <header className={common.pageHeader}>
          <div className={common.headerText}>
            <h1 className={common.title}>Tên miền</h1>
          </div>
          <div className={common.headerActions}>
            <Button variant="outline" size="sm" shape="app" href="/seller/site">
              Quay lại Site bán hàng
            </Button>
          </div>
        </header>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải cấu hình tên miền"
          message={siteRes.error}
          actionLabel="Quay lại Site riêng"
          actionHref="/seller/site"
        />
      </div>
    );
  }

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Tên miền</h1>
        </div>
        <div className={common.headerActions}>
          <Button variant="outline" size="sm" shape="app" href="/seller/site">
            Quay lại Site bán hàng
          </Button>
        </div>
      </header>

      <SellerSiteDomainsSection
        site={siteRes.site}
        initialDomains={domainsRes.domains}
        rootDomain={siteRes.rootDomain}
        requestHost={requestHost}
        requestProto={requestProto}
        loadError={domainsRes.error}
      />
    </div>
  );
}
