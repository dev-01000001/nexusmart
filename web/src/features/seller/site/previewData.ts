// Pure preview data mapper: turns Seller Center products & categories into storefront DTOs
// with rich sample fallbacks so template gallery & live code preview always render realistically.

import {
  CATEGORIES,
  PRODUCTS,
  type CategoryInfo,
  type CategorySlug,
  type Product,
  type ProductColor,
  type ProductSku,
  type SizeCode,
} from "@/data/products";
import { VOUCHERS, type Voucher } from "@/data/vouchers";
import type { SitePublicShopDTO } from "@/lib/site/types";
import { sanitizeSafeImageUrl } from "@/features/site/templateValidator";
import type { GwCategory, GwSellerProduct } from "@/features/seller/sellerApiTypes";

const VALID_CATEGORY_SLUGS = new Set<string>(CATEGORIES.map((c) => c.slug));
const VALID_SIZES: readonly SizeCode[] = ["S", "M", "L", "XL", "2XL"];

const COLOR_HEX_FALLBACK: ReadonlyArray<{ name: string; hex: string }> = [
  { name: "Đen", hex: "#1F262C" },
  { name: "Trắng", hex: "#F5F5F4" },
  { name: "Xanh than", hex: "#1E3A5F" },
  { name: "Xám đá", hex: "#717A84" },
  { name: "Xanh rêu", hex: "#355E4B" },
];

function slugifyAscii(input: string): string {
  return (
    input
      .normalize("NFD")
      .replace(/[\u0300-\u036f]/g, "")
      .replace(/đ/gi, "d")
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "") || "san-pham"
  );
}

function normalizeSizeCode(raw: string | undefined): SizeCode {
  const upper = (raw ?? "").trim().toUpperCase();
  if ((VALID_SIZES as readonly string[]).includes(upper)) return upper as SizeCode;
  if (upper === "XXL") return "2XL";
  return "M";
}

function mapSellerProductToStorefront(
  prod: GwSellerProduct,
  idx: number,
  categoryMap: Map<string, GwCategory>,
  shopId: string
): Product {
  const cat = categoryMap.get(prod.category_id);
  const rawSlug = cat?.slug ?? "ao-thun";
  const categorySlug: CategorySlug = VALID_CATEGORY_SLUGS.has(rawSlug)
    ? (rawSlug as CategorySlug)
    : "ao-thun";
  const categoryName = cat?.name ?? "Trang phục cơ bản";

  const safeImages = (prod.images ?? [])
    .map((u) => sanitizeSafeImageUrl(u))
    .filter(Boolean);
  const fallbackImg =
    safeImages[0] ||
    PRODUCTS[idx % PRODUCTS.length]?.image ||
    "/banners/campaign-everyday.jpg";

  const colorLookup = new Map<string, ProductColor>();
  const skus: ProductSku[] = [];

  for (let sIdx = 0; sIdx < (prod.skus ?? []).length; sIdx++) {
    const sku = prod.skus[sIdx];
    const attrs = sku.attributes ?? {};
    const colorName =
      attrs.color || attrs.mau || attrs["màu"] || COLOR_HEX_FALLBACK[sIdx % COLOR_HEX_FALLBACK.length].name;
    const colorCode = slugifyAscii(colorName);
    const size = normalizeSizeCode(attrs.size || attrs.co || attrs["cỡ"] || sku.name);
    const skuImg = sanitizeSafeImageUrl(sku.image_url) || fallbackImg;
    if (!colorLookup.has(colorCode)) {
      const palette = COLOR_HEX_FALLBACK[colorLookup.size % COLOR_HEX_FALLBACK.length];
      colorLookup.set(colorCode, {
        code: colorCode,
        name: colorName,
        hex: palette.hex,
        image: skuImg,
      });
    }
    skus.push({
      skuCode: sku.sku_code || `${slugifyAscii(prod.name)}-${colorCode}-${size}`,
      colorCode,
      size,
      stock: Math.max(0, Math.trunc(sku.available_stock ?? sku.stock ?? 0)),
      price: Math.max(0, Math.trunc(sku.price_vnd || prod.min_price_vnd || 199000)),
      skuId: sku.id,
    });
  }

  if (skus.length === 0) {
    colorLookup.set("den", { code: "den", name: "Đen", hex: "#1F262C", image: fallbackImg });
    for (const sz of ["S", "M", "L", "XL"] as const) {
      skus.push({
        skuCode: `${slugifyAscii(prod.name)}-den-${sz}`,
        colorCode: "den",
        size: sz,
        stock: 25,
        price: Math.max(0, Math.trunc(prod.min_price_vnd || 249000)),
      });
    }
  }

  const colors = Array.from(colorLookup.values());
  const sizes = Array.from(new Set(skus.map((s) => s.size)));
  const minPrice = Math.max(
    0,
    Math.trunc(prod.min_price_vnd || Math.min(...skus.map((s) => s.price)) || 249000)
  );
  const maxPrice = Math.max(minPrice, Math.trunc(prod.max_price_vnd || minPrice));

  return {
    id: prod.id,
    backendId: prod.id,
    slug: `${slugifyAscii(prod.name)}-${prod.id.slice(0, 6)}`,
    name: prod.name,
    shortName: prod.name.length > 42 ? `${prod.name.slice(0, 39)}...` : prod.name,
    categorySlug,
    categoryName,
    shopId,
    gender: "unisex",
    price: minPrice,
    originalPrice: maxPrice > minPrice ? maxPrice : undefined,
    image: fallbackImg,
    onModelImage: fallbackImg,
    gallery: safeImages.length > 0 ? safeImages : [fallbackImg],
    colors,
    sizes,
    skus,
    fabric: "Cotton Compact 220gsm",
    gsm: 220,
    fit: "Regular chuẩn phom",
    origin: "Xưởng may Việt Nam",
    careGuide: ["Giặt máy nước mát 30°C", "Không dùng thuốc tẩy mạnh", "Phơi nơi thoáng mát"],
    highlights: ["Chất vải thoáng khí, giữ phom ổn định", "Đồng kiểm khi nhận hàng"],
    description:
      prod.description?.trim() ||
      "Sản phẩm sản xuất và kiểm định trực tiếp tại xưởng gian hàng, minh bạch chất liệu và kích cỡ.",
    sizeChart: [
      { size: "S", heightCm: "158–166", weightKg: "50–58", chestOrWaistCm: "94", lengthCm: "66" },
      { size: "M", heightCm: "165–173", weightKg: "58–66", chestOrWaistCm: "100", lengthCm: "69" },
      { size: "L", heightCm: "172–179", weightKg: "66–75", chestOrWaistCm: "106", lengthCm: "72" },
      { size: "XL", heightCm: "177–185", weightKg: "75–85", chestOrWaistCm: "112", lengthCm: "74" },
    ],
    collectionSlugs: ["co-ban-hang-ngay"],
  };
}

