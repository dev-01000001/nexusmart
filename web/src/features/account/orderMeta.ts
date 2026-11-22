import type {
  GatewayOrder,
  GatewayOrderStatus,
  GatewayOrderTimelineEvent,
  GatewayPaymentMethod,
} from "@/lib/gateway-types";
import {
  GATEWAY_ORDER_STATUS_LABELS,
  GATEWAY_PAYMENT_METHOD_LABELS,
  GATEWAY_SHIPMENT_STATUS_LABELS,
  formatVoucherBenefit,
} from "@/lib/format";

export { formatVoucherBenefit };

export type BadgeTone = "neutral" | "warn" | "pos" | "neg";

export interface StatusMeta {
  label: string;
  tone: BadgeTone;
  description: string;
}

export const VALID_ORDER_STATUSES: readonly GatewayOrderStatus[] = [
  "PENDING",
  "CONFIRMED",
  "PREPARING",
  "SHIPPING",
  "DELIVERED",
  "COMPLETED",
  "CANCELLED",
  "RETURN_REQUESTED",
  "RETURNED",
];

export const ORDER_STATUS_INFO: Record<GatewayOrderStatus, StatusMeta> = {
  PENDING: {
    label: GATEWAY_ORDER_STATUS_LABELS.PENDING,
    tone: "warn",
    description: "Đơn hàng đang giữ chỗ sản phẩm, chờ bạn hoàn tất thanh toán.",
  },
  CONFIRMED: {
    label: GATEWAY_ORDER_STATUS_LABELS.CONFIRMED,
    tone: "neutral",
    description: "Đơn hàng đã được ghi nhận và chuyển tới gian hàng để chuẩn bị.",
  },
  PREPARING: {
    label: GATEWAY_ORDER_STATUS_LABELS.PREPARING,
    tone: "warn",
    description: "Gian hàng đang kiểm tra và đóng gói sản phẩm cho đơn hàng của bạn.",
  },
  SHIPPING: {
    label: "Đang giao hàng",
    tone: "neutral",
    description: "Đơn vị vận chuyển đã nhận kiện hàng và đang giao tới địa chỉ của bạn.",
  },
  DELIVERED: {
    label: "Đã giao hàng",
    tone: "pos",
    description:
      "Đơn hàng đã được giao thành công. Bạn có thể xác nhận đã nhận hoặc gửi yêu cầu trả hàng trong 7 ngày.",
  },
  COMPLETED: {
    label: GATEWAY_ORDER_STATUS_LABELS.COMPLETED,
    tone: "pos",
    description: "Đơn hàng đã hoàn tất. Cảm ơn bạn đã mua sắm tại NexusMart.",
  },
  CANCELLED: {
    label: GATEWAY_ORDER_STATUS_LABELS.CANCELLED,
    tone: "neg",
    description: "Đơn hàng đã được hủy theo yêu cầu hoặc do quá hạn thanh toán.",
  },
  RETURN_REQUESTED: {
    label: GATEWAY_ORDER_STATUS_LABELS.RETURN_REQUESTED,
    tone: "warn",
    description: "Yêu cầu trả hàng của bạn đã được gửi và đang chờ gian hàng xử lý.",
  },
  RETURNED: {
    label: GATEWAY_ORDER_STATUS_LABELS.RETURNED,
    tone: "neutral",
    description: "Đơn hàng đã hoàn tất thủ tục trả hàng.",
  },
};

export const ORDER_FILTER_TABS: Array<{ key: "" | GatewayOrderStatus; label: string }> = [
  { key: "", label: "Tất cả" },
  { key: "PENDING", label: "Chờ thanh toán" },
  { key: "CONFIRMED", label: "Đã xác nhận" },
  { key: "PREPARING", label: "Đang chuẩn bị" },
  { key: "SHIPPING", label: "Đang giao" },
  { key: "DELIVERED", label: "Đã giao" },
  { key: "COMPLETED", label: "Hoàn tất" },
  { key: "RETURN_REQUESTED", label: "Trả hàng" },
  { key: "CANCELLED", label: "Đã hủy" },
];

export function isValidOrderStatus(value: string | undefined): value is GatewayOrderStatus {
  if (!value) return false;
  return (VALID_ORDER_STATUSES as readonly string[]).includes(value);
}

export function getOrderStatusInfo(status: string): StatusMeta {
  if (isValidOrderStatus(status)) {
    return ORDER_STATUS_INFO[status];
  }
  return {
    label: "Đang xử lý",
    tone: "neutral",
    description: "Đơn hàng đang được cập nhật trạng thái.",
  };
}

