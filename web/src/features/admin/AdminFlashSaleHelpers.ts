// Pure validation and display helpers for Admin Flash Sale (/admin/flash-sale).
// Zero I/O, zero DOM, safe for server components, route handlers, client components, and Node tests.
import { formatInt } from "../../lib/format.ts";
import type { PillTone } from "./adminMeta.ts";
import type { ValidationResult } from "./adminComplaintsTypes.ts";
import { isValidAdminPathId } from "./AdminReturnsHelpers.ts";

export const MAX_FLASH_SALE_DISCOUNT_PERCENT = 90;
export const MAX_FLASH_SALE_CATEGORIES = 50;
export const FLASH_SALE_ITEMS_PAGE_SIZE = 100;
export const FLASH_SALE_ITEMS_MAX_PAGES = 5;
export const FLASH_SALE_PENDING_MAX_PAGES = 100;

const ALLOWED_FLASH_SALE_ITEM_STATUSES = new Set(["PENDING", "APPROVED", "REJECTED"]);

export function validateFlashSaleItemStatusFilter(raw: string | null | undefined): string | null {
  const clean = (raw ?? "").trim().toUpperCase();
  if (!clean) return "";
  return ALLOWED_FLASH_SALE_ITEM_STATUSES.has(clean) ? clean : null;
}

export interface PaginatedFlashSaleItemsResult<T> {
  items: T[];
  total: number;
  truncated: boolean;
  unloadedCount: number;
}

export interface FlashSaleItemsLoadMeta {
  loadedCount: number;
  totalCount: number;
  unloadedCount: number;
  pendingUnloadedCount: number;
}

export interface AdminFlashSaleItemsLoadResult<T> {
  items: T[];
  meta: FlashSaleItemsLoadMeta;
  error: string | null;
}

export async function collectPaginatedFlashSaleItemsWithMeta<T>(
  fetchPage: (
    page: number,
    pageSize: number
  ) => Promise<{ items?: T[]; total?: number } | null | undefined>,
  opts?: { pageSize?: number; maxPages?: number }
): Promise<PaginatedFlashSaleItemsResult<T>> {
  const pageSize = Math.max(
    1,
    Math.min(100, Math.trunc(opts?.pageSize ?? FLASH_SALE_ITEMS_PAGE_SIZE))
  );
  const maxPages = Math.max(1, Math.trunc(opts?.maxPages ?? FLASH_SALE_ITEMS_MAX_PAGES));
  const collected: T[] = [];
  let reportedTotal = 0;

  for (let page = 1; page <= maxPages; page += 1) {
    const res = await fetchPage(page, pageSize);
    const batch = Array.isArray(res?.items) ? res.items : [];
    if (typeof res?.total === "number" && Number.isFinite(res.total) && res.total > reportedTotal) {
      reportedTotal = Math.trunc(res.total);
    }
    if (batch.length === 0) break;
    collected.push(...batch);
    if ((reportedTotal > 0 && collected.length >= reportedTotal) || batch.length < pageSize) break;
  }

  const total = Math.max(reportedTotal, collected.length);
  const unloadedCount = Math.max(0, total - collected.length);
  return {
    items: collected,
    total,
    truncated: unloadedCount > 0,
    unloadedCount,
  };
}

export async function collectPaginatedFlashSaleItems<T>(
  fetchPage: (
    page: number,
    pageSize: number
  ) => Promise<{ items?: T[]; total?: number } | null | undefined>,
  opts?: { pageSize?: number; maxPages?: number }
): Promise<T[]> {
  const result = await collectPaginatedFlashSaleItemsWithMeta(fetchPage, opts);
  return result.items;
}

/**
 * Loads admin Flash sale item registrations via two parallel queries:
 * 1) Full `status=PENDING` pagination so older pending slots are never pushed off by history.
 * 2) Recent all-status pages for APPROVED/REJECTED history.
 * Uses `Promise.allSettled` so a failure in one sub-fetch never discards the other or crashes the page.
 */
