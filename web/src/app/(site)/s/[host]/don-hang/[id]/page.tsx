import React from "react";
import type { Metadata } from "next";
import { notFound, redirect } from "next/navigation";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { loginPath, requireSession } from "@/lib/server/session";
import { loadPublicSitePageBundle } from "@/lib/server/sites";
import { buildSiteMetadata } from "@/lib/site/seo";
import { fetchBuyerOrder } from "@/features/account/orders.server";
import { OrderDetailClientView } from "@/features/account/OrderDetailClientView";
import { loadSiteContext } from "@/features/site/siteContext.server";

export const dynamic = "force-dynamic";

interface PageProps {
  params: Promise<{ host: string; id: string }>;
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
    pageTitle: "Chi tiết đơn hàng",
    siteOrigin: ctx.siteOrigin,
  });
}

export default async function SiteOrderDetailPage({ params }: PageProps) {
  const { host, id } = await params;
  const decodedHost = decodeURIComponent(host);
  const bundle = await loadPublicSitePageBundle(decodedHost);
  if (!bundle) notFound();

  const session = await requireSession(`/don-hang/${encodeURIComponent(id)}`);
  const brandName = bundle.payload.config.theme.brand_name?.trim() || bundle.payload.shop.name;
  const shopId = bundle.payload.site.shop_id;

  try {
    const res = await fetchBuyerOrder(id, session, shopId, {
      siteProducts: bundle.products,
      siteShop: bundle.shopProfile,
    });
    return (
      <OrderDetailClientView
        brandName={brandName}
        orderId={id}
        initialOrder={res.order}
        suggestedProducts={res.suggestedProducts}
        initialShipment={res.shipment}
        initialShipmentEvents={res.shipmentEvents}
        initialReturnShipment={res.returnShipment}
        initialReturnRequest={res.returnRequest}
        initialRefunds={res.refunds}
        initialReviews={res.reviews}
        initialAddresses={res.addresses}
      />
    );
  } catch (err) {
    if (err instanceof GatewayError) {
      if (err.kind === "unauthenticated") {
        redirect(loginPath(`/don-hang/${encodeURIComponent(id)}`));
      }
      if (err.kind === "not_found" || err.kind === "forbidden") {
        notFound();
      }
      return (
        <OrderDetailClientView
          brandName={brandName}
          orderId={id}
          initialOrder={null}
          suggestedProducts={bundle.products.slice(0, 4)}
          initialError={SAFE_MESSAGES[err.kind]}
        />
      );
    }
    return (
      <OrderDetailClientView
        brandName={brandName}
        orderId={id}
        initialOrder={null}
        suggestedProducts={bundle.products.slice(0, 4)}
        initialError={SAFE_MESSAGES.unavailable}
      />
    );
  }
}
