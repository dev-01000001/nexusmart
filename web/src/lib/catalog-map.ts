/**
 * Pure mapping from gateway catalog DTOs to the storefront view model.
 *
 * Backend data is the source of truth for what exists, prices, stock, SKU ids,
 * voucher quotas and flash sale windows. Presentation content the backend does
 * not model (fabric notes, size charts, galleries, shop stories…) is merged in
 * from the content tables by stable keys: category slug, shop name, SKU code and
 * voucher code. No I/O here so it can be unit tested with `node --test`.
 */
import type {
  CategoryInfo,
  CategorySlug,
  Product,
  ProductColor,
  ProductSku,
  SizeCode,
} from "../data/products";
import type { Shop } from "../data/shops";
import type { Voucher, VoucherScope } from "../data/vouchers";
import type { FlashSaleItem, FlashSaleSlot, FlashSlotStatus } from "../data/flash-sale";
import { getVnDateParts } from "./format.ts";

export interface GwCategory {
  id: string;
  name: string;
  slug: string;
  parent_id?: string;
  sort_order?: number;
}

export interface GwSku {
  id: string;
  product_id?: string;
  shop_id?: string;
  sku_code: string;
  name?: string;
  attributes?: Record<string, string> | null;
  price_vnd: number;
  stock?: number;
  available_stock: number;
  image_url?: string;
  is_active?: boolean;
}

export interface GwProduct {
  id: string;
  shop_id: string;
  category_id: string;
  name: string;
  slug?: string;
  description?: string;
  images?: string[] | null;
  status?: string;
  min_price_vnd?: number;
  skus?: GwSku[] | null;
  created_at?: string;
}

export interface GwShop {
  id: string;
  name: string;
  description?: string;
  status?: string;
}

export interface GwVoucher {
  id: string;
  code: string;
  name: string;
  scope: string;
  shop_id?: string;
  discount_type: string;
  value_vnd: number;
  discount_percent: number;
  max_discount_vnd: number;
  min_order_vnd: number;
  quota: number;
  remaining_quota: number;
  ends_at?: string;
  is_active?: boolean;
  saved?: boolean;
}

export interface GwFlashSaleItem {
  id: string;
  shop_id?: string;
  sku_id: string;
  flash_price_vnd: number;
  original_price_vnd?: number;
  total_slots: number;
  remaining_slots: number;
  per_user_limit: number;
  status?: string;
}

export interface FlashSaleSkuDetail {
  id: string;
  skuId: string;
  flashPrice: number;
  originalPrice: number;
  totalQuota: number;
  remainingQuota: number;
  limitPerBuyer: number;
}

export interface SiteFlashSaleItem extends FlashSaleItem {
  skuDetailsBySku?: Record<string, FlashSaleSkuDetail>;
}

export interface SiteFlashSaleSlot extends Omit<FlashSaleSlot, "items"> {
  items: SiteFlashSaleItem[];
}

export interface FlashSaleSkuOption {
  sku: ProductSku;
  skuId: string;
  colorName: string;
  label: string;
  detail: FlashSaleSkuDetail;
}

export interface GwFlashSale {
  id: string;
  name?: string;
  starts_at?: string;
  ends_at?: string;
  items?: GwFlashSaleItem[] | null;
}

const KNOWN_SIZE_ORDER: readonly string[] = [
  "XXS",
  "XS",
  "S",
  "M",
  "L",
  "XL",
  "2XL",
  "XXL",
  "3XL",
  "XXXL",
  "4XL",
  "5XL",
];
const DEFAULT_SIZE_LABEL = "Mặc định";
const DEFAULT_PRODUCT_FALLBACK_IMAGE = "/banners/fabric-closeup.jpg";
const VN_OFFSET_MS = 7 * 60 * 60 * 1000;

function extractRawSkuSize(attrs: Record<string, string> | null | undefined): string {
  if (!attrs) return "";
  const raw = attrs.size ?? attrs.Size ?? attrs.co ?? attrs["cỡ"] ?? "";
  return typeof raw === "string" ? raw.trim() : "";
}

function buildSizeLabelsForSkus(
  skus: GwSku[],
  colorCodeForSku: (s: GwSku) => string
): Map<string, SizeCode> {
  const sizelessCountByColor = new Map<string, number>();
  for (const s of skus) {
    if (!extractRawSkuSize(s.attributes)) {
      const color = colorCodeForSku(s);
      sizelessCountByColor.set(color, (sizelessCountByColor.get(color) ?? 0) + 1);
    }
  }
  const usedKeys = new Set<string>();
  const out = new Map<string, SizeCode>();
  for (const s of skus) {
    const color = colorCodeForSku(s);
    const explicit = extractRawSkuSize(s.attributes);
    let label = explicit;
    if (!label) {
      label =
        (sizelessCountByColor.get(color) ?? 0) > 1
          ? s.name?.trim() || s.sku_code || DEFAULT_SIZE_LABEL
          : DEFAULT_SIZE_LABEL;
    }
    const pairKey = `${color}\u0000${label}`;
    if (usedKeys.has(pairKey)) {
      const disambiguated = s.sku_code?.trim() || s.id;
      if (disambiguated && !usedKeys.has(`${color}\u0000${disambiguated}`)) {
        label = disambiguated;
      }
    }
    usedKeys.add(`${color}\u0000${label}`);
    out.set(s.id || s.sku_code, label as SizeCode);
  }
  return out;
}

