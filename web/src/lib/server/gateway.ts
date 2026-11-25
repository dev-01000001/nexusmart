// Server-side client for the NexusMart REST gateway. Import only from server components,
// route handlers and server actions.
import { cookies, headers } from "next/headers";
import {
  ACCESS_COOKIE,
  GATEWAY_TIMEOUT_MS,
  GATEWAY_URL,
  REFRESH_COOKIE,
  baseCookie,
} from "./config";
import { GatewayError, errorFromResponse } from "./errors";
import { resolveForwardedClientIp } from "./http";
import { clearAuthCookies, rotateRefreshToken, setAuthCookies } from "./tokens";
import { resolveTraceContext, type TraceContext } from "./trace";

type QueryValue = string | number | boolean | null | undefined;

export interface GwOptions {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  /** Appended as a query string; null/undefined/"" are skipped, arrays repeat the key. */
  query?: Record<string, QueryValue | QueryValue[]>;
  /** JSON-encoded request body. */
  body?: unknown;
  /**
   * "user" (default): attach the access token from the session cookie when present; on 401
   * rotate the refresh token once and retry (only where cookies are writable: route handlers and
   * server actions – in server components the error is thrown instead; middleware refreshes
   * ahead of time so this is rare). "none": anonymous call.
   */
  auth?: "user" | "none";
  /** Sent as the `Idempotency-Key` header (checkout, payments). */
  idempotencyKey?: string;
  timeoutMs?: number;
  /**
   * Public, cacheable read: implies auth "none", reads no cookies/headers so the page can stay
   * static/ISR. Seconds, or false = cache forever until revalidateTag.
   */
  revalidate?: number | false;
  tags?: string[];
}

/**
 * Call the gateway and return the parsed JSON body (undefined for 204).
 * Throws GatewayError for every failure (HTTP error, timeout, network, bad JSON).
 */
export async function gw<T>(path: string, opts: GwOptions = {}): Promise<T> {
  const method = opts.method ?? "GET";
  const cacheable = opts.revalidate !== undefined;
  const reqHeaders: Record<string, string> = { Accept: "application/json" };
  if (opts.body !== undefined) reqHeaders["Content-Type"] = "application/json";
  if (opts.idempotencyKey) reqHeaders["Idempotency-Key"] = opts.idempotencyKey;

  let store: Awaited<ReturnType<typeof cookies>> | undefined;
  let access: string | undefined;
  let clientIp: string | null = null;
  let trace: TraceContext;
  if (!cacheable) {
    // Forward the browser IP only when proxy headers are explicitly trusted so untrusted
    // callers cannot smuggle spoofed X-Forwarded-For/X-Real-IP headers through the BFF.
    const h = await headers();
    clientIp = resolveForwardedClientIp(h);
    if (clientIp) reqHeaders["X-Forwarded-For"] = clientIp;
    trace = resolveTraceContext(h);
    reqHeaders["traceparent"] = trace.traceparent;
    if (trace.tracestate) reqHeaders["tracestate"] = trace.tracestate;
    if (opts.auth !== "none") {
      store = await cookies();
      access = store.get(ACCESS_COOKIE)?.value || undefined;
    }
  } else {
    trace = resolveTraceContext(null);
    reqHeaders["traceparent"] = trace.traceparent;
  }

  const url = buildUrl(path, opts.query);
  const send = (token?: string) =>
    doFetch(
      url,
      {
        method,
        headers: token ? { ...reqHeaders, Authorization: `Bearer ${token}` } : reqHeaders,
        body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
        timeoutMs: opts.timeoutMs ?? GATEWAY_TIMEOUT_MS,
        retry: method === "GET",
        cacheable,
        revalidate: opts.revalidate,
        tags: opts.tags,
        traceId: trace.traceId,
      },
      0
    );

  let res = await send(access);
  if (res.status === 401 && store) {
    const fresh = await refreshInPlace(store, trace, clientIp);
    if (fresh) res = await send(fresh);
  }
  if (!res.ok) throw await errorFromResponse(res, trace.traceId);
  if (res.status === 204) return undefined as T;
  try {
    return (await res.json()) as T;
  } catch {
    throw new GatewayError("unavailable", undefined, "", trace.traceId);
  }
}

export interface GwRawOptions {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  query?: Record<string, QueryValue | QueryValue[]>;
  /** JSON-encoded request body (ignored if `rawBody` is provided). */
  body?: unknown;
  /** Raw request body (e.g. Uint8Array / FormData for multipart CSV imports). */
  rawBody?: BodyInit;
  /** Explicit Content-Type header for `rawBody`. */
  contentType?: string;
  auth?: "user" | "none";
  accept?: string;
  idempotencyKey?: string;
  timeoutMs?: number;
  signal?: AbortSignal;
}

/**
 * Call the gateway and return the raw 2xx `Response` (used by SSE streaming proxies,
 * HTML/PDF shipping labels, CSV exports, and multipart uploads).
 * Attaches the user's session cookie JWT when present, rotates once on 401, and throws
 * `GatewayError` on non-2xx or transport errors so callers never leak upstream internals.
 */
