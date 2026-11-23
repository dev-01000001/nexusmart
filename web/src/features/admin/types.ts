import type { ProductStatus, ShopStatus, UserRole, UserStatus } from "@/lib/gateway-types";
import { GATEWAY_ORDER_STATUS_LABELS } from "@/lib/format";
import type { PillTone } from "./adminMeta";

export const ADMIN_SHOP_STATUS_META: Record<
  ShopStatus | string,
  { label: string; tone: PillTone }
> = {
  PENDING: { label: "Chờ duyệt", tone: "warning" },
  ACTIVE: { label: "Đang hoạt động", tone: "positive" },
  REJECTED: { label: "Từ chối", tone: "negative" },
  SUSPENDED: { label: "Tạm ngưng", tone: "negative" },
};

export const ADMIN_PRODUCT_STATUS_META: Record<
  ProductStatus | string,
  { label: string; tone: PillTone }
> = {
  PENDING: { label: "Chờ duyệt", tone: "warning" },
  APPROVED: { label: "Đã duyệt", tone: "positive" },
  REJECTED: { label: "Từ chối", tone: "negative" },
  HIDDEN: { label: "Đang ẩn", tone: "neutral" },
  OUT_OF_STOCK: { label: "Hết hàng", tone: "negative" },
};

export const ADMIN_USER_STATUS_META: Record<
  UserStatus | string,
  { label: string; tone: PillTone }
> = {
  ACTIVE: { label: "Hoạt động", tone: "positive" },
  PENDING_VERIFICATION: { label: "Chờ xác thực", tone: "warning" },
  LOCKED: { label: "Đã khóa", tone: "negative" },
};

export const ADMIN_USER_ROLE_LABELS: Record<UserRole | string, string> = {
  buyer: "Người mua",
  seller: "Người bán",
  admin: "Quản trị viên",
  super_admin: "Quản trị cấp cao",
};

export const ADMIN_ORDER_STATUS_META: Record<string, { label: string; tone: PillTone }> = {
  PENDING: { label: GATEWAY_ORDER_STATUS_LABELS.PENDING, tone: "warning" },
  CONFIRMED: { label: GATEWAY_ORDER_STATUS_LABELS.CONFIRMED, tone: "warning" },
  PREPARING: { label: GATEWAY_ORDER_STATUS_LABELS.PREPARING, tone: "neutral" },
  SHIPPING: { label: GATEWAY_ORDER_STATUS_LABELS.SHIPPING, tone: "neutral" },
  DELIVERED: { label: GATEWAY_ORDER_STATUS_LABELS.DELIVERED, tone: "positive" },
  COMPLETED: { label: GATEWAY_ORDER_STATUS_LABELS.COMPLETED, tone: "positive" },
  CANCELLED: { label: GATEWAY_ORDER_STATUS_LABELS.CANCELLED, tone: "negative" },
  RETURN_REQUESTED: { label: GATEWAY_ORDER_STATUS_LABELS.RETURN_REQUESTED, tone: "warning" },
  RETURNED: { label: GATEWAY_ORDER_STATUS_LABELS.RETURNED, tone: "negative" },
};

export const RECONCILE_ITEM_STATUS_META: Record<string, { label: string; tone: PillTone }> = {
  MATCHED: { label: "Khớp", tone: "positive" },
  MISSING_LOCAL: { label: "Thiếu trên sàn", tone: "negative" },
  MISSING_PROVIDER: { label: "Thiếu ở đối tác", tone: "warning" },
  AMOUNT_MISMATCH: { label: "Lệch số tiền", tone: "negative" },
};

export function getReconciliationQueueStatusMeta(status: string): {
  label: string;
  tone: PillTone;
} {
  switch ((status ?? "").trim().toLowerCase()) {
    case "pending":
      return { label: "Chờ xử lý", tone: "warning" };
    case "suspended":
      return { label: "Đang tạm treo", tone: "warning" };
    case "assigned":
      return { label: "Đã gán khớp", tone: "positive" };
    case "refunded":
      return { label: "Đã tạo hoàn tiền", tone: "positive" };
    default:
      return { label: "Đang cập nhật", tone: "neutral" };
  }
}

