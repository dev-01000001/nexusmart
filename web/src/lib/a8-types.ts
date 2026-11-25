import type {
  GatewayOrder,
  GatewayOrderAddressSnapshot,
  PaginatedResponse,
} from "@/lib/gateway-types";

// ============================================================================
// 1. AdminApprovalsPanel actor lookup helper (Wave 3b-2 effect re-run fix)
// ============================================================================

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * Returns unique valid UUIDs from `candidateIds` that have not yet settled in `settledIds`.
 * Callers must add an ID to `settledIds` only after its lookup completes without cancellation
 * so React StrictMode double-effects or dependency changes never drop actor display names.
 */
export function filterUnresolvedActorIds(
  candidateIds: Iterable<string>,
  settledIds: ReadonlySet<string>
): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of candidateIds) {
    const id = (raw ?? "").trim();
    if (!id || seen.has(id) || settledIds.has(id) || !UUID_PATTERN.test(id)) continue;
    seen.add(id);
    out.push(id);
  }
  return out;
}

// ============================================================================
// 2. UC-B08: Public Guest Order Lookup (POST /api/v1/orders/lookup, G7)
// ============================================================================

export interface OrderLookupRequest {
  order_code: string;
  phone: string;
}

export interface PublicOrderItem {
  product_name: string;
  sku_name: string;
  quantity: number;
  line_total_vnd: number;
}

export interface PublicOrderTimelineEvent {
  to_status: string;
  created_at?: string;
}

export interface PublicOrderSummary {
  order_code: string;
  status: string;
  payment_method: string;
  payment_status: string;
  subtotal_vnd: number;
  shipping_fee_vnd: number;
  total_payable_vnd: number;
  carrier_code: string;
  tracking_code: string;
  province_name: string;
  ward_name: string;
  recipient_name_masked: string;
  recipient_phone_masked: string;
  items: PublicOrderItem[];
  timeline: PublicOrderTimelineEvent[];
  created_at?: string;
  updated_at?: string;
  delivered_at?: string;
}

export interface OrderLookupResponse {
  order: PublicOrderSummary;
}

export function formatPublicOrderArea(
  wardName?: string | null,
  provinceName?: string | null
): string {
  const parts = [(wardName ?? "").trim(), (provinceName ?? "").trim()].filter(Boolean);
  return parts.length > 0 ? parts.join(", ") : "Đang cập nhật khu vực giao hàng";
}

export function formatLookupRateLimitMessage(retryAfterHeader?: string | null): string {
  const raw = (retryAfterHeader ?? "").trim();
  const parsedSec = Number.parseInt(raw, 10);
  if (Number.isFinite(parsedSec) && parsedSec > 0 && parsedSec <= 3600) {
    return `Bạn đã tra cứu quá số lần cho phép. Vui lòng thử lại sau ${parsedSec} giây.`;
  }
  return "Bạn đã tra cứu quá số lần cho phép. Vui lòng đợi ít phút rồi thử lại.";
}

export const ORDER_LOOKUP_NOT_FOUND_MESSAGE =
  "Không tìm thấy đơn hàng khớp mã đơn và số điện thoại nhận hàng. Vui lòng kiểm tra lại thông tin đã đặt.";

/**
 * Maps public order lookup HTTP status / error code strictly to Vietnamese user messages.
 * Never surfaces raw English backend `error.message` strings (W4).
 */
export function translateOrderLookupError(
  status: number,
  errorCode?: string | null,
  retryAfterHeader?: string | null
): string {
  const code = (errorCode ?? "").trim().toUpperCase();
  if (status === 404 || code === "ORDER_LOOKUP_NOT_FOUND" || code === "NOT_FOUND") {
    return ORDER_LOOKUP_NOT_FOUND_MESSAGE;
  }
  if (status === 429 || code === "RATE_LIMITED" || code === "RESOURCE_EXHAUSTED") {
    return formatLookupRateLimitMessage(retryAfterHeader);
  }
  if (
    status === 400 ||
    code === "INVALID_ARGUMENT" ||
    code === "BAD_REQUEST" ||
    code === "INVALID_PHONE"
  ) {
    return "Thông tin tra cứu chưa hợp lệ. Vui lòng kiểm tra lại mã đơn hàng và số điện thoại người nhận.";
  }
  if (
    status >= 500 ||
    code === "UNAVAILABLE" ||
    code === "INTERNAL" ||
    code === "DEADLINE_EXCEEDED"
  ) {
    return "Hệ thống tra cứu đơn hàng đang tạm bận. Vui lòng thử lại sau ít phút.";
  }
  return "Chưa thể tra cứu đơn hàng lúc này. Vui lòng kiểm tra lại thông tin và thử lại sau.";
}

// ============================================================================
// 3. UC-B26: Buyer Pre-Pickup Address Update (PATCH /api/v1/orders/{id}/address, G1–G3)
// ============================================================================

export interface UpdateOrderAddressRequest {
  address_id: string;
  preview_only: boolean;
  accept_fee_increase?: boolean;
  expected_new_shipping_fee_vnd?: number;
  idempotency_key?: string;
}

export interface UpdateOrderAddressResponse {
  order?: GatewayOrder;
  old_shipping_fee_vnd: number;
  new_shipping_fee_vnd: number;
  fee_delta_vnd: number;
  old_total_payable_vnd: number;
  new_total_payable_vnd: number;
  preview_only: boolean;
  fee_adjusted: boolean;
  requires_fee_confirmation: boolean;
  blocked_prepaid_increase: boolean;
  shipment_recreated: boolean;
  shipment_queued: boolean;
  shipment_job_id: string;
  resolved_address?: GatewayOrderAddressSnapshot;
}

