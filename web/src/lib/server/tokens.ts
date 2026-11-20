// Token cookies + refresh-token rotation. Safe to import from middleware (nodejs runtime),
// route handlers and server actions; does not touch next/headers.
import { createHash } from "node:crypto";
import {
  ACCESS_COOKIE,
  GATEWAY_TIMEOUT_MS,
  GATEWAY_URL,
  REFRESH_COOKIE,
  REFRESH_MAX_AGE_S,
  baseCookie,
} from "./config";
import { GatewayError, errorFromResponse } from "./errors";

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
 * True when the JWT is missing, malformed or expires within `skewS` seconds. Only used to decide
 * WHEN to refresh – the payload is not trusted for identity or role (the gateway verifies it).
 */
export function accessExpiresSoon(token: string | undefined, skewS = 60): boolean {
  if (!token) return true;
  try {
    const payload = JSON.parse(Buffer.from(token.split(".")[1] ?? "", "base64url").toString());
    return typeof payload.exp !== "number" || payload.exp - skewS <= Date.now() / 1000;
  } catch {
    return true;
  }
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
export function rotateRefreshToken(refreshToken: string): Promise<TokenPair | null> {
  const now = Date.now();
  for (const [k, v] of rotations) if (now - v.at > GRACE_MS) rotations.delete(k);
  const key = createHash("sha256").update(refreshToken).digest("hex");
  const hit = rotations.get(key);
  if (hit) return hit.p;
  const p = callRefresh(refreshToken);
  rotations.set(key, { at: now, p });
  p.catch(() => rotations.delete(key)); // outage: allow a retry on the next request
  return p;
}

async function callRefresh(refreshToken: string): Promise<TokenPair | null> {
  let res: Response;
  try {
    res = await fetch(`${GATEWAY_URL}/api/v1/auth/refresh`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify({ refresh_token: refreshToken }),
      cache: "no-store",
      signal: AbortSignal.timeout(GATEWAY_TIMEOUT_MS),
    });
  } catch {
    throw new GatewayError("unavailable");
  }
  if (res.ok) {
    const b = (await res.json()) as {
      access_token?: string;
      refresh_token?: string;
      expires_in_seconds?: number;
    };
    if (!b.access_token || !b.refresh_token) throw new GatewayError("unavailable");
    return {
      accessToken: b.access_token,
      refreshToken: b.refresh_token,
      expiresIn: Number(b.expires_in_seconds) || 900,
    };
  }
  const err = await errorFromResponse(res);
  if (err.kind === "unavailable" || err.kind === "rate_limited") throw err;
  return null; // 400/401/403/404: session is gone
}
