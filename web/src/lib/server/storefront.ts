// Pure tenant-scoping rules for the buyer BFF on a site host. The shop ALWAYS comes from the
// request Host (`resolveSiteFromRequest`), never from the client; these helpers only decide
// whether a backend object or client promo input belongs to that shop. Zero I/O so they are
// unit-tested directly.
import type { OrderLookupResponse, PublicOrderSummary } from "../a8-types";
import { ADMIN_STAFF_ROLES } from "../admin-rbac.ts";
import type { GwCart, GwPayment, GwVoucher } from "../../features/commerce/types";
import { GatewayError } from "./errors.ts";

export const STOREFRONT_CHANNEL = "STOREFRONT";

/** Keep only the site's shop group and lines; totals exclude unavailable items. */
export function scopeCartToShop(cart: GwCart | null | undefined, shopId: string): GwCart {
  const groups = (cart?.shop_groups ?? [])
    .filter((g) => g.shop_id === shopId)
    .map((g) => {
      const items = (g.items ?? []).filter((it) => !it.shop_id || it.shop_id === shopId);
      const availableItems = items.filter((it) => !it.unavailable);
      const hasUnavailable = availableItems.length !== items.length;
      const subtotalVnd =
        !hasUnavailable && items.length === (g.items ?? []).length
          ? Number(g.subtotal_vnd) || 0
          : availableItems.reduce(
              (sum, it) => sum + (Number(it.price_vnd) || 0) * (Number(it.quantity) || 0),
              0
            );
      const totalWeightGrams =
        !hasUnavailable && items.length === (g.items ?? []).length
          ? Number(g.total_weight_grams) || 0
          : availableItems.reduce(
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
  const availableLines = groups.flatMap((g) => g.items).filter((it) => !it.unavailable);
  return {
    user_id: cart?.user_id ?? "",
    shop_groups: groups,
    total_items: availableLines.reduce((sum, it) => sum + (Number(it.quantity) || 0), 0),
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

export interface OwnershipScopedOrder extends ChannelScoped {
  id?: string;
  buyer_id?: string;
}

export interface SessionIdentity {
  user: { id: string; role: string };
}

function isAdminRole(role: string): boolean {
  return (ADMIN_STAFF_ROLES as readonly string[]).includes(role);
}

/**
 * Verify an order belongs to BOTH `shopId`'s storefront AND the authenticated buyer (or admin).
 * Always throws 404 (`not_found`), never 403, so existence of cross-shop or other buyers' orders
 * is never disclosed on a site host.
 */
export function assertBuyerOrderInStorefront<T extends OwnershipScopedOrder>(
  order: T | null | undefined,
  shopId: string,
  session: SessionIdentity
): T {
  const scoped = assertInStorefront(order, shopId);
  if (!scoped.id) throw new GatewayError("not_found");
  if (scoped.buyer_id && scoped.buyer_id !== session.user.id && !isAdminRole(session.user.role)) {
    throw new GatewayError("not_found");
  }
  return scoped;
}

function rethrowNotFoundOnAuthz(err: unknown): never {
  if (err instanceof GatewayError && (err.kind === "not_found" || err.kind === "forbidden")) {
    throw new GatewayError("not_found");
  }
  throw err;
}

export interface ReturnOrderLink {
  id?: string;
  order_id?: string;
  shop_id?: string;
}

/**
 * Tenant + ownership guard for buyer return mutations (`evidence`, `escalate`) on a site host.
 * Server derives `order_id` solely from `GET /api/v1/returns/{id}` (never from client body),
 * then verifies the order belongs to `shopId` and `session.user`. Throws 404 on any mismatch.
 */
export async function verifySiteReturnAccess<
  TReturn extends ReturnOrderLink,
  TOrder extends OwnershipScopedOrder,
>(opts: {
  returnId: string;
  shopId: string;
  session: SessionIdentity;
  fetchReturn: (
    returnId: string
  ) => Promise<{ return_request?: TReturn | null } | null | undefined>;
  fetchOrder: (orderId: string) => Promise<{ order?: TOrder | null } | null | undefined>;
}): Promise<{ returnRequest: TReturn; order: TOrder }> {
  const cleanReturnId = opts.returnId.trim();
  const cleanShopId = opts.shopId.trim();
  if (!cleanReturnId || !cleanShopId) throw new GatewayError("not_found");

  let retRes: { return_request?: TReturn | null } | null | undefined;
  try {
    retRes = await opts.fetchReturn(cleanReturnId);
  } catch (err) {
    rethrowNotFoundOnAuthz(err);
  }

  const rr = retRes?.return_request;
  const derivedOrderId = typeof rr?.order_id === "string" ? rr.order_id.trim() : "";
  if (!rr || !derivedOrderId) throw new GatewayError("not_found");
  if (rr.id && rr.id.trim() !== cleanReturnId) throw new GatewayError("not_found");
  if (rr.shop_id && rr.shop_id.trim() !== cleanShopId) throw new GatewayError("not_found");

  let orderRes: { order?: TOrder | null } | null | undefined;
  try {
    orderRes = await opts.fetchOrder(derivedOrderId);
  } catch (err) {
    rethrowNotFoundOnAuthz(err);
  }

  const order = assertBuyerOrderInStorefront(orderRes?.order, cleanShopId, opts.session);
  return { returnRequest: rr, order };
}

export interface RefundOrderLink {
  refund_id?: string;
  id?: string;
  order_id?: string;
}

/**
 * Tenant + ownership guard for buyer refund bank-account submission on a site host.
 * Requires `orderId`, verifies the order belongs to `shopId` and `session.user`, then confirms
 * `GET /api/v1/orders/{orderId}/refunds` actually contains `refundId`. Throws 404 (never 403)
 * on any cross-shop, ownership, or refund-binding mismatch.
 */
export async function verifySiteRefundAccess<
  TRefund extends RefundOrderLink,
  TOrder extends OwnershipScopedOrder,
>(opts: {
  refundId: string;
  orderId: string;
  shopId: string;
  session: SessionIdentity;
  fetchOrder: (orderId: string) => Promise<{ order?: TOrder | null } | null | undefined>;
  fetchOrderRefunds: (
    orderId: string
  ) => Promise<{ items?: TRefund[]; refunds?: TRefund[] } | null | undefined>;
}): Promise<{ order: TOrder; refund: TRefund }> {
  const cleanRefundId = opts.refundId.trim();
  const cleanOrderId = opts.orderId.trim();
  const cleanShopId = opts.shopId.trim();
  if (!cleanRefundId || !cleanShopId) throw new GatewayError("not_found");
  if (!cleanOrderId) throw new GatewayError("invalid", "ORDER_ID_REQUIRED");

  let orderRes: { order?: TOrder | null } | null | undefined;
  try {
    orderRes = await opts.fetchOrder(cleanOrderId);
  } catch (err) {
    rethrowNotFoundOnAuthz(err);
  }

  const order = assertBuyerOrderInStorefront(orderRes?.order, cleanShopId, opts.session);

  let refundsRes: { items?: TRefund[]; refunds?: TRefund[] } | null | undefined;
  try {
    refundsRes = await opts.fetchOrderRefunds(cleanOrderId);
  } catch (err) {
    rethrowNotFoundOnAuthz(err);
  }

  const list = refundsRes?.items ?? refundsRes?.refunds ?? [];
  const matched = list.find((item) => {
    const id = (item?.refund_id ?? item?.id ?? "").trim();
    if (id !== cleanRefundId) return false;
    if (item?.order_id && item.order_id.trim() !== cleanOrderId) return false;
    return true;
  });
  if (!matched) throw new GatewayError("not_found");

  return { order, refund: matched };
}

export interface OrderLookupGatewayBody {
  order_code: string;
  phone: string;
  storefront_shop_id: string;
}

/**
 * Build the gateway `POST /api/v1/orders/lookup` payload, always pinning `storefront_shop_id`
 * from the resolved site host so orders from other shops are rejected as NOT_FOUND by gateway.
 */
export function buildOrderLookupGatewayBody(input: {
  orderCode: string;
  phone: string;
  storefrontShopId: string;
}): OrderLookupGatewayBody {
  const orderCode = input.orderCode.trim();
  const phone = input.phone.trim();
  const storefrontShopId = input.storefrontShopId.trim();
  if (!storefrontShopId) {
    throw new GatewayError("not_found");
  }
  if (orderCode.length < 3 || phone.replace(/\D/g, "").length < 8) {
    throw new GatewayError("invalid", "INVALID_ARGUMENT");
  }
  return {
    order_code: orderCode,
    phone,
    storefront_shop_id: storefrontShopId,
  };
}

/**
 * Execute site-scoped public order lookup and strictly whitelist non-PII `PublicOrderSummary`
 * fields (never exposing `street_address` or `label_url`).
 */
export async function executeSiteOrderLookup(opts: {
  orderCode: string;
  phone: string;
  storefrontShopId: string;
  postLookup: (
    body: OrderLookupGatewayBody
  ) => Promise<{ order?: PublicOrderSummary | null } | null | undefined>;
}): Promise<OrderLookupResponse> {
  const lookupBody = buildOrderLookupGatewayBody({
    orderCode: opts.orderCode,
    phone: opts.phone,
    storefrontShopId: opts.storefrontShopId,
  });

  const data = await opts.postLookup(lookupBody);
  const ord = data?.order;
  if (!ord) {
    throw new GatewayError("not_found", "ORDER_LOOKUP_NOT_FOUND");
  }

  return {
    order: {
      order_code: ord.order_code,
      status: ord.status,
      payment_method: ord.payment_method,
      payment_status: ord.payment_status,
      subtotal_vnd: Math.trunc(ord.subtotal_vnd ?? 0),
      shipping_fee_vnd: Math.trunc(ord.shipping_fee_vnd ?? 0),
      total_payable_vnd: Math.trunc(ord.total_payable_vnd ?? 0),
      carrier_code: ord.carrier_code ?? "",
      tracking_code: ord.tracking_code ?? "",
      province_name: ord.province_name ?? "",
      ward_name: ord.ward_name ?? "",
      recipient_name_masked: ord.recipient_name_masked ?? "",
      recipient_phone_masked: ord.recipient_phone_masked ?? "",
      items: Array.isArray(ord.items)
        ? ord.items.map((it) => ({
            product_name: it.product_name,
            sku_name: it.sku_name,
            quantity: Math.trunc(it.quantity ?? 0),
            line_total_vnd: Math.trunc(it.line_total_vnd ?? 0),
          }))
        : [],
      timeline: Array.isArray(ord.timeline)
        ? ord.timeline.map((ev) => ({
            to_status: ev.to_status,
            created_at: ev.created_at,
          }))
        : [],
      created_at: ord.created_at,
      updated_at: ord.updated_at,
      delivered_at: ord.delivered_at,
    },
  };
}

/**
 * Tenant + shipment-binding guard for `GET /api/shipping/labels/[code]` on a site host.
 * Requires `orderId`, verifies the order belongs to `shopId` and `session.user`, and confirms
 * a FORWARD or RETURN shipment of that order has `tracking_code === trackingCode`.
 * Always throws 404 (never 403) on any mismatch.
 */
export async function verifySiteShippingLabelAccess<TOrder extends OwnershipScopedOrder>(opts: {
  trackingCode: string;
  orderId: string;
  shopId: string;
  session: SessionIdentity;
  fetchOrder: (orderId: string) => Promise<{ order?: TOrder | null } | null | undefined>;
  fetchShipment: (
    orderId: string,
    direction: "FORWARD" | "RETURN"
  ) => Promise<{ shipment?: { tracking_code?: string | null } | null } | null | undefined>;
}): Promise<TOrder> {
  const cleanCode = opts.trackingCode.trim();
  const cleanOrderId = opts.orderId.trim();
  const cleanShopId = opts.shopId.trim();
  if (!cleanCode || !cleanOrderId || !cleanShopId) {
    throw new GatewayError("not_found");
  }

  let orderRes: { order?: TOrder | null } | null | undefined;
  try {
    orderRes = await opts.fetchOrder(cleanOrderId);
  } catch (err) {
    rethrowNotFoundOnAuthz(err);
  }

  const order = assertBuyerOrderInStorefront(orderRes?.order, cleanShopId, opts.session);

  let nonAuthzErr: unknown = null;
  for (const direction of ["FORWARD", "RETURN"] as const) {
    try {
      const shipRes = await opts.fetchShipment(cleanOrderId, direction);
      const code = (shipRes?.shipment?.tracking_code ?? "").trim();
      if (code && code === cleanCode) {
        return order;
      }
    } catch (err) {
      if (err instanceof GatewayError && (err.kind === "not_found" || err.kind === "forbidden")) {
        continue;
      }
      if (!nonAuthzErr) nonAuthzErr = err;
    }
  }

  if (nonAuthzErr) throw nonAuthzErr;
  throw new GatewayError("not_found");
}

export interface SiteQuoteLineInput {
  sku_id?: string;
  quantity?: number;
}

export interface SiteShopQuoteInput {
  shop_id?: string;
  weight_grams?: number;
  cod_amount_vnd?: number;
  items?: SiteQuoteLineInput[];
  payment_method?: string;
}

export interface SiteShippingQuoteBody {
  to_province_code?: string;
  to_ward_code?: string;
  weight_grams?: number;
  cod_amount_vnd?: number;
  items?: SiteQuoteLineInput[];
  payment_method?: string;
  shops?: SiteShopQuoteInput[];
}

/**
 * Normalizes a site shipping-quote request into the gateway `/api/v1/shipping/calculate-fee`
 * payload scoped strictly to `siteShopId`. Never forwards client-supplied `from_*` fields so
 * the gateway always resolves the shop's authoritative pickup address (audit 5e).
 */
export function buildSiteShippingQuotePayload(
  body: SiteShippingQuoteBody,
  siteShopId: string
): Record<string, unknown> {
  const toProvinceCode = (body.to_province_code ?? "").trim();
  const toWardCode = (body.to_ward_code ?? "").trim();

  if (!toProvinceCode) {
    throw new GatewayError("invalid", "INVALID_ARGUMENT");
  }

  let input: SiteShopQuoteInput | undefined;
  if (Array.isArray(body.shops) && body.shops.length > 0) {
    const hasForeign = body.shops.some((s) => {
      const id = (s?.shop_id ?? "").trim();
      return id.length > 0 && id !== siteShopId;
    });
    input =
      body.shops.find((s) => (s?.shop_id ?? "").trim() === siteShopId) ??
      body.shops.find((s) => !(s?.shop_id ?? "").trim());
    if (!input && hasForeign) {
      throw new GatewayError("invalid", "INVALID_ARGUMENT");
    }
  } else {
    input = {
      weight_grams: body.weight_grams,
      cod_amount_vnd: body.cod_amount_vnd,
      items: body.items,
      payment_method: body.payment_method,
    };
  }

  if (!input) {
    throw new GatewayError("invalid", "INVALID_ARGUMENT");
  }

  const normalizedItems = Array.isArray(input.items)
    ? input.items
        .map((it) => ({
          sku_id: typeof it?.sku_id === "string" ? it.sku_id.trim() : "",
          quantity: Math.max(1, Math.trunc(Number(it?.quantity ?? 1) || 1)),
        }))
        .filter((it) => it.sku_id.length > 0)
    : [];
  const rawPaymentMethod = (input.payment_method ?? body.payment_method ?? "").trim().toUpperCase();
  const paymentMethod =
    rawPaymentMethod === "COD" || rawPaymentMethod === "VIETQR" || rawPaymentMethod === "ZALOPAY"
      ? rawPaymentMethod
      : undefined;

  const gatewayPayload: Record<string, unknown> = {
    shop_id: siteShopId,
    to_province_code: toProvinceCode,
    to_ward_code: toWardCode,
  };
  if (normalizedItems.length > 0) {
    gatewayPayload.items = normalizedItems;
    if (paymentMethod) gatewayPayload.payment_method = paymentMethod;
  } else {
    gatewayPayload.weight_grams = Math.max(
      100,
      Math.trunc(Number(input.weight_grams ?? 500) || 500)
    );
    gatewayPayload.cod_amount_vnd = Math.max(0, Math.trunc(Number(input.cod_amount_vnd ?? 0) || 0));
    if (paymentMethod) gatewayPayload.payment_method = paymentMethod;
  }

  return gatewayPayload;
}