/**
 * Top-level `quote` payload returned alongside HTTP 409 `ADDRESS_FEE_CHANGED` (G2).
 */
export interface AddressFeeChangedQuote {
  old_shipping_fee_vnd: number;
  new_shipping_fee_vnd: number;
  fee_delta_vnd: number;
  new_total_payable_vnd: number;
}

/**
 * Strictly validates and extracts the 4 safe-integer VND fields from top-level `quote`
 * returned alongside HTTP 409 `ADDRESS_FEE_CHANGED` (R1).
 * Drops the entire `quote` (returns `null`) if any field is missing or not a safe integer.
 */
export function extractSafeAddressFeeQuote(rawBody: unknown): AddressFeeChangedQuote | null {
  if (!rawBody || typeof rawBody !== "object") return null;
  const quoteRaw = (rawBody as { quote?: unknown }).quote;
  if (!quoteRaw || typeof quoteRaw !== "object" || Array.isArray(quoteRaw)) return null;
  const q = quoteRaw as Record<string, unknown>;
  const oldFee = q.old_shipping_fee_vnd;
  const newFee = q.new_shipping_fee_vnd;
  const delta = q.fee_delta_vnd;
  const newTotal = q.new_total_payable_vnd;
  if (
    typeof oldFee !== "number" ||
    !Number.isSafeInteger(oldFee) ||
    typeof newFee !== "number" ||
    !Number.isSafeInteger(newFee) ||
    typeof delta !== "number" ||
    !Number.isSafeInteger(delta) ||
    typeof newTotal !== "number" ||
    !Number.isSafeInteger(newTotal)
  ) {
    return null;
  }
  return {
    old_shipping_fee_vnd: oldFee,
    new_shipping_fee_vnd: newFee,
    fee_delta_vnd: delta,
    new_total_payable_vnd: newTotal,
  };
}

/**
 * Extracts top-level `quote` from a 409 `ADDRESS_FEE_CHANGED` body (never `error.details`)
 * and merges it onto the previous preview snapshot so the buyer can immediately re-confirm.
 */
export function applyTopLevelAddressFeeQuote(
  rawBody: unknown,
  previousPreview: UpdateOrderAddressResponse,
  paymentMethod: string | null | undefined
): UpdateOrderAddressResponse | null {
  const safeQuote = extractSafeAddressFeeQuote(rawBody);
  if (!safeQuote) return null;
  const prepaid = isPrepaidOrderMethod(paymentMethod);
  const feeDelta = safeQuote.fee_delta_vnd;
  return {
    ...previousPreview,
    old_shipping_fee_vnd: safeQuote.old_shipping_fee_vnd,
    new_shipping_fee_vnd: safeQuote.new_shipping_fee_vnd,
    fee_delta_vnd: feeDelta,
    new_total_payable_vnd: safeQuote.new_total_payable_vnd,
    preview_only: true,
    requires_fee_confirmation: !prepaid && feeDelta > 0,
    blocked_prepaid_increase: prepaid && feeDelta > 0,
  };
}

export type AddressPreviewClassification =
  | "cod_increase"
  | "cod_decrease"
  | "no_fee_change"
  | "prepaid_increase_blocked"
  | "prepaid_unchanged";

export function isPrepaidOrderMethod(method: string | null | undefined): boolean {
  const key = (method ?? "").trim().toUpperCase();
  return key === "VIETQR" || key === "ZALOPAY";
}

/**
 * Matches G1 rules: buyer can request an address update only in CONFIRMED/PREPARING
 * when no async shipment job is actively QUEUED.
 */
export function canBuyerUpdateOrderAddress(
  order?: Pick<GatewayOrder, "status" | "shipment_job_status"> | null
): boolean {
  if (!order) return false;
  const st = (order.status ?? "").trim().toUpperCase();
  if (st !== "CONFIRMED" && st !== "PREPARING") return false;
  const jobSt = (order.shipment_job_status ?? "").trim().toUpperCase();
  return jobSt !== "QUEUED";
}

export function classifyAddressUpdatePreview(
  paymentMethod: string | null | undefined,
  preview: Pick<
    UpdateOrderAddressResponse,
    "fee_delta_vnd" | "requires_fee_confirmation" | "blocked_prepaid_increase"
  >
): AddressPreviewClassification {
  const prepaid = isPrepaidOrderMethod(paymentMethod);
  const delta = Math.trunc(preview.fee_delta_vnd ?? 0);

  if (prepaid) {
    if (preview.blocked_prepaid_increase || delta > 0) {
      return "prepaid_increase_blocked";
    }
    return "prepaid_unchanged";
  }

  if (preview.requires_fee_confirmation || delta > 0) {
    return "cod_increase";
  }
  if (delta < 0) {
    return "cod_decrease";
  }
  return "no_fee_change";
}

export function isAddressPreviewStale(
  previewAddressId: string | null | undefined,
  selectedAddressId: string | null | undefined
): boolean {
  const prev = (previewAddressId ?? "").trim();
  const curr = (selectedAddressId ?? "").trim();
  return !prev || !curr || prev !== curr;
}

export function isAddressUpdateInProgressError(err: unknown): boolean {
  const obj =
    err && typeof err === "object"
      ? (err as { code?: unknown; message?: unknown; status?: unknown })
      : null;
  const code = typeof obj?.code === "string" ? obj.code.trim().toUpperCase() : "";
  const msg = typeof obj?.message === "string" ? obj.message.trim().toLowerCase() : "";
  return (
    code === "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS" ||
    msg.includes("address_update_shipment_in_progress")
  );
}