function collectOrderedSizes(mapped: ProductSku[], baseOrder?: readonly SizeCode[]): SizeCode[] {
  const unique: string[] = [];
  const seen = new Set<string>();
  for (const m of mapped) {
    const sz = String(m.size ?? "").trim();
    if (!sz || seen.has(sz)) continue;
    seen.add(sz);
    unique.push(sz);
  }
  if (unique.length === 0) return [];

  const result: string[] = [];
  const placed = new Set<string>();
  if (baseOrder && baseOrder.length > 0) {
    for (const sz of baseOrder) {
      if (seen.has(sz) && !placed.has(sz)) {
        placed.add(sz);
        result.push(sz);
      }
    }
  }

  const remaining = unique.filter((sz) => !placed.has(sz));
  const known: Array<{ sz: string; rank: number }> = [];
  const numeric: Array<{ sz: string; val: number }> = [];
  const other: string[] = [];

  for (const sz of remaining) {
    const rank = KNOWN_SIZE_ORDER.indexOf(sz.toUpperCase());
    if (rank >= 0) {
      known.push({ sz, rank });
    } else if (/^\d+(?:\.\d+)?$/.test(sz)) {
      numeric.push({ sz, val: Number(sz) });
    } else {
      other.push(sz);
    }
  }
  known.sort((a, b) => a.rank - b.rank);
  numeric.sort((a, b) => a.val - b.val);

  for (const item of known) result.push(item.sz);
  for (const item of numeric) result.push(item.sz);
  for (const item of other) result.push(item);

  return result as SizeCode[];
}

export interface CategoryIndex {
  categories: CategoryInfo[];
  slugById: Map<string, CategorySlug>;
}

/** Keeps only backend categories that the storefront knows how to present (and their child subcategories). */
export function mergeCategories(gw: GwCategory[], content: CategoryInfo[]): CategoryIndex {
  const slugById = new Map<string, CategorySlug>();
  const byslug = new Map(gw.map((c) => [c.slug, c]));
  const categories: CategoryInfo[] = [];
  for (const info of content) {
    const real = byslug.get(info.slug);
    if (!real) continue;
    slugById.set(real.id, info.slug);
    categories.push({ ...info, name: real.name || info.name });
  }
  const byId = new Map(gw.map((c) => [c.id, c]));
  for (const c of gw) {
    if (slugById.has(c.id)) continue;
    let cur: GwCategory | undefined = c;
    const visited = new Set<string>();
    while (cur?.parent_id && !visited.has(cur.id)) {
      visited.add(cur.id);
      const parentSlug = slugById.get(cur.parent_id);
      if (parentSlug) {
        slugById.set(c.id, parentSlug);
        break;
      }
      cur = byId.get(cur.parent_id);
    }
  }
  return { categories, slugById };
}

/** Shops are matched to their presentation profile by display name. */
export function mergeShops(gw: GwShop[], content: Shop[]): Shop[] {
  const byName = new Map(content.map((s) => [s.name.trim().toLowerCase(), s]));
  return gw
    .filter((s) => !s.status || s.status === "ACTIVE")
    .map((s): Shop => {
      const profile = byName.get(s.name.trim().toLowerCase());
      if (profile) return { ...profile, backendId: s.id };
      return {
        id: s.id,
        backendId: s.id,
        slug: s.id,
        name: s.name,
        shortName: s.name,
        city: "",
        provinceCode: "",
        specialty: s.description ?? "",
        foundedYear: getVnDateParts(Date.now())?.year ?? 2026,
        responseTime: "",
        fulfillmentRate: "",
        returnPolicy: "",
        warehouseAddress: "",
        story: s.description ?? "",
        accentDot: "#9aa5ac",
      };
    });
}

/**
 * Keeps storefront workshops (matched from curated profiles) plus any active
 * backend shop that owns at least one mapped storefront product.
 */
export function filterStorefrontShops(
  shops: Shop[],
  products: Product[],
  contentShops: Shop[]
): Shop[] {
  const knownIds = new Set(contentShops.map((s) => s.id));
  const productShopIds = new Set(products.map((p) => p.shopId));
  return shops.filter((s) => knownIds.has(s.id) || productShopIds.has(s.id));
}

function activeSkus(p: GwProduct): GwSku[] {
  return (p.skus ?? []).filter((s) => s.is_active !== false);
}

function minPrice(skus: GwSku[], fallback: number): number {
  return skus.length ? Math.min(...skus.map((s) => s.price_vnd)) : fallback;
}

function realSku(base: ProductSku, s: GwSku): ProductSku {
  return {
    ...base,
    skuId: s.id,
    price: s.price_vnd,
    stock: Math.max(0, s.available_stock),
  };
}

