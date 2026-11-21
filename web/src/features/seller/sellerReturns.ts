// Pure DTO normalizers and validation helpers for Seller Returns, Notifications,
// AI Review Summaries, and Auto-Reply settings.
// Zero Next.js/Node runtime imports so it runs in Server Components, Client Components, and node --test.
import { sanitizeSafeUrl } from "../../lib/site/theme.ts";

export type UiStateMode = "loading" | "empty" | "error" | null;

export function parseUiStateParam(value: string | null): UiStateMode {
  if (value === "loading" || value === "empty" || value === "error") {
    return value;
  }
  return null;
}

export interface SellerReturnItem {
  order_item_id: string;
  sku_id: string;
  product_name: string;
  sku_name: string;
  quantity: number;
  refund_amount_vnd: number;
}

export interface SellerReturnRequest {
  id: string;
  order_id: string;
  order_code: string;
  buyer_id: string;
  shop_id: string;
  status: string;
  reason: string;
  seller_note: string;
  reject_reason: string;
  refund_amount_vnd: number;
  items: SellerReturnItem[];
  photos: string[];
  created_at: string;
  updated_at: string;
}

export interface ReturnStatusMeta {
  label: string;
  tone: "neutral" | "warn" | "pos" | "neg";
  canDecide: boolean;
}

export interface SellerNotification {
  id: string;
  user_id: string;
  category: string;
  title: string;
  body: string;
  link: string;
  event_id: string;
  is_read: boolean;
  created_at: string;
}

export interface SellerNotificationListResult {
  items: SellerNotification[];
  unreadCount: number;
  total: number;
}

export interface SellerNotificationSummary {
  total: number;
  unread: number;
  read: number;
  byCategory: {
    order: number;
    promotion: number;
    system: number;
    other: number;
  };
}

export interface SellerAddress {
  id: string;
  user_id: string;
  recipient_name: string;
  recipient_phone: string;
  province_code: string;
  province_name: string;
  ward_code: string;
  ward_name: string;
  street_address: string;
  is_default: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface SellerProductReviewSummary {
  product_id: string;
  review_count: number;
  average_rating: number;
  summary: string;
  pros: string[];
  cons: string[];
  ready: boolean;
  updated_at: string;
}

export interface SellerAutoReplyConfig {
  enabled: boolean;
  message: string;
  updated_at: string;
}

type UnknownRecord = Record<string, unknown>;

function asRecord(value: unknown): UnknownRecord | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  return value as UnknownRecord;
}

function readStr(obj: UnknownRecord, ...keys: string[]): string {
  for (const key of keys) {
    const val = obj[key];
    if (typeof val === "string") {
      const trimmed = val.trim();
      if (trimmed) return trimmed;
    }
  }
  return "";
}

function readNonNegInt(obj: UnknownRecord, ...keys: string[]): number {
  for (const key of keys) {
    const val = obj[key];
    if (typeof val === "number" && Number.isFinite(val)) {
      return Math.max(0, Math.trunc(val));
    }
    if (typeof val === "string" && val.trim() !== "") {
      const parsed = Number(val);
      if (Number.isFinite(parsed)) {
        return Math.max(0, Math.trunc(parsed));
      }
    }
  }
  return 0;
}

function readStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  const out: string[] = [];
  for (const item of value) {
    if (typeof item === "string") {
      const trimmed = item.trim();
      if (trimmed) out.push(trimmed);
    }
  }
  return out;
}

function normalizeReturnItem(raw: unknown): SellerReturnItem | null {
  const obj = asRecord(raw);
  if (!obj) return null;
  const orderItemId = readStr(obj, "order_item_id", "item_id", "id");
  const skuId = readStr(obj, "sku_id");
  const productName = readStr(obj, "product_name", "name");
  const skuName = readStr(obj, "sku_name", "variant_name");
  const quantity = Math.max(1, readNonNegInt(obj, "quantity", "qty") || 1);
  const refundAmountVnd = readNonNegInt(obj, "refund_amount_vnd", "line_refund_vnd", "amount_vnd");

  if (!orderItemId && !skuId && !productName) return null;
  return {
    order_item_id: orderItemId,
    sku_id: skuId,
    product_name: productName,
    sku_name: skuName,
    quantity,
    refund_amount_vnd: refundAmountVnd,
  };
}