export function translateAddressUpdateError(err: unknown): string {
  const obj =
    err && typeof err === "object"
      ? (err as { code?: unknown; message?: unknown; status?: unknown })
      : null;
  const code = typeof obj?.code === "string" ? obj.code.trim().toUpperCase() : "";
  const msg = typeof obj?.message === "string" ? obj.message.trim() : "";
  const lower = msg.toLowerCase();

  if (code === "ADDRESS_FEE_INCREASE_PREPAID" || lower.includes("address_fee_increase_prepaid")) {
    return "Phí giao hàng mới cao hơn đơn đã thanh toán trước. Hiện chưa hỗ trợ thu phụ thu thêm — bạn vui lòng giữ địa chỉ cũ hoặc hủy đơn để đặt lại.";
  }
  if (code === "ADDRESS_FEE_CHANGED" || lower.includes("address_fee_changed")) {
    return "Phí giao hàng vừa thay đổi theo báo giá mới nhất. Vui lòng xem lại mức phí mới và xác nhận lại.";
  }
  if (
    code === "ADDRESS_FEE_CONFIRMATION_REQUIRED" ||
    lower.includes("address_fee_confirmation_required")
  ) {
    return "Phí giao hàng mới cao hơn phí hiện tại. Vui lòng xác nhận đồng ý trả thêm khoản chênh lệch.";
  }
  if (
    code === "ADDRESS_UPDATE_CARRIER_PICKED_UP" ||
    lower.includes("address_update_carrier_picked_up") ||
    lower.includes("picked up")
  ) {
    return "Đơn vị vận chuyển đã lấy hàng nên không thể sửa địa chỉ giao hàng nữa.";
  }
  if (
    code === "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS" ||
    lower.includes("address_update_shipment_in_progress")
  ) {
    return "Đơn hàng đang xử lý thay đổi địa chỉ hoặc tạo/cập nhật vận đơn. Nút sửa địa chỉ tạm thời bị khóa cho tới khi hoàn tất.";
  }
  if (code === "ADDRESS_UPDATE_NOT_ALLOWED" || lower.includes("address_update_not_allowed")) {
    return "Đơn hàng ở trạng thái hiện tại không thể sửa địa chỉ giao hàng.";
  }
  if (code === "COD_AREA_NOT_SUPPORTED" || lower.includes("cod_area_not_supported")) {
    return "Khu vực nhận hàng mới chưa hỗ trợ thanh toán khi nhận hàng (COD). Vui lòng chọn địa chỉ khác.";
  }
  if (
    code === "COD_LIMIT_EXCEEDED" ||
    code === "ORDER_AMOUNT_OUT_OF_RANGE" ||
    lower.includes("cod_limit_exceeded")
  ) {
    return "Tổng tiền thu hộ COD sau khi cập nhật phí giao hàng vượt quá hạn mức cho phép.";
  }
  return "Chưa thể cập nhật địa chỉ giao hàng lúc này. Vui lòng thử lại.";
}

// ============================================================================
// 4. S04 & UC-S15: Seller Orders Server Pagination & Batch Operations (G4–G5, G8)
// ============================================================================

export type SellerOrderFilterKey =
  "all" | "CONFIRMED" | "PREPARING" | "SHIPPING" | "DONE" | "OTHER";

export const SELLER_ORDERS_PAGE_SIZE = 20;
export const MAX_BATCH_SHIP_ORDERS = 50;
export const BATCH_POLL_MAX_OFF_PAGE_PER_TICK = 5;
export const BATCH_POLL_MAX_TICKS = 15;

/**
 * Returns the clamped last page when a paginated response returns `items.length === 0`
 * while `total > 0` and `currentPage > 1` (R5), or `null` when no redirect is needed.
 */
export function resolveClampedPageOnEmpty(
  currentPage: number,
  itemCount: number,
  total: number,
  pageSize: number
): number | null {
  const safePage = Math.max(1, Math.trunc(currentPage || 1));
  const safeTotal = Math.max(0, Math.trunc(total || 0));
  const safeSize = Math.max(1, Math.trunc(pageSize || 1));
  if (safePage <= 1 || itemCount > 0 || safeTotal <= 0) {
    return null;
  }
  const lastPage = Math.max(1, Math.ceil(safeTotal / safeSize));
  return safePage > lastPage ? lastPage : null;
}

export interface TrackedBatchOrderState {
  tracking_code: string;
  status: string;
  shipment_job_status: string;
}

const TERMINAL_ORDER_STATUSES = new Set(["CANCELLED", "RETURNED", "DELIVERED", "COMPLETED"]);

/**
 * Selects up to `maxPerTick` off-page QUEUED batch-ship order IDs to poll via
 * `GET /seller/orders/{id}` using round-robin cursoring (R7).
 * Never re-polls orders already on the current page, orders that already have a
 * `tracking_code`, orders whose shipment job is `FAILED`, or terminal orders.
 */
