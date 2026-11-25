import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { requireRequestSite } from "@/lib/server/sites";
import { buildSiteShippingQuotePayload, type SiteShippingQuoteBody } from "@/lib/server/storefront";
import type { GwCarrierQuote } from "@/features/commerce/types";

/**
 * Shipping quotes for THIS site's shop only (derived from Host); quotes for any other shop are
 * rejected. Supports both logged-in checkout and guest PDP shipping fee estimation.
 * Never forwards client-supplied `from_*` fields so the gateway always resolves the shop's
 * authoritative pickup address (audit 5e).
 */
export const POST = apiRoute(async (req) => {
  const site = await requireRequestSite(req);
  const body = await readJson<SiteShippingQuoteBody>(req);
  const gatewayPayload = buildSiteShippingQuotePayload(body, site.shopId);

  const data = await gw<{
    quotes?: GwCarrierQuote[];
    cod_supported?: boolean;
    is_estimated?: boolean;
  }>("/api/v1/shipping/calculate-fee", {
    method: "POST",
    auth: "none",
    body: gatewayPayload,
  });

  const quotes = data.quotes ?? [];
  const codSupported =
    typeof data.cod_supported === "boolean"
      ? data.cod_supported
      : quotes.every((q) => q.cod_supported !== false);
  const isEstimated =
    typeof data.is_estimated === "boolean"
      ? data.is_estimated
      : quotes.some((q) => Boolean(q.is_estimated || q.is_fallback));

  return Response.json(
    {
      quotes,
      quotes_by_shop: { [site.shopId]: quotes },
      cod_supported: codSupported,
      is_estimated: isEstimated,
    },
    { headers: { "Cache-Control": "no-store" } }
  );
});
