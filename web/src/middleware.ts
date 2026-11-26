import { NextResponse, type NextRequest } from "next/server";
import {
  ACCESS_COOKIE,
  REFRESH_COOKIE,
  buildSiteCspHeader,
  getAppOrigins,
  getSiteRootDomain,
} from "@/lib/server/config";
import { GatewayError } from "@/lib/server/errors";
import {
  buildLoginRedirectUrl,
  isPrefetchRequest,
  resolveForwardedClientIp,
  resolveRequestHost,
  resolveRequestProto,
} from "@/lib/server/http";
import { lookupPublishedSiteHost, publishedSiteHostCache } from "@/lib/server/siteLookup";
import {
  accessExpiresSoon,
  applyAuthCookiesToHeader,
  clearAuthCookies,
  rotateRefreshToken,
  setAuthCookies,
  type TokenPair,
} from "@/lib/server/tokens";
import { resolveTraceContext, type TraceContext } from "@/lib/server/trace";
import {
  classifyHost,
  isApiPath,
  isBlockedSitePath,
  isBuyerApiPath,
  isDirectInternalSitePath,
  isProtectedPagePath,
  isSiteApiPath,
  isSiteMetadataPath,
  isTenantSiteHost,
  normalizeHostHeader,
  resolveCanonicalHostRedirect,
} from "@/lib/site/host";

/** Internal headers only the middleware may set; client-supplied copies are always dropped. */
const INTERNAL_SITE_HEADERS = ["x-site-host", "x-site-id", "x-site-shop-id"] as const;

function withPageCsp(res: NextResponse): NextResponse {
  res.headers.set("Content-Security-Policy", buildSiteCspHeader());
  return res;
}

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

export { isPrefetchRequest };

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
  trace: TraceContext,
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

  // Never rotate refresh tokens on speculative Next.js / browser prefetches.
  if (isPrefetchRequest(req)) {
    return forward(headers);
  }

  const clientIp = resolveForwardedClientIp(req.headers);
  try {
    const pair = await rotateRefreshToken(refresh, trace, { clientIp });
    if (pair) return forwardWithPair(pair);
    return protectedRoute ? redirectToLogin(true) : forwardWithPair(null);
  } catch (err) {
    // Transient gateway error or rate limit: never clear cookies or misdirect an active session.
    if (access && !accessExpiresSoon(access, 0)) {
      return forward(headers);
    }
    if (protectedRoute) {
      const isRateLimited = err instanceof GatewayError && err.kind === "rate_limited";
      const resHeaders: Record<string, string> = {
        "Content-Type": "text/plain; charset=utf-8",
        "Cache-Control": "no-store",
      };
      if (isRateLimited && err.retryAfter) {
        resHeaders["Retry-After"] = err.retryAfter;
      }
      return new NextResponse(
        isRateLimited
          ? "Bạn thao tác quá nhanh, vui lòng thử lại sau ít phút."
          : "Hệ thống đang tạm bận, vui lòng thử lại sau ít phút.",
        {
          status: isRateLimited ? 429 : 503,
          headers: resHeaders,
        }
      );
    }
    return forward(headers);
  }
}

function resolveWwwRootRedirect(
  req: NextRequest,
  rawHost: string,
  rootDomain: string
): string | null {
  const { hostname } = normalizeHostHeader(rawHost);
  const wwwHost = `www.${rootDomain}`;
  if (hostname !== wwwHost) return null;

  let targetOrigin = "";
  const configuredOrigin = getAppOrigins()[0];
  if (configuredOrigin) {
    try {
      const parsed = new URL(configuredOrigin);
      if (
        (parsed.protocol === "http:" || parsed.protocol === "https:") &&
        parsed.hostname.toLowerCase() !== wwwHost
      ) {
        targetOrigin = parsed.origin;
      }
    } catch {
      // Ignore malformed APP_ORIGIN and fall back to request protocol + SITE_ROOT_DOMAIN.
    }
  }
  if (!targetOrigin) {
    const proto =
      resolveRequestProto(req.headers, req.url) ||
      req.nextUrl.protocol.replace(/:$/, "").toLowerCase() ||
      "http";
    targetOrigin = `${proto}://${rootDomain}`;
  }

  const { pathname, search } = req.nextUrl;
  const safePath = pathname.startsWith("/") ? pathname : `/${pathname}`;
  return `${targetOrigin}${safePath}${search}`;
}

