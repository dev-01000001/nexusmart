// Error model shared by every BFF route. Gateway internals (raw messages, trace ids) stay on the
// server; the browser only ever sees { error: { code, message } } with a safe Vietnamese message.
import { extractSafeAddressFeeQuote, type AddressFeeChangedQuote } from "../a8-types.ts";
import { sanitizeGatewayError } from "./error-map.ts";
import { redactLogFields, scrubLogString } from "./trace.ts";

export type GatewayErrorKind =
  | "unauthenticated" // 401 – no session / session expired
  | "forbidden" // 403 – logged in but not allowed (also bad Origin)
  | "not_found" // 404
  | "conflict" // 409 – business rule (out of stock, wrong state, already exists…)
  | "invalid" // 400 – input rejected
  | "payload_too_large" // 413 – request body exceeds bounded limit (2 MiB)
  | "rate_limited" // 429
  | "unavailable"; // 503 – gateway down, timeout, 5xx, unexpected payload

const STATUS: Record<GatewayErrorKind, number> = {
  unauthenticated: 401,
  forbidden: 403,
  not_found: 404,
  conflict: 409,
  invalid: 400,
  payload_too_large: 413,
  rate_limited: 429,
  unavailable: 503,
};

export const SAFE_MESSAGES: Record<GatewayErrorKind, string> = {
  unauthenticated: "Phiên đăng nhập đã hết hạn. Bạn vui lòng đăng nhập lại.",
  forbidden: "Bạn không có quyền thực hiện thao tác này.",
  not_found: "Không tìm thấy nội dung bạn yêu cầu.",
  conflict: "Thao tác chưa thực hiện được. Bạn vui lòng tải lại trang và thử lại.",
  invalid: "Thông tin chưa hợp lệ. Bạn vui lòng kiểm tra lại.",
  payload_too_large: "Dữ liệu gửi lên vượt quá giới hạn cho phép (tối đa 2 MB).",
  rate_limited: "Bạn thao tác hơi nhanh. Vui lòng thử lại sau ít phút.",
  unavailable: "Hệ thống đang bận. Bạn vui lòng thử lại sau.",
};

export type SafeAddressFeeQuote = AddressFeeChangedQuote;

export interface GatewayErrorExtras {
  retryAfter?: string;
  safeMessage?: string;
  quote?: SafeAddressFeeQuote;
}

export class GatewayError extends Error {
  readonly kind: GatewayErrorKind;
  /** HTTP status the BFF returns to the browser. */
  readonly status: number;
  /** Canonical code, safe for the client (e.g. FAILED_PRECONDITION, COD_PHONE_NOT_VERIFIED). */
  readonly code: string;
  /** Raw gateway message. SERVER-ONLY: use it to pick a friendly message, never send it. */
  readonly detail: string;
  readonly traceId: string;
  readonly retryAfter?: string;
  /** Sanitized Vietnamese message derived from domain error map (safe for client). */
  readonly safeMessage?: string;
  /** Strictly whitelisted integer quote forwarded only on 409 ADDRESS_FEE_CHANGED. */
  readonly quote?: SafeAddressFeeQuote;

  constructor(
    kind: GatewayErrorKind,
    code?: string,
    detail = "",
    traceId = "",
    extras?: GatewayErrorExtras
  ) {
    super(`${kind}:${code ?? kind}`);
    this.name = "GatewayError";
    this.kind = kind;
    this.status = STATUS[kind];
    this.code = code || kind.toUpperCase();
    this.detail = detail;
    this.traceId = traceId;
    if (extras?.retryAfter) this.retryAfter = extras.retryAfter;
    if (extras?.safeMessage) this.safeMessage = extras.safeMessage;
    if (extras?.quote) this.quote = extras.quote;
  }
}

export function kindFromStatus(status: number): GatewayErrorKind {
  if (status === 401) return "unauthenticated";
  if (status === 403) return "forbidden";
  if (status === 404) return "not_found";
  if (status === 409 || status === 412 || status === 422) return "conflict";
  if (status === 413) return "payload_too_large";
  if (status === 400) return "invalid";
  if (status === 429) return "rate_limited";
  return "unavailable";
}

