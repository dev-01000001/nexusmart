// Pure enrichment helpers mapping backend cart/voucher DTOs with catalog presentation metadata.
// Backend remains the sole source of truth for prices, stock, totals, and voucher eligibility.
import type { Product } from "@/data/products";
import type { Shop } from "@/data/shops";
import { formatVnd } from "../../lib/format.ts";
import type { GwCartItem, GwCartShopGroup, GwVoucher } from "./types";

export interface EnrichedCartItem {
  raw: GwCartItem;
  productSlug: string;
  productName: string;
  variantLabel: string;
  image: string;
  returnEligible: boolean;
  isUnavailable: boolean;
  unavailableReason: string;
  isOutOfStock: boolean;
  exceedsStock: boolean;
}

export interface EnrichedCartShopGroup {
  raw: GwCartShopGroup;
  shopName: string;
  shopShortName: string;
  shopSlug: string;
  shopCity: string;
  accentDot: string;
  freeShipThreshold: number;
  items: EnrichedCartItem[];
}

const FALLBACK_IMAGE = "/products/ao-thun-cotton-trang.png";

export function findCatalogShop(shops: Shop[], backendShopId: string): Shop | undefined {
  return shops.find((s) => s.backendId === backendShopId || s.id === backendShopId);
}

export function findCatalogProductForCartItem(
  products: Product[],
  item: GwCartItem
): Product | undefined {
  return (
    products.find((p) => p.skus.some((s) => s.skuId === item.sku_id)) ??
    products.find((p) => p.backendId === item.product_id || p.id === item.product_id)
  );
}

export function enrichCartGroups(
  groups: GwCartShopGroup[],
  products: Product[],
  shops: Shop[]
): EnrichedCartShopGroup[] {
  return groups.map((group) => {
    const shop = findCatalogShop(shops, group.shop_id);
    const items: EnrichedCartItem[] = (group.items ?? []).map((item) => {
      const product = findCatalogProductForCartItem(products, item);
      const sku = product?.skus.find((s) => s.skuId === item.sku_id);
      const color = sku ? product?.colors.find((c) => c.code === sku.colorCode) : undefined;
      const variantLabel =
        color && sku
          ? `Màu: ${color.name} · Size ${sku.size}`
          : item.sku_name || "Phân loại tiêu chuẩn";
      const isUnavailable = Boolean(item.unavailable);
      return {
        raw: item,
        productSlug: product?.slug ?? item.product_id,
        productName: item.product_name || product?.name || "Sản phẩm NexusMart",
        variantLabel,
        image: item.image_url || color?.image || product?.image || FALLBACK_IMAGE,
        returnEligible: product?.returnEligible !== false,
        isUnavailable,
        unavailableReason: (item.unavailable_reason ?? "").trim(),
        isOutOfStock: !isUnavailable && item.available_stock <= 0,
        exceedsStock:
          !isUnavailable && item.available_stock > 0 && item.quantity > item.available_stock,
      };
    });

    return {
      raw: group,
      shopName: group.shop_name || shop?.name || "Gian hàng NexusMart",
      shopShortName: shop?.shortName || group.shop_name || "Gian hàng",
      shopSlug: shop?.slug || group.shop_id,
      shopCity: shop?.city || "",
      accentDot: shop?.accentDot || "var(--ui-brand)",
      freeShipThreshold: shop?.freeShipThreshold ?? 199000,
      items,
    };
  });
}

export function formatVoucherHeadline(v: GwVoucher): string {
  if (v.discount_type.toUpperCase() === "PERCENT") {
    const cap =
      v.max_discount_vnd > 0 ? ` (tối đa ${v.max_discount_vnd.toLocaleString("vi-VN")}₫)` : "";
    return `Giảm ${v.discount_percent}%${cap}`;
  }
  return `Giảm ${v.value_vnd.toLocaleString("vi-VN")}₫`;
}

export function formatVoucherIneligibleReason(
  v: GwVoucher,
  currentAmount: number,
  nowMs?: number
): string | null {
  if (!v.is_active) return "Mã ưu đãi hiện không còn hiệu lực.";
  if (v.remaining_quota <= 0) return "Mã ưu đãi đã hết lượt sử dụng.";
  const referenceNow = nowMs ?? Date.now();
  if (v.ends_at && Date.parse(v.ends_at) < referenceNow) return "Mã ưu đãi đã hết hạn sử dụng.";
  if (v.min_order_vnd > 0 && currentAmount < v.min_order_vnd) {
    const missing = v.min_order_vnd - currentAmount;
    return `Mua thêm ${missing.toLocaleString("vi-VN")}₫ để đạt mức tối thiểu ${v.min_order_vnd.toLocaleString("vi-VN")}₫.`;
  }
  const raw = (v.ineligible_reason ?? "").toLowerCase();
  if (raw.includes("limit") || raw.includes("per-user")) {
    return "Bạn đã dùng hết lượt áp dụng cho mã này.";
  }
  if (raw.includes("quota") || raw.includes("budget")) {
    return "Mã ưu đãi đã hết lượt sử dụng.";
  }
  if (raw.includes("expired")) return "Mã ưu đãi đã hết hạn sử dụng.";
  if (raw.includes("inactive")) return "Mã ưu đãi hiện không còn hiệu lực.";
  return null;
}

