// Server-only Seller Center helpers: strict RBAC + safe gateway loaders.
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { assertRole, requireRole, SELLER_ROLES, type Session } from "@/lib/server/session";
import type {
  GwCategory,
  GwFlashSale,
  GwSellerBalance,
  GwSellerOrder,
  GwSellerProduct,
  GwSellerShop,
  GwSellerVoucher,
} from "./sellerApiTypes";

export interface LoadResult<T> {
  data: T | null;
  error: string | null;
}

/** Page / layout server guard: requires backend role === "seller". */
export async function requireSellerPage(): Promise<Session> {
  return requireRole(...SELLER_ROLES);
}

/** Route handler guard: requires backend role === "seller" and an assigned shopId. */
export async function assertSellerSession(): Promise<Session> {
  const session = await assertRole(...SELLER_ROLES);
  if (!session.user.shopId) {
    throw new GatewayError("forbidden", "NO_SELLER_SHOP");
  }
  return session;
}

function safeLoadError(err: unknown): string {
  if (err instanceof GatewayError) {
    return SAFE_MESSAGES[err.kind];
  }
  return SAFE_MESSAGES.unavailable;
}

export async function loadSellerShop(): Promise<LoadResult<GwSellerShop>> {
  try {
    const res = await gw<{ shop: GwSellerShop | null }>("/api/v1/seller/shop");
    return { data: res.shop ?? null, error: null };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadSellerProducts(
  pageSize = 100
): Promise<LoadResult<{ items: GwSellerProduct[]; total: number }>> {
  try {
    const res = await gw<{ items: GwSellerProduct[]; total: number }>("/api/v1/seller/products", {
      query: { page: 1, page_size: pageSize },
    });
    return {
      data: { items: res.items ?? [], total: res.total ?? 0 },
      error: null,
    };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadSellerProductById(
  productId: string,
  expectedShopId: string
): Promise<LoadResult<GwSellerProduct>> {
  try {
    const res = await gw<{ product: GwSellerProduct | null }>(
      `/api/v1/products/${encodeURIComponent(productId)}`
    );
    const product = res.product ?? null;
    if (!product || product.shop_id !== expectedShopId) {
      return { data: null, error: null };
    }
    return { data: product, error: null };
  } catch (err) {
    if (err instanceof GatewayError && (err.kind === "not_found" || err.kind === "forbidden")) {
      return { data: null, error: null };
    }
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadSellerOrders(
  pageSize = 100
): Promise<LoadResult<{ items: GwSellerOrder[]; total: number }>> {
  try {
    const res = await gw<{ items: GwSellerOrder[]; total: number }>("/api/v1/seller/orders", {
      query: { page: 1, page_size: pageSize },
    });
    return {
      data: { items: res.items ?? [], total: res.total ?? 0 },
      error: null,
    };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadSellerOrderById(
  orderId: string,
  expectedShopId: string
): Promise<LoadResult<GwSellerOrder>> {
  try {
    const res = await gw<{ order: GwSellerOrder | null }>(
      `/api/v1/seller/orders/${encodeURIComponent(orderId)}`
    );
    const order = res.order ?? null;
    if (!order || order.shop_id !== expectedShopId) {
      return { data: null, error: null };
    }
    return { data: order, error: null };
  } catch (err) {
    if (err instanceof GatewayError && (err.kind === "not_found" || err.kind === "forbidden")) {
      return { data: null, error: null };
    }
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadSellerBalance(): Promise<LoadResult<GwSellerBalance>> {
  try {
    const res = await gw<GwSellerBalance>("/api/v1/seller/balance");
    return { data: res, error: null };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadCategories(): Promise<LoadResult<GwCategory[]>> {
  try {
    const res = await gw<{ categories: GwCategory[] }>("/api/v1/categories", { auth: "none" });
    return { data: res.categories ?? [], error: null };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadSellerVouchers(): Promise<LoadResult<GwSellerVoucher[]>> {
  try {
    const res = await gw<{ vouchers: GwSellerVoucher[] }>("/api/v1/seller/vouchers");
    return { data: res.vouchers ?? [], error: null };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadFlashSales(): Promise<LoadResult<GwFlashSale[]>> {
  try {
    const res = await gw<{ flash_sales: GwFlashSale[] }>("/api/v1/flash-sales", {
      query: { include_upcoming: true },
    });
    return { data: res.flash_sales ?? [], error: null };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}
