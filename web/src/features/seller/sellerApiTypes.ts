// Shared DTO types and pure view helpers for Seller Center backend integration.
import type { OrderStatusMeta } from "@/data";
import {
  normalizeSalesChannel,
  normalizeStorefrontShopId,
  type SalesChannel,
} from "../../lib/salesChannel";

export interface GwSellerShop {
  id: string;
  owner_user_id: string;
  name: string;
  description: string;
  pickup_address_id: string;
  contact_phone: string;
  business_license_doc: string;
  status: string;
  rejection_reason: string;
  created_at?: string;
  updated_at?: string;
}

export interface GwCategory {
  id: string;
  name: string;
  slug: string;
  parent_id: string;
  commission_rate_bps: number;
  sort_order: number;
}

export interface GwSellerSku {
  id: string;
  product_id: string;
  shop_id: string;
  product_name: string;
  sku_code: string;
  name: string;
  attributes: Record<string, string>;
  price_vnd: number;
  stock: number;
  reserved_stock: number;
  available_stock: number;
  version: number;
  weight_grams: number;
  image_url: string;
  category_id: string;
  commission_rate_bps: number;
  is_active: boolean;
}

export interface GwSellerProduct {
  id: string;
  shop_id: string;
  category_id: string;
  name: string;
  description: string;
  images: string[];
  status: string;
  rejection_reason: string;
  min_price_vnd: number;
  max_price_vnd: number;
  skus: GwSellerSku[];
  created_at?: string;
  updated_at?: string;
}

export interface GwSellerOrderItem {
  id: string;
  order_id: string;
  sku_id: string;
  product_id: string;
  product_name: string;
  sku_name: string;
  unit_price_vnd: number;
  effective_unit_price_vnd: number;
  quantity: number;
  line_total_vnd: number;
  weight_grams: number;
  flash_sale_item_id: string;
}

export interface GwSellerOrderTimeline {
  id: string;
  order_id: string;
  from_status: string;
  to_status: string;
  actor_id: string;
  actor_role: string;
  note: string;
  created_at?: string;
}

export interface GwSellerAddressSnapshot {
  recipient_name: string;
  recipient_phone: string;
  province_code: string;
  province_name: string;
  ward_code: string;
  ward_name: string;
  street_address: string;
}

export interface GwSellerOrder {
  id: string;
  order_code: string;
  checkout_group_id: string;
  buyer_id: string;
  shop_id: string;
  status: string;
  payment_method: string;
  payment_id: string;
  payment_status: string;
  subtotal_vnd: number;
  flash_sale_discount_vnd: number;
  shop_voucher_discount_vnd: number;
  platform_voucher_discount_vnd: number;
  freeship_discount_vnd: number;
  coins_discount_vnd: number;
  shipping_fee_vnd: number;
  commission_vnd: number;
  total_payable_vnd: number;
  carrier_code: string;
  service_code: string;
  tracking_code: string;
  label_url: string;
  cancel_reason: string;
  shipping_address?: GwSellerAddressSnapshot | null;
  items: GwSellerOrderItem[];
  timeline: GwSellerOrderTimeline[];
  sales_channel?: SalesChannel | string;
  storefront_shop_id?: string;
  payment_expires_at?: string;
  created_at?: string;
  updated_at?: string;
}

export function normalizeSellerOrder(order: GwSellerOrder): GwSellerOrder {
  const salesChannel = normalizeSalesChannel(order.sales_channel);
  return {
    ...order,
    sales_channel: salesChannel,
    storefront_shop_id: normalizeStorefrontShopId(order.storefront_shop_id, salesChannel),
  };
}

export interface GwSellerBalance {
  shop_id: string;
  pending_vnd: number;
  available_vnd: number;
  total_earned_vnd: number;
}

export interface GwSellerVoucher {
  id: string;
  code: string;
  name: string;
  scope: string;
  shop_id: string;
  discount_type: string;
  value_vnd: number;
  discount_percent: number;
  max_discount_vnd: number;
  min_order_vnd: number;
  quota: number;
  used_count: number;
  remaining_quota: number;
  per_user_limit: number;
  budget_vnd: number;
  used_budget_vnd: number;
  starts_at?: string;
  ends_at?: string;
  is_active: boolean;
}

export interface GwFlashSaleItem {
  id: string;
  flash_sale_id: string;
  shop_id: string;
  sku_id: string;
  flash_price_vnd: number;
  total_slots: number;
  remaining_slots: number;
  per_user_limit: number;
  status: string;
  rejection_reason: string;
}

export interface GwFlashSale {
  id: string;
  name: string;
  starts_at?: string;
  ends_at?: string;
  items: GwFlashSaleItem[];
}

export interface SellerOrderAction {
  kind: "confirm" | "ship";
  label: string;
  shortLabel: string;
}

