import type { GatewayReturnRequest } from "@/lib/gateway-types";

/**
 * Resolves a return request snapshot after a mutation: uses the inline snapshot when present,
 * otherwise falls back to a GET read without turning a succeeded mutation into an error.
 */
export async function resolveReturnRequest(
  inline: GatewayReturnRequest | null | undefined,
  fetchFallback: () => Promise<{ return_request?: GatewayReturnRequest | null }>
): Promise<GatewayReturnRequest | null> {
  if (inline) return inline;
  try {
    return (await fetchFallback())?.return_request ?? null;
  } catch {
    return null;
  }
}

/** Replaces the first matching item in `items`, or appends `nextItem` when no match exists. */
export function upsertItem<T>(
  items: readonly T[],
  nextItem: T,
  matches: (existing: T) => boolean
): T[] {
  let replaced = false;
  const next = items.map((item) => {
    if (matches(item)) {
      replaced = true;
      return nextItem;
    }
    return item;
  });
  return replaced ? next : [...next, nextItem];
}

/**
 * Applies an inline mutated item into `current`, or falls back to refetching the list.
 * Keeps `current` if the fallback read fails so a succeeded mutation is not lost.
 */
export async function resolveListMutation<T>(
  inlineItem: T | null | undefined,
  current: readonly T[],
  matches: (existing: T) => boolean,
  fetchFallback: () => Promise<readonly T[] | null | undefined>
): Promise<T[]> {
  if (inlineItem) return upsertItem(current, inlineItem, matches);
  try {
    const list = await fetchFallback();
    if (Array.isArray(list)) return [...list];
  } catch {
    // Keep existing list if fallback read fails after a succeeded mutation.
  }
  return [...current];
}
