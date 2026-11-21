export type PillTone = "positive" | "negative" | "warning" | "neutral";

export type AdminViewState = "loading" | "empty" | "error";

export const EMPTY_DASH = "\u2014";

export function parseStateParam(raw: string | null): AdminViewState | null {
  if (raw === "loading" || raw === "empty" || raw === "error") {
    return raw;
  }
  return null;
}

export function formatShortTimeDateVi(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const hh = d.getHours() < 10 ? `0${d.getHours()}` : String(d.getHours());
  const mm = d.getMinutes() < 10 ? `0${d.getMinutes()}` : String(d.getMinutes());
  const dd = d.getDate() < 10 ? `0${d.getDate()}` : String(d.getDate());
  const mo = d.getMonth() + 1 < 10 ? `0${d.getMonth() + 1}` : String(d.getMonth() + 1);
  return `${hh}:${mm} · ${dd}/${mo}`;
}
