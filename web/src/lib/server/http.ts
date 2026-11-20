// Route-handler plumbing: CSRF origin check + uniform error handling.
import type { NextRequest } from "next/server";
import { APP_ORIGINS } from "./config";
import { GatewayError, errorResponse } from "./errors";

const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"]);

/**
 * CSRF guard for write requests. Checks Fetch-Metadata (`Sec-Fetch-Site`), then validates the
 * `Origin` header (or `Referer` fallback ONLY when `Origin` is absent) against `APP_ORIGINS` or
 * the request host/protocol. Rejects foreign origins, `Origin: null`, cross-site/same-site
 * Fetch-Metadata, and requests carrying neither `Origin` nor `Referer` with 403 `BAD_ORIGIN`.
 * Server actions are already origin-checked by Next; route handlers must call this
 * (`apiRoute` does it automatically for non-GET/HEAD/OPTIONS methods).
 */
export function assertSameOrigin(req: Request): void {
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

  if (APP_ORIGINS.length > 0) {
    if (!APP_ORIGINS.includes(parsed.origin)) throw bad();
    return;
  }

  const host =
    req.headers.get("x-forwarded-host")?.split(",")[0]?.trim() || req.headers.get("host");
  if (!host || parsed.host !== host) throw bad();

  const forwardedProto = req.headers.get("x-forwarded-proto")?.split(",")[0]?.trim().toLowerCase();
  const expectedProto = forwardedProto || safeUrlProtocol(req.url);
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
      return errorResponse(err, opts.messages);
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
