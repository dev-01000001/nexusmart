import { NextResponse, type NextRequest } from "next/server";
import { ACCESS_COOKIE, REFRESH_COOKIE } from "@/lib/server/config";
import { buildLoginRedirectUrl } from "@/lib/server/http";
import {
  accessExpiresSoon,
  applyAuthCookiesToHeader,
  clearAuthCookies,
  rotateRefreshToken,
  setAuthCookies,
} from "@/lib/server/tokens";

const PROTECTED_PREFIXES = ["/tai-khoan", "/don-hang", "/seller", "/admin"];

function isProtectedPath(pathname: string): boolean {
  return PROTECTED_PREFIXES.some(
    (prefix) => pathname === prefix || pathname.startsWith(`${prefix}/`)
  );
}

function redirectToLogin(req: NextRequest, clearCookies: boolean): NextResponse {
  const target = buildLoginRedirectUrl(req);
  const res = NextResponse.redirect(target);
  if (clearCookies) clearAuthCookies(res.cookies);
  return res;
}

function nextWithCookies(
  req: NextRequest,
  pair: Awaited<ReturnType<typeof rotateRefreshToken>>
): NextResponse {
  const reqHeaders = new Headers(req.headers);
  const updated = applyAuthCookiesToHeader(req.headers.get("cookie"), pair);
  if (updated) reqHeaders.set("cookie", updated);
  else reqHeaders.delete("cookie");

  const res = NextResponse.next({ request: { headers: reqHeaders } });
  if (pair) setAuthCookies(res.cookies, pair);
  else clearAuthCookies(res.cookies);
  return res;
}

export async function middleware(req: NextRequest) {
  const { pathname } = req.nextUrl;
  const protectedRoute = isProtectedPath(pathname);
  const access = req.cookies.get(ACCESS_COOKIE)?.value;
  const refresh = req.cookies.get(REFRESH_COOKIE)?.value;

  // No session cookies at all.
  if (!access && !refresh) {
    return protectedRoute ? redirectToLogin(req, false) : NextResponse.next();
  }

  // Access token is present and not close to expiry: let the request through.
  // Server Components still verify real identity & role against backend /api/v1/users/me.
  if (access && !accessExpiresSoon(access)) {
    return NextResponse.next();
  }

  // Access token is missing, malformed, or expiring soon.
  if (!refresh) {
    return protectedRoute ? redirectToLogin(req, true) : nextWithCookies(req, null);
  }

  try {
    const pair = await rotateRefreshToken(refresh);
    if (pair) {
      return nextWithCookies(req, pair);
    }
    return protectedRoute ? redirectToLogin(req, true) : nextWithCookies(req, null);
  } catch {
    // Backend unavailable or rate-limited: keep existing cookies and let the page handle outage.
    return NextResponse.next();
  }
}

export const config = {
  matcher: [
    /*
     * Match page navigations only; exclude API route handlers (which enforce their own auth,
     * refresh, and CSRF), Next internals, and static public media/assets.
     */
    "/((?!api(?:/|$)|_next/static|_next/image|favicon\\.ico|robots\\.txt|sitemap\\.xml|products/|models/|banners/|videos/|atmosphere/|.*\\.(?:png|jpg|jpeg|gif|webp|svg|ico|mp4|webm|css|js|map|woff2?|ttf|json)$).*)",
  ],
};
