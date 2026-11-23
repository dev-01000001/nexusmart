import type {
  GatewayOrder,
  GatewayOrderStatus,
  GatewayOrderTimelineEvent,
  GatewayPaymentMethod,
  GatewayShipment,
  GatewayShop,
} from "@/lib/gateway-types";
import {
  GATEWAY_ORDER_STATUS_LABELS,
  GATEWAY_PAYMENT_METHOD_LABELS,
  GATEWAY_SHIPMENT_STATUS_LABELS,
  formatVoucherBenefit,
} from "@/lib/format";
import { isPaymentDeadlinePassed } from "@/features/checkout/helpers";

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
  { key: "RETURN_REQUESTED", label: "Yêu cầu trả hàng" },
  { key: "RETURNED", label: "Đã trả hàng" },
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

/**
 * An unpaid VietQR order is still PENDING and has not succeeded in payment.
 * Cancelling one unpaid VietQR sub-order cancels all remaining PENDING sub-orders in the same group.
 */
export function isUnpaidVietQrOrder(
  order: Pick<GatewayOrder, "status" | "payment_method" | "payment_status"> | null | undefined
): boolean {
  if (!order) return false;
  return (
    order.status.toUpperCase() === "PENDING" &&
    order.payment_method.toUpperCase() === "VIETQR" &&
    order.payment_status.toUpperCase() !== "SUCCEEDED"
  );
}

/**
 * Resolves all unpaid VietQR PENDING sub-orders sharing the same `checkout_group_id` as `target`.
 * Returns `isMultiShopGroup: true` only when cancelling `target` cancels multiple shops together.
 * Paid VietQR and COD orders always return `isMultiShopGroup: false`.
 */
export function resolveUnpaidVietQrCancelGroup<
  T extends Pick<
    GatewayOrder,
    | "id"
    | "order_code"
    | "checkout_group_id"
    | "shop_id"
    | "status"
    | "payment_method"
    | "payment_status"
  >,
>(
  target: T | null | undefined,
  candidateOrders: readonly T[]
): {
  isMultiShopGroup: boolean;
  shopCount: number;
  groupOrders: T[];
} {
  if (!target || !target.checkout_group_id || !isUnpaidVietQrOrder(target)) {
    return {
      isMultiShopGroup: false,
      shopCount: target ? 1 : 0,
      groupOrders: target ? [target] : [],
    };
  }

  const byId = new Map<string, T>();
  byId.set(target.id, target);
  for (const cand of candidateOrders) {
    if (cand.checkout_group_id === target.checkout_group_id && isUnpaidVietQrOrder(cand)) {
      byId.set(cand.id, cand);
    }
  }

  const groupOrders = Array.from(byId.values());
  const distinctShops = new Set(groupOrders.map((o) => o.shop_id).filter(Boolean)).size;
  const shopCount = distinctShops > 0 ? distinctShops : groupOrders.length;

  return {
    isMultiShopGroup: groupOrders.length > 1 && shopCount > 1,
    shopCount,
    groupOrders,
  };
}

/**
 * Natural Vietnamese confirmation copy when cancelling an unpaid multi-shop VietQR order.
 */
export function formatUnpaidGroupCancelWarning(shopCount: number): string {
  return `Hủy đơn chưa thanh toán sẽ hủy toàn bộ lần đặt hàng gồm ${shopCount} gian hàng`;
}

