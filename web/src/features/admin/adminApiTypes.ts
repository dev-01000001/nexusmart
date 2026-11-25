// Shared DTO types and pure display helpers for Admin Portal backend integration.
import type { PillTone } from "./adminMeta.ts";
import {
  normalizeSalesChannel,
  normalizeStorefrontShopId,
  type SalesChannel,
} from "../../lib/salesChannel.ts";

export interface GwAdminShop {
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

export interface GwAccountBalance {
  account_code: string;
  account_type: string;
  debit_total_vnd: number;
  credit_total_vnd: number;
  balance_vnd: number;
}

export interface GwLedgerSummary {
  total_debits_vnd: number;
  total_credits_vnd: number;
  is_balanced: boolean;
  sales_channel?: string;
  accounts: GwAccountBalance[];
}

export interface GwReconciliationItem {
  payment_id: string;
  provider_tx_id: string;
  local_amount_vnd: number;
  provider_amount_vnd: number;
  status: string;
  note: string;
  sales_channel?: string;
  storefront_shop_id?: string;
}

export interface GwReconciliationReport {
  run_id: string;
  matched_count: number;
  missing_local_count: number;
  missing_provider_count: number;
  amount_mismatch_count: number;
  sales_channel?: string;
  items: GwReconciliationItem[];
}

export interface GwAuditLog {
  id: string;
  actor_id: string;
  actor_role: string;
  action: string;
  target_type: string;
  target_id: string;
  before_json: string;
  after_json: string;
  reason: string;
  ip_address: string;
  created_at?: string;
}

export interface GwAdminOrder {
  id: string;
  order_code: string;
  shop_id: string;
  buyer_id: string;
  status: string;
  payment_method: string;
  payment_status: string;
  subtotal_vnd: number;
  commission_vnd: number;
  total_payable_vnd: number;
  sales_channel?: SalesChannel | string;
  storefront_shop_id?: string;
  created_at?: string;
}

export function normalizeAdminOrder(order: GwAdminOrder): GwAdminOrder {
  const salesChannel = normalizeSalesChannel(order.sales_channel);
  return {
    ...order,
    sales_channel: salesChannel,
    storefront_shop_id: normalizeStorefrontShopId(order.storefront_shop_id, salesChannel),
  };
}

export function getAdminOrderStatusMeta(status: string): { label: string; tone: PillTone } {
  const normalized = status
    .trim()
    .toUpperCase()
    .replace(/^ORDER_STATUS_/, "");
  switch (normalized) {
    case "PENDING":
    case "PENDING_PAYMENT":
      return { label: "Chờ thanh toán", tone: "warning" };
    case "PAID":
      return { label: "Đã thanh toán", tone: "positive" };
    case "CONFIRMED":
      return { label: "Chờ xác nhận", tone: "warning" };
    case "PREPARING":
    case "PROCESSING":
    case "PACKING":
      return { label: "Đang chuẩn bị", tone: "neutral" };
    case "SHIPPING":
    case "SHIPPED":
      return { label: "Đang giao", tone: "neutral" };
    case "DELIVERED":
      return { label: "Đã giao", tone: "positive" };
    case "COMPLETED":
      return { label: "Hoàn tất", tone: "positive" };
    case "RETURN_REQUESTED":
      return { label: "Yêu cầu trả hàng", tone: "warning" };
    case "RETURN_APPROVED":
      return { label: "Đã duyệt trả hàng", tone: "warning" };
    case "RETURN_REJECTED":
      return { label: "Từ chối trả hàng", tone: "negative" };
    case "RETURNING":
      return { label: "Đang hoàn hàng", tone: "warning" };
    case "RETURNED":
      return { label: "Đã trả hàng", tone: "negative" };
    case "REFUNDED":
      return { label: "Đã hoàn tiền", tone: "positive" };
    case "CANCELLED":
    case "CANCELED":
      return { label: "Đã hủy", tone: "negative" };
    case "UNSPECIFIED":
    case "":
      return { label: "Đang xử lý", tone: "neutral" };
    default:
      return { label: status.trim() || "Đang xử lý", tone: "neutral" };
  }
}

export interface GwAdminProduct {
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
  skus: Array<{
    id: string;
    sku_code: string;
    name: string;
    price_vnd: number;
    stock: number;
    available_stock: number;
  }>;
  created_at?: string;
}

export interface GwAdminFlashSaleItem {
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

export interface GwAdminFlashSale {
  id: string;
  name: string;
  starts_at?: string;
  ends_at?: string;
  items: GwAdminFlashSaleItem[];
}

export function getAdminShopStatusMeta(status: string): { label: string; tone: PillTone } {
  switch (status.toUpperCase()) {
    case "PENDING":
      return { label: "Chờ duyệt", tone: "warning" };
    case "ACTIVE":
      return { label: "Đã duyệt", tone: "positive" };
    case "REJECTED":
      return { label: "Từ chối", tone: "negative" };
    case "SUSPENDED":
      return { label: "Tạm khóa", tone: "negative" };
    default:
      return { label: status || "Đang xử lý", tone: "neutral" };
  }
}

export function getReconItemStatusMeta(status: string): { label: string; tone: PillTone } {
  switch (status.toUpperCase()) {
    case "MATCHED":
      return { label: "Đã khớp", tone: "positive" };
    case "AMOUNT_MISMATCH":
      return { label: "Lệch số tiền", tone: "negative" };
    case "MISSING_LOCAL":
      return { label: "Thiếu ở hệ thống", tone: "negative" };
    case "MISSING_PROVIDER":
      return { label: "Thiếu ở cổng TT", tone: "warning" };
    default:
      return { label: status || "Đang kiểm tra", tone: "neutral" };
  }
}

export function getAccountTypeLabelVi(accountType: string): string {
  switch (accountType.toUpperCase()) {
    case "ASSET":
      return "Tài sản";
    case "LIABILITY":
      return "Công nợ phải trả";
    case "REVENUE":
      return "Doanh thu sàn";
    case "EXPENSE":
      return "Chi phí / hoàn trả";
    default:
      return accountType || "Tài khoản sổ cái";
  }
}

export function getAuditTargetLabelVi(targetType: string): string {
  switch (targetType.toLowerCase()) {
    case "shop":
      return "Gian hàng";
    case "site":
      return "Site gian hàng";
    case "user":
      return "Tài khoản";
    case "product":
      return "Sản phẩm";
    case "order":
      return "Đơn hàng";
    case "payment":
      return "Đối soát";
    default:
      return targetType || "Hệ thống";
  }
}

export function getAuditActionLabelVi(action: string): string {
  switch (action) {
    case "admin.shop.review":
      return "Thẩm định hồ sơ gian hàng";
    case "admin.user.status":
      return "Cập nhật trạng thái tài khoản";
    case "admin.user.role":
      return "Thay đổi phân quyền tài khoản";
    case "admin.site.suspend":
      return "Tạm khóa site gian hàng";
    case "admin.site.unsuspend":
      return "Mở khóa site gian hàng";
    case "site.save_draft":
      return "Lưu bản nháp site";
    case "site.publish":
      return "Xuất bản site gian hàng";
    case "site.rollback":
      return "Khôi phục phiên bản site";
    default:
      return action || "Thao tác quản trị";
  }
}

export function getActorRoleLabelVi(role: string): string {
  switch (role.toLowerCase()) {
    case "super_admin":
      return "Quản trị tối cao";
    case "admin":
      return "Quản trị viên";
    case "seller":
      return "Người bán";
    case "buyer":
      return "Người mua";
    default:
      return role || "Hệ thống";
  }
}

export function getProductReviewStatusMeta(status: string): { label: string; tone: PillTone } {
  switch (status.toUpperCase()) {
    case "PENDING":
      return { label: "Chờ kiểm duyệt", tone: "warning" };
    case "APPROVED":
      return { label: "Đã duyệt bán", tone: "positive" };
    case "REJECTED":
      return { label: "Đã từ chối", tone: "negative" };
    case "OUT_OF_STOCK":
      return { label: "Hết hàng", tone: "neutral" };
    case "HIDDEN":
      return { label: "Đang ẩn", tone: "neutral" };
    default:
      return { label: status || "Đang xử lý", tone: "neutral" };
  }
}
