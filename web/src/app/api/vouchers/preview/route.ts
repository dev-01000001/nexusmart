import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { apiRoute, readJson } from "@/lib/server/http";
import { assertSession } from "@/lib/server/session";
import { translatePromotionWarning } from "@/features/commerce/checkoutErrors";
import type { GwCart, GwDiscountBreakdown } from "@/features/commerce/types";

interface PreviewLineInput {
  sku_id: string;
  quantity: number;
  unit_price_vnd: number;
  flash_sale_item_id?: string;
}

interface PreviewShopInput {
  shop_id: string;
  subtotal_vnd: number;
  shipping_fee_vnd: number;
  items: PreviewLineInput[];
}

interface PreviewBody {
  shop_items?: PreviewShopInput[];
  shipping_fees?: Record<string, number>;
  shop_voucher_ids?: Record<string, string | null | undefined>;
  platform_voucher_id?: string | null;
  freeship_voucher_id?: string | null;
  coins_to_use?: number;
  flash_sale_item_by_sku?: Record<string, string>;
}

function cleanVoucherMap(
  raw: Record<string, string | null | undefined> | undefined
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(raw ?? {})) {
    const cleanKey = k.trim();
    const cleanVal = typeof v === "string" ? v.trim() : "";
    if (cleanKey && cleanVal) out[cleanKey] = cleanVal;
  }
  return out;
}

export const POST = apiRoute(async (req) => {
  await assertSession();
  const body = await readJson<PreviewBody>(req);

  let shopItems: PreviewShopInput[] = Array.isArray(body.shop_items) ? body.shop_items : [];
  if (shopItems.length === 0) {
    const { cart } = await gw<{ cart: GwCart }>("/api/v1/cart");
    const fees = body.shipping_fees ?? {};
    const flashBySku = body.flash_sale_item_by_sku ?? {};
    shopItems = (cart?.shop_groups ?? []).map((g) => ({
      shop_id: g.shop_id,
      subtotal_vnd: g.subtotal_vnd,
      shipping_fee_vnd: Math.max(0, Math.trunc(Number(fees[g.shop_id] ?? 0) || 0)),
      items: g.items.map((it) => ({
        sku_id: it.sku_id,
        quantity: it.quantity,
        unit_price_vnd: it.price_vnd,
        flash_sale_item_id: flashBySku[it.sku_id] || "",
      })),
    }));
  }

  if (shopItems.length === 0) {
    const empty: GwDiscountBreakdown = {
      shops: [],
      total_subtotal_vnd: 0,
      total_flash_sale_discount_vnd: 0,
      total_shop_voucher_discount_vnd: 0,
      total_platform_voucher_discount_vnd: 0,
      total_freeship_discount_vnd: 0,
      total_coins_discount_vnd: 0,
      total_discount_vnd: 0,
      platform_cost_vnd: 0,
      shop_cost_vnd: 0,
      total_shipping_fee_vnd: 0,
      final_payable_vnd: 0,
      warnings: [],
    };
    return Response.json(
      { breakdown: empty, warnings_vi: [] },
      { headers: { "Cache-Control": "no-store" } }
    );
  }

  const coinsToUse = Math.max(0, Math.trunc(Number(body.coins_to_use ?? 0) || 0));
  if (!Number.isFinite(coinsToUse) || coinsToUse < 0) {
    throw new GatewayError("invalid", "INVALID_ARGUMENT");
  }

  const data = await gw<{ breakdown: GwDiscountBreakdown }>("/api/v1/promotions/preview", {
    method: "POST",
    body: {
      shop_items: shopItems,
      shop_voucher_ids: cleanVoucherMap(body.shop_voucher_ids),
      platform_voucher_id: (body.platform_voucher_id ?? "").trim(),
      freeship_voucher_id: (body.freeship_voucher_id ?? "").trim(),
      coins_to_use: coinsToUse,
    },
  });

  const warningsVi = (data.breakdown?.warnings ?? []).map(translatePromotionWarning);
  const safeBreakdown: GwDiscountBreakdown = {
    ...data.breakdown,
    warnings: warningsVi,
  };

  return Response.json(
    { breakdown: safeBreakdown, warnings_vi: warningsVi },
    { headers: { "Cache-Control": "no-store" } }
  );
});