export function needsVietQrPayment(
  order: Pick<
    GatewayOrder,
    "status" | "payment_method" | "payment_id" | "payment_status" | "payment_expires_at"
  >,
  nowMs = Date.now()
): boolean {
  const payStatus = order.payment_status.toUpperCase();
  if (isPaymentDeadlinePassed(order.payment_expires_at, nowMs)) {
    return false;
  }
  return (
    order.status === "PENDING" &&
    order.payment_method.toUpperCase() === "VIETQR" &&
    Boolean(order.payment_id) &&
    payStatus !== "SUCCEEDED" &&
    payStatus !== "EXPIRED" &&
    payStatus !== "CANCELLED"
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
  if (lower === "order created awaiting payment" || lower.includes("awaiting payment")) {
    return "Đơn hàng đã được tạo và đang chờ hoàn tất thanh toán.";
  }
  if (lower.includes("order created and confirmed (cod)")) {
    return "Đơn hàng thanh toán khi nhận hàng đã được xác nhận, chờ gian hàng chuẩn bị.";
  }
  if (lower.includes("order created and confirmed (vietqr)")) {
    return "Đơn hàng VietQR đã được tạo và xác nhận.";
  }
  if (lower.includes("order created and confirmed")) {
    return "Đơn hàng đã được tạo và xác nhận, chờ gian hàng chuẩn bị.";
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
  if (lower.includes("payment refunded") || lower.startsWith("refund ")) {
    return "Hệ thống đã ghi nhận lệnh hoàn tiền cho đơn hàng.";
  }
  if (
    lower.includes("seller confirmed return") ||
    lower.includes("confirmed returned goods") ||
    lower.includes("confirmed receipt of returned")
  ) {
    return "Gian hàng đã xác nhận nhận đủ hàng trả và chuyển bước hoàn tiền.";
  }
  if (lower.includes("seller confirmed")) {
    return "Gian hàng đã xác nhận và bắt đầu đóng gói sản phẩm.";
  }
  const shippedMatch = /^shipped\s+via\s+([^\s(]+)(?:\s*\(tracking:\s*([^)]+)\))?/i.exec(trimmed);
  if (shippedMatch) {
    const carrierName = getCarrierLabel(shippedMatch[1]);
    const track = shippedMatch[2]?.trim();
    return track
      ? `Gian hàng đã tạo vận đơn qua ${carrierName} (mã vận đơn: ${track}).`
      : `Gian hàng đã tạo vận đơn qua ${carrierName}.`;
  }
  if (lower.includes("shipment created")) {
    return "Gian hàng đã tạo vận đơn và đang chờ đơn vị vận chuyển lấy hàng.";
  }
  if (lower.includes("seller shipped") || lower.includes("handed to carrier")) {
    return "Gian hàng đã bàn giao kiện hàng cho đơn vị vận chuyển.";
  }
  if (lower.includes("buyer confirmed receipt")) {
    return "Người mua đã xác nhận nhận đủ hàng.";
  }
  if (lower.includes("seller_confirm_timeout") || lower.includes("seller confirm timeout")) {
    return "Đơn hàng tự động hủy do gian hàng quá hạn xác nhận.";
  }
  if (lower.includes("auto-completed") || lower.includes("auto_complete")) {
    return "Đơn hàng tự động hoàn tất sau 7 ngày kể từ khi giao thành công.";
  }
  if (lower.startsWith("return requested:")) {
    return `Lý do yêu cầu trả hàng: ${trimmed.slice("return requested:".length).trim()}`;
  }
  if (lower.startsWith("cancelled by buyer:")) {
    return `Người mua hủy đơn: ${trimmed.slice("cancelled by buyer:".length).trim()}`;
  }
  if (lower.startsWith("cancelled by seller:")) {
    return `Gian hàng hủy đơn: ${trimmed.slice("cancelled by seller:".length).trim()}`;
  }
  if (lower.startsWith("cancelled by admin:")) {
    return `Quản trị viên hủy đơn: ${trimmed.slice("cancelled by admin:".length).trim()}`;
  }
  if (lower.startsWith("shipment status:")) {
    const st = trimmed.slice("shipment status:".length).trim();
    return `Cập nhật vận chuyển: ${getShipmentStatusInfo(st).label}.`;
  }
  if (lower === "order delivered" || lower === "delivered") {
    return "Đơn vị vận chuyển đã giao hàng thành công.";
  }
  if (lower === "order returned" || lower === "returned") {
    return "Kiện hàng đã hoàn về gian hàng.";
  }
  return trimmed;
}

export const MAX_EVIDENCE_URLS = 5;
export const MAX_EVIDENCE_URL_LENGTH = 500;
export const MAX_REDELIVERY_ATTEMPTS = 3;

export interface SafeExternalEvidenceLink {
  href: string;
  hostname: string;
}

/**
 * Parses an external evidence URL and allows only http/https with a valid hostname.
 * Returns normalized href + lowercase hostname so UI clearly warns before leaving the site.
 */
export function parseSafeEvidenceUrl(
  raw: string | null | undefined
): SafeExternalEvidenceLink | null {
  const trimmed = (raw ?? "").trim();
  if (!trimmed) return null;
  try {
    const parsed = new URL(trimmed);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      return null;
    }
    if (!parsed.hostname) return null;
    return {
      href: parsed.toString(),
      hostname: parsed.hostname.toLowerCase(),
    };
  } catch {
    return null;
  }
}

/**
 * Builds the same-origin BFF shipping label path so the browser sends HttpOnly session cookies
 * instead of navigating directly to the gateway origin without a Bearer token.
 */
export function buildShippingLabelHref(trackingCode: string | null | undefined): string | null {
  const clean = (trackingCode ?? "").trim();
  if (!clean) return null;
  return `/api/gw/shipping/labels/${encodeURIComponent(clean)}`;
}

export function validateEvidenceUrls(rawUrls: readonly string[]): {
  ok: boolean;
  urls: string[];
  error?: string;
} {
  const cleaned: string[] = [];
  for (const raw of rawUrls) {
    const trimmed = raw.trim();
    if (!trimmed) continue;
    if (trimmed.length > MAX_EVIDENCE_URL_LENGTH) {
      return {
        ok: false,
        urls: [],
        error: `Mỗi liên kết bằng chứng không được vượt quá ${MAX_EVIDENCE_URL_LENGTH} ký tự.`,
      };
    }
    try {
      const parsed = new URL(trimmed);
      if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
        return {
          ok: false,
          urls: [],
          error: "Liên kết bằng chứng phải bắt đầu bằng http:// hoặc https://.",
        };
      }
    } catch {
      return {
        ok: false,
        urls: [],
        error: `Liên kết bằng chứng chưa đúng định dạng: ${trimmed}`,
      };
    }
    cleaned.push(trimmed);
  }
  if (cleaned.length > MAX_EVIDENCE_URLS) {
    return {
      ok: false,
      urls: [],
      error: `Chỉ được đính kèm tối đa ${MAX_EVIDENCE_URLS} liên kết bằng chứng.`,
    };
  }
  return { ok: true, urls: cleaned };
}

