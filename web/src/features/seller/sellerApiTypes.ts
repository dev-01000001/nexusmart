// Shared DTO types and pure view helpers for Seller Center backend integration.
import { GATEWAY_ORDER_STATUS_LABELS } from "../../lib/format.ts";
import {
  normalizeSalesChannel,
  normalizeStorefrontShopId,
  type SalesChannel,
} from "../../lib/salesChannel.ts";
import { getPaymentStatusInfo } from "../account/orderMeta.ts";

export type PillTone = "neutral" | "pos" | "warn" | "neg";

export interface SellerOrderStatusMeta {
  label: string;
  tone: PillTone;
  description: string;
}

export interface GwSellerShop {
  id: string;
  owner_user_id: string;
  name: string;
  slug?: string;
  description: string;
  logo_url?: string;
  pickup_address_id: string;
  contact_phone: string;
  business_license_doc: string;
  status: string;
  rejection_reason: string;
  suspended_reason?: string;
  suspended_at?: string;
  rating_avg?: number;
  commission_rate_bps?: number;
  created_at?: string;
  updated_at?: string;
}

export interface GwCategory {
  id: string;
  name: string;
  slug: string;
  parent_id: string;
  commission_rate_bps?: number;
  is_prohibited?: boolean;
  sort_order: number;
}

export interface GwSellerSku {
  id: string;
  product_id: string;
  shop_id: string;
  product_name?: string;
  sku_code: string;
  name: string;
  attributes: Record<string, string>;
  price_vnd: number;
  min_price_30d_vnd?: number;
  stock: number;
  reserved_stock: number;
  available_stock: number;
  version: number;
  weight_grams: number;
  image_url: string;
  category_id?: string;
  commission_rate_bps?: number;
  is_active: boolean;
}

export interface GwSellerProduct {
  id: string;
  shop_id: string;
  category_id: string;
  name: string;
  slug?: string;
  description: string;
  images: string[];
  status: string;
  rejection_reason: string;
  flagged_keywords?: string[];
  min_price_vnd: number;
  max_price_vnd: number;
  rating_avg?: number;
  rating_count?: number;
  skus: GwSellerSku[];
  created_at?: string;
  updated_at?: string;
}

