// Pure tenant-scoping rules for the buyer BFF on a site host. The shop ALWAYS comes from the
// request Host (`resolveSiteFromRequest`), never from the client; these helpers only decide
// whether a backend object or client promo input belongs to that shop. Zero I/O so they are
// unit-tested directly.
import type { GwCart, GwPayment, GwVoucher } from "../../features/commerce/types";
import { GatewayError } from "./errors.ts";

export const STOREFRONT_CHANNEL = "STOREFRONT";

/** Keep only the site's shop group and lines; totals are recomputed from the kept lines. */
export function scopeCartToShop(cart: GwCart | null | undefined, shopId: string): GwCart {
  const groups = (cart?.shop_groups ?? [])
    .filter((g) => g.shop_id === shopId)
    .map((g) => {
      const items = (g.items ?? []).filter((it) => !it.shop_id || it.shop_id === shopId);
      const subtotalVnd =
        items.length === (g.items ?? []).length
          ? Number(g.subtotal_vnd) || 0
          : items.reduce(
              (sum, it) => sum + (Number(it.price_vnd) || 0) * (Number(it.quantity) || 0),
              0
            );
      const totalWeightGrams =
        items.length === (g.items ?? []).length
          ? Number(g.total_weight_grams) || 0
          : items.reduce(
              (sum, it) => sum + (Number(it.weight_grams) || 0) * (Number(it.quantity) || 0),
              0
            );
      return {
        ...g,
        items,
        subtotal_vnd: subtotalVnd,
        total_weight_grams: totalWeightGrams,
      };
    });
  const lines = groups.flatMap((g) => g.items);
  return {
    user_id: cart?.user_id ?? "",
    shop_groups: groups,
    total_items: lines.reduce((sum, it) => sum + (Number(it.quantity) || 0), 0),
    total_vnd: groups.reduce((sum, g) => sum + g.subtotal_vnd, 0),
  };
}

export function cartHasSku(cart: GwCart | null | undefined, skuId: string): boolean {
  return (cart?.shop_groups ?? []).some((g) => (g.items ?? []).some((it) => it.sku_id === skuId));
}

interface ChannelScoped {
  sales_channel?: string;
  storefront_shop_id?: string;
}

/** True only for objects placed on THIS shop's site (STOREFRONT + matching storefront shop). */
export function belongsToStorefront(
  obj: ChannelScoped | null | undefined,
  shopId: string
): boolean {
  if (!obj || !shopId) return false;
  return (
    String(obj.sales_channel ?? "").toUpperCase() === STOREFRONT_CHANNEL &&
    (obj.storefront_shop_id ?? "") === shopId
  );
}

/**
 * Orders and payments from another site (or old marketplace orders) are invisible on this site:
 * 404, never 403, so their existence is not revealed.
 */
export function assertInStorefront<T extends ChannelScoped>(
  obj: T | null | undefined,
  shopId: string
): T {
  if (!obj || !belongsToStorefront(obj, shopId)) throw new GatewayError("not_found");
  return obj;
}

export function assertPaymentInStorefront(
  payment: GwPayment | null | undefined,
  shopId: string,
  userId: string
): GwPayment {
  const p = assertInStorefront(payment, shopId);
  if (p.user_id && p.user_id !== userId) throw new GatewayError("not_found");
  return p;
}

/** A voucher usable on this site: SHOP scope, owned by the site's shop. */
export function isSiteShopVoucher(
  v: Pick<GwVoucher, "scope" | "shop_id">,
  shopId: string
): boolean {
  return String(v.scope ?? "").toUpperCase() === "SHOP" && v.shop_id === shopId;
}

function cleanString(raw: unknown): string {
  return typeof raw === "string" ? raw.trim() : "";
}

/** True if client passed any platform-level discount (platform/freeship voucher or coins > 0). */
export function hasPlatformPromoInput(body: {
  platform_voucher_id?: unknown;
  freeship_voucher_id?: unknown;
  coins_to_use?: unknown;
}): boolean {
  if (cleanString(body.platform_voucher_id)) return true;
  if (cleanString(body.freeship_voucher_id)) return true;
  const coins = Number(body.coins_to_use ?? 0);
  return Number.isFinite(coins) && coins > 0;
}

/**
 * Resolve the single shop voucher ID for `shopId` from either flat `shop_voucher_id` or keyed
 * `shop_voucher_ids`. Rejects (throws 400 `FOREIGN_SHOP_VOUCHER`) if `shop_voucher_ids` contains
 * a non-empty voucher keyed under any other shop.
 */
export function resolveSiteShopVoucherMap(
  body: {
    shop_voucher_id?: unknown;
    shop_voucher_ids?: Record<string, string | null | undefined> | null;
  },
  shopId: string
): Record<string, string> {
  let voucherId = cleanString(body.shop_voucher_id);
  const map = body.shop_voucher_ids;
  if (map && typeof map === "object" && !Array.isArray(map)) {
    for (const [k, v] of Object.entries(map)) {
      const cleanKey = k.trim();
      const cleanVal = cleanString(v);
      if (!cleanVal) continue;
      if (cleanKey && cleanKey !== shopId) {
        throw new GatewayError("invalid", "FOREIGN_SHOP_VOUCHER");
      }
      if (!voucherId) voucherId = cleanVal;
    }
  }
  return voucherId ? { [shopId]: voucherId } : {};
}
