import type { GatewayErrorEnvelope, GatewayProduct, GatewaySKU } from "@/lib/gateway-types";
import { type AddressFeeChangedQuote, extractSafeAddressFeeQuote } from "@/lib/a8-types";

export const SESSION_EXPIRED_EVENT = "nexusmart:session-expired";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly quote?: AddressFeeChangedQuote;

  constructor(status: number, code: string, message: string, quote?: AddressFeeChangedQuote) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    if (quote) {
      this.quote = quote;
    }
  }
}

export interface ApiRequestInit extends Omit<RequestInit, "body"> {
  body?: BodyInit | null;
  json?: unknown;
  retryOn401?: boolean;
}

let inFlightRefresh: Promise<boolean> | null = null;
let refreshGeneration = 0;

/**
 * Single-flight refresh inside the browser tab so parallel 401s never replay the
 * same rotating refresh token and trigger family revocation.
 */
export function refreshSessionOnce(): Promise<boolean> {
  if (inFlightRefresh) {
    return inFlightRefresh;
  }

  inFlightRefresh = (async () => {
    try {
      const res = await fetch("/api/auth/refresh", {
        method: "POST",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
      });
      if (res.ok) {
        refreshGeneration += 1;
        return true;
      }
      return false;
    } catch {
      return false;
    }
  })().finally(() => {
    inFlightRefresh = null;
  });

  return inFlightRefresh;
}

function notifySessionExpired() {
  if (typeof window !== "undefined") {
    window.dispatchEvent(new Event(SESSION_EXPIRED_EVENT));
  }
}

async function parseErrorResponse(res: Response): Promise<ApiError> {
  let code = res.status >= 500 ? "UNAVAILABLE" : "BAD_REQUEST";
  let message =
    res.status >= 500
      ? "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút."
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

function buildRequestInit(init?: ApiRequestInit): RequestInit {
  const headers = new Headers(init?.headers);
  if (!headers.has("Accept")) {
    headers.set("Accept", "application/json");
  }

  let body = init?.body;
  if (init?.json !== undefined) {
    body = JSON.stringify(init.json);
    if (!headers.has("Content-Type")) {
      headers.set("Content-Type", "application/json");
    }
  }

  return {
    ...init,
    headers,
    body,
    credentials: "same-origin",
  };
}

/**
 * Calls the same-origin BFF proxy `/api/gw/<path>`.
 * On 401, triggers a single-flight `/api/auth/refresh` and retries the request once.
 */
export async function api<T>(path: string, init?: ApiRequestInit): Promise<T> {
  const cleanPath = path.replace(/^\/+/, "");
  const url = `/api/gw/${cleanPath}`;
  const reqInit = buildRequestInit(init);
  const genBefore = refreshGeneration;

  let res: Response;
  try {
    res = await fetch(url, reqInit);
  } catch {
    throw new ApiError(503, "UNAVAILABLE", "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút.");
  }

  if (res.status === 401 && init?.retryOn401 !== false) {
    const refreshed = refreshGeneration > genBefore ? true : await refreshSessionOnce();
    if (refreshed) {
      try {
        res = await fetch(url, reqInit);
      } catch {
        throw new ApiError(
          503,
          "UNAVAILABLE",
          "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút."
        );
      }
    }
    if (res.status === 401) {
      notifySessionExpired();
    }
  }

  if (!res.ok) {
    throw await parseErrorResponse(res);
  }

  if (res.status === 204) {
    return undefined as T;
  }

  return (await res.json()) as T;
}

/**
 * Downloads a binary/CSV response from `/api/gw/<path>` with single-flight 401 refresh retry.
 */
export async function apiBlob(path: string, init?: ApiRequestInit): Promise<Blob> {
  const cleanPath = path.replace(/^\/+/, "");
  const url = `/api/gw/${cleanPath}`;
  const headers = new Headers(init?.headers);
  if (!headers.has("Accept")) {
    headers.set("Accept", "*/*");
  }
  const reqInit: RequestInit = {
    ...init,
    method: init?.method ?? "GET",
    headers,
    credentials: "same-origin",
  };
  const genBefore = refreshGeneration;

  let res: Response;
  try {
    res = await fetch(url, reqInit);
  } catch {
    throw new ApiError(503, "UNAVAILABLE", "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút.");
  }

  if (res.status === 401 && init?.retryOn401 !== false) {
    const refreshed = refreshGeneration > genBefore ? true : await refreshSessionOnce();
    if (refreshed) {
      try {
        res = await fetch(url, reqInit);
      } catch {
        throw new ApiError(
          503,
          "UNAVAILABLE",
          "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút."
        );
      }
    }
    if (res.status === 401) {
      notifySessionExpired();
    }
  }

  if (!res.ok) {
    throw await parseErrorResponse(res);
  }

  return res.blob();
}

/**
 * Calls same-origin `/api/auth/<endpoint>` routes.
 */
export async function authApi<T>(
  endpoint: string,
  options?: { method?: "GET" | "POST"; json?: unknown }
): Promise<T> {
  const cleanEndpoint = endpoint.replace(/^\/+/, "");
  const reqInit = buildRequestInit({
    method: options?.method ?? "POST",
    json: options?.json,
  });

  let res: Response;
  try {
    res = await fetch(`/api/auth/${cleanEndpoint}`, reqInit);
  } catch {
    throw new ApiError(503, "UNAVAILABLE", "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút.");
  }

  if (!res.ok) {
    throw await parseErrorResponse(res);
  }

  return (await res.json()) as T;
}

export {
  getDefaultPathForRole,
  resolvePostLoginRedirect,
  sanitizeNextPath,
  sanitizeNextPath as sanitizeClientNextPath,
} from "@/lib/redirect";

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
 * Pure SKU resolver used by `resolveGatewaySkuId`.
 * Matches by exact `sku_code`, or by (`attributes.color_code` OR accent-folded `attributes.color`) + `attributes.size`.
 * Never substitutes a different color or size when the caller explicitly specified them.
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

/**
 * Resolves a real backend SKU UUID for search/editorial product cards by looking up
 * the product slug via the same-origin BFF proxy.
 */
export async function resolveGatewaySkuId(
  slug: string,
  match: GatewaySkuMatchQuery
): Promise<string | null> {
  const fetched = await api<{ product?: GatewayProduct }>(
    `products/slug/${encodeURIComponent(slug)}`
  );
  const backendSkus = fetched?.product?.skus ?? [];
  return matchGatewaySku(backendSkus, match);
}
