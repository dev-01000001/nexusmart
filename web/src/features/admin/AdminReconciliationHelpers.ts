// Pure validation helpers for Admin Reconciliation, Refunds & Needs-Review Orders (/admin/doi-soat).
// Zero I/O, zero DOM, safe for route handlers, client components, and Node tests.
import type { GatewayReconciliationAction } from "../../lib/gateway-types.ts";
import {
  isAmbiguousCheckoutSubmitError,
  resolveStableReferenceKey,
  type StableReferenceCache,
} from "../checkout/helpers.ts";
import type { ValidationResult } from "./adminComplaintsTypes.ts";
import { isValidAdminPathId } from "./AdminReturnsHelpers.ts";

export type { StableReferenceCache };

const IDEMPOTENCY_KEY_RE = /^[a-zA-Z0-9._:-]{4,128}$/;

/**
 * Builds a deterministic fingerprint for an admin money operation payload so the pinned
 * `Idempotency-Key` stays identical across retries of the same operation and rotates as soon
 * as any field changes.
 */
export function buildAdminMoneyFingerprint(
  scope: string,
  payload: Record<string, string | number | boolean | null | undefined>
): string {
  const sortedEntries = Object.keys(payload)
    .sort()
    .map((key) => {
      const raw = payload[key];
      const normalized = typeof raw === "string" ? raw.trim() : (raw ?? null);
      return [key, normalized] as const;
    });
  return JSON.stringify([scope.trim(), sortedEntries]);
}

/**
 * Returns true ONLY when the server definitively rejected the money mutation with a terminal 4xx
 * (e.g. 400/401/403/404/409 non-ABORTED/422), meaning no partial state is in flight and a fresh
 * `Idempotency-Key` should be minted on the next attempt. Ambiguous outcomes (network failure,
 * 5xx, 499, 408, 429, 409 ABORTED) return false so the caller keeps the pinned key.
 */
export function isDefinitiveAdminMoneyError(err: unknown): boolean {
  if (isAmbiguousCheckoutSubmitError(err)) {
    return false;
  }
  if (!err || typeof err !== "object") {
    return false;
  }
  const status = (err as { status?: unknown }).status;
  if (typeof status !== "number" || !Number.isFinite(status)) {
    return false;
  }
  return status >= 400 && status < 500 && status !== 408 && status !== 429 && status !== 499;
}

export function resolveAdminMoneyIdempotencyKey(input: {
  prefix: string;
  fingerprint: string;
  cache: StableReferenceCache | null;
  uuidFn?: () => string;
}): { key: string; cache: StableReferenceCache | null } {
  return resolveStableReferenceKey({
    prefix: input.prefix,
    fingerprint: input.fingerprint,
    cache: input.cache,
    uuidFn: input.uuidFn,
  });
}

export function nextAdminMoneyIdempotencyCacheAfterError(
  cache: StableReferenceCache | null,
  err: unknown
): StableReferenceCache | null {
  return isDefinitiveAdminMoneyError(err) ? null : cache;
}

export const RECON_QUEUE_ACTIONS: readonly GatewayReconciliationAction[] = [
  "ASSIGN",
  "REFUND",
  "SUSPEND",
] as const;

export interface ValidatedQueueResolveInput {
  action: GatewayReconciliationAction;
  payment_id: string;
  note: string;
}

export interface ValidatedManualRefundInput {
  payment_id: string;
  order_id: string;
  amount_vnd: number;
  reason: string;
}

export interface ValidatedSettleRefundInput {
  provider_refund_id: string;
  note: string;
}

