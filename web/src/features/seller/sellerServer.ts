// Server-only Seller Center helpers: strict RBAC, ID validation + safe gateway loaders.
import { notFound, redirect } from "next/navigation";
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import {
  assertRole,
  requireRole,
  requireSession,
  SELLER_ROLES,
  type Role,
  type Session,
} from "@/lib/server/session";
import {
  normalizeSellerOrder,
  normalizeSellerShipmentResponse,
  type GwCategory,
  type GwFlashSale,
  type GwFlashSaleItem,
  type GwSellerBalance,
  type GwSellerBankAccount,
  type GwSellerOrder,
  type GwSellerProduct,
  type GwSellerReport,
  type GwSellerReturnRequest,
  type GwSellerShop,
  type GwSellerVoucher,
  type GwShopDiscountProgram,
  type GwShopShippingSettings,
  type SellerShipmentPayload,
} from "./sellerApiTypes";
import type { SellerAddress } from "./sellerReturns";
import { isValidResourceId } from "./sellerValidation";

export interface LoadResult<T> {
  data: T | null;
  error: string | null;
}

const SELLER_PORTAL_ROLES: readonly Role[] = ["seller", "buyer"];

/** Validates a path resource ID (UUID or safe gateway ID) in BFF routes. */
export function assertValidId(raw: string | undefined, code = "INVALID_ID"): string {
  const clean = (raw ?? "").trim();
  if (!isValidResourceId(clean)) {
    throw new GatewayError("invalid", code);
  }
  return clean;
}

/** Page / layout server guard for the Seller Portal shell (allows buyer onboarding on /seller). */
export async function requireSellerPortalSession(nextPath = "/seller"): Promise<Session> {
  const session = await requireSession(nextPath);
  if (!SELLER_PORTAL_ROLES.includes(session.user.role)) {
    notFound();
  }
  return session;
}

/**
 * Strict seller subpage guard:
 * - Unauthenticated users redirect to `/dang-nhap`.
 * - Non-seller, non-buyer roles (admin staff) receive 404.
 * - Buyers (or sellers without an assigned shopId) redirect to `/seller` to finish shop registration/approval.
 */
export async function requireSellerPage(): Promise<Session> {
  const session = await requireSession("/seller");
  if (!SELLER_PORTAL_ROLES.includes(session.user.role)) {
    notFound();
  }
  if (session.user.role !== "seller" || !session.user.shopId) {
    redirect("/seller");
  }
  return session;
}

/** Strict seller role check (for routes that already handle no-shop internally). */
export async function requireSellerRoleOnly(): Promise<Session> {
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
    return err.safeMessage || SAFE_MESSAGES[err.kind];
  }
  return SAFE_MESSAGES.unavailable;
}

export async function loadSellerShop(): Promise<LoadResult<GwSellerShop>> {
  try {
    const res = await gw<{ shop: GwSellerShop | null }>("/api/v1/seller/shop");
    return { data: res.shop ?? null, error: null };
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "not_found") {
      return { data: null, error: null };
    }
    return { data: null, error: safeLoadError(err) };
  }
}

