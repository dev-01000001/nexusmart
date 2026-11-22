import type { GatewayErrorEnvelope, GatewayProduct } from "@/lib/gateway-types";

export const SESSION_EXPIRED_EVENT = "nexusmart:session-expired";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
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

  try {
    const data = (await res.json()) as Partial<GatewayErrorEnvelope> | null;
    if (data?.error?.code) {
      code = data.error.code;
    }
    if (data?.error?.message) {
      message = data.error.message;
    }
  } catch {
    // Keep generic fallback message
  }

  return new ApiError(res.status, code, message);
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

export { sanitizeNextPath, sanitizeNextPath as sanitizeClientNextPath } from "@/lib/redirect";

export function getErrorMessage(
  err: unknown,
  fallback = "Có lỗi xảy ra, vui lòng thử lại sau ít phút."
): string {
  if (err instanceof ApiError && err.message) {
    return err.message;
  }
  return fallback;
}

/**
 * Resolves a real backend SKU UUID for static/mock editorial product cards by looking up
 * the product slug via the same-origin BFF proxy.
 */
export async function resolveGatewaySkuId(
  slug: string,
  match: { skuCode?: string; colorCode?: string; size?: string }
): Promise<string | null> {
  const fetched = await api<{ product?: GatewayProduct }>(
    `products/slug/${encodeURIComponent(slug)}`
  );
  const backendSkus = fetched?.product?.skus ?? [];
  const resolved =
    (match.skuCode ? backendSkus.find((s) => s.sku_code === match.skuCode) : undefined) ??
    (match.colorCode && match.size
      ? backendSkus.find(
          (s) => s.attributes?.color_code === match.colorCode && s.attributes?.size === match.size
        )
      : undefined) ??
    (match.size
      ? backendSkus.find((s) => s.attributes?.size === match.size && s.available_stock > 0)
      : undefined) ??
    backendSkus.find((s) => s.available_stock > 0);
  return resolved && resolved.available_stock > 0 ? resolved.id : null;
}
