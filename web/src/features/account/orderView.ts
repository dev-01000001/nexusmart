// Chuyển đơn hàng từ gateway (snake_case, trạng thái backend) sang kiểu `Order` mà UI
// người mua đang dùng. Hàm thuần, không gọi mạng: dữ liệu trình bày (ảnh, slug, tên màu,
// tên gian hàng) được truyền vào qua `OrderLookups` (catalog dựng từ backend).
// Chỉ import kiểu để chạy được bằng `node --test` (type stripping).
import type {
  Order,
  OrderLineItem,
  OrderStatus,
  OrderTimelineEvent,
  PaymentMethod,
  PaymentStatus,
} from "@/data";

export interface GwAddressSnapshot {
  recipient_name: string;
  recipient_phone: string;
  province_code: string;
  province_name: string;
  ward_code?: string;
  ward_name: string;
  street_address: string;
}

export interface GwOrderItem {
  id: string;
  sku_id: string;
  product_id: string;
  product_name: string;
  sku_name: string;
  unit_price_vnd: number;
  effective_unit_price_vnd: number;
  quantity: number;
  line_total_vnd: number;
}

export interface GwTimelineEvent {
  id: string;
  from_status: string;
  to_status: string;
  actor_role: string;
  note: string;
  created_at?: string;
}

export interface GwOrder {
  id: string;
  order_code: string;
  checkout_group_id: string;
  buyer_id?: string;
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
  total_payable_vnd: number;
  carrier_code: string;
  tracking_code: string;
  cancel_reason: string;
  shipping_address?: GwAddressSnapshot | null;
  items: GwOrderItem[] | null;
  timeline: GwTimelineEvent[] | null;
  payment_expires_at?: string;
  created_at?: string;
  updated_at?: string;
}

/** Phần dữ liệu trình bày của sản phẩm mà dòng đơn cần (khớp cấu trúc `Product` của catalog). */
export interface LineCatalog {
  slug: string;
  name: string;
  image: string;
  colors: Array<{ code: string; name: string; image: string }>;
  skus: Array<{ skuId?: string; skuCode: string; colorCode: string; size: string }>;
}

export interface ShopCatalog {
  slug: string;
  name: string;
}

export interface OrderLookups {
  product(backendId: string): LineCatalog | undefined;
  shop(backendId: string): ShopCatalog | undefined;
}

/** `Order` của UI cộng các trường chỉ có ở backend để quyết định nút thao tác. */
export interface OrderView extends Order {
  checkoutGroupId: string;
  backendStatus: string;
  paymentId: string;
  shopSlug?: string;
  itemSkuIds: Record<string, string>;
  canCancel: boolean;
  canConfirmReceived: boolean;
  canRequestReturn: boolean;
}

const STATUS_MAP: Record<string, OrderStatus> = {
  PENDING: "cho_xac_nhan",
  CONFIRMED: "cho_xac_nhan",
  PREPARING: "dang_chuan_bi",
  SHIPPING: "dang_giao",
  DELIVERED: "da_giao",
  COMPLETED: "da_giao",
  RETURN_REQUESTED: "yeu_cau_tra_hang",
  RETURNED: "yeu_cau_tra_hang",
  CANCELLED: "da_huy",
};

export function mapOrderStatus(status: string): OrderStatus {
  return STATUS_MAP[status.toUpperCase()] ?? "cho_xac_nhan";
}

export function mapPaymentStatus(status: string): PaymentStatus | undefined {
  switch (status.toUpperCase()) {
    case "SUCCEEDED":
      return "da_thanh_toan";
    case "COD_PENDING":
      return "cod_cho_thu";
    case "PENDING":
      return "cho_thanh_toan";
    case "FAILED":
    case "EXPIRED":
    case "CANCELLED":
      return "thanh_toan_that_bai";
    case "REFUNDED":
      return "da_hoan_tien";
    default:
      return undefined;
  }
}

export function mapPaymentMethod(method: string): PaymentMethod {
  return method.toUpperCase() === "VIETQR" ? "vietqr" : "cod";
}

/** Bộ lọc trạng thái backend tương ứng với tab trạng thái của UI. */
export function backendStatusesFor(status: OrderStatus): string[] {
  return Object.entries(STATUS_MAP)
    .filter(([, ui]) => ui === status)
    .map(([backend]) => backend);
}

const RETURN_NOTE_PREFIX = "Return requested: ";

