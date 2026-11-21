// Token cookies + refresh-token rotation. Runtime-agnostic (uses Web Crypto + atob, no
// node:crypto or next/headers) so it runs identically in middleware, route handlers and actions.
import {
  ACCESS_COOKIE,
  GATEWAY_TIMEOUT_MS,
  GATEWAY_URL,
  REFRESH_COOKIE,
  REFRESH_MAX_AGE_S,
  baseCookie,
} from "./config.ts";
import { GatewayError, errorFromResponse } from "./errors.ts";
import { extractTraceId, generateTraceparent, type TraceContext } from "./trace.ts";

export interface TokenPair {
  accessToken: string;
  refreshToken: string;
  expiresIn: number;
}

/** Anything with Next's cookie `set` signature: cookies() store or NextResponse.cookies. */
interface CookieWriter {
  set(name: string, value: string, options: Record<string, unknown>): unknown;
}

export function setAuthCookies(jar: CookieWriter, pair: TokenPair) {
  const ttl = pair.expiresIn > 0 ? pair.expiresIn : 900;
  jar.set(ACCESS_COOKIE, pair.accessToken, { ...baseCookie, maxAge: ttl });
  jar.set(REFRESH_COOKIE, pair.refreshToken, { ...baseCookie, maxAge: REFRESH_MAX_AGE_S });
}

export function clearAuthCookies(jar: CookieWriter) {
  jar.set(ACCESS_COOKIE, "", { ...baseCookie, maxAge: 0 });
  jar.set(REFRESH_COOKIE, "", { ...baseCookie, maxAge: 0 });
}

/**
 * Rebuild an incoming `Cookie` header with updated (or stripped, when `pair === null`) auth
 * cookies so Server Components rendering the current request see rotated tokens immediately.
 */
export function applyAuthCookiesToHeader(
  cookieHeader: string | null,
  pair: TokenPair | null
): string {
  const parts = (cookieHeader ?? "")
    .split(";")
    .map((part) => part.trim())
    .filter(Boolean)
    .filter((part) => {
      const eq = part.indexOf("=");
      const name = eq === -1 ? part : part.slice(0, eq).trim();
      return name !== ACCESS_COOKIE && name !== REFRESH_COOKIE;
    });
  if (pair) {
    parts.push(`${ACCESS_COOKIE}=${pair.accessToken}`);
    parts.push(`${REFRESH_COOKIE}=${pair.refreshToken}`);
  }
  return parts.join("; ");
}

function decodeBase64Url(input: string): string {
  const b64 = input.replace(/-/g, "+").replace(/_/g, "/");
  const pad = b64.length % 4 === 0 ? "" : "=".repeat(4 - (b64.length % 4));
  return atob(b64 + pad);
}

/**
 * True when the JWT is missing, malformed or expires within `skewS` seconds. Only used to decide
 * WHEN to refresh – the payload is not trusted for identity or role (the gateway verifies it).
 */
export function accessExpiresSoon(token: string | undefined, skewS = 60): boolean {
  if (!token) return true;
  try {
    const segment = token.split(".")[1];
    if (!segment) return true;
    const payload = JSON.parse(decodeBase64Url(segment)) as { exp?: unknown };
    return typeof payload.exp !== "number" || payload.exp - skewS <= Date.now() / 1000;
  } catch {
    return true;
  }
}

async function sha256Hex(value: string): Promise<string> {
  const data = new TextEncoder().encode(value);
  const hash = await crypto.subtle.digest("SHA-256", data);
  return Array.from(new Uint8Array(hash), (b) => b.toString(16).padStart(2, "0")).join("");
}

// The user service revokes the whole token family when a refresh token is replayed (also for
// concurrent replays). Parallel requests carrying the same old cookie must therefore share ONE
// rotation: in-flight + recently finished rotations are memoised per process for GRACE_MS.
const GRACE_MS = 30_000;
type Entry = { at: number; p: Promise<TokenPair | null> };
const g = globalThis as unknown as { __nmRotations?: Map<string, Entry> };
const rotations: Map<string, Entry> = (g.__nmRotations ??= new Map());

/**
 * Rotate a refresh token. Resolves the new pair, or null when the session is dead (invalid /
 * expired / reused / account inactive). Throws GatewayError("unavailable") on outages so the
 * caller keeps the current cookies.
 */
export async function rotateRefreshToken(
  refreshToken: string,
  trace?: Pick<TraceContext, "traceparent" | "tracestate">
): Promise<TokenPair | null> {
  const key = await sha256Hex(refreshToken);
  const now = Date.now();
  for (const [k, v] of rotations) if (now - v.at > GRACE_MS) rotations.delete(k);
  const hit = rotations.get(key);
  if (hit) return hit.p;
  const p = callRefresh(refreshToken, trace);
  rotations.set(key, { at: now, p });
  p.catch(() => rotations.delete(key)); // outage: allow a retry on the next request
  return p;
}

async function callRefresh(
  refreshToken: string,
  trace?: Pick<TraceContext, "traceparent" | "tracestate">
): Promise<TokenPair | null> {
  const traceparent = trace?.traceparent || generateTraceparent();
  const traceId = extractTraceId(traceparent);
  let res: Response;
  try {
    res = await fetch(`${GATEWAY_URL}/api/v1/auth/refresh`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
        traceparent,
        ...(trace?.tracestate ? { tracestate: trace.tracestate } : {}),
      },
      body: JSON.stringify({ refresh_token: refreshToken }),
      cache: "no-store",
      signal: AbortSignal.timeout(GATEWAY_TIMEOUT_MS),
    });
  } catch {
    throw new GatewayError("unavailable", undefined, "", traceId);
  }
  if (res.ok) {
    const b = (await res.json()) as {
      access_token?: string;
      refresh_token?: string;
      expires_in_seconds?: number;
    };
    if (!b.access_token || !b.refresh_token) {
      throw new GatewayError("unavailable", undefined, "", traceId);
    }
    return {
      accessToken: b.access_token,
      refreshToken: b.refresh_token,
      expiresIn: Number(b.expires_in_seconds) || 900,
    };
  }
  const err = await errorFromResponse(res, traceId);
  if (err.kind === "unavailable" || err.kind === "rate_limited") throw err;
  return null; // 400/401/403/404: session is gone
}
