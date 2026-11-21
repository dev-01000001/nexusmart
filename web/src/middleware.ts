import { NextResponse, type NextRequest } from "next/server";
import { ACCESS_COOKIE, REFRESH_COOKIE, getSiteRootDomain } from "@/lib/server/config";
import { buildLoginRedirectUrl, resolveRequestHost } from "@/lib/server/http";
import { lookupPublishedSiteHost, publishedSiteHostCache } from "@/lib/server/siteLookup";
import {
  accessExpiresSoon,
  applyAuthCookiesToHeader,
  clearAuthCookies,
  rotateRefreshToken,
  setAuthCookies,
  type TokenPair,
} from "@/lib/server/tokens";
import {
  classifyHost,
  isApiPath,
  isBlockedSitePath,
  isBuyerApiPath,
  isDirectInternalSitePath,
  isProtectedPagePath,
  isSiteApiPath,
  isSiteMetadataPath,
} from "@/lib/site/host";

/** Internal headers only the middleware may set; client-supplied copies are always dropped. */
const INTERNAL_SITE_HEADERS = ["x-site-host", "x-site-id", "x-site-shop-id"] as const;

function notFoundResponse(): NextResponse {
  return new NextResponse("Not Found", {
    status: 404,
    headers: {
      "Content-Type": "text/plain; charset=utf-8",
      "Cache-Control": "no-store",
    },
  });
}

function strippedHeaders(req: NextRequest): Headers {
  const h = new Headers(req.headers);
  for (const name of INTERNAL_SITE_HEADERS) h.delete(name);
  return h;
}

type Forward = (headers: Headers) => NextResponse;

/**
 * Session upkeep for page requests on the CURRENT host only (cookies are host-only, so every
 * site and the root rotate their own refresh token independently). Protected pages without a
 * live session redirect to `/dang-nhap?next=…` on the same host.
 */
async function withSession(
  req: NextRequest,
  headers: Headers,
  protectedRoute: boolean,
  forward: Forward
): Promise<NextResponse> {
  const access = req.cookies.get(ACCESS_COOKIE)?.value;
  const refresh = req.cookies.get(REFRESH_COOKIE)?.value;

  const redirectToLogin = (clearCookies: boolean) => {
    const res = NextResponse.redirect(buildLoginRedirectUrl(req), 307);
    if (clearCookies) clearAuthCookies(res.cookies);
    return res;
  };
  const forwardWithPair = (pair: TokenPair | null) => {
    const updated = applyAuthCookiesToHeader(req.headers.get("cookie"), pair);
    if (updated) headers.set("cookie", updated);
    else headers.delete("cookie");
    const res = forward(headers);
    if (pair) setAuthCookies(res.cookies, pair);
    else clearAuthCookies(res.cookies);
    return res;
  };

  if (!access && !refresh) {
    return protectedRoute ? redirectToLogin(false) : forward(headers);
  }
  // Access token present and not close to expiry. Server Components still verify the real
  // identity and role against backend /api/v1/users/me.
  if (access && !accessExpiresSoon(access)) return forward(headers);
  if (!refresh) {
    return protectedRoute ? redirectToLogin(true) : forwardWithPair(null);
  }
  try {
    const pair = await rotateRefreshToken(refresh);
    if (pair) return forwardWithPair(pair);
    return protectedRoute ? redirectToLogin(true) : forwardWithPair(null);
  } catch {
    // Backend unavailable or rate-limited: keep existing cookies and let the page handle outage.
    return forward(headers);
  }
}

export async function middleware(req: NextRequest) {
  const { pathname } = req.nextUrl;

  // Direct external access to internal `/s` or `/s/...` is ALWAYS 404 on every host.
  if (isDirectInternalSitePath(pathname)) return notFoundResponse();

  // Phase 1 proxy trust rule: `TRUST_PROXY_HEADERS=true` honors x-forwarded-host, otherwise
  // ONLY the HTTP Host header.
  const hostClass = classifyHost(
    resolveRequestHost(req.headers, req.nextUrl.host),
    getSiteRootDomain()
  );

  // Unknown, foreign, reserved, multi-level, or malformed host -> 404 without calling backend.
  if (hostClass.kind === "unknown") return notFoundResponse();

  const headers = strippedHeaders(req);

  if (hostClass.kind === "site") {
    // Seller/admin pages + APIs stay 404 on site hosts without calling backend.
    if (isBlockedSitePath(pathname)) return notFoundResponse();

    const lookup = await publishedSiteHostCache.resolve(
      hostClass.hostname,
      lookupPublishedSiteHost
    );
    // 404 BEFORE any rewrite (prevents host cache poisoning & hides draft/suspended sites).
    if (lookup.status !== "found") return notFoundResponse();

    headers.set("x-site-host", hostClass.hostname);
    headers.set("x-site-id", lookup.siteId);
    headers.set("x-site-shop-id", lookup.shopId);

    // Buyer + auth BFF (`/api/auth`, `/api/cart`, `/api/checkout`, `/api/payments`,
    // `/api/vouchers`, `/api/orders`): route handlers re-derive the shop from Host and
    // refresh tokens on 401 themselves. Robots/sitemap are host-aware route files.
    if (isSiteApiPath(pathname) || isSiteMetadataPath(pathname)) {
      return NextResponse.next({ request: { headers } });
    }

    const rewriteUrl = req.nextUrl.clone();
    rewriteUrl.pathname = `/s/${encodeURIComponent(hostClass.hostname)}${pathname === "/" ? "" : pathname}`;
    return withSession(req, headers, isProtectedPagePath(pathname, "site"), (h) =>
      NextResponse.rewrite(rewriteUrl, { request: { headers: h } })
    );
  }

  // Root (platform) host: buyer commerce APIs do not exist here.
  if (isBuyerApiPath(pathname)) return notFoundResponse();
  if (isApiPath(pathname) || isSiteMetadataPath(pathname)) {
    return NextResponse.next({ request: { headers } });
  }
  return withSession(req, headers, isProtectedPagePath(pathname, "root"), (h) =>
    NextResponse.next({ request: { headers: h } })
  );
}

export const config = {
  matcher: [
    /*
     * Match pages, `/api/**`, `/robots.txt`, and `/sitemap.xml` so multi-tenant host isolation
     * runs before any route handler. Excludes Next internals and static public media/assets.
     */
    "/((?!_next/static|_next/image|favicon\\.ico|products/|models/|banners/|videos/|atmosphere/|.*\\.(?:png|jpg|jpeg|gif|webp|svg|ico|mp4|webm|css|js|map|woff2?|ttf|json)$).*)",
  ],
};
