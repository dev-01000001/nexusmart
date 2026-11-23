import { cookies } from "next/headers";
import { notFound, redirect } from "next/navigation";
import type { GatewayUser, UserRole } from "@/lib/gateway-types";
import { GatewayError, gatewayFetch } from "@/lib/server/gateway";
import { ACCESS_COOKIE } from "@/lib/server/session";

const UNAVAILABLE_MESSAGE = "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút.";

/**
 * Fetches the authenticated user directly from backend `GET /api/v1/users/me`.
 * Role and shop_id are always taken from backend verification, never from raw JWT claims.
 * Returns `null` only on missing token or 401/403; propagates `GatewayError` on backend
 * outage / timeout so error boundaries handle it instead of redirecting to login.
 */
export async function getServerSession(): Promise<GatewayUser | null> {
  const store = await cookies();
  const accessToken = store.get(ACCESS_COOKIE)?.value;
  if (!accessToken) {
    return null;
  }

  try {
    const data = await gatewayFetch<{ user: GatewayUser }>("/users/me", {
      token: accessToken,
      cache: "no-store",
    });
    return data?.user ?? null;
  } catch (err) {
    if (err instanceof GatewayError) {
      if (err.status === 401 || err.status === 403) {
        return null;
      }
      throw err;
    }
    throw new GatewayError(503, "UNAVAILABLE", UNAVAILABLE_MESSAGE);
  }
}

export async function requireUser(currentPath?: string): Promise<GatewayUser> {
  const store = await cookies();
  const hadAccessCookie = Boolean(store.get(ACCESS_COOKIE)?.value);
  const user = await getServerSession();
  if (!user) {
    const params = new URLSearchParams();
    if (currentPath) {
      params.set("next", currentPath);
    }
    if (hadAccessCookie) {
      params.set("expired", "1");
    }
    const qs = params.toString();
    redirect(qs ? `/dang-nhap?${qs}` : "/dang-nhap");
  }
  return user;
}

/**
 * Verifies authentication and backend role on the server.
 * Unauthenticated callers are redirected to `/dang-nhap`; authenticated users
 * without an allowed role receive 404 (`notFound()`).
 */
export async function requireRole(
  allowedRoles: readonly UserRole[],
  currentPath?: string
): Promise<GatewayUser> {
  const user = await requireUser(currentPath);
  if (!allowedRoles.includes(user.role)) {
    notFound();
  }
  return user;
}