/** Normalize a single seller return request record from gateway payloads. */
export function normalizeSellerReturn(raw: unknown): SellerReturnRequest | null {
  const obj = asRecord(raw);
  if (!obj) return null;

  const id = readStr(obj, "id", "return_id");
  if (!id) return null;

  const rawStatus = readStr(obj, "status") || "PENDING";
  const rawItems = Array.isArray(obj.items)
    ? obj.items
    : Array.isArray(obj.return_items)
      ? obj.return_items
      : [];
  const items = rawItems
    .map(normalizeReturnItem)
    .filter((it): it is SellerReturnItem => it !== null);

  const rawPhotos = Array.isArray(obj.photos)
    ? obj.photos
    : Array.isArray(obj.evidence_photos)
      ? obj.evidence_photos
      : Array.isArray(obj.evidence_urls)
        ? obj.evidence_urls
        : [];
  const photos = rawPhotos
    .map((u) => (typeof u === "string" ? sanitizeSafeUrl(u) : ""))
    .filter(Boolean);

  return {
    id,
    order_id: readStr(obj, "order_id"),
    order_code: readStr(obj, "order_code"),
    buyer_id: readStr(obj, "buyer_id", "user_id"),
    shop_id: readStr(obj, "shop_id"),
    status: rawStatus.toUpperCase(),
    reason: readStr(obj, "reason"),
    seller_note: readStr(obj, "seller_note", "note"),
    reject_reason: readStr(obj, "reject_reason", "rejection_reason"),
    refund_amount_vnd: readNonNegInt(obj, "refund_amount_vnd", "refund_vnd", "amount_vnd"),
    items,
    photos,
    created_at: readStr(obj, "created_at"),
    updated_at: readStr(obj, "updated_at"),
  };
}

/** Normalize a list or wrapper payload from GET /api/v1/orders/{id}/returns. */
export function normalizeSellerReturns(payload: unknown): SellerReturnRequest[] {
  if (Array.isArray(payload)) {
    return payload
      .map(normalizeSellerReturn)
      .filter((r): r is SellerReturnRequest => r !== null);
  }
  const obj = asRecord(payload);
  if (!obj) return [];

  const listCandidate =
    obj.returns ?? obj.items ?? obj.return_requests ?? obj.data ?? null;
  if (Array.isArray(listCandidate)) {
    return listCandidate
      .map(normalizeSellerReturn)
      .filter((r): r is SellerReturnRequest => r !== null);
  }

  const single = obj.return_request ?? obj.return ?? obj;
  const normalized = normalizeSellerReturn(single);
  return normalized ? [normalized] : [];
}

/** Map return request status to Vietnamese label, semantic tone, and seller action availability. */
export function getReturnStatusMeta(status: string): ReturnStatusMeta {
  switch ((status || "").trim().toUpperCase()) {
    case "PENDING":
    case "REQUESTED":
    case "RETURN_REQUESTED":
    case "OPEN":
      return { label: "Chờ duyệt", tone: "warn", canDecide: true };
    case "APPROVED":
    case "ACCEPTED":
      return { label: "Đã duyệt", tone: "pos", canDecide: false };
    case "REJECTED":
    case "DECLINED":
      return { label: "Đã từ chối", tone: "neg", canDecide: false };
    case "RETURNING":
    case "IN_TRANSIT":
    case "SHIPPING_BACK":
    case "PICKUP_SCHEDULED":
      return { label: "Đang hoàn hàng", tone: "neutral", canDecide: false };
    case "REFUNDED":
    case "COMPLETED":
    case "RETURNED":
      return { label: "Đã hoàn tiền", tone: "pos", canDecide: false };
    case "DISPUTED":
    case "ESCALATED":
      return { label: "Đang khiếu nại", tone: "warn", canDecide: false };
    case "CANCELLED":
    case "CANCELED":
      return { label: "Đã hủy", tone: "neutral", canDecide: false };
    default:
      return {
        label: status ? status.trim() : "Đang xử lý",
        tone: "neutral",
        canDecide: false,
      };
  }
}

export type RejectReasonValidation =
  | { ok: true; value: string }
  | { ok: false; code: "INVALID_REASON"; error: string };

/** Validate mandatory rejection reason (trimmed length 5..500). */
export function validateRejectReason(reason: unknown): RejectReasonValidation {
  if (typeof reason !== "string") {
    return {
      ok: false,
      code: "INVALID_REASON",
      error: "Lý do từ chối cần tối thiểu 5 ký tự để thông báo rõ cho người mua.",
    };
  }
  const trimmed = reason.trim();
  if (trimmed.length < 5) {
    return {
      ok: false,
      code: "INVALID_REASON",
      error: "Lý do từ chối cần tối thiểu 5 ký tự để thông báo rõ cho người mua.",
    };
  }
  if (trimmed.length > 500) {
    return {
      ok: false,
      code: "INVALID_REASON",
      error: "Lý do từ chối không vượt quá 500 ký tự.",
    };
  }
  return { ok: true, value: trimmed };
}

const PENDING_RETURN_STATUSES = new Set(["RETURN_REQUESTED", "PENDING", "REQUESTED"]);

/** Count orders or return requests awaiting seller review. */
export function countPendingReturns(
  entries: ReadonlyArray<{ status?: string | null }> | null | undefined
): number {
  if (!Array.isArray(entries)) return 0;
  let count = 0;
  for (const entry of entries) {
    const status = (entry?.status ?? "").trim().toUpperCase();
    if (PENDING_RETURN_STATUSES.has(status)) {
      count += 1;
    }
  }
  return count;
}

