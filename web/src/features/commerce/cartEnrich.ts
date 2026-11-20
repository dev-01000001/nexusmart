// Pure enrichment helpers mapping backend cart/voucher DTOs with catalog presentation metadata.
// Backend remains the sole source of truth for prices, stock, totals, and voucher eligibility.
import type { Product } from "@/data/products";
import type { Shop } from "@/data/shops";
import type { GwCartItem, GwCartShopGroup, GwVoucher } from "./types";

export interface EnrichedCartItem {
  raw: GwCartItem;
  productSlug: string;
  productName: string;
  variantLabel: string;
  image: string;
  returnEligible: boolean;
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
      return {
        raw: item,
        productSlug: product?.slug ?? item.product_id,
        productName: item.product_name || product?.name || "Sản phẩm NexusMart",
        variantLabel,
        image: item.image_url || color?.image || product?.image || FALLBACK_IMAGE,
        returnEligible: product?.returnEligible !== false,
        isOutOfStock: item.available_stock <= 0,
        exceedsStock: item.available_stock > 0 && item.quantity > item.available_stock,
      };
    });

    return {
      raw: group,
      shopName: group.shop_name || shop?.name || "Gian hàng NexusMart",
      shopShortName: shop?.shortName || group.shop_name || "Gian hàng",
      shopSlug: shop?.slug || group.shop_id,
      shopCity: shop?.city || "",
      accentDot: shop?.accentDot || "var(--accent)",
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

export function formatVoucherIneligibleReason(v: GwVoucher, currentAmount: number): string | null {
  if (!v.is_active) return "Mã ưu đãi hiện không còn hiệu lực.";
  if (v.remaining_quota <= 0) return "Mã ưu đãi đã hết lượt sử dụng.";
  if (v.ends_at && Date.parse(v.ends_at) < Date.now()) return "Mã ưu đãi đã hết hạn sử dụng.";
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
