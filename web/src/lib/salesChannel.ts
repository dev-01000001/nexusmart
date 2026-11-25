// Shared sales channel normalization, validation, and Vietnamese presentation helpers.
// Pure module (no Next.js/Node runtime imports) so it runs in Server Components, BFF routes,
// client components, and `node --test`.

export type SalesChannel = "MARKETPLACE" | "STOREFRONT";
export type SalesChannelFilter = "" | SalesChannel;

export const DEFAULT_SALES_CHANNEL: SalesChannel = "MARKETPLACE";

export interface SalesChannelMeta {
  value: SalesChannel;
  label: string;
  shortLabel: string;
  isStorefront: boolean;
}

/**
 * Normalize a raw sales_channel value from backend responses into a canonical SalesChannel.
 * Legacy or empty values always default to `"MARKETPLACE"`.
 */
export function normalizeSalesChannel(raw: unknown): SalesChannel {
  if (typeof raw === "string" && raw.trim().toUpperCase() === "STOREFRONT") {
    return "STOREFRONT";
  }
  return DEFAULT_SALES_CHANNEL;
}

/**
 * Normalize a raw storefront_shop_id value consistent with the resolved sales channel.
 * Marketplace orders never carry a storefront_shop_id.
 */
export function normalizeStorefrontShopId(raw: unknown, channel: SalesChannel): string {
  if (channel !== "STOREFRONT") return "";
  return typeof raw === "string" ? raw.trim() : "";
}

/**
 * Parse an optional sales_channel filter from query strings or request bodies.
 * Matches gateway REST contract strictly:
 * - `""`, `null`, `undefined` -> `""` (unfiltered)
 * - `"MARKETPLACE"` -> `"MARKETPLACE"`
 * - `"STOREFRONT"` -> `"STOREFRONT"`
 * - any other value -> `null` (invalid)
 */
export function parseSalesChannelFilter(raw: unknown): SalesChannelFilter | null {
  if (raw === null || raw === undefined) return "";
  if (typeof raw !== "string") return null;
  const upper = raw.trim().toUpperCase();
  if (upper === "") return "";
  if (upper === "MARKETPLACE" || upper === "STOREFRONT") return upper;
  return null;
}

/** Presentation metadata in clean Vietnamese for Seller Center and Admin Portal badges. */
export function getSalesChannelMeta(raw: unknown): SalesChannelMeta {
  const value = normalizeSalesChannel(raw);
  if (value === "STOREFRONT") {
    return {
      value,
      label: "Site riêng",
      shortLabel: "Site riêng",
      isStorefront: true,
    };
  }
  return {
    value: "MARKETPLACE",
    label: "Sàn NexusMart",
    shortLabel: "Sàn NexusMart",
    isStorefront: false,
  };
}
