// NexusMart Vietnamese currency, number, and date formatting utilities.
// All monetary amounts are strictly integer VND.

const TRUE_MINUS = "\u2212";

function formatGroupedInteger(absInt: number): string {
  const digits = String(Math.round(Math.abs(absInt)));
  let result = "";
  for (let i = 0; i < digits.length; i++) {
    if (i > 0 && (digits.length - i) % 3 === 0) {
      result += ".";
    }
    result += digits[i];
  }
  return result;
}

/**
 * Formats an integer VND amount as `199.000₫` (zero as `0₫`, negative as `-50.000₫`).
 */
export function formatVnd(amount: number): string {
  if (!Number.isFinite(amount)) return "0₫";
  const intAmount = Math.trunc(amount);
  const grouped = formatGroupedInteger(intAmount);
  if (intAmount < 0) {
    return `-${grouped}₫`;
  }
  return `${grouped}₫`;
}

/**
 * Formats a signed VND delta with explicit + or true minus (`+1.250.000₫`, `−420.000₫`, `0₫`).
 */
export function formatSignedVnd(delta: number): string {
  if (!Number.isFinite(delta)) return "0₫";
  const intDelta = Math.trunc(delta);
  if (intDelta === 0) return "0₫";
  const prefix = intDelta > 0 ? "+" : TRUE_MINUS;
  return `${prefix}${formatGroupedInteger(intDelta)}₫`;
}

/**
 * Compact VND string for chart y-axis ticks (`12,5 tr₫`, `450k₫`, `0₫`).
 */
export function formatCompactVnd(amount: number): string {
  if (!Number.isFinite(amount)) return "0₫";
  const intAmount = Math.trunc(amount);
  const abs = Math.abs(intAmount);
  const sign = intAmount < 0 ? TRUE_MINUS : "";
  if (abs === 0) return "0₫";
  if (abs >= 999_950_000) {
    const val = (abs / 1_000_000_000).toFixed(1).replace(".", ",").replace(/,0$/, "");
    return `${sign}${val} tỷ₫`;
  }
  if (abs >= 999_500) {
    const val = (abs / 1_000_000).toFixed(1).replace(".", ",").replace(/,0$/, "");
    return `${sign}${val} tr₫`;
  }
  if (abs >= 1_000) {
    const val = Math.round(abs / 1_000);
    return `${sign}${formatGroupedInteger(val)}k₫`;
  }
  return `${sign}${abs}₫`;
}

/**
 * Formats an integer count with Vietnamese dot thousands separators (`1.240`).
 */
export function formatInt(value: number): string {
  if (!Number.isFinite(value)) return "0";
  const intVal = Math.trunc(value);
  const grouped = formatGroupedInteger(intVal);
  return intVal < 0 ? `${TRUE_MINUS}${grouped}` : grouped;
}

/**
 * Formats a signed integer delta (`+14`, `−3`, `0`).
 */
export function formatSignedInt(delta: number): string {
  if (!Number.isFinite(delta)) return "0";
  const intVal = Math.trunc(delta);
  if (intVal === 0) return "0";
  return `${intVal > 0 ? "+" : TRUE_MINUS}${formatGroupedInteger(intVal)}`;
}

/**
 * Formats a percentage ratio (`0.124` -> `12,4%`) or already-percentage number if `isRatio = false`.
 */
export function formatPercent(value: number, digits = 1, isRatio = true): string {
  const safeDigits = Number.isFinite(digits) ? Math.max(0, Math.trunc(digits)) : 1;
  if (!Number.isFinite(value)) {
    return `${(0).toFixed(safeDigits).replace(".", ",")}%`;
  }
  const pct = isRatio ? value * 100 : value;
  return `${pct.toFixed(safeDigits).replace(".", ",")}%`;
}

/**
 * Formats discount percentage between originalPrice and salePrice (`-22%`).
 */
export function formatDiscountPercent(originalPrice: number, salePrice: number): string {
  if (
    !Number.isFinite(originalPrice) ||
    !Number.isFinite(salePrice) ||
    originalPrice <= 0 ||
    salePrice >= originalPrice
  ) {
    return "0%";
  }
  const pct = Math.min(100, Math.round(((originalPrice - salePrice) / originalPrice) * 100));
  if (pct <= 0) return "0%";
  return `-${pct}%`;
}

export const VN_UTC_OFFSET_MS = 7 * 60 * 60 * 1000;
const DAY_MS = 24 * 60 * 60 * 1000;

function twoDigit(n: number): string {
  return n < 10 ? `0${n}` : `${n}`;
}

