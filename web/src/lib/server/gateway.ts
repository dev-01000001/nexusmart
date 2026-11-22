import { cookies, headers as nextHeaders } from "next/headers";
import { NextResponse } from "next/server";
import type { GatewayErrorEnvelope } from "@/lib/gateway-types";
import {
  ACCESS_COOKIE,
  applyClientIpHeader,
  extractClientIp,
  getGatewayUrl,
} from "@/lib/server/session";

const DEFAULT_TIMEOUT_MS = 10_000;
const UNAVAILABLE_MESSAGE = "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút.";
const CODE_PATTERN = /^[A-Z0-9_]{2,64}$/;

export class GatewayError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "GatewayError";
    this.status = status;
    this.code = code;
  }
}

function defaultCodeForStatus(status: number): string {
  switch (status) {
    case 400:
      return "INVALID_ARGUMENT";
    case 401:
      return "UNAUTHENTICATED";
    case 403:
      return "PERMISSION_DENIED";
    case 404:
      return "NOT_FOUND";
    case 409:
      return "CONFLICT";
    case 429:
      return "RESOURCE_EXHAUSTED";
    default:
      return status >= 500 ? "UNAVAILABLE" : "BAD_REQUEST";
  }
}

function toSafeVietnameseMessage(status: number, code: string, rawMessage: string): string {
  const lower = rawMessage.toLowerCase();

  // Auth & OTP domain mappings (services/user)
  if (lower.includes("invalid email or password")) {
    return "Email hoặc mật khẩu chưa đúng.";
  }
  if (lower.includes("too many failed login")) {
    return "Tài khoản tạm thời bị khóa do đăng nhập sai nhiều lần. Vui lòng thử lại sau ít phút.";
  }
  if (lower.includes("account is locked")) {
    return "Tài khoản đang bị khóa. Vui lòng liên hệ bộ phận hỗ trợ.";
  }
  if (lower.includes("account pending verification")) {
    return "Tài khoản chưa xác thực email. Vui lòng nhập mã OTP đã gửi tới hộp thư của bạn.";
  }
  if (lower.includes("account is not pending verification")) {
    return "Tài khoản này đã được xác thực trước đó.";
  }
  if (lower.includes("account is not active")) {
    return "Tài khoản hiện chưa hoạt động.";
  }
  if (lower.includes("email is already registered")) {
    return "Địa chỉ email này đã được đăng ký. Vui lòng đăng nhập hoặc dùng email khác.";
  }
  if (lower.includes("phone is already registered")) {
    return "Số điện thoại này đã được sử dụng cho tài khoản khác.";
  }
  if (lower.includes("email or phone already registered")) {
    return "Email hoặc số điện thoại này đã được đăng ký.";
  }
  if (lower.includes("otp not found") || lower.includes("otp expired")) {
    return "Mã xác thực đã hết hạn hoặc không tồn tại. Vui lòng yêu cầu gửi lại mã mới.";
  }
  if (lower.includes("too many invalid otp")) {
    return "Bạn đã nhập sai mã xác thực quá nhiều lần. Vui lòng yêu cầu gửi lại mã mới.";
  }
  if (lower.includes("invalid otp")) {
    return "Mã xác thực chưa chính xác. Vui lòng kiểm tra lại hộp thư.";
  }
  if (lower.includes("otp resend limit")) {
    return "Bạn đã gửi lại mã quá nhiều lần. Vui lòng đợi ít phút rồi thử lại.";
  }
  if (lower.includes("password must be at least")) {
    return "Mật khẩu cần có tối thiểu 8 ký tự.";
  }
  if (lower.includes("valid email is required")) {
    return "Địa chỉ email chưa đúng định dạng.";
  }
  if (lower.includes("full_name is required")) {
    return "Vui lòng nhập họ và tên đầy đủ.";
  }
  if (lower.includes("refresh token reuse")) {
    return "Phiên đăng nhập đã bị thu hồi để bảo vệ tài khoản. Vui lòng đăng nhập lại.";
  }
  if (lower.includes("refresh token expired") || lower.includes("invalid refresh token")) {
    return "Phiên đăng nhập đã hết hạn. Vui lòng đăng nhập lại.";
  }

  // Commerce domain mappings
  if (
    code === "INSUFFICIENT_STOCK" ||
    lower.includes("insufficient stock") ||
    lower.includes("out of stock")
  ) {
    return "Sản phẩm không đủ số lượng tồn kho.";
  }
  if (lower.includes("voucher")) {
    return "Mã ưu đãi không hợp lệ hoặc đã hết lượt sử dụng.";
  }

  // Generic safe fallbacks by status/code — never echo raw internal gateway messages
  switch (code) {
    case "UNAUTHENTICATED":
      return "Vui lòng đăng nhập để tiếp tục.";
    case "PERMISSION_DENIED":
      return "Bạn không có quyền thực hiện thao tác này.";
    case "NOT_FOUND":
      return "Không tìm thấy thông tin yêu cầu.";
    case "ALREADY_EXISTS":
      return "Thông tin đã tồn tại trong hệ thống.";
    case "FAILED_PRECONDITION":
      return "Không thể thực hiện thao tác ở trạng thái hiện tại.";
    case "ABORTED":
      return "Dữ liệu vừa thay đổi, vui lòng tải lại và thử lại.";
    case "RESOURCE_EXHAUSTED":
      return "Bạn thao tác quá nhanh, vui lòng thử lại sau ít phút.";
    case "INVALID_ARGUMENT":
    case "INVALID_JSON":
      return "Thông tin chưa hợp lệ, vui lòng kiểm tra lại.";
    default:
      if (status === 401) return "Vui lòng đăng nhập để tiếp tục.";
      if (status === 403) return "Bạn không có quyền thực hiện thao tác này.";
      if (status === 404) return "Không tìm thấy thông tin yêu cầu.";
      if (status === 409) return "Yêu cầu bị xung đột với trạng thái hiện tại.";
      if (status === 429) return "Bạn thao tác quá nhanh, vui lòng thử lại sau ít phút.";
      return "Yêu cầu chưa thể xử lý, vui lòng kiểm tra lại thông tin.";
  }
}

