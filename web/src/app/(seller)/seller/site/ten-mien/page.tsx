import React from "react";
import type { Metadata } from "next";
import Link from "next/link";
import { headers } from "next/headers";
import { ErrorState, PageHeader } from "@/components/app";
import { Button } from "@/components/ui/button";
import { loadSellerSiteDomainsServer } from "@/lib/server/domains";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { requireSellerPage } from "@/features/seller/sellerServer";
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
      <div className="mx-auto max-w-[800px] space-y-6">
        <PageHeader
          title="Tên miền"
          backHref="/seller/site"
          subtitle="Quản lý địa chỉ mặc định và tên miền thương hiệu của cửa hàng."
          primaryAction={
            <Button variant="outline" size="sm" asChild>
              <Link href="/seller/site">Quay lại Site bán hàng</Link>
            </Button>
          }
        />
        <ErrorState
          title="Không thể tải cấu hình tên miền"
          description={siteRes.error}
          retryHref="/seller/site"
        />
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-[800px] space-y-6">
      <PageHeader
        title="Tên miền"
        backHref="/seller/site"
        subtitle="Quản lý địa chỉ mặc định và tên miền thương hiệu của cửa hàng."
        primaryAction={
          <Button variant="outline" size="sm" asChild>
            <Link href="/seller/site">Quay lại Site bán hàng</Link>
          </Button>
        }
      />

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
