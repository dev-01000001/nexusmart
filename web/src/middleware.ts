import { NextResponse, type NextRequest } from "next/server";
import {
  ACCESS_COOKIE,
  GATEWAY_URL,
  REFRESH_COOKIE,
  getSiteHostCacheTtls,
  getSiteRootDomain,
} from "@/lib/server/config";
import { buildLoginRedirectUrl, resolveRequestHost } from "@/lib/server/http";
import {
  accessExpiresSoon,
  applyAuthCookiesToHeader,
  clearAuthCookies,
  rotateRefreshToken,
  setAuthCookies,
} from "@/lib/server/tokens";
import {
  classifyHost,
  isBlockedSitePath,
  isDirectInternalSitePath,
  isSiteMetadataPath,
} from "@/lib/site/host";
import { createSiteHostCache, type SiteHostLookupResult } from "@/lib/site/cache";

const PROTECTED_PREFIXES = ["/tai-khoan", "/don-hang", "/seller", "/admin"];
const SITE_LOOKUP_TIMEOUT_MS = 4000;

const ttls = getSiteHostCacheTtls();
const siteHostCache = createSiteHostCache({
  positiveTtlMs: ttls.positiveTtlMs,
  negativeTtlMs: ttls.negativeTtlMs,
  maxEntries: 2000,
});

function isProtectedPath(pathname: string): boolean {
  return PROTECTED_PREFIXES.some(
    (prefix) => pathname === prefix || pathname.startsWith(`${prefix}/`)
  );
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

async function lookupPublishedSiteHost(hostname: string): Promise<SiteHostLookupResult> {
  try {
    const res = await fetch(
      `${GATEWAY_URL}/api/v1/sites/by-host?host=${encodeURIComponent(hostname)}`,
      {
        method: "GET",
        headers: { Accept: "application/json" },
        cache: "no-store",
        signal: AbortSignal.timeout(SITE_LOOKUP_TIMEOUT_MS),
      }
    );

    if (res.status === 404) {
      return { status: "not_found" };
    }
    if (!res.ok) {
      return { status: "error" };
    }

    const data = (await res.json()) as {
      site?: { id?: string; shop_id?: string; status?: string; primary_host?: string };
      shop?: { status?: string };
    };
    const site = data?.site;
    const shopStatus = data?.shop?.status ? String(data.shop.status).toUpperCase() : "ACTIVE";
    if (
      !site?.id ||
      !site?.shop_id ||
      String(site.status ?? "").toUpperCase() !== "PUBLISHED" ||
      shopStatus !== "ACTIVE"
    ) {
      return { status: "not_found" };
    }

    return {
      status: "found",
      siteId: site.id,
      shopId: site.shop_id,
      primaryHost: (site.primary_host || hostname).toLowerCase(),
    };
  } catch {
    return { status: "error" };
  }
}

export async function middleware(req: NextRequest) {
  const { pathname } = req.nextUrl;

  // Direct external access to internal `/s` or `/s/...` is ALWAYS 404 on every host.
  if (isDirectInternalSitePath(pathname)) {
    return notFoundResponse();
  }

  // Strictly obey Phase 1 proxy trust rule (`TRUST_PROXY_HEADERS === "true"` -> honor x-forwarded-host,
  // else ONLY the HTTP Host header).
  const rawHost = resolveRequestHost(req.headers, req.nextUrl.host);
  const hostClass = classifyHost(rawHost, getSiteRootDomain());

  // Unknown, foreign, reserved, multi-level, or malformed host -> 404 immediately WITHOUT calling backend.
  if (hostClass.kind === "unknown") {
    return notFoundResponse();
  }

  // Subdomain site host branch: anonymous multi-tenant site serving.
  if (hostClass.kind === "site") {
    // Block `/api`, `/seller`, `/admin`, `/s` on site hosts without calling backend.
    if (isBlockedSitePath(pathname)) {
      return notFoundResponse();
    }

    const lookup = await siteHostCache.resolve(hostClass.hostname, lookupPublishedSiteHost);
    if (lookup.status !== "found") {
      // Return 404 BEFORE any rewrite (prevents host cache poisoning & hides draft/suspended sites).
      return notFoundResponse();
    }

    const reqHeaders = new Headers(req.headers);
    reqHeaders.set("x-site-host", hostClass.hostname);
    reqHeaders.set("x-site-id", lookup.siteId);
    reqHeaders.set("x-site-shop-id", lookup.shopId);

    // Host-aware robots.txt & sitemap.xml are served directly by app/robots.ts & app/sitemap.ts.
    if (isSiteMetadataPath(pathname)) {
      return NextResponse.next({ request: { headers: reqHeaders } });
    }

    const rewriteUrl = req.nextUrl.clone();
    const suffix = pathname === "/" ? "" : pathname;
    rewriteUrl.pathname = `/s/${encodeURIComponent(hostClass.hostname)}${suffix}`;
    return NextResponse.rewrite(rewriteUrl, { request: { headers: reqHeaders } });
  }

  // Root marketplace host branch: identical to Phase 1 behavior.
  if (pathname === "/api" || pathname.startsWith("/api/") || isSiteMetadataPath(pathname)) {
    return NextResponse.next();
  }

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
     * Match pages, `/api/**`, `/robots.txt`, and `/sitemap.xml` so multi-tenant host isolation
     * blocks `/api`, `/seller`, `/admin`, `/s` on site hosts and rejects unknown hosts before
     * any route handler runs. Excludes Next internals and static public media/assets.
     */
    "/((?!_next/static|_next/image|favicon\\.ico|products/|models/|banners/|videos/|atmosphere/|.*\\.(?:png|jpg|jpeg|gif|webp|svg|ico|mp4|webm|css|js|map|woff2?|ttf|json)$).*)",
  ],
};
