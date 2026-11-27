// Server-only helpers for buyer orders & catalog enrichment scoped to a shop's site host.
import type { Product } from "@/data/products";
import type { Shop } from "@/data/shops";
import type {
  GatewayAddress,
  GatewayProductReview,
  GatewayRefund,
  GatewayReturnRequest,
  GatewayShipment,
  GatewayShipmentEvent,
} from "@/lib/gateway-types";
import { buildCatalogLookups } from "@/lib/catalog-map";
import { loadCatalogLookups } from "@/lib/catalog";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { ADMIN_ROLES, type Session } from "@/lib/server/session";
import { assertInStorefront, belongsToStorefront } from "@/lib/server/storefront";
import { mapGatewayOrder, type GwOrder, type OrderView } from "./orderView";

export interface BuyerOrdersResult {
  items: OrderView[];
  page: number;
  pageSize: number;
  total: number;
  shops: Shop[];
  suggestedProducts: Product[];
}

export async function fetchBuyerOrders(opts: {
  storefrontShopId: string;
  status?: string;
  page?: number;
  pageSize?: number;
  includeExtras?: boolean;
  siteProducts?: Product[];
  siteShop?: Shop | null;
  skuAttributesById?: Record<string, Record<string, string>>;
}): Promise<BuyerOrdersResult> {
  const shopId = opts.storefrontShopId.trim();
  if (!shopId) {
    throw new GatewayError("not_found");
  }

  const page = Math.max(1, Math.trunc(opts.page ?? 1));
  const pageSize = Math.max(1, Math.min(100, Math.trunc(opts.pageSize ?? 50)));
  const res = await gw<{
    items?: GwOrder[] | null;
    page?: number;
    page_size?: number;
    total?: number;
  }>("/api/v1/orders", {
    query: {
      storefront_shop_id: shopId,
      status: opts.status || undefined,
      page,
      page_size: pageSize,
    },
  });

  // Defense in depth: strictly keep orders belonging to THIS site's storefront_shop_id.
  const rawItems = (res.items ?? []).filter((o) => belongsToStorefront(o, shopId));
  if (rawItems.length === 0 && opts.includeExtras === false) {
    return {
      items: [],
      page: res.page ?? page,
      pageSize: res.page_size ?? pageSize,
      total: 0,
      shops: opts.siteShop ? [opts.siteShop] : [],
      suggestedProducts: (opts.siteProducts ?? []).slice(0, 4),
    };
  }

  const shops = opts.siteShop ? [opts.siteShop] : [];
  const baseLookups =
    opts.siteProducts && opts.siteProducts.length > 0
      ? buildCatalogLookups(opts.siteProducts, shops)
      : (await loadCatalogLookups()).lookups;
  const skuAttrMap = opts.skuAttributesById;
  const lookups = skuAttrMap
    ? {
        ...baseLookups,
        skuAttributes: (skuId: string) =>
          Object.prototype.hasOwnProperty.call(skuAttrMap, skuId) ? skuAttrMap[skuId] : undefined,
      }
    : baseLookups;

  const items = rawItems.map((o) => mapGatewayOrder(o, lookups));
  const total =
    typeof res.total === "number" && rawItems.length === (res.items ?? []).length
      ? res.total
      : items.length;

  return {
    items,
    page: res.page ?? page,
    pageSize: res.page_size ?? pageSize,
    total,
    shops,
    suggestedProducts: (opts.siteProducts ?? []).slice(0, 4),
  };
}

export function assertBuyerOrderOwnership(
  order: GwOrder | null | undefined,
  session: Session
): GwOrder {
  if (!order?.id) {
    throw new GatewayError("not_found");
  }
  const isAdmin = ADMIN_ROLES.includes(session.user.role);
  if (order.buyer_id && order.buyer_id !== session.user.id && !isAdmin) {
    throw new GatewayError("forbidden");
  }
  return order;
}