export function getPaymentMethodLabel(method: string): string {
  const key = method.toUpperCase() as GatewayPaymentMethod;
  return GATEWAY_PAYMENT_METHOD_LABELS[key] ?? "Thanh toán trực tuyến";
}

export function getPaymentStatusInfo(
  paymentStatus: string,
  paymentMethod: string,
  orderStatus?: string
): { label: string; tone: BadgeTone } {
  const status = paymentStatus.toUpperCase();
  const method = paymentMethod.toUpperCase();
  const ordStatus = (orderStatus ?? "").toUpperCase();

  if (status === "REFUNDED") {
    return { label: "Đã hoàn tiền", tone: "neutral" };
  }
  if (method === "COD") {
    if (ordStatus === "CANCELLED" || ordStatus === "RETURNED") {
      return { label: "Không thu hộ", tone: "neutral" };
    }
    if (ordStatus === "DELIVERED" || ordStatus === "COMPLETED") {
      return { label: "Đã thu hộ COD", tone: "pos" };
    }
    return { label: "Thu hộ khi giao", tone: "neutral" };
  }
  if (status === "SUCCEEDED" || status === "PAID") {
    return { label: "Đã thanh toán", tone: "pos" };
  }
  if (status === "FAILED") {
    return { label: "Thanh toán thất bại", tone: "neg" };
  }
  if (status === "EXPIRED") {
    return { label: "Hết hạn thanh toán", tone: "neg" };
  }
  if (status === "CANCELLED") {
    return { label: "Đã hủy thanh toán", tone: "neutral" };
  }
  if (status === "PENDING") {
    return { label: "Chờ thanh toán", tone: "warn" };
  }
  return { label: "Đang cập nhật", tone: "neutral" };
}

/**
 * State machine permissions per docs/contracts.md section 6:
 * - Cancel: PENDING, CONFIRMED, PREPARING
 * - Confirm received: DELIVERED
 * - Request return: DELIVERED
 * - VietQR retry/wait: PENDING + VIETQR + payment_id + not SUCCEEDED
 */
export function canCancelOrder(status: string): boolean {
  return status === "PENDING" || status === "CONFIRMED" || status === "PREPARING";
}

export function canConfirmReceived(status: string): boolean {
  return status === "DELIVERED";
}

export function canRequestReturn(status: string): boolean {
  return status === "DELIVERED";
}

export function needsVietQrPayment(order: GatewayOrder): boolean {
  return (
    order.status === "PENDING" &&
    order.payment_method.toUpperCase() === "VIETQR" &&
    Boolean(order.payment_id) &&
    order.payment_status.toUpperCase() !== "SUCCEEDED"
  );
}

const SHIPMENT_TONES: Record<string, BadgeTone> = {
  PICKING: "warn",
  DELIVERED: "pos",
  FAILED: "neg",
  RETURNING: "warn",
  CANCELLED: "neg",
};

export function getShipmentStatusInfo(status: string): { label: string; tone: BadgeTone } {
  const key = status.toUpperCase();
  return {
    label: GATEWAY_SHIPMENT_STATUS_LABELS[key] ?? "Đang cập nhật vận chuyển",
    tone: SHIPMENT_TONES[key] ?? "neutral",
  };
}

export function getCarrierLabel(carrierCode: string): string {
  switch (carrierCode.toUpperCase()) {
    case "GHN":
      return "Giao Hàng Nhanh";
    case "GHTK":
      return "Giao Hàng Tiết Kiệm";
    case "SPX":
      return "SPX Express";
    case "MOCK":
    case "MOCK_EXPRESS":
    case "NEXUS_EXPRESS":
      return "Vận chuyển tiêu chuẩn NexusMart";
    default:
      return carrierCode || "Đơn vị vận chuyển";
  }
}

