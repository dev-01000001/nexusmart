import type { CategorySlug, ProductColor, SizeCode } from "@/data";
import type {
  GatewayActiveShopDiscount,
  GatewayCategory,
  GatewayFlashSale,
  GatewayFlashSaleItem,
  GatewayProduct,
  GatewayPublicShop,
  GatewaySKU,
  GatewayVoucher,
} from "@/lib/gateway-types";
import { formatTimeHmVn, formatVnd } from "@/lib/format";
import {
  WORKSHOP_MEDIA,
  getCategoryEditorialBySlug,
  getEditorialVoucherMetaByCode,
  getProductEditorialBySlug,
  getShopEditorialBySlug,
} from "@/mock/productEditorial";
import type {
  CatalogCategory,
  CatalogFlashSaleItem,
  CatalogFlashSaleSlot,
  CatalogFlashSlotStatus,
  CatalogProduct,
  CatalogShop,
  CatalogSku,
  CatalogVoucher,
} from "@/lib/catalog-types";

const DEFAULT_FALLBACK_IMAGE = "/products/ao-thun-cotton-trang.png";

const KNOWN_CATEGORY_SLUGS = new Set<CategorySlug>([
  "ao-thun",
  "ao-polo",
  "quan-short",
  "quan-jogger",
  "do-lot",
  "tat-phu-kien",
]);

/**
 * Returns top-level (root) categories only (no parentId), falling back to all if none are roots.
 */
export function getRootCategories<T extends { parentId?: string | null }>(
  categories: readonly T[]
): T[] {
  const roots = categories.filter((c) => !c.parentId);
  return roots.length > 0 ? roots : [...categories];
}

/**
 * Maps each category slug to the set of itself plus all descendant category slugs (children,
 * grandchildren, etc.), with cycle protection.
 */
export function buildCategoryDescendantSlugMap(
  categories: ReadonlyArray<{ id: string; slug: string; parentId?: string | null }>
): Map<string, Set<string>> {
  const childrenByParentId = new Map<string, Array<{ id: string; slug: string }>>();
  for (const cat of categories) {
    const pid = cat.parentId?.trim();
    if (!pid) continue;
    const list = childrenByParentId.get(pid);
    if (list) {
      list.push(cat);
    } else {
      childrenByParentId.set(pid, [cat]);
    }
  }

  const out = new Map<string, Set<string>>();
  for (const cat of categories) {
    const slugs = new Set<string>([cat.slug]);
    const visited = new Set<string>([cat.id]);
    const stack = [...(childrenByParentId.get(cat.id) ?? [])];
    while (stack.length > 0) {
      const child = stack.pop()!;
      if (visited.has(child.id)) continue;
      visited.add(child.id);
      slugs.add(child.slug);
      const grand = childrenByParentId.get(child.id);
      if (grand) stack.push(...grand);
    }
    out.set(cat.slug, slugs);
  }
  return out;
}

const SIZE_ORDER = ["XS", "S", "M", "L", "XL", "2XL", "3XL", "4XL"];

const COLOR_HEX_FALLBACK: Record<string, string> = {
  trang: "#f5f5f2",
  kem: "#ece6d8",
  be: "#d6cec1",
  xamda: "#9aa5ac",
  xamdam: "#5c666e",
  xamtro: "#808991",
  xam: "#949ea6",
  den: "#1f2428",
  thanchi: "#313a40",
  xanhthan: "#25394a",
  xanhda: "#546f7d",
  covit: "#0d7480",
  xanh: "#2f5f73",
  do: "#9c3a3a",
  nau: "#6f5646",
  reu: "#4f6457",
};

function slugifyColorCode(input: string): string {
  const cleaned = input
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[đĐ]/g, "d")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "")
    .trim();
  return cleaned || "mac-dinh";
}

function guessHexForColor(code: string, name: string): string {
  if (COLOR_HEX_FALLBACK[code]) return COLOR_HEX_FALLBACK[code];
  const norm = slugifyColorCode(name);
  for (const [key, hex] of Object.entries(COLOR_HEX_FALLBACK)) {
    if (norm.includes(key)) return hex;
  }
  return "#5d6b72";
}