export function selectOffPageOrdersToPoll(
  batchResults: ReadonlyArray<Pick<BatchShipOrderResult, "order_id" | "outcome" | "tracking_code">>,
  pageOrdersById: ReadonlyMap<
    string,
    Pick<GatewayOrder, "tracking_code" | "status" | "shipment_job_status">
  >,
  trackedById: Readonly<Record<string, TrackedBatchOrderState | undefined>>,
  cursor = 0,
  maxPerTick = BATCH_POLL_MAX_OFF_PAGE_PER_TICK
): { ids: string[]; pendingCount: number; nextCursor: number } {
  const pendingIds: string[] = [];
  const seen = new Set<string>();

  for (const row of batchResults) {
    if ((row.outcome ?? "").trim().toUpperCase() !== "QUEUED") continue;
    const id = (row.order_id ?? "").trim();
    if (!id || seen.has(id)) continue;
    seen.add(id);

    // Orders already visible on the current page are refreshed by loadOrders().
    if (pageOrdersById.has(id)) continue;

    const tracked = trackedById[id];
    const tracking = (tracked?.tracking_code || row.tracking_code || "").trim();
    if (tracking) continue;

    const jobStatus = (tracked?.shipment_job_status ?? "").trim().toUpperCase();
    if (jobStatus === "FAILED") continue;

    const orderStatus = (tracked?.status ?? "").trim().toUpperCase();
    if (orderStatus && TERMINAL_ORDER_STATUSES.has(orderStatus)) continue;

    pendingIds.push(id);
  }

  if (pendingIds.length === 0) {
    return { ids: [], pendingCount: 0, nextCursor: 0 };
  }

  const limit = Math.max(1, Math.min(pendingIds.length, Math.trunc(maxPerTick || 1)));
  const start =
    ((Math.trunc(cursor || 0) % pendingIds.length) + pendingIds.length) % pendingIds.length;
  const ids: string[] = [];
  for (let i = 0; i < limit; i += 1) {
    ids.push(pendingIds[(start + i) % pendingIds.length]);
  }
  return {
    ids,
    pendingCount: pendingIds.length,
    nextCursor: (start + limit) % pendingIds.length,
  };
}

export interface SellerOrderStatusCount {
  status: string;
  count: number;
}

export interface SellerOrdersPaginatedResponse extends PaginatedResponse<GatewayOrder> {
  status_counts?: SellerOrderStatusCount[];
}

export type BatchShipOrderOutcome = "QUEUED" | "SKIPPED";

export interface BatchShipOrderResult {
  order_id: string;
  order_code: string;
  outcome: BatchShipOrderOutcome | string;
  reason_code: string;
  message: string;
  shipment_job_id: string;
  tracking_code: string;
}

export interface BatchShipOrdersRequest {
  order_ids: string[];
  carrier_code?: string;
  service_code?: string;
}

export interface BatchShipOrdersResponse {
  results: BatchShipOrderResult[];
  queued_count: number;
  skipped_count: number;
}

export interface PickingListItem {
  sku_id: string;
  product_id: string;
  product_name: string;
  sku_name: string;
  total_quantity: number;
  order_count: number;
  order_codes: string[];
}

export interface PickingListResponse {
  items: PickingListItem[];
  order_count: number;
  total_units: number;
}

export function buildSellerOrdersQuery(params: {
  page: number;
  pageSize?: number;
  filter: SellerOrderFilterKey;
  carrierCode?: string;
  paymentMethod?: string;
  keyword?: string;
  dueBeforeDate?: string;
  includeStatusCounts?: boolean;
}): string {
  const qs = new URLSearchParams();
  qs.set("page", String(Math.max(1, Math.trunc(params.page || 1))));
  qs.set(
    "page_size",
    String(Math.max(1, Math.min(50, Math.trunc(params.pageSize ?? SELLER_ORDERS_PAGE_SIZE))))
  );

  if (
    params.filter === "CONFIRMED" ||
    params.filter === "PREPARING" ||
    params.filter === "SHIPPING"
  ) {
    qs.set("status", params.filter);
  } else if (params.filter === "DONE") {
    qs.set("statuses", "DELIVERED,COMPLETED");
  } else if (params.filter === "OTHER") {
    qs.set("statuses", "PENDING,CANCELLED,RETURN_REQUESTED,RETURNED");
  }

  const carrier = (params.carrierCode ?? "").trim();
  if (carrier && carrier !== "all") {
    qs.set("carrier_code", carrier);
  }

  const payment = (params.paymentMethod ?? "").trim().toUpperCase();
  if (payment && payment !== "ALL") {
    qs.set("payment_method", payment);
  }

  const keyword = (params.keyword ?? "").trim();
  if (keyword) {
    qs.set("keyword", keyword);
  }

  const dueDate = (params.dueBeforeDate ?? "").trim();
  if (/^\d{4}-\d{2}-\d{2}$/.test(dueDate)) {
    qs.set("due_before", `${dueDate}T23:59:59+07:00`);
  }

  if (params.includeStatusCounts !== false) {
    qs.set("include_status_counts", "true");
  }

  return qs.toString();
}

export function aggregateSellerOrderStatusCounts(
  statusCounts: readonly SellerOrderStatusCount[] | null | undefined,
  fallbackTotal = 0
): Record<SellerOrderFilterKey, number> {
  const out: Record<SellerOrderFilterKey, number> = {
    all: 0,
    CONFIRMED: 0,
    PREPARING: 0,
    SHIPPING: 0,
    DONE: 0,
    OTHER: 0,
  };
  if (!Array.isArray(statusCounts) || statusCounts.length === 0) {
    out.all = Math.max(0, Math.trunc(fallbackTotal));
    return out;
  }
  for (const row of statusCounts) {
    const count = Math.max(0, Math.trunc(row?.count ?? 0));
    const st = (row?.status ?? "").trim().toUpperCase();
    out.all += count;
    if (st === "CONFIRMED") out.CONFIRMED += count;
    else if (st === "PREPARING") out.PREPARING += count;
    else if (st === "SHIPPING") out.SHIPPING += count;
    else if (st === "DELIVERED" || st === "COMPLETED") out.DONE += count;
    else if (
      st === "PENDING" ||
      st === "CANCELLED" ||
      st === "RETURN_REQUESTED" ||
      st === "RETURNED"
    ) {
      out.OTHER += count;
    }
  }
  return out;
}

export function toggleBatchSelection(
  currentIds: readonly string[],
  targetId: string,
  maxItems = MAX_BATCH_SHIP_ORDERS
): { next: string[]; capped: boolean } {
  if (currentIds.includes(targetId)) {
    return { next: currentIds.filter((id) => id !== targetId), capped: false };
  }
  if (currentIds.length >= maxItems) {
    return { next: [...currentIds], capped: true };
  }
  return { next: [...currentIds, targetId], capped: false };
}

