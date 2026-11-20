// Session helpers. Identity and role always come from the backend (/users/me), never from the
// client or from decoding the JWT locally.
import { cache } from "react";
import { cookies } from "next/headers";
import { notFound, redirect } from "next/navigation";
import { ACCESS_COOKIE, REFRESH_COOKIE } from "./config";
import { GatewayError } from "./errors";
import { gw } from "./gateway";

export type Role = "buyer" | "seller" | "admin" | "super_admin";
export const ADMIN_ROLES: readonly Role[] = ["admin", "super_admin"];
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
}

const VALID_ROLES = new Set<Role>(["buyer", "seller", "admin", "super_admin"]);

export function toSessionUser(u: GatewayUser): SessionUser {
  const rawRole = u.role as Role | undefined;
  return {
    id: u.id,
    fullName: u.full_name ?? "",
    email: u.email ?? "",
    phone: u.phone ?? "",
    role: rawRole && VALID_ROLES.has(rawRole) ? rawRole : "buyer",
    status: u.status ?? "",
    shopId: u.shop_id ?? "",
    avatarUrl: u.avatar_url ?? "",
    phoneVerified: Boolean(u.phone_verified),
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
  return next && next.startsWith("/") && !next.startsWith("//")
    ? `/dang-nhap?next=${encodeURIComponent(next)}`
    : "/dang-nhap";
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

// ---- Route handlers: throw GatewayError (apiRoute turns it into 401/403 JSON) ----

export async function assertSession(): Promise<Session> {
  const s = await getSession();
  if (!s) throw new GatewayError("unauthenticated");
  return s;
}

export async function assertRole(...roles: readonly Role[]): Promise<Session> {
  const s = await assertSession();
  if (!roles.includes(s.user.role)) throw new GatewayError("forbidden");
  return s;
}
