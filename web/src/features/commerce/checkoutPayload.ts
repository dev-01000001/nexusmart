// Pure payload sanitizer for the Phase 1 Checkout BFF (`POST /api/checkout`).
// Strips any client-supplied `sales_channel` / `storefront_shop_id` and unconditionally locks
// BFF checkout requests to `sales_channel: "MARKETPLACE"` (with no `storefront_shop_id`).
import { DEFAULT_SALES_CHANNEL, type SalesChannel } from "../../lib/salesChannel";

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
  shop_voucher_ids?: Record<string, string | null | undefined>;
  platform_voucher_id?: string | null;
  freeship_voucher_id?: string | null;
  coins_to_use?: number;
  payment_method?: string;
  shipping_choices?: ShippingChoiceBody[];
  idempotency_key?: string;
  // Accepted only so TypeScript/callers can see they are explicitly ignored in Phase 1.
  sales_channel?: unknown;
  storefront_shop_id?: unknown;
  [extra: string]: unknown;
}

export interface GatewayCheckoutPayload {
  address_id: string;
  from_cart: boolean;
  items: Array<{ sku_id: string; quantity: number; flash_sale_item_id: string }>;
  shop_voucher_ids: Record<string, string>;
  platform_voucher_id: string;
  freeship_voucher_id: string;
  coins_to_use: number;
  payment_method: "COD" | "VIETQR";
  shipping_choices: Array<{ shop_id: string; carrier_code: string; service_code: string }>;
  sales_channel: SalesChannel;
}

export type BuildCheckoutPayloadResult =
  | { ok: true; payload: GatewayCheckoutPayload }
  | { ok: false; code: "MISSING_ADDRESS" | "INVALID_PAYMENT_METHOD" };

function normalizePaymentMethod(raw: string | undefined): "COD" | "VIETQR" | null {
  const upper = (raw ?? "").trim().toUpperCase();
  if (upper === "COD") return "COD";
  if (upper === "VIETQR") return "VIETQR";
  return null;
}

/**
 * Validate and construct the gateway checkout payload from an untrusted browser request body.
 * Phase 1 invariant: client bodies can NEVER choose `sales_channel` or `storefront_shop_id`;
 * this function always emits `sales_channel: "MARKETPLACE"` and omits `storefront_shop_id`.
 */
export function buildMarketplaceCheckoutPayload(body: CheckoutBody): BuildCheckoutPayloadResult {
  const addressId = typeof body.address_id === "string" ? body.address_id.trim() : "";
  if (!addressId) {
    return { ok: false, code: "MISSING_ADDRESS" };
  }

  const paymentMethod = normalizePaymentMethod(
    typeof body.payment_method === "string" ? body.payment_method : undefined
  );
  if (!paymentMethod) {
    return { ok: false, code: "INVALID_PAYMENT_METHOD" };
  }

  const items = Array.isArray(body.items)
    ? body.items
        .map((it) => ({
          sku_id: typeof it?.sku_id === "string" ? it.sku_id.trim() : "",
          quantity: Math.trunc(Number(it?.quantity ?? 0)),
          flash_sale_item_id:
            typeof it?.flash_sale_item_id === "string" ? it.flash_sale_item_id.trim() : "",
        }))
        .filter((it) => it.sku_id.length > 0 && it.quantity > 0)
    : [];

  const fromCart = typeof body.from_cart === "boolean" ? body.from_cart : items.length === 0;

  const shopVoucherIds: Record<string, string> = {};
  for (const [shopId, voucherVal] of Object.entries(body.shop_voucher_ids ?? {})) {
    const cleanShop = shopId.trim();
    const cleanVoucher = typeof voucherVal === "string" ? voucherVal.trim() : "";
    if (cleanShop && cleanVoucher) shopVoucherIds[cleanShop] = cleanVoucher;
  }

  const shippingChoices = Array.isArray(body.shipping_choices)
    ? body.shipping_choices
        .map((c) => ({
          shop_id: typeof c?.shop_id === "string" ? c.shop_id.trim() : "",
          carrier_code: typeof c?.carrier_code === "string" ? c.carrier_code.trim() : "",
          service_code: typeof c?.service_code === "string" ? c.service_code.trim() : "",
        }))
        .filter((c) => c.shop_id.length > 0)
    : [];

  const coinsToUse = Math.max(0, Math.trunc(Number(body.coins_to_use ?? 0) || 0));

  return {
    ok: true,
    payload: {
      address_id: addressId,
      from_cart: fromCart,
      items,
      shop_voucher_ids: shopVoucherIds,
      platform_voucher_id:
        typeof body.platform_voucher_id === "string" ? body.platform_voucher_id.trim() : "",
      freeship_voucher_id:
        typeof body.freeship_voucher_id === "string" ? body.freeship_voucher_id.trim() : "",
      coins_to_use: coinsToUse,
      payment_method: paymentMethod,
      shipping_choices: shippingChoices,
      sales_channel: DEFAULT_SALES_CHANNEL,
    },
  };
}
