// Pure types, normalizers, and validators for Admin Complaints (/admin/khieu-nai)
// and Admin Platform Configuration (/admin/cau-hinh).
// Zero I/O, zero DOM, safe for server components, route handlers, client components, and Node tests.
import type { PillTone } from "./adminMeta.ts";
import { sanitizeSafeUrl } from "../../lib/site/theme.ts";

export type ComplaintStatus = "OPEN" | "IN_REVIEW" | "RESOLVED" | "DISMISSED";

export type ComplaintDecision =
  "REFUND_BUYER" | "APPROVE_RETURN" | "REJECT_COMPLAINT" | "PARTIAL_REFUND";

export const COMPLAINT_DECISIONS: readonly ComplaintDecision[] = [
  "REFUND_BUYER",
  "APPROVE_RETURN",
  "PARTIAL_REFUND",
  "REJECT_COMPLAINT",
] as const;

export interface ComplaintTimelineEntry {
  id: string;
  actor_role: string;
  actor_name: string;
  action: string;
  note: string;
  created_at: string;
}

export interface AdminComplaint {
  id: string;
  complaint_code: string;
  return_request_id: string;
  order_id: string;
  order_code: string;
  buyer_id: string;
  buyer_name: string;
  shop_id: string;
  shop_name: string;
  status: ComplaintStatus;
  dispute_amount_vnd: number;
  buyer_reason: string;
  seller_rejection_reason: string;
  evidence_urls: string[];
  decision: string;
  refund_amount_vnd: number | null;
  resolution_note: string;
  resolved_by: string;
  created_at: string;
  updated_at: string;
  resolved_at: string;
  timeline: ComplaintTimelineEntry[];
}

export interface ValidatedComplaintResolution {
  decision: ComplaintDecision;
  refund_amount_vnd?: number;
  note: string;
}

export interface AdminCategory {
  id: string;
  name: string;
  slug: string;
  parent_id: string;
  commission_rate_bps: number;
  sort_order: number;
  created_at?: string;
  updated_at?: string;
}

export interface ValidatedCategoryInput {
  name: string;
  slug: string;
  parent_id: string;
  commission_rate_bps: number;
  sort_order: number;
}

export type AdminVoucherScope = "PLATFORM" | "FREESHIP";
export type AdminVoucherDiscountType = "FIXED" | "PERCENT";

export interface AdminVoucher {
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

export interface ValidatedAdminVoucherInput {
  code: string;
  name: string;
  scope: AdminVoucherScope;
  discount_type: AdminVoucherDiscountType;
  value_vnd: number;
  discount_percent: number;
  max_discount_vnd: number;
  min_order_vnd: number;
  quota: number;
  per_user_limit: number;
  budget_vnd: number;
  starts_at: string;
  ends_at: string;
}

export type AdminUserStatus = "ACTIVE" | "LOCKED" | "SUSPENDED" | "PENDING_VERIFICATION";
export type AdminUserRole = "buyer" | "seller" | "admin" | "super_admin";

export const ADMIN_USER_ROLES: readonly AdminUserRole[] = [
  "buyer",
  "seller",
  "admin",
  "super_admin",
] as const;

export const ADMIN_USER_MUTABLE_STATUSES: readonly ("ACTIVE" | "LOCKED" | "SUSPENDED")[] = [
  "ACTIVE",
  "LOCKED",
  "SUSPENDED",
] as const;

export interface AdminUser {
  id: string;
  full_name: string;
  email: string;
  phone: string;
  role: string;
  status: string;
  shop_id: string;
  phone_verified: boolean;
  cod_rejected_count: number;
  avatar_url: string;
  created_at?: string;
  updated_at?: string;
}

export type ValidationResult<T> =
  { ok: true; value: T } | { ok: false; code: string; message: string };

const SLUG_RE = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const VOUCHER_CODE_RE = /^[A-Z0-9_-]{3,32}$/;

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function asString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function asNonNegativeInt(value: unknown, fallback = 0): number {
  if (typeof value === "number" && Number.isFinite(value)) {
    return Math.max(0, Math.trunc(value));
  }
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value.trim());
    if (Number.isFinite(parsed)) return Math.max(0, Math.trunc(parsed));
  }
  return fallback;
}

function asOptionalNonNegativeInt(value: unknown): number | null {
  if (value === null || value === undefined || value === "") return null;
  if (typeof value === "number" && Number.isFinite(value)) {
    return Math.max(0, Math.trunc(value));
  }
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value.trim());
    if (Number.isFinite(parsed)) return Math.max(0, Math.trunc(parsed));
  }
  return null;
}

