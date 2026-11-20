import React from "react";
import { randomUUID } from "node:crypto";
import { headers } from "next/headers";
import { loadCatalogLookups } from "@/lib/catalog";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { getSession } from "@/lib/server/session";
import { fetchPublishedSiteByShop } from "@/lib/server/sites";
import { deriveSiteOrigin } from "@/lib/site/host";
import type { StorefrontCheckoutContext } from "@/features/commerce/StorefrontBanner";
import type { GwAddress, GwCoinBalance, GwVoucher } from "@/features/commerce/types";
import { CheckoutClient } from "./CheckoutClient";

export const dynamic = "force-dynamic";

interface CheckoutPageProps {
  searchParams: Promise<{
    shop?: string;
    channel?: string;
    site?: string;
  }>;
}

export default async function CheckoutPage({ searchParams }: CheckoutPageProps) {
  const initialIdempotencyKey = randomUUID();
  const sp = await searchParams;
  const wantsStorefront = (sp.channel ?? "").trim().toLowerCase() === "storefront";
  const requestedShopId = (sp.shop ?? "").trim();

  let storefrontContext: StorefrontCheckoutContext | null = null;
  if (wantsStorefront && requestedShopId) {
    const verified = await fetchPublishedSiteByShop(requestedShopId, { uncached: true });
    if (verified) {
      const h = await headers();
      const rawHost = resolveRequestHost(h, "localhost");
      const proto = resolveRequestProto(h, "http://localhost");
      const siteHost = verified.site.primary_host || (sp.site ?? "").trim().toLowerCase();
      storefrontContext = {
        shopId: verified.site.shop_id,
        shopName: verified.shop.name,
        brandName: verified.config.theme.brand_name?.trim() || verified.shop.name,
        logoUrl: verified.config.theme.logo_url || "",
        accentColor: verified.config.theme.accent_color,
        accentTextColor: verified.config.theme.accent_text_color,
        siteHost,
        siteOrigin: deriveSiteOrigin({ siteHost, rawHost, proto }),
      };
    }
  }

  let session = null;
  let initialError: string | null = null;
  try {
    session = await getSession();
  } catch (err) {
    initialError =
      err instanceof GatewayError ? SAFE_MESSAGES[err.kind] : SAFE_MESSAGES.unavailable;
  }

  const catalogRes = await loadCatalogLookups().catch(() => ({
    products: [],
    shops: [],
    error: null,
  }));

  let initialAddresses: GwAddress[] = [];
  let initialVouchers: GwVoucher[] = [];
  let initialCoinBalance = 0;

  if (session) {
    const [addrRes, vRes, cRes] = await Promise.allSettled([
      gw<{ addresses: GwAddress[] }>("/api/v1/users/me/addresses"),
      gw<{ vouchers: GwVoucher[] }>("/api/v1/vouchers", {
        query: storefrontContext ? { shop_id: storefrontContext.shopId, scope: "SHOP" } : undefined,
      }),
      storefrontContext
        ? Promise.resolve<GwCoinBalance>({
            user_id: session.user.id,
            balance: 0,
            recent_transactions: [],
          })
        : gw<GwCoinBalance>("/api/v1/coins/balance"),
    ]);
    if (addrRes.status === "fulfilled") initialAddresses = addrRes.value.addresses ?? [];
    if (vRes.status === "fulfilled") {
      const rawV = vRes.value.vouchers ?? [];
      initialVouchers = storefrontContext
        ? rawV.filter(
            (v) =>
              v.scope.toUpperCase() === "SHOP" &&
              v.shop_id === storefrontContext.shopId &&
              v.is_active
          )
        : rawV;
    }
    if (cRes.status === "fulfilled") initialCoinBalance = Math.max(0, cRes.value.balance ?? 0);
  }

  return (
    <CheckoutClient
      initialIdempotencyKey={initialIdempotencyKey}
      catalogProducts={catalogRes.products}
      catalogShops={catalogRes.shops}
      initialAddresses={initialAddresses}
      initialVouchers={initialVouchers}
      initialCoinBalance={storefrontContext ? 0 : initialCoinBalance}
      initialError={initialError}
      storefrontContext={storefrontContext}
    />
  );
}
