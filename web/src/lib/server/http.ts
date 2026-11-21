// Route-handler plumbing: CSRF origin check, proxy-header trust gating, and uniform error handling.
import type { NextRequest } from "next/server";
import { isTrustedProxyHeadersEnabled } from "./config.ts";
import { GatewayError, errorResponse } from "./errors.ts";
import { extractTraceId } from "./trace.ts";

const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"]);

export interface ProxyTrustOptions {
  /** Override `TRUST_PROXY_HEADERS` env check (mainly for unit tests). */
  trustProxyHeaders?: boolean;
}

function firstForwardedValue(raw: string | null): string {
  if (!raw) return "";
  return raw.split(",")[0]?.trim() ?? "";
}

/**
 * Resolve the effective request host. Ignores `x-forwarded-host` unless `TRUST_PROXY_HEADERS=true`,
 * falling back to the HTTP `Host` header (then `fallbackHost`).
 */
export function resolveRequestHost(
  headers: Pick<Headers, "get">,
  fallbackHost = "",
  opts: ProxyTrustOptions = {}
): string {
  const trustProxy = opts.trustProxyHeaders ?? isTrustedProxyHeadersEnabled();
  if (trustProxy) {
    const forwarded = firstForwardedValue(headers.get("x-forwarded-host"));
    if (forwarded) return forwarded;
  }
  return headers.get("host")?.trim() || fallbackHost;
}

/**
 * Resolve the effective request protocol (`http` or `https`). Ignores `x-forwarded-proto` unless
 * `TRUST_PROXY_HEADERS=true`, falling back to the incoming request URL protocol.
 */
export function resolveRequestProto(
  headers: Pick<Headers, "get">,
  rawUrl: string,
  opts: ProxyTrustOptions = {}
): string {
  const trustProxy = opts.trustProxyHeaders ?? isTrustedProxyHeadersEnabled();
  if (trustProxy) {
    const forwarded = firstForwardedValue(headers.get("x-forwarded-proto")).toLowerCase();
    if (forwarded === "http" || forwarded === "https") return forwarded;
  }
  return safeUrlProtocol(rawUrl);
}

/**
 * Resolve the forwarded visitor IP to pass to the gateway. Returns `null` when
 * `TRUST_PROXY_HEADERS` is disabled so untrusted clients cannot smuggle spoofed
 * `X-Forwarded-For` / `X-Real-IP` headers through the BFF.
 */
export function resolveForwardedClientIp(
  headers: Pick<Headers, "get">,
  opts: ProxyTrustOptions = {}
): string | null {
  const trustProxy = opts.trustProxyHeaders ?? isTrustedProxyHeadersEnabled();
  if (!trustProxy) return null;
  const xff = firstForwardedValue(headers.get("x-forwarded-for"));
  if (xff) return xff;
  const realIp = headers.get("x-real-ip")?.trim();
  return realIp || null;
}

/**
 * Build the login redirect URL used by middleware for unauthenticated protected routes,
 * honoring `x-forwarded-host`/`x-forwarded-proto` ONLY when `TRUST_PROXY_HEADERS=true`.
 */
export function buildLoginRedirectUrl(
  req: {
    url: string;
    headers: Pick<Headers, "get">;
    nextUrl?: { pathname: string; search: string; protocol: string; host: string };
  },
  opts: ProxyTrustOptions = {}
): URL {
  const urlObj = req.nextUrl ?? new URL(req.url);
  const nextTarget = `${urlObj.pathname}${urlObj.search}`;
  const proto =
    resolveRequestProto(req.headers, req.url, opts) ||
    urlObj.protocol.replace(/:$/, "").toLowerCase() ||
    "http";
  const host = resolveRequestHost(req.headers, urlObj.host, opts);

  let target: URL;
  try {
    target = new URL("/dang-nhap", `${proto}://${host}`);
  } catch {
    target = new URL("/dang-nhap", req.url);
  }
  if (nextTarget.startsWith("/") && !nextTarget.startsWith("//")) {
    target.searchParams.set("next", nextTarget);
  }
  return target;
}

