import React from "react";
import type { Metadata } from "next";
import { notFound, redirect } from "next/navigation";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { loginPath, requireSession } from "@/lib/server/session";
import { loadPublicSitePageBundle } from "@/lib/server/sites";
import { buildSiteMetadata } from "@/lib/site/seo";
import { fetchAccountPageData } from "@/features/account/account.server";
import { AccountClientView } from "@/features/account/AccountClientView";
import { loadSiteContext } from "@/features/site/siteContext.server";

export const dynamic = "force-dynamic";

interface PageProps {
  params: Promise<{ host: string }>;
}

export async function generateMetadata({ params }: PageProps): Promise<Metadata> {
  const { host } = await params;
  const ctx = await loadSiteContext(host);
  if (!ctx) {
    return {
      title: "Không tìm thấy cửa hàng",
      robots: { index: false, follow: false },
    };
  }
  return buildSiteMetadata({
    config: ctx.payload.config,
    pageTitle: "Tài khoản",
    siteOrigin: ctx.siteOrigin,
    pathname: "/tai-khoan",
  });
}

export default async function SiteAccountPage({ params }: PageProps) {
  const { host } = await params;
  const decodedHost = decodeURIComponent(host);
  const bundle = await loadPublicSitePageBundle(decodedHost);
  if (!bundle) notFound();

  const session = await requireSession("/tai-khoan");
  const brandName = bundle.payload.config.theme.brand_name?.trim() || bundle.payload.shop.name;
  const shopId = bundle.payload.site.shop_id;

  try {
    const data = await fetchAccountPageData(session, {
      shopId,
      siteProducts: bundle.products,
      siteShop: bundle.shopProfile,
    });
    return (
      <AccountClientView
        brandName={brandName}
        initialProfile={data.profile}
        initialAddresses={data.addresses}
        initialOrders={data.orders}
        totalOrders={data.totalOrders}
        initialVouchers={data.vouchers}
        provinces={data.provinces}
        initialWards={data.initialWards}
        initialProvinceCode={data.initialProvinceCode}
        catalogProducts={data.catalogProducts}
        suggestedProducts={data.suggestedProducts}
      />
    );
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "unauthenticated") {
      redirect(loginPath("/tai-khoan"));
    }
    const msg = err instanceof GatewayError ? SAFE_MESSAGES[err.kind] : SAFE_MESSAGES.unavailable;
    return (
      <AccountClientView
        brandName={brandName}
        initialProfile={{
          id: session.user.id,
          fullName: session.user.fullName,
          email: session.user.email,
          phone: session.user.phone,
          role: session.user.role,
          shopId: session.user.shopId,
          avatarUrl: session.user.avatarUrl,
          phoneVerified: session.user.phoneVerified,
          memberSince: "",
          coins: 0,
        }}
        initialAddresses={[]}
        initialOrders={[]}
        totalOrders={0}
        initialVouchers={[]}
        provinces={[]}
        initialWards={[]}
        initialProvinceCode="79"
        catalogProducts={bundle.products}
        suggestedProducts={bundle.products.slice(0, 4)}
        initialError={msg}
      />
    );
  }
}
