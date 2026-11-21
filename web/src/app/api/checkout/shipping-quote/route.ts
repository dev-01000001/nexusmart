import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { requireRequestSite } from "@/lib/server/sites";
import type { GwCarrierQuote } from "@/features/commerce/types";

interface ShopQuoteInput {
  shop_id?: string;
  from_province_code?: string;
  from_ward_code?: string;
  weight_grams?: number;
  cod_amount_vnd?: number;
}

interface ShippingQuoteBody {
  to_province_code?: string;
  to_ward_code?: string;
  from_province_code?: string;
  from_ward_code?: string;
  weight_grams?: number;
  cod_amount_vnd?: number;
  shops?: ShopQuoteInput[];
}

/**
 * Shipping quotes for THIS site's shop only (derived from Host); quotes for any other shop are
 * rejected. Accepts either flat `{ to_province_code, weight_grams, cod_amount_vnd }` (no shop_id)
 * or the legacy `{ shops: [...] }` shape.
 */
export const POST = apiRoute(async (req) => {
  const site = await requireRequestSite(req);
  await assertSession();
  const body = await readJson<ShippingQuoteBody>(req);
  const toProvinceCode = (body.to_province_code ?? "").trim();
  const toWardCode = (body.to_ward_code ?? "").trim();

  if (!toProvinceCode) {
    throw new GatewayError("invalid", "INVALID_ARGUMENT");
  }

  let input: ShopQuoteInput | undefined;
  if (Array.isArray(body.shops) && body.shops.length > 0) {
    const hasForeign = body.shops.some((s) => {
      const id = (s?.shop_id ?? "").trim();
      return id.length > 0 && id !== site.shopId;
    });
    input =
      body.shops.find((s) => (s?.shop_id ?? "").trim() === site.shopId) ??
      body.shops.find((s) => !(s?.shop_id ?? "").trim());
    if (!input && hasForeign) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }
  } else {
    input = {
      from_province_code: body.from_province_code,
      from_ward_code: body.from_ward_code,
      weight_grams: body.weight_grams,
      cod_amount_vnd: body.cod_amount_vnd,
    };
  }

  if (!input) {
    throw new GatewayError("invalid", "INVALID_ARGUMENT");
  }

  const weightGrams = Math.max(100, Math.trunc(Number(input.weight_grams ?? 500) || 500));
  const codAmount = Math.max(0, Math.trunc(Number(input.cod_amount_vnd ?? 0) || 0));
  const data = await gw<{ quotes: GwCarrierQuote[] }>("/api/v1/shipping/calculate-fee", {
    method: "POST",
    body: {
      shop_id: site.shopId,
      from_province_code: (input.from_province_code ?? "").trim() || toProvinceCode,
      from_ward_code: (input.from_ward_code ?? "").trim() || toWardCode,
      to_province_code: toProvinceCode,
      to_ward_code: toWardCode,
      weight_grams: weightGrams,
      cod_amount_vnd: codAmount,
    },
  });

  const quotes = data.quotes ?? [];
  return Response.json(
    { quotes, quotes_by_shop: { [site.shopId]: quotes } },
    { headers: { "Cache-Control": "no-store" } }
  );
});