function parseSkuColorAndSize(sku: GatewaySKU): {
  colorCode: string;
  colorName: string;
  size: SizeCode;
} {
  const attrs = sku.attributes ?? {};
  const parts = sku.name
    .split("/")
    .map((p) => p.trim())
    .filter(Boolean);

  const rawColorName = attrs.color?.trim() || (parts.length >= 2 ? parts[0] : "") || "Mặc định";
  const colorCode = attrs.color_code?.trim() || slugifyColorCode(rawColorName);

  const rawSize =
    attrs.size?.trim() || (parts.length >= 2 ? parts[parts.length - 1] : parts[0]) || "M";

  return {
    colorCode,
    colorName: rawColorName,
    size: rawSize as SizeCode,
  };
}

function sortSizes(sizes: string[]): SizeCode[] {
  return [...sizes].sort((a, b) => {
    const ia = SIZE_ORDER.indexOf(a.toUpperCase());
    const ib = SIZE_ORDER.indexOf(b.toUpperCase());
    if (ia !== -1 && ib !== -1) return ia - ib;
    if (ia !== -1) return -1;
    if (ib !== -1) return 1;
    return a.localeCompare(b);
  }) as SizeCode[];
}

export function mapGatewayShop(raw: GatewayPublicShop): CatalogShop {
  const editorial = getShopEditorialBySlug(raw.slug);
  const story =
    raw.description?.trim() || editorial?.specialty || "Gian hàng đối tác tại NexusMart.";

  if (editorial) {
    return {
      id: raw.id,
      slug: raw.slug,
      name: raw.name,
      shortName: editorial.shortName,
      city: editorial.city,
      provinceCode: editorial.provinceCode,
      specialty: editorial.specialty,
      foundedYear: editorial.foundedYear,
      responseTime: editorial.responseTime,
      fulfillmentRate: editorial.fulfillmentRate,
      returnPolicy: editorial.returnPolicy,
      warehouseAddress: editorial.warehouseAddress,
      story,
      accentDot: editorial.accentDot,
      commissionRate: editorial.commissionRate,
      freeShipThreshold: editorial.freeShipThreshold,
      fastDeliveryEnabled: editorial.fastDeliveryEnabled,
      fastDeliveryFee: editorial.fastDeliveryFee,
      fastDeliveryNote: editorial.fastDeliveryNote,
      returnDays: editorial.returnDays,
      twoWayExchange: editorial.twoWayExchange,
      verifiedBadge: editorial.verifiedBadge,
      factoryProfile: editorial.factoryProfile,
      sizeCharts: editorial.sizeCharts,
      collections: undefined,
      internalMenu: editorial.internalMenu,
      status: raw.status,
      createdAt: raw.created_at,
      editorialShopId: editorial.editorialShopId,
      editorialCollections: editorial.collections,
      heroBanner: editorial.heroBanner,
      hasEditorialProfile: true,
    };
  }

  return {
    id: raw.id,
    slug: raw.slug,
    name: raw.name,
    shortName: raw.name,
    city: "Việt Nam",
    provinceCode: "HCM",
    specialty: story,
    foundedYear: raw.created_at ? new Date(raw.created_at).getUTCFullYear() : 2026,
    responseTime: "Trong ngày",
    fulfillmentRate: "Đang cập nhật",
    returnPolicy: "Đổi trả miễn phí theo chính sách NexusMart",
    warehouseAddress: "Kho gian hàng trên hệ thống NexusMart",
    story,
    accentDot: "#0d7480",
    commissionRate: 0.05,
    returnDays: 7,
    twoWayExchange: false,
    status: raw.status,
    createdAt: raw.created_at,
    editorialCollections: [],
    heroBanner: { src: WORKSHOP_MEDIA.src, alt: raw.name },
    hasEditorialProfile: false,
  };
}

export function mapGatewayCategory(raw: GatewayCategory): CatalogCategory {
  const editorial = getCategoryEditorialBySlug(raw.slug);
  const categorySlug = KNOWN_CATEGORY_SLUGS.has(raw.slug as CategorySlug)
    ? (raw.slug as CategorySlug)
    : undefined;

  return {
    id: raw.id,
    slug: raw.slug,
    categorySlug,
    name: raw.name,
    parentId: raw.parent_id ?? "",
    sortOrder: raw.sort_order ?? 0,
    description: editorial?.description ?? `Sản phẩm thuộc danh mục ${raw.name} tại NexusMart.`,
    subtitle: editorial?.subtitle ?? raw.name,
    featuredImage: editorial?.featuredImage ?? DEFAULT_FALLBACK_IMAGE,
    featuredAlt: editorial?.featuredAlt ?? raw.name,
    benefitSummary: editorial?.benefitSummary,
    subCategories: editorial?.subCategories,
  };
}

