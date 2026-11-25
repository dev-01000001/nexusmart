import { GatewayError } from "@/lib/server/errors";
import { apiRoute } from "@/lib/server/http";
import { gw } from "@/lib/server/gateway";
import { assertSellerSession } from "@/features/seller/sellerServer";
import type { GwSellerShop } from "@/features/seller/sellerApiTypes";
import {
  getSiteRootDomain,
  normalizeGwSite,
  type RawGwSite,
} from "@/features/seller/site/siteServer";

interface GatewaySellerSiteResponse {
  site?: RawGwSite | null;
  shop?: GwSellerShop | null;
  root_domain?: string;
}

export const GET = apiRoute(async () => {
  await assertSellerSession();
  const rootDomain = getSiteRootDomain();

  let site: ReturnType<typeof normalizeGwSite> = null;
  let shop: GwSellerShop | null = null;
  let resolvedRoot = rootDomain;

  try {
    const data = await gw<GatewaySellerSiteResponse>("/api/v1/seller/site");
    site = normalizeGwSite(data.site);
    shop = data.shop ?? null;
    if (data.root_domain?.trim()) {
      resolvedRoot = data.root_domain.trim().toLowerCase();
    }
  } catch (err) {
    if (!(err instanceof GatewayError && err.kind === "not_found")) {
      throw err;
    }
  }

  if (!shop) {
    try {
      const shopRes = await gw<{ shop: GwSellerShop | null }>("/api/v1/seller/shop");
      shop = shopRes.shop ?? null;
    } catch {
      shop = null;
    }
  }

  return Response.json(
    {
      site,
      shop,
      root_domain: resolvedRoot,
    },
    { headers: { "Cache-Control": "no-store" } }
  );
});