export function splitVariantParts(label: string): string[] {
  return label
    .split(/\s*·\s*/)
    .map((part) => part.trim())
    .filter(Boolean);
}

export function shortVoucherHint(v: GwVoucher, currentAmount: number, nowMs?: number): string {
  if (!v.is_active) return "Hết hiệu lực";
  if (v.remaining_quota <= 0) return "Hết lượt";
  const referenceNow = nowMs ?? Date.now();
  if (v.ends_at && Date.parse(v.ends_at) < referenceNow) return "Hết hạn";
  if (v.min_order_vnd > 0 && currentAmount < v.min_order_vnd) {
    return `Thêm ${formatVnd(v.min_order_vnd - currentAmount)}`;
  }
  const raw = (v.ineligible_reason ?? "").toLowerCase();
  if (raw.includes("limit") || raw.includes("per-user")) return "Đã dùng";
  if (raw.includes("quota") || raw.includes("budget")) return "Hết lượt";
  if (raw.includes("expired")) return "Hết hạn";
  if (raw.includes("inactive")) return "Hết hiệu lực";
  return "Chưa đủ";
}

export interface CheckoutPreviewKeyInput {
  shopId: string;
  rawSubtotal: number;
  cartItemsSignature: string;
  quotedShippingFee: number | null;
  voucherId: string | null | undefined;
  flashKey: string;
}

export function buildCheckoutPreviewKey(input: CheckoutPreviewKeyInput): string {
  return `${input.shopId}:${input.rawSubtotal}:${input.cartItemsSignature}:${input.quotedShippingFee ?? "none"}:${input.voucherId || ""}:${input.flashKey}`;
}

function foldVietnamese(input: string): string {
  return input
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[đĐ]/g, "d")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, " ")
    .trim();
}

/** Joins street, ward, and province without repeating segments already present in street_address. */
export function formatAddressLine(street?: string, ward?: string, province?: string): string {
  const parts: string[] = [];
  const streetClean = (street ?? "").trim();
  if (streetClean) parts.push(streetClean);

  const wardClean = (ward ?? "").trim();
  if (wardClean) {
    const foldedSoFar = foldVietnamese(parts.join(", "));
    const foldedWard = foldVietnamese(wardClean);
    if (!foldedWard || !foldedSoFar.includes(foldedWard)) {
      parts.push(wardClean);
    }
  }

  const provinceClean = (province ?? "").trim();
  if (provinceClean) {
    const foldedSoFar = foldVietnamese(parts.join(", "));
    const foldedProv = foldVietnamese(provinceClean);
    if (!foldedProv || !foldedSoFar.includes(foldedProv)) {
      parts.push(provinceClean);
    }
  }

  return parts.join(", ");
}

/** Buyer-facing Vietnamese carrier name (hides internal/mock gateway labels like "Mock Express"). */
export function formatCarrierLabel(carrierCode?: string, carrierName?: string): string {
  const rawName = (carrierName ?? "").trim();
  const rawCode = (carrierCode ?? "").trim();
  if (/mock|sandbox|test/i.test(rawName) || /mock|sandbox|test/i.test(rawCode)) {
    return "Giao hàng NexusFast";
  }
  return rawName || rawCode || "Đơn vị vận chuyển";
}

/** Buyer-facing Vietnamese shipping tier label (strips internal/mock prefixes). */
export function formatServiceLabel(serviceCode?: string, serviceName?: string): string {
  const stripped = (serviceName ?? "")
    .replace(/^\s*(mock(\s+express)?|sandbox|test)\s*[-:·]?\s*/i, "")
    .trim();
  const folded = foldVietnamese(stripped);
  const codeUpper = (serviceCode ?? "").trim().toUpperCase();

  if (folded === "tieu chuan" || (!stripped && (codeUpper === "STD" || codeUpper === "STANDARD"))) {
    return "Giao tiêu chuẩn";
  }
  if (
    folded === "nhanh" ||
    (!stripped && (codeUpper === "EXP" || codeUpper === "EXPRESS" || codeUpper === "FAST"))
  ) {
    return "Giao nhanh";
  }
  return stripped || "Giao tiêu chuẩn";
}