function normalizeComplaintStatus(raw: string): ComplaintStatus {
  const upper = raw
    .trim()
    .toUpperCase()
    .replace(/^COMPLAINT_STATUS_/, "");
  if (upper === "IN_REVIEW" || upper === "REVIEWING" || upper === "UNDER_REVIEW") {
    return "IN_REVIEW";
  }
  if (upper === "RESOLVED" || upper === "CLOSED") {
    return "RESOLVED";
  }
  if (upper === "DISMISSED" || upper === "REJECTED") {
    return "DISMISSED";
  }
  return "OPEN";
}

function normalizeTimeline(raw: unknown): ComplaintTimelineEntry[] {
  if (!Array.isArray(raw)) return [];
  return raw
    .map((entry, idx) => {
      const rec = asRecord(entry);
      const note = asString(rec.note || rec.message || rec.content);
      const action = asString(rec.action || rec.title || rec.status);
      if (!note && !action) return null;
      return {
        id: asString(rec.id) || `ev-${idx + 1}`,
        actor_role: asString(rec.actor_role || rec.role) || "system",
        actor_name: asString(rec.actor_name || rec.actor_id),
        action: action || "Cập nhật khiếu nại",
        note,
        created_at: asString(rec.created_at || rec.timestamp),
      };
    })
    .filter((item): item is ComplaintTimelineEntry => item !== null);
}

/**
 * Normalizes a complaint payload from either Swarm D proto/gateway DTOs or extended complaint shapes.
 * All evidence photo URLs are sanitized via `sanitizeSafeUrl`.
 */
export function normalizeComplaint(rawInput: unknown): AdminComplaint {
  const raw = asRecord(rawInput);
  const id = asString(raw.id || raw.complaint_id);
  const orderId = asString(raw.order_id);
  const orderCode =
    asString(raw.order_code) || (orderId ? `DH-${orderId.slice(0, 8).toUpperCase()}` : "");
  const complaintCode =
    asString(raw.complaint_code || raw.code) ||
    (id ? (id.toUpperCase().startsWith("KN-") ? id : `KN-${id.slice(0, 8).toUpperCase()}`) : "");

  const rawPhotos = Array.isArray(raw.evidence_urls)
    ? raw.evidence_urls
    : Array.isArray(raw.photos)
      ? raw.photos
      : [];
  const evidenceUrls = rawPhotos
    .map((url) => sanitizeSafeUrl(typeof url === "string" ? url : ""))
    .filter((url) => url.length > 0);

  const buyerId = asString(raw.buyer_id);
  const shopId = asString(raw.shop_id);
  const status = normalizeComplaintStatus(asString(raw.status));
  const decision = asString(raw.decision || raw.resolution)
    .toUpperCase()
    .replace(/^COMPLAINT_RESOLUTION_/, "");

  const resolutionNote = asString(raw.resolution_note || raw.admin_note || raw.note);
  const refundAmount = asOptionalNonNegativeInt(raw.refund_amount_vnd);
  const disputeAmount = asNonNegativeInt(
    raw.dispute_amount_vnd ?? raw.amount_vnd ?? raw.order_total_vnd ?? raw.refund_amount_vnd,
    0
  );

  return {
    id,
    complaint_code: complaintCode,
    return_request_id: asString(raw.return_request_id),
    order_id: orderId,
    order_code: orderCode,
    buyer_id: buyerId,
    buyer_name: asString(raw.buyer_name) || (buyerId ? `Người mua ${buyerId.slice(0, 8)}` : ""),
    shop_id: shopId,
    shop_name: asString(raw.shop_name) || (shopId ? `Gian hàng ${shopId.slice(0, 8)}` : ""),
    status,
    dispute_amount_vnd: disputeAmount,
    buyer_reason: asString(raw.buyer_reason || raw.reason),
    seller_rejection_reason: asString(
      raw.seller_rejection_reason || raw.seller_note || raw.rejection_reason
    ),
    evidence_urls: evidenceUrls,
    decision,
    refund_amount_vnd: refundAmount,
    resolution_note: resolutionNote,
    resolved_by: asString(raw.resolved_by),
    created_at: asString(raw.created_at),
    updated_at: asString(raw.updated_at || raw.resolved_at || raw.created_at),
    resolved_at: asString(raw.resolved_at),
    timeline: normalizeTimeline(raw.timeline || raw.notes),
  };
}

