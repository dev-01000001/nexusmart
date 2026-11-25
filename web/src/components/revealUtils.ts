// Pure reveal predicates and count-up formatting helpers (zero DOM, testable in Node).
import { formatInt, formatPercent, formatVnd } from "../lib/format.ts";

export const REVEAL_SELECTOR = "[data-reveal]";

export interface RectLike {
  top: number;
  bottom: number;
  width: number;
  height: number;
}

/**
 * Returns true if an element is already visible in the viewport or has been scrolled past above it.
 * Zero-sized (`display: none`) elements return false so IntersectionObserver can reveal them once shown.
 */
export function isRectImmediatelyVisible(rect: RectLike, viewportHeight: number): boolean {
  if (rect.width === 0 && rect.height === 0) return false;
  if (viewportHeight <= 0) return true;
  return rect.top < viewportHeight;
}

/** Clamped cubic ease-out curve matching `--ease-out` without overshoot. */
export function easeOutCubic(progress: number): number {
  const t = Math.max(0, Math.min(1, progress));
  return 1 - Math.pow(1 - t, 3);
}

export interface CountFormatOptions {
  format?: string | null;
  decimals?: number;
  prefix?: string;
  suffix?: string;
}

/** Formats a numeric value for `[data-reveal="count"]` targets. */
export function formatCountMetric(value: number, options: CountFormatOptions = {}): string {
  const format = options.format ?? null;
  const decimals = Math.max(0, Math.trunc(options.decimals ?? 0));
  const prefix = options.prefix ?? "";
  const suffix = options.suffix ?? "";

  let core: string;
  if (format === "vnd") {
    core = formatVnd(Math.round(value));
  } else if (format === "percent") {
    core = formatPercent(value, decimals, false);
  } else if (decimals > 0) {
    core = value.toFixed(decimals).replace(".", ",");
  } else {
    core = formatInt(Math.round(value));
  }
  return `${prefix}${core}${suffix}`;
}
