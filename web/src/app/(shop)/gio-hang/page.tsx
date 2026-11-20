import React from "react";
import { headers } from "next/headers";
import { loadCatalogLookups } from "@/lib/catalog";
import { gw } from "@/lib/server/gateway";
import { resolveRequestHost, resolveRequestProto } from "@/lib/server/http";
import { fetchPublishedSiteByShop } from "@/lib/server/sites";
import { deriveSiteOrigin } from "@/lib/site/host";
import type { StorefrontCheckoutContext } from "@/features/commerce/StorefrontBanner";
import type { GwVoucher } from "@/features/commerce/types";
import { CartClient, type CartHandoffSku } from "./CartClient";

export const dynamic = "force-dynamic";

interface CartPageProps {
  searchParams: Promise<{
    shop?: string;
    channel?: string;
    sku?: string;
    qty?: string;
    site?: string;
    flash?: string;
  }>;
}

export default async function CartPage({ searchParams }: CartPageProps) {
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

  const rawSku = (sp.sku ?? "").trim();
  const parsedQty = Number.parseInt(sp.qty ?? "1", 10);
  const handoffSku: CartHandoffSku | null =
    storefrontContext && rawSku
      ? {
          skuId: rawSku,
          qty: Number.isFinite(parsedQty) && parsedQty > 0 ? Math.min(parsedQty, 99) : 1,
          flashSaleItemId: (sp.flash ?? "").trim() || undefined,
        }
      : null;

  const [catalogRes, vouchersRes] = await Promise.allSettled([
    loadCatalogLookups(),
    gw<{ vouchers: GwVoucher[] }>("/api/v1/vouchers", {
      auth: "none",
      query: storefrontContext ? { shop_id: storefrontContext.shopId, scope: "SHOP" } : undefined,
    }),
  ]);

  const catalogProducts = catalogRes.status === "fulfilled" ? catalogRes.value.products : [];
  const catalogShops = catalogRes.status === "fulfilled" ? catalogRes.value.shops : [];
  const rawVouchers = vouchersRes.status === "fulfilled" ? (vouchersRes.value.vouchers ?? []) : [];
  const initialVouchers = storefrontContext
    ? rawVouchers.filter(
        (v) =>
          v.scope.toUpperCase() === "SHOP" && v.shop_id === storefrontContext.shopId && v.is_active
      )
    : rawVouchers;

  return (
    <CartClient
      catalogProducts={catalogProducts}
      catalogShops={catalogShops}
      initialVouchers={initialVouchers}
      storefrontContext={storefrontContext}
      handoffSku={handoffSku}
    />
  );
}