export function normalizeEnumSuffix(raw: string | undefined, prefix: string): string {
  const upper = (raw ?? "").trim().toUpperCase();
  return upper.startsWith(prefix) ? upper.slice(prefix.length) : upper;
}

export const RETURN_STATUS_INFO: Record<string, StatusMeta> = {
  PENDING: {
    label: "Chờ gian hàng phản hồi",
    tone: "warn",
    description:
      "Gian hàng có 3 ngày để phản hồi. Nếu quá hạn chưa phản hồi, hệ thống tự động chấp nhận trả hàng về.",
  },
  AWAITING_SHIPMENT: {
    label: "Chờ gửi hàng trả về",
    tone: "warn",
    description:
      "Vui lòng đóng gói và bàn giao kiện hàng trả theo mã vận đơn trong vòng 5 ngày để tiếp tục hoàn tiền.",
  },
  IN_TRANSIT: {
    label: "Hàng trả đang vận chuyển",
    tone: "neutral",
    description: "Đơn vị vận chuyển đã lấy hàng trả và đang chuyển về kho gian hàng.",
  },
  RECEIVED: {
    label: "Gian hàng đã nhận hàng trả",
    tone: "neutral",
    description:
      "Gian hàng có 3 ngày để kiểm tra hàng trả và xác nhận hoàn tiền. Quá hạn hệ thống tự xác nhận.",
  },
  REFUNDING: {
    label: "Đang xử lý hoàn tiền",
    tone: "warn",
    description:
      "Yêu cầu trả hàng đã được duyệt. Hệ thống đang tạo lệnh hoàn tiền cho đơn hàng của bạn.",
  },
  REFUNDED: {
    label: "Đã tạo hoàn tiền",
    tone: "pos",
    description:
      "Đơn hàng đã hoàn tất thủ tục trả hàng và ghi nhận lệnh hoàn tiền. Theo dõi tiến trình chuyển khoản bên dưới.",
  },
  REJECTED: {
    label: "Gian hàng từ chối",
    tone: "neg",
    description:
      "Gian hàng đã từ chối yêu cầu. Bạn có 3 ngày để mở khiếu nại lên NexusMart nếu chưa đồng ý.",
  },
  ESCALATED: {
    label: "Đang khiếu nại lên sàn",
    tone: "warn",
    description:
      "Quản trị viên NexusMart đang xem xét chứng từ hai bên để đưa ra quyết định phân xử.",
  },
  EVIDENCE_REQUESTED: {
    label: "Cần bổ sung bằng chứng",
    tone: "warn",
    description:
      "Quản trị viên yêu cầu bổ sung thêm thông tin hoặc liên kết bằng chứng trong vòng 3 ngày.",
  },
  CLOSED: {
    label: "Đã đóng yêu cầu",
    tone: "neutral",
    description: "Yêu cầu trả hàng đã đóng và đơn hàng được chuyển sang trạng thái Hoàn tất.",
  },
};

