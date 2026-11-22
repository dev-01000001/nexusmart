import { createHash } from "node:crypto";
import type { NextResponse } from "next/server";
import type { GatewayRefreshResponse } from "@/lib/gateway-types";
export { sanitizeNextPath } from "@/lib/redirect";

export const ACCESS_COOKIE = "nm_access_token";
export const REFRESH_COOKIE = "nm_refresh_token";

// Matches REFRESH_TOKEN_TTL=168h (7 days) in services/user.
export const REFRESH_COOKIE_MAX_AGE = 7 * 24 * 60 * 60;
export const DEFAULT_ACCESS_MAX_AGE = 15 * 60;

// Refresh proactively in middleware when access token has <= 60s remaining.
export const PROACTIVE_REFRESH_WINDOW_SECONDS = 60;

// Short grace window so concurrent/staggered requests arriving right after a rotation
// (before the browser applies Set-Cookie) receive the already-issued pair instead of
// triggering family revocation at services/user.
export const REFRESH_GRACE_TTL_MS = 5_000;
const REFRESH_CACHE_MAX_ENTRIES = 200;

const CONTROL_CHAR_PATTERN = /[\r\n\u0000-\u001f\u007f]/;

export function getGatewayUrl(): string {
  return (process.env.GATEWAY_URL || "http://localhost:38080").replace(/\/+$/, "");
}

export function isCookieSecure(): boolean {
  const raw = process.env.COOKIE_SECURE?.trim().toLowerCase();
  if (raw === "false" || raw === "0") return false;
  if (raw === "true" || raw === "1") return true;
  return process.env.NODE_ENV === "production";
}

function baseCookieOptions(maxAge: number) {
  return {
    httpOnly: true,
    sameSite: "lax" as const,
    path: "/",
    secure: isCookieSecure(),
    maxAge,
    // Intentionally omit `domain` so cookies remain host-only.
  };
}

export function setSessionCookies(
  res: NextResponse,
  tokens: {
    access_token: string;
    refresh_token: string;
    expires_in_seconds?: number;
  }
): void {
  const accessMaxAge =
    typeof tokens.expires_in_seconds === "number" && tokens.expires_in_seconds > 0
      ? Math.floor(tokens.expires_in_seconds)
      : DEFAULT_ACCESS_MAX_AGE;

  res.cookies.set(ACCESS_COOKIE, tokens.access_token, baseCookieOptions(accessMaxAge));
  res.cookies.set(REFRESH_COOKIE, tokens.refresh_token, baseCookieOptions(REFRESH_COOKIE_MAX_AGE));
}

export function clearSessionCookies(res: NextResponse): void {
  res.cookies.set(ACCESS_COOKIE, "", baseCookieOptions(0));
  res.cookies.set(REFRESH_COOKIE, "", baseCookieOptions(0));
}

/**
 * Extracts the client IP from incoming request headers (`x-forwarded-for`, falling back to
 * `x-real-ip`) so BFF forwards per-client IP to gateway rate limiting.
 */
export function extractClientIp(hdrs: Pick<Headers, "get"> | null | undefined): string | undefined {
  if (!hdrs) return undefined;

  const xff = hdrs.get("x-forwarded-for");
  if (xff) {
    const first = xff.split(",")[0]?.trim();
    if (first && !CONTROL_CHAR_PATTERN.test(first)) {
      return first;
    }
  }

  const xri = hdrs.get("x-real-ip")?.trim();
  if (xri && !CONTROL_CHAR_PATTERN.test(xri)) {
    return xri;
  }

  return undefined;
}

export function applyClientIpHeader(target: Headers, clientIp: string | undefined): void {
  if (clientIp && !CONTROL_CHAR_PATTERN.test(clientIp)) {
    target.set("X-Forwarded-For", clientIp);
  }
}

/**
 * Reads `exp` from an unverified JWT payload ONLY to decide when to trigger
 * proactive refresh. Never use decoded claims for authorization or RBAC.
 */
export function readJwtExp(token: string | undefined): number | null {
  if (!token) return null;
  const parts = token.split(".");
  if (parts.length !== 3) return null;
  try {
    const base64 = parts[1].replace(/-/g, "+").replace(/_/g, "/");
    const padded = base64.padEnd(Math.ceil(base64.length / 4) * 4, "=");
    const payload = JSON.parse(atob(padded)) as { exp?: unknown };
    return typeof payload.exp === "number" && Number.isFinite(payload.exp) ? payload.exp : null;
  } catch {
    return null;
  }
}

