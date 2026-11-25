// Pure validators and normalizers shared by Admin BFF route handlers and Admin UI panels.
// Zero I/O, zero DOM, safe for Next.js Route Handlers, Client Components, and Node unit tests.
import {
  ASSIGNABLE_USER_ROLES,
  CREATABLE_STAFF_ROLES,
  type CreatableStaffRole,
} from "../../lib/admin-rbac.ts";

export type ValidationResult<T> =
  { ok: true; value: T } | { ok: false; code: string; message: string };

const SAFE_ENTITY_ID_RE = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/;
const SAFE_CARRIER_CODE_RE = /^[a-z0-9][a-z0-9_-]{1,31}$/;
const CATEGORY_SLUG_RE = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/;

export const ALLOWED_PAYMENT_METHODS = ["COD", "VIETQR", "ZALOPAY"] as const;
export type AdminPaymentMethodCode = (typeof ALLOWED_PAYMENT_METHODS)[number];

export const ALLOWED_USER_MUTABLE_STATUSES = ["ACTIVE", "LOCKED"] as const;
export type AdminMutableUserStatus = (typeof ALLOWED_USER_MUTABLE_STATUSES)[number];

export const ALLOWED_SHIPPING_REGIONS = ["SAME_PROVINCE", "SAME_REGION", "CROSS_REGION"] as const;
export type AdminShippingRegion = (typeof ALLOWED_SHIPPING_REGIONS)[number];

export const ALLOWED_APPROVAL_STATUSES = [
  "PENDING",
  "PENDING_APPROVAL",
  "APPROVED",
  "REJECTED",
  "ALL",
] as const;

export const ALLOWED_APPROVAL_TYPES = ["shop", "refund"] as const;
export const ALLOWED_ACTIVITY_CATEGORIES = ["auth", "profile", "phone", "status", "role"] as const;
export const ALLOWED_SHOP_STATUSES = ["PENDING", "ACTIVE", "REJECTED", "SUSPENDED"] as const;
export const ALLOWED_SITE_STATUSES = ["DRAFT", "PUBLISHED", "SUSPENDED"] as const;
export const ALLOWED_USER_QUERY_STATUSES = ["ACTIVE", "LOCKED", "PENDING_VERIFICATION"] as const;

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function asTrimmedString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

/** Validates an opaque entity ID path parameter (UUID or safe alphanumeric/hyphen/underscore). */
export function parseEntityId(raw: unknown): string | null {
  const s = asTrimmedString(raw);
  if (!s || !SAFE_ENTITY_ID_RE.test(s)) return null;
  return s;
}

/** Validates a shipping carrier code path parameter (lowercase alphanumeric/hyphen/underscore). */
export function parseCarrierCode(raw: unknown): string | null {
  const s = asTrimmedString(raw).toLowerCase();
  if (!s || !SAFE_CARRIER_CODE_RE.test(s)) return null;
  return s;
}

/** Validates a payment method code (COD | VIETQR | ZALOPAY). */
export function parsePaymentMethodCode(raw: unknown): AdminPaymentMethodCode | null {
  const s = asTrimmedString(raw).toUpperCase();
  if ((ALLOWED_PAYMENT_METHODS as readonly string[]).includes(s)) {
    return s as AdminPaymentMethodCode;
  }
  return null;
}

export function parseOptionalPositiveInt(raw: string | null, max = 500): string | undefined {
  if (raw === null) return undefined;
  const s = raw.trim();
  if (!s) return undefined;
  if (!/^\d+$/.test(s)) return undefined;
  const n = Number(s);
  if (!Number.isSafeInteger(n) || n < 1 || n > max) return undefined;
  return String(n);
}

export interface ValidatedCreateStaffInput {
  full_name: string;
  email: string;
  phone: string;
  role: CreatableStaffRole;
}

/**
 * Validates POST /api/admin/staff body (F14: strictly rejects password/temporary_password and
 * disallows super_admin/buyer/seller roles).
 */
