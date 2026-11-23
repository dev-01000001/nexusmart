import type { GatewayOrderStatus, GatewayPaymentMethod, GatewayVoucher } from "@/lib/gateway-types";

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
  const intDelta = Math.trunc(delta);
  if (intDelta === 0) return "0₫";
  const prefix = intDelta > 0 ? "+" : TRUE_MINUS;
  return `${prefix}${formatGroupedInteger(intDelta)}₫`;
}

/**
 * Compact VND string for chart y-axis ticks (`12,5 tr₫`, `450k₫`, `0₫`).
 */
export function formatCompactVnd(amount: number): string {
  const intAmount = Math.trunc(amount);
  const abs = Math.abs(intAmount);
  const sign = intAmount < 0 ? TRUE_MINUS : "";
  if (abs === 0) return "0₫";
  if (abs >= 1_000_000_000) {
    const val = (abs / 1_000_000_000).toFixed(1).replace(".", ",").replace(/,0$/, "");
    return `${sign}${val} tỷ₫`;
  }
  if (abs >= 1_000_000) {
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
  const intVal = Math.trunc(value);
  const grouped = formatGroupedInteger(intVal);
  return intVal < 0 ? `${TRUE_MINUS}${grouped}` : grouped;
}

/**
 * Formats a signed integer delta (`+14`, `−3`, `0`).
 */
export function formatSignedInt(delta: number): string {
  const intVal = Math.trunc(delta);
  if (intVal === 0) return "0";
  return `${intVal > 0 ? "+" : TRUE_MINUS}${formatGroupedInteger(intVal)}`;
}

/**
 * Formats a percentage ratio (`0.124` -> `12,4%`) or already-percentage number if `isRatio = false`.
 */
export function formatPercent(value: number, digits = 1, isRatio = true): string {
  const pct = isRatio ? value * 100 : value;
  return `${pct.toFixed(digits).replace(".", ",")}%`;
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

const ISO_DATE_RE = /^\d{4}-\d{2}-\d{2}$/;
export const MAX_REPORT_RANGE_DAYS = 366;

/**
 * Returns `YYYY-MM-DD` in Asia/Ho_Chi_Minh shifted by `deltaDays` from `baseMs` (default now).
 */
export function formatVnDateOffset(deltaDays: number, baseMs = Date.now()): string {
  return formatVnDayKey(baseMs + Math.trunc(deltaDays) * 24 * 60 * 60 * 1000);
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
  const fMs = Date.parse(`${f}T00:00:00Z`);
  const tMs = Date.parse(`${t}T00:00:00Z`);
  if (!Number.isFinite(fMs) || !Number.isFinite(tMs)) {
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
  if (originalPrice <= 0 || salePrice >= originalPrice) return "0%";
  const pct = Math.round(((originalPrice - salePrice) / originalPrice) * 100);
  return `-${pct}%`;
}

const VN_OFFSET_MS = 7 * 60 * 60 * 1000;

function twoDigit(n: number): string {
  return n < 10 ? `0${n}` : `${n}`;
}

/**
 * Shifts a timestamp into Vietnam time (`Asia/Ho_Chi_Minh`, fixed UTC+7, no DST)
 * so UTC getters produce identical results on UTC servers and client browsers.
 */
function toVietnamDate(input: string | number | Date): Date | null {
  const ms =
    typeof input === "number" ? input : input instanceof Date ? input.getTime() : Date.parse(input);
  if (!Number.isFinite(ms)) return null;
  return new Date(ms + VN_OFFSET_MS);
}

/**
 * Formats an ISO timestamp string to Vietnamese short date `dd/mm/yyyy` in Asia/Ho_Chi_Minh (UTC+7).
 */
export function formatDateVi(iso: string): string {
  const vn = toVietnamDate(iso);
  if (!vn) return iso;
  return `${twoDigit(vn.getUTCDate())}/${twoDigit(vn.getUTCMonth() + 1)}/${vn.getUTCFullYear()}`;
}

/**
 * Formats an ISO timestamp string to Vietnamese date & time `HH:mm · dd/mm/yyyy` in Asia/Ho_Chi_Minh (UTC+7).
 */
export function formatDateTimeVi(iso: string): string {
  const vn = toVietnamDate(iso);
  if (!vn) return iso;
  return `${twoDigit(vn.getUTCHours())}:${twoDigit(vn.getUTCMinutes())} · ${twoDigit(vn.getUTCDate())}/${twoDigit(vn.getUTCMonth() + 1)}/${vn.getUTCFullYear()}`;
}

/**
 * Formats `dd/mm` in Asia/Ho_Chi_Minh (UTC+7) for compact chart axis labels or short timelines.
 */
export function formatDayMonthVi(iso: string): string {
  const vn = toVietnamDate(iso);
  if (!vn) return iso;
  return `${twoDigit(vn.getUTCDate())}/${twoDigit(vn.getUTCMonth() + 1)}`;
}

/**
 * Formats `HH:mm` in Asia/Ho_Chi_Minh (UTC+7); returns `--:--` for missing or invalid timestamps.
 */
export function formatTimeHmVn(iso: string | undefined): string {
  if (!iso) return "--:--";
  const vn = toVietnamDate(iso);
  if (!vn) return "--:--";
  return `${twoDigit(vn.getUTCHours())}:${twoDigit(vn.getUTCMinutes())}`;
}

/**
 * Formats `HH:mm · dd/mm` in Asia/Ho_Chi_Minh (UTC+7) for compact operational tables.
 */
export function formatShortTimeDateVi(iso: string): string {
  const vn = toVietnamDate(iso);
  if (!vn) return iso;
  return `${twoDigit(vn.getUTCHours())}:${twoDigit(vn.getUTCMinutes())} · ${twoDigit(vn.getUTCDate())}/${twoDigit(vn.getUTCMonth() + 1)}`;
}

/**
 * Formats `YYYY-MM-DD` calendar date key in Asia/Ho_Chi_Minh (UTC+7).
 */
export function formatVnDayKey(input: string | number | Date): string {
  const vn = toVietnamDate(input);
  if (!vn) return typeof input === "string" ? input : "";
  return `${vn.getUTCFullYear()}-${twoDigit(vn.getUTCMonth() + 1)}-${twoDigit(vn.getUTCDate())}`;
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
};

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
