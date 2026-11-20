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
 * Formats discount percentage between originalPrice and salePrice (`-22%`).
 */
export function formatDiscountPercent(originalPrice: number, salePrice: number): string {
  if (originalPrice <= 0 || salePrice >= originalPrice) return "0%";
  const pct = Math.round(((originalPrice - salePrice) / originalPrice) * 100);
  return `-${pct}%`;
}

function twoDigit(n: number): string {
  return n < 10 ? `0${n}` : `${n}`;
}

/**
 * Formats an ISO timestamp string to Vietnamese short date `dd/mm/yyyy`.
 */
export function formatDateVi(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${twoDigit(d.getDate())}/${twoDigit(d.getMonth() + 1)}/${d.getFullYear()}`;
}

/**
 * Formats an ISO timestamp string to Vietnamese date & time `HH:mm · dd/mm/yyyy`.
 */
export function formatDateTimeVi(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${twoDigit(d.getHours())}:${twoDigit(d.getMinutes())} · ${twoDigit(d.getDate())}/${twoDigit(d.getMonth() + 1)}/${d.getFullYear()}`;
}

/**
 * Formats `dd/mm` for compact chart axis labels or short timelines.
 */
export function formatDayMonthVi(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${twoDigit(d.getDate())}/${twoDigit(d.getMonth() + 1)}`;
}