export interface PreviewStorefrontDataset {
  shop: SitePublicShopDTO;
  products: Product[];
  categories: CategoryInfo[];
  vouchers: Voucher[];
}

export function buildPreviewStorefrontDataset(opts: {
  shopId: string;
  shopName: string;
  shopDescription?: string;
  sellerProducts: GwSellerProduct[];
  sellerCategories: GwCategory[];
}): PreviewStorefrontDataset {
  const shopId = opts.shopId || "preview-shop";
  const shopName = opts.shopName.trim() || "Gian hàng của bạn";
  const shop: SitePublicShopDTO = {
    id: shopId,
    name: shopName,
    description:
      opts.shopDescription?.trim() ||
      "Cửa hàng chính thức trên NexusMart — sản phẩm trực tiếp từ xưởng, minh bạch chất liệu và tồn kho.",
    status: "ACTIVE",
  };

  const catMap = new Map<string, GwCategory>();
  for (const c of opts.sellerCategories) {
    catMap.set(c.id, c);
  }

  const mappedSellerProducts = opts.sellerProducts
    .filter((p) => p.status?.toUpperCase() !== "REJECTED")
    .slice(0, 12)
    .map((p, idx) => mapSellerProductToStorefront(p, idx, catMap, shopId));

  const products: Product[] =
    mappedSellerProducts.length > 0
      ? mappedSellerProducts
      : PRODUCTS.slice(0, 8).map((p) => ({ ...p, shopId }));

  const presentSlugs = new Set(products.map((p) => p.categorySlug));
  const categories = CATEGORIES.filter((c) => presentSlugs.has(c.slug));
  const resolvedCategories = categories.length > 0 ? categories : CATEGORIES.slice(0, 4);

  const sampleShopVouchers: Voucher[] = VOUCHERS.filter((v) => v.scope === "shop")
    .slice(0, 4)
    .map((v, idx) => ({
      ...v,
      shopId,
      code: idx === 0 ? "SHOP30K" : idx === 1 ? "SHOP10" : v.code,
    }));

  return {
    shop,
    products,
    categories: resolvedCategories,
    vouchers: sampleShopVouchers,
  };
}
