import type { CategorySlug, Product, ProductSku, Shop, SizeCode } from "@/data";

/**
 * Mapped view models for storefront catalog & product pages.
 * Gateway DTOs live in `@/lib/gateway-types`.
 * Structurally compatible with legacy Product/Shop so shared components stay backward-compatible.
 */
export interface CatalogSku extends ProductSku {
  /** Real backend SKU UUID from Product service. */
  id: string;
  skuName: string;
  colorName: string;
  reservedStock: number;
  version: number;
  weightGrams: number;
  imageUrl: string;
  isActive: boolean;
  attributes: Record<string, string>;
}

export interface CatalogProduct extends Omit<Product, "skus"> {
  /** Real backend product UUID. */
  id: string;
  /** Real backend category UUID. */
  categoryId: string;
  /** Real backend category slug (unrestricted string for newly created admin categories). */
  rawCategorySlug: string;
  /** Real backend shop slug & names for display without static getShopById lookup. */
  shopSlug: string;
  shopName: string;
  shopShortName: string;
  shopProvinceCode?: string;
  status: string;
  minPriceVnd: number;
  maxPriceVnd: number;
  skus: CatalogSku[];
  /** Optional editorial product ID matched by slug (used only for mock reviews/Q&A/bundles). */
  editorialProductId?: string;
  /** Optional editorial shop ID matched by shop slug (used only for mock size/delivery helpers). */
  editorialShopId?: string;
  /** Bundle product slugs resolved from editorial metadata. */
  bundleSlugs?: string[];
  /** True when editorial metadata exists for this slug; false for newly created seller products. */
  hasEditorialContent: boolean;
}

export interface CatalogShopCollection {
  slug: string;
  title: string;
  subtitle: string;
  productSlugs: string[];
}

export interface CatalogShop extends Shop {
  /** Real backend shop UUID. */
  id: string;
  status: string;
  createdAt?: string;
  /** Optional static shop ID matched by slug for mock chat/size-chart helpers. */
  editorialShopId?: string;
  /** Shop editorial collections linked by product slug rather than static ID. */
  editorialCollections?: CatalogShopCollection[];
  /** Hero banner resolved from editorial media or default workshop asset. */
  heroBanner: { src: string; alt: string };
  /** True when editorial workshop profile exists for this slug. */
  hasEditorialProfile: boolean;
}

export interface CatalogCategory {
  id: string;
  slug: string;
  categorySlug?: CategorySlug;
  name: string;
  parentId: string;
  commissionRateBps: number;
  sortOrder: number;
  description: string;
  subtitle: string;
  featuredImage: string;
  featuredAlt: string;
  benefitSummary?: string;
  subCategories?: Array<{ slug: string; name: string; benefit: string }>;
}

export interface CatalogVoucher {
  id: string;
  code: string;
  name: string;
  description: string;
  scope: "platform" | "shop" | "freeship";
  shopId: string;
  discountType: "fixed" | "percent";
  valueVnd: number;
  discountPercent: number;
  maxDiscountVnd: number;
  minOrderVnd: number;
  quota: number;
  usedCount: number;
  remainingQuota: number;
  perUserLimit: number;
  startsAt?: string;
  endsAt?: string;
  isActive: boolean;
  saved: boolean;
  eligible: boolean;
  ineligibleReason: string;
  excludeOutlet?: boolean;
}

export type CatalogFlashSlotStatus = "live" | "upcoming" | "ended";

export interface CatalogFlashSaleItem {
  /** Real backend flash_sale_item_id (UUID). */
  id: string;
  flashSaleId: string;
  shopId: string;
  /** Real backend sku_id (UUID). */
  skuId: string;
  skuCode: string;
  skuName: string;
  skuColorName: string;
  skuSize: string;
  skuAvailableStock: number;
  flashPriceVnd: number;
  originalPriceVnd: number;
  totalSlots: number;
  remainingSlots: number;
  perUserLimit: number;
  status: string;
  product: CatalogProduct;
}

export interface CatalogFlashSaleSlot {
  id: string;
  name: string;
  label: string;
  startTime: string;
  endTime: string;
  startsAt: string;
  endsAt: string;
  status: CatalogFlashSlotStatus;
  statusLabel: string;
  items: CatalogFlashSaleItem[];
}

export function isCatalogProduct(product: Product | CatalogProduct): product is CatalogProduct {
  return "rawCategorySlug" in product && "hasEditorialContent" in product;
}

export function isCatalogSku(sku: ProductSku | CatalogSku | undefined): sku is CatalogSku {
  return Boolean(sku && "id" in sku && typeof sku.id === "string" && sku.id.length > 0);
}

export function findProductSku(
  product: Product | CatalogProduct,
  colorCode: string,
  size: SizeCode | string
): (ProductSku | CatalogSku) | undefined {
  return product.skus.find((sku) => sku.colorCode === colorCode && sku.size === size);
}
