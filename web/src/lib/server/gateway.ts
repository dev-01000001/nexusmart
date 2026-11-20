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
  if (!cacheable) {
    // Forward the browser IP only when proxy headers are explicitly trusted so untrusted
    // callers cannot smuggle spoofed X-Forwarded-For/X-Real-IP headers through the BFF.
    const h = await headers();
    const ip = resolveForwardedClientIp(h);
    if (ip) reqHeaders["X-Forwarded-For"] = ip;
    if (opts.auth !== "none") {
      store = await cookies();
      access = store.get(ACCESS_COOKIE)?.value || undefined;
    }
  }

  const url = buildUrl(path, opts.query);
  const send = (token?: string) =>
    doFetch(url, {
      method,
      headers: token ? { ...reqHeaders, Authorization: `Bearer ${token}` } : reqHeaders,
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
      timeoutMs: opts.timeoutMs ?? GATEWAY_TIMEOUT_MS,
      retry: method === "GET",
      cacheable,
      revalidate: opts.revalidate,
      tags: opts.tags,
    });

  let res = await send(access);
  if (res.status === 401 && store) {
    const fresh = await refreshInPlace(store);
    if (fresh) res = await send(fresh);
  }
  if (!res.ok) throw await errorFromResponse(res);
  if (res.status === 204) return undefined as T;
  try {
    return (await res.json()) as T;
  } catch {
    throw new GatewayError("unavailable");
  }
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
    throw new GatewayError("unavailable");
  }
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * Rotate the session after a 401. Returns the new access token, or null if the session is dead
 * or cookies are read-only here (server component render) – rotating without persisting the new
 * refresh token would get the whole token family revoked on the next request.
 */
async function refreshInPlace(store: Awaited<ReturnType<typeof cookies>>): Promise<string | null> {
  const rt = store.get(REFRESH_COOKIE)?.value;
  if (!rt) return null;
  try {
    store.set(ACCESS_COOKIE, "", { ...baseCookie, maxAge: 0 }); // throws when read-only
  } catch {
    return null;
  }
  const pair = await rotateRefreshToken(rt);
  if (!pair) {
    clearAuthCookies(store);
    return null;
  }
  setAuthCookies(store, pair);
  return pair.accessToken;
}
