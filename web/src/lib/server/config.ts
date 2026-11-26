// Server-only configuration for the BFF layer. Never import from client components.

export const GATEWAY_URL = (process.env.GATEWAY_URL || "http://localhost:28080").replace(
  /\/+$/,
  ""
);

const DEFAULT_PUBLIC_GATEWAY_PORT = "8080";

function isLoopbackHostname(hostname: string): boolean {
  const h = hostname
    .trim()
    .toLowerCase()
    .replace(/^\[|\]$/g, "");
  return h === "localhost" || h === "127.0.0.1" || h === "::1";
}

function isInternalDockerHostname(hostname: string): boolean {
  const h = hostname
    .trim()
    .toLowerCase()
    .replace(/^\[|\]$/g, "");
  if (!h || isLoopbackHostname(h)) return false;
  if (h.endsWith(".internal") || h.endsWith(".local")) return true;
  // Single-label service hostnames such as `gateway-bff` or `gateway` are compose-internal only.
  return !h.includes(".") && !h.includes(":");
}

function parsePublicWsBase(raw: string): URL | null {
  const cleaned = raw.trim();
  if (!cleaned) return null;
  try {
    const parsed = new URL(cleaned);
    if (
      parsed.protocol !== "ws:" &&
      parsed.protocol !== "wss:" &&
      parsed.protocol !== "http:" &&
      parsed.protocol !== "https:"
    ) {
      return null;
    }
    if (isInternalDockerHostname(parsed.hostname)) {
      return null;
    }
    if (parsed.protocol === "https:") parsed.protocol = "wss:";
    else if (parsed.protocol === "http:") parsed.protocol = "ws:";
    if (!parsed.pathname || parsed.pathname === "/") {
      parsed.pathname = "/api/v1/chat/ws";
    }
    parsed.search = "";
    parsed.hash = "";
    return parsed;
  } catch {
    return null;
  }
}

function isSecureRuntime(env: Record<string, string | undefined>): boolean {
  return (
    env.COOKIE_SECURE === "true" || (env.NODE_ENV === "production" && env.COOKIE_SECURE !== "false")
  );
}

/**
 * Resolves the browser-reachable WebSocket base URL for `/api/v1/chat/ws`.
 * Priority:
 *  1. `CHAT_WS_URL` / `NEXT_PUBLIC_CHAT_WS_URL` (explicit public WS URL, e.g. `wss://api.example.com/api/v1/chat/ws`)
 *  2. `PUBLIC_GATEWAY_URL` / `NEXT_PUBLIC_GATEWAY_URL` (public gateway origin, http(s) -> ws(s))
 *  3. Production HTTPS fallback: first `https://` origin from `APP_ORIGIN` -> `wss://<host>/api/v1/chat/ws`
 *  4. Local dev fallback: `GATEWAY_URL` ONLY when loopback, else `ws://localhost:${GATEWAY_PORT:-8080}/api/v1/chat/ws`.
 * Never leaks internal Docker service hostnames such as `gateway-bff`.
 */
export function resolveChatWsBaseUrl(env: Record<string, string | undefined> = process.env): URL {
  const explicitWs = (env.CHAT_WS_URL || env.NEXT_PUBLIC_CHAT_WS_URL || "").trim();
  const parsedWs = parsePublicWsBase(explicitWs);
  if (parsedWs) return parsedWs;

  const publicGw = (env.PUBLIC_GATEWAY_URL || env.NEXT_PUBLIC_GATEWAY_URL || "").trim();
  const parsedGw = parsePublicWsBase(publicGw);
  if (parsedGw) {
    parsedGw.pathname = "/api/v1/chat/ws";
    return parsedGw;
  }

  if (isSecureRuntime(env)) {
    const appOrigins = (env.APP_ORIGIN || "")
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    for (const origin of appOrigins) {
      const parsed = parsePublicWsBase(origin);
      if (parsed && parsed.protocol === "wss:") {
        parsed.pathname = "/api/v1/chat/ws";
        return parsed;
      }
    }
  }

  const rawPort = (env.GATEWAY_PORT || "").trim();
  const validPort =
    /^\d{1,5}$/.test(rawPort) && Number(rawPort) >= 1 && Number(rawPort) <= 65535 ? rawPort : "";

  const internalGw = (env.GATEWAY_URL || "").trim();
  if (!validPort && internalGw) {
    try {
      const parsed = new URL(internalGw);
      if (
        (parsed.protocol === "http:" || parsed.protocol === "https:") &&
        isLoopbackHostname(parsed.hostname)
      ) {
        parsed.protocol = parsed.protocol === "https:" ? "wss:" : "ws:";
        parsed.pathname = "/api/v1/chat/ws";
        parsed.search = "";
        parsed.hash = "";
        return parsed;
      }
    } catch {
      // Fall through to localhost + public port default
    }
  }

  const port = validPort || DEFAULT_PUBLIC_GATEWAY_PORT;
  return new URL(`ws://localhost:${port}/api/v1/chat/ws`);
}