export interface GwSellerOrderItem {
  id: string;
  order_id?: string;
  sku_id: string;
  product_id: string;
  product_name: string;
  sku_name: string;
  unit_price_vnd: number;
  effective_unit_price_vnd?: number;
  quantity: number;
  discount_vnd?: number;
  line_total_vnd: number;
  weight_grams?: number;
  flash_sale_item_id?: string;
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

export type GwOrderTimelineEvent = GwSellerOrderTimeline;

export interface GwSellerAddressSnapshot {
  recipient_name: string;
  recipient_phone: string;
  province_code: string;
  province_name: string;
  ward_code: string;
  ward_name: string;
  street_address: string;
}

export type GwOrderShippingAddress = GwSellerAddressSnapshot;

export interface GwSellerOrder {
  id: string;
  order_code: string;
  checkout_group_id?: string;
  buyer_id: string;
  shop_id: string;
  status: string;
  payment_method: string;
  payment_id: string;
  payment_status?: string;
  subtotal_vnd: number;
  flash_sale_discount_vnd: number;
  shop_program_discount_vnd?: number;
  shop_voucher_discount_vnd: number;
  shop_freeship_discount_vnd?: number;
  platform_voucher_discount_vnd: number;
  freeship_discount_vnd: number;
  coins_discount_vnd?: number;
  coin_discount_vnd?: number;
  shipping_fee_vnd: number;
  commission_vnd: number;
  payment_fee_vnd?: number;
  payment_fee_bps?: number;
  /** Optional server-computed net receivable; preferred over client arithmetic when present. */
  seller_net_receivable_vnd?: number;
  total_payable_vnd: number;
  carrier_code: string;
  service_code?: string;
  tracking_code: string;
  shipment_job_status?: string;
  label_url?: string;
  cancel_reason?: string;
  shipping_address?: GwSellerAddressSnapshot | null;
  items: GwSellerOrderItem[];
  timeline?: GwSellerOrderTimeline[];
  sales_channel?: SalesChannel | string;
  storefront_shop_id?: string;
  payment_expires_at?: string;
  delivered_at?: string;
  return_request_id?: string;
  return_status?: string;
  needs_manual_review?: boolean;
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
  total_earned_vnd?: number;
  total_paid_out_vnd?: number;
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
  status?: string;
}

export interface GwFlashSaleItem {
  id: string;
  flash_sale_id: string;
  shop_id: string;
  sku_id: string;
  original_price_vnd?: number;
  flash_price_vnd: number;
  total_slots: number;
  remaining_slots: number;
  per_user_limit: number;
  status: string;
  rejection_reason?: string;
}

export interface GwFlashSale {
  id: string;
  name: string;
  starts_at?: string;
  ends_at?: string;
  min_discount_percent?: number;
  status?: string;
  is_active?: boolean;
  items: GwFlashSaleItem[];
}

export interface GwShopDiscountProgramItem {
  id?: string;
  sku_id: string;
  original_price_vnd: number;
  discounted_price_vnd: number;
  per_user_limit: number;
  price_inflation_flagged?: boolean;
}

export interface GwShopDiscountProgram {
  id: string;
  shop_id: string;
  name: string;
  status: string;
  starts_at?: string;
  ends_at?: string;
  items: GwShopDiscountProgramItem[];
  created_at?: string;
  updated_at?: string;
}

export interface GwSellerBankAccount {
  id: string;
  shop_id: string;
  bank_code: string;
  bank_name: string;
  account_number_masked: string;
  account_holder_name: string;
  holder_name_matched: boolean;
  status: string;
  review_note?: string;
  verified_at?: string;
  payout_locked_until?: string;
  created_at?: string;
  updated_at?: string;
}

export interface GwSellerLedgerEntry {
  id: string;
  transaction_id?: string;
  account_code: string;
  debit_vnd: number;
  credit_vnd: number;
  delta_vnd: number;
  reference_type: string;
  reference_id: string;
  description: string;
  created_at?: string;
}

export interface GwShopShippingSettings {
  shop_id: string;
  enabled_carrier_codes: string[];
  default_weight_grams: number;
  default_length_cm: number;
  default_width_cm: number;
  default_height_cm: number;
  free_ship_enabled: boolean;
  free_ship_min_order_vnd: number;
  free_ship_max_vnd: number;
  updated_at?: string;
}

export interface GwSellerReportDailyPoint {
  date: string;
  order_count: number;
  completed_count?: number;
  cancelled_count?: number;
  returned_count?: number;
  gmv_vnd: number;
  net_revenue_vnd: number;
  commission_vnd: number;
  payment_fee_vnd: number;
}

export interface GwSellerReportTopProduct {
  product_id: string;
  product_name: string;
  units_sold: number;
  revenue_vnd: number;
  order_count?: number;
}

export interface GwSellerReportStatusCount {
  status: string;
  count: number;
}

export interface GwSellerReport {
  shop_id: string;
  from_date: string;
  to_date: string;
  gmv_vnd: number;
  net_revenue_vnd: number;
  commission_vnd: number;
  payment_fee_vnd: number;
  total_orders: number;
  completed_orders: number;
  cancelled_orders: number;
  returned_orders: number;
  cancel_rate_bps: number;
  return_rate_bps: number;
  orders_by_status: GwSellerReportStatusCount[];
  daily: GwSellerReportDailyPoint[];
  top_products: GwSellerReportTopProduct[];
}

export interface GwShipmentEvent {
  id: string;
  shipment_id?: string;
  status: string;
  raw_status?: string;
  carrier_status?: string;
  description: string;
  location?: string;
  occurred_at?: string;
}

export interface GwOrderShipment {
  id: string;
  shipment_id?: string;
  order_id: string;
  shop_id?: string;
  carrier_code: string;
  service_code: string;
  tracking_code: string;
  label_url?: string;
  status: string;
  direction?: string;
  return_request_id?: string;
  fee_vnd: number;
  actual_fee_vnd?: number;
  cod_amount_vnd: number;
  cod_fee_vnd?: number;
  redelivery_count?: number;
  events: GwShipmentEvent[];
  created_at?: string;
  updated_at?: string;
}

export interface SellerShipmentPayload {
  shipment: GwOrderShipment | null;
  events: GwShipmentEvent[];
}

function asNonNegInt(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? Math.max(0, Math.trunc(value)) : 0;
}

function normalizeShipmentEvent(raw: unknown): GwShipmentEvent | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
  const rec = raw as Record<string, unknown>;
  const id = typeof rec.id === "string" ? rec.id.trim() : "";
  const status = typeof rec.status === "string" ? rec.status.trim() : "";
  const description = typeof rec.description === "string" ? rec.description.trim() : "";
  if (!id && !status && !description) return null;
  const shipmentId = typeof rec.shipment_id === "string" ? rec.shipment_id.trim() : "";
  const rawStatus = typeof rec.raw_status === "string" ? rec.raw_status.trim() : "";
  const carrierStatus = typeof rec.carrier_status === "string" ? rec.carrier_status.trim() : "";
  const location = typeof rec.location === "string" ? rec.location.trim() : "";
  const occurredAt = typeof rec.occurred_at === "string" ? rec.occurred_at.trim() : "";
  return {
    id: id || `${status || "ev"}-${occurredAt || "0"}`,
    ...(shipmentId ? { shipment_id: shipmentId } : {}),
    status,
    ...(rawStatus ? { raw_status: rawStatus } : {}),
    ...(carrierStatus ? { carrier_status: carrierStatus } : {}),
    description,
    ...(location ? { location } : {}),
    ...(occurredAt ? { occurred_at: occurredAt } : {}),
  };
}