export function normalizeComplaintsListResponse(raw: unknown): {
  items: AdminComplaint[];
  total: number;
} {
  if (Array.isArray(raw)) {
    const items = raw.map(normalizeComplaint);
    return { items, total: items.length };
  }
  const rec = asRecord(raw);
  const list = Array.isArray(rec.items)
    ? rec.items
    : Array.isArray(rec.complaints)
      ? rec.complaints
      : [];
  const items = list.map(normalizeComplaint);
  const total =
    typeof rec.total === "number" && Number.isFinite(rec.total)
      ? Math.max(items.length, Math.trunc(rec.total))
      : items.length;
  return { items, total };
}

export function isComplaintResolved(status: string): boolean {
  const norm = normalizeComplaintStatus(status);
  return norm === "RESOLVED" || norm === "DISMISSED";
}

export function getComplaintStatusMeta(status: string): { label: string; tone: PillTone } {
  switch (normalizeComplaintStatus(status)) {
    case "OPEN":
      return { label: "Chờ xử lý", tone: "warning" };
    case "IN_REVIEW":
      return { label: "Đang xem xét", tone: "neutral" };
    case "RESOLVED":
      return { label: "Đã giải quyết", tone: "positive" };
    case "DISMISSED":
      return { label: "Đã bác khiếu nại", tone: "negative" };
    default:
      return { label: "Chờ xử lý", tone: "warning" };
  }
}

export function getComplaintDecisionLabelVi(decision: string): string {
  switch (
    decision
      .trim()
      .toUpperCase()
      .replace(/^COMPLAINT_RESOLUTION_/, "")
  ) {
    case "REFUND_BUYER":
    case "REFUND":
      return "Hoàn toàn bộ tiền cho người mua";
    case "APPROVE_RETURN":
    case "REPLACE":
      return "Chấp thuận trả hàng";
    case "PARTIAL_REFUND":
      return "Hoàn một phần tiền";
    case "REJECT_COMPLAINT":
    case "DISMISS":
      return "Bác khiếu nại";
    default:
      return decision || "Chưa có quyết định";
  }
}

export function validateComplaintResolution(
  input: unknown
): ValidationResult<ValidatedComplaintResolution> {
  const rec = asRecord(input);
  const rawDecision = asString(rec.decision).toUpperCase();
  if (!(COMPLAINT_DECISIONS as readonly string[]).includes(rawDecision)) {
    return {
      ok: false,
      code: "INVALID_DECISION",
      message: "Quyết định phân xử không hợp lệ.",
    };
  }
  const decision = rawDecision as ComplaintDecision;
  const note = asString(rec.note);
  if (note.length < 5) {
    return {
      ok: false,
      code: "NOTE_TOO_SHORT",
      message: "Ghi chú phân xử cần tối thiểu 5 ký tự để lưu vết kiểm toán.",
    };
  }
  if (note.length > 2000) {
    return {
      ok: false,
      code: "NOTE_TOO_LONG",
      message: "Ghi chú phân xử không được vượt quá 2.000 ký tự.",
    };
  }

  if (decision === "PARTIAL_REFUND") {
    const rawAmount = rec.refund_amount_vnd;
    if (typeof rawAmount !== "number" || !Number.isInteger(rawAmount) || rawAmount < 0) {
      return {
        ok: false,
        code: "INVALID_REFUND_AMOUNT",
        message: "Số tiền hoàn một phần phải là số nguyên VND không âm.",
      };
    }
    return {
      ok: true,
      value: {
        decision,
        refund_amount_vnd: rawAmount,
        note,
      },
    };
  }

  return {
    ok: true,
    value: {
      decision,
      note,
    },
  };
}

/**
 * Formats basis points into Vietnamese percentage notation: 500 -> "5,00%".
 */
export function formatCommissionBps(bps: number): string {
  if (!Number.isFinite(bps)) return "0,00%";
  const clamped = Math.max(0, Math.trunc(bps));
  return `${(clamped / 100).toFixed(2).replace(".", ",")}%`;
}

export function slugifyCategoryName(name: string): string {
  return name
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[đĐ]/g, "d")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 80);
}

