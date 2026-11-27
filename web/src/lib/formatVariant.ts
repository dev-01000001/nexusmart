// Shared, industry-agnostic SKU variant formatting and dynamic option-group derivation.
// Backward-compatible with legacy seeded keys (`color`, `color_name`, `color_code`, `color_hex`, `size`)
// while supporting arbitrary seller-defined option keys (e.g. "Màu men", "Quy cách", "Dung tích").
// Pure TypeScript — safe for server, client, direct per-file imports on site pages, and `node --test`.

const HEX_COLOR_RE = /^#(?:[0-9a-f]{3}|[0-9a-f]{4}|[0-9a-f]{6}|[0-9a-f]{8})$/i;

const HIDDEN_ATTRIBUTE_KEYS = new Set(["color_code", "color_hex"]);

export interface VariantAttributeEntry {
  /** Stable logical key ("color" for legacy color/color_name, "size" for legacy size, else raw key). */
  key: string;
  /** Human-readable Vietnamese label ("Màu sắc", "Kích cỡ", or seller's custom option name). */
  label: string;
  /** Option value preserving original casing (never uppercased). */
  value: string;
  /** Optional hex color when legacy `color_hex` is present and valid. */
  hex?: string;
}

export interface DynamicOptionValue {
  value: string;
  hex?: string;
  image?: string;
}

export interface DynamicOptionGroup {
  key: string;
  label: string;
  isColorSwatch: boolean;
  values: DynamicOptionValue[];
}

function parseValidHex(raw: string | undefined): string | undefined {
  const trimmed = (raw ?? "").trim();
  return HEX_COLOR_RE.test(trimmed) ? trimmed : undefined;
}

/**
 * Normalizes a SKU's `attributes` map into ordered, display-ready option entries:
 * - `color` / `color_name` -> label `"Màu sắc"` (prefers `color_name`, falls back to `color`)
 * - `size` -> label `"Kích cỡ"`
 * - `color_code`, `color_hex` -> hidden (hex attached to `"color"` entry when valid)
 * - Any other key -> displayed verbatim as entered by the seller
 * - Values preserve their original casing (never uppercased)
 */
export function normalizeVariantEntries(
  attributes?: Record<string, string> | null
): VariantAttributeEntry[] {
  if (!attributes || typeof attributes !== "object") return [];

  let colorRaw = "";
  let colorName = "";
  let colorHex: string | undefined;

  for (const [rawKey, rawVal] of Object.entries(attributes)) {
    const keyLower = rawKey.trim().toLowerCase();
    const val = typeof rawVal === "string" ? rawVal.trim() : "";
    if (!val) continue;
    if (keyLower === "color") colorRaw = val;
    else if (keyLower === "color_name") colorName = val;
    else if (keyLower === "color_hex") colorHex = parseValidHex(val);
  }

  const resolvedColorVal = colorName || colorRaw;
  const entries: VariantAttributeEntry[] = [];
  const emittedKeys = new Set<string>();

  for (const [rawKey, rawVal] of Object.entries(attributes)) {
    const key = rawKey.trim();
    const keyLower = key.toLowerCase();
    if (!key || HIDDEN_ATTRIBUTE_KEYS.has(keyLower)) continue;

    if (keyLower === "color" || keyLower === "color_name") {
      if (emittedKeys.has("color") || !resolvedColorVal) continue;
      emittedKeys.add("color");
      entries.push({
        key: "color",
        label: "Màu sắc",
        value: resolvedColorVal,
        ...(colorHex ? { hex: colorHex } : {}),
      });
      continue;
    }

    const val = typeof rawVal === "string" ? rawVal.trim() : "";
    if (!val) continue;

    if (keyLower === "size") {
      if (emittedKeys.has("size")) continue;
      emittedKeys.add("size");
      entries.push({
        key: "size",
        label: "Kích cỡ",
        value: val,
      });
      continue;
    }

    if (emittedKeys.has(key)) continue;
    emittedKeys.add(key);
    entries.push({
      key,
      label: key,
      value: val,
    });
  }

  return entries;
}

