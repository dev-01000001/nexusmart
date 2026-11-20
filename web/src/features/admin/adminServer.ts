// Server-only Admin Portal helpers: strict RBAC + safe gateway loaders.
import { GatewayError, SAFE_MESSAGES } from "@/lib/server/errors";
import { gw } from "@/lib/server/gateway";
import { ADMIN_ROLES, assertRole, requireRole, type Session } from "@/lib/server/session";
import type { SalesChannelFilter } from "@/lib/salesChannel";
import {
  normalizeAdminOrder,
  type GwAdminFlashSale,
  type GwAdminOrder,
  type GwAdminProduct,
  type GwAdminShop,
  type GwAdminVoucher,
  type GwAuditLog,
  type GwLedgerSummary,
} from "./adminApiTypes";

export interface AdminLoadResult<T> {
  data: T | null;
  error: string | null;
}

/** Page / layout server guard: requires backend role admin or super_admin. */
export async function requireAdminPage(): Promise<Session> {
  return requireRole(...ADMIN_ROLES);
}

/** Route handler guard: requires backend role admin or super_admin. */
export async function assertAdminSession(): Promise<Session> {
  return assertRole(...ADMIN_ROLES);
}

function safeLoadError(err: unknown): string {
  if (err instanceof GatewayError) {
    return SAFE_MESSAGES[err.kind];
  }
  return SAFE_MESSAGES.unavailable;
}

export async function loadAdminShops(
  status?: string,
  pageSize = 100
): Promise<AdminLoadResult<{ items: GwAdminShop[]; total: number }>> {
  try {
    const res = await gw<{ items: GwAdminShop[]; total: number }>("/api/v1/admin/shops", {
      query: { status: status || undefined, page: 1, page_size: pageSize },
    });
    return { data: { items: res.items ?? [], total: res.total ?? 0 }, error: null };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadAdminOrders(
  pageSize = 100,
  salesChannel?: SalesChannelFilter
): Promise<AdminLoadResult<{ items: GwAdminOrder[]; total: number }>> {
  try {
    const res = await gw<{ items: GwAdminOrder[]; total: number }>("/api/v1/admin/orders", {
      query: {
        page: 1,
        page_size: pageSize,
        sales_channel: salesChannel || undefined,
      },
    });
    return {
      data: {
        items: (res.items ?? []).map(normalizeAdminOrder),
        total: res.total ?? 0,
      },
      error: null,
    };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadAdminLedgerSummary(
  salesChannel?: SalesChannelFilter
): Promise<AdminLoadResult<GwLedgerSummary>> {
  try {
    const res = await gw<GwLedgerSummary>("/api/v1/admin/ledger/summary", {
      query: { sales_channel: salesChannel || undefined },
    });
    return {
      data: {
        total_debits_vnd: res.total_debits_vnd ?? 0,
        total_credits_vnd: res.total_credits_vnd ?? 0,
        is_balanced: Boolean(res.is_balanced),
        sales_channel: res.sales_channel ?? (salesChannel || ""),
        accounts: res.accounts ?? [],
      },
      error: null,
    };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadAdminAuditLogs(
  pageSize = 100
): Promise<AdminLoadResult<{ items: GwAuditLog[]; total: number }>> {
  try {
    const res = await gw<{ items: GwAuditLog[]; total: number }>("/api/v1/admin/audit-logs", {
      query: { page: 1, page_size: pageSize },
    });
    return { data: { items: res.items ?? [], total: res.total ?? 0 }, error: null };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadAdminProducts(
  status?: string,
  pageSize = 100
): Promise<AdminLoadResult<{ items: GwAdminProduct[]; total: number }>> {
  try {
    const res = await gw<{ items: GwAdminProduct[]; total: number }>("/api/v1/admin/products", {
      query: { status: status || undefined, page: 1, page_size: pageSize },
    });
    return { data: { items: res.items ?? [], total: res.total ?? 0 }, error: null };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadAdminVouchers(): Promise<AdminLoadResult<GwAdminVoucher[]>> {
  try {
    const res = await gw<{ vouchers: GwAdminVoucher[] }>("/api/v1/vouchers");
    return { data: res.vouchers ?? [], error: null };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}

export async function loadAdminFlashSales(): Promise<AdminLoadResult<GwAdminFlashSale[]>> {
  try {
    const res = await gw<{ flash_sales: GwAdminFlashSale[] }>("/api/v1/flash-sales", {
      query: { include_upcoming: true },
    });
    return { data: res.flash_sales ?? [], error: null };
  } catch (err) {
    return { data: null, error: safeLoadError(err) };
  }
}
