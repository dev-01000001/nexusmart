// Token cookies + refresh-token rotation + logout cache invalidation.
// Runs on Node.js runtime across middleware, RSC, and route handlers so single-flight
// deduplication and the 5s post-rotation grace cache are shared via globalThis (Symbol.for).
import { createHash } from "node:crypto";
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

function sha256Hex(value: string): string {
  return createHash("sha256").update(value).digest("hex");
}

// The user service revokes the whole token family when a refresh token is replayed (also for
// concurrent replays). Parallel requests carrying the same old cookie must therefore share ONE
// rotation: in-flight + recently finished rotations are memoised per process for REFRESH_GRACE_MS.
export const REFRESH_GRACE_MS = 5_000;
const MAX_ROTATIONS = 200;

interface RotationEntry {
  at: number;
  p: Promise<TokenPair | null>;
  pair?: TokenPair | null;
  settled: boolean;
  cancelled: boolean;
}

interface RefreshTokenState {
  rotations: Map<string, RotationEntry>;
  cancelledRawTokens: Map<string, number>;
}

const REFRESH_STATE_SYMBOL = Symbol.for("nexusmart.web.refreshTokenState");

// ponytail: in-process globalThis (Symbol.for) single-flight + 5s grace cache only shares state
// across Node middleware, RSC, and route handlers inside one process; multiple replicas require
// Redis or sticky sessions.
function getRefreshState(): RefreshTokenState {
  const g = globalThis as Record<symbol, RefreshTokenState | undefined>;
  let state = g[REFRESH_STATE_SYMBOL];
  if (!state) {
    state = {
      rotations: new Map<string, RotationEntry>(),
      cancelledRawTokens: new Map<string, number>(),
    };
    g[REFRESH_STATE_SYMBOL] = state;
  }
  return state;
}

function pruneMaps(state: RefreshTokenState, now: number) {
  const { rotations, cancelledRawTokens } = state;
  for (const [k, v] of rotations) {
    if (v.settled && now - v.at >= REFRESH_GRACE_MS) rotations.delete(k);
  }
  if (rotations.size > MAX_ROTATIONS) {
    for (const [k, v] of rotations) {
      if (rotations.size <= MAX_ROTATIONS) break;
      if (v.settled) rotations.delete(k);
    }
    while (rotations.size > MAX_ROTATIONS) {
      const oldest = rotations.keys().next().value;
      if (oldest === undefined) break;
      rotations.delete(oldest);
    }
  }
  for (const [tok, exp] of cancelledRawTokens) {
    if (exp <= now) cancelledRawTokens.delete(tok);
  }
}

export async function revokeTokenPairBestEffort(
  pair: TokenPair,
  trace?: Pick<TraceContext, "traceparent" | "tracestate">,
  clientIp?: string | null
): Promise<void> {
  const traceparent = trace?.traceparent || generateTraceparent();
  try {
    await fetch(`${GATEWAY_URL}/api/v1/auth/logout`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
        Authorization: `Bearer ${pair.accessToken}`,
        traceparent,
        ...(trace?.tracestate ? { tracestate: trace.tracestate } : {}),
        ...(clientIp ? { "X-Forwarded-For": clientIp } : {}),
      },
      body: JSON.stringify({ refresh_token: pair.refreshToken }),
      cache: "no-store",
      signal: AbortSignal.timeout(5_000),
    });
  } catch {
    // Best effort revocation when an in-flight refresh completes after logout.
  }
}

export interface InvalidateRefreshCacheResult {
  purgedTokens: TokenPair | null;
  inFlightPromise: Promise<TokenPair | null> | null;
}

/**
 * Purges any short-lived grace cache entry associated with a logged-out refresh token and marks
 * any in-flight refresh for that token as cancelled so it cannot write rotated tokens back into
 * the grace cache after logout.
 */
export async function invalidateRefreshCache(
  refreshToken: string | undefined
): Promise<InvalidateRefreshCacheResult> {
  if (!refreshToken) {
    return { purgedTokens: null, inFlightPromise: null };
  }
  const state = getRefreshState();
  const { rotations, cancelledRawTokens } = state;
  const now = Date.now();
  pruneMaps(state, now);
  cancelledRawTokens.set(refreshToken, now + REFRESH_GRACE_MS);

  const key = sha256Hex(refreshToken);
  let purgedTokens: TokenPair | null = null;
  let inFlightPromise: Promise<TokenPair | null> | null = null;

  const direct = rotations.get(key);
  if (direct) {
    direct.cancelled = true;
    if (direct.settled && direct.pair) {
      purgedTokens = direct.pair;
    } else if (!direct.settled) {
      inFlightPromise = direct.p;
    }
    rotations.delete(key);
  }

  for (const [entryKey, entry] of rotations) {
    if (entry.pair && entry.pair.refreshToken === refreshToken) {
      entry.cancelled = true;
      purgedTokens ??= entry.pair;
      rotations.delete(entryKey);
    }
  }

  return { purgedTokens, inFlightPromise };
}

export interface RotateRefreshOptions {
  clientIp?: string | null;
}

/**
 * Rotate a refresh token. Resolves the new pair, or null when the session is dead (invalid /
 * expired / reused / account inactive / cancelled by logout). Throws GatewayError("rate_limited")
 * on 429 or GatewayError("unavailable") on outages so the caller keeps the current cookies.
 */
export async function rotateRefreshToken(
  refreshToken: string,
  trace?: Pick<TraceContext, "traceparent" | "tracestate">,
  opts?: RotateRefreshOptions
): Promise<TokenPair | null> {
  const state = getRefreshState();
  const { rotations, cancelledRawTokens } = state;
  const now = Date.now();
  pruneMaps(state, now);
  if (cancelledRawTokens.has(refreshToken)) {
    return null;
  }

  const key = sha256Hex(refreshToken);
  const hit = rotations.get(key);
  if (hit && !hit.cancelled) return hit.p;

  const entry: RotationEntry = {
    at: now,
    p: Promise.resolve(null),
    settled: false,
    cancelled: false,
  };

  entry.p = callRefresh(refreshToken, trace, opts)
    .then(async (pair) => {
      entry.settled = true;
      if (pair && (entry.cancelled || cancelledRawTokens.has(refreshToken))) {
        if (rotations.get(key) === entry) {
          rotations.delete(key);
        }
        await revokeTokenPairBestEffort(pair, trace, opts?.clientIp);
        return null;
      }
      if (!pair) {
        if (rotations.get(key) === entry) {
          rotations.delete(key);
        }
        return null;
      }
      entry.pair = pair;
      entry.at = Date.now();
      return pair;
    })
    .catch((err) => {
      entry.settled = true;
      if (rotations.get(key) === entry) {
        rotations.delete(key);
      }
      throw err;
    });

  rotations.set(key, entry);
  return entry.p;
}

async function callRefresh(
  refreshToken: string,
  trace?: Pick<TraceContext, "traceparent" | "tracestate">,
  opts?: RotateRefreshOptions
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
        ...(opts?.clientIp ? { "X-Forwarded-For": opts.clientIp } : {}),
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