export interface BuyerOrderDetailResult {
  order: OrderView;
  suggestedProducts: Product[];
  shop?: Shop;
  shipment: GatewayShipment | null;
  shipmentEvents: GatewayShipmentEvent[];
  returnShipment: GatewayShipment | null;
  returnRequest: GatewayReturnRequest | null;
  refunds: GatewayRefund[];
  reviews: GatewayProductReview[];
  addresses: GatewayAddress[];
}

export async function fetchBuyerOrder(
  orderId: string,
  session: Session,
  storefrontShopId: string,
  opts: {
    siteProducts?: Product[];
    siteShop?: Shop | null;
    skuAttributesById?: Record<string, Record<string, string>>;
  } = {}
): Promise<BuyerOrderDetailResult> {
  const cleanId = orderId.trim();
  const shopId = storefrontShopId.trim();
  if (!cleanId || !shopId) {
    throw new GatewayError("not_found");
  }

  // Verify order existence, buyer ownership AND storefront tenant match before extra work.
  const res = await gw<{ order?: GwOrder | null }>(`/api/v1/orders/${encodeURIComponent(cleanId)}`);
  const owned = assertBuyerOrderOwnership(res.order, session);
  const raw = assertInStorefront(owned, shopId);

  const shops = opts.siteShop ? [opts.siteShop] : [];
  const baseLookups =
    opts.siteProducts && opts.siteProducts.length > 0
      ? buildCatalogLookups(opts.siteProducts, shops)
      : (await loadCatalogLookups()).lookups;
  const skuAttrMap = opts.skuAttributesById;
  const lookups = skuAttrMap
    ? {
        ...baseLookups,
        skuAttributes: (skuId: string) =>
          Object.prototype.hasOwnProperty.call(skuAttrMap, skuId) ? skuAttrMap[skuId] : undefined,
      }
    : baseLookups;

  const order = mapGatewayOrder(raw, lookups);
  const suggestedProducts = (opts.siteProducts ?? []).slice(0, 4);
  const encodedId = encodeURIComponent(raw.id);
  const statusUpper = raw.status.toUpperCase();
  const canEditAddr = statusUpper === "CONFIRMED" || statusUpper === "PREPARING";

  const [shipRes, retShipRes, retRes, refundsRes, reviewsRes, addrRes] = await Promise.allSettled([
    gw<{ shipment?: GatewayShipment | null; events?: GatewayShipmentEvent[] }>(
      `/api/v1/shipping/shipments/${encodedId}`
    ),
    gw<{ shipment?: GatewayShipment | null }>(`/api/v1/shipping/shipments/${encodedId}`, {
      query: { direction: "RETURN" },
    }),
    gw<{ return_request?: GatewayReturnRequest | null }>(`/api/v1/orders/${encodedId}/return`),
    gw<{ items?: GatewayRefund[]; refunds?: GatewayRefund[] }>(
      `/api/v1/orders/${encodedId}/refunds`
    ),
    statusUpper === "COMPLETED"
      ? gw<{ reviews?: GatewayProductReview[] }>(`/api/v1/orders/${encodedId}/reviews`)
      : Promise.resolve({ reviews: [] as GatewayProductReview[] }),
    canEditAddr
      ? gw<{ addresses?: GatewayAddress[] }>("/api/v1/users/me/addresses")
      : Promise.resolve({ addresses: [] as GatewayAddress[] }),
  ]);

  return {
    order,
    suggestedProducts,
    shop: opts.siteShop ?? undefined,
    shipment: shipRes.status === "fulfilled" ? (shipRes.value.shipment ?? null) : null,
    shipmentEvents: shipRes.status === "fulfilled" ? (shipRes.value.events ?? []) : [],
    returnShipment: retShipRes.status === "fulfilled" ? (retShipRes.value.shipment ?? null) : null,
    returnRequest: retRes.status === "fulfilled" ? (retRes.value.return_request ?? null) : null,
    refunds:
      refundsRes.status === "fulfilled"
        ? (refundsRes.value.items ?? refundsRes.value.refunds ?? [])
        : [],
    reviews: reviewsRes.status === "fulfilled" ? (reviewsRes.value.reviews ?? []) : [],
    addresses: addrRes.status === "fulfilled" ? (addrRes.value.addresses ?? []) : [],
  };
}
