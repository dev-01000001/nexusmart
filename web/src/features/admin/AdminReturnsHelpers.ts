// Pure validation and display helpers for Admin Returns (/admin/khieu-nai) and path ID validation.
// Zero I/O, zero DOM, safe for server components, BFF route handlers, client components, and Node tests.
import type { ReturnAdminDecision, ReturnFeeBearer } from "../../lib/gateway-types.ts";
import type { ValidationResult } from "./adminComplaintsTypes.ts";

const SAFE_PATH_ID_RE = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$/;

export const RETURN_ADMIN_DECISIONS: readonly ReturnAdminDecision[] = [
  "REFUND",
  "RETURN_AND_REFUND",
  "REQUEST_EVIDENCE",
  "REJECT",
] as const;

export const RETURN_FEE_BEARERS: readonly ReturnFeeBearer[] = [
  "SELLER",
  "BUYER",
  "PLATFORM",
] as const;

export interface ValidatedReturnResolution {
  decision: ReturnAdminDecision;
  note: string;
  return_fee_bearer: "" | ReturnFeeBearer;
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function asString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

/**
 * Validates path parameter IDs forwarded by BFF routes to the gateway.
 * Rejects empty values, path traversal, slashes, whitespace, encoded separators, and oversized IDs.
 */
export function isValidAdminPathId(raw: string | null | undefined): boolean {
  const clean = (raw ?? "").trim();
  if (!clean) return false;
  return SAFE_PATH_ID_RE.test(clean);
}

/**
 * Matches backend services/order AdminResolveReturn: only ESCALATED and EVIDENCE_REQUESTED
 * return disputes can receive an admin arbitration decision.
 */
export function canAdminResolveReturn(status: string | null | undefined): boolean {
  const norm = (status ?? "")
    .trim()
    .toUpperCase()
    .replace(/^RETURN_STATUS_/, "");
  return norm === "ESCALATED" || norm === "EVIDENCE_REQUESTED";
}

export function getReturnDecisionLabelVi(decision: string): string {
  switch (decision.trim().toUpperCase()) {
    case "REFUND":
      return "Hoàn tiền ngay (không cần gửi trả hàng)";
    case "RETURN_AND_REFUND":
      return "Trả hàng và hoàn tiền";
    case "REQUEST_EVIDENCE":
      return "Yêu cầu người mua bổ sung bằng chứng";
    case "REJECT":
      return "Từ chối khiếu nại";
    default:
      return "Phân xử yêu cầu";
  }
}

export function getReturnFeeBearerLabelVi(bearer: string | null | undefined): string {
  switch ((bearer ?? "").trim().toUpperCase()) {
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

export function validateReturnResolveInput(
  input: unknown,
  opts: { goodsReceived?: boolean } = {}
): ValidationResult<ValidatedReturnResolution> {
  const rec = asRecord(input);
  const rawDecision = asString(rec.decision).toUpperCase();
  if (!(RETURN_ADMIN_DECISIONS as readonly string[]).includes(rawDecision)) {
    return {
      ok: false,
      code: "INVALID_DECISION",
      message: "Quyết định phân xử yêu cầu trả hàng không hợp lệ.",
    };
  }
  const decision = rawDecision as ReturnAdminDecision;

  if (opts.goodsReceived && decision === "RETURN_AND_REFUND") {
    return {
      ok: false,
      code: "GOODS_ALREADY_RECEIVED",
      message: "Gian hàng đã nhận hàng trả; vui lòng chọn hoàn tiền trực tiếp hoặc từ chối.",
    };
  }

  const rawBearer = asString(rec.return_fee_bearer).toUpperCase();
  if (rawBearer && !(RETURN_FEE_BEARERS as readonly string[]).includes(rawBearer)) {
    return {
      ok: false,
      code: "INVALID_FEE_BEARER",
      message: "Bên chịu phí hoàn hàng không hợp lệ.",
    };
  }

  const note = asString(rec.note);
  if ((decision === "REJECT" || decision === "REQUEST_EVIDENCE") && note.length < 4) {
    return {
      ok: false,
      code: "NOTE_REQUIRED",
      message:
        decision === "REQUEST_EVIDENCE"
          ? "Vui lòng ghi rõ loại bằng chứng cần người mua bổ sung (tối thiểu 4 ký tự)."
          : "Vui lòng nhập lý do từ chối khiếu nại (tối thiểu 4 ký tự).",
    };
  }
  if (note.length > 2000) {
    return {
      ok: false,
      code: "NOTE_TOO_LONG",
      message: "Ghi chú phân xử không được vượt quá 2.000 ký tự.",
    };
  }

  const returnFeeBearer: "" | ReturnFeeBearer =
    decision === "RETURN_AND_REFUND" || decision === "REFUND"
      ? (rawBearer as "" | ReturnFeeBearer)
      : "";

  return {
    ok: true,
    value: {
      decision,
      note,
      return_fee_bearer: returnFeeBearer,
    },
  };
}
