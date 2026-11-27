// Pure validation and view helpers for Seller Payouts (/seller/tai-chinh).
// Zero I/O, zero DOM — safe for client components, route handlers, and Node unit tests.

export type SellerPayoutStatusTone = "attention" | "success" | "critical" | "neutral";

export interface SellerPayoutStatusMeta {
  label: string;
  tone: SellerPayoutStatusTone;
}

export function getSellerPayoutStatusMeta(
  status: string | null | undefined
): SellerPayoutStatusMeta {
  switch ((status ?? "").trim().toUpperCase()) {
    case "PENDING":
      return { label: "Chờ duyệt", tone: "attention" };
    case "COMPLETED":
      return { label: "Đã chi trả", tone: "success" };
    case "REJECTED":
      return { label: "Từ chối", tone: "critical" };
    default:
      return { label: status?.trim() || "Không xác định", tone: "neutral" };
  }
}

/**
 * Parses an integer VND string entered by a seller (digits only; rejects decimals/negative/NaN).
 */
export function parseWithdrawAmountVnd(raw: string): number | null {
  const digits = raw.trim().replace(/\s+/g, "");
  if (!/^\d+$/.test(digits)) {
    return null;
  }
  const n = Number(digits);
  if (!Number.isSafeInteger(n) || n <= 0) {
    return null;
  }
  return n;
}

export type WithdrawValidationResult =
  { ok: true; amountVnd: number } | { ok: false; message: string };

export function validateWithdrawAmountInput(
  raw: string,
  availableVnd: number
): WithdrawValidationResult {
  const amount = parseWithdrawAmountVnd(raw);
  if (amount === null) {
    return {
      ok: false,
      message: "Vui lòng nhập số tiền rút là số nguyên VND lớn hơn 0.",
    };
  }
  const safeAvailable = Number.isFinite(availableVnd) ? Math.max(0, Math.trunc(availableVnd)) : 0;
  if (safeAvailable <= 0) {
    return {
      ok: false,
      message: "Số dư khả dụng hiện bằng 0₫ nên chưa thể tạo yêu cầu rút tiền.",
    };
  }
  if (amount > safeAvailable) {
    return {
      ok: false,
      message: `Số tiền rút vượt quá số dư khả dụng (${safeAvailable.toLocaleString("vi-VN")}₫).`,
    };
  }
  return { ok: true, amountVnd: amount };
}

export function mintClientPayoutIdempotencyKey(uuidFn?: () => string): string {
  const randomPart =
    uuidFn?.() ??
    (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
      ? crypto.randomUUID()
      : `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`);
  return `payout-${randomPart}`;
}

export type SellerPayoutReadiness =
  | { ready: true }
  | {
      ready: false;
      reason: "no_bank" | "bank_unverified" | "bank_locked" | "pending_exists" | "zero_balance";
    };

export function evaluateSellerPayoutReadiness(input: {
  bankAccountStatus: string | null | undefined;
  isPayoutLocked: boolean;
  availableVnd: number;
  payoutPendingVnd: number;
  hasPendingPayoutRow: boolean;
}): SellerPayoutReadiness {
  const status = (input.bankAccountStatus ?? "").trim().toUpperCase();
  if (!status) {
    return { ready: false, reason: "no_bank" };
  }
  if (status !== "VERIFIED") {
    return { ready: false, reason: "bank_unverified" };
  }
  if (input.isPayoutLocked) {
    return { ready: false, reason: "bank_locked" };
  }
  if (input.hasPendingPayoutRow || input.payoutPendingVnd > 0) {
    return { ready: false, reason: "pending_exists" };
  }
  if (input.availableVnd <= 0) {
    return { ready: false, reason: "zero_balance" };
  }
  return { ready: true };
}
