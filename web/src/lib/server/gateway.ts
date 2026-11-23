import { cookies, headers as nextHeaders } from "next/headers";
import { NextResponse } from "next/server";
import { UNAVAILABLE_MESSAGE, sanitizeGatewayError } from "@/lib/server/error-map";
import {
  ACCESS_COOKIE,
  applyClientIpHeader,
  extractClientIp,
  getGatewayUrl,
} from "@/lib/server/session";

export { sanitizeGatewayError };

const DEFAULT_TIMEOUT_MS = 10_000;

export class GatewayError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryAfter?: string;

  constructor(status: number, code: string, message: string, retryAfter?: string) {
    super(message);
    this.name = "GatewayError";
    this.status = status;
    this.code = code;
    this.retryAfter = retryAfter;
  }
}

export function gatewayErrorResponse(err: unknown): NextResponse {
  if (err instanceof GatewayError) {
    const headers: Record<string, string> = {};
    if (err.status === 429 && err.retryAfter) {
      headers["Retry-After"] = err.retryAfter;
    }
    return NextResponse.json(
      { error: { code: err.code, message: err.message } },
      { status: err.status, headers }
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
    const retryAfter = res.headers.get("retry-after") ?? undefined;
    throw new GatewayError(sanitized.status, sanitized.code, sanitized.message, retryAfter);
  }

  return parsed as T;
}
