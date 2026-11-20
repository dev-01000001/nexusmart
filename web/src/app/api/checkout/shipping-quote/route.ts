import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import type { GwCarrierQuote } from "@/features/commerce/types";

interface ShopQuoteInput {
  shop_id: string;
  from_province_code?: string;
  from_ward_code?: string;
  weight_grams?: number;
  cod_amount_vnd?: number;
}

interface ShippingQuoteBody {
  to_province_code?: string;
  to_ward_code?: string;
  shops?: ShopQuoteInput[];
}

export const POST = apiRoute(async (req) => {
  await assertSession();
  const body = await readJson<ShippingQuoteBody>(req);
  const toProvinceCode = (body.to_province_code ?? "").trim();
  const toWardCode = (body.to_ward_code ?? "").trim();
  const shops = Array.isArray(body.shops) ? body.shops : [];

  if (!toProvinceCode || shops.length === 0) {
    throw new GatewayError("invalid", "INVALID_ARGUMENT");
  }

  const entries = await Promise.all(
    shops.map(async (s) => {
      const shopId = (s.shop_id ?? "").trim();
      const weightGrams = Math.max(100, Math.trunc(Number(s.weight_grams ?? 500) || 500));
      const codAmount = Math.max(0, Math.trunc(Number(s.cod_amount_vnd ?? 0) || 0));
      const data = await gw<{ quotes: GwCarrierQuote[] }>("/api/v1/shipping/calculate-fee", {
        method: "POST",
        body: {
          shop_id: shopId,
          from_province_code: (s.from_province_code ?? "").trim() || toProvinceCode,
          from_ward_code: (s.from_ward_code ?? "").trim() || toWardCode,
          to_province_code: toProvinceCode,
          to_ward_code: toWardCode,
          weight_grams: weightGrams,
          cod_amount_vnd: codAmount,
        },
      });
      return [shopId, data.quotes ?? []] as const;
    })
  );

  const quotesByShop: Record<string, GwCarrierQuote[]> = {};
  for (const [shopId, quotes] of entries) {
    quotesByShop[shopId] = quotes;
  }

  return Response.json(
    { quotes_by_shop: quotesByShop },
    { headers: { "Cache-Control": "no-store" } }
  );
});
