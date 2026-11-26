// Browser-side helper for calling this app's explicit /api/* route handlers (the BFF).
// Same-origin fetch: cookies ride along automatically and the browser sets `Origin` on writes.
import type { GatewayErrorEnvelope, GatewaySKU } from "@/lib/gateway-types";
import { type AddressFeeChangedQuote, extractSafeAddressFeeQuote } from "./a8-types.ts";

export const SESSION_EXPIRED_EVENT = "nexusmart:session-expired";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly quote?: AddressFeeChangedQuote;

  constructor(status: number, code: string, message: string, quote?: AddressFeeChangedQuote) {
    super(message); // already a safe Vietnamese message from the BFF
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    if (quote) {
      this.quote = quote;
    }
  }
}

export interface ApiOptions {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  /** JSON-serializable payload or raw BodyInit (FormData/Blob/string/Uint8Array). */
  body?: unknown;
  /** Alias for JSON payload (for compatibility with shared helpers). */
  json?: unknown;
  headers?: HeadersInit;
  signal?: AbortSignal;
  /** Retry once via single-flight `/api/auth/refresh` on 401 (default true except `/api/auth/*`). */
  retryOn401?: boolean;
}

type RefreshOutcome =
  { status: "refreshed" } | { status: "expired" } | { status: "transient"; error: ApiError };

let inFlightRefresh: Promise<RefreshOutcome> | null = null;
let refreshGeneration = 0;

/**
 * Single-flight refresh inside the browser tab so parallel 401s never replay the
 * same rotating refresh token and trigger family revocation. Distinguishes dead sessions
 * (`401`/`403`) from transient failures (`429`/`5xx`/network) so outages never log users out.
 */
function runSingleFlightRefresh(): Promise<RefreshOutcome> {
  if (inFlightRefresh) {
    return inFlightRefresh;
  }

  inFlightRefresh = (async (): Promise<RefreshOutcome> => {
    let res: Response;
    try {
      res = await fetch("/api/auth/refresh", {
        method: "POST",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
        cache: "no-store",
      });
    } catch {
      return {
        status: "transient",
        error: new ApiError(0, "NETWORK", "Không kết nối được. Bạn kiểm tra mạng và thử lại nhé."),
      };
    }
    if (res.ok) {
      refreshGeneration += 1;
      return { status: "refreshed" };
    }
    if (res.status === 401 || res.status === 403) {
      return { status: "expired" };
    }
    return {
      status: "transient",
      error: await parseErrorResponse(res),
    };
  })().finally(() => {
    inFlightRefresh = null;
  });

  return inFlightRefresh;
}

export function refreshSessionOnce(): Promise<boolean> {
  return runSingleFlightRefresh().then((outcome) => outcome.status === "refreshed");
}

function notifySessionExpired() {
  if (typeof window !== "undefined") {
    window.dispatchEvent(new Event(SESSION_EXPIRED_EVENT));
  }
}

function isBodyInit(val: unknown): val is BodyInit {
  if (typeof val === "string") return true;
  if (typeof FormData !== "undefined" && val instanceof FormData) return true;
  if (typeof Blob !== "undefined" && val instanceof Blob) return true;
  if (typeof URLSearchParams !== "undefined" && val instanceof URLSearchParams) return true;
  if (val instanceof ArrayBuffer || ArrayBuffer.isView(val)) return true;
  return false;
}

function buildFetchInit(opts: ApiOptions, defaultAccept = "application/json"): RequestInit {
  const headers = new Headers(opts.headers);
  if (!headers.has("Accept")) {
    headers.set("Accept", defaultAccept);
  }

  let encodedBody: BodyInit | undefined;
  const payload = opts.json !== undefined ? opts.json : opts.body;
  if (payload !== undefined && payload !== null) {
    if (isBodyInit(payload)) {
      encodedBody = payload;
    } else {
      encodedBody = JSON.stringify(payload);
      if (!headers.has("Content-Type")) {
        headers.set("Content-Type", "application/json");
      }
    }
  }

  return {
    method: opts.method ?? "GET",
    headers,
    body: encodedBody,
    credentials: "same-origin",
    cache: "no-store",
    signal: opts.signal,
  };
}