/**
 * CSRF guard for write requests. Checks Fetch-Metadata (`Sec-Fetch-Site`), then requires the
 * `Origin` header (or `Referer` fallback ONLY when `Origin` is absent) to be EXACTLY the host
 * serving this request (ignoring `x-forwarded-host`/`x-forwarded-proto` unless
 * `TRUST_PROXY_HEADERS=true`). Every site and the root host are separate origins: a write from
 * another site or from the root host is rejected. Also rejects `Origin: null`,
 * cross-site/same-site Fetch-Metadata, and requests carrying neither `Origin` nor `Referer`
 * with 403 `BAD_ORIGIN`. Server actions are already origin-checked by Next; route handlers must
 * call this (`apiRoute` does it automatically for non-GET/HEAD/OPTIONS methods).
 */
export function assertSameOrigin(req: Request, opts: ProxyTrustOptions = {}): void {
  const bad = () => new GatewayError("forbidden", "BAD_ORIGIN");
  const fetchSite = req.headers.get("sec-fetch-site");
  if (fetchSite === "cross-site" || fetchSite === "same-site") throw bad();

  const rawOrigin = req.headers.get("origin");
  let candidate: string | null = null;
  if (rawOrigin !== null) {
    if (!rawOrigin || rawOrigin === "null") throw bad();
    candidate = rawOrigin;
  } else {
    const referer = req.headers.get("referer");
    if (!referer) throw bad();
    candidate = referer;
  }

  let parsed: URL;
  try {
    parsed = new URL(candidate);
  } catch {
    throw bad();
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") throw bad();

  const rawHost = resolveRequestHost(req.headers, "", opts).toLowerCase();
  const expectedProto = resolveRequestProto(req.headers, req.url, opts);
  if (!rawHost) throw bad();
  let expectedHost: string;
  try {
    expectedHost = new URL(`${expectedProto === "https" ? "https" : "http"}://${rawHost}`).host;
  } catch {
    throw bad();
  }
  if (!expectedHost || parsed.host !== expectedHost) throw bad();

  if (
    (expectedProto === "http" || expectedProto === "https") &&
    parsed.protocol !== `${expectedProto}:`
  ) {
    throw bad();
  }
}

function safeUrlProtocol(rawUrl: string): string {
  try {
    return new URL(rawUrl).protocol.replace(/:$/, "").toLowerCase();
  } catch {
    return "";
  }
}

type Handler<C> = (req: NextRequest, ctx: C) => Promise<Response>;

/**
 * Wrap a route handler: CSRF check for non-GET methods, and any thrown error (GatewayError or
 * unexpected) becomes `{ error: { code, message } }` with a safe status. `messages` overrides the
 * Vietnamese text per code/kind.
 *
 *   export const POST = apiRoute(async (req) => { ... return Response.json(data); });
 */
export function apiRoute<C = unknown>(
  fn: Handler<C>,
  opts: { messages?: Record<string, string> } = {}
): Handler<C> {
  return async (req, ctx) => {
    try {
      if (!SAFE_METHODS.has(req.method)) assertSameOrigin(req);
      return await fn(req, ctx);
    } catch (err) {
      // Let Next handle its own control-flow errors (redirect()/notFound()).
      if (isNextControlFlow(err)) throw err;
      return errorResponse(err, opts.messages, extractTraceId(req.headers.get("traceparent")));
    }
  };
}

/** Parse a JSON body or throw GatewayError("invalid"). */
export async function readJson<T>(req: Request): Promise<T> {
  try {
    return (await req.json()) as T;
  } catch {
    throw new GatewayError("invalid", "INVALID_JSON");
  }
}

function isNextControlFlow(err: unknown): boolean {
  const digest = (err as { digest?: unknown } | null)?.digest;
  return (
    typeof digest === "string" && /^(NEXT_REDIRECT|NEXT_NOT_FOUND|NEXT_HTTP_ERROR)/.test(digest)
  );
}
