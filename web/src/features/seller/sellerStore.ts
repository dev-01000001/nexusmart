// Shared pure display helpers for Seller Center screens.
// All mock seller screens (/seller/chat, /seller/ho-so, /seller/tuong-tac) have been replaced
// with real backend-backed BFF routes and server loaders.

export const EMPTY_DASH = "\u2014";
const TRUE_MINUS = "\u2212";

export type UiStateMode = "loading" | "empty" | "error" | null;

export function parseUiState(value: string | null): UiStateMode {
  if (value === "loading" || value === "empty" || value === "error") {
    return value;
  }
  return null;
}

export function formatDeltaPct(
  current: number,
  prior: number
): {
  label: string;
  direction: "up" | "down" | "flat";
} {
  if (prior <= 0) {
    if (current <= 0) return { label: EMPTY_DASH, direction: "flat" };
    return { label: "Mới", direction: "up" };
  }
  const diff = current - prior;
  if (diff === 0) return { label: "0,0%", direction: "flat" };
  const pct = (Math.abs(diff) / prior) * 100;
  const formatted = pct.toFixed(1).replace(".", ",");
  return {
    label: `${diff > 0 ? "+" : TRUE_MINUS}${formatted}%`,
    direction: diff > 0 ? "up" : "down",
  };
}