function fromContent(p: GwProduct, content: Product, skus: GwSku[], shopId: string): Product {
  const byCode = new Map(skus.map((s) => [s.sku_code, s]));
  const matchedCodes = new Set<string>();
  const mapped: ProductSku[] = [];
  for (const base of content.skus) {
    const s = byCode.get(base.skuCode);
    if (s) {
      matchedCodes.add(s.sku_code);
      mapped.push(realSku(base, s));
    }
  }

  const colors = [...content.colors];
  const colorMap = new Map(colors.map((c) => [c.code, c]));
  const fallbackImage =
    p.images?.[0] ||
    content.image ||
    skus.find((s) => s.image_url)?.image_url ||
    DEFAULT_PRODUCT_FALLBACK_IMAGE;
  const extraSkus = skus.filter((s) => !matchedCodes.has(s.sku_code));
  if (extraSkus.length > 0) {
    const defaultColorCode = colors[0]?.code || "mac-dinh";
    const extraColorOf = (s: GwSku) => s.attributes?.color?.trim() || defaultColorCode;
    const sizeLabels = buildSizeLabelsForSkus(extraSkus, extraColorOf);
    for (const s of extraSkus) {
      const colorCode = extraColorOf(s);
      if (!colorMap.has(colorCode)) {
        const nextColor: ProductColor = {
          code: colorCode,
          name: s.attributes?.color_name || s.attributes?.color || "Mặc định",
          hex: s.attributes?.color_hex || "#9aa5ac",
          image: s.image_url || fallbackImage,
        };
        colorMap.set(colorCode, nextColor);
        colors.push(nextColor);
      }
      const size = sizeLabels.get(s.id || s.sku_code) ?? (DEFAULT_SIZE_LABEL as SizeCode);
      mapped.push(realSku({ skuCode: s.sku_code, colorCode, size, stock: 0, price: 0 }, s));
    }
  }

  const price = minPrice(skus, content.price);
  return {
    ...content,
    backendId: p.id,
    shopId,
    name: p.name || content.name,
    description: p.description || content.description,
    price,
    originalPrice:
      content.originalPrice && content.originalPrice > price ? content.originalPrice : undefined,
    colors,
    sizes: collectOrderedSizes(mapped, content.sizes),
    skus: mapped,
    createdAt: content.createdAt ?? p.created_at,
  };
}

function generic(
  p: GwProduct,
  skus: GwSku[],
  slug: CategorySlug,
  categoryName: string,
  shopId: string
): Product | null {
  const colors = new Map<string, ProductColor>();
  const mapped: ProductSku[] = [];
  const fallbackImage =
    p.images?.[0] || skus.find((s) => s.image_url)?.image_url || DEFAULT_PRODUCT_FALLBACK_IMAGE;
  const colorOf = (s: GwSku) => s.attributes?.color?.trim() || "mac-dinh";
  const sizeLabels = buildSizeLabelsForSkus(skus, colorOf);

  for (const s of skus) {
    const colorCode = colorOf(s);
    const size = sizeLabels.get(s.id || s.sku_code) ?? (DEFAULT_SIZE_LABEL as SizeCode);
    if (!colors.has(colorCode)) {
      colors.set(colorCode, {
        code: colorCode,
        name: s.attributes?.color_name || s.attributes?.color || "Mặc định",
        hex: s.attributes?.color_hex || "#9aa5ac",
        image: s.image_url || fallbackImage,
      });
    }
    mapped.push(realSku({ skuCode: s.sku_code, colorCode, size, stock: 0, price: 0 }, s));
  }
  if (!mapped.length) return null;
  const sizes = collectOrderedSizes(mapped);
  return {
    id: p.id,
    backendId: p.id,
    slug: p.slug?.trim() || p.id,
    name: p.name,
    shortName: p.name,
    categorySlug: slug,
    categoryName,
    shopId,
    gender: "unisex",
    price: minPrice(skus, p.min_price_vnd ?? 0),
    image: fallbackImage,
    gallery: p.images?.length ? p.images : [fallbackImage],
    colors: [...colors.values()],
    sizes,
    skus: mapped,
    fabric: "",
    gsm: 0,
    fit: "",
    origin: "",
    careGuide: [],
    highlights: [],
    description: p.description ?? "",
    sizeChart: [],
    createdAt: p.created_at,
  };
}

export interface ProductMergeContext {
  content: Product[];
  categories: CategoryIndex;
  /** backend shop id → storefront shop id */
  shopIdByBackend: Map<string, string>;
}

/**
 * Backend products in storefront categories, enriched with content matched by
 * SKU code. Products outside the storefront categories (test data, other
 * verticals) or without sellable SKUs are left out.
 */