async function parseErrorResponse(res: Response): Promise<ApiError> {
  let code = res.status >= 500 ? "UNAVAILABLE" : "BAD_REQUEST";
  let message =
    res.status >= 500
      ? "Hệ thống đang bận. Bạn vui lòng thử lại sau."
      : "Yêu cầu chưa thể xử lý, vui lòng thử lại.";
  let quote: AddressFeeChangedQuote | undefined;

  try {
    const data = (await res.json()) as Partial<GatewayErrorEnvelope> | null;
    if (data?.error?.code) {
      code = data.error.code;
    }
    if (data?.error?.message) {
      message = data.error.message;
    }
    if (res.status === 409 && code === "ADDRESS_FEE_CHANGED") {
      quote = extractSafeAddressFeeQuote(data) ?? undefined;
    }
  } catch {
    // Keep generic fallback message
  }

  return new ApiError(res.status, code, message, quote);
}

function shouldRetry401(path: string, opts: ApiOptions): boolean {
  if (opts.retryOn401 !== undefined) return opts.retryOn401;
  const pathname = path.split("?")[0] ?? path;
  if (!pathname.startsWith("/api/auth/")) return true;
  return (
    pathname === "/api/auth/addresses" ||
    pathname.startsWith("/api/auth/addresses/") ||
    pathname === "/api/auth/me"
  );
}

async function fetchWithAuthRetry(
  path: string,
  opts: ApiOptions,
  defaultAccept: string
): Promise<Response> {
  const reqInit = buildFetchInit(opts, defaultAccept);
  const genBefore = refreshGeneration;

  let res: Response;
  try {
    res = await fetch(path, reqInit);
  } catch {
    throw new ApiError(0, "NETWORK", "Không kết nối được. Bạn kiểm tra mạng và thử lại nhé.");
  }

  if (res.status === 401 && shouldRetry401(path, opts)) {
    const outcome: RefreshOutcome =
      refreshGeneration > genBefore ? { status: "refreshed" } : await runSingleFlightRefresh();

    if (outcome.status === "transient") {
      throw outcome.error;
    }

    if (outcome.status === "refreshed") {
      try {
        res = await fetch(path, reqInit);
      } catch {
        throw new ApiError(0, "NETWORK", "Không kết nối được. Bạn kiểm tra mạng và thử lại nhé.");
      }
      if (res.status === 401) {
        notifySessionExpired();
      }
    } else {
      // Refresh returned 401/403 -> session is genuinely dead.
      notifySessionExpired();
    }
  }

  if (!res.ok) {
    throw await parseErrorResponse(res);
  }

  return res;
}

/**
 * Call an explicit same-origin BFF endpoint (e.g. `/api/cart`, `/api/orders/...`).
 * Throws ApiError (status 0 = network failure). Returns undefined for 204.
 * On 401 (authenticated routes), triggers single-flight `/api/auth/refresh` and retries once.
 */
export async function api<T>(path: string, opts: ApiOptions = {}): Promise<T> {
  const res = await fetchWithAuthRetry(path, opts, "application/json");
  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

/**
 * Downloads a binary/CSV/PDF response from an explicit BFF endpoint with single-flight 401 retry.
 */
export async function apiBlob(path: string, opts: ApiOptions = {}): Promise<Blob> {
  const res = await fetchWithAuthRetry(path, opts, "*/*");
  return res.blob();
}

/**
 * Calls same-origin `/api/auth/<endpoint>` routes.
 */
export async function authApi<T>(
  endpoint: string,
  options?: { method?: "GET" | "POST"; json?: unknown; body?: unknown }
): Promise<T> {
  const cleanEndpoint = endpoint.replace(/^\/+/, "");
  return api<T>(`/api/auth/${cleanEndpoint}`, {
    method: options?.method ?? "POST",
    body: options?.json !== undefined ? options.json : options?.body,
    retryOn401: false,
  });
}

export {
  getDefaultPathForRole,
  resolvePostLoginRedirect,
  sanitizeNextPath,
  sanitizeNextPath as sanitizeClientNextPath,
} from "./redirect.ts";

export function getErrorMessage(
  err: unknown,
  fallback = "Có lỗi xảy ra, vui lòng thử lại sau ít phút."
): string {
  if (err instanceof ApiError && err.message) {
    if (err.code === "SHOP_SUSPENDED" && /\bSHOP_SUSPENDED\b/i.test(err.message)) {
      return "Gian hàng hiện đang tạm ngưng hoạt động nên không thể tiếp tục thao tác.";
    }
    return err.message;
  }
  return fallback;
}

function normalizeSkuAttrText(input?: string | null): string {
  return (input ?? "")
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[đĐ]/g, "d")
    .toLowerCase()
    .replace(/\s+/g, " ")
    .trim();
}

