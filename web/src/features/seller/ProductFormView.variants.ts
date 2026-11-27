// Pure Shopify-style variant option & SKU combination engine for Seller ProductFormView.
// Preserves legacy attribute keys (`color`, `color_name`, `color_code`, `color_hex`, `size`)
// on edited products while letting sellers define up to 3 arbitrary custom options.
// Safe for client, server, and `node --test`.

import { formatVariantAttributes, normalizeVariantEntries } from "../../lib/formatVariant.ts";
import type { GwSellerProduct } from "./sellerApiTypes";

export const MAX_VARIANT_OPTIONS = 3;
export const MAX_VARIANT_COMBINATIONS = 100;
export const DEFAULT_WEIGHT_GRAMS = 250;
export const MIN_WEIGHT_GRAMS = 1;
export const DEFAULT_VARIANT_NAME = "Mặc định";

export const OPTION_NAME_SUGGESTIONS = [
  "Màu sắc",
  "Kích cỡ",
  "Chất liệu",
  "Dung tích",
  "Quy cách",
] as const;

export interface VariantOptionDraft {
  id: string;
  /**
   * Preserved backend storage key when editing an existing product (e.g. `"color"` or `"size"`).
   * When undefined (or when the seller renames the option away from its legacy display label),
   * the trimmed `name` is saved directly as the attribute key.
   */
  storageKey?: string;
  /** Visible option name entered by the seller (e.g. "Màu men", "Quy cách"). */
  name: string;
  /** Ordered unique values preserving original casing (never auto-uppercased). */
  values: string[];
}

export interface EditableSkuRow {
  localKey: string;
  id?: string;
  version?: number;
  reservedStock: number;
  skuCode: string;
  name: string;
  attributes: Record<string, string>;
  priceVnd: number;
  stock: number;
  weightGrams: string;
  imageUrl: string;
}

export function clampWeightGrams(raw: string | number): number {
  const trimmed = typeof raw === "number" ? String(raw) : raw.trim();
  if (!trimmed) return DEFAULT_WEIGHT_GRAMS;
  const parsed = Number(trimmed);
  if (!Number.isFinite(parsed)) return DEFAULT_WEIGHT_GRAMS;
  return Math.max(MIN_WEIGHT_GRAMS, Math.trunc(parsed));
}

export function slugifySkuSegment(input: string, maxLen = 10): string {
  const cleaned = input
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[đĐ]/g, "d")
    .replace(/[^a-zA-Z0-9]+/g, "")
    .toUpperCase()
    .slice(0, maxLen);
  return cleaned || "VAR";
}

function slugifyColorKey(input: string): string {
  const cleaned = input
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[đĐ]/g, "d")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 32);
  return cleaned || "mac-dinh";
}

export function buildSkuPayloadName(
  attributes?: Record<string, string> | null,
  fallbackName?: string | null
): string {
  const formatted = formatVariantAttributes(attributes);
  if (formatted) return formatted.slice(0, 250);
  const cleanFallback = (fallbackName ?? "").trim();
  return (cleanFallback || DEFAULT_VARIANT_NAME).slice(0, 250);
}

/**
 * Creates the single clean default SKU row for a brand-new product (empty attributes,
 * no preset garment color/size, no preset price/stock/image).
 */
export function createEmptyDefaultSkuRow(defaultSkuStamp?: string): EditableSkuRow {
  const stamp = defaultSkuStamp?.trim() || "1001";
  return {
    localKey: `new-default-${stamp}`,
    reservedStock: 0,
    skuCode: `NM-${stamp}-01`,
    name: DEFAULT_VARIANT_NAME,
    attributes: {},
    priceVnd: 0,
    stock: 0,
    weightGrams: String(DEFAULT_WEIGHT_GRAMS),
    imageUrl: "",
  };
}

/**
 * Extracts up to `MAX_VARIANT_OPTIONS` option groups from existing SKU attributes.
 * Preserves legacy `color`/`size` storage keys so editing seeded products retains backward compatibility.
 */
export function extractOptionsFromSkus(
  skus: ReadonlyArray<{ attributes?: Record<string, string> | null }>
): VariantOptionDraft[] {
  const optionOrder: string[] = [];
  const optionMap = new Map<
    string,
    {
      id: string;
      storageKey: string;
      name: string;
      values: string[];
      seenValues: Set<string>;
    }
  >();

  for (const sku of skus) {
    const entries = normalizeVariantEntries(sku.attributes);
    for (const entry of entries) {
      let bucket = optionMap.get(entry.key);
      if (!bucket) {
        if (optionOrder.length >= MAX_VARIANT_OPTIONS) continue;
        bucket = {
          id: `opt-${optionOrder.length + 1}-${entry.key}`,
          storageKey: entry.key,
          name: entry.label,
          values: [],
          seenValues: new Set<string>(),
        };
        optionMap.set(entry.key, bucket);
        optionOrder.push(entry.key);
      }
      if (!bucket.seenValues.has(entry.value)) {
        bucket.seenValues.add(entry.value);
        bucket.values.push(entry.value);
      }
    }
  }

  return optionOrder.map((key) => {
    const bucket = optionMap.get(key)!;
    return {
      id: bucket.id,
      storageKey: bucket.storageKey,
      name: bucket.name,
      values: bucket.values,
    };
  });
}