export function localizeTimelineNote(rawNote: string): string {
  const trimmed = rawNote.trim();
  if (!trimmed) return "";
  const lower = trimmed.toLowerCase();
  if (lower.includes("order created and confirmed (cod)")) {
    return "Đơn hàng thanh toán khi nhận hàng đã được xác nhận, chờ gian hàng chuẩn bị.";
  }
  if (lower.includes("awaiting vietqr payment") || lower.includes("order created (vietqr)")) {
    return "Đơn hàng đã được tạo và đang chờ hoàn tất chuyển khoản VietQR.";
  }
  if (lower.includes("payment succeeded")) {
    return "Hệ thống đã xác nhận thanh toán thành công.";
  }
  if (lower.includes("payment expired")) {
    return "Đơn hàng tự động hủy do quá thời hạn thanh toán.";
  }
  if (lower.includes("payment failed")) {
    return "Giao dịch thanh toán chưa thành công.";
  }
  if (lower.includes("seller confirmed")) {
    return "Gian hàng đã xác nhận và bắt đầu đóng gói sản phẩm.";
  }
  if (lower.includes("seller shipped") || lower.includes("handed to carrier")) {
    return "Gian hàng đã bàn giao kiện hàng cho đơn vị vận chuyển.";
  }
  if (lower.includes("buyer confirmed receipt")) {
    return "Người mua đã xác nhận nhận đủ hàng.";
  }
  if (lower.startsWith("return requested:")) {
    return `Lý do yêu cầu trả hàng: ${trimmed.slice("return requested:".length).trim()}`;
  }
  return trimmed;
}

export function getTimelineEventTitle(ev: GatewayOrderTimelineEvent): string {
  const toStatus = ev.to_status;
  switch (toStatus) {
    case "PENDING":
      return "Đặt hàng thành công · Chờ thanh toán";
    case "CONFIRMED":
      return ev.from_status === "PENDING"
        ? "Thanh toán thành công · Đơn hàng đã xác nhận"
        : "Đặt hàng thành công · Đã xác nhận";
    case "PREPARING":
      return "Gian hàng đang chuẩn bị đơn hàng";
    case "SHIPPING":
      return "Đã bàn giao cho đơn vị vận chuyển";
    case "DELIVERED":
      return "Đơn hàng đã giao thành công";
    case "COMPLETED":
      return "Người mua đã xác nhận nhận hàng";
    case "CANCELLED":
      return "Đơn hàng đã hủy";
    case "RETURN_REQUESTED":
      return "Đã gửi yêu cầu trả hàng";
    case "RETURNED":
      return "Hoàn tất trả hàng";
    default:
      return "Cập nhật trạng thái đơn hàng";
  }
}

export function getShopStatusInfo(status: string): StatusMeta {
  switch (status.toUpperCase()) {
    case "PENDING":
      return {
        label: "Đang chờ duyệt",
        tone: "warn",
        description:
          "Hồ sơ đăng ký gian hàng của bạn đã được gửi và đang chờ quản trị viên xét duyệt.",
      };
    case "ACTIVE":
      return {
        label: "Đang hoạt động",
        tone: "pos",
        description:
          "Gian hàng của bạn đã được duyệt. Bạn có thể vào Kênh Người Bán để quản lý sản phẩm và đơn hàng.",
      };
    case "REJECTED":
      return {
        label: "Chưa được duyệt",
        tone: "neg",
        description:
          "Hồ sơ gian hàng chưa đạt yêu cầu. Vui lòng xem lý do và cập nhật đăng ký mới.",
      };
    case "SUSPENDED":
      return {
        label: "Tạm ngưng",
        tone: "neg",
        description: "Gian hàng hiện đang tạm ngưng hoạt động. Vui lòng liên hệ bộ phận hỗ trợ.",
      };
    default:
      return {
        label: "Đang xử lý",
        tone: "neutral",
        description: "Đang cập nhật trạng thái gian hàng.",
      };
  }
}

export function getNotificationCategoryLabel(category: string): string {
  switch (category.toUpperCase()) {
    case "ORDER":
      return "Đơn hàng";
    case "PROMOTION":
      return "Khuyến mãi";
    case "SYSTEM":
      return "Hệ thống";
    default:
      return "Thông báo";
  }
}

/**
 * Normalizes backend notification links safely into internal storefront paths.
 */
export function normalizeNotificationHref(rawLink: string): string | null {
  const trimmed = rawLink.trim();
  if (!trimmed || !trimmed.startsWith("/") || trimmed.startsWith("//") || trimmed.includes("\\")) {
    return null;
  }
  if (trimmed === "/orders") {
    return "/don-hang";
  }
  if (trimmed.startsWith("/orders/")) {
    return `/don-hang/${trimmed.slice("/orders/".length)}`;
  }
  return trimmed;
}

export function getVoucherScopeLabel(scope: string, shopName?: string): string {
  switch (scope.toUpperCase()) {
    case "PLATFORM":
      return "Toàn sàn";
    case "FREESHIP":
      return "Vận chuyển";
    case "SHOP":
      return shopName || "Gian hàng";
    default:
      return "Ưu đãi";
  }
}