export function collectBatchTrackingCodes(
  orders: ReadonlyArray<Pick<GatewayOrder, "tracking_code">>,
  maxItems = MAX_BATCH_SHIP_ORDERS
): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const ord of orders) {
    const code = (ord.tracking_code ?? "").trim();
    if (!code || seen.has(code)) continue;
    seen.add(code);
    out.push(code);
    if (out.length >= maxItems) break;
  }
  return out;
}

const BATCH_SHIP_REASON_LABELS: Record<string, string> = {
  ORDER_NOT_FOUND: "Không tìm thấy đơn hàng",
  FORBIDDEN: "Đơn hàng không thuộc gian hàng của bạn",
  ORDER_CANCELLED: "Đơn hàng đã bị hủy",
  INVALID_STATUS: "Trạng thái đơn không cho phép tạo vận đơn",
  ALREADY_HAS_SHIPMENT: "Đơn đã có vận đơn trước đó",
  ALREADY_QUEUED: "Đơn đã nằm trong hàng đợi tạo vận đơn",
  PAYMENT_NOT_INITIALIZED: "Thanh toán chưa sẵn sàng",
  SHOP_PICKUP_ADDRESS_REQUIRED: "Gian hàng chưa cấu hình địa chỉ lấy hàng hợp lệ",
  UNSUPPORTED_WARD: "Phường/xã chưa hỗ trợ hãng vận chuyển đã chọn",
  CARRIER_REJECTED: "Hãng vận chuyển từ chối tạo vận đơn",
  ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS: "Đơn đang xử lý thay đổi địa chỉ",
  INTERNAL_ERROR: "Lỗi hệ thống tạm thời, vui lòng thử lại",
};

/**
 * Translates backend batch-ship skip reason codes strictly to Vietnamese (R6).
 * Unknown codes never fall back to raw English backend messages or raw enum codes.
 */
export function translateBatchShipReasonCode(reasonCode?: string): string {
  const key = (reasonCode ?? "").trim().toUpperCase();
  if (key && BATCH_SHIP_REASON_LABELS[key]) {
    return BATCH_SHIP_REASON_LABELS[key];
  }
  return "Chưa thể tạo vận đơn cho đơn này";
}

// ============================================================================
// 5. S03 & UC-S13: Seller Products Pagination, Stock History & CSV Import (G6, G8)
// ============================================================================

export type SellerProductFilterKey =
  "all" | "APPROVED" | "PENDING" | "REJECTED" | "HIDDEN" | "LOW_OOS";

export const SELLER_PRODUCT_TAB_FILTER_KEYS: readonly SellerProductFilterKey[] = [
  "all",
  "APPROVED",
  "PENDING",
  "REJECTED",
  "HIDDEN",
  "LOW_OOS",
];

export type SellerProductTabCounts = Record<SellerProductFilterKey, number | null>;

export function createInitialSellerProductTabCounts(): SellerProductTabCounts {
  return {
    all: null,
    APPROVED: null,
    PENDING: null,
    REJECTED: null,
    HIDDEN: null,
    LOW_OOS: null,
  };
}

/**
 * Merges parallel `page=1&page_size=1` count results for all seller product tabs (including
 * `LOW_OOS` via `low_stock=true`). Rejected entries resolve to `null` so the UI hides unavailable
 * counts instead of displaying a misleading `0` (S1).
 */
export function settleSellerProductTabCounts(
  entries: ReadonlyArray<{ key: SellerProductFilterKey; result: PromiseSettledResult<number> }>
): SellerProductTabCounts {
  const next = createInitialSellerProductTabCounts();
  for (const entry of entries) {
    if (entry.result.status === "fulfilled" && Number.isFinite(entry.result.value)) {
      next[entry.key] = Math.max(0, Math.trunc(entry.result.value));
    } else {
      next[entry.key] = null;
    }
  }
  return next;
}

/**
 * Formats the seller products page header summary line, omitting any count segment that is `null`
 * (loading or failed) so unavailable counts are hidden rather than rendered as `0` (S1).
 */
export function formatSellerProductsHeaderSummary(
  shopName: string,
  counts: SellerProductTabCounts,
  formatCount: (n: number) => string = (n) => String(n)
): string {
  const parts: string[] = [shopName];
  if (counts.all !== null) parts.push(`${formatCount(counts.all)} sản phẩm`);
  if (counts.APPROVED !== null) parts.push(`${formatCount(counts.APPROVED)} đang bán`);
  if (counts.PENDING !== null) parts.push(`${formatCount(counts.PENDING)} chờ duyệt`);
  if (counts.LOW_OOS !== null) parts.push(`${formatCount(counts.LOW_OOS)} hết / sắp hết`);
  return parts.join(" · ");
}

export const SELLER_PRODUCTS_PAGE_SIZE = 20;
export const STOCK_HISTORY_PAGE_SIZE = 20;
export const MAX_IMPORT_CSV_BYTES = 1024 * 1024; // 1 MB (G6)
export const MAX_IMPORT_CSV_ROWS = 1000;

export type StockHistoryChangeType =
  "MANUAL_UPDATE" | "RESERVE" | "RELEASE" | "CONFIRM" | "RESTOCK";

export interface StockHistoryItem {
  id: string;
  sku_id: string;
  product_id: string;
  shop_id: string;
  product_name: string;
  sku_code: string;
  sku_name: string;
  order_id: string;
  change_type: StockHistoryChangeType | string;
  delta: number;
  stock_before: number;
  stock_after: number;
  reserved_before: number;
  reserved_after: number;
  actor_id: string;
  reason: string;
  created_at?: string;
}

