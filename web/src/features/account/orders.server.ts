// Server-only helpers for buyer orders & catalog enrichment.
import type { Product, Shop } from "@/data";
import { loadCatalogLookups } from "@/lib/catalog";
import { GatewayError } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { ADMIN_ROLES, type Session } from "@/lib/server/session";
import { mapGatewayOrder, type GwOrder, type OrderView } from "./orderView";

export interface BuyerOrdersResult {
  items: OrderView[];
  page: number;
  pageSize: number;
  total: number;
  shops: Shop[];
  suggestedProducts: Product[];
}

export async function fetchBuyerOrders(
  opts: { status?: string; page?: number; pageSize?: number; includeExtras?: boolean } = {}
): Promise<BuyerOrdersResult> {
  const page = Math.max(1, Math.trunc(opts.page ?? 1));
  const pageSize = Math.max(1, Math.min(100, Math.trunc(opts.pageSize ?? 50)));
  const res = await gw<{
    items?: GwOrder[] | null;
    page?: number;
    page_size?: number;
    total?: number;
  }>("/api/v1/orders", {
    query: {
      status: opts.status || undefined,
      page,
      page_size: pageSize,
    },
  });

  const rawItems = res.items ?? [];
  if (rawItems.length === 0 && opts.includeExtras === false) {
    return {
      items: [],
      page: res.page ?? page,
      pageSize: res.page_size ?? pageSize,
      total: res.total ?? 0,
      shops: [],
      suggestedProducts: [],
    };
  }

  const catalog = await loadCatalogLookups();
  const items = rawItems.map((o) => mapGatewayOrder(o, catalog.lookups));
  return {
    items,
    page: res.page ?? page,
    pageSize: res.page_size ?? pageSize,
    total: res.total ?? items.length,
    shops: catalog.shops,
    suggestedProducts: catalog.products.slice(0, 4),
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
}

export async function fetchBuyerOrder(
  orderId: string,
  session: Session
): Promise<BuyerOrderDetailResult> {
  const cleanId = orderId.trim();
  if (!cleanId) {
    throw new GatewayError("not_found");
  }
  // Verify order existence and buyer ownership before loading catalog lookups
  // so rejected 403/404 requests never trigger extra gateway fanout.
  const res = await gw<{ order?: GwOrder | null }>(`/api/v1/orders/${encodeURIComponent(cleanId)}`);
  const raw = assertBuyerOrderOwnership(res.order, session);
  const catalog = await loadCatalogLookups();
  const order = mapGatewayOrder(raw, catalog.lookups);
  const shop = catalog.shops.find((s) => (s.backendId ?? s.id) === raw.shop_id);
  const sameShop = shop ? catalog.products.filter((p) => p.shopId === shop.id) : [];
  const others = catalog.products.filter((p) => !shop || p.shopId !== shop.id);
  const suggestedProducts = [...sameShop, ...others].slice(0, 4);

  return { order, suggestedProducts, shop };
}
