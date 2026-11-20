import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import type { GwCart } from "@/features/commerce/types";

export const GET = apiRoute(async () => {
  await assertSession();
  const data = await gw<{ cart: GwCart }>("/api/v1/cart");
  return Response.json(data, { headers: { "Cache-Control": "no-store" } });
});

export const DELETE = apiRoute(async (req) => {
  const session = await assertSession();
  let skuIds: string[] | undefined;
  const raw = await req.text();
  if (raw.trim()) {
    let parsed: unknown;
    try {
      parsed = JSON.parse(raw);
    } catch {
      throw new GatewayError("invalid", "INVALID_JSON");
    }
    if (parsed && typeof parsed === "object" && "sku_ids" in parsed) {
      const list = (parsed as { sku_ids?: unknown }).sku_ids;
      if (Array.isArray(list)) {
        skuIds = list.filter((x): x is string => typeof x === "string" && x.trim().length > 0);
      }
    }
  }

  await gw<{ ok: boolean }>("/api/v1/cart", {
    method: "DELETE",
    body: skuIds && skuIds.length > 0 ? { sku_ids: skuIds } : undefined,
  });

  if (skuIds && skuIds.length > 0) {
    const fresh = await gw<{ cart: GwCart }>("/api/v1/cart");
    return Response.json(
      { ok: true, cart: fresh.cart },
      { headers: { "Cache-Control": "no-store" } }
    );
  }

  const emptyCart: GwCart = {
    user_id: session.user.id,
    shop_groups: [],
    total_items: 0,
    total_vnd: 0,
  };
  return Response.json({ ok: true, cart: emptyCart }, { headers: { "Cache-Control": "no-store" } });
});
