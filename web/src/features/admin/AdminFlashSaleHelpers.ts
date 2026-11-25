// Pure validation and display helpers for Admin Flash Sale (/admin/flash-sale).
// Zero I/O, zero DOM, safe for server components, route handlers, client components, and Node tests.
import type { PillTone } from "./adminMeta.ts";
import type { ValidationResult } from "./adminComplaintsTypes.ts";
import { isValidAdminPathId } from "./AdminReturnsHelpers.ts";

export const MAX_FLASH_SALE_DISCOUNT_PERCENT = 90;
export const MAX_FLASH_SALE_CATEGORIES = 50;

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
