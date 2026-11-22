import type {
  GatewayOrder,
  GatewayOrderStatus,
  GatewayProduct,
  ProductStatus,
  ShopStatus,
} from "@/lib/gateway-types";
import { GATEWAY_ORDER_STATUS_LABELS, GATEWAY_SHIPMENT_STATUS_LABELS } from "@/lib/format";

export type SellerPillTone = "neutral" | "warn" | "pos" | "neg";

export interface StatusMeta {
  label: string;
  tone: SellerPillTone;
}

export const SELLER_SHOP_STATUS_META: Record<ShopStatus | string, StatusMeta> = {
  ACTIVE: { label: "Đang hoạt động", tone: "pos" },
  PENDING: { label: "Chờ duyệt", tone: "warn" },
  REJECTED: { label: "Bị từ chối", tone: "neg" },
  SUSPENDED: { label: "Tạm ngưng", tone: "neg" },
};

export const SELLER_PRODUCT_STATUS_META: Record<ProductStatus | string, StatusMeta> = {
  APPROVED: { label: "Đang bán", tone: "pos" },
  PENDING: { label: "Chờ duyệt", tone: "warn" },
  REJECTED: { label: "Bị từ chối", tone: "neg" },
  HIDDEN: { label: "Đang ẩn", tone: "neutral" },
  OUT_OF_STOCK: { label: "Hết hàng", tone: "neg" },
};

export const SELLER_ORDER_STATUS_META: Record<GatewayOrderStatus | string, StatusMeta> = {
  PENDING: { label: GATEWAY_ORDER_STATUS_LABELS.PENDING, tone: "warn" },
  CONFIRMED: { label: "Chờ xác nhận", tone: "warn" },
  PREPARING: { label: GATEWAY_ORDER_STATUS_LABELS.PREPARING, tone: "neutral" },
  SHIPPING: { label: GATEWAY_ORDER_STATUS_LABELS.SHIPPING, tone: "neutral" },
  DELIVERED: { label: GATEWAY_ORDER_STATUS_LABELS.DELIVERED, tone: "pos" },
  COMPLETED: { label: GATEWAY_ORDER_STATUS_LABELS.COMPLETED, tone: "pos" },
  CANCELLED: { label: GATEWAY_ORDER_STATUS_LABELS.CANCELLED, tone: "neg" },
  RETURN_REQUESTED: { label: GATEWAY_ORDER_STATUS_LABELS.RETURN_REQUESTED, tone: "warn" },
  RETURNED: { label: GATEWAY_ORDER_STATUS_LABELS.RETURNED, tone: "neg" },
};

export const SHIPMENT_STATUS_LABELS = GATEWAY_SHIPMENT_STATUS_LABELS;

export const PAYMENT_METHOD_TEXT: Record<string, string> = {
  COD: "Thu hộ COD",
  VIETQR: "Chuyển khoản VietQR",
};

export const CARRIER_OPTIONS: Array<{
  carrierCode: string;
  carrierLabel: string;
  services: Array<{ serviceCode: string; serviceLabel: string }>;
}> = [
  {
    carrierCode: "ghn",
    carrierLabel: "Giao Hàng Nhanh (GHN)",
    services: [
      { serviceCode: "STANDARD", serviceLabel: "GHN Tiêu chuẩn" },
      { serviceCode: "EXPRESS", serviceLabel: "GHN Nhanh" },
    ],
  },
  {
    carrierCode: "mock_express",
    carrierLabel: "Chuyển phát nội địa nhanh",
    services: [
      { serviceCode: "STANDARD", serviceLabel: "Gói Tiêu chuẩn" },
      { serviceCode: "FAST", serviceLabel: "Gói Giao nhanh" },
    ],
  },
];

export function getShopStatusMeta(status: string): StatusMeta {
  return SELLER_SHOP_STATUS_META[status] ?? { label: status || "Chưa rõ", tone: "neutral" };
}

export function getProductStatusMeta(status: string): StatusMeta {
  return SELLER_PRODUCT_STATUS_META[status] ?? { label: status || "Chưa rõ", tone: "neutral" };
}

export function getOrderStatusMeta(status: string): StatusMeta {
  return SELLER_ORDER_STATUS_META[status] ?? { label: status || "Chưa rõ", tone: "neutral" };
}

/**
 * Valid seller actions by order state machine (docs/contracts.md §6):
 * - PENDING: cancel
 * - CONFIRMED: confirm (→ PREPARING), ship (→ SHIPPING), cancel
 * - PREPARING: ship (→ SHIPPING), cancel
 */
export function canSellerConfirmOrder(status: string): boolean {
  return status === "CONFIRMED";
}

export function canSellerShipOrder(status: string): boolean {
  return status === "CONFIRMED" || status === "PREPARING";
}

export function canSellerCancelOrder(status: string): boolean {
  return status === "PENDING" || status === "CONFIRMED" || status === "PREPARING";
}

/**
 * Computes integer VND net receivable for a shop sub-order.
 */
export function computeOrderNetVnd(order: GatewayOrder): number {
  const net =
    order.subtotal_vnd -
    order.flash_sale_discount_vnd -
    order.shop_voucher_discount_vnd -
    order.commission_vnd;
  return Math.max(0, Math.trunc(net));
}

export function getProductCoverImage(product: GatewayProduct): string {
  return (
    product.images?.[0] ||
    product.skus?.find((s) => Boolean(s.image_url))?.image_url ||
    "/products/ao-thun-cotton-trang.png"
  );
}

export function getProductTotalStock(product: GatewayProduct): number {
  return (product.skus ?? []).reduce((sum, s) => sum + (s.is_active ? Math.max(0, s.stock) : 0), 0);
}

export function getProductAvailableStock(product: GatewayProduct): number {
  return (product.skus ?? []).reduce(
    (sum, s) => sum + (s.is_active ? Math.max(0, s.available_stock) : 0),
    0
  );
}

export function mapShopSlugToMockId(slug?: string): string {
  switch (slug) {
    case "xuong-det-bach-dang":
      return "shop-bach-dang";
    case "kho-basic-sai-gon":
      return "shop-sai-gon";
    case "soi-tu-nhien-da-nang":
      return "shop-da-nang";
    case "det-kim-ha-noi":
      return "shop-ha-noi";
    default:
      return "shop-bach-dang";
  }
}