function normalizeSkuAttrSlug(input?: string | null): string {
  return normalizeSkuAttrText(input).replace(/[^a-z0-9]+/g, "");
}

export type GatewaySkuMatchCandidate = Pick<
  GatewaySKU,
  "id" | "sku_code" | "available_stock" | "is_active" | "attributes"
>;

export interface GatewaySkuMatchQuery {
  skuCode?: string;
  colorCode?: string;
  colorName?: string;
  size?: string;
}

/**
 * Pure SKU resolver: matches by exact `sku_code`, or by (`attributes.color_code` OR accent-folded
 * `attributes.color`) + `attributes.size`. Never substitutes a different color or size when
 * the caller explicitly specified them.
 */
export function matchGatewaySku(
  backendSkus: readonly GatewaySkuMatchCandidate[],
  match: GatewaySkuMatchQuery
): string | null {
  const isSellable = (s: GatewaySkuMatchCandidate) =>
    s.is_active !== false && s.available_stock > 0;

  const cleanSkuCode = (match.skuCode ?? "").trim();
  if (cleanSkuCode) {
    const bySkuCode = backendSkus.find((s) => s.sku_code === cleanSkuCode);
    if (bySkuCode) {
      return isSellable(bySkuCode) ? bySkuCode.id : null;
    }
  }

  const rawColorCode = (match.colorCode ?? "").trim();
  const rawColorName = (match.colorName ?? "").trim();
  const reqColorCode =
    rawColorCode && normalizeSkuAttrSlug(rawColorCode) !== "macdinh"
      ? normalizeSkuAttrText(rawColorCode)
      : "";
  const reqColorCodeSlug = reqColorCode ? normalizeSkuAttrSlug(reqColorCode) : "";
  const reqColorName =
    rawColorName && normalizeSkuAttrText(rawColorName) !== "mac dinh"
      ? normalizeSkuAttrText(rawColorName)
      : "";
  const reqColorNameSlug = reqColorName ? normalizeSkuAttrSlug(reqColorName) : "";
  const hasColor = Boolean(reqColorCode || reqColorName);

  const reqSizeLower = (match.size ?? "").trim().toLowerCase();
  const hasSize = Boolean(reqSizeLower);

  const matchesColor = (s: GatewaySkuMatchCandidate): boolean => {
    const skuColorCode = normalizeSkuAttrText(s.attributes?.color_code);
    const skuColorCodeSlug = normalizeSkuAttrSlug(s.attributes?.color_code);
    const skuColorName = normalizeSkuAttrText(s.attributes?.color);
    const skuColorNameSlug = normalizeSkuAttrSlug(s.attributes?.color);

    if (
      reqColorCode &&
      skuColorCode &&
      (skuColorCode === reqColorCode || skuColorCodeSlug === reqColorCodeSlug)
    ) {
      return true;
    }
    if (reqColorName && skuColorName && skuColorName === reqColorName) {
      return true;
    }
    if (reqColorCodeSlug && skuColorNameSlug && skuColorNameSlug === reqColorCodeSlug) {
      return true;
    }
    if (reqColorNameSlug && skuColorCodeSlug && skuColorCodeSlug === reqColorNameSlug) {
      return true;
    }
    return false;
  };

  const matchesSize = (s: GatewaySkuMatchCandidate): boolean =>
    (s.attributes?.size ?? "").trim().toLowerCase() === reqSizeLower;

  if (hasColor && hasSize) {
    const exact = backendSkus.find((s) => matchesColor(s) && matchesSize(s) && isSellable(s));
    return exact ? exact.id : null;
  }
  if (hasColor) {
    const byColor = backendSkus.find((s) => matchesColor(s) && isSellable(s));
    return byColor ? byColor.id : null;
  }
  if (hasSize) {
    const bySize = backendSkus.find((s) => matchesSize(s) && isSellable(s));
    return bySize ? bySize.id : null;
  }
  const fallback = backendSkus.find(isSellable);
  return fallback ? fallback.id : null;
}
