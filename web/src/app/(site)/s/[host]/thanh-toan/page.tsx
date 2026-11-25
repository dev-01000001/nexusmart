import React from "react";
import type { Metadata } from "next";
import { randomUUID } from "node:crypto";
import { notFound } from "next/navigation";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { getSession } from "@/lib/server/session";
import { loadPublicSitePageBundle } from "@/lib/server/sites";
import { buildSiteMetadata } from "@/lib/site/seo";
import type { GwAddress, GwVoucher } from "@/features/commerce/types";
import { loadSiteContext } from "@/features/site/siteContext.server";
import { CheckoutClient } from "./CheckoutClient";

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
    pageTitle: "Thanh toán",
    description: `Thanh toán đơn hàng COD hoặc VietQR tại ${ctx.brandName}.`,
    siteOrigin: ctx.siteOrigin,
    pathname: "/thanh-toan",
  });
}

export default async function SiteCheckoutPage({ params }: PageProps) {
  const { host } = await params;
  const decodedHost = decodeURIComponent(host);
  const bundle = await loadPublicSitePageBundle(decodedHost);
  if (!bundle) notFound();

  const initialIdempotencyKey = randomUUID();
  const shopId = bundle.payload.site.shop_id;
  const brandName = bundle.payload.config.theme.brand_name?.trim() || bundle.payload.shop.name;

  let session = null;
  let initialError: string | null = null;
  try {
    session = await getSession();
  } catch (err) {
    initialError =
      err instanceof GatewayError ? SAFE_MESSAGES[err.kind] : SAFE_MESSAGES.unavailable;
  }

  let initialAddresses: GwAddress[] = [];
  let initialVouchers: GwVoucher[] = [];

  if (session) {
    const [addrRes, vRes] = await Promise.allSettled([
      gw<{ addresses: GwAddress[] }>("/api/v1/users/me/addresses"),
      gw<{ vouchers: GwVoucher[] }>("/api/v1/vouchers", {
        query: { shop_id: shopId, scope: "SHOP" },
      }),
    ]);
    if (addrRes.status === "fulfilled") initialAddresses = addrRes.value.addresses ?? [];
    if (vRes.status === "fulfilled") {
      initialVouchers = (vRes.value.vouchers ?? []).filter(
        (v) => v.scope.toUpperCase() === "SHOP" && v.shop_id === shopId && v.is_active
      );
    }
  }

  const catalogShops = bundle.shopProfile ? [bundle.shopProfile] : [];

  return (
    <CheckoutClient
      brandName={brandName}
      initialIdempotencyKey={initialIdempotencyKey}
      catalogProducts={bundle.products}
      catalogShops={catalogShops}
      initialAddresses={initialAddresses}
      initialVouchers={initialVouchers}
      initialError={initialError}
      serverNowMs={Date.now()}
    />
  );
}
