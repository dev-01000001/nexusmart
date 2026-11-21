import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import { scopeCartToShop } from "@/lib/server/storefront";
import type { GwCart } from "@/features/commerce/types";

const NO_STORE = { "Cache-Control": "no-store" };

/** The buyer's cart lines of THIS site's shop only (shop derived from Host). */
export const GET = apiRoute(async (req) => {
  const site = await requireRequestSite(req);
  await assertSession();
  const data = await gw<{ cart: GwCart }>("/api/v1/cart", { query: { shop_id: site.shopId } });
  return Response.json({ cart: scopeCartToShop(data.cart, site.shopId) }, { headers: NO_STORE });
});

/** Clear this site's lines (all, or the listed `sku_ids`). Lines of other sites are untouched. */
export const DELETE = apiRoute(async (req) => {
  const site = await requireRequestSite(req);
  await assertSession();

  let skuIds: string[] = [];
  const raw = await req.text();
  if (raw.trim()) {
    let parsed: unknown;
    try {
      parsed = JSON.parse(raw);
    } catch {
      throw new GatewayError("invalid", "INVALID_JSON");
    }
    const list = (parsed as { sku_ids?: unknown } | null)?.sku_ids;
    if (Array.isArray(list)) {
      skuIds = list
        .filter((x): x is string => typeof x === "string" && x.trim().length > 0)
        .map((x) => x.trim());
    }
  }

  await gw<{ ok: boolean }>("/api/v1/cart", {
    method: "DELETE",
    body: skuIds.length > 0 ? { sku_ids: skuIds, shop_id: site.shopId } : { shop_id: site.shopId },
  });
  const fresh = await gw<{ cart: GwCart }>("/api/v1/cart", { query: { shop_id: site.shopId } });
  return Response.json(
    { ok: true, cart: scopeCartToShop(fresh.cart, site.shopId) },
    { headers: NO_STORE }
  );
});