export const STOCK_HISTORY_CHANGE_META: Record<
  string,
  { label: string; tone: "neutral" | "warn" | "pos" | "neg" }
> = {
  MANUAL_UPDATE: { label: "Cập nhật thủ công", tone: "neutral" },
  RESERVE: { label: "Giữ tồn đặt đơn", tone: "warn" },
  RELEASE: { label: "Hoàn tồn hủy đơn", tone: "pos" },
  CONFIRM: { label: "Xuất kho xác nhận", tone: "neg" },
  RESTOCK: { label: "Nhập lại kho", tone: "pos" },
};

const STOCK_HISTORY_TECHNICAL_REASONS: Record<string, string> = {
  "seller product update": "Cập nhật tồn kho từ trang chỉnh sửa sản phẩm",
  "seller stock update": "Cập nhật tồn kho thủ công",
  "manual stock update": "Cập nhật tồn kho thủ công",
  "update stock": "Cập nhật tồn kho thủ công",
  "order stock reservation": "Giữ tồn kho cho đơn hàng mới",
  "order stock release": "Hoàn tồn kho khi hủy đơn hàng",
  "order stock confirmation": "Xuất kho sau khi xác nhận đơn hàng",
  "order item restock": "Nhập lại kho từ đơn trả hàng",
  "create sku": "Khởi tạo tồn kho biến thể",
};

const STOCK_HISTORY_REASON_BY_TYPE: Record<string, string> = {
  MANUAL_UPDATE: "Cập nhật tồn kho thủ công",
  RESERVE: "Giữ tồn kho cho đơn hàng",
  RELEASE: "Hoàn tồn kho khi hủy đơn hàng",
  CONFIRM: "Xuất kho sau khi xác nhận đơn hàng",
  RESTOCK: "Nhập lại kho từ đơn trả hàng",
};

const PURE_ASCII_TECH_TEXT_RE = /^[a-zA-Z0-9\s_.:/(),-]+$/;

/**
 * Translates backend technical English `stock_history.reason` values into Vietnamese (R6).
 */
export function formatStockHistoryReason(
  changeType?: string | null,
  rawReason?: string | null
): string {
  const cleanReason = (rawReason ?? "").trim();
  const normalizedReason = cleanReason.toLowerCase();
  if (normalizedReason && STOCK_HISTORY_TECHNICAL_REASONS[normalizedReason]) {
    return STOCK_HISTORY_TECHNICAL_REASONS[normalizedReason];
  }
  const typeKey = (changeType ?? "").trim().toUpperCase();
  const typeFallback = STOCK_HISTORY_REASON_BY_TYPE[typeKey] ?? "Cập nhật tồn kho hệ thống";
  if (!cleanReason || PURE_ASCII_TECH_TEXT_RE.test(cleanReason)) {
    return typeFallback;
  }
  return cleanReason;
}

export type ProductImportJobStatus =
  "QUEUED" | "VALIDATING" | "READY" | "CONFIRMING" | "COMPLETED" | "FAILED";

export type ProductImportRowStatus = "PENDING" | "VALID" | "INVALID" | "CREATED" | "FAILED";

export interface ProductImportJob {
  id: string;
  shop_id: string;
  created_by: string;
  filename: string;
  status: ProductImportJobStatus | string;
  total_rows: number;
  valid_rows: number;
  invalid_rows: number;
  created_rows: number;
  failed_rows: number;
  error_message: string;
  created_at?: string;
  updated_at?: string;
  confirmed_at?: string;
  finished_at?: string;
}

export interface ProductImportRow {
  id: string;
  job_id: string;
  row_number: number;
  status: ProductImportRowStatus | string;
  error_code: string;
  error_message: string;
  product_name: string;
  sku_code: string;
  category_id: string;
  price_vnd: number;
  stock: number;
  weight_grams: number;
  created_product_id: string;
  raw_row_json: string;
}

export interface ProductImportJobResponse {
  job: ProductImportJob;
}

export interface ConfirmProductImportJobResponse {
  job: ProductImportJob;
  already_confirmed: boolean;
}

export const IMPORT_JOB_STATUS_META: Record<
  string,
  { label: string; tone: "neutral" | "warn" | "pos" | "neg" }
> = {
  QUEUED: { label: "Đang xếp hàng", tone: "warn" },
  VALIDATING: { label: "Đang kiểm tra dữ liệu", tone: "warn" },
  READY: { label: "Sẵn sàng xác nhận", tone: "pos" },
  CONFIRMING: { label: "Đang tạo sản phẩm", tone: "warn" },
  COMPLETED: { label: "Hoàn tất nhập", tone: "pos" },
  FAILED: { label: "Thất bại", tone: "neg" },
};

export const IMPORT_ROW_STATUS_META: Record<
  string,
  { label: string; tone: "neutral" | "warn" | "pos" | "neg" }
> = {
  PENDING: { label: "Chờ kiểm tra", tone: "neutral" },
  VALID: { label: "Hợp lệ", tone: "pos" },
  INVALID: { label: "Dữ liệu lỗi", tone: "neg" },
  CREATED: { label: "Đã tạo", tone: "pos" },
  FAILED: { label: "Tạo thất bại", tone: "neg" },
};

export function isImportJobPollingStatus(status: string | null | undefined): boolean {
  const key = (status ?? "").trim().toUpperCase();
  return key === "QUEUED" || key === "VALIDATING" || key === "CONFIRMING";
}

/**
 * Finished CSV import jobs (COMPLETED/FAILED) reset back to Step 1 when reopening the modal (R10).
 * Jobs in READY or active polling statuses are preserved.
 */