/**
 * Hydrates form `options` and `skuRows` from an existing product (edit mode), or creates
 * a blank single-variant starting state (create mode).
 */
export function toEditableFormState(
  product?: GwSellerProduct | null,
  defaultSkuStamp?: string
): { options: VariantOptionDraft[]; skuRows: EditableSkuRow[] } {
  if (!product || !product.skus || product.skus.length === 0) {
    return {
      options: [],
      skuRows: [createEmptyDefaultSkuRow(defaultSkuStamp)],
    };
  }

  const activeSkus = product.skus.filter((s) => s.is_active);
  const sourceSkus = activeSkus.length > 0 ? activeSkus : product.skus;

  const skuRows: EditableSkuRow[] = sourceSkus.map((s) => {
    const attrs: Record<string, string> = {};
    if (s.attributes && typeof s.attributes === "object") {
      for (const [k, v] of Object.entries(s.attributes)) {
        if (typeof v === "string" && k.trim() && v.trim()) {
          attrs[k.trim()] = v.trim();
        }
      }
    }
    return {
      localKey: s.id,
      id: s.id,
      version: s.version,
      reservedStock: s.reserved_stock,
      skuCode: s.sku_code,
      name: s.name?.trim() || buildSkuPayloadName(attrs, DEFAULT_VARIANT_NAME),
      attributes: attrs,
      priceVnd: s.price_vnd,
      stock: s.stock,
      weightGrams: String(
        s.weight_grams > 0 ? clampWeightGrams(s.weight_grams) : DEFAULT_WEIGHT_GRAMS
      ),
      imageUrl: s.image_url || "",
    };
  });

  const options = extractOptionsFromSkus(skuRows);
  return { options, skuRows };
}

function effectiveOptionStorageKey(opt: VariantOptionDraft): string {
  const cleanName = opt.name.trim();
  if (opt.storageKey === "color" && cleanName === "Màu sắc") return "color";
  if (opt.storageKey === "size" && cleanName === "Kích cỡ") return "size";
  return cleanName;
}

function combinationSignature(pairs: ReadonlyArray<{ key: string; value: string }>): string {
  return pairs.map((p) => `${p.key.toLowerCase()}=${p.value.toLowerCase()}`).join("\u0000");
}

function rowSignature(row: EditableSkuRow, activeKeys: ReadonlyArray<string>): string {
  const entries = normalizeVariantEntries(row.attributes);
  const byKey = new Map<string, string>();
  for (const e of entries) {
    byKey.set(e.key.toLowerCase(), e.value);
    byKey.set(e.label.toLowerCase(), e.value);
  }
  const pairs = activeKeys.map((k) => ({
    key: k,
    value: byKey.get(k.toLowerCase()) ?? "",
  }));
  return combinationSignature(pairs);
}

/**
 * Parses comma-separated or single option values while deduplicating (case-insensitive)
 * and preserving the user's exact casing.
 */
export function parseOptionValuesInput(raw: string, existingValues: readonly string[]): string[] {
  const seen = new Set(existingValues.map((v) => v.trim().toLowerCase()));
  const next = [...existingValues];
  for (const part of raw.split(",")) {
    const clean = part.trim();
    if (!clean) continue;
    const lower = clean.toLowerCase();
    if (seen.has(lower)) continue;
    seen.add(lower);
    next.push(clean);
  }
  return next;
}

export interface GenerateCombinationsInput {
  options: ReadonlyArray<VariantOptionDraft>;
  existingRows: ReadonlyArray<EditableSkuRow>;
  skuStamp: string;
  defaultPriceVnd?: number;
  defaultStock?: number;
  defaultWeightGrams?: string;
  defaultImageUrl?: string;
  maxCombinations?: number;
}

export interface GenerateCombinationsResult {
  rows: EditableSkuRow[];
  truncated: boolean;
  totalPossible: number;
}

/**
 * Generates the Shopify-style Cartesian product of up to 3 seller-defined options, preserving
 * matching existing SKU rows (id, version, reservedStock, skuCode, price, stock, weight, image,
 * and legacy color_code/color_hex metadata).
 */