export function mergeProducts(gw: GwProduct[], ctx: ProductMergeContext): Product[] {
  const contentBySku = new Map<string, Product>();
  for (const c of ctx.content) for (const s of c.skus) contentBySku.set(s.skuCode, c);
  const out: Product[] = [];
  const seen = new Set<string>();
  for (const p of gw) {
    const slug = ctx.categories.slugById.get(p.category_id);
    if (!slug) continue;
    if (p.status && p.status !== "APPROVED" && p.status !== "OUT_OF_STOCK") continue;
    const skus = activeSkus(p);
    if (!skus.length) continue;
    const shopId = ctx.shopIdByBackend.get(p.shop_id) ?? p.shop_id;
    const content = skus.map((s) => contentBySku.get(s.sku_code)).find(Boolean);
    if (content && !seen.has(content.id)) {
      seen.add(content.id);
      out.push(fromContent(p, content, skus, shopId));
      continue;
    }
    const categoryName = ctx.categories.categories.find((c) => c.slug === slug)?.name ?? slug;
    const g = generic(p, skus, slug, categoryName, shopId);
    if (g) out.push(g);
  }
  return out;
}

function vnClock(iso: string | undefined): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const d = new Date(t + VN_OFFSET_MS);
  return `${String(d.getUTCHours()).padStart(2, "0")}:${String(d.getUTCMinutes()).padStart(2, "0")}`;
}

function scopeOf(v: string): VoucherScope {
  const s = v.toLowerCase();
  return s === "shop" || s === "freeship" ? s : "platform";
}

/** Live vouchers from promotion, keeping marketing copy for known codes. */
export function mergeVouchers(
  gw: GwVoucher[],
  content: Voucher[],
  shopIdByBackend: Map<string, string>,
  allowedShopIds?: Set<string>
): Voucher[] {
  const byCode = new Map(content.map((v, idx) => [v.code.toUpperCase(), { v, idx }]));
  const mapped: Array<{ voucher: Voucher; order: number }> = [];
  for (const v of gw) {
    if (v.is_active === false) continue;
    if (/^E2E\s/i.test(v.name.trim())) continue;
    const scope = scopeOf(v.scope);
    const shopId = v.shop_id ? (shopIdByBackend.get(v.shop_id) ?? v.shop_id) : undefined;
    if (scope === "shop" && allowedShopIds && (!shopId || !allowedShopIds.has(shopId))) {
      continue;
    }
    const known = byCode.get(v.code.toUpperCase());
    const copy = known?.v;
    const percent = v.discount_type.toUpperCase() === "PERCENT";
    const real = {
      id: v.id,
      code: v.code,
      scope,
      shopId,
      discountType: percent ? ("percent" as const) : ("fixed" as const),
      discountValue: percent ? v.discount_percent : v.value_vnd,
      maxDiscount: v.max_discount_vnd > 0 ? v.max_discount_vnd : undefined,
      minOrderValue: v.min_order_vnd,
      expiresAt: v.ends_at ?? copy?.expiresAt ?? "",
      remainingUses: Math.max(0, v.remaining_quota),
      totalUses: v.quota,
      saved: Boolean(v.saved),
    };
    const voucher: Voucher = copy
      ? { ...copy, ...real }
      : { ...real, title: v.name, description: v.name };
    mapped.push({ voucher, order: known ? known.idx : Number.MAX_SAFE_INTEGER });
  }
  mapped.sort((a, b) => a.order - b.order);
  return mapped.map((entry) => entry.voucher);
}

const STATUS_LABELS: Record<FlashSlotStatus, string> = {
  ended: "Đã kết thúc",
  live: "Đang mở bán",
  upcoming: "Sắp mở bán",
};

/** Flash sale windows with SKU-level items grouped back to products while preserving exact per-SKU details. */
export function buildFlashSlots(
  gw: GwFlashSale[],
  products: Product[],
  now: number
): SiteFlashSaleSlot[] {
  const productBySku = new Map<string, Product>();
  const skuById = new Map<string, ProductSku>();
  for (const p of products) {
    for (const s of p.skus) {
      if (s.skuId) {
        productBySku.set(s.skuId, p);
        skuById.set(s.skuId, s);
      }
    }
  }
  const out: SiteFlashSaleSlot[] = [];
  for (const fs of [...gw].sort(
    (a, b) => Date.parse(a.starts_at ?? "") - Date.parse(b.starts_at ?? "")
  )) {
    const start = Date.parse(fs.starts_at ?? "");
    const end = Date.parse(fs.ends_at ?? "");
    const status: FlashSlotStatus = now < start ? "upcoming" : now < end ? "live" : "ended";
    const rawItems = fs.items ?? [];
    const grouped = new Map<string, SiteFlashSaleItem>();
    for (const it of rawItems) {
      if (it.status && it.status !== "APPROVED") continue;
      const product = productBySku.get(it.sku_id);
      if (!product) continue;
      const skuObj = skuById.get(it.sku_id);
      const skuOriginalPrice =
        skuObj && skuObj.price > 0
          ? skuObj.price
          : it.original_price_vnd && it.original_price_vnd > 0
            ? it.original_price_vnd
            : product.price;
      const skuDetail: FlashSaleSkuDetail = {
        id: it.id,
        skuId: it.sku_id,
        flashPrice: it.flash_price_vnd,
        originalPrice: skuOriginalPrice,
        totalQuota: it.total_slots,
        remainingQuota: Math.max(0, it.remaining_slots),
        limitPerBuyer: Math.max(0, it.per_user_limit),
      };
      const prev = grouped.get(product.id);
      if (!prev) {
        grouped.set(product.id, {
          id: it.id,
          productId: product.id,
          flashPrice: it.flash_price_vnd,
          originalPrice: skuOriginalPrice,
          totalQuota: it.total_slots,
          remainingQuota: Math.max(0, it.remaining_slots),
          limitPerBuyer: Math.max(0, it.per_user_limit),
          itemIdBySku: { [it.sku_id]: it.id },
          skuDetailsBySku: { [it.sku_id]: skuDetail },
        });
        continue;
      }
      prev.flashPrice = Math.min(prev.flashPrice, it.flash_price_vnd);
      prev.totalQuota += it.total_slots;
      prev.remainingQuota += Math.max(0, it.remaining_slots);
      prev.limitPerBuyer = Math.max(prev.limitPerBuyer, Math.max(0, it.per_user_limit));
      prev.itemIdBySku = { ...prev.itemIdBySku, [it.sku_id]: it.id };
      prev.skuDetailsBySku = { ...prev.skuDetailsBySku, [it.sku_id]: skuDetail };
    }
    // Drop foreign-vertical windows whose backend items belong entirely outside the storefront.
    if (rawItems.length > 0 && grouped.size === 0) continue;
    const label = vnClock(fs.starts_at);
    out.push({
      id: fs.id,
      label,
      startTime: label,
      endTime: vnClock(fs.ends_at),
      startsAt: fs.starts_at,
      endsAt: fs.ends_at,
      status,
      statusLabel: STATUS_LABELS[status],
      items: [...grouped.values()],
    });
  }
  return out;
}