export async function loadAdminFlashSaleItems<T extends { id: string }>(
  fetchItems: (params: {
    status?: "PENDING";
    page: number;
    pageSize: number;
  }) => Promise<{ items?: T[]; total?: number } | null | undefined>,
  opts?: { pageSize?: number; allMaxPages?: number; pendingMaxPages?: number }
): Promise<AdminFlashSaleItemsLoadResult<T>> {
  const pageSize = opts?.pageSize ?? FLASH_SALE_ITEMS_PAGE_SIZE;
  const [pendingSettled, allSettled] = await Promise.allSettled([
    collectPaginatedFlashSaleItemsWithMeta(
      (page, size) => fetchItems({ status: "PENDING", page, pageSize: size }),
      { pageSize, maxPages: opts?.pendingMaxPages ?? FLASH_SALE_PENDING_MAX_PAGES }
    ),
    collectPaginatedFlashSaleItemsWithMeta((page, size) => fetchItems({ page, pageSize: size }), {
      pageSize,
      maxPages: opts?.allMaxPages ?? FLASH_SALE_ITEMS_MAX_PAGES,
    }),
  ]);

  const seen = new Set<string>();
  const items: T[] = [];
  const appendUnique = (list: readonly T[]) => {
    for (const item of list) {
      if (!item?.id || seen.has(item.id)) continue;
      seen.add(item.id);
      items.push(item);
    }
  };

  if (pendingSettled.status === "fulfilled") {
    appendUnique(pendingSettled.value.items);
  }
  if (allSettled.status === "fulfilled") {
    appendUnique(allSettled.value.items);
  }

  const pendingTotal = pendingSettled.status === "fulfilled" ? pendingSettled.value.total : 0;
  const allTotal = allSettled.status === "fulfilled" ? allSettled.value.total : 0;
  const pendingUnloadedCount =
    pendingSettled.status === "fulfilled" ? pendingSettled.value.unloadedCount : 0;
  const totalCount = Math.max(allTotal, pendingTotal, items.length);
  const unloadedCount = Math.max(pendingUnloadedCount, Math.max(0, totalCount - items.length));

  let error: string | null = null;
  if (pendingSettled.status === "rejected" && allSettled.status === "rejected") {
    error = "Không thể tải danh sách suất đăng ký Flash sale lúc này.";
  } else if (allSettled.status === "rejected") {
    error =
      "Không thể tải đầy đủ danh sách suất đã xử lý; các suất chờ duyệt vẫn hiển thị bên dưới.";
  } else if (pendingSettled.status === "rejected") {
    error =
      "Không thể tải riêng toàn bộ suất chờ duyệt; một số suất chờ duyệt cũ hơn có thể chưa hiển thị.";
  }

  return {
    items,
    meta: {
      loadedCount: items.length,
      totalCount,
      unloadedCount,
      pendingUnloadedCount,
    },
    error,
  };
}

export function formatFlashSaleItemsNotice(
  meta: FlashSaleItemsLoadMeta | null | undefined
): string | null {
  if (!meta || meta.unloadedCount <= 0) return null;
  if (meta.pendingUnloadedCount > 0) {
    return `Còn ${formatInt(meta.unloadedCount)} suất đăng ký chưa tải (gồm ${formatInt(meta.pendingUnloadedCount)} suất chờ duyệt).`;
  }
  return `Còn ${formatInt(meta.unloadedCount)} suất đăng ký cũ hơn chưa tải; toàn bộ suất chờ duyệt đã được tải đầy đủ.`;
}

/**
 * Merges all-status Flash sale items from `GET /api/v1/admin/flash-sale-items` into the
 * flash sale windows returned by `GET /api/v1/admin/flash-sales` (which only embeds APPROVED items).
 * Deduplicates by `item.id`, preferring fresh records from `allItems`.
 */
export function mergeFlashSaleItems<
  TItem extends { id: string; flash_sale_id: string },
  TSale extends { id: string; items?: TItem[] },
>(flashSales: readonly TSale[], allItems: readonly TItem[]): TSale[] {
  const byFlashSaleId = new Map<string, TItem[]>();
  for (const item of allItems) {
    if (!item?.id || !item.flash_sale_id) continue;
    let list = byFlashSaleId.get(item.flash_sale_id);
    if (!list) {
      list = [];
      byFlashSaleId.set(item.flash_sale_id, list);
    }
    list.push(item);
  }

  return flashSales.map((fs) => {
    const seen = new Set<string>();
    const merged: TItem[] = [];

    for (const item of byFlashSaleId.get(fs.id) ?? []) {
      if (seen.has(item.id)) continue;
      seen.add(item.id);
      merged.push(item);
    }
    for (const existing of fs.items ?? []) {
      if (!existing?.id || seen.has(existing.id)) continue;
      seen.add(existing.id);
      merged.push(existing);
    }

    return {
      ...fs,
      items: merged,
    };
  });
}

export interface ValidatedApproveFlashSaleItemInput {
  approve: boolean;
  reason: string;
}

export function validateApproveFlashSaleItemInput(
  input: unknown
): ValidationResult<ValidatedApproveFlashSaleItemInput> {
  const rec = asRecord(input);
  if (typeof rec.approve !== "boolean") {
    return {
      ok: false,
      code: "INVALID_ARGUMENT",
      message: "Quyết định duyệt sản phẩm Flash sale phải là giá trị đúng/sai hợp lệ.",
    };
  }
  return {
    ok: true,
    value: {
      approve: rec.approve,
      reason: asString(rec.reason),
    },
  };
}