export interface ValidatedRefundDecisionInput {
  approve: boolean;
  note: string;
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function asString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

export function validateIdempotencyKeyHeader(raw: string | null | undefined): string | null {
  const clean = (raw ?? "").trim();
  if (!IDEMPOTENCY_KEY_RE.test(clean)) return null;
  return clean;
}

export function validateReconciliationQueueResolveInput(
  input: unknown
): ValidationResult<ValidatedQueueResolveInput> {
  const rec = asRecord(input);
  const rawAction = asString(rec.action).toUpperCase();
  if (!(RECON_QUEUE_ACTIONS as readonly string[]).includes(rawAction)) {
    return {
      ok: false,
      code: "INVALID_ACTION",
      message: "Hành động xử lý giao dịch lệch không hợp lệ.",
    };
  }
  const action = rawAction as GatewayReconciliationAction;
  const paymentId = asString(rec.payment_id);
  if (action === "ASSIGN" && !isValidAdminPathId(paymentId)) {
    return {
      ok: false,
      code: "INVALID_PAYMENT_ID",
      message: "Vui lòng nhập mã giao dịch nội bộ hợp lệ để gán khớp.",
    };
  }
  if (paymentId && !isValidAdminPathId(paymentId)) {
    return {
      ok: false,
      code: "INVALID_PAYMENT_ID",
      message: "Mã giao dịch nội bộ không đúng định dạng.",
    };
  }

  const note = asString(rec.note);
  if (note.length > 1000) {
    return {
      ok: false,
      code: "NOTE_TOO_LONG",
      message: "Ghi chú xử lý không được vượt quá 1.000 ký tự.",
    };
  }

  return {
    ok: true,
    value: {
      action,
      payment_id: action === "ASSIGN" ? paymentId : "",
      note,
    },
  };
}

export function validateManualRefundInput(
  input: unknown
): ValidationResult<ValidatedManualRefundInput> {
  const rec = asRecord(input);
  const paymentId = asString(rec.payment_id);
  if (!isValidAdminPathId(paymentId)) {
    return {
      ok: false,
      code: "INVALID_PAYMENT_ID",
      message: "Mã thanh toán không hợp lệ.",
    };
  }
  const orderId = asString(rec.order_id);
  if (!isValidAdminPathId(orderId)) {
    return {
      ok: false,
      code: "INVALID_ORDER_ID",
      message: "Mã đơn hàng không hợp lệ.",
    };
  }
  const amount = rec.amount_vnd;
  if (typeof amount !== "number" || !Number.isInteger(amount) || amount <= 0) {
    return {
      ok: false,
      code: "INVALID_AMOUNT_VND",
      message: "Số tiền hoàn phải là số nguyên VND lớn hơn 0.",
    };
  }
  const reason = asString(rec.reason);
  if (reason.length < 3 || reason.length > 500) {
    return {
      ok: false,
      code: "INVALID_REASON",
      message: "Lý do hoàn tiền cần từ 3 đến 500 ký tự.",
    };
  }

  return {
    ok: true,
    value: {
      payment_id: paymentId,
      order_id: orderId,
      amount_vnd: amount,
      reason,
    },
  };
}

export function validateSettleRefundInput(
  input: unknown
): ValidationResult<ValidatedSettleRefundInput> {
  const rec = asRecord(input);
  const providerRefundId = asString(rec.provider_refund_id);
  if (providerRefundId.length < 2 || providerRefundId.length > 120) {
    return {
      ok: false,
      code: "INVALID_PROVIDER_REFUND_ID",
      message: "Mã giao dịch chuyển khoản hoàn tiền cần từ 2 đến 120 ký tự.",
    };
  }
  const note = asString(rec.note);
  if (note.length > 500) {
    return {
      ok: false,
      code: "NOTE_TOO_LONG",
      message: "Ghi chú tất toán không được vượt quá 500 ký tự.",
    };
  }
  return {
    ok: true,
    value: {
      provider_refund_id: providerRefundId,
      note,
    },
  };
}

export function validateRefundApprovalDecisionInput(
  input: unknown
): ValidationResult<ValidatedRefundDecisionInput> {
  const rec = asRecord(input);
  if (typeof rec.approve !== "boolean") {
    return {
      ok: false,
      code: "INVALID_DECISION",
      message: "Quyết định phê duyệt hoàn tiền không hợp lệ.",
    };
  }
  const note = asString(rec.note);
  if (!rec.approve && note.length < 4) {
    return {
      ok: false,
      code: "NOTE_REQUIRED",
      message: "Vui lòng nhập lý do từ chối phê duyệt (tối thiểu 4 ký tự).",
    };
  }
  if (note.length > 500) {
    return {
      ok: false,
      code: "NOTE_TOO_LONG",
      message: "Ghi chú phê duyệt không được vượt quá 500 ký tự.",
    };
  }
  return {
    ok: true,
    value: {
      approve: rec.approve,
      note,
    },
  };
}

export function validateResolveOrderReviewInput(
  input: unknown
): ValidationResult<{ note: string }> {
  const rec = asRecord(input);
  const note = asString(rec.note);
  if (note.length > 500) {
    return {
      ok: false,
      code: "NOTE_TOO_LONG",
      message: "Ghi chú rà soát đơn không được vượt quá 500 ký tự.",
    };
  }
  return { ok: true, value: { note } };
}

export interface ValidatedSellerPayoutDecisionInput {
  decision: "APPROVE" | "REJECT";
  reason: string;
}

export function validateSellerPayoutDecisionInput(
  input: unknown
): ValidationResult<ValidatedSellerPayoutDecisionInput> {
  const rec = asRecord(input);
  const decision = asString(rec.decision).toUpperCase();
  if (decision !== "APPROVE" && decision !== "REJECT") {
    return {
      ok: false,
      code: "INVALID_ARGUMENT",
      message: "Quyết định chi trả chỉ chấp nhận APPROVE hoặc REJECT.",
    };
  }
  const reason = asString(rec.reason);
  if (decision === "REJECT" && reason.length === 0) {
    return {
      ok: false,
      code: "INVALID_ARGUMENT",
      message: "Vui lòng nhập lý do từ chối yêu cầu rút tiền.",
    };
  }
  if (reason.length > 500) {
    return {
      ok: false,
      code: "INVALID_ARGUMENT",
      message: "Lý do không được vượt quá 500 ký tự.",
    };
  }
  return {
    ok: true,
    value: {
      decision,
      reason,
    },
  };
}

export function computePendingPayoutTotalVnd(
  items: ReadonlyArray<{ status: string; amount_vnd: number }>
): number {
  let sum = 0;
  for (const item of items) {
    if (
      item.status.toUpperCase() === "PENDING" &&
      Number.isFinite(item.amount_vnd) &&
      item.amount_vnd > 0
    ) {
      sum += Math.trunc(item.amount_vnd);
    }
  }
  return sum;
}

const CANONICAL_UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export interface AdminEscrowReleaseResponse {
  released: boolean;
  released_amount_vnd: number;
  already_released: boolean;
}

export function validateEscrowReleaseOrderId(input: unknown): ValidationResult<string> {
  const clean = asString(input);
  if (!CANONICAL_UUID_RE.test(clean)) {
    return {
      ok: false,
      code: "INVALID_ORDER_ID",
      message: "Mã đơn hàng phải đúng định dạng UUID.",
    };
  }
  return { ok: true, value: clean };
}

export function formatEscrowReleaseToastMessage(
  result: AdminEscrowReleaseResponse,
  formatVndFn: (amountVnd: number) => string
): string {
  if (result.already_released) {
    return "Đơn này đã được giải ngân trước đó";
  }
  if (result.released) {
    return `Đã giải ngân ${formatVndFn(result.released_amount_vnd)}`;
  }
  return "Không tìm thấy khoản chờ giải ngân cho mã đơn hàng này.";
}

export function mapEscrowReleaseErrorMessage(err: unknown, fallback: string): string {
  if (err && typeof err === "object") {
    const rec = err as { status?: unknown; code?: unknown };
    const code = typeof rec.code === "string" ? rec.code.trim().toUpperCase() : "";
    if (rec.status === 409 || code === "FAILED_PRECONDITION" || code === "ALREADY_EXISTS") {
      return "Đơn chưa hoàn tất nên chưa thể giải ngân";
    }
  }
  return fallback;
}