/**
 * Resolves exact per-SKU flash sale detail without ever falling back to another SKU's item id,
 * flash price, or remaining quota.
 */
export function getFlashSaleSkuDetail(
  item: FlashSaleItem | SiteFlashSaleItem | null | undefined,
  skuId: string | null | undefined
): FlashSaleSkuDetail | null {
  const cleanSkuId = (skuId ?? "").trim();
  if (!item || !cleanSkuId) return null;
  const extended = item as SiteFlashSaleItem;
  const exact = extended.skuDetailsBySku?.[cleanSkuId];
  if (exact) return exact;
  const itemId = item.itemIdBySku?.[cleanSkuId];
  if (!itemId) return null;
  return {
    id: itemId,
    skuId: cleanSkuId,
    flashPrice: item.flashPrice,
    originalPrice: item.originalPrice,
    totalQuota: item.totalQuota,
    remainingQuota: Math.max(0, item.remainingQuota),
    limitPerBuyer: Math.max(0, item.limitPerBuyer),
  };
}

/**
 * Lists ONLY the SKUs of `product` that are registered in `item` (across all colors),
 * paired with their exact per-SKU flash price/quota/itemId and compact selection labels.
 */
export function listFlashSaleSkuOptions(
  product: Product,
  item: FlashSaleItem | SiteFlashSaleItem | null | undefined
): FlashSaleSkuOption[] {
  if (!item) return [];
  const colorNameByCode = new Map(product.colors.map((c) => [c.code, c.name]));
  const raw: Array<{
    sku: ProductSku;
    skuId: string;
    colorName: string;
    detail: FlashSaleSkuDetail;
  }> = [];
  for (const sku of product.skus) {
    const skuId = (sku.skuId ?? "").trim();
    if (!skuId) continue;
    const detail = getFlashSaleSkuDetail(item, skuId);
    if (!detail) continue;
    raw.push({
      sku,
      skuId,
      colorName: colorNameByCode.get(sku.colorCode) || sku.colorCode,
      detail,
    });
  }
  const distinctColors = new Set(raw.map((r) => r.sku.colorCode));
  const multiColor = distinctColors.size > 1;
  return raw.map((r) => {
    const sizeText = String(r.sku.size ?? "").trim();
    const label = multiColor
      ? sizeText && sizeText !== DEFAULT_SIZE_LABEL
        ? `${r.colorName} · ${sizeText}`
        : r.colorName
      : sizeText || r.colorName || DEFAULT_SIZE_LABEL;
    return {
      ...r,
      label,
    };
  });
}

/** Marks products that sit in a live or upcoming flash sale window. */
export function markFlashProducts(products: Product[], slots: FlashSaleSlot[]): Product[] {
  const ids = new Set(
    slots.filter((s) => s.status !== "ended").flatMap((s) => s.items.map((i) => i.productId))
  );
  return products.map((p) => ({ ...p, isFlashSale: ids.has(p.id) }));
}

export function pickDefaultFlashSlot(
  slots: FlashSaleSlot[],
  preferredId?: string | null
): FlashSaleSlot | null {
  if (!slots.length) return null;
  if (preferredId) {
    const exact = slots.find((s) => s.id === preferredId);
    if (exact) return exact;
  }
  return (
    slots.find((s) => s.status === "live") ??
    slots.find((s) => s.status === "upcoming") ??
    slots[0] ??
    null
  );
}

