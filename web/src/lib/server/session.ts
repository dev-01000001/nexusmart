// Session helpers. Identity and role always come from the backend (/users/me), never from the
// client or from decoding the JWT locally.
import { cache } from "react";
import { cookies, headers } from "next/headers";
import { notFound, redirect } from "next/navigation";
import { ADMIN_STAFF_ROLES, type AdminCapability, roleHasCapability } from "../admin-rbac";
import { sanitizeNextPath } from "../redirect";
import { ACCESS_COOKIE, REFRESH_COOKIE, baseCookie } from "./config";
import { GatewayError } from "./errors";
import { gw } from "./gateway";
import { resolveForwardedClientIp } from "./http";
import { shouldRefreshSellerSession } from "./sessionRefresh";
import { clearAuthCookies, rotateRefreshToken, setAuthCookies } from "./tokens";
import { resolveTraceContext } from "./trace";

export type Role =
  "buyer" | "seller" | "admin" | "super_admin" | "moderator" | "cskh" | "accountant" | "ops";

export const ADMIN_ROLES: readonly Role[] = [...ADMIN_STAFF_ROLES];
export const SELLER_ROLES: readonly Role[] = ["seller"];

export interface SessionUser {
  id: string;
  fullName: string;
  email: string;
  phone: string;
  role: Role;
  status: string;
  /** Shop owned by a seller ("" otherwise). Use this, never a shop_id sent by the client. */
  shopId: string;
  avatarUrl: string;
  phoneVerified: boolean;
  codRejectedCount: number;
}

export interface Session {
  user: SessionUser;
}

/** Gateway user DTO (snake_case) as returned by /users/me, /auth/login, /auth/verify-otp. */
export interface GatewayUser {
  id: string;
  full_name?: string;
  email?: string;
  phone?: string;
  role?: string;
  status?: string;
  shop_id?: string;
  avatar_url?: string;
  phone_verified?: boolean;
  cod_rejected_count?: number;
}

const VALID_ROLES = new Set<Role>([
  "buyer",
  "seller",
  "admin",
  "super_admin",
  "moderator",
  "cskh",
  "accountant",
  "ops",
]);

export function toSessionUser(u: GatewayUser): SessionUser {
  const rawRole = (u.role ?? "").trim().toLowerCase() as Role;
  return {
    id: u.id,
    fullName: u.full_name ?? "",
    email: u.email ?? "",
    phone: u.phone ?? "",
    role: VALID_ROLES.has(rawRole) ? rawRole : "buyer",
    status: u.status ?? "",
    shopId: u.shop_id ?? "",
    avatarUrl: u.avatar_url ?? "",
    phoneVerified: Boolean(u.phone_verified),
    codRejectedCount:
      typeof u.cod_rejected_count === "number" && Number.isFinite(u.cod_rejected_count)
        ? Math.max(0, Math.trunc(u.cod_rejected_count))
        : 0,
  };
}

/**
 * Current session or null. Memoised per request in server components. Returns null on
 * unauthenticated/forbidden; throws GatewayError("unavailable") when the backend is down.
 */
export const getSession = cache(async (): Promise<Session | null> => {
  const store = await cookies();
  if (!store.get(ACCESS_COOKIE)?.value && !store.get(REFRESH_COOKIE)?.value) return null;
  try {
    const { user } = await gw<{ user: GatewayUser | null }>("/api/v1/users/me");
    return user?.id ? { user: toSessionUser(user) } : null;
  } catch (err) {
    if (err instanceof GatewayError && (err.kind === "unauthenticated" || err.kind === "forbidden"))
      return null;
    throw err;
  }
});

export function loginPath(next?: string): string {
  const safe = sanitizeNextPath(next, "");
  return safe ? `/dang-nhap?next=${encodeURIComponent(safe)}` : "/dang-nhap";
}

// ---- Pages / layouts (server components): redirect or 404 ----

/** Redirects to /dang-nhap?next=… when there is no session. */
export async function requireSession(next?: string): Promise<Session> {
  const s = await getSession();
  if (!s) redirect(loginPath(next));
  return s;
}

/** Like requireSession, plus 404 when the backend role is not one of `roles`. */
export async function requireRole(...roles: readonly Role[]): Promise<Session> {
  const s = await requireSession();
  if (!roles.includes(s.user.role)) notFound();
  return s;
}

