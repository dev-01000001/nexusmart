import { NextRequest, NextResponse } from "next/server";
import type { GatewayUser } from "@/lib/gateway-types";
import { GatewayError, gatewayFetch } from "@/lib/server/gateway";
import { ACCESS_COOKIE, REFRESH_COOKIE, extractClientIp } from "@/lib/server/session";

export const dynamic = "force-dynamic";

const NO_STORE = { "Cache-Control": "no-store" };

async function fetchCurrentUser(
  accessToken: string,
  clientIp: string | undefined
): Promise<{
  user: GatewayUser | null;
  status: number;
}> {
  try {
    const data = await gatewayFetch<{ user: GatewayUser }>("/users/me", {
      token: accessToken,
      clientIp,
      cache: "no-store",
    });
    return { user: data?.user ?? null, status: 200 };
  } catch (err) {
    if (err instanceof GatewayError) {
      return { user: null, status: err.status };
    }
    return { user: null, status: 503 };
  }
}

/**
 * Strictly read-only session probe: never rotates refresh tokens or mutates cookies on GET
 * (so cross-site top-level GET navigations carrying SameSite=Lax cookies cannot rotate tokens).
 * When access is missing/expired and a refresh cookie exists, returns `can_refresh: true`
 * so the client triggers CSRF-protected `POST /api/auth/refresh`.
 */
export async function GET(req: NextRequest) {
  const accessToken = req.cookies.get(ACCESS_COOKIE)?.value;
  const hasRefreshCookie = Boolean(req.cookies.get(REFRESH_COOKIE)?.value);
  const clientIp = extractClientIp(req.headers);

  if (accessToken) {
    const me = await fetchCurrentUser(accessToken, clientIp);
    if (me.user) {
      return NextResponse.json({ user: me.user }, { status: 200, headers: NO_STORE });
    }
    if (me.status !== 401) {
      return NextResponse.json(
        { user: null, can_refresh: false },
        { status: 200, headers: NO_STORE }
      );
    }
  }

  return NextResponse.json(
    { user: null, can_refresh: hasRefreshCookie },
    { status: 200, headers: NO_STORE }
  );
}