export async function middleware(req: NextRequest) {
  const { pathname, search } = req.nextUrl;

  // Host is always authoritative (`x-forwarded-host` is never trusted, even when TRUST_PROXY_HEADERS=true).
  const rootDomain = getSiteRootDomain();
  const rawHost = resolveRequestHost(req.headers, req.nextUrl.host);

  // Permanently redirect `www.<SITE_ROOT_DOMAIN>` to the apex origin (including `/api/*`; 308 preserves method).
  const wwwRedirect = resolveWwwRootRedirect(req, rawHost, rootDomain);
  if (wwwRedirect) {
    return NextResponse.redirect(wwwRedirect, 308);
  }

  // Direct external access to internal `/s` or `/s/...` is ALWAYS 404 on every host.
  if (isDirectInternalSitePath(pathname)) return notFoundResponse();

  const hostClass = classifyHost(rawHost, rootDomain);

  // Unknown, reserved, multi-level, forbidden, or malformed host -> 404 without calling backend.
  if (hostClass.kind === "unknown") return notFoundResponse();

  const headers = strippedHeaders(req);
  const trace = resolveTraceContext(req.headers);
  headers.set("traceparent", trace.traceparent);
  if (trace.tracestate) headers.set("tracestate", trace.tracestate);

  if (isTenantSiteHost(hostClass)) {
    // Seller/admin pages + APIs stay 404 on site & custom hosts without calling backend.
    if (isBlockedSitePath(pathname)) return notFoundResponse();

    const clientIp = resolveForwardedClientIp(req.headers);
    const lookup = await publishedSiteHostCache.resolve(hostClass.hostname, (h) =>
      lookupPublishedSiteHost(h, trace, clientIp)
    );
    if (lookup.status === "error") {
      return new NextResponse("Hệ thống đang tạm bận, vui lòng thử lại sau ít phút.", {
        status: 503,
        headers: {
          "Content-Type": "text/plain; charset=utf-8",
          "Retry-After": "5",
          "Cache-Control": "no-store",
        },
      });
    }
    // 404 BEFORE any rewrite (prevents host cache poisoning & hides draft/suspended/unknown sites).
    if (lookup.status !== "found") return notFoundResponse();

    headers.set("x-site-host", hostClass.hostname);
    headers.set("x-site-id", lookup.siteId);
    headers.set("x-site-shop-id", lookup.shopId);

    // Buyer + auth BFF APIs: route handlers re-derive the shop from Host and refresh tokens on 401.
    // Robots/sitemap are host-aware route files. Never 308 APIs or robots/sitemap.
    if (isSiteApiPath(pathname) || isSiteMetadataPath(pathname)) {
      return NextResponse.next({ request: { headers } });
    }

    // Redirect GET page requests from a site's default subdomain to its active custom primary_host.
    const redirectTarget = resolveCanonicalHostRedirect({
      hostClass,
      primaryHost: lookup.primaryHost,
      rootDomain,
      method: req.method,
      pathname,
      search,
      proto: resolveRequestProto(req.headers, req.url),
    });
    if (redirectTarget) {
      return NextResponse.redirect(redirectTarget, {
        status: 308,
        headers: { "Cache-Control": "no-store" },
      });
    }

    const rewriteUrl = req.nextUrl.clone();
    rewriteUrl.pathname = `/s/${encodeURIComponent(hostClass.hostname)}${pathname === "/" ? "" : pathname}`;
    return withPageCsp(
      await withSession(req, headers, isProtectedPagePath(pathname, "site"), trace, (h) =>
        NextResponse.rewrite(rewriteUrl, { request: { headers: h } })
      )
    );
  }

  // Root (platform) host: buyer commerce APIs do not exist here.
  if (isBuyerApiPath(pathname)) return notFoundResponse();
  if (isApiPath(pathname) || isSiteMetadataPath(pathname)) {
    return NextResponse.next({ request: { headers } });
  }
  return withPageCsp(
    await withSession(req, headers, isProtectedPagePath(pathname, "root"), trace, (h) =>
      NextResponse.next({ request: { headers: h } })
    )
  );
}

export const config = {
  runtime: "nodejs",
  matcher: [
    /*
     * Match every app page, `/api/**`, `/s/**`, `/seller/**`, `/admin/**`, `/san-pham/**`,
     * `/robots.txt`, and `/sitemap.xml` (including paths ending in static suffixes such as
     * `.json`/`.js`) so multi-tenant host isolation runs before any route handler.
     * Excludes ONLY Next internals (`_next/static/*` without traversal, `_next/image`), app
     * icons (`favicon.ico`, `icon.svg`), and real static files under `web/public`.
     */
    "/((?!_next/static/(?!.*(?:\\.\\.|%2[eE]|%2[fF]|%5[cC]|\\\\))|_next/image$|favicon\\.ico$|icon\\.svg$|media-manifest\\.json$|globe/land-dots\\.json$|(?:products|models|banners|atmosphere)/[a-z0-9][a-z0-9_-]*\\.(?:png|jpg|jpeg|gif|webp|webm)$).*)",
  ],
};