export interface ValidatedCreateFlashSaleInput {
  name: string;
  starts_at: string;
  ends_at: string;
  min_discount_percent: number;
  category_ids: string[];
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function asString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

export function validateCreateFlashSaleInput(
  input: unknown
): ValidationResult<ValidatedCreateFlashSaleInput> {
  const rec = asRecord(input);
  const name = asString(rec.name);
  if (name.length < 2 || name.length > 160) {
    return {
      ok: false,
      code: "INVALID_FLASH_SALE_NAME",
      message: "Tên khung giờ cần từ 2 đến 160 ký tự.",
    };
  }

  const startsAt = asString(rec.starts_at);
  const endsAt = asString(rec.ends_at);
  const startMs = Date.parse(startsAt);
  const endMs = Date.parse(endsAt);
  if (!startsAt || !endsAt || !Number.isFinite(startMs) || !Number.isFinite(endMs)) {
    return {
      ok: false,
      code: "INVALID_FLASH_SALE_WINDOW",
      message: "Thời gian bắt đầu và kết thúc khung giờ chưa hợp lệ.",
    };
  }
  if (endMs <= startMs) {
    return {
      ok: false,
      code: "INVALID_FLASH_SALE_WINDOW",
      message: "Thời gian kết thúc phải sau thời gian bắt đầu.",
    };
  }

  let minDiscount = 0;
  if (
    rec.min_discount_percent !== undefined &&
    rec.min_discount_percent !== null &&
    rec.min_discount_percent !== ""
  ) {
    const raw =
      typeof rec.min_discount_percent === "string"
        ? Number(rec.min_discount_percent.trim())
        : rec.min_discount_percent;
    if (
      typeof raw !== "number" ||
      !Number.isInteger(raw) ||
      raw < 0 ||
      raw > MAX_FLASH_SALE_DISCOUNT_PERCENT
    ) {
      return {
        ok: false,
        code: "INVALID_MIN_DISCOUNT",
        message: `Mức giảm tối thiểu phải là số nguyên từ 0 đến ${MAX_FLASH_SALE_DISCOUNT_PERCENT}%.`,
      };
    }
    minDiscount = raw;
  }

  const categoryIds: string[] = [];
  if (rec.category_ids !== undefined && rec.category_ids !== null) {
    if (!Array.isArray(rec.category_ids)) {
      return {
        ok: false,
        code: "INVALID_CATEGORY_IDS",
        message: "Danh sách danh mục áp dụng không hợp lệ.",
      };
    }
    if (rec.category_ids.length > MAX_FLASH_SALE_CATEGORIES) {
      return {
        ok: false,
        code: "INVALID_CATEGORY_IDS",
        message: `Chỉ được chọn tối đa ${MAX_FLASH_SALE_CATEGORIES} danh mục cho một khung giờ.`,
      };
    }
    const seen = new Set<string>();
    for (const item of rec.category_ids) {
      if (typeof item !== "string" || !isValidAdminPathId(item)) {
        return {
          ok: false,
          code: "INVALID_CATEGORY_IDS",
          message: "Mã danh mục áp dụng không hợp lệ.",
        };
      }
      const clean = item.trim();
      if (!seen.has(clean)) {
        seen.add(clean);
        categoryIds.push(clean);
      }
    }
  }

  return {
    ok: true,
    value: {
      name,
      starts_at: new Date(startMs).toISOString(),
      ends_at: new Date(endMs).toISOString(),
      min_discount_percent: minDiscount,
      category_ids: categoryIds,
    },
  };
}

export function getFlashSaleWindowMeta(
  fs: { status?: string; starts_at?: string; ends_at?: string },
  nowMs: number
): { label: string; tone: PillTone; state: "UPCOMING" | "ACTIVE" | "ENDED" } {
  const explicit = (fs.status ?? "").trim().toUpperCase();
  if (explicit === "ENDED") {
    return { label: "Đã kết thúc", tone: "neutral", state: "ENDED" };
  }
  const start = fs.starts_at ? Date.parse(fs.starts_at) : Number.NaN;
  const end = fs.ends_at ? Date.parse(fs.ends_at) : Number.NaN;
  if (!Number.isNaN(end) && end <= nowMs) {
    return { label: "Đã kết thúc", tone: "neutral", state: "ENDED" };
  }
  if (explicit === "ACTIVE" || (!Number.isNaN(start) && start <= nowMs)) {
    return { label: "Đang diễn ra", tone: "positive", state: "ACTIVE" };
  }
  return { label: "Sắp diễn ra", tone: "warning", state: "UPCOMING" };
}

export function canEndFlashSaleEarly(
  fs: { status?: string; starts_at?: string; ends_at?: string },
  nowMs: number
): boolean {
  return getFlashSaleWindowMeta(fs, nowMs).state !== "ENDED";
}

export function computeFlashDiscountPercent(
  originalPriceVnd: number | undefined,
  flashPriceVnd: number
): number | null {
  if (
    typeof originalPriceVnd !== "number" ||
    !Number.isFinite(originalPriceVnd) ||
    originalPriceVnd <= 0 ||
    !Number.isFinite(flashPriceVnd) ||
    flashPriceVnd >= originalPriceVnd
  ) {
    return null;
  }
  return Math.min(99, Math.round(((originalPriceVnd - flashPriceVnd) / originalPriceVnd) * 100));
}
