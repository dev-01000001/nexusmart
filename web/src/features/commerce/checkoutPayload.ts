// Pure builder for the gateway payload of `POST /api/checkout` on a site host.
// Every order placed through the web is a STOREFRONT order of the shop that owns the site serving
// the request: `sales_channel` and `storefront_shop_id` come from the server-resolved site, never
// from the body. Platform-level discounts (platform/freeship vouchers, coins) and vouchers of
// other shops are rejected; shop vouchers and shipping choices are scoped to the site's shop.

export interface CheckoutItemBody {
  sku_id?: string;
  quantity?: number;
  flash_sale_item_id?: string;
}

export interface ShippingChoiceBody {
  shop_id?: string;
  carrier_code?: string;
  service_code?: string;
}

export interface CheckoutBody {
  address_id?: string;
  from_cart?: boolean;
  items?: CheckoutItemBody[];
  /** Preferred flat voucher ID (no shop_id from client). */
  shop_voucher_id?: string | null;
  /** Optional legacy map form; any non-empty key other than the site's shop is rejected. */
  shop_voucher_ids?: Record<string, string | null | undefined>;
  payment_method?: string;
  /** Preferred flat shipping choice (no shop_id from client). */
  shipping_choice?: { carrier_code?: string; service_code?: string } | null;
  shipping_choices?: ShippingChoiceBody[];
  idempotency_key?: string;
  /** Disallowed platform promos: non-empty / positive values are rejected. */
  platform_voucher_id?: string | null;
  freeship_voucher_id?: string | null;
  coins_to_use?: number;
  // Anything else the client sends (sales_channel, storefront_shop_id, hints…) is ignored.
  [extra: string]: unknown;
}

export interface GatewayCheckoutPayload {
  address_id: string;
  from_cart: boolean;
  items: Array<{ sku_id: string; quantity: number; flash_sale_item_id: string }>;
  shop_voucher_ids: Record<string, string>;
  payment_method: "COD" | "VIETQR" | "ZALOPAY";
  shipping_choices: Array<{ shop_id: string; carrier_code: string; service_code: string }>;
  sales_channel: "STOREFRONT";
  storefront_shop_id: string;
}

export type CheckoutPayloadErrorCode =
  | "MISSING_ADDRESS"
  | "INVALID_PAYMENT_METHOD"
  | "SITE_NOT_PUBLISHED"
  | "PLATFORM_PROMO_NOT_ALLOWED"
  | "FOREIGN_SHOP_VOUCHER";

export type BuildCheckoutPayloadResult =
  { ok: true; payload: GatewayCheckoutPayload } | { ok: false; code: CheckoutPayloadErrorCode };

function normalizePaymentMethod(raw: unknown): "COD" | "VIETQR" | "ZALOPAY" | null {
  const upper = (typeof raw === "string" ? raw : "").trim().toUpperCase();
  if (upper === "COD") return "COD";
  if (upper === "VIETQR") return "VIETQR";
  if (upper === "ZALOPAY") return "ZALOPAY";
  return null;
}

function str(raw: unknown): string {
  return typeof raw === "string" ? raw.trim() : "";
}

/**
 * Build the STOREFRONT checkout payload for the site's shop (`siteShopId`, resolved from Host).
 */
export function buildStorefrontCheckoutPayload(
  body: CheckoutBody,
  siteShopId: string
): BuildCheckoutPayloadResult {
  const shopId = siteShopId.trim();
  if (!shopId) return { ok: false, code: "SITE_NOT_PUBLISHED" };

  const addressId = str(body.address_id);
  if (!addressId) return { ok: false, code: "MISSING_ADDRESS" };

  const paymentMethod = normalizePaymentMethod(body.payment_method);
  if (!paymentMethod) return { ok: false, code: "INVALID_PAYMENT_METHOD" };

  const coins = Number(body.coins_to_use ?? 0);
  if (
    str(body.platform_voucher_id) ||
    str(body.freeship_voucher_id) ||
    (Number.isFinite(coins) && coins > 0)
  ) {
    return { ok: false, code: "PLATFORM_PROMO_NOT_ALLOWED" };
  }

  let voucherId = str(body.shop_voucher_id);
  if (
    body.shop_voucher_ids &&
    typeof body.shop_voucher_ids === "object" &&
    !Array.isArray(body.shop_voucher_ids)
  ) {
    for (const [k, v] of Object.entries(body.shop_voucher_ids)) {
      const cleanKey = k.trim();
      const cleanVal = str(v);
      if (!cleanVal) continue;
      if (cleanKey && cleanKey !== shopId) {
        return { ok: false, code: "FOREIGN_SHOP_VOUCHER" };
      }
      if (!voucherId) voucherId = cleanVal;
    }
  }
  const shopVoucherIds: Record<string, string> = voucherId ? { [shopId]: voucherId } : {};

  const items = Array.isArray(body.items)
    ? body.items
        .map((it) => ({
          sku_id: str(it?.sku_id),
          quantity: Math.trunc(Number(it?.quantity ?? 0)),
          flash_sale_item_id: str(it?.flash_sale_item_id),
        }))
        .filter((it) => it.sku_id.length > 0 && Number.isFinite(it.quantity) && it.quantity > 0)
    : [];

  const fromCart = typeof body.from_cart === "boolean" ? body.from_cart : items.length === 0;

  let shippingChoices: Array<{ shop_id: string; carrier_code: string; service_code: string }> = [];
  if (body.shipping_choice && typeof body.shipping_choice === "object") {
    const carrierCode = str(body.shipping_choice.carrier_code);
    const serviceCode = str(body.shipping_choice.service_code);
    if (carrierCode || serviceCode) {
      shippingChoices = [{ shop_id: shopId, carrier_code: carrierCode, service_code: serviceCode }];
    }
  }
  if (shippingChoices.length === 0 && Array.isArray(body.shipping_choices)) {
    const matched = body.shipping_choices
      .map((c) => ({
        shop_id: str(c?.shop_id),
        carrier_code: str(c?.carrier_code),
        service_code: str(c?.service_code),
      }))
      .find(
        (c) =>
          (c.shop_id === "" || c.shop_id === shopId) &&
          (c.carrier_code.length > 0 || c.service_code.length > 0)
      );
    if (matched) {
      shippingChoices = [
        {
          shop_id: shopId,
          carrier_code: matched.carrier_code,
          service_code: matched.service_code,
        },
      ];
    }
  }

  return {
    ok: true,
    payload: {
      address_id: addressId,
      from_cart: fromCart,
      items,
      shop_voucher_ids: shopVoucherIds,
      payment_method: paymentMethod,
      shipping_choices: shippingChoices,
      sales_channel: "STOREFRONT",
      storefront_shop_id: shopId,
    },
  };
}