/**
 * Normalizes gateway `GET /api/v1/shipping/shipments/{order_id}` (`{ shipment, events }`),
 * preserving root-level `events` and `shipment_id` / `fee_vnd` fields.
 */
export function normalizeSellerShipmentResponse(raw: unknown): SellerShipmentPayload {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return { shipment: null, events: [] };
  }
  const root = raw as Record<string, unknown>;
  const rawShipment =
    root.shipment && typeof root.shipment === "object" && !Array.isArray(root.shipment)
      ? (root.shipment as Record<string, unknown>)
      : null;

  const rawEventsSource = Array.isArray(root.events)
    ? root.events
    : rawShipment && Array.isArray(rawShipment.events)
      ? rawShipment.events
      : [];
  const events = rawEventsSource
    .map(normalizeShipmentEvent)
    .filter((ev): ev is GwShipmentEvent => ev !== null);

  if (!rawShipment) {
    return { shipment: null, events };
  }

  const shipmentId =
    (typeof rawShipment.shipment_id === "string" && rawShipment.shipment_id.trim()) ||
    (typeof rawShipment.id === "string" && rawShipment.id.trim()) ||
    "";

  const shipment: GwOrderShipment = {
    id: shipmentId,
    ...(shipmentId ? { shipment_id: shipmentId } : {}),
    order_id: typeof rawShipment.order_id === "string" ? rawShipment.order_id : "",
    ...(typeof rawShipment.shop_id === "string" ? { shop_id: rawShipment.shop_id } : {}),
    carrier_code: typeof rawShipment.carrier_code === "string" ? rawShipment.carrier_code : "",
    service_code: typeof rawShipment.service_code === "string" ? rawShipment.service_code : "",
    tracking_code: typeof rawShipment.tracking_code === "string" ? rawShipment.tracking_code : "",
    ...(typeof rawShipment.label_url === "string" ? { label_url: rawShipment.label_url } : {}),
    status: typeof rawShipment.status === "string" ? rawShipment.status : "",
    ...(typeof rawShipment.direction === "string" ? { direction: rawShipment.direction } : {}),
    ...(typeof rawShipment.return_request_id === "string"
      ? { return_request_id: rawShipment.return_request_id }
      : {}),
    fee_vnd: asNonNegInt(rawShipment.fee_vnd),
    ...(typeof rawShipment.actual_fee_vnd === "number" &&
    Number.isFinite(rawShipment.actual_fee_vnd)
      ? { actual_fee_vnd: Math.max(0, Math.trunc(rawShipment.actual_fee_vnd)) }
      : {}),
    cod_amount_vnd: asNonNegInt(rawShipment.cod_amount_vnd),
    ...(typeof rawShipment.cod_fee_vnd === "number" && Number.isFinite(rawShipment.cod_fee_vnd)
      ? { cod_fee_vnd: Math.max(0, Math.trunc(rawShipment.cod_fee_vnd)) }
      : {}),
    ...(typeof rawShipment.redelivery_count === "number" &&
    Number.isFinite(rawShipment.redelivery_count)
      ? { redelivery_count: Math.max(0, Math.trunc(rawShipment.redelivery_count)) }
      : {}),
    events,
    ...(typeof rawShipment.created_at === "string" ? { created_at: rawShipment.created_at } : {}),
    ...(typeof rawShipment.updated_at === "string" ? { updated_at: rawShipment.updated_at } : {}),
  };

  return { shipment, events };
}