/** Normalize a single notification entry and sanitize any deep link. */
export function normalizeSellerNotification(raw: unknown): SellerNotification | null {
  const obj = asRecord(raw);
  if (!obj) return null;
  const id = readStr(obj, "id");
  if (!id) return null;

  return {
    id,
    user_id: readStr(obj, "user_id"),
    category: (readStr(obj, "category") || "SYSTEM").toUpperCase(),
    title: readStr(obj, "title") || "Thông báo hệ thống",
    body: readStr(obj, "body", "message"),
    link: sanitizeSafeUrl(readStr(obj, "link", "url")),
    event_id: readStr(obj, "event_id"),
    is_read: Boolean(obj.is_read),
    created_at: readStr(obj, "created_at"),
  };
}

/** Normalize GET /api/v1/notifications response. */
export function normalizeSellerNotifications(payload: unknown): SellerNotificationListResult {
  if (Array.isArray(payload)) {
    const items = payload
      .map(normalizeSellerNotification)
      .filter((n): n is SellerNotification => n !== null);
    const unreadCount = items.filter((n) => !n.is_read).length;
    return { items, unreadCount, total: items.length };
  }
  const obj = asRecord(payload);
  if (!obj) return { items: [], unreadCount: 0, total: 0 };

  const rawItems = Array.isArray(obj.items)
    ? obj.items
    : Array.isArray(obj.notifications)
      ? obj.notifications
      : [];
  const items = rawItems
    .map(normalizeSellerNotification)
    .filter((n): n is SellerNotification => n !== null);

  const computedUnread = items.filter((n) => !n.is_read).length;
  const unreadCount =
    typeof obj.unread_count === "number" && Number.isFinite(obj.unread_count)
      ? Math.max(0, Math.trunc(obj.unread_count))
      : computedUnread;
  const total =
    typeof obj.total === "number" && Number.isFinite(obj.total)
      ? Math.max(items.length, Math.trunc(obj.total))
      : items.length;

  return { items, unreadCount, total };
}

/** Summarize notification read state and category breakdown. */
export function summarizeNotifications(
  items: ReadonlyArray<Pick<SellerNotification, "is_read" | "category">> | null | undefined
): SellerNotificationSummary {
  const byCategory = { order: 0, promotion: 0, system: 0, other: 0 };
  if (!Array.isArray(items)) {
    return { total: 0, unread: 0, read: 0, byCategory };
  }

  let unread = 0;
  let read = 0;
  for (const item of items) {
    if (item.is_read) {
      read += 1;
    } else {
      unread += 1;
    }
    switch ((item.category || "").trim().toUpperCase()) {
      case "ORDER":
        byCategory.order += 1;
        break;
      case "PROMOTION":
        byCategory.promotion += 1;
        break;
      case "SYSTEM":
        byCategory.system += 1;
        break;
      default:
        byCategory.other += 1;
        break;
    }
  }
  return { total: items.length, unread, read, byCategory };
}

export function getNotificationCategoryLabel(category: string): string {
  switch ((category || "").trim().toUpperCase()) {
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

/** Normalize GET /api/seller/products/[id]/review-summary payload. */
export function normalizeProductReviewSummary(
  payload: unknown,
  fallbackProductId = ""
): SellerProductReviewSummary {
  const root = asRecord(payload);
  const obj = asRecord(root?.review_summary ?? root?.data ?? root) ?? {};

  const productId = readStr(obj, "product_id") || fallbackProductId;
  const reviewCount = readNonNegInt(obj, "review_count", "total_reviews", "count");
  const rawAvg = Number(obj.average_rating ?? obj.avg_rating ?? 0);
  const averageRating = Number.isFinite(rawAvg) && rawAvg > 0 ? Math.min(5, rawAvg) : 0;
  const summary = readStr(obj, "summary", "overview", "text");
  const pros = readStringArray(obj.pros ?? obj.highlights ?? obj.strengths);
  const cons = readStringArray(obj.cons ?? obj.drawbacks ?? obj.weaknesses);
  const updatedAt = readStr(obj, "updated_at", "generated_at");

  const hasContent = Boolean(summary) || pros.length > 0 || cons.length > 0;
  const explicitlyUnavailable = obj.ready === false || obj.insufficient_reviews === true;
  const ready = !explicitlyUnavailable && hasContent && (reviewCount === 0 || reviewCount >= 10);

  return {
    product_id: productId,
    review_count: reviewCount,
    average_rating: averageRating,
    summary,
    pros,
    cons,
    ready,
    updated_at: updatedAt,
  };
}

/** Normalize GET/PUT /api/seller/chat/auto-reply payload. */
export function normalizeAutoReplyConfig(payload: unknown): SellerAutoReplyConfig {
  const root = asRecord(payload);
  const obj = asRecord(root?.auto_reply ?? root?.config ?? root) ?? {};
  return {
    enabled: Boolean(obj.enabled ?? obj.is_enabled),
    message: readStr(obj, "message", "template", "greeting"),
    updated_at: readStr(obj, "updated_at"),
  };
}