export function validateCategoryInput(input: unknown): ValidationResult<ValidatedCategoryInput> {
  const rec = asRecord(input);
  const name = asString(rec.name);
  if (name.length < 2 || name.length > 120) {
    return {
      ok: false,
      code: "INVALID_CATEGORY_NAME",
      message: "Tên danh mục cần từ 2 đến 120 ký tự.",
    };
  }

  const slug = asString(rec.slug).toLowerCase();
  if (!slug || slug.length > 120 || !SLUG_RE.test(slug)) {
    return {
      ok: false,
      code: "INVALID_CATEGORY_SLUG",
      message: "Slug danh mục chỉ gồm chữ thường không dấu, số và dấu gạch ngang.",
    };
  }

  const parentId = asString(rec.parent_id);
  const rawBps = rec.commission_rate_bps;
  if (typeof rawBps !== "number" || !Number.isInteger(rawBps) || rawBps < 0 || rawBps > 5000) {
    return {
      ok: false,
      code: "INVALID_COMMISSION_BPS",
      message: "Phí hoa hồng phải là số nguyên từ 0 đến 5.000 bps (tương ứng 0% đến 50%).",
    };
  }

  let sortOrder = 0;
  if (rec.sort_order !== undefined && rec.sort_order !== null && rec.sort_order !== "") {
    if (
      typeof rec.sort_order !== "number" ||
      !Number.isInteger(rec.sort_order) ||
      rec.sort_order < 0
    ) {
      return {
        ok: false,
        code: "INVALID_SORT_ORDER",
        message: "Thứ tự hiển thị phải là số nguyên không âm.",
      };
    }
    sortOrder = rec.sort_order;
  }

  return {
    ok: true,
    value: {
      name,
      slug,
      parent_id: parentId,
      commission_rate_bps: rawBps,
      sort_order: sortOrder,
    },
  };
}

export function validateAdminVoucherInput(
  input: unknown
): ValidationResult<ValidatedAdminVoucherInput> {
  const rec = asRecord(input);
  const code = asString(rec.code).toUpperCase();
  if (!VOUCHER_CODE_RE.test(code)) {
    return {
      ok: false,
      code: "INVALID_VOUCHER_CODE",
      message: "Mã voucher cần từ 3 đến 32 ký tự gồm chữ in hoa, số, gạch ngang hoặc gạch dưới.",
    };
  }

  const name = asString(rec.name);
  if (name.length < 2 || name.length > 160) {
    return {
      ok: false,
      code: "INVALID_VOUCHER_NAME",
      message: "Tên chương trình voucher cần từ 2 đến 160 ký tự.",
    };
  }

  const rawScope = asString(rec.scope).toUpperCase();
  if (rawScope !== "PLATFORM" && rawScope !== "FREESHIP") {
    return {
      ok: false,
      code: "INVALID_VOUCHER_SCOPE",
      message: "Phạm vi voucher quản trị chỉ hỗ trợ PLATFORM hoặc FREESHIP.",
    };
  }
  const scope: AdminVoucherScope = rawScope;

  const rawDiscountType = asString(rec.discount_type || "FIXED").toUpperCase();
  if (rawDiscountType !== "FIXED" && rawDiscountType !== "PERCENT") {
    return {
      ok: false,
      code: "INVALID_DISCOUNT_TYPE",
      message: "Loại giảm giá phải là FIXED hoặc PERCENT.",
    };
  }
  const discountType: AdminVoucherDiscountType = rawDiscountType;

  let valueVnd = 0;
  let discountPercent = 0;
  let maxDiscountVnd = 0;

  if (discountType === "FIXED") {
    if (
      typeof rec.value_vnd !== "number" ||
      !Number.isInteger(rec.value_vnd) ||
      rec.value_vnd <= 0
    ) {
      return {
        ok: false,
        code: "INVALID_VALUE_VND",
        message: "Mức giảm cố định phải là số nguyên VND lớn hơn 0.",
      };
    }
    valueVnd = rec.value_vnd;
    if (rec.max_discount_vnd !== undefined && rec.max_discount_vnd !== null) {
      if (
        typeof rec.max_discount_vnd !== "number" ||
        !Number.isInteger(rec.max_discount_vnd) ||
        rec.max_discount_vnd < 0
      ) {
        return {
          ok: false,
          code: "INVALID_MAX_DISCOUNT_VND",
          message: "Giảm tối đa phải là số nguyên VND không âm.",
        };
      }
      maxDiscountVnd = rec.max_discount_vnd;
    }
  } else {
    if (
      typeof rec.discount_percent !== "number" ||
      !Number.isInteger(rec.discount_percent) ||
      rec.discount_percent < 1 ||
      rec.discount_percent > 100
    ) {
      return {
        ok: false,
        code: "INVALID_DISCOUNT_PERCENT",
        message: "Phần trăm giảm giá phải là số nguyên từ 1 đến 100.",
      };
    }
    discountPercent = rec.discount_percent;
    if (
      typeof rec.max_discount_vnd !== "number" ||
      !Number.isInteger(rec.max_discount_vnd) ||
      rec.max_discount_vnd <= 0
    ) {
      return {
        ok: false,
        code: "INVALID_MAX_DISCOUNT_VND",
        message: "Voucher giảm theo phần trăm cần mức giảm tối đa là số nguyên VND lớn hơn 0.",
      };
    }
    maxDiscountVnd = rec.max_discount_vnd;
  }

  const rawMinOrder = rec.min_order_vnd ?? 0;
  if (typeof rawMinOrder !== "number" || !Number.isInteger(rawMinOrder) || rawMinOrder < 0) {
    return {
      ok: false,
      code: "INVALID_MIN_ORDER_VND",
      message: "Giá trị đơn tối thiểu phải là số nguyên VND không âm.",
    };
  }

  if (typeof rec.quota !== "number" || !Number.isInteger(rec.quota) || rec.quota < 1) {
    return {
      ok: false,
      code: "INVALID_QUOTA",
      message: "Tổng số lượt sử dụng phải là số nguyên lớn hơn hoặc bằng 1.",
    };
  }

  const rawPerUser = rec.per_user_limit ?? 1;
  if (
    typeof rawPerUser !== "number" ||
    !Number.isInteger(rawPerUser) ||
    rawPerUser < 1 ||
    rawPerUser > rec.quota
  ) {
    return {
      ok: false,
      code: "INVALID_PER_USER_LIMIT",
      message: "Giới hạn mỗi người phải là số nguyên từ 1 đến tổng số lượt.",
    };
  }

  const rawBudget = rec.budget_vnd ?? 0;
  if (typeof rawBudget !== "number" || !Number.isInteger(rawBudget) || rawBudget < 0) {
    return {
      ok: false,
      code: "INVALID_BUDGET_VND",
      message: "Ngân sách tối đa phải là số nguyên VND không âm.",
    };
  }

  const startsAt = asString(rec.starts_at);
  const endsAt = asString(rec.ends_at);
  const startMs = Date.parse(startsAt);
  const endMs = Date.parse(endsAt);
  if (!startsAt || !endsAt || Number.isNaN(startMs) || Number.isNaN(endMs)) {
    return {
      ok: false,
      code: "INVALID_VOUCHER_WINDOW",
      message: "Thời gian bắt đầu và kết thúc voucher chưa hợp lệ.",
    };
  }
  if (endMs <= startMs) {
    return {
      ok: false,
      code: "INVALID_VOUCHER_WINDOW",
      message: "Thời gian kết thúc phải sau thời gian bắt đầu.",
    };
  }

  return {
    ok: true,
    value: {
      code,
      name,
      scope,
      discount_type: discountType,
      value_vnd: valueVnd,
      discount_percent: discountPercent,
      max_discount_vnd: maxDiscountVnd,
      min_order_vnd: rawMinOrder,
      quota: rec.quota,
      per_user_limit: rawPerUser,
      budget_vnd: rawBudget,
      starts_at: new Date(startMs).toISOString(),
      ends_at: new Date(endMs).toISOString(),
    },
  };
}

