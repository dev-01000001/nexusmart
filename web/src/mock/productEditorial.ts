/**
 * Editorial & mock-only metadata module for storefront catalog and product pages.
 *
 * Backend services own authoritative catalog entities (products, SKUs, stock, integer VND prices,
 * categories, shops, vouchers, flash sales). Fields that do not exist in backend schemas yet
 * (fabric story, GSM, model measurements, care guide, size charts, workshop profiles, mock
 * reviews/Q&A references, shock deals & combos) live here and are looked up strictly by `slug`
 * (or voucher `code`), degrading cleanly when a seller creates a brand-new product or shop.
 */

import {
  BADGE_LABELS,
  CARE_GUIDE_LIBRARY,
  CATEGORIES,
  COMBO_OFFERS,
  FABRIC_FEATURE_LABELS,
  FIT_LABELS,
  FLASH_SALE_RULES,
  GENDER_LABELS,
  GIFT_ORDER_THRESHOLD,
  JOURNAL_ARTICLES,
  MATERIAL_LABELS,
  MEMBERSHIP_TIERS,
  OCCASION_LABELS,
  PATTERN_LABELS,
  PLATFORM_FREESHIP_THRESHOLD,
  PRODUCTS,
  SAMPLE_DATA_NOTICE,
  SHOCK_DEALS,
  SHOPS,
  SPORT_LABELS,
  TRENDING_KEYWORDS,
  VOUCHERS,
  matchesGenderFilter,
  type CategoryInfo,
  type CategorySlug,
  type Product,
  type Shop,
} from "@/data";
import {
  CAMPAIGN_BANNERS,
  CATEGORY_TILES_MEDIA,
  FABRIC_MEDIA,
  MEDIA_BANNERS,
  MODEL_SHOTS,
  WORKSHOP_MEDIA,
} from "@/data/media";
import type { CatalogShopCollection } from "@/lib/catalog-types";

export {
  BADGE_LABELS,
  CAMPAIGN_BANNERS,
  CARE_GUIDE_LIBRARY,
  FABRIC_FEATURE_LABELS,
  FABRIC_MEDIA,
  FIT_LABELS,
  FLASH_SALE_RULES,
  GENDER_LABELS,
  GIFT_ORDER_THRESHOLD,
  JOURNAL_ARTICLES,
  MATERIAL_LABELS,
  MEDIA_BANNERS,
  MEMBERSHIP_TIERS,
  OCCASION_LABELS,
  PATTERN_LABELS,
  PLATFORM_FREESHIP_THRESHOLD,
  SAMPLE_DATA_NOTICE,
  SPORT_LABELS,
  TRENDING_KEYWORDS,
  WORKSHOP_MEDIA,
  matchesGenderFilter,
};

export interface ProductEditorial {
  editorialProductId: string;
  slug: string;
  shortName: string;
  gender: Product["gender"];
  originalPrice?: number;
  onModelImage?: string;
  colors: Product["colors"];
  fabric: string;
  gsm: number;
  fit: string;
  origin: string;
  careGuide: string[];
  highlights: string[];
  sizeChart: Product["sizeChart"];
  isNew?: boolean;
  material?: Product["material"];
  materialLabel?: string;
  fitType?: Product["fitType"];
  occasion?: Product["occasion"];
  sport?: Product["sport"];
  fabricFeatures?: Product["fabricFeatures"];
  pattern?: Product["pattern"];
  badges?: Product["badges"];
  isCombo?: boolean;
  isPack?: boolean;
  comboSlots?: Product["comboSlots"];
  tierDiscounts?: Product["tierDiscounts"];
  bundleSlugs: string[];
  giftThreshold?: Product["giftThreshold"];
  returnEligible?: boolean;
  returnExclusionReason?: string;
  slaText?: string;
  maxPerOrder?: number;
  modelInfo?: Product["modelInfo"];
  careInstructions?: Product["careInstructions"];
  hasVideo?: boolean;
  videoUrl?: string;
  videoPoster?: string;
  faqs?: Product["faqs"];
  salesRank?: number;
  shopSizeChart?: Product["shopSizeChart"];
  collectionSlugs?: string[];
}

export interface ShopEditorial {
  editorialShopId: string;
  slug: string;
  shortName: string;
  city: string;
  provinceCode: string;
  specialty: string;
  foundedYear: number;
  responseTime: string;
  fulfillmentRate: string;
  returnPolicy: string;
  warehouseAddress: string;
  accentDot: string;
  freeShipThreshold?: number;
  fastDeliveryEnabled?: boolean;
  fastDeliveryFee?: number;
  fastDeliveryNote?: string;
  returnDays?: number;
  twoWayExchange?: boolean;
  verifiedBadge?: string;
  factoryProfile?: Shop["factoryProfile"];
  sizeCharts?: Shop["sizeCharts"];
  collections: CatalogShopCollection[];
  internalMenu?: Shop["internalMenu"];
  heroBanner: { src: string; alt: string };
}