export function validateCreateStaffInput(
  input: unknown
): ValidationResult<ValidatedCreateStaffInput> {
  const rec = asRecord(input);
  if (rec.password !== undefined || rec.temporary_password !== undefined) {
    return {
      ok: false,
      code: "PASSWORD_NOT_ACCEPTED",
      message: "Không được nhập mật khẩu khi tạo nhân viên; hệ thống tự gửi email đặt mật khẩu.",
    };
  }
  const fullName = asTrimmedString(rec.full_name);
  if (fullName.length < 2 || fullName.length > 120) {
    return {
      ok: false,
      code: "INVALID_FULL_NAME",
      message: "Họ tên nhân viên cần từ 2 đến 120 ký tự.",
    };
  }
  const email = asTrimmedString(rec.email).toLowerCase();
  if (!email || email.length > 254 || !EMAIL_RE.test(email)) {
    return {
      ok: false,
      code: "INVALID_EMAIL",
      message: "Vui lòng nhập địa chỉ email công việc hợp lệ.",
    };
  }
  const phone = asTrimmedString(rec.phone);
  if (phone.length > 30) {
    return {
      ok: false,
      code: "INVALID_PHONE",
      message: "Số điện thoại không được vượt quá 30 ký tự.",
    };
  }
  const role = asTrimmedString(rec.role).toLowerCase();
  if (!(CREATABLE_STAFF_ROLES as readonly string[]).includes(role)) {
    return {
      ok: false,
      code: "INVALID_STAFF_ROLE",
      message: "Vai trò nhân viên nội bộ không hợp lệ.",
    };
  }
  return {
    ok: true,
    value: {
      full_name: fullName,
      email,
      phone,
      role: role as CreatableStaffRole,
    },
  };
}

export interface ValidatedUpdateUserRoleInput {
  role: string;
  reason: string;
}

/** Validates PATCH /api/admin/users/[id]/role (forbids assigning super_admin). */
export function validateUpdateUserRoleInput(
  input: unknown
): ValidationResult<ValidatedUpdateUserRoleInput> {
  const rec = asRecord(input);
  const role = asTrimmedString(rec.role).toLowerCase();
  if (!(ASSIGNABLE_USER_ROLES as readonly string[]).includes(role)) {
    return {
      ok: false,
      code: "INVALID_USER_ROLE",
      message: "Vai trò cần chuyển đổi không hợp lệ.",
    };
  }
  const reason = asTrimmedString(rec.reason).slice(0, 500);
  return { ok: true, value: { role, reason } };
}

export interface ValidatedUpdateUserStatusInput {
  status: AdminMutableUserStatus;
  reason: string;
}

/** Validates PATCH /api/admin/users/[id]/status (only ACTIVE or LOCKED; SUSPENDED is rejected). */
export function validateUpdateUserStatusInput(
  input: unknown
): ValidationResult<ValidatedUpdateUserStatusInput> {
  const rec = asRecord(input);
  const status = asTrimmedString(rec.status).toUpperCase();
  if (!(ALLOWED_USER_MUTABLE_STATUSES as readonly string[]).includes(status)) {
    return {
      ok: false,
      code: "INVALID_USER_STATUS",
      message: "Trạng thái tài khoản chỉ hỗ trợ hoạt động hoặc khóa.",
    };
  }
  const reason = asTrimmedString(rec.reason).slice(0, 500);
  return { ok: true, value: { status: status as AdminMutableUserStatus, reason } };
}

export interface ValidatedAdminShopReviewInput {
  decision: "APPROVE" | "REJECT" | "REQUEST_INFO";
  reason: string;
  expected_owner_name: string;
}

/** Validates POST /api/admin/shops/[id]/review including D6 expected_owner_name on APPROVE. */
export function validateAdminShopReviewInput(
  input: unknown
): ValidationResult<ValidatedAdminShopReviewInput> {
  const rec = asRecord(input);
  const decision = asTrimmedString(rec.decision).toUpperCase();
  if (decision !== "APPROVE" && decision !== "REJECT" && decision !== "REQUEST_INFO") {
    return {
      ok: false,
      code: "INVALID_DECISION",
      message: "Quyết định thẩm định gian hàng không hợp lệ.",
    };
  }
  const reason = asTrimmedString(rec.reason).slice(0, 1000);
  if ((decision === "REJECT" || decision === "REQUEST_INFO") && !reason) {
    return {
      ok: false,
      code: "REASON_REQUIRED",
      message: "Vui lòng nhập lý do cụ thể khi từ chối hoặc yêu cầu bổ sung hồ sơ.",
    };
  }
  const expectedOwnerName = asTrimmedString(rec.expected_owner_name).slice(0, 200);
  if (decision === "APPROVE" && !expectedOwnerName) {
    return {
      ok: false,
      code: "EXPECTED_OWNER_NAME_REQUIRED",
      message: "Cần đối chiếu họ tên chủ gian hàng trước khi phê duyệt.",
    };
  }
  return {
    ok: true,
    value: {
      decision,
      reason,
      expected_owner_name: decision === "APPROVE" ? expectedOwnerName : "",
    },
  };
}

