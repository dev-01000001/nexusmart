// Error model shared by every BFF route. Gateway internals (messages, trace ids) stay on the
// server; the browser only ever sees { error: { code, message } } with a Vietnamese message.

export type GatewayErrorKind =
  | "unauthenticated" // 401 – no session / session expired
  | "forbidden" // 403 – logged in but not allowed (also bad Origin)
  | "not_found" // 404
  | "conflict" // 409 – business rule (out of stock, wrong state, already exists…)
  | "invalid" // 400 – input rejected
  | "rate_limited" // 429
  | "unavailable"; // 503 – gateway down, timeout, 5xx, unexpected payload

const STATUS: Record<GatewayErrorKind, number> = {
  unauthenticated: 401,
  forbidden: 403,
  not_found: 404,
  conflict: 409,
  invalid: 400,
  rate_limited: 429,
  unavailable: 503,
};

export const SAFE_MESSAGES: Record<GatewayErrorKind, string> = {
  unauthenticated: "Phiên đăng nhập đã hết hạn. Bạn vui lòng đăng nhập lại.",
  forbidden: "Bạn không có quyền thực hiện thao tác này.",
  not_found: "Không tìm thấy nội dung bạn yêu cầu.",
  conflict: "Thao tác chưa thực hiện được. Bạn vui lòng tải lại trang và thử lại.",
  invalid: "Thông tin chưa hợp lệ. Bạn vui lòng kiểm tra lại.",
  rate_limited: "Bạn thao tác hơi nhanh. Vui lòng thử lại sau ít phút.",
  unavailable: "Hệ thống đang bận. Bạn vui lòng thử lại sau.",
};

export class GatewayError extends Error {
  readonly kind: GatewayErrorKind;
  /** HTTP status the BFF returns to the browser. */
  readonly status: number;
  /** Canonical code, safe for the client (e.g. FAILED_PRECONDITION, MISSING_IDEMPOTENCY_KEY). */
  readonly code: string;
  /** Raw gateway message. SERVER-ONLY: use it to pick a friendly message, never send it. */
  readonly detail: string;
  readonly traceId: string;

  constructor(kind: GatewayErrorKind, code?: string, detail = "", traceId = "") {
    super(`${kind}:${code ?? kind}`);
    this.name = "GatewayError";
    this.kind = kind;
    this.status = STATUS[kind];
    this.code = code || kind.toUpperCase();
    this.detail = detail;
    this.traceId = traceId;
  }
}

export function kindFromStatus(status: number): GatewayErrorKind {
  if (status === 401) return "unauthenticated";
  if (status === 403) return "forbidden";
  if (status === 404) return "not_found";
  if (status === 409 || status === 412 || status === 422) return "conflict";
  if (status === 400) return "invalid";
  if (status === 429) return "rate_limited";
  return "unavailable";
}

/** Build a GatewayError from a non-2xx gateway response (reads the standard error envelope). */
export async function errorFromResponse(res: Response): Promise<GatewayError> {
  const kind = kindFromStatus(res.status);
  let code = "";
  let detail = "";
  let traceId = "";
  try {
    const body = (await res.json()) as { error?: { code?: string; message?: string; trace_id?: string } };
    code = body.error?.code ?? "";
    detail = body.error?.message ?? "";
    traceId = body.error?.trace_id ?? "";
  } catch {
    // non-JSON body (proxy error page etc.)
  }
  // Internal failures never expose the backend code.
  if (kind === "unavailable") code = "UNAVAILABLE";
  return new GatewayError(kind, code || undefined, detail, traceId);
}

/** Structured JSON log line without PII/secrets. */
export function logServer(level: "info" | "warn" | "error", msg: string, fields: object = {}) {
  const line = JSON.stringify({ level, msg, service: "web-bff", ...fields });
  if (level === "error") console.error(line);
  else console.log(line);
}

/**
 * Convert any thrown value into a safe JSON error response.
 * `messages` overrides the Vietnamese text per `code` (or per kind), e.g.
 * `{ FAILED_PRECONDITION: "Sản phẩm vừa hết hàng." }`.
 */
export function errorResponse(err: unknown, messages: Record<string, string> = {}): Response {
  const e =
    err instanceof GatewayError ? err : new GatewayError("unavailable", "UNAVAILABLE", String(err));
  if (!(err instanceof GatewayError)) {
    logServer("error", "bff_unhandled_error", {
      error: err instanceof Error ? err.name : typeof err,
    });
  } else if (e.kind === "unavailable") {
    logServer("warn", "bff_gateway_unavailable", { trace_id: e.traceId });
  }
  const message = messages[e.code] ?? messages[e.kind] ?? SAFE_MESSAGES[e.kind];
  return Response.json(
    { error: { code: e.code, message } },
    { status: e.status, headers: { "Cache-Control": "no-store" } }
  );
}