export interface CategoryEditorial {
  slug: CategorySlug;
  description: string;
  subtitle: string;
  featuredImage: string;
  featuredAlt: string;
  benefitSummary?: string;
  subCategories?: CategoryInfo["subCategories"];
}

export interface MockShockDealRef {
  id: string;
  productSlug: string;
  dealPrice: number;
  originalPrice: number;
  totalQuota: number;
  remainingQuota: number;
  limitPerOrder: number;
  limitPerDay: number;
  returnEligible: boolean;
  note: string;
}

export interface MockComboOfferRef {
  id: string;
  slug: string;
  title: string;
  subtitle: string;
  shopSlug: string;
  productSlugs: string[];
  comboPrice: number;
  originalTotalPrice: number;
  discountLabel: string;
  note: string;
}

const PRODUCT_SLUG_BY_STATIC_ID = new Map<string, string>(PRODUCTS.map((p) => [p.id, p.slug]));
const SHOP_SLUG_BY_STATIC_ID = new Map<string, string>(SHOPS.map((s) => [s.id, s.slug]));

const SHOP_BANNER_BY_SLUG: Record<string, { src: string; alt: string }> = {
  "xuong-det-bach-dang": { src: WORKSHOP_MEDIA.src, alt: WORKSHOP_MEDIA.alt },
  "kho-basic-sai-gon": { src: MEDIA_BANNERS.running.src, alt: MEDIA_BANNERS.running.alt },
  "soi-tu-nhien-da-nang": { src: MEDIA_BANNERS.greenFiber.src, alt: MEDIA_BANNERS.greenFiber.alt },
  "det-kim-ha-noi": { src: FABRIC_MEDIA.src, alt: FABRIC_MEDIA.alt },
};

const PRODUCT_EDITORIAL_BY_SLUG = new Map<string, ProductEditorial>(
  PRODUCTS.map((p) => [
    p.slug,
    {
      editorialProductId: p.id,
      slug: p.slug,
      shortName: p.shortName,
      gender: p.gender,
      originalPrice: p.originalPrice,
      onModelImage: p.onModelImage,
      colors: p.colors,
      fabric: p.fabric,
      gsm: p.gsm,
      fit: p.fit,
      origin: p.origin,
      careGuide: p.careGuide,
      highlights: p.highlights,
      sizeChart: p.sizeChart,
      isNew: p.isNew,
      material: p.material,
      materialLabel: p.materialLabel,
      fitType: p.fitType,
      occasion: p.occasion,
      sport: p.sport,
      fabricFeatures: p.fabricFeatures,
      pattern: p.pattern,
      badges: p.badges,
      isCombo: p.isCombo,
      isPack: p.isPack,
      comboSlots: p.comboSlots,
      tierDiscounts: p.tierDiscounts,
      bundleSlugs: (p.bundleIds ?? [])
        .map((id) => PRODUCT_SLUG_BY_STATIC_ID.get(id))
        .filter((slug): slug is string => Boolean(slug)),
      giftThreshold: p.giftThreshold,
      returnEligible: p.returnEligible,
      returnExclusionReason: p.returnExclusionReason,
      slaText: p.slaText,
      maxPerOrder: p.maxPerOrder,
      modelInfo: p.modelInfo,
      careInstructions: p.careInstructions,
      hasVideo: p.hasVideo,
      videoUrl: p.videoUrl,
      videoPoster: p.videoPoster,
      faqs: p.faqs,
      salesRank: p.salesRank,
      shopSizeChart: p.shopSizeChart,
      collectionSlugs: p.collectionSlugs,
    },
  ])
);

const SHOP_EDITORIAL_BY_SLUG = new Map<string, ShopEditorial>(
  SHOPS.map((s) => [
    s.slug,
    {
      editorialShopId: s.id,
      slug: s.slug,
      shortName: s.shortName,
      city: s.city,
      provinceCode: s.provinceCode,
      specialty: s.specialty,
      foundedYear: s.foundedYear,
      responseTime: s.responseTime,
      fulfillmentRate: s.fulfillmentRate,
      returnPolicy: s.returnPolicy,
      warehouseAddress: s.warehouseAddress,
      accentDot: s.accentDot,
      freeShipThreshold: s.freeShipThreshold,
      fastDeliveryEnabled: s.fastDeliveryEnabled,
      fastDeliveryFee: s.fastDeliveryFee,
      fastDeliveryNote: s.fastDeliveryNote,
      returnDays: s.returnDays,
      twoWayExchange: s.twoWayExchange,
      verifiedBadge: s.verifiedBadge,
      factoryProfile: s.factoryProfile,
      sizeCharts: s.sizeCharts,
      collections: (s.collections ?? []).map((col) => ({
        slug: col.slug,
        title: col.title,
        subtitle: col.subtitle,
        productSlugs: col.productIds
          .map((id) => PRODUCT_SLUG_BY_STATIC_ID.get(id))
          .filter((slug): slug is string => Boolean(slug)),
      })),
      internalMenu: s.internalMenu,
      heroBanner: SHOP_BANNER_BY_SLUG[s.slug] ?? {
        src: WORKSHOP_MEDIA.src,
        alt: WORKSHOP_MEDIA.alt,
      },
    },
  ])
);

