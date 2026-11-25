import type { GatewayOrderStatus, GatewayPaymentMethod, GatewayVoucher } from "./gateway-types";

// NexusMart Vietnamese currency, number, date, and shared gateway label utilities.
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
 * Formats basis points (`10000` = 100%, `500` = `5%`, `125` = `1,25%`, `0` = `0%`).
 */
export function formatBpsPercent(bps: number): string {
  const safe = Number.isFinite(bps) ? Math.trunc(bps) : 0;
  const pct = safe / 100;
  if (Number.isInteger(pct)) {
    return `${pct}%`;
  }
  const formatted = pct.toFixed(2).replace(/0+$/, "").replace(/\.$/, "").replace(".", ",");
  return `${formatted}%`;
}

/**
 * Formats integer average rating scaled by 100 (0..500 -> `"4,8"`, `"5,0"`, `"0,0"`).
 */
export function formatRatingFromX100(ratingAvgX100: number | undefined | null): string {
  const safe =
    typeof ratingAvgX100 === "number" && Number.isFinite(ratingAvgX100)
      ? Math.max(0, Math.min(500, Math.trunc(ratingAvgX100)))
      : 0;
  const tenths = Math.round(safe / 10);
  const whole = Math.floor(tenths / 10);
  const frac = tenths % 10;
  return `${whole},${frac}`;
}

const ISO_DATE_RE = /^\d{4}-\d{2}-\d{2}$/;
export const MAX_REPORT_RANGE_DAYS = 366;

/**
 * Returns `YYYY-MM-DD` in Asia/Ho_Chi_Minh shifted by `deltaDays` from `baseMs` (default now).
 */
export function formatVnDateOffset(deltaDays: number, baseMs = Date.now()): string {
  return formatVnDayKey(baseMs + Math.trunc(deltaDays) * 24 * 60 * 60 * 1000);
}

function parseIsoCalendarDateUtcMs(raw: string): number | null {
  if (!ISO_DATE_RE.test(raw)) return null;
  const [yStr, mStr, dStr] = raw.split("-");
  const y = Number(yStr);
  const m = Number(mStr);
  const d = Number(dStr);
  if (m < 1 || m > 12 || d < 1 || d > 31) return null;
  const dt = new Date(Date.UTC(y, m - 1, d));
  if (dt.getUTCFullYear() !== y || dt.getUTCMonth() !== m - 1 || dt.getUTCDate() !== d) {
    return null;
  }
  return dt.getTime();
}

/**
 * Validates report date range matching gateway/order rules: `YYYY-MM-DD`, `from <= to`, inclusive span <= 366 days.
 */
export function validateReportDateRange(
  fromDate: string,
  toDate: string
): { ok: true; days: number } | { ok: false; message: string } {
  const f = fromDate.trim();
  const t = toDate.trim();
  if (!ISO_DATE_RE.test(f) || !ISO_DATE_RE.test(t)) {
    return { ok: false, message: "Ngày báo cáo phải đúng định dạng YYYY-MM-DD." };
  }
  const fMs = parseIsoCalendarDateUtcMs(f);
  const tMs = parseIsoCalendarDateUtcMs(t);
  if (fMs === null || tMs === null) {
    return { ok: false, message: "Ngày báo cáo không hợp lệ." };
  }
  if (fMs > tMs) {
    return { ok: false, message: "Ngày bắt đầu phải trước hoặc bằng ngày kết thúc." };
  }
  const days = Math.floor((tMs - fMs) / (24 * 60 * 60 * 1000)) + 1;
  if (days > MAX_REPORT_RANGE_DAYS) {
    return {
      ok: false,
      message: `Khoảng thời gian báo cáo tối đa ${MAX_REPORT_RANGE_DAYS} ngày (đang chọn ${days} ngày).`,
    };
  }
  return { ok: true, days };
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
 * Formats `HH:mm` in Asia/Ho_Chi_Minh (UTC+7); returns `--:--` for missing or invalid timestamps.
 */
export function formatTimeHmVn(iso: string | undefined): string {
  if (!iso) return "--:--";
  const p = getVnDateParts(iso);
  if (!p) return "--:--";
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

/**
 * Formats `YYYY-MM-DD` calendar date key in Asia/Ho_Chi_Minh (UTC+7).
 */
export function formatVnDayKey(input: string | number | Date): string {
  return formatVnIsoDate(input);
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

/**
 * Shared Vietnamese labels for gateway order statuses (single source of truth across buyer, seller & admin).
 */
export const GATEWAY_ORDER_STATUS_LABELS: Record<GatewayOrderStatus, string> = {
  PENDING: "Chờ thanh toán",
  CONFIRMED: "Đã xác nhận",
  PREPARING: "Đang chuẩn bị",
  SHIPPING: "Đang giao",
  DELIVERED: "Đã giao",
  COMPLETED: "Hoàn tất",
  CANCELLED: "Đã hủy",
  RETURN_REQUESTED: "Yêu cầu trả hàng",
  RETURNED: "Đã trả hàng",
};

export const GATEWAY_SHIPMENT_STATUS_LABELS: Record<string, string> = {
  QUEUED: "Đang tạo vận đơn (tự thử lại)",
  CREATED: "Đã tạo vận đơn",
  PICKING: "Đang lấy hàng",
  DELIVERING: "Đang vận chuyển",
  DELIVERED: "Giao thành công",
  FAILED: "Giao thất bại",
  RETURNING: "Đang hoàn hàng",
  RETURNED: "Đã hoàn về kho",
  CANCELLED: "Đã hủy vận đơn",
};

export const GATEWAY_PAYMENT_METHOD_LABELS: Record<GatewayPaymentMethod, string> = {
  COD: "Thanh toán khi nhận hàng (COD)",
  VIETQR: "Chuyển khoản VietQR",
  ZALOPAY: "Ví điện tử ZaloPay",
};

/**
 * Single source of truth for Vietnamese shipping carrier display labels across buyer, seller & admin.
 * - `ghn` -> "Giao Hàng Nhanh (GHN)"
 * - `mock_express` -> "Chuyển phát Nexus"
 * - other codes -> API-provided `carrierName` (if any), else code fallback.
 */
export function formatCarrierLabel(
  carrierCode: string | undefined,
  carrierName?: string | null
): string {
  const code = (carrierCode ?? "").trim().toLowerCase();
  if (code === "ghn") return "Giao Hàng Nhanh (GHN)";
  if (code === "mock_express") return "Chuyển phát Nexus";
  const cleanName = (carrierName ?? "").trim();
  if (cleanName) {
    if (/mock[\s_-]*express/i.test(cleanName)) return "Chuyển phát Nexus";
    if (/^giao\s+hang\s+nhanh(?:\s*\(ghn\))?$/i.test(cleanName)) {
      return "Giao Hàng Nhanh (GHN)";
    }
    return cleanName;
  }
  return (carrierCode ?? "").trim() || "Đơn vị vận chuyển";
}

export function formatVoucherBenefit(
  voucher: Pick<
    GatewayVoucher,
    "discount_type" | "discount_percent" | "max_discount_vnd" | "value_vnd" | "name" | "code"
  >
): string {
  if (voucher.discount_type.toUpperCase() === "PERCENT" && voucher.discount_percent > 0) {
    const cap =
      voucher.max_discount_vnd > 0 ? ` (tối đa ${formatVnd(voucher.max_discount_vnd)})` : "";
    return `Giảm ${voucher.discount_percent}%${cap}`;
  }
  if (voucher.value_vnd > 0) {
    return `Giảm ${formatVnd(voucher.value_vnd)}`;
  }
  return voucher.name || voucher.code;
}