export function mapGatewayProduct(
  raw: GatewayProduct,
  context: {
    categoriesById: Map<string, CatalogCategory>;
    shopsById: Map<string, CatalogShop>;
    flashSkuIds?: Set<string>;
    liveFlashItemsBySkuId?: Map<string, GatewayFlashSaleItem>;
    activeShopDiscountsBySkuId?: Map<string, GatewayActiveShopDiscount>;
  }
): CatalogProduct {
  const rawSlug = raw.slug ?? raw.id;
  const editorial = getProductEditorialBySlug(rawSlug);
  const category = context.categoriesById.get(raw.category_id);
  const shop = context.shopsById.get(raw.shop_id);

  const rawSkus = Array.isArray(raw.skus) ? raw.skus : [];
  const activeSkus = rawSkus.filter((s) => s.is_active !== false);
  const effectiveRawSkus = activeSkus.length > 0 ? activeSkus : rawSkus;

  const editorialColorByCode = new Map((editorial?.colors ?? []).map((c) => [c.code, c]));

  const mappedSkus: CatalogSku[] = effectiveRawSkus.map((sku) => {
    const parsed = parseSkuColorAndSize(sku);
    const availableStock = Math.max(0, Math.trunc(sku.available_stock ?? sku.stock ?? 0));
    const edColor = editorialColorByCode.get(parsed.colorCode);
    const regularPriceVnd = Math.max(0, Math.trunc(sku.price_vnd));
    const liveFlash = context.liveFlashItemsBySkuId?.get(sku.id);
    const hasValidFlash =
      Boolean(liveFlash) &&
      (liveFlash!.remaining_slots ?? 0) > 0 &&
      Math.trunc(liveFlash!.flash_price_vnd) > 0 &&
      Math.trunc(liveFlash!.flash_price_vnd) < regularPriceVnd;
    const shopDiscount = !hasValidFlash
      ? context.activeShopDiscountsBySkuId?.get(sku.id)
      : undefined;
    const hasValidShopDiscount =
      Boolean(shopDiscount) &&
      Math.trunc(shopDiscount!.discounted_price_vnd) > 0 &&
      Math.trunc(shopDiscount!.discounted_price_vnd) < regularPriceVnd;

    return {
      id: sku.id,
      skuCode: sku.sku_code,
      skuName: sku.name,
      colorCode: parsed.colorCode,
      colorName: edColor?.name ?? parsed.colorName,
      size: parsed.size,
      stock: availableStock,
      reservedStock: Math.max(0, Math.trunc(sku.reserved_stock ?? 0)),
      price: regularPriceVnd,
      regularPriceVnd,
      activeFlashItemId: hasValidFlash ? liveFlash!.id : undefined,
      activeShopDiscountItemId: hasValidShopDiscount ? shopDiscount!.item_id : undefined,
      activeShopDiscountName: hasValidShopDiscount
        ? shopDiscount!.program_name || "Giảm giá gian hàng"
        : undefined,
      version: sku.version ?? 1,
      weightGrams: sku.weight_grams ?? 0,
      imageUrl: sku.image_url?.trim() || "",
      isActive: sku.is_active !== false,
      attributes: sku.attributes ?? {},
    };
  });

  const rawImages = (raw.images ?? []).map((img) => img.trim()).filter(Boolean);
  const skuImages = mappedSkus.map((s) => s.imageUrl).filter(Boolean);
  const gallery = Array.from(new Set([...rawImages, ...skuImages]));
  if (gallery.length === 0) {
    gallery.push(DEFAULT_FALLBACK_IMAGE);
  }
  const primaryImage = gallery[0];
  const onModelImage =
    gallery.find((src) => src.startsWith("/models/")) ?? editorial?.onModelImage ?? undefined;

  // Build unique colors preserving editorial swatch order & hex where matched
  const colorMap = new Map<string, ProductColor>();
  if (editorial) {
    for (const edColor of editorial.colors) {
      const matchingSku = mappedSkus.find((s) => s.colorCode === edColor.code);
      if (matchingSku) {
        colorMap.set(edColor.code, {
          code: edColor.code,
          name: matchingSku.colorName || edColor.name,
          hex: edColor.hex,
          image: matchingSku.imageUrl || edColor.image || primaryImage,
        });
      }
    }
  }
  for (const sku of mappedSkus) {
    if (!colorMap.has(sku.colorCode)) {
      const edColor = editorialColorByCode.get(sku.colorCode);
      colorMap.set(sku.colorCode, {
        code: sku.colorCode,
        name: sku.colorName,
        hex: edColor?.hex ?? guessHexForColor(sku.colorCode, sku.colorName),
        image: sku.imageUrl || primaryImage,
      });
    }
  }
  if (colorMap.size === 0) {
    colorMap.set("mac-dinh", {
      code: "mac-dinh",
      name: "Mặc định",
      hex: "#5d6b72",
      image: primaryImage,
    });
  }
  const colors = Array.from(colorMap.values());

  const uniqueSizes = Array.from(new Set(mappedSkus.map((s) => s.size)));
  const sizes = sortSizes(uniqueSizes);

  // Integer VND min/max price & real strikethrough derived strictly from real SKUs + live flash / active shop discount.
  // Single-price rule: live flash sale > active shop discount program > regular SKU price.
  // NEVER use mock editorial.originalPrice for strikethrough pricing.
  const effectiveEntries = mappedSkus
    .filter((s) => s.price > 0)
    .map((s) => {
      const regular = s.price;
      const liveFlash = context.liveFlashItemsBySkuId?.get(s.id);
      if (
        liveFlash &&
        (liveFlash.remaining_slots ?? 0) > 0 &&
        Math.trunc(liveFlash.flash_price_vnd) > 0 &&
        Math.trunc(liveFlash.flash_price_vnd) < regular
      ) {
        const orig =
          liveFlash.original_price_vnd &&
          Math.trunc(liveFlash.original_price_vnd) > Math.trunc(liveFlash.flash_price_vnd)
            ? Math.trunc(liveFlash.original_price_vnd)
            : regular;
        return { effective: Math.trunc(liveFlash.flash_price_vnd), original: orig };
      }
      const shopDisc = context.activeShopDiscountsBySkuId?.get(s.id);
      if (
        shopDisc &&
        Math.trunc(shopDisc.discounted_price_vnd) > 0 &&
        Math.trunc(shopDisc.discounted_price_vnd) < regular
      ) {
        const orig =
          shopDisc.original_price_vnd > Math.trunc(shopDisc.discounted_price_vnd)
            ? Math.trunc(shopDisc.original_price_vnd)
            : regular;
        return { effective: Math.trunc(shopDisc.discounted_price_vnd), original: orig };
      }
      return { effective: regular, original: undefined as number | undefined };
    });

  const effectivePrices = effectiveEntries.map((e) => e.effective);
  const minPriceVnd =
    effectivePrices.length > 0
      ? Math.min(...effectivePrices)
      : Math.max(0, Math.trunc(raw.min_price_vnd ?? 0));
  const maxPriceVnd =
    effectivePrices.length > 0
      ? Math.max(...effectivePrices)
      : Math.max(minPriceVnd, Math.trunc(raw.max_price_vnd ?? 0));

  const cheapestEntry = effectiveEntries.find((e) => e.effective === minPriceVnd);
  const originalPrice =
    cheapestEntry?.original && cheapestEntry.original > minPriceVnd
      ? cheapestEntry.original
      : undefined;

  const rawCategorySlug = category?.slug ?? "ao-thun";
  const categorySlug: CategorySlug = KNOWN_CATEGORY_SLUGS.has(rawCategorySlug as CategorySlug)
    ? (rawCategorySlug as CategorySlug)
    : "ao-thun";
  const categoryName = category?.name ?? "Đồ cơ bản";

  const isFlashSale = Boolean(
    (context.flashSkuIds && mappedSkus.some((s) => context.flashSkuIds!.has(s.id))) ||
    (context.liveFlashItemsBySkuId &&
      mappedSkus.some((s) => context.liveFlashItemsBySkuId!.has(s.id)))
  );

  const shopName = shop?.name ?? "Gian hàng NexusMart";
  const shopShortName = shop?.shortName ?? shopName;

  return {
    id: raw.id,
    slug: rawSlug,
    name: raw.name,
    shortName: editorial?.shortName ?? raw.name,
    categoryId: raw.category_id,
    categorySlug,
    rawCategorySlug,
    categoryName,
    shopId: raw.shop_id,
    shopSlug: shop?.slug ?? "",
    shopName,
    shopShortName,
    shopProvinceCode: shop?.provinceCode,
    gender: editorial?.gender ?? "unisex",
    price: minPriceVnd,
    minPriceVnd,
    maxPriceVnd,
    originalPrice,
    image: primaryImage,
    onModelImage,
    gallery,
    colors,
    sizes,
    skus: mappedSkus,
    fabric: editorial?.fabric ?? "",
    gsm: editorial?.gsm ?? 0,
    fit: editorial?.fit ?? "",
    origin: editorial?.origin ?? (shop ? `${shop.city} · ${shop.name}` : "Việt Nam"),
    careGuide: editorial?.careGuide ?? [],
    highlights: editorial?.highlights ?? [],
    description: raw.description?.trim() || "",
    sizeChart: editorial?.sizeChart ?? [],
    isFlashSale,
    isNew: editorial?.isNew,
    material: editorial?.material,
    materialLabel: editorial?.materialLabel,
    fitType: editorial?.fitType,
    occasion: editorial?.occasion,
    sport: editorial?.sport,
    fabricFeatures: editorial?.fabricFeatures,
    pattern: editorial?.pattern,
    badges: editorial?.badges,
    isCombo: undefined,
    isPack: editorial?.isPack,
    comboSlots: undefined,
    tierDiscounts: undefined,
    bundleIds: undefined,
    bundleSlugs: [],
    giftThreshold: undefined,
    returnEligible: editorial?.returnEligible,
    returnExclusionReason: editorial?.returnExclusionReason,
    slaText: editorial?.slaText,
    maxPerOrder: editorial?.maxPerOrder,
    modelInfo: editorial?.modelInfo,
    careInstructions: editorial?.careInstructions,
    hasVideo: editorial?.hasVideo,
    videoUrl: editorial?.videoUrl,
    videoPoster: editorial?.videoPoster,
    faqs: editorial?.faqs,
    salesRank: editorial?.salesRank,
    createdAt: raw.created_at,
    shopSizeChart: editorial?.shopSizeChart,
    collectionSlugs: editorial?.collectionSlugs,
    status: raw.status,
    editorialProductId: editorial?.editorialProductId,
    editorialShopId: shop?.editorialShopId,
    hasEditorialContent: Boolean(editorial),
  };
}