export interface ValidatedShopBankReviewInput {
  bank_account_id: string;
  decision: "APPROVE" | "REJECT";
  reason: string;
}

/** Validates POST /api/admin/shops/[id]/bank-account/review (requires bank_account_id). */
export function validateShopBankReviewInput(
  input: unknown
): ValidationResult<ValidatedShopBankReviewInput> {
  const rec = asRecord(input);
  const bankAccountId = parseEntityId(rec.bank_account_id);
  if (!bankAccountId) {
    return {
      ok: false,
      code: "INVALID_BANK_ACCOUNT_ID",
      message: "Thiếu mã bản ghi tài khoản ngân hàng hợp lệ.",
    };
  }
  const decision = asTrimmedString(rec.decision).toUpperCase();
  if (decision !== "APPROVE" && decision !== "REJECT") {
    return {
      ok: false,
      code: "INVALID_DECISION",
      message: "Quyết định thẩm định tài khoản ngân hàng không hợp lệ.",
    };
  }
  const reason = asTrimmedString(rec.reason).slice(0, 1000);
  if (decision === "REJECT" && reason.length < 3) {
    return {
      ok: false,
      code: "REASON_REQUIRED",
      message: "Vui lòng nhập lý do từ chối tài khoản ngân hàng (tối thiểu 3 ký tự).",
    };
  }
  return {
    ok: true,
    value: {
      bank_account_id: bankAccountId,
      decision,
      reason,
    },
  };
}

export interface ValidatedPaymentMethodConfigInput {
  enabled: boolean;
  min_order_vnd: number;
  max_order_vnd: number;
  expiry_minutes: number;
  note: string;
  expected_version: number;
}

/** Validates PUT /api/admin/payment-methods/[method] with integer VND and version lock. */
export function validatePaymentMethodConfigInput(
  input: unknown
): ValidationResult<ValidatedPaymentMethodConfigInput> {
  const rec = asRecord(input);
  if (typeof rec.enabled !== "boolean") {
    return {
      ok: false,
      code: "INVALID_ENABLED",
      message: "Trạng thái bật/tắt phương thức thanh toán không hợp lệ.",
    };
  }
  if (
    typeof rec.min_order_vnd !== "number" ||
    !Number.isInteger(rec.min_order_vnd) ||
    rec.min_order_vnd < 0
  ) {
    return {
      ok: false,
      code: "INVALID_MIN_ORDER_VND",
      message: "Giá trị đơn tối thiểu phải là số nguyên VND không âm.",
    };
  }
  if (
    typeof rec.max_order_vnd !== "number" ||
    !Number.isInteger(rec.max_order_vnd) ||
    rec.max_order_vnd < 0
  ) {
    return {
      ok: false,
      code: "INVALID_MAX_ORDER_VND",
      message: "Giá trị đơn tối đa phải là số nguyên VND không âm.",
    };
  }
  if (rec.max_order_vnd > 0 && rec.min_order_vnd > rec.max_order_vnd) {
    return {
      ok: false,
      code: "INVALID_ORDER_RANGE",
      message: "Giá trị tối thiểu không được lớn hơn giá trị tối đa.",
    };
  }
  if (
    typeof rec.expiry_minutes !== "number" ||
    !Number.isInteger(rec.expiry_minutes) ||
    rec.expiry_minutes < 0 ||
    rec.expiry_minutes > 1440
  ) {
    return {
      ok: false,
      code: "INVALID_EXPIRY_MINUTES",
      message: "Thời hạn giữ đơn phải là số nguyên từ 0 đến 1440 phút.",
    };
  }
  if (
    typeof rec.expected_version !== "number" ||
    !Number.isInteger(rec.expected_version) ||
    rec.expected_version < 0
  ) {
    return {
      ok: false,
      code: "INVALID_EXPECTED_VERSION",
      message: "Thiếu thông tin phiên bản cấu hình hiện tại.",
    };
  }
  const note = asTrimmedString(rec.note).slice(0, 500);
  return {
    ok: true,
    value: {
      enabled: rec.enabled,
      min_order_vnd: rec.min_order_vnd,
      max_order_vnd: rec.max_order_vnd,
      expiry_minutes: rec.expiry_minutes,
      note,
      expected_version: rec.expected_version,
    },
  };
}

