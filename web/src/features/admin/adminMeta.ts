import { formatShortTimeDateVi } from "../../lib/format.ts";

export type PillTone = "positive" | "negative" | "warning" | "neutral";

export type AdminViewState = "loading" | "empty" | "error";

export const EMPTY_DASH = "Chưa có";

export function parseStateParam(raw: string | null): AdminViewState | null {
  if (raw === "loading" || raw === "empty" || raw === "error") {
    return raw;
  }
  return null;
}

/**
 * Converts a `<input type="datetime-local">` string entered in Vietnam time (UTC+7)
 * into a UTC ISO-8601 timestamp, or `""` when empty or invalid.
 */
export function localVnInputToIso(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) return "";
  const normalized = trimmed.length === 16 ? `${trimmed}:00+07:00` : `${trimmed}+07:00`;
  const parsed = new Date(normalized);
  return Number.isNaN(parsed.getTime()) ? "" : parsed.toISOString();
}

export { formatShortTimeDateVi };