/**
 * Formats SKU `attributes` as `"Tên: giá trị · Tên: giá trị"`.
 * Falls back to `fallback` (trimmed) when `attributes` has no visible entries.
 */
export function formatVariantAttributes(
  attributes?: Record<string, string> | null,
  fallback?: string | null
): string {
  const entries = normalizeVariantEntries(attributes);
  if (entries.length > 0) {
    return entries.map((e) => `${e.label}: ${e.value}`).join(" · ");
  }
  return (fallback ?? "").trim();
}

/**
 * Single shared formatter for displaying a variant label in Cart, Checkout, Order Detail
 * (buyer / seller / admin), PDP drawers, and moderation tables.
 */
export function formatVariantLabel(input: {
  attributes?: Record<string, string> | null;
  skuName?: string | null;
  fallback?: string;
}): string {
  const fromAttrs = formatVariantAttributes(input.attributes);
  if (fromAttrs) return fromAttrs;
  const cleanName = (input.skuName ?? "").trim();
  if (cleanName) return cleanName;
  return (input.fallback ?? "").trim();
}

/**
 * Compact slash-joined values (`"Men lam / Bộ 7 món"` or `"Mặc định"` when no attributes).
 */
export function formatSkuShortName(
  attributes?: Record<string, string> | null,
  fallback = "Mặc định"
): string {
  const entries = normalizeVariantEntries(attributes);
  if (entries.length === 0) return fallback;
  return entries.map((e) => e.value).join(" / ");
}

/**
 * Maps a SKU's normalized attributes into a `{ [logicalKey]: value }` lookup for PDP matching.
 */
export function getSkuOptionMap(
  attributes?: Record<string, string> | null
): Record<string, string> {
  const map: Record<string, string> = {};
  for (const entry of normalizeVariantEntries(attributes)) {
    map[entry.key] = entry.value;
  }
  return map;
}

/**
 * Derives dynamic storefront PDP option groups directly from a product's SKU `attributes`.
 * Returns `[]` for simple products whose SKUs have no visible attributes.
 */
export function buildDynamicOptionGroups(
  skus: ReadonlyArray<{ attributes?: Record<string, string> | null; image?: string }>
): DynamicOptionGroup[] {
  const groupOrder: string[] = [];
  const groupsByKey = new Map<
    string,
    {
      key: string;
      label: string;
      hasHex: boolean;
      valueOrder: string[];
      valuesByVal: Map<string, DynamicOptionValue>;
    }
  >();

  for (const sku of skus) {
    const entries = normalizeVariantEntries(sku.attributes);
    for (const entry of entries) {
      let grp = groupsByKey.get(entry.key);
      if (!grp) {
        grp = {
          key: entry.key,
          label: entry.label,
          hasHex: false,
          valueOrder: [],
          valuesByVal: new Map(),
        };
        groupsByKey.set(entry.key, grp);
        groupOrder.push(entry.key);
      }
      if (entry.hex) grp.hasHex = true;

      const existing = grp.valuesByVal.get(entry.value);
      if (!existing) {
        grp.valueOrder.push(entry.value);
        grp.valuesByVal.set(entry.value, {
          value: entry.value,
          ...(entry.hex ? { hex: entry.hex } : {}),
          ...(sku.image ? { image: sku.image } : {}),
        });
      } else {
        if (!existing.hex && entry.hex) existing.hex = entry.hex;
        if (!existing.image && sku.image) existing.image = sku.image;
      }
    }
  }

  return groupOrder.map((key) => {
    const grp = groupsByKey.get(key)!;
    return {
      key: grp.key,
      label: grp.label,
      isColorSwatch: grp.key === "color" && grp.hasHex,
      values: grp.valueOrder.map((v) => grp.valuesByVal.get(v)!),
    };
  });
}