/** Loads the authenticated user's shop via GET /api/v1/shops/me (works for buyer/pending/rejected/active). */
export async function loadMyShop(): Promise<LoadResult<GwSellerShop>> {
  try {
    const res = await gw<{ shop: GwSellerShop | null }>("/api/v1/shops/me");
    return { data: res.shop ?? null, error: null };
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "not_found") {
      return { data: null, error: null };
    }
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadSellerProducts(
  pageSize = 20,
  page = 1
): Promise<LoadResult<{ items: GwSellerProduct[]; total: number }>> {
  try {
    const res = await gw<{ items: GwSellerProduct[]; total: number }>("/api/v1/seller/products", {
      query: { page, page_size: pageSize },
    });
    return {
      data: { items: res.items ?? [], total: res.total ?? 0 },
      error: null,
    };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

/** Loads seller's own product (including PENDING/REJECTED/HIDDEN) via /api/v1/seller/products/{id}. */
export async function loadSellerProductById(
  productId: string,
  expectedShopId: string
): Promise<LoadResult<GwSellerProduct>> {
  if (!isValidResourceId(productId)) {
    return { data: null, error: null };
  }
  try {
    const res = await gw<{ product: GwSellerProduct | null }>(
      `/api/v1/seller/products/${encodeURIComponent(productId.trim())}`
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
  pageSize = 20,
  page = 1,
  includeStatusCounts = false
): Promise<
  LoadResult<{
    items: GwSellerOrder[];
    total: number;
    status_counts?: Array<{ status: string; count: number }>;
  }>
> {
  try {
    const res = await gw<{
      items: GwSellerOrder[];
      total: number;
      status_counts?: Array<{ status: string; count: number }>;
    }>("/api/v1/seller/orders", {
      query: {
        page,
        page_size: pageSize,
        include_status_counts: includeStatusCounts ? "true" : undefined,
      },
    });
    return {
      data: {
        items: (res.items ?? []).map(normalizeSellerOrder),
        total: res.total ?? 0,
        status_counts: res.status_counts,
      },
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
  if (!isValidResourceId(orderId)) {
    return { data: null, error: null };
  }
  try {
    const res = await gw<{ order: GwSellerOrder | null }>(
      `/api/v1/seller/orders/${encodeURIComponent(orderId.trim())}`
    );
    const order = res.order ?? null;
    if (!order || order.shop_id !== expectedShopId) {
      return { data: null, error: null };
    }
    return { data: normalizeSellerOrder(order), error: null };
  } catch (err) {
    if (err instanceof GatewayError && (err.kind === "not_found" || err.kind === "forbidden")) {
      return { data: null, error: null };
    }
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadSellerOrderShipment(
  orderId: string
): Promise<LoadResult<SellerShipmentPayload>> {
  if (!isValidResourceId(orderId)) {
    return { data: null, error: null };
  }
  try {
    const res = await gw<unknown>(
      `/api/v1/shipping/shipments/${encodeURIComponent(orderId.trim())}`
    );
    return { data: normalizeSellerShipmentResponse(res), error: null };
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "not_found") {
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

export async function loadSellerBankAccount(): Promise<LoadResult<GwSellerBankAccount>> {
  try {
    const res = await gw<{ bank_account: GwSellerBankAccount | null }>(
      "/api/v1/seller/bank-account"
    );
    return { data: res.bank_account ?? null, error: null };
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "not_found") {
      return { data: null, error: null };
    }
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadSellerShippingSettings(): Promise<LoadResult<GwShopShippingSettings>> {
  try {
    const res = await gw<{ settings: GwShopShippingSettings | null }>(
      "/api/v1/seller/shipping-settings"
    );
    return { data: res.settings ?? null, error: null };
  } catch (err) {
    if (err instanceof GatewayError && err.kind === "not_found") {
      return { data: null, error: null };
    }
    return { data: null, error: safeLoadError(err) };
  }
}

/** Loads seller categories with commission_rate_bps and is_prohibited metadata. */
export async function loadSellerCategories(): Promise<LoadResult<GwCategory[]>> {
  try {
    const res = await gw<{ categories: GwCategory[] }>("/api/v1/seller/categories");
    return { data: res.categories ?? [], error: null };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadCategories(): Promise<LoadResult<GwCategory[]>> {
  const sellerRes = await loadSellerCategories();
  if (!sellerRes.error && sellerRes.data) {
    return sellerRes;
  }
  try {
    const res = await gw<{ categories: GwCategory[] }>("/api/v1/categories", { auth: "none" });
    return { data: res.categories ?? [], error: null };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadSellerVouchers(): Promise<LoadResult<GwSellerVoucher[]>> {
  try {
    const res = await gw<{ vouchers: GwSellerVoucher[] }>("/api/v1/seller/vouchers", {
      query: { include_inactive: "true" },
    });
    return { data: res.vouchers ?? [], error: null };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadFlashSales(): Promise<LoadResult<GwFlashSale[]>> {
  try {
    const res = await gw<{ flash_sales?: GwFlashSale[]; items?: GwFlashSale[] }>(
      "/api/v1/seller/flash-sales",
      { query: { page: 1, page_size: 50 } }
    );
    return { data: res.flash_sales ?? res.items ?? [], error: null };
  } catch {
    try {
      const fallback = await gw<{ flash_sales: GwFlashSale[] }>("/api/v1/flash-sales", {
        query: { include_upcoming: true },
      });
      return { data: fallback.flash_sales ?? [], error: null };
    } catch (err) {
      return { data: null, error: safeLoadError(err) };
    }
  }
}

export async function loadSellerFlashSaleItems(): Promise<LoadResult<GwFlashSaleItem[]>> {
  try {
    const res = await gw<{ items: GwFlashSaleItem[] }>("/api/v1/seller/flash-sales/items", {
      query: { page: 1, page_size: 50 },
    });
    return { data: res.items ?? [], error: null };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadSellerDiscountPrograms(): Promise<LoadResult<GwShopDiscountProgram[]>> {
  try {
    const res = await gw<{ items: GwShopDiscountProgram[] }>("/api/v1/seller/discount-programs", {
      query: { page: 1, page_size: 50 },
    });
    return { data: res.items ?? [], error: null };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadSellerReturns(
  status?: string,
  page = 1,
  pageSize = 20
): Promise<LoadResult<{ items: GwSellerReturnRequest[]; total: number }>> {
  try {
    const res = await gw<{ items: GwSellerReturnRequest[]; total: number }>(
      "/api/v1/seller/returns",
      {
        query: {
          status: status && status !== "ALL" ? status : undefined,
          page,
          page_size: pageSize,
        },
      }
    );
    return {
      data: { items: res.items ?? [], total: res.total ?? 0 },
      error: null,
    };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadSellerPendingReturnsCount(): Promise<number> {
  try {
    const res = await gw<{ items: GwSellerReturnRequest[]; total: number }>(
      "/api/v1/seller/returns",
      {
        query: { status: "PENDING", page: 1, page_size: 1 },
      }
    );
    return Math.max(0, Math.trunc(res.total ?? res.items?.length ?? 0));
  } catch {
    return 0;
  }
}

export async function loadSellerAddresses(): Promise<LoadResult<SellerAddress[]>> {
  try {
    const res = await gw<{ addresses?: SellerAddress[] }>("/api/v1/users/me/addresses");
    return { data: res.addresses ?? [], error: null };
  } catch (err) {
    return { data: [], error: safeLoadError(err) };
  }
}

export async function loadSellerReport(
  fromDate?: string,
  toDate?: string,
  topLimit = 10
): Promise<LoadResult<GwSellerReport>> {
  try {
    const res = await gw<{ report?: GwSellerReport } | GwSellerReport>("/api/v1/seller/reports", {
      query: {
        from_date: fromDate || undefined,
        to_date: toDate || undefined,
        top_limit: topLimit,
      },
    });
    const report =
      res && typeof res === "object" && "report" in res && res.report
        ? res.report
        : (res as GwSellerReport);
    return { data: report ?? null, error: null };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}
