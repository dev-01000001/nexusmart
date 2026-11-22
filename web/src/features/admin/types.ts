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