export function sanitizeGatewayError(
  status: number,
  body: unknown
): { status: number; code: string; message: string } {
  if (status >= 500 || status <= 0) {
    return { status: 503, code: "UNAVAILABLE", message: UNAVAILABLE_MESSAGE };
  }

  const envelope = body as Partial<GatewayErrorEnvelope> | null;
  const rawCode = typeof envelope?.error?.code === "string" ? envelope.error.code.trim() : "";
  const code = CODE_PATTERN.test(rawCode) ? rawCode : defaultCodeForStatus(status);
  const rawMessage = typeof envelope?.error?.message === "string" ? envelope.error.message : "";

  return {
    status,
    code,
    message: toSafeVietnameseMessage(status, code, rawMessage),
  };
}

export function gatewayErrorResponse(err: unknown): NextResponse {
  if (err instanceof GatewayError) {
    return NextResponse.json(
      { error: { code: err.code, message: err.message } },
      { status: err.status }
    );
  }
  return NextResponse.json(
    { error: { code: "UNAVAILABLE", message: UNAVAILABLE_MESSAGE } },
    { status: 503 }
  );
}

export interface GatewayFetchOptions {
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
  /**
   * Explicit access token override. Pass `false` to omit Authorization header
   * even when a session cookie exists (e.g. public auth endpoints).
   * Leave `undefined` to read `nm_access_token` from cookies automatically.
   */
  token?: string | false;
  clientIp?: string;
  timeoutMs?: number;
  cache?: RequestCache;
  next?: NextFetchRequestConfig;
}

/**
 * Server-side client for calling gateway `/api/v1/...`.
 * Attaches `Authorization: Bearer <access>` from cookie by default, forwards client IP
 * via `X-Forwarded-For`, enforces a timeout, and throws sanitized `GatewayError`.
 */
export async function gatewayFetch<T>(path: string, options: GatewayFetchOptions = {}): Promise<T> {
  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  const url = `${getGatewayUrl()}/api/v1${normalizedPath}`;

  const headers = new Headers(options.headers);
  if (!headers.has("Accept")) {
    headers.set("Accept", "application/json");
  }

  if (!headers.has("X-Forwarded-For")) {
    let clientIp = options.clientIp;
    if (!clientIp) {
      try {
        clientIp = extractClientIp(await nextHeaders());
      } catch {
        // Called outside request scope — omit X-Forwarded-For.
      }
    }
    applyClientIpHeader(headers, clientIp);
  }

  let accessToken: string | undefined;
  if (options.token === false) {
    accessToken = undefined;
  } else if (typeof options.token === "string") {
    accessToken = options.token;
  } else {
    const store = await cookies();
    accessToken = store.get(ACCESS_COOKIE)?.value;
  }

  if (accessToken) {
    headers.set("Authorization", `Bearer ${accessToken}`);
  }

  let body: BodyInit | undefined;
  if (options.body !== undefined && options.body !== null) {
    if (typeof options.body === "string" || options.body instanceof ArrayBuffer) {
      body = options.body;
    } else {
      body = JSON.stringify(options.body);
      if (!headers.has("Content-Type")) {
        headers.set("Content-Type", "application/json");
      }
    }
  }

  const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  let res: Response;
  try {
    res = await fetch(url, {
      method: options.method ?? "GET",
      headers,
      body,
      cache: options.cache ?? "no-store",
      next: options.next,
      signal: AbortSignal.timeout(timeoutMs),
    });
  } catch {
    throw new GatewayError(503, "UNAVAILABLE", UNAVAILABLE_MESSAGE);
  }

  const text = await res.text().catch(() => "");
  let parsed: unknown = null;
  if (text) {
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = null;
    }
  }

  if (!res.ok) {
    const sanitized = sanitizeGatewayError(res.status, parsed);
    throw new GatewayError(sanitized.status, sanitized.code, sanitized.message);
  }

  return parsed as T;
}