export interface ValidatedCategoryWithProhibitedInput {
  name: string;
  slug: string;
  parent_id: string;
  commission_rate_bps: number;
  sort_order: number;
  is_prohibited: boolean;
}

/** Validates POST/PUT /api/admin/categories including `is_prohibited`. */
export function validateAdminCategoryPayload(
  input: unknown
): ValidationResult<ValidatedCategoryWithProhibitedInput> {
  const rec = asRecord(input);
  const name = asTrimmedString(rec.name);
  if (name.length < 2 || name.length > 120) {
    return {
      ok: false,
      code: "INVALID_CATEGORY_NAME",
      message: "Tên danh mục cần từ 2 đến 120 ký tự.",
    };
  }
  const slug = asTrimmedString(rec.slug).toLowerCase();
  if (!slug || slug.length > 120 || !CATEGORY_SLUG_RE.test(slug)) {
    return {
      ok: false,
      code: "INVALID_CATEGORY_SLUG",
      message: "Slug danh mục chỉ gồm chữ thường không dấu, số và dấu gạch ngang.",
    };
  }
  const rawParentId = asTrimmedString(rec.parent_id);
  if (rawParentId && !parseEntityId(rawParentId)) {
    return {
      ok: false,
      code: "INVALID_PARENT_ID",
      message: "Mã danh mục cha không hợp lệ.",
    };
  }
  const rawBps = rec.commission_rate_bps;
  if (typeof rawBps !== "number" || !Number.isInteger(rawBps) || rawBps < 0 || rawBps > 5000) {
    return {
      ok: false,
      code: "INVALID_COMMISSION_BPS",
      message: "Phí hoa hồng phải là số nguyên từ 0 đến 5.000 bps (0% đến 50%).",
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
  if (rec.is_prohibited !== undefined && typeof rec.is_prohibited !== "boolean") {
    return {
      ok: false,
      code: "INVALID_IS_PROHIBITED",
      message: "Trạng thái danh mục hàng cấm không hợp lệ.",
    };
  }
  return {
    ok: true,
    value: {
      name,
      slug,
      parent_id: rawParentId,
      commission_rate_bps: rawBps,
      sort_order: sortOrder,
      is_prohibited: rec.is_prohibited === true,
    },
  };
}

export interface ValidatedCategoryFeeScheduleInput {
  commission_rate_bps: number;
  effective_from: string;
  note: string;
}

export function validateCategoryFeeScheduleInput(
  input: unknown
): ValidationResult<ValidatedCategoryFeeScheduleInput> {
  const rec = asRecord(input);
  if (
    typeof rec.commission_rate_bps !== "number" ||
    !Number.isInteger(rec.commission_rate_bps) ||
    rec.commission_rate_bps < 0 ||
    rec.commission_rate_bps > 5000
  ) {
    return {
      ok: false,
      code: "INVALID_COMMISSION_BPS",
      message: "Phí hoa hồng hợp lệ là số nguyên từ 0 đến 5.000 bps (0% – 50%).",
    };
  }
  const rawEff = asTrimmedString(rec.effective_from);
  const effMs = Date.parse(rawEff);
  if (!rawEff || Number.isNaN(effMs)) {
    return {
      ok: false,
      code: "INVALID_EFFECTIVE_FROM",
      message: "Thời điểm bắt đầu hiệu lực không hợp lệ.",
    };
  }
  return {
    ok: true,
    value: {
      commission_rate_bps: rec.commission_rate_bps,
      effective_from: new Date(effMs).toISOString(),
      note: asTrimmedString(rec.note).slice(0, 500),
    },
  };
}

export interface ValidatedPaymentFeeConfigInput {
  payment_method: AdminPaymentMethodCode;
  fee_bps: number;
  effective_from: string;
  note: string;
}

export function validatePaymentFeeConfigInput(
  input: unknown
): ValidationResult<ValidatedPaymentFeeConfigInput> {
  const rec = asRecord(input);
  const method = parsePaymentMethodCode(rec.payment_method);
  if (!method) {
    return {
      ok: false,
      code: "INVALID_PAYMENT_METHOD",
      message: "Phương thức thanh toán không hợp lệ.",
    };
  }
  if (
    typeof rec.fee_bps !== "number" ||
    !Number.isInteger(rec.fee_bps) ||
    rec.fee_bps < 0 ||
    rec.fee_bps > 2000
  ) {
    return {
      ok: false,
      code: "INVALID_FEE_BPS",
      message: "Phí thanh toán hợp lệ là số nguyên từ 0 đến 2.000 bps (0% – 20%).",
    };
  }
  const rawEff = asTrimmedString(rec.effective_from);
  const effMs = Date.parse(rawEff);
  if (!rawEff || Number.isNaN(effMs)) {
    return {
      ok: false,
      code: "INVALID_EFFECTIVE_FROM",
      message: "Thời điểm bắt đầu hiệu lực không hợp lệ.",
    };
  }
  return {
    ok: true,
    value: {
      payment_method: method,
      fee_bps: rec.fee_bps,
      effective_from: new Date(effMs).toISOString(),
      note: asTrimmedString(rec.note).slice(0, 500),
    },
  };
}

export interface ValidatedCarrierFallbackRate {
  region: AdminShippingRegion;
  base_fee_vnd: number;
  base_weight_grams: number;
  extra_fee_vnd_per_500g: number;
}

export interface ValidatedCarrierUpdateInput {
  enabled: boolean;
  api_token?: string;
  fallback_rates: ValidatedCarrierFallbackRate[];
}

export function validateCarrierUpdateInput(
  input: unknown
): ValidationResult<ValidatedCarrierUpdateInput> {
  const rec = asRecord(input);
  if (typeof rec.enabled !== "boolean") {
    return {
      ok: false,
      code: "INVALID_ENABLED",
      message: "Trạng thái bật/tắt hãng vận chuyển không hợp lệ.",
    };
  }
  if (!Array.isArray(rec.fallback_rates)) {
    return {
      ok: false,
      code: "INVALID_FALLBACK_RATES",
      message: "Bảng cước dự phòng không hợp lệ.",
    };
  }
  const fallbackRates: ValidatedCarrierFallbackRate[] = [];
  for (const item of rec.fallback_rates) {
    const r = asRecord(item);
    const region = asTrimmedString(r.region).toUpperCase();
    if (!(ALLOWED_SHIPPING_REGIONS as readonly string[]).includes(region)) {
      return {
        ok: false,
        code: "INVALID_REGION",
        message: "Tuyến vận chuyển trong bảng cước dự phòng không hợp lệ.",
      };
    }
    if (
      typeof r.base_fee_vnd !== "number" ||
      !Number.isInteger(r.base_fee_vnd) ||
      r.base_fee_vnd < 0 ||
      typeof r.base_weight_grams !== "number" ||
      !Number.isInteger(r.base_weight_grams) ||
      r.base_weight_grams <= 0 ||
      typeof r.extra_fee_vnd_per_500g !== "number" ||
      !Number.isInteger(r.extra_fee_vnd_per_500g) ||
      r.extra_fee_vnd_per_500g < 0
    ) {
      return {
        ok: false,
        code: "INVALID_FALLBACK_RATE_VALUES",
        message: "Cước dự phòng phải là số nguyên VND không âm và mốc khối lượng lớn hơn 0g.",
      };
    }
    fallbackRates.push({
      region: region as AdminShippingRegion,
      base_fee_vnd: r.base_fee_vnd,
      base_weight_grams: r.base_weight_grams,
      extra_fee_vnd_per_500g: r.extra_fee_vnd_per_500g,
    });
  }
  const payload: ValidatedCarrierUpdateInput = {
    enabled: rec.enabled,
    fallback_rates: fallbackRates,
  };
  if (rec.api_token !== undefined && rec.api_token !== null) {
    if (typeof rec.api_token !== "string") {
      return {
        ok: false,
        code: "INVALID_API_TOKEN",
        message: "Khóa kết nối API không hợp lệ.",
      };
    }
    const cleanToken = rec.api_token.trim();
    if (cleanToken) {
      if (cleanToken.length > 512) {
        return {
          ok: false,
          code: "INVALID_API_TOKEN",
          message: "Khóa kết nối API vượt quá độ dài cho phép.",
        };
      }
      payload.api_token = cleanToken;
    }
  }
  return { ok: true, value: payload };
}