export interface VnDateParts {
  year: number;
  month: number;
  day: number;
  hour: number;
  minute: number;
  second: number;
  /** Calendar date key `YYYY-MM-DD` in Asia/Ho_Chi_Minh (fixed UTC+7). */
  dateKey: string;
  /** UTC midnight timestamp (ms) representing this VN calendar day. */
  dayStartUtcMs: number;
}

/**
 * Extracts calendar and clock components in Vietnam time (Asia/Ho_Chi_Minh, fixed UTC+7, no DST).
 * Independent of the host machine's TZ so SSR (TZ=UTC) and browser (+07) always agree.
 */
export function getVnDateParts(input: string | number | Date): VnDateParts | null {
  const ms =
    typeof input === "number" ? input : input instanceof Date ? input.getTime() : Date.parse(input);
  if (!Number.isFinite(ms)) return null;
  const d = new Date(ms + VN_UTC_OFFSET_MS);
  const year = d.getUTCFullYear();
  const month = d.getUTCMonth() + 1;
  const day = d.getUTCDate();
  const hour = d.getUTCHours();
  const minute = d.getUTCMinutes();
  const second = d.getUTCSeconds();
  return {
    year,
    month,
    day,
    hour,
    minute,
    second,
    dateKey: `${year}-${twoDigit(month)}-${twoDigit(day)}`,
    dayStartUtcMs: Date.UTC(year, month - 1, day),
  };
}

/**
 * Formats an ISO timestamp string to Vietnamese short date `dd/mm/yyyy` in Asia/Ho_Chi_Minh (UTC+7).
 */
export function formatDateVi(iso: string): string {
  const p = getVnDateParts(iso);
  if (!p) return iso;
  return `${twoDigit(p.day)}/${twoDigit(p.month)}/${p.year}`;
}

/**
 * Formats an ISO timestamp string to Vietnamese date & time `HH:mm, dd/mm/yyyy` in Asia/Ho_Chi_Minh (UTC+7).
 */
export function formatDateTimeVi(iso: string): string {
  const p = getVnDateParts(iso);
  if (!p) return iso;
  return `${twoDigit(p.hour)}:${twoDigit(p.minute)}, ${twoDigit(p.day)}/${twoDigit(p.month)}/${p.year}`;
}

/**
 * Formats `dd/mm` in Asia/Ho_Chi_Minh (UTC+7) for compact chart axis labels or short timelines.
 */
export function formatDayMonthVi(iso: string): string {
  const p = getVnDateParts(iso);
  if (!p) return iso;
  return `${twoDigit(p.day)}/${twoDigit(p.month)}`;
}

/**
 * Formats `HH:mm` in Asia/Ho_Chi_Minh (UTC+7).
 */
export function formatTimeVi(input: string | number | Date): string {
  const p = getVnDateParts(input);
  if (!p) return typeof input === "string" ? input : "";
  return `${twoDigit(p.hour)}:${twoDigit(p.minute)}`;
}

/**
 * Formats `HH:mm dd/mm` in Asia/Ho_Chi_Minh (UTC+7) for dense operational tables.
 */
export function formatShortTimeDateVi(iso: string): string {
  const p = getVnDateParts(iso);
  if (!p) return iso;
  return `${twoDigit(p.hour)}:${twoDigit(p.minute)} ${twoDigit(p.day)}/${twoDigit(p.month)}`;
}

/**
 * Formats `YYYY-MM-DD` in Asia/Ho_Chi_Minh (UTC+7) for date inputs and daily bucketing.
 */
export function formatVnIsoDate(input: string | number | Date): string {
  const p = getVnDateParts(input);
  if (!p) return typeof input === "string" ? input : "";
  return p.dateKey;
}

export interface VnDayBucketMeta {
  key: string;
  label: string;
  dayStartUtcMs: number;
}

/**
 * Builds an ordered list of the past `count` Vietnam calendar days ending on the VN day of `nowMs`.
 */
export function buildVnPastDays(count: number, nowMs: number): VnDayBucketMeta[] {
  if (!Number.isFinite(count)) return [];
  const n = Math.floor(count);
  const today = getVnDateParts(nowMs);
  if (!today || n <= 0) return [];
  const days: VnDayBucketMeta[] = [];
  for (let i = n - 1; i >= 0; i--) {
    const dayStartUtcMs = today.dayStartUtcMs - i * DAY_MS;
    const d = new Date(dayStartUtcMs);
    const year = d.getUTCFullYear();
    const month = d.getUTCMonth() + 1;
    const day = d.getUTCDate();
    days.push({
      key: `${year}-${twoDigit(month)}-${twoDigit(day)}`,
      label: `${twoDigit(day)}/${twoDigit(month)}`,
      dayStartUtcMs,
    });
  }
  return days;
}