export function getReturnStatusInfo(rawStatus: string | undefined): StatusMeta {
  const key = normalizeEnumSuffix(rawStatus, "RETURN_STATUS_");
  return (
    RETURN_STATUS_INFO[key] ?? {
      label: "Đang xử lý trả hàng",
      tone: "neutral",
      description: "Yêu cầu trả hàng đang được hệ thống cập nhật.",
    }
  );
}

export function getReturnFeeBearerLabel(rawBearer: string | undefined): string {
  switch ((rawBearer ?? "").trim().toUpperCase()) {
    case "SELLER":
      return "Gian hàng chịu phí";
    case "BUYER":
      return "Người mua chịu phí";
    case "PLATFORM":
      return "Sàn hỗ trợ phí";
    default:
      return "Chưa ấn định";
  }
}

export function getRefundStatusInfo(rawStatus: string | undefined): {
  label: string;
  tone: BadgeTone;
} {
  const key = normalizeEnumSuffix(rawStatus, "REFUND_STATUS_");
  if (key === "DONE" || key === "COMPLETED") {
    return { label: "Đã chuyển khoản hoàn tiền", tone: "pos" };
  }
  if (key === "FAILED") {
    return { label: "Hoàn tiền thất bại", tone: "neg" };
  }
  if (key === "PENDING") {
    return { label: "Chờ chuyển khoản hoàn tiền", tone: "warn" };
  }
  return { label: "Đang cập nhật hoàn tiền", tone: "neutral" };
}

export function isRefundPending(rawStatus: string | undefined): boolean {
  return normalizeEnumSuffix(rawStatus, "REFUND_STATUS_") === "PENDING";
}

export function validateBankAccountNumber(raw: string): boolean {
  const trimmed = raw.trim();
  return /^\d{6,19}$/.test(trimmed);
}