/** Build a GatewayError from a non-2xx gateway response (reads the standard error envelope). */
export async function errorFromResponse(
  res: Response,
  fallbackTraceId = ""
): Promise<GatewayError> {
  const kind = kindFromStatus(res.status);
  let rawCode = "";
  let detail = "";
  let traceId = "";
  let parsedBody: unknown = null;
  try {
    parsedBody = await res.json();
    const body = parsedBody as {
      error?: { code?: string; message?: string; trace_id?: string };
    } | null;
    rawCode = body?.error?.code ?? "";
    detail = body?.error?.message ?? "";
    traceId = body?.error?.trace_id ?? "";
  } catch {
    // non-JSON body (proxy error page etc.)
  }
  if (!traceId) {
    traceId = res.headers.get("x-trace-id")?.trim() || fallbackTraceId;
  }
  const retryAfter = res.headers.get("retry-after")?.trim() || undefined;

  // Internal failures (5xx / network) never expose the backend code or detail.
  if (kind === "unavailable") {
    return new GatewayError("unavailable", "UNAVAILABLE", detail, traceId, { retryAfter });
  }

  const sanitized = sanitizeGatewayError(res.status, parsedBody);
  const code = sanitized.code || rawCode || undefined;
  const quote =
    res.status === 409 && sanitized.code === "ADDRESS_FEE_CHANGED"
      ? (extractSafeAddressFeeQuote(parsedBody) ?? undefined)
      : undefined;

  return new GatewayError(kind, code, detail, traceId, {
    retryAfter,
    safeMessage: sanitized.message,
    quote,
  });
}

/**
 * True ONLY when the gateway specifically reports that a shop has not declared a bank account yet
 * (`404 NOT_FOUND` with bank-account detail), so `/api/admin/shops/[id]/bank-account` can return
 * `200 { bank_account: null }` without masking other 404/auth/upstream errors.
 */
export function isShopBankAccountNotFoundError(err: unknown): boolean {
  return (
    err instanceof GatewayError &&
    err.kind === "not_found" &&
    err.code === "NOT_FOUND" &&
    err.detail.toLowerCase().includes("bank account")
  );
}

/** Structured JSON log line with RFC3339 timestamp, trace_id, and PII/secret redaction. */
export function logServer(level: "info" | "warn" | "error", msg: string, fields: object = {}) {
  const safeFields = redactLogFields(fields as Record<string, unknown>);
  const line = JSON.stringify({
    time: new Date().toISOString(),
    level,
    msg: scrubLogString(msg),
    service: "web-bff",
    trace_id: typeof safeFields.trace_id === "string" ? safeFields.trace_id : "",
    ...safeFields,
  });
  if (level === "error") console.error(line);
  else console.log(line);
}

/**
 * Convert any thrown value into a safe JSON error response.
 * `messages` overrides the Vietnamese text per `code` (or per kind), e.g.
 * `{ FAILED_PRECONDITION: "Sản phẩm vừa hết hàng." }`.
 */
export function errorResponse(
  err: unknown,
  messages: Record<string, string> = {},
  fallbackTraceId = ""
): Response {
  const e =
    err instanceof GatewayError
      ? err
      : new GatewayError("unavailable", "UNAVAILABLE", String(err), fallbackTraceId);
  const traceId = e.traceId || fallbackTraceId;
  if (!(err instanceof GatewayError)) {
    logServer("error", "bff_unhandled_error", {
      trace_id: traceId,
      error: err instanceof Error ? err.name : typeof err,
    });
  } else if (e.kind === "unavailable") {
    logServer("warn", "bff_gateway_unavailable", { trace_id: traceId });
  }
  const message = messages[e.code] ?? messages[e.kind] ?? e.safeMessage ?? SAFE_MESSAGES[e.kind];
  const headers: Record<string, string> = { "Cache-Control": "no-store" };
  if (e.retryAfter && (e.status === 429 || e.status === 503)) {
    headers["Retry-After"] = e.retryAfter;
  }
  const payload: Record<string, unknown> = { error: { code: e.code, message } };
  if (e.status === 409 && e.code === "ADDRESS_FEE_CHANGED" && e.quote) {
    payload.quote = e.quote;
  }
  return Response.json(payload, { status: e.status, headers });
}
