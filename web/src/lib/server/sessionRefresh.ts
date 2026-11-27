// Pure helpers for detecting stale JWT claims vs backend-verified user/shop state
// and guarding single-attempt session refresh without infinite loops.
// Safe for server, client, and `node --test`.

export interface MinimalSessionUser {
  role: string;
  shopId?: string | null;
}

export interface DecodedRefreshCheckClaims {
  role: string;
  shopId: string;
}

export const CLIENT_SESSION_SYNC_COOLDOWN_MS = 30_000;

function decodeBase64UrlUtf8(segment: string): string {
  const b64 = segment.replace(/-/g, "+").replace(/_/g, "/");
  const pad = b64.length % 4 === 0 ? "" : "=".repeat(4 - (b64.length % 4));
  if (typeof Buffer !== "undefined") {
    return Buffer.from(b64 + pad, "base64").toString("utf8");
  }
  return atob(b64 + pad);
}

/**
 * Extracts `role` and `shop_id` from a 3-part JWT payload strictly to detect whether the cached
 * HttpOnly access token lags behind backend-verified `/api/v1/users/me`.
 * NEVER use these unverified claims to grant permissions.
 */
export function decodeJwtClaimsForRefreshCheck(
  token: string | undefined
): DecodedRefreshCheckClaims | null {
  if (!token) return null;
  const parts = token.split(".");
  if (parts.length !== 3 || !parts[1]) return null;
  try {
    const raw = JSON.parse(decodeBase64UrlUtf8(parts[1])) as Record<string, unknown>;
    if (!raw || typeof raw !== "object" || typeof raw.role !== "string") return null;
    const role = raw.role.trim().toLowerCase();
    if (!role) return null;
    const shopId = typeof raw.shop_id === "string" ? raw.shop_id.trim() : "";
    return { role, shopId };
  } catch {
    return null;
  }
}

/**
 * True when a syntactically valid JWT access token carries a different `role` or `shop_id`
 * than the backend-verified user profile (e.g. buyer token minted before admin approved shop).
 */
export function isAccessTokenStaleForUser(
  accessToken: string | undefined,
  user: MinimalSessionUser
): boolean {
  const claims = decodeJwtClaimsForRefreshCheck(accessToken);
  if (!claims) return false;
  const userRole = (user.role ?? "").trim().toLowerCase();
  const userShopId = (user.shopId ?? "").trim();
  if (!userRole) return false;
  if (claims.role !== userRole) return true;
  if (userRole === "seller" && userShopId && claims.shopId !== userShopId) return true;
  return false;
}

export interface RefreshSessionDecisionInput {
  accessToken?: string;
  hasRefreshToken: boolean;
  user: MinimalSessionUser;
  shopStatus?: string | null;
  attempted: boolean;
}

/**
 * Decides whether the BFF should perform a single refresh-token rotation to synchronize
 * updated role/shop claims (e.g. right after admin approves a shop).
 * Always returns `false` once `attempted` is true so refresh can never loop.
 */
export function shouldRefreshSellerSession(input: RefreshSessionDecisionInput): boolean {
  if (input.attempted) return false;
  if (!input.hasRefreshToken) return false;

  if (isAccessTokenStaleForUser(input.accessToken, input.user)) {
    return true;
  }

  const normalizedShopStatus = (input.shopStatus ?? "").trim().toUpperCase();
  const normalizedRole = (input.user.role ?? "").trim().toLowerCase();
  const normalizedShopId = (input.user.shopId ?? "").trim();
  if (normalizedShopStatus === "ACTIVE" && (normalizedRole !== "seller" || !normalizedShopId)) {
    return true;
  }

  return false;
}

export interface ClientSessionSyncAttempt {
  userId?: string;
  atMs?: number;
}

/**
 * Client-side loop guard for `<SellerSessionSync />`: allows at most one automatic refresh+reload
 * per user within `cooldownMs`.
 */
export function shouldAttemptClientSessionSync(
  lastAttempt: ClientSessionSyncAttempt | null | undefined,
  userId: string,
  nowMs: number,
  cooldownMs = CLIENT_SESSION_SYNC_COOLDOWN_MS
): boolean {
  const cleanUserId = userId.trim();
  if (!cleanUserId) return false;
  if (!lastAttempt || lastAttempt.userId !== cleanUserId) return true;
  if (typeof lastAttempt.atMs !== "number" || !Number.isFinite(lastAttempt.atMs)) return true;
  const elapsed = nowMs - lastAttempt.atMs;
  if (elapsed < 0) return true;
  return elapsed >= cooldownMs;
}