export async function gwRaw(path: string, opts: GwRawOptions = {}): Promise<Response> {
  const method = opts.method ?? "GET";
  const reqHeaders: Record<string, string> = {
    Accept: opts.accept ?? "text/event-stream",
  };
  if (opts.contentType) {
    reqHeaders["Content-Type"] = opts.contentType;
  } else if (opts.rawBody === undefined && opts.body !== undefined) {
    reqHeaders["Content-Type"] = "application/json";
  }
  if (opts.idempotencyKey) reqHeaders["Idempotency-Key"] = opts.idempotencyKey;

  const h = await headers();
  const clientIp = resolveForwardedClientIp(h);
  if (clientIp) reqHeaders["X-Forwarded-For"] = clientIp;
  const trace = resolveTraceContext(h);
  reqHeaders["traceparent"] = trace.traceparent;
  if (trace.tracestate) reqHeaders["tracestate"] = trace.tracestate;

  let store: Awaited<ReturnType<typeof cookies>> | undefined;
  let access: string | undefined;
  if (opts.auth !== "none") {
    store = await cookies();
    access = store.get(ACCESS_COOKIE)?.value || undefined;
  }

  const url = buildUrl(path, opts.query);
  const timeoutMs = opts.timeoutMs ?? 60_000;
  const payload: BodyInit | undefined =
    opts.rawBody !== undefined
      ? opts.rawBody
      : opts.body === undefined
        ? undefined
        : JSON.stringify(opts.body);

  const send = async (token?: string): Promise<Response> => {
    const signals: AbortSignal[] = [AbortSignal.timeout(timeoutMs)];
    if (opts.signal) signals.push(opts.signal);
    const combinedSignal =
      signals.length > 1 && typeof AbortSignal.any === "function"
        ? AbortSignal.any(signals)
        : (opts.signal ?? signals[0]);
    try {
      return await fetch(url, {
        method,
        headers: token ? { ...reqHeaders, Authorization: `Bearer ${token}` } : reqHeaders,
        body: payload,
        cache: "no-store",
        signal: combinedSignal,
      });
    } catch {
      throw new GatewayError("unavailable", undefined, "", trace.traceId);
    }
  };

  let res = await send(access);
  if (res.status === 401 && store) {
    const fresh = await refreshInPlace(store, trace, clientIp);
    if (fresh) res = await send(fresh);
  }
  if (!res.ok) throw await errorFromResponse(res, trace.traceId);
  return res;
}

function buildUrl(path: string, query?: GwOptions["query"]): string {
  const u = new URL(path.startsWith("/") ? path : `/${path}`, `${GATEWAY_URL}/`);
  for (const [k, v] of Object.entries(query ?? {})) {
    for (const item of Array.isArray(v) ? v : [v]) {
      if (item !== undefined && item !== null && item !== "")
        u.searchParams.append(k, String(item));
    }
  }
  return `${GATEWAY_URL}${u.pathname}${u.search}`;
}

interface FetchInit {
  method: string;
  headers: Record<string, string>;
  body?: string;
  timeoutMs: number;
  retry: boolean;
  cacheable: boolean;
  revalidate?: number | false;
  tags?: string[];
  traceId: string;
}

async function doFetch(url: string, init: FetchInit, attempt = 0): Promise<Response> {
  try {
    const res = await fetch(url, {
      method: init.method,
      headers: init.headers,
      body: init.body,
      signal: AbortSignal.timeout(init.timeoutMs),
      ...(init.cacheable
        ? { next: { revalidate: init.revalidate, tags: init.tags } }
        : { cache: "no-store" as const }),
    });
    // One retry with backoff for idempotent reads on transient upstream failures.
    if (init.retry && attempt === 0 && [502, 503, 504].includes(res.status)) {
      await sleep(200);
      return doFetch(url, init, 1);
    }
    return res;
  } catch {
    if (init.retry && attempt === 0) {
      await sleep(200);
      return doFetch(url, init, 1);
    }
    throw new GatewayError("unavailable", undefined, "", init.traceId);
  }
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * Rotate the session after a 401. Returns the new access token, or null if the session is dead
 * or cookies are read-only here (server component render) – rotating without persisting the new
 * refresh token would get the whole token family revoked on the next request.
 */
async function refreshInPlace(
  store: Awaited<ReturnType<typeof cookies>>,
  trace?: Pick<TraceContext, "traceparent" | "tracestate">,
  clientIp?: string | null
): Promise<string | null> {
  const rt = store.get(REFRESH_COOKIE)?.value;
  if (!rt) return null;
  try {
    store.set(ACCESS_COOKIE, "", { ...baseCookie, maxAge: 0 }); // throws when read-only
  } catch {
    return null;
  }
  const pair = await rotateRefreshToken(rt, trace, { clientIp });
  if (!pair) {
    clearAuthCookies(store);
    return null;
  }
  setAuthCookies(store, pair);
  return pair.accessToken;
}
