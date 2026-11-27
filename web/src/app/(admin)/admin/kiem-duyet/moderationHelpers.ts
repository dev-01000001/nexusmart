// Pure helpers for Admin Product Moderation server-side filtering, pagination, and tab counts.
// Safe for server, client, and `node --test` (type stripping).

export const ADMIN_MODERATION_PAGE_SIZE = 15;

export const PRODUCT_MODERATION_STATUSES = [
  "PENDING",
  "APPROVED",
  "REJECTED",
  "OUT_OF_STOCK",
  "HIDDEN",
] as const;

export type ProductModerationStatus = (typeof PRODUCT_MODERATION_STATUSES)[number];
export type ProductStatusFilter = "all" | ProductModerationStatus;

export type ProductTabCounts = Record<ProductStatusFilter, number>;

export function createEmptyProductTabCounts(): ProductTabCounts {
  return {
    all: 0,
    PENDING: 0,
    APPROVED: 0,
    REJECTED: 0,
    OUT_OF_STOCK: 0,
    HIDDEN: 0,
  };
}

export function normalizeProductTabCounts(
  raw?: Partial<Record<string, number>> | null,
  fallback?: ProductTabCounts
): ProductTabCounts {
  const base = fallback ? { ...fallback } : createEmptyProductTabCounts();
  if (!raw || typeof raw !== "object") return base;
  const keys: ProductStatusFilter[] = ["all", ...PRODUCT_MODERATION_STATUSES];
  for (const key of keys) {
    const val = raw[key];
    if (typeof val === "number" && Number.isFinite(val) && val >= 0) {
      base[key] = Math.trunc(val);
    }
  }
  return base;
}

export function buildAdminProductsQuery(params: {
  status: ProductStatusFilter;
  keyword?: string;
  page: number;
  pageSize?: number;
}): string {
  const sp = new URLSearchParams();
  const safePage = Math.max(1, Math.trunc(Number.isFinite(params.page) ? params.page : 1));
  const safePageSize = Math.max(
    1,
    Math.min(100, Math.trunc(params.pageSize ?? ADMIN_MODERATION_PAGE_SIZE))
  );
  sp.set("page", String(safePage));
  sp.set("page_size", String(safePageSize));
  if (params.status && params.status !== "all") {
    sp.set("status", params.status);
  }
  const cleanKw = (params.keyword ?? "").trim().slice(0, 120);
  if (cleanKw) {
    sp.set("keyword", cleanKw);
  }
  return sp.toString();
}

export function clampModerationPage(
  page: number,
  totalItems: number,
  pageSize = ADMIN_MODERATION_PAGE_SIZE
): { page: number; totalPages: number } {
  const safeSize = Math.max(1, Math.trunc(pageSize));
  const safeTotal = Math.max(0, Math.trunc(Number.isFinite(totalItems) ? totalItems : 0));
  const totalPages = Math.max(1, Math.ceil(safeTotal / safeSize));
  const safePage = Math.max(1, Math.min(totalPages, Math.trunc(Number.isFinite(page) ? page : 1)));
  return { page: safePage, totalPages };
}

export function pickNextProductIdOnPage(
  items: ReadonlyArray<{ id: string; status: string }>,
  currentId: string,
  activeFilter: ProductStatusFilter
): string | null {
  const currentIdx = items.findIndex((p) => p.id === currentId);
  const matching = items.filter(
    (p) => activeFilter === "all" || p.status.toUpperCase() === activeFilter
  );
  if (matching.length === 0) return null;

  for (let i = Math.max(0, currentIdx + 1); i < items.length; i++) {
    const cand = items[i];
    if (cand.id !== currentId && cand.status.toUpperCase() === "PENDING") {
      return cand.id;
    }
  }
  const afterSame = currentIdx >= 0 ? items[currentIdx + 1] : undefined;
  if (afterSame && matching.some((p) => p.id === afterSame.id)) {
    return afterSame.id;
  }
  const other = matching.find((p) => p.id !== currentId);
  return other?.id ?? matching[0].id;
}