export function shouldResetImportJobOnModalOpen(status: string | null | undefined): boolean {
  const key = (status ?? "").trim().toUpperCase();
  return key === "COMPLETED" || key === "FAILED";
}

export function validateImportCsvFile(file: {
  name: string;
  size: number;
}): { ok: true } | { ok: false; message: string } {
  const name = (file.name ?? "").trim().toLowerCase();
  if (!name.endsWith(".csv")) {
    return {
      ok: false,
      message: "Vui lòng chọn tệp định dạng .csv (mã hóa UTF-8).",
    };
  }
  if (!Number.isFinite(file.size) || file.size <= 0) {
    return { ok: false, message: "Tệp CSV đang trống, vui lòng kiểm tra lại." };
  }
  if (file.size > MAX_IMPORT_CSV_BYTES) {
    return {
      ok: false,
      message: "Dung lượng tệp CSV vượt quá giới hạn tối đa 1 MB.",
    };
  }
  return { ok: true };
}

/**
 * Translates CSV product import errors to clean Vietnamese messages (W5, R9).
 */
export function translateProductImportError(
  err: unknown,
  fallback = "Không thể xử lý tệp nhập CSV lúc này. Vui lòng thử lại."
): string {
  const obj =
    err && typeof err === "object"
      ? (err as { code?: unknown; message?: unknown; status?: unknown })
      : null;
  const code = typeof obj?.code === "string" ? obj.code.trim().toUpperCase() : "";
  const status = typeof obj?.status === "number" ? obj.status : 0;
  const lower = typeof obj?.message === "string" ? obj.message.trim().toLowerCase() : "";

  if (
    code === "IMPORT_JOB_IN_PROGRESS" ||
    lower.includes("import_job_in_progress") ||
    (status === 409 &&
      (lower.includes("job in progress") ||
        lower.includes("import job already") ||
        lower.includes("already in progress")))
  ) {
    return "Gian hàng đang có một tệp nhập đang xử lý. Vui lòng chờ đợt nhập hiện tại hoàn tất rồi thử lại.";
  }
  if (code === "IMPORT_JOB_NOT_READY" || lower.includes("import_job_not_ready")) {
    return "Đợt nhập chưa ở trạng thái sẵn sàng xác nhận hoặc không có dòng hợp lệ.";
  }
  if (
    code === "IMPORT_INVALID_UTF8" ||
    lower.includes("import_invalid_utf8") ||
    lower.includes("invalid utf-8") ||
    lower.includes("invalid utf8")
  ) {
    return "Tệp CSV phải được mã hóa UTF-8 và không chứa ký tự lạ.";
  }
  if (
    code === "IMPORT_FILE_TOO_LARGE" ||
    code === "PAYLOAD_TOO_LARGE" ||
    status === 413 ||
    lower.includes("import_file_too_large")
  ) {
    return "Dung lượng tệp CSV vượt quá giới hạn tối đa 1 MB.";
  }
  if (code === "IMPORT_TOO_MANY_ROWS" || lower.includes("import_too_many_rows")) {
    return "Tệp CSV vượt quá giới hạn tối đa 1.000 dòng dữ liệu.";
  }
  if (code === "IMPORT_INVALID_HEADER" || lower.includes("import_invalid_header")) {
    return "Cấu trúc cột tiêu đề hoặc định dạng tệp CSV chưa đúng mẫu. Vui lòng tải tệp mẫu CSV mới nhất.";
  }
  if (code === "SHOP_SUSPENDED" || lower.includes("shop_suspended")) {
    return "Gian hàng hiện đang tạm ngưng hoạt động nên không thể nhập sản phẩm mới.";
  }
  if (status === 429 || code === "RATE_LIMITED") {
    return "Bạn thao tác quá nhanh. Vui lòng đợi ít giây rồi thử lại.";
  }
  if (status >= 500 || code === "UNAVAILABLE") {
    return "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút.";
  }
  return fallback;
}

/**
 * Vietnamese error descriptions for per-row CSV product import error codes (W7).
 */
export const PRODUCT_IMPORT_ROW_ERROR_LABELS: Record<string, string> = {
  INVALID_PRODUCT_NAME: "Tên sản phẩm không hợp lệ hoặc bị bỏ trống.",
  INVALID_PRODUCT_TEXT: "Mô tả hoặc thông tin văn bản của sản phẩm không hợp lệ.",
  INVALID_SKU_CODE: "Mã SKU không hợp lệ hoặc bị bỏ trống.",
  INVALID_SKU_TEXT: "Tên phân loại SKU không hợp lệ.",
  DUPLICATE_SKU_IN_FILE: "Mã SKU bị trùng lặp trong cùng tệp CSV.",
  DUPLICATE_SKU_IN_SHOP: "Mã SKU đã tồn tại trong gian hàng.",
  INVALID_CATEGORY: "Mã danh mục không hợp lệ hoặc bị bỏ trống.",
  CATEGORY_NOT_FOUND: "Không tìm thấy danh mục tương ứng trên hệ thống.",
  PROHIBITED_CATEGORY: "Danh mục này thuộc nhóm hạn chế hoặc cấm kinh doanh.",
  INVALID_PRICE: "Giá bán không hợp lệ (phải là số nguyên VND lớn hơn 0).",
  INVALID_STOCK: "Số lượng tồn kho không hợp lệ (phải là số nguyên không âm).",
  INVALID_WEIGHT: "Khối lượng sản phẩm (gram) không hợp lệ.",
  INVALID_SLUG: "Đường dẫn (slug) sản phẩm không hợp lệ.",
  DUPLICATE_SLUG: "Đường dẫn (slug) sản phẩm đã tồn tại hoặc bị trùng lặp.",
  INVALID_ATTRIBUTES: "Thuộc tính phân loại sản phẩm không đúng định dạng.",
  BANNED_CONTENT: "Nội dung sản phẩm chứa từ khóa vi phạm quy định kiểm duyệt.",
  SHOP_SUSPENDED: "Gian hàng đang tạm ngưng hoạt động nên không thể tạo sản phẩm mới.",
  INTERNAL_ERROR: "Lỗi hệ thống tạm thời khi xử lý dòng này.",
};