export function secondsUntilSlotEdge(slot: FlashSaleSlot | null, nowMs: number): number {
  if (!slot) return 0;
  const iso =
    slot.status === "live" ? slot.endsAt : slot.status === "upcoming" ? slot.startsAt : undefined;
  if (!iso) return 0;
  const target = Date.parse(iso);
  if (Number.isNaN(target)) return 0;
  return Math.max(0, Math.floor((target - nowMs) / 1000));
}

export function findProductByParam(products: Product[], slugOrId: string): Product | undefined {
  return products.find((p) => p.slug === slugOrId || p.backendId === slugOrId || p.id === slugOrId);
}

export function findShopByParam(shops: Shop[], slugOrId: string): Shop | undefined {
  return shops.find((s) => s.slug === slugOrId || s.backendId === slugOrId || s.id === slugOrId);
}

export interface CatalogLookups {
  product(idOrSlug: string): Product | undefined;
  shop(idOrSlug: string): Shop | undefined;
  productBySkuId(skuIdOrCode: string): Product | undefined;
}

export function buildCatalogLookups(products: Product[], shops: Shop[]): CatalogLookups {
  const byProd = new Map<string, Product>();
  const bySku = new Map<string, Product>();
  for (const p of products) {
    byProd.set(p.id, p);
    byProd.set(p.slug, p);
    if (p.backendId) byProd.set(p.backendId, p);
    for (const s of p.skus) {
      bySku.set(s.skuCode, p);
      if (s.skuId) bySku.set(s.skuId, p);
    }
  }
  const byShop = new Map<string, Shop>();
  for (const s of shops) {
    byShop.set(s.id, s);
    byShop.set(s.slug, s);
    if (s.backendId) byShop.set(s.backendId, s);
  }
  return {
    product: (key) => byProd.get(key),
    shop: (key) => byShop.get(key),
    productBySkuId: (key) => bySku.get(key),
  };
}

export interface LiveVoucherCheck {
  valid: boolean;
  discount: number;
  reason?: string;
}

/** Validates a live backend voucher against an order subtotal without reading mock tables. */
export function checkLiveVoucher(
  v: Voucher,
  opts: { eligibleAmount: number; shopId?: string; hasOutletItems?: boolean; nowMs?: number }
): LiveVoucherCheck {
  const nowMs = opts.nowMs ?? Date.now();
  if (v.expiresAt) {
    const exp = Date.parse(v.expiresAt);
    if (!Number.isNaN(exp) && exp < nowMs) {
      return { valid: false, discount: 0, reason: "Mã đã hết hạn sử dụng." };
    }
  }
  if (v.remainingUses <= 0) {
    return { valid: false, discount: 0, reason: "Mã đã hết lượt sử dụng." };
  }
  if (v.scope === "shop" && v.shopId && opts.shopId && v.shopId !== opts.shopId) {
    return { valid: false, discount: 0, reason: "Mã chỉ áp dụng cho gian hàng phát hành." };
  }
  if (v.excludeOutlet && opts.hasOutletItems) {
    return { valid: false, discount: 0, reason: "Không áp dụng cho sản phẩm Outlet." };
  }
  if (opts.eligibleAmount < v.minOrderValue) {
    const missing = v.minOrderValue - opts.eligibleAmount;
    return {
      valid: false,
      discount: 0,
      reason: `Cần thêm ${missing.toLocaleString("vi-VN")}₫ để đạt đơn tối thiểu.`,
    };
  }
  const raw =
    v.discountType === "fixed"
      ? v.discountValue
      : Math.floor((opts.eligibleAmount * v.discountValue) / 100);
  const capped = v.maxDiscount ? Math.min(raw, v.maxDiscount) : raw;
  const discount = Math.max(0, Math.min(capped, opts.eligibleAmount));
  return { valid: true, discount };
}

export interface TtlCache<T> {
  get(loader: () => Promise<T>, nowMs?: number): Promise<T>;
  clear(): void;
}

/**
 * Tiny in-process TTL cache with in-flight deduplication.
 * Rejections are never cached so temporary gateway outages recover immediately.
 */
export function createTtlCache<T>(ttlMs: number): TtlCache<T> {
  let value: T | undefined;
  let hasValue = false;
  let expiresAt = 0;
  let inflight: Promise<T> | null = null;

  return {
    get(loader, nowMs) {
      const t = nowMs ?? Date.now();
      if (hasValue && t < expiresAt) {
        return Promise.resolve(value as T);
      }
      if (inflight) return inflight;
      const p = loader().then(
        (res) => {
          value = res;
          hasValue = true;
          expiresAt = (nowMs ?? Date.now()) + ttlMs;
          inflight = null;
          return res;
        },
        (err) => {
          inflight = null;
          throw err;
        }
      );
      inflight = p;
      return p;
    },
    clear() {
      value = undefined;
      hasValue = false;
      expiresAt = 0;
      inflight = null;
    },
  };
}