const CATEGORY_EDITORIAL_BY_SLUG = new Map<string, CategoryEditorial>(
  CATEGORIES.map((c) => {
    const tile = CATEGORY_TILES_MEDIA[c.slug as keyof typeof CATEGORY_TILES_MEDIA];
    return [
      c.slug,
      {
        slug: c.slug,
        description: c.description,
        subtitle: tile?.subtitle ?? c.description,
        featuredImage: tile?.src ?? c.featuredImage,
        featuredAlt: tile?.alt ?? c.name,
        benefitSummary: c.benefitSummary,
        subCategories: c.subCategories,
      },
    ];
  })
);

const VOUCHER_EDITORIAL_BY_CODE = new Map(
  VOUCHERS.map((v) => [
    v.code.toUpperCase(),
    {
      description: v.description,
      excludeOutlet: v.excludeOutlet,
      conditions: v.conditions,
    },
  ])
);

export function getProductEditorialBySlug(slug: string): ProductEditorial | null {
  return PRODUCT_EDITORIAL_BY_SLUG.get(slug) ?? null;
}

export function getShopEditorialBySlug(slug: string): ShopEditorial | null {
  return SHOP_EDITORIAL_BY_SLUG.get(slug) ?? null;
}

export function getCategoryEditorialBySlug(slug: string): CategoryEditorial | null {
  return CATEGORY_EDITORIAL_BY_SLUG.get(slug) ?? null;
}

export function getLegacyShopSlugById(staticShopId: string): string | null {
  return SHOP_SLUG_BY_STATIC_ID.get(staticShopId) ?? null;
}

export function getEditorialProductSlugById(staticProductId: string): string | null {
  return PRODUCT_SLUG_BY_STATIC_ID.get(staticProductId) ?? null;
}

export function getEditorialModelShotBySlug(slug: string): { src: string; alt: string } | null {
  const editorial = PRODUCT_EDITORIAL_BY_SLUG.get(slug);
  if (!editorial) return null;
  const shot = MODEL_SHOTS[editorial.editorialProductId];
  if (shot) {
    return { src: shot.src, alt: shot.alt };
  }
  if (editorial.onModelImage) {
    return { src: editorial.onModelImage, alt: `${editorial.shortName} khi mặc thực tế` };
  }
  return null;
}

export function getEditorialVoucherMetaByCode(
  code: string
): { description?: string; excludeOutlet?: boolean; conditions?: string[] } | null {
  return VOUCHER_EDITORIAL_BY_CODE.get(code.trim().toUpperCase()) ?? null;
}

export function getHomeCategoryBonusTile() {
  return CATEGORY_TILES_MEDIA["ao-khoac"];
}

export function getMockFlashExtras(): {
  shockDeals: MockShockDealRef[];
  comboOffers: MockComboOfferRef[];
} {
  const shockDeals: MockShockDealRef[] = SHOCK_DEALS.map((deal) => ({
    id: deal.id,
    productSlug: PRODUCT_SLUG_BY_STATIC_ID.get(deal.productId) ?? "",
    dealPrice: deal.dealPrice,
    originalPrice: deal.originalPrice,
    totalQuota: deal.totalQuota,
    remainingQuota: deal.remainingQuota,
    limitPerOrder: deal.limitPerOrder,
    limitPerDay: deal.limitPerDay,
    returnEligible: deal.returnEligible,
    note: deal.note,
  })).filter((d) => Boolean(d.productSlug));

  const comboOffers: MockComboOfferRef[] = COMBO_OFFERS.map((offer) => ({
    id: offer.id,
    slug: offer.slug,
    title: offer.title,
    subtitle: offer.subtitle,
    shopSlug: SHOP_SLUG_BY_STATIC_ID.get(offer.shopId) ?? "",
    productSlugs: offer.productIds
      .map((id) => PRODUCT_SLUG_BY_STATIC_ID.get(id))
      .filter((slug): slug is string => Boolean(slug)),
    comboPrice: offer.comboPrice,
    originalTotalPrice: offer.originalTotalPrice,
    discountLabel: offer.discountLabel,
    note: offer.note,
  })).filter((c) => c.productSlugs.length > 0);

  return { shockDeals, comboOffers };
}

/**
 * Fallback used strictly by components embedded on mock-only pages (e.g. SizeFinder on /tap-chi)
 * when server catalog props are not passed.
 */
export function getEditorialCatalogFallback(): { products: Product[]; shops: Shop[] } {
  return { products: PRODUCTS, shops: SHOPS };
}