/** Like requireSession, plus 404 when the backend role lacks the required admin capability. */
export async function requireCapability(capability: AdminCapability): Promise<Session> {
  const s = await requireSession();
  if (!roleHasCapability(s.user.role, capability)) notFound();
  return s;
}

const getRequestRefreshGuard = cache(() => ({ attempted: false }));

/**
 * True when the current request's HttpOnly access cookie carries stale role/shop claims
 * compared to the backend-verified user (or an ACTIVE shop without seller claims).
 */
export async function isCurrentAccessTokenStale(
  user: Pick<SessionUser, "role" | "shopId">,
  shopStatus?: string | null
): Promise<boolean> {
  const store = await cookies();
  return shouldRefreshSellerSession({
    accessToken: store.get(ACCESS_COOKIE)?.value,
    hasRefreshToken: Boolean(store.get(REFRESH_COOKIE)?.value),
    user,
    shopStatus,
    attempted: false,
  });
}

/**
 * Rotates the refresh token at most ONCE per request (only where cookies are writable, e.g.
 * Route Handlers) when the access token JWT claims lag behind the backend-verified user/shop.
 * Never weakens authorization — the refreshed JWT issued by `user` service remains authoritative.
 */
export async function refreshStaleSessionCookiesOnce(
  user: Pick<SessionUser, "role" | "shopId">,
  shopStatus?: string | null
): Promise<boolean> {
  const guard = getRequestRefreshGuard();
  const store = await cookies();
  const accessToken = store.get(ACCESS_COOKIE)?.value;
  const refreshToken = store.get(REFRESH_COOKIE)?.value;

  if (
    !shouldRefreshSellerSession({
      accessToken,
      hasRefreshToken: Boolean(refreshToken),
      user,
      shopStatus,
      attempted: guard.attempted,
    }) ||
    !refreshToken
  ) {
    return false;
  }

  // Probe cookie mutability BEFORE rotating the refresh token; rotating in a read-only Server
  // Component without persisting the new refresh cookie would trigger token-family revocation.
  try {
    store.set(ACCESS_COOKIE, accessToken ?? "", { ...baseCookie, maxAge: 900 });
  } catch {
    return false;
  }

  guard.attempted = true;
  const h = await headers();
  const trace = resolveTraceContext(h);
  const clientIp = resolveForwardedClientIp(h);

  try {
    const pair = await rotateRefreshToken(refreshToken, trace, { clientIp });
    if (!pair) {
      clearAuthCookies(store);
      return false;
    }
    setAuthCookies(store, pair);
    return true;
  } catch {
    // Transient 429/5xx: keep existing cookies untouched and let the request continue safely.
    return false;
  }
}

// ---- Route handlers: throw GatewayError (apiRoute turns it into 401/403 JSON) ----

export async function assertSession(): Promise<Session> {
  const s = await getSession();
  if (!s) throw new GatewayError("unauthenticated");
  return s;
}

export async function assertRole(...roles: readonly Role[]): Promise<Session> {
  const s = await assertSession();

  if (roles.includes(s.user.role)) {
    // Safety net: backend `/users/me` reflects DB immediately after admin approves a shop,
    // while the HttpOnly access JWT may still carry `role=buyer`. Rotate once in-place so
    // downstream gateway calls in this route handler carry the refreshed seller JWT.
    await refreshStaleSessionCookiesOnce(s.user);
    return s;
  }

  if (roles.includes("seller") && s.user.role === "buyer") {
    const myShop = await gw<{ shop?: { status?: string } | null }>("/api/v1/shops/me")
      .then((res) => res.shop ?? null)
      .catch(() => null);
    if (myShop?.status?.toUpperCase() === "ACTIVE") {
      const refreshed = await refreshStaleSessionCookiesOnce(s.user, myShop.status);
      if (refreshed) {
        const freshRes = await gw<{ user: GatewayUser | null }>("/api/v1/users/me").catch(
          () => null
        );
        if (freshRes?.user?.id) {
          const freshUser = toSessionUser(freshRes.user);
          if (roles.includes(freshUser.role)) {
            return { user: freshUser };
          }
        }
      }
    }
  }

  throw new GatewayError("forbidden");
}

export async function assertCapability(capability: AdminCapability): Promise<Session> {
  const s = await assertSession();
  if (!roleHasCapability(s.user.role, capability)) throw new GatewayError("forbidden");
  await refreshStaleSessionCookiesOnce(s.user);
  return s;
}
