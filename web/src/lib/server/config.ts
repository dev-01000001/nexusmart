// Server-only configuration for the BFF layer. Never import from client components.

export const GATEWAY_URL = (process.env.GATEWAY_URL || "http://localhost:28080").replace(
  /\/+$/,
  ""
);

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
