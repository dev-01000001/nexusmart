import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { requireRequestSite } from "@/lib/server/sites";
import { executeSiteOrderLookup } from "@/lib/server/storefront";
import type { PublicOrderSummary } from "@/lib/a8-types";

export const dynamic = "force-dynamic";

export const POST = apiRoute(async (req) => {
  const site = await requireRequestSite(req);
  const body = await readJson<{ order_code?: unknown; phone?: unknown }>(req);
  const orderCode = typeof body?.order_code === "string" ? body.order_code : "";
  const phone = typeof body?.phone === "string" ? body.phone : "";

  const safeResponse = await executeSiteOrderLookup({
    orderCode,
    phone,
    storefrontShopId: site.shopId,
    postLookup: (lookupBody) =>
      gw<{ order?: PublicOrderSummary | null }>("/api/v1/orders/lookup", {
        method: "POST",
        auth: "none",
        body: lookupBody,
      }),
  });

  return Response.json(safeResponse, { headers: { "Cache-Control": "no-store" } });
});
