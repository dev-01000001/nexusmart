import { NextRequest, NextResponse } from "next/server";
import {
  ACCESS_COOKIE,
  PROACTIVE_REFRESH_WINDOW_SECONDS,
  REFRESH_COOKIE,
  buildCookieHeaderWithUpdates,
  clearSessionCookies,
  extractClientIp,
  readJwtExp,
  refreshTokensWithGateway,
  sanitizeNextPath,
  setSessionCookies,
} from "@/lib/server/session";

const PROTECTED_PREFIXES = ["/tai-khoan", "/don-hang", "/thanh-toan", "/seller", "/admin"];

function isProtectedPath(pathname: string): boolean {
  return PROTECTED_PREFIXES.some(
    (prefix) => pathname === prefix || pathname.startsWith(`${prefix}/`)
  );
}

function isPrefetchRequest(req: NextRequest): boolean {
  if (
    req.headers.get("next-router-prefetch") !== null ||
    req.headers.get("next-router-segment-prefetch") !== null ||
    req.headers.get("x-middleware-prefetch") !== null
  ) {
    return true;
  }
  for (const name of ["purpose", "sec-purpose", "x-purpose"]) {
    const val = req.headers.get(name)?.toLowerCase();
    if (val && val.includes("prefetch")) {
      return true;
    }
  }
  return false;
}

function redirectToLogin(req: NextRequest): NextResponse {
  const fullPath = sanitizeNextPath(`${req.nextUrl.pathname}${req.nextUrl.search}`, "/");
  const loginUrl = new URL("/dang-nhap", req.url);
  loginUrl.searchParams.set("next", fullPath);
  return NextResponse.redirect(loginUrl, 307);
}

export async function middleware(req: NextRequest) {
  const { pathname } = req.nextUrl;
  const needsAuth = isProtectedPath(pathname);

  const accessToken = req.cookies.get(ACCESS_COOKIE)?.value;
  const refreshToken = req.cookies.get(REFRESH_COOKIE)?.value;

  if (!accessToken && !refreshToken) {
    return needsAuth ? redirectToLogin(req) : NextResponse.next();
  }

  const nowSec = Math.floor(Date.now() / 1000);
  const exp = readJwtExp(accessToken);
  const accessValid = Boolean(accessToken && exp !== null && exp > nowSec);
  const accessNearExpiry =
    !accessToken || exp === null || exp - nowSec <= PROACTIVE_REFRESH_WINDOW_SECONDS;

  if (!accessNearExpiry) {
    return NextResponse.next();
  }

  if (!refreshToken) {
    if (!accessValid) {
      const res = needsAuth ? redirectToLogin(req) : NextResponse.next();
      clearSessionCookies(res);
      return res;
    }
    return NextResponse.next();
  }

  // Never rotate refresh tokens on speculative Next.js / browser prefetches.
  if (isPrefetchRequest(req)) {
    return NextResponse.next();
  }

  const refreshed = await refreshTokensWithGateway(refreshToken, {
    clientIp: extractClientIp(req.headers),
  });
  if (refreshed.ok && refreshed.tokens) {
    const requestHeaders = new Headers(req.headers);
    requestHeaders.set(
      "cookie",
      buildCookieHeaderWithUpdates(req.headers.get("cookie"), {
        [ACCESS_COOKIE]: refreshed.tokens.access_token,
        [REFRESH_COOKIE]: refreshed.tokens.refresh_token,
      })
    );
    const res = NextResponse.next({ request: { headers: requestHeaders } });
    setSessionCookies(res, refreshed.tokens);
    return res;
  }

  if (refreshed.status === 401 || refreshed.status === 403) {
    const res = needsAuth ? redirectToLogin(req) : NextResponse.next();
    clearSessionCookies(res);
    return res;
  }

  // Transient gateway error: never misdirect a user with an active session to login.
  if (accessValid) {
    return NextResponse.next();
  }

  if (needsAuth) {
    const isRateLimited = refreshed.status === 429;
    const headers: Record<string, string> = {
      "Content-Type": "text/plain; charset=utf-8",
      "Cache-Control": "no-store",
    };
    if (isRateLimited && refreshed.retryAfter) {
      headers["Retry-After"] = refreshed.retryAfter;
    }
    return new NextResponse(
      isRateLimited
        ? "Bạn thao tác quá nhanh, vui lòng thử lại sau ít phút."
        : "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút.",
      {
        status: isRateLimited ? 429 : 503,
        headers,
      }
    );
  }

  return NextResponse.next();
}

export const config = {
  runtime: "nodejs",
  matcher: [
    "/((?!api(?:/|$)|_next/static|_next/image|favicon.ico|.*\\.(?:png|jpg|jpeg|gif|webp|svg|ico|css|js|map|woff|woff2|txt|xml)$).*)",
  ],
};