function matchKnownImportErrorCode(rawText: string): string | null {
  const upper = rawText.trim().toUpperCase();
  if (!upper) return null;
  if (PRODUCT_IMPORT_ROW_ERROR_LABELS[upper]) {
    return upper;
  }
  const prefixMatch = /^([A-Z][A-Z0-9_]{2,})(?:\s*[:(]|$)/.exec(upper);
  if (prefixMatch && PRODUCT_IMPORT_ROW_ERROR_LABELS[prefixMatch[1]]) {
    return prefixMatch[1];
  }
  for (const key of Object.keys(PRODUCT_IMPORT_ROW_ERROR_LABELS)) {
    if (upper.includes(key)) {
      return key;
    }
  }
  return null;
}

/**
 * Resolves a CSV import row error strictly to Vietnamese by `error_code` first;
 * raw `error_message` is used only as a last resort when no code matches (W7).
 */
export function translateProductImportRowError(
  errorCode?: string | null,
  fallbackMessage?: string | null
): string {
  const cleanCode = (errorCode ?? "").trim().toUpperCase();
  if (cleanCode && PRODUCT_IMPORT_ROW_ERROR_LABELS[cleanCode]) {
    return PRODUCT_IMPORT_ROW_ERROR_LABELS[cleanCode];
  }
  const rawMsg = (fallbackMessage ?? "").trim();
  if (rawMsg) {
    const matchedCode = matchKnownImportErrorCode(rawMsg);
    if (matchedCode) {
      return PRODUCT_IMPORT_ROW_ERROR_LABELS[matchedCode];
    }
    return rawMsg;
  }
  return "Dữ liệu dòng không hợp lệ.";
}

/**
 * Translates `importJob.error_message` to Vietnamese when it carries a known backend error code (W7).
 */
export function translateProductImportJobError(errorMessage?: string | null): string {
  const raw = (errorMessage ?? "").trim();
  if (!raw) return "";
  const sentinel = "__UNMATCHED_IMPORT_JOB_ERR__";
  const mappedJobErr = translateProductImportError({ message: raw }, sentinel);
  if (mappedJobErr !== sentinel) {
    return mappedJobErr;
  }
  const matchedRowCode = matchKnownImportErrorCode(raw);
  if (matchedRowCode) {
    return PRODUCT_IMPORT_ROW_ERROR_LABELS[matchedRowCode];
  }
  return raw;
}

export function buildSellerProductsQuery(params: {
  page: number;
  pageSize?: number;
  filter: SellerProductFilterKey;
  categoryId?: string;
  keyword?: string;
}): string {
  const qs = new URLSearchParams();
  qs.set("page", String(Math.max(1, Math.trunc(params.page || 1))));
  qs.set(
    "page_size",
    String(Math.max(1, Math.min(50, Math.trunc(params.pageSize ?? SELLER_PRODUCTS_PAGE_SIZE))))
  );
  if (params.filter === "LOW_OOS") {
    qs.set("low_stock", "true");
  } else if (params.filter !== "all") {
    qs.set("status", params.filter);
  }
  const cat = (params.categoryId ?? "").trim();
  if (cat && cat !== "all") {
    qs.set("category_id", cat);
  }
  const kw = (params.keyword ?? "").trim();
  if (kw) {
    qs.set("keyword", kw);
  }
  return qs.toString();
}

export function buildStockHistoryQuery(params: {
  page: number;
  pageSize?: number;
  changeType?: string;
  fromDate?: string;
  toDate?: string;
}): string {
  const qs = new URLSearchParams();
  qs.set("page", String(Math.max(1, Math.trunc(params.page || 1))));
  qs.set(
    "page_size",
    String(Math.max(1, Math.min(50, Math.trunc(params.pageSize ?? STOCK_HISTORY_PAGE_SIZE))))
  );
  const ct = (params.changeType ?? "").trim().toUpperCase();
  if (ct && ct !== "ALL") {
    qs.set("change_type", ct);
  }
  const fromDate = (params.fromDate ?? "").trim();
  if (/^\d{4}-\d{2}-\d{2}$/.test(fromDate)) {
    qs.set("from_time", `${fromDate}T00:00:00+07:00`);
  }
  const toDate = (params.toDate ?? "").trim();
  if (/^\d{4}-\d{2}-\d{2}$/.test(toDate)) {
    qs.set("to_time", `${toDate}T23:59:59+07:00`);
  }
  return qs.toString();
}

// ============================================================================
// 6. §10 Display Pricing Helpers (SizeFinder)
// ============================================================================

export function resolveSizeFinderProductPricing(product: {
  price: number;
  originalPrice?: number;
}): { displayPriceVnd: number; originalPriceVnd?: number } {
  const displayPriceVnd = Math.max(0, Math.trunc(product.price ?? 0));
  const orig = product.originalPrice != null ? Math.trunc(product.originalPrice) : 0;
  return {
    displayPriceVnd,
    originalPriceVnd: orig > displayPriceVnd && displayPriceVnd > 0 ? orig : undefined,
  };
}

export const RETIRED_VOUCHER_CODES = ["EDU15", "SINHNHAT50", "NEXUSVIP12"] as const;