/**
 * Replaces or appends cookies in a raw Cookie request header so Server Components
 * rendered during the same request after middleware refresh see the new access token.
 */
export function buildCookieHeaderWithUpdates(
  existingHeader: string | null,
  updates: Record<string, string>
): string {
  const map = new Map<string, string>();
  if (existingHeader) {
    for (const part of existingHeader.split(";")) {
      const idx = part.indexOf("=");
      if (idx <= 0) continue;
      const name = part.slice(0, idx).trim();
      const val = part.slice(idx + 1).trim();
      if (name) map.set(name, val);
    }
  }
  for (const [k, v] of Object.entries(updates)) {
    if (v === "") {
      map.delete(k);
    } else {
      map.set(k, v);
    }
  }
  return Array.from(map.entries())
    .map(([k, v]) => `${k}=${v}`)
    .join("; ");
}

export interface RefreshGatewayResult {
  ok: boolean;
  status: number;
  tokens?: GatewayRefreshResponse;
  code?: string;
}

export interface RefreshGatewayOptions {
  timeoutMs?: number;
  clientIp?: string;
}

interface CachedRefreshEntry {
  tokens: GatewayRefreshResponse;
  expiresAt: number;
}

interface InFlightRefreshEntry {
  promise: Promise<RefreshGatewayResult>;
  cancelled: boolean;
}

interface RefreshStateStore {
  inFlightRefreshes: Map<string, InFlightRefreshEntry>;
  recentRefreshes: Map<string, CachedRefreshEntry>;
}

const REFRESH_STATE_SYMBOL = Symbol.for("nexusmart.bff.refreshState");

// ponytail: in-memory single-flight + 5s grace cache shared via globalThis across Node
// middleware and route handler bundles within one Next.js process; multiple replicas require Redis.
function getRefreshState(): RefreshStateStore {
  const g = globalThis as Record<symbol, RefreshStateStore | undefined>;
  let store = g[REFRESH_STATE_SYMBOL];
  if (!store) {
    store = {
      inFlightRefreshes: new Map<string, InFlightRefreshEntry>(),
      recentRefreshes: new Map<string, CachedRefreshEntry>(),
    };
    g[REFRESH_STATE_SYMBOL] = store;
  }
  return store;
}

function sha256Hex(value: string): string {
  return createHash("sha256").update(value).digest("hex");
}

function pruneRecentRefreshes(recentRefreshes: Map<string, CachedRefreshEntry>, now: number): void {
  for (const [k, entry] of recentRefreshes) {
    if (entry.expiresAt <= now) {
      recentRefreshes.delete(k);
    }
  }
  while (recentRefreshes.size > REFRESH_CACHE_MAX_ENTRIES) {
    const oldestKey = recentRefreshes.keys().next().value;
    if (oldestKey === undefined) break;
    recentRefreshes.delete(oldestKey);
  }
}

async function bestEffortRevokeTokens(
  tokens: GatewayRefreshResponse,
  clientIp: string | undefined
): Promise<void> {
  const headers = new Headers({
    "Content-Type": "application/json",
    Accept: "application/json",
    Authorization: `Bearer ${tokens.access_token}`,
  });
  applyClientIpHeader(headers, clientIp);
  try {
    await fetch(`${getGatewayUrl()}/api/v1/auth/logout`, {
      method: "POST",
      headers,
      body: JSON.stringify({ refresh_token: tokens.refresh_token }),
      cache: "no-store",
      signal: AbortSignal.timeout(5_000),
    });
  } catch {
    // Best effort: logout still clears cookies and revokes caller tokens.
  }
}

export interface InvalidateRefreshCacheResult {
  purgedTokens: GatewayRefreshResponse | null;
  inFlightPromise: Promise<RefreshGatewayResult> | null;
}

/**
 * Synchronously purges any short-lived grace cache entry associated with a logged-out
 * refresh token and marks any in-flight refresh for that token as cancelled so it cannot
 * write rotated tokens back into `recentRefreshes` after logout.
 */