export function generateVariantCombinations(
  input: GenerateCombinationsInput
): GenerateCombinationsResult {
  const maxCombos = Math.max(1, Math.trunc(input.maxCombinations ?? MAX_VARIANT_COMBINATIONS));
  const stamp = input.skuStamp.trim() || "1001";

  // Keep up to 3 active options with unique non-empty names and at least 1 value.
  const seenNames = new Set<string>();
  const activeOptions: Array<{
    storageKey: string;
    name: string;
    values: string[];
  }> = [];

  for (const opt of input.options) {
    if (activeOptions.length >= MAX_VARIANT_OPTIONS) break;
    const cleanName = opt.name.trim();
    const cleanValues = opt.values.map((v) => v.trim()).filter(Boolean);
    if (!cleanName || cleanValues.length === 0) continue;
    const storageKey = effectiveOptionStorageKey(opt);
    const dedupeKey = storageKey.toLowerCase();
    if (seenNames.has(dedupeKey)) continue;
    seenNames.add(dedupeKey);
    activeOptions.push({ storageKey, name: cleanName, values: cleanValues });
  }

  // Zero active options -> single default variant row with empty attributes.
  if (activeOptions.length === 0) {
    const existingDefault =
      input.existingRows.find((r) => Object.keys(r.attributes).length === 0) ??
      input.existingRows[0];
    if (existingDefault) {
      return {
        rows: [
          {
            ...existingDefault,
            name: DEFAULT_VARIANT_NAME,
            attributes: {},
          },
        ],
        truncated: false,
        totalPossible: 1,
      };
    }
    return {
      rows: [createEmptyDefaultSkuRow(stamp)],
      truncated: false,
      totalPossible: 1,
    };
  }

  const totalPossible = activeOptions.reduce((acc, opt) => acc * opt.values.length, 1);

  // Index existing rows by combination signature AND preserve legacy color metadata per display name.
  const activeKeys = activeOptions.map((o) => o.storageKey);
  const existingBySig = new Map<string, EditableSkuRow>();
  const legacyColorMetaByDisplay = new Map<
    string,
    { color?: string; color_code?: string; color_hex?: string }
  >();

  for (const row of input.existingRows) {
    const sig = rowSignature(row, activeKeys);
    if (sig && !existingBySig.has(sig)) {
      existingBySig.set(sig, row);
    }
    const colorDisplay = (row.attributes.color_name ?? row.attributes.color ?? "").trim();
    if (colorDisplay && !legacyColorMetaByDisplay.has(colorDisplay.toLowerCase())) {
      legacyColorMetaByDisplay.set(colorDisplay.toLowerCase(), {
        color: row.attributes.color,
        color_code: row.attributes.color_code,
        color_hex: row.attributes.color_hex,
      });
    }
  }

  // Cartesian product up to `maxCombos`.
  type ComboPair = { storageKey: string; name: string; value: string };
  let combos: ComboPair[][] = [[]];
  for (const opt of activeOptions) {
    const next: ComboPair[][] = [];
    for (const prefix of combos) {
      for (const val of opt.values) {
        if (next.length >= maxCombos) break;
        next.push([...prefix, { storageKey: opt.storageKey, name: opt.name, value: val }]);
      }
      if (next.length >= maxCombos) break;
    }
    combos = next;
  }

  const basePrice =
    input.defaultPriceVnd ?? input.existingRows.find((r) => r.priceVnd > 0)?.priceVnd ?? 0;
  const baseStock = input.defaultStock ?? input.existingRows.find((r) => r.stock > 0)?.stock ?? 0;
  const baseWeight =
    input.defaultWeightGrams ?? input.existingRows[0]?.weightGrams ?? String(DEFAULT_WEIGHT_GRAMS);
  const baseImage = input.defaultImageUrl ?? "";

  const usedCodes = new Set<string>();
  const rows: EditableSkuRow[] = combos.map((combo, idx) => {
    const sig = combinationSignature(combo.map((c) => ({ key: c.storageKey, value: c.value })));
    const matched = existingBySig.get(sig);

    const attributes: Record<string, string> = {};
    for (const part of combo) {
      if (part.storageKey === "color") {
        const legacy = legacyColorMetaByDisplay.get(part.value.toLowerCase());
        attributes.color = legacy?.color || slugifyColorKey(part.value);
        attributes.color_name = part.value;
        if (legacy?.color_code) attributes.color_code = legacy.color_code;
        if (legacy?.color_hex) attributes.color_hex = legacy.color_hex;
      } else {
        attributes[part.storageKey] = part.value;
      }
    }

    let skuCode = matched?.skuCode?.trim() || "";
    if (!skuCode || usedCodes.has(skuCode.toUpperCase())) {
      const segs = combo.map((c) => slugifySkuSegment(c.value, 8)).join("-");
      let candidate = `NM-${stamp}-${segs}`;
      let suffix = 2;
      while (usedCodes.has(candidate.toUpperCase())) {
        candidate = `NM-${stamp}-${segs}-${suffix}`;
        suffix += 1;
      }
      skuCode = candidate;
    }
    usedCodes.add(skuCode.toUpperCase());

    return {
      localKey: matched?.localKey ?? `combo-${idx}-${sig}`,
      id: matched?.id,
      version: matched?.version,
      reservedStock: matched?.reservedStock ?? 0,
      skuCode,
      name: buildSkuPayloadName(attributes),
      attributes,
      priceVnd: matched ? matched.priceVnd : basePrice,
      stock: matched ? matched.stock : baseStock,
      weightGrams: matched?.weightGrams ?? baseWeight,
      imageUrl: matched?.imageUrl ?? baseImage,
    };
  });

  return {
    rows,
    truncated: totalPossible > maxCombos,
    totalPossible,
  };
}