export function resolveSellerShipmentFeeVnd(
  shipment: Pick<GwOrderShipment, "fee_vnd" | "actual_fee_vnd"> | null | undefined
): number {
  if (!shipment) return 0;
  if (typeof shipment.actual_fee_vnd === "number" && shipment.actual_fee_vnd > 0) {
    return Math.trunc(shipment.actual_fee_vnd);
  }
  if (typeof shipment.fee_vnd === "number" && shipment.fee_vnd > 0) {
    return Math.trunc(shipment.fee_vnd);
  }
  return 0;
}

/**
 * Extracts total unread seller chat messages from gateway
 * `GET /api/v1/seller/chat/conversations` (`total_unread` preferred over paginated `items`).
 */
export function extractSellerUnreadChatCount(raw: unknown): number {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return 0;
  const rec = raw as Record<string, unknown>;
  if (typeof rec.total_unread === "number" && Number.isFinite(rec.total_unread)) {
    return Math.max(0, Math.trunc(rec.total_unread));
  }
  if (Array.isArray(rec.items)) {
    return rec.items.reduce((sum, item) => {
      if (!item || typeof item !== "object") return sum;
      const unread = (item as Record<string, unknown>).unread_count;
      return sum + asNonNegInt(unread);
    }, 0);
  }
  return 0;
}

export interface GwSellerReturnEvent {
  id: string;
  return_request_id: string;
  from_status: string;
  to_status: string;
  note: string;
  actor_id: string;
  actor_role: string;
  created_at?: string;
}

export interface GwSellerReturnRequest {
  id: string;
  order_id: string;
  order_code: string;
  buyer_id: string;
  shop_id: string;
  status: string;
  reason: string;
  refund_amount_vnd: number;
  return_fee_vnd: number;
  return_fee_bearer: string;
  evidence_urls: string[];
  seller_response?: string;
  seller_note?: string;
  seller_evidence_urls?: string[];
  dispute_reason?: string;
  admin_note?: string;
  return_tracking_code?: string;
  return_carrier_code?: string;
  return_label_url?: string;
  deadline_at?: string;
  created_at?: string;
  updated_at?: string;
  events?: GwSellerReturnEvent[];
}

export interface SellerOrderAction {
  kind: "confirm" | "ship";
  label: string;
  shortLabel: string;
}

