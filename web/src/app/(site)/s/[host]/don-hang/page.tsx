import React from "react";
import type { Metadata } from "next";
import { notFound, redirect } from "next/navigation";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { loginPath, requireSession } from "@/lib/server/session";
import { loadPublicSitePageBundle } from "@/lib/server/sites";
import { buildSiteMetadata } from "@/lib/site/seo";
import { fetchBuyerOrders } from "@/features/account/orders.server";
import { OrdersClientView } from "@/features/account/OrdersClientView";
import { loadShopSkuAttributesMap, loadSiteContext } from "@/features/site/siteContext.server";

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
    pageTitle: "Đơn hàng của tôi",
    siteOrigin: ctx.siteOrigin,
    pathname: "/don-hang",
  });
}

export default async function SiteOrdersPage({ params }: PageProps) {
  const { host } = await params;
  const decodedHost = decodeURIComponent(host);
  const bundle = await loadPublicSitePageBundle(decodedHost);
  if (!bundle) notFound();

  await requireSession("/don-hang");

  const brandName = bundle.payload.config.theme.brand_name?.trim() || bundle.payload.shop.name;
  const shopId = bundle.payload.site.shop_id;

  try {
    const skuAttributesById = await loadShopSkuAttributesMap(shopId);
    const res = await fetchBuyerOrders({
      storefrontShopId: shopId,
      page: 1,
      pageSize: 50,
      siteProducts: bundle.products,
      siteShop: bundle.shopProfile,
      skuAttributesById,
    });
    return (
      <OrdersClientView
        brandName={brandName}
        initialOrders={res.items}
        suggestedProducts={res.suggestedProducts}
      />
    );
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "unauthenticated") {
      redirect(loginPath("/don-hang"));
    }
    const msg = err instanceof GatewayError ? SAFE_MESSAGES[err.kind] : SAFE_MESSAGES.unavailable;
    return (
      <OrdersClientView
        brandName={brandName}
        initialOrders={[]}
        suggestedProducts={bundle.products.slice(0, 4)}
        initialError={msg}
      />
    );
  }
}