export function invalidateRefreshCache(
  refreshToken: string | undefined
): InvalidateRefreshCacheResult {
  if (!refreshToken) {
    return { purgedTokens: null, inFlightPromise: null };
  }
  const state = getRefreshState();
  const key = sha256Hex(refreshToken);

  let purgedTokens: GatewayRefreshResponse | null = null;
  const direct = state.recentRefreshes.get(key);
  if (direct) {
    purgedTokens = direct.tokens;
    state.recentRefreshes.delete(key);
  }

  for (const [entryKey, entry] of state.recentRefreshes) {
    if (entry.tokens.refresh_token === refreshToken) {
      purgedTokens ??= entry.tokens;
      state.recentRefreshes.delete(entryKey);
    }
  }

  let inFlightPromise: Promise<RefreshGatewayResult> | null = null;
  const inFlight = state.inFlightRefreshes.get(key);
  if (inFlight) {
    inFlight.cancelled = true;
    inFlightPromise = inFlight.promise;
    state.inFlightRefreshes.delete(key);
  }

  return { purgedTokens, inFlightPromise };
}

/**
 * Calls gateway POST /api/v1/auth/refresh with single-flight deduplication and a short
 * (5s) post-rotation grace cache keyed by sha256(oldRefreshToken), shared across Node
 * middleware and route handlers on globalThis. Prevents concurrent/staggered requests
 * from replaying a just-rotated token and triggering family revocation.
 */
export function refreshTokensWithGateway(
  refreshToken: string,
  options: RefreshGatewayOptions = {}
): Promise<RefreshGatewayResult> {
  const state = getRefreshState();
  const key = sha256Hex(refreshToken);
  const now = Date.now();

  const cached = state.recentRefreshes.get(key);
  if (cached) {
    if (cached.expiresAt > now) {
      return Promise.resolve({ ok: true, status: 200, tokens: cached.tokens });
    }
    state.recentRefreshes.delete(key);
  }

  const existing = state.inFlightRefreshes.get(key);
  if (existing && !existing.cancelled) {
    return existing.promise;
  }

  const timeoutMs = options.timeoutMs ?? 10_000;
  const reqHeaders = new Headers({
    "Content-Type": "application/json",
    Accept: "application/json",
  });
  applyClientIpHeader(reqHeaders, options.clientIp);

  const entry: InFlightRefreshEntry = {
    cancelled: false,
    promise: Promise.resolve({ ok: false, status: 503, code: "UNAVAILABLE" }),
  };

  entry.promise = (async (): Promise<RefreshGatewayResult> => {
    try {
      const res = await fetch(`${getGatewayUrl()}/api/v1/auth/refresh`, {
        method: "POST",
        headers: reqHeaders,
        body: JSON.stringify({ refresh_token: refreshToken }),
        cache: "no-store",
        signal: AbortSignal.timeout(timeoutMs),
      });

      const body = (await res.json().catch(() => null)) as
        GatewayRefreshResponse | { error?: { code?: string; message?: string } } | null;

      if (!res.ok) {
        const errCode =
          body && "error" in body && typeof body.error?.code === "string"
            ? body.error.code
            : res.status >= 500
              ? "UNAVAILABLE"
              : "UNAUTHENTICATED";
        return {
          ok: false,
          status: res.status >= 500 ? 503 : res.status,
          code: errCode,
        };
      }

      const tokens = body as GatewayRefreshResponse | null;
      if (!tokens?.access_token || !tokens?.refresh_token) {
        return { ok: false, status: 503, code: "UNAVAILABLE" };
      }

      if (entry.cancelled) {
        await bestEffortRevokeTokens(tokens, options.clientIp);
        return { ok: false, status: 401, code: "UNAUTHENTICATED" };
      }

      const settledAt = Date.now();
      pruneRecentRefreshes(state.recentRefreshes, settledAt);
      state.recentRefreshes.set(key, {
        tokens,
        expiresAt: settledAt + REFRESH_GRACE_TTL_MS,
      });

      return { ok: true, status: 200, tokens };
    } catch {
      return { ok: false, status: 503, code: "UNAVAILABLE" };
    }
  })().finally(() => {
    if (state.inFlightRefreshes.get(key) === entry) {
      state.inFlightRefreshes.delete(key);
    }
  });

  state.inFlightRefreshes.set(key, entry);
  return entry.promise;
}