function timelineText(
  ev: GwTimelineEvent,
  order: GwOrder
): { title: string; note: string } | undefined {
  const to = ev.to_status.toUpperCase();
  const from = ev.from_status.toUpperCase();
  const isVietQr = order.payment_method.toUpperCase() === "VIETQR";
  switch (to) {
    case "PENDING":
      return {
        title: "Đặt hàng thành công",
        note: isVietQr
          ? "Đơn đang chờ bạn hoàn tất chuyển khoản VietQR."
          : "Đơn đã được ghi nhận và chuyển tới gian hàng.",
      };
    case "CONFIRMED":
      if (from === "PENDING") {
        return {
          title: "Đã nhận thanh toán",
          note: "Thanh toán thành công, đơn đã chuyển tới gian hàng chuẩn bị.",
        };
      }
      return {
        title: "Đặt hàng thành công",
        note: isVietQr
          ? "Đã xác nhận thanh toán VietQR và chuyển đơn tới gian hàng."
          : "Thanh toán khi nhận hàng (COD), gian hàng sẽ xác nhận đơn sớm.",
      };
    case "PREPARING":
      return {
        title: "Gian hàng đã xác nhận",
        note: "Gian hàng đang đóng gói và chuẩn bị bàn giao vận chuyển.",
      };
    case "SHIPPING":
      return {
        title: "Đã bàn giao vận chuyển",
        note: order.tracking_code
          ? `Kiện hàng đang trên đường giao, mã vận đơn ${order.tracking_code}.`
          : "Kiện hàng đang trên đường giao tới bạn.",
      };
    case "DELIVERED":
      return {
        title: "Giao hàng thành công",
        note: "Kiện hàng đã được giao tới người nhận.",
      };
    case "COMPLETED":
      return { title: "Đơn hàng hoàn tất", note: "Đơn hàng đã hoàn tất. Cảm ơn bạn đã mua sắm." };
    case "CANCELLED":
      return {
        title: ev.actor_role === "buyer" ? "Bạn đã hủy đơn" : "Đơn hàng đã hủy",
        note:
          ev.actor_role === "buyer" && ev.note.trim()
            ? `Lý do: ${ev.note.trim()}`
            : from === "PENDING" && isVietQr
              ? "Đơn tự hủy do chưa hoàn tất thanh toán trong thời hạn."
              : "Đơn hàng đã được hủy trước khi bàn giao vận chuyển.",
      };
    case "RETURN_REQUESTED":
      return {
        title: "Đã gửi yêu cầu trả hàng",
        note: ev.note.startsWith(RETURN_NOTE_PREFIX)
          ? ev.note.slice(RETURN_NOTE_PREFIX.length)
          : "Gian hàng đang xem xét yêu cầu của bạn.",
      };
    case "RETURNED":
      return { title: "Đã hoàn tất trả hàng", note: "Hàng trả đã được gian hàng nhận lại." };
    default:
      return undefined;
  }
}

function mapLine(item: GwOrderItem, lookups: OrderLookups): OrderLineItem {
  const product = lookups.product(item.product_id);
  const sku = product?.skus.find((s) => s.skuId === item.sku_id);
  const color = sku ? product?.colors.find((c) => c.code === sku.colorCode) : undefined;
  return {
    skuCode: sku?.skuCode ?? item.sku_id,
    productId: item.product_id,
    productSlug: product?.slug ?? "",
    productName: product?.name ?? item.product_name,
    colorName: color?.name ?? item.sku_name,
    size: sku?.size ?? "",
    image: color?.image ?? product?.image ?? "",
    unitPrice: item.effective_unit_price_vnd || item.unit_price_vnd,
    quantity: item.quantity,
    lineTotal: item.line_total_vnd,
  };
}

export function mapGatewayOrder(o: GwOrder, lookups: OrderLookups): OrderView {
  const backendStatus = o.status.toUpperCase();
  const shop = lookups.shop(o.shop_id);
  const addr = o.shipping_address;
  const paymentStatus = mapPaymentStatus(o.payment_status);
  const events = o.timeline ?? [];

  const timeline: OrderTimelineEvent[] = [];
  let cancelReason: string | undefined;
  let returnReason: string | undefined;
  for (const ev of events) {
    const text = timelineText(ev, o);
    if (!text) continue;
    timeline.push({
      timestamp: ev.created_at ?? o.created_at ?? "",
      status: mapOrderStatus(ev.to_status),
      title: text.title,
      note: text.note,
    });
    if (ev.to_status.toUpperCase() === "CANCELLED") cancelReason = text.note;
    if (ev.to_status.toUpperCase() === "RETURN_REQUESTED") returnReason = text.note;
  }

  const rawItems = o.items ?? [];
  const mappedItems = rawItems.map((it) => mapLine(it, lookups));
  const itemSkuIds: Record<string, string> = {};
  rawItems.forEach((raw, idx) => {
    const line = mappedItems[idx];
    if (line && raw.sku_id) {
      itemSkuIds[line.skuCode] = raw.sku_id;
    }
  });

  return {
    id: o.id,
    orderCode: o.order_code,
    checkoutGroupId: o.checkout_group_id,
    shopId: o.shop_id,
    shopName: shop?.name ?? "Gian hàng NexusMart",
    shopSlug: shop?.slug,
    buyerName: addr?.recipient_name ?? "",
    buyerPhone: addr?.recipient_phone ?? "",
    shippingAddress: addr
      ? [addr.street_address, addr.ward_name, addr.province_name].filter(Boolean).join(", ")
      : "",
    provinceCode: addr?.province_code ?? "",
    status: mapOrderStatus(backendStatus),
    paymentStatus,
    paymentMethod: mapPaymentMethod(o.payment_method),
    paymentDueAt: paymentStatus === "cho_thanh_toan" ? o.payment_expires_at : undefined,
    isPaid: paymentStatus === "da_thanh_toan",
    carrier: o.carrier_code.toUpperCase() === "GHN" ? "GHN" : undefined,
    createdAt: o.created_at ?? "",
    updatedAt: o.updated_at ?? o.created_at ?? "",
    items: mappedItems,
    itemSkuIds,
    // Giá dòng đã là giá sau flash sale nên tạm tính hiển thị trừ luôn phần flash sale.
    subtotal: o.subtotal_vnd - o.flash_sale_discount_vnd,
    shippingFee: o.shipping_fee_vnd,
    shopVoucherDiscount: o.shop_voucher_discount_vnd,
    platformVoucherDiscount: o.platform_voucher_discount_vnd,
    freeshipDiscount: o.freeship_discount_vnd,
    coinDiscount: o.coins_discount_vnd,
    total: o.total_payable_vnd,
    trackingCode: o.tracking_code || undefined,
    cancelReason: backendStatus === "CANCELLED" ? cancelReason : undefined,
    returnReason,
    timeline,
    backendStatus,
    paymentId: o.payment_id,
    canCancel: ["PENDING", "CONFIRMED", "PREPARING"].includes(backendStatus),
    canConfirmReceived: backendStatus === "DELIVERED",
    canRequestReturn: backendStatus === "DELIVERED",
  };
}