export const VN_BANK_OPTIONS: Array<{ code: string; label: string }> = [
  { code: "VCB", label: "Vietcombank (VCB)" },
  { code: "TCB", label: "Techcombank (TCB)" },
  { code: "MB", label: "MB Bank (MB)" },
  { code: "ACB", label: "ACB" },
  { code: "BIDV", label: "BIDV" },
  { code: "VTB", label: "VietinBank (VTB)" },
  { code: "TPB", label: "TPBank (TPB)" },
  { code: "VPB", label: "VPBank (VPB)" },
  { code: "STB", label: "Sacombank (STB)" },
  { code: "SHB", label: "SHB" },
  { code: "VIB", label: "VIB" },
  { code: "HDB", label: "HDBank (HDB)" },
  { code: "OCB", label: "OCB" },
  { code: "MSB", label: "MSB" },
];

export function canRequestRedelivery(
  shipment: Pick<GatewayShipment, "status" | "direction" | "redelivery_count"> | null | undefined
): boolean {
  if (!shipment) return false;
  const st = normalizeEnumSuffix(shipment.status, "SHIPMENT_STATUS_");
  const dir = normalizeEnumSuffix(shipment.direction || "FORWARD", "SHIPMENT_DIRECTION_");
  if (dir !== "FORWARD" || st !== "FAILED") return false;
  return (shipment.redelivery_count ?? 0) < MAX_REDELIVERY_ATTEMPTS;
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

const VN_PHONE_REGEX = /^(?:0|\+?84)(?:[35789]\d{8}|2\d{9})$/;

/**
 * Strips spaces, hyphens, and dots to match services/user `normalizeVNPhone`.
 */
export function normalizeVietnamesePhone(raw: string): string {
  return raw.trim().replace(/[\s.-]+/g, "");
}

/**
 * Validates Vietnamese mobile/landline phone numbers matching services/user `isValidVNPhone`.
 */
export function isValidVietnamesePhone(raw: string): boolean {
  return VN_PHONE_REGEX.test(normalizeVietnamesePhone(raw));
}

/**
 * Admin `REQUEST_INFO` keeps shop status `PENDING` while setting `rejection_reason`.
 */
export function isShopInfoRequested(
  shop: Pick<GatewayShop, "status" | "rejection_reason"> | null | undefined
): boolean {
  if (!shop) return false;
  return shop.status.toUpperCase() === "PENDING" && Boolean((shop.rejection_reason ?? "").trim());
}

/**
 * Matches services/user `RegisterShop`: allowed when buyer has no shop yet,
 * when shop was `REJECTED`, or when admin sent `REQUEST_INFO` (`PENDING` + reason).
 */
export function canSubmitShopApplication(
  shop: Pick<GatewayShop, "status" | "rejection_reason"> | null | undefined
): boolean {
  if (!shop) return true;
  const status = shop.status.toUpperCase();
  if (status === "REJECTED") return true;
  return status === "PENDING" && Boolean((shop.rejection_reason ?? "").trim());
}

export function getShopStatusInfo(status: string, rejectionReason?: string): StatusMeta {
  switch (status.toUpperCase()) {
    case "PENDING":
      if (rejectionReason && rejectionReason.trim()) {
        return {
          label: "Yêu cầu bổ sung",
          tone: "warn",
          description:
            "Quản trị viên yêu cầu bạn bổ sung hoặc chỉnh sửa hồ sơ gian hàng trước khi xét duyệt lại.",
        };
      }
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
  if (trimmed === "/verify-otp" || trimmed.startsWith("/verify-otp?")) {
    return "/dang-ky";
  }
  if (trimmed === "/seller/dashboard") {
    return "/seller";
  }
  if (trimmed === "/seller/orders") {
    return "/seller/don-hang";
  }
  if (trimmed.startsWith("/seller/orders/")) {
    return `/seller/don-hang/${trimmed.slice("/seller/orders/".length)}`;
  }
  if (trimmed === "/seller/returns" || trimmed.startsWith("/seller/returns/")) {
    return "/seller/tra-hang";
  }
  if (trimmed === "/admin/returns" || trimmed.startsWith("/admin/returns/")) {
    return "/admin/khieu-nai";
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