/**
 * Prevents an admin from locking or altering their own account privileges (UC-A03).
 */
export function canModifyUserAccount(
  actorUserId: string,
  targetUserId: string,
  action: "status" | "role" = "status"
): { allowed: boolean; reason?: string } {
  const actor = actorUserId.trim();
  const target = targetUserId.trim();
  if (!actor || !target) {
    return {
      allowed: false,
      reason: "Thiếu mã định danh tài khoản để kiểm tra quyền thao tác.",
    };
  }
  if (actor === target) {
    return {
      allowed: false,
      reason:
        action === "role"
          ? "Không thể tự thay đổi vai trò của tài khoản đang đăng nhập."
          : "Không thể tự khóa hoặc thay đổi trạng thái tài khoản của chính bạn.",
    };
  }
  return { allowed: true };
}

export function getUserStatusMeta(status: string): { label: string; tone: PillTone } {
  switch (status.trim().toUpperCase()) {
    case "ACTIVE":
      return { label: "Đang hoạt động", tone: "positive" };
    case "LOCKED":
      return { label: "Đã khóa", tone: "negative" };
    case "SUSPENDED":
      return { label: "Tạm đình chỉ", tone: "negative" };
    case "PENDING_VERIFICATION":
      return { label: "Chờ xác thực", tone: "warning" };
    default:
      return { label: status || "Không xác định", tone: "neutral" };
  }
}

export function getVoucherScopeMeta(scope: string): { label: string; tone: PillTone } {
  switch (scope.trim().toUpperCase()) {
    case "PLATFORM":
      return { label: "Voucher sàn", tone: "positive" };
    case "FREESHIP":
      return { label: "Freeship", tone: "neutral" };
    case "SHOP":
      return { label: "Voucher gian hàng", tone: "warning" };
    default:
      return { label: scope || "Khuyến mãi", tone: "neutral" };
  }
}