export function getSellerOrderStatusMeta(status: string): OrderStatusMeta {
  switch (status.toUpperCase()) {
    case "PENDING":
      return {
        label: "Chờ thanh toán",
        tone: "warn",
        description: "Đơn đang chờ người mua hoàn tất thanh toán trực tuyến.",
      };
    case "CONFIRMED":
      return {
        label: "Chờ xác nhận",
        tone: "warn",
        description: "Đơn đã sẵn sàng, chờ gian hàng xác nhận chuẩn bị hàng.",
      };
    case "PREPARING":
      return {
        label: "Đang chuẩn bị",
        tone: "neutral",
        description: "Gian hàng đang đóng gói và chuẩn bị bàn giao vận chuyển.",
      };
    case "SHIPPING":
      return {
        label: "Đang giao",
        tone: "neutral",
        description: "Đơn vị vận chuyển đã tiếp nhận và đang giao đến người mua.",
      };
    case "DELIVERED":
      return {
        label: "Đã giao",
        tone: "pos",
        description: "Người mua đã nhận hàng, chờ hoàn tất đối soát.",
      };
    case "COMPLETED":
      return {
        label: "Hoàn tất",
        tone: "pos",
        description: "Đơn hàng đã hoàn tất đối soát và giải ngân.",
      };
    case "RETURN_REQUESTED":
      return {
        label: "Yêu cầu trả hàng",
        tone: "warn",
        description: "Người mua đã gửi yêu cầu trả hàng / hoàn tiền.",
      };
    case "RETURNED":
      return {
        label: "Đã trả hàng",
        tone: "neg",
        description: "Đơn hàng đã hoàn trả về gian hàng.",
      };
    case "CANCELLED":
      return {
        label: "Đã hủy",
        tone: "neg",
        description: "Đơn hàng đã hủy và hoàn lại tồn kho SKU.",
      };
    default:
      return {
        label: status || "Không xác định",
        tone: "neutral",
        description: "Trạng thái đơn hàng từ hệ thống.",
      };
  }
}

/** Matches OrderService state machine: CONFIRMED -> confirm (PREPARING); PREPARING -> ship (SHIPPING). */
export function getNextBackendOrderAction(status: string): SellerOrderAction | null {
  switch (status.toUpperCase()) {
    case "CONFIRMED":
      return { kind: "confirm", label: "Xác nhận đơn hàng", shortLabel: "Xác nhận" };
    case "PREPARING":
      return { kind: "ship", label: "Bàn giao vận chuyển", shortLabel: "Giao ĐVVC" };
    default:
      return null;
  }
}

export function getSellerProductStatusMeta(status: string): {
  label: string;
  tone: "pos" | "warn" | "neg" | "neutral";
} {
  switch (status.toUpperCase()) {
    case "APPROVED":
      return { label: "Đang bán", tone: "pos" };
    case "PENDING":
      return { label: "Chờ duyệt", tone: "warn" };
    case "REJECTED":
      return { label: "Bị từ chối", tone: "neg" };
    case "HIDDEN":
      return { label: "Đang ẩn", tone: "neutral" };
    case "OUT_OF_STOCK":
      return { label: "Tạm hết hàng", tone: "neg" };
    default:
      return { label: status || "Chờ duyệt", tone: "neutral" };
  }
}

export function getShopStatusLabel(status: string): {
  label: string;
  tone: "pos" | "warn" | "neg" | "neutral";
} {
  switch (status.toUpperCase()) {
    case "ACTIVE":
      return { label: "Đang hoạt động", tone: "pos" };
    case "PENDING":
      return { label: "Chờ phê duyệt", tone: "warn" };
    case "REJECTED":
      return { label: "Bị từ chối", tone: "neg" };
    case "SUSPENDED":
      return { label: "Tạm khóa", tone: "neg" };
    default:
      return { label: status || "Đang xử lý", tone: "neutral" };
  }
}

export function formatPaymentMethodVi(method: string): string {
  switch (method.toUpperCase()) {
    case "VIETQR":
      return "VietQR";
    case "COD":
      return "Thu hộ COD";
    default:
      return method || "Khác";
  }
}

export function formatPaymentStatusVi(status: string): string {
  switch (status.toUpperCase()) {
    case "SUCCEEDED":
      return "Đã thanh toán";
    case "COD_PENDING":
      return "Thu hộ khi giao";
    case "PENDING":
      return "Chờ thanh toán";
    case "FAILED":
      return "Thanh toán thất bại";
    case "EXPIRED":
      return "Hết hạn thanh toán";
    case "CANCELLED":
      return "Đã hủy thanh toán";
    case "REFUNDED":
      return "Đã hoàn tiền";
    default:
      return status || "Đang xử lý";
  }
}

export function computeSellerNetReceivable(order: GwSellerOrder): number {
  const gross =
    order.subtotal_vnd - order.flash_sale_discount_vnd - order.shop_voucher_discount_vnd;
  return Math.max(0, gross - order.commission_vnd);
}

export function summarizeOrderItems(items: GwSellerOrderItem[]): string {
  if (!items || items.length === 0) return "Chưa có sản phẩm";
  return items
    .map((it) => `${it.product_name}${it.sku_name ? ` (${it.sku_name})` : ""} x${it.quantity}`)
    .join(", ");
}

/** Map a backend shop display name to the matching local mock presentation key (used only by mock-only tabs). */
export function resolvePresentationShopKey(shopName?: string): string {
  const lower = (shopName ?? "").toLowerCase();
  if (lower.includes("sài gòn") || lower.includes("sai gon")) return "shop-sai-gon";
  if (lower.includes("đà nẵng") || lower.includes("da nang")) return "shop-da-nang";
  if (lower.includes("hà nội") || lower.includes("ha noi")) return "shop-ha-noi";
  return "shop-bach-dang";
}