/** True when a backend product belongs to `shopId`, is publicly sellable, and has >= 1 active SKU. */
export function isPublicSellableShopProduct(
  p: GwProduct | null | undefined,
  shopId: string
): p is GwProduct {
  if (!p || !shopId || p.shop_id !== shopId) return false;
  const status = String(p.status ?? "").toUpperCase();
  if (status !== "APPROVED" && status !== "OUT_OF_STOCK") return false;
  return activeSkus(p).length > 0;
}

/**
 * Ensures already shop-verified products in custom/non-seeded categories preserve their real
 * backend category slug & name instead of being dropped on single-shop views (home, catalog, PDP, flash sale).
 */
export function extendCategoryIndexForShopProducts(
  base: CategoryIndex,
  gwCats: GwCategory[],
  products: GwProduct[]
): CategoryIndex {
  const missing = products.filter((p) => p.category_id && !base.slugById.has(p.category_id));
  if (missing.length === 0) return base;
  const fallbackSlug: CategorySlug = base.categories[0]?.slug ?? "ao-thun";
  const slugById = new Map(base.slugById);
  const categories = [...base.categories];
  const seenSlugs = new Set<string>(categories.map((c) => c.slug));
  const gwById = new Map(gwCats.map((c) => [c.id, c]));
  for (const p of missing) {
    if (slugById.has(p.category_id)) continue;
    const real = gwById.get(p.category_id);
    if (real) {
      const catSlug = (real.slug?.trim() || real.id) as CategorySlug;
      slugById.set(p.category_id, catSlug);
      if (!seenSlugs.has(catSlug)) {
        seenSlugs.add(catSlug);
        categories.push({
          slug: catSlug,
          name: real.name?.trim() || catSlug,
          description: "",
          featuredImage: "",
        });
      }
      continue;
    }
    slugById.set(p.category_id, fallbackSlug);
    if (!seenSlugs.has(fallbackSlug)) {
      seenSlugs.add(fallbackSlug);
      categories.push({
        slug: fallbackSlug,
        name: "Sản phẩm",
        description: "",
        featuredImage: "",
      });
    }
  }
  return { categories, slugById };
}

export interface SiteSlugLookupResult {
  discontinued?: boolean;
  shop_id?: string;
  category_id?: string;
  name?: string;
  product?: GwProduct | null;
}

function isOwnedDiscontinuedStub(
  stub: SiteSlugLookupResult | null | undefined,
  shopId: string
): stub is SiteSlugLookupResult & { discontinued: true } {
  if (!stub?.discontinued) return false;
  const expected = shopId.trim();
  if (!expected) return false;
  const stubShopId = (stub.shop_id ?? stub.product?.shop_id ?? "").trim();
  return stubShopId !== "" && stubShopId === expected;
}

export type SiteIdLookupResult =
  { status: "ok"; product: GwProduct | null } | { status: "not_found" } | { status: "error" };

export type ResolvedSiteProductEntry =
  { kind: "discontinued"; discontinuedName: string } | { kind: "product"; product: Product };

/**
 * Resolves a single product for a shop site from cached bundle products with live fallback to
 * `/api/v1/products/slug/{slug}` and `/api/v1/products/{id}` so newly approved or off-page
 * products render immediately while strictly enforcing shop isolation and sellable status.
 */
export async function resolveSiteProductEntry(opts: {
  bundleProducts: Product[];
  shopId: string;
  slugOrId: string;
  shopIdByBackend: Map<string, string>;
  contentProducts: Product[];
  contentCategories: CategoryInfo[];
  fetchBySlug: (slug: string) => Promise<SiteSlugLookupResult | null>;
  fetchById: (id: string) => Promise<SiteIdLookupResult>;
  fetchCategories: () => Promise<GwCategory[]>;
}): Promise<ResolvedSiteProductEntry | null> {
  const rawParam = opts.slugOrId.trim();
  if (!rawParam) return null;
  const cleanParam = rawParam.toLowerCase();
  const expectedShopId = opts.shopIdByBackend.get(opts.shopId) ?? opts.shopId;

  const mergeOne = async (raw: GwProduct): Promise<Product | null> => {
    const gwCats = await opts.fetchCategories();
    const baseIndex = mergeCategories(gwCats, opts.contentCategories);
    const categoryIndex = extendCategoryIndexForShopProducts(baseIndex, gwCats, [raw]);
    const merged = mergeProducts([raw], {
      content: opts.contentProducts,
      categories: categoryIndex,
      shopIdByBackend: opts.shopIdByBackend,
    })[0];
    if (!merged || merged.shopId !== expectedShopId) return null;
    return merged;
  };

  const matched = opts.bundleProducts.find(
    (p) =>
      p.slug.toLowerCase() === cleanParam ||
      p.id.toLowerCase() === cleanParam ||
      p.backendId?.toLowerCase() === cleanParam
  );

  if (matched) {
    if (!matched.backendId) {
      return { kind: "product", product: matched };
    }
    const liveRes = await opts.fetchById(matched.backendId);
    if (liveRes.status === "not_found") {
      const stub = await opts.fetchBySlug(matched.slug);
      if (stub?.discontinued) {
        if (!isOwnedDiscontinuedStub(stub, opts.shopId)) return null;
        return {
          kind: "discontinued",
          discontinuedName: (stub.name || stub.product?.name || matched.name || "Sản phẩm").trim(),
        };
      }
      return null;
    }
    if (liveRes.status === "ok" && liveRes.product) {
      if (!isPublicSellableShopProduct(liveRes.product, opts.shopId)) {
        return null;
      }
      const merged = await mergeOne(liveRes.product);
      if (merged) {
        return {
          kind: "product",
          product: { ...merged, isFlashSale: matched.isFlashSale },
        };
      }
    }
    return { kind: "product", product: matched };
  }

  let rawProduct: GwProduct | null = null;
  const slugRes = await opts.fetchBySlug(rawParam);
  if (slugRes?.discontinued) {
    if (!isOwnedDiscontinuedStub(slugRes, opts.shopId)) {
      return null;
    }
    return {
      kind: "discontinued",
      discontinuedName: (slugRes.name || slugRes.product?.name || "Sản phẩm").trim(),
    };
  }
  if (slugRes?.product) {
    rawProduct = slugRes.product;
  }

  if (!rawProduct) {
    const idRes = await opts.fetchById(rawParam);
    if (idRes.status === "ok" && idRes.product) {
      rawProduct = idRes.product;
    }
  }

  if (!isPublicSellableShopProduct(rawProduct, opts.shopId)) {
    return null;
  }

  const merged = await mergeOne(rawProduct);
  return merged ? { kind: "product", product: merged } : null;
}

