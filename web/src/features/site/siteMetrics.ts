// Pure storefront metric helpers (zero DOM/React dependencies, directly unit-testable in Node).

export interface ParsedNumericStat {
  prefix: string;
  to: number;
  suffix: string;
}

/**
 * Extracts a safe positive integer for one-shot count-up metrics (e.g. "98%", "220gsm",
 * "1.500+", "12.000+"). Returns null for ranges ("1 đến 3 ngày"), decimals/ratios ("4.9/5"),
 * and standalone 4-digit years (1900..2099 such as "2024") so they render verbatim.
 */
export function parseNumericStat(raw: string): ParsedNumericStat | null {
  const trimmed = raw.trim();
  const m = trimmed.match(/^([^\d-]*?)(\d{1,3}(?:\.\d{3})+|\d{1,7})(.*)$/);
  if (!m) return null;

  const [, prefix, numToken, suffix] = m;
  // Reject decimals, ratios, ranges, or any secondary digits in suffix (e.g. "1 đến 3 ngày", "4.9/5").
  if (/^[\s]*[.,/\-–—]/.test(suffix) || /\d/.test(suffix)) {
    return null;
  }

  const digits = numToken.replace(/\./g, "");
  const num = Number.parseInt(digits, 10);
  if (!Number.isFinite(num) || num <= 0 || num > 99_999_999) {
    return null;
  }

  // Skip standalone 4-digit years (e.g. "2024", "Từ 2024") so formatInt never turns "2024" into "2.024".
  if (!numToken.includes(".") && num >= 1900 && num <= 2099 && !suffix.trim()) {
    return null;
  }

  return { prefix, to: num, suffix };
}