export const ADMIN_AUDIT_TARGET_TYPE_LABELS: Record<string, string> = {
  shop: "Gian hàng",
  user: "Người dùng",
  product: "Sản phẩm",
  category: "Danh mục",
  voucher: "Mã giảm giá",
  flash_sale: "Flash sale",
  flash_sale_item: "Sản phẩm Flash sale",
  coin: "Xu thành viên",
  payment: "Thanh toán",
  refund: "Hoàn tiền",
  reconciliation_item: "Đối soát giao dịch",
  return_request: "Trả hàng / khiếu nại",
  carrier: "Hãng vận chuyển",
  admin: "Hệ thống quản trị",
};

export function getAdminAuditTargetTypeLabel(targetType: string): string {
  const key = (targetType ?? "").trim().toLowerCase();
  if (!key) return "Hệ thống quản trị";
  return ADMIN_AUDIT_TARGET_TYPE_LABELS[key] ?? "Hệ thống quản trị";
}

export function getAdminReturnStatusMeta(status: string): { label: string; tone: PillTone } {
  const key = (status ?? "")
    .trim()
    .toUpperCase()
    .replace(/^RETURN_STATUS_/, "");
  switch (key) {
    case "PENDING":
      return { label: "Chờ gian hàng", tone: "warning" };
    case "AWAITING_SHIPMENT":
      return { label: "Chờ gửi trả", tone: "warning" };
    case "IN_TRANSIT":
      return { label: "Đang hoàn về", tone: "neutral" };
    case "RECEIVED":
      return { label: "Shop đã nhận", tone: "neutral" };
    case "REFUNDING":
      return { label: "Đang hoàn tiền", tone: "warning" };
    case "REFUNDED":
      return { label: "Đã hoàn tiền", tone: "positive" };
    case "REJECTED":
      return { label: "Shop từ chối", tone: "negative" };
    case "ESCALATED":
      return { label: "Cần phân xử", tone: "negative" };
    case "EVIDENCE_REQUESTED":
      return { label: "Chờ bằng chứng", tone: "warning" };
    case "CLOSED":
      return { label: "Đã đóng", tone: "neutral" };
    default:
      return { label: "Đang xử lý", tone: "neutral" };
  }
}

export function getAdminRefundStatusMeta(status: string): { label: string; tone: PillTone } {
  const key = (status ?? "")
    .trim()
    .toUpperCase()
    .replace(/^REFUND_STATUS_/, "");
  if (key === "DONE" || key === "COMPLETED") {
    return { label: "Đã tất toán", tone: "positive" };
  }
  if (key === "FAILED") {
    return { label: "Thất bại", tone: "negative" };
  }
  if (key === "PENDING") {
    return { label: "Chờ chuyển khoản", tone: "warning" };
  }
  return { label: "Đang cập nhật", tone: "neutral" };
}

export const SHIPPING_REGION_LABELS: Record<string, string> = {
  SAME_PROVINCE: "Nội tỉnh / thành phố",
  SAME_REGION: "Nội miền",
  CROSS_REGION: "Liên miền",
};

export function getAdminShopStatusMeta(status: string): { label: string; tone: PillTone } {
  return ADMIN_SHOP_STATUS_META[status] ?? { label: status || "Chưa rõ", tone: "neutral" };
}

export function getAdminProductStatusMeta(status: string): { label: string; tone: PillTone } {
  return ADMIN_PRODUCT_STATUS_META[status] ?? { label: status || "Chưa rõ", tone: "neutral" };
}

export function getAdminUserStatusMeta(status: string): { label: string; tone: PillTone } {
  return ADMIN_USER_STATUS_META[status] ?? { label: status || "Chưa rõ", tone: "neutral" };
}

export function getAdminOrderStatusMeta(status: string): { label: string; tone: PillTone } {
  return ADMIN_ORDER_STATUS_META[status] ?? { label: status || "Chưa rõ", tone: "neutral" };
}

export function formatCommissionBps(bps: number): string {
  const pct = bps / 100;
  return `${pct.toFixed(1).replace(".", ",")}%`;
}
