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
  sku_id: string;
  flash_price_vnd: number;
  total_slots: number;
  remaining_slots: number;
  per_user_limit: number;
  status?: string;
}

export interface GwFlashSale {
  id: string;
  name?: string;
  starts_at?: string;
  ends_at?: string;
  items?: GwFlashSaleItem[] | null;
}

const SIZE_CODES: readonly SizeCode[] = ["S", "M", "L", "XL", "2XL"];
const VN_OFFSET_MS = 7 * 60 * 60 * 1000;

function isSizeCode(v: string | undefined): v is SizeCode {
  return v !== undefined && (SIZE_CODES as readonly string[]).includes(v);
}

export interface CategoryIndex {
  categories: CategoryInfo[];
  slugById: Map<string, CategorySlug>;
}

/** Keeps only backend categories that the storefront knows how to present. */
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
        foundedYear: new Date().getFullYear(),
        responseTime: "",
        fulfillmentRate: "",
        returnPolicy: "",
        warehouseAddress: "",
        story: s.description ?? "",
        accentDot: "#9aa5ac",
        commissionRate: 0,
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
  const mapped: ProductSku[] = [];
  for (const base of content.skus) {
    const s = byCode.get(base.skuCode);
    if (s) mapped.push(realSku(base, s));
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
  const fallbackImage = p.images?.[0] ?? skus.find((s) => s.image_url)?.image_url ?? "";
  for (const s of skus) {
    const size = s.attributes?.size;
    if (!isSizeCode(size)) continue;
    const colorCode = s.attributes?.color || "mac-dinh";
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
  if (!mapped.length || !fallbackImage) return null;
  const sizes = SIZE_CODES.filter((sz) => mapped.some((m) => m.size === sz));
  return {
    id: p.id,
    backendId: p.id,
    slug: p.id,
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

/** Flash sale windows with SKU-level items grouped back to products. */
export function buildFlashSlots(
  gw: GwFlashSale[],
  products: Product[],
  now: number
): FlashSaleSlot[] {
  const productBySku = new Map<string, Product>();
  for (const p of products) for (const s of p.skus) if (s.skuId) productBySku.set(s.skuId, p);
  const out: FlashSaleSlot[] = [];
  for (const fs of [...gw].sort(
    (a, b) => Date.parse(a.starts_at ?? "") - Date.parse(b.starts_at ?? "")
  )) {
    if (fs.name && /^E2E\s/i.test(fs.name.trim())) continue;
    const start = Date.parse(fs.starts_at ?? "");
    const end = Date.parse(fs.ends_at ?? "");
    const status: FlashSlotStatus = now < start ? "upcoming" : now < end ? "live" : "ended";
    const rawItems = fs.items ?? [];
    const grouped = new Map<string, FlashSaleItem>();
    for (const it of rawItems) {
      if (it.status && it.status !== "APPROVED") continue;
      const product = productBySku.get(it.sku_id);
      if (!product) continue;
      const prev = grouped.get(product.id);
      if (!prev) {
        grouped.set(product.id, {
          id: it.id,
          productId: product.id,
          flashPrice: it.flash_price_vnd,
          originalPrice: product.price,
          totalQuota: it.total_slots,
          remainingQuota: Math.max(0, it.remaining_slots),
          limitPerBuyer: it.per_user_limit,
          itemIdBySku: { [it.sku_id]: it.id },
        });
        continue;
      }
      prev.flashPrice = Math.min(prev.flashPrice, it.flash_price_vnd);
      prev.totalQuota += it.total_slots;
      prev.remainingQuota += Math.max(0, it.remaining_slots);
      prev.limitPerBuyer = Math.max(prev.limitPerBuyer, it.per_user_limit);
      prev.itemIdBySku = { ...prev.itemIdBySku, [it.sku_id]: it.id };
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