export function getSellerOrderStatusMeta(
  status: string,
  shipmentJobStatus?: string,
  trackingCode?: string
): SellerOrderStatusMeta {
  const normalized = (status ?? "").trim().toUpperCase();
  const job = (shipmentJobStatus ?? "").trim().toUpperCase();
  const hasTracking = Boolean((trackingCode ?? "").trim());
  if ((normalized === "CONFIRMED" || normalized === "PREPARING") && !hasTracking) {
    if (job === "QUEUED") {
      return {
        label: "Đang chờ tạo vận đơn",
        tone: "warn",
        description: "Hệ thống đang chờ tạo vận đơn với hãng vận chuyển.",
      };
    }
    if (job === "FAILED") {
      return {
        label: "Tạo vận đơn thất bại",
        tone: "neg",
        description: "Chưa tạo được vận đơn với hãng vận chuyển. Bạn có thể thử tạo lại ngay.",
      };
    }
  }
  if (normalized === "PREPARING" && hasTracking) {
    return {
      label: "Chờ hãng lấy hàng",
      tone: "warn",
      description: "Đã tạo vận đơn, chờ đơn vị vận chuyển đến lấy kiện hàng.",
    };
  }
  switch (normalized) {
    case "PENDING":
    case "PENDING_PAYMENT":
      return {
        label: GATEWAY_ORDER_STATUS_LABELS.PENDING,
        tone: "warn",
        description: "Đơn đang chờ người mua hoàn tất thanh toán trực tuyến.",
      };
    case "CONFIRMED":
      return {
        label: GATEWAY_ORDER_STATUS_LABELS.CONFIRMED,
        tone: "warn",
        description: "Đơn đã sẵn sàng, chờ gian hàng xác nhận chuẩn bị hàng.",
      };
    case "PREPARING":
      return {
        label: GATEWAY_ORDER_STATUS_LABELS.PREPARING,
        tone: "neutral",
        description: "Gian hàng đang đóng gói và chuẩn bị bàn giao vận chuyển.",
      };
    case "READY_TO_SHIP":
      return {
        label: "Chờ hãng lấy hàng",
        tone: "warn",
        description: "Đã tạo vận đơn, chờ đơn vị vận chuyển đến lấy kiện hàng.",
      };
    case "SHIPPING":
      return {
        label: GATEWAY_ORDER_STATUS_LABELS.SHIPPING,
        tone: "neutral",
        description: "Đơn vị vận chuyển đã tiếp nhận và đang giao đến người mua.",
      };
    case "DELIVERY_FAILED":
      return {
        label: "Giao thất bại",
        tone: "neg",
        description: "Hãng vận chuyển giao hàng chưa thành công.",
      };
    case "DELIVERED":
      return {
        label: GATEWAY_ORDER_STATUS_LABELS.DELIVERED,
        tone: "pos",
        description: "Người mua đã nhận hàng, chờ hoàn tất đối soát.",
      };
    case "COMPLETED":
      return {
        label: GATEWAY_ORDER_STATUS_LABELS.COMPLETED,
        tone: "pos",
        description: "Đơn hàng đã hoàn tất đối soát và giải ngân.",
      };
    case "RETURN_REQUESTED":
      return {
        label: GATEWAY_ORDER_STATUS_LABELS.RETURN_REQUESTED,
        tone: "warn",
        description: "Người mua đã gửi yêu cầu trả hàng / hoàn tiền.",
      };
    case "RETURNED":
      return {
        label: GATEWAY_ORDER_STATUS_LABELS.RETURNED,
        tone: "neg",
        description: "Đơn hàng đã hoàn trả về gian hàng.",
      };
    case "REFUNDED":
      return {
        label: "Đã hoàn tiền",
        tone: "neg",
        description: "Đơn hàng đã được hoàn tiền cho người mua.",
      };
    case "CANCELLED":
      return {
        label: GATEWAY_ORDER_STATUS_LABELS.CANCELLED,
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

export function getNextBackendOrderAction(
  status: string,
  trackingCode?: string,
  shipmentJobStatus?: string
): SellerOrderAction | null {
  const normalized = (status ?? "").trim().toUpperCase();
  const hasTracking = Boolean((trackingCode ?? "").trim());
  const job = (shipmentJobStatus ?? "").trim().toUpperCase();
  switch (normalized) {
    case "CONFIRMED":
      if (!hasTracking && (job === "QUEUED" || job === "FAILED")) {
        return { kind: "ship", label: "Thử tạo lại ngay", shortLabel: "Thử lại" };
      }
      return { kind: "confirm", label: "Xác nhận đơn hàng", shortLabel: "Xác nhận" };
    case "PREPARING": {
      if (hasTracking) return null;
      if (job === "QUEUED" || job === "FAILED") {
        return { kind: "ship", label: "Thử tạo lại ngay", shortLabel: "Thử lại" };
      }
      return { kind: "ship", label: "Tạo vận đơn", shortLabel: "Tạo vận đơn" };
    }
    default:
      return null;
  }
}

export function getSellerProductStatusMeta(status: string): {
  label: string;
  tone: PillTone;
} {
  switch ((status ?? "").toUpperCase()) {
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
  tone: PillTone;
} {
  switch ((status ?? "").toUpperCase()) {
    case "ACTIVE":
      return { label: "Đang hoạt động", tone: "pos" };
    case "PENDING":
      return { label: "Chờ phê duyệt", tone: "warn" };
    case "REJECTED":
      return { label: "Bị từ chối", tone: "neg" };
    case "SUSPENDED":
      return { label: "Tạm ngưng", tone: "neg" };
    default:
      return { label: status || "Đang xử lý", tone: "neutral" };
  }
}

export function formatPaymentMethodVi(method: string): string {
  switch ((method ?? "").toUpperCase()) {
    case "VIETQR":
      return "VietQR";
    case "ZALOPAY":
      return "Ví ZaloPay";
    case "COD":
      return "Thu hộ COD";
    default:
      return method || "Khác";
  }
}

export function formatPaymentStatusVi(
  status: string,
  paymentMethod?: string,
  orderStatus?: string
): string {
  return getPaymentStatusInfo(status, paymentMethod ?? "", orderStatus).label;
}

export function computeSellerOrderDeductions(
  order: Pick<
    GwSellerOrder,
    | "flash_sale_discount_vnd"
    | "shop_program_discount_vnd"
    | "shop_voucher_discount_vnd"
    | "shop_freeship_discount_vnd"
    | "commission_vnd"
    | "payment_fee_vnd"
  >
): number {
  return Math.trunc(
    (order.flash_sale_discount_vnd ?? 0) +
      (order.shop_program_discount_vnd ?? 0) +
      (order.shop_voucher_discount_vnd ?? 0) +
      (order.shop_freeship_discount_vnd ?? 0) +
      (order.commission_vnd ?? 0) +
      (order.payment_fee_vnd ?? 0)
  );
}

/**
 * Computes seller net receivable in integer VND.
 * Uses server-provided `seller_net_receivable_vnd` when present; otherwise deducts
 * flash sale, shop discount program, shop voucher, shop freeship, commission, and payment fee.
 */
export function computeSellerNetReceivable(
  order: Pick<
    GwSellerOrder,
    | "subtotal_vnd"
    | "flash_sale_discount_vnd"
    | "shop_program_discount_vnd"
    | "shop_voucher_discount_vnd"
    | "shop_freeship_discount_vnd"
    | "commission_vnd"
    | "payment_fee_vnd"
    | "seller_net_receivable_vnd"
  >
): number {
  if (
    typeof order.seller_net_receivable_vnd === "number" &&
    Number.isFinite(order.seller_net_receivable_vnd) &&
    order.seller_net_receivable_vnd >= 0
  ) {
    return Math.trunc(order.seller_net_receivable_vnd);
  }
  return Math.max(0, Math.trunc(order.subtotal_vnd) - computeSellerOrderDeductions(order));
}

export function summarizeOrderItems(items: GwSellerOrderItem[]): string {
  if (!items || items.length === 0) return "Chưa có sản phẩm";
  return items
    .map((it) => `${it.product_name}${it.sku_name ? ` (${it.sku_name})` : ""} x${it.quantity}`)
    .join(", ");
}

export function resolvePresentationShopKey(shopName?: string): string {
  const lower = (shopName ?? "").toLowerCase();
  if (lower.includes("sài gòn") || lower.includes("sai gon")) return "shop-sai-gon";
  if (lower.includes("đà nẵng") || lower.includes("da nang")) return "shop-da-nang";
  if (lower.includes("hà nội") || lower.includes("ha noi")) return "shop-ha-noi";
  return "shop-bach-dang";
}
