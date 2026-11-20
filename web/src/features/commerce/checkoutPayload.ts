// Pure payload sanitizer & verified storefront checkout payload builder for `POST /api/checkout`.
// Phase 1 invariant preserved: raw client `sales_channel` / `storefront_shop_id` WITHOUT an explicit
// storefront mode hint is ALWAYS stripped and locked to `MARKETPLACE`.
// Phase 2 addition: when `checkout_channel_hint === "storefront"` is present, the BFF independently
// verifies via `GET /api/v1/sites/by-shop/<shopId>` that the shop has a PUBLISHED site on an ACTIVE
// shop before emitting `sales_channel: "STOREFRONT"` + `storefront_shop_id`.
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
  /**
   * Explicit storefront checkout hint sent ONLY by the storefront checkout UI (`/thanh-toan?channel=storefront&shop=...`).
   * The BFF NEVER trusts this blindly — it independently verifies the shop's PUBLISHED site against the backend!
   */
  checkout_channel_hint?: unknown;
  storefront_host?: unknown;
  // Raw `sales_channel` is ALWAYS ignored; `storefront_shop_id` is only read as an unverified hint
  // when `checkout_channel_hint === "storefront"`.
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
  storefront_shop_id?: string;
}

export type BuildCheckoutPayloadResult =
  | { ok: true; payload: GatewayCheckoutPayload }
  | { ok: false; code: "MISSING_ADDRESS" | "INVALID_PAYMENT_METHOD" | "SITE_NOT_PUBLISHED" };

export interface StorefrontCheckoutHint {
  shopId: string;
  host?: string;
}

export interface VerifiedStorefrontSite {
  shopId: string;
  primaryHost: string;
}

function normalizePaymentMethod(raw: string | undefined): "COD" | "VIETQR" | null {
  const upper = (raw ?? "").trim().toUpperCase();
  if (upper === "COD") return "COD";
  if (upper === "VIETQR") return "VIETQR";
  return null;
}

/**
 * Detect whether the request explicitly asks for storefront checkout verification.
 * Sending raw `{ sales_channel: "STOREFRONT", storefront_shop_id: "..." }` WITHOUT
 * `checkout_channel_hint: "storefront"` intentionally returns `null` so Phase 1's strict
 * marketplace lock remains 100% intact.
 */
export function parseStorefrontCheckoutHint(body: CheckoutBody): StorefrontCheckoutHint | null {
  if (typeof body.checkout_channel_hint !== "string") return null;
  if (body.checkout_channel_hint.trim().toLowerCase() !== "storefront") return null;

  const shopId = typeof body.storefront_shop_id === "string" ? body.storefront_shop_id.trim() : "";
  const host =
    typeof body.storefront_host === "string" && body.storefront_host.trim()
      ? body.storefront_host.trim().toLowerCase()
      : undefined;

  return { shopId, host };
}

function buildBaseCheckoutPayload(body: CheckoutBody): BuildCheckoutPayloadResult {
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

/**
 * Validate and construct a standard MARKETPLACE gateway checkout payload.
 * Unconditionally strips raw client `sales_channel` and `storefront_shop_id`.
 */
export function buildMarketplaceCheckoutPayload(body: CheckoutBody): BuildCheckoutPayloadResult {
  return buildBaseCheckoutPayload(body);
}

/**
 * Construct a server-verified STOREFRONT gateway checkout payload for a confirmed `PUBLISHED` site.
 * Locks `sales_channel: "STOREFRONT"` + `storefront_shop_id: verifiedShopId`, clears platform/freeship
 * vouchers & platform coins, and scopes shop vouchers & shipping choices to `verifiedShopId`.
 */
export function buildStorefrontCheckoutPayload(
  body: CheckoutBody,
  verifiedShopId: string
): BuildCheckoutPayloadResult {
  const cleanShop = verifiedShopId.trim();
  if (!cleanShop) {
    return { ok: false, code: "SITE_NOT_PUBLISHED" };
  }

  const base = buildBaseCheckoutPayload(body);
  if (!base.ok) return base;

  const scopedShopVouchers: Record<string, string> = {};
  if (base.payload.shop_voucher_ids[cleanShop]) {
    scopedShopVouchers[cleanShop] = base.payload.shop_voucher_ids[cleanShop];
  }

  const scopedShippingChoices = base.payload.shipping_choices.filter(
    (c) => c.shop_id === cleanShop
  );

  return {
    ok: true,
    payload: {
      ...base.payload,
      shop_voucher_ids: scopedShopVouchers,
      platform_voucher_id: "",
      freeship_voucher_id: "",
      coins_to_use: 0,
      shipping_choices: scopedShippingChoices,
      sales_channel: "STOREFRONT",
      storefront_shop_id: cleanShop,
    },
  };
}

/**
 * Full BFF checkout payload resolver:
 * - No storefront hint -> standard `MARKETPLACE` payload (raw `sales_channel` stripped).
 * - Storefront hint present -> independently verifies via `verifyPublishedSite(shopId, host)`;
 *   rejects with `SITE_NOT_PUBLISHED` if the shop has no PUBLISHED site (draft/suspended/unknown).
 */
export async function resolveCheckoutPayload(
  body: CheckoutBody,
  verifyPublishedSite: (
    shopId: string,
    expectedHost?: string
  ) => Promise<VerifiedStorefrontSite | null>
): Promise<BuildCheckoutPayloadResult> {
  const hint = parseStorefrontCheckoutHint(body);
  if (!hint) {
    return buildMarketplaceCheckoutPayload(body);
  }

  if (!hint.shopId) {
    return { ok: false, code: "SITE_NOT_PUBLISHED" };
  }

  const verified = await verifyPublishedSite(hint.shopId, hint.host);
  if (!verified || verified.shopId !== hint.shopId) {
    return { ok: false, code: "SITE_NOT_PUBLISHED" };
  }

  return buildStorefrontCheckoutPayload(body, verified.shopId);
}
