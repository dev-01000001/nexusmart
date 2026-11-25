import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { requireRequestSite } from "@/lib/server/sites";
import type { GatewayActiveShopDiscountsResponse } from "@/lib/gateway-types";

export const dynamic = "force-dynamic";

const MAX_SKU_IDS = 200;

export const GET = apiRoute(async (req) => {
  const site = await requireRequestSite(req);
  const { searchParams } = new URL(req.url);
  const rawSkuIds = (searchParams.get("sku_ids") ?? "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean)
    .slice(0, MAX_SKU_IDS);

  const data = await gw<GatewayActiveShopDiscountsResponse>("/api/v1/shop-discounts/active", {
    auth: "none",
    query: {
      shop_id: site.shopId,
      sku_ids: rawSkuIds.length > 0 ? rawSkuIds.join(",") : undefined,
    },
  });

  const discounts = (Array.isArray(data?.discounts) ? data.discounts : []).filter(
    (d) => !d.shop_id || d.shop_id === site.shopId
  );

  return Response.json({ discounts }, { headers: { "Cache-Control": "no-store" } });
});