/** Browser-reachable WebSocket origin (e.g. `ws://localhost:8080` or `wss://api.nexusmart.vn`) for CSP `connect-src`. */
export function resolveChatWsOrigin(env: Record<string, string | undefined> = process.env): string {
  return resolveChatWsBaseUrl(env).origin;
}

/**
 * Site-wide Content-Security-Policy for non-`/api` pages, evaluated with runtime env so
 * `connect-src` always matches the public WebSocket origin returned by chat `ws-ticket`.
 */
export function buildSiteCspHeader(env: Record<string, string | undefined> = process.env): string {
  const chatWsOrigin = resolveChatWsOrigin(env);
  const connectSrc = chatWsOrigin ? `'self' ${chatWsOrigin}` : "'self'";
  return `object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'; img-src 'self' https: data: blob:; connect-src ${connectSrc}`;
}

/**
 * Browser-reachable WebSocket URL for `/api/v1/chat/ws` with short-lived single-use ticket attached.
 */
export function buildChatWsUrl(
  ticket: string,
  env: Record<string, string | undefined> = process.env
): string {
  const base = resolveChatWsBaseUrl(env);
  if (ticket) {
    base.searchParams.set("ticket", ticket);
  }
  return base.toString();
}

/** Default timeout for one gateway call (ms). */
export const GATEWAY_TIMEOUT_MS = Number(process.env.GATEWAY_TIMEOUT_MS) || 8000;

/**
 * Secure cookies everywhere except local http dev. `COOKIE_SECURE=false` lets a production
 * build run over plain http (e.g. compose on localhost without TLS).
 */
export const COOKIE_SECURE =
  process.env.COOKIE_SECURE === "true" ||
  (process.env.NODE_ENV === "production" && process.env.COOKIE_SECURE !== "false");

// `__Host-` prefix forces Secure + Path=/ + no Domain (host-only) in the browser.
const prefix = COOKIE_SECURE ? "__Host-" : "";
export const ACCESS_COOKIE = `${prefix}nm_at`;
export const REFRESH_COOKIE = `${prefix}nm_rt`;

/** Refresh cookie lifetime; matches user service REFRESH_TOKEN_TTL (default 168h). */
export const REFRESH_MAX_AGE_S = Number(process.env.REFRESH_TOKEN_TTL_SECONDS) || 7 * 24 * 3600;

export const baseCookie = {
  httpOnly: true,
  secure: COOKIE_SECURE,
  sameSite: "lax" as const,
  path: "/",
};

/**
 * Optional comma-separated allow-list of origins for write requests (e.g.
 * "https://nexusmart.vn"). Empty = same origin as the request Host.
 */
export const APP_ORIGINS = (process.env.APP_ORIGIN || "")
  .split(",")
  .map((s) => s.trim().replace(/\/+$/, ""))
  .filter(Boolean);

/**
 * Trust reverse-proxy forwarding headers (`x-forwarded-proto`, `x-forwarded-for`, `x-real-ip`)
 * ONLY when Next.js sits exclusively behind a trusted proxy that strips/overwrites client-supplied
 * copies. `x-forwarded-host` is NEVER trusted (`Host` is always authoritative). Default `false` so
 * spoofed headers are ignored.
 */
export const TRUST_PROXY_HEADERS = process.env.TRUST_PROXY_HEADERS === "true";

/** Runtime check matching `TRUST_PROXY_HEADERS` (reflects env changes in tests). */
export function isTrustedProxyHeadersEnabled(): boolean {
  return process.env.TRUST_PROXY_HEADERS === "true";
}

function normalizeRootDomain(raw: string | undefined): string {
  const cleaned = (raw || "nexusmart.localhost")
    .trim()
    .toLowerCase()
    .replace(/^\.+|\.+$/g, "");
  return cleaned || "nexusmart.localhost";
}

/**
 * Root domain under which seller subdomain sites are served (e.g. `bach-dang.nexusmart.localhost`).
 */
export const SITE_ROOT_DOMAIN = normalizeRootDomain(process.env.SITE_ROOT_DOMAIN);

/** Runtime reader for `SITE_ROOT_DOMAIN` (reflects env changes in tests). */
export function getSiteRootDomain(): string {
  return normalizeRootDomain(process.env.SITE_ROOT_DOMAIN);
}

/** Short positive TTL (ms) for middleware host -> PUBLISHED site cache. */
export const SITE_HOST_CACHE_TTL_MS = Math.max(
  250,
  Number(process.env.SITE_HOST_CACHE_TTL_MS) || 3000
);

/** Short negative TTL (ms) for middleware host -> 404 negative cache. */
export const SITE_HOST_NEG_CACHE_TTL_MS = Math.max(
  250,
  Number(process.env.SITE_HOST_NEG_CACHE_TTL_MS) || 1500
);

export function getSiteHostCacheTtls(): { positiveTtlMs: number; negativeTtlMs: number } {
  return {
    positiveTtlMs: Math.max(250, Number(process.env.SITE_HOST_CACHE_TTL_MS) || 3000),
    negativeTtlMs: Math.max(250, Number(process.env.SITE_HOST_NEG_CACHE_TTL_MS) || 1500),
  };
}