export function formatVoucherSummary(v: {
  discountType: "fixed" | "percent";
  valueVnd: number;
  discountPercent: number;
  maxDiscountVnd: number;
  minOrderVnd: number;
}): string {
  const discountPart =
    v.discountType === "percent"
      ? v.maxDiscountVnd > 0
        ? `Giảm ${v.discountPercent}% tối đa ${formatVnd(v.maxDiscountVnd)}`
        : `Giảm ${v.discountPercent}%`
      : `Giảm ${formatVnd(v.valueVnd)}`;
  if (v.minOrderVnd > 0) {
    return `${discountPart} cho đơn từ ${formatVnd(v.minOrderVnd)}.`;
  }
  return `${discountPart} không yêu cầu giá trị đơn tối thiểu.`;
}

export function mapGatewayVoucher(raw: GatewayVoucher): CatalogVoucher {
  const rawScope = raw.scope.toUpperCase();
  const scope: CatalogVoucher["scope"] =
    rawScope === "SHOP" ? "shop" : rawScope === "FREESHIP" ? "freeship" : "platform";
  const discountType: CatalogVoucher["discountType"] =
    raw.discount_type.toUpperCase() === "PERCENT" ? "percent" : "fixed";
  const editorialMeta = getEditorialVoucherMetaByCode(raw.code);

  const valueVnd = Math.max(0, Math.trunc(raw.value_vnd ?? 0));
  const discountPercent = Math.max(0, Math.trunc(raw.discount_percent ?? 0));
  const maxDiscountVnd = Math.max(0, Math.trunc(raw.max_discount_vnd ?? 0));
  const minOrderVnd = Math.max(0, Math.trunc(raw.min_order_vnd ?? 0));

  return {
    id: raw.id,
    code: raw.code,
    name: raw.name || raw.code,
    description:
      editorialMeta?.description ??
      formatVoucherSummary({
        discountType,
        valueVnd,
        discountPercent,
        maxDiscountVnd,
        minOrderVnd,
      }),
    scope,
    shopId: raw.shop_id ?? "",
    discountType,
    valueVnd,
    discountPercent,
    maxDiscountVnd,
    minOrderVnd,
    quota: Math.max(0, Math.trunc(raw.quota ?? 0)),
    usedCount: Math.max(0, Math.trunc(raw.used_count ?? 0)),
    remainingQuota: Math.max(0, Math.trunc(raw.remaining_quota ?? 0)),
    perUserLimit: Math.max(1, Math.trunc(raw.per_user_limit ?? 1)),
    startsAt: raw.starts_at,
    endsAt: raw.ends_at,
    isActive: raw.is_active !== false,
    saved: Boolean(raw.saved),
    eligible: Boolean(raw.eligible),
    ineligibleReason: raw.ineligible_reason ?? "",
    excludeOutlet: editorialMeta?.excludeOutlet,
  };
}