/**
 * Extracts unique approved SKU IDs belonging to `shopId` from live/upcoming flash sales,
 * bounded to `maxSkus` (default 200 to match gateway `sku_ids` limit).
 */
export function collectShopFlashSkuIds(
  flashSales: GwFlashSale[],
  shopId: string,
  maxSkus = 200
): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const fs of flashSales) {
    for (const it of fs.items ?? []) {
      if (it.status && String(it.status).toUpperCase() !== "APPROVED") continue;
      if (it.shop_id && it.shop_id !== shopId) continue;
      const skuId = (it.sku_id ?? "").trim();
      if (!skuId || seen.has(skuId)) continue;
      seen.add(skuId);
      out.push(skuId);
      if (out.length >= maxSkus) return out;
    }
  }
  return out;
}

/**
 * Loads fresh flash-sale products exclusively for `shopId`'s approved flash items via bounded
 * batch `sku_ids` queries (no N+1, no stale bundle cache, filters hidden/deleted & foreign items).
 */
export async function resolveFreshShopFlashSales(opts: {
  shopId: string;
  flashSales: GwFlashSale[];
  shopIdByBackend: Map<string, string>;
  contentProducts: Product[];
  contentCategories: CategoryInfo[];
  fetchProductsBySkuIds: (skuIds: string[]) => Promise<GwProduct[]>;
  fetchCategories: () => Promise<GwCategory[]>;
  nowMs?: number;
  batchSize?: number;
}): Promise<{ slots: SiteFlashSaleSlot[]; products: Product[] }> {
  const skuIds = collectShopFlashSkuIds(opts.flashSales, opts.shopId);
  if (skuIds.length === 0) {
    return { slots: [], products: [] };
  }

  const batchSize = Math.max(1, Math.min(200, opts.batchSize ?? 50));
  const chunks: string[][] = [];
  for (let i = 0; i < skuIds.length; i += batchSize) {
    chunks.push(skuIds.slice(i, i + batchSize));
  }

  const [gwCats, ...batches] = await Promise.all([
    opts.fetchCategories(),
    ...chunks.map((chunk) => opts.fetchProductsBySkuIds(chunk)),
  ]);

  const rawById = new Map<string, GwProduct>();
  for (const batch of batches) {
    for (const p of batch) {
      if (!isPublicSellableShopProduct(p, opts.shopId)) continue;
      if (!rawById.has(p.id)) rawById.set(p.id, p);
    }
  }
  const rawProducts = [...rawById.values()];
  if (rawProducts.length === 0) {
    return { slots: [], products: [] };
  }

  const baseIndex = mergeCategories(gwCats, opts.contentCategories);
  const categoryIndex = extendCategoryIndexForShopProducts(baseIndex, gwCats, rawProducts);
  const expectedShopId = opts.shopIdByBackend.get(opts.shopId) ?? opts.shopId;
  let products = mergeProducts(rawProducts, {
    content: opts.contentProducts,
    categories: categoryIndex,
    shopIdByBackend: opts.shopIdByBackend,
  }).filter((p) => p.shopId === expectedShopId);

  const shopFlashSales: GwFlashSale[] = opts.flashSales.map((fs) => ({
    ...fs,
    items: (fs.items ?? []).filter((it) => !it.shop_id || it.shop_id === opts.shopId),
  }));
  const slots = buildFlashSlots(shopFlashSales, products, opts.nowMs ?? Date.now()).filter(
    (s) => s.items.length > 0
  );
  if (slots.length > 0) {
    products = markFlashProducts(products, slots);
  }
  return { slots, products };
}