export function mapGatewayFlashSales(
  rawSales: GatewayFlashSale[],
  products: CatalogProduct[],
  nowMs: number
): CatalogFlashSaleSlot[] {
  const skuIndex = new Map<string, { product: CatalogProduct; sku: CatalogSku }>();
  for (const product of products) {
    for (const sku of product.skus) {
      if (sku.id) {
        skuIndex.set(sku.id, { product, sku });
      }
    }
  }

  const mapped: CatalogFlashSaleSlot[] = rawSales.map((sale) => {
    const startMs = sale.starts_at ? Date.parse(sale.starts_at) : 0;
    const endMs = sale.ends_at ? Date.parse(sale.ends_at) : 0;

    let status: CatalogFlashSlotStatus = "upcoming";
    let statusLabel = "Sắp mở bán";
    if (Number.isFinite(startMs) && Number.isFinite(endMs) && startMs > 0 && endMs > 0) {
      if (nowMs >= startMs && nowMs < endMs) {
        status = "live";
        statusLabel = "Đang mở bán";
      } else if (nowMs >= endMs) {
        status = "ended";
        statusLabel = "Đã kết thúc";
      }
    }

    const startTime = formatTimeHmVn(sale.starts_at);
    const endTime = formatTimeHmVn(sale.ends_at);

    const items: CatalogFlashSaleItem[] = [];
    for (const rawItem of sale.items ?? []) {
      if (rawItem.status && rawItem.status.toUpperCase() !== "APPROVED") {
        continue;
      }
      const match = skuIndex.get(rawItem.sku_id);
      if (!match) continue;
      const { product, sku } = match;
      const flashPriceVnd = Math.max(0, Math.trunc(rawItem.flash_price_vnd));
      const realOriginal =
        rawItem.original_price_vnd && Math.trunc(rawItem.original_price_vnd) > flashPriceVnd
          ? Math.trunc(rawItem.original_price_vnd)
          : (sku.regularPriceVnd ?? sku.price);
      const originalPriceVnd = realOriginal > flashPriceVnd ? realOriginal : sku.price;

      items.push({
        id: rawItem.id,
        flashSaleId: rawItem.flash_sale_id || sale.id,
        shopId: rawItem.shop_id || product.shopId,
        skuId: rawItem.sku_id,
        skuCode: sku.skuCode,
        skuName: sku.skuName || `${sku.colorName} / ${sku.size}`,
        skuColorName: sku.colorName,
        skuSize: sku.size,
        skuAvailableStock: sku.stock,
        flashPriceVnd,
        originalPriceVnd,
        totalSlots: Math.max(0, Math.trunc(rawItem.total_slots ?? 0)),
        remainingSlots: Math.max(0, Math.trunc(rawItem.remaining_slots ?? 0)),
        perUserLimit: Math.max(1, Math.trunc(rawItem.per_user_limit ?? 1)),
        status: rawItem.status || "APPROVED",
        product,
      });
    }

    return {
      id: sale.id,
      name: sale.name,
      label: startTime,
      startTime,
      endTime,
      startsAt: sale.starts_at ?? "",
      endsAt: sale.ends_at ?? "",
      status,
      statusLabel,
      items,
    };
  });

  // Sort chronologically by startsAt so live slots precede upcoming slots in time order
  mapped.sort((a, b) => {
    const ta = Date.parse(a.startsAt) || 0;
    const tb = Date.parse(b.startsAt) || 0;
    return ta - tb;
  });

  return mapped;
}

export function resolvePriceBucketRange(bucket: string): {
  minPriceVnd?: number;
  maxPriceVnd?: number;
} {
  switch (bucket) {
    case "under-180":
      return { maxPriceVnd: 179_999 };
    case "180-250":
      return { minPriceVnd: 180_000, maxPriceVnd: 250_000 };
    case "250-350":
      return { minPriceVnd: 250_001, maxPriceVnd: 350_000 };
    case "over-350":
      return { minPriceVnd: 350_001 };
    case "under-200":
      return { maxPriceVnd: 199_999 };
    case "200-300":
      return { minPriceVnd: 200_000, maxPriceVnd: 300_000 };
    case "over-300":
      return { minPriceVnd: 300_001 };
    default:
      return {};
  }
}
